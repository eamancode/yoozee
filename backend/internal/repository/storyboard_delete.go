package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

// Task admission and deletion both fence on the owned project before reading its shots.
// The no-op update also obtains SQLite's write lock before any snapshot reads.
func (r *Repository) LockActiveStoryboardProject(userID, projectID string) (*model.Project, error) {
	if err := lockStoryboardProject(r.db.Where("user_id = ?", userID), projectID); err != nil {
		return nil, err
	}
	return r.ProjectForUser(userID, projectID)
}

// Writes from the legacy workbench must acquire their lock in the same project scope as its list.
func (r *Repository) LockWorkbenchStoryboardProject(userID, projectID string) (*model.Project, error) {
	if err := lockStoryboardProject(r.workbenchProjects(userID), projectID); err != nil {
		return nil, err
	}
	return r.WorkbenchProjectForUser(userID, projectID)
}

func lockStoryboardProject(tx *gorm.DB, projectID string) error {
	result := tx.Model(&model.Project{}).Where("id = ?", projectID).
		UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Recheck the episode inside the writing transaction. A shot or document
// prepared before a concurrent deletion must never recreate its children.
func lockStoryboardUnit(tx *gorm.DB, projectID, unitID string) error {
	if unitID == "" {
		return nil // project-level shots, documents and links are supported
	}
	if err := lockStoryboardProject(tx, projectID); err != nil {
		return err
	}
	var unit model.ProjectUnit
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").
		Where("project_id = ? AND id = ?", projectID, unitID).Take(&unit).Error
}

// Content records can target an episode, a shot or both.
func lockStoryboardContent(tx *gorm.DB, projectID, unitID, shotID string) error {
	if err := lockStoryboardUnit(tx, projectID, unitID); err != nil {
		return err
	}
	if shotID == "" {
		return nil
	}
	if unitID == "" {
		if err := lockStoryboardProject(tx, projectID); err != nil {
			return err
		}
	}
	shot := tx.Where("project_id = ?", projectID)
	if unitID != "" {
		shot = shot.Where("unit_id = ?", unitID)
	}
	return lockShotRow(shot, shotID)
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
