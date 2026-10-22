// 镜头表落库（yoozee 2.1）：分镜行 + spec → 剧本台词表 → 镜头表 → 检查 → 装段 → 写段版本。
// 镜头表是段的来源，段是派生物；用户改镜头表就整章重装段。领域规则在 internal/drama。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const (
	dramaShotlistMaxRows = 200
	// 一个分镜行的 spec（镜头表的一镜）上限，防止把整章塞进一行。
	dramaShotlistRowMaxBytes = 32 << 10
)

// DramaShotlistRule 是渠道实际能提交的段长（秒）：叠加在项目生成模型的上限上，只能收紧不能放宽。
type DramaShotlistRule struct {
	MaxSeconds int `json:"maxSeconds"`
	MinSeconds int `json:"minSeconds"`
}

type DramaShotlistWriteRequest struct {
	// Rows 是分镜行（前端 StoryboardRow），带或不带 spec。
	Rows            []json.RawMessage  `json:"rows"`
	Rule            *DramaShotlistRule `json:"rule,omitempty"`
	ExpectedShotIDs []string           `json:"expectedShotIds,omitempty"`
}

// DramaShotlistView 是一章的镜头表、台词表与最近一次检查结果。
type DramaShotlistView struct {
	UnitID    string            `json:"unitId"`
	Version   int               `json:"version"`
	Shotlist  drama.Shotlist    `json:"shotlist"`
	Script    *drama.Script     `json:"script,omitempty"`
	Check     drama.CheckReport `json:"check"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

type DramaShotlistWriteResult struct {
	Shotlist DramaShotlistView  `json:"shotlist"`
	Segments []DramaSegmentView `json:"segments"`
	Check    drama.CheckReport  `json:"check"`
	Stats    drama.RowsStats    `json:"stats"`
	// Notes 是装段说明：找不到合法切点的场、渠道时长等，给用户看。
	Notes []string `json:"notes"`
}

// shotlistRow 是分镜行里服务端会读的字段（spec 之外的平铺字段只用来补段版本的备注）。
type shotlistRow struct {
	drama.StoryboardRow
	Characters []struct {
		CharacterAssetID string `json:"characterAssetId"`
	} `json:"characters"`
	VideoMotionPrompt     string `json:"videoMotionPrompt"`
	ImageGenerationPrompt string `json:"imageGenerationPrompt"`
	NegativePrompt        string `json:"negativePrompt"`
}

func (r shotlistRow) assetIDs() []string {
	var ids []string
	for _, b := range r.AssetBindings {
		if id := strings.TrimSpace(b.NodeID); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	for _, c := range r.Characters {
		if id := strings.TrimSpace(c.CharacterAssetID); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids[:min(6, len(ids))]
}

var (
	htmlBlockBreak = regexp.MustCompile(`(?i)<\s*(br\s*/?|/p|/div|/h[1-6]|/li|/blockquote|/pre)\s*>`)
	htmlTag        = regexp.MustCompile(`<[^>]*>`)
)

// chapterPlainText 把章节正文（编辑器保存的 HTML）还原成逐行纯文本：段落、换行、标题各成一行。
func chapterPlainText(source string) string {
	text := htmlBlockBreak.ReplaceAllString(source, "\n")
	text = htmlTag.ReplaceAllString(text, "")
	return strings.TrimSpace(html.UnescapeString(text))
}

func shotlistCheckFailed(report drama.CheckReport, stats drama.RowsStats) error {
	var first string
	for _, item := range report.Items {
		if item.Level == "error" {
			first = item.Message
			break
		}
	}
	err := NewAppError(http.StatusBadRequest, fmt.Sprintf("镜头表检查有 %d 个 error，没有保存：%s", report.Errors, first))
	err.Reason = ReasonShotlistCheckFailed
	err.Details = map[string]any{"check": report, "stats": stats}
	return err
}

// dramaShotlistBible 用分镜行绑定的项目资产拼一份最小资产圣经：资产标题即人物 / 场景 / 道具名，文件是资产主版本 ID。
// 镜头 refs_hint 里写的「名称·变体」也挂到同一个版本上，装段按 refs_hint 取参考时就能落到柚子资产。
func dramaShotlistBible(shots []drama.Shot, rows []shotlistRow, assets []ProjectAssetSummary) *drama.Bible {
	byID := make(map[string]ProjectAssetSummary, len(assets))
	for _, a := range assets {
		byID[a.ID] = a
	}
	bible := &drama.Bible{}
	added := map[string]bool{}
	variants := map[string][]string{}
	for _, sh := range shots {
		for _, hint := range sh.RefsHint {
			if name, variant, ok := strings.Cut(hint, "·"); ok && variant != "" && !slices.Contains(variants[name], variant) {
				variants[name] = append(variants[name], variant)
			}
		}
	}
	for i := range rows {
		for _, id := range rows[i].assetIDs() {
			a, ok := byID[id]
			if !ok || a.PrimaryVersionID == "" || added[a.Title] || strings.TrimSpace(a.Title) == "" {
				continue
			}
			added[a.Title] = true
			switch dramaRefRole(a.Category) {
			case "identity":
				ch := drama.BibleCharacter{Name: a.Title, Variants: []drama.BibleVariant{{Kind: "base", Sheet: a.PrimaryVersionID}}}
				for _, v := range variants[a.Title] {
					ch.Variants = append(ch.Variants, drama.BibleVariant{Label: v, Kind: "state", Sheet: a.PrimaryVersionID})
				}
				bible.Characters = append(bible.Characters, ch)
			case "location":
				bible.Locations = append(bible.Locations, drama.BibleLocation{Name: a.Title, Plate: a.PrimaryVersionID})
			case "prop":
				bible.Props = append(bible.Props, drama.BibleProp{Name: a.Title, Image: a.PrimaryVersionID})
			}
		}
	}
	return bible
}

// attachRefsHints 让每镜的 refs_hint 带上这一行绑定的资产名：模型写的提示保留（「病历夹·掉在地上」进装段的状态句），
// 绑定了但没写进提示的资产补在后面，装段时一起挂成参考。
func attachRefsHints(shots []drama.Shot, rows []shotlistRow, assets []ProjectAssetSummary) {
	titles := make(map[string]string, len(assets))
	for _, a := range assets {
		if a.PrimaryVersionID != "" {
			titles[a.ID] = strings.TrimSpace(a.Title)
		}
	}
	for i := range shots {
		if i >= len(rows) {
			break
		}
		hints := slices.Clone([]string(shots[i].RefsHint))
		named := map[string]bool{}
		for _, h := range hints {
			name, _, _ := strings.Cut(h, "·")
			named[name] = true
		}
		for _, id := range rows[i].assetIDs() {
			if title := titles[id]; title != "" && !named[title] {
				named[title] = true
				hints = append(hints, title)
			}
		}
		shots[i].RefsHint = hints
	}
}

func dramaShotlistModel(project *model.Project, rule *DramaShotlistRule) (drama.Model, error) {
	m, err := projectDramaModel(project)
	if err != nil {
		return drama.Model{}, err
	}
	if rule == nil {
		return m, nil
	}
	if rule.MaxSeconds > 0 && rule.MaxSeconds < m.Duration.Max {
		m.Duration.Max = rule.MaxSeconds
	}
	if rule.MinSeconds > m.Duration.Min && rule.MinSeconds <= m.Duration.Max {
		m.Duration.Min = rule.MinSeconds
	}
	if m.Duration.Max < m.Duration.Min {
		return drama.Model{}, BadAuthRequest("段长规则无效：最长秒数小于最短秒数")
	}
	return m, nil
}

func dramaCheckContext(script *drama.Script) (drama.CheckContext, *drama.Profile, error) {
	profile, err := drama.BuiltinProfile(drama.DefaultProfileID)
	if err != nil {
		return drama.CheckContext{}, nil, err
	}
	vocab, err := drama.BuiltinVocab()
	if err != nil {
		return drama.CheckContext{}, nil, err
	}
	rules, err := drama.BuiltinShotRules()
	if err != nil {
		return drama.CheckContext{}, nil, err
	}
	return drama.CheckContext{Script: script, Profile: profile, Vocab: vocab, Rules: rules}, profile, nil
}

// WriteDramaShotlist 把一章的分镜行转成镜头表、检查、装段并整章替换段：检查有 error 时整批拒绝，不落库。
func (s *Service) WriteDramaShotlist(userID string, projectID string, unitID string, req DramaShotlistWriteRequest) (DramaShotlistWriteResult, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	unitID = strings.TrimSpace(unitID)
	unit, err := s.repo.ProjectUnit(projectID, unitID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DramaShotlistWriteResult{}, BadAuthRequest(fmt.Sprintf("章节 %q 不在这个项目里", unitID))
		}
		return DramaShotlistWriteResult{}, err
	}
	if len(req.Rows) == 0 || len(req.Rows) > dramaShotlistMaxRows {
		return DramaShotlistWriteResult{}, BadAuthRequest(fmt.Sprintf("分镜行数量必须在 1 到 %d 之间", dramaShotlistMaxRows))
	}
	rows := make([]shotlistRow, 0, len(req.Rows))
	storyRows := make([]drama.StoryboardRow, 0, len(req.Rows))
	for i, raw := range req.Rows {
		var row shotlistRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return DramaShotlistWriteResult{}, BadAuthRequest(fmt.Sprintf("第 %d 个分镜行格式无效：%s", i+1, err.Error()))
		}
		if len(row.Spec) > dramaShotlistRowMaxBytes {
			return DramaShotlistWriteResult{}, BadAuthRequest(fmt.Sprintf("第 %d 个分镜行的 spec 超过 %d KB", i+1, dramaShotlistRowMaxBytes>>10))
		}
		rows = append(rows, row)
		storyRows = append(storyRows, row.StoryboardRow)
	}
	m, err := dramaShotlistModel(project, req.Rule)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	// 1 章节正文 → 台词表
	var script *drama.Script
	if parsed := drama.ScriptFromText(chapterPlainText(unit.SourceText), unit.Title); len(parsed.Scenes) > 0 {
		script = &parsed
	}
	// 2 分镜行 → 镜头表
	converted := drama.RowsToShotlist(storyRows, script, drama.DefaultProfileID)
	shotlist := converted.Shotlist
	// 3 检查
	ctx, profile, err := dramaCheckContext(script)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	report := drama.CheckShots(shotlist, ctx)
	report.AddSchemaIssues(converted.SchemaIssues)
	if report.HasErrors() {
		return DramaShotlistWriteResult{}, shotlistCheckFailed(report, converted.Stats)
	}
	// 4 装段：参考取分镜行绑定的项目资产
	assets, err := s.ProjectAssets(userID, projectID)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	packShots := slices.Clone(shotlist.Shots)
	attachRefsHints(packShots, rows, assets)
	bible := dramaShotlistBible(packShots, rows, assets)
	segments, problems := drama.Pack(drama.PackInput{
		Shotlist: drama.Shotlist{Profile: shotlist.Profile, Episode: shotlist.Episode, Shots: packShots},
		Script:   script, Bible: bible, Model: m, Profile: profile, ProfileID: shotlist.Profile, Vocab: ctx.Vocab,
		FileExists: func(string) bool { return true },
	})
	rowByShot := make(map[string]shotlistRow, len(rows))
	for i, sh := range shotlist.Shots {
		rowByShot[sh.ID] = rows[i]
	}
	inputs := make([]ReplaceProjectUnitShotInput, 0, len(segments))
	for _, seg := range segments {
		input, err := dramaSegmentShotInput(seg, rowByShot)
		if err != nil {
			return DramaShotlistWriteResult{}, err
		}
		inputs = append(inputs, input)
	}
	// 5 镜头表与段同一事务写入
	shotlistJSON, _ := json.Marshal(shotlist)
	checkJSON, _ := json.Marshal(report)
	record := &model.DramaShotlist{ShotlistJSON: string(shotlistJSON), CheckJSON: string(checkJSON)}
	if script != nil {
		scriptJSON, _ := json.Marshal(script)
		record.ScriptJSON = string(scriptJSON)
	}
	if _, err := s.replaceProjectUnitShots(userID, projectID, unitID, ReplaceProjectUnitShotsRequest{Shots: inputs, ExpectedShotIDs: req.ExpectedShotIDs}, record); err != nil {
		return DramaShotlistWriteResult{}, err
	}
	views, err := s.DramaSegments(userID, projectID, unitID)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	notes := append([]string{}, problems...)
	if script == nil {
		notes = append(notes, "章节正文为空，台词覆盖（R-SHOT-001）没有检查")
	}
	view := DramaShotlistView{UnitID: unitID, Version: record.Version, Shotlist: shotlist, Script: script, Check: report, UpdatedAt: record.UpdatedAt}
	return DramaShotlistWriteResult{Shotlist: view, Segments: views, Check: report, Stats: converted.Stats, Notes: notes}, nil
}

// dramaSegmentShotInput 把装出的一段写成章节镜头：段 JSON 进版本 spec，平铺字段由它补齐，分镜行的提示词备注接在后面。
func dramaSegmentShotInput(seg drama.Segment, rowByShot map[string]shotlistRow) (ReplaceProjectUnitShotInput, error) {
	spec, err := json.Marshal(seg)
	if err != nil {
		return ReplaceProjectUnitShotInput{}, err
	}
	var texts, lines, videoPrompts []string
	for _, sh := range seg.Shots {
		texts = append(texts, sh.Text)
		for _, ln := range sh.Lines {
			lines = append(lines, ln.Speaker+"："+ln.Text)
		}
	}
	var imagePrompt, negative string
	var versions []string
	for _, id := range seg.ShotIDs {
		row := rowByShot[id]
		if p := strings.TrimSpace(row.VideoMotionPrompt); p != "" {
			videoPrompts = append(videoPrompts, p)
		}
		imagePrompt = firstNonEmpty(imagePrompt, strings.TrimSpace(row.ImageGenerationPrompt))
		negative = firstNonEmpty(negative, strings.TrimSpace(row.NegativePrompt))
	}
	for _, ref := range seg.Refs {
		if ref.Role != "motion" && !slices.Contains(versions, ref.File) {
			versions = append(versions, ref.File)
		}
	}
	description := strings.Join(texts, "；")
	title := seg.ID
	if seg.Title != "" {
		title += " " + seg.Title
	}
	durationMs := int64(seg.Dur * 1000)
	return ReplaceProjectUnitShotInput{
		CreateProjectShotRequest: CreateProjectShotRequest{Title: title, Description: description, DurationMs: durationMs, Revision: ShotRevisionInput{
			PlotDescription: description, Dialogue: strings.Join(lines, "\n"), DurationMs: durationMs,
			VideoPrompt: strings.Join(videoPrompts, "\n"), ImagePrompt: imagePrompt, NegativePrompt: negative, Spec: spec,
		}},
		AssetVersionIDs: versions[:min(6, len(versions))],
	}, nil
}

// DramaShotlist 读一章的镜头表、台词表与最近一次检查结果；这一章还没有镜头表时返回 nil。
func (s *Service) DramaShotlist(userID string, projectID string, unitID string) (*DramaShotlistView, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	unitID = strings.TrimSpace(unitID)
	if _, err := s.repo.ProjectUnit(projectID, unitID); err != nil {
		return nil, err
	}
	record, err := s.repo.DramaShotlistForUnit(projectID, unitID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	view := &DramaShotlistView{UnitID: unitID, Version: record.Version, UpdatedAt: record.UpdatedAt}
	if err := json.Unmarshal([]byte(record.ShotlistJSON), &view.Shotlist); err != nil {
		return nil, fmt.Errorf("镜头表数据损坏: %w", err)
	}
	if strings.TrimSpace(record.ScriptJSON) != "" {
		var script drama.Script
		if err := json.Unmarshal([]byte(record.ScriptJSON), &script); err == nil {
			view.Script = &script
		}
	}
	if strings.TrimSpace(record.CheckJSON) != "" {
		_ = json.Unmarshal([]byte(record.CheckJSON), &view.Check)
	}
	return view, nil
}
