// 短剧流程的服务端出图排队（M7.2）：资产设计建的角色、场景、道具（project_drama_assets.go）由服务端直接建出图任务
// （M8 卡 B 起色卡由代码合成，不再排出图任务）。图片任务模板由前端按图片模型组好（渠道配置在浏览器），
// 加密存在任务 input 顶层，任务结束随密钥一起从任务正文去掉；同时排队的任务数有上限，排不上的记成「等空位」，
// 前面的出图任务结束（成功落库，或失败、取消落库）之后自动接着排。出图状态记在资产 payload.metadata.assetImage 里。

package app

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

// 出图排队状态（payload.metadata.assetImage 的 status）。
const (
	dramaImageNone    = "none"
	dramaImageWaiting = "waiting"
	dramaImageQueued  = "queued"
	dramaImageBound   = "bound"
	dramaImageStale   = "stale"
	dramaImageFailed  = "failed"
)

// dramaImageQueueMu 让同一进程里的排队互斥（多张同时出完图时只有一个在挑下一批）；跨进程靠 payload 条件更新。
var dramaImageQueueMu sync.Mutex

// DramaImageQueueResult 是一次排出图：Queued 是建了任务的资产，Waiting 是等空位的，Failed 是没建成的（原因记在资产上）。
type DramaImageQueueResult struct {
	Queued  []string `json:"queued"`
	Waiting []string `json:"waiting"`
	Failed  []string `json:"failed"`
}

func emptyDramaImageQueueResult() DramaImageQueueResult {
	return DramaImageQueueResult{Queued: []string{}, Waiting: []string{}, Failed: []string{}}
}

type dramaImageState struct {
	Status    string `json:"status"`
	TaskID    string `json:"taskId,omitempty"`
	Error     string `json:"error,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// setDramaImageState 改一个资产的 payload（payload 条件更新：别的地方同时改了就重读再改，最多三次）。
func (s *Service) setDramaImageState(userID string, assetID string, update func(payload map[string]any)) error {
	for attempt := 0; attempt < 3; attempt++ {
		asset, err := s.repo.AssetForUser(userID, assetID)
		if err != nil {
			return err
		}
		payload := assetPayload(asset)
		update(payload)
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		ok, err := s.repo.UpdateAssetPayloadIfUnchanged(userID, assetID, asset.PayloadJSON, string(raw), time.Now())
		if err != nil || ok {
			return err
		}
	}
	return errors.New("资产同时被别处修改，出图状态没有写上")
}

// putDramaImageState 把出图状态写成 state。
func (s *Service) putDramaImageState(userID string, assetID string, key string, state dramaImageState) error {
	return s.setDramaImageState(userID, assetID, func(payload map[string]any) { payloadMetadata(payload)[key] = state })
}

// dramaImageStateOf 读资产 payload.metadata[key] 里的出图状态。
func dramaImageStateOf(asset *model.Asset, key string) dramaImageState {
	state, _ := decodeInto[dramaImageState](payloadMetadata(assetPayload(asset))[key])
	return state
}

// dramaImageTaskActive 是这个出图任务还在排队或运行。
func (s *Service) dramaImageTaskActive(userID string, taskID string) bool {
	if strings.TrimSpace(taskID) == "" {
		return false
	}
	task, err := s.repo.TaskForUser(userID, taskID)
	return err == nil && (task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning)
}

// dramaImageView 是出图状态给页面看的样子：记成「已排队」但任务已经失败或取消的，按失败显示（失败、取消落库后由 afterDramaImageTaskEnded 记成失败，这里兜住还没记上的）；
// 任务成功、收尾记状态之前，资产已经有图（imaged）就当已绑。
func (s *Service) dramaImageView(userID string, asset *model.Asset, key string, imaged bool) dramaImageState {
	state := dramaImageStateOf(asset, key)
	if state.Status == "" {
		state.Status = dramaImageNone
	}
	if state.Status != dramaImageQueued || state.TaskID == "" {
		return state
	}
	task, err := s.repo.TaskForUser(userID, state.TaskID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state.Status, state.Error = dramaImageFailed, "出图任务不见了"
		return state
	}
	if err != nil {
		return state
	}
	switch task.Status {
	case model.TaskStatusFailed, model.TaskStatusCancelled:
		state.Status, state.Error = dramaImageFailed, firstNonEmpty(userFacingTaskError(task.Error), "出图任务失败")
	case model.TaskStatusSucceeded:
		if imaged {
			state.Status = dramaImageBound
		}
	}
	return state
}

// dramaImageJob 是排队里的一个资产：Build 组它的出图任务（组不出来时记成失败，原因写在资产上）；level 是参考图的降级档
// （dramaRefsAll / dramaRefsNoPalette / dramaRefsNone，模型不支持这么多参考图时一档档往下降）。
type dramaImageJob struct {
	Asset model.Asset
	Build func(level int) (CreateTaskRequest, error)
}

// dispatchDramaImages 按顺序给「等空位」的资产建出图任务：空位（同时排队或运行的任务上限减去已有的）用完就停，剩下的保持「等空位」。
// 调用方持有 dramaImageQueueMu，jobs 只放状态是「等空位」的资产。
func (s *Service) dispatchDramaImages(userID string, key string, jobs []dramaImageJob) (DramaImageQueueResult, error) {
	result := emptyDramaImageQueueResult()
	if len(jobs) == 0 {
		return result, nil
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return result, err
	}
	active, err := s.repo.ActiveTaskCountForUser(userID)
	if err != nil {
		return result, err
	}
	slots := int64(policy.Task.ActiveTaskLimit) - active
	now := time.Now().Format(time.RFC3339Nano)
	for _, job := range jobs {
		asset := job.Asset
		if slots <= 0 {
			result.Waiting = append(result.Waiting, asset.ID)
			continue
		}
		task, createErr := s.createDramaImageTask(userID, asset, job.Build)
		if createErr != nil {
			state := dramaImageState{Status: dramaImageFailed, Error: userFacingTaskError(createErr.Error()), UpdatedAt: now}
			// 同时任务数在建任务时才会被别的请求占满、数据库一直忙（重试过了）：这一个和后面的留到下一次接着排，不算出图失败。
			if strings.Contains(createErr.Error(), "同时排队或运行的任务最多") || databaseBusyError(createErr) {
				state = dramaImageState{Status: dramaImageWaiting, UpdatedAt: now}
				if databaseBusyError(createErr) {
					state.Error = "数据库忙，这一个没排上：前面的出完会接着排，也可以到资产页点「继续出图」"
				}
				slots = 0
				result.Waiting = append(result.Waiting, asset.ID)
			} else {
				result.Failed = append(result.Failed, asset.ID)
			}
			if err := s.putDramaImageState(userID, asset.ID, key, state); err != nil {
				return result, err
			}
			continue
		}
		slots--
		result.Queued = append(result.Queued, asset.ID)
		if err := s.putDramaImageState(userID, asset.ID, key, dramaImageState{Status: dramaImageQueued, TaskID: task.ID, UpdatedAt: now}); err != nil {
			return result, err
		}
	}
	return result, nil
}

// createDramaImageTask 建一个出图任务。SQLite 下别的写入正占着库时建任务会报「database is locked」：
// 退避重试三次（每次重组请求），还忙就把错误交回给调用方（留在「等空位」，不记成出图失败）。
// 模型说不支持这次的参考图时，去掉色卡图再建，还不行就不带参考图（记一条日志）。
func (s *Service) createDramaImageTask(userID string, asset model.Asset, build func(level int) (CreateTaskRequest, error)) (*model.Task, error) {
	level := dramaRefsAll
	for attempt := 0; ; attempt++ {
		req, err := build(level)
		if err != nil {
			return nil, err
		}
		task, err := s.CreateTask(userID, req)
		if err != nil && modelCapabilityError(err) && level < dramaRefsNone && req.Input["referenceImages"] != nil {
			level++
			_ = s.log(userID, "", "warn", "资产出图参考图降级："+asset.Title, dramaRefsLevelNote[level])
			continue
		}
		if err == nil || !databaseBusyError(err) || attempt == 3 {
			return task, err
		}
		time.Sleep(time.Duration(100<<attempt) * time.Millisecond)
	}
}

// databaseBusyError 是 SQLite 的忙 / 锁错误（事务写冲突），稍后重试就能过。
func databaseBusyError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "database is locked") || strings.Contains(text, "database table is locked") || strings.Contains(text, "sqlite_busy")
}

// afterDramaImageTaskEnded 在资产设计出图任务失败或取消落库之后跑（任务输入还带着加密的模板）：还记着「出图中、是这个任务」的资产
// 记成失败（资产页「继续出图」可以重出，不自动重试），然后接着排「等空位」的，免得一张失败把后面的都卡住。这里的失败只记日志。
// 色卡（M8 卡 B 起）由代码合成，不走出图任务。
func (s *Service) afterDramaImageTaskEnded(task model.Task) {
	if task.Status != model.TaskStatusFailed && task.Status != model.TaskStatusCancelled {
		return
	}
	if !strings.Contains(task.InputJSON, `"dramaAsset"`) {
		return
	}
	decrypted, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		_ = s.log(task.UserID, task.ID, "warn", "出图任务结束后读不出任务输入", err.Error())
		return
	}
	var input struct {
		Metadata struct {
			CharacterID    string `json:"characterAssetId"`
			ProjectAssetID string `json:"projectAssetId"`
			ProjectID      string `json:"domainProjectId"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal([]byte(decrypted), &input)
	assetID := firstNonEmpty(input.Metadata.CharacterID, input.Metadata.ProjectAssetID)
	projectID := firstNonEmpty(task.ProjectID, input.Metadata.ProjectID)
	if assetID == "" || projectID == "" {
		return
	}
	if asset, err := s.repo.AssetForUser(task.UserID, assetID); err == nil {
		if state := dramaImageStateOf(asset, assetImageKey); state.Status == dramaImageQueued && state.TaskID == task.ID {
			failed := dramaImageState{Status: dramaImageFailed, TaskID: task.ID, Error: firstNonEmpty(userFacingTaskError(task.Error), "出图任务失败"), UpdatedAt: time.Now().Format(time.RFC3339Nano)}
			if err := s.putDramaImageState(task.UserID, assetID, assetImageKey, failed); err != nil {
				_ = s.log(task.UserID, task.ID, "warn", "出图失败状态没有写上", err.Error())
			}
		}
	}
	s.continueDramaImages(task, projectID, decrypted)
}

// continueDramaImages 是一个资产出图任务结束后用它带的模板接着排「等空位」的资产。
func (s *Service) continueDramaImages(task model.Task, projectID string, decrypted string) {
	if template := taskAssetImageTemplate(decrypted); template != nil {
		if _, err := s.queueDramaAssetImages(task.UserID, projectID, *template, task.ID, nil); err != nil {
			_ = s.log(task.UserID, task.ID, "warn", "接着排资产出图失败", err.Error())
		}
	}
}

// DramaImageTaskTemplate 是给资产出图用的图片任务模板：前端按项目默认图片模型组好的任务（模型选择、input.config 等），
// 服务端每个资产换上自己的提示词和 metadata 后建任务。它放在任务 input 的顶层 assetImageTask，任务结束后随密钥一起从任务正文去掉。
// MaxReferenceImages 是这个图片模型最多带几张参考图（前端按渠道模型能力写，0 是不能带；不给是不知道，不按名额挑，建任务被拒时再降级）。
type DramaImageTaskTemplate struct {
	Model              string         `json:"model"`
	LogicalModelID     string         `json:"logicalModelId,omitempty"`
	Provider           string         `json:"provider,omitempty"`
	Input              map[string]any `json:"input"`
	MaxReferenceImages *int           `json:"maxReferenceImages,omitempty"`
}

func (t *DramaImageTaskTemplate) usable() bool {
	return t != nil && (strings.TrimSpace(t.Model) != "" || strings.TrimSpace(t.LogicalModelID) != "")
}

// dramaImageTaskInput 是按模板组出图任务的 input：模板的 input（config 等）照用，去掉参考图、遮罩、对话历史和模板自己，
// 返回 input 和它的 metadata（调用方往里写资产出图的标识）。
func dramaImageTaskInput(template DramaImageTaskTemplate) (map[string]any, map[string]any) {
	input := map[string]any{}
	if copied, ok := decodeInto[map[string]any](template.Input); ok && copied != nil {
		input = copied
	}
	for _, key := range []string{"referenceImages", "referenceVideos", "referenceAudios", "mask", "textHistory", "assetImageTask"} {
		delete(input, key)
	}
	metadata, _ := input["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	}
	input["metadata"] = metadata
	return input, metadata
}

// dramaImageTaskRequestOf 是按模板建的图片任务（canvas_image / image）。
func dramaImageTaskRequestOf(projectID string, template DramaImageTaskTemplate, prompt string, input map[string]any) CreateTaskRequest {
	input["mode"], input["prompt"] = "image", prompt
	return CreateTaskRequest{
		ProjectID: projectID, Type: "canvas_image", Operation: "image", Prompt: prompt,
		Provider: strings.TrimSpace(template.Provider), Model: strings.TrimSpace(template.Model), LogicalModelID: strings.TrimSpace(template.LogicalModelID), Input: input,
	}
}
