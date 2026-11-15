package database

import (
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// v100 makes room for the two SSS pipelines at the front: 本体论 1→3, 资产一体化 2→4, 原流程 3→5 (empty / NULL → 5).
// Order matters (highest first) so no row is renumbered twice in one pass; the ledger keeps replays from running it again.
// IDs and labels are frozen as of v100; never read them from drama.Workflow.
var sssWorkflowRenumber = [][2]string{{"pipeline-3", "pipeline-5"}, {"pipeline-2", "pipeline-4"}, {"pipeline-1", "pipeline-3"}, {"", "pipeline-5"}}

var sssWorkflowLabels = map[string]string{"pipeline-3": "流水线三 本体论导演", "pipeline-4": "流水线四 资产一体化", "pipeline-5": "流水线五 原流程"}

func migrateSSSWorkflowRenumber(tx *gorm.DB) error {
	for _, mapping := range sssWorkflowRenumber {
		if err := tx.Table("projects").Where("workflow_id = ?", mapping[0]).UpdateColumn("workflow_id", mapping[1]).Error; err != nil {
			return err
		}
	}
	if err := tx.Table("projects").Where("workflow_id IS NULL").UpdateColumn("workflow_id", "pipeline-5").Error; err != nil {
		return err
	}
	// 同 v97：只在 PostgreSQL 上原地改列默认；SQLite 改默认要重建整张表，旧库保留旧默认，建项总是显式写 workflow_id。
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec("ALTER TABLE projects ALTER COLUMN workflow_id SET DEFAULT 'pipeline-5'").Error; err != nil {
			return err
		}
	}
	if !tx.Migrator().HasTable("tasks") {
		return nil
	}
	var tasks []struct {
		ID        string `gorm:"primaryKey"`
		InputJSON string
	}
	return tx.Table("tasks").Select("id", "input_json").Where("input_json LIKE ?", `%"assetWorkflow"%`).FindInBatches(&tasks, 100, func(_ *gorm.DB, _ int) error {
		for _, task := range tasks {
			input, changed, err := renumberSSSTaskWorkflow(task.InputJSON)
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

// renumberSSSTaskWorkflow rewrites metadata.assetWorkflow.id/label only; every other byte-relevant value is kept as RawMessage.
func renumberSSSTaskWorkflow(input string) (string, bool, error) {
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
	next := ""
	for _, mapping := range sssWorkflowRenumber {
		if id == mapping[0] {
			next = mapping[1]
			break
		}
	}
	if next == "" {
		return "", false, errors.New("invalid historical workflow")
	}
	workflow["id"], _ = json.Marshal(next)
	workflow["label"], _ = json.Marshal(sssWorkflowLabels[next])
	metadata["assetWorkflow"], _ = json.Marshal(workflow)
	doc["metadata"], _ = json.Marshal(metadata)
	encoded, err := json.Marshal(doc)
	return string(encoded), true, err
}
