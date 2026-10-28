package drama

import "slices"

// 短剧流程的步骤表（M6）：流程（步骤、顺序、输出契约、代码负责组装的变量、解析与检查）写在代码里，改了要发版；
// 每一步「怎么想、怎么写」交给按项目风格档案绑定的技能（drama_step_bindings）。现有 style.lock（风格锁定，项目级）、
// script.adapt（剧本优化，项目级）、scene.palette（场次色卡，项目级）、assets.extract（资产设计，按章节或剧集）和 chapter.shots（生成分段，按章节或剧集）。

const (
	// StepStyleLock 是风格锁定（建项目时的导演风格分析）：项目类型 + 风格文字 + 参考图 + 画风 → 全局风格锚 + 主基调色卡（分组色板）。
	StepStyleLock = "style.lock"
	// ContractStyleLock 是风格锁定的输出契约：第一行风格锚，之后每组一行色板。
	ContractStyleLock = "style.lock@1"
	// StepChapterShots 是生成分段：一章正文 → 镜头行（带 spec）。
	StepChapterShots = "chapter.shots"
	// ContractChapterShots 是生成分段的输出契约版本；改契约就加版本号并换 schema 文件。
	// @3 起一镜一行（JSON Lines）：任务内按场续写、前端边流边解析都靠它。
	ContractChapterShots = "chapter.shots@3"
	// StepScriptAdapt 是剧本优化：所选原文章节 → 若干集短剧剧本（episode 单元）；已是剧本的只调节奏。
	StepScriptAdapt = "script.adapt"
	// ContractScriptAdapt 是剧本优化的输出契约：一集一块（一行 JSON 头加正文）。
	ContractScriptAdapt = "script.adapt@1"
	// StepScenePalette 是场次色卡：项目锁定的风格锚 + 主基调色卡 + 全部剧集的场次 → 逐场色彩脚本（bible_json.color_script），
	// 每场一张代码合成的色卡资产（palette，kind=scene）。M8 卡 B 由导演风格 style.bible 改来，不再写风格锚、不再调图片模型。
	StepScenePalette = "scene.palette"
	// ContractScenePalette 是场次色卡的输出契约：每场一行色彩脚本（scene 写场编号）。
	ContractScenePalette = "scene.palette@1"
	// StepAssetsExtract 是资产设计：一集（或一章）剧本 → 角色、场景、道具和它们的外观提示词，收尾直接写成已确认资产并排出图。
	StepAssetsExtract = "assets.extract"
	// ContractAssetsExtract 是资产设计的输出契约：一个资产一行（JSON Lines）。
	ContractAssetsExtract = "assets.extract@1"
)

// 步骤作用的范围（Step.Scope）。
const (
	// StepScopeUnit 是对一个章节或剧集跑（POST /projects/:id/units/:unitId/drama/steps/:step/run）。
	StepScopeUnit = "unit"
	// StepScopeProject 是对整个项目跑，输入由请求点名（POST /projects/:id/drama/steps/:step/run）。
	StepScopeProject = "project"
)

// StepEvalMaxConcurrency 是评测同时在跑的任务数上限（步骤表的 EvalConcurrency 超过它按它算）。
const StepEvalMaxConcurrency = 2

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
	// Metrics 是这一步要判的指标（Gate* 编号）；为空时全部都判。
	Metrics []string `json:"metrics,omitempty"`
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

// TurnContext 是校验一轮输出时可用的上下文。
type TurnContext struct {
	// Index 是第几轮（从 1 数）；Turn 是这一轮（场或集）。
	Index int
	Turn  ShotsScene
	// Prior 是前面各轮的输出按顺序拼接（场次色卡校验承接色用）。
	Prior string
	// Sources 是这次运行点名的来源单元（剧本优化校验 sources 用），其余步骤为空。
	Sources []string
}

// TurnPlan 是一步把一次运行切成多轮的方式（M7.2 逐轮续写）：生成分段、资产设计一轮一场，剧本优化、场次色卡一轮一集。
// 轮次本身（场次表、集次表）在建任务时算好存进 metadata.dramaRun.scenes，这里只管每一轮怎么说、怎么验。
type TurnPlan struct {
	// Unit 是一轮叫什么（场、集），用在进度和错误文字里。
	Unit string
	// Intro 是第一轮接在提示词后面的分回合说明；只有一轮时不加。
	Intro func(turns []ShotsScene) string
	// Next 是第 k 轮（从 1 数，k ≥ 2）的续写指令；written 是前面各轮已经写出的条数（镜头数、集数）。
	Next func(turns []ShotsScene, k int, written int) string
	// Count 解析一轮的输出，返回这一轮写了几条；解析不出或不合格时返回错误，这一轮按临时失败重试。
	Count func(text string, turn TurnContext) (int, error)
}

// Step 是步骤表的一行。
type Step struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Scope 是作用范围：unit（一章 / 一集）或 project（整个项目）。
	Scope string `json:"scope"`
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
	// EvalConcurrency 是这一步的评测全局同时在跑的任务数（所有正在进行的评测共用，不叠加），不超过 StepEvalMaxConcurrency。
	EvalConcurrency int `json:"evalConcurrency"`
	// TaskSource 是这一步文本任务 metadata.source（前端按它认出任务、刷新后恢复结果）。
	TaskSource string `json:"taskSource"`

	// Rows / Check / Pack 是这一步的解析器与检查器：分镜行 → 剧本台词表对照的镜头表、镜头表检查、装段（段检查在装段之后）。
	Rows  func(rows []StoryboardRow, script *Script, profile string) RowsResult `json:"-"`
	Check func(shotlist Shotlist, ctx CheckContext) CheckReport                 `json:"-"`
	Pack  func(in PackInput) ([]Segment, []string)                              `json:"-"`
	// Turns 是逐轮续写的切法。
	Turns TurnPlan `json:"-"`
}

var steps = []Step{
	{
		ID:       StepStyleLock,
		Label:    "风格锁定",
		Scope:    StepScopeProject,
		Contract: ContractStyleLock,
		Inputs: []StepInput{
			{Name: "skill", Label: "技能上下文（skill-context）与用户任务：【项目类型】【用户的风格描述】【参考图】", Source: "按项目风格档案绑定的技能当前版本；项目类型、风格文字（2000 字）由请求带来；参考图是请求点名的本人图片资源（最多 6 张），以图片引用随任务发给文本模型（input.referenceImages），提示词里只写张数"},
			{Name: "brief", Label: "【项目需求】", Source: "需求文档 brief_json 的片名、用途、平台、口味、约束、不做的事等，共 1500 字"},
			{Name: "projectStyle", Label: "【项目画风：…】", Source: "项目画风快照 styleProfileJson 的 title、selection 与 prompt，按小节取到 1200 字；没选画风时不写"},
			{Name: "profile", Label: "【风格档案：…】", Source: "项目风格档案（profiles/*.json）的名称、画面质感段（style_block.zh，600 字）与光线政策"},
			{Name: "contract", Label: "【受保护输出契约】", Source: "style.lock@1 schema 生成（第一行风格锚，之后每组一行色板）"},
		},
		Parser:           "一轮；StyleLockFromText 逐行解析风格锚和分组色板（缺括号补齐、字段类型不符单字段降级），CountStyleLockTurn 当轮校验；收尾只写进任务结果，用户点「锁定此风格」才写 bible_json 并合成主色卡",
		Checks:           []string{"CheckStyleLock（R-LOCK-*：有风格锚、至少 4 色、HEX 是 #RRGGBB；缺主角组或环境组、缺色名、HEX 重复、字段降级读出是 warn）"},
		SkillBudgetChars: 32_000,
		SkillLinkedFiles: 3,
		DefaultSkill:     "yz-style-lock",
		// 闸门先宽：只看解析率（有风格锚且主基调色卡至少 4 色才算解析成功）。
		Gate:            StepGate{Metrics: []string{GateParseRate}, ParseRate: 0.9},
		EvalConcurrency: 1,
		EvalSet: []StepEvalChapter{
			{ID: "qichuang-e01", Profile: "live-realism", Label: "《七床》第一集（悬疑·真人剧，按画风定风格）"},
		},
		TaskSource: "short-drama-style-lock",
		Turns: TurnPlan{
			Unit:  "轮",
			Intro: func([]ShotsScene) string { return "" },
			Next:  func([]ShotsScene, int, int) string { return "" },
			Count: CountStyleLockTurn,
		},
	},
	{
		ID:       StepScriptAdapt,
		Label:    "剧本优化",
		Scope:    StepScopeProject,
		Contract: ContractScriptAdapt,
		Inputs: []StepInput{
			{Name: "skill", Label: "技能上下文（skill-context）与用户任务：所选原文章节（每段标段号）、改编方式", Source: "按项目风格档案绑定的技能当前版本；原文取各章保存的 HTML，按编辑器 getText 的规则转成纯文本后按空行分段编号"},
			{Name: "brief", Label: "【项目需求】", Source: "需求文档 brief_json 的片名、用途、平台、口味、约束、不做的事，以及 episode_seconds、segment_seconds 与各集钩子要求，共 1500 字"},
			{Name: "episodePlan", Label: "【分集要求】", Source: "请求里的目标集数或每集秒数（没给时取 brief 的 episode_seconds 按原文字数估，再没有就一章一集）；单段最长秒数取 brief 的 segment_seconds"},
			{Name: "contract", Label: "【受保护输出契约】", Source: "script.adapt@1 schema 生成（一集一块：JSON 头加正文）"},
		},
		Parser:           "每一轮 ScriptAdaptEpisodesFromText 解析出一集（JSON 头 + 至少一个场次头），任务收尾时解析全部轮次的输出",
		Checks:           []string{"CheckScriptAdapt（R-ADAPT-*：每集有场次头、sources 在所选章节内且段号有效、台词行能被台词表读出）"},
		SkillBudgetChars: 32_000,
		SkillLinkedFiles: 3,
		DefaultSkill:     "yz-script-adapt",
		// 闸门先宽：只看解析率，其余指标等评测集多了再加。
		Gate:            StepGate{Metrics: []string{GateParseRate}, ParseRate: 0.9},
		EvalConcurrency: 1,
		EvalSet: []StepEvalChapter{
			{ID: "qichuang-novel-c01", Profile: "live-realism", Label: "《七床》小说第一章（悬疑·真人剧）"},
		},
		TaskSource: "short-drama-script-adapt",
		Turns: TurnPlan{
			Unit:  "集",
			Intro: ScriptAdaptTurnPlan,
			Next:  func(turns []ShotsScene, k int, _ int) string { return ScriptAdaptTurnPrompt(turns, k) },
			Count: func(text string, turn TurnContext) (int, error) { return CountScriptAdaptTurn(text, turn.Sources) },
		},
	},
	{
		ID:       StepScenePalette,
		Label:    "场次色卡",
		Scope:    StepScopeProject,
		Contract: ContractScenePalette,
		Inputs: []StepInput{
			{Name: "skill", Label: "技能上下文（skill-context）与用户任务：各集的场次表（场编号、场次头、内外 / 地点 / 时间、每场开头 300 字）", Source: "按项目风格档案绑定的技能当前版本；场次取各剧集（kind=episode，没有剧集时取原文章节）保存的 HTML 转成的纯文本，按场次头切场（SplitScenes：同一集里连续同地点且内外相同的场次头并成一场），超过总字数预算时每场摘录同比缩短（不少于 80 字）"},
			{Name: "anchor", Label: "【项目风格锚】", Source: "bible_json.style_anchor（风格锁定 style.lock 锁定的；本步骤只读不改）"},
			{Name: "masterPalette", Label: "【主基调色卡】", Source: "bible_json.master_palette 全文：按主角组、配角组、道具组、环境组列出每个颜色的中文名、英文名、HEX、用途"},
			{Name: "profile", Label: "【风格档案：…】", Source: "项目风格档案（profiles/*.json）的名称、画面质感段（style_block.zh，600 字）与光线政策"},
			{Name: "existing", Label: "【已有场次色卡】", Source: "这次不重写的各集的 color_script（承接用，只列场编号、场次头和颜色，4000 字）"},
			{Name: "contract", Label: "【受保护输出契约】", Source: "scene.palette@1 schema 生成（每场一行色彩脚本）"},
		},
		Parser:           "每一轮 ScenePaletteFromText 解析出这一集各场的色彩脚本（缺括号补齐、字段类型不符单字段降级，风格锚行忽略），CountScenePaletteTurn 当轮校验 HEX、色数和承接色；任务收尾时解析全部轮次的输出",
		Checks:           []string{"CheckScenePalette（R-SPAL-*：每场 3～32 色、HEX 是 #RRGGBB、inherits_from 指向已有的场、shared_colors 的 HEX 与承接场一致是 error；场次对不上、与主色卡同名色 HEX 不一致、缺英文名或分组、多写风格锚是 warn）"},
		SkillBudgetChars: 32_000,
		SkillLinkedFiles: 3,
		DefaultSkill:     "yz-scene-palette",
		// 闸门先宽：只看解析率（解析出的场 / 应写的场 ≥ 0.9 才算这一章解析成功）。
		Gate:            StepGate{Metrics: []string{GateParseRate}, ParseRate: 0.9},
		EvalConcurrency: 1,
		EvalSet: []StepEvalChapter{
			{ID: "qichuang-e01", Profile: "live-realism", Label: "《七床》第一集（悬疑·真人剧）"},
		},
		TaskSource: "short-drama-scene-palette",
		Turns: TurnPlan{
			Unit:  "集",
			Intro: ScenePaletteTurnPlan,
			Next:  func(turns []ShotsScene, k int, _ int) string { return ScenePaletteTurnPrompt(turns, k) },
			Count: CountScenePaletteTurn,
		},
	},
	{
		ID:       StepAssetsExtract,
		Label:    "资产设计",
		Scope:    StepScopeUnit,
		Contract: ContractAssetsExtract,
		Inputs: []StepInput{
			{Name: "skill", Label: "技能上下文（skill-context）与用户任务：剧集或章节标题、正文", Source: "按项目风格档案绑定的技能当前版本；正文取单元保存的 HTML，按编辑器 getText 的规则转成纯文本"},
			{Name: "known", Label: "【项目已有资产】", Source: "项目资产清单里的角色、场景、道具（不含归档的），名字、别名和是否缺外观，最多 120 项、4000 字"},
			{Name: "anchor", Label: "【全局风格锚】", Source: "bible_json.style_anchor（风格锁定写的）"},
			{Name: "colorScript", Label: "【本集色彩脚本】", Source: "bible_json.color_script 里这一集的各场（场次色卡写的；集号按场次表的编号：有剧集看剧集序号，没有剧集看原文章节序号），2400 字"},
			{Name: "profile", Label: "【风格档案：…】", Source: "项目风格档案（profiles/*.json）的名称、画面质感段（600 字）与光线政策"},
			{Name: "projectStyle", Label: "【项目画风：…】", Source: "项目画风快照 styleProfileJson 的 title 与 prompt，按小节取到 1200 字"},
			{Name: "contract", Label: "【受保护输出契约】", Source: "assets.extract@1 schema 生成（一个资产一行）"},
		},
		Parser:           "每一轮 CountAssetsExtractTurn 当轮校验（至少一行能用的资产，写了外观要有 prompt_en）；任务收尾时 AssetsExtractFromText 解析全部轮次的输出",
		Checks:           []string{"CheckAssetsExtract（R-ASSET-*：名字非空、分类合法、新资产有 prompt_en；与已有资产同名或同别名的标成合并，同一资产写了几行并成一行）"},
		SkillBudgetChars: 32_000,
		SkillLinkedFiles: 3,
		DefaultSkill:     "yz-assets-extract",
		// 闸门先宽：只看解析率（能用的资产行占读出的行 ≥ 0.9，且至少一行）。
		Gate:            StepGate{Metrics: []string{GateParseRate}, ParseRate: 0.9},
		EvalConcurrency: 1,
		EvalSet: []StepEvalChapter{
			{ID: "qichuang-e01", Profile: "live-realism", Label: "《七床》第一集（悬疑·真人剧）"},
		},
		TaskSource: "short-drama-assets-extract",
		Turns: TurnPlan{
			Unit:  "场",
			Intro: AssetsExtractTurnPlan,
			Next:  func(turns []ShotsScene, k int, _ int) string { return AssetsExtractTurnPrompt(turns, k) },
			Count: CountAssetsExtractTurn,
		},
	},
	{
		ID:       StepChapterShots,
		Label:    "生成分段",
		Scope:    StepScopeUnit,
		Contract: ContractChapterShots,
		Inputs: []StepInput{
			{Name: "skill", Label: "技能上下文（skill-context）与用户任务：章节标题、章节正文", Source: "按项目风格档案绑定的技能当前版本；正文取章节保存的 HTML，按编辑器 getText 的规则转成纯文本"},
			{Name: "projectStyle", Label: "【项目画风：…】", Source: "项目画风快照 styleProfileJson 的 title / prompt，按小节取到 1200 字"},
			{Name: "anchor", Label: "【全局风格锚】", Source: "bible_json.style_anchor（导演风格写的；画风媒介、光线、镜头语言、调色、禁用项）"},
			{Name: "characters", Label: "【已确认角色外观】", Source: "项目里有角色卡版本的角色资产，外貌、服装、体型、随身道具、一致性，共 1600 字"},
			{Name: "episodeSeconds", Label: "【本集时长】", Source: "需求文档 brief_json.episode_seconds"},
			{Name: "contract", Label: "【受保护输出契约】", Source: "chapter.shots@3 schema 生成（一镜一行）"},
			{Name: "assets", Label: "【项目已确认资产】", Source: "项目资产清单（角色、有主版本的场景和道具，最多 60 项）与场景锚点；带 assets 字段要求"},
			{Name: "palette", Label: "【本场色卡】（每一轮）", Source: "bible_json.color_script 里这一场的颜色（中文名、HEX、用途）：按导演风格的场次表对到本单元的场，对不上当前场次表的不写"},
			{Name: "genre", Label: "【戏类卡：…】（每一轮）", Source: "第一轮之前同一模型判出的本场戏类（武戏 / 文戏 / 悬疑戏 / 单人情绪戏，判不出按文戏）；技能包 cards/戏类-<戏类>.md，同一戏类第一次出现时整张带上（8000 字），之后只提一句"},
		},
		Parser:           "任务收尾时服务端 StoryboardRowsFromText 逐行解析（整段 JSON 兜底），按名字绑资产后由 RowsToShotlist 对照剧本台词表转成镜头表；装段后本场色卡以 role=style 绑到这一场的段",
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
		// 评测任务一个一个投：线上文本渠道扛不住十个生成分段同时跑（多数在提交前 502）。
		EvalConcurrency: 1,
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
		Turns: TurnPlan{
			Unit:  "场",
			Intro: ChapterShotsTurnPlan,
			Next: func(turns []ShotsScene, k int, written int) string {
				return ChapterShotsTurnPrompt(turns, k, written+1)
			},
			Count: func(text string, _ TurnContext) (int, error) {
				rows, _, err := StoryboardRowsFromText(text)
				return len(rows), err
			},
		},
	},
}

// 按小节生成（M8 卡 F）的戏类步骤：绑定表里「chapter.shots#<戏类>」一格绑一整套分镜技能。前端仍然只跑 chapter.shots，
// 服务端按项目视频模型单段上限选戏类（DefaultShotGenre），按戏类取绑定的技能、切小节、一轮一节；收尾按戏类换转换器。
// 预算按包放宽到 4 万字符、4 张卡（§5 第 4 条）；评测集还没有按小节的章节，包上传后不能真跑评测（见 docs pending-test）。
const (
	SectionSkillBudgetChars = 40_000
	SectionSkillLinkedFiles = 4
)

func init() {
	defaults := map[string]string{ShotGenreWenxi15: "yz-shots-wenxi15", ShotGenreWenxi30: "yz-shots-wenxi30"}
	base, _ := StepByID(StepChapterShots)
	for _, g := range shotGenres {
		steps = append(steps, Step{
			ID: g.Step, Label: "生成分段 · " + g.Label, Scope: StepScopeUnit, Contract: g.Contract,
			Inputs: []StepInput{
				{Name: "skill", Label: "技能上下文（skill-context）与用户任务：章节标题、章节正文（只供理解前后剧情）", Source: "按项目风格档案绑定的这一戏类的技能当前版本；SKILL.md 点名必读的 4 张卡全部注入"},
				{Name: "projectStyle", Label: "【项目画风：…】", Source: "同 chapter.shots"},
				{Name: "anchor", Label: "【全局风格锚】", Source: "bible_json.style_anchor"},
				{Name: "masterPalette", Label: "【主基调色卡】", Source: "bible_json.master_palette（风格锁定时生成），按分组列色名和 HEX"},
				{Name: "characters", Label: "【已确认角色外观】", Source: "同 chapter.shots"},
				{Name: "episodeSeconds", Label: "【本集时长】", Source: "需求文档 brief_json.episode_seconds"},
				{Name: "assets", Label: "【项目已确认资产】", Source: "同 chapter.shots"},
				{Name: "sections", Label: "【分节生成说明】", Source: "代码按戏类切好的小节表（PlanSections：按台词与动作估时，不超过戏类与视频模型的单段上限，不在接话中间切）"},
				{Name: "turn", Label: "【第 k 节】（每一轮）", Source: "本场第一节带【本场色卡】；本节剧本范围原文、本节台词表（台词 id + 说话人 + 原文）、本节人物与资产（含子卡名「名·变体」）、本节输出要求（技能原生格式、时长上限、片段 / 小节号）"},
			},
			Parser:           "每一轮 ConvertSection 按戏类读技能原生输出（读不出镜头按临时失败重试）；收尾逐节转成分镜行（台词对本节台词表落 line_id、片段级站位灯光音效下沉、室内补光源锚点、配乐字样剥掉），按名字绑资产后 RowsToShotlist 转镜头表；每节原样输出存进 drama_shotlists.sections_json",
			Checks:           []string{"CheckShots（R-SHOT-*；R-SHOT-006/022/024/026 按戏类放宽成 warn）", "CheckSectionDurations（R-SEG-001：一节估时不超过模型单段上限）", "Pack 一节一段（R-SEG-008 每集时长）"},
			SkillBudgetChars: SectionSkillBudgetChars,
			SkillLinkedFiles: SectionSkillLinkedFiles,
			DefaultSkill:     defaults[g.ID],
			Gate:             base.Gate,
			EvalConcurrency:  1,
			TaskSource:       base.TaskSource,
			Rows:             RowsToShotlist,
			Check:            CheckShots,
			Pack:             Pack,
			Turns: TurnPlan{
				Unit:  "节",
				Intro: func([]ShotsScene) string { return "" },
				Next:  func([]ShotsScene, int, int) string { return "" },
				Count: CountSectionTurn(g.ID),
			},
		})
	}
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
