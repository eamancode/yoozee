package drama

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// chapter.shots（生成分段）的提示词组装：技能上下文 + 代码负责的变量 + 按 schema 生成的契约。
// 各段的写法、字数上限和先后与原先前端 chapterStoryboardTaskPrompt 一致。

// StepStyle 是项目画风（画风快照的标题和执行规范原文）。
type StepStyle struct {
	Title  string
	Prompt string
}

// StepCharacter 是已确认的角色（角色卡当前版本的设定）。
type StepCharacter struct {
	Name       string
	Definition map[string]any
}

// StepAsset 是资产清单的一项。
type StepAsset struct {
	Title string
	// CategoryLabel 是清单里括号里的分类（角色、场景、道具……）。
	CategoryLabel string
	// Brief 是一句话设定（预览文字前 40 字）；为空不写。
	Brief   string
	Anchors []string
}

// ChapterShotsInput 是组装生成分段提示词的全部输入。
type ChapterShotsInput struct {
	ChapterTitle string
	// SourceText 是章节正文纯文本（编辑器 getText 的结果）。
	SourceText     string
	Style          StepStyle
	Characters     []StepCharacter
	Assets         []StepAsset
	EpisodeSeconds float64
	Skill          StepSkill
}

const (
	stepStylePromptChars     = 1200
	stepCharacterPromptChars = 1600
	stepCharacterLineChars   = 220
	stepCharacterFieldChars  = 80
	stepAssetBriefChars      = 40
	stepAssetAnchors         = 12
	// StepChapterShotsMaxAssets 是资产清单最多几项。
	StepChapterShotsMaxAssets = 60
)

// stepStyleSections 是画风小节按对写分镜画面的用处排序，超预算时从后往前舍弃。
var stepStyleSections = []string{"风格组合", "视觉媒介", "角色设计系统", "项目色彩与光影", "服饰、材质与场景", "全局禁用", "影像与动态基线", "叙事气质", "题材世界观", "使用边界", "资产一致性", "项目定位"}

var stepCharacterVisualFields = [][2]string{{"appearance", "外貌"}, {"clothing", "服装"}, {"physique", "体型"}, {"props", "随身道具"}, {"consistencyPrompt", "一致性"}}

var stepStyleSection = regexp.MustCompile(`^【([^】]+)】`)

// ChapterShotsTask 是交给技能的【用户任务】：章节标题、正文、拆解要求。
func ChapterShotsTask(title, sourceText string) string {
	return strings.Join([]string{"章节：" + title, sourceText, "请将本章拆解为可直接进入短剧分镜制作的镜头脚本，保持剧情因果、人物关系、关键动作与台词完整。"}, "\n\n")
}

// BuildChapterShotsPrompt 组装生成分段的提示词。
func BuildChapterShotsPrompt(in ChapterShotsInput) (StepPrompt, error) {
	step, _ := StepByID(StepChapterShots)
	contract, err := BuiltinContract(step.Contract)
	if err != nil {
		return StepPrompt{}, err
	}
	skillPrompt, files, ok := StepSkillPrompt(in.Skill, ChapterShotsTask(in.ChapterTitle, in.SourceText), step.SkillBudgetChars, step.SkillLinkedFiles)
	if !ok {
		return StepPrompt{}, fmt.Errorf("技能 %s 当前版本没有 SKILL.md", in.Skill.ID)
	}
	projectContext := joinTruthy([]string{
		skillPrompt,
		StepStylePrompt(in.Style),
		StepCharacterAppearancePrompt(in.Characters, in.SourceText),
		StepEpisodeDurationPrompt(in.EpisodeSeconds),
	}, "\n\n")
	withContract := projectContext + "\n\n" + contract.Text(contract.DefaultRequirement(), ContractTextOptions{SkillShaped: true})
	text := joinTruthy([]string{withContract, StepAssetPrompt(contract, in.Assets)}, "\n\n")
	return StepPrompt{Text: text, Files: files}, nil
}

// StepStylePrompt 是【项目画风】段：按小节取到预算为止、按原顺序输出；自由书写的画风没有【】小节，按行截取。
func StepStylePrompt(style StepStyle) string {
	var lines []string
	for _, line := range strings.Split(style.Prompt, "\n") {
		if line = jsCompact(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	rank := func(line string) int {
		m := stepStyleSection.FindStringSubmatch(line)
		name := ""
		if m != nil {
			name = m[1]
		}
		if i := slices.Index(stepStyleSections, name); i >= 0 {
			return i
		}
		return len(stepStyleSections)
	}
	order := make([]int, len(lines))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		if ra, rb := rank(lines[a]), rank(lines[b]); ra != rb {
			return ra - rb
		}
		return a - b
	})
	kept := map[int]string{}
	remaining := stepStylePromptChars
	for _, index := range order {
		line := lines[index]
		if n := jsLen(line); n <= remaining {
			kept[index] = line
			remaining -= n
		} else if remaining >= 60 {
			kept[index] = jsSlice(line, remaining-1) + "…"
			break
		}
	}
	title := jsTrim(style.Title)
	if title == "" {
		title = "未命名画风"
	}
	out := []string{"【项目画风：" + title + "】分镜里的画面、光影、服饰和场景描述都按这个画风写，不要写成其他媒介；这是项目级基线，不要整段抄进每个镜头。"}
	for i := range lines {
		if line, ok := kept[i]; ok {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// StepCharacterAppearancePrompt 是【已确认角色外观】段：只取画面相关字段；本章正文里先出现的角色排在前面，超预算的角色不写。
func StepCharacterAppearancePrompt(characters []StepCharacter, sourceText string) string {
	type item struct {
		line     string
		position int
		index    int
	}
	var ordered []item
	for i, c := range characters {
		line := stepCharacterLine(c)
		if line == "" {
			continue
		}
		ordered = append(ordered, item{line: line, position: firstMention(c, sourceText), index: i})
	}
	slices.SortStableFunc(ordered, func(a, b item) int {
		if a.position != b.position {
			if a.position < b.position {
				return -1
			}
			return 1
		}
		return a.index - b.index
	})
	var lines []string
	remaining := stepCharacterPromptChars
	for _, it := range ordered {
		n := jsLen(it.line)
		if n > remaining {
			break
		}
		lines = append(lines, it.line)
		remaining -= n
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(append([]string{"【已确认角色外观】角色出镜时，imageGenerationPrompt 写出本镜看得见的外貌、发型、服装和标志物，与下面的设定一致，不要自行改换；剧情明确换装或受伤时才改。"}, lines...), "\n")
}

// firstMention 是角色名或别名在正文里第一次出现的位置；没出现排最后。
func firstMention(c StepCharacter, sourceText string) int {
	names := []any{c.Name}
	if aliases, ok := c.Definition["aliases"].([]any); ok {
		names = append(names, aliases...)
	}
	best := math.MaxInt
	for _, name := range names {
		text, ok := name.(string)
		if !ok || jsTrim(text) == "" {
			continue
		}
		if i := strings.Index(sourceText, jsTrim(text)); i >= 0 && i < best {
			best = i
		}
	}
	return best
}

func stepCharacterLine(c StepCharacter) string {
	name := jsTrim(c.Name)
	var parts []string
	for _, field := range stepCharacterVisualFields {
		text, _ := c.Definition[field[0]].(string)
		if text = jsTruncate(jsCompact(text), stepCharacterFieldChars); text != "" {
			parts = append(parts, field[1]+"："+text)
		}
	}
	// 只有一段角色提示词的设定是 { prompt }。
	if len(parts) == 0 {
		text, _ := c.Definition["prompt"].(string)
		if text = jsCompact(text); text != "" {
			parts = append(parts, text)
		}
	}
	if name == "" || len(parts) == 0 {
		return ""
	}
	return jsTruncate("- "+name+"｜"+strings.Join(parts, "；"), stepCharacterLineChars)
}

// StepEpisodeDurationPrompt 是【本集时长】段：镜头估时合计留 10% 余量，与 R-SEG-008 检查分段合计用同一个数。
func StepEpisodeDurationPrompt(episodeSeconds float64) string {
	if episodeSeconds <= 0 || math.IsNaN(episodeSeconds) || math.IsInf(episodeSeconds, 0) {
		return ""
	}
	seconds := int(math.Floor(episodeSeconds + 0.5))
	return fmt.Sprintf("【本集时长】本集目标 %d 秒，镜头 est 合计不超过 %d 秒（%d×1.1）；戏多时并镜或删掉不推进剧情的镜头，不靠压短单镜估时凑数。", seconds, int(math.Floor(float64(seconds)*1.1+0.5)), seconds)
}

// StepAssetPrompt 是【项目已确认资产】段，带契约里 assets 字段的要求；清单为空时整段不写。
func StepAssetPrompt(contract *Contract, assets []StepAsset) string {
	if len(assets) == 0 {
		return ""
	}
	lines := []string{"【项目已确认资产】"}
	hasAnchors := false
	for _, a := range assets {
		line := "- " + a.Title + "（" + a.CategoryLabel + "）"
		if a.Brief != "" && a.Brief != a.Title {
			line += "：" + a.Brief
		}
		if len(a.Anchors) > 0 {
			hasAnchors = true
			anchors := a.Anchors
			if len(anchors) > stepAssetAnchors {
				anchors = anchors[:stepAssetAnchors]
			}
			line += "；锚点：" + strings.Join(anchors, "、")
		}
		lines = append(lines, line)
	}
	lines = append(lines, contract.FieldInstruction("assets"))
	if hasAnchors {
		lines = append(lines, "- 场景写了锚点的，镜头 spec 的 blocking.at 只用该场景的锚点名，不另起位置名。")
	}
	return strings.Join(lines, "\n")
}

// StepAssetBrief 是资产的一句话设定：预览文字压成一行取前 40 字；没有预览时设定是 JSON（不写）或只有标题。
func StepAssetBrief(previewText string, hasDefinition bool, title string) string {
	prompt := jsSlice(jsCompact(previewText), 600)
	if prompt == "" {
		if hasDefinition {
			return ""
		}
		prompt = jsTrim(title)
	}
	if strings.HasPrefix(jsTrim(prompt), "{") {
		return ""
	}
	return jsSlice(jsCompact(prompt), stepAssetBriefChars)
}

// joinTruthy 去掉空串后拼接（前端 filter(Boolean).join）。
func joinTruthy(parts []string, sep string) string {
	return strings.Join(slices.DeleteFunc(slices.Clone(parts), func(p string) bool { return p == "" }), sep)
}

// ShotsScene 是逐场续写的一场：剧本场编号（与镜头表台词表同一套编号）、场标题和开场原句（区分同名的场）。
type ShotsScene struct {
	ID      string `json:"scene"`
	Heading string `json:"heading"`
	Opening string `json:"opening"`
}

const shotsSceneOpeningChars = 30

// ChapterShotsScenes 按正文的场次头切场（ScriptFromText 的规则：「## 」「内·」「外·」「第N场」等开头的行）；
// 不到两场时返回 nil，整章一轮写完。
func ChapterShotsScenes(plainText string, title string) []ShotsScene {
	script := ScriptFromText(plainText, title)
	if len(script.Scenes) < 2 {
		return nil
	}
	out := make([]ShotsScene, 0, len(script.Scenes))
	for _, sc := range script.Scenes {
		scene := ShotsScene{ID: sc.ID, Heading: sc.Heading}
		if len(sc.Beats) > 0 {
			b := sc.Beats[0]
			text := b.Text
			if b.Speaker != "" {
				text = b.Speaker + "：" + text
			}
			scene.Opening = firstSentence(text, shotsSceneOpeningChars)
		}
		out = append(out, scene)
	}
	return out
}

// firstSentence 取第一句（到第一个句末标点为止），超过 limit 个字按字截断。
func firstSentence(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	for i, r := range runes {
		if i >= limit {
			break
		}
		if strings.ContainsRune("。！？!?", r) {
			return string(runes[:i+1])
		}
	}
	return string(runes[:min(limit, len(runes))])
}

func shotsSceneLabel(s ShotsScene) string {
	label := "「" + s.Heading + "」"
	if s.Opening != "" {
		label += "（从「" + s.Opening + "」起）"
	}
	return label
}

// ChapterShotsTurnPlan 是逐场续写第一回合接在提示词后面的分回合说明（线上对照实验验证过的写法，输出改成一镜一行）。
func ChapterShotsTurnPlan(scenes []ShotsScene) string {
	parts := make([]string, 0, len(scenes))
	ids := make([]string, 0, len(scenes))
	for i, s := range scenes {
		parts = append(parts, fmt.Sprintf("第 %d 场%s", i+1, shotsSceneLabel(s)))
		ids = append(ids, s.ID)
	}
	n := len(scenes)
	return fmt.Sprintf("【分回合输出】本章按正文的场景标题共 %d 场：%s。这个对话分 %d 个回合写完整章：本回合只写第 1 场的全部镜头；我说「继续」时再写下一场。"+
		"每个回合仍按【受保护输出契约】一镜一行输出，只写本场的镜头；shotNumber 接着上一回合往下编，不从 1 重来；spec.scene 写本场编号（%s），spec.id 写「场编号-本场序号」。"+
		"写一场的第一个镜头前，先看上一回合最后一个镜头的站位、朝向、谁在画左画右、手里拿着什么、灯光状态和 end_state，从那里接着写。【本集时长】是 %d 场合计，按戏份分摊。",
		n, strings.Join(parts, "；"), n, strings.Join(ids, "、"), n)
}

// ChapterShotsTurnPrompt 是第 k 场（从 1 数，k ≥ 2）的续写指令；nextShot 是本场第一个镜头的 shotNumber。
func ChapterShotsTurnPrompt(scenes []ShotsScene, k int, nextShot int) string {
	s := scenes[k-1]
	return fmt.Sprintf("继续写第 %d 场：%s，到这一场结束为止。shotNumber 从 %d 起，spec.scene 写 %s。先对照上一回合最后一个镜头的站位、朝向、画左画右、手里的道具、灯光和 end_state 接着写。"+
		"只输出本场的镜头，一镜一行，不要重复前面已经写过的镜头。", k, shotsSceneLabel(s), nextShot, s.ID)
}
