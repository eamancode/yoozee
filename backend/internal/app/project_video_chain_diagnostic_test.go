package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestVideoChainOriginal503Receipts(t *testing.T) {
	for _, tc := range []struct {
		name, detail, category, count string
		resubmit                      bool
	}{
		{"explicit rejection", "当前分组 default 下对于模型 lec-vp-seedance-2-5-f 无可用渠道", "route_unavailable", "increment", true},
		{"unspecified service failure", "Service unavailable", "upstream_service", "ignore", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVideoChainFixture(t, "A", "A")
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/videos" {
					t.Errorf("unexpected original request: %s %s", r.Method, r.URL.Path)
				}
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprintf(w, `{"error":{"message":%q}}`, tc.detail)
			}))
			t.Cleanup(provider.Close)
			f.req.Model.Input["config"].(map[string]any)["baseUrl"] = provider.URL
			chain := f.start(t)
			state := f.untilTask(t, chain.ID, 0)
			child, runErr := runQueuedTask(t, f.s)
			if runErr == nil || child.ID != state.Steps[0].TaskID || child.Status != model.TaskStatusFailed {
				t.Fatalf("expected original failed video, got status %s and error %v", child.Status, runErr)
			}
			var input map[string]any
			if err := json.Unmarshal([]byte(child.InputJSON), &input); err != nil {
				t.Fatal(err)
			}
			marker, _ := input[videoChainMetadataKey].(map[string]any)
			if marker["previewHash"] != chain.PreviewHash || marker["shotId"] != state.Steps[0].ShotID ||
				marker["contentSeconds"] != state.Steps[0].ContentSeconds || marker["generationSeconds"] != state.Steps[0].GenerationSeconds {
				t.Fatalf("original confirmation lost: %+v", marker)
			}
			d, err := f.s.videoChainAttemptDiagnostic(child)
			if err != nil || d.AttemptID == "" || d.QuoteID != chain.PreviewHash || d.Category != tc.category || d.UpstreamCode != "HTTP 503" ||
				d.SubmissionOutcome != "unknown" || d.CountDecision != tc.count || d.SafeToResubmit != tc.resubmit || d.CanQueryProvider {
				t.Fatalf("original receipt: %+v, %v", d, err)
			}
			state = f.tick(t, chain.ID)
			if state.Steps[0].Status != "failed" || state.Steps[1].Status != "waiting" || calls.Load() != 1 {
				t.Fatalf("failed chain did additional work: statuses %s, %s; creates %d", state.Steps[0].Status, state.Steps[1].Status, calls.Load())
			}
		})
	}
}

func TestVideoChainAttemptReconciliationDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, code, message, phase, dispatch, providerID, poll, billing, category, count string
		resubmit, query                                                                  bool
	}{
		{"timeout after acceptance", "", taskTimeoutMessage("canvas_video"), "execution", "accepted", "original", "", "refunded", "timeout", "ignore", false, true},
		{"open bill", "", "", "execution", "accepted", "original", "provider_failed", "uncertain", "provider_failed", "increment", false, true},
		{"reconciled failure", "", "", "execution", "accepted", "original", "provider_failed", "refunded", "provider_failed", "increment", true, true},
		{"late receipt overrides rejection", "upstream_503", "无可用渠道", "execution", "submission_unknown", "original", "", "refunded", "route_unavailable", "ignore", false, true},
		{"generic execution error", "", "任务执行失败", "execution", "submission_unknown", "", "", "refunded", "unknown", "ignore", false, false},
		{"preflight", "", "参考图必须至少一张", "preparation", "not_sent", "", "", "none", "preflight", "ignore", true, false},
		{"parameter error", "upstream_400", "", "execution", "rejected_no_job", "", "", "refunded", "invalid_input", "ignore", true, false},
		{"moderation", "", "内容审核未通过", "execution", "accepted", "original", "provider_failed", "settled", "content_rejected", "ignore", true, true},
		{"insufficient balance", "upstream_402", "", "execution", "rejected_no_job", "", "", "none", "insufficient_balance", "ignore", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := model.Task{Status: model.TaskStatusFailed, Error: tc.message, PollStage: tc.poll}
			recordTaskDiagnostic(&child, tc.phase, "provider_execution_failed", "unknown", "", nil)
			attempt := model.RouteAttempt{FailureCode: tc.code, DispatchState: tc.dispatch, FailureMessage: tc.message}
			d := videoChainAttemptDiagnostic{Category: "unknown", CountDecision: "ignore", SubmissionOutcome: "unknown", BillingStatus: tc.billing}
			d.classify(&child, attempt, tc.providerID)
			if d.Category != tc.category || d.CountDecision != tc.count || d.SafeToResubmit != tc.resubmit || d.CanQueryProvider != tc.query {
				t.Fatalf("receipt: %+v", d)
			}
		})
	}
}

func TestVideoChainOriginalIDBelongsToCurrentRun(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	now := time.Now().UTC()
	child := &model.Task{ID: "retried-child", UserID: "user-1", RouteRun: 2, Status: model.TaskStatusFailed}
	attempt := model.RouteAttempt{ID: "attempt-2", TaskID: child.ID, RouteRun: 2, AttemptNumber: 1, StartedAt: now, DispatchState: "submission_unknown"}
	if err := f.db.Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	for _, log := range []model.ApiCallLog{
		{ID: "earlier", UserID: child.UserID, RequestKind: "create", CreatedAt: now.Add(-time.Minute)},
		{ID: "another-user", UserID: "other", RequestKind: "poll", CreatedAt: now},
		{ID: "uploaded-ref", UserID: child.UserID, RequestKind: "upload", CreatedAt: now},
	} {
		log.TaskID, log.Capability, log.ProviderRequestID = child.ID, "video", log.ID
		if err := f.db.Create(&log).Error; err != nil {
			t.Fatal(err)
		}
	}
	d, err := f.s.videoChainAttemptDiagnostic(child)
	if err != nil || d.AttemptID != attempt.ID || d.originalRequestID != "" || d.CanQueryProvider || d.SafeToResubmit {
		t.Fatalf("unrelated receipt adopted: %+v, %v", d, err)
	}
	log := model.ApiCallLog{ID: "current", TaskID: child.ID, UserID: child.UserID, RequestKind: "create", Capability: "video", ProviderRequestID: "provider-job-2", CreatedAt: now.Add(time.Second)}
	if err := f.db.Create(&log).Error; err != nil {
		t.Fatal(err)
	}
	d, err = f.s.videoChainAttemptDiagnostic(child)
	if err != nil || d.originalRequestID != "provider-job-2" || !d.CanQueryProvider || d.SubmissionOutcome != "accepted" || d.SafeToResubmit {
		t.Fatalf("current receipt missing: %+v, %v", d, err)
	}
}
