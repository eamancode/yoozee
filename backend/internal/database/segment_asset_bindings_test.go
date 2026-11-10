package database

import (
	"encoding/json"
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestSegmentBindingMigrationSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	assertSegmentBindingMigration(t, db)
}

func TestSegmentBindingMigrationPostgres(t *testing.T) {
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
	name := fmt.Sprintf("segment_binding_%d", time.Now().UnixNano())
	if err := base.Exec(`CREATE SCHEMA "` + name + `"`).Error; err != nil {
		t.Fatal(err)
	}
	defer base.Exec(`DROP SCHEMA IF EXISTS "` + name + `" CASCADE`)
	scoped, err := postgresDSNWithSearchPath(dsn, name)
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(Config{Driver: "postgres", DSN: scoped})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	assertSegmentBindingMigration(t, db)
}

func assertSegmentBindingMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&model.Project{}, &model.Shot{}, &model.ShotRevision{}, &model.Asset{}, &model.AssetVersion{}, &model.ProjectAssetLink{}, &model.ShotAssetReference{}, &model.Task{}, &model.ShotArtifact{}); err != nil {
		t.Fatal(err)
	}
	p := model.Project{ID: "binding-project", UserID: "owner"}
	shot := model.Shot{ID: "binding-shot", ProjectID: p.ID, CurrentRevisionID: "binding-revision"}
	seg := drama.Segment{ID: "S01", Refs: []drama.Ref{{File: "fixed-v1", Role: "identity"}, {File: "child", Role: "identity"}, {File: "foreign-v1", Role: "prop"}}}
	raw, _ := json.Marshal(seg)
	rev := model.ShotRevision{ID: shot.CurrentRevisionID, ShotID: shot.ID, Version: 1, SpecJSON: string(raw), CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	for _, row := range []any{&p, &shot, &rev,
		&model.Asset{ID: "main", UserID: p.UserID, PrimaryVersionID: "fixed-v2"}, &model.Asset{ID: "child", UserID: p.UserID, PrimaryVersionID: "child-v1", ParentAssetID: "main"}, &model.Asset{ID: "foreign", UserID: "another-owner", PrimaryVersionID: "foreign-v1"},
		&model.AssetVersion{ID: "fixed-v1", AssetID: "main", Version: 1}, &model.AssetVersion{ID: "fixed-v2", AssetID: "main", Version: 2}, &model.AssetVersion{ID: "child-v1", AssetID: "child", Version: 1}, &model.AssetVersion{ID: "foreign-v1", AssetID: "foreign", Version: 1},
		&model.ProjectAssetLink{ID: "link-main", ProjectID: p.ID, AssetID: "main"}, &model.ProjectAssetLink{ID: "link-child", ProjectID: p.ID, AssetID: "child"}, &model.ProjectAssetLink{ID: "link-foreign", ProjectID: p.ID, AssetID: "foreign"},
		&model.ShotAssetReference{ID: "keep-manual", ShotID: shot.ID, AssetVersionID: "fixed-v2", Role: "reference", Status: "linked"},
		&model.ShotAssetReference{ID: "keep-style", ShotID: shot.ID, AssetVersionID: "fixed-v2", Role: "style", Status: "linked"},
		&model.Task{ID: "historic-failure", Status: model.TaskStatusFailed, ResultJSON: `{"error":"kept"}`},
		&model.ShotArtifact{ID: "keep-artifact", ShotID: shot.ID, ProjectID: p.ID, Type: "video", Version: 1, Status: "ready", Selected: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	var before model.ShotRevision
	if err := db.First(&before, "id = ?", rev.ID).Error; err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		if err := db.Transaction(migrateSegmentAssetBindings); err != nil {
			t.Fatal(err)
		}
	}
	var refs []model.ShotAssetReference
	if err := db.Where("shot_id = ?", shot.ID).Find(&refs).Error; err != nil {
		t.Fatal(err)
	}
	if len(refs) != 4 {
		t.Fatalf("backfill not idempotent or overwrote manual refs: %+v", refs)
	}
	for _, r := range refs {
		if r.Source == "segment" && r.AssetVersionID == "fixed-v2" {
			t.Fatal("fixed historical version followed primary")
		}
		if r.AssetVersionID == "foreign-v1" {
			t.Fatal("cross-owner reference materialized")
		}
	}
	var kept model.ShotRevision
	db.First(&kept, "id = ?", rev.ID)
	if !reflect.DeepEqual(kept, before) {
		t.Fatal("backfill rewrote revision")
	}
	var artifact model.ShotArtifact
	db.First(&artifact, "id = ?", "keep-artifact")
	if artifact.Status != "ready" || !artifact.Selected {
		t.Fatal("backfill invalidated artifact")
	}
	var task model.Task
	db.First(&task, "id = ?", "historic-failure")
	if task.Status != model.TaskStatusFailed || task.ResultJSON != `{"error":"kept"}` {
		t.Fatal("backfill changed historic failure")
	}
}
