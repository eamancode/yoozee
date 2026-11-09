package repository

import (
	"encoding/json"
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
func (r *Repository) RegisterStoryboardReference(controller *model.Task, revisionID string, asset *model.Asset, version *model.AssetVersion, representation *model.AssetRepresentation, link *model.ProjectAssetLink, reference *model.ShotAssetReference, artifact *model.ShotArtifact, alreadyRegistered bool) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		fence := &model.Task{UserID: controller.UserID, ProjectID: controller.ProjectID, ParentControllerID: controller.ID, ParentControllerLease: controller.LeaseOwner}
		if err := requireVideoChainParent(tx, fence); err != nil {
			return err
		}
		var shot model.Shot
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND project_id = ? AND current_revision_id = ?", reference.ShotID, link.ProjectID, revisionID).First(&shot).Error; err != nil {
			return err
		}
		var resource model.Resource
		if err := tx.Where("id = ? AND user_id = ? AND status = ?", representation.ResourceID, asset.UserID, model.ResourceStatusReady).First(&resource).Error; err != nil {
			return err
		}
		var existing int64
		attachmentChanged := false
		var storyboards []model.ShotArtifact
		if err := tx.Where("shot_id = ? AND type = ? AND status = ? AND id <> ?", shot.ID, "storyboard", "ready", artifact.ID).Find(&storyboards).Error; err != nil {
			return err
		}
		for _, previous := range storyboards {
			var meta struct {
				Role string `json:"role"`
			}
			if json.Unmarshal([]byte(previous.MetadataJSON), &meta) == nil && meta.Role == "continuity_reference" {
				if err := tx.Model(&model.ShotArtifact{}).Where("id = ?", previous.ID).Updates(map[string]any{"status": "stale", "selected": false, "updated_at": time.Now()}).Error; err != nil {
					return err
				}
				attachmentChanged = true
			}
		}
		if err := tx.Model(&model.ShotArtifact{}).Where("id = ?", artifact.ID).Count(&existing).Error; err != nil {
			return err
		}
		if existing == 0 {
			if err := lockShotRow(tx, shot.ID); err != nil {
				return err
			}
			var latest int
			if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND type = ?", shot.ID, "storyboard").Select("COALESCE(MAX(version), 0)").Scan(&latest).Error; err != nil {
				return err
			}
			var selected int64
			if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND type = ? AND selected = ? AND status = ?", shot.ID, "storyboard", true, "ready").Count(&selected).Error; err != nil {
				return err
			}
			artifact.Version, artifact.Selected = latest+1, selected == 0
			if err := tx.Create(artifact).Error; err != nil {
				return err
			}
			attachmentChanged = true
		} else {
			var selected int64
			if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND type = ? AND selected = ? AND status = ? AND id <> ?", shot.ID, "storyboard", true, "ready", artifact.ID).Count(&selected).Error; err != nil {
				return err
			}
			result := tx.Model(&model.ShotArtifact{}).Where("id = ? AND (status <> ? OR selected <> ?)", artifact.ID, "ready", selected == 0).Updates(map[string]any{"status": "ready", "selected": selected == 0})
			if result.Error != nil {
				return result.Error
			}
			attachmentChanged = attachmentChanged || result.RowsAffected > 0
		}
		if alreadyRegistered {
			var bound model.ShotAssetReference
			if err := tx.Where("id = ? AND shot_id = ? AND asset_version_id = ? AND status = ?", reference.ID, shot.ID, version.ID, "linked").First(&bound).Error; err != nil {
				return err
			}
			if attachmentChanged {
				return tx.Model(&model.Project{}).Where("id = ? AND user_id = ?", link.ProjectID, asset.UserID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": time.Now()}).Error
			}
			return nil
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
