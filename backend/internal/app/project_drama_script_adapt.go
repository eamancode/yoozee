// 剧本优化（script.adapt，M7.2 第 3 阶段）：项目级步骤。用户在章节页选原文章节（默认全部还没被剧集引用的），
// 服务端按项目记忆组装提示词、建一个任务，worker 在任务里一轮写一集（逐轮续写机制与生成分段共用 project_drama_run.go），
// 收尾时解析、检查，把每一集写成 kind=episode 的项目单元：ParentID 是占比最大的来源章节，完整来源写在正文开头的注释里
// （不加表不加列）。已是剧本的项目按「只调节奏」跑，原地改写已有剧集。会替换已有剧集、或改写已有镜头表的剧集时要先确认。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// DramaProjectStepRunRequest 是「对这个项目跑一步」（风格锁定、剧本优化、场次色卡）：选哪些单元，剧本优化另有怎么改、写几集，
// 风格锁定另有项目类型、风格文字和参考图，加文本模型选择；提示词由服务端组装。
type DramaProjectStepRunRequest struct {
	Model          string         `json:"model"`
	LogicalModelID string         `json:"logicalModelId"`
	Config         map[string]any `json:"config"`
	// SourceUnitIDs 是来源单元：adapt 是原文章节（默认全部还没被剧集引用的），rhythm 是已有剧集（默认全部）；
	// 场次色卡是要做色卡的剧集（默认全部；项目还没有剧集时是原文章节）。
	SourceUnitIDs []string `json:"sourceUnitIds,omitempty"`
	// ProjectType / StyleText / ReferenceImages 是风格锁定的输入：项目类型（live-action …，不给时取项目锁定记录里的）、
	// 用户写的风格文字、参考图（本人已就绪的图片资源 ID，最多 6 张）。
	ProjectType     string   `json:"projectType,omitempty"`
	StyleText       string   `json:"styleText,omitempty"`
	ReferenceImages []string `json:"referenceImages,omitempty"`
	// LockOnFinish 是风格锁定收尾直接锁定（建项目时只选了画风预设，按画风当场生成主色卡）。
	LockOnFinish bool `json:"lockOnFinish,omitempty"`
	// Mode 是 adapt（小说转剧本，默认）或 rhythm（已是剧本，只调节奏）。
	Mode string `json:"mode,omitempty"`
	// TargetEpisodes / EpisodeSeconds 二选一：写几集，或每集多少秒（按原文字数估集数）；都不给时取需求文档每集时长，再没有就一章一集。
	TargetEpisodes int     `json:"targetEpisodes,omitempty"`
	EpisodeSeconds float64 `json:"episodeSeconds,omitempty"`
	// Replace 确认替换引用了所选章节的已有剧集（连同它们的镜头表和段），或改写已有镜头表的剧集。
	Replace bool `json:"replace,omitempty"`
	// ResumeTaskID 是「从第 N 集继续」：上一次没写完的剧本优化任务。
	ResumeTaskID string `json:"resumeTaskId,omitempty"`
	TraceID      string `json:"-"`
	RequestID    string `json:"-"`
	// ChainAssets 是场次色卡写完后要不要按集依次接着提取资产（不给时接，false 不接）；剧本优化给了会传给接着跑的场次色卡。
	// AssetImageTask 是接着提取后给新资产出图的图片任务模板（前端按图片模型组好，和任务输入一起加密存）；不给时只提取不出图。
	ChainAssets    *bool                   `json:"chainAssets,omitempty"`
	AssetImageTask *DramaImageTaskTemplate `json:"assetImageTask,omitempty"`
	// chainedFrom 是服务端自动接着建任务时的上一步任务（metadata.chainedFrom）。
	chainedFrom string
}

// dramaAdaptMeta 是任务 metadata.dramaAdapt：收尾按它写剧集。
type dramaAdaptMeta struct {
	Mode string `json:"mode"`
	// SourceUnitIDs 是来源单元（按项目里的顺序）。
	SourceUnitIDs []string `json:"sourceUnitIds"`
	// Paragraphs 是来源原文章节 → 段数（检查 sources 的段号用）。
	Paragraphs map[string]int `json:"paragraphs,omitempty"`
	Episodes   int            `json:"episodes"`
	// ReplaceUnitIDs 是这次要替换掉的已有剧集（adapt）或原地改写的剧集（rhythm，与 SourceUnitIDs 相同）。
	ReplaceUnitIDs []string `json:"replaceUnitIds,omitempty"`
	// ChainAssets 是请求里给的「接着提取资产」开关（没给为空），传给写完剧集后接着跑的场次色卡。
	ChainAssets *bool `json:"chainAssets,omitempty"`
}

// DramaEpisodeImpact 是重跑会动到的一集：替换（adapt）或改写（rhythm）前在确认框里列出。
type DramaEpisodeImpact struct {
	UnitID      string `json:"unitId"`
	Title       string `json:"title"`
	Shots       int    `json:"shots"`
	HasShotlist bool   `json:"hasShotlist"`
}

// DramaEpisodeView 是一集剧集和它的来源（章节页「原文 / 剧集」互相跳转、剧本优化对话框选章节用）。
type DramaEpisodeView struct {
	UnitID    string              `json:"unitId"`
	Title     string              `json:"title"`
	ParentID  string              `json:"parentId,omitempty"`
	Position  int                 `json:"position"`
	WordCount int                 `json:"wordCount"`
	Sources   []drama.AdaptSource `json:"sources"`
	// Shots / HasShotlist 是这一集已有几段、有没有镜头表。
	Shots       int  `json:"shots"`
	HasShotlist bool `json:"hasShotlist"`
}

// DramaEpisodesView 是项目的剧集与已改编的原文章节。
type DramaEpisodesView struct {
	Episodes []DramaEpisodeView `json:"episodes"`
	// AdaptedChapterIDs 是已被剧集引用的原文章节（来源注释或 ParentID）。
	AdaptedChapterIDs []string `json:"adaptedChapterIds"`
}

// DramaScriptAdaptEpisode 是收尾写出的一集（任务 result.dramaStep.episodes）。
type DramaScriptAdaptEpisode struct {
	UnitID   string              `json:"unitId"`
	No       int                 `json:"no"`
	Title    string              `json:"title"`
	ParentID string              `json:"parentId,omitempty"`
	Sources  []drama.AdaptSource `json:"sources"`
	Scenes   int                 `json:"scenes"`
}

// DramaScriptAdaptOutcome 是剧本优化收尾写进任务结果 result.dramaStep 的摘要。
type DramaScriptAdaptOutcome struct {
	Step     string                    `json:"step"`
	Status   string                    `json:"status"`
	Mode     string                    `json:"mode"`
	Episodes []DramaScriptAdaptEpisode `json:"episodes"`
	// Replaced 是被整体替换掉的已有剧集；Rewritten 是原地改写的剧集（调节奏）。
	Replaced  []string              `json:"replaced,omitempty"`
	Rewritten []string              `json:"rewritten,omitempty"`
	Parse     drama.AdaptParseStats `json:"parse"`
	Errors    int                   `json:"errors"`
	Warnings  int                   `json:"warnings"`
	// Items 是检查提醒（最多 50 条）。
	Items []drama.CheckItem `json:"items,omitempty"`
	// Chained 是写完剧集后自动接着建的场次色卡任务。
	Chained *DramaChainedTask `json:"chained,omitempty"`
}

const dramaAdaptOutcomeItems = 50

// RunDramaProjectStep 对整个项目跑一步（风格锁定、剧本优化、场次色卡）：服务端组装提示词并创建文本任务。
func (s *Service) RunDramaProjectStep(userID string, projectID string, stepID string, req DramaProjectStepRunRequest) (DramaStepRunResult, error) {
	step, err := dramaStep(stepID)
	if err != nil {
		return DramaStepRunResult{}, err
	}
	if step.Scope != drama.StepScopeProject {
		return DramaStepRunResult{}, BadAuthRequest(fmt.Sprintf("「%s」按章节跑，请用 POST /projects/:id/units/:unitId/drama/steps/%s/run", step.Label, step.ID))
	}
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return DramaStepRunResult{}, err
	}
	var plan *dramaStepPlan
	switch step.ID {
	case drama.StepStyleLock:
		plan, err = s.planStyleLock(userID, project, step, req)
	case drama.StepScriptAdapt:
		plan, err = s.planScriptAdapt(project, step, req)
		if plan != nil {
			plan.adapt.ChainAssets = req.ChainAssets
		}
	case drama.StepScenePalette:
		plan, err = s.planScenePalette(project, step, req)
		if plan != nil {
			plan.palette.ChainedFrom = req.chainedFrom
			plan.palette.ChainAssets = req.ChainAssets == nil || *req.ChainAssets
		}
	default:
		err = BadAuthRequest(fmt.Sprintf("步骤 %s 还不能在服务端运行", step.ID))
	}
	if err != nil {
		return DramaStepRunResult{}, err
	}
	run := newDramaRunMeta(plan, "")
	if resumeID := strings.TrimSpace(req.ResumeTaskID); resumeID != "" {
		if run, err = s.resumeDramaRun(userID, project.ID, "", plan, resumeID); err != nil {
			return DramaStepRunResult{}, err
		}
	}
	taskReq := dramaStepTaskRequest(project.ID, "", plan, dramaStepTaskOptions{Model: req.Model, LogicalModelID: req.LogicalModelID, Config: req.Config, TraceID: req.TraceID, RequestID: req.RequestID})
	metadata := taskReq.Input["metadata"].(map[string]any)
	metadata["dramaRun"] = run
	if req.chainedFrom != "" {
		metadata["chainedFrom"] = req.chainedFrom
	}
	// 接着提取资产的出图模板带着渠道配置，和其余任务输入一样加密存，任务结束随密钥一起去掉。
	if (step.ID == drama.StepScriptAdapt || step.ID == drama.StepScenePalette) && req.AssetImageTask.usable() && (req.ChainAssets == nil || *req.ChainAssets) {
		taskReq.Input["assetImageTask"] = req.AssetImageTask
	}
	task, err := s.CreateTask(userID, taskReq)
	var modelErr *ModelError
	if len(plan.referenceImages) > 0 && errors.As(err, &modelErr) && modelErr.AppError != nil && modelErr.ErrorCode == ErrCodeModelCapabilityNotSupported {
		// 风格分析带了参考图、所选文本模型没开图片输入：说清楚是要换能读图的模型（原错误只说「不支持当前请求」）。
		modelErr.AppError.Message = "参考图要用能读图的文本模型：所选模型不支持图片输入，换一个模型，或去掉参考图只写风格描述"
	}
	if err != nil {
		return DramaStepRunResult{}, err
	}
	return DramaStepRunResult{Task: task, Usage: plan.usage}, nil
}

// planScriptAdapt 选来源、算集数、查会动到的已有剧集，组装提示词。
func (s *Service) planScriptAdapt(project *model.Project, step drama.Step, req DramaProjectStepRunRequest) (*dramaStepPlan, error) {
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = drama.ScriptAdaptModeAdapt
	}
	if mode != drama.ScriptAdaptModeAdapt && mode != drama.ScriptAdaptModeRhythm {
		return nil, BadAuthRequest("mode 只能是 adapt（小说转剧本）或 rhythm（只调节奏）")
	}
	if req.TargetEpisodes < 0 || req.TargetEpisodes > drama.ScriptAdaptMaxEpisodes {
		return nil, BadAuthRequest(fmt.Sprintf("目标集数应在 1 到 %d 之间", drama.ScriptAdaptMaxEpisodes))
	}
	if req.EpisodeSeconds != 0 && (req.EpisodeSeconds < 5 || req.EpisodeSeconds > 3600) {
		return nil, BadAuthRequest("每集秒数应在 5 到 3600 之间")
	}
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(units, func(i, j int) bool { return units[i].Position < units[j].Position })
	sources, err := scriptAdaptSourceUnits(units, mode, req.SourceUnitIDs)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(sources))
	for _, u := range sources {
		ids = append(ids, u.ID)
	}
	replace := scriptAdaptReplaced(units, mode, ids)
	if len(replace) > 0 && !req.Replace {
		impacts, err := s.dramaEpisodeImpacts(project.ID, units, replace)
		if err != nil {
			return nil, err
		}
		// 调节奏只改正文；没有镜头表和段的剧集不用确认。
		if mode == drama.ScriptAdaptModeAdapt || slices.ContainsFunc(impacts, func(i DramaEpisodeImpact) bool { return i.Shots > 0 || i.HasShotlist }) {
			return nil, scriptAdaptReplaceRequired(mode, impacts)
		}
	}
	usage, skill, err := s.dramaStepSkill(project, step, nil)
	if err != nil {
		return nil, err
	}
	return scriptAdaptPlan(project, sources, mode, req.TargetEpisodes, req.EpisodeSeconds, replace, usage, skill)
}

// scriptAdaptPlan 把来源单元、改编方式和技能版本组装成提示词与运行参数（不读库，评测直接拿评测集的章节喂进来）。
// 集数：给了集数用集数，给了每集秒数按原文字数估，都没给就一章一集；提示词里的每集时长没给时取需求文档。
func scriptAdaptPlan(project *model.Project, sources []model.ProjectUnit, mode string, targetEpisodes int, episodeSeconds float64, replace []string, usage DramaStepUsage, skill drama.StepSkill) (*dramaStepPlan, error) {
	inputs := make([]drama.ScriptAdaptSource, 0, len(sources))
	ids := make([]string, 0, len(sources))
	paragraphs := map[string]int{}
	total := 0
	for _, u := range sources {
		text := chapterEditorText(u.SourceText)
		if text == "" {
			return nil, BadAuthRequest(fmt.Sprintf("「%s」没有正文", u.Title))
		}
		total += drama.JSLen(text)
		inputs = append(inputs, drama.ScriptAdaptSource{UnitID: u.ID, Title: u.Title, Text: text})
		ids = append(ids, u.ID)
		if mode == drama.ScriptAdaptModeAdapt {
			paragraphs[u.ID] = len(drama.ScriptAdaptParagraphs(text))
		}
	}
	if total > drama.ScriptAdaptMaxSourceChars {
		return nil, BadAuthRequest(fmt.Sprintf("所选正文共 %d 字，一次最多 %d 字，请少选几章分几次优化", total, drama.ScriptAdaptMaxSourceChars))
	}
	episodes := drama.ScriptAdaptEpisodeCount(mode, inputs, targetEpisodes, episodeSeconds)
	if episodeSeconds <= 0 {
		episodeSeconds = briefEpisodeSeconds(project.BriefJSON)
	}
	prompt, err := drama.BuildScriptAdaptPrompt(drama.ScriptAdaptInput{
		Mode: mode, Sources: inputs, Episodes: episodes, EpisodeSeconds: episodeSeconds,
		SegmentSeconds: float64(briefSegmentSeconds(project.BriefJSON)), BriefJSON: project.BriefJSON, Skill: skill,
		Anchor: projectLockedAnchor(project.BibleJSON),
	})
	if err != nil {
		return nil, err
	}
	records := make([]map[string]any, 0, len(sources))
	for _, u := range sources {
		records = append(records, map[string]any{"unitId": u.ID, "title": u.Title, "kind": string(u.Kind)})
	}
	adapt := &dramaAdaptMeta{Mode: mode, SourceUnitIDs: ids, Episodes: episodes, ReplaceUnitIDs: replace}
	if mode == drama.ScriptAdaptModeAdapt {
		adapt.Paragraphs = paragraphs
	}
	return &dramaStepPlan{
		usage: usage, prompt: prompt, skill: skill,
		scenes: drama.ScriptAdaptTurns(mode, inputs, episodes),
		record: map[string]any{"requirements": "输出可直接进入提取资产和生成分段的短剧剧本，一集一块。", "dramaSources": records},
		adapt:  adapt,
	}, nil
}

// scriptAdaptSourceUnits 是这次的来源单元（按项目顺序）：点名的要属于项目、类型对；没点名时 adapt 取全部还没被剧集引用的原文章节，
// rhythm 取全部剧集。
func scriptAdaptSourceUnits(units []model.ProjectUnit, mode string, requested []string) ([]model.ProjectUnit, error) {
	kind := model.ProjectUnitKindChapter
	if mode == drama.ScriptAdaptModeRhythm {
		kind = model.ProjectUnitKindEpisode
	}
	var out []model.ProjectUnit
	if len(requested) == 0 {
		adapted := adaptedChapterIDs(units)
		for _, u := range units {
			if u.Kind == kind && (kind == model.ProjectUnitKindEpisode || !adapted[u.ID]) {
				out = append(out, u)
			}
		}
		if len(out) == 0 {
			if kind == model.ProjectUnitKindEpisode {
				return nil, BadAuthRequest("项目里还没有剧集，先导入剧本或用「小说转剧本」改编")
			}
			return nil, BadAuthRequest("没有还没改编的原文章节；要重新改编请在对话框里勾选章节")
		}
		return out, nil
	}
	want := map[string]bool{}
	for _, id := range requested {
		want[strings.TrimSpace(id)] = true
	}
	for _, u := range units {
		if !want[u.ID] {
			continue
		}
		if u.Kind != kind {
			if kind == model.ProjectUnitKindEpisode {
				return nil, BadAuthRequest(fmt.Sprintf("「%s」不是剧集，只调节奏只能选剧集", u.Title))
			}
			return nil, BadAuthRequest(fmt.Sprintf("「%s」不是原文章节，小说转剧本只能选原文章节", u.Title))
		}
		out = append(out, u)
		delete(want, u.ID)
	}
	if len(want) > 0 {
		return nil, BadAuthRequest("所选单元不存在或不属于该项目")
	}
	return out, nil
}

// unitEpisodeSources 是一集剧集的来源：正文开头的来源注释；没有注释但有 ParentID 时按整章算。
func unitEpisodeSources(u model.ProjectUnit) []drama.AdaptSource {
	if sources := drama.EpisodeSources(u.SourceText); len(sources) > 0 {
		return sources
	}
	if u.ParentID != "" {
		return []drama.AdaptSource{{UnitID: u.ParentID}}
	}
	return []drama.AdaptSource{}
}

// adaptedChapterIDs 是已被剧集引用的原文章节。
func adaptedChapterIDs(units []model.ProjectUnit) map[string]bool {
	out := map[string]bool{}
	for _, u := range units {
		if u.Kind != model.ProjectUnitKindEpisode {
			continue
		}
		for _, src := range unitEpisodeSources(u) {
			out[src.UnitID] = true
		}
	}
	return out
}

// scriptAdaptReplaced 是这次会动到的已有剧集：adapt 是引用了所选章节的剧集（整体替换），rhythm 是所选剧集（原地改写）。
func scriptAdaptReplaced(units []model.ProjectUnit, mode string, sourceIDs []string) []string {
	if mode == drama.ScriptAdaptModeRhythm {
		return slices.Clone(sourceIDs)
	}
	var out []string
	for _, u := range units {
		if u.Kind != model.ProjectUnitKindEpisode {
			continue
		}
		if slices.ContainsFunc(unitEpisodeSources(u), func(src drama.AdaptSource) bool { return slices.Contains(sourceIDs, src.UnitID) }) {
			out = append(out, u.ID)
		}
	}
	return out
}

// dramaEpisodeImpacts 数这些剧集各有几段、有没有镜头表。
func (s *Service) dramaEpisodeImpacts(projectID string, units []model.ProjectUnit, ids []string) ([]DramaEpisodeImpact, error) {
	shots, err := s.repo.ProjectShots(projectID)
	if err != nil {
		return nil, err
	}
	shotlists, err := s.repo.DramaShotlistUnitIDs(projectID)
	if err != nil {
		return nil, err
	}
	count := map[string]int{}
	for _, shot := range shots {
		count[shot.UnitID]++
	}
	var out []DramaEpisodeImpact
	for _, u := range units {
		if slices.Contains(ids, u.ID) {
			out = append(out, DramaEpisodeImpact{UnitID: u.ID, Title: u.Title, Shots: count[u.ID], HasShotlist: slices.Contains(shotlists, u.ID)})
		}
	}
	return out, nil
}

func scriptAdaptReplaceRequired(mode string, impacts []DramaEpisodeImpact) error {
	message := fmt.Sprintf("这次会替换已有的 %d 集剧集（连同它们的镜头表和段），请确认后再跑", len(impacts))
	if mode == drama.ScriptAdaptModeRhythm {
		message = "这次会改写已有镜头表或段的剧集正文，镜头表和段不会自动更新，请确认后再跑"
	}
	err := NewAppError(http.StatusConflict, message)
	err.Reason = ReasonDramaEpisodesReplace
	err.Details = map[string]any{"mode": mode, "episodes": impacts}
	return err
}

// DramaEpisodes 列出项目的剧集、各自的来源和镜头表情况，以及已改编的原文章节。
func (s *Service) DramaEpisodes(userID string, projectID string) (DramaEpisodesView, error) {
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return DramaEpisodesView{}, err
	}
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return DramaEpisodesView{}, err
	}
	sort.SliceStable(units, func(i, j int) bool { return units[i].Position < units[j].Position })
	var ids []string
	for _, u := range units {
		if u.Kind == model.ProjectUnitKindEpisode {
			ids = append(ids, u.ID)
		}
	}
	impacts, err := s.dramaEpisodeImpacts(project.ID, units, ids)
	if err != nil {
		return DramaEpisodesView{}, err
	}
	view := DramaEpisodesView{Episodes: []DramaEpisodeView{}, AdaptedChapterIDs: []string{}}
	byID := map[string]DramaEpisodeImpact{}
	for _, i := range impacts {
		byID[i.UnitID] = i
	}
	for _, u := range units {
		if u.Kind != model.ProjectUnitKindEpisode {
			continue
		}
		impact := byID[u.ID]
		view.Episodes = append(view.Episodes, DramaEpisodeView{UnitID: u.ID, Title: u.Title, ParentID: u.ParentID, Position: u.Position, WordCount: u.WordCount, Sources: unitEpisodeSources(u), Shots: impact.Shots, HasShotlist: impact.HasShotlist})
	}
	adapted := adaptedChapterIDs(units)
	for _, u := range units {
		if u.Kind == model.ProjectUnitKindChapter && adapted[u.ID] {
			view.AdaptedChapterIDs = append(view.AdaptedChapterIDs, u.ID)
		}
	}
	return view, nil
}

// ---------------------------------------------------------------- 收尾

// taskDramaAdapt 从任务输入读 metadata.dramaAdapt。
func taskDramaAdapt(inputJSON string) *dramaAdaptMeta {
	var input struct {
		Metadata struct {
			DramaAdapt *dramaAdaptMeta `json:"dramaAdapt"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil {
		return nil
	}
	return input.Metadata.DramaAdapt
}

// applyScriptAdaptTask 是剧本优化的 Apply：模型输出 → 剧集 → 检查 → 写库。检查有 error 时不写库、任务失败（原文和已有剧集都不动）。
func (s *Service) applyScriptAdaptTask(task model.Task, meta *dramaStepTaskMeta, result map[string]interface{}) (DramaScriptAdaptOutcome, error) {
	adapt := taskDramaAdapt(task.InputJSON)
	var input struct {
		Metadata struct {
			ProjectID string `json:"domainProjectId"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal([]byte(task.InputJSON), &input)
	projectID := firstNonEmpty(strings.TrimSpace(input.Metadata.ProjectID), strings.TrimSpace(task.ProjectID))
	if adapt == nil || projectID == "" || len(adapt.SourceUnitIDs) == 0 {
		return DramaScriptAdaptOutcome{}, errors.New("剧本优化任务缺少项目或运行参数")
	}
	// 写库前再确认项目仍属于任务的用户、没有归档（建任务时查过，收尾可能隔了很久）。
	if _, err := s.activeProjectForUser(task.UserID, projectID); err != nil {
		return DramaScriptAdaptOutcome{}, err
	}
	text, _ := result["text"].(string)
	episodes, stats := drama.ScriptAdaptEpisodesFromText(text)
	report := drama.CheckScriptAdapt(episodes, drama.ScriptAdaptCheckContext{Mode: adapt.Mode, Paragraphs: adapt.Paragraphs, Episodes: adapt.Episodes})
	if report.Errors > 0 {
		for _, item := range report.Items {
			if item.Level == "error" {
				return DramaScriptAdaptOutcome{}, fmt.Errorf("剧本检查有 %d 个问题，没有写入剧集：%s %s", report.Errors, item.ShotID, item.Message)
			}
		}
	}
	if adapt.Mode == drama.ScriptAdaptModeRhythm && len(episodes) != len(adapt.SourceUnitIDs) {
		return DramaScriptAdaptOutcome{}, fmt.Errorf("只调节奏应改写 %d 集，解析出 %d 集，没有写入", len(adapt.SourceUnitIDs), len(episodes))
	}
	units, err := s.repo.ProjectUnits(projectID)
	if err != nil {
		return DramaScriptAdaptOutcome{}, err
	}
	outcome := DramaScriptAdaptOutcome{Step: meta.Step, Status: DramaStepOutcomeWritten, Mode: adapt.Mode, Episodes: []DramaScriptAdaptEpisode{}, Parse: stats, Errors: report.Errors, Warnings: report.Warnings}
	for _, item := range report.Items {
		if len(outcome.Items) < dramaAdaptOutcomeItems {
			outcome.Items = append(outcome.Items, item)
		}
	}
	byID := map[string]model.ProjectUnit{}
	for _, u := range units {
		byID[u.ID] = u
	}
	now := time.Now()
	var create, update []model.ProjectUnit
	var deleteIDs []string
	if adapt.Mode == drama.ScriptAdaptModeRhythm {
		for k, ep := range episodes {
			current, ok := byID[adapt.SourceUnitIDs[k]]
			if !ok {
				return DramaScriptAdaptOutcome{}, fmt.Errorf("要改写的剧集 %s 已被删除，没有写入", adapt.SourceUnitIDs[k])
			}
			current.SourceText = drama.WithEpisodeSources(drama.EpisodeSourceText(nil, ep.Text), drama.EpisodeSources(current.SourceText))
			current.WordCount = model.ProjectUnitWordCount(current.SourceText)
			current.Status = model.ProjectUnitStatusReady
			current.UpdatedAt = now
			update = append(update, current)
			outcome.Rewritten = append(outcome.Rewritten, current.ID)
			outcome.Episodes = append(outcome.Episodes, DramaScriptAdaptEpisode{UnitID: current.ID, No: k + 1, Title: current.Title, ParentID: current.ParentID, Sources: unitEpisodeSources(current), Scenes: len(ep.Scenes)})
		}
	} else {
		for _, id := range adapt.ReplaceUnitIDs {
			if _, ok := byID[id]; ok {
				deleteIDs = append(deleteIDs, id)
			}
		}
		for k, ep := range episodes {
			title := ep.Title
			if title == "" {
				title = fmt.Sprintf("第 %d 集", k+1)
			}
			if !model.ValidProjectUnitTitle(title) {
				title = string([]rune(title)[:model.ProjectUnitTitleMaxRunes])
			}
			sourceText := drama.EpisodeSourceText(ep.Sources, ep.Text)
			unit := model.ProjectUnit{
				ID: newID(), ProjectID: projectID, ParentID: drama.MainAdaptSource(ep.Sources), Kind: model.ProjectUnitKindEpisode, Title: title,
				SourceText: sourceText, WordCount: model.ProjectUnitWordCount(sourceText), Status: model.ProjectUnitStatusReady, CreatedAt: now, UpdatedAt: now,
			}
			create = append(create, unit)
			outcome.Episodes = append(outcome.Episodes, DramaScriptAdaptEpisode{UnitID: unit.ID, No: k + 1, Title: title, ParentID: unit.ParentID, Sources: ep.Sources, Scenes: len(ep.Scenes)})
		}
		outcome.Replaced = deleteIDs
	}
	order := scriptAdaptUnitOrder(units, deleteIDs, create)
	if err := s.repo.ApplyDramaEpisodes(projectID, deleteIDs, create, update, order); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DramaScriptAdaptOutcome{}, errors.New("要改写的剧集已被删除，没有写入")
		}
		return DramaScriptAdaptOutcome{}, err
	}
	return outcome, nil
}

// scriptAdaptUnitOrder 是写库后整个项目的单元顺序：原文章节在前（原顺序），剧集排在所有章节之后，
// 按主要来源章节的位置排（没有来源的导入剧本保持原来的相对位置），同一来源按集号。
func scriptAdaptUnitOrder(units []model.ProjectUnit, deleted []string, created []model.ProjectUnit) []string {
	sorted := slices.Clone(units)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Position < sorted[j].Position })
	chapterPos := map[string]int{}
	var order []string
	var episodes []model.ProjectUnit
	for _, u := range sorted {
		if slices.Contains(deleted, u.ID) {
			continue
		}
		if u.Kind == model.ProjectUnitKindEpisode {
			episodes = append(episodes, u)
			continue
		}
		chapterPos[u.ID] = len(order)
		order = append(order, u.ID)
	}
	episodes = append(episodes, created...)
	key := func(index int, u model.ProjectUnit) int {
		if pos, ok := chapterPos[u.ParentID]; ok {
			return pos
		}
		return len(chapterPos) + index
	}
	keys := make(map[string]int, len(episodes))
	for i, u := range episodes {
		keys[u.ID] = key(i, u)
	}
	sort.SliceStable(episodes, func(i, j int) bool { return keys[episodes[i].ID] < keys[episodes[j].ID] })
	for _, u := range episodes {
		order = append(order, u.ID)
	}
	return order
}
