package database

import (
	"testing"
	"yingce/backend/internal/model"
)

func TestPersonalDirectorStyleScopeMigration(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:director-scope?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.DramaStyleLibraryEntry{}, &model.ProjectUnit{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.DramaStyleLibraryEntry{ID: "old", ProjectType: "live-action", Name: "胶片", Source: "director", CreatedBy: "owner"}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migratePersonalDirectorStyleScope(db); err != nil {
			t.Fatal(err)
		}
	}
	var row model.DramaStyleLibraryEntry
	if err := db.First(&row, "id = ?", "old").Error; err != nil || row.ScopeOwner != "owner" {
		t.Fatalf("backfill: %+v %v", row, err)
	}
	for _, scope := range []string{"other", ""} {
		if err := db.Create(&model.DramaStyleLibraryEntry{ID: "new-" + scope, ProjectType: row.ProjectType, Name: row.Name, Source: "analysis", ScopeOwner: scope, CreatedBy: scope}).Error; err != nil {
			t.Fatalf("distinct namespace: %v", err)
		}
	}
	if err := db.Create(&model.DramaStyleLibraryEntry{ID: "duplicate", ProjectType: row.ProjectType, Name: row.Name, ScopeOwner: "owner"}).Error; err == nil {
		t.Fatal("same owner duplicate accepted")
	}
	if !db.Migrator().HasColumn(&model.ProjectUnit{}, "SpeechLedgerJSON") || !db.Migrator().HasColumn(&model.DramaStyleLibraryEntry{}, "StyleProfileJSON") {
		t.Fatal("new columns absent")
	}
}
