package database

import (
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// MigrateSchema runs this and the v97 ledger insert in one transaction. The ledger
// is essential: replaying this data transform would renumber integrated projects twice.
func migrateOntologyWorkflowRenumber(tx *gorm.DB) error {
	for _, mapping := range [][2]string{{"pipeline-2", "pipeline-3"}, {"pipeline-1", "pipeline-2"}, {"", "pipeline-3"}} {
		if err := tx.Table("projects").Where("workflow_id = ?", mapping[0]).UpdateColumn("workflow_id", mapping[1]).Error; err != nil {
			return err
		}
	}
	if err := tx.Table("projects").Where("workflow_id IS NULL").UpdateColumn("workflow_id", drama.WorkflowOriginal).Error; err != nil {
		return err
	}
	if err := tx.Migrator().AlterColumn(&model.Project{}, "WorkflowID"); err != nil {
		return err
	}
	if !tx.Migrator().HasTable(&model.Task{}) {
		return nil
	}
	// Only tasks.input_json stores the workflow snapshot (including evaluation and
	// retry tasks). Other input_json columns store workflow step data / memory proposals.
	var tasks []struct {
		ID        string `gorm:"primaryKey"`
		InputJSON string
	}
	return tx.Table("tasks").Select("id", "input_json").Where("input_json LIKE ?", `%"assetWorkflow"%`).FindInBatches(&tasks, 100, func(_ *gorm.DB, _ int) error {
		for _, task := range tasks {
			input, changed, err := renumberTaskWorkflow(task.InputJSON)
			if err != nil {
				// Never include the input or parser's value in migration errors: tasks may contain secrets.
				return fmt.Errorf("任务 %s 的工作流快照无效，迁移已中止", task.ID)
			}
			if changed {
				if err := tx.Table("tasks").Where("id = ?", task.ID).UpdateColumn("input_json", input).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}).Error
}

func renumberTaskWorkflow(input string) (string, bool, error) {
	// RawMessage preserves encrypted config values, large integers, skill hashes,
	// contracts and layouts without converting them through float64 or current defaults.
	var doc, metadata, workflow map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &doc); err != nil {
		return "", false, err
	}
	if len(doc["metadata"]) == 0 {
		return input, false, nil
	}
	if err := json.Unmarshal(doc["metadata"], &metadata); err != nil {
		return "", false, err
	}
	if len(metadata["assetWorkflow"]) == 0 {
		return input, false, nil
	}
	if err := json.Unmarshal(metadata["assetWorkflow"], &workflow); err != nil {
		return "", false, err
	}
	if len(workflow["id"]) == 0 {
		return input, false, nil
	}
	var id string
	if err := json.Unmarshal(workflow["id"], &id); err != nil {
		return "", false, err
	}
	switch id {
	case "pipeline-2", "":
		id = drama.WorkflowOriginal
	case "pipeline-1":
		id = drama.WorkflowIntegrated
	case "pipeline-3":
		return input, false, nil
	default:
		return "", false, errors.New("invalid historical workflow")
	}
	definition, _ := drama.Workflow(id)
	workflow["id"], _ = json.Marshal(id)
	workflow["label"], _ = json.Marshal(definition.Label)
	metadata["assetWorkflow"], _ = json.Marshal(workflow)
	doc["metadata"], _ = json.Marshal(metadata)
	encoded, err := json.Marshal(doc)
	return string(encoded), true, err
}
