package drama

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 证据种类（契约 evidence）。
const (
	EvidenceTranscript = "transcript"
	EvidenceFrames     = "frames"
	EvidenceFaces      = "faces"
	EvidenceLevels     = "levels"
	EvidenceVoice      = "voice"
	EvidencePick       = "pick"
	EvidenceGate       = "gate"
	EvidenceReview     = "review"
	EvidenceDelivery   = "delivery"
	EvidenceAcceptance = "acceptance"
)

// ValidEvidenceKind 是否是契约里的证据种类。
func ValidEvidenceKind(kind string) bool {
	switch kind {
	case EvidenceTranscript, EvidenceFrames, EvidenceFaces, EvidenceLevels, EvidenceVoice, EvidencePick, EvidenceGate, EvidenceReview, EvidenceDelivery, EvidenceAcceptance:
		return true
	}
	return false
}

// ShotInput / ArtifactState / EvidenceState 是推导进度需要的最小字段集。
type ShotInput struct {
	ID         string
	UnitID     string
	UnitTitle  string
	Title      string
	Position   int
	DurationMs int64
	Revision   *RevisionInputs
	RefCount   int
}

type ArtifactState struct {
	ID        string
	ShotID    string
	Type      string
	Status    string
	Selected  bool
	UpdatedAt time.Time
}

type EvidenceState struct {
	ShotID     string
	ArtifactID string
	Kind       string
	Passed     *bool
	CreatedAt  time.Time
}

// SegmentProgress 是进度表的一行：一段六格。
type SegmentProgress struct {
	ShotID             string   `json:"shotId"`
	UnitID             string   `json:"unitId"`
	UnitTitle          string   `json:"unitTitle,omitempty"`
	SegmentID          string   `json:"segmentId"`
	Title              string   `json:"title"`
	DurationMs         int64    `json:"durationMs"`
	OverLimit          bool     `json:"overLimit"`
	HasDialogue        bool     `json:"hasDialogue"`
	Script             bool     `json:"script"`
	Refs               bool     `json:"refs"`
	Generated          bool     `json:"generated"`
	Reviewed           bool     `json:"reviewed"`
	InCut              bool     `json:"inCut"`
	Accepted           bool     `json:"accepted"`
	Takes              int      `json:"takes"`
	SelectedArtifactID string   `json:"selectedArtifactId,omitempty"`
	MissingEvidence    []string `json:"missingEvidence,omitempty"`
	// Issues 是带问题装段后挂在这一段镜头上的镜头表检查 error 条数（由 app 按镜头表填）。
	Issues int `json:"issues,omitempty"`
}

// EndState 是片子做完要满足的八件事之一。
type EndState struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Done   bool   `json:"done"`
	Detail string `json:"detail"`
	Skill  string `json:"skill"`
}

type ProgressCounts struct {
	Segments  int `json:"segments"`
	Script    int `json:"script"`
	Refs      int `json:"refs"`
	Generated int `json:"generated"`
	Takes     int `json:"takes"`
	Reviewed  int `json:"reviewed"`
	InCut     int `json:"inCut"`
	Accepted  int `json:"accepted"`
	OverLimit int `json:"overLimit"`
	// Issues 是带问题的段数（由 app 填，见 SegmentProgress.Issues）。
	Issues int `json:"issues"`
}

type Progress struct {
	Model   Model  `json:"model"`
	RunMode string `json:"runMode"`
	// PrevizGate 是视频前的预演闸门是否生效；PrevizGateSetting 是需求文档里的设置（auto / on / off）。由 app 按项目填。
	PrevizGate        bool              `json:"previzGate"`
	PrevizGateSetting string            `json:"previzGateSetting"`
	Segments          []SegmentProgress `json:"segments"`
	Counts            ProgressCounts    `json:"counts"`
	Line              string            `json:"line"`
	EndStates         []EndState        `json:"endStates"`
	Next              *EndState         `json:"next,omitempty"`
	// Pipeline 是建项后的流水线（停在哪、等你做什么），由 app 按产物、任务和资产状态填。
	Pipeline *Pipeline `json:"pipeline,omitempty"`
}

// ProgressInput 汇总一个项目的生产数据。
type ProgressInput struct {
	Model     Model
	RunMode   string
	BriefJSON string
	BibleJSON string
	Shots     []ShotInput
	Artifacts []ArtifactState
	Evidence  []EvidenceState
	// Episodes 是项目里的剧集单元（kind=episode，按顺序）：「剧本定了」看它们。
	Episodes []EpisodeInput
	// Palettes 是项目里的场次色卡（kind=scene，不含场次已不存在的、对不上当前场次表的）：「色卡齐了」看当前每一场是否都有合成了图的色卡。
	Palettes []PaletteInput
	// StyleScenes 是当前的场次表（有剧集看剧集，没有看原文章节，一章当一集；切场与场次色卡同一套）：
	// 「色卡齐了」以它为准，色卡缺场、多场都算没齐。StyleFromChapters 表示取自原文章节。
	StyleScenes       []SceneRef
	StyleFromChapters bool
	// StyleRuleShifted 是切场规则改成「连续同地点并成一场」后，旧规则写的、编号已经错位的色彩脚本场数：「色卡齐了」的说明里提示场次规则已更新。
	StyleRuleShifted int
	// Assets 是项目里角色、场景、道具的概况：「参考齐了」的规则不看它，只在说明里区分资产未提取和有资产还没出图。
	Assets AssetsInput
}

// AssetsInput 是项目里角色、场景、道具一共几个（不含归档的），其中几个还没出图（角色没有三视图，场景、道具没有图）。
type AssetsInput struct {
	Total    int
	Unimaged int
}

// SceneRef 是一场的集号和场序（场编号 E01S02）。
type SceneRef struct {
	Episode int
	SceneNo int
}

// PaletteInput 是一张场次色卡：Code 是场编号（E01S02），Imaged 是已确认的主版本有合成图。
type PaletteInput struct {
	Code   string
	Imaged bool
}

// EpisodeInput 是一集剧本：WordCount 是正文字数（来源注释不算）。
type EpisodeInput struct {
	ID        string
	Title     string
	WordCount int
}

// evidenceFor 返回某条产物上某种证据的最新一条。
func latestEvidence(items []EvidenceState, artifactID, kind string) *EvidenceState {
	var best *EvidenceState
	for i := range items {
		e := &items[i]
		if e.ArtifactID != artifactID || e.Kind != kind {
			continue
		}
		if best == nil || e.CreatedAt.After(best.CreatedAt) {
			best = e
		}
	}
	return best
}

func passed(e *EvidenceState) bool { return e != nil && e.Passed != nil && *e.Passed }

// RequiredEvidence 返回一段入片前必须有的三项证据（无台词的段不要求听写）。
func RequiredEvidence(hasDialogue bool) []string {
	if hasDialogue {
		return []string{EvidenceFaces, EvidenceTranscript, EvidenceFrames}
	}
	return []string{EvidenceFaces, EvidenceFrames}
}

var evidenceLabel = map[string]string{EvidenceFaces: "同一个人", EvidenceTranscript: "台词一字不差", EvidenceFrames: "无崩坏"}

// MissingEvidence 列出一条候选还缺哪几项已通过的证据（用中文标签）。
func MissingEvidence(items []EvidenceState, artifactID string, hasDialogue bool) []string {
	var missing []string
	for _, kind := range RequiredEvidence(hasDialogue) {
		if !passed(latestEvidence(items, artifactID, kind)) {
			missing = append(missing, evidenceLabel[kind])
		}
	}
	return missing
}

var briefFactKeys = []string{"purpose", "platform", "duration", "taste", "budget", "acceptance"}

func briefConfirmed(raw string) (bool, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return false, "还没有需求文档"
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &doc) != nil {
		return false, "需求文档格式错误"
	}
	var missing []string
	for _, key := range briefFactKeys {
		var fact struct {
			Status string `json:"status"`
		}
		if value, ok := doc[key]; !ok || json.Unmarshal(value, &fact) != nil || (fact.Status != "已确认" && fact.Status != "默认，已授权") {
			missing = append(missing, factLabel[key])
		}
	}
	if len(missing) > 0 {
		return false, "未确认：" + strings.Join(missing, "、")
	}
	return true, "六项都已确认或已授权"
}

var factLabel = map[string]string{"purpose": "用途", "platform": "平台与画幅", "duration": "时长", "taste": "口味", "budget": "预算", "acceptance": "验收标准"}

func bibleState(raw string) (hasCharacters bool, allChecked bool, unchecked int) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return false, false, 0
	}
	var doc struct {
		Characters []struct {
			Variants []struct {
				Checked bool `json:"checked"`
			} `json:"variants"`
		} `json:"characters"`
		Props []struct {
			Checked bool `json:"checked"`
		} `json:"props"`
		Locations []struct {
			Checked bool `json:"checked"`
		} `json:"locations"`
	}
	if json.Unmarshal([]byte(raw), &doc) != nil {
		return false, false, 0
	}
	for _, c := range doc.Characters {
		for _, v := range c.Variants {
			if !v.Checked {
				unchecked++
			}
		}
	}
	for _, p := range doc.Props {
		if !p.Checked {
			unchecked++
		}
	}
	for _, l := range doc.Locations {
		if !l.Checked {
			unchecked++
		}
	}
	return len(doc.Characters) > 0, unchecked == 0, unchecked
}

// Derive 从生产数据推导进度表和十个终态（不读也不写任何状态机）。
func Derive(in ProgressInput) Progress {
	out := Progress{Model: in.Model, RunMode: in.RunMode, Segments: []SegmentProgress{}}
	var filmGate, filmDelivery, filmAcceptance *EvidenceState
	for i := range in.Evidence {
		e := &in.Evidence[i]
		if e.ShotID != "" {
			continue
		}
		pick := func(current **EvidenceState) {
			if *current == nil || e.CreatedAt.After((*current).CreatedAt) {
				*current = e
			}
		}
		switch e.Kind {
		case EvidenceGate:
			pick(&filmGate)
		case EvidenceDelivery:
			pick(&filmDelivery)
		case EvidenceAcceptance:
			pick(&filmAcceptance)
		}
	}
	maxMs := in.Model.MaxDurationMs()
	var lastSelection time.Time
	for _, shot := range in.Shots {
		row := SegmentProgress{ShotID: shot.ID, UnitID: shot.UnitID, UnitTitle: shot.UnitTitle, Title: shot.Title, DurationMs: shot.DurationMs}
		row.SegmentID = fmt.Sprintf("S%02d", shot.Position+1)
		hasDialogue := false
		if shot.Revision != nil {
			if seg, err := ParseSegment([]byte(shot.Revision.SpecJSON)); err == nil {
				row.SegmentID = seg.ID
				hasDialogue = seg.HasDialogue()
			} else {
				hasDialogue = strings.TrimSpace(shot.Revision.Dialogue) != ""
			}
		}
		row.HasDialogue = hasDialogue
		row.OverLimit = maxMs > 0 && shot.DurationMs > maxMs
		row.Script = shot.Revision != nil && strings.TrimSpace(shot.Revision.PlotDescription) != "" && shot.DurationMs > 0 && !row.OverLimit
		row.Refs = shot.RefCount > 0
		var selected *ArtifactState
		for i := range in.Artifacts {
			a := &in.Artifacts[i]
			if a.ShotID != shot.ID || a.Type != ArtifactVideo || a.Status == "failed" || a.Status == "stale" {
				continue
			}
			row.Takes++
			if a.Selected {
				selected = a
			}
		}
		row.Generated = row.Takes > 0
		if selected != nil {
			row.SelectedArtifactID = selected.ID
			row.MissingEvidence = MissingEvidence(in.Evidence, selected.ID, hasDialogue)
			row.Reviewed = len(row.MissingEvidence) == 0
			if selected.UpdatedAt.After(lastSelection) {
				lastSelection = selected.UpdatedAt
			}
			accepted := latestEvidence(in.Evidence, selected.ID, EvidenceAcceptance)
			row.Accepted = passed(accepted) || (filmAcceptance != nil && passed(filmAcceptance) && filmAcceptance.CreatedAt.After(selected.UpdatedAt))
			row.InCut = row.Reviewed && filmGate != nil && passed(filmGate) && filmGate.CreatedAt.After(selected.UpdatedAt)
		}
		out.Segments = append(out.Segments, row)
	}
	c := &out.Counts
	c.Segments = len(out.Segments)
	for _, r := range out.Segments {
		for _, cell := range []struct {
			on  bool
			ptr *int
		}{{r.Script, &c.Script}, {r.Refs, &c.Refs}, {r.Generated, &c.Generated}, {r.Reviewed, &c.Reviewed}, {r.InCut, &c.InCut}, {r.Accepted, &c.Accepted}, {r.OverLimit, &c.OverLimit}} {
			if cell.on {
				*cell.ptr++
			}
		}
		c.Takes += r.Takes
	}
	out.Line = fmt.Sprintf("%d 段 · 剧本 %d · 参考 %d · 生成 %d（候选 %d 选 %d）· 过审 %d · 入片 %d · 验收 %d", c.Segments, c.Script, c.Refs, c.Generated, c.Takes, selectedCount(out.Segments), c.Reviewed, c.InCut, c.Accepted)
	if c.OverLimit > 0 {
		out.Line += fmt.Sprintf(" · %d 段超过 %s 的 %d 秒上限", c.OverLimit, in.Model.Label, in.Model.Duration.Max)
	}

	all := func(f func(SegmentProgress) bool) bool {
		if len(out.Segments) == 0 {
			return false
		}
		for _, r := range out.Segments {
			if !f(r) {
				return false
			}
		}
		return true
	}
	briefOK, briefDetail := briefConfirmed(in.BriefJSON)
	if !ValidRunMode(in.RunMode) {
		briefOK, briefDetail = false, "还没定生成模式"
	}
	hasCharacters, bibleChecked, unchecked := bibleState(in.BibleJSON)
	scriptOK, scriptDetail := episodesWritten(in.Episodes)
	styleOK, styleDetail, paletteOK, paletteDetail := styleBibleState(in.BibleJSON, in.Palettes, in.StyleScenes, in.StyleFromChapters, in.StyleRuleShifted)
	refsOK := all(func(r SegmentProgress) bool { return r.Refs }) && bibleChecked && hasCharacters
	reviewedOK := all(func(r SegmentProgress) bool { return r.Reviewed })
	gateOK := filmGate != nil && passed(filmGate) && filmGate.CreatedAt.After(lastSelection)
	deliveredOK := gateOK && filmDelivery != nil && filmDelivery.CreatedAt.After(filmGate.CreatedAt)
	acceptedOK := deliveredOK && filmAcceptance != nil && passed(filmAcceptance) && filmAcceptance.CreatedAt.After(filmDelivery.CreatedAt)
	reusableOK := refsOK && deliveredOK
	refsDetail := fmt.Sprintf("%d/%d 段已挂参考", c.Refs, c.Segments)
	switch {
	case in.Assets.Total == 0:
		refsDetail = "资产未提取（章节页「提取资产」）；" + refsDetail
	case in.Assets.Unimaged > 0:
		refsDetail += fmt.Sprintf("；%d/%d 个资产还没出图", in.Assets.Unimaged, in.Assets.Total)
	}
	if unchecked > 0 {
		refsDetail += fmt.Sprintf("；连戏表还有 %d 项没校验", unchecked)
	}
	gateDetail := "还没有成片门禁记录"
	if filmGate != nil {
		gateDetail = "门禁未过"
		if passed(filmGate) {
			gateDetail = "门禁已过"
			if !gateOK {
				gateDetail = "门禁之后又换了选中的候选，需要重新导出并量"
			}
		}
	}
	out.EndStates = []EndState{
		{"brief", "需求问透了", briefOK, briefDetail, "yz-brief"},
		{"script", "剧本定了", scriptOK, scriptDetail, "yz-script-adapt"},
		{"style", "风格定了", styleOK, styleDetail, "yz-style-lock"},
		{"palette", "色卡齐了", paletteOK, paletteDetail, "yz-scene-palette"},
		{"refs", "参考齐了", refsOK, refsDetail, "yz-cast"},
		{"review", "每段过审了", reviewedOK, fmt.Sprintf("%d/%d 段三项证据齐", c.Reviewed, c.Segments), "yz-shoot"},
		{"gate", "成片过门禁了", gateOK, gateDetail, "yz-cut"},
		{"delivered", "交了", deliveredOK, boolText(deliveredOK, "交付包已出", "还没交付"), "yz-deliver"},
		{"accepted", "甲方验收了", acceptedOK, boolText(acceptedOK, "验收通过", "还没有验收结论"), "yz-deliver"},
		{"reusable", "能复用", reusableOK, boolText(reusableOK, "素材库和连戏表可作下一集输入", "参考齐、交付之后成立"), "yz-deliver"},
	}
	for i := range out.EndStates {
		if !out.EndStates[i].Done {
			next := out.EndStates[i]
			out.Next = &next
			break
		}
	}
	return out
}

// styleBibleState 是「风格定了」（M8 起看锁定结果：风格锚非空、主基调色卡至少一个颜色）和「色卡齐了」（M8 卡 B 起只看场次色卡：
// 当前场次表的每一场都有场次色卡、合成图已绑上，且没有当前场次表里不存在的场次色卡）。
func styleBibleState(bibleJSON string, palettes []PaletteInput, scenes []SceneRef, fromChapters bool, ruleShifted int) (bool, string, bool, string) {
	var doc struct {
		StyleAnchor   *StyleAnchor     `json:"style_anchor"`
		MasterPalette *MasterPalette   `json:"master_palette"`
		StyleLock     *StyleLockRecord `json:"style_lock"`
	}
	raw := strings.TrimSpace(bibleJSON)
	if json.Unmarshal([]byte(raw), &doc) != nil {
		// 形状不对的字段（手改坏了）分开读，别让一个字段连累另一个。
		var loose map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &loose)
		_ = json.Unmarshal(loose["style_anchor"], &doc.StyleAnchor)
		_ = json.Unmarshal(loose["master_palette"], &doc.MasterPalette)
		_ = json.Unmarshal(loose["style_lock"], &doc.StyleLock)
	}
	anchorOK := !doc.StyleAnchor.Empty()
	locked := anchorOK && !doc.MasterPalette.Empty()
	styleDetail := "还没有锁定项目风格：建项目时选风格库或写风格、传参考图，也可以在章节页点「分析项目风格」"
	switch {
	case locked:
		source := map[string]string{StyleLockSourceLibrary: "取自平台风格库", StyleLockSourceAnalysis: "风格分析后锁定", StyleLockSourcePreset: "按画风生成后锁定", StyleLockSourceDirector: "导演分析后锁定"}
		how := ""
		if doc.StyleLock != nil {
			how = source[doc.StyleLock.Source]
		}
		styleDetail = strings.TrimPrefix(fmt.Sprintf("%s，风格锚与 %d 色主基调色卡", how, len(doc.MasterPalette.Colors())), "，")
	case anchorOK:
		styleDetail = "有风格锚但没有主基调色卡：在章节页点「分析项目风格」重新锁定"
	}
	if len(scenes) == 0 {
		detail := "项目还没有剧集或章节"
		return locked, styleDetail, false, detail
	}
	unit, units := "集", "剧集"
	if fromChapters {
		unit, units = "章", "原文章节"
	}
	label := func(r SceneRef) string { return fmt.Sprintf("第 %d %s第 %d 场", r.Episode, unit, r.SceneNo) }
	expected := map[string]bool{}
	for _, r := range scenes {
		expected[SceneCode(r.Episode, r.SceneNo)] = true
	}
	imaged := map[string]bool{}
	has := map[string]bool{}
	var extra []SceneRef
	for _, p := range palettes {
		if !expected[p.Code] && !has[p.Code] {
			if episode, sceneNo, ok := ParseSceneCode(p.Code); ok {
				extra = append(extra, SceneRef{Episode: episode, SceneNo: sceneNo})
			}
		}
		has[p.Code] = true
		imaged[p.Code] = imaged[p.Code] || p.Imaged
	}
	var missing, unimaged []SceneRef
	for _, r := range scenes {
		code := SceneCode(r.Episode, r.SceneNo)
		switch {
		case !has[code]:
			missing = append(missing, r)
		case !imaged[code]:
			unimaged = append(unimaged, r)
		}
	}
	total := len(scenes)
	done := total - len(missing) - len(unimaged)
	paletteDetail := fmt.Sprintf("%d/%d 场有场次色卡", done, total)
	if len(missing) > 0 {
		paletteDetail += "，" + sceneList(missing, label) + "缺（章节页「生成场次色卡」）"
	}
	if len(unimaged) > 0 {
		paletteDetail += "，" + sceneList(unimaged, label) + "的色卡图没合成上（资产页「重新合成」）"
	}
	if len(extra) > 0 {
		paletteDetail += "，" + sceneList(extra, label) + "已不在当前" + units + "里"
	}
	if ruleShifted > 0 {
		paletteDetail += fmt.Sprintf("（场次规则已更新：同一%s里连续同地点的场次头并成一场，%d 场旧色彩脚本的场编号对不上，重新生成场次色卡）", unit, ruleShifted)
	}
	return locked, styleDetail, len(missing) == 0 && len(unimaged) == 0 && len(extra) == 0, paletteDetail
}

// sceneList 把几场写成「第 2 集第 1 场、第 2 集第 2 场」，超过三场只写前三场加「等 N 场」。
func sceneList(refs []SceneRef, label func(SceneRef) string) string {
	parts := make([]string, 0, 3)
	for _, r := range refs[:min(3, len(refs))] {
		parts = append(parts, label(r))
	}
	out := strings.Join(parts, "、")
	if len(refs) > 3 {
		out += fmt.Sprintf("等 %d 场", len(refs))
	}
	return out
}

// episodesWritten 是「剧本定了」：至少一集剧本，每集都有正文（剧本优化写出，或导入剧本）。
func episodesWritten(episodes []EpisodeInput) (bool, string) {
	if len(episodes) == 0 {
		return false, "还没有剧集：在章节页点「剧本优化」，或导入剧本"
	}
	for i, ep := range episodes {
		if ep.WordCount <= 0 {
			return false, fmt.Sprintf("第 %d 集「%s」还没有正文", i+1, ep.Title)
		}
	}
	return true, fmt.Sprintf("%d 集剧本", len(episodes))
}

func selectedCount(rows []SegmentProgress) int {
	n := 0
	for _, r := range rows {
		if r.SelectedArtifactID != "" {
			n++
		}
	}
	return n
}

func boolText(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}
