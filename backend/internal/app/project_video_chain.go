package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const videoChainMetadataKey = "_projectVideoChain"

type ProjectVideoChainModel struct {
	Model          string         `json:"model"`
	LogicalModelID string         `json:"logicalModelId,omitempty"`
	Input          map[string]any `json:"input"`
}

type ProjectVideoChainRequest struct {
	PrepareOnly bool                              `json:"prepareOnly,omitempty"`
	Scope       string                            `json:"scope,omitempty"`
	UnitID      string                            `json:"unitId"`
	ShotID      string                            `json:"shotId"`
	Model       ProjectVideoChainModel            `json:"model"`
	Models      map[string]ProjectVideoChainModel `json:"models,omitempty"`
	PreviewHash string                            `json:"previewHash,omitempty"`
	RequestID   string                            `json:"requestId,omitempty"`

	ResumeChainID string `json:"resumeChainId,omitempty"`
}

type ProjectVideoChainStep struct {
	FreezeReference       bool               `json:"freezeReference,omitempty"`
	ShotID                string             `json:"shotId"`
	RevisionID            string             `json:"revisionId"`
	Title                 string             `json:"title"`
	Scene                 string             `json:"scene"`
	ContentSeconds        float64            `json:"contentSeconds"`
	GenerationSeconds     float64            `json:"generationSeconds"`
	Model                 string             `json:"model"`
	Status                string             `json:"status"`
	Phase                 string             `json:"phase"`
	Error                 string             `json:"error,omitempty"`
	ReferenceWarning      string             `json:"referenceWarning,omitempty"`
	ReferenceSkipped      bool               `json:"referenceSkipped,omitempty"`
	TaskID                string             `json:"taskId,omitempty"`
	PreviousTaskIDs       []string           `json:"previousTaskIds,omitempty"`
	VideoResourceID       string             `json:"videoResourceId,omitempty"`
	FrameResourceID       string             `json:"frameResourceId,omitempty"`
	SourceVideoResourceID string             `json:"sourceVideoResourceId,omitempty"`
	SourceVideoTaskID     string             `json:"sourceVideoTaskId,omitempty"`
	FrameAssetID          string             `json:"frameAssetId,omitempty"`
	FrameTimeSeconds      *float64           `json:"frameTimeSeconds,omitempty"`
	FrameSelection        string             `json:"frameSelection,omitempty"`
	SourceRevisionID      string             `json:"sourceRevisionId,omitempty"`
	SourceShotID          string             `json:"sourceShotId,omitempty"`
	ReferenceTag          string             `json:"referenceTag,omitempty"`
	ReferenceCount        int                `json:"referenceCount"`
	ReferenceAudioCount   int                `json:"referenceAudioCount"`
	Ending                bool               `json:"ending"`
	Quote                 *LogicalModelQuote `json:"quote,omitempty"`

	Diagnostic *videoChainAttemptDiagnostic `json:"diagnostic,omitempty"`
}

type ProjectVideoChainView struct {
	WhiteModelStoryboardEnabled bool                    `json:"whiteModelStoryboardEnabled"`
	Scope                       string                  `json:"scope,omitempty"`
	PrepareOnly                 bool                    `json:"prepareOnly,omitempty"`
	ID                          string                  `json:"id,omitempty"`
	ProjectID                   string                  `json:"projectId"`
	UnitID                      string                  `json:"unitId"`
	AnchorShotID                string                  `json:"anchorShotId"`
	Status                      string                  `json:"status"`
	Stage                       string                  `json:"stage"`
	Error                       string                  `json:"error,omitempty"`
	PreviewHash                 string                  `json:"previewHash"`
	ConfirmationBlockedReason   string                  `json:"confirmationBlockedReason,omitempty"`
	TotalMicrocredits           int64                   `json:"totalMicrocredits"`
	Steps                       []ProjectVideoChainStep `json:"steps"`
}

type videoChainStepState struct {
	ProjectVideoChainStep
	CompiledModel   string            `json:"compiledModel,omitempty"`
	Input           map[string]any    `json:"input"`
	LogicalModelID  string            `json:"logicalModelId,omitempty"`
	Signature       string            `json:"signature"`
	SceneSignature  string            `json:"sceneSignature,omitempty"`
	SceneResourceID string            `json:"sceneResourceId,omitempty"`
	Uploads         map[string]string `json:"uploads,omitempty"`
	UploadScope     string            `json:"uploadScope,omitempty"`
}

type videoChainState struct {
	confirmationBlockedReason   string                   // computed only for the read-only resume preview
	RouteRevision               int64                    `json:"routeRevision,omitempty"`
	WhiteModelStoryboardEnabled *bool                    `json:"whiteModelStoryboardEnabled,omitempty"`
	UnitID                      string                   `json:"unitId"`
	AnchorShotID                string                   `json:"anchorShotId"`
	Request                     ProjectVideoChainRequest `json:"request"`
	PreviewHash                 string                   `json:"previewHash"`
	Steps                       []videoChainStepState    `json:"steps"`
	ResumeConfirmations         map[string]string        `json:"resumeConfirmations,omitempty"`
}

func videoChainHash(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func videoChainOperation(unitID string) string { return "video-chain:" + unitID }

func videoChainActiveConflict(id string) *AppError {
	conflict := NewAppError(http.StatusConflict, "本章节已有执行中的生成链，请查看该链进度或停止后续生成")
	conflict.Details = map[string]any{"activeVideoChainId": id}
	return conflict
}

func (s *Service) videoChainState(task *model.Task) (videoChainState, error) {
	var state videoChainState
	raw, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		return state, err
	}
	err = json.Unmarshal([]byte(raw), &state)
	if err == nil {
		if state.WhiteModelStoryboardEnabled == nil {
			enabled := true // Chains accepted before the project switch always used white frames.
			state.WhiteModelStoryboardEnabled = &enabled
		}
		for i := range state.Steps {
			for key, value := range state.Steps[i].Uploads {
				if strings.HasPrefix(value, encryptedSettingPrefix) {
					plain, decryptErr := s.decryptSettingSecret(value)
					if decryptErr != nil {
						return state, decryptErr
					}
					state.Steps[i].Uploads[key] = plain
				}
			}
		}
	}
	return state, err
}

func videoChainView(projectID string, task *model.Task, state videoChainState) ProjectVideoChainView {
	view := ProjectVideoChainView{Scope: state.Request.Scope, PrepareOnly: state.Request.PrepareOnly, ProjectID: projectID, UnitID: state.UnitID, Status: "preview", PreviewHash: state.PreviewHash, Steps: []ProjectVideoChainStep{}}
	view.WhiteModelStoryboardEnabled = state.WhiteModelStoryboardEnabled == nil || *state.WhiteModelStoryboardEnabled
	view.AnchorShotID = state.AnchorShotID
	view.ConfirmationBlockedReason = state.confirmationBlockedReason
	if task != nil {
		view.ID = task.ID
		view.Status = string(task.Status)
		view.Stage = task.Stage
		view.Error = task.Error
	}
	for _, step := range state.Steps {
		view.Steps = append(view.Steps, step.ProjectVideoChainStep)
		if step.Quote != nil && !state.Request.PrepareOnly && !(task == nil && step.Status == "succeeded") {
			view.TotalMicrocredits += step.Quote.AmountMicrocredits
		}
	}
	return view
}

func (s *Service) PreviewProjectVideoChain(userID, projectID string, req ProjectVideoChainRequest) (ProjectVideoChainView, error) {
	if req.ResumeChainID != "" {
		task, old, err := s.videoChainTaskForUser(userID, projectID, req.ResumeChainID)
		if err != nil {
			return ProjectVideoChainView{}, err
		}
		next, err := s.prepareVideoChainResume(task, old, req, true)
		if err != nil {
			return ProjectVideoChainView{}, err
		}
		return videoChainView(projectID, nil, next), nil
	}
	state, err := s.prepareProjectVideoChain(userID, projectID, req)
	if err != nil {
		return ProjectVideoChainView{}, err
	}
	if err := s.checkOriginalVideoChain(s.repo, userID, projectID, state); err != nil {
		return ProjectVideoChainView{}, err
	}
	return videoChainView(projectID, nil, state), nil
}

func (s *Service) ProjectVideoChains(userID, projectID, unitID string) ([]ProjectVideoChainView, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	if _, err := s.repo.ProjectUnit(projectID, unitID); err != nil {
		return nil, err
	}
	tasks, err := s.repo.ProjectVideoChains(userID, projectID, videoChainOperation(unitID))
	if err != nil {
		return nil, err
	}
	views := []ProjectVideoChainView{}
	states := make([]videoChainState, len(tasks))
	var pending []string
	for i := range tasks {
		state, err := s.videoChainState(&tasks[i])
		if err != nil {
			return nil, err
		}
		states[i] = state
		if tasks[i].Status == model.TaskStatusFailed {
			for j := range states[i].Steps {
				step := &states[i].Steps[j]
				if step.Status == "succeeded" {
					continue
				}
				if _, err := s.videoChainRecoveryChild(&tasks[i], step); err != nil {
					// The admission path fails closed. A missing original receipt
					// must not hide the rest of the chapter's existing results.
					step.Diagnostic = &videoChainAttemptDiagnostic{Category: "unknown", Phase: step.Phase, TerminalStatus: "unknown", CountDecision: "ignore", SubmissionOutcome: "unknown", BillingStatus: "unknown"}
				}
				break
			}
		}
		if tasks[i].Status == model.TaskStatusCancelled {
			for _, step := range state.Steps {
				if step.TaskID != "" && step.Status == "running" {
					pending = append(pending, step.TaskID)
				}
			}
		}
	}
	children, err := s.repo.VideoChainChildTasks(userID, pending)
	if err != nil {
		return nil, err
	}
	byID := map[string]model.Task{}
	for _, child := range children {
		byID[child.ID] = child
	}
	for i := range tasks {
		if tasks[i].Status == model.TaskStatusCancelled {
			for j := range states[i].Steps {
				step := &states[i].Steps[j]
				if child, ok := byID[step.TaskID]; ok {
					switch child.Status {
					case model.TaskStatusSucceeded:
						step.Status, step.Phase = "succeeded", "completed"
						step.VideoResourceID, _ = taskOutputResource(child.ResultJSON, child.Type)
					case model.TaskStatusFailed:
						step.Status, step.Error = "failed", sanitizeUserVisibleError(child.Error)
					case model.TaskStatusCancelled:
						step.Status, step.Phase = "cancelled", "cancelled"
					}
				}
			}
		}
		views = append(views, videoChainView(projectID, &tasks[i], states[i]))
	}
	return views, nil
}

func (s *Service) CreateProjectVideoChain(userID, projectID string, req ProjectVideoChainRequest) (ProjectVideoChainView, error) {
	if req.ResumeChainID != "" {
		return ProjectVideoChainView{}, BadAuthRequest("请在原生成链确认继续出片")
	}
	if s.IsDraining() {
		return ProjectVideoChainView{}, BadAuthRequest("服务正在维护，请稍后提交连续生成")
	}
	if strings.TrimSpace(req.RequestID) == "" || len(req.RequestID) > 160 {
		return ProjectVideoChainView{}, BadAuthRequest("连续生成缺少有效的提交标识")
	}
	state, err := s.prepareProjectVideoChain(userID, projectID, req)
	if err != nil {
		return ProjectVideoChainView{}, err
	}
	if req.PreviewHash == "" || req.PreviewHash != state.PreviewHash {
		return ProjectVideoChainView{}, BadAuthRequest("镜头、模型或报价已变化，请刷新连续生成预览后再提交")
	}
	id := videoChainHash([]string{userID, projectID, req.UnitID, req.RequestID})[:32]
	for i := range state.Steps {
		state.Steps[i].TaskID = videoChainHash([]string{id, state.Steps[i].ShotID, state.Steps[i].Signature})[:32]
	}
	raw, err := s.encodeVideoChainState(state)
	if err != nil {
		return ProjectVideoChainView{}, err
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return ProjectVideoChainView{}, err
	}
	now := time.Now()
	task := model.Task{ID: id, UserID: userID, ProjectID: projectID, Type: model.TaskTypeProjectVideoChain, Operation: videoChainOperation(req.UnitID), Status: model.TaskStatusQueued, Stage: "等待开始连续生成", Prompt: "同场景连续视频生成", InputJSON: raw, RouteRun: 1, CreatedAt: now, UpdatedAt: now}
	s.storageMu.Lock()
	usage, err := s.repo.UserStorageUsage(userID)
	if err == nil {
		err = validateTaskStorageQuotaWithPolicy(usage, int64(len(raw)+len(task.Prompt)), policy.Resource)
	}
	var saved *model.Task
	if err == nil {
		saved, err = s.repo.CreateProjectVideoChain(&task, policy.Task.ActiveTaskLimit, *state.WhiteModelStoryboardEnabled, func(tx *repository.Repository) error {
			if err := (&Service{repo: tx}).verifySceneChainSnapshots(userID, projectID, state); err != nil {
				return err
			}
			if !req.PrepareOnly {
				if err := s.checkVideoChainQuoteRevision(tx, userID, projectID, state); err != nil {
					return err
				}
			}
			return s.checkOriginalVideoChain(tx, userID, projectID, state)
		})
	}
	s.storageMu.Unlock()
	if errors.Is(err, repository.ErrTaskStateConflict) {
		return ProjectVideoChainView{}, BadAuthRequest("项目线路或白模分镜设置已变化，请刷新预览后重新提交")
	}
	if err != nil {
		return ProjectVideoChainView{}, storyboardTaskAdmissionError(err)
	}
	stored, err := s.videoChainState(saved)
	if err != nil {
		return ProjectVideoChainView{}, err
	}
	if stored.PreviewHash != state.PreviewHash {
		return ProjectVideoChainView{}, videoChainActiveConflict(saved.ID)
	}
	s.wakeTaskDispatcher()
	return videoChainView(projectID, saved, stored), nil
}

func (s *Service) encodeVideoChainState(state videoChainState) (string, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	var value map[string]any
	if err = json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if err = s.protectTaskSecrets(value); err != nil {
		return "", err
	}
	steps, _ := value["steps"].([]any)
	for _, item := range steps {
		step, _ := item.(map[string]any)
		uploads, _ := step["uploads"].(map[string]any)
		for key, value := range uploads {
			if text, ok := value.(string); ok && text != "" && !strings.HasPrefix(text, encryptedSettingPrefix) {
				encrypted, err := s.encryptSettingSecret(text)
				if err != nil {
					return "", err
				}
				uploads[key] = encrypted
			}
		}
	}
	raw, err = json.Marshal(value)
	if len(raw) > 8<<20 {
		return "", BadAuthRequest("连续生成的数据超过上限，请缩短本场镜头或减少重复参考信息")
	}
	return string(raw), err
}

func (s *Service) videoChainTaskForUser(userID, projectID, id string) (*model.Task, videoChainState, error) {
	task, err := s.repo.TaskForUser(userID, id)
	if err != nil {
		return nil, videoChainState{}, err
	}
	if task.ProjectID != projectID || task.Type != model.TaskTypeProjectVideoChain {
		return nil, videoChainState{}, BadAuthRequest("连续生成链不属于当前项目")
	}
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return nil, videoChainState{}, err
	}
	state, err := s.videoChainState(task)
	return task, state, err
}

func (s *Service) CancelProjectVideoChain(userID, projectID, id string) (ProjectVideoChainView, error) {
	task, state, err := s.videoChainTaskForUser(userID, projectID, id)
	if err != nil {
		return ProjectVideoChainView{}, err
	}
	if err = s.repo.CancelProjectVideoChain(userID, id); err != nil {
		return ProjectVideoChainView{}, err
	}
	for _, step := range state.Steps {
		child, err := s.repo.TaskForUser(userID, step.TaskID)
		if err == nil && child.Status == model.TaskStatusQueued {
			if _, err := s.CancelTask(context.Background(), userID, child.ID); err != nil {
				_ = s.log(userID, id, "warn", "停止生成链后取消排队镜头失败，worker 将检查父链状态", err.Error())
			}
		}
	}
	task, err = s.repo.TaskForUser(userID, id)
	if err != nil {
		return ProjectVideoChainView{}, err
	}
	return videoChainView(projectID, task, state), nil
}

func videoChainPositionPrompt(tag string) string {
	return fmt.Sprintf("【站位参考】请以「%s」该图中的人物位置、朝向、彼此距离，以及场景结构和道具位置，作为本镜起始状态的空间参考；", tag)
}
