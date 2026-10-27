// 短剧制作线（yoozee 1.0 skills）的项目服务：生成模型与段长上限、需求文档与连戏表、
// 方言编译、证据与选中、进度推导、成片门禁与交付包。领域规则在 internal/drama。

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
)

const (
	dramaDocumentMaxBytes = 512 << 10
	dramaNoteMaxRunes     = 2000
	dramaPickReasonRunes  = 200
)

type UpdateProjectDramaDocumentRequest struct {
	Document json.RawMessage `json:"document"`
}

type CreateShotEvidenceRequest struct {
	ArtifactID string         `json:"artifactId"`
	ShotID     string         `json:"shotId"`
	Kind       string         `json:"kind"`
	Passed     *bool          `json:"passed"`
	Metrics    map[string]any `json:"metrics"`
	Note       string         `json:"note"`
	ResourceID string         `json:"resourceId"`
	TaskID     string         `json:"taskId"`
}

type PickShotArtifactRequest struct {
	Reason string `json:"reason"`
}

type CompileProjectShotRequest struct {
	Model  string  `json:"model"`
	Style  *string `json:"style"`
	Acting *string `json:"acting"`
}

type DramaGateRequest struct {
	ResourceID string   `json:"resourceId"`
	ArtifactID string   `json:"artifactId"`
	TargetLUFS *float64 `json:"targetLufs"`
}

type DramaGateResult struct {
	Report   drama.GateReport   `json:"report"`
	Evidence model.ShotEvidence `json:"evidence"`
}

type DramaCompileResult struct {
	ShotID  string              `json:"shotId"`
	Spec    drama.Segment       `json:"spec"`
	Derived bool                `json:"derived"`
	Result  drama.CompileResult `json:"result"`
	// CompileProfile 是这次编译用的编译档案（0 为内置默认）；段里的镜头画面句是装段时按当时的档案写的。
	CompileProfile DramaCompileProfileRef `json:"compileProfile"`
}

type DramaSegmentView struct {
	ShotID     string        `json:"shotId"`
	UnitID     string        `json:"unitId"`
	Title      string        `json:"title"`
	Position   int           `json:"position"`
	DurationMs int64         `json:"durationMs"`
	RevisionID string        `json:"revisionId,omitempty"`
	Spec       drama.Segment `json:"spec"`
	Derived    bool          `json:"derived"`
}

type DramaSegmentsWriteRequest struct {
	UnitID   string            `json:"unitId"`
	Segments []json.RawMessage `json:"segments"`
	// Order 是本章全部段号的新顺序，拆段、合段时给：段按它排位置，不在其中的段连同生成结果删除。只改段内容时不给。
	Order []string `json:"order,omitempty"`
	// Inherit 是「段号 → 从哪几段继承资产引用」（人物、场景、道具、本场色卡）：拆段时新段继承原段，合段时留下的段并上被并掉的段。
	// 键是这次写入的段号，来源是写入前已有的段号；按（版本, 用途）去重，镜头产出登记（role=output）不继承。
	Inherit map[string][]string `json:"inherit,omitempty"`
}

// DramaModels 返回可选的生成模型目录。
func (s *Service) DramaModels() (drama.Registry, error) {
	return drama.Models()
}

func normalizeProjectGenerationModel(value string) (string, error) {
	m, err := drama.ResolveModel(value)
	if err != nil {
		return "", BadAuthRequest("不支持的生成模型：" + strings.TrimSpace(value))
	}
	return m.ID, nil
}

func normalizeProjectAuthMode(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return drama.AuthStep, nil
	}
	if !drama.ValidAuthMode(value) {
		return "", BadAuthRequest("授权档位只能是每步确认（step）或全权（full）")
	}
	return value, nil
}

// checkProjectVideoModelFamily 确保项目默认视频模型属于所选生成模型族（不认识的渠道模型名放行）。
func checkProjectVideoModelFamily(generationModel string, defaultVideoModel string) error {
	if strings.TrimSpace(defaultVideoModel) == "" {
		return nil
	}
	family, ok := drama.MatchChannelModel(defaultVideoModel)
	if ok && family.ID != generationModel {
		return BadAuthRequest(fmt.Sprintf("默认视频模型属于 %s，与项目的生成模型 %s 不一致", family.Label, generationModel))
	}
	return nil
}

func projectDramaModel(project *model.Project) (drama.Model, error) {
	return drama.ResolveModel(project.GenerationModel)
}

func segmentTooLongError(m drama.Model, label string, durationMs int64) error {
	err := NewAppError(http.StatusBadRequest, fmt.Sprintf("%s 时长 %.1f 秒，超过 %s 一段的上限 %d 秒，请拆成两段", label, float64(durationMs)/1000, m.Label, m.Duration.Max))
	err.Reason = ReasonSegmentTooLong
	return err
}

func ensureSegmentWithinModel(project *model.Project, label string, durationMs int64) error {
	m, err := projectDramaModel(project)
	if err != nil {
		return err
	}
	if durationMs > m.MaxDurationMs() {
		return segmentTooLongError(m, label, durationMs)
	}
	return nil
}

// applyRevisionSpec 解析版本里的中性分段，补齐旧字段（时长、画面描述、台词），返回规范化后的 JSON。
func applyRevisionSpec(input *ShotRevisionInput) (string, error) {
	if len(bytes.TrimSpace(input.Spec)) == 0 || string(bytes.TrimSpace(input.Spec)) == "null" {
		return "", nil
	}
	seg, err := drama.ParseSegment(input.Spec)
	if err != nil {
		return "", BadAuthRequest(err.Error())
	}
	if input.DurationMs == 0 {
		input.DurationMs = int64(seg.Dur * 1000)
	}
	if strings.TrimSpace(input.PlotDescription) == "" {
		var parts []string
		if seg.Title != "" {
			parts = append(parts, seg.Title)
		}
		for _, sh := range seg.Shots {
			parts = append(parts, sh.Text)
		}
		input.PlotDescription = strings.Join(parts, "；")
	}
	if strings.TrimSpace(input.Dialogue) == "" {
		var lines []string
		for _, sh := range seg.Shots {
			for _, ln := range sh.Lines {
				lines = append(lines, ln.Speaker+"："+ln.Text)
			}
		}
		input.Dialogue = strings.Join(lines, "\n")
	}
	return seg.Canonical(), nil
}

func revisionInputs(rev *model.ShotRevision) drama.RevisionInputs {
	if rev == nil {
		return drama.RevisionInputs{}
	}
	return drama.RevisionInputs{
		PlotDescription: rev.PlotDescription, Action: rev.Action, Dialogue: rev.Dialogue, ShotSize: rev.ShotSize,
		CameraAngle: rev.CameraAngle, CameraMovement: rev.CameraMovement, DurationMs: rev.DurationMs,
		ImagePrompt: rev.ImagePrompt, VideoPrompt: rev.VideoPrompt, NegativePrompt: rev.NegativePrompt,
		ContinuityNotes: rev.ContinuityNotes, ActionBeatsJSON: rev.ActionBeatsJSON, SpecJSON: rev.SpecJSON,
	}
}

func referenceIDs(references []model.ShotAssetReference) []string {
	ids := make([]string, 0, len(references))
	for _, ref := range references {
		ids = append(ids, ref.AssetVersionID+"|"+ref.Role)
	}
	return ids
}

// staleDeciderFor 返回「只有依赖输入变了的产物才过期」的判定函数。
func staleDeciderFor(revision *model.ShotRevision) repository.ShotArtifactStaleDecider {
	inputs := revisionInputs(revision)
	return func(artifacts []model.ShotArtifact, references []model.ShotAssetReference) []string {
		states := make([]drama.ArtifactInput, 0, len(artifacts))
		for _, a := range artifacts {
			states = append(states, drama.ArtifactInput{ID: a.ID, Type: a.Type, InputHash: a.InputHash})
		}
		return drama.StaleArtifacts(states, inputs, referenceIDs(references))
	}
}

func (s *Service) artifactInputHash(shotID string, revisionID string, artifactType string) string {
	revision, err := s.repo.ShotRevisionForShot(shotID, revisionID)
	if err != nil {
		return ""
	}
	references, err := s.repo.ShotAssetReferencesForShot(shotID)
	if err != nil {
		return ""
	}
	return drama.InputHash(artifactType, revisionInputs(revision), referenceIDs(references))
}

// ---------------------------------------------------------------- 需求文档与连戏表

func decodeDramaDocument(raw json.RawMessage) (map[string]any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, BadAuthRequest("文档不能为空")
	}
	if len(raw) > dramaDocumentMaxBytes {
		return nil, BadAuthRequest("文档超过 512 KB")
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return nil, BadAuthRequest("文档必须是 JSON 对象")
	}
	return doc, nil
}

// ProjectBrief 读取需求文档；生成模型和授权档位总以项目字段为准。
func (s *Service) ProjectBrief(userID string, projectID string) (map[string]any, error) {
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if strings.TrimSpace(project.BriefJSON) != "" {
		_ = json.Unmarshal([]byte(project.BriefJSON), &doc)
	}
	doc = drama.NormalizeBrief(doc)
	if _, ok := doc["title"]; !ok {
		doc["title"] = project.Name
	}
	m, err := projectDramaModel(project)
	if err != nil {
		return nil, err
	}
	doc["video_model"] = m.ID
	authMode, _ := normalizeProjectAuthMode(project.AuthMode)
	doc["auth_mode"] = authMode
	return doc, nil
}

// UpdateProjectBrief 写需求文档；文档里改了生成模型或授权档位时同步到项目字段。
func (s *Service) UpdateProjectBrief(userID string, projectID string, raw json.RawMessage) (map[string]any, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	doc, err := decodeDramaDocument(raw)
	if err != nil {
		return nil, err
	}
	// 按契约 brief 校验：形状不对的需求文档会让「需求问透了」永远不成立，宁可当场拒绝让写的一方改。
	if _, ok := doc["title"]; !ok {
		doc["title"] = project.Name
	}
	checked, _ := json.Marshal(doc)
	if _, err := drama.ParseBrief(checked); err != nil {
		return nil, BadAuthRequest(err.Error())
	}
	update := UpdateProjectRequest{}
	if value, ok := doc["video_model"].(string); ok && strings.TrimSpace(value) != "" && value != project.GenerationModel {
		update.GenerationModel = &value
	}
	if value, ok := doc["auth_mode"].(string); ok && strings.TrimSpace(value) != "" && value != project.AuthMode {
		update.AuthMode = &value
	}
	if update.GenerationModel != nil || update.AuthMode != nil {
		if _, err := s.UpdateProject(userID, projectID, update); err != nil {
			return nil, err
		}
	}
	delete(doc, "video_model")
	delete(doc, "auth_mode")
	encoded, _ := json.Marshal(doc)
	if err := s.repo.UpdateProjectDramaDocument(userID, projectID, "brief_json", string(encoded), time.Now()); err != nil {
		return nil, err
	}
	return s.ProjectBrief(userID, projectID)
}

func (s *Service) ProjectBible(userID string, projectID string) (map[string]any, error) {
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if strings.TrimSpace(project.BibleJSON) != "" {
		_ = json.Unmarshal([]byte(project.BibleJSON), &doc)
	}
	return doc, nil
}

func (s *Service) UpdateProjectBible(userID string, projectID string, raw json.RawMessage) (map[string]any, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	doc, err := decodeDramaDocument(raw)
	if err != nil {
		return nil, err
	}
	if err := validateDramaBibleDocument(doc); err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(doc)
	if err := s.repo.UpdateProjectDramaDocument(userID, projectID, "bible_json", string(encoded), time.Now()); err != nil {
		return nil, err
	}
	return doc, nil
}

// validateDramaBibleDocument 是连戏表的形状校验（页面保存、导演风格收尾和风格锁定共用）：列表字段必须是数组；style_anchor、
// master_palette（色板 HEX 合法）、style_lock 是对象；
// color_script 每场的 HEX 必须是 #RRGGBB、集号场序是正整数，同一场不能写两次。
func validateDramaBibleDocument(doc map[string]any) error {
	for _, key := range []string{"characters", "props", "locations", "screen_text", "timeline", "spatial_maps", "color_script"} {
		if value, ok := doc[key]; ok {
			if _, isList := value.([]any); !isList {
				return BadAuthRequest("连戏表的 " + key + " 必须是数组")
			}
		}
	}
	if value, ok := doc["style_anchor"]; ok && value != nil {
		if _, isObject := value.(map[string]any); !isObject {
			return BadAuthRequest("连戏表的 style_anchor 必须是对象")
		}
		var anchor drama.StyleAnchor
		encoded, _ := json.Marshal(value)
		if json.Unmarshal(encoded, &anchor) != nil {
			return BadAuthRequest("连戏表的 style_anchor 字段类型不对（forbidden 是字符串数组，其余是字符串）")
		}
	}
	if value, ok := doc["master_palette"]; ok && value != nil {
		if _, isObject := value.(map[string]any); !isObject {
			return BadAuthRequest("连戏表的 master_palette 必须是对象")
		}
		encoded, _ := json.Marshal(value)
		var palette drama.MasterPalette
		if json.Unmarshal(encoded, &palette) != nil {
			return BadAuthRequest("连戏表的 master_palette 要是 {groups: [{key, name, colors: [...]}]}")
		}
		if err := drama.ValidateMasterPalette(palette); err != nil {
			return BadAuthRequest("连戏表的" + err.Error())
		}
	}
	if value, ok := doc["style_lock"]; ok && value != nil {
		encoded, _ := json.Marshal(value)
		var record drama.StyleLockRecord
		if _, isObject := value.(map[string]any); !isObject || json.Unmarshal(encoded, &record) != nil {
			return BadAuthRequest("连戏表的 style_lock 必须是对象，字段都是字符串")
		}
	}
	if value, ok := doc["color_script"]; ok {
		encoded, _ := json.Marshal(value)
		var entries []drama.ColorScriptEntry
		if json.Unmarshal(encoded, &entries) != nil {
			return BadAuthRequest("连戏表的 color_script 每一项要有 episode、scene_no 和颜色数组")
		}
		seen := map[string]bool{}
		for _, e := range entries {
			if e.Episode <= 0 || e.SceneNo <= 0 {
				return BadAuthRequest("色彩脚本的 episode、scene_no 必须是正整数")
			}
			if seen[e.Code()] {
				return BadAuthRequest("色彩脚本里 " + e.Code() + " 写了两次")
			}
			seen[e.Code()] = true
			for _, c := range e.Colors() {
				if !drama.ValidHexColor(drama.NormalizeHexColor(c.Hex)) {
					return BadAuthRequest(fmt.Sprintf("色彩脚本 %s 的「%s」HEX「%s」不是 #RRGGBB", e.Code(), c.NameZh, c.Hex))
				}
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------- 分段

var dialogueLinePattern = regexp.MustCompile(`^\s*([^：:\n]{1,24})[：:]\s*(.+)$`)

func parseLegacyDialogue(text string) []drama.Line {
	var out []drama.Line
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if m := dialogueLinePattern.FindStringSubmatch(raw); m != nil {
			out = append(out, drama.Line{Speaker: strings.TrimSpace(m[1]), Text: strings.TrimSpace(m[2])})
			continue
		}
		out = append(out, drama.Line{Speaker: "旁白", Text: raw, VO: true})
	}
	return out
}

type dramaAssetInfo struct {
	Title    string
	Category model.AssetCategory
}

func (s *Service) dramaAssetIndex(userID string, projectID string) map[string]dramaAssetInfo {
	index := map[string]dramaAssetInfo{}
	assets, err := s.ProjectAssets(userID, projectID)
	if err != nil {
		return index
	}
	for _, asset := range assets {
		index[asset.ID] = dramaAssetInfo{Title: asset.Title, Category: asset.Category}
	}
	return index
}

func dramaRefRole(category model.AssetCategory) string {
	switch strings.ToLower(string(category)) {
	case "character", "role", "person":
		return "identity"
	case "scene", "location", "environment":
		return "location"
	case "prop", "item", "object":
		return "prop"
	default:
		return "frame"
	}
}

// segmentForShot 返回一个镜头的中性分段：有分段 JSON 用它；旧镜头按「只有一个镜头的段」合成。
func (s *Service) segmentForShot(userID string, projectID string, shot *model.Shot, unitTitle string, assets map[string]dramaAssetInfo) (drama.Segment, *model.ShotRevision, bool, error) {
	var revision *model.ShotRevision
	if shot.CurrentRevisionID != "" {
		rev, err := s.repo.ShotRevisionForShot(shot.ID, shot.CurrentRevisionID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return drama.Segment{}, nil, false, err
		}
		revision = rev
	}
	if revision != nil && strings.TrimSpace(revision.SpecJSON) != "" {
		if seg, err := drama.ParseSegment([]byte(revision.SpecJSON)); err == nil {
			return seg, revision, false, nil
		}
	}
	seg := drama.Segment{ID: fmt.Sprintf("S%02d", shot.Position+1), Scene: firstNonEmpty(unitTitle, "一"), Title: shot.Title, Dur: float64(shot.DurationMs) / 1000, Mode: drama.ModeTextOnly}
	if seg.Dur <= 0 {
		seg.Dur = 5
	}
	sub := drama.SubShot{At: 0, Text: firstNonEmpty(shot.Description, shot.Title)}
	if revision != nil {
		sub.Size = revision.ShotSize
		sub.Camera = strings.TrimSpace(strings.Join(nonEmpty(revision.CameraAngle, revision.CameraMovement), "，"))
		sub.Text = strings.TrimSpace(strings.Join(nonEmpty(revision.PlotDescription, revision.Action), "。"))
		sub.Lines = parseLegacyDialogue(revision.Dialogue)
		seg.Rules = revision.ContinuityNotes
	}
	if sub.Text == "" {
		sub.Text = shot.Title
	}
	seg.Shots = []drama.SubShot{sub}
	references, err := s.repo.ShotAssetReferencesForShot(shot.ID)
	if err != nil {
		return drama.Segment{}, nil, false, err
	}
	for _, ref := range references {
		version, err := s.repo.AssetVersionForProject(projectID, ref.AssetVersionID)
		if err != nil {
			continue
		}
		info := assets[version.AssetID]
		seg.Refs = append(seg.Refs, drama.Ref{File: ref.AssetVersionID, Role: dramaRefRole(info.Category), Note: info.Title})
	}
	if len(seg.Refs) > 0 {
		seg.Mode = drama.ModeLockIdentity
	}
	return seg, revision, true, nil
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

// DramaSegments 列出项目（或某章）的全部段。
func (s *Service) DramaSegments(userID string, projectID string, unitID string) ([]DramaSegmentView, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	shots, err := s.repo.ProjectShots(projectID)
	if err != nil {
		return nil, err
	}
	units, err := s.repo.ProjectUnitSummaries(projectID)
	if err != nil {
		return nil, err
	}
	unitTitles := map[string]string{}
	for _, unit := range units {
		unitTitles[unit.ID] = unit.Title
	}
	assets := s.dramaAssetIndex(userID, projectID)
	out := []DramaSegmentView{}
	for i := range shots {
		shot := &shots[i]
		if unitID != "" && shot.UnitID != unitID {
			continue
		}
		seg, revision, derived, err := s.segmentForShot(userID, projectID, shot, unitTitles[shot.UnitID], assets)
		if err != nil {
			return nil, err
		}
		view := DramaSegmentView{ShotID: shot.ID, UnitID: shot.UnitID, Title: shot.Title, Position: shot.Position, DurationMs: shot.DurationMs, Spec: seg, Derived: derived}
		if revision != nil {
			view.RevisionID = revision.ID
		}
		out = append(out, view)
	}
	return out, nil
}

// WriteDramaSegments 按段号新增或修改一章的段：已有段号的写新版本，新段号建新镜头。
// 不给 Order 时不删除段；给了 Order（拆段、合段）就按它重排，并删除不在其中的段。
func (s *Service) WriteDramaSegments(userID string, projectID string, req DramaSegmentsWriteRequest) ([]DramaSegmentView, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	unitID := strings.TrimSpace(req.UnitID)
	if _, err := s.repo.ProjectUnit(projectID, unitID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, BadAuthRequest(fmt.Sprintf("章节 %q 不在这个项目里：先用 drama_chapters_read 取章节 ID", unitID))
		}
		return nil, err
	}
	if len(req.Segments) == 0 || len(req.Segments) > 60 {
		return nil, BadAuthRequest("一次写入 1 到 60 段")
	}
	m, err := projectDramaModel(project)
	if err != nil {
		return nil, err
	}
	parsed := make([]drama.Segment, 0, len(req.Segments))
	written := make([]string, 0, len(req.Segments))
	for _, raw := range req.Segments {
		seg, err := drama.ParseSegment(raw)
		if err != nil {
			return nil, BadAuthRequest(err.Error())
		}
		if slices.Contains(written, seg.ID) {
			return nil, BadAuthRequest(fmt.Sprintf("一次写入里段号 %s 出现了两次", seg.ID))
		}
		written = append(written, seg.ID)
		if issues := drama.Issues(seg, m); drama.HasError(issues) {
			for _, issue := range issues {
				if issue.Level == "error" && strings.Contains(issue.Message, "超过") && strings.Contains(issue.Message, "时长") {
					return nil, segmentTooLongError(m, seg.ID, int64(seg.Dur*1000))
				}
			}
			return nil, BadAuthRequest(issues[0].Message)
		}
		parsed = append(parsed, seg)
	}
	existing, err := s.DramaSegments(userID, projectID, unitID)
	if err != nil {
		return nil, err
	}
	byID := map[string]DramaSegmentView{}
	maxPosition := -1
	current := make([]drama.ExistingSegment, 0, len(existing))
	for _, view := range existing {
		byID[view.Spec.ID] = view
		maxPosition = max(maxPosition, view.Position)
		current = append(current, drama.ExistingSegment{ID: view.Spec.ID, Derived: view.Derived})
	}
	removed, err := drama.PlanSegmentOrder(req.Order, written, current)
	if err != nil {
		return nil, BadAuthRequest(err.Error())
	}
	for target, sources := range req.Inherit {
		if !slices.Contains(written, target) {
			return nil, BadAuthRequest(fmt.Sprintf("inherit 里的 %s 不在这次写入里", drama.EchoSegmentID(target)))
		}
		for _, source := range sources {
			if _, ok := byID[source]; !ok || source == target {
				return nil, BadAuthRequest(fmt.Sprintf("%s 要继承的 %s 不是本章已有的另一段", target, drama.EchoSegmentID(source)))
			}
		}
	}
	shotOf := make(map[string]string, len(parsed))
	for _, seg := range parsed {
		spec, _ := json.Marshal(seg)
		title := firstNonEmpty(seg.Title, seg.ID)
		input := CreateProjectShotRequest{UnitID: unitID, Title: seg.ID + " " + title, Revision: ShotRevisionInput{Spec: spec}}
		if view, ok := byID[seg.ID]; ok {
			input.ID = view.ShotID
			input.Position = view.Position
			// 分段 JSON 管不到的备注（视频提示词备注、负面提示词）沿用上一版，改段内容时不能清空。
			if view.RevisionID != "" {
				if previous, err := s.repo.ShotRevisionForShot(view.ShotID, view.RevisionID); err == nil {
					input.Revision.VideoPrompt = previous.VideoPrompt
					input.Revision.NegativePrompt = previous.NegativePrompt
				}
			}
		} else {
			maxPosition++
			input.Position = maxPosition
		}
		if seg.Title == "" {
			input.Title = seg.ID
		}
		shot, err := s.CreateProjectShot(userID, projectID, input)
		if err != nil {
			return nil, err
		}
		shotOf[seg.ID] = shot.ID
	}
	// 继承要在删段之前：合段时被并掉的段一删，引用也跟着删了。
	if err := s.inheritSegmentReferences(projectID, unitID, req.Inherit, shotOf, byID); err != nil {
		return nil, err
	}
	if len(req.Order) == 0 {
		return s.DramaSegments(userID, projectID, unitID)
	}
	now := time.Now()
	for _, id := range removed {
		if err := s.repo.DeleteProjectShot(projectID, byID[id].ShotID, now); err != nil {
			return nil, err
		}
	}
	views, err := s.DramaSegments(userID, projectID, unitID)
	if err != nil {
		return nil, err
	}
	// 合成段号随位置变，删段后会漂移：已有段用写入前的镜头 ID，新段从写入后的分段里找。
	shotByID := make(map[string]string, len(views))
	for _, view := range views {
		if !view.Derived {
			shotByID[view.Spec.ID] = view.ShotID
		}
	}
	for id, view := range byID {
		shotByID[id] = view.ShotID
	}
	ordered := make([]string, 0, len(req.Order))
	for _, id := range req.Order {
		ordered = append(ordered, shotByID[strings.TrimSpace(id)])
	}
	if err := s.repo.ReorderUnitShots(projectID, unitID, ordered, now); err != nil {
		return nil, err
	}
	return s.DramaSegments(userID, projectID, unitID)
}

// inheritSegmentReferences 把来源段的资产引用补到目标段上（拆段、合段，见 DramaSegmentsWriteRequest.Inherit）。
// 目标段已有的（版本, 用途）不重复加；加了引用的段产物过期，之后来源资产换版本也会跟到它。
func (s *Service) inheritSegmentReferences(projectID string, unitID string, inherit map[string][]string, shotOf map[string]string, before map[string]DramaSegmentView) error {
	if len(inherit) == 0 {
		return nil
	}
	references, err := s.repo.ProjectUnitShotAssetReferences(projectID, unitID)
	if err != nil {
		return err
	}
	byShot := map[string][]model.ShotAssetReference{}
	for _, ref := range references {
		byShot[ref.ShotID] = append(byShot[ref.ShotID], ref)
	}
	targets := make([]string, 0, len(inherit))
	for target := range inherit {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	now := time.Now()
	for _, target := range targets {
		shotID := shotOf[target]
		seen := map[string]bool{}
		for _, ref := range byShot[shotID] {
			seen[ref.AssetVersionID+"|"+ref.Role] = true
		}
		for _, source := range inherit[target] {
			for _, ref := range byShot[before[source].ShotID] {
				key := ref.AssetVersionID + "|" + ref.Role
				if ref.Role == "output" || seen[key] {
					continue
				}
				seen[key] = true
				copied := model.ShotAssetReference{ID: newID(), ShotID: shotID, AssetVersionID: ref.AssetVersionID, Role: ref.Role, Status: ref.Status, CreatedAt: now}
				if err := s.repo.UpsertShotAssetReferenceAndInvalidate(projectID, &copied, now); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// CompileProjectShot 把一段编译成项目生成模型（或指定模型）的提示词。
func (s *Service) CompileProjectShot(userID string, projectID string, shotID string, req CompileProjectShotRequest) (DramaCompileResult, error) {
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return DramaCompileResult{}, err
	}
	shot, err := s.repo.ShotForProject(projectID, strings.TrimSpace(shotID))
	if err != nil {
		return DramaCompileResult{}, err
	}
	unitTitle := ""
	if shot.UnitID != "" {
		if unit, unitErr := s.repo.ProjectUnit(projectID, shot.UnitID); unitErr == nil {
			unitTitle = unit.Title
		}
	}
	seg, _, derived, err := s.segmentForShot(userID, projectID, shot, unitTitle, s.dramaAssetIndex(userID, projectID))
	if err != nil {
		return DramaCompileResult{}, err
	}
	modelID := firstNonEmpty(strings.TrimSpace(req.Model), project.GenerationModel)
	m, err := drama.ResolveModel(modelID)
	if err != nil {
		return DramaCompileResult{}, BadAuthRequest(err.Error())
	}
	// 2.1 段写了风格档案（由镜头表装出）：默认风格块和表演块取档案的，与本机 compile.py 一致。
	var profile *drama.Profile
	if strings.TrimSpace(seg.Profile) != "" {
		if profile, err = drama.BuiltinProfile(strings.TrimSpace(seg.Profile)); err != nil {
			return DramaCompileResult{}, BadAuthRequest(err.Error())
		}
	}
	// 没图的资产不能编译成「@图片N」：人物改写外貌声明，场景、道具只靠镜头文字（存量段、前端装段、Agent 写的段都兜住）。
	lookup, err := s.dramaRefLookup(userID, projectID, seg)
	if err != nil {
		return DramaCompileResult{}, err
	}
	phrases, err := s.activeCompileProfile()
	if err != nil {
		return DramaCompileResult{}, err
	}
	seg, refIssues := dropImagelessRefs(seg, lookup, phrases.Cast.LockMax)
	// 风格段：请求里给了就用请求的；否则项目选了画风用画风执行规范（档案 style_block 只在没选画风时兜底）。
	style := req.Style
	if style == nil {
		style = projectStyleOverride(phrases, projectDramaContext(project))
	}
	result, err := drama.CompileWith(seg, m, style, req.Acting, profile, phrases)
	if err != nil {
		return DramaCompileResult{}, err
	}
	result.Issues = append(result.Issues, refIssues...)
	return DramaCompileResult{ShotID: shot.ID, Spec: seg, Derived: derived, Result: result, CompileProfile: compileProfileRef(phrases)}, nil
}

// ---------------------------------------------------------------- 证据、选中、进度

func evidenceStates(items []model.ShotEvidence) []drama.EvidenceState {
	out := make([]drama.EvidenceState, 0, len(items))
	for _, e := range items {
		out = append(out, drama.EvidenceState{ShotID: e.ShotID, ArtifactID: e.ArtifactID, Kind: e.Kind, Passed: e.Passed, CreatedAt: e.CreatedAt})
	}
	return out
}

func (s *Service) ProjectEvidence(userID string, projectID string) ([]model.ShotEvidence, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	return s.repo.ProjectShotEvidence(projectID)
}

func (s *Service) shotHasDialogue(projectID string, shot *model.Shot) bool {
	if shot.CurrentRevisionID == "" {
		return false
	}
	revision, err := s.repo.ShotRevisionForShot(shot.ID, shot.CurrentRevisionID)
	if err != nil {
		return false
	}
	if seg, err := drama.ParseSegment([]byte(revision.SpecJSON)); err == nil {
		return seg.HasDialogue()
	}
	return strings.TrimSpace(revision.Dialogue) != ""
}

func (s *Service) expectedDialogue(shot *model.Shot) string {
	if shot.CurrentRevisionID == "" {
		return ""
	}
	revision, err := s.repo.ShotRevisionForShot(shot.ID, shot.CurrentRevisionID)
	if err != nil {
		return ""
	}
	if seg, err := drama.ParseSegment([]byte(revision.SpecJSON)); err == nil {
		return seg.Dialogue()
	}
	var b strings.Builder
	for _, line := range parseLegacyDialogue(revision.Dialogue) {
		b.WriteString(line.Text)
	}
	return b.String()
}

// CreateShotEvidence 记一条证据。听写证据必须给转写任务，由服务端按该段台词算字错率；门禁和交付证据只能由对应接口生成。
func (s *Service) CreateShotEvidence(userID string, projectID string, req CreateShotEvidenceRequest) (model.ShotEvidence, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return model.ShotEvidence{}, err
	}
	kind := strings.TrimSpace(req.Kind)
	if !drama.ValidEvidenceKind(kind) {
		return model.ShotEvidence{}, BadAuthRequest("不支持的证据种类：" + kind)
	}
	if kind == drama.EvidenceGate || kind == drama.EvidenceDelivery || kind == drama.EvidencePick {
		return model.ShotEvidence{}, BadAuthRequest("门禁、交付和选中证据由对应操作自动记录")
	}
	if utf8.RuneCountInString(req.Note) > dramaNoteMaxRunes {
		return model.ShotEvidence{}, BadAuthRequest("说明过长")
	}
	evidence := model.ShotEvidence{ID: newID(), ProjectID: projectID, Kind: kind, Passed: req.Passed, Note: strings.TrimSpace(req.Note), CreatedBy: userID, CreatedAt: time.Now()}
	var shot *model.Shot
	if artifactID := strings.TrimSpace(req.ArtifactID); artifactID != "" {
		artifact, err := s.repo.ShotArtifactForProject(projectID, artifactID)
		if err != nil {
			return model.ShotEvidence{}, err
		}
		evidence.ArtifactID, evidence.ShotID, evidence.UnitID = artifact.ID, artifact.ShotID, artifact.UnitID
	} else if shotID := strings.TrimSpace(req.ShotID); shotID != "" {
		evidence.ShotID = shotID
	}
	if evidence.ShotID != "" {
		found, err := s.repo.ShotForProject(projectID, evidence.ShotID)
		if err != nil {
			return model.ShotEvidence{}, err
		}
		shot = found
		evidence.UnitID = found.UnitID
	}
	if kind != drama.EvidenceAcceptance && kind != drama.EvidenceReview && evidence.ArtifactID == "" {
		return model.ShotEvidence{}, BadAuthRequest("这种证据必须挂在一条候选上")
	}
	if resourceID := strings.TrimSpace(req.ResourceID); resourceID != "" {
		if _, err := s.repo.ResourceForUser(userID, resourceID); err != nil {
			return model.ShotEvidence{}, err
		}
		evidence.ResourceID = resourceID
	}
	metrics := req.Metrics
	if metrics == nil {
		metrics = map[string]any{}
	}
	if kind == drama.EvidenceTranscript {
		taskID := strings.TrimSpace(req.TaskID)
		if taskID == "" {
			return model.ShotEvidence{}, BadAuthRequest("听写证据需要转写任务（先对候选发起转写）")
		}
		task, err := s.repo.TaskForUser(userID, taskID)
		if err != nil {
			return model.ShotEvidence{}, err
		}
		if task.Type != model.TaskTypeTimelineTranscription || task.Status != model.TaskStatusSucceeded {
			return model.ShotEvidence{}, BadAuthRequest("转写任务还没有成功完成")
		}
		var result timelineTranscriptionResult
		if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
			return model.ShotEvidence{}, BadAuthRequest("转写结果无法读取")
		}
		var heard strings.Builder
		for _, segment := range result.Segments {
			heard.WriteString(segment.Text)
		}
		expected := s.expectedDialogue(shot)
		cer := drama.CER(expected, heard.String())
		metrics = map[string]any{"cer": cer, "threshold": drama.DefaultCERThreshold, "expected": expected, "heard": heard.String(), "segments": result.Segments}
		ok := cer <= drama.DefaultCERThreshold
		evidence.Passed = &ok
		evidence.TaskID = task.ID
	}
	if kind == drama.EvidenceLevels || kind == drama.EvidenceVoice {
		evidence.Passed = nil
	}
	encoded, err := json.Marshal(metrics)
	if err != nil {
		return model.ShotEvidence{}, BadAuthRequest("量数格式无效")
	}
	evidence.MetricsJSON = string(encoded)
	if err := s.repo.CreateShotEvidence(&evidence); err != nil {
		return model.ShotEvidence{}, err
	}
	return evidence, nil
}

// PickShotArtifact 选中一条视频候选：三项证据（同一个人、台词一字不差、无崩坏）不齐不能选。
func (s *Service) PickShotArtifact(userID string, projectID string, artifactID string, req PickShotArtifactRequest) (model.ShotArtifact, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return model.ShotArtifact{}, err
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > dramaPickReasonRunes {
		return model.ShotArtifact{}, BadAuthRequest("选中要写一句理由（200 字以内）")
	}
	artifact, err := s.repo.ShotArtifactForProject(projectID, strings.TrimSpace(artifactID))
	if err != nil {
		return model.ShotArtifact{}, err
	}
	if artifact.Type != drama.ArtifactVideo {
		return model.ShotArtifact{}, BadAuthRequest("只有视频候选需要按证据选中")
	}
	shot, err := s.repo.ShotForProject(projectID, artifact.ShotID)
	if err != nil {
		return model.ShotArtifact{}, err
	}
	evidence, err := s.repo.ProjectShotEvidence(projectID)
	if err != nil {
		return model.ShotArtifact{}, err
	}
	if missing := drama.MissingEvidence(evidenceStates(evidence), artifact.ID, s.shotHasDialogue(projectID, shot)); len(missing) > 0 {
		appErr := NewAppError(http.StatusBadRequest, "这条候选还缺证据："+strings.Join(missing, "、"))
		appErr.Reason = ReasonEvidenceMissing
		return model.ShotArtifact{}, appErr
	}
	now := time.Now()
	selected, err := s.repo.SelectShotArtifact(projectID, artifact.ID, now)
	if err != nil {
		if errors.Is(err, repository.ErrShotArtifactNotReady) {
			return model.ShotArtifact{}, BadAuthRequest(err.Error())
		}
		return model.ShotArtifact{}, err
	}
	pass := true
	pick := model.ShotEvidence{ID: newID(), ProjectID: projectID, UnitID: artifact.UnitID, ShotID: artifact.ShotID, ArtifactID: artifact.ID, Kind: drama.EvidencePick, Passed: &pass, MetricsJSON: "{}", Note: reason, CreatedBy: userID, CreatedAt: now}
	if err := s.repo.CreateShotEvidence(&pick); err != nil {
		return model.ShotArtifact{}, err
	}
	return *selected, nil
}

// DramaProgress 由产物和证据推导进度表与十个终态。
func (s *Service) DramaProgress(userID string, projectID string) (drama.Progress, error) {
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return drama.Progress{}, err
	}
	m, err := projectDramaModel(project)
	if err != nil {
		return drama.Progress{}, err
	}
	shots, err := s.repo.ProjectShots(projectID)
	if err != nil {
		return drama.Progress{}, err
	}
	revisions, err := s.repo.ProjectShotRevisions(projectID)
	if err != nil {
		return drama.Progress{}, err
	}
	revisionByID := map[string]*model.ShotRevision{}
	for i := range revisions {
		revisionByID[revisions[i].ID] = &revisions[i]
	}
	references, err := s.repo.ProjectShotAssetReferences(projectID)
	if err != nil {
		return drama.Progress{}, err
	}
	refCount := map[string]int{}
	for _, ref := range references {
		refCount[ref.ShotID]++
	}
	// 要正文：「风格定了」「色卡齐了」按当前剧集的场次表算。
	units, err := s.repo.ProjectUnits(projectID)
	if err != nil {
		return drama.Progress{}, err
	}
	// 章节顺序用 ProjectUnits 的排序（position，同位置按创建时间）里的下标：直接比 position 时两章同位置会按段号交错。
	unitOrder, unitTitle := map[string]int{}, map[string]string{}
	for i, unit := range units {
		unitOrder[unit.ID], unitTitle[unit.ID] = i, unit.Title
	}
	sort.SliceStable(shots, func(i, j int) bool {
		if unitOrder[shots[i].UnitID] != unitOrder[shots[j].UnitID] {
			return unitOrder[shots[i].UnitID] < unitOrder[shots[j].UnitID]
		}
		return shots[i].Position < shots[j].Position
	})
	input := drama.ProgressInput{Model: m, AuthMode: project.AuthMode, BriefJSON: project.BriefJSON, BibleJSON: project.BibleJSON}
	if input.Palettes, err = s.projectPaletteProgress(userID, projectID); err != nil {
		return drama.Progress{}, err
	}
	input.StyleScenes, input.StyleFromChapters = styleBibleSceneRefs(units)
	input.BibleJSON = currentColorScriptBible(project.BibleJSON, styleSceneTable(units))
	input.StyleRuleShifted = legacyShiftedColorScript(project.BibleJSON, units)
	assets, err := s.ProjectAssets(userID, projectID)
	if err != nil {
		return drama.Progress{}, err
	}
	input.Assets = projectAssetsProgress(assets)
	for _, unit := range units {
		if unit.Kind == model.ProjectUnitKindEpisode {
			input.Episodes = append(input.Episodes, drama.EpisodeInput{ID: unit.ID, Title: unit.Title, WordCount: unit.WordCount})
		}
	}
	for i := range shots {
		shot := shots[i]
		row := drama.ShotInput{ID: shot.ID, UnitID: shot.UnitID, UnitTitle: unitTitle[shot.UnitID], Title: shot.Title, Position: shot.Position, DurationMs: shot.DurationMs, RefCount: refCount[shot.ID]}
		if rev := revisionByID[shot.CurrentRevisionID]; rev != nil {
			inputs := revisionInputs(rev)
			row.Revision = &inputs
			if seg, err := drama.ParseSegment([]byte(rev.SpecJSON)); err == nil && len(seg.Refs) > 0 && row.RefCount == 0 {
				row.RefCount = len(seg.Refs)
			}
			// 不锁人物的段：首帧模式要真有首帧；纯文字段只有没人说话（空镜、环境）时才算不需要参考。
			if seg, err := drama.ParseSegment([]byte(rev.SpecJSON)); err == nil {
				switch seg.Mode {
				case drama.ModeLockFirstFrame:
					if strings.TrimSpace(seg.FirstFrame) != "" {
						row.RefCount = max(row.RefCount, 1)
					}
				case drama.ModeTextOnly:
					if !seg.HasDialogue() {
						row.RefCount = max(row.RefCount, 1)
					}
				}
			}
		}
		input.Shots = append(input.Shots, row)
	}
	artifacts, err := s.repo.ProjectShotArtifacts(projectID)
	if err != nil {
		return drama.Progress{}, err
	}
	for _, a := range artifacts {
		input.Artifacts = append(input.Artifacts, drama.ArtifactState{ID: a.ID, ShotID: a.ShotID, Type: a.Type, Status: a.Status, Selected: a.Selected, UpdatedAt: a.UpdatedAt})
	}
	evidence, err := s.repo.ProjectShotEvidence(projectID)
	if err != nil {
		return drama.Progress{}, err
	}
	input.Evidence = evidenceStates(evidence)
	progress := drama.Derive(input)
	progress.PrevizGateSetting = briefPrevizGate(project.BriefJSON)
	progress.PrevizGate = drama.PrevizGateEnabled(progress.PrevizGateSetting, project.AuthMode)
	return progress, nil
}

// ---------------------------------------------------------------- 量数：转写、抽帧、门禁

func (s *Service) dramaArtifactForQA(userID string, projectID string, artifactID string) (*model.ShotArtifact, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	artifact, err := s.repo.ShotArtifactForProject(projectID, strings.TrimSpace(artifactID))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(artifact.ResourceID) == "" {
		return nil, BadAuthRequest("这条候选没有媒体文件")
	}
	return artifact, nil
}

// DramaTranscribe 对一条候选创建本地转写任务；完成后用 CreateShotEvidence(kind=transcript, taskId) 记字错率。
func (s *Service) DramaTranscribe(userID string, projectID string, artifactID string) (*model.Task, error) {
	artifact, err := s.dramaArtifactForQA(userID, projectID, artifactID)
	if err != nil {
		return nil, err
	}
	return s.CreateTimelineTranscriptionTask(userID, TimelineTranscriptionCreateRequest{ResourceID: artifact.ResourceID, ProjectID: projectID, Language: "zh"})
}

// withResourceFile 把资源下载到临时文件交给 fn（量数工具只读本地文件）。
func (s *Service) withResourceFile(userID string, resourceID string, fn func(path string, resource *model.Resource) error) error {
	resource, reader, err := s.OpenResource(userID, resourceID)
	if err != nil {
		return err
	}
	defer reader.Close()
	dir, err := os.MkdirTemp("", "drama-measure-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ext := filepath.Ext(resource.ObjectKey)
	if ext == "" {
		ext = ".mp4"
	}
	path := filepath.Join(dir, "media"+ext)
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := file.ReadFrom(reader); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return fn(path, resource)
}

// DramaFrames 给一条候选出抽帧拼图，存成资源并记一条结论待定的 frames 证据。
func (s *Service) DramaFrames(userID string, projectID string, artifactID string) (model.ShotEvidence, error) {
	artifact, err := s.dramaArtifactForQA(userID, projectID, artifactID)
	if err != nil {
		return model.ShotEvidence{}, err
	}
	tools, err := drama.DefaultTools()
	if err != nil {
		return model.ShotEvidence{}, NewAppError(http.StatusServiceUnavailable, err.Error())
	}
	var sheet *model.Resource
	err = s.withResourceFile(userID, artifact.ResourceID, func(path string, _ *model.Resource) error {
		out := filepath.Join(filepath.Dir(path), "sheet.jpg")
		if err := drama.ContactSheet(context.Background(), tools, path, out); err != nil {
			return BadAuthRequest(err.Error())
		}
		file, err := os.Open(out)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		sheet, err = s.UploadResourceFile(userID, "contact-sheet-"+artifact.ID+".jpg", info.Size(), "image", 0, 0, 0, file)
		return err
	})
	if err != nil {
		return model.ShotEvidence{}, err
	}
	evidence := model.ShotEvidence{ID: newID(), ProjectID: projectID, UnitID: artifact.UnitID, ShotID: artifact.ShotID, ArtifactID: artifact.ID, Kind: drama.EvidenceFrames, MetricsJSON: "{}", ResourceID: sheet.ID, Note: "抽帧拼图，待看过后判无崩坏", CreatedBy: userID, CreatedAt: time.Now()}
	if err := s.repo.CreateShotEvidence(&evidence); err != nil {
		return model.ShotEvidence{}, err
	}
	return evidence, nil
}

var briefLoudnessPattern = regexp.MustCompile(`(-\d+(?:\.\d+)?)\s*LUFS`)

func briefTargetLUFS(project *model.Project) float64 {
	var doc struct {
		Platform struct {
			Value string `json:"value"`
		} `json:"platform"`
	}
	if json.Unmarshal([]byte(project.BriefJSON), &doc) == nil {
		if m := briefLoudnessPattern.FindStringSubmatch(doc.Platform.Value); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				return v
			}
		}
	}
	return drama.DefaultTargetLUFS
}

// DramaGate 对成片（或一条候选）跑门禁；不给候选时记为成片级证据。
func (s *Service) DramaGate(userID string, projectID string, req DramaGateRequest) (DramaGateResult, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return DramaGateResult{}, err
	}
	evidence := model.ShotEvidence{ID: newID(), ProjectID: projectID, Kind: drama.EvidenceGate, CreatedBy: userID}
	resourceID := strings.TrimSpace(req.ResourceID)
	if artifactID := strings.TrimSpace(req.ArtifactID); artifactID != "" {
		artifact, err := s.dramaArtifactForQA(userID, projectID, artifactID)
		if err != nil {
			return DramaGateResult{}, err
		}
		resourceID = artifact.ResourceID
		evidence.ArtifactID, evidence.ShotID, evidence.UnitID, evidence.Kind = artifact.ID, artifact.ShotID, artifact.UnitID, drama.EvidenceLevels
	}
	if resourceID == "" {
		// 不指定时检查最近一次剪辑导出的成片。
		if film := s.latestProjectFilm(userID, projectID); film != nil {
			resourceID = film.ResourceID
			evidence.TaskID = film.TaskID
		}
	}
	if resourceID == "" {
		return DramaGateResult{}, BadAuthRequest("还没有导出的成片：请先在剪辑里导出，或指定成片资源与候选")
	}
	target := briefTargetLUFS(project)
	if req.TargetLUFS != nil {
		target = *req.TargetLUFS
	}
	tools, err := drama.DefaultTools()
	if err != nil {
		return DramaGateResult{}, NewAppError(http.StatusServiceUnavailable, err.Error())
	}
	var report drama.GateReport
	err = s.withResourceFile(userID, resourceID, func(path string, _ *model.Resource) error {
		var measureErr error
		report, measureErr = drama.Gate(context.Background(), tools, path, target)
		if measureErr != nil {
			return BadAuthRequest(measureErr.Error())
		}
		return nil
	})
	if err != nil {
		return DramaGateResult{}, err
	}
	encoded, _ := json.Marshal(report)
	evidence.MetricsJSON = string(encoded)
	evidence.ResourceID = resourceID
	if evidence.Kind == drama.EvidenceGate {
		passed := report.Passed
		evidence.Passed = &passed
		evidence.Note = "成片门禁"
	} else {
		evidence.Note = "候选响度量数（只报数）"
	}
	evidence.CreatedAt = time.Now()
	if err := s.repo.CreateShotEvidence(&evidence); err != nil {
		return DramaGateResult{}, err
	}
	return DramaGateResult{Report: report, Evidence: evidence}, nil
}

// enforceDramaSegmentTaskDuration 拦下超过项目生成模型单段上限的短剧视频任务（只看带项目和镜头标识的任务）。
// dramaTaskTarget 取短剧视频任务的项目和镜头标识；不是带项目和镜头的视频任务返回空。
func dramaTaskTarget(input map[string]any) (string, string) {
	if mode, _ := input["mode"].(string); mode != "video" {
		return "", ""
	}
	projectID, _ := input["domainProjectId"].(string)
	shotID, _ := input["shotId"].(string)
	if metadata, ok := input["metadata"].(map[string]any); ok {
		if projectID == "" {
			projectID, _ = metadata["domainProjectId"].(string)
		}
		if shotID == "" {
			shotID, _ = metadata["shotId"].(string)
		}
	}
	return strings.TrimSpace(projectID), strings.TrimSpace(shotID)
}

// enforceDramaModelFamily 拦下与项目生成模型不同族的短剧视频任务：分段按项目模型的上限和方言写，
// 换成别的模型族生成会超长或读错提示词。认不出模型族的渠道模型放行。
func (s *Service) enforceDramaModelFamily(userID string, input map[string]any, names ...string) error {
	projectID, shotID := dramaTaskTarget(input)
	if projectID == "" || shotID == "" {
		return nil
	}
	if config, ok := input["config"].(map[string]any); ok {
		for _, key := range []string{"videoModel", "model"} {
			if value, _ := config[key].(string); value != "" {
				names = append(names, value)
			}
		}
	}
	var family drama.Model
	found := false
	for _, name := range names {
		if m, ok := drama.MatchChannelModel(name); ok {
			family, found = m, true
			break
		}
	}
	if !found {
		return nil
	}
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return nil
	}
	m, err := projectDramaModel(project)
	if err != nil {
		return err
	}
	if family.ID != m.ID {
		return BadAuthRequest(fmt.Sprintf("这次选的视频模型属于 %s，项目的生成模型是 %s：请换成 %s 的视频模型，或在项目设置里改生成模型", family.Label, m.Label, m.Label))
	}
	return nil
}

// enforceDramaPrevizGate 是视频前的预演闸门（brief_json.previz_gate，auto 跟授权档位）：闸门生效时，给镜头建视频任务要求这一段
// 有选中、没过期的黑白预演（action_board），否则拒绝（reason previz_required）。不是带项目和镜头的视频任务、项目读不到时放行。
func (s *Service) enforceDramaPrevizGate(userID string, input map[string]any) error {
	projectID, shotID := dramaTaskTarget(input)
	if projectID == "" || shotID == "" {
		return nil
	}
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil || !projectPrevizGate(project) {
		return nil
	}
	ready, err := s.repo.SelectedShotArtifactReady(projectID, shotID, drama.ArtifactActionBoard)
	if err != nil || ready {
		return err
	}
	appErr := NewAppError(http.StatusBadRequest, "这一段还没有选中的黑白预演：先生成并选中黑白预演，再生成视频（预演闸门可在项目设置里关掉）")
	appErr.Reason = ReasonPrevizRequired
	return appErr
}

func (s *Service) enforceDramaSegmentTaskDuration(userID string, input map[string]any) error {
	projectID, shotID := dramaTaskTarget(input)
	if projectID == "" || shotID == "" {
		return nil
	}
	config, _ := input["config"].(map[string]any)
	seconds := 0.0
	switch value := config["videoSeconds"].(type) {
	case string:
		seconds, _ = strconv.ParseFloat(strings.TrimSpace(value), 64)
	case float64:
		seconds = value
	}
	if seconds <= 0 {
		return nil
	}
	project, err := s.repo.ProjectForUser(userID, strings.TrimSpace(projectID))
	if err != nil {
		return nil
	}
	m, err := projectDramaModel(project)
	if err != nil {
		return err
	}
	if seconds > float64(m.Duration.Max) {
		return segmentTooLongError(m, "本段视频", int64(seconds*1000))
	}
	return nil
}
