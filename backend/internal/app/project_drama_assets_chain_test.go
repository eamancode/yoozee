package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// startAssetsChain 跑一个开了接续的场次色卡任务（template 不为 nil 时任务输入带资产出图模板），返回接着建的第 1 集提取任务。
func startAssetsChain(t *testing.T, svc *Service, db *gorm.DB, project model.Project, upstream string, template *DramaImageTaskTemplate) *model.Task {
	t.Helper()
	step, _ := drama.StepByID(drama.StepScenePalette)
	current, _ := svc.repo.ProjectForUser("user-1", project.ID)
	plan, err := svc.planScenePalette(current, step, DramaProjectStepRunRequest{})
	if err != nil {
		t.Fatal(err)
	}
	taskReq := dramaStepTaskRequest(project.ID, "", plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": upstream, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = newDramaRunMeta(plan, "")
	if template != nil {
		taskReq.Input["assetImageTask"] = template
	}
	insertDramaTestTask(t, svc, db, project.ID, taskReq)
	// 接着建的任务走 CreateTask：测试里没有计费用的渠道模型，关掉计费才建得成。
	if _, err := svc.UpdateFeatureAvailability(&model.User{ID: "admin", Role: "admin"}, FeatureAvailability{CustomChannelsEnabled: true, TaskCenterEnabled: true}); err != nil {
		t.Fatal(err)
	}
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("场次色卡：%v %q", err, done.Error)
	}
	chained := scenePaletteOutcomeOf(t, done).Chained
	if chained == nil || chained.TaskID == "" {
		t.Fatalf("色卡写完接着提取第 1 集：%+v", chained)
	}
	first, err := svc.repo.Task(chained.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	return first
}

// chainedTasksAfter 是接着 taskID 建的任务（metadata.chainedFrom）。
func chainedTasksAfter(t *testing.T, db *gorm.DB, taskID string) []model.Task {
	t.Helper()
	var tasks []model.Task
	if err := db.Where("input_json LIKE ?", `%"chainedFrom":"`+taskID+`"%`).Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	return tasks
}

func imageTaskCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&model.Task{}).Where("type = ?", "canvas_image").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

// 两集链式：第 1 集提取时上游 500，任务失败、原因留在任务上；接着照样提取第 2 集（metadata.chainedFrom 指向失败的第 1 集），
// 第 2 集带着色卡任务的出图模板给新资产排出图，模板里的密钥不以明文留在任务里。失败的第 1 集之后重试成功也不再重复接第 2 集。
func TestAssetsChainSkipsFailedEpisode(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, svc, db)
	ep2 := &streamingShotsUpstream{scenes: map[string][]string{"E01S01": {variantLinEp2, assetsInLift}}}
	broken := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"upstream boom"}}`, http.StatusInternalServerError)
	})
	server := httptest.NewServer((&routedUpstream{}).route("剧本：第 1 集", broken).route("剧本：第 2 集", ep2).route("场次色卡", scenePaletteUpstream()))
	defer server.Close()
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusServiceUnavailable) }))
	defer images.Close()
	template := &DramaImageTaskTemplate{Model: "queue-image", Input: map[string]any{"mode": "image", "config": map[string]any{
		"channelId": "", "baseUrl": images.URL, "apiKey": "image-secret-key", "model": "queue-image", "interfaceType": "openai-image", "apiFormat": "openai", "size": "1024x1024", "count": "1",
	}}}
	first := startAssetsChain(t, svc, db, project, server.URL, template)

	// runQueuedTask 对失败的任务返回 worker 的错误，这里只看任务状态。
	failed, _ := runQueuedTask(t, svc)
	if failed == nil || failed.ID != first.ID || failed.Status != model.TaskStatusFailed || !strings.Contains(failed.Error, "HTTP 500") {
		t.Fatalf("第 1 集提取失败、原因记在任务上：%+v", failed)
	}
	next := chainedTasksAfter(t, db, first.ID)
	if len(next) != 1 || !strings.Contains(next[0].InputJSON, `"chapterId":"`+episodes[1].ID+`"`) {
		t.Fatalf("第 1 集失败后接着提取第 2 集：%d", len(next))
	}
	if strings.Contains(next[0].InputJSON, "image-secret-key") {
		t.Fatal("接着建的提取任务里出图模板的密钥不能是明文")
	}
	done2, err := runQueuedTask(t, svc)
	if err != nil || done2.ID != next[0].ID || done2.Status != model.TaskStatusSucceeded {
		t.Fatalf("第 2 集提取：%v %q", err, done2.Error)
	}
	out2 := assetsOutcomeOf(t, done2)
	if out2.Created == 0 || len(out2.Images.Queued)+len(out2.Images.Waiting) == 0 || imageTaskCount(t, db) == 0 {
		t.Fatalf("第 2 集给新资产出图：%+v 出图任务 %d", out2.Images, imageTaskCount(t, db))
	}
	// 用户在任务中心重试失败的第 1 集：它已经接过第 2 集，成功后不再重复接。
	if again := svc.chainAfterAssetsExtract(*failed, project.ID, taskDramaAssets(failed.InputJSON)); again != nil {
		t.Fatalf("接过的不再接：%+v", again)
	}
}

// 两集链式：第 1 集被用户取消，整串停下，第 2 集不提取。
func TestAssetsChainStopsOnCancel(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedEpisodeProject(t, svc, db)
	ep1 := &streamingShotsUpstream{scenes: map[string][]string{"E01S01": {variantLinMain, assetsHall}, "E01S02": {variantLinWet, assetsLift}}}
	server := httptest.NewServer((&routedUpstream{}).route("剧本：第 1 集", ep1).route("场次色卡", scenePaletteUpstream()))
	defer server.Close()
	first := startAssetsChain(t, svc, db, project, server.URL, nil)
	cancelled, err := svc.CancelTask(context.Background(), "user-1", first.ID)
	if err != nil || cancelled.Status != model.TaskStatusCancelled {
		t.Fatalf("取消第 1 集：%v", err)
	}
	if next := chainedTasksAfter(t, db, first.ID); len(next) != 0 {
		t.Fatalf("取消后不接第 2 集：%d", len(next))
	}
	// worker 侧收尾成取消（上下文取消）也不接。
	latest, _ := svc.repo.Task(first.ID)
	svc.afterDramaTaskEnded(*latest)
	if next := chainedTasksAfter(t, db, first.ID); len(next) != 0 {
		t.Fatalf("取消的任务收尾后也不接：%d", len(next))
	}
}

// 场次色卡没带出图模板（剧本优化对话框关了出图）：链式提取完不排出图任务。
func TestAssetsChainWithoutImagesQueuesNoImageTasks(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedEpisodeProject(t, svc, db)
	ep1 := &streamingShotsUpstream{scenes: map[string][]string{"E01S01": {variantLinMain, assetsHall}, "E01S02": {variantLinWet, assetsLift}}}
	server := httptest.NewServer((&routedUpstream{}).route("剧本：第 1 集", ep1).route("场次色卡", scenePaletteUpstream()))
	defer server.Close()
	startAssetsChain(t, svc, db, project, server.URL, nil)
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("第 1 集提取：%v %q", err, done.Error)
	}
	if out := assetsOutcomeOf(t, done); out.Created == 0 || len(out.Images.Queued)+len(out.Images.Waiting) != 0 || imageTaskCount(t, db) != 0 {
		t.Fatalf("关了出图不排出图任务：%+v %d", out.Images, imageTaskCount(t, db))
	}
}

// 剧本优化对话框的「出图」开关：开着时请求带出图模板，开关状态记进 metadata.dramaAdapt.assetImages，模板加密存进任务输入，
// 写完剧集接着建的场次色卡任务带上它（各集提取接着出图）；关掉时不带模板，场次色卡也不带。
func TestScriptAdaptImageSwitchCarriesTemplate(t *testing.T) {
	for _, on := range []bool{true, false} {
		svc, db := newDramaWorkerTestService(t)
		project, _ := seedNovelProject(t, svc, db)
		if _, err := svc.UpdateFeatureAvailability(&model.User{ID: "admin", Role: "admin"}, FeatureAvailability{CustomChannelsEnabled: true, TaskCenterEnabled: true}); err != nil {
			t.Fatal(err)
		}
		req := DramaProjectStepRunRequest{Model: "test", Config: map[string]any{"baseUrl": "http://127.0.0.1:1", "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}}
		if on {
			req.AssetImageTask = &DramaImageTaskTemplate{Model: "queue-image", Input: map[string]any{"mode": "image", "config": map[string]any{"baseUrl": "http://127.0.0.1:1", "apiKey": "image-secret-key", "model": "queue-image", "interfaceType": "openai-image"}}}
		}
		result, err := svc.RunDramaProjectStep("user-1", project.ID, drama.StepScriptAdapt, req)
		if err != nil {
			t.Fatal(err)
		}
		adapt, _ := svc.repo.Task(result.Task.ID)
		if strings.Contains(adapt.InputJSON, "image-secret-key") {
			t.Fatal("剧本优化任务里出图模板的密钥不能是明文")
		}
		if got := strings.Contains(adapt.InputJSON, `"assetImages":true`); got != on {
			t.Fatalf("出图开关 %v 记进 metadata.dramaAdapt：%v", on, got)
		}
		lockTestProjectStyle(t, db, project.ID)
		chained := svc.continueAfterScript(*adapt, project.ID)
		if chained == nil || chained.TaskID == "" {
			t.Fatalf("接着建场次色卡：%+v", chained)
		}
		palette, _ := svc.repo.Task(chained.TaskID)
		decrypted, err := svc.decryptTaskInputJSON(palette.InputJSON)
		if err != nil {
			t.Fatal(err)
		}
		if got := taskAssetImageTemplate(decrypted) != nil; got != on || strings.Contains(palette.InputJSON, "image-secret-key") {
			t.Fatalf("出图开关 %v：场次色卡带模板 %v", on, got)
		}
	}
}
