package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

func (r *Repository) ProjectVideoRoute(projectID string) (*model.ProjectVideoRoute, error) {
	var state model.ProjectVideoRoute
	err := r.db.Where("project_id = ?", projectID).Take(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &state, err
}

// Callers hold the project row lock across receipt deduplication, streak and route changes.
func (r *Repository) ApplyProjectVideoRouteEvent(event *model.ProjectVideoRouteEvent) (int, bool, error) {
	write := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(event)
	// A late receipt can establish that a previously failed attempt actually
	// produced a video. Correct this attempt once. Replaying its success later
	// must not erase failures belonging to subsequent tasks on the same route.
	if write.Error == nil && write.RowsAffected == 0 && event.Decision == "reset" {
		write = r.db.Model(&model.ProjectVideoRouteEvent{}).
			Where("task_id = ? AND route_run = ? AND project_id = ? AND channel_id = ? AND provider_model_key = ? AND decision = ?",
				event.TaskID, event.RouteRun, event.ProjectID, event.ChannelID, event.ProviderModelKey, "increment").
			Update("decision", "reset")
	}
	if write.Error != nil || write.RowsAffected == 0 {
		return 0, false, write.Error
	}
	key := model.ProjectVideoRouteStreak{ProjectID: event.ProjectID, ChannelID: event.ChannelID, ProviderModelKey: event.ProviderModelKey}
	var streak model.ProjectVideoRouteStreak
	err := r.db.Where(&key).Take(&streak).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		streak = key
	} else if err != nil {
		return 0, false, err
	}
	if event.Decision == "reset" {
		streak.Failures = 0
	} else {
		streak.Failures++
	}
	return streak.Failures, true, r.db.Save(&streak).Error
}

func (r *Repository) SaveProjectVideoRoute(state *model.ProjectVideoRoute) error {
	return r.db.Save(state).Error
}

func (r *Repository) ValidateProjectVideoRouteRevision(projectID string, expected int64) error {
	current, err := r.ProjectVideoRoute(projectID)
	if err != nil {
		return err
	}
	if current == nil && expected == 0 || current != nil && current.Revision == expected {
		return nil
	}
	return ErrTaskStateConflict
}

// Group edits replace the binding rows after updating the group; hold shared
// locks on the user and group until the same transaction reserves the credits.
func (r *Repository) LockVideoQuoteGroup(userID string) error {
	var row struct{ ID string }
	return r.db.Model(&model.User{}).Select("users.id").
		Joins("JOIN user_groups ON user_groups.code = COALESCE(NULLIF(LOWER(TRIM(users.user_group)), ''), ?)", model.DefaultUserGroupCode).
		Where("users.id = ?", userID).Clauses(clause.Locking{Strength: "SHARE"}).Take(&row).Error
}
