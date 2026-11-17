package repository

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// The stored inputs are shared by both task admission and deletion. Only read
// stable IDs here: generated text, display numbers and prompts are not references.
type storyboardTaskScope struct {
	projectID string
	unitID    string
	shotIDs   []string
}

func storyboardScope(task *model.Task) (storyboardTaskScope, error) {
	var scope storyboardTaskScope
	if task.Type == model.TaskTypeProjectVideoChain {
		var state struct {
			UnitID string
			Steps  []struct{ ShotID, SourceShotID string }
		}
		if err := json.Unmarshal([]byte(task.InputJSON), &state); err != nil {
			return scope, err
		}
		scope.projectID, scope.unitID = task.ProjectID, state.UnitID
		for _, step := range state.Steps {
			if step.ShotID != "" {
				scope.shotIDs = append(scope.shotIDs, step.ShotID)
			}
			if step.SourceShotID != "" {
				scope.shotIDs = append(scope.shotIDs, step.SourceShotID)
			}
		}
		return scope, nil
	}
	if !strings.Contains(task.InputJSON, `"shotId"`) && !strings.Contains(task.InputJSON, `"short-drama-chapter-storyboard"`) {
		return scope, nil
	}
	type director struct{ Repair *struct{ ShotID string } }
	var input struct {
		DomainProjectID, ShotID, UnitID, WorkflowStepID string

		Metadata struct {
			Source, DomainProjectID, ShotID, UnitID, ChapterID, WorkflowStepID string

			DramaSSS, DramaOntology director
		}
		Chain struct{ SourceShotID string } `json:"_projectVideoChain"`
	}
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return scope, err
	}
	meta := input.Metadata
	// The separate drama production API uses drama_segments.id, not shots.id.
	if meta.Source == "drama_production" {
		return scope, nil
	}
	scope.projectID = strings.TrimSpace(input.DomainProjectID)
	if scope.projectID == "" {
		scope.projectID = strings.TrimSpace(meta.DomainProjectID)
	}
	if scope.projectID == "" && (input.WorkflowStepID != "" || meta.WorkflowStepID != "") {
		scope.projectID = task.ProjectID
	}
	if scope.projectID == "" {
		return scope, nil
	}
	if meta.Source == "short-drama-chapter-storyboard" {
		scope.unitID = strings.TrimSpace(meta.ChapterID)
		for _, plan := range []director{meta.DramaSSS, meta.DramaOntology} {
			if plan.Repair != nil && plan.Repair.ShotID != "" {
				scope.shotIDs = append(scope.shotIDs, plan.Repair.ShotID)
			}
		}
		// An episode-wide rebuild may replace every shot; a repair writes one.
		return scope, nil
	}
	shotID := strings.TrimSpace(input.ShotID)
	if shotID == "" {
		shotID = strings.TrimSpace(meta.ShotID)
	}
	if shotID == "" {
		return storyboardTaskScope{}, nil
	}
	scope.unitID = strings.TrimSpace(input.UnitID)
	if scope.unitID == "" {
		scope.unitID = strings.TrimSpace(meta.UnitID)
	}
	scope.shotIDs = append(scope.shotIDs, shotID)
	if input.Chain.SourceShotID != "" {
		scope.shotIDs = append(scope.shotIDs, input.Chain.SourceShotID)
	}
	return scope, nil
}

// Caller holds the project's deletion/admission lock. Checking the exact IDs
// again here fences a preview or retry prepared before a concurrent deletion.
func (scope storyboardTaskScope) requireShots(tx *gorm.DB) error {
	if len(scope.shotIDs) == 0 {
		return nil
	}
	slices.Sort(scope.shotIDs)
	ids := slices.Compact(scope.shotIDs)
	query := tx.Model(&model.Shot{}).Where("project_id = ? AND id IN ?", scope.projectID, ids)
	if scope.unitID != "" {
		query = query.Where("unit_id = ?", scope.unitID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return ErrProjectUnitShotsChanged
	}
	return nil
}

func lockStoryboardTaskProject(tx *gorm.DB, task *model.Task) (storyboardTaskScope, error) {
	scope, err := storyboardScope(task)
	if err != nil || scope.projectID == "" || (len(scope.shotIDs) == 0 && scope.unitID == "") {
		return scope, err
	}
	repo := New(tx)
	if _, err := repo.LockActiveStoryboardProject(task.UserID, scope.projectID); err != nil {
		return scope, err
	}
	if task.ProjectID != scope.projectID {
		var canvas model.CanvasProject
		if err := tx.Select("id").Where("id = ? AND project_id = ? AND user_id = ?", task.ProjectID, scope.projectID, task.UserID).First(&canvas).Error; err != nil {
			return scope, err
		}
	}
	return scope, nil
}

func requireStoryboardTaskTarget(tx *gorm.DB, task *model.Task) error {
	scope, err := lockStoryboardTaskProject(tx, task)
	if err != nil {
		return err
	}
	return scope.requireShots(tx)
}

// ActiveTasksForStoryboardShot must run inside the project deletion transaction.
// Linked canvas tasks and direct project tasks use different task.project_id values.
func (r *Repository) ActiveTasksForStoryboardShot(userID, projectID, unitID, shotID string) ([]model.Task, error) {
	active := []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning, model.TaskStatusTextReplay}
	canvases := r.db.Model(&model.CanvasProject{}).Select("id").Where("project_id = ? AND user_id = ?", projectID, userID)
	var tasks []model.Task
	if err := r.db.Select("id", "project_id", "type", "status", "stage", "input_json").
		Where("user_id = ? AND (project_id = ? OR project_id IN (?))", userID, projectID, canvases).
		Where("(status IN ? OR (status = ? AND lease_owner LIKE ? AND lease_expires_at > ?))", active, model.TaskStatusFailed, "manual-recovery:%", time.Now()).
		Order("id").Find(&tasks).Error; err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	var linked []string
	if err := r.db.Model(&model.ProductionTaskLink{}).Where("project_id = ? AND shot_id = ?", projectID, shotID).Pluck("task_id", &linked).Error; err != nil {
		return nil, err
	}
	var blockers []model.Task
	for _, task := range tasks {
		scope, err := storyboardScope(&task)
		if err != nil {
			return nil, err
		}
		wholeEpisode := len(scope.shotIDs) == 0 && scope.unitID == unitID && unitID != ""
		if slices.Contains(linked, task.ID) || (scope.projectID == projectID && (wholeEpisode || slices.Contains(scope.shotIDs, shotID))) {
			task.InputJSON = ""
			if task.Status == model.TaskStatusFailed {
				task.Stage = "正在查询或恢复原上游任务"
			}
			blockers = append(blockers, task)
		}
	}
	return blockers, nil
}
