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
