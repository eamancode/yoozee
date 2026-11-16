package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestSSSProjectSetupAcrossEpisodes(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	s, db := newChapterShotsTestService(t)
	p := model.Project{ID: "setup", UserID: "user-1", Name: "测试", WorkflowID: drama.WorkflowSSSDrama, Status: model.ProjectStatusActive}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	step := mustStep(t, drama.StepDirectorSetup)
	if _, err := s.RunDramaProjectStep(p.UserID, p.ID, step.ID, DramaProjectStepRunRequest{}); err == nil || !strings.Contains(err.Error(), "尚无剧集") {
		t.Fatalf("缺少前置仍能立项: %v", err)
	}
	for _, kind := range []string{model.OntologyDocWorld, model.OntologyDocConcept} {
		if err := s.repo.AppendOntologyDocument(&model.ProjectOntologyDocument{ID: newID(), Kind: kind, ProjectID: p.ID, TaskID: "source-" + kind, CreatedBy: p.UserID, Content: "原始文档-" + kind}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		u := model.ProjectUnit{ID: fmt.Sprint("ep", i), ProjectID: p.ID, Kind: model.ProjectUnitKindEpisode, Position: i, Title: fmt.Sprintf("第%d集", i), SourceText: fmt.Sprintf("最终剧本内容%d", i)}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	plan, err := s.planDirectorSetup(p.UserID, &p, step, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.setup.Batches) != 1 || len(plan.setup.Basis) != 3 {
		t.Fatal("三集应合并一批", plan.setup.Batches)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) == 0 {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		prompt := body.Messages[len(body.Messages)-1].Content
		for _, key := range []string{"最终剧本内容1", "最终剧本内容2", "最终剧本内容3", "原始文档-world", "原始文档-concept"} {
			if !strings.Contains(prompt, key) {
				t.Errorf("立项缺少 %s", key)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "## 全剧圣经\n全部剧集共同基准\n## 待确认\n道具样式"}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	run := newDramaRunMeta(plan, "")
	req := dramaStepTaskRequest(p.ID, "", plan, dramaStepTaskOptions{})
	req.Input["metadata"].(map[string]any)["dramaRun"] = run
	raw, _ := json.Marshal(req.Input)
	now := time.Now()
	task := model.Task{ID: "all-episodes", Type: "canvas_text", Operation: "storyboard", UserID: p.UserID, ProjectID: p.ID, Status: model.TaskStatusRunning, LeaseOwner: "local", LeaseExpiresAt: ptr(now.Add(time.Hour)), InputJSON: string(raw), CreatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	result, err := s.runDramaRun(dramaRunCtx(task), dramaRunInput(server.URL), run)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.finalizeDramaStepTask(task, result); err != nil || !ok {
		t.Fatal(ok, err)
	}
	docs, err := s.ProjectOntologyDocuments(p.UserID, p.ID)
	if err != nil || docs.Bible == nil || docs.Setup == nil || docs.BibleBasis.Episodes != 3 || docs.BibleBasis.Outdated {
		t.Fatal("立项文档和依据", err, docs.BibleBasis)
	}
	if err := db.Model(&model.ProjectUnit{}).Where("id = ?", "ep2").Update("source_text", "第二集已改").Error; err != nil {
		t.Fatal(err)
	}
	docs, err = s.ProjectOntologyDocuments(p.UserID, p.ID)
	if err != nil || !docs.BibleBasis.Outdated {
		t.Fatal("正文已改，依据未提示过时", err)
	}
	extra := model.ProjectUnit{ID: "extra", ProjectID: p.ID, Kind: model.ProjectUnitKindEpisode, Position: 4, Title: "第四集", SourceText: "另一天"}
	if err := db.Create(&extra).Error; err != nil {
		t.Fatal(err)
	}
	docs, err = s.ProjectOntologyDocuments(p.UserID, p.ID)
	if err != nil || docs.BibleBasis.Total != 4 || docs.BibleBasis.Episodes != 3 || !docs.BibleBasis.Outdated {
		t.Fatal("新增一集后依据集数错误", err, docs.BibleBasis)
	}
	legacy := insertPipelineStepTask(t, db, p, drama.StepChapterShots, "ep1", model.TaskStatusSucceeded)
	if err := s.repo.AppendOntologyDocument(&model.ProjectOntologyDocument{ID: newID(), ProjectID: p.ID, CreatedBy: p.UserID, TaskID: legacy.ID, Kind: model.OntologyDocBible, Content: "旧版单集圣经"}); err != nil {
		t.Fatal(err)
	}
	docs, err = s.ProjectOntologyDocuments(p.UserID, p.ID)
	if err != nil || !docs.BibleBasis.Legacy || docs.BibleBasis.Episodes != 1 || docs.BibleBasis.Total != 4 {
		t.Fatal("旧编排的圣经依据错误", err, docs.BibleBasis)
	}
}

func TestSSSSetupBatchHonorsPreviousBibleBudget(t *testing.T) {
	m := directorSetupMeta{Context: strings.Repeat("文", sssMaxPromptRunes-1200), Basis: []string{"1", "2", "3", "4", "5"}, Scripts: map[string]string{}}
	for _, id := range m.Basis {
		m.Scripts[id] = id + "集\n" + strings.Repeat("剧", 430)
	}
	first, err := m.remainingBatches(0, "")
	if err != nil || len(first) != 3 || len(first[0]) != 2 {
		t.Fatal(first, err)
	}
	prev := "## 全剧圣经\n" + strings.Repeat("前", 280) + "\n## 待确认\n无"
	next, err := m.remainingBatches(2, prev)
	if err != nil || len(next) != 3 || len(next[0]) != 1 {
		t.Fatal("未按实际圣经计算批量", next, err)
	}
	m.Batches = append(first[:1], next...)
	prompt, err := m.prompt(2, []dramaRunTurn{{Text: prev}})
	if err != nil || !strings.Contains(prompt, "增补回合") || !strings.Contains(prompt, strings.Repeat("前", 280)) || len([]rune(prompt)) > sssMaxPromptRunes {
		t.Fatal("增补批输入错误", err)
	}
	m.Scripts["3"] = "第三集\n" + strings.Repeat("超", 2000)
	if _, err = m.remainingBatches(2, prev); err == nil || !strings.Contains(err.Error(), "第三集") {
		t.Fatal("单集超额未报出集名", err)
	}
}

func TestSSSMultiBatchSetupResumesFromSavedBible(t *testing.T) {
	s, db := newDramaWorkerTestService(t)
	project, units := seedEpisodeProject(t, s, db)
	if _, err := s.UpdateFeatureAvailability(&model.User{ID: "admin", Role: model.UserRoleAdmin}, FeatureAvailability{CustomChannelsEnabled: true, TaskCenterEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("workflow_id", drama.WorkflowSSSDrama).Error; err != nil {
		t.Fatal(err)
	}
	project.WorkflowID = drama.WorkflowSSSDrama
	for _, kind := range []string{model.OntologyDocWorld, model.OntologyDocConcept} {
		if err := s.repo.AppendOntologyDocument(&model.ProjectOntologyDocument{ID: newID(), Kind: kind, ProjectID: project.ID, TaskID: newID(), CreatedBy: project.UserID, Content: "医院"}); err != nil {
			t.Fatal(err)
		}
	}
	shortPlan, err := s.planDirectorSetup(project.UserID, &project, mustStep(t, drama.StepDirectorSetup), nil)
	if err != nil {
		t.Fatal(err)
	}
	// 长度取实际技能和材料剩余空间的四成，使常规批次容纳两集。
	scriptLength := (sssMaxPromptRunes - len([]rune(shortPlan.setup.Context)) - 1000) * 4 / 10
	for len(units) < 5 {
		u := model.ProjectUnit{ID: newID(), ProjectID: project.ID, Kind: model.ProjectUnitKindEpisode, Position: 20 + len(units), Title: fmt.Sprintf("长剧第%d集", len(units)+1)}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		units = append(units, u)
	}
	for i, u := range units {
		text := fmt.Sprintf("定稿标识-%d\n", i+1) + strings.Repeat("景", scriptLength)
		if err := db.Model(&u).Update("source_text", text).Error; err != nil {
			t.Fatal(err)
		}
	}
	var interrupted atomic.Bool
	interrupted.Store(true)
	var batches [][]int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) == 0 {
			t.Errorf("无法解析文本请求: %v", err)
			w.WriteHeader(400)
			return
		}
		prompt := request.Messages[len(request.Messages)-1].Content
		var ids []int
		for i := 1; i <= 5; i++ {
			if strings.Contains(prompt, fmt.Sprintf("定稿标识-%d", i)) {
				ids = append(ids, i)
			}
		}
		if len(ids) == 0 {
			t.Error("立项漏掉本批剧本")
			w.WriteHeader(400)
			return
		}
		if interrupted.Load() && ids[0] > 1 {
			http.Error(w, "fixture interruption", http.StatusUnauthorized)
			return
		}
		if len(batches) > 0 {
			previous := batches[len(batches)-1]
			if !strings.Contains(prompt, fmt.Sprintf("已覆盖到第%d集", previous[len(previous)-1])) || !strings.Contains(prompt, "增补回合") {
				t.Error("增补未使用紧邻上一批的圣经")
			}
		}
		batches = append(batches, ids)
		w.Header().Set("Content-Type", "application/json")
		reply := fmt.Sprintf("## 全剧圣经\n已覆盖到第%d集\n## 待确认\n无", ids[len(ids)-1])
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": reply}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	req := DramaProjectStepRunRequest{Model: "test", Config: map[string]any{"baseUrl": server.URL, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}}
	first, err := s.RunDramaProjectStep(project.UserID, project.ID, drama.StepDirectorSetup, req)
	if err != nil {
		t.Fatal(err)
	}
	failed, _ := runQueuedTask(t, s)
	prog := taskDramaRunProgress(failed.ResultJSON)
	if failed.Status != model.TaskStatusFailed || prog == nil || prog.Completed != 1 || prog.Total != 3 {
		t.Fatalf("第二批失败后没有保留第一批检查点: status=%s error=%s progress=%v", failed.Status, failed.Error, prog != nil)
	}
	interrupted.Store(false)
	req.ResumeTaskID = first.Task.ID
	if _, err := s.RunDramaProjectStep(project.UserID, project.ID, drama.StepDirectorSetup, req); err != nil {
		t.Fatal(err)
	}
	done, err := runQueuedTask(t, s)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("继续立项失败: %v %s", err, done.Error)
	}
	if len(batches) != 3 || !slices.Equal(batches[0], []int{1, 2}) || !slices.Equal(batches[1], []int{3, 4}) || !slices.Equal(batches[2], []int{5}) {
		t.Fatalf("检查点续跑重复或遗漏剧集: %v", batches)
	}
	docs, err := s.ProjectOntologyDocuments(project.UserID, project.ID)
	if err != nil || docs.Bible == nil || docs.BibleBasis.Episodes != 5 || !strings.Contains(docs.Bible.Content, "已覆盖到第5集") {
		t.Fatal("最后一批圣经未成为全剧依据", err)
	}
}

func TestSSSPipelineStartsSetupBeforeFirstStoryboard(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	s, db, project, _, _ := assetQueueFixture(t)
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	if _, err := s.UpdateFeatureAvailability(admin, FeatureAvailability{CustomChannelsEnabled: true, TaskCenterEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Updates(map[string]any{"workflow_id": drama.WorkflowSSSDrama, "run_mode": drama.RunEveryStep}).Error; err != nil {
		t.Fatal(err)
	}
	project.WorkflowID = drama.WorkflowSSSDrama
	palette := httptest.NewServer(scenePaletteUpstream())
	defer palette.Close()
	queueScenePaletteTask(t, s, db, project, palette.URL, DramaProjectStepRunRequest{})
	if done, err := runQueuedTask(t, s); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("色卡夹具失败: %v %+v", err, done)
	}
	if err := db.Model(&model.Asset{}).Where("id LIKE ?", "asset-%").Update("kind", "image").Error; err != nil {
		t.Fatal(err)
	}
	write := func(kind, unit, content string) {
		t.Helper()
		if err := s.repo.AppendOntologyDocument(&model.ProjectOntologyDocument{ID: newID(), TaskID: newID(), ProjectID: project.ID, UnitID: unit, CreatedBy: project.UserID, Kind: kind, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	write(model.OntologyDocConcept, "", "立意与结构")
	write(model.OntologyDocWorld, "", "基准与总谱")
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	var episodes []string
	for _, u := range units {
		if u.Kind == model.ProjectUnitKindEpisode {
			episodes = append(episodes, u.ID)
			insertPipelineStepTask(t, db, project, drama.StepAssetsExtract, u.ID, model.TaskStatusSucceeded)
			write(model.OntologyDocDesign, u.ID, "## 第一部分 本集设计\n追问\n## 第二部分 台词定稿表\n无")
		}
	}
	if len(episodes) != 2 {
		t.Fatal("预期两集", episodes)
	}
	setupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "## 全剧圣经\n测试医院\n## 待确认\n无"}, "finish_reason": "stop"}}})
	}))
	defer setupServer.Close()
	config := map[string]any{"baseUrl": setupServer.URL, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}
	out, err := s.DramaPipelineNext(project.UserID, project.ID, DramaPipelineNextRequest{Model: "test", Config: config})
	if err != nil || out.Task == nil || taskDirectorSetup(out.Task.InputJSON) == nil {
		t.Fatalf("分镜前没有先立项: stage=%s err=%v", out.Stage, err)
	}
	if countTasks(t, db, "input_json LIKE ?", `%"step":"chapter.shots"%`) != 0 {
		t.Fatal("立项运行中提前建了分镜任务")
	}
	done, err := runQueuedTask(t, s)
	if err != nil || done.Status != model.TaskStatusSucceeded || done.ID != out.Task.ID {
		t.Fatalf("单任务名额下的立项没有成功: %v %+v", err, done)
	}
	var result struct {
		Step DramaStepTaskOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(done.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	chained := result.Step.Chained
	if chained == nil || chained.TaskID == "" || chained.UnitID != episodes[0] || chained.Remaining != 1 {
		t.Fatalf("立项成功后未按顺序开始分镜: %+v", chained)
	}
	s.afterSSSQueueTask(done, out.Task.InputJSON)
	if countTasks(t, db, "input_json LIKE ?", `%"step":"chapter.shots"%`) != 1 {
		t.Fatal("重复收尾新建了第二个分镜任务")
	}
}
