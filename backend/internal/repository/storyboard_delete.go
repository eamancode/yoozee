package repository

import (
	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// Task admission and deletion both fence on the owned project before reading its shots.
// The no-op update also obtains SQLite's write lock before any snapshot reads.
func (r *Repository) LockActiveStoryboardProject(userID, projectID string) (*model.Project, error) {
	result := r.db.Model(&model.Project{}).Where("id = ? AND user_id = ?", projectID, userID).
		UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, gorm.ErrRecordNotFound
	}
	return r.ProjectForUser(userID, projectID)
}

func (r *Repository) ShotArtifactIDs(projectID, shotID string) ([]string, error) {
	var ids []string
	err := r.db.Model(&model.ShotArtifact{}).Where("project_id = ? AND shot_id = ?", projectID, shotID).
		Order("id").Pluck("id", &ids).Error
	return ids, err
}

// A concurrent repack must not overwrite the pruned source with its old snapshot.
func (r *Repository) SavePrunedShotlist(record *model.DramaShotlist) error {
	return upsertDramaShotlist(r.db, record, record.Version)
}

func (r *Repository) OtherUnitShotSpecs(shot *model.Shot) ([]model.ShotRevision, error) {
	var revisions []model.ShotRevision
	err := r.db.Table("shots").Select("shots.duration_ms, shots.id AS shot_id, shot_revisions.id, shot_revisions.spec_json").
		Joins("LEFT JOIN shot_revisions ON shot_revisions.id = shots.current_revision_id AND shot_revisions.shot_id = shots.id").
		Where("shots.project_id = ? AND shots.unit_id = ? AND shots.id <> ?", shot.ProjectID, shot.UnitID, shot.ID).
		Order("shots.position, shots.created_at, shots.id").Scan(&revisions).Error
	return revisions, err
}
