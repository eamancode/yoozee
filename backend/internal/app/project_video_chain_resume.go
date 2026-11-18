package app

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (s *Service) checkOriginalVideoChain(repo *repository.Repository, userID, projectID string, next videoChainState) error {
	if next.Request.PrepareOnly {
		return nil
	}
	parents, err := repo.InactiveVideoChains(userID, projectID, videoChainOperation(next.UnitID))
	if err != nil {
		return err
	}
	requested := make(map[string]bool, len(next.Steps))
	for _, step := range next.Steps {
		if step.Status != "succeeded" {
			requested[step.ShotID] = true
		}
	}
	for _, parent := range parents {
		if parent.ID == next.Request.ResumeChainID {
			continue
		}
		var state struct{ Steps []ProjectVideoChainStep }
		if err := json.Unmarshal([]byte(parent.InputJSON), &state); err != nil {
			return err
		}
		for _, step := range state.Steps {
			if !requested[step.ShotID] || step.Status == "succeeded" || step.TaskID == "" {
				continue
			}
			// Child admission may have committed just before cancellation or
			// loss of the controller lease, with its parent still at "prepared".
			child, err := repo.TaskForUser(userID, step.TaskID)
			if errors.Is(err, gorm.ErrRecordNotFound) && step.Phase != "generating" {
				continue
			}
			if err != nil {
				return err
			}
			if child.ProjectID != projectID || child.Type != "canvas_video" {
				return failedPrecondition("原视频请求与当前项目不匹配")
			}
			d, err := videoChainDiagnosticFromRepo(repo, child)
			if err != nil {
				return err
			}
			if child.Status == model.TaskStatusSucceeded && (d.BillingStatus == "none" || d.BillingStatus == "settled") ||
				(parent.Status == model.TaskStatusCancelled || next.Request.ResumeChainID != "") && d.SafeToResubmit {
				continue
			}
			e := failedPrecondition("本段已有原生成请求，请查看原任务状态并从原失败段重新报价")
			e.Details = map[string]any{"resumeChainId": parent.ID, "shotId": step.ShotID, "taskId": child.ID, "diagnostic": d}
			return e
		}
	}
	return nil
}

// Reconcile the original child and its order before changing models. A missing
// local child is expected for failures in preflight/upload; it is not proof of
// a rejected submission once the controller checkpoint says "generating".
func (s *Service) videoChainRecoveryChild(parent *model.Task, step *videoChainStepState) (*model.Task, error) {
	if step.TaskID == "" {
		return nil, nil
	}
	child, err := s.repo.TaskForUser(parent.UserID, step.TaskID)
	if errors.Is(err, gorm.ErrRecordNotFound) && step.Phase != "generating" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if child.Type != "canvas_video" || child.ProjectID != parent.ProjectID {
		return nil, failedPrecondition("原视频请求不属于当前项目")
	}
	d, err := s.videoChainAttemptDiagnostic(child)
	if err != nil {
		return nil, err
	}
	step.Diagnostic = &d
	return child, nil
}

func unresolvedVideoChainAttempt(chainID string, step videoChainStepState) *AppError {
	msg := "原请求受理状态或费用尚未核实，请根据原任务与渠道回执核对后继续"
	if d := step.Diagnostic; d != nil {
		switch {
		case d.CanRecoverMedia:
			msg = "原视频已生成，请先恢复原任务的作品保存"
		case d.CanQueryProvider:
			msg = "请先查询原视频任务的上游终态与结算，确认结果后再继续"
		case d.BillingStatus == "uncertain":
			msg = "原视频任务费用仍待核对，请查看原订单后继续"
		}
	}
	err := failedPrecondition(msg)
	err.Details = map[string]any{"resumeChainId": chainID, "shotId": step.ShotID, "taskId": step.TaskID, "diagnostic": step.Diagnostic}
	return err
}

func videoChainConfirmationBlock(chainID string, step videoChainStepState, child *model.Task) *AppError {
	if strings.HasPrefix(child.LeaseOwner, "manual-recovery:") && child.LeaseExpiresAt != nil && child.LeaseExpiresAt.After(time.Now()) {
		err := unresolvedVideoChainAttempt(chainID, step)
		err.Message = "原视频任务正在回查，请等待回查结束后重新报价"
		return err
	}
	if !step.Diagnostic.SafeToResubmit {
		return unresolvedVideoChainAttempt(chainID, step)
	}
	return nil
}

func (s *Service) prepareVideoChainResume(task *model.Task, old videoChainState, req ProjectVideoChainRequest, previewOnly bool) (videoChainState, error) {
	if task.Status != model.TaskStatusFailed {
		return old, failedPrecondition("只有已暂停失败的生成链可以重新报价")
	}
	if len(old.Steps) == 0 {
		return old, failedPrecondition("原生成链缺少分段记录，无法确定恢复位置")
	}
	if req.ResumeChainID != task.ID || req.UnitID != old.UnitID || req.ShotID != old.AnchorShotID ||
		(req.Scope == "single") != (old.Request.Scope == "single") || req.PrepareOnly != old.Request.PrepareOnly {
		return old, BadAuthRequest("恢复请求与原链的章节、起始段或生成范围不一致")
	}
	completed := 0
	var originFingerprint, blockedReason string
	for i := range old.Steps {
		step := &old.Steps[i]
		if step.Status == "succeeded" {
			completed++
			continue
		}
		child, err := s.videoChainRecoveryChild(task, step)
		if err != nil {
			return old, err
		}
		if child == nil {
			break
		}
		if child.Status == model.TaskStatusSucceeded && s.billingSettled(child) {
			id, _ := taskOutputResource(child.ResultJSON, child.Type)
			if _, err := s.videoChainReadyResource(task.UserID, id, "video/"); err != nil {
				return old, err
			}
			step.Status, step.Phase, step.Error, step.VideoResourceID = "succeeded", "completed", "", id
			completed++
			continue
		}
		if block := videoChainConfirmationBlock(task.ID, *step, child); block != nil {
			if !previewOnly {
				return old, block
			}
			blockedReason = block.Message
		}
		originFingerprint = videoChainHash([]any{step.Signature, child.ID, child.RouteRun, child.BillingOrderID, step.Diagnostic, step.Diagnostic.originalRequestID, blockedReason})
		break
	}
	if completed == len(old.Steps) {
		// The original upstream job delivered everything. Closing its
		// controller requires neither a live model nor a new quote for work.
		old.Request = req
		old.PreviewHash = videoChainHash([]any{task.ID, old.PreviewHash, "original-results-complete"})
		return old, nil
	}
	next, err := s.prepareVideoChainSteps(task.UserID, task.ProjectID, req, old.WhiteModelStoryboardEnabled, old.Steps[:completed])
	if err != nil {
		return old, err
	}
	if len(next.Steps) != len(old.Steps) {
		return old, failedPrecondition("原链的镜头数量已变化，请核对分镜与已有结果")
	}
	for i := completed; i < len(next.Steps); i++ {
		step, previous := &next.Steps[i], old.Steps[i]
		if step.ShotID != previous.ShotID {
			return old, failedPrecondition("原链的镜头顺序已变化，请核对分镜与已有结果")
		}
		step.PreviousTaskIDs = previous.PreviousTaskIDs
		if previous.Diagnostic != nil {
			step.PreviousTaskIDs = append(step.PreviousTaskIDs, previous.TaskID)
			if previewOnly {
				step.TaskID, step.Diagnostic = previous.TaskID, previous.Diagnostic
			}
		}
		if previous.FrameResourceID != "" && step.SourceShotID != "" && step.SourceShotID == previous.SourceShotID && step.SourceRevisionID == previous.SourceRevisionID && !step.ReferenceSkipped {
			if _, err := s.videoChainReadyResource(task.UserID, previous.FrameResourceID, "image/"); err == nil {
				step.FrameResourceID, step.FrameSelection, step.FrameTimeSeconds = previous.FrameResourceID, previous.FrameSelection, previous.FrameTimeSeconds
				step.SourceVideoResourceID, step.SourceVideoTaskID = previous.SourceVideoResourceID, previous.SourceVideoTaskID
				// registerVideoChainFrame checks the current binding and reuses
				// the deterministic asset for this source.
			}
		}
		if step.Signature == previous.Signature {
			step.Uploads, step.UploadScope = previous.Uploads, previous.UploadScope
		}
	}
	next.confirmationBlockedReason = blockedReason
	next.PreviewHash = videoChainHash([]any{task.ID, old.PreviewHash, completed, next.PreviewHash, originFingerprint})
	next.ResumeConfirmations = old.ResumeConfirmations
	return next, s.checkOriginalVideoChain(s.repo, task.UserID, task.ProjectID, next)
}

func (s *Service) RetryProjectVideoChain(userID, projectID, id string, req ProjectVideoChainRequest) (ProjectVideoChainView, error) {
	if s.IsDraining() {
		return ProjectVideoChainView{}, BadAuthRequest("服务正在维护，请稍后继续")
	}
	if req.ResumeChainID != id || strings.TrimSpace(req.RequestID) == "" || len(req.RequestID) > 160 || req.PreviewHash == "" {
		return ProjectVideoChainView{}, BadAuthRequest("请先重新报价，再使用同一确认标识从失败段继续")
	}
	task, old, err := s.videoChainTaskForUser(userID, projectID, id)
	if err != nil {
		return ProjectVideoChainView{}, err
	}
	confirmation := videoChainHash(req)
	if prior, ok := old.ResumeConfirmations[req.RequestID]; ok {
		if prior != confirmation {
			return ProjectVideoChainView{}, failedPrecondition("此确认标识已用于其他报价，请重新预览")
		}
		return videoChainView(projectID, task, old), nil
	}
	next, err := s.prepareVideoChainResume(task, old, req, false)
	if err != nil {
		return ProjectVideoChainView{}, err
	}
	if req.PreviewHash != next.PreviewHash {
		return ProjectVideoChainView{}, failedPrecondition("本段、原任务状态、模型或报价已变化，请重新预览")
	}
	if next.ResumeConfirmations == nil {
		next.ResumeConfirmations = make(map[string]string)
	}
	next.ResumeConfirmations[req.RequestID] = confirmation
	for i := range next.Steps {
		step := &next.Steps[i]
		if step.Status != "succeeded" {
			step.TaskID = videoChainHash([]string{id, req.RequestID, next.PreviewHash, step.ShotID})[:32]
		}
	}
	raw, err := s.encodeVideoChainState(next)
	if err != nil {
		return ProjectVideoChainView{}, err
	}
	check := func(tx *repository.Repository) error {
		for i, step := range next.Steps {
			if step.Status == "succeeded" || old.Steps[i].Diagnostic == nil {
				continue
			}
			child, err := tx.LockOriginalVideoChainChild(userID, projectID, old.Steps[i].TaskID)
			if err != nil {
				return err
			}
			d, err := videoChainDiagnosticFromRepo(tx, child)
			if err != nil {
				return err
			}
			original := old.Steps[i]
			original.Diagnostic = &d
			if block := videoChainConfirmationBlock(id, original, child); block != nil {
				return block
			}
		}
		if err := (&Service{repo: tx}).verifySceneChainSnapshots(userID, projectID, next); err != nil {
			return err
		}
		if req.PrepareOnly || next.Steps[len(next.Steps)-1].Status == "succeeded" {
			return nil
		}
		if err := s.checkVideoChainQuoteRevision(tx, userID, projectID, next); err != nil {
			return err
		}
		return s.checkOriginalVideoChain(tx, userID, projectID, next)
	}
	if err := s.repo.ResumeProjectVideoChain(*task, raw, check); err != nil {
		if errors.Is(err, repository.ErrTaskStateConflict) {
			latest, saved, readErr := s.videoChainTaskForUser(userID, projectID, id)
			if readErr == nil && saved.ResumeConfirmations[req.RequestID] == confirmation {
				return videoChainView(projectID, latest, saved), nil
			}
			return ProjectVideoChainView{}, failedPrecondition("已有生成链在运行或原链状态已变化，请刷新")
		}
		return ProjectVideoChainView{}, storyboardTaskAdmissionError(err)
	}
	task, saved, err := s.videoChainTaskForUser(userID, projectID, id)
	if err != nil {
		return ProjectVideoChainView{}, err
	}
	s.wakeTaskDispatcher()
	return videoChainView(projectID, task, saved), nil
}
