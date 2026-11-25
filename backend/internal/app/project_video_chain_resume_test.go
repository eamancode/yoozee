package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"yingce/backend/internal/model"
)

func (f *videoChainFixture) resumeQuote(t *testing.T, id string) ProjectVideoChainRequest {
	t.Helper()
	req := f.req
	req.ResumeChainID, req.PreviewHash, req.RequestID = id, "", "resume-confirmation"
	preview, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, req)
	if err != nil || preview.ConfirmationBlockedReason != "" {
		t.Fatalf("expected confirmable recovery quote, got %q, %v", preview.ConfirmationBlockedReason, err)
	}
	req.PreviewHash = preview.PreviewHash
	return req
}

func (f *videoChainFixture) blockedResumeQuote(t *testing.T, req ProjectVideoChainRequest, segment int, originalTaskID string) ProjectVideoChainView {
	t.Helper()
	preview, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, req)
	if err != nil || preview.ConfirmationBlockedReason == "" || preview.PreviewHash == "" || len(preview.Steps) <= segment {
		t.Fatalf("expected read-only recovery quote, got %+v, %v", preview, err)
	}
	step := preview.Steps[segment]
	if step.TaskID != originalTaskID || step.Diagnostic == nil || step.Diagnostic.SafeToResubmit {
		t.Fatalf("quote lost original receipt: %+v", step)
	}
	req.PreviewHash = preview.PreviewHash
	_, err = f.s.RetryProjectVideoChain("user-1", f.projectID, req.ResumeChainID, req)
	var blocked *AppError
	if !errors.As(err, &blocked) || blocked.Reason != ReasonFailedPrecondition || blocked.Message != preview.ConfirmationBlockedReason || blocked.Details["taskId"] != originalTaskID {
		t.Fatalf("confirmation did not independently reject original %s: %v", originalTaskID, err)
	}
	return preview
}

func TestVideoChainUncertainOriginalRecoversWithoutAnotherGeneration(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	var queries atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/late-receipt":
			queries.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"late-receipt","status":"succeeded","video_url":"http://%s/original.mp4"}`, r.Host)
		case r.Method == http.MethodGet && r.URL.Path == "/original.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(agentAvatarTestMP4())
		default:
			t.Errorf("unexpected create/query %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(provider.Close)
	seedOnlySlotSystemVideo(t, f)
	if err := f.db.Model(&model.Project{}).Where("id = ?", f.projectID).Update("type", "short-drama").Error; err != nil {
		t.Fatal(err)
	}
	f.req.Model.Input = globalSixSecondsRequest("false")
	if err := f.db.Model(&model.ModelChannel{}).Where("id = ?", "system-video").Update("base_url", provider.URL).Error; err != nil {
		t.Fatal(err)
	}
	chain := f.start(t)
	step := f.untilTask(t, chain.ID, 0).Steps[0]
	original, err := f.s.repo.TaskForUser("user-1", step.TaskID)
	if err != nil || original.BillingOrderID == "" {
		t.Fatal("original order was not reserved", err)
	}
	if err := f.s.taskBilling().MarkBillingUncertain(original.BillingOrderID, "fixture receipt pending"); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Task{}).Where("id = ?", step.TaskID).Updates(map[string]any{
		"status": model.TaskStatusFailed, "error": "任务执行失败",
		"execution_diagnostic_json": `{"phase":"execution","submissionOutcome":"unknown"}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	f.tick(t, chain.ID)
	if _, err := f.s.QueryFailedVideoTask(context.Background(), "user-1", step.TaskID); err == nil || queries.Load() != 0 {
		t.Fatal("missing provider receipt queried a guessed ID")
	}
	if err := f.db.Create(&model.RouteAttempt{ID: newID(), TaskID: step.TaskID, RouteRun: 1, AttemptNumber: 1, ProviderRequestID: "late-receipt", DispatchState: "accepted"}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := f.s.QueryFailedVideoTask(context.Background(), "user-1", step.TaskID)
	if err != nil || !result.Recovered || !result.BillingSettled || queries.Load() != 1 {
		t.Fatal("did not recover through original route attempt receipt", err)
	}
	order, err := f.s.repo.BillingOrder(original.BillingOrderID)
	if err != nil || order.Status != model.BillingStatusSettled || order.TaskID != original.ID {
		t.Fatal("original task and order did not settle together", err)
	}
	result, err = f.s.QueryFailedVideoTask(context.Background(), "user-1", original.ID)
	if err != nil || !result.Recovered || queries.Load() != 1 {
		t.Fatal("repeated success query lost its original result", err)
	}
	var resets int64
	if err := f.db.Model(&model.ProjectVideoRouteEvent{}).Where("task_id = ? AND decision = ?", original.ID, "reset").Count(&resets).Error; err != nil || resets != 1 {
		t.Fatal("confirmed original success did not reset the route exactly once", resets, err)
	}
	if err := f.db.Model(&model.ChannelModelPriceTier{}).Where("channel_model_id = ?", "system-model").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	req := f.resumeQuote(t, chain.ID)
	resumed, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID, req)
	if err != nil || resumed.Steps[0].TaskID != step.TaskID || resumed.Steps[0].Status != "succeeded" {
		t.Fatal("original successful result discarded", err)
	}
	f.tick(t, chain.ID)
	var children int64
	if err := f.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&children).Error; err != nil || children != 1 {
		t.Fatal("recovered original scheduled a second child", children, err)
	}
}

func TestVideoChainTimeoutQueriesOriginalAndConfirmsNewRouteOnce(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	var queries, creates atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/original-job":
			n := queries.Add(1)
			if n == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"error":{"message":"fixture poll failed"}}`)
			} else if n == 2 {
				fmt.Fprint(w, `{"status":"processing"}`)
			} else {
				fmt.Fprint(w, `{"status":"failed","message":"fixture provider failed"}`)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/media/upload":
			fmt.Fprintf(w, `{"url":"http://%s/ref.png"}`, r.Host)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos":
			var body struct{ Model string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "seedance-2.5-replacement" {
				t.Errorf("unexpected paid create for model %q: %v", body.Model, err)
			}
			creates.Add(1)
			fmt.Fprintf(w, `{"id":"replacement","status":"succeeded","video_url":"http://%s/result.mp4"}`, r.Host)
		case r.Method == http.MethodGet && r.URL.Path == "/result.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(agentAvatarTestMP4())
		default:
			t.Errorf("unexpected mock request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(provider.Close)
	f.req.Model.Input["config"].(map[string]any)["baseUrl"] = provider.URL
	chain := f.start(t)
	first := f.untilTask(t, chain.ID, 0).Steps[0]
	firstVideo := f.complete(t, first.TaskID)
	f.tick(t, chain.ID)
	second := f.untilTask(t, chain.ID, 1).Steps[1]
	if err := f.db.Model(&model.Task{}).Where("id = ?", second.TaskID).Updates(map[string]any{
		"status": model.TaskStatusFailed, "error": "任务执行超时", "provider_request_id": "original-job",
		"execution_diagnostic_json": `{"phase":"execution","code":"timeout","submissionOutcome":"accepted"}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	f.tick(t, chain.ID)
	req := f.req
	req.RequestID, req.ResumeChainID = "resume-confirmation", chain.ID
	readOnly := f.blockedResumeQuote(t, req, 1, second.TaskID)
	if readOnly.Steps[0].TaskID != first.TaskID || readOnly.Steps[0].VideoResourceID != firstVideo ||
		readOnly.Steps[1].FrameResourceID != second.FrameResourceID || readOnly.Steps[1].Diagnostic.SubmissionOutcome != "accepted" || queries.Load() != 0 || creates.Load() != 0 {
		t.Fatal("read-only quote lost the original frame/result or issued a request")
	}
	fresh := req
	fresh.ResumeChainID, fresh.RequestID = "", "bypass-original"
	if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, fresh); err == nil {
		t.Fatal("new controller bypassed original accepted job")
	}
	if _, err := f.s.QueryFailedVideoTask(context.Background(), "user-1", second.TaskID); err == nil {
		t.Fatal("HTTP polling failure reported success")
	}
	f.blockedResumeQuote(t, req, 1, second.TaskID)
	query, err := f.s.QueryFailedVideoTask(context.Background(), "user-1", second.TaskID)
	if err != nil || query.Recovered || query.ProviderStatus != "processing" {
		t.Fatal(query, err)
	}
	f.blockedResumeQuote(t, req, 1, second.TaskID)
	if _, err := f.s.QueryFailedVideoTask(context.Background(), "user-1", second.TaskID); err == nil {
		t.Fatal("terminal upstream failure was reported as processing")
	}
	beforeTerminalReplay := queries.Load()
	terminal, err := f.s.QueryFailedVideoTask(context.Background(), "user-1", second.TaskID)
	if err != nil || terminal.ProviderStatus != "failed" || terminal.Recovered || queries.Load() != beforeTerminalReplay {
		t.Fatalf("terminal replay did not return stored failure without recontacting provider: %+v %v", terminal, err)
	}
	oldQuote := f.resumeQuote(t, chain.ID)
	if readOnly.PreviewHash == oldQuote.PreviewHash {
		t.Fatal("read-only quote became confirmable after reconciliation")
	}
	req.PreviewHash = readOnly.PreviewHash
	if _, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID, req); err == nil {
		t.Fatal("stale read-only quote was accepted after reconciliation")
	}
	f.req.Model.Input = cloneVideoChainInput(f.req.Model.Input)
	f.req.Model.Input["config"].(map[string]any)["model"] = "seedance-2.5-replacement"
	f.req.Model.Model = "seedance-2.5-replacement"
	newQuote := f.resumeQuote(t, chain.ID)
	if newQuote.PreviewHash == oldQuote.PreviewHash {
		t.Fatal("changed model reused old quote")
	}
	stale := newQuote
	stale.PreviewHash = oldQuote.PreviewHash
	if _, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID, stale); err == nil {
		t.Fatal("stale model quote accepted")
	}
	type confirmation struct {
		view ProjectVideoChainView
		err  error
	}
	start := make(chan struct{})
	results := make(chan confirmation, 2)
	for range 2 {
		go func() {
			<-start
			view, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID, newQuote)
			results <- confirmation{view, err}
		}()
	}
	close(start)
	once, twice := <-results, <-results
	if once.err != nil || twice.err != nil || once.view.Steps[1].TaskID != twice.view.Steps[1].TaskID {
		t.Fatal("concurrent confirmation scheduled a different child", once.err, twice.err)
	}
	resumed := f.untilTask(t, chain.ID, 1)
	if resumed.Steps[0].TaskID != first.TaskID || resumed.Steps[0].VideoResourceID != firstVideo ||
		resumed.Steps[1].Model != newQuote.Model.Model || resumed.Steps[1].TaskID == second.TaskID ||
		len(resumed.Steps[1].PreviousTaskIDs) != 1 || resumed.Steps[1].PreviousTaskIDs[0] != second.TaskID {
		t.Fatal("failed-segment lineage not preserved")
	}
	var count int64
	if err := f.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&count).Error; err != nil || count != 3 || queries.Load() != 3 || creates.Load() != 0 {
		t.Fatalf("children=%d queries=%d unexpected provider requests=%d err=%v", count, queries.Load(), creates.Load(), err)
	}
	replacement, err := runQueuedTask(t, f.s)
	if err != nil || replacement.Status != model.TaskStatusSucceeded || replacement.ID != resumed.Steps[1].TaskID {
		t.Fatalf("confirmed child status=%s error=%v", replacement.Status, err)
	}
	f.tick(t, chain.ID)
	f.tick(t, chain.ID)
	final, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID, newQuote)
	if err != nil || final.Status != "succeeded" || creates.Load() != 1 || final.Steps[0].VideoResourceID != firstVideo {
		t.Fatal("confirmation replay after completion", final.Status, creates.Load(), err)
	}
}
