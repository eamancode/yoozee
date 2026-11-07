package database

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
	"yingce/backend/internal/model"
)

func TestProjectAssetWorkflowMigrationKeepsHistoricalProjectsOriginal(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE projects (id TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("INSERT INTO projects(id) VALUES ('historical')").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Transaction(migrateProjectAssetWorkflows); err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err = db.First(&project, "id = ?", "historical").Error; err != nil {
		t.Fatal(err)
	}
	if project.WorkflowID != "pipeline-2" || project.WorkflowVersion != "1" {
		t.Fatalf("historical project rerouted: %+v", project)
	}
	if err = db.Exec("UPDATE projects SET workflow_id = 'pipeline-1'").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Transaction(migrateProjectAssetWorkflows); err != nil {
		t.Fatal(err)
	}
	if err = db.First(&project, "id = ?", "historical").Error; err != nil {
		t.Fatal(err)
	}
	if project.WorkflowID != "pipeline-1" {
		t.Fatal("migration replay overwrote workflow")
	}
}
