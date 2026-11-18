package app

import (
	"sync"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func videoRecoveryQuote(t *testing.T, f *videoChainFixture, req ProjectVideoChainRequest) ProjectVideoChainRequest {
	t.Helper()
	req.RecoveryV2 = true
	req.PreviewHash = ""
	quote, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, req)
	if err != nil || quote.PreviewHash == "" || quote.QuoteExpiresAt <= time.Now().Unix() {
		t.Fatalf("unable to obtain v2 quote: %+v %v", quote, err)
	}
	req.PreviewHash, req.QuoteExpiresAt = quote.PreviewHash, quote.QuoteExpiresAt
	return req
}

func TestVideoRecoveryReceiptAfterLostResponse(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	req := videoRecoveryQuote(t, f, f.req)
	receipt, err := f.s.ProjectVideoChainReceipt("user-1", f.projectID, req.UnitID, req.RequestID)
	if err != nil || receipt != nil {
		t.Fatalf("quote itself produced a receipt: %v %v", receipt, err)
	}
	if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, req); err != nil {
		t.Fatal(err)
	}
	receipt, err = f.s.ProjectVideoChainReceipt("user-1", f.projectID, req.UnitID, req.RequestID)
	if err != nil || receipt == nil || receipt.Status != "queued" {
		t.Fatalf("committed response could not be recovered: %v %v", receipt, err)
	}
	repeated, err := f.s.CreateProjectVideoChain("user-1", f.projectID, req)
	if err != nil || repeated.ID != receipt.ID {
		t.Fatalf("same request did not return original attempt: %+v %v", repeated, err)
	}
	changed := req
	changed.ShotID = "another"
	if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, changed); err == nil {
		t.Fatal("changed request reused existing idempotency key")
	}
	changed = req
	changed.RequestID = "other-key"
	if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, changed); err == nil {
		t.Fatal("quote reused with a different key")
	}
	if foreign, err := f.s.ProjectVideoChainReceipt("different-user", f.projectID, req.UnitID, req.RequestID); err == nil || foreign != nil {
		t.Fatal("foreign user reached receipt")
	}
	var controllers, bills int64
	f.db.Model(&model.Task{}).Where("project_id = ?", f.projectID).Count(&controllers)
	f.db.Model(&model.BillingOrder{}).Count(&bills)
	if controllers != 1 || bills != 0 || f.uploads.Load() != 0 {
		t.Fatalf("quote/replay side effects: controllers=%d orders=%d uploads=%d", controllers, bills, f.uploads.Load())
	}
}

func TestVideoRecoveryRestartRetainsOriginalAndSerializesTabs(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	original := f.start(t)
	if err := f.db.Model(&model.Task{}).Where("id = ?", original.ID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "fixture submission failed"}).Error; err != nil {
		t.Fatal(err)
	}
	req := f.req
	req.RestartChainID = original.ID
	req.RequestID = "tab-one"
	first := videoRecoveryQuote(t, f, req)
	req.RequestID = "tab-two"
	second := videoRecoveryQuote(t, f, req)
	expired := first
	expired.QuoteExpiresAt = time.Now().Add(-time.Second).Unix()
	if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, expired); err == nil {
		t.Fatal("expired quote accepted")
	}
	var wg sync.WaitGroup
	results := make(chan ProjectVideoChainView, 2)
	for _, q := range []ProjectVideoChainRequest{first, second} {
		wg.Add(1)
		go func(q ProjectVideoChainRequest) {
			defer wg.Done()
			view, err := f.s.CreateProjectVideoChain("user-1", f.projectID, q)
			if err == nil {
				results <- view
			}
		}(q)
	}
	wg.Wait()
	close(results)
	if len(results) != 1 {
		t.Fatalf("got %d successful starts; want one", len(results))
	}
	next := <-results
	if next.ID == original.ID || len(next.Steps) != 1 || next.Steps[0].TaskID == original.Steps[0].TaskID || next.Steps[0].VideoResourceID != "" {
		t.Fatalf("restart reused original attempt: %+v", next)
	}
	older, err := f.s.repo.TaskForUser("user-1", original.ID)
	if err != nil || older.Status != model.TaskStatusFailed || older.Error != "fixture submission failed" {
		t.Fatalf("original attempt was changed: %v %v", older, err)
	}
	var controllers int64
	f.db.Model(&model.Task{}).Where("project_id = ?", f.projectID).Count(&controllers)
	if controllers != 2 {
		t.Fatalf("got %d task rows", controllers)
	}
}

func TestVideoRecoveryResumeCreatesSeparateController(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	original := f.start(t)
	if err := f.db.Model(&model.Task{}).Where("id = ?", original.ID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "fixture failure"}).Error; err != nil {
		t.Fatal(err)
	}
	req := f.req
	req.ResumeChainID, req.RequestID = original.ID, "resume-new-controller"
	req = videoRecoveryQuote(t, f, req)
	next, err := f.s.RetryProjectVideoChain("user-1", f.projectID, original.ID, req)
	if err != nil || next.ID == original.ID || len(next.Steps) != len(original.Steps) {
		t.Fatalf("resume failed: %+v %v", next, err)
	}
	receipt, err := f.s.ProjectVideoChainReceipt("user-1", f.projectID, req.UnitID, req.RequestID)
	if err != nil || receipt == nil || receipt.ID != next.ID {
		t.Fatalf("cannot recover resume receipt: %+v %v", receipt, err)
	}
}

func TestVideoRecoveryConfirmRechecksBalanceAndRoute(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	seedOnlySlotSystemVideo(t, f)
	f.req.Model.Input = globalSixSecondsRequest("false")
	req := videoRecoveryQuote(t, f, f.req)
	if err := f.db.Model(&model.CreditAccount{}).Where("user_id = ?", "user-1").Update("available_microcredits", 0).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, req); err == nil {
		t.Fatal("insufficient balance admitted a controller")
	}
	assertVideoChainPreflightOnly(t, f)
	if err := f.db.Model(&model.CreditAccount{}).Where("user_id = ?", "user-1").Update("available_microcredits", 50_000_000).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.ModelChannel{}).Where("id = ?", "system-video").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, req); err == nil {
		t.Fatal("disabled quoted route admitted a controller")
	}
	assertVideoChainPreflightOnly(t, f)
}

func TestVideoRecoveryReceiptReplayDoesNotReadmitChildOrDoubleRefund(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	seedOnlySlotSystemVideo(t, f)
	f.req.Model.Input = globalSixSecondsRequest("false")
	req := videoRecoveryQuote(t, f, f.req)
	accepted, err := f.s.CreateProjectVideoChain("user-1", f.projectID, req)
	if err != nil {
		t.Fatal(err)
	}
	state := f.untilTask(t, accepted.ID, 0)
	child, err := f.s.repo.TaskForUser("user-1", state.Steps[0].TaskID)
	if err != nil || child.BillingOrderID == "" {
		t.Fatalf("one reserved order expected: %+v %v", child, err)
	}
	for i := 0; i < 2; i++ {
		replay, err := f.s.CreateProjectVideoChain("user-1", f.projectID, req)
		if err != nil || replay.ID != accepted.ID {
			t.Fatalf("replay admitted another controller: %+v %v", replay, err)
		}
		_ = f.s.taskBilling().RefundBilling(child.BillingOrderID, "offline test fixture")
	}
	var orders, children int64
	f.db.Model(&model.BillingOrder{}).Where("task_id = ?", child.ID).Count(&orders)
	f.db.Model(&model.Task{}).Where("project_id = ? AND type = ?", f.projectID, "canvas_video").Count(&children)
	account, err := f.s.repo.CreditAccount("user-1")
	if err != nil || orders != 1 || children != 1 || account.AvailableMicrocredits != 50_000_000 || account.ReservedMicrocredits != 0 || f.uploads.Load() != 0 {
		t.Fatalf("replay or duplicate refund changed ledger: orders=%d children=%d account=%+v err=%v", orders, children, account, err)
	}
}

func TestVideoRecoveryKeepsSelectedTakeOnLateOutput(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	old := model.ShotArtifact{ID: "selected-video", ProjectID: f.projectID, UnitID: f.req.UnitID, ShotID: "s0", RevisionID: "rs0", TaskID: "old-video", Type: "video", Version: 1, Selected: true, Status: "ready"}
	if err := f.db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	next := old
	next.ID, next.TaskID, next.Version = "fresh-video", "new-video", 0
	for i := 0; i < 2; i++ {
		if err := f.s.repo.RegisterTaskOutputRecords(f.projectID, nil, nil, &next, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.First(&old, "id = ?", "selected-video").Error; err != nil || !old.Selected {
		t.Fatalf("existing selection overwritten: %+v %v", old, err)
	}
	var outputs []model.ShotArtifact
	if err := f.db.Where("project_id = ? AND type = ?", f.projectID, "video").Order("version").Find(&outputs).Error; err != nil || len(outputs) != 2 || outputs[1].Version != 2 || outputs[1].Selected {
		t.Fatalf("replacement result overwritten or replayed: %+v %v", outputs, err)
	}
}
