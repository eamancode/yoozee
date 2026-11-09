package app

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// 续跑：输入没变时轮数按实际进度定、必须调模型（不能被误判成只收尾而不建计费单）；剧本改过就拒绝；各轮都写完时才是只收尾。
// 另外锁定三个新接口对别人的项目返回 404（不是 500）。
func TestOntologyResumeUsesRealProgressAndRejectsChangedInputs(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	previous := dramaTurnBackoffBase
	dramaTurnBackoffBase = time.Millisecond
	t.Cleanup(func() { dramaTurnBackoffBase = previous })
	svc, db := newChapterShotsTestService(t)
	now := time.Now()
	project := model.Project{ID: "project-resume", UserID: "user-1", Name: "当铺续跑", Status: model.ProjectStatusActive, GenerationModel: "seedance-2.5", WorkflowID: drama.WorkflowOntology, WorkflowVersion: drama.WorkflowVersion, AspectRatio: "9:16", StyleProfileJSON: `{"prompt":"雨夜写实，冷暖对比"}`, Revision: 1, CreatedAt: now, UpdatedAt: now}
	unit := model.ProjectUnit{ID: "unit-r1", ProjectID: project.ID, Kind: model.ProjectUnitKindChapter, Title: "第一集", SourceText: "<p>林夏走进当铺。</p>", Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	plan, run, err := svc.prepareDramaStepRun("user-1", &project, unit.ID, drama.StepChapterShots, DramaStepRunRequest{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ontology == nil || !strings.Contains(plan.prompt.Text, "雨夜写实，冷暖对比") {
		t.Fatal("locked project style must be in the material package")
	}
	upstream := &fakeOntologyUpstream{failU2: true}
	server := httptest.NewServer(upstream.handler(t))
	defer server.Close()
	taskReq := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = run
	input, _ := json.Marshal(taskReq.Input)
	first := model.Task{ID: "task-first", UserID: "user-1", ProjectID: project.ID, Type: "canvas_text", Operation: "storyboard", Status: model.TaskStatusRunning, LeaseOwner: "worker-first", LeaseExpiresAt: ptr(now.Add(time.Hour)), InputJSON: string(input), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.runOntologyRun(dramaRunCtx(first), dramaRunInput(server.URL), run, taskDramaOntology(first.InputJSON)); err == nil {
		t.Fatal("u2 must fail")
	}
	if err := db.Model(&model.Task{}).Where("id = ?", first.ID).Update("status", model.TaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	// 立项、规划、讲戏、u1 写完：4 轮；节奏总表 2 个单元 → 实际总轮数 6。
	_, resumed, err := svc.prepareDramaStepRun("user-1", &project, unit.ID, drama.StepChapterShots, DramaStepRunRequest{ResumeTaskID: first.ID}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ResumeFrom != 5 || resumed.Turns != 6 || dramaRunCallsNoUpstream(&resumed) || dramaRunUpstreamCalls(&resumed) != 2 {
		t.Fatalf("resume must keep calling the model: from=%d turns=%d", resumed.ResumeFrom, resumed.Turns)
	}
	// 剧本改过：拒绝接着旧轮次写。
	if err := db.Model(&model.ProjectUnit{}).Where("id = ?", unit.ID).Update("source_text", "<p>林夏走进另一家当铺。</p>").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.prepareDramaStepRun("user-1", &project, unit.ID, drama.StepChapterShots, DramaStepRunRequest{ResumeTaskID: first.ID}, nil, nil); err == nil || !strings.Contains(err.Error(), "改过") {
		t.Fatalf("changed script must reject resume: %v", err)
	}
	// 只按小节重写不适用于本体论分镜。
	if _, _, err := svc.prepareDramaStepRun("user-1", &project, unit.ID, drama.StepChapterShots, DramaStepRunRequest{OnlySections: []string{"E01S01-P1"}}, nil, nil); err == nil || !strings.Contains(err.Error(), "返修") {
		t.Fatalf("only-sections must be rejected: %v", err)
	}

	// 各轮都写完的旧任务：续跑只收尾。
	meta := taskDramaOntology(first.InputJSON)
	out := ontologyOutputs{Setup: "x", Plan: "x", Direct: "x", Units: []string{"x", "x"}, Check: "x"}
	if _, done := ontologyNextTurn(meta, out, []string{"s01-u1", "s01-u2"}); !done {
		t.Fatal("all turns written must be done")
	}

	// 别人的项目：三个接口都是 404。
	for name, call := range map[string]func() error{
		"documents": func() error { _, err := svc.ProjectOntologyDocuments("intruder", project.ID); return err },
		"bible": func() error {
			_, err := svc.UpdateOntologyBible("intruder", project.ID, UpdateOntologyBibleRequest{Content: "x"})
			return err
		},
		"repair": func() error {
			_, err := svc.RepairOntologySegment("intruder", project.ID, unit.ID, OntologyRepairRequest{ShotID: "s", Symptom: "x"})
			return err
		},
	} {
		err := call()
		var appErr *AuthError
		if !errors.As(err, &appErr) || appErr.Status != 404 {
			t.Errorf("%s: want 404, got %v", name, err)
		}
	}
}
