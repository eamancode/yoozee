package app

import (
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestAdminVideoReceiptRequiresCurrentAttemptLog(t *testing.T) {
	f := newDelistedVideoFixture(t, "system")
	actor := &model.User{ID: "fixture-admin", Role: model.UserRoleAdmin}
	start := time.Now().UTC().Add(-15 * time.Second)
	f.failOriginal(t, map[string]any{"route_run": 2, "provider_request_id": "", "started_at": start})
	if err := f.db.Model(&model.BillingOrder{}).Where("id = ?", f.order.ID).Update("provider_request_id", "").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.RouteAttempt{}).Where("task_id = ?", f.original.ID).Updates(map[string]any{
		"route_run": 2, "started_at": start, "provider_request_id": "", "dispatch_state": "submission_unknown",
	}).Error; err != nil {
		t.Fatal(err)
	}
	addLog := func(id, user, channel, order, job string, began, ended time.Time) {
		t.Helper()
		entry := model.ApiCallLog{ID: id, TaskID: f.original.ID, UserID: user, ChannelID: channel, BillingOrderID: order,
			Capability: "video", RequestKind: "create", Source: "backend-task", ProviderRequestID: job, Status: model.ApiCallStatusSucceeded,
			UpstreamURL: f.server.URL + "/v1/videos", Model: f.key, VideoProviderModelKey: f.input.Config.ProviderModelKey,
			StartedAt: began, CreatedAt: ended}
		if err := f.db.Create(&entry).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := start.Add(time.Second)
	addLog("previous-run", "user-1", f.channel, f.order.ID, "previous-job", start.Add(-time.Hour), start.Add(-time.Second))
	addLog("previous-in-flight", "user-1", f.channel, f.order.ID, "late-old-job", start.Add(-time.Hour), now)
	addLog("wrong-order", "user-1", f.channel, "previous-order", "wrong-order-job", now, now)
	addLog("wrong-channel", "user-1", "other-channel", f.order.ID, "wrong-channel-job", now, now)
	addLog("wrong-user", "other-user", f.channel, f.order.ID, "wrong-user-job", now, now)
	for _, id := range []string{"previous-run", "previous-in-flight", "wrong-order", "wrong-channel", "wrong-user"} {
		if _, err := f.s.AdminQueryFailedVideoTask(t.Context(), actor, id, ""); err == nil {
			t.Fatalf("administrator queried unrelated receipt from %s", id)
		}
	}
	if _, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID); err == nil {
		t.Fatal("owner queried an unrelated receipt")
	}
	if _, err := f.s.AdminRecoverVideoByURL(t.Context(), actor, "previous-run", f.server.URL+"/original.mp4", "previous-job"); err == nil {
		t.Fatal("administrator attached a prior run's result URL to the current order")
	}
	addLog("current-run", "user-1", f.channel, f.order.ID, delistedReceipt, now, now)
	if _, err := f.s.AdminQueryFailedVideoTask(t.Context(), actor, "current-run", "operator-typed-previous-id"); err == nil || f.requests.Load() != 0 {
		t.Fatal("an operator override or an unrelated row reached the provider")
	}
	result, err := f.s.AdminQueryFailedVideoTask(t.Context(), actor, "current-run", delistedReceipt)
	if err != nil || !result.Recovered || !result.BillingSettled {
		t.Fatalf("current receipt did not recover its original task and order: %+v, %v", result, err)
	}
	f.checkCompletion(t, 1, 1)
	var previous model.ApiCallLog
	if err := f.db.First(&previous, "id = ?", "previous-run").Error; err != nil || previous.PollCount != 0 || previous.ProviderRequestID != "previous-job" {
		t.Fatalf("poll changed an earlier attempt's log: %+v, %v", previous, err)
	}
}
