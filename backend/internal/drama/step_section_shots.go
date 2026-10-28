package drama

import (
	"fmt"
	"strings"
)

// 按小节生成（M8 卡 F）的提示词：第一轮 = 这一戏类技能的上下文（SKILL.md + 4 张必读卡）+ 项目记忆 + 【分节生成说明】+ 第 1 节；
// 之后每一轮只发这一节（同一对话续写，技能和项目记忆在历史里）。每一节写本节剧本范围原文、本节台词表、本节人物与资产和输出要求；
// 输出格式交给技能原文（7.3 的片段、即梦的小节），平台只规定小节边界，不再拼 JSON Lines 契约。

// SectionShotsInput 是组装按小节生成提示词的全部输入。
type SectionShotsInput struct {
	ChapterShotsInput
	Genre ShotGenre
	Plan  SectionPlan
	// ModelLabel 是项目视频模型（提示里说明一节的上限从哪来）。
	ModelLabel    string
	MasterPalette *MasterPalette
	// Palettes 是各场的【本场色卡】段（单元内场编号 → 文字），本场第一节带上；没有的场不写。
	Palettes map[string]string
}

// SectionShotsPrompt 是组装好的按小节生成提示词：Prompt 是第一轮（不含第 1 节），Turns 是每一节的本轮指令（第一轮接在 Prompt 后面）。
type SectionShotsPrompt struct {
	StepPrompt
	Turns []string
}

// SectionShotsTask 是交给技能的【用户任务】。
func SectionShotsTask(title, sourceText string, genre ShotGenre, n int) string {
	return strings.Join([]string{
		"章节：" + title,
		sourceText,
		fmt.Sprintf("平台已把本章按%s的口径切成 %d 个小节（一个小节 = 一次视频生成），见【分节生成说明】。上面的正文只供理解前后剧情；每一轮只写平台点名的那一个小节，按技能的原生分镜格式输出，保持剧情因果、人物关系、关键动作与台词完整。", genre.Label, n),
	}, "\n\n")
}

// BuildSectionShotsPrompt 组装按小节生成的提示词；技能缺 SKILL.md 或计划里没有小节时返回错误。
func BuildSectionShotsPrompt(in SectionShotsInput) (SectionShotsPrompt, error) {
	if len(in.Plan.Sections) == 0 {
		return SectionShotsPrompt{}, fmt.Errorf("章节正文切不出小节")
	}
	step, ok := StepByID(in.Genre.Step)
	if !ok {
		return SectionShotsPrompt{}, fmt.Errorf("戏类 %s 没有步骤", in.Genre.ID)
	}
	skillPrompt, files, ok := StepSkillPrompt(in.Skill, SectionShotsTask(in.ChapterTitle, in.SourceText, in.Genre, len(in.Plan.Sections)), step.SkillBudgetChars, step.SkillLinkedFiles)
	if !ok {
		return SectionShotsPrompt{}, fmt.Errorf("技能 %s 当前版本没有 SKILL.md", in.Skill.ID)
	}
	memory, truncated := fitPromptBudget([]promptSection{
		{"episodeSeconds", StepEpisodeDurationPrompt(in.EpisodeSeconds)},
		{"anchor", styleAnchorPrompt(in.Anchor, "【全局风格锚】项目锁定的全片取向，优先于技能自带的画风假设：画面、光影、镜头语言和调色都按它写，禁用项一律不出现：", true)},
		{"masterPalette", sectionMasterPalettePrompt(in.MasterPalette)},
		{"characters", sectionCharacterPrompt(in.Characters, in.SourceText)},
		{"projectStyle", stepStylePrompt(in.Style, "分镜里的画面、光影、服饰和场景都按这个画风写；技能里写的默认画风（真人写实、3D 动漫等）以这里为准。这是项目级基线，不要整段抄进每个镜头。")},
		{"assets", sectionAssetPrompt(in.Assets)},
	}, ChapterShotsMemoryBudget)
	text := joinTruthy([]string{
		skillPrompt,
		memory["projectStyle"],
		memory["anchor"],
		memory["masterPalette"],
		memory["characters"],
		memory["episodeSeconds"],
		memory["assets"],
		SectionPlanPrompt(in.Plan, in.ModelLabel),
	}, "\n\n")
	out := SectionShotsPrompt{StepPrompt: StepPrompt{Text: text, Files: files, Truncated: truncated}}
	for k, sec := range in.Plan.Sections {
		palette := ""
		if sec.First {
			palette = in.Palettes[sec.Scene]
		}
		out.Turns = append(out.Turns, SectionTurnPrompt(in.Plan, k+1, palette, in.Assets))
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

// SectionPlanPrompt 是【分节生成说明】：小节表、一节的上限、分回合写法。
func SectionPlanPrompt(plan SectionPlan, modelLabel string) string {
	var b strings.Builder
	limit := fmt.Sprintf("%.0f 秒", plan.MaxSeconds)
	if modelLabel != "" {
		limit += "（项目视频模型 " + modelLabel + " 的单段上限与" + plan.Genre.Label + "口径取小）"
	}
	fmt.Fprintf(&b, "【分节生成说明】本章共 %d 个小节，一个小节 = 一次视频生成 = 技能里的一个%s，时长不超过 %s。这个对话分 %d 个回合写完：每个回合只写我点名的那一节，写完就停；我发下一节时再接着写。各节如下：\n",
		len(plan.Sections), sectionUnitWord(plan.Genre), limit, len(plan.Sections))
	for k, s := range plan.Sections {
		fmt.Fprintf(&b, "%d. %s「%s」第 %d 节：正文第 %d～%d 行，%d 句台词，估时约 %.0f 秒\n", k+1, s.ID, s.Heading, s.No, s.Lines[0], s.Lines[1], len(s.LineIDs), s.Est)
	}
	b.WriteString("同一场的后一节从上一节最后一镜的站位、朝向、手里的道具和灯光接着写。")
	return b.String()
}

func sectionUnitWord(g ShotGenre) string {
	if g.ID == ShotGenreWenxi15 {
		return "「片段」"
	}
	return "「小节」"
}

// SectionTurnPrompt 是第 k 节（从 1 数）的本轮指令；palette 是本场色卡（本场第一节才给）。
func SectionTurnPrompt(plan SectionPlan, k int, palette string, assets []StepAsset) string {
	sec := plan.Sections[k-1]
	n := len(plan.Sections)
	var parts []string
	head := fmt.Sprintf("【第 %d/%d 节：%s】场 %s「%s」第 %d 节，戏类：%s，估时约 %.0f 秒，上限 %.0f 秒。", k, n, sec.ID, sec.Scene, sec.Heading, sec.No, plan.Genre.Label, sec.Est, plan.MaxSeconds)
	if k > 1 {
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
	if len(lines) == 0 {
		parts = append(parts, "【本节台词表】本节没有台词，不要补写台词。")
	} else {
		parts = append(parts, "【本节台词表】每句逐字照抄（不删、不改、不合并），每句只写在一个镜头里；不要写本节以外的台词：\n"+strings.Join(lines, "\n"))
	}
	if cast := sectionCastPrompt(sec, assets); cast != "" {
		parts = append(parts, cast)
	}
	parts = append(parts, sectionOutputRule(plan, k))
	return strings.Join(parts, "\n\n")
}

// sectionCastPrompt 是【本节人物与资产】：本节说话的人和正文提到的角色（同名的子卡「名·变体」一起列出）、本场场景、提到的道具。
func sectionCastPrompt(sec Section, assets []StepAsset) string {
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
		return ""
	}
	return "【本节人物与资产】名字照写（同一人有几张子卡时按本节的服装 / 状态选一张，写「名·变体」原样）：\n" + strings.Join(lines, "\n")
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
	limit := fmt.Sprintf("%.0f", plan.MaxSeconds)
	switch plan.Genre.ID {
	case ShotGenreWenxi15:
		rule := fmt.Sprintf("【本节输出要求】按技能的固定输出格式写成一个片段：标题写「## 片段 %d（时长：XX秒）」，时长 10～%s 秒（本节内容少时可短于 10 秒，不要为凑时长加戏），4 镜或 6 镜；要素提取、当前空间站位、镜头行、最后一镜结束站位、下一片段继承点照技能格式写全。", k, limit)
		if !sec.First {
			rule += "这是同一场的后续片段：衔接参考首帧、参考首帧（0s-0.5s）照上一节的最后一镜结束站位和下一片段继承点写。"
		} else {
			rule += "这是本场第一个片段，不写衔接参考首帧。"
		}
		return rule + "不要输出 JSON、代码块、整集完成标记或续写锚点，只写这一个片段。"
	case ShotGenreWenxi30:
		rule := fmt.Sprintf("【本节输出要求】按技能的导演分镜字段格式写成一个小节：标题写「小节%d：本节标题」，生成时长 0～%s 秒以内，小节头、每个镜头「镜头N（a-b秒）」的全部字段、小节约束、站位标注、三个连续性总结和即梦粘贴提示词照技能格式写全。", k, limit)
		if k == 1 {
			rule += "第一节之前先写一次参数确认、全局风格锁定和角色音色设定（之后各节不再重复）。"
		} else {
			rule += "参数确认、全局风格锁定、角色音色设定已在第一节前写过，不要重复。"
		}
		return rule + "不写台词核对摘要和自检摘要；不要输出 JSON，只写这一个小节。"
	}
	return "【本节输出要求】按技能的原生格式只写这一节。"
}
