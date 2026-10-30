package app

import (
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func setProjectRunMode(t *testing.T, db *gorm.DB, projectID string, mode string) {
	t.Helper()
	if err := db.Model(&model.Project{}).Where("id = ?", projectID).Update("run_mode", mode).Error; err != nil {
		t.Fatal(err)
	}
}

func countTasks(t *testing.T, db *gorm.DB, where string, args ...any) int64 {
	t.Helper()
	var n int64
	query := db.Model(&model.Task{})
	if where != "" {
		query = query.Where(where, args...)
	}
	if err := query.Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// pipelineTestTextConfig 是接着建任务用的文本配置（假上游，任务不会真跑）。
var pipelineTestTextConfig = map[string]any{"baseUrl": "http://127.0.0.1:1", "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}

// insertPipelineStepTask 直接写一个流程步骤任务（成功或排队），只为让流水线认出「这一集提取过」之类的状态。
func insertPipelineStepTask(t *testing.T, db *gorm.DB, project model.Project, step string, unitID string, status model.TaskStatus) model.Task {
	t.Helper()
	metadata := map[string]any{"domainProjectId": project.ID, "dramaStep": map[string]any{"step": step}}
	if unitID != "" {
		metadata["chapterId"] = unitID
	}
	input, _ := json.Marshal(map[string]any{"mode": "text", "metadata": metadata, "config": pipelineTestTextConfig})
	task := model.Task{ID: newID(), UserID: "user-1", ProjectID: project.ID, Type: "canvas_text", Operation: "storyboard", Status: status, Model: "test", InputJSON: string(input), ResultJSON: "{}"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	return task
}

// 剧本写完之后按生成模式接：每步都问我不接；交给导演分析、还没锁定风格时接导演定画风（收尾直接锁定、存风格库）；
// 已锁定时接场次色卡（资产我要过目也接）；从风格中心挑的却没锁上时写明原因。
func TestContinueAfterScriptFollowsRunMode(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedEpisodeProject(t, svc, db)
	if _, err := svc.UpdateFeatureAvailability(&model.User{ID: "admin", Role: "admin"}, FeatureAvailability{CustomChannelsEnabled: true, TaskCenterEnabled: true}); err != nil {
		t.Fatal(err)
	}
	adapt := insertPipelineStepTask(t, db, project, drama.StepScriptAdapt, "", model.TaskStatusRunning)
	before := countTasks(t, db, "")

	setProjectRunMode(t, db, project.ID, drama.RunEveryStep)
	if chained := svc.continueAfterScript(adapt, project.ID); chained != nil || countTasks(t, db, "") != before {
		t.Fatalf("每步都问我不该接：%+v", chained)
	}

	setProjectRunMode(t, db, project.ID, drama.RunReviewFromAssets)
	chained := svc.continueAfterScript(adapt, project.ID)
	if chained == nil || chained.TaskID == "" || chained.Step != drama.StepScenePalette {
		t.Fatalf("资产我要过目：风格已锁定时接场次色卡：%+v", chained)
	}
	if err := db.Delete(&model.Task{}, "id = ?", chained.TaskID).Error; err != nil {
		t.Fatal(err)
	}

	// 还没锁定风格、交给导演分析：接导演定画风，带上建项时的描述。
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Updates(map[string]any{"bible_json": `{"characters":[]}`, "style_source": `{"mode":"director","brief":"冷调写实"}`, "run_mode": drama.RunUntilVideo}).Error; err != nil {
		t.Fatal(err)
	}
	chained = svc.continueAfterScript(adapt, project.ID)
	if chained == nil || chained.TaskID == "" || chained.Step != drama.StepStyleLock {
		t.Fatalf("交给导演分析：接导演定画风：%+v", chained)
	}
	lockTask, _ := svc.repo.Task(chained.TaskID)
	lock := taskDramaStyleLock(lockTask.InputJSON)
	if lock == nil || !lock.Pipeline || !lock.AutoLock || lock.Source != drama.StyleLockSourceDirector || lock.Text != "冷调写实" || lock.ProjectType != drama.VisualLiveAction {
		t.Fatalf("导演定画风的运行参数：%+v", lock)
	}
	if !strings.Contains(lockTask.Prompt, "【故事】") || !strings.Contains(lockTask.Prompt, "冷调写实") {
		t.Fatalf("导演定画风带剧本节选和描述：%s", lockTask.Prompt)
	}
	if err := db.Delete(&model.Task{}, "id = ?", chained.TaskID).Error; err != nil {
		t.Fatal(err)
	}

	// 从风格中心挑的却没锁上：不接，写明原因。
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("style_source", `{"mode":"library","styleId":"gone"}`).Error; err != nil {
		t.Fatal(err)
	}
	if chained = svc.continueAfterScript(adapt, project.ID); chained == nil || chained.TaskID != "" || chained.Error != errStyleNotLocked {
		t.Fatalf("没锁定风格时写明原因：%+v", chained)
	}
}

// 场次色卡写完后：跑到出视频前接着提取资产；资产我要过目、每步都问我在这里停。
func TestChainAssetsAfterScenePaletteFollowsRunMode(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedEpisodeProject(t, svc, db)
	if _, err := svc.UpdateFeatureAvailability(&model.User{ID: "admin", Role: "admin"}, FeatureAvailability{CustomChannelsEnabled: true, TaskCenterEnabled: true}); err != nil {
		t.Fatal(err)
	}
	palette := queueScenePaletteTask(t, svc, db, project, "http://127.0.0.1:1", DramaProjectStepRunRequest{})
	for _, mode := range []string{drama.RunReviewFromAssets, drama.RunEveryStep} {
		setProjectRunMode(t, db, project.ID, mode)
		if chained := svc.chainAssetsAfterScenePalette(*palette, project.ID); chained != nil {
			t.Fatalf("%s 不该接着提取资产：%+v", mode, chained)
		}
	}
	// 跑到出视频前接着提取第 1 集（测试数据没有场次色卡，提取任务建不成，原因记在 chained 上）。
	setProjectRunMode(t, db, project.ID, drama.RunUntilVideo)
	if chained := svc.chainAssetsAfterScenePalette(*palette, project.ID); chained == nil || chained.Step != drama.StepAssetsExtract || !strings.Contains(chained.Error, "场次色卡") {
		t.Fatalf("跑到出视频前接着提取第 1 集：%+v", chained)
	}
}

// 资产都齐了才开始生成分镜：还有集没提取、还有资产没图时等着；齐了按集依次生成；资产我要过目不接；
// 每一集都有镜头表时什么都不建——尤其不会建视频任务。
func TestContinueAfterAssetsStartsShotsOnlyWhenReady(t *testing.T) {
	svc, db, project, _, _ := assetQueueFixture(t)
	if _, err := svc.UpdateFeatureAvailability(&model.User{ID: "admin", Role: "admin"}, FeatureAvailability{CustomChannelsEnabled: true, TaskCenterEnabled: true}); err != nil {
		t.Fatal(err)
	}
	policy := defaultRuntimePolicy()
	if _, err := svc.UpdateRuntimePolicySetting(&model.User{ID: "admin", Role: model.UserRoleAdmin}, policy); err != nil {
		t.Fatal(err)
	}
	episodes, err := svc.repo.ProjectUnits(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	var eps []model.ProjectUnit
	for _, u := range episodes {
		if u.Kind == model.ProjectUnitKindEpisode {
			eps = append(eps, u)
		}
	}
	text := &DramaPipelineText{Model: "test", Config: pipelineTestTextConfig}
	shots := func() int64 { return countTasks(t, db, "input_json LIKE ?", `%"step":"chapter.shots"%`) }

	// 只提取了第 1 集（第 2 集正在收尾）：资产没图，不开始。
	insertPipelineStepTask(t, db, project, drama.StepAssetsExtract, eps[0].ID, model.TaskStatusSucceeded)
	finishing := insertPipelineStepTask(t, db, project, drama.StepAssetsExtract, eps[1].ID, model.TaskStatusRunning)
	if chained := svc.continueAfterAssets("user-1", project.ID, text, &finishing); chained != nil || shots() != 0 {
		t.Fatalf("资产还没出图时不该开始生成分镜：%+v", chained)
	}
	// 资产都有图了：从第 1 集开始，第 2 集排在队列里。
	if err := db.Model(&model.Asset{}).Where("id LIKE ?", "asset-%").Update("kind", "image").Error; err != nil {
		t.Fatal(err)
	}
	setProjectRunMode(t, db, project.ID, drama.RunReviewFromAssets)
	if chained := svc.continueAfterAssets("user-1", project.ID, text, &finishing); chained != nil || shots() != 0 {
		t.Fatalf("资产我要过目不该接着生成分镜：%+v", chained)
	}
	setProjectRunMode(t, db, project.ID, drama.RunUntilVideo)
	chained := svc.continueAfterAssets("user-1", project.ID, text, &finishing)
	if chained == nil || chained.TaskID == "" || chained.UnitID != eps[0].ID || chained.Remaining != 1 {
		t.Fatalf("资产齐了从第 1 集生成分镜：%+v", chained)
	}
	first, _ := svc.repo.Task(chained.TaskID)
	if meta := taskDramaPipeline(first.InputJSON); meta == nil || len(meta.Queue) != 1 || meta.Queue[0] != eps[1].ID {
		t.Fatalf("第 2 集排在队列里：%s", first.InputJSON)
	}
	// 已经有分镜在生成时不再开一串。
	if again := svc.continueAfterAssets("user-1", project.ID, text, nil); again != nil {
		t.Fatalf("已经在生成分镜时不该再开一串：%+v", again)
	}

	// 两集都有镜头表了：接下一集时什么都不建，也不会建视频任务。
	for _, ep := range eps {
		if err := db.Create(&model.DramaShotlist{ID: newID(), ProjectID: project.ID, UnitID: ep.ID, Version: 1, ShotlistJSON: "{}", CheckJSON: "{}"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&model.Task{}).Where("id = ?", first.ID).Update("status", model.TaskStatusSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	before := countTasks(t, db, "")
	if next := svc.continueShotsQueue(*first, project.ID); next != nil {
		t.Fatalf("队列里的集都有镜头表了不该再建：%+v", next)
	}
	if again := svc.continueAfterAssets("user-1", project.ID, text, nil); again != nil {
		t.Fatalf("每一集都有分镜了不该再建：%+v", again)
	}
	if countTasks(t, db, "") != before || countTasks(t, db, "type = ?", "canvas_video") != 0 {
		t.Fatal("流水线不能自动建视频任务")
	}
}

// 进度里带流水线（默认跑到出视频前）；「继续」要先给文本模型；任何模式都不自动出视频。
func TestDramaProgressCarriesPipeline(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedEpisodeProject(t, svc, db)
	progress, err := svc.DramaProgress("user-1", project.ID)
	if err != nil || progress.Pipeline == nil {
		t.Fatalf("进度里带流水线：%v", err)
	}
	if progress.Pipeline.RunMode != drama.RunUntilVideo || progress.Pipeline.Current == "" {
		t.Fatalf("流水线：%+v", progress.Pipeline)
	}
	// 没给文本模型：先报要文本模型（不会建任务）。
	if _, err := svc.DramaPipelineNext("user-1", project.ID, DramaPipelineNextRequest{}); err == nil {
		t.Fatal("没给文本模型应拒绝")
	}
	for _, mode := range drama.RunModes {
		if drama.ShouldAutoContinue(mode, drama.StageVideo) {
			t.Fatalf("%s 不能自动出视频", mode)
		}
	}
	if countTasks(t, db, "type = ?", "canvas_video") != 0 {
		t.Fatal("不该有视频任务")
	}
}
