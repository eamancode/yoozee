package drama

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// scene.palette（场次色卡，M8 卡 B）：以项目锁定的全局风格锚和主基调色卡为参考，给每集每场写色彩脚本。提示词 = 技能上下文
// （用户任务里是各集的场次表：场编号、场次头、内外 / 地点 / 时间、开头 300 字）+ 风格锚 + 主基调色卡（分组全文）+ 风格档案
// + 这次不重写的已有色彩脚本 + 按 schema 生成的契约；一个任务里一轮写一集。输出一行一个 JSON：每场一行（场编号、承接哪一场、
// 承接色、本场新增色，每色带分组），边流边解析。本步骤不写风格锚（风格锚只由 style.lock 产出），模型多写了也忽略。
// 收尾时写进 bible_json.color_script，每场一张场次色卡资产，色卡图由代码按色值合成（palette_image.go），不调图片模型。

const (
	// ScenePaletteSceneExcerptChars 是每场摘录的字数上限（场次头之后的正文，JS 字符串长度）。
	ScenePaletteSceneExcerptChars = 300
	// ScenePaletteMinExcerptChars 是总字数超预算、按比例缩短时每场至少留多少字。
	ScenePaletteMinExcerptChars = 80
	// ScenePaletteExcerptBudget 是全部场次摘录的总字数预算。
	ScenePaletteExcerptBudget = 36_000
	// ScenePaletteMaxEpisodes 是一次运行最多写几集（一轮一集）。
	ScenePaletteMaxEpisodes = 40
	// ScenePaletteMinColors 是每场至少几个颜色；ScenePaletteMaxColors 是合成图上最多画几个（多了不写库）。
	ScenePaletteMinColors     = 3
	ScenePaletteMaxColors     = StyleLockMaxColors
	scenePaletteExistingChars = 4000
)

// ---------------------------------------------------------------- 输入与提示词

// ScenePaletteScene 是一集里的一场：场序、场次头、内外 / 地点 / 时间和开头的摘录。
type ScenePaletteScene struct {
	No       int
	Heading  string
	InOut    string
	Location string
	Times    []string
	Excerpt  string
}

// ScenePaletteEpisode 是一集（或没有剧集时的一章原文）和它的场次。
type ScenePaletteEpisode struct {
	No     int
	UnitID string
	Title  string
	Scenes []ScenePaletteScene
}

// ScenePaletteInput 是组装场次色卡提示词的全部输入。
type ScenePaletteInput struct {
	Episodes []ScenePaletteEpisode
	// FromChapters 表示项目还没有剧集，场次取自原文章节。
	FromChapters bool
	// Anchor、Master 是项目锁定的风格锚和主基调色卡（只读参考，本步骤不改）。
	Anchor  *StyleAnchor
	Master  MasterPalette
	Profile StyleBibleProfile
	// Existing 是这次不重写的各集的色彩脚本（承接用）。
	Existing []ColorScriptEntry
	Skill    StepSkill
}

// ScenePaletteScenes 是一集的场次表：场取自 SplitScenes（连续同地点且内外相同并成一场），每场摘录开头 limit 个字；
// 没有场次头时整集算一场，场次头用标题，摘录取整集正文。
func ScenePaletteScenes(plainText string, title string, limit int) []ScenePaletteScene {
	scenes := SplitScenes(plainText, title)
	if len(scenes) == 1 && len(scenes[0].Sources) == 0 {
		return []ScenePaletteScene{{No: 1, Heading: scenes[0].Heading, Excerpt: jsTruncate(jsCompact(plainText), limit)}}
	}
	out := make([]ScenePaletteScene, 0, len(scenes))
	for _, sc := range scenes {
		out = append(out, ScenePaletteScene{No: sc.No, Heading: sc.Heading, InOut: sc.InOut, Location: sc.Location, Times: sc.Times, Excerpt: sceneExcerpt(sc, limit)})
	}
	return out
}

// ScenePaletteExcerptLimit 是 scenes 场时每场摘录的字数：总预算按场数平分，在 80 到 300 字之间。
func ScenePaletteExcerptLimit(scenes int) int {
	if scenes <= 0 {
		return ScenePaletteSceneExcerptChars
	}
	return min(ScenePaletteSceneExcerptChars, max(ScenePaletteMinExcerptChars, ScenePaletteExcerptBudget/scenes))
}

// ScenePaletteTurns 是逐集续写的轮次：一轮一集，ID 是集号，Heading 是集标题。
func ScenePaletteTurns(episodes []ScenePaletteEpisode) []ShotsScene {
	out := make([]ShotsScene, 0, len(episodes))
	for _, ep := range episodes {
		out = append(out, ShotsScene{ID: strconv.Itoa(ep.No), Heading: ep.Title})
	}
	return out
}

// sceneFacts 是场次表里一场的内外、地点、时间（读不出的不写）。
func sceneFacts(sc ScenePaletteScene) string {
	var parts []string
	if sc.InOut != "" {
		parts = append(parts, "内外："+sc.InOut)
	}
	if sc.Location != "" {
		parts = append(parts, "地点："+sc.Location)
	}
	if len(sc.Times) > 0 {
		parts = append(parts, "时间："+strings.Join(sc.Times, "→"))
	}
	if len(parts) == 0 {
		return ""
	}
	return "（" + strings.Join(parts, "；") + "）"
}

// ScenePaletteTask 是交给技能的【用户任务】：要做什么，和各集的场次表。
func ScenePaletteTask(episodes []ScenePaletteEpisode, fromChapters bool) string {
	var b strings.Builder
	unit := "集剧本"
	if fromChapters {
		unit = "章原文（项目还没有剧集，按原文章节做色卡，一章当一集）"
	}
	scenes := 0
	for _, ep := range episodes {
		scenes += len(ep.Scenes)
	}
	fmt.Fprintf(&b, "请为下面 %d %s的每一场写场次色卡的色彩脚本（共 %d 场）。全片风格已经锁定：颜色从【项目风格锚】的调色倾向和【主基调色卡】里取，"+
		"角色、道具沿用主基调色卡里同名的颜色（HEX 照抄），环境按这一场的地点、内外和时间定。"+
		"前后相连、同一地点或同一组人物的场要承接前一场的颜色（承接色 HEX 不变），只为本场新出现的人物、道具、光源和情绪加新增色。", len(episodes), unit, scenes)
	b.WriteString("\n\n【剧集场次】场编号 E 后是集号、S 后是本集场序；每场附开头的一段正文。")
	for _, ep := range episodes {
		fmt.Fprintf(&b, "\n\n第 %d 集「%s」（%d 场）", ep.No, ep.Title, len(ep.Scenes))
		for _, sc := range ep.Scenes {
			fmt.Fprintf(&b, "\n%s 场次头：%s%s\n  %s", SceneCode(ep.No, sc.No), sc.Heading, sceneFacts(sc), sc.Excerpt)
		}
	}
	return b.String()
}

// MasterPalettePrompt 是【主基调色卡】段：按分组列出全部颜色（中文名、英文名、HEX、用途）。没有颜色时为空。
func MasterPalettePrompt(p MasterPalette, intro string) string {
	p = NormalizeMasterPalette(p)
	if p.Empty() {
		return ""
	}
	lines := []string{"【主基调色卡】" + intro}
	for _, g := range p.Groups {
		def := MasterPaletteGroupDefOf(g)
		lines = append(lines, fmt.Sprintf("%s（group: %s）：", def.Name, def.Key))
		for _, c := range g.Colors {
			line := "- " + firstNonBlank(c.NameZh, c.NameEn, "未命名") + " / " + jsTrim(c.NameEn) + " " + c.Hex
			if u := jsCompact(c.Usage); u != "" {
				line += "：" + u
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// BuildScenePalettePrompt 组装场次色卡的提示词。
func BuildScenePalettePrompt(in ScenePaletteInput) (StepPrompt, error) {
	step, _ := StepByID(StepScenePalette)
	contract, err := BuiltinContract(step.Contract)
	if err != nil {
		return StepPrompt{}, err
	}
	skillPrompt, files, ok := StepSkillPrompt(in.Skill, ScenePaletteTask(in.Episodes, in.FromChapters), step.SkillBudgetChars, step.SkillLinkedFiles)
	if !ok {
		return StepPrompt{}, fmt.Errorf("技能 %s 当前版本没有 SKILL.md", in.Skill.ID)
	}
	text := joinTruthy([]string{
		skillPrompt,
		StyleLockAnchorPrompt(in.Anchor, "这是建项目时锁定的全片风格，色卡的颜色服从它的调色倾向和禁用项；本步骤只写色彩脚本，不改风格锚。"),
		MasterPalettePrompt(in.Master, "这是锁定风格时定的全片主基调色卡：角色、道具的颜色从对应分组里取，同名颜色 HEX 照抄；场景颜色以环境组为基础，按本场改。每个颜色的 group 写它归哪一组。"),
		styleBibleProfilePrompt(in.Profile),
		scenePaletteExistingPrompt(in.Existing),
		contract.Text(contract.DefaultRequirement(), ContractTextOptions{SkillShaped: true}),
	}, "\n\n")
	return StepPrompt{Text: text, Files: files}, nil
}

// scenePaletteExistingPrompt 是这次不重写的各集色彩脚本（重跑一部分剧集时承接用）；没有时不写。超过 4000 字截断。
func scenePaletteExistingPrompt(existing []ColorScriptEntry) string {
	if len(existing) == 0 {
		return ""
	}
	lines := []string{"【已有场次色卡】下面这些场这次不重写，可以作为 inherits_from 承接（承接色的 name_zh、name_en、hex 照抄）："}
	remaining := scenePaletteExistingChars
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
	return strings.Join(lines, "\n")
}

// ScenePaletteTurnPlan 是第一轮接在提示词后面的分回合说明。
func ScenePaletteTurnPlan(turns []ShotsScene) string {
	n := len(turns)
	return fmt.Sprintf("【分回合输出】本次共 %d 集，这个对话分 %d 个回合写完：本回合只写第 %s 集，这一集每场一行；我说「继续」时再写下一集。"+
		"每个回合只写一集，scene 用这一集的场编号；不写风格锚。", n, n, turns[0].ID)
}

// ScenePaletteTurnPrompt 是第 k 轮（从 1 数，k ≥ 2）的续写指令。
func ScenePaletteTurnPrompt(turns []ShotsScene, k int) string {
	t := turns[k-1]
	return fmt.Sprintf("继续写第 %s 集「%s」：这一集每场一行色彩脚本，scene 写 E%02dS 开头的场编号。承接前面的场时承接色的 name_zh、name_en、hex 照抄。不要写风格锚，不要重复前面已经写过的集。", t.ID, t.Heading, atoiOr(t.ID))
}

func atoiOr(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// ---------------------------------------------------------------- 解析

// ScenePaletteOutput 是解析出的色彩脚本（按输出顺序）。
type ScenePaletteOutput struct {
	Script []ColorScriptEntry `json:"colorScript"`
	// AnchorIgnored 是模型多写了几行风格锚（本步骤不写风格锚，忽略）。
	AnchorIgnored int `json:"-"`
}

// ScenePaletteParseStats 是解析统计：Scenes 是读出的场，Invalid 是像 JSON 却读不出的行，Repaired 是补齐括号才读出的行，
// Coerced 是有字段类型不符、降级读出的行，AnchorIgnored 是忽略的风格锚行。
type ScenePaletteParseStats struct {
	Episodes      int `json:"episodes"`
	Scenes        int `json:"scenes"`
	Invalid       int `json:"invalid"`
	Repaired      int `json:"repaired,omitempty"`
	Coerced       int `json:"coerced,omitempty"`
	AnchorIgnored int `json:"anchorIgnored,omitempty"`
}

// ScenePaletteFromText 逐行解析场次色卡的输出：每场一行 JSON（scene 写场编号）；也认旧写法——集的 JSON 头 {"episode": N}
// 下面每场写 scene_no，没写集号的场归到前面最近的 JSON 头（defaultEpisode 是没有 JSON 头时的集号）。回合标记、代码块围栏、
// 非 JSON 的说明文字跳过；风格锚行忽略（记 AnchorIgnored）。结尾缺括号的行补齐再读；字段逐个读，类型不符只降级那个字段。
func ScenePaletteFromText(text string, defaultEpisode int) (ScenePaletteOutput, ScenePaletteParseStats) {
	out := ScenePaletteOutput{Script: []ColorScriptEntry{}}
	var stats ScenePaletteParseStats
	episode := defaultEpisode
	episodes := map[int]bool{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(jsTrim(raw), ",")
		if !strings.HasPrefix(line, "{") {
			continue
		}
		obj, repaired, ok := parseJSONObjectLine(line)
		if !ok {
			stats.Invalid++
			continue
		}
		if _, marker := obj[ShotLinesMarkerKey]; marker {
			continue
		}
		if repaired {
			stats.Repaired++
		}
		_, hasSceneNo := obj["scene_no"]
		_, hasShared := obj["shared_colors"]
		_, hasNew := obj["new_colors"]
		isScene := hasSceneNo || hasShared || hasNew
		_, wrapped := obj["style_anchor"]
		_, wrappedCamel := obj["styleAnchor"]
		if wrapped || wrappedCamel || (!isScene && looksLikeStyleAnchor(obj)) {
			out.AnchorIgnored++
			if !isScene {
				continue
			}
		}
		if !isScene {
			if rawEp, ok := obj["episode"]; ok {
				var n any
				_ = json.Unmarshal(rawEp, &n)
				if no := jsonInt(n); no > 0 {
					episode = no
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
	stats.AnchorIgnored = out.AnchorIgnored
	return out, stats
}

// CountScenePaletteTurn 校验一轮（一集）的输出：这一集至少一场，集号与这一轮一致；每场色数、HEX 合法；承接本次前面已写的场时
// 承接色 HEX 一致（承接本次没写的场——已有色彩脚本——留给收尾检查）。不合格按临时失败重试这一轮。
func CountScenePaletteTurn(text string, turn TurnContext) (int, error) {
	want, _ := strconv.Atoi(turn.Turn.ID)
	out, stats := ScenePaletteFromText(text, want)
	if len(out.Script) == 0 {
		return 0, fmt.Errorf("这一轮没有解析出任何一场的色彩脚本（%d 行读不出）", stats.Invalid)
	}
	prior, _ := ScenePaletteFromText(turn.Prior, 0)
	known := map[string]ColorScriptEntry{}
	for _, e := range prior.Script {
		known[e.Code()] = e
	}
	for _, e := range out.Script {
		if want > 0 && e.Episode != want {
			return 0, fmt.Errorf("这一轮应写第 %d 集，%s 写成了第 %d 集", want, e.Code(), e.Episode)
		}
		if e.SceneNo <= 0 {
			return 0, fmt.Errorf("有一场没写场编号（scene 要写 E%02dS01 这样的场编号）", want)
		}
		for _, item := range checkScenePaletteEntry(e, known, nil, false) {
			if item.Level == "error" {
				return 0, fmt.Errorf("%s：%s", item.ShotID, item.Message)
			}
		}
		known[e.Code()] = e
	}
	return len(out.Script), nil
}

// ---------------------------------------------------------------- 检查

// 场次色卡检查的规则编号。
const (
	RuleScenePaletteNoScript   = "R-SPAL-001"
	RuleScenePaletteFewColors  = "R-SPAL-002"
	RuleScenePaletteBadHex     = "R-SPAL-003"
	RuleScenePaletteInherit    = "R-SPAL-004"
	RuleScenePaletteSharedHex  = "R-SPAL-005"
	RuleScenePaletteScenes     = "R-SPAL-006"
	RuleScenePaletteNoShared   = "R-SPAL-007"
	RuleScenePaletteMasterHex  = "R-SPAL-008"
	RuleScenePaletteColorName  = "R-SPAL-009"
	RuleScenePaletteDuplicate  = "R-SPAL-010"
	RuleScenePaletteFieldType  = "R-SPAL-011"
	RuleScenePaletteAnchor     = "R-SPAL-012"
	RuleScenePaletteNoGroup    = "R-SPAL-013"
	RuleScenePaletteManyColors = "R-SPAL-014"
)

// ScenePaletteCheckContext 是检查场次色卡输出要用的输入：这次应写的各集场次、这次不重写的已有色彩脚本和项目的主基调色卡。
type ScenePaletteCheckContext struct {
	Episodes []ScenePaletteEpisode
	Existing []ColorScriptEntry
	Master   MasterPalette
}

// CheckScenePalette 检查解析出的色彩脚本：每场 ≥3 色（不超过合成图能画的色数）、HEX 合法、inherits_from 指向前面已有的场
// （本次前面写过的，或已有色彩脚本里的）、承接色 HEX 与承接场一致——这些是 error，不写库；场次对不上、承接却没写承接色、
// 与主色卡同名色 HEX 不一致、颜色缺英文名或分组、字段类型不符降级读出、多写了风格锚是 warn。
func CheckScenePalette(out ScenePaletteOutput, ctx ScenePaletteCheckContext) CheckReport {
	report := CheckReport{Kind: StepScenePalette, Items: []CheckItem{}, Skipped: []CheckSkip{}}
	add := func(item CheckItem) {
		report.Items = append(report.Items, item)
		if item.Level == "error" {
			report.Errors++
		} else {
			report.Warnings++
		}
	}
	if out.AnchorIgnored > 0 {
		add(CheckItem{Level: "warn", Rule: RuleScenePaletteAnchor, Message: "多写了风格锚：场次色卡不改风格锚（风格锚只由风格锁定产出），已忽略"})
	}
	if len(out.Script) == 0 {
		add(CheckItem{Level: "error", Rule: RuleScenePaletteNoScript, Message: "没有解析出色彩脚本：每场要单独写一行 JSON"})
		return report
	}
	known := map[string]ColorScriptEntry{}
	for _, e := range ctx.Existing {
		known[e.Code()] = e
	}
	master := NormalizeMasterPalette(ctx.Master)
	seen := map[string]bool{}
	for _, e := range out.Script {
		code := e.Code()
		if seen[code] {
			add(CheckItem{Level: "warn", Rule: RuleScenePaletteDuplicate, ShotID: code, Message: "同一场写了两次，只取第一次"})
			continue
		}
		seen[code] = true
		for _, note := range e.Coerced {
			add(CheckItem{Level: "warn", Rule: RuleScenePaletteFieldType, ShotID: code, Message: note})
		}
		for _, item := range checkScenePaletteEntry(e, known, &master, true) {
			add(item)
		}
		known[code] = e
	}
	expected := map[string]bool{}
	for _, ep := range ctx.Episodes {
		for _, sc := range ep.Scenes {
			code := SceneCode(ep.No, sc.No)
			expected[code] = true
			if !seen[code] {
				add(CheckItem{Level: "warn", Rule: RuleScenePaletteScenes, ShotID: code, Message: fmt.Sprintf("「%s」没有写色彩脚本，这一场没有色卡", sc.Heading)})
			}
		}
	}
	if len(expected) > 0 {
		for _, e := range out.Script {
			if !expected[e.Code()] {
				add(CheckItem{Level: "warn", Rule: RuleScenePaletteScenes, ShotID: e.Code(), Message: "剧集里没有这一场（场序超出或集号不在这次运行里），不写色卡"})
			}
		}
	}
	return report
}

// checkScenePaletteEntry 检查一场：色数、HEX、分组、承接、与主色卡同名色；strictInherit 为 false 时找不到承接场不报
// （当轮校验看不到已有色彩脚本）；master 为 nil 时不比主色卡。
func checkScenePaletteEntry(e ColorScriptEntry, known map[string]ColorScriptEntry, master *MasterPalette, strictInherit bool) []CheckItem {
	code := e.Code()
	var items []CheckItem
	add := func(level, rule, message string) {
		items = append(items, CheckItem{Level: level, Rule: rule, ShotID: code, Message: message})
	}
	colors := e.Colors()
	if len(colors) < ScenePaletteMinColors {
		add("error", RuleScenePaletteFewColors, fmt.Sprintf("只有 %d 个颜色，每场至少 %d 个", len(colors), ScenePaletteMinColors))
	}
	if len(colors) > ScenePaletteMaxColors {
		add("error", RuleScenePaletteManyColors, fmt.Sprintf("写了 %d 个颜色，色卡上最多画 %d 个", len(colors), ScenePaletteMaxColors))
	}
	var masterColors []PaletteColor
	if master != nil {
		masterColors = master.Colors()
	}
	for _, c := range colors {
		if !ValidHexColor(c.Hex) {
			add("error", RuleScenePaletteBadHex, fmt.Sprintf("「%s」的 HEX「%s」不是 #RRGGBB", c.NameZh, c.Hex))
		}
		if c.NameEn == "" {
			add("warn", RuleScenePaletteColorName, fmt.Sprintf("「%s」没写英文名，色卡上只写中文名和 HEX", c.NameZh))
		}
		if c.Group == "" {
			add("warn", RuleScenePaletteNoGroup, fmt.Sprintf("「%s」没写 group，色卡上归到「其他」", firstNonBlank(c.NameZh, c.NameEn, c.Hex)))
		}
		if c.NameZh != "" {
			if i := slices.IndexFunc(masterColors, func(m PaletteColor) bool { return m.NameZh == c.NameZh }); i >= 0 && masterColors[i].Hex != c.Hex {
				add("warn", RuleScenePaletteMasterHex, fmt.Sprintf("「%s」写成了 %s，主基调色卡里同名色是 %s", c.NameZh, c.Hex, masterColors[i].Hex))
			}
		}
	}
	if e.InheritsFrom == "" {
		return items
	}
	if e.InheritsFrom == code {
		add("error", RuleScenePaletteInherit, "inherits_from 写成了自己")
		return items
	}
	parent, ok := known[e.InheritsFrom]
	if !ok {
		if strictInherit {
			add("error", RuleScenePaletteInherit, fmt.Sprintf("inherits_from「%s」不是前面已有的场", e.InheritsFrom))
		}
		return items
	}
	if len(e.SharedColors) == 0 {
		add("warn", RuleScenePaletteNoShared, fmt.Sprintf("承接 %s 却没写承接色", e.InheritsFrom))
	}
	parentColors := parent.Colors()
	for _, c := range e.SharedColors {
		if slices.ContainsFunc(parentColors, func(p PaletteColor) bool { return p.Hex == c.Hex }) {
			continue
		}
		message := fmt.Sprintf("承接色「%s」%s 在承接场 %s 里没有", c.NameZh, c.Hex, e.InheritsFrom)
		if i := slices.IndexFunc(parentColors, func(p PaletteColor) bool { return p.NameZh != "" && p.NameZh == c.NameZh }); i >= 0 {
			message = fmt.Sprintf("承接色「%s」写成了 %s，承接场 %s 里是 %s", c.NameZh, c.Hex, e.InheritsFrom, parentColors[i].Hex)
		}
		add("error", RuleScenePaletteSharedHex, message)
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

// PaletteHeading 是合成色卡图上的标题：「E01S02 · 内·电梯口·夜」。
func PaletteHeading(e ColorScriptEntry) string {
	if e.Scene == "" {
		return e.Code()
	}
	return e.Code() + " · " + e.Scene
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

// ScenePaletteGroups 把一场的颜色按主色卡的分组排成色板（合成图按组一行）；没写分组的归「其他」。
// 承接色排在本场新增色前面（组内保持输出顺序）。
func ScenePaletteGroups(e ColorScriptEntry) MasterPalette {
	var groups []MasterPaletteGroup
	for _, c := range e.Colors() {
		key := c.Group
		if key == "" {
			key = PaletteGroupOther
		}
		name := ""
		if key == PaletteGroupOther {
			name = "其他"
		}
		groups = append(groups, MasterPaletteGroup{Key: key, Name: name, Colors: []PaletteColor{c}})
	}
	return NormalizeMasterPalette(MasterPalette{Groups: groups})
}

// ScenePaletteColorsLine 是视频参考图名额不够、不带色卡图时写进视频提示词的一行：「本场色值：冷白荧光 #E8F0F2、…」。
func ScenePaletteColorsLine(colors []PaletteColor) string {
	parts := make([]string, 0, len(colors))
	for _, c := range colors {
		if !ValidHexColor(c.Hex) {
			continue
		}
		parts = append(parts, strings.TrimSpace(firstNonBlank(c.NameZh, c.NameEn)+" "+c.Hex))
	}
	if len(parts) == 0 {
		return ""
	}
	return "本场色值：" + strings.Join(parts, "、") + "。"
}
