package drama

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// assets.extract（资产设计，M7.2 第 5 阶段）：一集（或一章）剧本 → 角色、场景、道具，每个资产带中文外观和英文出图提示词。
// 提示词 = 技能上下文（用户任务里是本单元的剧本正文）+ 项目已有资产名录 + 全局风格锚 + 主基调色卡（角色、道具用）+ 本集场次色卡
// （场景用）+ 用色要求 + 风格档案 + 项目画风 + 按 schema 生成的契约（M8 卡 D 起要先有场次色卡才能提取，角色可带服装 / 状态子卡）；一个任务里一轮写一场（场次切分与生成分段同一套，不到两场整单元一轮），一个资产一行 JSON，边流边解析。
// 收尾时按名字和别名与项目已有资产合并（已有的只补别名和缺的外观），其余建成已确认资产，再排出图任务。

const (
	// AssetsExtractMaxKnown 是【项目已有资产】最多列几项。
	AssetsExtractMaxKnown = 120
	assetsKnownChars      = 4000
	assetsColorChars      = 2400
	assetsAppearanceChars = 600
	assetsPromptChars     = 1200
	// assetsVariantLabelChars 是子卡变体名最多几个字。
	assetsVariantLabelChars = 24
)

// 资产设计的分类（与 model.AssetCategory 同名）。
const (
	AssetCharacter   = "character"
	AssetEnvironment = "environment"
	AssetProp        = "prop"
)

var assetCategoryNames = map[string]string{AssetCharacter: "角色", AssetEnvironment: "场景", AssetProp: "道具"}

// AssetCategoryName 是资产设计分类的中文名；不是这三类返回空串。
func AssetCategoryName(category string) string { return assetCategoryNames[category] }

// AssetNameKey 是资产名的比对键：只留字母和数字并转小写（与 model.AssetCandidateNameKey 同一规则）。
func AssetNameKey(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, strings.TrimSpace(value))
}

// AssetsKnown 是项目里已有的一个资产（名录用）：Incomplete 是还缺外观或出图提示词，模型可以补写。
type AssetsKnown struct {
	Category   string
	Name       string
	Aliases    []string
	Incomplete bool
	// Variants 是这个角色已有的子卡（变体名）。
	Variants []string
}

// AssetsExtractInput 是组装资产设计提示词的全部输入。
type AssetsExtractInput struct {
	UnitTitle string
	// UnitLabel 是本单元叫什么（「第 2 集」「第 3 章」），UnitNoun 是集或章，用在用户任务里。
	UnitLabel string
	UnitNoun  string
	// SourceText 是正文纯文本（编辑器 getText 的结果）。
	SourceText string
	Known      []AssetsKnown
	Anchor     *StyleAnchor
	// Master 是项目锁定的主基调色卡（角色、道具按它取色）。
	Master MasterPalette
	// ColorScript 是本集各场的场次色卡（色彩脚本，场景按本场色卡取色；没有剧集或还没做场次色卡时为空）。
	ColorScript []ColorScriptEntry
	Profile     StyleBibleProfile
	Style       StepStyle
	Skill       StepSkill
}

// AssetsExtractTask 是交给技能的【用户任务】：本单元的标题、正文和提取要求。
func AssetsExtractTask(label, noun, title, sourceText string) string {
	head := label
	if title = jsTrim(title); title != "" {
		head += "「" + title + "」"
	}
	return strings.Join([]string{
		"剧本：" + head,
		sourceText,
		"请从这一" + noun + "里提取需要跨镜头保持一致的角色、场景和道具：每个资产写中文外观描述和英文出图提示词（角色是三视图设定图，场景、道具是概念图），供资产库建档和出图。",
	}, "\n\n")
}

// BuildAssetsExtractPrompt 组装资产设计的提示词。
func BuildAssetsExtractPrompt(in AssetsExtractInput) (StepPrompt, error) {
	step, _ := StepByID(StepAssetsExtract)
	contract, err := BuiltinContract(step.Contract)
	if err != nil {
		return StepPrompt{}, err
	}
	label, noun := firstNonEmptyString(in.UnitLabel, "本集"), firstNonEmptyString(in.UnitNoun, "集")
	skillPrompt, files, ok := StepSkillPrompt(in.Skill, AssetsExtractTask(label, noun, in.UnitTitle, in.SourceText), step.SkillBudgetChars, step.SkillLinkedFiles)
	if !ok {
		return StepPrompt{}, fmt.Errorf("技能 %s 当前版本没有 SKILL.md", in.Skill.ID)
	}
	text := joinTruthy([]string{
		skillPrompt,
		assetsKnownPrompt(in.Known),
		assetsAnchorPrompt(in.Anchor),
		assetsMasterPrompt(in.Master),
		assetsColorScriptPrompt(in.ColorScript),
		assetsColorRulePrompt(in),
		assetsProfilePrompt(in.Profile),
		stepStylePrompt(in.Style, "资产的外观和出图提示词按这个画风写，不要写成其他媒介。"),
		contract.Text(contract.DefaultRequirement(), ContractTextOptions{SkillShaped: true}),
	}, "\n\n")
	return StepPrompt{Text: text, Files: files}, nil
}

// assetsKnownPrompt 是【项目已有资产】段：分类、名字、别名，缺外观的标出来；超过 4000 字或 120 项截断。没有时不写。
func assetsKnownPrompt(known []AssetsKnown) string {
	if len(known) == 0 {
		return ""
	}
	lines := []string{"【项目已有资产】下面这些资产已经建好：它们再出现时只写一行 category、name（照抄）和新见到的 aliases，不重复外观；标了「缺外观」的可以补写 appearance 和 prompt_en；角色已有的子卡再出现时 variants 里 label 照抄、只写这次的 scenes。"}
	remaining := assetsKnownChars
	for i, k := range known {
		if i >= AssetsExtractMaxKnown {
			lines = append(lines, "- ……（其余略）")
			break
		}
		line := "- " + jsTrim(k.Name) + "（" + firstNonEmptyString(AssetCategoryName(k.Category), k.Category)
		if len(k.Aliases) > 0 {
			line += "；别名：" + strings.Join(k.Aliases, "、")
		}
		if len(k.Variants) > 0 {
			line += "；子卡：" + strings.Join(k.Variants, "、")
		}
		if k.Incomplete {
			line += "；缺外观"
		}
		line += "）"
		if n := jsLen(line); n > remaining {
			lines = append(lines, "- ……（其余略）")
			break
		} else {
			remaining -= n
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// assetsAnchorPrompt 是【全局风格锚】段（导演风格写的）：没有时不写。
func assetsAnchorPrompt(anchor *StyleAnchor) string {
	return styleAnchorPrompt(anchor, "【全局风格锚】角色、场景、道具的外观和出图提示词都按这个风格写：", false)
}

// styleAnchorPrompt 是风格锚的各项（withCamera 时带镜头语言，分镜用）：没有风格锚时为空。
func styleAnchorPrompt(anchor *StyleAnchor, head string, withCamera bool) string {
	if anchor.Empty() {
		return ""
	}
	fields := [][2]string{{"画风 / 媒介", anchor.Medium}, {"美术风格", anchor.ArtStyle}, {"光线政策", anchor.Lighting}}
	if withCamera {
		fields = append(fields, [2]string{"镜头语言", anchor.Camera})
	}
	fields = append(fields, [][2]string{{"调色倾向", anchor.Grading}, {"时代", anchor.Era}, {"类型", anchor.Genre}, {"画质", anchor.RenderQuality}}...)
	lines := []string{head}
	for _, f := range fields {
		if v := jsCompact(f[1]); v != "" {
			lines = append(lines, "- "+f[0]+"："+v)
		}
	}
	if len(anchor.Forbidden) > 0 {
		lines = append(lines, "- 禁用："+strings.Join(anchor.Forbidden, "、"))
	}
	return strings.Join(lines, "\n")
}

// assetsColorScriptPrompt 是【本集场次色卡】段：每场的颜色（中文名、HEX、用途），用于这一场发生地的场景资产。没有时不写。
func assetsColorScriptPrompt(script []ColorScriptEntry) string {
	if len(script) == 0 {
		return ""
	}
	lines := []string{"【本集场次色卡】场次色卡给每一场定的颜色，用于这一场发生地的场景资产（场景用色从本场色卡与主基调色卡取）："}
	remaining := assetsColorChars
	for _, e := range script {
		colors := make([]string, 0, len(e.SharedColors)+len(e.NewColors))
		for _, c := range e.Colors() {
			item := c.NameZh + " " + c.Hex
			if c.Usage != "" {
				item += "（" + c.Usage + "）"
			}
			colors = append(colors, item)
		}
		line := fmt.Sprintf("- 第 %d 场 %s「%s」：%s", e.SceneNo, e.Code(), e.Scene, strings.Join(colors, "、"))
		if n := jsLen(line); n > remaining {
			lines = append(lines, "- ……（其余略）")
			break
		} else {
			remaining -= n
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// assetsProfilePrompt 是【风格档案】段：档案名、画面质感段和光线政策。
func assetsProfilePrompt(p StyleBibleProfile) string {
	if jsTrim(p.Label) == "" && jsTrim(p.StyleBlock) == "" {
		return ""
	}
	lines := []string{"【风格档案：" + jsTrim(p.Label) + "】后面的分镜和视频按这个档案执行，资产外观和出图提示词不要和它冲突。"}
	if block := jsCompact(p.StyleBlock); block != "" {
		lines = append(lines, "- 画面质感："+jsTruncate(block, stylePromptProfileChars))
	}
	if policy := jsTrim(p.LightPolicy); policy != "" {
		lines = append(lines, "- 光线政策："+policy)
	}
	return strings.Join(lines, "\n")
}

// AssetsExtractTurnPlan 是逐场续写第一回合接在提示词后面的分回合说明。
func AssetsExtractTurnPlan(scenes []ShotsScene) string {
	parts := make([]string, 0, len(scenes))
	for i, s := range scenes {
		parts = append(parts, fmt.Sprintf("第 %d 场%s", i+1, shotsSceneLabel(s)))
	}
	n := len(scenes)
	return fmt.Sprintf("【分回合输出】本集按场次标题共 %d 场：%s。这个对话分 %d 个回合写完：本回合只写第 1 场里出现的资产；我说「继续」时再写下一场。"+
		"每个回合仍按【受保护输出契约】一个资产一行；前面回合已经写过的资产不再重复外观，有新的叫法时只写一行 category、name 和新的 aliases，换了服装或状态时写一行 category、name 和 variants（只写新的一套）；first_scene 写这一回合的场序。",
		n, strings.Join(parts, "；"), n)
}

// AssetsExtractTurnPrompt 是第 k 场（从 1 数，k ≥ 2）的续写指令。
func AssetsExtractTurnPrompt(scenes []ShotsScene, k int) string {
	s := scenes[k-1]
	return fmt.Sprintf("继续写第 %d 场：%s里出现的资产，first_scene 写 %d。前面回合和【项目已有资产】里已有的资产只在有新叫法时写一行 category、name 和 aliases，不重复外观；角色在这一场换了服装或状态时写一行 category、name 和 variants（scenes 写 %d）；这一场的发生地前面没写过就写一行场景。一个资产一行，不要重复前面已经写过的行。", k, shotsSceneLabel(s), k, k)
}

// ---------------------------------------------------------------- 解析

// AssetsExtractRow 是模型写的一行资产。
type AssetsExtractRow struct {
	Category    string   `json:"category"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Appearance  string   `json:"appearance"`
	PromptEn    string   `json:"prompt_en"`
	FirstScene  int      `json:"first_scene"`
	PaletteHint string   `json:"palette_hint"`
	// Anchors 只有场景写：人物能站、能走到的位置（生成分段的站位和镜头表检查 R-SHOT-012 用）。
	Anchors []string `json:"anchors,omitempty"`
	// Variants 只有角色写：换了服装或状态的各套形象（收尾建成子卡「名·变体」）。
	Variants []AssetsVariant `json:"variants,omitempty"`
	// Merge 是与项目已有资产或前面的行同名、同别名（检查时标上），收尾时合并，不新建。
	Merge bool `json:"merge,omitempty"`
}

// HasLook 是写了外观或出图提示词（合并行一般两项都空）。
func (r AssetsExtractRow) HasLook() bool { return r.Appearance != "" || r.PromptEn != "" }

// AssetsExtractParseStats 是解析统计：Rows 是读出的资产行，Invalid 是像 JSON 却读不出、或不是资产的行。
type AssetsExtractParseStats struct {
	Rows    int `json:"rows"`
	Invalid int `json:"invalid"`
}

// assetsAliasSplit 是别名写成一句话时的分隔符。
var assetsAliasSplit = regexp.MustCompile(`[、，,；;/\n]`)

var assetsCategoryAliases = map[string]string{
	"character": AssetCharacter, "role": AssetCharacter, "角色": AssetCharacter, "人物": AssetCharacter,
	"environment": AssetEnvironment, "scene": AssetEnvironment, "location": AssetEnvironment, "场景": AssetEnvironment,
	"prop": AssetProp, "item": AssetProp, "道具": AssetProp,
}

// NormalizeAssetCategory 把常见写法规整成 character / environment / prop；认不出返回原样（检查会报）。
func NormalizeAssetCategory(category string) string {
	key := strings.ToLower(jsTrim(category))
	if v, ok := assetsCategoryAliases[key]; ok {
		return v
	}
	return key
}

// AssetsExtractFromText 逐行解析资产设计的输出：一行一个 JSON 对象。回合标记、代码块围栏、非 JSON 的说明文字跳过；
// aliases 写成一句话、first_scene 写成字符串（「2」「S02」）也认。
func AssetsExtractFromText(text string) ([]AssetsExtractRow, AssetsExtractParseStats) {
	rows := []AssetsExtractRow{}
	var stats AssetsExtractParseStats
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(jsTrim(raw), ",")
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &obj) != nil {
			stats.Invalid++
			continue
		}
		if _, marker := obj[ShotLinesMarkerKey]; marker {
			continue
		}
		_, hasName := obj["name"]
		_, hasCategory := obj["category"]
		if !hasName && !hasCategory {
			stats.Invalid++
			continue
		}
		row := AssetsExtractRow{Aliases: []string{}}
		str := func(key string) string {
			var v any
			_ = json.Unmarshal(obj[key], &v)
			if s, ok := v.(string); ok {
				return jsTrim(s)
			}
			return ""
		}
		row.Category, row.Name = NormalizeAssetCategory(str("category")), str("name")
		row.Appearance, row.PromptEn, row.PaletteHint = str("appearance"), str("prompt_en"), str("palette_hint")
		row.Aliases = assetsAliasList(obj["aliases"], row.Name)
		if row.Category == AssetEnvironment {
			row.Anchors = assetsAliasList(obj["anchors"], "")
		}
		if row.Category == AssetCharacter {
			row.Variants = parseAssetsVariants(obj["variants"])
		}
		var first any
		_ = json.Unmarshal(obj["first_scene"], &first)
		row.FirstScene = assetsSceneNo(first)
		rows = append(rows, row)
		stats.Rows++
	}
	return rows, stats
}

// assetsAliasList 读别名：数组或一句话，去空、去重、去掉和名字相同的。
func assetsAliasList(raw json.RawMessage, name string) []string {
	var v any
	_ = json.Unmarshal(raw, &v)
	var items []string
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			if s, ok := item.(string); ok {
				items = append(items, s)
			}
		}
	case string:
		items = assetsAliasSplit.Split(x, -1)
	}
	out := []string{}
	seen := map[string]bool{}
	if key := AssetNameKey(name); key != "" {
		seen[key] = true
	}
	for _, item := range items {
		item = jsTrim(item)
		key := AssetNameKey(item)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

var assetsSceneDigits = regexp.MustCompile(`(\d+)\s*$`)

// assetsSceneNo 读场序：数字，或「2」「S02」「E01S02」这样的字符串（取最后一段数字）。
func assetsSceneNo(v any) int {
	switch x := v.(type) {
	case float64:
		if x >= 1 {
			return int(x)
		}
	case string:
		if m := assetsSceneDigits.FindStringSubmatch(jsTrim(x)); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

// CountAssetsExtractTurn 校验一轮（一场）的输出：至少一行能用的资产（名字非空、分类合法）；写了外观却没写出图提示词按不合格重试这一轮。
func CountAssetsExtractTurn(text string, _ TurnContext) (int, error) {
	rows, stats := AssetsExtractFromText(text)
	valid := 0
	for _, r := range rows {
		if r.Name == "" || AssetCategoryName(r.Category) == "" {
			continue
		}
		if r.Appearance != "" && r.PromptEn == "" {
			return 0, fmt.Errorf("「%s」写了外观却没写 prompt_en（英文出图提示词）", r.Name)
		}
		valid++
	}
	if valid == 0 {
		return 0, fmt.Errorf("这一轮没有解析出任何一行资产（%d 行读不出）", stats.Invalid)
	}
	return valid, nil
}

// ---------------------------------------------------------------- 检查

// 资产设计检查的规则编号。
const (
	RuleAssetNoName      = "R-ASSET-001"
	RuleAssetCategory    = "R-ASSET-002"
	RuleAssetNoPrompt    = "R-ASSET-003"
	RuleAssetNoLook      = "R-ASSET-004"
	RuleAssetDuplicate   = "R-ASSET-005"
	RuleAssetNoRows      = "R-ASSET-006"
	RuleAssetFirstScene  = "R-ASSET-007"
	RuleAssetPaletteName = "R-ASSET-008"
	// RuleAssetPaletteHex 是外观里写的 HEX 不在对应色卡里（角色、道具查主基调色卡，场景查本场色卡和主基调色卡）。
	RuleAssetPaletteHex = "R-ASSET-009"
	// RuleAssetVariant 是子卡写得不全（没有变体名、没有外观、场序超出），这一项没建。
	RuleAssetVariant = "R-ASSET-010"
)

// AssetsExtractCheckContext 是检查资产设计输出要用的输入：项目已有资产、本单元的场数（0 表示不查场序）、本集场次色卡、
// 主基调色卡和本单元各场的地点（第 k 项是第 k 场，场景资产按它找出现在哪几场的色卡）。
type AssetsExtractCheckContext struct {
	Known          []AssetsKnown
	Scenes         int
	ColorScript    []ColorScriptEntry
	Master         MasterPalette
	SceneLocations []string
}

// AssetsExtractResult 是检查后的资产：Rows 是要写的（合并行 Merge=true，同一资产写了几行已经并成一行），Skipped 是没写的行数，
// SkippedBy 是其中分类合法的按分类计数。
type AssetsExtractResult struct {
	Rows      []AssetsExtractRow
	Skipped   int
	SkippedBy map[string]int
	Report    CheckReport
}

// CheckAssetsExtract 检查并整理解析出的资产：名字为空、分类不合法、新资产没写出图提示词的行不写（error，记成跳过）；
// 与已有资产同名或同别名的标成合并（不报错）；同一资产写了几行的并成第一行（别名合并，空字段用后面的补，warn）；
// 新资产没写外观、场序超出、palette_hint 的色名色卡里没有、外观里的 HEX 不在对应色卡里、子卡写得不全是 warn。
// 角色名写成「林小满·淋湿」的行当成「林小满」的子卡。
func CheckAssetsExtract(rows []AssetsExtractRow, ctx AssetsExtractCheckContext) AssetsExtractResult {
	out := AssetsExtractResult{Rows: []AssetsExtractRow{}, SkippedBy: map[string]int{}, Report: CheckReport{Kind: StepAssetsExtract, Items: []CheckItem{}, Skipped: []CheckSkip{}}}
	add := func(level, rule, name, message string) {
		out.Report.Items = append(out.Report.Items, CheckItem{Level: level, Rule: rule, ShotID: name, Message: message})
		if level == "error" {
			out.Report.Errors++
		} else {
			out.Report.Warnings++
		}
	}
	known := map[string]bool{}
	// knownVariants 是已有角色（名字和别名的键）→ 它已有子卡的变体名键。
	knownVariants := map[string]map[string]bool{}
	for _, k := range ctx.Known {
		labels := map[string]bool{}
		for _, v := range k.Variants {
			labels[AssetVariantKey(v)] = true
		}
		for _, name := range append([]string{k.Name}, k.Aliases...) {
			if key := AssetNameKey(name); key != "" {
				known[k.Category+":"+key] = true
				if k.Category == AssetCharacter {
					knownVariants[key] = labels
				}
			}
		}
	}
	colorNames := map[string]bool{}
	for _, e := range ctx.ColorScript {
		for _, c := range e.Colors() {
			colorNames[AssetNameKey(c.NameZh)], colorNames[AssetNameKey(c.NameEn)] = true, true
		}
	}
	for _, c := range ctx.Master.Colors() {
		colorNames[AssetNameKey(c.NameZh)], colorNames[AssetNameKey(c.NameEn)] = true, true
	}
	delete(colorNames, "")
	written := map[string]int{}
	for _, r := range rows {
		if converted, ok := variantFromTitledRow(r); ok {
			r = converted
		}
		label := firstNonEmptyString(r.Name, "（无名）")
		if r.Name == "" || AssetNameKey(r.Name) == "" {
			add("error", RuleAssetNoName, label, "资产名为空，这一行没写")
			out.Skipped++
			continue
		}
		if AssetCategoryName(r.Category) == "" {
			add("error", RuleAssetCategory, label, fmt.Sprintf("分类「%s」不是 character、environment 或 prop，这一行没写", r.Category))
			out.Skipped++
			continue
		}
		keys := []string{r.Category + ":" + AssetNameKey(r.Name)}
		for _, a := range r.Aliases {
			keys = append(keys, r.Category+":"+AssetNameKey(a))
		}
		if i, ok := firstKey(written, keys); ok {
			prev := &out.Rows[i]
			if r.HasLook() && prev.HasLook() {
				add("warn", RuleAssetDuplicate, label, fmt.Sprintf("和前面的「%s」是同一个资产，又写了一遍外观，只取第一次", prev.Name))
			}
			mergeAssetsRow(prev, r)
			for _, k := range assetsRowKeys(*prev) {
				written[k] = i
			}
			continue
		}
		r.Merge = slices.ContainsFunc(keys, func(k string) bool { return known[k] })
		if !r.Merge && r.PromptEn == "" {
			add("error", RuleAssetNoPrompt, label, "新资产没写 prompt_en（英文出图提示词），没有建")
			out.Skipped++
			out.SkippedBy[r.Category]++
			continue
		}
		if !r.Merge && r.Appearance == "" {
			add("warn", RuleAssetNoLook, label, "新资产没写中文外观描述")
		}
		if ctx.Scenes > 0 && r.FirstScene > ctx.Scenes {
			add("warn", RuleAssetFirstScene, label, fmt.Sprintf("first_scene 写成第 %d 场，本集只有 %d 场", r.FirstScene, ctx.Scenes))
		}
		if len(colorNames) > 0 {
			for _, name := range assetsAliasSplit.Split(r.PaletteHint, -1) {
				if key := AssetNameKey(name); key != "" && !colorNames[key] {
					add("warn", RuleAssetPaletteName, label, fmt.Sprintf("palette_hint 里的「%s」色卡里没有", jsTrim(name)))
				}
			}
		}
		out.Rows = append(out.Rows, r)
		for _, k := range assetsRowKeys(r) {
			written[k] = len(out.Rows) - 1
		}
	}
	if len(out.Rows) == 0 {
		add("error", RuleAssetNoRows, "", "没有一行能用的资产")
	}
	// 合并进来的行可能把超出的场序又带回来：最后统一清掉；子卡和 HEX 在并完行之后查。
	for i := range out.Rows {
		if ctx.Scenes > 0 && out.Rows[i].FirstScene > ctx.Scenes {
			out.Rows[i].FirstScene = 0
		}
		row := out.Rows[i]
		checkAssetsVariants(&out.Rows[i], ctx.Scenes, func(label string) bool {
			for _, key := range assetsRowKeys(row) {
				if knownVariants[strings.TrimPrefix(key, AssetCharacter+":")][AssetVariantKey(label)] {
					return true
				}
			}
			return false
		}, add)
		checkAssetsHexes(out.Rows[i], ctx, add)
	}
	return out
}

func firstKey(index map[string]int, keys []string) (int, bool) {
	for _, k := range keys {
		if i, ok := index[k]; ok {
			return i, true
		}
	}
	return 0, false
}

func assetsRowKeys(r AssetsExtractRow) []string {
	keys := []string{}
	for _, name := range append([]string{r.Name}, r.Aliases...) {
		if key := AssetNameKey(name); key != "" {
			keys = append(keys, r.Category+":"+key)
		}
	}
	return keys
}

// mergeAssetsRow 把同一资产的后一行并进前一行：别名合并（后一行的名字和前一行不同时也算别名），空字段用后一行补。
func mergeAssetsRow(prev *AssetsExtractRow, next AssetsExtractRow) {
	seen := map[string]bool{AssetNameKey(prev.Name): true}
	for _, a := range prev.Aliases {
		seen[AssetNameKey(a)] = true
	}
	for _, a := range append([]string{next.Name}, next.Aliases...) {
		if key := AssetNameKey(a); key != "" && !seen[key] {
			seen[key] = true
			prev.Aliases = append(prev.Aliases, jsTrim(a))
		}
	}
	if prev.Appearance == "" {
		prev.Appearance = next.Appearance
	}
	if prev.PromptEn == "" {
		prev.PromptEn = next.PromptEn
	}
	if prev.FirstScene == 0 {
		prev.FirstScene = next.FirstScene
	}
	if prev.PaletteHint == "" {
		prev.PaletteHint = next.PaletteHint
	}
	if len(prev.Anchors) == 0 {
		prev.Anchors = next.Anchors
	}
	prev.Variants = mergeAssetsVariants(prev.Variants, next.Variants)
}

// AssetsAppearance 是写进资产设定的外观描述（压成一行，600 字以内）。
func AssetsAppearance(r AssetsExtractRow) string {
	return jsTruncate(jsCompact(r.Appearance), assetsAppearanceChars)
}

// AssetsPromptEn 是写进资产设定的英文出图提示词（压成一行，1200 字以内）。
func AssetsPromptEn(r AssetsExtractRow) string {
	return jsTruncate(jsCompact(r.PromptEn), assetsPromptChars)
}

// AssetsPaletteColors 是 palette_hint 引用到的色卡颜色（本集场次色卡和主基调色卡里按色名或英文名对上的，去重，按提到的先后）。
func AssetsPaletteColors(hint string, script []ColorScriptEntry, master MasterPalette) []PaletteColor {
	byKey := map[string]PaletteColor{}
	colors := master.Colors()
	for _, e := range script {
		colors = append(colors, e.Colors()...)
	}
	for _, c := range colors {
		for _, k := range []string{AssetNameKey(c.NameZh), AssetNameKey(c.NameEn)} {
			if _, ok := byKey[k]; k != "" && !ok {
				byKey[k] = c
			}
		}
	}
	var out []PaletteColor
	seen := map[string]bool{}
	for _, name := range assetsAliasSplit.Split(hint, -1) {
		if c, ok := byKey[AssetNameKey(name)]; ok && !seen[c.Hex] {
			seen[c.Hex] = true
			out = append(out, c)
		}
	}
	return out
}
