package repository

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

func storyboardTaskTestRepo(t *testing.T) *Repository {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "storyboard.db") + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(&model.Project{}, &model.Shot{}, &model.CanvasProject{}, &model.ProductionTaskLink{}, &model.Task{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.Project{ID: "project", UserID: "user", Name: "任务保护", Status: model.ProjectStatusActive},
		&model.CanvasProject{ID: "canvas", UserID: "user", ProjectID: "project"},
		&model.Shot{ID: "target", ProjectID: "project", UnitID: "episode"},
		&model.Shot{ID: "neighbor", ProjectID: "project", UnitID: "episode"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return New(db)
}

func TestStoryboardActiveTasksUseStableIDs(t *testing.T) {
	repo := storyboardTaskTestRepo(t)
	add := func(id, owner, project, kind, input string, status model.TaskStatus) {
		t.Helper()
		task := model.Task{ID: id, UserID: owner, ProjectID: project, Type: kind, InputJSON: input, Status: status}
		if err := repo.db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	const direct = `{"metadata":{"domainProjectId":"project","shotId":"target"}}`
	const neighbor = `{"metadata":{"domainProjectId":"project","shotId":"neighbor"}}`
	const chain = `{"unitId":"episode","steps":[{"shotId":"neighbor","sourceShotId":"target"}]}`
	const repair = `{"metadata":{"domainProjectId":"project","source":"short-drama-chapter-storyboard","chapterId":"episode","dramaSSS":{"repair":{"shotId":"neighbor"}}}}`
	add("chain", "user", "project", model.TaskTypeProjectVideoChain, chain, model.TaskStatusQueued)
	add("cancelled-chain", "user", "project", model.TaskTypeProjectVideoChain, chain, model.TaskStatusCancelled)
	add("still-running-child", "user", "project", "canvas_video", `{"metadata":{"domainProjectId":"project","shotId":"neighbor"},"_projectVideoChain":{"sourceShotId":"target"}}`, model.TaskStatusRunning)
	add("canvas-image", "user", "canvas", "canvas_image", direct, model.TaskStatusRunning)
	add("rebuild", "user", "project", "canvas_text", `{"metadata":{"domainProjectId":"project","source":"short-drama-chapter-storyboard","chapterId":"episode"}}`, model.TaskStatusRunning)
	add("neighbor-repair", "user", "project", "canvas_text", repair, model.TaskStatusQueued)
	add("another", "user", "project", "canvas_video", neighbor, model.TaskStatusQueued)
	add("completed", "user", "project", "canvas_video", direct, model.TaskStatusSucceeded)
	add("provider-query", "user", "project", "canvas_video", direct, model.TaskStatusFailed)
	add("expired-query", "user", "project", "canvas_video", direct, model.TaskStatusFailed)
	if err := repo.ClaimFailedTaskProviderRecovery("provider-query", "user", "manual-recovery:current", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := repo.ClaimFailedTaskProviderRecovery("expired-query", "user", "manual-recovery:expired", -time.Minute); err != nil {
		t.Fatal(err)
	}
	add("other-owner", "someone", "project", "canvas_video", direct, model.TaskStatusRunning)
	add("other-canvas", "user", "unrelated", "canvas_video", direct, model.TaskStatusRunning)
	add("legacy", "user", "project", "canvas_video", `{"metadata":{"domainProjectId":"project","source":"drama_production","shotId":"target"}}`, model.TaskStatusRunning)
	add("saved-link", "user", "project", "canvas_video", "{}", model.TaskStatusQueued)
	if err := repo.db.Create(&model.ProductionTaskLink{ID: "link", TaskID: "saved-link", ProjectID: "project", ShotID: "target"}).Error; err != nil {
		t.Fatal(err)
	}
	blockers, err := repo.ActiveTasksForStoryboardShot("user", "project", "episode", "target")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, blocker := range blockers {
		if blocker.InputJSON != "" {
			t.Fatal("task input exposed")
		}
		ids = append(ids, blocker.ID)
	}
	if !slices.Equal(ids, []string{"canvas-image", "chain", "provider-query", "rebuild", "saved-link", "still-running-child"}) {
		t.Fatalf("unexpected blockers: %v", ids)
	}
}

func TestStoryboardTaskAdmissionAfterConcurrentDelete(t *testing.T) {
	repo := storyboardTaskTestRepo(t)
	deleted := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- repo.Transaction(func(tx *Repository) error {
			if _, err := tx.LockActiveStoryboardProject("user", "project"); err != nil {
				return err
			}
			if err := tx.db.Delete(&model.Shot{}, "id = ?", "target").Error; err != nil {
				return err
			}
			close(deleted)
			<-release
			return nil
		})
	}()
	select {
	case err := <-result:
		t.Fatalf("delete failed before task submission: %v", err)
	case <-deleted:
	}
	admit := make(chan error, 1)
	started := make(chan struct{})
	task := model.Task{ID: "new", UserID: "user", ProjectID: "canvas", Type: "canvas_image", Status: model.TaskStatusQueued, InputJSON: `{"metadata":{"domainProjectId":"project","unitId":"episode","shotId":"target"}}`}
	go func() {
		close(started)
		admit <- repo.CreateTaskWithActiveLimit(&task, 10)
	}()
	<-started
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := <-admit; !errors.Is(err, ErrProjectUnitShotsChanged) {
		t.Fatalf("stale request admitted: %v", err)
	}
	if err := repo.CreateTaskWithCreditReservation(&task, &model.BillingOrder{}, 10); !errors.Is(err, ErrProjectUnitShotsChanged) {
		t.Fatalf("stale request reached reservation: %v", err)
	}
	if _, err := repo.RetryTaskWithBilling("user", &task, nil, 10); !errors.Is(err, ErrProjectUnitShotsChanged) {
		t.Fatalf("stale request reached retry: %v", err)
	}
	chain := model.Task{ID: "chain", UserID: "user", ProjectID: "project", Type: model.TaskTypeProjectVideoChain, InputJSON: `{"unitId":"episode","steps":[{"shotId":"neighbor","sourceShotId":"target"}]}`}
	if _, err := repo.CreateProjectVideoChain(&chain, 10, false); !errors.Is(err, ErrProjectUnitShotsChanged) {
		t.Fatalf("chain admitted with removed predecessor: %v", err)
	}
	var count int64
	if err := repo.db.Model(&model.Task{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unexpected tasks after deletion: %d, %v", count, err)
	}
	chain.Status = model.TaskStatusFailed
	if err := repo.db.Create(&chain).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.ResumeProjectVideoChain(chain, chain.InputJSON, func(*Repository) error {
		t.Fatal("stale resume passed the shot check")
		return nil
	}); !errors.Is(err, ErrProjectUnitShotsChanged) {
		t.Fatalf("resumed with a deleted source: %v", err)
	}
	task.ID, task.Status, task.MediaRecoveryJSON = "old-result", model.TaskStatusFailed, "stored-checkpoint"
	if err := repo.db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.RequeueTaskMediaRecovery(&task, "fixture"); err != nil {
		t.Fatalf("recovery of the original media must remain available: %v", err)
	}
	link := &model.ProductionTaskLink{ID: "late-link", TaskID: task.ID, ProjectID: "project", ShotID: "target"}
	if err := repo.RegisterTaskOutputRecords("project", nil, link, nil); !errors.Is(err, ErrShotGone) {
		t.Fatalf("late link for deleted shot should roll back: %v", err)
	}
	if err := repo.db.Model(link).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("orphan output link: %d, %v", count, err)
	}
}
