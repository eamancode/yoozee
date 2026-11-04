package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
	"yingce/backend/internal/model"
)

func (r *Repository) StoryboardReferenceConsumers(projectID, versionID string) ([]model.StoryboardReferenceConsumer, error) {
	var rows []model.StoryboardReferenceConsumer
	err := r.db.Table("shot_asset_references ref").Select("ref.shot_id, shots.title, ref.status").Joins("JOIN shots ON shots.id = ref.shot_id").Where("shots.project_id = ? AND ref.asset_version_id = ? AND ref.role = ?", projectID, versionID, "continuity").Order("shots.position").Scan(&rows).Error
	return rows, err
}

// Registration shares the cancellation and lease fence with child admission.
func (r *Repository) RegisterStoryboardReference(controller *model.Task, revisionID string, asset *model.Asset, version *model.AssetVersion, representation *model.AssetRepresentation, link *model.ProjectAssetLink, reference *model.ShotAssetReference, alreadyRegistered bool) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		fence := &model.Task{UserID: controller.UserID, ProjectID: controller.ProjectID, ParentControllerID: controller.ID, ParentControllerLease: controller.LeaseOwner}
		if err := requireVideoChainParent(tx, fence); err != nil {
			return err
		}
		var shot model.Shot
		if err := tx.Where("id = ? AND project_id = ? AND current_revision_id = ?", reference.ShotID, link.ProjectID, revisionID).First(&shot).Error; err != nil {
			return err
		}
		var resource model.Resource
		if err := tx.Where("id = ? AND user_id = ? AND status = ?", representation.ResourceID, asset.UserID, model.ResourceStatusReady).First(&resource).Error; err != nil {
			return err
		}
		if alreadyRegistered {
			var bound model.ShotAssetReference
			return tx.Where("id = ? AND shot_id = ? AND asset_version_id = ? AND status = ?", reference.ID, shot.ID, version.ID, "linked").First(&bound).Error
		}
		for _, row := range []any{asset, version, representation, link} {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.ShotAssetReference{}).Where("shot_id = ? AND role = ? AND id <> ? AND status = ?", shot.ID, "continuity", reference.ID, "linked").Update("status", "superseded").Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.Assignments(map[string]any{"status": "linked"})}).Create(reference).Error; err != nil {
			return err
		}
		return tx.Model(&model.Project{}).Where("id = ? AND user_id = ?", link.ProjectID, asset.UserID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": time.Now()}).Error
	})
}
