package app

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestSSSDesignBackfillPreservesStoryboard(t *testing.T) {
	s, db := newDramaWorkerTestService(t)
	p, _ := seedNovelProject(t, s, db)
	values := []model.ProjectUnit{{ID: "old-one", ProjectID: p.ID, Kind: model.ProjectUnitKindEpisode, Title: "第一集", SourceText: "旧的正文", Position: 5}, {ID: "old-two", ProjectID: p.ID, Kind: model.ProjectUnitKindEpisode, Title: "第二集", SourceText: "原有正文", Position: 6}}
	if err := db.Create(&values).Error; err != nil {
		t.Fatal(err)
	}
	step := mustStep(t, drama.StepScriptAdapt)
	req := DramaProjectStepRunRequest{Mode: "design", SourceUnitIDs: []string{"old-one", "old-two"}}
	if _, err := s.RunDramaProjectStep(p.UserID, p.ID, step.ID, req); err == nil {
		t.Fatal("原工作流不应允许补写设计")
	}
	if err := db.Model(&model.Project{}).Where("id = ?", p.ID).Update("workflow_id", drama.WorkflowSSSDrama).Error; err != nil {
		t.Fatal(err)
	}
	p.WorkflowID = drama.WorkflowSSSDrama
	shot := model.Shot{ID: "preserve", ProjectID: p.ID, UnitID: "old-one", Title: "旧分镜", CurrentRevisionID: "legacy-revision"}
	if err := db.Create(&shot).Error; err != nil {
		t.Fatal(err)
	}
	const preserved = "旧八部分，测试原文"
	if err := s.repo.AppendOntologyDocument(&model.ProjectOntologyDocument{ID: newID(), ProjectID: p.ID, UnitID: "old-one", Kind: model.OntologyDocEpisode, Content: preserved, CreatedBy: p.UserID, TaskID: "old-task"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.RunDramaProjectStep(p.UserID, p.ID, step.ID, req)
	var cause *AppError
	if !errors.As(err, &cause) || cause.Status != 409 || cause.Reason != ReasonDramaEpisodesReplace || cause.Details["mode"] != "design" {
		t.Fatalf("覆盖提示缺失: %v", err)
	}
	req.Replace = true
	lines := func(n int, title string) []string {
		return episodeLines(n, title, "[]", "## 内·市场·日", "阿婆：收着。", "## 第一部分　本集设计", "雨夜相遇", "## 第二部分　台词定稿表", "| 定稿台词 | 处理 |", "|---|---|", "| 收着。 | 保留 |")
	}
	up := &streamingShotsUpstream{scenes: map[string][]string{"E01S01": lines(1, "第一集"), "E01S02": lines(2, "第二集")}}
	server := httptest.NewServer(up)
	defer server.Close()
	queueScriptAdaptTask(t, s, db, p, server.URL, req)
	done, err := runQueuedTask(t, s)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("补写失败 %v %s", err, done.Error)
	}
	if up.calls != 2 {
		t.Fatal("补写额外运行了立意轮", up.calls)
	}
	for _, u := range values {
		design, err := s.repo.LatestOntologyDocument(p.ID, u.ID, model.OntologyDocDesign)
		if err != nil || !strings.Contains(design.Content, "收着。") {
			t.Fatal(u.ID, err)
		}
		updated, err := s.repo.ProjectUnit(p.ID, u.ID)
		if err != nil || !strings.Contains(updated.SourceText, "收着。") || strings.Contains(updated.SourceText, "台词定稿表") {
			t.Fatal("正文和设计未分离", err)
		}
	}
	still, err := s.repo.LatestOntologyDocument(p.ID, "old-one", model.OntologyDocEpisode)
	if err != nil || still.Version != 1 || still.Content != preserved {
		t.Fatal("改动了已有分镜文档", err)
	}
	shots, err := s.repo.ProjectUnitShots(p.ID, "old-one")
	if err != nil || len(shots) != 1 || shots[0].CurrentRevisionID != shot.CurrentRevisionID {
		t.Fatal("改动了已有段", err)
	}
}
