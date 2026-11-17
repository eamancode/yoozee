package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"yingce/backend/internal/model"
)

func TestVideoChainResumeCancelledBeforeDispatch(t *testing.T) {
	for _, billing := range []string{"refunded", "none"} {
		t.Run(billing, func(t *testing.T) {
			f := newVideoChainFixture(t, "A", "A")
			var creates atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/videos":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"id":"job-%d","status":"succeeded","video_url":"http://%s/result.mp4"}`, creates.Add(1), r.Host)
				case r.Method == http.MethodGet && r.URL.Path == "/result.mp4":
					w.Header().Set("Content-Type", "video/mp4")
					_, _ = w.Write(agentAvatarTestMP4())
				case r.Method == http.MethodPost && r.URL.Path == "/v1/media/upload":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"url":"http://%s/ref.png"}`, r.Host)
				default:
					t.Errorf("unexpected local provider request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(provider.Close)
			f.req.Model.Input["config"].(map[string]any)["baseUrl"] = provider.URL
			if billing == "refunded" {
				seedOnlySlotSystemVideo(t, f)
				if err := f.db.Model(&model.ModelChannel{}).Where("id = ?", "system-video").Update("base_url", provider.URL).Error; err != nil {
					t.Fatal(err)
				}
				f.req.Model.Input = globalSixSecondsRequest("false")
			}
			if err := f.db.Model(&model.Project{}).Where("id = ?", f.projectID).Update("type", "short-drama").Error; err != nil {
				t.Fatal(err)
			}
			chain := f.start(t)
			first := f.untilTask(t, chain.ID, 0).Steps[0]
			child, err := runQueuedTask(t, f.s)
			if err != nil || child.Status != model.TaskStatusSucceeded || child.ID != first.TaskID {
				t.Fatalf("first segment failed: %s %v", child.Error, err)
			}
			original := f.untilTask(t, chain.ID, 1)
			firstVideo, cancelledID := original.Steps[0].VideoResourceID, original.Steps[1].TaskID
			if _, err := f.s.CancelTask(context.Background(), "user-1", cancelledID); err != nil {
				t.Fatal(err)
			}
			stopped := f.tick(t, chain.ID)
			d := stopped.Steps[1].Diagnostic
			if stopped.Steps[1].Status != "failed" || d == nil || d.Category != "cancelled" ||
				d.SubmissionOutcome != "not_submitted" || d.BillingStatus != billing || d.CountDecision != "ignore" || !d.SafeToResubmit {
				t.Fatalf("unexpected original cancellation: %+v", d)
			}
			if _, err := f.s.RetryTask("user-1", cancelledID); err == nil {
				t.Fatal("task center replayed the cancelled child")
			}
			fresh := f.req
			fresh.RequestID = "new-controller"
			if _, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, fresh); err == nil {
				t.Fatal("new chain quoted the failed segment outside the original chain")
			}
			if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, fresh); err == nil {
				t.Fatal("a fresh controller bypassed the original chain")
			}
			resume := f.req
			resume.RequestID, resume.ResumeChainID, resume.PreviewHash = "continue-cancelled-segment", chain.ID, ""
			quote, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, resume)
			if err != nil {
				t.Fatal(err)
			}
			if firstVideo == "" || quote.Steps[0].VideoResourceID != firstVideo || quote.Steps[0].TaskID != first.TaskID ||
				quote.Steps[0].Status != "succeeded" || billing == "refunded" && quote.TotalMicrocredits != 1_500_000 {
				t.Fatal("quote did not preserve the completed segment and price only the pending one", quote.TotalMicrocredits)
			}
			f.tick(t, chain.ID)
			var children int64
			if err := f.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&children).Error; err != nil || children != 2 || creates.Load() != 1 {
				t.Fatal("cancel or preview replayed history", children, creates.Load(), err)
			}
			resume.PreviewHash = quote.PreviewHash
			view, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID, resume)
			if err != nil {
				t.Fatal(err)
			}
			replacement := view.Steps[1]
			if replacement.TaskID == cancelledID || len(replacement.PreviousTaskIDs) != 1 || replacement.PreviousTaskIDs[0] != cancelledID {
				t.Fatal("replacement lost the original task lineage")
			}
			f.untilTask(t, chain.ID, 1)
			again, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID, resume)
			if err != nil || again.Steps[1].TaskID != replacement.TaskID {
				t.Fatal("repeated confirmation replaced the step twice", err)
			}
			child, err = runQueuedTask(t, f.s)
			if err != nil || child.ID != replacement.TaskID || child.Status != model.TaskStatusSucceeded {
				t.Fatal("confirmed replacement was not generated", child.Status, err)
			}
			f.tick(t, chain.ID)
			f.tick(t, chain.ID)
			final, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID, resume)
			if err != nil || final.Status != "succeeded" || final.Steps[0].VideoResourceID != firstVideo || creates.Load() != 2 {
				t.Fatalf("final state %s, creates %d, error %v", final.Status, creates.Load(), err)
			}
			originalTask, err := f.s.repo.TaskForUser("user-1", cancelledID)
			if err != nil || originalTask.Status != model.TaskStatusCancelled {
				t.Fatal("cancelled original was modified", err)
			}
			var events int64
			if err := f.db.Model(&model.ProjectVideoRouteEvent{}).Where("task_id = ?", cancelledID).Count(&events).Error; err != nil || events != 0 {
				t.Fatal("cancelled original affected channel routing", events, err)
			}
			if err := f.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&children).Error; err != nil || children != 3 {
				t.Fatal("duplicate child after completion", children, err)
			}
			var orders []model.BillingOrder
			if err := f.db.Where("user_id = ?", "user-1").Find(&orders).Error; err != nil {
				t.Fatal(err)
			}
			bills := make(map[string]model.BillingStatus)
			for _, order := range orders {
				bills[order.TaskID] = order.Status
			}
			if billing == "refunded" && (len(orders) != 3 || bills[first.TaskID] != model.BillingStatusSettled ||
				bills[replacement.TaskID] != model.BillingStatusSettled || bills[cancelledID] != model.BillingStatusRefunded) ||
				billing == "none" && len(orders) != 0 {
				t.Fatalf("unexpected orders after repeated confirmation: %v", bills)
			}
		})
	}
}

func TestVideoChainCancelledOriginalRequiresReconciliation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		task    map[string]any
		bill    model.BillingStatus
		logJob  bool
		attempt string
		outcome string
	}{
		{name: "unknown submission", task: map[string]any{"execution_diagnostic_json": `{"phase":"cancellation","submissionOutcome":"unknown"}`}, outcome: "unknown"},
		{name: "selected attempt during running cancellation", task: map[string]any{"execution_diagnostic_json": `{"phase":"cancellation","submissionOutcome":"unknown"}`}, attempt: "not_sent", outcome: "unknown"},
		{name: "provider rejected a sent request", attempt: "rejected_no_job", outcome: "unknown"},
		{name: "accepted original", task: map[string]any{"provider_request_id": "original-job"}, outcome: "accepted"},
		{name: "polling original without job id", task: map[string]any{"poll_stage": "poll"}, outcome: "unknown"},
		{name: "accepted in original request log", logJob: true, outcome: "accepted"},
		{name: "provider cancelled accepted job", task: map[string]any{"status": model.TaskStatusFailed, "poll_stage": "provider_cancelled", "provider_request_id": "original-job"}, outcome: "accepted"},
		{name: "uncertain bill", bill: model.BillingStatusUncertain, outcome: "not_submitted"},
		{name: "reserved bill", bill: model.BillingStatusReserved, outcome: "not_submitted"},
		{name: "settled bill", bill: model.BillingStatusSettled, outcome: "not_submitted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVideoChainFixture(t, "A")
			seedOnlySlotSystemVideo(t, f)
			f.req.Model.Input = globalSixSecondsRequest("false")
			chain := f.start(t)
			id := f.untilTask(t, chain.ID, 0).Steps[0].TaskID
			if _, err := f.s.CancelTask(context.Background(), "user-1", id); err != nil {
				t.Fatal(err)
			}
			f.tick(t, chain.ID)
			confirmed := f.resumeQuote(t, chain.ID)
			child, err := f.s.repo.TaskForUser("user-1", id)
			if err != nil {
				t.Fatal(err)
			}
			if tc.task != nil {
				if err := f.db.Model(child).Updates(tc.task).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.bill != "" {
				if err := f.db.Model(&model.BillingOrder{}).Where("id = ?", child.BillingOrderID).Update("status", tc.bill).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.logJob {
				if err := f.db.Create(&model.ApiCallLog{ID: newID(), UserID: child.UserID, TaskID: child.ID, ChannelID: "system-video",
					BillingOrderID: child.BillingOrderID, Capability: "video", RequestKind: "create", ProviderRequestID: "original-job"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.attempt != "" {
				if err := f.db.Create(&model.RouteAttempt{ID: newID(), TaskID: id, RouteRun: child.RouteRun, AttemptNumber: 1,
					ChannelID: "system-video", DispatchState: tc.attempt}).Error; err != nil {
					t.Fatal(err)
				}
			}
			child, err = f.s.repo.TaskForUser("user-1", id)
			if err != nil {
				t.Fatal(err)
			}
			d, err := f.s.videoChainAttemptDiagnostic(child)
			if err != nil || d.Category != "cancelled" || d.SubmissionOutcome != tc.outcome || d.CountDecision != "ignore" || d.SafeToResubmit {
				t.Fatalf("unexpected cancellation receipt %+v: %v", d, err)
			}
			if _, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, confirmed); err == nil {
				t.Fatal("original chain allowed unresolved cancellation")
			}
			if _, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID, confirmed); err == nil {
				t.Fatal("previously valid quote admitted another generation")
			}
			// A stopped controller can be replaced only after the original
			// child is reconciled, including any pending order or late receipt.
			if _, err := f.s.CancelProjectVideoChain("user-1", f.projectID, chain.ID); err != nil {
				t.Fatal(err)
			}
			fresh := f.req
			fresh.RequestID = "bypass-cancelled-controller"
			if _, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, fresh); err == nil {
				t.Fatal("new chain quoted unresolved cancellation")
			}
			if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, fresh); err == nil {
				t.Fatal("new chain bypassed unresolved cancellation")
			}
			var orders int64
			if err := f.db.Model(&model.BillingOrder{}).Count(&orders).Error; err != nil || orders != 1 {
				t.Fatal("unresolved cancellation generated another order", orders, err)
			}
		})
	}
}
