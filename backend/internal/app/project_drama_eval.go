// 评测重跑（M5）：选一个阶段的候选版本，与基线版本在同一评测集上各跑一遍，上游产物复用已存结果，
// 只重跑该阶段和下游，输出指标表。本期实现「视频提示词」阶段：已存分镜行（或镜头表）→ 检查 → 装段 → 编译，不调模型。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const (
	// DramaEvalStageCompile 是视频提示词阶段：重新装段 + 编译。
	DramaEvalStageCompile = "compile"
	dramaEvalMaxUnits     = 20
	dramaEvalRunsLimit    = 30
)

// DramaEvalInput 是评测集里的一章：优先用已存的分镜行原文；老镜头表没有原文时用镜头表加已存段的参考反推绑定。
type DramaEvalInput struct {
	Label       string
	Rows        []json.RawMessage
	ChapterText string
	Title       string
	Assets      []ProjectAssetSummary
	Shotlist    *drama.Shotlist
	Segments    []drama.Segment
}

// DramaEvalRow 是指标表的一行（一个版本 × 一章）。SpecRate 为 -1 表示没有分镜行原文、算不出来。
type DramaEvalRow struct {
	Unit         string  `json:"unit"`
	Version      int     `json:"version"`
	VersionName  string  `json:"versionName"`
	Source       string  `json:"source"`
	Errors       int     `json:"errors"`
	Warnings     int     `json:"warnings"`
	Rows         int     `json:"rows"`
	SpecRate     float64 `json:"specRate"`
	Shots        int     `json:"shots"`
	Segments     int     `json:"segments"`
	TotalSeconds float64 `json:"totalSeconds"`
	MinSeconds   float64 `json:"minSeconds"`
	MaxSeconds   float64 `json:"maxSeconds"`
	Chars        int     `json:"chars"`
	AvgChars     int     `json:"avgChars"`
	MaxChars     int     `json:"maxChars"`
	OverLimit    int     `json:"overLimit"`
	OverSoft     int     `json:"overSoft"`
	CompileWarns int     `json:"compileWarns"`
	PackProblems int     `json:"packProblems"`
	Failure      string  `json:"failure,omitempty"`
}

// DramaEvalSegment 是一段在候选与基线下的提示词，便于逐段人工对比。
type DramaEvalSegment struct {
	Unit           string `json:"unit"`
	Segment        string `json:"segment"`
	Seconds        int    `json:"seconds"`
	Candidate      string `json:"candidate"`
	Baseline       string `json:"baseline"`
	CandidateChars int    `json:"candidateChars"`
	BaselineChars  int    `json:"baselineChars"`
}

// DramaEvalRun 是一次评测的结果。
type DramaEvalRun struct {
	ID        string                 `json:"id,omitempty"`
	Stage     string                 `json:"stage"`
	ProjectID string                 `json:"projectId,omitempty"`
	Model     string                 `json:"model"`
	Candidate DramaCompileProfileRef `json:"candidate"`
	Baseline  DramaCompileProfileRef `json:"baseline"`
	Rows      []DramaEvalRow         `json:"rows"`
	Segments  []DramaEvalSegment     `json:"segments"`
	Markdown  string                 `json:"markdown"`
	CreatedAt time.Time              `json:"createdAt"`
}

type DramaEvalRequest struct {
	Stage            string   `json:"stage"`
	ProjectID        string   `json:"projectId"`
	UnitIDs          []string `json:"unitIds"`
	CandidateVersion int      `json:"candidateVersion"`
	BaselineVersion  *int     `json:"baselineVersion"`
	Model            string   `json:"model"`
}

type dramaEvalOutcome struct {
	row      DramaEvalRow
	segments []drama.Segment
	prompts  []drama.CompileResult
}

// EvalDramaCompile 在评测集上用基线和候选两份编译档案各跑一遍「装段 + 编译」，不碰数据库、不调模型。
func EvalDramaCompile(inputs []DramaEvalInput, m drama.Model, candidate, baseline *drama.CompileProfile) (DramaEvalRun, error) {
	run := DramaEvalRun{Stage: DramaEvalStageCompile, Model: m.ID, Candidate: compileProfileRef(candidate), Baseline: compileProfileRef(baseline), Rows: []DramaEvalRow{}, Segments: []DramaEvalSegment{}, CreatedAt: time.Now()}
	for _, in := range inputs {
		base, err := evalDramaUnit(in, m, baseline)
		if err != nil {
			return run, err
		}
		cand, err := evalDramaUnit(in, m, candidate)
		if err != nil {
			return run, err
		}
		run.Rows = append(run.Rows, base.row, cand.row)
		for i, res := range cand.prompts {
			item := DramaEvalSegment{Unit: in.Label, Segment: res.Segment, Seconds: res.Duration, Candidate: res.Prompt, CandidateChars: res.Chars}
			if i < len(base.prompts) && base.prompts[i].Segment == res.Segment {
				item.Baseline, item.BaselineChars = base.prompts[i].Prompt, base.prompts[i].Chars
			}
			run.Segments = append(run.Segments, item)
		}
	}
	run.Markdown = dramaEvalMarkdown(run)
	return run, nil
}

func evalDramaUnit(in DramaEvalInput, m drama.Model, phrases *drama.CompileProfile) (dramaEvalOutcome, error) {
	out := dramaEvalOutcome{row: DramaEvalRow{Unit: in.Label, Version: phrases.Version(), VersionName: phrases.Name(), SpecRate: -1}}
	var script *drama.Script
	if parsed := drama.ScriptFromText(in.ChapterText, in.Title); len(parsed.Scenes) > 0 {
		script = &parsed
	}
	var plan dramaShotlistPlan
	var err error
	if len(in.Rows) > 0 {
		out.row.Source = "rows"
		var rows []shotlistRow
		if rows, err = parseShotlistRows(in.Rows); err != nil {
			return out, err
		}
		plan, err = planDramaShotlist(rows, script, in.Assets, m, phrases)
		out.row.Rows = plan.Stats.Rows
		if plan.Stats.Rows > 0 {
			out.row.SpecRate = float64(plan.Stats.Rows-plan.Stats.NoSpec) / float64(plan.Stats.Rows)
		}
	} else if in.Shotlist != nil {
		out.row.Source = "shotlist"
		plan, err = planStoredDramaShotlist(*in.Shotlist, script, in.Segments, m, phrases)
		out.row.Rows = len(in.Shotlist.Shots)
	} else {
		return out, fmt.Errorf("%s 没有镜头表", in.Label)
	}
	out.row.Errors, out.row.Warnings, out.row.Shots = plan.Report.Errors, plan.Report.Warnings, len(plan.Shotlist.Shots)
	out.row.PackProblems = len(plan.Problems)
	if err != nil {
		var appErr *AppError
		if errors.As(err, &appErr) && appErr.Reason == ReasonShotlistCheckFailed {
			out.row.Failure = "镜头表检查有 error，没有装段"
			return out, nil
		}
		return out, err
	}
	out.segments = plan.Segments
	out.row.Segments = len(plan.Segments)
	out.row.MinSeconds = math.Inf(1)
	for _, seg := range plan.Segments {
		out.row.TotalSeconds += seg.Dur
		out.row.MinSeconds = math.Min(out.row.MinSeconds, seg.Dur)
		out.row.MaxSeconds = math.Max(out.row.MaxSeconds, seg.Dur)
		var style *drama.Profile
		if id := strings.TrimSpace(seg.Profile); id != "" {
			if style, err = drama.BuiltinProfile(id); err != nil {
				return out, err
			}
		}
		res, err := drama.CompileWith(seg, m, nil, nil, style, phrases)
		if err != nil {
			return out, err
		}
		out.prompts = append(out.prompts, res)
		out.row.Chars += res.Chars
		out.row.MaxChars = max(out.row.MaxChars, res.Chars)
		for _, issue := range res.Issues {
			switch {
			case issue.Level == "error" && strings.Contains(issue.Message, "提示词") && strings.Contains(issue.Message, "上限"):
				out.row.OverLimit++
			case issue.Level == "warn" && strings.Contains(issue.Message, "软预算"):
				out.row.OverSoft++
			case issue.Level == "warn":
				out.row.CompileWarns++
			}
		}
	}
	if len(plan.Segments) == 0 {
		out.row.MinSeconds = 0
	} else {
		out.row.AvgChars = out.row.Chars / len(plan.Segments)
	}
	return out, nil
}

// planStoredDramaShotlist 给没有分镜行原文的老镜头表用：检查已存镜头表，参考绑定从已存段反推（段里每个参考挂回它包含的镜头）。
func planStoredDramaShotlist(shotlist drama.Shotlist, script *drama.Script, segments []drama.Segment, m drama.Model, phrases *drama.CompileProfile) (dramaShotlistPlan, error) {
	plan := dramaShotlistPlan{Shotlist: shotlist}
	ctx, profile, err := dramaCheckContext(script)
	if err != nil {
		return plan, err
	}
	plan.Report = drama.CheckShots(shotlist, ctx)
	if plan.Report.HasErrors() {
		return plan, shotlistCheckFailed(plan.Report, plan.Stats)
	}
	bible := &drama.Bible{}
	seen := map[string]bool{}
	hints := map[string][]string{}
	for _, seg := range segments {
		for _, r := range seg.Refs {
			name := strings.SplitN(strings.SplitN(r.Note, "，", 2)[0], "（", 2)[0]
			if name == "" || r.Role == "motion" {
				continue
			}
			if !seen[name] {
				seen[name] = true
				switch r.Role {
				case "identity":
					bible.Characters = append(bible.Characters, drama.BibleCharacter{Name: name, Variants: []drama.BibleVariant{{Kind: "base", Sheet: r.File}}})
				case "prop":
					bible.Props = append(bible.Props, drama.BibleProp{Name: name, Image: r.File})
				default:
					bible.Locations = append(bible.Locations, drama.BibleLocation{Name: name, Plate: r.File})
				}
			}
			for _, id := range seg.ShotIDs {
				if !slices.Contains(hints[id], name) {
					hints[id] = append(hints[id], name)
				}
			}
		}
	}
	shots := slices.Clone(shotlist.Shots)
	for i := range shots {
		hs := slices.Clone([]string(shots[i].RefsHint))
		named := map[string]bool{}
		for _, h := range hs {
			named[strings.SplitN(h, "·", 2)[0]] = true
		}
		present := map[string]bool{shots[i].Focus: true}
		for _, b := range shots[i].Blocking {
			present[b.Who] = true
		}
		for _, name := range hints[shots[i].ID] {
			isCharacter := slices.ContainsFunc(bible.Characters, func(c drama.BibleCharacter) bool { return c.Name == name })
			if named[name] || (isCharacter && !present[name]) {
				continue
			}
			named[name] = true
			hs = append(hs, name)
		}
		shots[i].RefsHint = hs
	}
	plan.Segments, plan.Problems = drama.Pack(drama.PackInput{
		Shotlist: drama.Shotlist{Profile: shotlist.Profile, Episode: shotlist.Episode, Shots: shots},
		Script:   script, Bible: bible, Model: m, Profile: profile, ProfileID: shotlist.Profile, Vocab: ctx.Vocab,
		FileExists: func(string) bool { return true }, Phrases: phrases,
	})
	return plan, nil
}

func dramaEvalMarkdown(run DramaEvalRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "评测：视频提示词（装段 + 编译）｜模型 %s｜基线 %s｜候选 %s\n\n", run.Model, profileLabel(run.Baseline.Version, run.Baseline.Name), profileLabel(run.Candidate.Version, run.Candidate.Name))
	b.WriteString("| 章节 | 档案 | 来源 | 检查 error / warn | spec 写出率 | 镜头 | 段数 | 总时长 | 段时长 | 字数 合计 / 平均 / 最长 | 超上限 | 超软预算 | 编译 warn | 装段问题 |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range run.Rows {
		spec := "—"
		if r.SpecRate >= 0 {
			spec = fmt.Sprintf("%.0f%%（%d 行）", r.SpecRate*100, r.Rows)
		}
		source := "分镜行原文"
		if r.Source == "shotlist" {
			source = "镜头表 + 段参考反推"
		}
		segs := fmt.Sprintf("%d", r.Segments)
		if r.Failure != "" {
			segs = r.Failure
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d / %d | %s | %d | %s | %s 秒 | %s～%s 秒 | %d / %d / %d | %d | %d | %d | %d |\n",
			r.Unit, profileLabel(r.Version, r.VersionName), source, r.Errors, r.Warnings, spec, r.Shots, segs, trimFloat(r.TotalSeconds), trimFloat(r.MinSeconds), trimFloat(r.MaxSeconds),
			r.Chars, r.AvgChars, r.MaxChars, r.OverLimit, r.OverSoft, r.CompileWarns, r.PackProblems)
	}
	changed := 0
	for _, s := range run.Segments {
		if s.Candidate != s.Baseline {
			changed++
		}
	}
	fmt.Fprintf(&b, "\n候选与基线提示词不同的段：%d / %d\n", changed, len(run.Segments))
	return b.String()
}

// profileLabel 是档案在表里的写法：存库版本写「v3 名称」，离线文件（版本号 < 0）只写名称。
func profileLabel(version int, name string) string {
	if version < 0 {
		return name
	}
	return fmt.Sprintf("v%d %s", version, name)
}

func trimFloat(x float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", x), "0"), ".")
}

// AdminDramaEval 在一个项目的章节上跑评测（管理员可评测任何项目，只读）：章节默认取有镜头表的第一章。
func (s *Service) AdminDramaEval(actor *model.User, req DramaEvalRequest) (*DramaEvalRun, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	stage := firstNonEmpty(strings.TrimSpace(req.Stage), DramaEvalStageCompile)
	if stage != DramaEvalStageCompile {
		return nil, BadAuthRequest("目前只支持视频提示词阶段（compile）的评测")
	}
	project, err := s.repo.ProjectByID(strings.TrimSpace(req.ProjectID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("项目不存在")
	}
	if err != nil {
		return nil, err
	}
	m, err := drama.ResolveModel(firstNonEmpty(strings.TrimSpace(req.Model), project.GenerationModel))
	if err != nil {
		return nil, BadAuthRequest(err.Error())
	}
	candidate, err := s.compileProfileVersion(req.CandidateVersion)
	if err != nil {
		return nil, err
	}
	var baseline *drama.CompileProfile
	if req.BaselineVersion != nil {
		baseline, err = s.compileProfileVersion(*req.BaselineVersion)
	} else {
		baseline, err = s.activeCompileProfile()
	}
	if err != nil {
		return nil, err
	}
	unitIDs, err := s.dramaEvalUnits(project.ID, req.UnitIDs)
	if err != nil {
		return nil, err
	}
	assets, err := s.ProjectAssets(project.UserID, project.ID)
	if err != nil {
		return nil, err
	}
	inputs := make([]DramaEvalInput, 0, len(unitIDs))
	for _, unitID := range unitIDs {
		in, err := s.dramaEvalInput(project, unitID, assets)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, in)
	}
	run, err := EvalDramaCompile(inputs, m, candidate, baseline)
	if err != nil {
		return nil, err
	}
	run.ID, run.ProjectID = newID(), project.ID
	result, _ := json.Marshal(run)
	units, _ := json.Marshal(unitIDs)
	record := &model.DramaEvalRun{ID: run.ID, Stage: stage, ProjectID: project.ID, UnitIDsJSON: string(units), Model: m.ID, CandidateVersion: candidate.Version(), BaselineVersion: baseline.Version(), ResultJSON: string(result), CreatedBy: actor.ID, CreatedAt: run.CreatedAt}
	if err := s.repo.CreateDramaEvalRun(record); err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *Service) dramaEvalUnits(projectID string, requested []string) ([]string, error) {
	shotlists, err := s.repo.DramaShotlistsForProject(projectID)
	if err != nil {
		return nil, err
	}
	has := map[string]bool{}
	for _, sl := range shotlists {
		has[sl.UnitID] = true
	}
	if len(requested) > 0 {
		if len(requested) > dramaEvalMaxUnits {
			return nil, BadAuthRequest(fmt.Sprintf("一次最多评测 %d 章", dramaEvalMaxUnits))
		}
		out := make([]string, 0, len(requested))
		for _, id := range requested {
			id = strings.TrimSpace(id)
			if !has[id] {
				return nil, BadAuthRequest(fmt.Sprintf("章节 %q 没有镜头表，不能评测视频提示词阶段", id))
			}
			out = append(out, id)
		}
		return out, nil
	}
	units, err := s.repo.ProjectUnits(projectID)
	if err != nil {
		return nil, err
	}
	for _, u := range units {
		if has[u.ID] {
			return []string{u.ID}, nil
		}
	}
	return nil, BadAuthRequest("这个项目还没有镜头表（先在章节里生成分段）")
}

func (s *Service) dramaEvalInput(project *model.Project, unitID string, assets []ProjectAssetSummary) (DramaEvalInput, error) {
	unit, err := s.repo.ProjectUnit(project.ID, unitID)
	if err != nil {
		return DramaEvalInput{}, err
	}
	record, err := s.repo.DramaShotlistForUnit(project.ID, unitID)
	if err != nil {
		return DramaEvalInput{}, err
	}
	in := DramaEvalInput{Label: unit.Title, ChapterText: chapterPlainText(unit.SourceText), Title: unit.Title, Assets: assets}
	if strings.TrimSpace(record.RowsJSON) != "" {
		if err := json.Unmarshal([]byte(record.RowsJSON), &in.Rows); err != nil {
			return in, fmt.Errorf("镜头表分镜行原文损坏: %w", err)
		}
		return in, nil
	}
	var shotlist drama.Shotlist
	if err := json.Unmarshal([]byte(record.ShotlistJSON), &shotlist); err != nil {
		return in, fmt.Errorf("镜头表数据损坏: %w", err)
	}
	in.Shotlist = &shotlist
	views, err := s.DramaSegments(project.UserID, project.ID, unitID)
	if err != nil {
		return in, err
	}
	for _, v := range views {
		if !v.Derived {
			in.Segments = append(in.Segments, v.Spec)
		}
	}
	return in, nil
}

// AdminDramaEvalRuns 返回最近的评测记录。
func (s *Service) AdminDramaEvalRuns(actor *model.User) ([]DramaEvalRun, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	records, err := s.repo.DramaEvalRuns(dramaEvalRunsLimit)
	if err != nil {
		return nil, err
	}
	out := make([]DramaEvalRun, 0, len(records))
	for _, r := range records {
		var run DramaEvalRun
		if err := json.Unmarshal([]byte(r.ResultJSON), &run); err != nil {
			continue
		}
		run.Segments = nil
		out = append(out, run)
	}
	return out, nil
}
