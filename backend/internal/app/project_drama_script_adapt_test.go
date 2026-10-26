package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// seedNovelProject 建一个导入了三章小说的项目（画风取七床评测数据，生成分段要用）。
func seedNovelProject(t *testing.T, svc *Service, db *gorm.DB) (model.Project, []model.ProjectUnit) {
	t.Helper()
	files, err := drama.ReadEvalChapter("qichuang-e01")
	if err != nil {
		t.Fatal(err)
	}
	var seed model.Project
	if err := json.Unmarshal(files.Project, &seed); err != nil {
		t.Fatal(err)
	}
	project, err := svc.CreateProject("user-1", CreateProjectRequest{Name: "小说改编", SourceType: "novel", StylePresetID: seed.StylePresetID, StyleProfileJSON: seed.StyleProfileJSON, GenerationModel: "seedance-2.5"})
	if err != nil {
		t.Fatal(err)
	}
	brief := `{"title": "小说改编", "episode_seconds": 90, "segment_seconds": 15}`
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("brief_json", brief).Error; err != nil {
		t.Fatal(err)
	}
	project.BriefJSON = brief
	chapters := []CreateProjectUnitRequest{
		{Kind: "chapter", Title: "第一章 七床空了", SourceText: plainTextToHTML("夜班的走廊总是比白天长。\n七床的门虚掩着。\n“七床？”她叫了一声。")},
		{Kind: "chapter", Title: "第二章 电梯口", SourceText: plainTextToHTML("电梯门正在合上。\n走出来的人穿着病号服。\n“小满。”周岸说。")},
		{Kind: "chapter", Title: "第三章 刀", SourceText: plainTextToHTML("防火门被撞开。\n黑衣人冲了出来。\n刀掉在地上。\n“现在信了？”")},
	}
	units, err := svc.ImportProjectUnits("user-1", project.ID, ImportProjectUnitsRequest{Units: chapters})
	if err != nil {
		t.Fatal(err)
	}
	return project, units
}

// queueScriptAdaptTask 按 RunDramaProjectStep 的形状建一个剧本优化任务（自定义渠道指向假上游）。
func queueScriptAdaptTask(t *testing.T, svc *Service, db *gorm.DB, project model.Project, upstream string, req DramaProjectStepRunRequest) *model.Task {
	t.Helper()
	step, _ := drama.StepByID(drama.StepScriptAdapt)
	plan, err := svc.planScriptAdapt(&project, step, req)
	if err != nil {
		t.Fatal(err)
	}
	taskReq := dramaStepTaskRequest(project.ID, "", plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": upstream, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = newDramaRunMeta(plan, "")
	return insertDramaTestTask(t, svc, db, project.ID, taskReq)
}

func episodeLines(no int, title string, sources string, body ...string) []string {
	return append([]string{fmt.Sprintf(`{"no": %d, "title": %q, "sources": %s}`, no, title, sources)}, body...)
}

// 三章小说改编成两集：一个任务两轮、收尾建两个 episode 单元（来源注释、主要来源、排在章节之后）；
// 剧集上的生成分段按场次头切场、台词表读得出台词；重跑要确认替换；只调节奏原地改写、来源不变。
func TestScriptAdaptWritesEpisodesThroughWorker(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, chapters := seedNovelProject(t, svc, db)
	c1, c2, c3 := chapters[0].ID, chapters[1].ID, chapters[2].ID
	upstream := &streamingShotsUpstream{scenes: map[string][]string{
		"E01S01": episodeLines(1, "七床空了", fmt.Sprintf(`[{"unitId": %q, "from": 1, "to": 3}, {"unitId": %q, "from": 1, "to": 1}]`, c1, c2),
			"## 内·病房走廊·夜", "（夜里的病房走廊，日光灯一盏接一盏。）", "林小满：七床？", "## 内·电梯口·夜", "周岸：小满。"),
		"E01S02": episodeLines(2, "现在信了", fmt.Sprintf(`[{"unitId": %q, "from": 2, "to": 3}, {"unitId": %q, "from": 1, "to": 4}]`, c2, c3),
			"## 内·电梯口·夜", "（防火门被撞开，黑衣人冲出来。）", "周岸（压低声音）：退后！", "周岸：现在信了？"),
	}}
	server := httptest.NewServer(upstream)
	defer server.Close()

	queued := queueScriptAdaptTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{TargetEpisodes: 2})
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("剧本优化：%v %s %q", err, done.Status, done.Error)
	}
	if upstream.calls != 2 || upstream.histories[1] != 2 {
		t.Fatalf("一集一轮、第二轮带第一轮对话：%d %v", upstream.calls, upstream.histories)
	}
	var result struct {
		DramaStep DramaScriptAdaptOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(done.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	outcome := result.DramaStep
	// 写完剧集自动接着建导演风格任务（测试里没有计费用的渠道模型，建任务被拒，原因记在 chained 上，剧集照写）。
	if outcome.Chained == nil || outcome.Chained.Step != drama.StepStyleBible || (outcome.Chained.TaskID == "" && outcome.Chained.Error == "") {
		t.Fatalf("自动接着跑导演风格：%+v", outcome.Chained)
	}
	if outcome.Step != drama.StepScriptAdapt || outcome.Status != DramaStepOutcomeWritten || len(outcome.Episodes) != 2 || outcome.Errors != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.Episodes[0].ParentID != c1 || outcome.Episodes[1].ParentID != c3 || outcome.Episodes[1].Scenes != 1 || len(outcome.Episodes[1].Sources) != 2 {
		t.Fatalf("主要来源按段数：%+v", outcome.Episodes)
	}
	units, err := svc.repo.ProjectUnits(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 5 {
		t.Fatalf("三章原文 + 两集：%d", len(units))
	}
	byPos := map[int]model.ProjectUnit{}
	for _, u := range units {
		byPos[u.Position] = u
	}
	ep1, ep2 := byPos[3], byPos[4]
	if byPos[0].ID != c1 || ep1.Kind != model.ProjectUnitKindEpisode || ep2.Kind != model.ProjectUnitKindEpisode || ep1.Title != "七床空了" || ep1.ID != outcome.Episodes[0].UnitID {
		t.Fatalf("剧集排在章节之后：%+v", byPos)
	}
	if !strings.HasPrefix(ep1.SourceText, fmt.Sprintf("<!-- sources: %s:1-3;%s:1-1 --><p>## 内·病房走廊·夜</p>", c1, c2)) || ep1.WordCount == 0 || ep1.Status != model.ProjectUnitStatusReady {
		t.Fatalf("剧集正文：%+v", ep1)
	}

	// 下游原样可用：剧集上的生成分段按场次头逐场，台词表读得出台词。
	plan, err := svc.planDramaStep("user-1", &project, ep1.ID, drama.StepChapterShots)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.scenes) != 2 || plan.scenes[1].Heading != "内·电梯口·夜" || strings.Contains(plan.prompt.Text, "sources:") {
		t.Fatalf("剧集按场切：%+v", plan.scenes)
	}
	if script := drama.ScriptFromText(chapterPlainText(ep2.SourceText), ep2.Title); script.LineCount() != 2 || script.Scenes[0].Beats[1].Speaker != "周岸" {
		t.Fatalf("台词表：%+v", script)
	}
	view, err := svc.DramaEpisodes("user-1", project.ID)
	if err != nil || len(view.Episodes) != 2 || len(view.AdaptedChapterIDs) != 3 || len(view.Episodes[0].Sources) != 2 {
		t.Fatalf("剧集与来源：%v %+v", err, view)
	}
	progress, err := svc.DramaProgress("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range progress.EndStates {
		if e.Key == "script" && (!e.Done || e.Detail != "2 集剧本") {
			t.Fatalf("剧本定了：%+v", e)
		}
	}

	// 不计费时接着建的导演风格任务用同一文本模型，metadata.chainedFrom 指向剧本优化任务，定风格的是新写的两集。
	if _, err := svc.UpdateFeatureAvailability(&model.User{ID: "admin", Role: "admin"}, FeatureAvailability{CustomChannelsEnabled: true, TaskCenterEnabled: true}); err != nil {
		t.Fatal(err)
	}
	chained := svc.chainStyleBibleAfterAdapt(*queued, project.ID)
	if chained == nil || chained.TaskID == "" {
		t.Fatalf("接着建导演风格任务：%+v", chained)
	}
	styleTask, err := svc.repo.Task(chained.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if meta := taskDramaStyle(styleTask.InputJSON); meta == nil || meta.ChainedFrom != queued.ID || len(meta.Episodes) != 2 || meta.Episodes[0].UnitID != ep1.ID || !strings.Contains(styleTask.InputJSON, `"chainedFrom":"`+queued.ID+`"`) {
		t.Fatalf("导演风格任务：%s", styleTask.InputJSON)
	}
	if err := db.Delete(&model.Task{}, "id = ?", styleTask.ID).Error; err != nil {
		t.Fatal(err)
	}

	// 都改编过了：默认没有可选章节；点名第一章要先确认替换引用了它的第 1 集。
	step, _ := drama.StepByID(drama.StepScriptAdapt)
	if _, err := svc.planScriptAdapt(&project, step, DramaProjectStepRunRequest{}); err == nil || !strings.Contains(err.Error(), "没有还没改编的原文章节") {
		t.Fatalf("默认来源：%v", err)
	}
	_, err = svc.planScriptAdapt(&project, step, DramaProjectStepRunRequest{SourceUnitIDs: []string{c1}})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Reason != ReasonDramaEpisodesReplace {
		t.Fatalf("重跑要确认：%v", err)
	}
	if impacts := appErr.Details["episodes"].([]DramaEpisodeImpact); len(impacts) != 1 || impacts[0].UnitID != ep1.ID {
		t.Fatalf("影响面：%+v", appErr.Details)
	}
	if _, err := svc.planScriptAdapt(&project, step, DramaProjectStepRunRequest{SourceUnitIDs: []string{ep1.ID}}); err == nil || !strings.Contains(err.Error(), "不是原文章节") {
		t.Fatalf("小说转剧本只能选原文：%v", err)
	}

	// 只调节奏：两集原地改写，来源注释保留，集数和单元不变。
	upstream.calls, upstream.histories = 0, nil
	upstream.scenes = map[string][]string{
		"E01S01": episodeLines(1, "七床空了", "[]", "## 内·病房走廊·夜", "林小满：七床？人呢？"),
		"E01S02": episodeLines(2, "现在信了", "[]", "## 内·电梯口·夜", "周岸：退后！"),
	}
	queueScriptAdaptTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{Mode: drama.ScriptAdaptModeRhythm})
	rhythm, err := runQueuedTask(t, svc)
	if err != nil || rhythm.Status != model.TaskStatusSucceeded {
		t.Fatalf("只调节奏：%v %s %q", err, rhythm.Status, rhythm.Error)
	}
	after, err := svc.repo.ProjectUnit(project.ID, ep1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(after.SourceText, fmt.Sprintf("<!-- sources: %s:1-3;%s:1-1 --><p>## 内·病房走廊·夜</p><p>林小满：七床？人呢？</p>", c1, c2)) || after.Title != "七床空了" {
		t.Fatalf("原地改写：%s", after.SourceText)
	}
	if units, _ := svc.repo.ProjectUnits(project.ID); len(units) != 5 {
		t.Fatalf("调节奏不增减单元：%d", len(units))
	}
}
