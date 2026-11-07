package repository

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

func (r *Repository) ActiveSpatialTask(userID, projectID, key string) (*model.Task, error) {
	var task model.Task
	err := r.db.Where("user_id = ? AND project_id = ? AND status IN ? AND input_json LIKE ?", userID, projectID, []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning}, `%"spatialSourceKey":"`+key+`"%`).Order("created_at DESC").First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &task, err
}

func (r *Repository) SaveProjectSpatialImage(userID, projectID, assetID, expectedVersion string, representation *model.AssetRepresentation) (bool, error) {
	applied := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var asset model.Asset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", assetID, userID).First(&asset).Error; err != nil {
			return err
		}
		var linked int64
		if err := tx.Model(&model.ProjectAssetLink{}).Where("project_id = ? AND asset_id = ?", projectID, assetID).Count(&linked).Error; err != nil {
			return err
		}
		if linked != 1 {
			return gorm.ErrRecordNotFound
		}
		if asset.PrimaryVersionID != expectedVersion {
			return ErrProjectAssetVersionChanged
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(representation)
		applied = result.RowsAffected == 1
		return result.Error
	})
	return applied, err
}
