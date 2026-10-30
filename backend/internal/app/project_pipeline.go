// 建项后的流水线与停靠点：拆剧本 → 导演定画风 → 场次色卡 → 角色场景资产（提取 + 出图）→ 分镜 → 装段 → 出视频 → 剪辑。
// 一步收尾时由服务端按项目的生成模式（drama.ShouldAutoContinue）决定要不要接着建下一步的任务；所有链式触发点都走这里：
//   - 剧本优化写完剧集 → 导演定画风（交给导演分析且还没锁定）或场次色卡（continueAfterScript）；
//   - 流水线跑的导演定画风锁定后 → 场次色卡（continueAfterStyle）；
//   - 场次色卡写完 → 按集依次提取资产（chainAssetsAfterScenePalette）；
//   - 各集都提取完、新资产都出完图 → 按集依次生成分镜（continueAfterAssets，提取收尾和出图任务结束时都会问一次），
//     装段在生成分镜的收尾里由代码完成；
//   - 出视频在任何模式下都不自动建任务（ShouldAutoContinue 写死），停在那里等人带着预估花费点。
// 同一环节里的接续（按集依次提取、按集依次生成分镜）是这一环节自己的队列，不看生成模式。改生成模式只影响之后的接续，
// 已经停住的环节不追补。文本模型配置随接着建的任务一路带下去（出图任务里放在模板的 next 上），和任务输入一样加密存。
// 「停在哪、等你做什么」由 projectPipeline 按产物、任务和资产状态推出，写进进度（progress.pipeline）。

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// DramaPipelineText 是接着建文本步骤任务要用的模型选择和配置（前端按文本模型组好）。
type DramaPipelineText struct {
	Model          string         `json:"model"`
	LogicalModelID string         `json:"logicalModelId,omitempty"`
	Config         map[string]any `json:"config,omitempty"`
}

func (t *DramaPipelineText) usable() bool {
	return t != nil && (strings.TrimSpace(t.Model) != "" || strings.TrimSpace(t.LogicalModelID) != "")
}

// DramaPipelineNextRequest 是流水线「继续」：从当前环节开始做，之后按生成模式接续。文本模型必填；ImageTask 是给新资产出图的模板，
// 不给时提取资产只建资产不出图（停在资产，等配好图片模型再继续）。
type DramaPipelineNextRequest struct {
	Model          string                  `json:"model"`
	LogicalModelID string                  `json:"logicalModelId"`
	Config         map[string]any          `json:"config"`
	ImageTask      *DramaImageTaskTemplate `json:"imageTask,omitempty"`
	TraceID        string                  `json:"-"`
	RequestID      string                  `json:"-"`
}

// DramaPipelineNextResult 是「继续」开始的环节和建的任务（排出图时没有任务，Images 是排队结果）。
type DramaPipelineNextResult struct {
	Stage    string                 `json:"stage"`
	Task     *model.Task            `json:"task,omitempty"`
	Images   *DramaImageQueueResult `json:"images,omitempty"`
	Pipeline drama.Pipeline         `json:"pipeline"`
}

// dramaPipelineMeta 是任务 metadata.dramaPipeline：同一环节里还要接着做的单元（按集依次生成分镜）。
type dramaPipelineMeta struct {
	Queue []string `json:"queue"`
}

// dramaPipelineMu 让同一进程里「资产都齐了 → 开始生成分镜」只判一次（几张图同时出完时）。
var dramaPipelineMu sync.Mutex

// shouldAutoContinue 是项目这一步做完后要不要自动开始 next 这一环。
func (s *Service) shouldAutoContinue(project *model.Project, next string) bool {
	return project != nil && drama.ShouldAutoContinue(project.RunMode, next)
}

// pipelineState 是推流水线、找下一步要用的项目状态。
type pipelineState struct {
	project  *model.Project
	episodes []model.ProjectUnit
	chapters []model.ProjectUnit
	// active 是还在排队或运行的任务：按步骤 ID，另有 image（资产出图）、video。
	active map[string][]model.Task
	// latest 是每个「步骤|单元」最近的一个任务（项目级步骤单元为空）。
	latest map[string]model.Task
	// lastStep 是最近一个成功收尾的流程步骤任务（链式接续没接上时，原因记在它的结果里）。
	lastStep *model.Task
	// extracted 是提取资产成功过的单元。
	extracted map[string]bool
	shotlist  map[string]bool
	draft     map[string]int
	packed    map[string]bool
	assets    pipelineAssets
	locked    bool
	lockedVia string
}

// pipelineAssets 是资产设计建的角色、场景、道具的出图情况。
type pipelineAssets struct {
	Total, Imaged, Pending, Failed int
}

const (
	pipelineKeyImage = "image"
	pipelineKeyVideo = "video"
)

// taskChapterID 读任务 metadata.chapterId。
func taskChapterID(inputJSON string) string {
	var input struct {
		Metadata struct {
			ChapterID string `json:"chapterId"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal([]byte(inputJSON), &input)
	return strings.TrimSpace(input.Metadata.ChapterID)
}

func taskActive(task model.Task) bool {
	return task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning
}

// loadPipelineState 读项目的单元、流程任务、镜头表、段和资产出图状态。
func (s *Service) loadPipelineState(userID string, project *model.Project) (*pipelineState, error) {
	st := &pipelineState{project: project, active: map[string][]model.Task{}, latest: map[string]model.Task{}, extracted: map[string]bool{}, shotlist: map[string]bool{}, draft: map[string]int{}, packed: map[string]bool{}}
	units, err := s.repo.ProjectUnitSummaries(project.ID)
	if err != nil {
		return nil, err
	}
	for _, unit := range units {
		switch unit.Kind {
		case model.ProjectUnitKindEpisode:
			st.episodes = append(st.episodes, unit)
		case model.ProjectUnitKindChapter:
			st.chapters = append(st.chapters, unit)
		}
	}
	tasks, err := s.repo.ProjectPipelineTasks(userID, project.ID)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		key := ""
		switch {
		case task.Type == "canvas_video":
			key = pipelineKeyVideo
		case strings.Contains(task.InputJSON, `"dramaAsset"`):
			key = pipelineKeyImage
		default:
			if meta := taskDramaStep(task.InputJSON); meta != nil {
				key = meta.Step
			}
		}
		if key == "" {
			continue
		}
		if taskActive(task) {
			st.active[key] = append(st.active[key], task)
		}
		unitID := taskChapterID(task.InputJSON)
		if _, ok := st.latest[key+"|"+unitID]; !ok {
			st.latest[key+"|"+unitID] = task
		}
		if key == drama.StepAssetsExtract && task.Status == model.TaskStatusSucceeded && unitID != "" {
			st.extracted[unitID] = true
		}
		if st.lastStep == nil && task.Status == model.TaskStatusSucceeded && key != pipelineKeyImage && key != pipelineKeyVideo {
			last := task
			st.lastStep = &last
		}
	}
	shotlists, err := s.repo.DramaShotlistsForProject(project.ID)
	if err != nil {
		return nil, err
	}
	for _, row := range shotlists {
		st.shotlist[row.UnitID] = true
		var report drama.CheckReport
		if json.Unmarshal([]byte(row.CheckJSON), &report) == nil && report.HasErrors() && !report.Accepted {
			st.draft[row.UnitID] = report.Errors
		}
	}
	shots, err := s.repo.ProjectShots(project.ID)
	if err != nil {
		return nil, err
	}
	for _, shot := range shots {
		st.packed[shot.UnitID] = true
	}
	assets, err := s.ProjectAssets(userID, project.ID)
	if err != nil {
		return nil, err
	}
	for _, a := range assets {
		if !slices.Contains(dramaAssetCategories, a.Category) || a.Status == model.AssetVersionStatusArchived {
			continue
		}
		st.assets.Total++
		imaged := a.MediaType == "image"
		if a.Category == model.AssetCategoryCharacter {
			imaged = a.Character != nil && a.Character.VisualStatus == "ready"
		}
		switch {
		case imaged:
			st.assets.Imaged++
		case a.Image != nil && (a.Image.Status == dramaImageWaiting || a.Image.Status == dramaImageQueued):
			st.assets.Pending++
		case a.Image != nil && (a.Image.Status == dramaImageFailed || a.Image.Status == dramaImageStale):
			st.assets.Failed++
		}
	}
	_, _, record, locked := projectLockedStyle(project.BibleJSON)
	st.locked, st.lockedVia = locked, record.Source
	return st, nil
}

// failedLatest 是这一步（单元为空是项目级）最近一个任务失败的原因；不是失败返回空串。
func (st *pipelineState) failedLatest(step string, unitID string) string {
	task, ok := st.latest[step+"|"+unitID]
	if !ok || task.Status != model.TaskStatusFailed {
		return ""
	}
	return firstNonEmpty(userFacingTaskError(task.Error), "任务失败")
}

// unextracted 是还没提取过资产的剧集（按顺序）。
func (st *pipelineState) unextracted() []string {
	var out []string
	for _, ep := range st.episodes {
		if !st.extracted[ep.ID] {
			out = append(out, ep.ID)
		}
	}
	return out
}

// withoutShotlist 是还没有镜头表的剧集（按顺序）。
func (st *pipelineState) withoutShotlist() []string {
	var out []string
	for _, ep := range st.episodes {
		if !st.shotlist[ep.ID] {
			out = append(out, ep.ID)
		}
	}
	return out
}

func (st *pipelineState) episodeLabel(unitID string) string {
	for i, ep := range st.episodes {
		if ep.ID == unitID {
			return fmt.Sprintf("第 %d 集", i+1)
		}
	}
	return "有一集"
}

// assetsReady 是角色场景资产这一环做完了：每一集都提取过、资产都出好了图（没有在排、没有失败、没有还没出的）。
func (st *pipelineState) assetsReady() bool {
	return len(st.episodes) > 0 && len(st.unextracted()) == 0 && st.assets.Total > 0 && st.assets.Imaged == st.assets.Total
}

// projectPipeline 推出流水线：各格的事实交给 drama.DerivePipeline 判停在哪。
func (s *Service) projectPipeline(userID string, project *model.Project, progress drama.Progress) (drama.Pipeline, error) {
	st, err := s.loadPipelineState(userID, project)
	if err != nil {
		return drama.Pipeline{}, err
	}
	end := map[string]drama.EndState{}
	for _, e := range progress.EndStates {
		end[e.Key] = e
	}
	facts := map[string]drama.PipelineFacts{}

	script := drama.PipelineFacts{Done: end["script"].Done, Running: len(st.active[drama.StepScriptAdapt]) > 0}
	switch {
	case script.Done:
		script.Detail = fmt.Sprintf("%d 集", len(st.episodes))
	case len(st.chapters) > 0:
		script.Detail = fmt.Sprintf("%d 章原文", len(st.chapters))
	}
	if !script.Done {
		if reason := st.failedLatest(drama.StepScriptAdapt, ""); reason != "" {
			script.Problem = "上次拆剧本失败：" + reason
		} else if len(st.chapters) == 0 && len(st.episodes) == 0 {
			script.Problem, script.Manual, script.Action = "还没有正文：到章节页导入小说或剧本", true, "去章节页"
			if project.SourceType == ProjectSourceIdea {
				script.Problem = "创意还没生成章节：到章节页导入正文，或回首页重新建项"
			}
		}
	}
	facts[drama.StageScript] = script

	style := drama.PipelineFacts{Done: st.locked, Running: len(st.active[drama.StepStyleLock]) > 0}
	if st.locked {
		style.Detail = map[string]string{drama.StyleLockSourceLibrary: "取自风格中心", drama.StyleLockSourceDirector: "导演分析"}[st.lockedVia]
	} else if reason := st.failedLatest(drama.StepStyleLock, ""); reason != "" {
		style.Problem = "上次导演定画风失败：" + reason
	}
	facts[drama.StageStyle] = style

	palette := drama.PipelineFacts{Done: end["palette"].Done, Running: len(st.active[drama.StepScenePalette]) > 0}
	if !palette.Done {
		if reason := st.failedLatest(drama.StepScenePalette, ""); reason != "" {
			palette.Problem = "上次场次色卡失败：" + reason
		}
	}
	facts[drama.StagePalette] = palette

	assets := drama.PipelineFacts{Done: st.assetsReady(), Running: len(st.active[drama.StepAssetsExtract]) > 0 || len(st.active[pipelineKeyImage]) > 0 || st.assets.Pending > 0}
	if st.assets.Total > 0 {
		assets.Detail = fmt.Sprintf("%d 个资产 · %d 个有图", st.assets.Total, st.assets.Imaged)
	}
	if !assets.Done {
		for _, unitID := range st.unextracted() {
			if reason := st.failedLatest(drama.StepAssetsExtract, unitID); reason != "" {
				assets.Problem = st.episodeLabel(unitID) + "提取资产失败：" + reason
				break
			}
		}
		if assets.Problem == "" && len(st.unextracted()) == 0 {
			switch {
			case st.assets.Failed > 0:
				assets.Problem = fmt.Sprintf("%d 个资产出图失败：点继续重新出图，或到资产页处理", st.assets.Failed)
			case st.assets.Total > st.assets.Imaged:
				assets.Problem = fmt.Sprintf("%d 个资产还没出图：选好图片模型后点继续给它们出图", st.assets.Total-st.assets.Imaged)
			}
		}
	}
	facts[drama.StageAssets] = assets

	shots := drama.PipelineFacts{Done: len(st.episodes) > 0 && len(st.withoutShotlist()) == 0, Running: len(st.active[drama.StepChapterShots]) > 0}
	shots.Detail = fmt.Sprintf("%d/%d 集", len(st.episodes)-len(st.withoutShotlist()), len(st.episodes))
	if !shots.Done {
		for _, unitID := range st.withoutShotlist() {
			if reason := st.failedLatest(drama.StepChapterShots, unitID); reason != "" {
				shots.Problem = st.episodeLabel(unitID) + "生成分镜失败：" + reason
				break
			}
		}
	}
	facts[drama.StageShots] = shots

	pack := drama.PipelineFacts{Done: shots.Done && len(st.draft) == 0}
	for _, ep := range st.episodes {
		if !st.packed[ep.ID] && st.shotlist[ep.ID] && st.draft[ep.ID] == 0 {
			pack.Done = false
		}
	}
	if !pack.Done {
		for _, ep := range st.episodes {
			if n := st.draft[ep.ID]; n > 0 {
				pack.Problem, pack.Manual, pack.Action = fmt.Sprintf("%s分镜检查有 %d 个问题，还没装段：到章节页处理后重新装段", st.episodeLabel(ep.ID), n), true, "去章节页"
				break
			}
		}
	}
	facts[drama.StagePack] = pack

	video := drama.PipelineFacts{Done: progress.Counts.Segments > 0 && progress.Counts.Generated == progress.Counts.Segments, Running: len(st.active[pipelineKeyVideo]) > 0}
	if progress.Counts.Segments > 0 {
		video.Detail = fmt.Sprintf("%d/%d 段", progress.Counts.Generated, progress.Counts.Segments)
	}
	facts[drama.StageVideo] = video
	facts[drama.StageCut] = drama.PipelineFacts{Done: end["gate"].Done}
	pipeline := drama.DerivePipeline(project.RunMode, facts)
	// 生成模式本该接着做、却停在这一格又没有别的原因：多半是链式接续没接上（比如参考图要能读图的模型），
	// 原因记在最近一个成功收尾的步骤结果里，写出来。
	if stop := pipeline.Stop; stop != nil && facts[stop.Stage].Problem == "" && drama.ShouldAutoContinue(project.RunMode, stop.Stage) {
		if reason := s.lastChainError(userID, st.lastStep); reason != "" {
			f := facts[stop.Stage]
			f.Problem = "没能自动开始：" + reason
			facts[stop.Stage] = f
			pipeline = drama.DerivePipeline(project.RunMode, facts)
		}
	}
	return pipeline, nil
}

// lastChainError 读一个步骤任务结果里没接上的原因（result.dramaStep.chained.error 或 next.error）；没有返回空串。
func (s *Service) lastChainError(userID string, task *model.Task) string {
	if task == nil {
		return ""
	}
	full, err := s.repo.TaskForUser(userID, task.ID)
	if err != nil {
		return ""
	}
	var result struct {
		DramaStep struct {
			Chained *DramaChainedTask `json:"chained"`
			Next    *DramaChainedTask `json:"next"`
		} `json:"dramaStep"`
	}
	if json.Unmarshal([]byte(full.ResultJSON), &result) != nil {
		return ""
	}
	for _, chained := range []*DramaChainedTask{result.DramaStep.Next, result.DramaStep.Chained} {
		if chained != nil && chained.TaskID == "" && chained.Error != "" {
			return chained.Error
		}
	}
	return ""
}

// DramaPipelineNext 是流水线「继续」：从当前环节开始做（用户点的，不看生成模式），之后按生成模式接续。出视频、剪辑不接。
func (s *Service) DramaPipelineNext(userID string, projectID string, req DramaPipelineNextRequest) (DramaPipelineNextResult, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return DramaPipelineNextResult{}, err
	}
	progress, err := s.DramaProgress(userID, projectID)
	if err != nil {
		return DramaPipelineNextResult{}, err
	}
	pipeline := *progress.Pipeline
	if pipeline.Stop == nil {
		if pipeline.Current == "" {
			return DramaPipelineNextResult{}, BadAuthRequest("流水线已经走完了")
		}
		return DramaPipelineNextResult{}, failedPrecondition(fmt.Sprintf("「%s」正在做，等它做完", drama.PipelineStageLabel(pipeline.Current)))
	}
	stage := pipeline.Stop.Stage
	if stage == drama.StageVideo || stage == drama.StageCut {
		err := failedPrecondition("出视频要花钱：到制作台看过预估花费再生成，流水线不会替你点")
		err.Reason = ReasonPipelineVideoManual
		return DramaPipelineNextResult{}, err
	}
	text := &DramaPipelineText{Model: strings.TrimSpace(req.Model), LogicalModelID: strings.TrimSpace(req.LogicalModelID), Config: req.Config}
	if !text.usable() {
		return DramaPipelineNextResult{}, BadAuthRequest("先选一个文本模型")
	}
	imageTask := req.ImageTask
	if !imageTask.usable() {
		imageTask = nil
	}
	st, err := s.loadPipelineState(userID, project)
	if err != nil {
		return DramaPipelineNextResult{}, err
	}
	out := DramaPipelineNextResult{Stage: stage}
	projectStep := DramaProjectStepRunRequest{Model: text.Model, LogicalModelID: text.LogicalModelID, Config: text.Config, AssetImageTask: imageTask, TraceID: req.TraceID, RequestID: req.RequestID}
	switch stage {
	case drama.StageScript:
		if len(st.chapters) == 0 {
			return DramaPipelineNextResult{}, failedPrecondition("还没有正文：到章节页导入小说或剧本")
		}
		projectStep.Mode = drama.ScriptAdaptModeAdapt
		if project.SourceType == ProjectSourceIdea {
			projectStep.TargetEpisodes = briefEpisodeCount(project.BriefJSON)
		}
		result, err := s.RunDramaProjectStep(userID, projectID, drama.StepScriptAdapt, projectStep)
		if err != nil {
			return DramaPipelineNextResult{}, err
		}
		out.Task = result.Task
	case drama.StageStyle:
		if project.StyleSource.Mode == model.ProjectStyleLibrary && project.StyleSource.StyleID != "" {
			if _, err := s.LockDramaStyle(userID, projectID, DramaStyleLockRequest{LibraryID: project.StyleSource.StyleID}); err != nil {
				return DramaPipelineNextResult{}, err
			}
			break
		}
		projectStep.pipeline, projectStep.StyleText, projectStep.ReferenceImages = true, project.StyleSource.Brief, project.StyleSource.RefImageIDs
		result, err := s.RunDramaProjectStep(userID, projectID, drama.StepStyleLock, projectStep)
		if err != nil {
			return DramaPipelineNextResult{}, err
		}
		out.Task = result.Task
	case drama.StagePalette:
		result, err := s.RunDramaProjectStep(userID, projectID, drama.StepScenePalette, projectStep)
		if err != nil {
			return DramaPipelineNextResult{}, err
		}
		out.Task = result.Task
	case drama.StageAssets:
		if queue := st.unextracted(); len(queue) > 0 {
			result, err := s.RunDramaStep(userID, projectID, queue[0], drama.StepAssetsExtract, DramaStepRunRequest{Model: text.Model, LogicalModelID: text.LogicalModelID, Config: text.Config, ImageTask: imageTask, TraceID: req.TraceID, RequestID: req.RequestID, chain: &dramaAssetChain{Queue: slices.Clone(queue[1:])}})
			if err != nil {
				return DramaPipelineNextResult{}, err
			}
			out.Task = result.Task
			break
		}
		if imageTask == nil {
			return DramaPipelineNextResult{}, BadAuthRequest("资产还没出图：先选一个图片模型")
		}
		template := *imageTask
		template.Next = text
		only, err := s.pipelineUnimagedAssets(userID, projectID)
		if err != nil {
			return DramaPipelineNextResult{}, err
		}
		images, err := s.queueDramaAssetImages(userID, projectID, template, "", only)
		if err != nil {
			return DramaPipelineNextResult{}, err
		}
		out.Images = &images
	case drama.StageShots:
		queue := st.withoutShotlist()
		if len(queue) == 0 {
			return DramaPipelineNextResult{}, failedPrecondition("每一集都有分镜了")
		}
		chained := s.startShotsQueue(userID, projectID, text, "", queue)
		if chained.TaskID == "" {
			return DramaPipelineNextResult{}, BadAuthRequest(firstNonEmpty(chained.Error, "没能开始生成分镜"))
		}
		out.Task, _ = s.repo.TaskForUser(userID, chained.TaskID)
	default:
		return DramaPipelineNextResult{}, failedPrecondition(pipeline.Stop.Reason)
	}
	if progress, err = s.DramaProgress(userID, projectID); err == nil && progress.Pipeline != nil {
		out.Pipeline = *progress.Pipeline
	}
	return out, nil
}

// failedPrecondition 是 409 类的「现在做不了」（状态不对，不是请求写错）。
func failedPrecondition(message string) *AppError {
	err := NewAppError(http.StatusConflict, message)
	err.Reason = ReasonFailedPrecondition
	return err
}

// briefEpisodeCount 是需求文档里写了几集（建项选创意时按「生成 N 集」写的）；没写返回 0。
func briefEpisodeCount(briefJSON string) int {
	var doc struct {
		Episodes []drama.BriefEpisode `json:"episodes"`
	}
	_ = json.Unmarshal([]byte(briefJSON), &doc)
	return len(doc.Episodes)
}

// pipelineUnimagedAssets 是资产设计建的、还没有图也没在排的资产（重新排出图用）。
func (s *Service) pipelineUnimagedAssets(userID string, projectID string) ([]string, error) {
	assets, err := s.projectDramaImageAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, item := range assets {
		if item.imaged {
			continue
		}
		state := dramaImageStateOf(&item.asset, assetImageKey)
		if state.Status == dramaImageQueued && s.dramaImageTaskActive(userID, state.TaskID) {
			continue
		}
		ids = append(ids, item.asset.ID)
	}
	if len(ids) == 0 {
		return nil, failedPrecondition("资产都在出图或已经有图了")
	}
	return ids, nil
}

// ---------------------------------------------------------------- 链式接续

// dramaTaskText 读任务的模型选择和（解密后的）文本配置，接着建下一步时沿用。
func (s *Service) dramaTaskText(task model.Task) (*DramaPipelineText, dramaChainInput, error) {
	input, err := s.dramaChainInputOf(task)
	if err != nil {
		return nil, input, err
	}
	return &DramaPipelineText{Model: task.Model, LogicalModelID: task.LogicalModelID, Config: input.Config}, input, nil
}

// continueAfterScript 在剧本优化写完剧集后接下一步：交给导演分析、还没锁定风格时接导演定画风；已锁定时接场次色卡。
// 生成模式不让接时返回 nil。
func (s *Service) continueAfterScript(task model.Task, projectID string) *DramaChainedTask {
	project, err := s.activeProjectForUser(task.UserID, projectID)
	if err != nil {
		return &DramaChainedTask{Step: drama.StepScenePalette, Error: err.Error()}
	}
	if _, _, _, locked := projectLockedStyle(project.BibleJSON); !locked {
		if project.StyleSource.Mode != model.ProjectStyleDirector {
			return &DramaChainedTask{Step: drama.StepScenePalette, Error: errStyleNotLocked}
		}
		if !s.shouldAutoContinue(project, drama.StageStyle) {
			return nil
		}
		return s.chainProjectStep(task, project, drama.StepStyleLock)
	}
	if !s.shouldAutoContinue(project, drama.StagePalette) {
		return nil
	}
	return s.chainProjectStep(task, project, drama.StepScenePalette)
}

// continueAfterStyle 在流水线跑的导演定画风锁定后，按生成模式接着跑场次色卡（还没有剧集时不接）。
func (s *Service) continueAfterStyle(task model.Task, projectID string) *DramaChainedTask {
	project, err := s.activeProjectForUser(task.UserID, projectID)
	if err != nil {
		return &DramaChainedTask{Step: drama.StepScenePalette, Error: err.Error()}
	}
	if !s.shouldAutoContinue(project, drama.StagePalette) {
		return nil
	}
	units, err := s.repo.ProjectUnitSummaries(projectID)
	if err != nil || !slices.ContainsFunc(units, func(u model.ProjectUnit) bool { return u.Kind == model.ProjectUnitKindEpisode }) {
		return nil
	}
	return s.chainProjectStep(task, project, drama.StepScenePalette)
}

// chainProjectStep 用上一个任务的文本模型和配置接着建一个项目级步骤（metadata.chainedFrom = 上一个任务）；出图模板一路带下去。
// 导演定画风按项目的画风来源（描述、参考图）跑、收尾直接锁定。
func (s *Service) chainProjectStep(task model.Task, project *model.Project, stepID string) *DramaChainedTask {
	chained := &DramaChainedTask{Step: stepID}
	step, ok := drama.StepByID(stepID)
	if !ok {
		return nil
	}
	if _, _, err := s.dramaStepSkill(project, step, nil); err != nil {
		chained.Error = userFacingTaskError(err.Error())
		return chained
	}
	text, input, err := s.dramaTaskText(task)
	if err != nil {
		chained.Error = err.Error()
		return chained
	}
	req := DramaProjectStepRunRequest{Model: text.Model, LogicalModelID: text.LogicalModelID, Config: text.Config, TraceID: task.TraceID, chainedFrom: task.ID, AssetImageTask: input.AssetImageTask}
	if stepID == drama.StepStyleLock {
		req.pipeline, req.StyleText, req.ReferenceImages = true, project.StyleSource.Brief, project.StyleSource.RefImageIDs
	}
	result, err := s.RunDramaProjectStep(task.UserID, project.ID, stepID, req)
	if err != nil {
		chained.Error = userFacingTaskError(err.Error())
		_ = s.log(task.UserID, task.ID, "warn", "没有接着做"+step.Label, chained.Error)
		return chained
	}
	chained.TaskID = result.Task.ID
	return chained
}

// continueAfterAssets 在一集提取收尾、或一个资产出图任务结束后问一次：每一集都提取过、资产都出好了图、没有正在生成的分镜时，
// 按生成模式从第一集没有镜头表的开始按集依次生成分镜。finishing 是正在收尾的提取任务（它还没写成成功，算这一集已提取、不算在跑）。
func (s *Service) continueAfterAssets(userID string, projectID string, text *DramaPipelineText, finishing *model.Task) *DramaChainedTask {
	if !text.usable() {
		return nil
	}
	dramaPipelineMu.Lock()
	defer dramaPipelineMu.Unlock()
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil || !s.shouldAutoContinue(project, drama.StageShots) {
		return nil
	}
	st, err := s.loadPipelineState(userID, project)
	if err != nil {
		return nil
	}
	if finishing != nil {
		if unitID := taskChapterID(finishing.InputJSON); unitID != "" {
			st.extracted[unitID] = true
		}
		st.active[drama.StepAssetsExtract] = slices.DeleteFunc(st.active[drama.StepAssetsExtract], func(t model.Task) bool { return t.ID == finishing.ID })
	}
	if len(st.active[drama.StepAssetsExtract]) > 0 || len(st.active[drama.StepChapterShots]) > 0 || !st.assetsReady() {
		return nil
	}
	queue := st.withoutShotlist()
	if len(queue) == 0 {
		return nil
	}
	from := ""
	if finishing != nil {
		from = finishing.ID
	}
	return s.startShotsQueue(userID, projectID, text, from, queue)
}

// startShotsQueue 按顺序给 queue 里第一个能生成的剧集建生成分镜任务，剩下的记进 metadata.dramaPipeline.queue；
// 一个都建不成时返回最后一个原因。
func (s *Service) startShotsQueue(userID string, projectID string, text *DramaPipelineText, from string, queue []string) *DramaChainedTask {
	chained := &DramaChainedTask{Step: drama.StepChapterShots}
	for i, unitID := range queue {
		req := DramaStepRunRequest{Model: text.Model, LogicalModelID: text.LogicalModelID, Config: text.Config, chainedFrom: from, pipelineQueue: slices.Clone(queue[i+1:])}
		result, err := s.RunDramaStep(userID, projectID, unitID, drama.StepChapterShots, req)
		if err != nil {
			chained.Error = userFacingTaskError(err.Error())
			_ = s.log(userID, from, "warn", "没有接着生成分镜", chained.Error)
			continue
		}
		chained.TaskID, chained.UnitID, chained.Error = result.Task.ID, unitID, ""
		chained.Remaining = len(queue) - i - 1
		return chained
	}
	return chained
}

// continueShotsQueue 在一集生成分镜收尾后，接着生成同一队列里下一集（期间已经有了镜头表的跳过）。不是流水线排的、队列空了返回 nil。
func (s *Service) continueShotsQueue(task model.Task, projectID string) *DramaChainedTask {
	meta := taskDramaPipeline(task.InputJSON)
	if meta == nil || len(meta.Queue) == 0 {
		return nil
	}
	if done, err := s.repo.TaskChainedFromExists(task.UserID, task.ID); err != nil || done {
		return nil
	}
	text, _, err := s.dramaTaskText(task)
	if err != nil {
		return &DramaChainedTask{Step: drama.StepChapterShots, Error: err.Error()}
	}
	project, err := s.activeProjectForUser(task.UserID, projectID)
	if err != nil {
		return nil
	}
	st, err := s.loadPipelineState(task.UserID, project)
	if err != nil {
		return nil
	}
	queue := slices.DeleteFunc(slices.Clone(meta.Queue), func(id string) bool { return st.shotlist[id] })
	if len(queue) == 0 {
		return nil
	}
	return s.startShotsQueue(task.UserID, projectID, text, task.ID, queue)
}

// continueAfterImages 在一个资产出图任务结束（成功或失败落库）后，用模板上带的文本配置问一次要不要开始生成分镜。
func (s *Service) continueAfterImages(task model.Task, projectID string, decrypted string) {
	template := taskAssetImageTemplate(decrypted)
	if template == nil || !template.Next.usable() {
		return
	}
	if chained := s.continueAfterAssets(task.UserID, projectID, template.Next, nil); chained != nil && chained.Error != "" && chained.TaskID == "" {
		_ = s.log(task.UserID, task.ID, "warn", "资产出完图，没有接着生成分镜", chained.Error)
	}
}

// taskDramaPipeline 读任务 metadata.dramaPipeline；没有返回 nil。
func taskDramaPipeline(inputJSON string) *dramaPipelineMeta {
	if !strings.Contains(inputJSON, `"dramaPipeline"`) {
		return nil
	}
	var input struct {
		Metadata struct {
			DramaPipeline *dramaPipelineMeta `json:"dramaPipeline"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil {
		return nil
	}
	return input.Metadata.DramaPipeline
}
