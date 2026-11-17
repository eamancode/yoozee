package repository

import (
	"encoding/json"

	"yingce/backend/internal/model"
)

// DetachDeletedShotContinuity retires only automatically attached frames
// whose recorded source is this shot. Asset versions and media stay in the library.
// The caller owns the transaction that also removes the source shot.
func (r *Repository) DetachDeletedShotContinuity(shot *model.Shot) error {
	var bindings []struct {
		ID             string
		DefinitionJSON string
	}
	err := r.db.Table("shot_asset_references AS ref").Select("ref.id, v.definition_json").
		Joins("JOIN shots AS consumer ON consumer.id = ref.shot_id").
		Joins("JOIN asset_versions AS v ON v.id = ref.asset_version_id").
		Joins("JOIN assets AS a ON a.id = v.asset_id").
		Where("consumer.project_id = ? AND consumer.unit_id = ? AND consumer.id <> ?", shot.ProjectID, shot.UnitID, shot.ID).
		Where("ref.role = ? AND ref.status = ? AND a.category = ?", "continuity", "linked", model.AssetCategoryStoryboardReference).
		Scan(&bindings).Error
	if err != nil {
		return err
	}
	var refIDs []string
	for _, ref := range bindings {
		var src struct {
			SourceShotID string `json:"sourceShotId"`
		}
		if json.Unmarshal([]byte(ref.DefinitionJSON), &src) == nil && src.SourceShotID == shot.ID {
			refIDs = append(refIDs, ref.ID)
		}
	}
	if len(refIDs) > 0 {
		if err := r.db.Model(&model.ShotAssetReference{}).Where("id IN ?", refIDs).Update("status", "superseded").Error; err != nil {
			return err
		}
	}

	var artifacts []model.ShotArtifact
	if err := r.db.Select("id, metadata_json").Where("project_id = ? AND unit_id = ? AND shot_id <> ? AND type = ? AND (status = ? OR selected = ?)",
		shot.ProjectID, shot.UnitID, shot.ID, "storyboard", "ready", true).Find(&artifacts).Error; err != nil {
		return err
	}
	var artifactIDs []string
	for _, artifact := range artifacts {
		var meta struct {
			Role   string `json:"role"`
			Source struct {
				ShotID string `json:"sourceShotId"`
			} `json:"continuityReference"`
		}
		if json.Unmarshal([]byte(artifact.MetadataJSON), &meta) == nil && meta.Role == "continuity_reference" && meta.Source.ShotID == shot.ID {
			artifactIDs = append(artifactIDs, artifact.ID)
		}
	}
	if len(artifactIDs) > 0 {
		return r.db.Model(&model.ShotArtifact{}).Where("id IN ?", artifactIDs).
			Updates(map[string]any{"status": "stale", "selected": false}).Error
	}
	return nil
}
