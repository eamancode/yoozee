package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestSpeechRollbackUsesSourceAndPreservesHistoricalLedger(t *testing.T) {
	s, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, s, db)
	unit := episodes[0]
	ledger, _ := json.Marshal(map[string]any{
		"sourceHash": fmt.Sprintf("%x", sha256.Sum256([]byte(unit.SourceText))),
		"script":     drama.Script{Title: "台账注入", Scenes: []drama.ScriptScene{{ID: "E01S01", Beats: []drama.ScriptBeat{{ID: "E01S01L01", Kind: "line", Speaker: "未知", Text: "台账注入"}}}}},
	})
	if err := db.Model(&model.ProjectUnit{}).Where("id = ?", unit.ID).Update("speech_ledger_json", string(ledger)).Error; err != nil {
		t.Fatal(err)
	}
	current, err := s.repo.ProjectUnit(project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	script, _ := json.Marshal(dramaShotlistScript(current, false))
	if strings.Contains(string(script), "台账注入") || !strings.Contains(string(script), "七床") {
		t.Fatal("分镜仍被独立台账替换", string(script))
	}
	if _, exists := drama.StepByID("speech.extract"); exists {
		t.Fatal("独立声音步骤仍可发起")
	}
	if _, err := s.planDramaStep("user-1", &project, unit.ID, "speech.extract", nil); err == nil {
		t.Fatal("下线步骤仍创建生成计划")
	}
	stored, _ := s.repo.ProjectUnit(project.ID, unit.ID)
	if stored.SpeechLedgerJSON != string(ledger) {
		t.Fatal("回退清除了历史数据")
	}
}

func TestSpeechRollbackRejectsHistoricalTaskRetryBeforeBilling(t *testing.T) {
	s, db := newDramaWorkerTestService(t)
	task := model.Task{ID: "retired-speech", UserID: "user-1", Type: "canvas_text", Status: model.TaskStatusFailed, Attempts: 1, InputJSON: `{"metadata":{"dramaStep":{"step":"speech.extract"}}}`}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryTask("user-1", task.ID); err == nil || !strings.Contains(err.Error(), "已还原") {
		t.Fatal("历史声音任务重试未被阻断", err)
	}
	stored, _ := s.repo.TaskForUser("user-1", task.ID)
	if stored.Status != model.TaskStatusFailed || stored.Attempts != 1 || stored.BillingOrderID != "" {
		t.Fatal("被拒绝的重试改变了任务或账单", stored)
	}
	var orders int64
	db.Model(&model.BillingOrder{}).Count(&orders)
	if orders != 0 {
		t.Fatal("下线流程仍产生扣费订单", orders)
	}
}
