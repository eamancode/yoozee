package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

// paletteQueueFixture 是三张还没出图的色卡（导演风格不带出图跑完），同时任务数上限 1，图片上游一直返回 500。
func paletteQueueFixture(t *testing.T) (*Service, *gorm.DB, model.Project, DramaImageTaskTemplate, *atomic.Int32) {
	t.Helper()
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedEpisodeProject(t, svc, db)
	text := httptest.NewServer(styleBibleUpstream())
	t.Cleanup(text.Close)
	queueStyleBibleTask(t, svc, db, project, text.URL, DramaProjectStepRunRequest{})
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("导演风格：%v %q", err, done.Error)
	}
	var imageCalls atomic.Int32
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		imageCalls.Add(1)
		http.Error(w, `{"error":{"message":"upstream boom"}}`, http.StatusInternalServerError)
	}))
	t.Cleanup(images.Close)
	policy := defaultRuntimePolicy()
	policy.Task.ActiveTaskLimit = 1
	if _, err := svc.UpdateRuntimePolicySetting(&model.User{ID: "admin", Role: model.UserRoleAdmin}, policy); err != nil {
		t.Fatal(err)
	}
	template := DramaImageTaskTemplate{Model: "queue-image", Input: map[string]any{"mode": "image", "prompt": "色卡", "config": map[string]any{
		"channelId": "", "baseUrl": images.URL, "apiKey": "test-key", "model": "queue-image", "interfaceType": "openai-image", "apiFormat": "openai", "size": "1024x1024", "count": "1",
	}}}
	return svc, db, project, template, &imageCalls
}

// paletteStates 是按场编号的色卡出图状态（存在资产上的那份）。
func paletteStates(t *testing.T, svc *Service, projectID string) map[string]dramaImageState {
	t.Helper()
	palettes, err := svc.projectPaletteAssets("user-1", projectID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]dramaImageState{}
	for code, asset := range palettes {
		out[code] = dramaImageStateOf(&asset, paletteImageKey)
	}
	return out
}

func activeImageTasks(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&model.Task{}).Where("type = ? AND status IN ?", "canvas_image", []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

// 上限 1：第一张出图失败后它记成失败、第二张自动排上；第二张取消后同样记失败、第三张排上；第三张也失败后不再建任务（不自动重试）。
// 「继续出图」再把失败的重新排。
func TestDramaImageQueueContinuesAfterFailedOrCancelledTask(t *testing.T) {
	svc, db, project, template, imageCalls := paletteQueueFixture(t)
	first, err := svc.QueueDramaPaletteImages("user-1", project.ID, DramaPaletteImagesRequest{ImageTask: &template})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Queued) != 1 || len(first.Waiting) != 2 || len(first.Failed) != 0 {
		t.Fatalf("上限 1：排上 1 张、等 2 张：%+v", first)
	}
	states := paletteStates(t, svc, project.ID)
	if states["E01S01"].Status != dramaImageQueued || states["E01S02"].Status != dramaImageWaiting || states["E02S01"].Status != dramaImageWaiting {
		t.Fatalf("排队状态：%+v", states)
	}

	failed, _ := runQueuedTask(t, svc)
	if failed.Status != model.TaskStatusFailed || imageCalls.Load() == 0 {
		t.Fatalf("第一张该失败：%s %q（上游调用 %d 次）", failed.Status, failed.Error, imageCalls.Load())
	}
	states = paletteStates(t, svc, project.ID)
	if s := states["E01S01"]; s.Status != dramaImageFailed || s.TaskID != failed.ID || s.Error == "" {
		t.Fatalf("失败的那张记成失败：%+v", s)
	}
	if states["E01S02"].Status != dramaImageQueued || states["E02S01"].Status != dramaImageWaiting || activeImageTasks(t, db) != 1 {
		t.Fatalf("第一张失败后第二张接着排：%+v", states)
	}

	if _, err := svc.CancelTask(context.Background(), "user-1", states["E01S02"].TaskID); err != nil {
		t.Fatal(err)
	}
	cancelledID := states["E01S02"].TaskID
	states = paletteStates(t, svc, project.ID)
	if s := states["E01S02"]; s.Status != dramaImageFailed || s.TaskID != cancelledID {
		t.Fatalf("取消的那张记成失败：%+v", s)
	}
	if states["E02S01"].Status != dramaImageQueued || activeImageTasks(t, db) != 1 {
		t.Fatalf("第二张取消后第三张接着排：%+v", states)
	}

	if last, _ := runQueuedTask(t, svc); last.Status != model.TaskStatusFailed {
		t.Fatalf("第三张该失败：%s", last.Status)
	}
	states = paletteStates(t, svc, project.ID)
	for code, s := range states {
		if s.Status != dramaImageFailed {
			t.Fatalf("%s 记成失败：%+v", code, s)
		}
	}
	var imageTasks int64
	db.Model(&model.Task{}).Where("type = ?", "canvas_image").Count(&imageTasks)
	if imageTasks != 3 || activeImageTasks(t, db) != 0 {
		t.Fatalf("失败的不自动重试：共 %d 个出图任务，还在跑 %d 个", imageTasks, activeImageTasks(t, db))
	}

	again, err := svc.QueueDramaPaletteImages("user-1", project.ID, DramaPaletteImagesRequest{ImageTask: &template})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Queued) != 1 || len(again.Waiting) != 2 {
		t.Fatalf("继续出图重排失败的：%+v", again)
	}
}

// SQLite 别的写入占着库时建任务报「database is locked」：退避重试后建上；一直忙时留在「等空位」并写明原因，不记成出图失败。
func TestDramaImageQueueRetriesDatabaseLocked(t *testing.T) {
	svc, db, project, template, _ := paletteQueueFixture(t)
	var locks atomic.Int32
	if err := db.Callback().Create().Before("gorm:create").Register("test:database-locked", func(tx *gorm.DB) {
		if tx.Statement.Table == "tasks" && locks.Load() > 0 {
			locks.Add(-1)
			_ = tx.AddError(errors.New("database is locked (5) (SQLITE_BUSY)"))
		}
	}); err != nil {
		t.Fatal(err)
	}

	locks.Store(2)
	result, err := svc.QueueDramaPaletteImages("user-1", project.ID, DramaPaletteImagesRequest{ImageTask: &template})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Queued) != 1 || len(result.Failed) != 0 || locks.Load() != 0 {
		t.Fatalf("锁了两次，重试后建上：%+v（剩 %d 次锁）%+v", result, locks.Load(), paletteStates(t, svc, project.ID))
	}
	// 取消第一张后接着排第二张时库一直忙：第二张留在等空位、写明原因，第三张也不动，都不算出图失败。
	locks.Store(100)
	if _, err := svc.CancelTask(context.Background(), "user-1", paletteStates(t, svc, project.ID)["E01S01"].TaskID); err != nil {
		t.Fatal(err)
	}
	locks.Store(0)
	states := paletteStates(t, svc, project.ID)
	if s := states["E01S02"]; s.Status != dramaImageWaiting || s.Error == "" {
		t.Fatalf("库忙没排上的留在等空位并写明原因：%+v", s)
	}
	if states["E02S01"].Status != dramaImageWaiting || states["E01S01"].Status != dramaImageFailed || activeImageTasks(t, db) != 0 {
		t.Fatalf("库忙时后面的不动：%+v", states)
	}
	// 「继续出图」时库不忙了：照常排上。
	again, err := svc.QueueDramaPaletteImages("user-1", project.ID, DramaPaletteImagesRequest{ImageTask: &template})
	if err != nil || len(again.Queued) != 1 || len(again.Failed) != 0 {
		t.Fatalf("继续出图：%+v %v", again, err)
	}
}
