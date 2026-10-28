package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

// assetQueueTitles 是排队用例里三个场景资产，按建的先后（出图也按这个顺序排）。
var assetQueueTitles = []string{"病房走廊", "电梯口", "电梯里"}

// assetQueueFixture 是三个还没出图的场景资产（资产设计建的，payload 记了出图状态），同时任务数上限 1，图片上游一直返回 500。
// M8 卡 B 起色卡由代码合成，排队只剩资产设计的角色、场景、道具。
func assetQueueFixture(t *testing.T) (*Service, *gorm.DB, model.Project, DramaImageTaskTemplate, *atomic.Int32) {
	t.Helper()
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedEpisodeProject(t, svc, db)
	folderID, err := svc.resolveProjectAssetFolderID(project.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for i, title := range assetQueueTitles {
		now := base.Add(time.Duration(i) * time.Second)
		assetID, versionID := "asset-"+strconv.Itoa(i+1), "version-"+strconv.Itoa(i+1)
		definition, _ := json.Marshal(map[string]any{"prompt": title + "，夜，医院", "description": title})
		payload, _ := json.Marshal(map[string]any{"id": assetID, "kind": "entity", "category": model.AssetCategoryEnvironment, "status": model.AssetVersionStatusConfirmed, "primaryVersionId": versionID, "title": title,
			"metadata": map[string]any{assetImageKey: dramaImageState{Status: dramaImageNone}}})
		rows := []any{
			&model.Asset{ID: assetID, UserID: "user-1", Kind: "entity", Category: model.AssetCategoryEnvironment, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: versionID, Title: title, PayloadJSON: string(payload), CreatedAt: now, UpdatedAt: now},
			&model.AssetVersion{ID: versionID, AssetID: assetID, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: string(definition), CreatedAt: now, UpdatedAt: now},
			&model.ProjectAssetLink{ID: "link-" + strconv.Itoa(i+1), ProjectID: project.ID, AssetID: assetID, FolderID: folderID, Position: i, CreatedAt: now},
		}
		for _, row := range rows {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
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
	template := DramaImageTaskTemplate{Model: "queue-image", Input: map[string]any{"mode": "image", "prompt": "场景", "config": map[string]any{
		"channelId": "", "baseUrl": images.URL, "apiKey": "test-key", "model": "queue-image", "interfaceType": "openai-image", "apiFormat": "openai", "size": "1024x1024", "count": "1",
	}}}
	return svc, db, project, template, &imageCalls
}

// assetStates 是按资产标题的出图状态（存在资产上的那份）。
func assetStates(t *testing.T, svc *Service, projectID string) map[string]dramaImageState {
	t.Helper()
	assets, err := svc.projectDramaImageAssets("user-1", projectID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]dramaImageState{}
	for _, a := range assets {
		out[a.asset.Title] = dramaImageStateOf(&a.asset, assetImageKey)
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
	svc, db, project, template, imageCalls := assetQueueFixture(t)
	first, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Queued) != 1 || len(first.Waiting) != 2 || len(first.Failed) != 0 {
		t.Fatalf("上限 1：排上 1 张、等 2 张：%+v", first)
	}
	states := assetStates(t, svc, project.ID)
	if states["病房走廊"].Status != dramaImageQueued || states["电梯口"].Status != dramaImageWaiting || states["电梯里"].Status != dramaImageWaiting {
		t.Fatalf("排队状态：%+v", states)
	}

	failed, _ := runQueuedTask(t, svc)
	if failed.Status != model.TaskStatusFailed || imageCalls.Load() == 0 {
		t.Fatalf("第一张该失败：%s %q（上游调用 %d 次）", failed.Status, failed.Error, imageCalls.Load())
	}
	states = assetStates(t, svc, project.ID)
	if s := states["病房走廊"]; s.Status != dramaImageFailed || s.TaskID != failed.ID || s.Error == "" {
		t.Fatalf("失败的那张记成失败：%+v", s)
	}
	if states["电梯口"].Status != dramaImageQueued || states["电梯里"].Status != dramaImageWaiting || activeImageTasks(t, db) != 1 {
		t.Fatalf("第一张失败后第二张接着排：%+v", states)
	}

	if _, err := svc.CancelTask(context.Background(), "user-1", states["电梯口"].TaskID); err != nil {
		t.Fatal(err)
	}
	cancelledID := states["电梯口"].TaskID
	states = assetStates(t, svc, project.ID)
	if s := states["电梯口"]; s.Status != dramaImageFailed || s.TaskID != cancelledID {
		t.Fatalf("取消的那张记成失败：%+v", s)
	}
	if states["电梯里"].Status != dramaImageQueued || activeImageTasks(t, db) != 1 {
		t.Fatalf("第二张取消后第三张接着排：%+v", states)
	}

	if last, _ := runQueuedTask(t, svc); last.Status != model.TaskStatusFailed {
		t.Fatalf("第三张该失败：%s", last.Status)
	}
	states = assetStates(t, svc, project.ID)
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

	again, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Queued) != 1 || len(again.Waiting) != 2 {
		t.Fatalf("继续出图重排失败的：%+v", again)
	}
}

// SQLite 别的写入占着库时建任务报「database is locked」：退避重试后建上；一直忙时留在「等空位」并写明原因，不记成出图失败。
func TestDramaImageQueueRetriesDatabaseLocked(t *testing.T) {
	svc, db, project, template, _ := assetQueueFixture(t)
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
	result, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Queued) != 1 || len(result.Failed) != 0 || locks.Load() != 0 {
		t.Fatalf("锁了两次，重试后建上：%+v（剩 %d 次锁）%+v", result, locks.Load(), assetStates(t, svc, project.ID))
	}
	// 取消第一张后接着排第二张时库一直忙：第二张留在等空位、写明原因，第三张也不动，都不算出图失败。
	locks.Store(100)
	if _, err := svc.CancelTask(context.Background(), "user-1", assetStates(t, svc, project.ID)["病房走廊"].TaskID); err != nil {
		t.Fatal(err)
	}
	locks.Store(0)
	states := assetStates(t, svc, project.ID)
	if s := states["电梯口"]; s.Status != dramaImageWaiting || s.Error == "" {
		t.Fatalf("库忙没排上的留在等空位并写明原因：%+v", s)
	}
	if states["电梯里"].Status != dramaImageWaiting || states["病房走廊"].Status != dramaImageFailed || activeImageTasks(t, db) != 0 {
		t.Fatalf("库忙时后面的不动：%+v", states)
	}
	// 「继续出图」时库不忙了：照常排上。
	again, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template})
	if err != nil || len(again.Queued) != 1 || len(again.Failed) != 0 {
		t.Fatalf("继续出图：%+v %v", again, err)
	}
}
