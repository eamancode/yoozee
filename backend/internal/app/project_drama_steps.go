// 短剧流程步骤（M6）：按项目风格档案查绑定、取技能当前版本，在服务端组装提示词（技能上下文 + 代码变量 + 按 schema 生成的契约），
// 再创建文本任务；前端只说「对这一章跑这一步」（生成分段、资产设计，资产设计见 project_drama_assets.go）或「对这个项目跑这一步」（风格锁定、剧本优化、导演风格，
// 见 project_drama_style_lock.go、project_drama_script_adapt.go、project_drama_style_bible.go）。
// 另有管理员读写「风格档案 × 步骤 → 技能」绑定表的接口。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/skills"
)

// DramaProfileRef 是风格档案的 ID 和名字。
type DramaProfileRef struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// DramaStepSkillRef 是一步实际用的技能版本。
type DramaStepSkillRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	VersionID   string `json:"versionId"`
	Version     string `json:"version"`
	ContentHash string `json:"contentHash"`
}

// DramaStepUsage 是一个项目跑某一步时用哪个技能（弹窗里显示「本步使用：…」）。
type DramaStepUsage struct {
	Step      string            `json:"step"`
	StepLabel string            `json:"stepLabel"`
	Contract  string            `json:"contract"`
	Profile   DramaProfileRef   `json:"profile"`
	Skill     DramaStepSkillRef `json:"skill"`
	// Bound 为 false 表示绑定表里没配这一格，用的是步骤默认技能。
	Bound bool `json:"bound"`
}

// DramaStepRunRequest 是「对这一章跑这一步」：只带文本模型选择，提示词由服务端组装。
type DramaStepRunRequest struct {
	Model          string         `json:"model"`
	LogicalModelID string         `json:"logicalModelId"`
	Config         map[string]any `json:"config"`
	// ResumeTaskID 是「从第 N 场继续」：同一章上一次没跑完的生成分段任务，新任务带上它已写完的场接着写。
	ResumeTaskID string `json:"resumeTaskId,omitempty"`
	// Rule 是视频渠道实际能提交的段长，收尾装段时叠在项目生成模型上限上（只收紧）。
	Rule *DramaShotlistRule `json:"rule,omitempty"`
	// ImageTask 是资产设计写完后给新资产出图用的图片任务模板（前端按图片模型组好）；不给时只建资产不出图。
	ImageTask *DramaImageTaskTemplate `json:"imageTask,omitempty"`
	TraceID   string                  `json:"-"`
	RequestID string                  `json:"-"`
}

type DramaStepRunResult struct {
	Task  *model.Task    `json:"task"`
	Usage DramaStepUsage `json:"usage"`
}

// DramaStepPrompt 是服务端为一章组装出的提示词（不建任务，给对照与排查用）。
type DramaStepPrompt struct {
	Usage  DramaStepUsage           `json:"usage"`
	Prompt string                   `json:"prompt"`
	Files  []drama.StepSkillFileUse `json:"files"`
}

// dramaStepAssetRecord 是写进任务 input.canvasAssets 的资产清单（只作记录，模型看的是提示词）。
type dramaStepAssetRecord struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Category string   `json:"category"`
	Anchors  []string `json:"anchors,omitempty"`
}

type dramaStepCharacterRecord struct {
	AssetID    string         `json:"assetId"`
	VersionID  string         `json:"versionId"`
	Name       string         `json:"name"`
	Definition map[string]any `json:"definition"`
}

// dramaStepPlan 是组装好的一次步骤运行。
type dramaStepPlan struct {
	usage  DramaStepUsage
	prompt drama.StepPrompt
	skill  drama.StepSkill
	// scenes 是逐轮续写的轮次：生成分段一场一轮（正文按场次头切，不到两场为空，整章一轮），剧本优化一集一轮。
	scenes []drama.ShotsScene
	// record 是写进任务 input 的记录字段（资产清单、画风、角色、来源单元；只作记录，模型看的是提示词）。
	record map[string]any
	// adapt 是剧本优化的运行参数（metadata.dramaAdapt），收尾按它写剧集。
	adapt *dramaAdaptMeta
	// style 是导演风格的运行参数（metadata.dramaStyle），收尾按它检查场次、排色卡出图。
	style *dramaStyleMeta
	// assets 是资产设计的运行参数（metadata.dramaAssets），收尾按它取本集色彩脚本、排资产出图。
	assets *dramaAssetsMeta
	// shots 是生成分段的逐场注入（metadata.dramaShots）：戏类判定的提示词、每场的色卡段和戏类卡。
	shots *dramaShotsMeta
	// lock 是风格锁定的运行参数（metadata.dramaStyleLock）；referenceImages 是随任务发给模型的参考图（input.referenceImages）。
	lock            *dramaStyleLockMeta
	referenceImages []map[string]any
}

func dramaStep(stepID string) (drama.Step, error) {
	step, ok := drama.StepByID(strings.TrimSpace(stepID))
	if !ok {
		return drama.Step{}, BadAuthRequest(fmt.Sprintf("不支持的短剧流程步骤 %s", stepID))
	}
	return step, nil
}

// DramaStepUsage 返回项目跑这一步用的风格档案和技能版本。
func (s *Service) DramaStepUsage(userID string, projectID string, stepID string) (DramaStepUsage, error) {
	step, err := dramaStep(stepID)
	if err != nil {
		return DramaStepUsage{}, err
	}
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return DramaStepUsage{}, err
	}
	usage, _, err := s.dramaStepSkill(project, step)
	return usage, err
}

// dramaStepSkill 按项目风格档案查绑定（没配用步骤默认技能），读技能当前版本。
func (s *Service) dramaStepSkill(project *model.Project, step drama.Step) (DramaStepUsage, drama.StepSkill, error) {
	profileID := projectDramaContext(project).profileID()
	skillID, bound := step.DefaultSkill, false
	row, err := s.repo.DramaStepBinding(profileID, step.ID)
	switch {
	case err == nil:
		skillID, bound = row.SkillID, true
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return DramaStepUsage{}, drama.StepSkill{}, err
	}
	pkg, err := s.skillDomain().StepSkillPackage(skillID)
	if err != nil {
		return DramaStepUsage{}, drama.StepSkill{}, err
	}
	skill := stepSkillOf(pkg)
	usage := DramaStepUsage{
		Step: step.ID, StepLabel: step.Label, Contract: step.Contract, Bound: bound,
		Profile: dramaProfileRef(profileID),
		Skill:   DramaStepSkillRef{ID: skill.ID, Name: skill.Name, VersionID: skill.VersionID, Version: skill.Version, ContentHash: skill.ContentHash},
	}
	return usage, skill, nil
}

func dramaProfileRef(id string) DramaProfileRef {
	ref := DramaProfileRef{ID: id, Label: id}
	if p, err := drama.BuiltinProfile(id); err == nil && p.Label != "" {
		ref.Label = p.Label
	}
	return ref
}

// DramaStepPrompt 组装一章这一步的提示词，不建任务。
func (s *Service) DramaStepPrompt(userID string, projectID string, unitID string, stepID string) (DramaStepPrompt, error) {
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return DramaStepPrompt{}, err
	}
	plan, err := s.planDramaStep(userID, project, unitID, stepID)
	if err != nil {
		return DramaStepPrompt{}, err
	}
	return DramaStepPrompt{Usage: plan.usage, Prompt: plan.prompt.Text, Files: plan.prompt.Files}, nil
}

// RunDramaStep 对一章跑这一步：服务端组装提示词并创建文本任务，任务 metadata 记下步骤、契约、风格档案、技能版本和内容哈希。
func (s *Service) RunDramaStep(userID string, projectID string, unitID string, stepID string, req DramaStepRunRequest) (DramaStepRunResult, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return DramaStepRunResult{}, err
	}
	plan, err := s.planDramaStep(userID, project, unitID, stepID)
	if err != nil {
		return DramaStepRunResult{}, err
	}
	if plan.assets != nil {
		plan.assets.Images = req.ImageTask.usable()
	}
	run := newDramaRunMeta(plan, unitID)
	if resumeID := strings.TrimSpace(req.ResumeTaskID); resumeID != "" {
		if run, err = s.resumeDramaRun(userID, project.ID, unitID, plan, resumeID); err != nil {
			return DramaStepRunResult{}, err
		}
	}
	taskReq := dramaStepTaskRequest(project.ID, unitID, plan, dramaStepTaskOptions{Model: req.Model, LogicalModelID: req.LogicalModelID, Config: req.Config, Rule: req.Rule, TraceID: req.TraceID, RequestID: req.RequestID})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = run
	if plan.assets != nil && plan.assets.Images {
		// 模板带着渠道配置，和其余任务输入一样加密存，任务结束随密钥一起去掉。
		taskReq.Input["assetImageTask"] = req.ImageTask
	}
	task, err := s.CreateTask(userID, taskReq)
	if err != nil {
		return DramaStepRunResult{}, err
	}
	return DramaStepRunResult{Task: task, Usage: plan.usage}, nil
}

// dramaStepTaskOptions 是建步骤任务时请求带来的模型选择、段长规则和追踪标识。
type dramaStepTaskOptions struct {
	Model          string
	LogicalModelID string
	Config         map[string]any
	Rule           *DramaShotlistRule
	TraceID        string
	RequestID      string
}

// dramaStepTaskRequest 是这一步的文本任务：与原先前端发的章节分镜任务同一形状（canvas_text / storyboard，metadata.source 按步骤），
// metadata 另记步骤、契约、风格档案、技能版本和内容哈希。input 里的资产、画风、角色、来源只作记录，模型看的是提示词。
// unitID 为空是项目级步骤（剧本优化），metadata 不带 chapterId。
func dramaStepTaskRequest(projectID string, unitID string, plan *dramaStepPlan, req dramaStepTaskOptions) CreateTaskRequest {
	step, _ := drama.StepByID(plan.usage.Step)
	skillFiles := make([]map[string]any, 0, len(plan.prompt.Files))
	for _, f := range plan.prompt.Files {
		skillFiles = append(skillFiles, map[string]any{"skillId": plan.skill.ID, "path": f.Path, "sha256": f.SHA256})
	}
	metadata := map[string]any{
		"domainProjectId": projectID,
		"source":          step.TaskSource,
		"skillIds":        []string{plan.skill.ID},
		"skillVersions":   []map[string]any{{"skillId": plan.skill.ID, "versionId": plan.skill.VersionID, "version": plan.skill.Version}},
		"skillFiles":      skillFiles,
		"dramaStep": map[string]any{
			"step":         plan.usage.Step,
			"contract":     plan.usage.Contract,
			"styleProfile": plan.usage.Profile.ID,
			"bound":        plan.usage.Bound,
			"skillId":      plan.skill.ID,
			"versionId":    plan.skill.VersionID,
			"version":      plan.skill.Version,
			"contentHash":  plan.skill.ContentHash,
			"rule":         req.Rule,
		},
	}
	if len(plan.prompt.Truncated) > 0 {
		metadata["dramaStep"].(map[string]any)["truncated"] = plan.prompt.Truncated
	}
	if unitID != "" {
		metadata["chapterId"] = unitID
	}
	if plan.adapt != nil {
		metadata["dramaAdapt"] = plan.adapt
	}
	if plan.style != nil {
		metadata["dramaStyle"] = plan.style
	}
	if plan.assets != nil {
		metadata["dramaAssets"] = plan.assets
	}
	if plan.shots != nil {
		metadata["dramaShots"] = plan.shots
	}
	if plan.lock != nil {
		metadata["dramaStyleLock"] = plan.lock
	}
	// 后端只在 video_ 前缀的任务上兜底 mode，文本任务必须显式带上。
	input := map[string]any{"mode": "text", "metadata": metadata}
	for k, v := range plan.record {
		input[k] = v
	}
	if len(plan.referenceImages) > 0 {
		refs := make([]any, 0, len(plan.referenceImages))
		for _, ref := range plan.referenceImages {
			refs = append(refs, ref)
		}
		input["referenceImages"] = refs
	}
	if req.Config != nil {
		input["config"] = req.Config
	}
	return CreateTaskRequest{
		ProjectID: projectID, Type: "canvas_text", Operation: "storyboard", Prompt: plan.prompt.Text,
		Model: strings.TrimSpace(req.Model), LogicalModelID: strings.TrimSpace(req.LogicalModelID), Input: input,
		TraceID: req.TraceID, RequestID: req.RequestID,
	}
}

// dramaStepTaskMeta 是任务 metadata.dramaStep（dramaStepTaskRequest 写入）。
type dramaStepTaskMeta struct {
	Step         string `json:"step"`
	Contract     string `json:"contract"`
	StyleProfile string `json:"styleProfile"`
	SkillID      string `json:"skillId"`
	VersionID    string `json:"versionId"`
	// Rule 是起任务时带的段长规则（渠道能力），收尾装段用。
	Rule *DramaShotlistRule `json:"rule,omitempty"`
}

// taskDramaStep 从任务输入读 metadata.dramaStep；不是流程步骤任务返回 nil。metadata 不含密钥，不用解密。
func taskDramaStep(inputJSON string) *dramaStepTaskMeta {
	if !strings.Contains(inputJSON, `"dramaStep"`) {
		return nil
	}
	var input struct {
		Metadata struct {
			DramaStep *dramaStepTaskMeta `json:"dramaStep"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil || input.Metadata.DramaStep == nil || strings.TrimSpace(input.Metadata.DramaStep.Step) == "" {
		return nil
	}
	return input.Metadata.DramaStep
}

// planDramaStep 读章节、画风、资产、需求文档和绑定技能，组装提示词。按单元跑的是 chapter.shots 和 assets.extract（章节或剧集都行）。
func (s *Service) planDramaStep(userID string, project *model.Project, unitID string, stepID string) (*dramaStepPlan, error) {
	step, err := dramaStep(stepID)
	if err != nil {
		return nil, err
	}
	if step.Scope == drama.StepScopeProject {
		return nil, BadAuthRequest(fmt.Sprintf("「%s」是项目级步骤，请用 POST /projects/:id/drama/steps/%s/run", step.Label, step.ID))
	}
	if step.ID != drama.StepChapterShots && step.ID != drama.StepAssetsExtract {
		return nil, BadAuthRequest(fmt.Sprintf("步骤 %s 还不能在服务端运行", step.ID))
	}
	unit, err := s.repo.ProjectUnit(project.ID, strings.TrimSpace(unitID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, BadAuthRequest("章节不存在或不属于该项目")
	}
	if err != nil {
		return nil, err
	}
	if step.ID == drama.StepAssetsExtract {
		usage, skill, err := s.dramaStepSkill(project, step)
		if err != nil {
			return nil, err
		}
		return s.planAssetsExtract(userID, project, unit, usage, skill)
	}
	if chapterEditorText(unit.SourceText) == "" {
		return nil, BadAuthRequest("当前章节没有可用于分镜的正文")
	}
	// 只存了预设 ID 的老项目没有画风原文：服务端不带前端的画风目录，请用户在项目设置里重新保存一次画风。
	if projectStyleSnapshot(project).Prompt == "" {
		return nil, BadAuthRequest("请先在项目设置中选择项目画风，再生成分镜")
	}
	usage, skill, err := s.dramaStepSkill(project, step)
	if err != nil {
		return nil, err
	}
	assets, err := s.ProjectAssets(userID, project.ID)
	if err != nil {
		return nil, err
	}
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return nil, err
	}
	return chapterShotsPlan(project, unit, units, assets, usage, skill)
}

func projectStyleSnapshot(project *model.Project) styleProfileDocument {
	var style styleProfileDocument
	if raw := strings.TrimSpace(project.StyleProfileJSON); raw != "" {
		_ = json.Unmarshal([]byte(raw), &style)
	}
	style.Prompt = strings.TrimSpace(style.Prompt)
	return style
}

// chapterShotsPlan 把项目、章节、资产清单和技能版本组装成提示词（不读库，测试直接拿接口 JSON 喂进来）。
// units 是项目全部单元（按导演风格的编号找本单元每场的色彩脚本；评测只给这一章）。
func chapterShotsPlan(project *model.Project, unit *model.ProjectUnit, units []model.ProjectUnit, assets []ProjectAssetSummary, usage DramaStepUsage, skill drama.StepSkill) (*dramaStepPlan, error) {
	style := projectStyleSnapshot(project)
	anchor, _ := projectStyleBible(project.BibleJSON)
	stepAssets, assetRecords := chapterShotsAssets(assets)
	characters, characterRecords := chapterShotsCharacters(assets)
	prompt, err := drama.BuildChapterShotsPrompt(drama.ChapterShotsInput{
		ChapterTitle:   unit.Title,
		SourceText:     chapterEditorText(unit.SourceText),
		Style:          drama.StepStyle{Title: style.Title, Prompt: style.Prompt},
		Characters:     characters,
		Assets:         stepAssets,
		EpisodeSeconds: briefEpisodeSeconds(project.BriefJSON),
		Anchor:         anchor,
		Skill:          skill,
	})
	if err != nil {
		return nil, err
	}
	scenes := drama.ChapterShotsScenes(chapterPlainText(unit.SourceText), unit.Title)
	shots := chapterShotsSceneMeta(project, unit, units, skill)
	record := map[string]any{
		"canvasAssets":        assetRecords,
		"requirements":        "输出可直接写入分镜制作并继续生成分镜图、动作预演和镜头视频的分镜表。",
		"projectStyle":        map[string]any{"presetId": style.PresetID, "title": style.Title, "prompt": style.Prompt},
		"characters":          characterRecords,
		"shotDurationSeconds": 0,
		"shotCount":           0,
	}
	return &dramaStepPlan{usage: usage, prompt: prompt, skill: skill, scenes: scenes, record: record, shots: shots}, nil
}

// briefEpisodeSeconds 是需求文档顶层的 episode_seconds（项目设置里的每集时长）；没设或不是正数返回 0。
func briefEpisodeSeconds(briefJSON string) float64 {
	doc := map[string]any{}
	if raw := strings.TrimSpace(briefJSON); raw != "" {
		_ = json.Unmarshal([]byte(raw), &doc)
	}
	if v, ok := doc["episode_seconds"].(float64); ok && v > 0 {
		return v
	}
	return 0
}

var chapterShotsSceneCategories = []string{"scene", "environment", "location"}

// chapterShotsAssets 是交给生成分段的资产清单：角色要有角色卡版本；场景、道具没出图（entity）也进清单，但要有主版本；
// 归档的不进，最多 60 项。分类名按素材分类表（角色、场景、道具、素材、其他）。
func chapterShotsAssets(assets []ProjectAssetSummary) ([]drama.StepAsset, []dramaStepAssetRecord) {
	var out []drama.StepAsset
	var records []dramaStepAssetRecord
	for _, a := range assets {
		if len(out) >= drama.StepChapterShotsMaxAssets {
			break
		}
		if string(a.Status) == "archived" {
			continue
		}
		category := string(a.Category)
		isCharacter := category == "character"
		switch {
		case isCharacter:
			if a.Character == nil || a.Character.VersionID == "" {
				continue
			}
		case a.MediaType == "image" || a.MediaType == "video" || a.MediaType == "audio":
		case a.MediaType == "entity" && (slices.Contains(chapterShotsSceneCategories, category) || category == "prop"):
		default:
			continue
		}
		if !isCharacter && a.PrimaryVersionID == "" {
			continue
		}
		var definition map[string]any
		if a.Character != nil && a.Character.Definition != nil {
			definition = a.Character.Definition
		} else if a.Visual != nil {
			definition = a.Visual.Definition
		}
		var anchors []string
		if slices.Contains(chapterShotsSceneCategories, category) && a.Visual != nil {
			anchors = sceneAnchorList(a.Visual.Definition["anchors"])
		}
		label := assetCategoryLabel(category)
		if isCharacter {
			label = assetCategoryLabel("character")
		}
		out = append(out, drama.StepAsset{Title: a.Title, CategoryLabel: label, Brief: drama.StepAssetBrief(a.PreviewText, len(definition) > 0, a.Title), Anchors: anchors})
		records = append(records, dramaStepAssetRecord{ID: a.ID, Title: a.Title, Category: category, Anchors: anchors})
	}
	return out, records
}

// chapterShotsCharacters 是写进【已确认角色外观】的角色：有角色卡版本的角色资产（设定取角色卡当前版本）。
func chapterShotsCharacters(assets []ProjectAssetSummary) ([]drama.StepCharacter, []dramaStepCharacterRecord) {
	var out []drama.StepCharacter
	var records []dramaStepCharacterRecord
	for _, a := range assets {
		if string(a.Category) != "character" || a.Character == nil || a.Character.VersionID == "" {
			continue
		}
		definition := a.Character.Definition
		if definition == nil {
			definition = map[string]any{}
		}
		out = append(out, drama.StepCharacter{Name: a.Title, Definition: definition})
		records = append(records, dramaStepCharacterRecord{AssetID: a.ID, VersionID: a.Character.VersionID, Name: a.Title, Definition: definition})
	}
	return out, records
}

var assetCategoryLabels = map[string]string{"character": "角色", "environment": "场景", "prop": "道具", "material": "素材", "other": "其他", "wardrobe": "道具", "weapon": "道具", "accessory": "道具", "style": "素材"}

// assetCategoryLabel 是素材分类的中文名（与前端 assetCategoryLabel 一致：不认识的归「其他」）。
func assetCategoryLabel(category string) string {
	if label, ok := assetCategoryLabels[strings.ToLower(strings.TrimSpace(category))]; ok {
		return label
	}
	return "其他"
}

// ---------------------------------------------------------------- 绑定表（管理员）

// DramaStepBindingCell 是绑定矩阵的一格。
type DramaStepBindingCell struct {
	StyleProfile string `json:"styleProfile"`
	Step         string `json:"step"`
	// SkillID 为空表示没配，用步骤默认技能。
	SkillID   string     `json:"skillId"`
	SkillName string     `json:"skillName,omitempty"`
	Version   string     `json:"version,omitempty"`
	UpdatedBy string     `json:"updatedBy,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

type DramaStepDescriptor struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Contract     string `json:"contract"`
	DefaultSkill string `json:"defaultSkill"`
}

// DramaStepBindingMatrix 是后台「流程技能」页的绑定矩阵。
type DramaStepBindingMatrix struct {
	Profiles []DramaProfileRef      `json:"profiles"`
	Steps    []DramaStepDescriptor  `json:"steps"`
	Cells    []DramaStepBindingCell `json:"cells"`
	Skills   []DramaStepSkillOption `json:"skills"`
}

// DramaStepSkillOption 是可绑定的技能（启用中的技能，带当前版本号）。
type DramaStepSkillOption struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"`
}

type DramaStepBindingsRequest struct {
	Bindings []DramaStepBindingCell `json:"bindings"`
}

// DramaProfiles 是五个风格档案（新建项目、项目设置的选项）与画风对照表（前端按它推荐默认档案）。
type DramaProfiles struct {
	Profiles []DramaProfileRef `json:"profiles"`
	Default  string            `json:"default"`
	StyleMap drama.StyleMap    `json:"styleMap"`
}

func (s *Service) DramaProfiles() (DramaProfiles, error) {
	sm, err := drama.BuiltinStyleMap()
	if err != nil {
		return DramaProfiles{}, err
	}
	out := DramaProfiles{Default: sm.Default, StyleMap: sm}
	for _, id := range drama.BuiltinProfileIDs() {
		out.Profiles = append(out.Profiles, dramaProfileRef(id))
	}
	return out, nil
}

func (s *Service) AdminDramaStepBindings(actor *model.User) (DramaStepBindingMatrix, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return DramaStepBindingMatrix{}, err
	}
	rows, err := s.repo.DramaStepBindings()
	if err != nil {
		return DramaStepBindingMatrix{}, err
	}
	matrix := DramaStepBindingMatrix{Profiles: []DramaProfileRef{}, Steps: []DramaStepDescriptor{}, Cells: []DramaStepBindingCell{}, Skills: []DramaStepSkillOption{}}
	for _, id := range drama.BuiltinProfileIDs() {
		matrix.Profiles = append(matrix.Profiles, dramaProfileRef(id))
	}
	for _, step := range drama.Steps() {
		matrix.Steps = append(matrix.Steps, DramaStepDescriptor{ID: step.ID, Label: step.Label, Contract: step.Contract, DefaultSkill: step.DefaultSkill})
	}
	skills, err := s.repo.SkillsForPackageEnsure()
	if err != nil {
		return DramaStepBindingMatrix{}, err
	}
	byID := map[string]model.Skill{}
	for _, sk := range skills {
		byID[sk.ID] = sk
		if !sk.IsPrivate {
			matrix.Skills = append(matrix.Skills, DramaStepSkillOption{ID: sk.ID, Name: sk.Name, Version: sk.VersionLabel, Source: sk.SourceType})
		}
	}
	slices.SortFunc(matrix.Skills, func(a, b DramaStepSkillOption) int { return strings.Compare(a.Name, b.Name) })
	for _, row := range rows {
		updated := row.UpdatedAt
		cell := DramaStepBindingCell{StyleProfile: row.StyleProfile, Step: row.Step, SkillID: row.SkillID, UpdatedBy: row.UpdatedBy, UpdatedAt: &updated}
		if sk, ok := byID[row.SkillID]; ok {
			cell.SkillName, cell.Version = sk.Name, sk.VersionLabel
		}
		matrix.Cells = append(matrix.Cells, cell)
	}
	return matrix, nil
}

// UpdateDramaStepBindings 写绑定：档案、步骤都要是已知的，技能要存在、启用且公开；SkillID 为空表示删掉这一格（回到默认技能）。
func (s *Service) UpdateDramaStepBindings(actor *model.User, req DramaStepBindingsRequest) (DramaStepBindingMatrix, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return DramaStepBindingMatrix{}, err
	}
	if len(req.Bindings) == 0 || len(req.Bindings) > 100 {
		return DramaStepBindingMatrix{}, BadAuthRequest("一次写 1-100 格绑定")
	}
	rows := make([]model.DramaStepBinding, 0, len(req.Bindings))
	for _, cell := range req.Bindings {
		profile, stepID, skillID := strings.TrimSpace(cell.StyleProfile), strings.TrimSpace(cell.Step), strings.TrimSpace(cell.SkillID)
		if !drama.ValidProfileID(profile) {
			return DramaStepBindingMatrix{}, BadAuthRequest(fmt.Sprintf("未知的风格档案 %s", profile))
		}
		if _, ok := drama.StepByID(stepID); !ok {
			return DramaStepBindingMatrix{}, BadAuthRequest(fmt.Sprintf("未知的流程步骤 %s", stepID))
		}
		if skillID != "" {
			skill, err := s.repo.Skill(skillID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return DramaStepBindingMatrix{}, BadAuthRequest(fmt.Sprintf("技能 %s 不存在或已停用", skillID))
			}
			if err != nil {
				return DramaStepBindingMatrix{}, err
			}
			if skill.IsPrivate {
				return DramaStepBindingMatrix{}, BadAuthRequest(fmt.Sprintf("技能「%s」是私有技能，不能绑定到流程", skill.Name))
			}
			// 绑定前确认技能当前版本能读出 SKILL.md，坏包不进流程。
			pkg, err := s.skillDomain().StepSkillPackage(skillID)
			if err != nil {
				return DramaStepBindingMatrix{}, err
			}
			if !slices.ContainsFunc(pkg.Files, func(f skills.StepPackageFile) bool { return f.Path == "SKILL.md" && !f.Binary }) {
				return DramaStepBindingMatrix{}, BadAuthRequest(fmt.Sprintf("技能「%s」当前版本没有 SKILL.md", skill.Name))
			}
		}
		rows = append(rows, model.DramaStepBinding{ID: newID(), StyleProfile: profile, Step: stepID, SkillID: skillID, UpdatedBy: actor.ID})
	}
	if err := s.repo.SaveDramaStepBindings(rows); err != nil {
		return DramaStepBindingMatrix{}, err
	}
	return s.AdminDramaStepBindings(actor)
}
