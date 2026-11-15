package database

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// seedSSSWorkflowMigration builds a v99 database (every migration but v100 recorded) with the
// three post-v97 workflows, an empty value and a NULL.
func seedSSSWorkflowMigration(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&schemaMigration{}); err != nil {
		t.Fatal(err)
	}
	for _, item := range schemaMigrations {
		if item.version == 100 {
			continue
		}
		if err := db.Create(&schemaMigration{Version: item.version, Name: item.name, Checksum: item.checksum, AppliedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, sql := range []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, workflow_id VARCHAR(32) DEFAULT 'pipeline-3', workflow_version VARCHAR(24) NOT NULL DEFAULT '1', updated_at TEXT)`,
		`CREATE TABLE tasks (id TEXT PRIMARY KEY, input_json TEXT, result_json TEXT, status TEXT, updated_at TEXT)`,
		`INSERT INTO projects(id,workflow_id,updated_at) VALUES ('ontology','pipeline-1','unchanged'), ('integrated','pipeline-2','unchanged'), ('original','pipeline-3','unchanged'), ('empty','','unchanged'), ('null',NULL,'unchanged')`,
	} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestSSSWorkflowMigrationRenumbersProjectsAndTasks(t *testing.T) {
	db := seedSSSWorkflowMigration(t)
	ids := []string{"pipeline-1", "pipeline-2", "pipeline-3", ""}
	for i := 0; i < 120; i++ {
		input := fmt.Sprintf(`{"config":{"workflowId":"pipeline-1","counter":9007199254740993,"apiKey":{"encrypted":"opaque"}},"metadata":{"assetWorkflow":{"id":%q,"label":"old","version":"1","assetsContract":"frozen-contract"},"dramaStep":{"versionId":"frozen-version"}},"prompt":"pipeline-2 stays literal"}`, ids[i%4])
		if err := db.Exec("INSERT INTO tasks(id,input_json,result_json,status,updated_at) VALUES (?,?,?,?,?)", fmt.Sprintf("task-%03d", i), input, `{"text":"checkpoint"}`, "failed", "unchanged").Error; err != nil {
			t.Fatal(err)
		}
	}
	unrelated := `{"config":{"workflowId":"pipeline-1"},"metadata":{"other":true}}`
	if err := db.Exec("INSERT INTO tasks(id,input_json) VALUES ('unrelated',?)", unrelated).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ontology": "pipeline-3", "integrated": "pipeline-4", "original": "pipeline-5", "empty": "pipeline-5", "null": "pipeline-5"}
	assertProjects := func(extra map[string]string) {
		t.Helper()
		var projects []struct{ ID, WorkflowID, UpdatedAt string }
		if err := db.Table("projects").Order("id").Find(&projects).Error; err != nil {
			t.Fatal(err)
		}
		for _, p := range projects {
			w, ok := want[p.ID]
			if e, has := extra[p.ID]; has {
				w, ok = e, true
			}
			if !ok || p.WorkflowID != w {
				t.Fatalf("project %s = %s, want %s", p.ID, p.WorkflowID, w)
			}
		}
	}
	assertProjects(nil)
	labels := map[string]string{"pipeline-3": "流水线三 本体论导演", "pipeline-4": "流水线四 资产一体化", "pipeline-5": "流水线五 原流程"}
	next := map[string]string{"pipeline-1": "pipeline-3", "pipeline-2": "pipeline-4", "pipeline-3": "pipeline-5", "": "pipeline-5"}
	var tasks []struct{ ID, InputJSON, ResultJSON, UpdatedAt string }
	if err := db.Table("tasks").Order("id").Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	for i, task := range tasks[:120] {
		var doc, metadata map[string]json.RawMessage
		var workflow map[string]string
		if err := json.Unmarshal([]byte(task.InputJSON), &doc); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(doc["metadata"], &metadata)
		_ = json.Unmarshal(metadata["assetWorkflow"], &workflow)
		id := next[ids[i%4]]
		if workflow["id"] != id || workflow["label"] != labels[id] || workflow["assetsContract"] != "frozen-contract" || workflow["version"] != "1" {
			t.Fatalf("task %s workflow = %v", task.ID, workflow)
		}
		if string(doc["config"]) != `{"workflowId":"pipeline-1","counter":9007199254740993,"apiKey":{"encrypted":"opaque"}}` || string(doc["prompt"]) != `"pipeline-2 stays literal"` || !strings.Contains(string(metadata["dramaStep"]), "frozen-version") || task.ResultJSON != `{"text":"checkpoint"}` || task.UpdatedAt != "unchanged" {
			t.Fatal("task payload changed", task.ID)
		}
	}
	if tasks[120].InputJSON != unrelated {
		t.Fatal("unrelated task changed")
	}
	// Replay: the ledger stops a second renumbering; new rows written after v100 keep their IDs.
	if err := db.Exec("INSERT INTO projects(id,workflow_id) VALUES ('sss-film','pipeline-1'), ('sss-drama','pipeline-2')").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	assertProjects(map[string]string{"sss-film": "pipeline-1", "sss-drama": "pipeline-2"})
	var after []struct{ ID, InputJSON, ResultJSON, UpdatedAt string }
	if err := db.Table("tasks").Order("id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	for i := range tasks {
		if tasks[i] != after[i] {
			t.Fatal("replay changed task", tasks[i].ID)
		}
	}
	var count int64
	if err := db.Model(&schemaMigration{}).Where("version = 100").Count(&count).Error; err != nil || count != 1 {
		t.Fatal("migration ledger", count, err)
	}
}

func TestSSSWorkflowMigrationRollsBackOnInvalidTask(t *testing.T) {
	db := seedSSSWorkflowMigration(t)
	if err := db.Exec("INSERT INTO tasks(id,input_json) VALUES (?,?)", "broken", `{"metadata":{"assetWorkflow":{"id":"pipeline-9","secret":"do-not-log-this-secret"}}}`).Error; err != nil {
		t.Fatal(err)
	}
	err := MigrateSchema(db)
	if err == nil || strings.Contains(err.Error(), "do-not-log-this-secret") {
		t.Fatal("expected sanitized failure", err)
	}
	var id string
	if err := db.Table("projects").Where("id = 'ontology'").Select("workflow_id").Scan(&id).Error; err != nil || id != "pipeline-1" {
		t.Fatal("project partially migrated", id, err)
	}
	var count int64
	if err := db.Model(&schemaMigration{}).Where("version = 100").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("failed migration recorded", count, err)
	}
}
