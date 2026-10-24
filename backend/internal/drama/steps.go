package drama

import "slices"

// 短剧流程的步骤表（M6）：流程（步骤、顺序、输出契约、代码负责组装的变量、解析与检查）写在代码里，改了要发版；
// 每一步「怎么想、怎么写」交给按项目风格档案绑定的技能（drama_step_bindings）。这一期只有 chapter.shots（生成分段）。

const (
	// StepChapterShots 是生成分段：一章正文 → 镜头行（带 spec）。
	StepChapterShots = "chapter.shots"
	// ContractChapterShots 是生成分段的输出契约版本；改契约就加版本号并换 schema 文件。
	ContractChapterShots = "chapter.shots@2"
)

// StepInput 是代码负责组装进提示词的一项变量（技能只写怎么用，不写怎么取）。
type StepInput struct {
	Name string `json:"name"`
	// Label 是提示词里这一段的标题或用途。
	Label string `json:"label"`
	// Source 是变量从哪里取。
	Source string `json:"source"`
}

// StepGate 是包上传后真跑评测、自动启用新版本的门槛（M6 第三期）。比率都是 0～1。
type StepGate struct {
	// ParseRate 是评测章节里模型输出能解析出镜头行的比例。
	ParseRate float64 `json:"parseRate"`
	// SpecRate 是镜头行写了 spec 的比例。
	SpecRate float64 `json:"specRate"`
	// DialogueCoverage 是正文台词逐字落进镜头的比例。
	DialogueCoverage float64 `json:"dialogueCoverage"`
	// MaxCheckErrors 是镜头表检查 error 条数上限。
	MaxCheckErrors int `json:"maxCheckErrors"`
	// MaxWarnRatio 是检查 warn 条数相对当前启用版本的上限倍数。
	MaxWarnRatio float64 `json:"maxWarnRatio"`
	// MaxDurationRatio 是镜头估时合计相对每集时长的上限倍数。
	MaxDurationRatio float64 `json:"maxDurationRatio"`
	// MaxTruncated 是输出被截断的章节数上限。
	MaxTruncated int `json:"maxTruncated"`
}

// StepEvalChapter 是一步的评测章节：包上传后在这些章节上用线上默认文本模型真跑这一步。
// 数据（项目、章节、资产清单的接口 JSON）在 evalset/<id>/，由 app 测试按真实用户路径建项目后导出。
type StepEvalChapter struct {
	ID string `json:"id"`
	// Profile 是这一章按哪个风格档案跑（包覆盖这个档案就要跑这一章）。
	Profile string `json:"profile"`
	Label   string `json:"label"`
}

// Step 是步骤表的一行。
type Step struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Contract 是输出契约版本（contracts/ 下的 schema）。
	Contract string `json:"contract"`
	// Inputs 是代码负责组装的变量，按在提示词里出现的先后。
	Inputs []StepInput `json:"inputs"`
	// Parser / Checks 说明模型输出怎么解析、用哪些检查器把关（函数见 Rows / Check / Pack）。
	Parser string   `json:"parser"`
	Checks []string `json:"checks"`
	// SkillBudgetChars 是技能上下文（SKILL.md 加点名的卡）的字数上限，按 JS 字符串长度计。
	SkillBudgetChars int `json:"skillBudgetChars"`
	// SkillLinkedFiles 是除 SKILL.md 外最多读几张卡。
	SkillLinkedFiles int `json:"skillLinkedFiles"`
	// DefaultSkill 是绑定表里没配时用的技能。
	DefaultSkill string   `json:"defaultSkill"`
	Gate         StepGate `json:"gate"`
	// EvalSet 是评测集，按风格档案配置：包覆盖的每个档案跑它的章节，再加包清单 eval 点名的章节。
	EvalSet []StepEvalChapter `json:"evalSet"`
	// TaskSource 是这一步文本任务 metadata.source（前端按它认出任务、刷新后恢复结果）。
	TaskSource string `json:"taskSource"`

	// Rows / Check / Pack 是这一步的解析器与检查器：分镜行 → 剧本台词表对照的镜头表、镜头表检查、装段（段检查在装段之后）。
	Rows  func(rows []StoryboardRow, script *Script, profile string) RowsResult `json:"-"`
	Check func(shotlist Shotlist, ctx CheckContext) CheckReport                 `json:"-"`
	Pack  func(in PackInput) ([]Segment, []string)                              `json:"-"`
}

var steps = []Step{
	{
		ID:       StepChapterShots,
		Label:    "生成分段",
		Contract: ContractChapterShots,
		Inputs: []StepInput{
			{Name: "skill", Label: "技能上下文（skill-context）与用户任务：章节标题、章节正文", Source: "按项目风格档案绑定的技能当前版本；正文取章节保存的 HTML，按编辑器 getText 的规则转成纯文本"},
			{Name: "projectStyle", Label: "【项目画风：…】", Source: "项目画风快照 styleProfileJson 的 title / prompt，按小节取到 1200 字"},
			{Name: "characters", Label: "【已确认角色外观】", Source: "项目里有角色卡版本的角色资产，外貌、服装、体型、随身道具、一致性，共 1600 字"},
			{Name: "episodeSeconds", Label: "【本集时长】", Source: "需求文档 brief_json.episode_seconds"},
			{Name: "contract", Label: "【受保护输出契约】", Source: "chapter.shots@2 schema 生成"},
			{Name: "assets", Label: "【项目已确认资产】", Source: "项目资产清单（角色、有主版本的场景和道具，最多 60 项）与场景锚点；带 assets 字段要求"},
		},
		Parser:           "前端 storyboardRowsFromTask 取出 rows（带 spec），PUT /drama/shotlist 后由 RowsToShotlist 对照剧本台词表转成镜头表",
		Checks:           []string{"CheckShots（镜头表规则 R-SHOT-*）", "Pack 装段后的段检查（R-SEG-*，含每集时长 R-SEG-008）"},
		SkillBudgetChars: 32_000,
		SkillLinkedFiles: 3,
		DefaultSkill:     "yz-chapter-shots",
		Gate: StepGate{
			ParseRate:        1,
			SpecRate:         1,
			DialogueCoverage: 1,
			MaxCheckErrors:   0,
			MaxWarnRatio:     1.2,
			MaxDurationRatio: 1.1,
			MaxTruncated:     0,
		},
		// 真人剧用《七床》第一集；其余四个档案各用一类题材的测试章节（打斗、科幻奇观、分手情绪、悬疑）。
		// game-ip 还没有专门的测试项目，暂用悬疑章节跑它的档案规则。
		EvalSet: []StepEvalChapter{
			{ID: "qichuang-e01", Profile: "live-realism", Label: "《七床》第一集（悬疑·真人剧）"},
			{ID: "dadou-e01", Profile: "action-epic", Label: "《擂台之下》第一集（打斗）"},
			{ID: "kehuan-e01", Profile: "anime-3d", Label: "《第七层天幕》第一集（科幻奇观）"},
			{ID: "fenshou-e01", Profile: "duanju-shiga", Label: "《最后一班地铁》第一集（分手情绪）"},
			{ID: "xuanyi-e01", Profile: "game-ip", Label: "《第七病区》第一集（悬疑）"},
		},
		TaskSource: "short-drama-chapter-storyboard",
		Rows:       RowsToShotlist,
		Check:      CheckShots,
		Pack:       Pack,
	},
}

// EvalChapter 查这一步评测集里的一章。
func (s Step) EvalChapter(id string) (StepEvalChapter, bool) {
	for _, c := range s.EvalSet {
		if c.ID == id {
			return c, true
		}
	}
	return StepEvalChapter{}, false
}

// EvalChapters 是一个包要跑的评测章节：先按评测集顺序取包覆盖的档案的章节，再补清单 eval 点名的（去重，不认识的跳过，
// 静态校验已经拦下）。
func (s Step) EvalChapters(profiles []string, named []string) []StepEvalChapter {
	var out []StepEvalChapter
	seen := map[string]bool{}
	for _, c := range s.EvalSet {
		if slices.Contains(profiles, c.Profile) && !seen[c.ID] {
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	for _, id := range named {
		if c, ok := s.EvalChapter(id); ok && !seen[id] {
			seen[id] = true
			out = append(out, c)
		}
	}
	return out
}

// Steps 是步骤表（按流程顺序）。
func Steps() []Step { return slices.Clone(steps) }

// StepByID 查一个步骤。
func StepByID(id string) (Step, bool) {
	for _, s := range steps {
		if s.ID == id {
			return s, true
		}
	}
	return Step{}, false
}
