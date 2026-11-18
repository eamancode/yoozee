package drama

// SSS 导演分镜（流水线一电影、流水线二短剧）：技能 yz-sss-film-director / yz-sss-drama-director 按 runtime-context.json 分轮装载，
// 服务端分轮调用。本文件是纯函数：轮次上下文拼装、材料包、各轮输出的解析与校验、单元卡 → 分段（粘贴提示词里的 @名称 换成 @图k）。
// 运行编排、写库在 app/project_sss.go。

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	SSSKindFilm  = "film"
	SSSKindDrama = "drama"
	// ContractSSSDirector 是 SSS 导演分镜技能包声明的输出契约（分轮装载，见 runtime-context.json）。
	ContractSSSDirector = "sss.director@1"
	// SSSPromptMaxChars 是参数.md「提示词长度上限」。
	SSSPromptMaxChars = 15000
	// SSSUnitMaxSeconds 是参数.md「平台上限」的默认值（含同场景切开时最后 1 秒全景定格）。
	SSSUnitMaxSeconds = 30
	// SSSNoMusicLine 是手册/10 声音段的最后一句。
	SSSNoMusicLine = "本段不使用背景音乐"
	// SSSMaxUnits 是一集最多几个单元（防止单轮上下文和结果无界增长）。
	SSSMaxUnits = 16
	// SSSStageBudgetChars 是一轮装载的技能文件字数上限（包上传时校验；启发库按单元另装，不计在内）。
	SSSStageBudgetChars = 70000
	// SSSStageMaxFiles 是一轮最多装几个技能文件（不含 SKILL.md）。
	SSSStageMaxFiles = 6
)

// SSSDirectorSkill / SSSScriptSkill / SSSStyleSkill 是两种片种的内置技能 ID。
func SSSDirectorSkill(kind string) string { return "yz-sss-" + kind + "-director" }
func SSSScriptSkill(kind string) string   { return "yz-sss-" + kind + "-script" }
func SSSStyleSkill(kind string) string    { return "yz-sss-" + kind + "-style" }

// SSSKindLabel 是片种的中文名（材料包「片种」）。
func SSSKindLabel(kind string) string {
	if kind == SSSKindFilm {
		return "电影"
	}
	return "短剧"
}

// 一轮的种类。立项只在项目还没有全剧圣经时跑；每集依次是 规划 → 讲戏 → 切分 → 每个单元两轮（单元卡、编译）→ 复看；
// 返修是 返修（诊断、重写单元卡）→ 编译。
const (
	SSSTurnSetup   = "setup"
	SSSTurnPlan    = "plan"
	SSSTurnDirect  = "direct"
	SSSTurnCut     = "cut"
	SSSTurnCard    = "card"
	SSSTurnCompile = "compile"
	SSSTurnReview  = "review"
	SSSTurnRepair  = "repair"
)

// SSSTurnKinds 是 runtime-context.json 必须列出的轮次。
var SSSTurnKinds = []string{SSSTurnSetup, SSSTurnPlan, SSSTurnDirect, SSSTurnCut, SSSTurnCard, SSSTurnCompile, SSSTurnReview, SSSTurnRepair}

// SSSRuntimeContext 是技能包 runtime-context.json。
type SSSRuntimeContext struct {
	SchemaVersion int                 `json:"schemaVersion"`
	ExcludedPaths []string            `json:"excludedPaths"`
	Heuristics    SSSHeuristics       `json:"heuristics"`
	Stages        map[string][]string `json:"stages"`
}

// SSSHeuristics 是启发库：总索引在切分轮装载，各库文件按单元选用的库号整份装载。
type SSSHeuristics struct {
	Dir        string `json:"dir"`
	Index      string `json:"index"`
	MaxPerUnit int    `json:"maxPerUnit"`
}

// ReadSSSRuntimeContext 读并校验技能包的分轮装载清单。
func ReadSSSRuntimeContext(skill StepSkill) (SSSRuntimeContext, error) {
	f := skill.file(StepPackageContextFile)
	if f == nil {
		return SSSRuntimeContext{}, errors.New("SSS 导演技能缺少 runtime-context.json")
	}
	var rc SSSRuntimeContext
	if err := json.Unmarshal([]byte(f.Content), &rc); err != nil || rc.SchemaVersion != 1 || rc.Stages == nil {
		return SSSRuntimeContext{}, errors.New("SSS 导演技能的 runtime-context.json 无效")
	}
	if rc.Heuristics.Dir == "" || rc.Heuristics.MaxPerUnit <= 0 {
		return SSSRuntimeContext{}, errors.New("SSS 导演技能的 runtime-context.json 缺少启发库配置")
	}
	return rc, nil
}

// SSSStageFiles 是某种轮次要装载的技能文件路径（不截断、不读排除目录）。
func SSSStageFiles(rc SSSRuntimeContext, kind string) ([]string, error) {
	list, ok := rc.Stages[kind]
	if !ok {
		return nil, fmt.Errorf("SSS 导演技能的 runtime-context.json 缺少轮次 %s", kind)
	}
	for _, p := range list {
		if slices.ContainsFunc(rc.ExcludedPaths, func(prefix string) bool { return strings.HasPrefix(p, prefix) }) {
			return nil, fmt.Errorf("轮次 %s 点名了排除目录里的文件 %s", kind, p)
		}
	}
	return list, nil
}

// SSSHeuristicFiles 把库号（A05、F01、E01……）换成启发库里的文件：文件名以「库号_」开头；E01 分上下两份时两份都装。
func SSSHeuristicFiles(skill StepSkill, rc SSSRuntimeContext, codes []string) ([]string, error) {
	var out []string
	for _, code := range codes {
		var hits []string
		for _, f := range skill.Files {
			if strings.HasPrefix(f.Path, rc.Heuristics.Dir) && strings.HasPrefix(path.Base(f.Path), code+"_") && f.Path != rc.Heuristics.Index {
				hits = append(hits, f.Path)
			}
		}
		if len(hits) == 0 {
			return nil, fmt.Errorf("启发库里没有库号 %s", code)
		}
		sort.Strings(hits)
		for _, h := range hits {
			if !slices.Contains(out, h) {
				out = append(out, h)
			}
		}
	}
	return out, nil
}

// SSSHeuristicCodes 是启发库里全部库号（按文件名排序；不含总索引）。
func SSSHeuristicCodes(skill StepSkill, rc SSSRuntimeContext) []string {
	var out []string
	for _, f := range skill.Files {
		if !strings.HasPrefix(f.Path, rc.Heuristics.Dir) || f.Path == rc.Heuristics.Index {
			continue
		}
		code, _, ok := strings.Cut(path.Base(f.Path), "_")
		if ok && !slices.Contains(out, code) {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}

// SSSSkillContext 拼出一轮的技能上下文：文件原文依次排好，每个文件一个标题；extra 是这一轮另装的启发库文件。返回实际装载的文件。
func SSSSkillContext(skill StepSkill, kind string, extra []string) (string, []StepSkillFileUse, error) {
	rc, err := ReadSSSRuntimeContext(skill)
	if err != nil {
		return "", nil, err
	}
	paths, err := SSSStageFiles(rc, kind)
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	b.WriteString(stepSkillPreamble)
	uses := make([]StepSkillFileUse, 0, len(paths)+len(extra))
	for _, p := range append(slices.Clone(paths), extra...) {
		f := skill.file(p)
		if f == nil || f.Binary {
			return "", nil, fmt.Errorf("SSS 导演技能缺少文件 %s", p)
		}
		content := strings.NewReplacer(
			"与场景参考图100%一致", "按平台提供的本场场景图和已核对的空间事实执行",
			"与场景参考图 100% 一致", "按平台提供的本场场景图和已核对的空间事实执行",
		).Replace(f.Content)
		fmt.Fprintf(&b, "\n\n<skill-file path=%q>\n%s\n</skill-file>", p, content)
		uses = append(uses, StepSkillFileUse{Path: p, SHA256: f.SHA256})
	}
	return b.String(), uses, nil
}

// ---------------------------------------------------------------- 材料包

// SSSMaterial 是材料包「素材清单」里的一项：@Name = 资产的主图。Index 是素材清单序号（存档、返修用），VersionID 是柚子资产版本 ID。
type SSSMaterial struct {
	Index     int    `json:"index"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Note      string `json:"note,omitempty"`
	VersionID string `json:"versionId"`
}

// SSSBrief 是材料包「指定」部分。
type SSSBrief struct {
	Kind           string `json:"kind"`
	AspectRatio    string `json:"aspectRatio,omitempty"`
	Medium         string `json:"medium,omitempty"`
	EpisodeSeconds int    `json:"episodeSeconds,omitempty"`
	// MaxUnitSeconds 是平台硬约束：min(参数.md 平台上限 30 秒, 视频模型单段上限)。
	MaxUnitSeconds int `json:"maxUnitSeconds"`
	// MaxImages 是视频模型一次最多带几张参考图（0 表示不限，仍受参数.md 30 张约束）。
	MaxImages int `json:"maxImages,omitempty"`
	// Style 是项目已锁定的画风、全局风格锚和主基调色卡。
	Style string `json:"style,omitempty"`
}

// SSSDocs 是材料包里由前面环节写好的文档：立意页与全集结构表（拆剧本）、世界与总谱（导演定画风）、全剧圣经、上一集交接卡。
type SSSDocs struct {
	Design  string `json:"design,omitempty"`
	Concept string `json:"concept,omitempty"`
	World   string `json:"world,omitempty"`
	Bible   string `json:"bible,omitempty"`
	Handoff string `json:"handoff,omitempty"`
}

// SSSMaterialPackage 组材料包：指定（对应 SKILL.md「开工前要知道的六件事」）、素材清单、前面环节的文档。
func SSSMaterialPackage(brief SSSBrief, materials []SSSMaterial, docs SSSDocs) string {
	var b strings.Builder
	b.WriteString("【材料包】\n\n【指定】（对应「开工前要知道的六件事」，都已确定，不用再列进待确认）\n")
	fmt.Fprintf(&b, "模式：%s模式\n", SSSKindLabel(brief.Kind))
	maxSeconds := brief.MaxUnitSeconds
	if maxSeconds <= 0 || maxSeconds > SSSUnitMaxSeconds {
		maxSeconds = SSSUnitMaxSeconds
	}
	fmt.Fprintf(&b, "1. 平台上限：视频模型单次最长生成 %d 秒（同场景切开时含最后 1 秒全景定格）", maxSeconds)
	if maxSeconds <= 15 {
		b.WriteString("，全剧只用短档")
	}
	b.WriteString("；平台固定输出 30 秒：关；按实际秒数计费。\n")
	if resolution, ok := SSSResolution(brief.AspectRatio); ok {
		fmt.Fprintf(&b, "2. 画幅：%s，分辨率 %s。\n", aspectLabel(brief.AspectRatio), resolution)
	} else {
		fmt.Fprintf(&b, "2. 画幅：%s（项目画幅；参数.md 只给了竖屏 9:16、横屏 16:9 的默认分辨率，这个画幅不写固定像素，输出设置里写画幅比例）。\n", aspectLabel(brief.AspectRatio))
	}
	fmt.Fprintf(&b, "3. 媒介：%s。\n", firstNonBlank(brief.Medium, "真人写实"))
	b.WriteString("4. 模型出声：会，对白、音效、环境声随画面一起生成；不生成背景音乐。\n")
	b.WriteString("5. 素材引用：命名标签 @名称，名称就是下面【素材清单】里 @ 后面的那串字，逐字照写，不另起名；")
	if brief.MaxImages > 0 {
		fmt.Fprintf(&b, "一个单元最多带 %d 张参考图；", brief.MaxImages)
	}
	b.WriteString("镜头表的台词/声音栏用「说话人（本句表演）：\"台词\"」标出实际对白。编译器按本段绑定的人物版本和这些对白自动关联已有声音样本、分配音频编号。草稿不自行填写 @音频、@某某声音 或 @视频。只有本轮任务指定「本单元要写 @站位参考」时，在生效参考素材中单独写一行「@站位参考：上一段最后一秒的全景定格画面；人物位置、朝向和持物以此为准」。其他单元不写该行。提示词按即梦十五段输出。\n")
	b.WriteString("6. 用户手里有：本集剧本（见【本集剧本】）、拆剧本写好的立意页与全集结构表、导演定画风写好的世界基准段与视听总谱、资产出图的参考图（见【素材清单】）。\n")
	if brief.EpisodeSeconds > 0 {
		fmt.Fprintf(&b, "每集目标时长：约 %d 秒。\n", brief.EpisodeSeconds)
	}
	if t := strings.TrimSpace(brief.Style); t != "" {
		fmt.Fprintf(&b, "\n【项目画风（已锁定的硬性视觉约束）】\n%s\n", t)
	}
	b.WriteString("\n【素材清单】（每一项关联到有图的资产。场景空间以平台提供的本场绑定和已核对事实为准。）\n")
	if len(materials) == 0 {
		b.WriteString("无（提示词里不写任何 @ 标签）\n")
	}
	for _, m := range materials {
		line := "@" + m.Name
		if note := strings.TrimSpace(firstNonBlank(m.Note, categoryLabel(m.Category))); note != "" {
			line += " " + note
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n【立意页与全集结构表】（拆剧本写的）\n" + firstNonBlank(strings.TrimSpace(docs.Concept), "无") + "\n")
	b.WriteString("\n【世界基准段与视听总谱】（导演定画风写的；世界基准段、时代锚句是锁，逐字搬）\n" + firstNonBlank(strings.TrimSpace(docs.World), "无") + "\n")
	b.WriteString("\n【全剧圣经】\n" + firstNonBlank(strings.TrimSpace(docs.Bible), "无") + "\n")
	b.WriteString("\n【上一集交接卡】\n" + firstNonBlank(strings.TrimSpace(docs.Handoff), "无（这是第一集，或上一集还没做）") + "\n")
	return b.String()
}

// SSSResolution 是参数.md 给的默认分辨率：只有竖屏 9:16（没设画幅时按竖屏）和横屏 16:9 有；其余画幅 ok=false，不写像素。
func SSSResolution(ratio string) (string, bool) {
	switch strings.TrimSpace(ratio) {
	case "16:9":
		return "3840×2160", true
	case "9:16", "":
		return "2160×3840", true
	}
	return "", false
}

// SSSStyleText 把项目锁定的画风说明、全局风格锚和主基调色卡写成材料包里的一段（同本体论）。
func SSSStyleText(stylePrompt string, anchor *StyleAnchor, palette MasterPalette) string {
	return OntologyStyleText(stylePrompt, anchor, palette)
}

// ---------------------------------------------------------------- 本轮任务

// SSSTurnInstruction 是每种轮次的任务说明（接在技能上下文和材料包后面）。unit 只在单元卡、编译、返修轮用。
func SSSTurnInstruction(kind string, unit string, codes []string) string {
	switch kind {
	case SSSTurnSetup:
		return "【本轮任务】这是立项回合的第三步（立意页、全集结构表、世界基准段与视听总谱前面的环节已经写好，在材料包里）。按手册/03 和模板/02 写全剧圣经：世界一节的世界基准段、时代锚句从【世界基准段与视听总谱】逐字搬进来，视觉总谱、声音总谱同样搬进来；人物卡、场景卡、关键道具卡按本集剧本和【素材清单】写，素材登记里的标签名就用素材清单里的名称。依次输出 `## 全剧圣经`、`## 待确认` 两部分，标题一字不改。不要输出每集八部分。"
	case SSSTurnPlan:
		return "【本轮任务】这是每集回合的第一轮。按手册/04 做集设计、按手册/05 做台词手术，只输出这两个部分，标题一字不改：`## 第一部分　本集设计`、`## 第二部分　台词定稿表`。"
	case SSSTurnDirect:
		return "【本轮任务】按手册/06 讲戏（每场开头一本在场人物账），只输出 `## 第三部分　讲戏本`，标题一字不改。"
	case SSSTurnCut:
		return "【本轮任务】按手册/07 预切单元、写各单元的导演阐述和视觉策略，按手册/08 整场排镜头表，再装箱定下单元总表。依次输出三部分，标题一字不改：\n" +
			"1. `## 整场镜头表`：按场写，每场一张完整镜头表（镜头的顺序、时长、台词定稿后不再动），表后写每个单元装哪几镜、各单元的导演阐述和视觉策略要点；\n" +
			"2. `## 第四部分　单元总表`：表头第一列写「单元」，另列「场次」与讲戏本小节的场次号一致，每个单元一行，单元编号写成 E〔集〕-U〔单元〕（如 E2-U1），表后写合计、成本、装满检查、在场检查、节奏检查；\n" +
			"3. `## 启发库选用`：一张表 `| 单元 | 库号 |`，每个单元一行，按启发库总索引给这个单元选 1 到 4 个库号（如 A05、F01、D01，只写库号，用顿号隔开），后面写单元卡时会把这几个库整份给你。"
	case SSSTurnCard:
		return fmt.Sprintf("【本轮任务】只写单元 %s 的单元卡（模板/04 的版式），这一轮写到「接缝」为止，不写「粘贴提示词」和「风险与备选」（下一轮编译时写）。先一行标题 `### %s｜〔长档／短档〕｜叙事〔T〕秒＋定格 1 秒＝〔总时长〕秒｜〔镜数〕镜＋定格｜〔场景〕（本场第〔n〕段／共〔N〕段）｜〔功能词〕`（不定格的单元写「〔总时长〕秒｜〔镜数〕镜」），再依次写难事与在场、导演阐述、视觉策略、声光设计、镜头表、表演时间轴、物理要点、接缝。镜头的顺序、时长、台词照【整场镜头表】里分给这个单元的那几镜，不改。镜头表从 0 秒开始、各行首尾相接；标题写了「＋定格 1 秒」的，镜头表最后一行就是那一秒全景定格（职责栏填「全景定格」），最后一行的结束秒必须等于标题的总时长——定格和换场景留的技术尾巴都要写进镜头表，不能只写在标题里。这一单元查用的启发库：%s（已整份附上）。只写这一个单元。", unit, unit, strings.Join(codes, "、"))
	case SSSTurnCompile:
		return fmt.Sprintf("【本轮任务】按手册/10 和模板/05，把单元 %s 的单元卡（见【前序产物：本单元的单元卡】）编译成即梦十五段提示词。只输出两部分：先一行 `#### 粘贴提示词`，后面用一个代码块（```）装十五段完整提示词（段名单独一行，顺序不变，交给用户的版本不带〔出处〕标注）；再一行 `#### 风险与备选` 写风险与备选。编译不创作：每一段都从单元卡、全剧圣经搬，锁逐字复制。", unit)
	case SSSTurnReview:
		return "【本轮任务】按手册/11 和质检/02 做导演复看与质检交接，只输出三部分，标题一字不改：`## 第六部分　后期备忘`、`## 第七部分　集交接卡`（版式按模板/03）、`## 第八部分　复看记录与待确认`。复看发现必须改的单元，写进待确认并写明改法（单元卡已经交付，这一轮不重写单元卡）。"
	case SSSTurnRepair:
		return fmt.Sprintf("【本轮任务】这是返修回合，只返修单元 %s。按手册/12 和质检/03 诊断，回到责任环节重写。依次输出 `## 诊断`、`## 改法`、`## 重写后的单元卡`；重写后的单元卡版式与原单元卡相同，单元编号不变，写到「接缝」为止（粘贴提示词下一轮编译时重写）。镜头表从 0 秒开始、各行首尾相接；标题写了「＋定格 1 秒」的，镜头表最后一行就是那一秒全景定格（职责栏填「全景定格」），最后一行的结束秒必须等于标题的总时长——定格和换场景留的技术尾巴都要写进镜头表，不能只写在标题里。", unit)
	}
	return ""
}

// ---------------------------------------------------------------- 输出解析

var (
	sssH2 = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)
	// sssH4 是单元卡里的栏目标签：模板写「#### 镜头表」，范例写「**镜头表：**」，两种都认（三级、四级标题也算）。
	sssH4       = regexp.MustCompile(`(?m)^(?:#{3,4}[ \t]*|\*\*)([^*\n]+?)(?:\*\*)?[：:]?(?:\*\*)?[ \t]*$`)
	sssUnitID   = regexp.MustCompile(`E\d{1,3}-U\d{1,2}[a-z]?`)
	sssCardHead = regexp.MustCompile(`(?m)^###\s*(E\d{1,3}-U\d{1,2}[a-z]?)\s*[｜|]\s*(.+?)\s*$`)
	sssSeconds  = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*秒`)
	sssShots    = regexp.MustCompile(`([0-9]+)\s*镜`)
	sssTimeCell = regexp.MustCompile(`^【?([0-9.]+)\s*[–\-~～至]\s*([0-9.]+)\s*秒?】?$`)
	sssFence    = regexp.MustCompile("(?s)```[^\\n]*\\n(.*?)\\n?```")
)

func sssCompact(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// SSSSection 取 `## 标题` 到下一个二级标题之间的正文。title 去掉空白后按前缀匹配（「第一部分」配「第一部分　本集设计」）。
func SSSSection(text string, title string) (string, bool) {
	return sssHeadingSection(sssH2, text, title)
}

func sssHeadingSection(re *regexp.Regexp, text string, title string) (string, bool) {
	want := sssCompact(title)
	locs := re.FindAllStringSubmatchIndex(text, -1)
	for i, loc := range locs {
		if !strings.HasPrefix(sssCompact(text[loc[2]:loc[3]]), want) {
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

// SSSRequireSections 检查各标题都在且有正文。
func SSSRequireSections(text string, titles ...string) error {
	var missing []string
	for _, t := range titles {
		if body, ok := SSSSection(text, t); !ok || strings.TrimSpace(body) == "" {
			missing = append(missing, "## "+t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少固定标题或正文为空：%s", strings.Join(missing, "、"))
	}
	return nil
}

func sssTableRows(text string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if strings.Trim(strings.Join(cells, ""), "-: ") == "" {
			continue // 表头下的分隔行
		}
		rows = append(rows, cells)
	}
	return rows
}

// SSSUnits 从第四部分单元总表取单元编号（按表中顺序，去重）。
func SSSUnits(cut string) ([]string, error) {
	table, ok := SSSSection(cut, "第四部分")
	if !ok {
		return nil, errors.New("缺少固定标题：## 第四部分　单元总表")
	}
	var units []string
	for _, cells := range sssTableRows(table) {
		id := sssUnitID.FindString(cells[0])
		if id != "" && !slices.Contains(units, id) {
			units = append(units, id)
		}
	}
	if len(units) == 0 {
		return nil, errors.New("单元总表里没有单元行（第一列应是 E〔集〕-U〔单元〕，如 E2-U1）")
	}
	if len(units) > SSSMaxUnits {
		return nil, fmt.Errorf("单元总表有 %d 个单元，超过一集上限 %d：同场景按最少段数重新装箱", len(units), SSSMaxUnits)
	}
	return units, nil
}

var sssCodePattern = regexp.MustCompile(`[A-G]\d{2}`)

// SSSHeuristicChoices 读切分轮的「启发库选用」表：每个单元 1 到 max 个库号，库号要在 known 里。
func SSSHeuristicChoices(cut string, units []string, known []string, max int) (map[string][]string, error) {
	body, ok := SSSSection(cut, "启发库选用")
	if !ok {
		return nil, errors.New("缺少固定标题：## 启发库选用")
	}
	out := map[string][]string{}
	for _, cells := range sssTableRows(body) {
		id := sssUnitID.FindString(cells[0])
		if id == "" || len(cells) < 2 {
			continue
		}
		var codes []string
		for _, c := range sssCodePattern.FindAllString(strings.Join(cells[1:], " "), -1) {
			if !slices.Contains(codes, c) {
				codes = append(codes, c)
			}
		}
		out[id] = codes
	}
	var problems []string
	for _, u := range units {
		codes := out[u]
		switch {
		case len(codes) == 0:
			problems = append(problems, fmt.Sprintf("单元 %s 没有选启发库", u))
		case len(codes) > max:
			problems = append(problems, fmt.Sprintf("单元 %s 选了 %d 个库号，最多 %d 个", u, len(codes), max))
		}
		for _, c := range codes {
			if !slices.Contains(known, c) {
				problems = append(problems, fmt.Sprintf("单元 %s 的库号 %s 不在启发库里（现有：%s）", u, c, strings.Join(known, "、")))
			}
		}
	}
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "；"))
	}
	return out, nil
}

// SSSUnitCard 是一张解析好的单元卡（单元卡轮的正文 + 编译轮的粘贴提示词与风险备选）。
type SSSUnitCard struct {
	ID           string  `json:"id"`
	Scene        string  `json:"scene"`
	TotalSeconds float64 `json:"totalSeconds"`
	Shots        int     `json:"shots"`
	Freeze       bool    `json:"freeze"`
	// Body 是标题行起到「接缝」为止的单元卡正文；Paste 是【粘贴提示词】代码块里的原文；Risk 是风险与备选。
	Body   string `json:"body"`
	Paste  string `json:"paste,omitempty"`
	Risk   string `json:"risk,omitempty"`
	Fenced bool   `json:"-"`
}

// Text 是这张卡的完整原文（拼八部分用）。
func (c SSSUnitCard) Text() string {
	parts := []string{strings.TrimSpace(c.Body)}
	if strings.TrimSpace(c.Paste) != "" {
		parts = append(parts, "#### 粘贴提示词\n\n```\n"+strings.TrimSpace(c.Paste)+"\n```")
	}
	if strings.TrimSpace(c.Risk) != "" {
		parts = append(parts, "#### 风险与备选\n\n"+strings.TrimSpace(c.Risk))
	}
	return strings.Join(parts, "\n\n")
}

// ParseSSSUnitCards 解析文本里的单元卡（标题行起到下一张卡或下一个二级标题）。
func ParseSSSUnitCards(text string) ([]SSSUnitCard, error) {
	locs := sssCardHead.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		return nil, errors.New("没有找到单元卡标题行（### E〔集〕-U〔单元〕｜〔档〕｜叙事 T 秒＋定格 1 秒＝总时长 秒｜N 镜＋定格｜〔场景〕｜〔功能词〕）")
	}
	cards := make([]SSSUnitCard, 0, len(locs))
	for i, loc := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if next := sssH2.FindStringIndex(text[loc[1]:end]); next != nil {
			end = loc[1] + next[0]
		}
		card, err := parseSSSCardHeader(text[loc[2]:loc[3]], text[loc[4]:loc[5]])
		if err != nil {
			return nil, err
		}
		body := strings.TrimSpace(text[loc[0]:end])
		if paste, ok := sssHeadingSection(sssH4, body, "粘贴提示词"); ok {
			if m := sssFence.FindStringSubmatch(paste); m != nil {
				card.Paste, card.Fenced = strings.TrimSpace(m[1]), true
			} else {
				card.Paste = strings.TrimSpace(paste)
			}
			card.Risk, _ = sssHeadingSection(sssH4, body, "风险与备选")
			body = strings.TrimSpace(body[:sssH4Index(body, "粘贴提示词")])
		}
		card.Body = body
		cards = append(cards, card)
	}
	return cards, nil
}

func sssH4Index(text string, title string) int {
	want := sssCompact(title)
	for _, loc := range sssH4.FindAllStringSubmatchIndex(text, -1) {
		if strings.HasPrefix(sssCompact(text[loc[2]:loc[3]]), want) {
			return loc[0]
		}
	}
	return len(text)
}

// parseSSSCardHeader 读标题行：「叙事 T 秒＋定格 1 秒＝总时长 秒」取等号后的总时长，没有等号取第一个「N 秒」；「N 镜（＋定格）」取镜数；
// 场景取第三个字段（去掉括号里的本场第几段）。
func parseSSSCardHeader(id string, rest string) (SSSUnitCard, error) {
	fields := strings.FieldsFunc(rest, func(r rune) bool { return r == '｜' || r == '|' })
	card := SSSUnitCard{ID: id}
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	for _, f := range fields {
		switch {
		case card.TotalSeconds == 0 && sssSeconds.MatchString(f):
			seg := f
			if at := strings.LastIndexAny(f, "＝="); at >= 0 {
				seg = f[at:]
			}
			if m := sssSeconds.FindStringSubmatch(seg); m != nil {
				card.TotalSeconds, _ = strconv.ParseFloat(m[1], 64)
			}
			card.Freeze = strings.Contains(f, "定格")
		case card.Shots == 0 && sssShots.MatchString(f):
			card.Shots, _ = strconv.Atoi(sssShots.FindStringSubmatch(f)[1])
			card.Freeze = card.Freeze || strings.Contains(f, "定格")
		}
	}
	if len(fields) >= 4 {
		scene := fields[3]
		if at := strings.IndexAny(scene, "（("); at > 0 {
			scene = scene[:at]
		}
		card.Scene = strings.TrimSpace(scene)
	}
	if card.TotalSeconds <= 0 || card.Shots <= 0 || card.Scene == "" {
		return SSSUnitCard{}, fmt.Errorf("单元卡 %s 的标题行读不出总时长、镜数或场景：%s", id, rest)
	}
	return card, nil
}

// ---------------------------------------------------------------- 镜头表

// SSSShotRow 是镜头表的一行。
type SSSShotRow struct {
	N          int
	Start, End float64
	Size       string
	Camera     string
	Text       string
	Lines      string
	Duty       string
}

// SSSShotTable 读单元卡「镜头表」：表头第一列「镜」，要有「时间」「画面与表演」列；每一行时间写成 a–b。
func SSSShotTable(card SSSUnitCard) ([]SSSShotRow, error) {
	body, ok := sssHeadingSection(sssH4, card.Body, "镜头表")
	if !ok {
		return nil, fmt.Errorf("单元卡 %s 缺少「#### 镜头表」", card.ID)
	}
	var header []string
	var rows []SSSShotRow
	for _, cells := range sssTableRows(body) {
		if cells[0] == "镜" || cells[0] == "镜头" || cells[0] == "镜号" {
			header = cells
			continue
		}
		if header == nil {
			continue
		}
		n, err := strconv.Atoi(strings.Trim(cells[0], "★ "))
		if err != nil {
			continue
		}
		col := func(names ...string) string {
			for _, name := range names {
				if i := slices.IndexFunc(header, func(h string) bool { return strings.HasPrefix(sssCompact(h), name) }); i >= 0 && i < len(cells) {
					return strings.TrimSpace(cells[i])
				}
			}
			return ""
		}
		m := sssTimeCell.FindStringSubmatch(col("时间"))
		if m == nil {
			return nil, fmt.Errorf("单元卡 %s 第 %d 镜的时间写成了「%s」，应写成 0–3.5", card.ID, n, col("时间"))
		}
		start, _ := strconv.ParseFloat(m[1], 64)
		end, _ := strconv.ParseFloat(m[2], 64)
		text := col("画面与表演", "画面")
		if text == "" {
			return nil, fmt.Errorf("单元卡 %s 第 %d 镜没有「画面与表演」", card.ID, n)
		}
		rows = append(rows, SSSShotRow{N: n, Start: start, End: end, Size: col("景别"), Camera: strings.Trim(col("机位")+" "+col("运动"), " "), Text: text, Lines: col("台词"), Duty: col("职责")})
	}
	if header == nil {
		return nil, fmt.Errorf("单元卡 %s 的镜头表缺少表头（| 镜 | 时间 | 时长 | … | 画面与表演 | 台词/声音 | 职责 |）", card.ID)
	}
	if !slices.ContainsFunc(header, func(h string) bool { return strings.HasPrefix(sssCompact(h), "时间") }) {
		return nil, fmt.Errorf("单元卡 %s 的镜头表缺少「时间」列", card.ID)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("单元卡 %s 的镜头表没有镜头行", card.ID)
	}
	return rows, nil
}

// sssCheckShotTable 校验镜头表与标题行一致：行数是镜数（或再加 1 个全景定格），镜号从 1 连续，时间从 0 起首尾相接，最后一镜在总时长结束。
func sssCheckShotTable(card SSSUnitCard) ([]SSSShotRow, error) {
	rows, err := SSSShotTable(card)
	if err != nil {
		return nil, err
	}
	if len(rows) != card.Shots && len(rows) != card.Shots+1 {
		return nil, fmt.Errorf("镜头表有 %d 行，标题行写的是 %d 镜（全景定格不计）", len(rows), card.Shots)
	}
	const eps = 0.11
	for i, r := range rows {
		switch {
		case r.N != i+1:
			return nil, fmt.Errorf("镜头表第 %d 行的镜号是 %d，应从 1 连续编号", i+1, r.N)
		case r.End <= r.Start:
			return nil, fmt.Errorf("第 %d 镜的结束秒不大于开始秒", r.N)
		case i == 0 && math.Abs(r.Start) > eps:
			return nil, errors.New("第 1 镜应从 0 秒开始")
		case i > 0 && math.Abs(r.Start-rows[i-1].End) > eps:
			return nil, fmt.Errorf("第 %d 镜从 %s 秒开始，与上一镜结束的 %s 秒接不上", r.N, secText(r.Start), secText(rows[i-1].End))
		}
	}
	if last := rows[len(rows)-1].End; math.Abs(last-card.TotalSeconds) > 0.51 {
		// 时长对不上照样拒绝（不吞掉差的秒数）；标题写了定格、表里却没有定格那一行时，说清楚缺的是哪一行，重试时模型照着补。
		if card.Freeze && !slices.ContainsFunc(rows, func(r SSSShotRow) bool { return strings.Contains(r.Duty, "定格") }) {
			return nil, fmt.Errorf("最后一镜在 %s 秒结束，标题行的总时长是 %s 秒：标题写了「＋定格 1 秒」，镜头表却没有全景定格那一行——在表末补一行全景定格（职责栏填「全景定格」），让最后一行在 %s 秒结束", secText(last), secText(card.TotalSeconds), secText(card.TotalSeconds))
		}
		return nil, fmt.Errorf("最后一镜在 %s 秒结束，标题行的总时长是 %s 秒：镜头表最后一行的结束秒要等于标题总时长（技术尾巴也写进镜头表）", secText(last), secText(card.TotalSeconds))
	}
	return rows, nil
}

// SSSUnitTimingLine 是单元卡轮（返修轮）附在本轮任务后面的时长核算：单元总表（切分轮第四部分）里这一单元的原行，
// 写卡时照它核对叙事时长、总时长和镜数。找不到这一行时为空（不影响本轮，单元卡仍按标题和镜头表校验）。
func SSSUnitTimingLine(cut string, unit string) string {
	table, ok := SSSSection(cut, "第四部分")
	if !ok || unit == "" {
		return ""
	}
	var header []string
	for _, cells := range sssTableRows(table) {
		if len(cells) == 0 {
			continue
		}
		if header == nil && sssUnitID.FindString(cells[0]) == "" {
			header = cells
			continue
		}
		if sssUnitID.FindString(cells[0]) != unit {
			continue
		}
		row := "| " + strings.Join(cells, " | ") + " |"
		if header != nil {
			row = "| " + strings.Join(header, " | ") + " |\n" + row
		}
		return "【单元总表核算】单元总表里本单元这一行（写镜头表时照它核对叙事时长、总时长、镜数）：\n" + row + "\n镜头表最后一行的结束秒＝总时长；带定格的单元，定格那一秒是镜头表的最后一行。"
	}
	return ""
}

// ---------------------------------------------------------------- 粘贴提示词

// SSSPromptSections 是模板/05 即梦十五段的段名（段名单独一行，顺序不变）。
var SSSPromptSections = []string{"场景背景", "生效参考素材", "场景地图", "首帧与走位", "格式模式", "光学", "摄影机", "动作", "表演", "物理", "布光", "声音", "风格", "输出设置", "正向锁定"}

// sssCheckSections 检查十五段段名都单独成行且顺序正确。
func sssCheckSections(paste string) error {
	lines := strings.Split(paste, "\n")
	at := 0
	var missing []string
	found := make([]int, 0, len(SSSPromptSections))
	for _, name := range SSSPromptSections {
		hit := -1
		for i := at; i < len(lines); i++ {
			if sssSectionName(lines[i]) == name {
				hit = i
				break
			}
		}
		if hit < 0 {
			missing = append(missing, name)
			continue
		}
		found = append(found, hit)
		at = hit + 1
	}
	if len(missing) > 0 {
		return fmt.Errorf("十五段缺少或顺序不对：%s（段名单独一行，按 场景背景、生效参考素材……正向锁定 的顺序）", strings.Join(missing, "、"))
	}
	// 每一段都要有正文：段名到下一个段名之间不能全是空白。确实不适用的段明写「无」，不留空。
	var empty []string
	for k, start := range found {
		end := len(lines)
		if k+1 < len(found) {
			end = found[k+1]
		}
		if strings.TrimSpace(strings.Join(lines[start+1:end], "\n")) == "" {
			empty = append(empty, SSSPromptSections[k])
		}
	}
	if len(empty) > 0 {
		return fmt.Errorf("十五段里这些段没有正文：%s（编译要从单元卡和圣经把内容搬进来；确实不适用的段写「无」）", strings.Join(empty, "、"))
	}
	return nil
}

// sssSectionName 是一行去掉装饰（【】、#、*、冒号）后的文字，用来认段名行。
func sssSectionName(line string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "【】#*：:"))
}

// sssTagMatch 是提示词里的一处 @ 标签。
type sssTagMatch struct {
	start, end int // 字节区间，含 @
	material   int // 素材清单下标；-1 表示对不上或是保留标签
	raw        string
	// 草稿保留标签由服务端分配，先于素材名判断。
	reserved string
}

// SSSReservedTag 标识不能当作图片名称的媒体标签。
func SSSReservedTag(token string) string {
	switch {
	case sssReservedMedia.MatchString(token):
		return "media"
	case strings.HasSuffix(token, "声音"):
		return "voice"
	}
	return ""
}

var sssReservedMedia = regexp.MustCompile(`^(?:视频|音频|站位参考)\d*$`)

// sssTagDelims 是标签后面可能紧跟的分隔符（标签对不上时用来截出写错的那一串）。
const sssTagDelims = " \t\n：:，,。、；;（）()「」“”\"'【】<>《》!！?？"

// sssTagEnds 表示标签在这里结束：后面没有字了，或紧跟分隔符。
func sssTagEnds(rest string) bool {
	return rest == "" || strings.IndexAny(rest, sssTagDelims) == 0
}

// sssScanTags 扫出提示词里的全部 @ 标签：按素材名最长前缀匹配；对不上的记 -1。
func sssScanTags(text string, materials []SSSMaterial) []sssTagMatch {
	order := make([]int, len(materials))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return len(materials[order[a]].Name) > len(materials[order[b]].Name) })
	var out []sssTagMatch
	for i := 0; i < len(text); {
		at := strings.IndexByte(text[i:], '@')
		if at < 0 {
			break
		}
		start := i + at
		rest := text[start+1:]
		match := sssTagMatch{start: start, material: -1}
		token := rest
		if stop := strings.IndexAny(rest, sssTagDelims); stop >= 0 {
			token = rest[:stop]
		}
		if SSSReservedTag(token) == "media" {
			// @视频N、@音频N、@站位参考N 整串精确匹配就判：即使素材清单里恰好有叫「视频」「站位参考」的资产，也不当参考图用。
			match.end, match.raw, match.reserved = start+1+len(token), "@"+token, "media"
			out = append(out, match)
			i = match.end
			continue
		}
		// 「声音」要按素材名的边界判：@林夏压低声音、@林夏听到声音 是人物引用 + 动作正文，照常引用林夏；
		// 只有素材名本身以「声音」结尾，或名字后面紧跟「声音」且标签到此为止（@林夏声音：…），才是参考音频写法。
		for _, idx := range order {
			name := materials[idx].Name
			if name != "" && strings.HasPrefix(rest, name) {
				match.material, match.end, match.raw = idx, start+1+len(name), "@"+name
				after := rest[len(name):]
				voiceTag := strings.HasPrefix(after, "声音") && sssTagEnds(after[len("声音"):])
				if strings.HasSuffix(name, "声音") || voiceTag {
					match.material, match.reserved = -1, "voice"
					if voiceTag {
						match.end, match.raw = match.end+len("声音"), match.raw+"声音"
					}
				}
				break
			}
		}
		if match.material < 0 && match.reserved == "" {
			match.end, match.raw = start+1+len(token), "@"+token
			// 对不上素材的 @某某声音 也按参考音频写法说明原因（同样不合格）。
			if SSSReservedTag(token) == "voice" {
				match.reserved = "voice"
			}
		}
		out = append(out, match)
		i = max(match.end, start+1)
	}
	return out
}

// SSSPasteTags 是提示词里用到的素材（素材清单下标，按第一次出现的顺序去重）和写错的标签。
func SSSPasteTags(paste string, materials []SSSMaterial) (used []int, problems []string) {
	for _, m := range sssScanTags(paste, materials) {
		problem := ""
		switch {
		case m.reserved == "media":
			problem = fmt.Sprintf("标签 %s 不可用：草稿只引用清单中的图片或指定的站位图；音频编号由编译器按绑定与台词分配", m.raw)
		case m.reserved == "voice":
			problem = fmt.Sprintf("标签 %s 不可用：请在台词栏写说话人；绑定版本的声音样本由编译器关联", m.raw)
		case m.material < 0:
			problem = fmt.Sprintf("标签 %s 不在素材清单里（只能用素材清单里的名称）", m.raw)
		}
		switch {
		case problem != "":
			if !slices.Contains(problems, problem) {
				problems = append(problems, problem)
			}
		default:
			if !slices.Contains(used, m.material) {
				used = append(used, m.material)
			}
		}
	}
	return used, problems
}

// SSSUnitLimits 是校验单元卡用的边界。
type SSSUnitLimits struct {
	PositionReference bool
	MaxSeconds        int
	MaxImages         int
	Materials         []SSSMaterial
	// CapSeconds 是本集各单元合计时长上限（0 不限），UsedSeconds 是本集其他单元已占的秒数。
	CapSeconds  float64
	UsedSeconds float64
}

// SSSEpisodeCap 是一集各单元合计时长的上限（秒）：每集目标的 1.1 倍；没设每集时长返回 0（不限）。
func SSSEpisodeCap(episodeSeconds int) float64 {
	return OntologyEpisodeCap(episodeSeconds)
}

// SSSBudgetLine 是单元卡、编译、返修轮提示词末尾的时长预算说明；不限时为空。
func SSSBudgetLine(lim SSSUnitLimits) string {
	if lim.CapSeconds <= 0 {
		return ""
	}
	return fmt.Sprintf("【时长预算】本集各单元合计不超过 %s 秒；其他单元已用 %s 秒，本单元总时长最多 %s 秒。", secText(lim.CapSeconds), secText(lim.UsedSeconds), secText(math.Max(0, lim.CapSeconds-lim.UsedSeconds)))
}

var sssForbiddenInPaste = []string{"同上", "接上一镜", "如前所述"}

// ValidateSSSCardBody 校验单元卡轮（或返修轮）写的单元卡正文：编号、时长上限、镜头表。
func ValidateSSSCardBody(card SSSUnitCard, want string, lim SSSUnitLimits) error {
	var problems []string
	if want != "" && card.ID != want {
		problems = append(problems, fmt.Sprintf("单元编号应为 %s，写成了 %s", want, card.ID))
	}
	maxSeconds := lim.MaxSeconds
	if maxSeconds <= 0 || maxSeconds > SSSUnitMaxSeconds {
		maxSeconds = SSSUnitMaxSeconds
	}
	if card.TotalSeconds > float64(maxSeconds)+1e-6 {
		problems = append(problems, fmt.Sprintf("总时长 %s 秒，超过平台上限 %d 秒（含最后 1 秒全景定格）", secText(card.TotalSeconds), maxSeconds))
	}
	if lim.CapSeconds > 0 && lim.UsedSeconds+card.TotalSeconds > lim.CapSeconds+1e-6 {
		problems = append(problems, fmt.Sprintf("本集各单元合计不能超过 %s 秒（每集目标的 1.1 倍），其他单元已用 %s 秒，这一单元最多 %s 秒", secText(lim.CapSeconds), secText(lim.UsedSeconds), secText(math.Max(0, lim.CapSeconds-lim.UsedSeconds))))
	}
	if _, err := sssCheckShotTable(card); err != nil {
		problems = append(problems, err.Error())
	}
	if OntologyUnfinished(card.Body) {
		problems = append(problems, "这张卡没写完（出现了未完续写标记），本轮必须一次写完")
	}
	if len(problems) > 0 {
		return fmt.Errorf("单元卡 %s：%s", firstNonBlank(card.ID, want), strings.Join(problems, "；"))
	}
	return nil
}

// ValidateSSSPaste 校验编译轮写的粘贴提示词：代码块、十五段、长度、背景音乐、标签、参考图数量、禁用写法。
func ValidateSSSPaste(card SSSUnitCard, lim SSSUnitLimits) error {
	var problems []string
	paste := card.Paste
	if !card.Fenced {
		problems = append(problems, "粘贴提示词必须放在一个代码块（```）里")
	}
	chars := len([]rune(paste))
	switch {
	case strings.TrimSpace(paste) == "":
		problems = append(problems, "粘贴提示词为空")
	case chars > SSSPromptMaxChars:
		problems = append(problems, fmt.Sprintf("粘贴提示词 %d 字符，超过上限 %d", chars, SSSPromptMaxChars))
	}
	if strings.TrimSpace(paste) != "" {
		if err := sssCheckSections(paste); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if !strings.Contains(paste, SSSNoMusicLine) {
		problems = append(problems, "声音段最后没有写「"+SSSNoMusicLine+"」")
	}
	if sssUnitID.MatchString(paste) {
		problems = append(problems, "粘贴提示词里出现了单元编号")
	}
	for _, w := range sssForbiddenInPaste {
		if strings.Contains(paste, w) {
			problems = append(problems, "粘贴提示词里出现了「"+w+"」")
		}
	}
	if lim.PositionReference {
		count := 0
		section := ""
		for _, line := range strings.Split(paste, "\n") {
			if name := sssSectionName(line); slices.Contains(SSSPromptSections, name) {
				section = name
				continue
			}
			for _, m := range sssScanTags(line, lim.Materials) {
				if positionToken(m.raw) {
					count++
					if section != "生效参考素材" || strings.TrimSpace(line[:m.start]) != "" || strings.Contains(line[m.end:], "@") {
						problems = append(problems, "@站位参考 必须在生效参考素材中单独起一行，避免跳过站位图时误删其他指令")
					}
				}
			}
		}
		if count != 1 {
			problems = append(problems, "同场景后续段必须写一次 @站位参考")
		}
		paste = ApplyPositionReference(paste, "")
	}
	used, tagProblems := SSSPasteTags(paste, lim.Materials)
	problems = append(problems, tagProblems...)
	if lim.PositionReference && lim.MaxImages > 0 && len(used)+1 > lim.MaxImages {
		problems = append(problems, "同场景后续段要给站位参考图留一个名额")
	}
	if lim.MaxImages > 0 && len(used) > lim.MaxImages {
		problems = append(problems, fmt.Sprintf("这一单元用了 %d 张参考图，超过视频模型上限 %d 张", len(used), lim.MaxImages))
	}
	if strings.TrimSpace(card.Risk) == "" {
		problems = append(problems, "缺少「#### 风险与备选」")
	}
	if OntologyUnfinished(paste) || OntologyUnfinished(card.Risk) {
		problems = append(problems, "出现了未完续写标记，本轮必须一次写完")
	}
	if len(problems) > 0 {
		return fmt.Errorf("单元 %s 的粘贴提示词：%s", card.ID, strings.Join(problems, "；"))
	}
	return nil
}

// ParseSSSCompile 读编译轮输出：粘贴提示词代码块和风险与备选，合进单元卡。
func ParseSSSCompile(body SSSUnitCard, compile string) SSSUnitCard {
	card := body
	card.Paste, card.Fenced, card.Risk = "", false, ""
	if paste, ok := sssHeadingSection(sssH4, compile, "粘贴提示词"); ok {
		if m := sssFence.FindStringSubmatch(paste); m != nil {
			card.Paste, card.Fenced = strings.TrimSpace(m[1]), true
		} else {
			card.Paste = strings.TrimSpace(paste)
		}
	} else if m := sssFence.FindStringSubmatch(compile); m != nil {
		card.Paste, card.Fenced = strings.TrimSpace(m[1]), true
	}
	card.Risk, _ = sssHeadingSection(sssH4, compile, "风险与备选")
	return card
}

// ---------------------------------------------------------------- 单元卡 → 分段

// SSSSegment 把单元卡装成一段：粘贴提示词原文存在 Verbatim（@名称 按本段第一次出现的顺序换成 @图1..k，与 Refs 顺序一致），
// 镜头表转成段内镜头（只供分镜卡显示、时长判断，不参与编译）。order 从 1 数，段号 S01、S02……
func SSSSegment(card SSSUnitCard, order int, materials []SSSMaterial, allowPosition ...bool) (Segment, error) {
	tags := sssScanTags(card.Paste, materials)
	remap := map[int]int{}
	var refs []Ref
	var b strings.Builder
	last := 0
	for _, t := range tags {
		if positionToken(t.raw) && len(allowPosition) > 0 && allowPosition[0] {
			continue
		}
		if t.material < 0 {
			return Segment{}, fmt.Errorf("单元 %s 的标签 %s 对不上素材清单", card.ID, t.raw)
		}
		m := materials[t.material]
		if strings.TrimSpace(m.VersionID) == "" {
			return Segment{}, fmt.Errorf("单元 %s 引用的 @%s 没有资产版本", card.ID, m.Name)
		}
		k, ok := remap[t.material]
		if !ok {
			k = len(remap) + 1
			remap[t.material] = k
			refs = append(refs, Ref{File: m.VersionID, Role: ontologyRefRole(m.Category), Note: m.Name})
		}
		b.WriteString(card.Paste[last:t.start])
		fmt.Fprintf(&b, "@图%d", k)
		last = t.end
	}
	b.WriteString(card.Paste[last:])
	rows, err := sssCheckShotTable(card)
	if err != nil {
		return Segment{}, fmt.Errorf("单元 %s：%w", card.ID, err)
	}
	shots := make([]SubShot, 0, len(rows))
	for _, r := range rows {
		shots = append(shots, SubShot{At: math.Max(r.Start, 0), Size: r.Size, Camera: r.Camera, Text: r.Text, Sound: r.Lines, Purpose: r.Duty})
	}
	seg := Segment{
		ID: fmt.Sprintf("S%02d", order), Scene: card.Scene, Title: card.ID, Dur: card.TotalSeconds,
		Mode: ModeTextOnly, Refs: refs, Shots: shots, Verbatim: b.String(), Lang: "zh",
	}
	if len(refs) > 0 {
		seg.Mode = ModeLockIdentity
	}
	if err := seg.Validate(); err != nil {
		return Segment{}, err
	}
	return seg, nil
}

// ---------------------------------------------------------------- 八部分

// SSSEpisodeParts 是每集八部分的标题（SKILL.md 第五节，顺序不变）。
var SSSEpisodeParts = []string{"第一部分　本集设计", "第二部分　台词定稿表", "第三部分　讲戏本", "第四部分　单元总表", "第五部分　单元卡", "第六部分　后期备忘", "第七部分　集交接卡", "第八部分　复看记录与待确认"}

// SSSEpisodeDocument 把各轮产物拼成八部分（缺的部分写"本集无"）；切分轮的整场镜头表附在第四部分后面。
func SSSEpisodeDocument(plan, direct, cut string, cards []SSSUnitCard, review string) string {
	part := func(text, title string) string {
		body, ok := SSSSection(text, strings.Fields(title)[0])
		if !ok || body == "" {
			body = "本集无"
		}
		return "## " + title + "\n\n" + body
	}
	units := make([]string, 0, len(cards))
	for _, c := range cards {
		units = append(units, c.Text())
	}
	unitText := strings.Join(units, "\n\n")
	if unitText == "" {
		unitText = "本集无"
	}
	table := part(cut, SSSEpisodeParts[3])
	if shots, ok := SSSSection(cut, "整场镜头表"); ok && shots != "" {
		table += "\n\n### 整场镜头表（装箱前）\n\n" + shots
	}
	return strings.Join([]string{
		part(plan, SSSEpisodeParts[0]), part(plan, SSSEpisodeParts[1]), part(direct, SSSEpisodeParts[2]), table,
		"## " + SSSEpisodeParts[4] + "\n\n" + unitText,
		part(review, SSSEpisodeParts[5]), part(review, SSSEpisodeParts[6]), part(review, SSSEpisodeParts[7]),
	}, "\n\n") + "\n"
}

// ParseSSSEpisodeCards 从八部分文档里读出各单元卡（返修时找原卡用）。
func ParseSSSEpisodeCards(doc string) ([]SSSUnitCard, error) {
	body, ok := SSSSection(doc, "第五部分")
	if !ok {
		return nil, errors.New("八部分里没有第五部分单元卡")
	}
	// 单元卡里有四级标题，没有二级标题；第五部分一直到第六部分。
	return ParseSSSUnitCards(body)
}

// SSSBibleText 取立项轮的全剧圣经正文。
func SSSBibleText(setup string) (string, bool) {
	body, ok := SSSSection(setup, "全剧圣经")
	return body, ok && strings.TrimSpace(body) != ""
}

// SSSHandoffText 取复看轮的集交接卡正文。
func SSSHandoffText(review string) (string, bool) {
	body, ok := SSSSection(review, "第七部分")
	return body, ok && strings.TrimSpace(body) != "" && strings.TrimSpace(body) != "本集无"
}

// SSSPending 取复看轮第八部分里的「待确认」（没有小节时取整个第八部分）。
func SSSPending(review string) string {
	body, ok := SSSSection(review, "第八部分")
	if !ok {
		return ""
	}
	if at := strings.Index(body, "待确认"); at >= 0 {
		if line := strings.LastIndex(body[:at], "\n"); line >= 0 && strings.HasPrefix(strings.TrimSpace(body[line:at]), "#") {
			return strings.TrimSpace(body[line:])
		}
	}
	return body
}
