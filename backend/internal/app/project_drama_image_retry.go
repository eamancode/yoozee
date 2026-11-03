package app

import (
	"encoding/json"
	"strings"
	"time"

	"yingce/backend/internal/model"
)

// The task snapshot CAS claims the one retry across workers and restarts.
// RetryTask keeps the task ID and enforces billing, quota and submission safety.
func (s *Service) retryFailedDramaImage(task model.Task, asset *model.Asset) bool {
	if task.Type != "canvas_image" || task.Status != model.TaskStatusFailed || task.MediaRecoveryJSON != "" {
		return false
	}
	state := dramaImageStateOf(asset, assetImageKey)
	if strings.Contains(task.InputJSON, `"dramaAsset"`) && (state.TaskID != task.ID || state.Status != dramaImageQueued) {
		return false
	}
	if state.TaskID != "" && state.TaskID != task.ID && s.dramaImageTaskActive(task.UserID, state.TaskID) {
		return false
	}
	decrypted, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		return false
	}
	var input map[string]any
	if json.Unmarshal([]byte(decrypted), &input) != nil {
		return false
	}
	metadata, _ := input["metadata"].(map[string]any)
	if metadata == nil {
		return false
	}
	if version, _ := metadata["projectAssetVersionId"].(string); version != "" && version != asset.PrimaryVersionID {
		return false
	}
	if count, _ := metadata["_assetImageAutoRetryCount"].(float64); count >= 1 {
		return false
	}
	if isContentModerationFailure(task.Error) || dramaImageChannelFailure(task.Error) {
		return false
	}
	if err := s.taskBilling().CheckRetryEligibility(task.BillingOrderID); err != nil {
		return false
	}
	metadata["_assetImageAutoRetryCount"] = 1
	if err := s.protectTaskSecrets(input); err != nil {
		return false
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return false
	}
	claimed, err := s.repo.ClaimAssetImageAutoRetry(task.UserID, task.ID, task.InputJSON, string(encoded))
	if err != nil || !claimed {
		latest, readErr := s.repo.Task(task.ID)
		return readErr == nil && (latest.Status == model.TaskStatusQueued || latest.Status == model.TaskStatusRunning)
	}
	// Pipeline assets expose progress on their card. Manual asset tasks use the
	// same durable task claim without replacing an unrelated card operation.
	if state.TaskID != task.ID || state.Status != dramaImageQueued {
		if !strings.Contains(task.InputJSON, `"dramaAsset"`) {
			state = dramaImageState{Status: dramaImageQueued, TaskID: task.ID}
		} else {
			return false
		}
	}
	state.AutoRetryCount = 1
	state.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	payload := assetPayload(asset)
	payloadMetadata(payload)[assetImageKey] = state
	raw, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	_, _ = s.repo.UpdateAssetPayloadIfUnchanged(task.UserID, asset.ID, asset.PayloadJSON, string(raw), time.Now())
	if _, err := s.RetryTask(task.UserID, task.ID); err != nil {
		_ = s.log(task.UserID, task.ID, "warn", "资产自动重试未排上", userFacingTaskError(err.Error()))
		// The normal terminal path records the failure without losing the retry count.
		asset.PayloadJSON = string(raw)
		return false
	}
	_ = s.log(task.UserID, task.ID, "info", "资产出图自动重试（1/1）", "")
	return true
}

// Task errors are persisted as safe provider summaries; match those canonical classifications, never raw upstream bodies.
func dramaImageChannelFailure(message string) bool {
	for _, code := range []int{401, 403, 402, 404} {
		if strings.HasPrefix(message, (providerHTTPError{StatusCode: code}).summary()) {
			return true
		}
	}
	return false
}
