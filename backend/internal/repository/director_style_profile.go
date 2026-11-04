package repository

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
	"yingce/backend/internal/model"
)

// The project lock serializes repeated completions and keeps one personal source.
func (r *Repository) SaveDirectorStyleProfile(profile *model.StyleProfile, expectedRevision *int64) (*model.StyleProfile, error) {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if profile.SourceProjectID != "" {
			var project model.Project
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", profile.SourceProjectID, profile.UserID).First(&project).Error; err != nil {
				return err
			}
		}
		var current model.StyleProfile
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", profile.ID, profile.UserID).First(&current).Error
		if err == nil && current.SourceFingerprint == profile.SourceFingerprint {
			*profile = current
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if expectedRevision != nil && current.Revision != *expectedRevision {
			return ErrTaskStateConflict
		}
		profile.Revision = current.Revision + 1
		profile.CoverURL, profile.CoverResourceID, profile.CoverImageRevision = current.CoverURL, current.CoverResourceID, current.CoverImageRevision
		profile.Favorite, profile.LastUsedAt = current.Favorite, current.LastUsedAt
		if current.ID != "" {
			profile.CreatedAt = current.CreatedAt
		}
		var document map[string]any
		if err := json.Unmarshal([]byte(profile.ProfileJSON), &document); err != nil {
			return err
		}
		document["revision"], document["presetId"], document["sourceProfileId"], document["source"] = profile.Revision, profile.ID, profile.ID, "user"
		document["coverUrl"] = profile.CoverURL
		raw, err := json.Marshal(document)
		if err != nil {
			return err
		}
		profile.ProfileJSON = string(raw)
		if current.ID == "" {
			created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(profile)
			if created.Error != nil {
				return created.Error
			}
			if created.RowsAffected == 0 {
				return tx.Where("id = ? AND user_id = ?", profile.ID, profile.UserID).First(profile).Error
			}
		} else if err := tx.Save(profile).Error; err != nil {
			return err
		}
		if profile.SourceProjectID != "" && profile.SourceLibraryID == "" {
			return tx.Model(&model.Project{}).Where("id = ? AND user_id = ?", profile.SourceProjectID, profile.UserID).Updates(map[string]any{"style_profile_json": profile.ProfileJSON, "style_preset_id": profile.ID, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now()}).Error
		}
		return nil
	})
	return profile, err
}

func (r *Repository) ReserveStyleProfileCover(profile *model.StyleProfile, taskID string) (bool, error) {
	result := r.db.Model(&model.StyleProfile{}).Where("id = ? AND user_id = ? AND revision = ? AND COALESCE(cover_task_id, '') = ?", profile.ID, profile.UserID, profile.Revision, profile.CoverTaskID).
		Updates(map[string]any{"cover_task_id": taskID, "cover_revision": profile.Revision, "cover_attempt": profile.CoverAttempt + 1, "cover_status": "queued", "cover_error": "", "updated_at": time.Now()})
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) SetStyleProfileCoverState(userID, id, taskID, status, reason string) error {
	return r.db.Model(&model.StyleProfile{}).Where("id = ? AND user_id = ? AND cover_task_id = ?", id, userID, taskID).Updates(map[string]any{"cover_status": status, "cover_error": reason, "updated_at": time.Now()}).Error
}

func (r *Repository) CompleteStyleProfileCover(userID, id, taskID string, revision int64, resourceID, url string) (bool, error) {
	applied := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var profile model.StyleProfile
		if err := tx.Where("id = ? AND user_id = ?", id, userID).First(&profile).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		// Use the same project -> profile lock order as director updates.
		if profile.SourceProjectID != "" {
			var project model.Project
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", profile.SourceProjectID, userID).First(&project).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND cover_task_id = ? AND revision = ? AND cover_revision = ?", id, userID, taskID, revision, revision).First(&profile).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		var document map[string]any
		if err := json.Unmarshal([]byte(profile.ProfileJSON), &document); err != nil {
			return err
		}
		document["coverUrl"] = url
		raw, err := json.Marshal(document)
		if err != nil {
			return err
		}
		result := tx.Model(&model.StyleProfile{}).Where("id = ? AND user_id = ? AND cover_task_id = ? AND revision = ?", id, userID, taskID, revision).Updates(map[string]any{"cover_url": url, "cover_resource_id": resourceID, "cover_status": "ready", "cover_error": "", "cover_image_revision": revision, "profile_json": string(raw), "updated_at": time.Now()})
		if result.Error != nil {
			return result.Error
		}
		applied = result.RowsAffected == 1
		if applied && profile.SourceProjectID != "" {
			return tx.Model(&model.Project{}).Where("id = ? AND user_id = ? AND style_profile_json = ?", profile.SourceProjectID, userID, profile.ProfileJSON).Updates(map[string]any{"style_profile_json": string(raw), "revision": gorm.Expr("revision + 1"), "updated_at": time.Now()}).Error
		}
		return nil
	})
	return applied, err
}

func (r *Repository) PersonalDirectorStyleEntries(userID string) ([]model.DramaStyleLibraryEntry, error) {
	var rows []model.DramaStyleLibraryEntry
	err := r.db.Where("created_by = ? AND scope_owner = ? AND source IN ?", userID, userID, []string{"director", "analysis"}).Find(&rows).Error
	return rows, err
}
