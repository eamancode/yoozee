package drama

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// style.lock（风格锁定，M8 卡 A）：建项目时按项目类型、用户的风格文字、参考图（随任务以图片引用发给能读图的文本模型）和画风预设，
// 定下全片的风格锚和主基调色卡（分组色板）。一个任务一轮；输出一行一个 JSON：第一行风格锚，之后每组一行，边流边解析。
// 收尾只解析、检查，把候选写进任务结果；用户在页面上点「锁定此风格」后才写进 bible_json.style_anchor / master_palette，
// 主色卡图由代码按色值合成（palette_image.go），不调图片模型。

const (
	// StyleLockMinColors / StyleLockMaxColors 是主基调色卡的色数下限（少于它不能锁定）和合成图上最多画几个色。
	StyleLockMinColors = 4
	StyleLockMaxColors = 32
	// StyleLockMaxImages 是一次分析最多带几张参考图。
	StyleLockMaxImages = 6
	// StyleLockTextChars 是用户风格文字的上限（JS 字符串长度）。
	StyleLockTextChars = 2000
)

// 主基调色卡的分组键。
const (
	PaletteGroupProtagonist = "protagonist"
	PaletteGroupSupporting  = "supporting"
	PaletteGroupProps       = "props"
	PaletteGroupEnvironment = "environment"
	PaletteGroupOther       = "other"
)

// MasterPaletteGroupDef 是主基调色卡的一个固定分组：键、中文名、合成图上的英文标题。
type MasterPaletteGroupDef struct {
	Key  string
	Name string
	En   string
}

// MasterPaletteGroupDefs 是固定分组（合成图和提示词按这个顺序）。
var MasterPaletteGroupDefs = []MasterPaletteGroupDef{
	{PaletteGroupProtagonist, "主角组", "PROTAGONISTS"},
	{PaletteGroupSupporting, "配角组", "SUPPORTING CAST"},
	{PaletteGroupProps, "道具组", "PROPS"},
	{PaletteGroupEnvironment, "环境组", "ENVIRONMENT"},
}

// MasterPaletteGroup 是主基调色卡的一组。
type MasterPaletteGroup struct {
	// Key 是 protagonist / supporting / props / environment；认不出的分组是 other，Name 保留模型写的名字。
	Key    string         `json:"key"`
	Name   string         `json:"name"`
	Colors []PaletteColor `json:"colors"`
}

// MasterPalette 是主基调色卡（bible_json.master_palette，也随风格存进平台风格库）。
type MasterPalette struct {
	Groups []MasterPaletteGroup `json:"groups"`
}

// Colors 是全部颜色（按分组顺序）。
func (p *MasterPalette) Colors() []PaletteColor {
	if p == nil {
		return nil
	}
	var out []PaletteColor
	for _, g := range p.Groups {
		out = append(out, g.Colors...)
	}
	return out
}

// Empty 表示一个颜色都没有。
func (p *MasterPalette) Empty() bool { return len(p.Colors()) == 0 }

// MasterPaletteGroupKey 把模型写的组名认成固定分组；认不出返回 other。
func MasterPaletteGroupKey(name string) string {
	n := strings.ToLower(jsCompact(name))
	for _, def := range MasterPaletteGroupDefs {
		if n == def.Key || n == strings.ToLower(def.Name) {
			return def.Key
		}
	}
	switch {
	case strings.Contains(n, "主角") || strings.Contains(n, "主演") || strings.Contains(n, "protagonist") || strings.Contains(n, "lead"):
		return PaletteGroupProtagonist
	case strings.Contains(n, "配角") || strings.Contains(n, "群演") || strings.Contains(n, "supporting"):
		return PaletteGroupSupporting
	case strings.Contains(n, "道具") || strings.Contains(n, "prop"):
		return PaletteGroupProps
	case strings.Contains(n, "环境") || strings.Contains(n, "场景") || strings.Contains(n, "空间") || strings.Contains(n, "environment") || strings.Contains(n, "scene"):
		return PaletteGroupEnvironment
	}
	return PaletteGroupOther
}

// MasterPaletteGroupDefOf 是一个分组键的定义；other 返回以组名为名的定义。
func MasterPaletteGroupDefOf(g MasterPaletteGroup) MasterPaletteGroupDef {
	for _, def := range MasterPaletteGroupDefs {
		if def.Key == g.Key {
			return def
		}
	}
	name := jsTrim(g.Name)
	if name == "" {
		name = "其他"
	}
	return MasterPaletteGroupDef{Key: PaletteGroupOther, Name: name, En: "OTHER"}
}

// NormalizeMasterPalette 规整色板：分组键按组名认，同一分组合并，按固定分组顺序排（认不出的排最后）；HEX 规整成大写 #RRGGBB；空组去掉。
func NormalizeMasterPalette(p MasterPalette) MasterPalette {
	byKey := map[string]int{}
	var out []MasterPaletteGroup
	for _, g := range p.Groups {
		key := g.Key
		if key == "" || (key != PaletteGroupOther && !slices.ContainsFunc(MasterPaletteGroupDefs, func(d MasterPaletteGroupDef) bool { return d.Key == key })) {
			key = MasterPaletteGroupKey(g.Name)
		}
		name := jsTrim(g.Name)
		if key != PaletteGroupOther {
			name = MasterPaletteGroupDefOf(MasterPaletteGroup{Key: key}).Name
		}
		colors := make([]PaletteColor, 0, len(g.Colors))
		for _, c := range g.Colors {
			c.Hex, c.NameZh, c.NameEn, c.Usage = NormalizeHexColor(c.Hex), jsTrim(c.NameZh), jsTrim(c.NameEn), jsTrim(c.Usage)
			colors = append(colors, c)
		}
		mergeKey := key
		if key == PaletteGroupOther {
			mergeKey = "other:" + name
		}
		if i, ok := byKey[mergeKey]; ok {
			out[i].Colors = append(out[i].Colors, colors...)
			continue
		}
		byKey[mergeKey] = len(out)
		out = append(out, MasterPaletteGroup{Key: key, Name: name, Colors: colors})
	}
	rank := func(key string) int {
		if i := slices.IndexFunc(MasterPaletteGroupDefs, func(d MasterPaletteGroupDef) bool { return d.Key == key }); i >= 0 {
			return i
		}
		return len(MasterPaletteGroupDefs)
	}
	slices.SortStableFunc(out, func(a, b MasterPaletteGroup) int { return rank(a.Key) - rank(b.Key) })
	out = slices.DeleteFunc(out, func(g MasterPaletteGroup) bool { return len(g.Colors) == 0 })
	if out == nil {
		out = []MasterPaletteGroup{}
	}
	return MasterPalette{Groups: out}
}

// ValidateMasterPalette 是写库前的形状校验：至少一个颜色，每个颜色 HEX 合法。
func ValidateMasterPalette(p MasterPalette) error {
	if p.Empty() {
		return fmt.Errorf("主基调色卡一个颜色都没有")
	}
	if n := len(p.Colors()); n > StyleLockMaxColors {
		return fmt.Errorf("主基调色卡有 %d 个颜色，最多 %d 个", n, StyleLockMaxColors)
	}
	for _, g := range p.Groups {
		for _, c := range g.Colors {
			if !ValidHexColor(NormalizeHexColor(c.Hex)) {
				return fmt.Errorf("主基调色卡「%s」的「%s」HEX「%s」不是 #RRGGBB", MasterPaletteGroupDefOf(g).Name, c.NameZh, c.Hex)
			}
		}
	}
	return nil
}

// 项目锁定风格的来源（bible_json.style_lock.source）。
const (
	// StyleLockSourceAnalysis 是风格分析（用户写了风格文字或传了参考图）。
	StyleLockSourceAnalysis = "analysis"
	// StyleLockSourceLibrary 是从平台风格库选的现成风格（不跑分析）。
	StyleLockSourceLibrary = "library"
	// StyleLockSourcePreset 是只按画风预设当场生成的（没有文字也没有参考图），不回写风格库。
	StyleLockSourcePreset = "preset"
	// StyleLockSourceDirector 是建项时交给导演分析：拆完剧本后流水线自动跑，按故事（和可选的描述、参考图）定风格，
	// 收尾直接锁定并存进平台风格库（来源 director，说明里写项目名）。
	StyleLockSourceDirector = "director"
	// StyleLockStoryChars 是导演定画风时带进提示词的剧本节选上限（JS 字符串长度）。
	StyleLockStoryChars = 6000
)

// StyleLockRecord 是项目锁定风格的来源（bible_json.style_lock，服务端写）。
type StyleLockRecord struct {
	Source string `json:"source"`
	// LibraryID 是选用的平台风格库条目，或锁定后存进风格库的条目。
	LibraryID   string `json:"library_id,omitempty"`
	ProjectType string `json:"project_type,omitempty"`
	// TaskID 是风格分析任务；AssetID / ResourceID 是项目里的主色卡资产和合成的主色卡图。
	TaskID     string `json:"task_id,omitempty"`
	AssetID    string `json:"asset_id,omitempty"`
	ResourceID string `json:"resource_id,omitempty"`
	LockedAt   string `json:"locked_at,omitempty"`
	LockedBy   string `json:"locked_by,omitempty"`
}

// ---------------------------------------------------------------- 项目类型

// ProjectTypeDef 是画面类型（项目 visual_type，与前端 PROJECT_VISUAL_TYPES 同值），平台风格库按它归档。
type ProjectTypeDef struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// 画面类型的取值。
const (
	VisualLiveAction = "live_action"
	VisualCGReal     = "cg_real"
	VisualAnime3D    = "anime_3d"
	VisualAnime2D    = "anime_2d"
)

// ProjectTypes 是画面类型（建项页样张卡的顺序）。
var ProjectTypes = []ProjectTypeDef{
	{VisualLiveAction, "仿真人"},
	{VisualCGReal, "写实 CG"},
	{VisualAnime3D, "3D 动漫"},
	{VisualAnime2D, "2D 动漫"},
}

// VisualTypeProfile 是画面类型默认的 yoozee 风格档案：仿真人、写实 CG 走 live-realism，3D、2D 动漫走 anime-3d（2D 档案待补）。
func VisualTypeProfile(visualType string) string {
	switch visualType {
	case VisualAnime3D, VisualAnime2D:
		return "anime-3d"
	}
	return DefaultProfileID
}

// ProjectTypeLabel 是项目类型的中文名；不认识返回空串。
func ProjectTypeLabel(value string) string {
	for _, t := range ProjectTypes {
		if t.Value == jsTrim(value) {
			return t.Label
		}
	}
	return ""
}

// ---------------------------------------------------------------- 输入与提示词

// StyleLockInput 是组装风格锁定提示词的全部输入。
type StyleLockInput struct {
	// ProjectType 是画面类型（live_action …）；Text 是用户写的风格文字；Images 是随任务带的参考图张数。
	ProjectType string
	Text        string
	Images      int
	// Story 是剧本节选（导演定画风时带，按故事定风格）；空则不带。
	Story     string
	BriefJSON string
	Style     StepStyle
	Profile   StyleBibleProfile
	Skill     StepSkill
}

// StyleLockTask 是交给技能的【用户任务】。
func StyleLockTask(in StyleLockInput) string {
	var b strings.Builder
	b.WriteString("请为这个短剧项目锁定全片的视觉风格：先写一份全局风格锚（画风与媒介、光比与光线政策、镜头语言、调色倾向、禁用项），" +
		"再给出主基调色卡——按主角组、配角组、道具组、环境组分组的色板，每个颜色写中文名、英文名、HEX 和用途。主基调色卡是全片的底色，" +
		"后面每一场的色卡、角色道具场景的外观都从这里取色。")
	if label := ProjectTypeLabel(in.ProjectType); label != "" {
		fmt.Fprintf(&b, "\n\n【项目类型】%s：风格锚的 medium 要与这个类型一致。", label)
	}
	if text := jsTrim(in.Text); text != "" {
		b.WriteString("\n\n【用户的风格描述】以下是用户写的想要的风格，风格锚和色板都按它定，没写到的你来补足：\n" + jsTruncate(text, StyleLockTextChars))
	}
	if in.Images > 0 {
		fmt.Fprintf(&b, "\n\n【参考图】这条消息附了 %d 张参考图：按图反推风格锁定卡（媒介、人物与场景审美、画面质感、光影），主基调色卡的颜色取图里真实出现的主色。", in.Images)
	}
	if story := jsTrim(in.Story); story != "" {
		b.WriteString("\n\n【故事】以下是这部戏的剧本节选：按题材、年代、地域、人物身份和情绪走向定风格和色板，主角组的颜色要贴着主角的身份和处境：\n" + jsTruncate(story, StyleLockStoryChars))
	}
	if jsTrim(in.Text) == "" && in.Images == 0 {
		if jsTrim(in.Story) != "" {
			b.WriteString("\n\n用户没有写风格描述，也没有上传参考图：由你当导演，按【故事】、【项目类型】和【风格档案】自己定风格和色板。")
		} else {
			b.WriteString("\n\n用户没有写风格描述，也没有上传参考图：按【项目画风】和【风格档案】定风格和色板。")
		}
	}
	return b.String()
}

// BuildStyleLockPrompt 组装风格锁定的提示词。
func BuildStyleLockPrompt(in StyleLockInput) (StepPrompt, error) {
	step, _ := StepByID(StepStyleLock)
	contract, err := BuiltinContract(step.Contract)
	if err != nil {
		return StepPrompt{}, err
	}
	skillPrompt, files, ok := StepSkillPrompt(in.Skill, StyleLockTask(in), step.SkillBudgetChars, step.SkillLinkedFiles)
	if !ok {
		return StepPrompt{}, fmt.Errorf("技能 %s 当前版本没有 SKILL.md", in.Skill.ID)
	}
	style := ""
	if jsTrim(in.Style.Prompt) != "" || len(in.Style.Selection) > 0 {
		style = styleBibleStylePrompt(in.Style)
		style = strings.Replace(style, "色板的颜色在它的色彩与光影基线里取，不另起一套色系。", "主基调色卡在它的色彩与光影基线里取色；用户的风格描述或参考图与它冲突时，以用户为准。", 1)
	}
	text := joinTruthy([]string{
		skillPrompt,
		stepBriefPrompt(in.BriefJSON, "【项目需求】定风格时遵守下面的需求。"),
		style,
		styleBibleProfilePrompt(in.Profile),
		contract.Text(contract.DefaultRequirement(), ContractTextOptions{SkillShaped: true}),
	}, "\n\n")
	return StepPrompt{Text: text, Files: files}, nil
}

// StyleLockAnchorPrompt 是剧本优化等下游步骤里的【项目风格锚】段：项目锁定的风格锚（没有锁定时返回空串）。
func StyleLockAnchorPrompt(anchor *StyleAnchor, intro string) string {
	if anchor.Empty() {
		return ""
	}
	lines := []string{"【项目风格锚】" + intro}
	add := func(label, value string) {
		if value = jsCompact(value); value != "" {
			lines = append(lines, "- "+label+"："+value)
		}
	}
	add("画风 / 媒介", anchor.Medium)
	add("美术风格", anchor.ArtStyle)
	add("时代", anchor.Era)
	add("类型", anchor.Genre)
	add("光线", anchor.Lighting)
	add("镜头语言", anchor.Camera)
	add("调色", anchor.Grading)
	if len(anchor.Forbidden) > 0 {
		add("禁用", strings.Join(anchor.Forbidden, "、"))
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------- 解析

// StyleLockOutput 是解析出的风格锚和主基调色卡。
type StyleLockOutput struct {
	Anchor  *StyleAnchor  `json:"styleAnchor,omitempty"`
	Palette MasterPalette `json:"masterPalette"`
	// Coerced 是色板里字段类型不符、降级读出的说明（检查报 warn）。
	Coerced          []string `json:"-"`
	anchorUnreadable bool
}

// AnchorProblem 说明风格锚缺什么；有可用的风格锚时返回空串。
func (o StyleLockOutput) AnchorProblem() string {
	return anchorProblem(o.Anchor, o.anchorUnreadable)
}

// StyleLockParseStats 是解析统计。
type StyleLockParseStats struct {
	Groups   int  `json:"groups"`
	Colors   int  `json:"colors"`
	Invalid  int  `json:"invalid"`
	Repaired int  `json:"repaired,omitempty"`
	Anchor   bool `json:"anchor"`
}

// StyleLockFromText 逐行解析风格锁定的输出：风格锚 {"style_anchor": {…}}（没包也认）、每组 {"group": "主角组", "colors": [...]}；
// 也认整张色板写在一行 {"master_palette": {"groups": [...]}} 或 {"master_palette": [...]}。结尾缺括号的行补齐再读，字段逐个读、
// 类型不符只降级那个字段（与导演风格同一套读法）；风格锚写了多次取最后一个非空的。
func StyleLockFromText(text string) (StyleLockOutput, StyleLockParseStats) {
	var out StyleLockOutput
	var stats StyleLockParseStats
	var groups []MasterPaletteGroup
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(jsTrim(raw), ",")
		if !strings.HasPrefix(line, "{") {
			continue
		}
		obj, repaired, ok := parseJSONObjectLine(line)
		if !ok {
			if strings.Contains(line, `"style_anchor"`) {
				out.anchorUnreadable = true
			}
			stats.Invalid++
			continue
		}
		if _, marker := obj[ShotLinesMarkerKey]; marker {
			continue
		}
		if repaired {
			stats.Repaired++
		}
		rawAnchor, wrapped := obj["style_anchor"]
		if !wrapped {
			rawAnchor, wrapped = obj["styleAnchor"]
		}
		_, isGroup := obj["colors"]
		if wrapped || (!isGroup && looksLikeStyleAnchor(obj)) {
			anchorObj := obj
			if wrapped {
				anchorObj = nil
				if json.Unmarshal(rawAnchor, &anchorObj) != nil || anchorObj == nil {
					out.anchorUnreadable = true
					stats.Invalid++
					continue
				}
			}
			anchor := decodeStyleAnchor(anchorObj)
			if !wrapped {
				anchor.Coerced = append([]string{"风格锚没有包在 style_anchor 里，按风格锚读"}, anchor.Coerced...)
			}
			if repaired {
				anchor.Coerced = append(anchor.Coerced, "风格锚这一行括号不配对，补齐后读出")
			}
			if out.Anchor.Empty() || !anchor.Empty() {
				out.Anchor = anchor
			}
		}
		for _, key := range []string{"master_palette", "masterPalette"} {
			if rawPalette, ok := obj[key]; ok {
				groups = append(groups, decodePaletteGroups(rawPalette, &out.Coerced)...)
			}
		}
		if isGroup {
			groups = append(groups, decodePaletteGroup(obj, &out.Coerced))
		}
	}
	out.Palette = NormalizeMasterPalette(MasterPalette{Groups: groups})
	stats.Anchor = !out.Anchor.Empty()
	stats.Groups = len(out.Palette.Groups)
	stats.Colors = len(out.Palette.Colors())
	return out, stats
}

// decodePaletteGroup 逐字段读一组：组名取 group（没有时取 name），颜色按导演风格的颜色读法。
func decodePaletteGroup(obj map[string]json.RawMessage, notes *[]string) MasterPaletteGroup {
	r := &styleFieldReader{obj: obj}
	name := r.text("group", false, false)
	if name == "" {
		name = r.text("name", false, false)
	}
	r.label = "「" + firstNonBlank(name, "未命名分组") + "」的 "
	for i := range r.notes {
		r.notes[i] = r.label + r.notes[i]
	}
	colors := r.colors("colors")
	*notes = append(*notes, r.notes...)
	return MasterPaletteGroup{Key: MasterPaletteGroupKey(name), Name: name, Colors: colors}
}

// decodePaletteGroups 读整张色板：{"groups": [...]} 或分组数组。
func decodePaletteGroups(raw json.RawMessage, notes *[]string) []MasterPaletteGroup {
	var wrapper struct {
		Groups []json.RawMessage `json:"groups"`
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &wrapper) == nil && wrapper.Groups != nil {
		list = wrapper.Groups
	} else if json.Unmarshal(raw, &list) != nil {
		*notes = append(*notes, "master_palette 写成了"+jsonKind(raw)+"，没读")
		return nil
	}
	out := make([]MasterPaletteGroup, 0, len(list))
	for _, item := range list {
		var obj map[string]json.RawMessage
		if json.Unmarshal(item, &obj) != nil || obj == nil {
			*notes = append(*notes, "master_palette 里有一组写成了"+jsonKind(item)+"，没读")
			continue
		}
		out = append(out, decodePaletteGroup(obj, notes))
	}
	return out
}

// CountStyleLockTurn 校验这一轮的输出：要有风格锚、有色板，检查没有 error；不合格按临时失败重试。返回颜色数。
func CountStyleLockTurn(text string, _ TurnContext) (int, error) {
	out, stats := StyleLockFromText(text)
	report := CheckStyleLock(out)
	for _, item := range report.Items {
		if item.Level == "error" {
			return 0, fmt.Errorf("%s（%d 行读不出）", item.Message, stats.Invalid)
		}
	}
	return stats.Colors, nil
}

// ---------------------------------------------------------------- 检查

// 风格锁定检查的规则编号。
const (
	RuleLockNoAnchor     = "R-LOCK-001"
	RuleLockFewColors    = "R-LOCK-002"
	RuleLockBadHex       = "R-LOCK-003"
	RuleLockGroupMissing = "R-LOCK-004"
	RuleLockColorName    = "R-LOCK-005"
	RuleLockFieldType    = "R-LOCK-006"
	RuleLockDuplicateHex = "R-LOCK-007"
	RuleLockTooMany      = "R-LOCK-008"
)

// CheckStyleLock 检查风格锚和主基调色卡：没有风格锚、色数不到 4、HEX 不合法是 error（不能锁定）；缺主角组或环境组、颜色缺中文名或英文名、
// 同一个 HEX 写了两次、字段类型降级读出、色数超过合成图上限是 warn。
func CheckStyleLock(out StyleLockOutput) CheckReport {
	report := CheckReport{Kind: StepStyleLock, Items: []CheckItem{}, Skipped: []CheckSkip{}}
	add := func(level, rule, where, message string) {
		report.Items = append(report.Items, CheckItem{Level: level, Rule: rule, ShotID: where, Message: message})
		if level == "error" {
			report.Errors++
		} else {
			report.Warnings++
		}
	}
	if problem := out.AnchorProblem(); problem != "" {
		add("error", RuleLockNoAnchor, "", problem)
	} else {
		for _, note := range out.Anchor.Coerced {
			add("warn", RuleLockFieldType, "", note)
		}
	}
	for _, note := range out.Coerced {
		add("warn", RuleLockFieldType, "", note)
	}
	colors := out.Palette.Colors()
	if len(colors) < StyleLockMinColors {
		add("error", RuleLockFewColors, "", fmt.Sprintf("主基调色卡只有 %d 个颜色，至少 %d 个（每组一行 {\"group\": \"主角组\", \"colors\": [...]}）", len(colors), StyleLockMinColors))
	}
	if len(colors) > StyleLockMaxColors {
		add("warn", RuleLockTooMany, "", fmt.Sprintf("主基调色卡有 %d 个颜色，合成图只画前 %d 个", len(colors), StyleLockMaxColors))
	}
	for _, key := range []string{PaletteGroupProtagonist, PaletteGroupEnvironment} {
		if !slices.ContainsFunc(out.Palette.Groups, func(g MasterPaletteGroup) bool { return g.Key == key }) {
			add("warn", RuleLockGroupMissing, "", fmt.Sprintf("没有写%s", MasterPaletteGroupDefOf(MasterPaletteGroup{Key: key}).Name))
		}
	}
	seen := map[string]string{}
	for _, g := range out.Palette.Groups {
		group := MasterPaletteGroupDefOf(g).Name
		for _, c := range g.Colors {
			name := firstNonBlank(c.NameZh, c.NameEn, c.Hex)
			if !ValidHexColor(c.Hex) {
				add("error", RuleLockBadHex, group, fmt.Sprintf("「%s」的 HEX「%s」不是 #RRGGBB", name, c.Hex))
				continue
			}
			if c.NameZh == "" || c.NameEn == "" {
				add("warn", RuleLockColorName, group, fmt.Sprintf("「%s」%s 没写%s", name, c.Hex, map[bool]string{true: "中文名", false: "英文名"}[c.NameZh == ""]))
			}
			if prev, ok := seen[c.Hex]; ok {
				add("warn", RuleLockDuplicateHex, group, fmt.Sprintf("%s 在%s里已经写过", c.Hex, prev))
			} else {
				seen[c.Hex] = group
			}
		}
	}
	return report
}
