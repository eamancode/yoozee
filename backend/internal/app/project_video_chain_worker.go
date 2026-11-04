package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// The controller performs one durable transition per lease. Waiting for a child
// releases the worker slot; closing a browser has no effect on the chain.
func (s *Service) processProjectVideoChain(ctx context.Context, task *model.Task) error {
	state, err := s.videoChainState(task)
	if err != nil {
		return s.repo.SaveProjectVideoChain(task, task.InputJSON, model.TaskStatusFailed, "连续生成检查点读取失败", "检查点无法读取，请检查服务端加密配置后重试", task.Progress)
	}
	index := -1
	for i := range state.Steps {
		if state.Steps[i].Status != "succeeded" {
			index = i
			break
		}
	}
	if index < 0 {
		stage := "同场景连续生成已完成"
		if state.Request.Scope == "single" {
			stage = "镜头视频已完成"
		}
		return s.saveVideoChain(task, state, model.TaskStatusSucceeded, stage, "")
	}
	step := &state.Steps[index]
	fail := func(cause error) error {
		step.Status, step.Error = "failed", sanitizeUserVisibleError(cause.Error())
		label := map[string]string{"waiting": "前置检查", "extracting": "末帧截取", "registering": "分镜参考登记与绑定", "uploading": "参考素材上传", "prepared": "提交准备", "submitting": "视频提交", "generating": "视频生成"}[step.Phase]
		if label == "" {
			label = "处理"
		}
		return s.saveVideoChain(task, state, model.TaskStatusFailed, fmt.Sprintf("%s · %s失败，后续等待", step.Title, label), step.Error)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.activeProjectForUser(task.UserID, task.ProjectID); err != nil {
		return fail(err)
	}
	child, readErr := s.repo.TaskForUser(task.UserID, step.TaskID)
	if readErr != nil && !errors.Is(readErr, gorm.ErrRecordNotFound) {
		return fail(readErr)
	}
	if readErr == nil {
		switch child.Status {
		case model.TaskStatusSucceeded:
			id, _ := taskOutputResource(child.ResultJSON, child.Type)
			if _, err := s.videoChainReadyResource(task.UserID, id, "video/"); err != nil {
				return fail(err)
			}
			step.VideoResourceID, step.Status, step.Phase, step.Error = id, "succeeded", "completed", ""
			return s.saveVideoChain(task, state, model.TaskStatusRunning, step.Title+" 已完成", "")
		case model.TaskStatusFailed:
			if step.RetryRouteRun > child.RouteRun {
				if child.MediaRecoveryJSON != "" {
					_, err = s.RecoverTaskMedia(task.UserID, child.ID)
				} else {
					_, err = s.taskLifecycle().retryTaskAdmitted(task.UserID, child.ID, &taskAdmission{ParentControllerID: task.ID, ParentControllerLease: task.LeaseOwner, ExpectedRouteRun: step.RetryRouteRun - 1})
				}
				if err != nil {
					return fail(err)
				}
				step.Status, step.Phase = "running", "generating"
				return s.saveVideoChain(task, state, model.TaskStatusRunning, step.Title+" 等待生成", "")
			}
			return fail(fmt.Errorf("视频生成失败：%s", child.Error))
		case model.TaskStatusCancelled:
			return fail(BadAuthRequest("当前镜头生成已取消，请停止本链后重新预览"))
		default:
			step.Status, step.Phase = "running", "generating"
			return s.saveVideoChain(task, state, model.TaskStatusRunning, step.Title+" · "+child.Stage, "")
		}
	}
	// Re-read scene order and compilation before every new child. Changed plans
	// pause before charging; explicit retry rebuilds from the current project state.
	fresh, err := s.prepareProjectVideoChain(task.UserID, task.ProjectID, state.Request)
	if err != nil {
		return fail(err)
	}
	if len(fresh.Steps) != len(state.Steps) || fresh.Steps[index].Signature != step.Signature {
		return fail(BadAuthRequest("镜头内容、叙事顺序或模型已变化，点击重试以最新内容继续"))
	}
	if step.SourceShotID != "" {
		if index > 0 {
			previous := &state.Steps[index-1]
			if previous.Status != "succeeded" || previous.VideoResourceID == "" {
				return fail(BadAuthRequest("上一镜头视频尚未完成"))
			}
			step.SourceVideoResourceID, step.SourceVideoTaskID = previous.VideoResourceID, previous.TaskID
		}
		if step.FrameResourceID == "" {
			step.Phase = "extracting"
			if reused, err := s.reuseVideoChainFrame(task, step); err != nil {
				return fail(err)
			} else if reused {
				return s.saveVideoChain(task, state, model.TaskStatusRunning, step.Title+" · 已有片末参考已复用", "")
			}
			observation := dramaTailFrameObservation{Method: "test-hook"}
			var id string
			var err error
			if s.dramaTailFrameHook != nil {
				id, err = s.extractDramaTailFrame(task.UserID, step.SourceVideoResourceID)
			} else {
				err = s.withDramaVideoFile(task.UserID, step.SourceVideoResourceID, func(resource *model.Resource, path string) error {
					var extractErr error
					id, extractErr = s.saveDramaTailFrame(task.UserID, resource, path, &observation)
					return extractErr
				})
			}
			if err != nil {
				return fail(err)
			}
			if _, err := s.videoChainReadyResource(task.UserID, id, "image/"); err != nil {
				return fail(err)
			}
			step.FrameResourceID = id
			step.FrameTimeSeconds, step.FrameSelection = observation.TimeSeconds, observation.Method
			return s.saveVideoChain(task, state, model.TaskStatusRunning, step.Title+" · 片末截帧已保存", "")
		}
		if step.FrameAssetID == "" {
			step.Phase = "registering"
		}
		if err := s.registerVideoChainFrame(task, step); err != nil {
			return fail(fmt.Errorf("分镜参考保存或绑定已失效：%w", err))
		}
	}
	input, err := s.videoChainInput(task, step)
	if err != nil {
		return fail(err)
	}
	if step.Phase != "prepared" {
		step.Phase = "uploading"
		if err := s.uploadVideoChainReferences(ctx, task, &state, step, input); err != nil {
			return fail(err)
		}
		step.Phase = "prepared"
		return s.saveVideoChain(task, state, model.TaskStatusRunning, step.Title+" · 参考素材已准备", "")
	}
	if state.Request.PrepareOnly {
		step.Status, step.Phase = "succeeded", "reference_ready"
		return s.saveVideoChain(task, state, model.TaskStatusSucceeded, "站位参考已准备，未提交视频生成", "")
	}
	step.Phase = "submitting"
	maxCharge := int64(0)
	if step.Quote != nil {
		maxCharge = step.Quote.AmountMicrocredits
	}
	metadata, _ := input["metadata"].(map[string]any)
	_, err = s.CreateTask(task.UserID, CreateTaskRequest{ProjectID: task.ProjectID, Type: "canvas_video", Operation: stringValue(metadata["videoEditOperation"]), Model: step.Model, LogicalModelID: step.LogicalModelID, Prompt: stringValue(input["prompt"]), Input: input, admission: &taskAdmission{ID: step.TaskID, MaxCharge: maxCharge, ParentControllerID: task.ID, ParentControllerLease: task.LeaseOwner}})
	if err != nil {
		// A prior lease may have committed this exact task before its controller
		// checkpoint. The deterministic ID is the idempotency boundary.
		if _, exists := s.repo.TaskForUser(task.UserID, step.TaskID); exists != nil {
			return fail(err)
		}
	}
	step.Status, step.Phase = "running", "generating"
	return s.saveVideoChain(task, state, model.TaskStatusRunning, step.Title+" 已提交，后续等待", "")
}

func (s *Service) saveVideoChain(task *model.Task, state videoChainState, status model.TaskStatus, stage, reason string) error {
	raw, err := s.encodeVideoChainState(state)
	if err != nil {
		return err
	}
	completed := 0
	for _, step := range state.Steps {
		if step.Status == "succeeded" {
			completed++
		}
	}
	progress := 0
	if len(state.Steps) > 0 {
		progress = completed * 100 / len(state.Steps)
	}
	return s.repo.SaveProjectVideoChain(task, raw, status, stage, strings.TrimSpace(reason), progress)
}
