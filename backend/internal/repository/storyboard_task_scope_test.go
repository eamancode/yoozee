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
	if err := db.AutoMigrate(&model.Project{}, &model.ProjectUnit{}, &model.Shot{}, &model.CanvasProject{}, &model.WorkflowInstance{}, &model.WorkflowStepInstance{}, &model.ProductionTaskLink{}, &model.Task{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.Project{ID: "project", UserID: "user", Name: "任务保护", Status: model.ProjectStatusActive},
		&model.ProjectUnit{ID: "episode", ProjectID: "project", Kind: model.ProjectUnitKindEpisode},
		&model.WorkflowInstance{ID: "workflow", ProjectID: "project", UnitID: "episode"},
		&model.WorkflowStepInstance{ID: "surviving-step", WorkflowInstanceID: "workflow"},
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

func TestUnitTaskAdmissionFencesDeletedEpisode(t *testing.T) {
	repo := storyboardTaskTestRepo(t)
	const director = `{"metadata":{"domainProjectId":"project","chapterId":"removed","source":"short-drama-chapter-storyboard"}}`
	if err := repo.db.Create(&model.ProjectUnit{ID: "removed", ProjectID: "project", Kind: model.ProjectUnitKindEpisode}).Error; err != nil {
		t.Fatal(err)
	}
	deleted := make(chan struct{})
	release := make(chan struct{})
	deleteResult := make(chan error, 1)
	go func() {
		deleteResult <- repo.Transaction(func(tx *Repository) error {
			if _, err := tx.LockActiveStoryboardProject("user", "project"); err != nil {
				return err
			}
			if err := tx.db.Delete(&model.ProjectUnit{}, "project_id = ? AND id = ?", "project", "removed").Error; err != nil {
				return err
			}
			close(deleted)
			<-release
			return nil
		})
	}()
	select {
	case err := <-deleteResult:
		t.Fatalf("delete ended before admission: %v", err)
	case <-deleted:
	}
	task := model.Task{ID: "director", UserID: "user", ProjectID: "project", Type: "canvas_text", Status: model.TaskStatusQueued, InputJSON: director}
	started := make(chan struct{})
	admission := make(chan error, 1)
	go func() {
		close(started)
		admission <- repo.CreateTaskWithActiveLimit(&task, 10)
	}()
	<-started
	close(release)
	if err := <-deleteResult; err != nil {
		t.Fatal(err)
	}
	if err := <-admission; !errors.Is(err, ErrStoryboardTaskUnitGone) {
		t.Fatalf("whole-episode storyboard queued after deletion: %v", err)
	}
	if err := repo.CreateTaskWithCreditReservation(&task, &model.BillingOrder{}, 10); !errors.Is(err, ErrStoryboardTaskUnitGone) {
		t.Fatalf("stale episode reached billing: %v", err)
	}
	if _, err := repo.RetryTaskWithBilling("user", &task, nil, 10); !errors.Is(err, ErrStoryboardTaskUnitGone) {
		t.Fatalf("stale episode reached retry: %v", err)
	}
}

func TestUnitTaskAdmissionChecksOnlyItsProjectInputs(t *testing.T) {
	repo := storyboardTaskTestRepo(t)
	if err := repo.db.Create(&model.ProjectUnit{ID: "foreign", ProjectID: "another", Kind: model.ProjectUnitKindEpisode}).Error; err != nil {
		t.Fatal(err)
	}
	task := model.Task{UserID: "user", ProjectID: "canvas", Type: "canvas_text", Status: model.TaskStatusQueued}
	for _, tc := range []struct{ name, metadata string }{
		{"extract", `"source":"short-drama-assets-extract","chapterId":"missing"`},
		{"extract-queue", `"source":"short-drama-assets-extract","chapterId":"episode","dramaAssets":{"unitId":"episode","chain":{"queue":["missing"]}}`},
		{"director-queue", `"source":"short-drama-chapter-storyboard","chapterId":"episode","dramaPipeline":{"queue":["missing"]}`},
		{"setup", `"source":"short-drama-director-setup","dramaSetup":{"basis":["episode","missing"]}`},
		{"adapt", `"source":"short-drama-script-adapt","dramaAdapt":{"sourceUnitIds":["episode"],"replaceUnitIds":["missing"]}`},
		{"palette", `"source":"short-drama-scene-palette","dramaScenePalette":{"episodes":[{"unitId":"foreign"}]}`},
		{"unit-only", `"unitId":"foreign"`},
		{"workflow-step", `"workflowStepId":"deleted-step"`},
	} {
		task.ID = tc.name
		task.InputJSON = `{"metadata":{"domainProjectId":"project",` + tc.metadata + `}}`
		if err := repo.CreateTaskWithActiveLimit(&task, 10); !errors.Is(err, ErrStoryboardTaskUnitGone) {
			t.Errorf("%s queued with a missing or foreign episode: %v", tc.name, err)
		}
	}
	for _, tc := range []struct{ name, input string }{
		{"remaining", `{"metadata":{"domainProjectId":"project","source":"short-drama-assets-extract","chapterId":"episode","dramaAssets":{"unitId":"episode","chain":{"queue":["episode"]}}}}`},
		{"project-style", `{"metadata":{"domainProjectId":"project","source":"short-drama-style-lock","dramaStyleLock":{"text":"deleted episode"}}}`},
		{"linked-step", `{"workflowStepId":"surviving-step","domainProjectId":"project"}`},
		{"evaluation", `{"metadata":{"source":"drama-step-eval","dramaAdapt":{"sourceUnitIds":["fixture-only"]}}}`},
		{"independent-drama", `{"metadata":{"domainProjectId":"project","source":"drama_production","shotId":"independent-shot"}}`},
	} {
		task.ID, task.InputJSON = tc.name, tc.input
		if err := repo.CreateTaskWithActiveLimit(&task, 10); err != nil {
			t.Errorf("%s should remain usable: %v", tc.name, err)
		}
	}
	var count int64
	if err := repo.db.Model(&model.Task{}).Count(&count).Error; err != nil || count != 5 {
		t.Fatalf("unexpected persisted tasks: %d, %v", count, err)
	}
}

func TestUnitTaskAdmissionResolvesControllerOnlyChildren(t *testing.T) {
	repo := storyboardTaskTestRepo(t)
	expires := time.Now().Add(time.Hour)
	saveParent := func(id, owner, project, unit, kind string) {
		t.Helper()
		parent := model.Task{
			ID: id, UserID: owner, ProjectID: project, Type: kind, Status: model.TaskStatusRunning, LeaseOwner: "worker", LeaseExpiresAt: &expires,
			InputJSON: `{"unitId":"` + unit + `","steps":[]}`,
		}
		if err := repo.db.Create(&parent).Error; err != nil {
			t.Fatal(err)
		}
	}
	saveParent("stale", "user", "project", "deleted", model.TaskTypeProjectVideoChain)
	saveParent("current", "user", "project", "episode", model.TaskTypeProjectVideoChain)
	saveParent("foreign-user", "other", "project", "episode", model.TaskTypeProjectVideoChain)
	saveParent("foreign-project", "user", "elsewhere", "episode", model.TaskTypeProjectVideoChain)
	saveParent("wrong-type", "user", "project", "episode", "canvas_text")
	for _, tc := range []struct {
		parent string
		want   error
	}{
		{"stale", ErrStoryboardTaskUnitGone}, {"current", nil}, {"foreign-user", gorm.ErrRecordNotFound},
		{"foreign-project", gorm.ErrRecordNotFound}, {"wrong-type", gorm.ErrRecordNotFound},
	} {
		child := model.Task{
			ID: "child-" + tc.parent, UserID: "user", ProjectID: "project", Type: "canvas_video", Status: model.TaskStatusQueued,
			ParentControllerID: tc.parent, ParentControllerLease: "worker", InputJSON: `{"_projectVideoChain":{"controllerId":"` + tc.parent + `"}}`,
		}
		if err := repo.CreateTaskWithActiveLimit(&child, 10); !errors.Is(err, tc.want) {
			t.Errorf("child of %s: got %v, want %v", tc.parent, err, tc.want)
		}
	}
	sourceOnly := model.Task{
		ID: "source-only", UserID: "user", ProjectID: "project", Type: "canvas_video", Status: model.TaskStatusQueued,
		ParentControllerID: "stale", ParentControllerLease: "worker", InputJSON: `{"_projectVideoChain":{"controllerId":"stale","sourceShotId":"neighbor"}}`,
	}
	if err := repo.CreateTaskWithActiveLimit(&sourceOnly, 10); !errors.Is(err, ErrStoryboardTaskUnitGone) {
		t.Fatalf("surviving source shot from a different episode hid a deleted target: %v", err)
	}
	var children int64
	if err := repo.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&children).Error; err != nil || children != 1 {
		t.Fatalf("unexpected children: %d, %v", children, err)
	}
	original := model.Task{ID: "original", UserID: "user", ProjectID: "project", Type: "canvas_video", Status: model.TaskStatusFailed,
		InputJSON: `{"_projectVideoChain":{"controllerId":"stale"}}`}
	if err := repo.db.Create(&original).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.ClaimFailedTaskProviderRecovery(original.ID, original.UserID, "manual-recovery:fixture", time.Minute); err != nil {
		t.Fatalf("original upstream status query was blocked: %v", err)
	}
}
