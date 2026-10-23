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
	// Imaged 是有图可挂的资产 ID（见 dramaImagedAssets）：没图的资产不成参考，人物写外貌声明，场景、道具写进镜头描述。
	Imaged   map[string]bool
	Shotlist *drama.Shotlist
	Segments []drama.Segment
	// project 是项目画风、每集时长（零值为默认档案、档案风格块、不查时长；离线评测文件没有项目时用零值）。
	project dramaProjectContext
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
		plan, err = planDramaShotlist(rows, script, in.Assets, in.Imaged, m, phrases, in.project)
		out.row.Rows = plan.Stats.Rows
		if plan.Stats.Rows > 0 {
			out.row.SpecRate = float64(plan.Stats.Rows-plan.Stats.NoSpec) / float64(plan.Stats.Rows)
		}
	} else if in.Shotlist != nil {
		out.row.Source = "shotlist"
		plan, err = planStoredDramaShotlist(*in.Shotlist, script, in.Segments, in.Assets, in.Imaged, m, phrases, in.project)
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
		res, err := drama.CompileWith(seg, m, projectStyleOverride(phrases, in.project), nil, style, phrases)
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

// planStoredDramaShotlist 给没有分镜行原文的老镜头表用：检查已存镜头表，每镜绑定的资产从已存段反推，再走与写镜头表相同的装段。
// 反推来源：段参考的文件（资产、主版本或人物卡版本）、段的人物声明（没图的人物），以及镜头描述里装段写下的「场景·X：」「道具·X：」（没图的场景、道具）。
// 人物只挂到这一镜在场（焦点、站位）的镜头上；有图的场景、道具挂到 refs_hint 写了它的镜头（都没写就挂整段）；没图的挂回描述所在的那一镜。
func planStoredDramaShotlist(shotlist drama.Shotlist, script *drama.Script, segments []drama.Segment, assets []ProjectAssetSummary, imaged map[string]bool, m drama.Model, phrases *drama.CompileProfile, pc dramaProjectContext) (dramaShotlistPlan, error) {
	plan := dramaShotlistPlan{Shotlist: shotlist}
	ctx, profile, err := dramaCheckContext(script, pc, assets)
	if err != nil {
		return plan, err
	}
	plan.Report = drama.CheckShots(shotlist, ctx)
	if plan.Report.HasErrors() {
		return plan, shotlistCheckFailed(plan.Report, plan.Stats)
	}
	byFile := map[string]ProjectAssetSummary{}
	byTitle := map[string]ProjectAssetSummary{}
	for _, a := range assets {
		if a.PrimaryVersionID == "" {
			continue
		}
		byFile[a.ID], byFile[a.PrimaryVersionID] = a, a
		if a.Character != nil && a.Character.VersionID != "" {
			byFile[a.Character.VersionID] = a
		}
		byTitle[strings.TrimSpace(a.Title)] = a
	}
	bound := map[string][]ProjectAssetSummary{}
	bind := func(shotID string, a ProjectAssetSummary) {
		if !slices.ContainsFunc(bound[shotID], func(b ProjectAssetSummary) bool { return b.ID == a.ID }) {
			bound[shotID] = append(bound[shotID], a)
		}
	}
	hinted := map[string]map[string]bool{}
	for _, sh := range shotlist.Shots {
		hinted[sh.ID] = map[string]bool{}
		for _, h := range sh.RefsHint {
			name, _, _ := strings.Cut(h, "·")
			hinted[sh.ID][strings.TrimSpace(name)] = true
		}
	}
	for _, seg := range segments {
		var found []ProjectAssetSummary
		for _, r := range seg.Refs {
			if a, ok := byFile[r.File]; ok && r.Role != "motion" {
				found = append(found, a)
			}
		}
		for _, c := range seg.Cast {
			if a, ok := byTitle[strings.TrimSpace(c.Name)]; ok {
				found = append(found, a)
			}
		}
		for _, a := range found {
			// 场景、道具挂到 refs_hint 写了它的镜头（都没写就挂整段），参考的先后顺序跟着镜头走。
			title := strings.TrimSpace(a.Title)
			targets := seg.ShotIDs
			if dramaRefRole(a.Category) != "identity" {
				if own := slices.DeleteFunc(slices.Clone(seg.ShotIDs), func(id string) bool { return !hinted[id][title] }); len(own) > 0 {
					targets = own
				}
			}
			for _, id := range targets {
				bind(id, a)
			}
		}
		// 没图的场景、道具：装段把描述写在段里第一个用到它的镜头上，就挂回那一镜。
		for _, sub := range seg.Shots {
			for _, a := range assets {
				kind := map[string]string{"location": "场景", "prop": "道具"}[dramaRefRole(a.Category)]
				if kind != "" && sub.ShotID != "" && strings.Contains(sub.Text, kind+"·"+strings.TrimSpace(a.Title)+"：") {
					bind(sub.ShotID, a)
				}
			}
		}
	}
	shots := slices.Clone(shotlist.Shots)
	rows := make([]shotlistRow, len(shots))
	for i := range shots {
		hints := slices.Clone([]string(shots[i].RefsHint))
		named := map[string]bool{}
		for _, h := range hints {
			name, _, _ := strings.Cut(h, "·")
			named[name] = true
		}
		present := map[string]bool{shots[i].Focus: true}
		for _, b := range shots[i].Blocking {
			present[b.Who] = true
		}
		for _, a := range bound[shots[i].ID] {
			title := strings.TrimSpace(a.Title)
			if dramaRefRole(a.Category) == "identity" && !present[title] {
				continue
			}
			rows[i].AssetBindings = append(rows[i].AssetBindings, drama.RowAssetBinding{NodeID: a.ID})
			if !named[title] {
				named[title] = true
				hints = append(hints, title)
			}
		}
		shots[i].RefsHint = hints
	}
	// 与写镜头表同一条规则把场景收成本场的一个：老段可能挂过两个场景，按现在的规则重装就只剩本场的。
	locations := map[string]bool{}
	for _, a := range assets {
		if a.PrimaryVersionID != "" && dramaRefRole(a.Category) == "location" {
			locations[strings.TrimSpace(a.Title)] = true
		}
	}
	keepSceneLocations(shots, locations, script)
	shotlist.Profile = pc.profileID() // 按现在的画风对照重装，段写的档案与检查用的一致
	plan.Segments, plan.Problems = packHintedShots(shotlist, shots, rows, script, assets, imaged, m, profile, ctx.Vocab, phrases)
	if err := checkDramaSegmentTotal(&plan.Report, plan.Segments, pc); err != nil {
		return plan, err
	}
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
	imaged, err := s.dramaImagedAssets(assets)
	if err != nil {
		return nil, err
	}
	inputs := make([]DramaEvalInput, 0, len(unitIDs))
	for _, unitID := range unitIDs {
		in, err := s.dramaEvalInput(project, unitID, assets, imaged)
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

func (s *Service) dramaEvalInput(project *model.Project, unitID string, assets []ProjectAssetSummary, imaged map[string]bool) (DramaEvalInput, error) {
	unit, err := s.repo.ProjectUnit(project.ID, unitID)
	if err != nil {
		return DramaEvalInput{}, err
	}
	record, err := s.repo.DramaShotlistForUnit(project.ID, unitID)
	if err != nil {
		return DramaEvalInput{}, err
	}
	in := DramaEvalInput{Label: unit.Title, ChapterText: chapterPlainText(unit.SourceText), Title: unit.Title, Assets: assets, Imaged: imaged, project: projectDramaContext(project)}
	in.project.Episode = s.dramaChapterEpisode(project.ID, unit)
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
