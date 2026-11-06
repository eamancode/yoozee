package database

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

func TestProjectWhiteModelMigrationPreservesExistingAndDefaultsNewOff(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	assertProjectWhiteModelMigration(t, db, "DATETIME")
}

func TestProjectWhiteModelPostgresMigration(t *testing.T) {
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
	name := fmt.Sprintf("white_model_migration_%d", time.Now().UnixNano())
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
	assertProjectWhiteModelMigration(t, db, "TIMESTAMPTZ")
}

func assertProjectWhiteModelMigration(t *testing.T, db *gorm.DB, timestampType string) {
	t.Helper()
	if err := db.Exec("CREATE TABLE projects (id TEXT PRIMARY KEY, updated_at " + timestampType + ")").Error; err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Truncate(time.Second)
	if err := db.Exec("INSERT INTO projects(id, updated_at) VALUES (?, ?)", "old", before).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(migrateProjectWhiteModelStoryboard); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO projects(id, updated_at) VALUES (?, ?)", "new", before).Error; err != nil {
		t.Fatal(err)
	}
	var old, fresh model.Project
	if err := db.First(&old, "id = ?", "old").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&fresh, "id = ?", "new").Error; err != nil {
		t.Fatal(err)
	}
	if !old.WhiteModelStoryboardEnabled || fresh.WhiteModelStoryboardEnabled || !old.UpdatedAt.Equal(before) {
		t.Fatal("legacy/default/timestamp mismatch", old, fresh)
	}
}
