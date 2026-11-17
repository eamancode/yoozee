package repository

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// These writes can be prepared while their episode exists and arrive after the
// DELETE commits, including when there were no shots to lock during deletion.
func TestMissingStoryboardUnitRejectsPreparedWrites(t *testing.T) {
	repo := storyboardTaskTestRepo(t)
	if err := repo.db.AutoMigrate(&model.ProjectUnit{}, &model.ProjectOntologyDocument{}, &model.DramaShotlist{}, &model.ShotRevision{}, &model.ShotEvidence{}); err != nil {
		t.Fatal(err)
	}
	const missing = "removed"
	shot := model.Shot{ID: "late", ProjectID: "project", UnitID: missing}
	shotlist := model.DramaShotlist{ProjectID: "project", UnitID: missing}
	doc := model.ProjectOntologyDocument{ID: "late-doc", ProjectID: "project", UnitID: missing, Kind: model.OntologyDocEpisode, Content: "prepared"}
	for _, check := range []struct {
		name  string
		write func() error
	}{
		{"shot", func() error {
			return repo.SaveShotWithRevision(&shot, &model.ShotRevision{ID: "late-rev", ShotID: shot.ID}, true)
		}},
		{"draft", func() error { return repo.SaveDramaShotlist(&shotlist) }},
		{"repack", func() error {
			return repo.ReplaceProjectUnitShotsWithShotlist("project", missing, []model.Shot{shot}, nil, nil, nil, &shotlist)
		}},
		{"incremental repack", func() error {
			return repo.ReplaceProjectUnitShotsKeeping("project", missing, []UnitShotWrite{{Shot: shot}}, nil, &shotlist, time.Now())
		}},
		{"director", func() error { return repo.AppendOntologyDocument(&doc) }},
		{"episode evidence", func() error {
			return repo.CreateShotEvidence(&model.ShotEvidence{ID: "late-evidence", ProjectID: "project", UnitID: missing})
		}},
	} {
		t.Run(check.name, func(t *testing.T) {
			if err := check.write(); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("write to removed episode: %v", err)
			}
		})
	}
	for _, table := range []any{&model.Shot{}, &model.DramaShotlist{}, &model.ProjectOntologyDocument{}, &model.ShotEvidence{}} {
		var count int64
		if err := repo.db.Model(table).Where("unit_id = ?", missing).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%T after stale writes: count=%d err=%v", table, count, err)
		}
	}
	// A project-level director document has no episode dependency.
	doc.ID, doc.UnitID, doc.Kind = "bible", "", model.OntologyDocBible
	if err := repo.AppendOntologyDocument(&doc); err != nil {
		t.Fatal(err)
	}
}
