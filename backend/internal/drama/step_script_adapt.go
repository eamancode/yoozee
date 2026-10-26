package drama

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// script.adapt（剧本优化，M7.2 第 3 阶段）：所选原文章节 → 若干集短剧剧本。提示词 = 技能上下文（用户任务里是标了段号的原文）
// + 项目需求 + 分集要求 + 按 schema 生成的契约；一个任务里一轮写一集，模型一集一块输出（一行 JSON 头 + 正文），
// 边流边解析。正文的场次头、动作、台词与 ScriptFromText 同一套规则，写进 episode 单元后生成分段直接读它。

// 剧本优化的两种方式。
const (
	// ScriptAdaptModeAdapt 是小说转剧本：原文章节（kind=chapter）改编成新的剧集。
	ScriptAdaptModeAdapt = "adapt"
	// ScriptAdaptModeRhythm 是只调节奏：已有剧集（kind=episode）原地改写，集数不变。
	ScriptAdaptModeRhythm = "rhythm"
)

const (
	// ScriptAdaptMaxSourceChars 是一次运行所选正文的总字数上限（JS 字符串长度）；超过请分几次跑。
	ScriptAdaptMaxSourceChars = 60_000
	// ScriptAdaptMaxEpisodes 是一次运行最多写几集。
	ScriptAdaptMaxEpisodes = 40
	// ScriptAdaptSourceCharsPerSecond 是按每集秒数估集数时，一秒成片大约对应多少字原文（小说改编时删减、压缩后的经验值）。
	ScriptAdaptSourceCharsPerSecond = 18
	stepBriefPromptChars            = 1500
)

// ScriptAdaptSource 是一段输入：原文章节（adapt）或已有剧集（rhythm）。Text 是编辑器 getText 的纯文本。
type ScriptAdaptSource struct {
	UnitID string
	Title  string
	Text   string
}

// ScriptAdaptInput 是组装剧本优化提示词的全部输入。
type ScriptAdaptInput struct {
	Mode    string
	Sources []ScriptAdaptSource
	// Episodes 是要写几集（已按请求、每集秒数或章节数算好）。
	Episodes       int
	EpisodeSeconds float64
	SegmentSeconds float64
	// BriefJSON 是需求文档原文（宽松读取，读不出的字段不写）。
	BriefJSON string
	Skill     StepSkill
}

// StepPrompt 是一步组装出的提示词和注入的技能文件。
type StepPrompt struct {
	Text  string
	Files []StepSkillFileUse
}

// ScriptAdaptParagraphs 把纯文本按行切成段（导入的小说一段一行，编辑器里段与段之间空一行），去掉空行。段号从 1 数。
func ScriptAdaptParagraphs(text string) []string {
	var out []string
	for _, line := range lineBreakPattern.Split(text, -1) {
		if line = jsTrim(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// ScriptAdaptEpisodeCount 是要写几集：请求给了集数就用它；否则按每集秒数和原文字数估；都没有就一章一集。
func ScriptAdaptEpisodeCount(mode string, sources []ScriptAdaptSource, target int, episodeSeconds float64) int {
	if mode == ScriptAdaptModeRhythm {
		return len(sources)
	}
	n := target
	if n <= 0 && episodeSeconds > 0 {
		chars := 0
		for _, s := range sources {
			chars += jsLen(s.Text)
		}
		n = int(math.Round(float64(chars) / (episodeSeconds * ScriptAdaptSourceCharsPerSecond)))
	}
	if n <= 0 {
		n = len(sources)
	}
	return min(max(n, 1), ScriptAdaptMaxEpisodes)
}

// ScriptAdaptTurns 是逐集续写的轮次：一轮一集，ID 是集号；调节奏时 Heading 是原剧集标题。
func ScriptAdaptTurns(mode string, sources []ScriptAdaptSource, episodes int) []ShotsScene {
	out := make([]ShotsScene, 0, episodes)
	for k := 1; k <= episodes; k++ {
		turn := ShotsScene{ID: strconv.Itoa(k), Heading: fmt.Sprintf("第 %d 集", k)}
		if mode == ScriptAdaptModeRhythm && k <= len(sources) {
			turn.Heading = sources[k-1].Title
		}
		out = append(out, turn)
	}
	return out
}

// ScriptAdaptTask 是交给技能的【用户任务】：改编方式和标了编号、段号的正文。
func ScriptAdaptTask(mode string, sources []ScriptAdaptSource, episodes int) string {
	var b strings.Builder
	if mode == ScriptAdaptModeRhythm {
		fmt.Fprintf(&b, "下面 %d 集已经是短剧剧本，这次只调节奏：人物、称呼、事件顺序、关键道具和台词的事实与立场不变，删掉拖沓重复的动作和信息，让冲突尽早出现、每集结尾停在具体的画面或台词上；场次头、动作、台词按【受保护输出契约】的格式改写。集数不变，第 k 集改写下面的【剧集 k】，JSON 头的 sources 写空数组 []。", len(sources))
		for i, s := range sources {
			fmt.Fprintf(&b, "\n\n【剧集 %d】%s（unitId: %s）\n%s", i+1, s.Title, s.UnitID, jsTrim(s.Text))
		}
		return b.String()
	}
	fmt.Fprintf(&b, "请把下面 %d 章原文改编成 %d 集短剧剧本。原文按章给出，每章标了编号（unitId）和段号，JSON 头的 sources 按它们写。", len(sources), episodes)
	for i, s := range sources {
		paragraphs := ScriptAdaptParagraphs(s.Text)
		fmt.Fprintf(&b, "\n\n【原文章节 %d】%s（unitId: %s，共 %d 段）", i+1, s.Title, s.UnitID, len(paragraphs))
		for j, p := range paragraphs {
			fmt.Fprintf(&b, "\n[%d] %s", j+1, p)
		}
	}
	return b.String()
}

// BuildScriptAdaptPrompt 组装剧本优化的提示词。
func BuildScriptAdaptPrompt(in ScriptAdaptInput) (StepPrompt, error) {
	step, _ := StepByID(StepScriptAdapt)
	contract, err := BuiltinContract(step.Contract)
	if err != nil {
		return StepPrompt{}, err
	}
	skillPrompt, files, ok := StepSkillPrompt(in.Skill, ScriptAdaptTask(in.Mode, in.Sources, in.Episodes), step.SkillBudgetChars, step.SkillLinkedFiles)
	if !ok {
		return StepPrompt{}, fmt.Errorf("技能 %s 当前版本没有 SKILL.md", in.Skill.ID)
	}
	text := joinTruthy([]string{
		skillPrompt,
		StepBriefPrompt(in.BriefJSON),
		ScriptAdaptPlanPrompt(in.Mode, in.Episodes, in.EpisodeSeconds, in.SegmentSeconds),
		contract.Text(contract.DefaultRequirement(), ContractTextOptions{SkillShaped: true}),
	}, "\n\n")
	return StepPrompt{Text: text, Files: files}, nil
}

// ScriptAdaptPlanPrompt 是【分集要求】段：写几集、每集多长、一段视频最长多少秒。
func ScriptAdaptPlanPrompt(mode string, episodes int, episodeSeconds float64, segmentSeconds float64) string {
	lines := []string{"【分集要求】"}
	if mode == ScriptAdaptModeRhythm {
		lines = append(lines, fmt.Sprintf("- 本次共 %d 集，与上面的剧集一一对应，集号、集数和每集的主要情节不变。", episodes))
	} else {
		lines = append(lines, fmt.Sprintf("- 本次共写 %d 集：按原文顺序把情节分到各集，不重复、不遗漏推动剧情的内容；一章可以拆成几集，几章也可以合成一集。", episodes))
	}
	if episodeSeconds > 0 {
		lines = append(lines, fmt.Sprintf("- 每集成片目标约 %d 秒：台词按每秒 4～5 个字估，动作和停顿另算，写多了就删不推动剧情的戏，不靠压台词凑时长。", int(math.Round(episodeSeconds))))
	}
	if segmentSeconds > 0 {
		lines = append(lines, fmt.Sprintf("- 一段视频最长 %d 秒，后面按场切段：一场戏里一口气说完的台词不要超过这个长度，长对话中间留动作或反应。", int(math.Round(segmentSeconds))))
	}
	return strings.Join(lines, "\n")
}

// StepBriefPrompt 是【项目需求】段：需求文档里写给编剧看的事实，按 1500 字截断；需求文档为空或读不出时不写。
func StepBriefPrompt(briefJSON string) string {
	return stepBriefPrompt(briefJSON, "【项目需求】改编时遵守下面的需求，与原文冲突时以原文事实为准。")
}

// stepBriefPrompt 是带指定引导句的【项目需求】段。
func stepBriefPrompt(briefJSON string, intro string) string {
	var brief Brief
	if raw := strings.TrimSpace(briefJSON); raw == "" || json.Unmarshal([]byte(raw), &brief) != nil {
		return ""
	}
	var lines []string
	add := func(label, value string) {
		if value = jsCompact(value); value != "" {
			lines = append(lines, "- "+label+"："+value)
		}
	}
	fact := func(label string, f *BriefFact) {
		if f != nil {
			add(label, f.Value)
		}
	}
	add("片名", brief.Title)
	fact("用途", brief.Purpose)
	fact("平台与画幅", brief.Platform)
	fact("片长", brief.Duration)
	fact("口味", brief.Taste)
	if s := brief.EpisodeSeconds; s != nil && *s > 0 {
		add("每集时长", fmt.Sprintf("%d 秒", int(math.Round(*s))))
	}
	if s := brief.SegmentSeconds; s != nil && *s > 0 {
		add("单段最长", fmt.Sprintf("%d 秒", *s))
	}
	for _, ep := range brief.Episodes {
		if ep.HookRequirement != "" {
			add(fmt.Sprintf("第 %d 集钩子", ep.No), ep.HookRequirement)
		}
	}
	for _, c := range brief.Constraints {
		add("约束", c)
	}
	for _, c := range brief.NotDoing {
		add("不做", c)
	}
	if len(lines) == 0 {
		return ""
	}
	out := intro
	remaining := stepBriefPromptChars
	for _, line := range lines {
		n := jsLen(line)
		if n > remaining {
			break
		}
		out += "\n" + line
		remaining -= n
	}
	return out
}

// ScriptAdaptTurnPlan 是第一轮接在提示词后面的分回合说明。
func ScriptAdaptTurnPlan(turns []ShotsScene) string {
	n := len(turns)
	first := "本回合只写第 1 集（JSON 头加正文）"
	if turns[0].Heading != "" && turns[0].Heading != "第 1 集" {
		first += "，对应【剧集 1】「" + turns[0].Heading + "」"
	}
	return fmt.Sprintf("【分回合输出】本次共 %d 集，这个对话分 %d 个回合写完：%s；我说「继续」时再写下一集。每个回合只写一集，JSON 头的 no 写这一回合的集号；情节按原文顺序接着上一集往下写，不重复前面已经写过的内容。", n, n, first)
}

// ScriptAdaptTurnPrompt 是第 k 集（从 1 数，k ≥ 2）的续写指令。
func ScriptAdaptTurnPrompt(turns []ShotsScene, k int) string {
	which := ""
	if t := turns[k-1]; t.Heading != "" && t.Heading != fmt.Sprintf("第 %d 集", k) {
		which = "，对应【剧集 " + strconv.Itoa(k) + "】「" + t.Heading + "」"
	}
	return fmt.Sprintf("继续写第 %d 集%s：JSON 头的 no 写 %d，情节接着第 %d 集结尾往下。只输出这一集（JSON 头加正文），不要重复前面已经写过的集。", k, which, k, k-1)
}

// AdaptSource 是一集的一个来源：原文章节的第 From 到第 To 段（含首尾）。
type AdaptSource struct {
	UnitID string `json:"unitId"`
	From   int    `json:"from"`
	To     int    `json:"to"`
}

// AdaptScene 是一集里的一场（场次头原文，去掉「## 」）。
type AdaptScene struct {
	Heading string `json:"heading"`
}

// AdaptEpisode 是解析出的一集。
type AdaptEpisode struct {
	No      int           `json:"no"`
	Title   string        `json:"title"`
	Sources []AdaptSource `json:"sources"`
	Scenes  []AdaptScene  `json:"scenes"`
	// Text 是规整后的正文（逐行，场次头以「## 」开头）。
	Text string `json:"text"`
	// Header 表示写了 JSON 头（没写头的按整段正文算一集）。
	Header bool `json:"-"`
}

// AdaptParseStats 是剧本优化输出的解析统计：Blocks 是读到的集（有 JSON 头或正文），Episodes 是其中带场次头的。
type AdaptParseStats struct {
	Blocks   int     `json:"blocks"`
	Episodes int     `json:"episodes"`
	Rate     float64 `json:"rate"`
}

var (
	adaptBoldSpeaker  = regexp.MustCompile(`^[*_]{2}([^*_：:]{1,8})[*_]{2}\s*([（(：:])`)
	adaptSkillScene   = regexp.MustCompile(`^【场景\s*[一二三四五六七八九十\p{Nd}]*】\s*(.+)$`)
	adaptSkillTagLine = regexp.MustCompile(`^【(画面|结尾画面)】\s*(.+)$`)
	adaptSkillHook    = regexp.MustCompile(`^【结尾钩子】\s*(.+)$`)
	adaptEpisodeTitle = regexp.MustCompile(`^第\s*[一二三四五六七八九十百零〇\p{Nd}]+\s*集\s*[：:]?\s*(.*)$`)
	adaptSceneSep     = regexp.MustCompile(`\s*[｜|]\s*`)
)

// normalizeAdaptLine 把常见的偏差写法规整成契约格式：技能原生的「【场景1】地点｜内｜夜」改成「## 内·地点·夜」，
// 「【画面】…」改成「（…）」，人物名外面的 ** 去掉。其余原样。
func normalizeAdaptLine(line string) string {
	if m := adaptSkillScene.FindStringSubmatch(line); m != nil {
		parts := adaptSceneSep.Split(strings.TrimSpace(m[1]), -1)
		if len(parts) == 3 {
			return "## " + parts[1] + "·" + parts[0] + "·" + parts[2]
		}
		return "## " + strings.Join(parts, "·")
	}
	if m := adaptSkillTagLine.FindStringSubmatch(line); m != nil {
		return "（" + strings.Trim(m[2], "（）()") + "）"
	}
	if m := adaptSkillHook.FindStringSubmatch(line); m != nil {
		return "（结尾钩子：" + strings.Trim(m[1], "（）()") + "）"
	}
	return adaptBoldSpeaker.ReplaceAllString(line, "$1$2")
}

// isSceneHeadingLine 是 ScriptFromText 认作场次头的行。
func isSceneHeadingLine(line string) bool {
	return sceneHeadingPrefix.MatchString(line) || strings.HasPrefix(line, "## ")
}

// adaptHeader 读一行 JSON 头：要是对象，带 no 或 title；回合标记不算。
func adaptHeader(line string) (map[string]any, bool) {
	if !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var obj map[string]any
	if json.Unmarshal([]byte(strings.TrimSuffix(line, ",")), &obj) != nil {
		return nil, false
	}
	if _, marker := obj[ShotLinesMarkerKey]; marker {
		return obj, true
	}
	_, hasNo := obj["no"]
	_, hasTitle := obj["title"]
	if !hasNo && !hasTitle {
		return nil, false
	}
	return obj, true
}

// ScriptAdaptEpisodesFromText 解析剧本优化的模型输出：一行 JSON 头开始一集，下面的行是正文，直到下一个头；
// 回合标记、代码块围栏、头之前的说明文字跳过。整段没有 JSON 头但有场次头时按一集算（Header=false）。
func ScriptAdaptEpisodesFromText(text string) ([]AdaptEpisode, AdaptParseStats) {
	var episodes []AdaptEpisode
	var body []string
	var cur *AdaptEpisode
	var loose []string
	flush := func() {
		if cur == nil {
			return
		}
		finishAdaptEpisode(cur, body)
		episodes = append(episodes, *cur)
		cur, body = nil, nil
	}
	for _, raw := range strings.Split(text, "\n") {
		line := jsTrim(raw)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		if obj, ok := adaptHeader(line); ok {
			if _, marker := obj[ShotLinesMarkerKey]; marker {
				continue
			}
			flush()
			cur = &AdaptEpisode{Header: true, No: jsonInt(obj["no"]), Title: jsTrim(fmt.Sprint(valueOr(obj["title"], "")))}
			cur.Sources = adaptSources(obj["sources"])
			continue
		}
		if cur == nil {
			loose = append(loose, line)
			continue
		}
		body = append(body, line)
	}
	flush()
	if len(episodes) == 0 && len(loose) > 0 {
		ep := AdaptEpisode{}
		finishAdaptEpisode(&ep, loose)
		if len(ep.Scenes) > 0 {
			episodes = append(episodes, ep)
		}
	}
	stats := AdaptParseStats{Blocks: len(episodes)}
	for i := range episodes {
		if episodes[i].No <= 0 {
			episodes[i].No = i + 1
		}
		if len(episodes[i].Scenes) > 0 {
			stats.Episodes++
		}
	}
	if stats.Blocks > 0 {
		stats.Rate = float64(stats.Episodes) / float64(stats.Blocks)
	}
	return episodes, stats
}

// finishAdaptEpisode 规整正文：逐行规整写法，场次头之前的「第 N 集：标题」行当标题（头里没写时）并去掉。
func finishAdaptEpisode(ep *AdaptEpisode, lines []string) {
	var out []string
	for _, line := range lines {
		line = normalizeAdaptLine(line)
		if len(ep.Scenes) == 0 {
			if m := adaptEpisodeTitle.FindStringSubmatch(line); m != nil {
				if ep.Title == "" {
					ep.Title = jsTrim(m[1])
				}
				continue
			}
		}
		if isSceneHeadingLine(line) {
			ep.Scenes = append(ep.Scenes, AdaptScene{Heading: jsTrim(strings.TrimLeft(line, "# "))})
		}
		out = append(out, line)
	}
	if ep.Scenes == nil {
		ep.Scenes = []AdaptScene{}
	}
	if ep.Sources == nil {
		ep.Sources = []AdaptSource{}
	}
	ep.Text = strings.Join(out, "\n")
}

func adaptSources(v any) []AdaptSource {
	items, _ := v.([]any)
	out := []AdaptSource{}
	for _, item := range items {
		obj, _ := item.(map[string]any)
		if obj == nil {
			continue
		}
		id := jsTrim(fmt.Sprint(valueOr(obj["unitId"], "")))
		if id == "" {
			continue
		}
		out = append(out, AdaptSource{UnitID: id, From: jsonInt(obj["from"]), To: jsonInt(obj["to"])})
	}
	return out
}

func valueOr(v any, fallback any) any {
	if v == nil {
		return fallback
	}
	return v
}

// jsonInt 读 JSON 数字或数字字符串；读不出返回 0。
func jsonInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	}
	return 0
}

// CountScriptAdaptTurn 校验一轮（一集）的输出：恰好一集，带场次头；sources 不为空时（改编）来源要在所选章节里。
func CountScriptAdaptTurn(text string, sources []string) (int, error) {
	episodes, _ := ScriptAdaptEpisodesFromText(text)
	if len(episodes) != 1 {
		return 0, fmt.Errorf("这一轮应写一集，解析出 %d 集", len(episodes))
	}
	ep := episodes[0]
	if len(ep.Scenes) == 0 {
		return 0, fmt.Errorf("这一集没有以「## 」开头的场次头")
	}
	if len(sources) > 0 {
		if len(ep.Sources) == 0 {
			return 0, fmt.Errorf("这一集的 JSON 头没写 sources")
		}
		for _, s := range ep.Sources {
			if !slices.Contains(sources, s.UnitID) {
				return 0, fmt.Errorf("sources 里的 %s 不是所选的原文章节", s.UnitID)
			}
		}
	}
	return 1, nil
}

// ScriptAdaptCheckContext 是检查剧本优化输出要用的输入。
type ScriptAdaptCheckContext struct {
	Mode string
	// Paragraphs 是所选原文章节 → 段数（改编时校验 sources）。
	Paragraphs map[string]int
	// Episodes 是要写的集数。
	Episodes int
}

// 剧本优化检查的规则编号。
const (
	RuleAdaptNoEpisodes  = "R-ADAPT-001"
	RuleAdaptNoScene     = "R-ADAPT-002"
	RuleAdaptNoSources   = "R-ADAPT-003"
	RuleAdaptUnknownUnit = "R-ADAPT-004"
	RuleAdaptRange       = "R-ADAPT-005"
	RuleAdaptCount       = "R-ADAPT-006"
	RuleAdaptLine        = "R-ADAPT-007"
	RuleAdaptNoDialogue  = "R-ADAPT-008"
)

// adaptLineLabelRunes 是判断「这一行像台词」时看行首多少个字里有没有冒号。
const adaptLineLabelRunes = 12

// CheckScriptAdapt 检查解析出的剧集：每集要有场次头；改编时 sources 不能空、要在所选章节里，段号越界的提醒；
// 像台词却读不出人物的行（后面的台词表、生成分段读不到）逐行提醒。error 级问题不写库。
func CheckScriptAdapt(episodes []AdaptEpisode, ctx ScriptAdaptCheckContext) CheckReport {
	report := CheckReport{Kind: StepScriptAdapt, Items: []CheckItem{}, Skipped: []CheckSkip{}}
	add := func(level, rule, where, message string) {
		report.Items = append(report.Items, CheckItem{Level: level, Rule: rule, ShotID: where, Message: message})
		if level == "error" {
			report.Errors++
		} else {
			report.Warnings++
		}
	}
	if len(episodes) == 0 {
		add("error", RuleAdaptNoEpisodes, "", "没有解析出剧集：每集要先写一行 JSON 头，再写正文")
		return report
	}
	if ctx.Episodes > 0 && len(episodes) != ctx.Episodes {
		add("warn", RuleAdaptCount, "", fmt.Sprintf("要写 %d 集，解析出 %d 集", ctx.Episodes, len(episodes)))
	}
	for _, ep := range episodes {
		where := fmt.Sprintf("第 %d 集", ep.No)
		if len(ep.Scenes) == 0 {
			add("error", RuleAdaptNoScene, where, "没有场次头（以「## 」开头的「内·地点·夜」行），生成分段切不出场")
		}
		if ctx.Mode != ScriptAdaptModeRhythm {
			if len(ep.Sources) == 0 {
				add("error", RuleAdaptNoSources, where, "JSON 头没写 sources，不知道改编自哪几章")
			}
			for _, s := range ep.Sources {
				total, ok := ctx.Paragraphs[s.UnitID]
				switch {
				case !ok:
					add("error", RuleAdaptUnknownUnit, where, fmt.Sprintf("sources 里的 %s 不是所选的原文章节", s.UnitID))
				case s.From < 1 || s.To < s.From || s.To > total:
					add("warn", RuleAdaptRange, where, fmt.Sprintf("sources 的段号 %d-%d 不在该章的 1-%d 段内", s.From, s.To, total))
				}
			}
		}
		lines := 0
		for _, line := range strings.Split(ep.Text, "\n") {
			if isSceneHeadingLine(line) {
				continue
			}
			if speaker, ok := ScriptLineSpeaker(line); ok {
				if strings.ContainsAny(speaker, "【】[]*#\"“”「」《》") {
					add("warn", RuleAdaptLine, where, fmt.Sprintf("「%s」的人物名带了符号，台词表会按「%s」记人物", jsTruncate(line, 40), speaker))
				}
				lines++
				continue
			}
			if looksLikeDialogue(line) {
				add("warn", RuleAdaptLine, where, fmt.Sprintf("「%s」像台词，但读不出「人物：台词」，台词表会把它当动作", jsTruncate(line, 40)))
			}
		}
		if lines == 0 && len(ep.Scenes) > 0 {
			add("warn", RuleAdaptNoDialogue, where, "这一集一句台词也没有")
		}
	}
	return report
}

// ScriptLineSpeaker 按台词表的规则（ScriptFromText）读一行台词的人物；不是台词返回 false。
func ScriptLineSpeaker(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "（") || strings.HasPrefix(line, "(") {
		return "", false
	}
	m := scriptLinePattern.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// looksLikeDialogue 是行首十来个字里有冒号、又不在括号里的行（多半是人物名太长或带了符号的台词）。
func looksLikeDialogue(line string) bool {
	if strings.HasPrefix(line, "（") || strings.HasPrefix(line, "(") {
		return false
	}
	head := line
	if utf8.RuneCountInString(head) > adaptLineLabelRunes {
		head = string([]rune(head)[:adaptLineLabelRunes])
	}
	return strings.ContainsAny(head, "：:")
}

// MainAdaptSource 是占比最大的来源章节（按段数合计；一样多取先写的）。
func MainAdaptSource(sources []AdaptSource) string {
	best, bestN := "", -1
	count := map[string]int{}
	var order []string
	for _, s := range sources {
		if _, seen := count[s.UnitID]; !seen {
			order = append(order, s.UnitID)
		}
		count[s.UnitID] += max(1, s.To-s.From+1)
	}
	for _, id := range order {
		if count[id] > bestN {
			best, bestN = id, count[id]
		}
	}
	return best
}

// ---------------------------------------------------------------- 剧集正文的存法

// episodeSourcesComment 是剧集正文开头的来源注释：<!-- sources: unitId:from-to;… -->。编辑器（TipTap）不显示注释，
// 纯文本转换也会去掉它；这是来源唯一可回溯的位置（不加表不加列），前端保存时原样带回。
var episodeSourcesComment = regexp.MustCompile(`^\s*<!--\s*sources:([^>]*?)-->`)

// EpisodeSourceText 把一集写成 episode 单元的 SourceText：来源注释 + 每行一个段落（HTML 转义）。sources 为空时不写注释。
func EpisodeSourceText(sources []AdaptSource, text string) string {
	var b strings.Builder
	b.WriteString(EpisodeSourcesComment(sources))
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			b.WriteString("<p>" + html.EscapeString(line) + "</p>")
		}
	}
	return b.String()
}

// EpisodeSourcesComment 是来源注释行；sources 为空返回空串。
func EpisodeSourcesComment(sources []AdaptSource) string {
	if len(sources) == 0 {
		return ""
	}
	parts := make([]string, 0, len(sources))
	for _, s := range sources {
		parts = append(parts, fmt.Sprintf("%s:%d-%d", s.UnitID, s.From, s.To))
	}
	return "<!-- sources: " + strings.Join(parts, ";") + " -->"
}

// EpisodeSources 读剧集正文开头的来源注释；没有返回 nil。
func EpisodeSources(sourceText string) []AdaptSource {
	m := episodeSourcesComment.FindStringSubmatch(sourceText)
	if m == nil {
		return nil
	}
	var out []AdaptSource
	for _, part := range strings.Split(m[1], ";") {
		part = strings.TrimSpace(part)
		id, span, ok := strings.Cut(part, ":")
		if !ok || strings.TrimSpace(id) == "" {
			continue
		}
		from, to, _ := strings.Cut(span, "-")
		out = append(out, AdaptSource{UnitID: strings.TrimSpace(id), From: jsonInt(from), To: jsonInt(to)})
	}
	return out
}

// WithEpisodeSources 换掉（或去掉）正文开头的来源注释，正文其余部分不动。
func WithEpisodeSources(sourceText string, sources []AdaptSource) string {
	rest := episodeSourcesComment.ReplaceAllString(sourceText, "")
	return EpisodeSourcesComment(sources) + strings.TrimLeft(rest, " \t\r\n")
}
