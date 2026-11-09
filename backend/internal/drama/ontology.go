package drama

// 本体论导演分镜（流水线一）：技能 yz-ontology-director 按 runtime-context.json 分环节装载，服务端分轮调用。
// 本文件是纯函数：环节上下文拼装、各轮输出的解析与校验、单元卡 → 分段（粘贴提示词原文保存为 Verbatim）。
// 运行编排、写库在 app/project_ontology_*.go。

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	OntologySkillID = "yz-ontology-director"
	// OntologyPromptMaxChars 是技能 params.md P02 的粘贴提示词上限。
	OntologyPromptMaxChars = 15000
	// OntologyUnitMaxSeconds 是技能 params.md P01 的单元总时长上限（含 1 秒站位交接）。
	OntologyUnitMaxSeconds = 30
	// OntologyNoMusicLine 是 R11：每条粘贴提示词必须写明的句子。
	OntologyNoMusicLine = "本段不使用背景音乐"
	// OntologyMaxUnits 是一集最多几个单元（P25 参考 4–7 个；上限防止单轮上下文和结果无界增长）。
	OntologyMaxUnits = 16
)

// 一轮的种类。立项只在项目还没有世界圣经卡时跑；每集依次是 规划 → 讲戏 → 每个单元一轮 → 质检；返修单独一轮。
const (
	OntologyTurnProject = "project"
	OntologyTurnPlan    = "plan"
	OntologyTurnDirect  = "direct"
	OntologyTurnUnit    = "unit"
	OntologyTurnCheck   = "check"
	OntologyTurnRepair  = "repair"
)

// ontologyTurnStages 是每种轮次装载的环节（文件清单取 runtime-context.json 里这些环节的并集，按出现顺序去重）。
var ontologyTurnStages = map[string][]string{
	OntologyTurnProject: {"S0", "S1"},
	OntologyTurnPlan:    {"S0", "S2", "S3"},
	OntologyTurnDirect:  {"S4", "S5"},
	OntologyTurnUnit:    {"S6", "S7"},
	OntologyTurnCheck:   {"S8", "S1"},
	OntologyTurnRepair:  {"S8", "S6", "S7"},
}

// OntologyRuntimeContext 是技能包 runtime-context.json。
type OntologyRuntimeContext struct {
	SchemaVersion int                 `json:"schemaVersion"`
	ExcludedPaths []string            `json:"excludedPaths"`
	Stages        map[string][]string `json:"stages"`
}

// OntologyStageFiles 是某种轮次要装载的技能文件路径（不截断、不读排除目录）。
func OntologyStageFiles(skill StepSkill, kind string) ([]string, error) {
	stages, ok := ontologyTurnStages[kind]
	if !ok {
		return nil, fmt.Errorf("本体论导演没有「%s」这种轮次", kind)
	}
	ctxFile := skill.file(StepPackageContextFile)
	if ctxFile == nil {
		return nil, errors.New("本体论导演技能缺少 runtime-context.json")
	}
	var rc OntologyRuntimeContext
	if err := json.Unmarshal([]byte(ctxFile.Content), &rc); err != nil || rc.SchemaVersion != 1 {
		return nil, errors.New("本体论导演技能的 runtime-context.json 无效")
	}
	var paths []string
	for _, stage := range stages {
		list, ok := rc.Stages[stage]
		if !ok {
			return nil, fmt.Errorf("runtime-context.json 缺少环节 %s", stage)
		}
		for _, p := range list {
			if slices.ContainsFunc(rc.ExcludedPaths, func(prefix string) bool { return strings.HasPrefix(p, prefix) }) {
				return nil, fmt.Errorf("环节 %s 点名了排除目录里的文件 %s", stage, p)
			}
			if !slices.Contains(paths, p) {
				paths = append(paths, p)
			}
		}
	}
	return paths, nil
}

// OntologySkillContext 拼出一轮的技能上下文：文件原文依次排好，每个文件一个标题。返回实际装载的文件（写进任务 metadata）。
func OntologySkillContext(skill StepSkill, kind string) (string, []StepSkillFileUse, error) {
	paths, err := OntologyStageFiles(skill, kind)
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	b.WriteString(stepSkillPreamble)
	uses := make([]StepSkillFileUse, 0, len(paths))
	for _, p := range paths {
		f := skill.file(p)
		if f == nil || f.Binary {
			return "", nil, fmt.Errorf("本体论导演技能缺少文件 %s", p)
		}
		fmt.Fprintf(&b, "\n\n<skill-file path=%q>\n%s\n</skill-file>", p, f.Content)
		uses = append(uses, StepSkillFileUse{Path: p, SHA256: f.SHA256})
	}
	return b.String(), uses, nil
}

// OntologyTurnInstruction 是每种轮次的任务说明（接在技能上下文和材料包后面）。unit 只在单元轮、返修轮用。
func OntologyTurnInstruction(kind string, unit string) string {
	switch kind {
	case OntologyTurnProject:
		return "【本轮任务】这是立项回合。按 S0、S1 执行，依次输出 `## 世界圣经卡`、`## 待确认清单`；缺核心人物或核心场景参考图时再输出 `## 补图建议`。不要输出每集七部分。"
	case OntologyTurnPlan:
		return "【本轮任务】这是每集回合的第一轮。按 S0、S2、S3 执行，只输出这三个部分，标题一字不改：`## 一、本集定位`、`## 二、台词对照表`、`## 三、节奏总表`。节奏总表每个单元一行，第一列写单元编号 sXX-uN。"
	case OntologyTurnDirect:
		return "【本轮任务】按 S4、S5 执行。输出 `## 四、讲戏本`（标题一字不改），再输出 `## 氛围设计`（按场写光、色、空气、声、时，供后面的单元卡引用）。不要输出单元卡。"
	case OntologyTurnUnit:
		return fmt.Sprintf("【本轮任务】按 S6、S7 执行，只写单元 %s 的一张单元卡：先一行 `### 单元卡 %s｜总时长 N 秒（叙事 T 秒 + 交接 1 秒）｜N 镜｜节奏X档`，再 `【导演分镜卡】`，再 `【粘贴提示词】` 后用一个代码块（```）装九段粘贴提示词。只写这一个单元，不写其他单元，不写七部分的其他标题。", unit, unit)
	case OntologyTurnCheck:
		return "【本轮任务】按 S8 自检全部单元卡，并按 S1 写本集交接卡。只输出 `## 六、本集交接卡` 和 `## 七、自检结果与待确认清单` 两个部分，标题一字不改。自检表每个单元一行，硬失败的格写 ✗ 并在备注写原因。"
	case OntologyTurnRepair:
		return fmt.Sprintf("【本轮任务】这是返修回合，只返修单元 %s。按 S8 诊断，回到责任环节重写，再按 S7 编译。依次输出 `## 诊断`、`## 改法`、`## 重写后的单元卡`；重写后的单元卡格式与原单元卡相同（标题行、【导演分镜卡】、【粘贴提示词】代码块），单元编号不变。", unit)
	}
	return ""
}

// ---------------------------------------------------------------- 材料包

// OntologyMaterial 是材料包「素材清单」里的一项：@图Index = 资产的主图。VersionID 是柚子资产版本 ID（编译时当参考文件）。
type OntologyMaterial struct {
	Index     int    `json:"index"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Note      string `json:"note,omitempty"`
	VersionID string `json:"versionId"`
}

// OntologyBrief 是材料包「指定」部分。
type OntologyBrief struct {
	Genre          string `json:"genre,omitempty"`
	AspectRatio    string `json:"aspectRatio,omitempty"`
	Medium         string `json:"medium,omitempty"`
	EpisodeSeconds int    `json:"episodeSeconds,omitempty"`
	// MaxUnitSeconds 是平台硬约束：min(P01, 视频模型单段上限)。小于 30 时覆盖技能的 P01。
	MaxUnitSeconds int    `json:"maxUnitSeconds"`
	Extra          string `json:"extra,omitempty"`
	// Style 是项目已锁定的画风、全局风格锚和主基调色卡（OntologyStyleText 组好），每条提示词都要遵守。
	Style string `json:"style,omitempty"`
}

// OntologyStyleText 把项目锁定的画风说明、全局风格锚和主基调色卡写成材料包里的一段；都没有时为空。
func OntologyStyleText(stylePrompt string, anchor *StyleAnchor, palette MasterPalette) string {
	var parts []string
	if t := strings.TrimSpace(stylePrompt); t != "" {
		parts = append(parts, "画风："+t)
	}
	if t := strings.TrimSpace(styleAnchorPrompt(anchor, "全局风格锚（画面、光影、镜头语言和调色都按它写，禁用项一律不出现）：", true)); t != "" {
		parts = append(parts, t)
	}
	if !palette.Empty() {
		if t := strings.TrimSpace(MasterPalettePrompt(palette, "主基调色卡：")); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

// OntologyMaterialPackage 按技能 templates/01_材料包.md 组材料包。bible / handoff 为空时写"无"。
func OntologyMaterialPackage(brief OntologyBrief, materials []OntologyMaterial, bible string, handoff string) string {
	var b strings.Builder
	b.WriteString("【材料包】\n\n【指定】\n")
	fmt.Fprintf(&b, "片种：%s\n画幅：%s\n媒介轨：%s\n", firstNonBlank(brief.Genre, "短剧"), aspectLabel(brief.AspectRatio), firstNonBlank(brief.Medium, "写实"))
	if brief.EpisodeSeconds > 0 {
		fmt.Fprintf(&b, "每集时长：约%d秒\n", brief.EpisodeSeconds)
	}
	b.WriteString("背景音乐：不要\n站位交接镜头：开启（默认）\n节奏：短剧（默认）\n")
	if brief.MaxUnitSeconds > 0 && brief.MaxUnitSeconds < OntologyUnitMaxSeconds {
		fmt.Fprintf(&b, "平台硬约束覆盖：本项目视频模型一次最多生成 %d 秒，P01 单元总时长上限按 %d 秒执行（含 1 秒站位交接），P11、P13 随之下调。\n", brief.MaxUnitSeconds, brief.MaxUnitSeconds)
	}
	if strings.TrimSpace(brief.Extra) != "" {
		fmt.Fprintf(&b, "其他要求：%s\n", strings.TrimSpace(brief.Extra))
	}
	if strings.TrimSpace(brief.Style) != "" {
		fmt.Fprintf(&b, "\n【项目画风（已锁定的硬性视觉约束，写进每条粘贴提示词的全局设定）】\n%s\n", strings.TrimSpace(brief.Style))
	}
	b.WriteString("\n【素材清单】\n")
	if len(materials) == 0 {
		b.WriteString("无\n")
	}
	for _, m := range materials {
		line := fmt.Sprintf("@图%d = %s", m.Index, m.Name)
		if note := strings.TrimSpace(firstNonBlank(m.Note, categoryLabel(m.Category))); note != "" {
			line += " " + note
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("参考音频、参考视频：无（粘贴提示词不得出现 @音频、@视频）\n")
	b.WriteString("\n【世界圣经卡】\n" + firstNonBlank(strings.TrimSpace(bible), "无") + "\n")
	b.WriteString("\n【集交接卡】\n" + firstNonBlank(strings.TrimSpace(handoff), "无") + "\n")
	return b.String()
}

func aspectLabel(ratio string) string {
	switch strings.TrimSpace(ratio) {
	case "16:9":
		return "横屏16:9"
	case "", "9:16":
		return "竖屏9:16"
	default:
		return ratio
	}
}

func categoryLabel(category string) string {
	switch category {
	case "character":
		return "人物"
	case "environment":
		return "场景"
	case "prop":
		return "道具"
	}
	return ""
}

func ontologyRefRole(category string) string {
	switch category {
	case "environment":
		return "location"
	case "prop":
		return "prop"
	}
	return "identity"
}

// ---------------------------------------------------------------- 输出解析

var (
	ontologyH2         = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)
	ontologyUnitHeader = regexp.MustCompile(`(?m)^###\s*单元卡\s*【?(s\d{2,3}-u\d+[a-z]?)】?\s*[｜|]\s*总时长\s*【?([0-9.]+)】?\s*秒(?:（叙事\s*【?([0-9.]+)】?\s*秒[^）]*）)?\s*[｜|]\s*【?(\d+)】?\s*镜\s*[｜|]\s*节奏\s*【?([^\s|｜】]+?)】?档?\s*$`)
	ontologyUnitID     = regexp.MustCompile(`s(\d{2,3})-u\d+[a-z]?`)
	ontologyImageTag   = regexp.MustCompile(`@图(?:片)?(\d+)`)
	ontologyFence      = regexp.MustCompile("(?s)```[^\\n]*\\n(.*?)\\n?```")
	ontologyShotRow    = regexp.MustCompile(`^\|\s*(\d+)\s*\|\s*【?([0-9.]+)\s*[–\-~～至]\s*([0-9.]+)\s*秒?】?\s*\|`)
)

// OntologySection 取 `## 标题` 到下一个二级标题之间的正文。title 按前缀匹配（"一、本集定位"）。
func OntologySection(text string, title string) (string, bool) {
	locs := ontologyH2.FindAllStringSubmatchIndex(text, -1)
	for i, loc := range locs {
		heading := strings.TrimSpace(text[loc[2]:loc[3]])
		if !strings.HasPrefix(heading, title) {
			continue
		}
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		return strings.TrimSpace(text[loc[1]:end]), true
	}
	return "", false
}

// OntologyRequireSections 检查各标题都在（解析失败按临时失败重试，所以错误要写清缺哪个）。
func OntologyRequireSections(text string, titles ...string) error {
	var missing []string
	for _, t := range titles {
		if _, ok := OntologySection(text, t); !ok {
			missing = append(missing, "## "+t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少固定标题：%s", strings.Join(missing, "、"))
	}
	return nil
}

// OntologyRhythmUnits 从节奏总表取单元编号（按表中顺序，去重）。
func OntologyRhythmUnits(plan string) ([]string, error) {
	table, ok := OntologySection(plan, "三、节奏总表")
	if !ok {
		return nil, errors.New("缺少固定标题：## 三、节奏总表")
	}
	var units []string
	for _, line := range strings.Split(table, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) == 0 {
			continue
		}
		id := ontologyUnitID.FindString(strings.TrimSpace(cells[0]))
		if id != "" && !slices.Contains(units, id) {
			units = append(units, id)
		}
	}
	if len(units) == 0 {
		return nil, errors.New("节奏总表里没有单元行（第一列应是 sXX-uN）")
	}
	if len(units) > OntologyMaxUnits {
		return nil, fmt.Errorf("节奏总表有 %d 个单元，超过一集上限 %d：按 R4 同场合并、把单元时长用满后重排", len(units), OntologyMaxUnits)
	}
	return units, nil
}

// OntologyUnitCard 是一张解析好的单元卡。
type OntologyUnitCard struct {
	ID               string  `json:"id"`
	Scene            string  `json:"scene"`
	TotalSeconds     float64 `json:"totalSeconds"`
	NarrativeSeconds float64 `json:"narrativeSeconds,omitempty"`
	Shots            int     `json:"shots"`
	Tempo            string  `json:"tempo"`
	// Card 是【导演分镜卡】正文，Paste 是【粘贴提示词】代码块里的原文（素材编号仍是材料包的全集编号）。
	Card  string `json:"card"`
	Paste string `json:"paste"`
	// Text 是这张卡的完整原文（标题行起），拼七部分用。
	Text string `json:"text"`
	// Fenced 表示粘贴提示词放在代码块里（契约要求）。
	Fenced bool `json:"-"`
}

// ontologyTableRow 是镜头表的一行：镜头号、起止秒。
type ontologyTableRow struct {
	N          int
	Start, End float64
}

// ParseOntologyUnitCards 解析文本里的全部单元卡。
func ParseOntologyUnitCards(text string) ([]OntologyUnitCard, error) {
	locs := ontologyUnitHeader.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		return nil, errors.New("没有找到单元卡标题行（### 单元卡 sXX-uN｜总时长 N 秒（叙事 T 秒 + 交接 1 秒）｜N 镜｜节奏X档）")
	}
	cards := make([]OntologyUnitCard, 0, len(locs))
	for i, loc := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		// 下一个二级标题（七部分的其他部分）也是这张卡的终点。
		if next := ontologyH2.FindStringIndex(text[loc[1]:end]); next != nil {
			end = loc[1] + next[0]
		}
		sub := func(k int) string {
			if loc[2*k] < 0 {
				return ""
			}
			return text[loc[2*k]:loc[2*k+1]]
		}
		body := text[loc[1]:end]
		card := OntologyUnitCard{ID: sub(1), Tempo: sub(5), Text: strings.TrimSpace(text[loc[0]:end])}
		card.Scene = "s" + ontologyUnitID.FindStringSubmatch(card.ID)[1]
		card.TotalSeconds, _ = strconv.ParseFloat(sub(2), 64)
		card.NarrativeSeconds, _ = strconv.ParseFloat(sub(3), 64)
		card.Shots, _ = strconv.Atoi(sub(4))
		cardAt := strings.Index(body, "【导演分镜卡】")
		pasteAt := strings.Index(body, "【粘贴提示词】")
		if cardAt < 0 || pasteAt < 0 || pasteAt < cardAt {
			return nil, fmt.Errorf("单元卡 %s 缺少【导演分镜卡】或【粘贴提示词】", card.ID)
		}
		card.Card = strings.TrimSpace(body[cardAt+len("【导演分镜卡】") : pasteAt])
		paste := body[pasteAt+len("【粘贴提示词】"):]
		if m := ontologyFence.FindStringSubmatch(paste); m != nil {
			card.Paste, card.Fenced = strings.TrimSpace(m[1]), true
		} else {
			card.Paste = strings.TrimSpace(paste)
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// ---------------------------------------------------------------- 校验

// OntologyUnitLimits 是校验单元卡用的边界。
type OntologyUnitLimits struct {
	MaxSeconds int
	Materials  int
	MaxImages  int
}

var ontologyForbiddenInPaste = []string{"同上", "接上一镜", "@音频", "@视频"}

// ValidateOntologyUnit 按 R4/R5/R9/R11 与平台约束校验一张单元卡；返回的错误会作为本轮输出修正意见回给模型。
func ValidateOntologyUnit(card OntologyUnitCard, want string, lim OntologyUnitLimits) error {
	var problems []string
	if want != "" && card.ID != want {
		problems = append(problems, fmt.Sprintf("单元编号应为 %s，写成了 %s", want, card.ID))
	}
	maxSeconds := lim.MaxSeconds
	if maxSeconds <= 0 || maxSeconds > OntologyUnitMaxSeconds {
		maxSeconds = OntologyUnitMaxSeconds
	}
	if card.TotalSeconds <= 0 || card.TotalSeconds > float64(maxSeconds) {
		problems = append(problems, fmt.Sprintf("总时长 %s 秒，应在 0–%d 秒之间（含 1 秒站位交接）", secText(card.TotalSeconds), maxSeconds))
	}
	if card.Shots <= 0 {
		problems = append(problems, "标题行的镜头数无效")
	}
	if !card.Fenced {
		problems = append(problems, "【粘贴提示词】必须放在一个代码块（```）里")
	}
	if err := ontologyCheckTable(card); err != nil {
		problems = append(problems, err.Error())
	}
	if OntologyUnfinished(card.Text) {
		problems = append(problems, "这张卡没写完（出现了未完续写标记），本轮必须一次写完整张卡")
	}
	paste := card.Paste
	chars := len([]rune(paste))
	switch {
	case strings.TrimSpace(paste) == "":
		problems = append(problems, "【粘贴提示词】为空")
	case chars > OntologyPromptMaxChars:
		problems = append(problems, fmt.Sprintf("粘贴提示词 %d 字符，超过上限 %d", chars, OntologyPromptMaxChars))
	}
	if !strings.Contains(paste, OntologyNoMusicLine) {
		problems = append(problems, "粘贴提示词没有写「"+OntologyNoMusicLine+"」")
	}
	if ontologyUnitID.MatchString(paste) {
		problems = append(problems, "粘贴提示词里出现了场次或单元编号")
	}
	for _, w := range ontologyForbiddenInPaste {
		if strings.Contains(paste, w) {
			problems = append(problems, "粘贴提示词里出现了「"+w+"」")
		}
	}
	used := OntologyImageIndexes(paste)
	for _, n := range used {
		if n < 1 || n > lim.Materials {
			problems = append(problems, fmt.Sprintf("粘贴提示词引用了 @图%d，素材清单里没有这一项", n))
		}
	}
	if lim.MaxImages > 0 && len(used) > lim.MaxImages {
		problems = append(problems, fmt.Sprintf("这一单元用了 %d 张参考图，超过视频模型上限 %d 张", len(used), lim.MaxImages))
	}
	if len(problems) > 0 {
		return fmt.Errorf("单元卡 %s：%s", firstNonBlank(card.ID, want), strings.Join(problems, "；"))
	}
	return nil
}

// OntologyImageIndexes 是提示词里出现的 @图N（按第一次出现的顺序，去重）。@图片N 视同 @图N。
func OntologyImageIndexes(text string) []int {
	var out []int
	for _, m := range ontologyImageTag.FindAllStringSubmatch(text, -1) {
		n, err := strconv.Atoi(m[1])
		if err == nil && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// ---------------------------------------------------------------- 单元卡 → 分段

// OntologySegment 把单元卡装成一段：粘贴提示词原文存在 Verbatim（素材编号按本段实际用到的图重排为 1..k，与 Refs 顺序一致），
// 镜头表里的镜头转成段内镜头（只供分镜卡显示、时长与场次判断，不参与编译）。order 从 1 数，段号 S01、S02……
func OntologySegment(card OntologyUnitCard, order int, materials []OntologyMaterial) (Segment, error) {
	byIndex := map[int]OntologyMaterial{}
	for _, m := range materials {
		byIndex[m.Index] = m
	}
	used := OntologyImageIndexes(card.Paste)
	remap := map[int]int{}
	refs := make([]Ref, 0, len(used))
	for i, n := range used {
		m, ok := byIndex[n]
		if !ok || strings.TrimSpace(m.VersionID) == "" {
			return Segment{}, fmt.Errorf("单元卡 %s 引用的 @图%d 不在素材清单里", card.ID, n)
		}
		remap[n] = i + 1
		refs = append(refs, Ref{File: m.VersionID, Role: ontologyRefRole(m.Category), Note: m.Name})
	}
	verbatim := ontologyImageTag.ReplaceAllStringFunc(card.Paste, func(tag string) string {
		n, _ := strconv.Atoi(ontologyImageTag.FindStringSubmatch(tag)[1])
		return fmt.Sprintf("@图%d", remap[n])
	})
	shots, err := ontologyParseShots(card)
	if err != nil {
		return Segment{}, fmt.Errorf("单元卡 %s：%w", card.ID, err)
	}
	seg := Segment{
		ID: fmt.Sprintf("S%02d", order), Scene: card.Scene, Title: card.ID, Dur: card.TotalSeconds,
		Mode: ModeTextOnly, Refs: refs, Shots: shots, Verbatim: verbatim, Lang: "zh",
	}
	if len(refs) > 0 {
		seg.Mode = ModeLockIdentity
	}
	if err := seg.Validate(); err != nil {
		return Segment{}, err
	}
	return seg, nil
}

// ontologyShotHeaders 是镜头表第一列可接受的表头写法。
var ontologyShotHeaders = []string{"镜头", "镜号", "镜头号"}

// ontologyParseShots 读【导演分镜卡】镜头表：表头必须认得出（第一列镜头、有时间列和画面列），每一条带时间的镜头行都必须
// 转成段内镜头。校验和装段共用这一份解析，认不出时报错，不退回整卡一镜。
func ontologyParseShots(card OntologyUnitCard) ([]SubShot, error) {
	var header []string
	var shots []SubShot
	rows := 0
	for _, line := range strings.Split(card.Card, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) > 0 && slices.Contains(ontologyShotHeaders, cells[0]) {
			header = cells
			continue
		}
		m := ontologyShotRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rows++
		if header == nil {
			return nil, errors.New("镜头表缺少表头（第一列写「镜头」，并有「时间」「动作与表情」列）")
		}
		col := func(name string) string {
			if i := slices.Index(header, name); i >= 0 && i < len(cells) {
				return strings.Trim(cells[i], "【】")
			}
			return ""
		}
		at, _ := strconv.ParseFloat(m[2], 64)
		text := firstNonBlank(col("动作与表情"), col("戏剧职责"), col("第一注意力"))
		if text == "" {
			return nil, fmt.Errorf("镜头 %s 没有「动作与表情」（或戏剧职责）内容", m[1])
		}
		if n := len(shots); n > 0 && at <= shots[n-1].At {
			return nil, fmt.Errorf("镜头 %s 的开始秒不大于上一镜", m[1])
		}
		shots = append(shots, SubShot{At: math.Max(at, 0), Size: col("景别"), Camera: strings.Trim(col("机位")+" "+col("运镜"), " "), Text: text, Purpose: col("戏剧职责"), Composition: col("构图")})
	}
	if header != nil && !slices.Contains(header, "时间") {
		return nil, errors.New("镜头表缺少「时间」列")
	}
	if rows == 0 || len(shots) != rows {
		return nil, errors.New("【导演分镜卡】里没有读得出的镜头表（| 镜头 | 时间 | … |，时间写成 0–2 秒）")
	}
	return shots, nil
}

// CompileVerbatim 是本体论段的编译：提示词就是粘贴提示词原文，只把 @图k 换成模型的参考图编号写法；参考文件按 Refs 顺序。
func CompileVerbatim(seg Segment, m Model) CompileResult {
	prompt := ontologyImageTag.ReplaceAllStringFunc(seg.Verbatim, func(tag string) string {
		n, _ := strconv.Atoi(ontologyImageTag.FindStringSubmatch(tag)[1])
		return ReferenceImageTag(m, n)
	})
	files := make([]CompiledFile, 0, len(seg.Refs))
	for i, r := range seg.Refs {
		files = append(files, CompiledFile{File: r.File, Tag: ReferenceImageTag(m, i+1), Role: r.Role})
	}
	issues := Issues(seg, m)
	if issues == nil {
		issues = []Issue{}
	}
	chars := len([]rune(prompt))
	if chars > OntologyPromptMaxChars {
		issues = append(issues, Issue{"error", fmt.Sprintf("%s 粘贴提示词 %d 字符，超过上限 %d", seg.ID, chars, OntologyPromptMaxChars)})
	}
	if limit := m.PromptMaxChars; limit != nil && chars > *limit {
		issues = append(issues, Issue{"error", fmt.Sprintf("%s 提示词 %d 字符，超过 %s 的上限 %d", seg.ID, chars, m.Label, *limit)})
	}
	result := CompileResult{Segment: seg.ID, Model: m.ID, Duration: SubmitDuration(seg.Dur, m), Prompt: prompt, Files: files, Chars: chars, Warnings: []string{}, Issues: issues}
	result.Paste = prompt
	if m.Dialect() == "seedance" {
		result.Paste = PasteVersion(result)
	}
	return result
}

// OntologyEpisodeDocument 把各轮产物拼成技能规定的七部分（标题一字不改，缺的部分写"本集无"）。
func OntologyEpisodeDocument(plan, direct string, cards []OntologyUnitCard, check string) string {
	part := func(text, title string) string {
		body, ok := OntologySection(text, title)
		if !ok || body == "" {
			body = "本集无"
		}
		return "## " + title + "\n" + body
	}
	unitTexts := make([]string, 0, len(cards))
	for _, c := range cards {
		unitTexts = append(unitTexts, c.Text)
	}
	units := strings.Join(unitTexts, "\n\n")
	if units == "" {
		units = "本集无"
	}
	return strings.Join([]string{
		part(plan, "一、本集定位"), part(plan, "二、台词对照表"), part(plan, "三、节奏总表"), part(direct, "四、讲戏本"),
		"## 五、单元卡\n" + units, part(check, "六、本集交接卡"), part(check, "七、自检结果与待确认清单"),
	}, "\n\n") + "\n"
}

// OntologyHardFailures 读自检表：某单元某个 H 列写了 ✗ 的，返回 "sXX-uN H3" 这样的条目。
func OntologyHardFailures(check string) []string {
	table, ok := OntologySection(check, "七、自检结果与待确认清单")
	if !ok {
		return nil
	}
	var header []string
	var out []string
	for _, line := range strings.Split(table, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) > 1 && cells[0] == "单元" && cells[1] == "H1" {
			header = cells
			continue
		}
		if header == nil || strings.Trim(cells[0], "-: ") == "" {
			continue // 表外的行，或表头下的分隔行
		}
		if cells[0] == "编号" {
			header = nil // 自检表结束，进入待确认清单表
			continue
		}
		id := ontologyUnitID.FindString(cells[0])
		if id == "" {
			continue
		}
		for i := 1; i < len(cells) && i < len(header); i++ {
			if strings.HasPrefix(header[i], "H") && strings.ContainsAny(cells[i], "✗×✘") {
				out = append(out, id+" "+header[i])
			}
		}
	}
	return out
}

// ontologyTableRows 读【导演分镜卡】镜头表的镜头号和起止秒。
func ontologyTableRows(card OntologyUnitCard) []ontologyTableRow {
	var rows []ontologyTableRow
	for _, line := range strings.Split(card.Card, "\n") {
		m := ontologyShotRow.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		start, _ := strconv.ParseFloat(m[2], 64)
		end, _ := strconv.ParseFloat(m[3], 64)
		rows = append(rows, ontologyTableRow{N: n, Start: start, End: end})
	}
	return rows
}

// ontologyCheckTable 校验镜头表与标题行一致：行数是叙事镜头数（或再加 1 个站位交接镜头），镜头号从 1 连续，时间从 0 起首尾相接，
// 最后一镜在总时长结束。
func ontologyCheckTable(card OntologyUnitCard) error {
	if _, err := ontologyParseShots(card); err != nil {
		return err
	}
	rows := ontologyTableRows(card)
	if len(rows) != card.Shots && len(rows) != card.Shots+1 {
		return fmt.Errorf("镜头表有 %d 行，标题行写的是 %d 镜（不含站位交接镜头）", len(rows), card.Shots)
	}
	const eps = 0.11
	for i, r := range rows {
		switch {
		case r.N != i+1:
			return fmt.Errorf("镜头表第 %d 行的镜头号是 %d，应从 1 连续编号", i+1, r.N)
		case r.End <= r.Start:
			return fmt.Errorf("镜头 %d 的结束秒不大于开始秒", r.N)
		case i == 0 && math.Abs(r.Start) > eps:
			return errors.New("镜头 1 应从 0 秒开始")
		case i > 0 && math.Abs(r.Start-rows[i-1].End) > eps:
			return fmt.Errorf("镜头 %d 从 %s 秒开始，与上一镜结束的 %s 秒接不上", r.N, secText(r.Start), secText(rows[i-1].End))
		}
	}
	if last := rows[len(rows)-1].End; math.Abs(last-card.TotalSeconds) > 0.51 {
		return fmt.Errorf("最后一镜在 %s 秒结束，标题行的总时长是 %s 秒", secText(last), secText(card.TotalSeconds))
	}
	return nil
}

// ontologyUnfinishedPattern 是技能 5.7「未完，回复继续」及常见变体：回复(引号)继续、未完……继续、【未完、未完待续、待续）。
var ontologyUnfinishedPattern = regexp.MustCompile(`回复\s*[“"「『'‘]?\s*继续|未完[^。\n]{0,20}继续|【\s*未完|未完待续|[（(【]\s*待续`)

// OntologyUnfinished 判断输出里有没有技能的「未完，回复继续」续写标记。服务端按单元分轮，每轮都要求一次写完；出现标记按不合格重试。
func OntologyUnfinished(text string) bool {
	return ontologyUnfinishedPattern.MatchString(text)
}

// ontologyCheckMarks 是自检表一格可以写的结果：通过、硬失败、不适用。
var ontologyCheckMarks = map[string]bool{"✓": true, "√": true, "✔": true, "✗": true, "×": true, "✘": true, "—": true, "-": true, "不适用": true}

// ValidateOntologyCheck 校验质检轮：交接卡有正文，自检表有 H1–H11 列，每个单元一行。
func ValidateOntologyCheck(text string, units []string) error {
	if err := OntologyRequireSections(text, "六、本集交接卡", "七、自检结果与待确认清单"); err != nil {
		return err
	}
	if body, _ := OntologySection(text, "六、本集交接卡"); strings.TrimSpace(body) == "" || strings.TrimSpace(body) == "本集无" {
		return errors.New("本集交接卡为空")
	}
	table, _ := OntologySection(text, "七、自检结果与待确认清单")
	inTable, header := false, false
	seen := map[string]bool{}
	for _, line := range strings.Split(table, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			inTable = false
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) > 1 && cells[0] == "单元" && cells[1] == "H1" {
			// 表头必须依次是 单元、H1…H11（后面可以再有表演分、字符数、备注）。
			for i := 1; i <= 11; i++ {
				if i >= len(cells) || cells[i] != fmt.Sprintf("H%d", i) {
					return errors.New("自检表表头必须依次是 | 单元 | H1 | H2 | … | H11 |")
				}
			}
			inTable, header = true, true
			continue
		}
		if !inTable || strings.Trim(cells[0], "-: ") == "" {
			continue
		}
		id := ontologyUnitID.FindString(cells[0])
		if id == "" {
			return fmt.Errorf("自检表里有认不出单元编号的行：%s", cells[0])
		}
		if seen[id] {
			return fmt.Errorf("自检表里单元 %s 出现了两次", id)
		}
		if !slices.Contains(units, id) {
			return fmt.Errorf("自检表里的单元 %s 不在节奏总表里", id)
		}
		seen[id] = true
		for i := 1; i <= 11; i++ {
			if i >= len(cells) || !ontologyCheckMarks[strings.Trim(cells[i], "【】 ")] {
				return fmt.Errorf("自检表单元 %s 的 H%d 没写结果（用 ✓ 通过、✗ 硬失败、— 不适用）", id, i)
			}
		}
	}
	if !header {
		return errors.New("自检表缺少 | 单元 | H1 | … | H11 | 表头")
	}
	var missing []string
	for _, u := range units {
		if !seen[u] {
			missing = append(missing, u)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("自检表缺少这些单元的行：%s", strings.Join(missing, "、"))
	}
	return nil
}
