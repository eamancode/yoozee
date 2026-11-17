package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestStoryboardShotDeleteAndTaskAdmissionLock(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, order := range []string{"delete_first", "admission_first"} {
				t.Run(order, func(t *testing.T) {
					var db *gorm.DB
					if dialect == "postgres" {
						_, db = newProjectNameConflictPostgresService(t)
						pool, err := db.DB()
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = pool.Close() })
					} else {
						db = newSQLiteTestDB(t)
					}
					seedShotDeleteLockTest(t, db)
					deleteShot := func(conn *gorm.DB) error {
						svc := &Service{repo: repository.New(conn)}
						return svc.DeleteProjectShotChecked("owner", "project", "target", DeleteProjectShotRequest{
							ExpectedRevisionID: ptr("target-rev"), ExpectedArtifactIDs: []string{"target-take"},
						})
					}
					admitTask := func(conn *gorm.DB) error {
						return repository.New(conn).CreateTaskWithActiveLimit(&model.Task{
							ID: "requested", UserID: "owner", ProjectID: "project", Status: model.TaskStatusQueued, Type: "canvas_video",
							InputJSON: `{"metadata":{"domainProjectId":"project","unitId":"episode","shotId":"target"}}`,
						}, 10)
					}
					deleted := order == "delete_first"
					if deleted {
						err := raceShotDeleteAndAdmission(t, db, deleteShot, admitTask)
						if !errors.Is(err, repository.ErrProjectUnitShotsChanged) {
							t.Fatalf("admission after delete: got %v, want missing shot", err)
						}
					} else {
						err := raceShotDeleteAndAdmission(t, db, admitTask, deleteShot)
						var blocked *AppError
						if !errors.As(err, &blocked) || blocked.Status != http.StatusConflict || !strings.Contains(blocked.Message, "requested") {
							t.Fatalf("delete after admission: got %v, want 409 naming requested task", err)
						}
					}
					assertShotDeleteLockState(t, db, deleted)
				})
			}
		})
	}
}

func seedShotDeleteLockTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&model.Project{}, &model.ProjectUnit{}, &model.Shot{}, &model.ShotRevision{},
		&model.ShotArtifact{}, &model.ShotEvidence{}, &model.ShotAssetReference{}, &model.ProjectAssetCandidate{},
		&model.ProductionTaskLink{}, &model.CanvasProject{}, &model.Task{}, &model.Asset{}, &model.AssetVersion{}, &model.Resource{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.Project{ID: "project", Name: "单卡并发夹具", UserID: "owner", Status: model.ProjectStatusActive},
		&model.ProjectUnit{ID: "episode", ProjectID: "project", Kind: model.ProjectUnitKindEpisode},
		&model.Resource{ID: "shared-file", UserID: "owner", Kind: "video"},
		&model.Asset{ID: "actor", UserID: "owner", PrimaryVersionID: "actor-v1"},
		&model.AssetVersion{ID: "actor-v1", AssetID: "actor", Version: 1},
		&model.Task{ID: "neighbor-job", UserID: "owner", ProjectID: "project", Type: "canvas_video", Status: model.TaskStatusQueued,
			InputJSON: `{"metadata":{"domainProjectId":"project","unitId":"episode","shotId":"neighbor"}}`},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for pos, id := range []string{"target", "neighbor"} {
		for _, row := range []any{
			&model.Shot{ID: id, ProjectID: "project", UnitID: "episode", Position: pos, CurrentRevisionID: id + "-rev"},
			&model.ShotRevision{ID: id + "-rev", ShotID: id, Version: 1, PlotDescription: id},
			&model.ShotArtifact{ID: id + "-take", ProjectID: "project", UnitID: "episode", ShotID: id, RevisionID: id + "-rev",
				Type: "video", Version: 1, Status: "ready", Selected: true, ResourceID: "shared-file"},
			&model.ShotAssetReference{ID: id + "-ref", ShotID: id, Role: "reference", Status: "linked", AssetVersionID: "actor-v1"},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Hold the successful production operation open until a separate connection has
// actually contested its write lock. Both operations use their normal entry points.
func raceShotDeleteAndAdmission(t *testing.T, db *gorm.DB, first, second func(*gorm.DB) error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	holder := db.Begin()
	if holder.Error != nil {
		t.Fatal(holder.Error)
	}
	defer holder.Rollback()
	if err := first(holder); err != nil {
		t.Fatalf("first operation: %v", err)
	}
	return db.Connection(func(waiter *gorm.DB) error {
		waiter = waiter.Session(&gorm.Session{NewDB: true})
		if db.Dialector.Name() == "sqlite" {
			// SQLite has no lock-wait catalog. Require SQLITE_BUSY while the
			// first transaction is open, then retry on the same connection.
			if err := waiter.Exec("PRAGMA busy_timeout = 0").Error; err != nil {
				return err
			}
			var busy sqlite3.Error
			if err := second(waiter); !errors.As(err, &busy) || busy.Code != sqlite3.ErrBusy {
				return fmt.Errorf("expected competing SQLite writer to be busy, got %v", err)
			}
			if err := holder.Commit().Error; err != nil {
				return err
			}
			return second(waiter)
		}

		var waiterPID int
		if err := waiter.Raw("SELECT pg_backend_pid()").Scan(&waiterPID).Error; err != nil {
			return err
		}
		done := make(chan struct{})
		var result error
		go func() {
			defer close(done)
			result = second(waiter)
		}()
		defer func() {
			// Also release the waiter if the observer query fails or times out.
			holder.Rollback()
			<-done
		}()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			var blocked bool
			if err := holder.Raw("SELECT pg_backend_pid() = ANY(pg_blocking_pids(?))", waiterPID).Scan(&blocked).Error; err != nil {
				return err
			}
			if blocked {
				break
			}
			select {
			case <-done:
				return fmt.Errorf("second operation returned before holder released its lock: %v", result)
			case <-ctx.Done():
				return ctx.Err()
			case <-tick.C:
			}
		}
		if err := holder.Commit().Error; err != nil {
			return err
		}
		<-done
		return result
	})
}

func assertShotDeleteLockState(t *testing.T, db *gorm.DB, deleted bool) {
	t.Helper()
	targetCount, taskCount := int64(1), int64(1)
	neighborPosition := 1
	if deleted {
		targetCount, taskCount, neighborPosition = 0, 0, 0
	}
	for _, check := range []struct {
		table any
		id    string
		count int64
	}{
		{&model.Shot{}, "target", targetCount}, {&model.ShotRevision{}, "target-rev", targetCount},
		{&model.ShotArtifact{}, "target-take", targetCount}, {&model.ShotAssetReference{}, "target-ref", targetCount},
		{&model.Task{}, "requested", taskCount}, {&model.Task{}, "neighbor-job", 1},
		{&model.ProjectUnit{}, "episode", 1}, {&model.Resource{}, "shared-file", 1}, {&model.AssetVersion{}, "actor-v1", 1},
	} {
		var count int64
		if err := db.Model(check.table).Where("id = ?", check.id).Count(&count).Error; err != nil || count != check.count {
			t.Fatalf("%T %s: count %d, want %d, error %v", check.table, check.id, count, check.count, err)
		}
	}
	var shot model.Shot
	var take model.ShotArtifact
	var binding model.ShotAssetReference
	var revision model.ShotRevision
	if err := db.First(&shot, "id = ?", "neighbor").Error; err != nil || shot.Position != neighborPosition || shot.CurrentRevisionID != "neighbor-rev" {
		t.Fatalf("neighbor after race: %+v, %v", shot, err)
	}
	if err := db.First(&revision, "id = ?", shot.CurrentRevisionID).Error; err != nil || revision.ShotID != shot.ID || revision.PlotDescription != "neighbor" {
		t.Fatalf("neighbor revision: %+v, %v", revision, err)
	}
	if err := db.First(&take, "id = ?", "neighbor-take").Error; err != nil || take.ShotID != shot.ID || take.ResourceID != "shared-file" || !take.Selected {
		t.Fatalf("neighbor take: %+v, %v", take, err)
	}
	if err := db.First(&binding, "id = ?", "neighbor-ref").Error; err != nil || binding.ShotID != shot.ID || binding.AssetVersionID != "actor-v1" || binding.Status != "linked" {
		t.Fatalf("neighbor binding: %+v, %v", binding, err)
	}
}
