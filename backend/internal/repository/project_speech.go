package repository

import (
	"gorm.io/gorm"
	"time"
	"yingce/backend/internal/model"
)

func (r *Repository) SaveUnitSpeechLedger(userID, projectID, unitID, source, ledger string) error {
	result := r.db.Model(&model.ProjectUnit{}).
		Where("id = ? AND project_id = ? AND source_text = ?", unitID, projectID, source).
		Where("project_id IN (?)", r.db.Model(&model.Project{}).Select("id").Where("id = ? AND user_id = ?", projectID, userID)).
		Updates(map[string]any{"speech_ledger_json": ledger, "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
