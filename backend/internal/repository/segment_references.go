package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

type SegmentReferenceSync struct {
	ExpectedRevisionID string
	References         []model.ShotAssetReference
}

func (r *Repository) LockShotForProject(projectID, shotID string) (*model.Shot, error) {
	var shot model.Shot
	err := r.db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&shot, "id = ? AND project_id = ?", shotID, projectID).Error
	return &shot, err
}

// Segment references are a projection of the saved spec. Independent style,
// continuity and manually attached references are not removed by this sync.
func syncSegmentReferences(tx *gorm.DB, projectID, shotID string, refs []model.ShotAssetReference) error {
	ids := make([]string, 0, len(refs))
	for i := range refs {
		ref := &refs[i]
		var count int64
		if err := tx.Table("asset_versions av").Joins("JOIN assets a ON a.id = av.asset_id").
			Joins("JOIN project_asset_links pal ON pal.asset_id = a.id").Joins("JOIN projects p ON p.id = pal.project_id AND p.user_id = a.user_id").
			Where("p.id = ? AND av.id = ?", projectID, ref.AssetVersionID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return gorm.ErrRecordNotFound
		}
		ref.ShotID, ref.Role, ref.Status, ref.Source, ref.Position = shotID, "reference", "linked", "segment", i
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "shot_id"}, {Name: "asset_version_id"}, {Name: "role"}},
			DoUpdates: clause.AssignmentColumns([]string{"status", "source", "position"})}).Create(ref).Error; err != nil {
			return err
		}
		ids = append(ids, ref.AssetVersionID)
	}
	q := tx.Where("shot_id = ? AND source = ? AND role = ?", shotID, "segment", "reference")
	if len(ids) > 0 {
		q = q.Where("asset_version_id NOT IN ?", ids)
	}
	return q.Delete(&model.ShotAssetReference{}).Error
}

// Resolve a fixed version first; an asset ID explicitly means its current version.
func (r *Repository) ProjectReferenceVersion(userID, projectID, file string) (*model.AssetVersion, error) {
	var version model.AssetVersion
	err := r.db.Table("asset_versions av").Select("av.*").Joins("JOIN assets a ON a.id = av.asset_id").
		Joins("JOIN project_asset_links pal ON pal.asset_id = a.id").
		Where("pal.project_id = ? AND a.user_id = ? AND (av.id = ? OR (a.id = ? AND av.id = a.primary_version_id))", projectID, userID, file, file).
		Order("av.id").First(&version).Error
	return &version, err
}
