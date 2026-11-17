package repository

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// The stored inputs are shared by both task admission and deletion. Only read
// stable IDs here: generated text, display numbers and prompts are not references.
type storyboardTaskScope struct {
	projectID      string
	unitID         string   // Owning unit for shots, or the episode-wide storyboard target.
	unitIDs        []string // Other explicit inputs and queued episodes; these do not replace shots.
	shotIDs        []string
	controllerID   string
	workflowStepID string
}

var ErrStoryboardTaskUnitGone = errors.New("任务关联的剧集或章节已删除")

func (scope storyboardTaskScope) concernsUnit(unitID string) bool {
	return unitID != "" && (scope.unitID == unitID || slices.Contains(scope.unitIDs, unitID))
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
	if !strings.Contains(task.InputJSON, `"domainProjectId"`) && !strings.Contains(task.InputJSON, `"workflowStepId"`) && !strings.Contains(task.InputJSON, `"_projectVideoChain"`) {
		return scope, nil
	}
	type director struct{ Repair *struct{ ShotID string } }
	var input struct {
		DomainProjectID, ShotID, UnitID, WorkflowStepID string

		Metadata struct {
			Source, DomainProjectID, ShotID, UnitID, ChapterID, WorkflowStepID string

			DramaSSS, DramaOntology director
			DramaAdapt              struct{ SourceUnitIDs, ReplaceUnitIDs []string }
			DramaScenePalette       struct{ Episodes []struct{ UnitID string } }
			DramaSetup              struct{ Basis, Queue []string }
			DramaPipeline           struct{ Queue []string }
			DramaAssets             struct {
				UnitID string
				Chain  struct{ Queue []string }
			}
		}
		Chain struct{ ControllerID, ShotID, SourceShotID string } `json:"_projectVideoChain"`
	}
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return scope, err
	}
	meta := input.Metadata
	// The separate drama production API uses drama_segments.id, not shots.id.
	if meta.Source == "drama_production" || meta.Source == "drama-step-eval" {
		return scope, nil
	}
	scope.controllerID = input.Chain.ControllerID
	scope.workflowStepID = strings.TrimSpace(input.WorkflowStepID)
	if scope.workflowStepID == "" {
		scope.workflowStepID = strings.TrimSpace(meta.WorkflowStepID)
	}
	scope.projectID = strings.TrimSpace(input.DomainProjectID)
	if scope.projectID == "" {
		scope.projectID = strings.TrimSpace(meta.DomainProjectID)
	}
	if scope.projectID == "" && (scope.workflowStepID != "" || scope.controllerID != "") {
		scope.projectID = task.ProjectID
	}
	if scope.projectID == "" {
		return scope, nil
	}
	switch meta.Source {
	case "short-drama-chapter-storyboard":
		scope.unitID = strings.TrimSpace(meta.ChapterID)
		scope.unitIDs = meta.DramaPipeline.Queue
		for _, plan := range []director{meta.DramaSSS, meta.DramaOntology} {
			if plan.Repair != nil && plan.Repair.ShotID != "" {
				scope.shotIDs = append(scope.shotIDs, plan.Repair.ShotID)
			}
		}
		// An episode-wide rebuild may replace every shot; a repair writes one.
		return scope, nil
	case "short-drama-assets-extract":
		scope.unitIDs = append(meta.DramaAssets.Chain.Queue, meta.ChapterID, meta.DramaAssets.UnitID)
	case "short-drama-director-setup":
		scope.unitIDs = append(meta.DramaSetup.Basis, meta.DramaSetup.Queue...)
		scope.unitIDs = append(scope.unitIDs, meta.DramaPipeline.Queue...)
	case "short-drama-script-adapt":
		scope.unitIDs = append(meta.DramaAdapt.SourceUnitIDs, meta.DramaAdapt.ReplaceUnitIDs...)
	case "short-drama-scene-palette":
		for _, episode := range meta.DramaScenePalette.Episodes {
			scope.unitIDs = append(scope.unitIDs, episode.UnitID)
		}
	}
	shotID := strings.TrimSpace(input.ShotID)
	if shotID == "" {
		shotID = strings.TrimSpace(meta.ShotID)
	}
	if shotID == "" {
		shotID = input.Chain.ShotID
	}
	unitID := strings.TrimSpace(input.UnitID)
	if unitID == "" {
		unitID = strings.TrimSpace(meta.UnitID)
	}
	if shotID != "" {
		scope.unitID = unitID
		scope.shotIDs = append(scope.shotIDs, shotID)
	} else if unitID != "" {
		scope.unitIDs = append(scope.unitIDs, unitID)
	}
	if input.Chain.SourceShotID != "" {
		scope.shotIDs = append(scope.shotIDs, input.Chain.SourceShotID)
	}
	return scope, nil
}

// Caller holds the project's deletion/admission lock. Checking the exact IDs
// again here fences a preview or retry prepared before a concurrent deletion.
func (scope storyboardTaskScope) requireTargets(tx *gorm.DB) error {
	if scope.projectID == "" {
		return nil
	}
	if scope.workflowStepID != "" {
		if _, err := New(tx).WorkflowStepForProject(scope.projectID, scope.workflowStepID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStoryboardTaskUnitGone
			}
			return err
		}
	}
	unitIDs := append(slices.Clone(scope.unitIDs), scope.unitID)
	unitIDs = slices.DeleteFunc(unitIDs, func(id string) bool { return id == "" })
	if len(unitIDs) != 0 {
		slices.Sort(unitIDs)
		unitIDs = slices.Compact(unitIDs)
		var count int64
		if err := tx.Model(&model.ProjectUnit{}).Where("project_id = ? AND id IN ?", scope.projectID, unitIDs).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(unitIDs)) {
			return ErrStoryboardTaskUnitGone
		}
	}
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
	if err != nil || scope.projectID == "" || (len(scope.shotIDs) == 0 && scope.unitID == "" && len(scope.unitIDs) == 0 && scope.controllerID == "" && scope.workflowStepID == "") {
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
	// Older children carry only the internal controller marker. The controller's
	// lease authorizes execution, but its saved inputs can outlive the episode.
	if scope.controllerID != "" && scope.unitID == "" && len(scope.unitIDs) == 0 {
		parent, err := controllerStoryboardScope(tx, task.UserID, scope.projectID, scope.controllerID)
		if err != nil {
			return err
		}
		if parent.unitID == "" && len(parent.shotIDs) == 0 {
			return ErrStoryboardTaskUnitGone
		}
		if err := parent.requireTargets(tx); err != nil {
			return err
		}
		scope.unitID = parent.unitID
		if len(scope.shotIDs) == 0 {
			return nil
		}
	}
	return scope.requireTargets(tx)
}

func controllerStoryboardScope(tx *gorm.DB, userID, projectID, controllerID string) (storyboardTaskScope, error) {
	var parent model.Task
	if err := tx.Select("type", "project_id", "input_json").Where("id = ? AND user_id = ? AND project_id = ? AND type = ?", controllerID, userID, projectID, model.TaskTypeProjectVideoChain).Take(&parent).Error; err != nil {
		return storyboardTaskScope{}, err
	}
	return storyboardScope(&parent)
}

// ActiveTasksForStoryboardShot must run inside the project deletion transaction.
// Linked canvas tasks and direct project tasks use different task.project_id values.
func (r *Repository) ActiveTasksForStoryboardShot(userID, projectID, unitID, shotID string) ([]model.Task, error) {
	return r.activeStoryboardTasks(userID, projectID, unitID, []string{shotID}, false, "")
}

type EpisodeActiveTasksError struct {
	UnitID string
	Tasks  []model.Task
}

func (e *EpisodeActiveTasksError) Error() string { return "仍有任务使用本集" }

func (r *Repository) activeStoryboardTasks(userID, projectID, unitID string, shotIDs []string, wholeUnit bool, applyingTaskID string) ([]model.Task, error) {
	active := []model.TaskStatus{"pending", model.TaskStatusQueued, model.TaskStatusRunning, model.TaskStatusTextReplay}
	canvases := r.db.Model(&model.CanvasProject{}).Select("id").Where("project_id = ? AND user_id = ?", projectID, userID)
	links, workflowSteps, err := r.storyboardTaskLinks(projectID, unitID, shotIDs, wholeUnit)
	if err != nil {
		return nil, err
	}
	var tasks []struct {
		model.Task
		Linked bool
	}
	if err := r.db.Model(&model.Task{}).Select("tasks.id, tasks.project_id, tasks.type, tasks.status, tasks.stage, tasks.operation, tasks.input_json, links.task_id IS NOT NULL AS linked").
		Joins("LEFT JOIN (?) AS links ON links.task_id = tasks.id", links).
		Where("user_id = ? AND (project_id = ? OR project_id IN (?) OR links.task_id IS NOT NULL)", userID, projectID, canvases).
		Where("(status IN ? OR (status = ? AND lease_owner LIKE ? AND lease_expires_at > ?))", active, model.TaskStatusFailed, "manual-recovery:%", time.Now()).
		Order("tasks.id").Find(&tasks).Error; err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	targetShots := make(map[string]bool, len(shotIDs))
	for _, id := range shotIDs {
		targetShots[id] = true
	}
	matches := func(scope storyboardTaskScope) bool {
		if scope.projectID != projectID {
			return false
		}
		if wholeUnit && (scope.concernsUnit(unitID) || workflowSteps[scope.workflowStepID]) {
			return true
		}
		if !wholeUnit && len(scope.shotIDs) == 0 && scope.unitID == unitID && unitID != "" {
			return true
		}
		for _, id := range scope.shotIDs {
			if targetShots[id] {
				return true
			}
		}
		return false
	}
	var blockers []model.Task
	for _, task := range tasks {
		if task.ID == applyingTaskID {
			continue
		}
		if !task.Linked {
			scope, err := storyboardScope(&task.Task)
			if err != nil {
				return nil, err
			}
			if !matches(scope) {
				if !wholeUnit || scope.controllerID == "" {
					continue
				}
				// A terminated parent can still have a running original child.
				parentScope, err := controllerStoryboardScope(r.db, userID, projectID, scope.controllerID)
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				if err != nil {
					return nil, err
				}
				if !matches(parentScope) {
					continue
				}
			}
		}
		task.InputJSON = ""
		if task.Status == model.TaskStatusFailed {
			task.Stage = "正在查询或恢复原上游任务"
		}
		blockers = append(blockers, task.Task)
	}
	return blockers, nil
}

func (r *Repository) storyboardTaskLinks(projectID, unitID string, shotIDs []string, wholeUnit bool) (*gorm.DB, map[string]bool, error) {
	if !wholeUnit {
		links := r.db.Model(&model.ProductionTaskLink{}).Distinct("task_id").Where("project_id = ? AND shot_id IN ?", projectID, shotIDs)
		return links, nil, nil
	}
	shots := r.db.Model(&model.Shot{}).Select("id").Where("project_id = ? AND unit_id = ?", projectID, unitID)
	unitOrShot := func(row any) *gorm.DB {
		return r.db.Model(row).Select("task_id").Where("project_id = ? AND (unit_id = ? OR shot_id IN (?)) AND task_id <> ''", projectID, unitID, shots)
	}
	documents := r.db.Model(&model.ProjectOntologyDocument{}).Select("task_id").Where("project_id = ? AND unit_id = ? AND task_id <> ''", projectID, unitID)
	instances := r.db.Model(&model.WorkflowInstance{}).Select("id").Where("project_id = ? AND unit_id = ?", projectID, unitID)
	steps := r.db.Model(&model.WorkflowStepInstance{}).Where("workflow_instance_id IN (?)", instances)
	var stepIDs []string
	if err := steps.Pluck("id", &stepIDs).Error; err != nil {
		return nil, nil, err
	}
	stepSet := make(map[string]bool, len(stepIDs))
	for _, id := range stepIDs {
		stepSet[id] = true
	}
	workflowTasks := r.db.Model(&model.WorkflowStepTask{}).Select("task_id").Where("workflow_step_id IN (?)", steps.Select("id"))
	links := r.db.Raw("? UNION ? UNION ? UNION ? UNION ?", unitOrShot(&model.ProductionTaskLink{}), unitOrShot(&model.ShotArtifact{}), unitOrShot(&model.ShotEvidence{}), documents, workflowTasks)
	return links, stepSet, nil
}
