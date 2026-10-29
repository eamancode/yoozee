package drama

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// 按小节生成（M8 卡 F / 卡 E）的提示词：一个戏类一条对话（lane）。每条对话的第一轮 = 这一戏类技能的上下文（SKILL.md + 必读卡）+
// 项目记忆 + 【分节生成说明】+ 这条对话的第一节；之后每一轮只发这一节（同一对话续写，技能和项目记忆在历史里）。一章全是默认文戏时只有一条对话；
// 用户把某几节改成武戏后，武戏节另起一条对话，用武戏技能写。每一节写本节剧本范围原文、本节台词表、本节人物与资产和输出要求；
// 输出格式交给技能原文（7.3 的片段、即梦的小节、v7 的每镜标准格式），平台只规定小节边界，不再拼 JSON Lines 契约。

// SectionShotsInput 是组装按小节生成提示词的全部输入。
type SectionShotsInput struct {
	ChapterShotsInput
	// Skills 是计划里每个戏类绑定的技能（戏类 → 技能）；ChapterShotsInput.Skill 不用。
	Skills map[string]StepSkill
	Plan   SectionPlan
	// ModelLabel 是项目视频模型（提示里说明一节的上限从哪来）；AspectRatio 是项目画幅（武戏参数表用，可为空）。
	ModelLabel    string
	AspectRatio   string
	MasterPalette *MasterPalette
	// Palettes 是各场的【本场色卡】段（单元内场编号 → 文字），本场第一节带上；没有的场不写。
	Palettes map[string]string
	// Casts 是各场换上的角色子卡（M8 卡 D，单元内场编号 → 主卡名 → 子卡名「名·变体」）；每一节只带本节所在场的。
	Casts map[string]map[string]string
}

// SectionShotsPrompt 是组装好的按小节生成提示词：StepPrompt 是第 1 节所在戏类那条对话的第一轮（不含第 1 节），Lanes 是其余戏类各自
// 那条对话的第一轮（戏类 → 提示词，第一次写到这个戏类的节时接在本轮指令前面）；Turns 是每一节的本轮指令。
type SectionShotsPrompt struct {
	StepPrompt
	Lanes map[string]StepPrompt
	Turns []string
}

// SectionShotsTask 是交给技能的【用户任务】（genre 是这条对话的戏类）。
func SectionShotsTask(title, sourceText string, plan SectionPlan, genre ShotGenre) string {
	n, mine := len(plan.Sections), 0
	for _, s := range plan.Sections {
		if s.Genre == genre.ID {
			mine++
		}
	}
	scope := fmt.Sprintf("平台已把本章按%s的口径切成 %d 个小节（一个小节 = 一次视频生成），见【分节生成说明】。", genre.Label, n)
	if mine != n {
		scope = fmt.Sprintf("平台已把本章切成 %d 个小节（一个小节 = 一次视频生成），其中 %d 节是%s，由这个对话按本技能写，见【分节生成说明】。", n, mine, genre.Label)
	}
	return strings.Join([]string{
		"章节：" + title,
		sourceText,
		scope + "上面的正文只供理解前后剧情；每一轮只写平台点名的那一个小节，按技能的原生分镜格式输出，保持剧情因果、人物关系、关键动作与台词完整。",
	}, "\n\n")
}

// BuildSectionShotsPrompt 组装按小节生成的提示词；某个戏类没给技能、技能缺 SKILL.md 或计划里没有小节时返回错误。
func BuildSectionShotsPrompt(in SectionShotsInput) (SectionShotsPrompt, error) {
	if len(in.Plan.Sections) == 0 {
		return SectionShotsPrompt{}, fmt.Errorf("章节正文切不出小节")
	}
	memory, truncated := fitPromptBudget([]promptSection{
		{"episodeSeconds", StepEpisodeDurationPrompt(in.EpisodeSeconds)},
		{"anchor", styleAnchorPrompt(in.Anchor, "【全局风格锚】项目锁定的全片取向，优先于技能自带的画风假设：画面、光影、镜头语言和调色都按它写，禁用项一律不出现：", true)},
		{"masterPalette", sectionMasterPalettePrompt(in.MasterPalette)},
		{"characters", sectionCharacterPrompt(in.Characters, in.SourceText)},
		{"projectStyle", stepStylePrompt(in.Style, "分镜里的画面、光影、服饰和场景都按这个画风写；技能里写的默认画风（真人写实、3D 动漫、CG 仙侠等）以这里为准。这是项目级基线，不要整段抄进每个镜头。")},
		{"assets", sectionAssetPrompt(in.Assets)},
	}, ChapterShotsMemoryBudget)
	out := SectionShotsPrompt{Lanes: map[string]StepPrompt{}}
	for i, g := range in.Plan.Genres() {
		step, ok := StepByID(g.Step)
		if !ok {
			return SectionShotsPrompt{}, fmt.Errorf("戏类 %s 没有步骤", g.ID)
		}
		skill, ok := in.Skills[g.ID]
		if !ok {
			return SectionShotsPrompt{}, fmt.Errorf("戏类%s没有绑定技能", g.Label)
		}
		skillPrompt, files, ok := StepSkillPrompt(skill, SectionShotsTask(in.ChapterTitle, in.SourceText, in.Plan, g), step.SkillBudgetChars, step.SkillLinkedFiles)
		if !ok {
			return SectionShotsPrompt{}, fmt.Errorf("技能 %s 当前版本没有 SKILL.md", skill.ID)
		}
		text := joinTruthy([]string{
			skillPrompt,
			memory["projectStyle"],
			memory["anchor"],
			memory["masterPalette"],
			memory["characters"],
			memory["episodeSeconds"],
			memory["assets"],
			SectionPlanPrompt(in.Plan, g, in.ModelLabel),
		}, "\n\n")
		lane := StepPrompt{Text: text, Files: files, Truncated: truncated}
		if i == 0 {
			out.StepPrompt = lane
		} else {
			out.Lanes[g.ID] = lane
		}
	}
	for k, sec := range in.Plan.Sections {
		palette := ""
		if sec.First {
			palette = in.Palettes[sec.Scene]
		}
		out.Turns = append(out.Turns, SectionTurnPrompt(in.Plan, k+1, palette, in.Assets, in.Casts[sec.Scene], in.AspectRatio))
	}
	return out, nil
}

// sectionMasterPalettePrompt 是【主基调色卡】段（卡 B 的 MasterPalettePrompt，说明换成生成分段的用法）；没有主色卡时为空。
func sectionMasterPalettePrompt(p *MasterPalette) string {
	if p == nil {
		return ""
	}
	return MasterPalettePrompt(*p, "全片底色：角色服装、道具和场景的主色从这里取，每一场再按【本场色卡】微调，不另起一套色系。")
}

// sectionCharacterPrompt 是【已确认角色外观】段（同生成分段，说明换成按技能格式写）。
func sectionCharacterPrompt(characters []StepCharacter, sourceText string) string {
	text := StepCharacterAppearancePrompt(characters, sourceText)
	if text == "" {
		return ""
	}
	_, rest, _ := strings.Cut(text, "\n")
	return "【已确认角色外观】角色出镜时画面写出的外貌、发型、服装和标志物与下面的设定一致，不要自行改换；剧情明确换装或受伤时才改。角色名一律写资产名（下面「｜」前的名字）。\n" + rest
}

// sectionAssetPrompt 是【项目已确认资产】段：名字和分类，场景带锚点（站位、光源只用这些锚点名）。
func sectionAssetPrompt(assets []StepAsset) string {
	if len(assets) == 0 {
		return ""
	}
	lines := []string{"【项目已确认资产】角色、场景、道具一律写这里的原名（子卡写「名·变体」原样）："}
	for _, a := range assets {
		line := "- " + a.Title + "（" + a.CategoryLabel + "）"
		if a.Brief != "" && a.Brief != a.Title {
			line += "：" + a.Brief
		}
		if len(a.Anchors) > 0 {
			anchors := a.Anchors
			if len(anchors) > stepAssetAnchors {
				anchors = anchors[:stepAssetAnchors]
			}
			line += "；锚点：" + strings.Join(anchors, "、")
		}
		lines = append(lines, line)
	}
	lines = append(lines, "- 场景写了锚点的，人物站位、光源位置只用该场景的锚点名，不另起位置名。")
	return strings.Join(lines, "\n")
}

// SectionPlanPrompt 是【分节生成说明】：小节表、一节的上限、分回合写法。genre 是这条对话的戏类：一章有几个戏类时，这条对话只写这个戏类的节，
// 其余节由另一套分镜技能写（小节表照样全列，便于前后衔接）。
func SectionPlanPrompt(plan SectionPlan, genre ShotGenre, modelLabel string) string {
	var b strings.Builder
	var mine []string
	hi := 0.0
	for k, s := range plan.Sections {
		if s.Genre == genre.ID {
			mine = append(mine, fmt.Sprintf("%d", k+1))
			if hi == 0 {
				hi = sectionMax(plan, s)
			}
		}
	}
	limit := fmt.Sprintf("%.0f 秒", hi)
	if modelLabel != "" {
		limit += "（项目视频模型 " + modelLabel + " 的单段上限与" + genre.Label + "口径取小）"
	}
	if len(mine) == len(plan.Sections) {
		fmt.Fprintf(&b, "【分节生成说明】本章共 %d 个小节，一个小节 = 一次视频生成 = 技能里的一个%s，时长不超过 %s。这个对话分 %d 个回合写完：每个回合只写我点名的那一节，写完就停；我发下一节时再接着写。各节如下：\n",
			len(plan.Sections), sectionUnitWord(genre), limit, len(plan.Sections))
	} else {
		fmt.Fprintf(&b, "【分节生成说明】本章共 %d 个小节，一个小节 = 一次视频生成。其中第 %s 节是%s，由这个对话按本技能写（一节 = 技能里的一个%s，时长不超过 %s），分 %d 个回合：每个回合只写我点名的那一节，写完就停；其余小节由另一套分镜技能写，不用你写。各节如下：\n",
			len(plan.Sections), strings.Join(mine, "、"), genre.Label, sectionUnitWord(genre), limit, len(mine))
	}
	for k, s := range plan.Sections {
		tag := ""
		if s.Genre != genre.ID {
			if g, ok := ShotGenreByID(s.Genre); ok {
				tag = "（" + g.Label + "，另一套技能写）"
			}
		}
		fmt.Fprintf(&b, "%d. %s「%s」第 %d 节%s：正文第 %d～%d 行，%d 句台词，估时约 %.0f 秒\n", k+1, s.ID, s.Heading, s.No, tag, s.Lines[0], s.Lines[1], len(s.LineIDs), s.Est)
	}
	b.WriteString("同一场的后一节从上一节最后一镜的站位、朝向、手里的道具和灯光接着写。")
	return b.String()
}

func sectionUnitWord(g ShotGenre) string {
	switch g.ID {
	case ShotGenreWenxi15:
		return "「片段」"
	case ShotGenreWuxi:
		return "分镜骨架（N 秒 N 镜）"
	}
	return "「小节」"
}

// sectionMax 是一节的上限（没写 Max 的节取计划的默认上限）。
func sectionMax(plan SectionPlan, sec Section) float64 {
	if sec.Max > 0 {
		return sec.Max
	}
	return plan.MaxSeconds
}

// SectionTurnPrompt 是第 k 节（从 1 数）的本轮指令；palette 是本场色卡（本场第一节才给），cast 是本场换上的子卡（主卡名 → 子卡名），
// aspect 是项目画幅（武戏参数表用）。
func SectionTurnPrompt(plan SectionPlan, k int, palette string, assets []StepAsset, cast map[string]string, aspect string) string {
	sec := plan.Sections[k-1]
	genre, _ := ShotGenreByID(sec.Genre)
	n := len(plan.Sections)
	var parts []string
	head := fmt.Sprintf("【第 %d/%d 节：%s】场 %s「%s」第 %d 节，戏类：%s，估时约 %.0f 秒，上限 %.0f 秒。", k, n, sec.ID, sec.Scene, sec.Heading, sec.No, genre.Label, sec.Est, sectionMax(plan, sec))
	if slices.ContainsFunc(plan.Sections[:k-1], func(s Section) bool { return s.Genre == sec.Genre }) {
		head = "继续写下一节。" + head
	}
	parts = append(parts, head)
	if palette != "" {
		parts = append(parts, palette)
	}
	parts = append(parts, fmt.Sprintf("【本节剧本范围】正文第 %d～%d 行（只写这些内容，不要写到下一节）：\n%s", sec.Lines[0], sec.Lines[1], sec.Text()))
	var lines []string
	for _, b := range sec.Beats {
		if b.isLine() {
			line := fmt.Sprintf("- %s %s：%s", b.ID, b.Speaker, b.Text)
			if b.Kind == "vo" {
				line += "（画外）"
			}
			lines = append(lines, line)
		}
	}
	switch {
	case len(lines) == 0:
		parts = append(parts, "【本节台词表】本节没有台词，不要补写台词。")
	case genre.ID == ShotGenreWuxi:
		parts = append(parts, "【本节台词表】每句逐字照抄（不删、不改、不合并、不改成战吼或拟声），一句可以跨相邻几个 1 秒镜头说完（写在开口那一镜，后面几镜写「（接上句）」）；不要写本节以外的台词：\n"+strings.Join(lines, "\n"))
	default:
		parts = append(parts, "【本节台词表】每句逐字照抄（不删、不改、不合并），每句只写在一个镜头里；不要写本节以外的台词：\n"+strings.Join(lines, "\n"))
	}
	characters, castText := sectionCastPrompt(sec, assets, cast)
	if castText != "" {
		parts = append(parts, castText)
	}
	if genre.ID == ShotGenreWuxi {
		parts = append(parts, wuxiParamsPrompt(sec, characters, assets, aspect), WuxiBeatsPrompt(int(sec.Est)))
	}
	parts = append(parts, sectionOutputRule(plan, k))
	return strings.Join(parts, "\n\n")
}

// wuxiParamsPrompt 是武戏节的【参数表（平台已填）】：v7 参数表里平台定得了的几项（时长、模式、场地、A / B、闪帧、画幅），其余按技能默认。
// A / B 按本节先开口的人排（没说话的角色排在后面）。
func wuxiParamsPrompt(sec Section, characters []string, assets []StepAsset, aspect string) string {
	place := ParseSceneHeading(sec.Heading).Location
	for _, a := range assets {
		if a.CategoryLabel == "场景" && SectionPlaceMatches(sec.Heading, a.Title) {
			place = a.Title
			break
		}
	}
	// A / B 先取本节说话的人（按开口先后，子卡名「名·变体」照写），再取正文提到的其余角色。
	var ordered []string
	for _, sp := range sec.Speakers {
		for _, c := range characters {
			if (c == sp || strings.HasPrefix(c, sp+"·")) && !containsString(ordered, c) {
				ordered = append(ordered, c)
				break
			}
		}
	}
	for _, c := range characters {
		if !containsString(ordered, c) {
			ordered = append(ordered, c)
		}
	}
	characters = ordered
	who := "A：" + firstNonBlank(sliceAt(characters, 0), "按剧本写") + "；B：" + firstNonBlank(sliceAt(characters, 1), "无（单人或群戏，按剧本写）")
	if len(characters) > 2 {
		who += "；其他在场：" + strings.Join(characters[2:], "、")
	}
	fields := []string{
		fmt.Sprintf("时长：%d 秒（%d 镜，1 秒 1 镜）", int(sec.Est), int(sec.Est)),
		"模式：D·短剧分镜（台词逐字保留）",
		"场地：" + firstNonBlank(place, sec.Heading),
		who,
		"2D 闪帧：启用（按 P2+ 命中嵌入）",
	}
	if aspect = strings.TrimSpace(aspect); aspect != "" {
		fields = append(fields, "画幅："+aspect)
	}
	return "【参数表（平台已填）】" + strings.Join(fields, "｜") + "。其余参数按技能默认；不再输出参数表。"
}

// wuxiBeats 是 v7.0 4.02 的 30 秒导演节拍图（每段的起点秒数）。
var wuxiBeats = []struct {
	at    float64
	label string
}{{0, "建立威胁"}, {3, "第一次真正交锋"}, {8, "攻防升级"}, {14, "战局反转"}, {20, "极限爆发"}, {26, "终结+余波"}}

// WuxiBeatsPrompt 是一节 n 秒武戏的导演节拍：30 秒节拍图按 n/30 缩放到整秒，挤到同一秒的段并进前一段。
func WuxiBeatsPrompt(n int) string {
	if n <= 0 {
		return ""
	}
	type span struct {
		a, b  int
		label string
	}
	var spans []span
	for i, beat := range wuxiBeats {
		a := int(math.Round(beat.at * float64(n) / 30))
		b := n
		if i+1 < len(wuxiBeats) {
			b = int(math.Round(wuxiBeats[i+1].at * float64(n) / 30))
		}
		if b <= a {
			if len(spans) > 0 {
				spans[len(spans)-1].label += "+" + beat.label
			}
			continue
		}
		spans = append(spans, span{a, b, beat.label})
	}
	if len(spans) > 0 {
		spans[len(spans)-1].b = n
	}
	parts := make([]string, 0, len(spans))
	for _, s := range spans {
		parts = append(parts, fmt.Sprintf("%d–%ds %s", s.a, s.b, s.label))
	}
	return fmt.Sprintf("【本节节拍】4.02 的 30 秒导演节拍图按 %d/30 缩放：%s。", n, strings.Join(parts, "；"))
}

func sliceAt(items []string, i int) string {
	if i < len(items) {
		return items[i]
	}
	return ""
}

// sectionCastPrompt 是【本节人物与资产】：本节说话的人和正文提到的角色（同名的子卡「名·变体」一起列出）、本场场景、提到的道具。
// cast 里的角色（这一场换上了子卡）写子卡名，并另起一行【本场角色子卡】写明主卡在这一场用哪张子卡。另返回本节角色（武戏参数表的 A / B）。
func sectionCastPrompt(sec Section, assets []StepAsset, cast map[string]string) ([]string, string) {
	text := sec.Heading + "\n" + sec.Text()
	var characters, places, props []string
	for _, a := range assets {
		base, _, _ := strings.Cut(a.Title, "·")
		mentioned := strings.Contains(text, a.Title) || (base != "" && strings.Contains(text, base)) || containsString(sec.Speakers, base)
		switch {
		case a.CategoryLabel == "角色" && mentioned:
			characters = append(characters, a.Title)
		case a.CategoryLabel == "场景" && (mentioned || SectionPlaceMatches(sec.Heading, a.Title)):
			place := a.Title
			if len(a.Anchors) > 0 {
				place += "（锚点：" + strings.Join(a.Anchors[:min(len(a.Anchors), stepAssetAnchors)], "、") + "）"
			}
			places = append(places, place)
		case a.CategoryLabel == "道具" && mentioned:
			props = append(props, a.Title)
		}
	}
	for _, s := range sec.Speakers {
		if !slicesHasPrefix(characters, s) {
			characters = append(characters, s)
		}
	}
	var swapped []string
	for i, name := range characters {
		if child := cast[name]; child != "" {
			characters[i] = child
			swapped = append(swapped, fmt.Sprintf("%s 用「%s」", name, child))
		}
	}
	var lines []string
	if len(characters) > 0 {
		lines = append(lines, "角色："+strings.Join(characters, "、"))
	}
	if len(places) > 0 {
		lines = append(lines, "场景："+strings.Join(places, "、"))
	}
	if len(props) > 0 {
		lines = append(lines, "道具："+strings.Join(props, "、"))
	}
	if len(lines) == 0 {
		return characters, ""
	}
	out := "【本节人物与资产】名字照写（同一人有几张子卡时按本节的服装 / 状态选一张，写「名·变体」原样）：\n" + strings.Join(lines, "\n")
	if len(swapped) > 0 {
		out += "\n【本场角色子卡】这一场" + strings.Join(swapped, "；") + "：人物名（含站位）写子卡名「名·变体」原样，外貌按子卡的服装 / 状态写。"
	}
	return characters, out
}

func slicesHasPrefix(items []string, base string) bool {
	for _, it := range items {
		if it == base || strings.HasPrefix(it, base+"·") {
			return true
		}
	}
	return false
}

// SectionPlaceMatches 是场景资产对得上场次头的地点。
func SectionPlaceMatches(heading, title string) bool {
	loc := SceneLocationKey(ParseSceneHeading(heading).Location)
	t := SceneLocationKey(title)
	return loc != "" && t != "" && (strings.Contains(loc, t) || strings.Contains(t, loc))
}

// sectionOutputRule 是这一节的输出要求：按技能原生格式，平台只给片段 / 小节号和时长上限。
func sectionOutputRule(plan SectionPlan, k int) string {
	sec := plan.Sections[k-1]
	genre, _ := ShotGenreByID(sec.Genre)
	limit := fmt.Sprintf("%.0f", sectionMax(plan, sec))
	switch sec.Genre {
	case ShotGenreWenxi15:
		rule := fmt.Sprintf("【本节输出要求】按技能的固定输出格式写成一个片段：标题写「## 片段 %d（时长：XX秒）」，时长按技能规则写在 10～%s 秒，4 镜或 6 镜；要素提取、当前空间站位、镜头行、最后一镜结束站位、下一片段继承点照技能格式写全。", k, limit)
		if n := len(sec.LineIDs); n > genre.MaxLines && !linesFit(sec.Beats, genre) {
			rule += fmt.Sprintf("本节有 %d 句台词（连着接话，平台没有拆开），仍写成这一个片段，按技能规则安排镜头和时长。", n)
		}
		if !sec.First {
			rule += "这是同一场的后续片段：衔接参考首帧、参考首帧（0s-0.5s）照上一节的最后一镜结束站位和下一片段继承点写。"
		} else {
			rule += "这是本场第一个片段，不写衔接参考首帧。"
		}
		return rule + "不要输出 JSON、代码块、整集完成标记或续写锚点，只写这一个片段。"
	case ShotGenreWenxi30:
		rule := fmt.Sprintf("【本节输出要求】按技能的导演分镜字段格式写成一个小节：标题写「小节%d：本节标题」，生成时长 0～%s 秒以内，小节头、每个镜头「镜头N（a-b秒）」的全部字段、小节约束、站位标注、三个连续性总结照技能格式写全；不写即梦粘贴提示词（柚子按镜头字段编译视频提示词）。", k, limit)
		if k == 1 {
			rule += "第一节之前先写一次参数确认、全局风格锁定和角色音色设定（之后各节不再重复）。"
		} else {
			rule += "参数确认、全局风格锁定、角色音色设定已在第一节前写过，不要重复。"
		}
		return rule + "不写台词核对摘要和自检摘要；不要输出 JSON，只写这一个小节。"
	case ShotGenreWuxi:
		n := int(sec.Est)
		rule := fmt.Sprintf("【本节输出要求】按技能接入说明里的 v7.0 每镜标准格式写本节：先写一行【核心观看体验】，再从「镜 1｜0-1s」写到「镜 %d｜%d-%ds」，共 %d 镜、每镜 1 秒（不超过 %s 秒）；每镜的景别 / 焦段 / 运镜、摄影机目标、站位、动作链、力度、受击、战损、闪帧、特效、环境反馈、表情、台词、光影、下一镜连接逐行写全，站位和动作链里写人物真名。", n, n-1, n, n, limit)
		if !sec.First {
			rule += "这是同一场的后续小节：镜 1 从上一节最后一镜的站位、朝向、距离和战损接着打。"
		}
		return rule + "不写参数表、全局设定、核心铁律、SSS 一句话、负面词和自检清单，不要输出 JSON 或代码块，只写这一节。"
	}
	return "【本节输出要求】按技能的原生格式只写这一节。"
}
