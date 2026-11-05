package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

var ErrVideoChainStopped = errors.New("连续生成链已停止，未提交后续镜头")

func (r *Repository) VideoChainChildTasks(userID string, ids []string) ([]model.Task, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var tasks []model.Task
	err := r.db.Select("id", "status", "result_json", "error", "type").Where("user_id = ? AND id IN ?", userID, ids).Find(&tasks).Error
	return tasks, err
}

func requireVideoChainParent(tx *gorm.DB, task *model.Task) error {
	if task.ParentControllerID == "" {
		return nil
	}
	var parent model.Task
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND project_id = ? AND type = ?", task.ParentControllerID, task.UserID, task.ProjectID, model.TaskTypeProjectVideoChain).First(&parent).Error; err != nil {
		return err
	}
	if parent.Status != model.TaskStatusRunning {
		return ErrVideoChainStopped
	}
	if task.ParentControllerLease == "" || parent.LeaseOwner != task.ParentControllerLease || parent.LeaseExpiresAt == nil || !parent.LeaseExpiresAt.After(time.Now()) {
		return ErrTaskStateConflict
	}
	return nil
}

func (r *Repository) ProjectVideoChains(userID, projectID, operation string) ([]model.Task, error) {
	var tasks []model.Task
	query := r.db.Where("user_id = ? AND project_id = ? AND type = ?", userID, projectID, model.TaskTypeProjectVideoChain)
	if operation != "" {
		query = query.Where("operation = ?", operation)
	}
	err := query.Order("created_at desc").Limit(100).Find(&tasks).Error
	return tasks, err
}

// The project lock serializes controller creation. Child admission and cancellation
// share the parent-task lock; its lease fences stale controllers before charging.
func (r *Repository) CreateProjectVideoChain(task *model.Task, limit int) (*model.Task, error) {
	var existing model.Task
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var project model.Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", task.ProjectID, task.UserID).First(&project).Error; err != nil {
			return err
		}
		err := tx.Where("user_id = ? AND project_id = ? AND type = ? AND id = ?", task.UserID, task.ProjectID, model.TaskTypeProjectVideoChain, task.ID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = tx.Where("user_id = ? AND project_id = ? AND type = ? AND operation = ? AND status IN ?", task.UserID, task.ProjectID, model.TaskTypeProjectVideoChain, task.Operation, []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning}).First(&existing).Error
		}
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var count int64
		if err := tx.Model(&model.Task{}).Where("user_id = ? AND type = ? AND status IN ?", task.UserID, model.TaskTypeProjectVideoChain, []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning}).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(limit) {
			return ErrActiveTaskLimit
		}
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		existing = *task
		return nil
	})
	return &existing, err
}

func (r *Repository) SaveProjectVideoChain(task *model.Task, input string, status model.TaskStatus, stage, reason string, progress int) error {
	now := time.Now()
	updates := map[string]any{"input_json": input, "status": status, "stage": stage, "error": reason, "progress": progress, "updated_at": now, "lease_owner": "", "lease_expires_at": nil}
	if status == model.TaskStatusRunning {
		updates["next_poll_at"] = now.Add(2 * time.Second)
	} else {
		updates["next_poll_at"] = nil
		updates["completed_at"] = now
	}
	result := taskLeaseWriter(r.db.Model(&model.Task{}), task.LeaseOwner).Where("id = ? AND user_id = ? AND type = ? AND status = ?", task.ID, task.UserID, model.TaskTypeProjectVideoChain, model.TaskStatusRunning).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTaskStateConflict
	}
	return nil
}

func (r *Repository) CheckpointProjectVideoChain(task *model.Task, input string) error {
	result := taskLeaseWriter(r.db.Model(&model.Task{}), task.LeaseOwner).Where("id = ? AND user_id = ? AND type = ? AND status = ?", task.ID, task.UserID, model.TaskTypeProjectVideoChain, model.TaskStatusRunning).Updates(map[string]any{"input_json": input, "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTaskStateConflict
	}
	return nil
}

func (r *Repository) ResumeProjectVideoChain(userID, id, expectedInput, nextInput string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var task model.Task
		if err := tx.Where("id = ? AND user_id = ? AND type = ?", id, userID, model.TaskTypeProjectVideoChain).First(&task).Error; err != nil {
			return err
		}
		var project model.Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", task.ProjectID, userID).First(&project).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.Task{}).Where("id <> ? AND user_id = ? AND project_id = ? AND type = ? AND operation = ? AND status IN ?", id, userID, task.ProjectID, model.TaskTypeProjectVideoChain, task.Operation, []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrTaskStateConflict
		}
		result := tx.Model(&model.Task{}).Where("id = ? AND user_id = ? AND status = ? AND input_json = ?", id, userID, model.TaskStatusFailed, expectedInput).Updates(map[string]any{"input_json": nextInput, "status": model.TaskStatusQueued, "stage": "等待继续连续生成", "error": "", "next_poll_at": nil, "completed_at": nil, "lease_owner": "", "lease_expires_at": nil, "updated_at": time.Now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrTaskStateConflict
		}
		return nil
	})
}

func (r *Repository) CancelProjectVideoChain(userID, id string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var task model.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND type = ?", id, userID, model.TaskTypeProjectVideoChain).First(&task).Error; err != nil {
			return err
		}
		if task.Status == model.TaskStatusSucceeded || task.Status == model.TaskStatusCancelled {
			return nil
		}
		return tx.Model(&task).Updates(map[string]any{"status": model.TaskStatusCancelled, "stage": "已停止后续镜头", "completed_at": time.Now(), "next_poll_at": nil, "updated_at": time.Now()}).Error
	})
}
