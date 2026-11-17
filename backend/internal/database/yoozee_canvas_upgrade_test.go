package database

import (
	"path/filepath"
	"reflect"
	"testing"

	"yingce/backend/internal/model"
)

func TestYoozeeCanvasUpgradePreservesExistingLedger(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "upgrade.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	// Recreate the deployed schema-100 boundary, including absence of the
	// separate official asset ledger and all three new business tables.
	for _, table := range []any{&yingceSchemaMigration{}, &model.OfficialAsset{}, &model.OfficialAssetImport{}, &model.OfficialAssetRecommendation{}} {
		if err := db.Migrator().DropTable(table); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version > 100").Error; err != nil {
		t.Fatal(err)
	}
	var original []schemaMigration
	if err := db.Order("version").Find(&original).Error; err != nil {
		t.Fatal(err)
	}
	if len(original) == 0 || original[len(original)-1].Name != "sss_workflow_renumber" {
		t.Fatal("expected Yoozee schema 100")
	}
	raw := migrationCanvas(t, "保留旧镜头")
	if err := db.Create(&model.CanvasProject{ID: "project", UserID: "actor", Revision: 5, PayloadJSON: raw}).Error; err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		if err := MigrateSchema(db); err != nil {
			t.Fatal(err)
		}
		status, err := ReadSchemaStatus(db)
		if err != nil || !status.Ready || status.YingceCurrent != 4 || status.Current != CurrentSchemaVersion {
			t.Fatalf("status %+v: %v", status, err)
		}
		var after []schemaMigration
		if err := db.Where("version <= 100").Order("version").Find(&after).Error; err != nil || !reflect.DeepEqual(after, original) {
			t.Fatalf("old migration ledger changed: %v", err)
		}
		var current model.CanvasProject
		if err := db.First(&current, "id = ?", "project").Error; err != nil || current.Revision != 6 || current.PayloadJSON == raw {
			t.Fatalf("canvas migration did not apply exactly once: revision %d, error %v", current.Revision, err)
		}
	}
	if err := db.Exec("UPDATE yingce_schema_migrations SET checksum='wrong' WHERE version=2").Error; err != nil {
		t.Fatal(err)
	}
	if err := RequireSchemaVersion(db); err == nil {
		t.Fatal("accepted an invalid official asset migration")
	}
	if err := MigrateSchema(db); err == nil {
		t.Fatal("overwrote an invalid official asset migration")
	}
}
