package app

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestSpeechExtractWorkerUsesIndependentSkillAndPersistsCompleteLedger(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, units := seedEpisodeProject(t, svc, db)
	unit := units[0]
	upstream := &streamingShotsUpstream{scenes: map[string][]string{"E01S01": {
		`{"scene":"E01S01","paragraph":2,"speaker":"林小满","kind":"dialogue","text":"七床？"}`,
		`{"scene":"E01S02","paragraph":1,"speaker":"周岸","kind":"dialogue","text":"小满。"}`,
	}}}
	server := httptest.NewServer(upstream)
	defer server.Close()
	plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepSpeechExtract, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.usage.Skill.ID != "yz-speech-extract" {
		t.Fatalf("wrong independent skill: %+v", plan.usage)
	}
	req := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": server.URL, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	req.Input["metadata"].(map[string]any)["dramaRun"] = newDramaRunMeta(plan, unit.ID)
	insertDramaTestTask(t, svc, db, project.ID, req)
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("speech worker: %v %+v", err, done)
	}
	current, err := svc.repo.ProjectUnit(project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	ledger := unitSpeechLedger(current)
	if ledger == nil || len(ledger.Entries) != 2 || ledger.Script.LineCount() != 2 || ledger.SkillVersion == "" {
		t.Fatalf("ledger not persisted: %+v", ledger)
	}
	if countTasks(t, db, "type = ?", "canvas_video") != 0 {
		t.Fatal("speech must not invoke video")
	}
}

func TestSpeechLedgerPersistenceRejectsStaleSourceAndOtherOwner(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, units := seedEpisodeProject(t, svc, db)
	unit := units[0]
	ledger := drama.SpeechLedger{SourceHash: speechSourceHash(unit.SourceText), Entries: []drama.SpeechEntry{}}
	raw, _ := json.Marshal(ledger)
	if err := svc.repo.SaveUnitSpeechLedger("user-2", project.ID, unit.ID, unit.SourceText, string(raw)); err == nil {
		t.Fatal("cross owner ledger write accepted")
	}
	if err := svc.repo.SaveUnitSpeechLedger("user-1", project.ID, unit.ID, unit.SourceText, string(raw)); err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetUnitSpeechLedger("user-1", project.ID, unit.ID)
	if err != nil || view["ready"] != true {
		t.Fatalf("saved ledger: %+v %v", view, err)
	}
	if _, err := svc.GetUnitSpeechLedger("user-2", project.ID, unit.ID); err == nil {
		t.Fatal("cross owner read accepted")
	}
	if err := db.Model(&model.ProjectUnit{}).Where("id = ?", unit.ID).Update("source_text", unit.SourceText+"\n旁白：新的一天。").Error; err != nil {
		t.Fatal(err)
	}
	view, err = svc.GetUnitSpeechLedger("user-1", project.ID, unit.ID)
	if err != nil || view["ready"] != false {
		t.Fatalf("stale ledger stayed ready: %+v %v", view, err)
	}
	if err := svc.repo.SaveUnitSpeechLedger("user-1", project.ID, unit.ID, unit.SourceText, string(raw)); err == nil {
		t.Fatal("stale source overwrote ledger")
	}
}
