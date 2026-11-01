package database

import (
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

type projectBeforeRedesign struct {
	model.Project
	AuthMode string
}

func (projectBeforeRedesign) TableName() string { return "projects" }

func TestProjectCreateUpgradePreservesIndexes(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:project-create-upgrade?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&projectBeforeRedesign{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO projects (id,user_id,name,auth_mode) VALUES ('old','u','old','full')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(migrateProjectCreateRedesign); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"idx_projects_user_name", "idx_projects_user_id", "idx_projects_status", "idx_projects_type", "idx_projects_updated_at", "idx_projects_cover_resource_id"} {
		if !db.Migrator().HasIndex(&model.Project{}, index) {
			t.Fatalf("missing index %s", index)
		}
	}
	if db.Migrator().HasColumn(&model.Project{}, "auth_mode") {
		t.Fatal("auth_mode still exists")
	}
	var old model.Project
	if err := db.First(&old, "id = ?", "old").Error; err != nil {
		t.Fatal(err)
	}
	if old.RunMode != "until_video" {
		t.Fatalf("run mode = %q", old.RunMode)
	}
	create := func(id string) *gorm.DB {
		return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "name"}}, DoNothing: true}).Create(&model.Project{ID: id, UserID: "u", Name: "new"})
	}
	if result := create("new-1"); result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("create: %v, %d", result.Error, result.RowsAffected)
	}
	if result := create("new-2"); result.Error != nil || result.RowsAffected != 0 {
		t.Fatalf("duplicate: %v, %d", result.Error, result.RowsAffected)
	}
}
