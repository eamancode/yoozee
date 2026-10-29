package repository

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// 一章 2000 段（按小节生成的整章上限）整章替换：段、段版本、参考分批插入，不因单条 INSERT 的绑定参数超限失败
// （2000 个段版本 × 18 列超过 SQLite 的 32766 个参数）。
func TestReplaceProjectUnitShotsWithShotlistInsertsInBatches(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "shots.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Project{}, &model.Shot{}, &model.ShotRevision{}, &model.ShotAssetReference{}, &model.ShotArtifact{}, &model.ShotEvidence{}, &model.ProjectAssetCandidate{}, &model.DramaShotlist{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	now := time.Now()
	if err := db.Create(&model.Project{ID: "p1", UserID: "u1", Name: "长章"}).Error; err != nil {
		t.Fatal(err)
	}
	const n = 2000
	shots := make([]model.Shot, 0, n)
	revisions := make([]model.ShotRevision, 0, n)
	references := make([]model.ShotAssetReference, 0, n*3)
	for i := 0; i < n; i++ {
		shotID, revisionID := fmt.Sprintf("s%04d", i), fmt.Sprintf("r%04d", i)
		shots = append(shots, model.Shot{ID: shotID, ProjectID: "p1", UnitID: "u1", CurrentRevisionID: revisionID, Title: "段", Description: "段", Position: i, CreatedAt: now, UpdatedAt: now})
		revisions = append(revisions, model.ShotRevision{ID: revisionID, ShotID: shotID, Version: 1, CreatedAt: now})
		for j, role := range []string{"character", "scene", "palette"} {
			references = append(references, model.ShotAssetReference{ID: fmt.Sprintf("a%04d-%d", i, j), ShotID: shotID, AssetVersionID: fmt.Sprintf("v%d", j), Role: role, Status: "active", CreatedAt: now})
		}
	}
	shotlist := &model.DramaShotlist{ProjectID: "p1", UnitID: "u1", ShotlistJSON: "{}"}
	if err := New(db).ReplaceProjectUnitShotsWithShotlist("p1", "u1", shots, revisions, references, nil, shotlist); err != nil {
		t.Fatalf("2000 段整章替换：%v", err)
	}
	var count int64
	db.Model(&model.ShotRevision{}).Count(&count)
	if count != n {
		t.Fatalf("段版本 %d 条", count)
	}
	db.Model(&model.ShotAssetReference{}).Count(&count)
	if count != n*3 {
		t.Fatalf("参考 %d 条", count)
	}
}
