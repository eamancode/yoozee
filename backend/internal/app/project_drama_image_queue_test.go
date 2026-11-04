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
func assetQueueFixture(t *testing.T, response ...func(http.ResponseWriter, *http.Request, int32)) (*Service, *gorm.DB, model.Project, DramaImageTaskTemplate, *atomic.Int32) {
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
		call := imageCalls.Add(1)
		if len(response) > 0 {
			response[0](w, r, call)
			return
		}
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

func TestDramaImageChannelFailurePausesBatchAndResumesExplicitly(t *testing.T) {
	for _, code := range []int{401, 403, 402, 404} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			svc, db, project, template, calls := assetQueueFixture(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
				http.Error(w, "channel unavailable", code)
			})
			if _, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template}); err != nil {
				t.Fatal(err)
			}
			failed, _ := runQueuedTask(t, svc)
			states := assetStates(t, svc, project.ID)
			if failed.Status != model.TaskStatusFailed || calls.Load() != 1 || activeImageTasks(t, db) != 0 || states["电梯口"].Status != dramaImagePaused || states["电梯里"].Status != dramaImagePaused {
				t.Fatalf("channel did not pause: task=%s calls=%d states=%+v", failed.Status, calls.Load(), states)
			}
			oldBatch := states["病房走廊"].BatchID
			resumed, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template})
			if err != nil || len(resumed.Queued) != 1 || len(resumed.Waiting) != 2 {
				t.Fatalf("resume: %+v %v", resumed, err)
			}
			svc.afterDramaImageTaskEnded(*failed)
			states = assetStates(t, svc, project.ID)
			if states["病房走廊"].BatchID == oldBatch || states["电梯口"].Status != dramaImageWaiting || activeImageTasks(t, db) != 1 {
				t.Fatalf("stale callback modified recovery: %+v", states)
			}
		})
	}
}

func TestDramaImageInvalidSelectionDoesNotPartiallyReset(t *testing.T) {
	svc, _, project, template, _ := assetQueueFixture(t)
	_, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template, AssetIDs: []string{"asset-1", "not-in-project"}})
	if err == nil {
		t.Fatal("invalid selection accepted")
	}
	if state := assetStates(t, svc, project.ID)["病房走廊"]; state.Status != dramaImageNone {
		t.Fatalf("valid prefix was mutated: %+v", state)
	}
}

func TestLowPriorityPropsRemainOptionalUntilExplicitSelection(t *testing.T) {
	svc, db, project, template, calls := assetQueueFixture(t)
	asset, err := svc.repo.AssetForUser("user-1", "asset-1")
	if err != nil {
		t.Fatal(err)
	}
	payload := assetPayload(asset)
	payload["data"] = map[string]any{"definition": map[string]any{"productionPriority": "low"}}
	raw, _ := json.Marshal(payload)
	if err := db.Model(&model.Asset{}).Where("id = ?", asset.ID).Updates(map[string]any{"category": model.AssetCategoryProp, "payload_json": string(raw)}).Error; err != nil {
		t.Fatal(err)
	}
	queued, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range append(queued.Queued, queued.Waiting...) {
		if id == asset.ID {
			t.Fatal("background prop queued by default")
		}
	}
	explicit, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template, AssetIDs: []string{asset.ID}})
	if err != nil || len(explicit.Waiting) != 1 || explicit.Waiting[0] != asset.ID {
		t.Fatalf("manual selection unavailable: %+v %v", explicit, err)
	}
	if calls.Load() != 0 {
		t.Fatal("queue test must not invoke image model")
	}
}

func TestDramaImageStaleCompletionCannotReplaceResumedState(t *testing.T) {
	svc, _, project, _, _ := assetQueueFixture(t)
	state := dramaImageState{Status: dramaImageQueued, TaskID: "new-task", BatchID: "new-batch", AutoRetryCount: 1}
	if err := svc.putDramaImageState("user-1", "asset-1", assetImageKey, state); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{dramaImageBound, dramaImageFailed} {
		if err := svc.finishDramaImageState("user-1", "asset-1", dramaImageState{Status: status, TaskID: "old-task"}); err != nil {
			t.Fatal(err)
		}
		if got := assetStates(t, svc, project.ID)["病房走廊"]; got != state {
			t.Fatalf("stale completion overwrote state: %+v", got)
		}
	}
	asset, _ := svc.repo.AssetForUser("user-1", "asset-1")
	if got := svc.assetImageSummary("user-1", asset, true); got.Status == dramaImageBound {
		t.Fatal("existing image hides the new task state")
	}
}

func TestDramaImageSummaryUsesNewerBoundManualImage(t *testing.T) {
	svc, db, project, _, _ := assetQueueFixture(t)
	base := time.Now()
	for i, status := range []model.TaskStatus{model.TaskStatusFailed, model.TaskStatusSucceeded, model.TaskStatusFailed} {
		task := model.Task{ID: "manual-state-" + strconv.Itoa(i), UserID: "user-1", ProjectID: project.ID, Type: "canvas_image", Status: status, CreatedAt: base.Add(time.Duration(i) * time.Second)}
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	representation := model.AssetRepresentation{ID: "manual-representation", TaskID: "manual-state-1", AssetVersionID: "version-1", MediaType: "image", Role: "primary"}
	if err := db.Create(&representation).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ taskID, want string }{{"manual-state-0", dramaImageBound}, {"manual-state-2", dramaImageFailed}} {
		if err := svc.putDramaImageState("user-1", "asset-1", assetImageKey, dramaImageState{Status: dramaImageFailed, TaskID: tc.taskID, Error: "failure"}); err != nil {
			t.Fatal(err)
		}
		asset, err := svc.repo.AssetForUser("user-1", "asset-1")
		if err != nil {
			t.Fatal(err)
		}
		got := svc.assetImageSummary("user-1", asset, true)
		if got.Status != tc.want || (tc.want == dramaImageBound && (got.Error != "" || got.TaskID != "manual-state-1")) {
			t.Fatalf("summary for %s: %+v", tc.taskID, got)
		}
	}
}

func TestDramaImageAutomaticRetryCanSucceed(t *testing.T) {
	svc, db, project, template, calls := assetQueueFixture(t, func(w http.ResponseWriter, r *http.Request, call int32) {
		if call == 1 {
			http.Error(w, "temporary upstream error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"b64_json": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="}}})
	})
	queued, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template})
	if err != nil || len(queued.Queued) != 1 {
		t.Fatalf("queue: %+v %v", queued, err)
	}
	first, _ := runQueuedTask(t, svc)
	if first.Status != model.TaskStatusQueued {
		t.Fatalf("retry not queued: %+v", first)
	}
	done, _ := runQueuedTask(t, svc)
	if done.ID != first.ID || done.Status != model.TaskStatusSucceeded || calls.Load() != 2 {
		t.Fatalf("second attempt: %s %q calls=%d", done.Status, done.Error, calls.Load())
	}
	if states := assetStates(t, svc, project.ID); states["电梯口"].Status != dramaImageQueued || activeImageTasks(t, db) != 1 {
		t.Fatalf("success did not continue queue: %+v", states)
	}
	var videos int64
	db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&videos)
	if videos != 0 {
		t.Fatal("image retry created video task")
	}
}

func TestDramaImageAutomaticRetrySkipsRecoveryAndUncertainBilling(t *testing.T) {
	for _, reason := range []string{"media-recovery", "uncertain-billing"} {
		t.Run(reason, func(t *testing.T) {
			svc, db, project, template, calls := assetQueueFixture(t)
			queued, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template})
			if err != nil || len(queued.Queued) != 1 {
				t.Fatal(err)
			}
			task, err := svc.repo.Task(assetStates(t, svc, project.ID)["病房走廊"].TaskID)
			if err != nil {
				t.Fatal(err)
			}
			task.Status = model.TaskStatusFailed
			if reason == "media-recovery" {
				task.MediaRecoveryJSON = `{"pending":true}`
			} else {
				order := model.BillingOrder{ID: "uncertain-image", UserID: task.UserID, TaskID: task.ID, Status: model.BillingStatusUncertain}
				if err := db.Create(&order).Error; err != nil {
					t.Fatal(err)
				}
				task.BillingOrderID = order.ID
			}
			if err := db.Save(task).Error; err != nil {
				t.Fatal(err)
			}
			svc.afterDramaImageTaskEnded(*task)
			latest, _ := svc.repo.Task(task.ID)
			if latest.Status != model.TaskStatusFailed || calls.Load() != 0 {
				t.Fatalf("unsafe image was retried: %+v calls=%d", latest, calls.Load())
			}
		})
	}
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

	firstAttempt, _ := runQueuedTask(t, svc)
	if firstAttempt.Status != model.TaskStatusQueued || assetStates(t, svc, project.ID)["病房走廊"].AutoRetryCount != 1 {
		t.Fatalf("第一次失败应自动重试同一任务：%+v", firstAttempt)
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

	if last, _ := runQueuedTask(t, svc); last.Status != model.TaskStatusQueued {
		t.Fatalf("第三张第一次失败应重试：%s", last.Status)
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
		t.Fatalf("每张仅自动重试一次：共 %d 个出图任务，还在跑 %d 个", imageTasks, activeImageTasks(t, db))
	}
	if imageCalls.Load() != 4 {
		t.Fatalf("失败各调用两次，取消不调用：%d", imageCalls.Load())
	}
	svc.afterDramaImageTaskEnded(*failed)
	if activeImageTasks(t, db) != 0 {
		t.Fatal("重复结束回调不得再次自动重试")
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
