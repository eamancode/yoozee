package repository

import "yingce/backend/internal/model"

// ClaimAssetImageAutoRetry persists the one automatic retry before enqueueing.
// Status and snapshot comparison make duplicate worker callbacks harmless.
func (r *Repository) ClaimAssetImageAutoRetry(userID, taskID, expectedInput, nextInput string) (bool, error) {
	result := r.db.Model(&model.Task{}).
		Where("id = ? AND user_id = ? AND type = ? AND status = ? AND input_json = ?", taskID, userID, "canvas_image", model.TaskStatusFailed, expectedInput).
		Update("input_json", nextInput)
	return result.RowsAffected == 1, result.Error
}
