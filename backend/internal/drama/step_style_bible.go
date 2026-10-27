package drama

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// style.bible（导演风格，M7.2 第 4 阶段）：全部剧集的场次 → 全局风格锚 + 逐场色彩脚本。提示词 = 技能上下文（用户任务里是
// 各集的场次表）+ 项目需求 + 项目画风 + 风格档案 + 已有风格锚与色彩脚本 + 按 schema 生成的契约；一个任务里一轮写一集，
// 第一轮另写风格锚。输出一行一个 JSON：风格锚、每集的 JSON 头、每场一行色彩脚本，边流边解析。
// 收尾时写进 bible_json.style_anchor / color_script，每场建一个色卡资产；色卡出图的英文提示词由 PalettePrompt
// 按《色卡提示词卡》的模板拼，色块数、英文名和 HEX 全部来自色彩脚本。

const (
	// StyleBibleSceneExcerptChars 是每场摘录的字数上限（场次头之后的正文，JS 字符串长度）。
	StyleBibleSceneExcerptChars = 300
	// StyleBibleMinExcerptChars 是总字数超预算、按比例缩短时每场至少留多少字。
	StyleBibleMinExcerptChars = 80
	// StyleBibleExcerptBudget 是全部场次摘录的总字数预算。
	StyleBibleExcerptBudget = 36_000
	// StyleBibleMaxEpisodes 是一次运行最多写几集（一轮一集）。
	StyleBibleMaxEpisodes = 40
	// StyleBibleMinColors 是每场至少几个颜色。
	StyleBibleMinColors     = 3
	stylePromptProfileChars = 600
	styleExistingChars      = 4000
)

// StyleAnchor 是全局风格锚（bible_json.style_anchor）：契约 bible 原有 art_style / render_quality / era / genre，
// 导演风格加 medium、lighting、camera、grading、forbidden。
type StyleAnchor struct {
	Medium        string   `json:"medium,omitempty"`
	ArtStyle      string   `json:"art_style,omitempty"`
	Lighting      string   `json:"lighting,omitempty"`
	Camera        string   `json:"camera,omitempty"`
	Grading       string   `json:"grading,omitempty"`
	Forbidden     []string `json:"forbidden,omitempty"`
	Era           string   `json:"era,omitempty"`
	Genre         string   `json:"genre,omitempty"`
	RenderQuality string   `json:"render_quality,omitempty"`
	// Coerced 是解析时字段类型不符、降级读出的说明（检查报 warn），不写库。
	Coerced []string `json:"-"`
}

// Empty 表示风格锚一个字段都没写。
func (a *StyleAnchor) Empty() bool {
	if a == nil {
		return true
	}
	for _, v := range []string{a.Medium, a.ArtStyle, a.Lighting, a.Camera, a.Grading, a.Era, a.Genre, a.RenderQuality} {
		if jsTrim(v) != "" {
			return false
		}
	}
	return len(a.Forbidden) == 0
}

// PaletteColor 是色彩脚本里的一个颜色。
type PaletteColor struct {
	NameZh string `json:"name_zh"`
	NameEn string `json:"name_en"`
	Hex    string `json:"hex"`
	Usage  string `json:"usage"`
}

// ColorScriptEntry 是一场的色彩脚本（bible_json.color_script 的一项，也是这一场色卡资产的设定）。
type ColorScriptEntry struct {
	Episode int `json:"episode"`
	SceneNo int `json:"scene_no"`
	// Scene 是场次头原文（不带「## 」）。
	Scene        string         `json:"scene"`
	InheritsFrom string         `json:"inherits_from"`
	SharedColors []PaletteColor `json:"shared_colors"`
	NewColors    []PaletteColor `json:"new_colors"`
	Purpose      string         `json:"purpose,omitempty"`
	// References 是视觉影视参考，多个用「；」分隔；模型写成字符串数组时解析拼成一个字符串。
	References string `json:"references,omitempty"`
	// UnitID 是写这一场时它所在的单元（剧集或原文章节），收尾时由服务端填；改编出剧集后，原文章节版同编号的条目靠它认出来。
	UnitID string `json:"unit_id,omitempty"`
	// SceneRule 是写这一场时的切场规则（SceneRuleMerged，收尾时由服务端填）；没有（0）是规则改变前一个场次头一场时写的，
	// 场编号可能和现在的场次表错位，见 LegacySceneCurrent。
	SceneRule int `json:"scene_rule,omitempty"`
	// Coerced 是解析时字段类型不符、降级读出的说明（检查报 warn），不写库。
	Coerced []string `json:"-"`
}

// SceneCode 是场编号 E01S02（第 1 集第 2 场）。
func SceneCode(episode, sceneNo int) string { return fmt.Sprintf("E%02dS%02d", episode, sceneNo) }

// Code 是这一场的场编号。
func (e ColorScriptEntry) Code() string { return SceneCode(e.Episode, e.SceneNo) }

// Colors 是色卡上从左到右的全部颜色：共享色（或场景基础色）在前，本场新增色在后。
func (e ColorScriptEntry) Colors() []PaletteColor {
	return append(slices.Clone(e.SharedColors), e.NewColors...)
}

var (
	hexColorPattern = regexp.MustCompile(`^#[0-9A-F]{6}$`)
	bareHexPattern  = regexp.MustCompile(`^[0-9A-Fa-f]{6}$`)
	sceneCodeInput  = regexp.MustCompile(`(?i)^E(\d{1,3})S(\d{1,3})$`)
)

// ValidHexColor 是 #RRGGBB（大写，规整之后）。
func ValidHexColor(hex string) bool { return hexColorPattern.MatchString(hex) }

// NormalizeHexColor 把常见写法规整成大写 #RRGGBB：去空白、补 #；其余原样（检查会报不合法）。
func NormalizeHexColor(hex string) string {
	hex = strings.ToUpper(jsTrim(hex))
	if bareHexPattern.MatchString(hex) {
		hex = "#" + hex
	}
	return hex
}

// NormalizeSceneCode 把 e1s2、E01S02 规整成 E01S02；不是场编号返回原样（去空白）。
func NormalizeSceneCode(code string) string {
	code = jsTrim(code)
	if m := sceneCodeInput.FindStringSubmatch(code); m != nil {
		ep, _ := strconv.Atoi(m[1])
		sc, _ := strconv.Atoi(m[2])
		return SceneCode(ep, sc)
	}
	return code
}

// normalize 规整一场：HEX、场编号、场次头去掉「## 」，颜色数组不为 nil。
func (e *ColorScriptEntry) normalize() {
	e.Scene = jsTrim(strings.TrimLeft(jsTrim(e.Scene), "# "))
	e.InheritsFrom = NormalizeSceneCode(e.InheritsFrom)
	for _, list := range []*[]PaletteColor{&e.SharedColors, &e.NewColors} {
		if *list == nil {
			*list = []PaletteColor{}
		}
		for i := range *list {
			c := &(*list)[i]
			c.Hex, c.NameZh, c.NameEn, c.Usage = NormalizeHexColor(c.Hex), jsTrim(c.NameZh), jsTrim(c.NameEn), jsTrim(c.Usage)
		}
	}
	e.Purpose, e.References = jsTrim(e.Purpose), jsTrim(e.References)
}

// ---------------------------------------------------------------- 输入与提示词

// StyleBibleScene 是一集里的一场：场序、场次头和开头的摘录。
type StyleBibleScene struct {
	No      int
	Heading string
	Excerpt string
}

// StyleBibleEpisode 是一集（或没有剧集时的一章原文）和它的场次。
type StyleBibleEpisode struct {
	No     int
	UnitID string
	Title  string
	Scenes []StyleBibleScene
}

// StyleBibleProfile 是项目风格档案里写给导演风格看的部分。
type StyleBibleProfile struct {
	Label       string
	StyleBlock  string
	LightPolicy string
}

// StyleBibleInput 是组装导演风格提示词的全部输入。
type StyleBibleInput struct {
	Episodes []StyleBibleEpisode
	// FromChapters 表示项目还没有剧集，场次取自原文章节。
	FromChapters bool
	BriefJSON    string
	Style        StepStyle
	Profile      StyleBibleProfile
	// Anchor 是已有的风格锚；Existing 是这次不重写的各集的色彩脚本（承接用）。
	Anchor   *StyleAnchor
	Existing []ColorScriptEntry
	Skill    StepSkill
}

// StyleBibleScenes 是导演风格一集的场次表：场取自 SplitScenes（连续同地点并成一场），每场摘录开头 limit 个字；
// 没有场次头时整集算一场，场次头用标题，摘录取整集正文。
func StyleBibleScenes(plainText string, title string, limit int) []StyleBibleScene {
	scenes := SplitScenes(plainText, title)
	if len(scenes) == 1 && len(scenes[0].Sources) == 0 {
		return []StyleBibleScene{{No: 1, Heading: scenes[0].Heading, Excerpt: jsTruncate(jsCompact(plainText), limit)}}
	}
	out := make([]StyleBibleScene, 0, len(scenes))
	for _, sc := range scenes {
		out = append(out, StyleBibleScene{No: sc.No, Heading: sc.Heading, Excerpt: sceneExcerpt(sc, limit)})
	}
	return out
}

// StyleBibleExcerptLimit 是 scenes 场时每场摘录的字数：总预算按场数平分，在 80 到 300 字之间。
func StyleBibleExcerptLimit(scenes int) int {
	if scenes <= 0 {
		return StyleBibleSceneExcerptChars
	}
	return min(StyleBibleSceneExcerptChars, max(StyleBibleMinExcerptChars, StyleBibleExcerptBudget/scenes))
}

// StyleBibleTurns 是逐集续写的轮次：一轮一集，ID 是集号，Heading 是集标题。
func StyleBibleTurns(episodes []StyleBibleEpisode) []ShotsScene {
	out := make([]ShotsScene, 0, len(episodes))
	for _, ep := range episodes {
		out = append(out, ShotsScene{ID: strconv.Itoa(ep.No), Heading: ep.Title})
	}
	return out
}

// StyleBibleTask 是交给技能的【用户任务】：要做什么，和各集的场次表。
func StyleBibleTask(episodes []StyleBibleEpisode, fromChapters bool) string {
	var b strings.Builder
	unit := "集剧本"
	if fromChapters {
		unit = "章原文（项目还没有剧集，按原文章节定风格，一章当一集）"
	}
	scenes := 0
	for _, ep := range episodes {
		scenes += len(ep.Scenes)
	}
	fmt.Fprintf(&b, "请为下面 %d %s定全片的导演风格：先写一份全局风格锚（画风与媒介、光比与光线政策、镜头语言、调色倾向、禁用项），再为每一场写色彩脚本（共 %d 场）。"+
		"色彩脚本决定每一场色卡上的颜色：前后相连、同一地点或同一组人物的场要承接前一场的共享色，HEX 不变，只为本场新出现的人物、道具、光源和情绪加新增色。", len(episodes), unit, scenes)
	b.WriteString("\n\n【剧集场次】场编号 E 后是集号、S 后是本集场序；每场附开头的一段正文。")
	for _, ep := range episodes {
		fmt.Fprintf(&b, "\n\n第 %d 集「%s」（%d 场）", ep.No, ep.Title, len(ep.Scenes))
		for _, sc := range ep.Scenes {
			fmt.Fprintf(&b, "\n%s 场次头：%s\n  %s", SceneCode(ep.No, sc.No), sc.Heading, sc.Excerpt)
		}
	}
	return b.String()
}

// BuildStyleBiblePrompt 组装导演风格的提示词。
func BuildStyleBiblePrompt(in StyleBibleInput) (StepPrompt, error) {
	step, _ := StepByID(StepStyleBible)
	contract, err := BuiltinContract(step.Contract)
	if err != nil {
		return StepPrompt{}, err
	}
	skillPrompt, files, ok := StepSkillPrompt(in.Skill, StyleBibleTask(in.Episodes, in.FromChapters), step.SkillBudgetChars, step.SkillLinkedFiles)
	if !ok {
		return StepPrompt{}, fmt.Errorf("技能 %s 当前版本没有 SKILL.md", in.Skill.ID)
	}
	text := joinTruthy([]string{
		skillPrompt,
		stepBriefPrompt(in.BriefJSON, "【项目需求】定风格时遵守下面的需求。"),
		styleBibleStylePrompt(in.Style),
		styleBibleProfilePrompt(in.Profile),
		styleBibleExistingPrompt(in.Anchor, in.Existing),
		contract.Text(contract.DefaultRequirement(), ContractTextOptions{SkillShaped: true}),
	}, "\n\n")
	return StepPrompt{Text: text, Files: files}, nil
}

// styleBibleStylePrompt 是【项目画风】段：风格锚的 medium 与它一致，色彩脚本在它的色彩与光影基线里取色；预设的选项另起一行。
func styleBibleStylePrompt(style StepStyle) string {
	out := stepStylePrompt(style, "风格锚的 medium 与这个画风一致，不改媒介；色彩脚本的颜色在它的色彩与光影基线里取，不另起一套色系。")
	if len(style.Selection) == 0 {
		return out
	}
	keys := make([]string, 0, len(style.Selection))
	for k := range style.Selection {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if v := jsTrim(style.Selection[k]); v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	if len(parts) == 0 {
		return out
	}
	line := "画风选项：" + strings.Join(parts, "；")
	if out == "" {
		title := jsTrim(style.Title)
		if title == "" {
			title = "未命名画风"
		}
		return "【项目画风：" + title + "】" + line
	}
	return out + "\n" + line
}

// styleBibleProfilePrompt 是【风格档案】段：档案名、画面质感段和光线政策。
func styleBibleProfilePrompt(p StyleBibleProfile) string {
	if jsTrim(p.Label) == "" && jsTrim(p.StyleBlock) == "" {
		return ""
	}
	lines := []string{"【风格档案：" + jsTrim(p.Label) + "】后面的分镜和视频按这个档案执行，风格锚不要和它冲突。"}
	if block := jsCompact(p.StyleBlock); block != "" {
		lines = append(lines, "- 画面质感："+jsTruncate(block, stylePromptProfileChars))
	}
	if policy := jsTrim(p.LightPolicy); policy != "" {
		lines = append(lines, "- 光线政策："+policy)
	}
	return strings.Join(lines, "\n")
}

// styleBibleExistingPrompt 是已有的风格锚和这次不重写的各集色彩脚本（重跑时保持连续）；都没有时不写。超过 4000 字截断。
func styleBibleExistingPrompt(anchor *StyleAnchor, existing []ColorScriptEntry) string {
	var parts []string
	if !anchor.Empty() {
		encoded, _ := json.Marshal(anchor)
		parts = append(parts, "【已有风格锚】这是上一次定下的风格锚，沿用它的取向，只在剧本需要时修订：\n"+string(encoded))
	}
	if len(existing) > 0 {
		lines := []string{"【已有色彩脚本】下面这些场这次不重写，可以作为 inherits_from 承接（共享色的 HEX 照抄）："}
		remaining := styleExistingChars
		for _, e := range existing {
			colors := make([]string, 0, len(e.SharedColors)+len(e.NewColors))
			for _, c := range e.Colors() {
				colors = append(colors, c.NameZh+"/"+c.NameEn+" "+c.Hex)
			}
			line := fmt.Sprintf("- %s「%s」：%s", e.Code(), e.Scene, strings.Join(colors, "、"))
			if n := jsLen(line); n > remaining {
				lines = append(lines, "- ……（其余略）")
				break
			} else {
				remaining -= n
			}
			lines = append(lines, line)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// StyleBibleTurnPlan 是第一轮接在提示词后面的分回合说明。
func StyleBibleTurnPlan(turns []ShotsScene) string {
	n := len(turns)
	return fmt.Sprintf("【分回合输出】本次共 %d 集，这个对话分 %d 个回合写完：本回合先写一行全局风格锚，再写第 %s 集（JSON 头加这一集每场一行）；我说「继续」时再写下一集。"+
		"每个回合只写一集，JSON 头的 episode 写这一回合的集号；风格锚只在本回合写一次。", n, n, turns[0].ID)
}

// StyleBibleTurnPrompt 是第 k 轮（从 1 数，k ≥ 2）的续写指令。
func StyleBibleTurnPrompt(turns []ShotsScene, k int) string {
	t := turns[k-1]
	return fmt.Sprintf("继续写第 %s 集「%s」：JSON 头的 episode 写 %s，下面每场一行色彩脚本。承接前面的场时共享色的 name_zh、name_en、hex 照抄。不要再写风格锚，不要重复前面已经写过的集。", t.ID, t.Heading, t.ID)
}

// ---------------------------------------------------------------- 解析

// StyleBibleOutput 是解析出的风格锚和色彩脚本（按输出顺序）。
type StyleBibleOutput struct {
	Anchor *StyleAnchor       `json:"styleAnchor,omitempty"`
	Script []ColorScriptEntry `json:"colorScript"`
	Titles map[int]string     `json:"-"`
	// anchorUnreadable 表示有一行写了 style_anchor，但补过括号也读不出来。
	anchorUnreadable bool
}

// AnchorProblem 说明风格锚缺什么；有可用的风格锚时返回空串。
func (o StyleBibleOutput) AnchorProblem() string {
	switch {
	case !o.Anchor.Empty():
		return ""
	case o.Anchor != nil:
		detail := "medium、art_style、lighting、camera、grading、forbidden 都没写"
		if i := slices.IndexFunc(o.Anchor.Coerced, func(n string) bool { return strings.Contains(n, "字段名一个都不认识") }); i >= 0 {
			detail = o.Anchor.Coerced[i]
		}
		return "全局风格锚是空的（" + detail + "）"
	case o.anchorUnreadable:
		return "全局风格锚那一行不是合法的 JSON，读不出来：要在第一个 JSON 头之前单独一行写 {\"style_anchor\": {…}}，整个对象写在同一行、括号配对"
	}
	return "没有写全局风格锚：要在第一个 JSON 头之前单独一行写 {\"style_anchor\": {…}}"
}

// StyleBibleParseStats 是解析统计：Scenes 是读出的场，Invalid 是像 JSON 却读不出的行，Repaired 是补齐括号才读出的行，
// Coerced 是有字段类型不符、降级读出的行。
type StyleBibleParseStats struct {
	Episodes int `json:"episodes"`
	Scenes   int `json:"scenes"`
	Invalid  int `json:"invalid"`
	Repaired int `json:"repaired,omitempty"`
	Coerced  int `json:"coerced,omitempty"`
	// Anchor 表示读到了风格锚。
	Anchor bool `json:"anchor"`
}

// StyleBibleFromText 逐行解析导演风格的输出：一行一个 JSON 对象——风格锚 {"style_anchor": {…}}、集的 JSON 头
// {"episode": N}、每场的色彩脚本；没写 episode 的场归到前面最近的 JSON 头（defaultEpisode 是没有 JSON 头时的集号）。
// 回合标记、代码块围栏、非 JSON 的说明文字跳过；风格锚写了多次时取最后一次。结尾缺括号的行补齐再读；字段逐个读，
// 类型不符只降级那个字段（见 step_style_bible_decode.go），不丢整行。风格锚没包 style_anchor、或和 JSON 头写在同一行也认。
func StyleBibleFromText(text string, defaultEpisode int) (StyleBibleOutput, StyleBibleParseStats) {
	out := StyleBibleOutput{Script: []ColorScriptEntry{}, Titles: map[int]string{}}
	var stats StyleBibleParseStats
	episode := defaultEpisode
	episodes := map[int]bool{}
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
		_, isScene := obj["scene_no"]
		_, hasShared := obj["shared_colors"]
		_, hasNew := obj["new_colors"]
		isScene = isScene || hasShared || hasNew
		rawAnchor, wrapped := obj["style_anchor"]
		if !wrapped {
			rawAnchor, wrapped = obj["styleAnchor"]
		}
		if wrapped || (!isScene && looksLikeStyleAnchor(obj)) {
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
			// 后写的空风格锚不盖掉前面读出的。
			if out.Anchor.Empty() || !anchor.Empty() {
				out.Anchor = anchor
			}
			stats.Anchor = !out.Anchor.Empty()
			if len(anchor.Coerced) > 0 {
				stats.Coerced++
			}
			if _, header := obj["episode"]; !wrapped || !header {
				continue
			}
		}
		if !isScene {
			if rawEp, ok := obj["episode"]; ok {
				var n any
				_ = json.Unmarshal(rawEp, &n)
				if no := jsonInt(n); no > 0 {
					episode = no
					var title string
					_ = json.Unmarshal(obj["title"], &title)
					out.Titles[no] = jsTrim(title)
				}
			}
			continue
		}
		entry := decodeColorScriptEntry(obj)
		if repaired {
			entry.Coerced = append(entry.Coerced, "这一行括号不配对，补齐后读出")
		}
		if len(entry.Coerced) > 0 {
			stats.Coerced++
		}
		if entry.Episode <= 0 {
			entry.Episode = episode
		}
		entry.normalize()
		out.Script = append(out.Script, entry)
		episodes[entry.Episode] = true
		stats.Scenes++
	}
	stats.Episodes = len(episodes)
	return out, stats
}

// CountStyleBibleTurn 校验一轮（一集）的输出：第一轮要有风格锚；这一集至少一场，集号与这一轮一致；每场色数、HEX 合法；
// 承接本次前面已写的场时共享色 HEX 一致（承接本次没写的场——已有色彩脚本——留给收尾检查）。不合格按临时失败重试这一轮。
func CountStyleBibleTurn(text string, turn TurnContext) (int, error) {
	want, _ := strconv.Atoi(turn.Turn.ID)
	out, stats := StyleBibleFromText(text, want)
	if turn.Index == 1 && out.Anchor.Empty() {
		return 0, fmt.Errorf("第一回合%s", out.AnchorProblem())
	}
	if len(out.Script) == 0 {
		return 0, fmt.Errorf("这一轮没有解析出任何一场的色彩脚本（%d 行读不出）", stats.Invalid)
	}
	prior, _ := StyleBibleFromText(turn.Prior, 0)
	known := map[string]ColorScriptEntry{}
	for _, e := range prior.Script {
		known[e.Code()] = e
	}
	for _, e := range out.Script {
		if want > 0 && e.Episode != want {
			return 0, fmt.Errorf("这一轮应写第 %d 集，%s 写成了第 %d 集", want, e.Code(), e.Episode)
		}
		for _, item := range checkColorScriptEntry(e, known, false) {
			if item.Level == "error" {
				return 0, fmt.Errorf("%s：%s", item.ShotID, item.Message)
			}
		}
		known[e.Code()] = e
	}
	return len(out.Script), nil
}

// ---------------------------------------------------------------- 检查

// 导演风格检查的规则编号。
const (
	RuleStyleNoAnchor  = "R-STYLE-001"
	RuleStyleFewColors = "R-STYLE-002"
	RuleStyleBadHex    = "R-STYLE-003"
	RuleStyleInherit   = "R-STYLE-004"
	RuleStyleSharedHex = "R-STYLE-005"
	RuleStyleScenes    = "R-STYLE-006"
	RuleStyleHeading   = "R-STYLE-007"
	RuleStyleNoShared  = "R-STYLE-008"
	RuleStyleColorName = "R-STYLE-009"
	RuleStyleNoScript  = "R-STYLE-010"
	RuleStyleDuplicate = "R-STYLE-011"
	RuleStyleFieldType = "R-STYLE-012"
)

// StyleBibleCheckContext 是检查导演风格输出要用的输入：这次应写的各集场次，和这次不重写的已有色彩脚本。
type StyleBibleCheckContext struct {
	Episodes []StyleBibleEpisode
	Existing []ColorScriptEntry
}

// CheckStyleBible 检查解析出的风格锚和色彩脚本：有风格锚；每场 ≥3 色、HEX 合法、inherits_from 指向前面已有的场
// （本次前面写过的，或已有色彩脚本里的）、共享色 HEX 与承接场一致——这些是 error，不写库；场次对不上、场次头改了、
// 承接却没写共享色、颜色缺英文名、字段类型不符降级读出（R-STYLE-012）是 warn。
func CheckStyleBible(out StyleBibleOutput, ctx StyleBibleCheckContext) CheckReport {
	report := CheckReport{Kind: StepStyleBible, Items: []CheckItem{}, Skipped: []CheckSkip{}}
	add := func(item CheckItem) {
		report.Items = append(report.Items, item)
		if item.Level == "error" {
			report.Errors++
		} else {
			report.Warnings++
		}
	}
	if problem := out.AnchorProblem(); problem != "" {
		add(CheckItem{Level: "error", Rule: RuleStyleNoAnchor, Message: problem})
	} else {
		for _, note := range out.Anchor.Coerced {
			add(CheckItem{Level: "warn", Rule: RuleStyleFieldType, Message: note})
		}
	}
	if len(out.Script) == 0 {
		add(CheckItem{Level: "error", Rule: RuleStyleNoScript, Message: "没有解析出色彩脚本：每场要单独写一行 JSON"})
		return report
	}
	known := map[string]ColorScriptEntry{}
	for _, e := range ctx.Existing {
		known[e.Code()] = e
	}
	seen := map[string]bool{}
	for _, e := range out.Script {
		code := e.Code()
		if seen[code] {
			add(CheckItem{Level: "warn", Rule: RuleStyleDuplicate, ShotID: code, Message: "同一场写了两次，只取第一次"})
			continue
		}
		seen[code] = true
		for _, note := range e.Coerced {
			add(CheckItem{Level: "warn", Rule: RuleStyleFieldType, ShotID: code, Message: note})
		}
		for _, item := range checkColorScriptEntry(e, known, true) {
			add(item)
		}
		known[code] = e
	}
	for _, ep := range ctx.Episodes {
		for _, sc := range ep.Scenes {
			code := SceneCode(ep.No, sc.No)
			if !seen[code] {
				add(CheckItem{Level: "warn", Rule: RuleStyleScenes, ShotID: code, Message: fmt.Sprintf("「%s」没有写色彩脚本，这一场没有色卡", sc.Heading)})
			}
		}
	}
	expected := map[string]string{}
	for _, ep := range ctx.Episodes {
		for _, sc := range ep.Scenes {
			expected[SceneCode(ep.No, sc.No)] = sc.Heading
		}
	}
	if len(expected) > 0 {
		for _, e := range out.Script {
			heading, ok := expected[e.Code()]
			switch {
			case !ok:
				add(CheckItem{Level: "warn", Rule: RuleStyleScenes, ShotID: e.Code(), Message: "剧集里没有这一场（场序超出），这一场的色卡对不上镜头"})
			case e.Scene != "" && jsCompact(e.Scene) != jsCompact(heading):
				add(CheckItem{Level: "warn", Rule: RuleStyleHeading, ShotID: e.Code(), Message: fmt.Sprintf("场次头写成了「%s」，剧集里是「%s」", e.Scene, heading)})
			}
		}
	}
	return report
}

// checkColorScriptEntry 检查一场：色数、HEX、承接；strictInherit 为 false 时找不到承接场不报（当轮校验看不到已有色彩脚本）。
func checkColorScriptEntry(e ColorScriptEntry, known map[string]ColorScriptEntry, strictInherit bool) []CheckItem {
	code := e.Code()
	var items []CheckItem
	add := func(level, rule, message string) {
		items = append(items, CheckItem{Level: level, Rule: rule, ShotID: code, Message: message})
	}
	colors := e.Colors()
	if len(colors) < StyleBibleMinColors {
		add("error", RuleStyleFewColors, fmt.Sprintf("只有 %d 个颜色，每场至少 %d 个", len(colors), StyleBibleMinColors))
	}
	for _, c := range colors {
		if !ValidHexColor(c.Hex) {
			add("error", RuleStyleBadHex, fmt.Sprintf("「%s」的 HEX「%s」不是 #RRGGBB", c.NameZh, c.Hex))
		}
		if c.NameEn == "" {
			add("warn", RuleStyleColorName, fmt.Sprintf("「%s」没写英文名，色卡提示词里只写 HEX", c.NameZh))
		}
	}
	if e.InheritsFrom == "" {
		return items
	}
	if e.InheritsFrom == code {
		add("error", RuleStyleInherit, "inherits_from 写成了自己")
		return items
	}
	parent, ok := known[e.InheritsFrom]
	if !ok {
		if strictInherit {
			add("error", RuleStyleInherit, fmt.Sprintf("inherits_from「%s」不是前面已有的场", e.InheritsFrom))
		}
		return items
	}
	if len(e.SharedColors) == 0 {
		add("warn", RuleStyleNoShared, fmt.Sprintf("承接 %s 却没写共享色", e.InheritsFrom))
	}
	parentColors := parent.Colors()
	for _, c := range e.SharedColors {
		if slices.ContainsFunc(parentColors, func(p PaletteColor) bool { return p.Hex == c.Hex }) {
			continue
		}
		message := fmt.Sprintf("共享色「%s」%s 在承接场 %s 里没有", c.NameZh, c.Hex, e.InheritsFrom)
		if i := slices.IndexFunc(parentColors, func(p PaletteColor) bool { return p.NameZh != "" && p.NameZh == c.NameZh }); i >= 0 {
			message = fmt.Sprintf("共享色「%s」写成了 %s，承接场 %s 里是 %s", c.NameZh, c.Hex, e.InheritsFrom, parentColors[i].Hex)
		}
		add("error", RuleStyleSharedHex, message)
	}
	return items
}

// ---------------------------------------------------------------- 色卡

// PaletteTitle 是色卡资产的标题：「E01S02 色卡 · 场次头」。
func PaletteTitle(e ColorScriptEntry) string {
	title := e.Code() + " 色卡"
	if e.Scene != "" {
		title += " · " + e.Scene
	}
	return jsTruncate(title, 200)
}

// PaletteDesign 是《色卡提示词卡》【色板设计】的结构句：有承接是「N色复合色板=a色跨段共享 + b色本段新增」，
// 没有承接是「N色场景基础色板=a色基础色 + b色新增色」。
func PaletteDesign(e ColorScriptEntry) string {
	n, a, b := len(e.SharedColors)+len(e.NewColors), len(e.SharedColors), len(e.NewColors)
	if e.InheritsFrom != "" {
		return fmt.Sprintf("%d色复合色板=%d色跨段共享 + %d色本段新增（承接 %s）", n, a, b, e.InheritsFrom)
	}
	return fmt.Sprintf("%d色场景基础色板=%d色基础色 + %d色新增色", n, a, b)
}

// PalettePrompt 按《色卡提示词卡》的英文模板（【prompt】【Subject】【Color Specifications】【Composition】【Lighting】
// 【Style】【Quality】【Constraints】）拼出色卡的出图提示词：色块数、英文名和 HEX 全部取自这一场的色彩脚本，
// 标题行写项目 / 集 / 场。模板句与卡里的原文一致，不由模型改写。
func PalettePrompt(project string, e ColorScriptEntry) string {
	colors := e.Colors()
	n := len(colors)
	specs := make([]string, 0, n)
	for i, c := range colors {
		name := c.NameEn
		if name == "" {
			name = fmt.Sprintf("Color %d", i+1)
		}
		role := "new color in this scene"
		if i < len(e.SharedColors) {
			role = "scene base color"
			if e.InheritsFrom != "" {
				role = "shared color inherited from " + e.InheritsFrom
			}
		}
		if c.Usage != "" {
			role = c.Usage + "; " + role
		}
		specs = append(specs, fmt.Sprintf("%d. %s %s (%s)", i+1, name, c.Hex, role))
	}
	project = jsTrim(project)
	if project == "" {
		project = "PROJECT"
	}
	title := fmt.Sprintf("%s / EP%02d / SC%02d COLOR REFERENCE", project, e.Episode, e.SceneNo)
	return strings.Join([]string{
		"【prompt】",
		"A clean color palette swatch reference card design. 16:9 horizontal layout. White background.",
		"【Subject】",
		fmt.Sprintf("A horizontal arrangement of %d evenly-sized rectangular color swatches arranged in a single row across the canvas. Each swatch is a clean rectangular block of pure flat color with sharp clean edges, no gradient, no texture, no noise, no shadows. Below each color swatch is a small label showing the HEX color code in clean monospace black text.", n),
		"【Color Specifications】",
		fmt.Sprintf("The %d swatches from left to right are:", n),
		strings.Join(specs, "\n"),
		"【Composition】",
		fmt.Sprintf("Each swatch is roughly equal in width and height, arranged with thin equal spacing between them. The labels (HEX codes) are positioned directly below each swatch in small clean monospace black text. The entire palette occupies the central 80%% of the canvas with white margins on all sides. Title text at the top center: \"%s\" in small clean black sans-serif text.", title),
		"【Lighting】",
		"Flat even neutral lighting throughout, no shadows on the swatches, no highlights, perfectly even color reproduction.",
		"【Style】",
		"Professional graphic design palette reference card, color theory chart aesthetic, clean minimal design, like a Pantone color guide or design reference sheet.",
		"【Quality】",
		"Hyperrealistic clean digital design rendering, sharp clean edges on every swatch, perfectly even flat color blocks, professional color reference quality, 8k ultra clean rendering.",
		"【Constraints】",
		"NOT painterly, NOT artistic, NOT illustrated, NOT decorative, NOT gradient, NOT textured, NOT photographic scene, NOT physical objects, NOT shadows on swatches, NOT 3D rendering, NOT colorful background, NOT design elements other than swatches and labels, NOT random colors, NOT colors unrelated to the script, NOT changing inherited shared HEX colors unless explicitly required by the script, NOT cyberpunk neon colors unless the script requires cyberpunk, NOT bright saturated futuristic colors unless the script requires them.",
	}, "\n")
}
