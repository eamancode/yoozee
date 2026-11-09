package database

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestOntologyWorkflowMigrationSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	assertOntologyWorkflowMigration(t, db)
}

func TestOntologyWorkflowMigrationPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("CANVAS_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("CANVAS_TEST_POSTGRES_DSN is not configured")
	}
	base, err := Open(Config{Driver: "postgres", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	baseSQL, _ := base.DB()
	defer baseSQL.Close()
	name := fmt.Sprintf("ontology_workflow_%d", time.Now().UnixNano())
	if err := base.Exec(`CREATE SCHEMA "` + name + `"`).Error; err != nil {
		t.Fatal(err)
	}
	defer base.Exec(`DROP SCHEMA IF EXISTS "` + name + `" CASCADE`)
	testDSN, err := postgresDSNWithSearchPath(dsn, name)
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(Config{Driver: "postgres", DSN: testDSN})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	assertOntologyWorkflowMigration(t, db)
}

func seedOntologyWorkflowMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&schemaMigration{}); err != nil {
		t.Fatal(err)
	}
	// Exercise the real migration ledger with only v97 pending, against v96 columns.
	for _, item := range schemaMigrations {
		if item.version >= 97 {
			continue
		}
		if err := db.Create(&schemaMigration{Version: item.version, Name: item.name, Checksum: item.checksum, AppliedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, sql := range []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, workflow_id VARCHAR(32) DEFAULT 'pipeline-2', workflow_version VARCHAR(24) NOT NULL DEFAULT '1', updated_at TEXT)`,
		`CREATE TABLE tasks (id TEXT PRIMARY KEY, input_json TEXT, result_json TEXT, status TEXT, updated_at TEXT)`,
		`INSERT INTO projects(id,workflow_id,updated_at) VALUES ('integrated','pipeline-1','unchanged'), ('original','pipeline-2','unchanged'), ('empty','','unchanged'), ('null',NULL,'unchanged')`,
	} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func assertOntologyWorkflowMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	seedOntologyWorkflowMigration(t, db)
	for i := 0; i < 105; i++ {
		id := []string{"pipeline-1", "pipeline-2", ""}[i%3]
		input := fmt.Sprintf(`{"config":{"workflowId":"pipeline-1","counter":9007199254740993,"apiKey":{"encrypted":"opaque"}},"metadata":{"assetWorkflow":{"id":%q,"label":"old","version":"1","assetsContract":"frozen-contract","characterLayout":"frozen-layout"},"dramaStep":{"step":"chapter.shots","contract":"chapter.shots@3","versionId":"frozen-version"},"dramaRun":{"resumeTaskId":"checkpoint"}},"prompt":"pipeline-1 stays literal"}`, id)
		if err := db.Exec("INSERT INTO tasks(id,input_json,result_json,status,updated_at) VALUES (?,?,?,?,?)", fmt.Sprintf("task-%03d", i), input, `{"text":"checkpoint output"}`, []string{"failed", "queued", "succeeded"}[i%3], "unchanged").Error; err != nil {
			t.Fatal(err)
		}
	}
	unrelated := `{"config":{"workflowId":"pipeline-1"},"metadata":{"other":true}}`
	if err := db.Exec("INSERT INTO tasks(id,input_json) VALUES (?,?)", "unrelated", unrelated).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	var projects []struct{ ID, WorkflowID, WorkflowVersion, UpdatedAt string }
	if err := db.Table("projects").Order("id").Find(&projects).Error; err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		want := "pipeline-3"
		if p.ID == "integrated" {
			want = "pipeline-2"
		}
		if p.WorkflowID != want || p.WorkflowVersion != "1" || p.UpdatedAt != "unchanged" {
			t.Fatalf("project migration: %+v", p)
		}
	}
	var tasks []struct{ ID, InputJSON, ResultJSON, Status, UpdatedAt string }
	if err := db.Table("tasks").Order("id").Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	for i, task := range tasks[:105] {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(task.InputJSON), &doc); err != nil {
			t.Fatal(err)
		}
		var metadata map[string]json.RawMessage
		_ = json.Unmarshal(doc["metadata"], &metadata)
		var workflow map[string]string
		_ = json.Unmarshal(metadata["assetWorkflow"], &workflow)
		want, label := "pipeline-3", "流水线三 原流程"
		if i%3 == 0 {
			want, label = "pipeline-2", "流水线二 资产一体化"
		}
		if workflow["id"] != want || workflow["label"] != label || workflow["version"] != "1" || workflow["assetsContract"] != "frozen-contract" || workflow["characterLayout"] != "frozen-layout" {
			t.Fatalf("snapshot changed: %v", workflow)
		}
		if string(doc["config"]) != `{"workflowId":"pipeline-1","counter":9007199254740993,"apiKey":{"encrypted":"opaque"}}` || !strings.Contains(string(metadata["dramaStep"]), "frozen-version") || !strings.Contains(string(metadata["dramaRun"]), "checkpoint") || string(doc["prompt"]) != `"pipeline-1 stays literal"` || task.ResultJSON != `{"text":"checkpoint output"}` || task.UpdatedAt != "unchanged" || task.Status != []string{"failed", "queued", "succeeded"}[i%3] {
			t.Fatal("task payload/checkpoint/status/timestamp changed", task.ID)
		}
	}
	if tasks[105].InputJSON != unrelated {
		t.Fatal("unrelated upstream workflow changed")
	}
	if err := db.Exec("INSERT INTO projects(id) VALUES ('database-default')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO projects(id,workflow_id) VALUES ('ontology','pipeline-1')").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	var replay []struct{ ID, WorkflowID string }
	if err := db.Table("projects").Order("id").Find(&replay).Error; err != nil {
		t.Fatal(err)
	}
	for _, p := range replay {
		want := "pipeline-3"
		if p.ID == "ontology" {
			want = "pipeline-1"
		}
		// SQLite 不重建 projects（会丢索引和唯一约束），旧库保留 pipeline-2 列默认；建项总是显式写 workflow_id。
		// 迁移后的裸 INSERT 拿到列默认值，重复启动也不能再把它顺延。
		if p.ID == "database-default" && db.Dialector.Name() == "sqlite" {
			want = "pipeline-2" // PostgreSQL 原地改了列默认，仍是 pipeline-3
		}
		if p.ID == "integrated" {
			want = "pipeline-2"
		}
		if p.WorkflowID != want {
			t.Fatalf("replay renumbered %s to %s", p.ID, p.WorkflowID)
		}
	}
	var after []struct{ ID, InputJSON, ResultJSON, Status, UpdatedAt string }
	if err := db.Table("tasks").Order("id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	for i := range tasks {
		if tasks[i] != after[i] {
			t.Fatal("replay changed task", tasks[i].ID)
		}
	}
	var count int64
	if err := db.Model(&schemaMigration{}).Where("version = 97").Count(&count).Error; err != nil || count != 1 {
		t.Fatal("migration ledger", count, err)
	}
}

func TestOntologyWorkflowMigrationRollsBackOnInvalidTask(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	seedOntologyWorkflowMigration(t, db)
	if err := db.Exec("INSERT INTO tasks(id,input_json) VALUES (?,?)", "broken", `{"metadata":{"assetWorkflow": "do-not-log-this-secret"`).Error; err != nil {
		t.Fatal(err)
	}
	err = MigrateSchema(db)
	if err == nil || strings.Contains(err.Error(), "do-not-log-this-secret") {
		t.Fatal("expected sanitized failure", err)
	}
	var id string
	if err := db.Table("projects").Where("id = 'integrated'").Select("workflow_id").Scan(&id).Error; err != nil || id != "pipeline-1" {
		t.Fatal("project partially migrated", id, err)
	}
	var count int64
	if err := db.Model(&schemaMigration{}).Where("version = 97").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("failed migration recorded", count, err)
	}
	if err := db.Exec("DELETE FROM tasks WHERE id = 'broken'").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
}
