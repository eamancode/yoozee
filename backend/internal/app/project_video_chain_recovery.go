package app

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const videoRecoveryQuoteTTL = 5 * time.Minute

func videoRecoveryRequestID(userID, projectID, unitID, requestID string) string {
	return videoChainHash([]string{userID, projectID, unitID, requestID})[:32]
}

func validVideoRecoveryRequestID(id string) bool {
	return id != "" && strings.TrimSpace(id) == id && len(id) <= 160
}

// ProjectVideoChainReceipt has no side effects. A durable queued task is the
// receipt; the worker cannot run a request that has not committed its receipt.
func (s *Service) ProjectVideoChainReceipt(userID, projectID, unitID, requestID string) (*ProjectVideoChainView, error) {
	if !validVideoRecoveryRequestID(requestID) || unitID == "" {
		return nil, BadAuthRequest("生成回执缺少章节或确认标识")
	}
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	if _, err := s.repo.ProjectUnit(projectID, unitID); err != nil {
		return nil, err
	}
	task, state, err := s.videoChainTaskForUser(userID, projectID, videoRecoveryRequestID(userID, projectID, unitID, requestID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if state.UnitID != unitID || state.Request.RequestID != requestID {
		return nil, nil
	}
	view := videoChainView(projectID, task, state)
	return &view, nil
}

func (s *Service) prepareVideoRecovery(userID, projectID string, req ProjectVideoChainRequest, preview bool) (videoChainState, *model.Task, error) {
	var state videoChainState
	if !validVideoRecoveryRequestID(req.RequestID) || req.UnitID == "" || req.PrepareOnly ||
		(req.ResumeChainID != "" && req.RestartChainID != "") {
		return state, nil, BadAuthRequest("连续生成请求缺少有效的确认标识或模式无效")
	}
	if preview {
		req.QuoteExpiresAt = time.Now().Add(videoRecoveryQuoteTTL).Unix()
	} else if req.QuoteExpiresAt <= time.Now().Unix() || req.QuoteExpiresAt > time.Now().Add(videoRecoveryQuoteTTL).Unix() {
		return state, nil, failedPrecondition("报价已过期，请重新报价后再确认")
	}
	sourceID := req.ResumeChainID
	if sourceID == "" {
		sourceID = req.RestartChainID
	}
	var source *model.Task
	var err error
	if sourceID == "" {
		state, err = s.prepareProjectVideoChain(userID, projectID, req)
	} else {
		var old videoChainState
		source, old, err = s.videoChainTaskForUser(userID, projectID, sourceID)
		if err != nil {
			return state, nil, err
		}
		if source.Status != model.TaskStatusFailed || old.UnitID != req.UnitID || old.AnchorShotID != req.ShotID ||
			(old.Request.Scope == "single") != (req.Scope == "single") || old.Request.PrepareOnly {
			return state, nil, failedPrecondition("原生成链、章节或起始段已变化，请刷新后重试")
		}
		if req.ResumeChainID != "" {
			state, err = s.prepareVideoChainResume(source, old, req, preview)
			if err == nil {
				err = s.verifyVideoRecoveryPrefix(userID, projectID, state)
			}
		} else {
			// Intentionally pass no completed steps or uploads: restart must generate
			// the chain from its beginning and preserve the source as history.
			state, err = s.prepareProjectVideoChain(userID, projectID, req)
		}
	}
	if err != nil {
		return state, nil, err
	}
	if err := s.checkOriginalVideoChain(s.repo, userID, projectID, state); err != nil {
		return state, nil, err
	}
	sourceFingerprint := ""
	if source != nil {
		sourceFingerprint = videoChainHash([]any{source.ID, source.Status, source.InputJSON})
	}
	// The binding includes the immutable input and per-step price fingerprint,
	// explicit mode/source, ownership scope, one request key and expiry.
	req.PreviewHash = ""
	state.Request.QuoteExpiresAt = req.QuoteExpiresAt
	var reused [][3]string
	for _, step := range state.Steps {
		if step.Status == "succeeded" {
			reused = append(reused, [3]string{step.ShotID, step.RevisionID, step.VideoResourceID})
		}
	}
	state.PreviewHash = videoChainHash([]any{"video-recovery-v2", userID, projectID, req, state.PreviewHash, sourceFingerprint, reused, state.confirmationBlockedReason})
	return state, source, nil
}

func (s *Service) verifyVideoRecoveryPrefix(userID, projectID string, state videoChainState) error {
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return err
	}
	if state.WhiteModelStoryboardEnabled == nil || project.WhiteModelStoryboardEnabled != *state.WhiteModelStoryboardEnabled {
		return failedPrecondition("白模分镜设置已变化，无法复用原生成链；请从头重新生成")
	}
	shots, err := s.repo.ProjectUnitShots(projectID, state.UnitID)
	if err != nil {
		return err
	}
	revisions := make(map[string]string, len(shots))
	for _, shot := range shots {
		revisions[shot.ID] = shot.CurrentRevisionID
	}
	for _, step := range state.Steps {
		if step.Status != "succeeded" {
			break
		}
		if revisions[step.ShotID] != step.RevisionID || step.RevisionID == "" {
			return failedPrecondition("已完成段的分镜已修改，无法安全复用；请明确选择从头重新生成")
		}
		if step.SceneSignature != "" {
			if err := s.verifySceneSnapshot(userID, projectID, step.ShotID, step.RevisionID, step.SceneSignature); err != nil {
				return failedPrecondition("已完成段的场景或参考素材已修改；请从头重新生成")
			}
		}
		if _, err := s.videoChainReadyResource(userID, step.VideoResourceID, "video/"); err != nil {
			return failedPrecondition("已完成段的视频已不可用，无法安全复用；请明确选择从头重新生成")
		}
	}
	return nil
}

func (s *Service) confirmVideoRecovery(userID, projectID string, req ProjectVideoChainRequest) (ProjectVideoChainView, error) {
	var view ProjectVideoChainView
	if !validVideoRecoveryRequestID(req.RequestID) || req.UnitID == "" || req.PreviewHash == "" {
		return view, BadAuthRequest("请先报价，再用同一个确认标识提交")
	}
	requestFingerprint := videoChainHash(req)
	id := videoRecoveryRequestID(userID, projectID, req.UnitID, req.RequestID)
	receipt := func(task *model.Task, state videoChainState) (ProjectVideoChainView, error) {
		if state.ReceiptFingerprint != requestFingerprint {
			return view, failedPrecondition("此确认标识已用于另一份报价，请先查看原提交回执")
		}
		return videoChainView(projectID, task, state), nil
	}
	existing, stored, err := s.videoChainTaskForUser(userID, projectID, id)
	if err == nil {
		return receipt(existing, stored)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return view, err
	}
	if s.IsDraining() {
		return view, failedPrecondition("服务正在维护，请稍后重新报价")
	}
	state, source, err := s.prepareVideoRecovery(userID, projectID, req, false)
	if err != nil {
		return view, err
	}
	if state.PreviewHash != req.PreviewHash {
		return view, failedPrecondition("镜头、线路、原任务或报价已变化，请重新预览")
	}
	state.ReceiptFingerprint = requestFingerprint
	state.ResumeConfirmations = nil
	for i := range state.Steps {
		step := &state.Steps[i]
		if step.Status != "succeeded" {
			step.TaskID = videoChainHash([]string{id, step.ShotID, step.Signature})[:32]
		}
	}
	raw, err := s.encodeVideoChainState(state)
	if err != nil {
		return view, err
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return view, err
	}
	now := time.Now()
	task := model.Task{ID: id, UserID: userID, ProjectID: projectID, Type: model.TaskTypeProjectVideoChain,
		Operation: videoChainOperation(req.UnitID), Status: model.TaskStatusQueued, Stage: "已确认，等待连续生成", Prompt: "同场景连续视频生成",
		InputJSON: raw, RouteRun: 1, CreatedAt: now, UpdatedAt: now}
	check := func(tx *repository.Repository) error {
		if req.QuoteExpiresAt <= time.Now().Unix() {
			return failedPrecondition("报价已过期，请重新报价后再确认")
		}
		if source != nil {
			current, err := tx.LockVideoRecoverySource(userID, projectID, source.ID)
			if err != nil {
				return err
			}
			if current.Status != model.TaskStatusFailed || source.InputJSON != current.InputJSON {
				return failedPrecondition("原任务已变化，请刷新并重新报价")
			}
			old, err := s.videoChainState(current)
			if err != nil {
				return err
			}
			for i := range old.Steps {
				step := old.Steps[i]
				if step.TaskID == "" || step.Status == "succeeded" {
					continue
				}
				child, err := tx.LockOriginalVideoChainChild(userID, projectID, step.TaskID)
				if errors.Is(err, gorm.ErrRecordNotFound) && step.Phase != "generating" {
					continue
				}
				if err != nil {
					return err
				}
				d, err := videoChainDiagnosticFromRepo(tx, child)
				if err != nil {
					return err
				}
				step.Diagnostic = &d
				if req.ResumeChainID != "" && i < len(state.Steps) && state.Steps[i].Status == "succeeded" && child.Status == model.TaskStatusSucceeded {
					continue
				}
				if err := videoChainConfirmationBlock(source.ID, step, child); err != nil {
					return err
				}
			}
		}
		scoped := &Service{repo: tx}
		if err := scoped.verifySceneChainSnapshots(userID, projectID, state); err != nil {
			return err
		}
		if req.ResumeChainID != "" {
			if err := scoped.verifyVideoRecoveryPrefix(userID, projectID, state); err != nil {
				return err
			}
		}
		if err := s.checkVideoChainQuoteRevision(tx, userID, projectID, state); err != nil {
			return err
		}
		if total := videoChainView(projectID, nil, state).TotalMicrocredits; total > 0 {
			available, err := tx.AvailableVideoRecoveryCredits(userID)
			if err != nil {
				return err
			}
			if available < total {
				return failedPrecondition("可用积分不足，未提交生成；充值或缩小范围后请重新报价")
			}
		}
		return s.checkOriginalVideoChain(tx, userID, projectID, state)
	}
	s.storageMu.Lock()
	usage, err := s.repo.UserStorageUsage(userID)
	if err == nil {
		err = validateTaskStorageQuotaWithPolicy(usage, int64(len(raw)+len(task.Prompt)), policy.Resource)
	}
	var saved *model.Task
	if err == nil {
		saved, err = s.repo.CreateProjectVideoChain(&task, policy.Task.ActiveTaskLimit, *state.WhiteModelStoryboardEnabled, check)
	}
	s.storageMu.Unlock()
	if err != nil {
		return view, storyboardTaskAdmissionError(err)
	}
	if saved.ID != id {
		return view, videoChainActiveConflict(saved.ID)
	}
	persisted, err := s.videoChainState(saved)
	if err != nil {
		return view, err
	}
	accepted, err := receipt(saved, persisted)
	if err == nil {
		s.wakeTaskDispatcher()
	}
	return accepted, err
}
