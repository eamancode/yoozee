package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// 重试的旧提取保留原创建时间：项目里再多历史任务，也要看得见它在跑（R7-P1-01）。
func TestActiveExtractionVisibleBeyondRecentHistory(t *testing.T) {
	svc, db, project, _ := newProjectDramaTestService(t, "seedance-2.5")
	now := time.Now()
	input := `{"metadata":{"dramaStep":{"step":"` + drama.StepAssetsExtract + `"},"domainProjectId":"` + project.ID + `"}}`
	old := model.Task{ID: "old-retried-extraction", UserID: "user-1", ProjectID: project.ID, Type: "canvas_text", Status: model.TaskStatusRunning, InputJSON: input, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	history := make([]model.Task, 0, 2000)
	for i := 0; i < 2000; i++ {
		history = append(history, model.Task{ID: fmt.Sprintf("history-%04d", i), UserID: "user-1", ProjectID: project.ID, Type: "canvas_text", Status: model.TaskStatusSucceeded, InputJSON: input, CreatedAt: now, UpdatedAt: now})
	}
	if err := db.CreateInBatches(history, 20).Error; err != nil {
		t.Fatal(err)
	}
	if !svc.otherAssetsExtractActive("user-1", project.ID, "finishing-extraction") {
		t.Fatal("a running old extraction hidden behind 2000 newer tasks must still defer images")
	}
}

// ontologyTestResult 是一份 n 个单元、每个 seconds 秒（最后一个 last 秒，0 表示同 seconds）的本体论任务结果。
func ontologyTestResult(n, seconds, last int) map[string]interface{} {
	var plan strings.Builder
	plan.WriteString("## 一、本集定位\n测试。\n\n## 二、台词对照表\n本集无\n\n## 三、节奏总表\n| 单元 | 所属场 |\n|---|---|\n")
	var names []string
	var units []dramaRunTurn
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("s01-u%d", i)
		names = append(names, name)
		fmt.Fprintf(&plan, "| %s | s01 |\n", name)
		sec := seconds
		if i == n && last > 0 {
			sec = last
		}
		units = append(units, dramaRunTurn{Ontology: drama.OntologyTurnUnit, Scene: name, Text: ontologyFakeCard(name, sec)})
	}
	turns := append([]dramaRunTurn{{Ontology: drama.OntologyTurnPlan, Text: plan.String()}, {Ontology: drama.OntologyTurnDirect, Text: "## 四、讲戏本\n测试。"}}, units...)
	turns = append(turns, dramaRunTurn{Ontology: drama.OntologyTurnCheck, Text: "## 六、本集交接卡\n测试。\n\n## 七、自检结果与待确认清单\n" + ontologyFakeCheckTable(names...)})
	return map[string]interface{}{"dramaRun": dramaRunProgress{Turns: turns}}
}

// 两次整集替换交错收尾（R7-P1-02）：B 先读到旧段后停住，A 再收尾。要删的旧段在同集锁内读，
// 最后段数与七部分一致、合计不超过每集上限，不会留下别的收尾写的多余段。
func TestOntologyWholeReplacementsSerializeRemoval(t *testing.T) {
	svc, db, project, unit := newProjectDramaTestService(t, "seedance-2.5")
	meta := &dramaOntologyMeta{Brief: drama.OntologyBrief{EpisodeSeconds: 60, MaxUnitSeconds: 15}}
	input, _ := json.Marshal(map[string]interface{}{"metadata": map[string]interface{}{"domainProjectId": project.ID, "chapterId": unit.ID}})
	apply := func(id string, result map[string]interface{}) error {
		_, err := svc.applyOntologyShotsTask(model.Task{ID: id, UserID: "user-1", ProjectID: project.ID, InputJSON: string(input)}, nil, meta, result)
		return err
	}
	if err := apply("initial", ontologyTestResult(6, 10, 0)); err != nil {
		t.Fatal(err)
	}
	read := make(chan struct{})
	resume := make(chan struct{})
	var intercepted atomic.Bool
	if err := db.Callback().Query().After("gorm:query").Register("test:pause-shot-read", func(tx *gorm.DB) {
		if tx.Statement.Table == "shots" && intercepted.CompareAndSwap(false, true) {
			close(read)
			<-resume
		}
	}); err != nil {
		t.Fatal(err)
	}
	doneB := make(chan error, 1)
	go func() { doneB <- apply("replacement-B", ontologyTestResult(6, 11, 0)) }()
	select {
	case <-read:
	case err := <-doneB:
		t.Fatalf("B finished before reading shots: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for B to read shots")
	}
	doneA := make(chan error, 1)
	go func() { doneA <- apply("replacement-A", ontologyTestResult(7, 10, 6)) }()
	time.Sleep(100 * time.Millisecond)
	close(resume)
	if errA, errB := <-doneA, <-doneB; errA != nil || errB != nil {
		t.Fatalf("A=%v B=%v", errA, errB)
	}
	views, err := svc.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	var total float64
	for _, v := range views {
		total += v.Spec.Dur
	}
	doc, err := svc.repo.LatestOntologyDocument(project.ID, unit.ID, model.OntologyDocEpisode)
	if err != nil {
		t.Fatal(err)
	}
	cards, err := drama.ParseOntologyUnitCards(doc.Content)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != len(cards) || total > drama.OntologyEpisodeCap(60)+1e-6 {
		t.Fatalf("persisted %d segments (%g s) but the document has %d cards; cap %g", len(views), total, len(cards), drama.OntologyEpisodeCap(60))
	}
}
