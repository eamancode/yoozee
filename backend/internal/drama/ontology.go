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
	Genre          string
	AspectRatio    string
	Medium         string
	EpisodeSeconds int
	// MaxUnitSeconds 是平台硬约束：min(P01, 视频模型单段上限)。小于 30 时覆盖技能的 P01。
	MaxUnitSeconds int
	Extra          string
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
			card.Paste = strings.TrimSpace(m[1])
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
	seg := Segment{
		ID: fmt.Sprintf("S%02d", order), Scene: card.Scene, Title: card.ID, Dur: card.TotalSeconds,
		Mode: ModeTextOnly, Refs: refs, Shots: ontologySubShots(card), Verbatim: verbatim, Lang: "zh",
	}
	if len(refs) > 0 {
		seg.Mode = ModeLockIdentity
	}
	if err := seg.Validate(); err != nil {
		return Segment{}, err
	}
	return seg, nil
}

// ontologySubShots 读【导演分镜卡】镜头表：镜头号、时间、景别、机位、运镜、动作与表情。读不出时退回一镜（全卡文字）。
func ontologySubShots(card OntologyUnitCard) []SubShot {
	var header []string
	var shots []SubShot
	for _, line := range strings.Split(card.Card, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) > 0 && cells[0] == "镜头" {
			header = cells
			continue
		}
		m := ontologyShotRow.FindStringSubmatch(line)
		if m == nil || header == nil {
			continue
		}
		at, _ := strconv.ParseFloat(m[2], 64)
		col := func(name string) string {
			if i := slices.Index(header, name); i >= 0 && i < len(cells) {
				return strings.Trim(cells[i], "【】")
			}
			return ""
		}
		text := firstNonBlank(col("动作与表情"), col("戏剧职责"), col("第一注意力"))
		if text == "" {
			continue
		}
		at = math.Max(at, 0)
		if n := len(shots); n > 0 && at <= shots[n-1].At {
			continue
		}
		shots = append(shots, SubShot{At: at, Size: col("景别"), Camera: strings.Trim(col("机位")+" "+col("运镜"), " "), Text: text, Purpose: col("戏剧职责"), Composition: col("构图")})
	}
	if len(shots) == 0 {
		shots = []SubShot{{At: 0, Text: firstNonBlank(card.Card, card.ID)}}
	}
	return shots
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
