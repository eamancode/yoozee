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
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
	"yingce/backend/internal/sllcatalog"
)

const delistedReceipt = "previously-accepted-fixture"

type delistedVideoFixture struct {
	*videoChainFixture
	channel, key string
	server       *httptest.Server
	order        *model.BillingOrder
	original     *model.Task
	input        canvasGenerationInput
	queries      atomic.Int32
	downloads    atomic.Int32
	requests     atomic.Int32
}

func newDelistedVideoFixture(t *testing.T, kind string) *delistedVideoFixture {
	t.Helper()
	f := &delistedVideoFixture{videoChainFixture: newVideoChainFixture(t, "A"), channel: "system-video", key: "seedance-2.5-test"}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/"+delistedReceipt:
			if r.Header.Get("Authorization") != "Bearer fixture-only" {
				t.Error("query did not use the original channel credential")
			}
			f.queries.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":%q,"status":"succeeded","video_url":"http://%s/original.mp4"}`, delistedReceipt, r.Host)
		case r.Method == http.MethodGet && r.URL.Path == "/original.mp4":
			f.downloads.Add(1)
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(agentAvatarTestMP4())
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	if kind == "sll" {
		f.channel, f.key = "sll-fixture", "lec-fixture-seedance-2-5"
		ch := model.ModelChannel{ID: f.channel, Scope: model.ChannelScopeSystem, Enabled: true, Name: "SLL", BaseURL: "https://api.lecapi.net", APIKey: "fixture-only"}
		entry := sllcatalog.Entry{
			ModelID: f.key, DisplayName: "Seedance 2.5 fixture", Group: "default", GroupRatio: 1,
			Tiers:        []sllcatalog.Tier{{Resolution: "720p", CostMicroCNY: 5_000_000, PriceCNY: 5}},
			Capabilities: sllcatalog.Capabilities{Durations: []int{30}, DurationMin: 30, DurationMax: 30, AspectRatios: []string{"16:9"}, FixedResolution: "720p"},
		}
		for _, row := range []any{&ch, &model.CreditAccount{UserID: "user-1", AvailableMicrocredits: 50_000_000}} {
			if err := f.db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.s.repo.SLLCatalog().Import(t.Context(), ch.ID, "default", sllcatalog.Catalog{Version: "available", Entries: []sllcatalog.Entry{entry}}, time.Now()); err != nil {
			t.Fatal(err)
		}
	} else {
		seedOnlySlotSystemVideo(t, f.videoChainFixture)
		if err := f.db.Model(&model.ModelChannel{}).Where("id = ?", f.channel).Update("base_url", f.server.URL).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := CreateTaskRequest{
		ProjectID: f.projectID, Type: "canvas_video", Model: f.key, Operation: "text_to_video", Prompt: "fixture",
		Input: map[string]any{"mode": "video", "config": map[string]any{
			"channelId": f.channel, "model": f.key, "videoSeconds": "30", "vquality": "720p", "size": "16:9", "videoGenerateAudio": "false",
		}},
	}
	if kind == "unified" {
		f.selectUnifiedModel(t, &req)
	}
	if kind == "unbilled" {
		if _, err := f.s.UpdateFeatureAvailability(&model.User{ID: "admin", Role: "admin"}, FeatureAvailability{TaskCenterEnabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	task, err := f.s.CreateTask("user-1", req)
	if err != nil {
		t.Fatal(err)
	}
	channelModelID, attemptChannelID := "system-model", f.channel
	if kind == "unbilled" {
		if task.BillingOrderID != "" {
			t.Fatalf("direct task unexpectedly charged: %s", task.BillingOrderID)
		}
		// Historical direct attempts stored only the channel-model primary key.
		attemptChannelID = ""
	} else {
		f.order, err = f.s.repo.BillingOrder(task.BillingOrderID)
		if err != nil || f.order.AmountMicrocredits <= 0 || (f.order.PriceTierID == "") != (kind == "unified") || f.order.ChannelID != f.channel {
			t.Fatalf("wrong original sale: %+v %v", f.order, err)
		}
		channelModelID = f.order.ChannelModelID
		if err := f.s.MarkBillingRunning(f.order.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.db.Model(&model.BillingOrder{}).Where("id = ?", f.order.ID).Update("provider_request_id", delistedReceipt).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{"status": model.TaskStatusRunning, "provider_request_id": delistedReceipt, "poll_stage": "polling"}).Error; err != nil {
		t.Fatal(err)
	}
	attempt := model.RouteAttempt{ID: newID(), TaskID: task.ID, RouteRun: task.RouteRun, AttemptNumber: 1, ChannelID: attemptChannelID, ChannelModelID: channelModelID,
		RouteID: task.RouteID, LogicalModelID: task.LogicalModelID, LogicalModelRevisionID: task.LogicalModelRevisionID,
		DispatchState: "accepted", ProviderRequestID: delistedReceipt, Status: "accepted"}
	if err := f.db.Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	f.original, err = f.s.repo.TaskForUser("user-1", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(f.original.InputJSON), &f.input); err != nil {
		t.Fatal(err)
	}
	if kind == "sll" {
		if _, err := f.s.repo.SLLCatalog().Import(t.Context(), f.channel, "default", sllcatalog.Catalog{Version: "withdrawn"}, time.Now()); err != nil {
			t.Fatal(err)
		}
	} else if err := f.db.Model(&model.ChannelModelPriceTier{}).Where("id = ?", "tier-30").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if kind == "unified" {
		if err := f.db.Model(&model.LogicalModelRoute{}).Where("id = ?", task.RouteID).Update("enabled", false).Error; err != nil {
			t.Fatal(err)
		}
		f.s.invalidateRouteCatalog()
		if _, err := f.s.QuoteLogicalModel("user-1", req.LogicalModelID, ModelRequestIntentFromTaskInput(req.Input, req.Type, req.Operation)); err == nil {
			t.Fatal("new logical quote used withdrawn route")
		}
	}
	// Both public admission paths still reject the withdrawn spec.
	quote := ChannelModelQuoteRequest{ChannelID: f.channel, ModelKey: f.key, Intent: ModelRequestIntentFromTaskInput(req.Input, req.Type, req.Operation)}
	if _, err := f.s.QuoteChannelModel("user-1", quote); err == nil {
		t.Fatal("new quote used withdrawn tier")
	}
	if _, err := f.s.CreateTask("user-1", req); err == nil {
		t.Fatal("new task admitted against withdrawn tier")
	}
	return f
}

func (f *delistedVideoFixture) selectUnifiedModel(t *testing.T, req *CreateTaskRequest) {
	t.Helper()
	if _, err := f.s.UpdateFeatureAvailability(&model.User{ID: "admin", Role: "admin"}, FeatureAvailability{FrontendModelsEnabled: true, CreditsEnabled: true, TaskCenterEnabled: true}); err != nil {
		t.Fatal(err)
	}
	cm, err := f.s.repo.ChannelModel("system-model")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := channelModelCapabilitySpec(*cm, f.s.protocolRegistry())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	logical := model.LogicalModel{ID: "unified-video", Code: "historic-video-alias", Capability: "video", Enabled: true,
		ActiveRevisionID: "historic-revision", PricePolicy: "unified", BillingMode: "fixed_request", UnitPriceMicrocredits: 1_200_000}
	for _, row := range []any{
		&logical,
		&model.LogicalModelRevision{ID: logical.ActiveRevisionID, LogicalModelID: logical.ID, Version: 1, CapabilitySpecJSON: string(raw), DefaultOptionsJSON: `{"videoSeconds":30}`},
		&model.LogicalModelRoute{ID: "unified-route", LogicalModelRevisionID: logical.ActiveRevisionID, ChannelModelID: cm.ID, Enabled: true, Weight: 1},
	} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	req.LogicalModelID, req.Model = logical.ID, logical.Code
	req.Input = map[string]any{"mode": "video", "config": map[string]any{}, "capabilityOptions": map[string]any{"videoSeconds": 30, "size": "16:9", "vquality": "720p", "videoGenerateAudio": false}}
}

func (f *delistedVideoFixture) failOriginal(t *testing.T, updates map[string]any) {
	t.Helper()
	updates["status"] = model.TaskStatusFailed
	if err := f.db.Model(&model.Task{}).Where("id = ?", f.original.ID).Updates(updates).Error; err != nil {
		t.Fatal(err)
	}
}

func (f *delistedVideoFixture) recoverOriginalMedia(t *testing.T) {
	t.Helper()
	checkpoint := mediaCheckpoint{Mode: "video", StartedAt: time.Now(), Items: []mediaCheckpointItem{{Reference: protocol.MediaReference{URL: f.server.URL + "/original.mp4"}}}}
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.s.encryptSettingSecret(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	f.failOriginal(t, map[string]any{"media_recovery_json": saved, "media_stage": "download"})
	if _, err := f.s.RecoverTaskMedia("user-1", f.original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runQueuedTask(t, f.s); err != nil {
		t.Fatal(err)
	}
}

func (f *delistedVideoFixture) checkCompletion(t *testing.T, queries, downloads int32) {
	t.Helper()
	task, err := f.s.repo.TaskForUser("user-1", f.original.ID)
	if err != nil || task.Status != model.TaskStatusSucceeded || task.BillingOrderID != f.order.ID {
		t.Fatalf("old task not delivered: %+v %v", task, err)
	}
	order, err := f.s.repo.BillingOrder(f.order.ID)
	if err != nil || order.Status != model.BillingStatusSettled || order.ProviderRequestID != delistedReceipt ||
		order.AmountMicrocredits != f.order.AmountMicrocredits || order.PriceTierID != f.order.PriceTierID || order.ChannelID != f.channel {
		t.Fatalf("old order not settled against its receipt: %+v %v", order, err)
	}
	for _, table := range []any{&model.Task{}, &model.BillingOrder{}, &model.RouteAttempt{}} {
		var count int64
		if err := f.db.Model(table).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("extra task/order/dispatch: %T count %d, %v", table, count, err)
		}
	}
	if f.requests.Load() != queries+downloads || f.queries.Load() != queries || f.downloads.Load() != downloads {
		t.Fatalf("unexpected original requests: %d total / %d queries / %d downloads", f.requests.Load(), f.queries.Load(), f.downloads.Load())
	}
}

func TestDelistedVideoReceiptKeepsOriginalOrder(t *testing.T) {
	for _, path := range []string{"manual", "worker", "media"} {
		t.Run(path, func(t *testing.T) {
			f := newDelistedVideoFixture(t, "system")
			switch path {
			case "manual":
				f.failOriginal(t, map[string]any{"error": "执行超时"})
				result, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID)
				if err != nil || !result.Recovered || !result.BillingSettled {
					t.Fatalf("query failed: %+v %v", result, err)
				}
			case "worker":
				stored, err := runQueuedTask(t, f.s)
				if err != nil || stored.ID != f.original.ID {
					t.Fatalf("polling failed: %v %+v", err, stored)
				}
			case "media":
				f.recoverOriginalMedia(t)
				f.checkCompletion(t, 0, 1)
				return
			}
			f.checkCompletion(t, 1, 1)
		})
	}
}

func TestDelistedVideoReceiptHistoricalShapes(t *testing.T) {
	t.Run("unified order without tier", func(t *testing.T) {
		f := newDelistedVideoFixture(t, "unified")
		if f.order.Model != "historic-video-alias" || f.order.ChannelModelID != "system-model" || f.order.PriceTierID != "" {
			t.Fatalf("wrong unified order: %+v", f.order)
		}
		f.failOriginal(t, map[string]any{"error": "执行超时"})
		result, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID)
		if err != nil || !result.Recovered || !result.BillingSettled {
			t.Fatalf("unified original order: %+v %v", result, err)
		}
		f.checkCompletion(t, 1, 1)
	})
	t.Run("direct receipt without billing", func(t *testing.T) {
		f := newDelistedVideoFixture(t, "unbilled")
		f.failOriginal(t, map[string]any{"error": "执行超时"})
		result, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID)
		if err != nil || !result.Recovered || result.Task.ID != f.original.ID {
			t.Fatalf("unbilled original receipt: %+v %v", result, err)
		}
		var orders int64
		if err := f.db.Model(&model.BillingOrder{}).Count(&orders).Error; err != nil || orders != 0 || f.queries.Load() != 1 || f.downloads.Load() != 1 || f.requests.Load() != 2 {
			t.Fatalf("unexpected billing or dispatch: orders=%d, requests=%d, %v", orders, f.requests.Load(), err)
		}
	})
	t.Run("historic channel and model only", func(t *testing.T) {
		f := newDelistedVideoFixture(t, "system")
		f.failOriginal(t, map[string]any{"error": "执行超时", "input_json": fmt.Sprintf(`{"mode":"video","config":{"channelId":%q,"model":%q}}`, f.channel, f.key)})
		if err := f.db.Where("id = ?", f.order.PriceTierID).Delete(&model.ChannelModelPriceTier{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Model(&model.ChannelModel{}).Where("id = ?", f.order.ChannelModelID).Update("provider_model_key", "replacement-model").Error; err != nil {
			t.Fatal(err)
		}
		stored, err := f.s.repo.TaskForUser("user-1", f.original.ID)
		if err != nil {
			t.Fatal(err)
		}
		ctx := withProviderAnalytics(t.Context(), f.s, *stored)
		cfg, pollOnly, err := f.s.resolveVideoReceiptConfig(ctx, providerConfig{ChannelID: f.channel, Model: f.key}, delistedReceipt)
		if err != nil || !pollOnly || cfg.Model != "seedance-2.5-mock" || cfg.InterfaceType != "seedance-videos-compatible" || cfg.PriceTierID != f.order.PriceTierID {
			t.Fatalf("historical order did not resolve its original upstream model and protocol: %+v, %v", cfg, err)
		}
		result, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID)
		if err != nil || !result.Recovered || !result.BillingSettled {
			t.Fatalf("historical original order: %+v %v", result, err)
		}
		f.checkCompletion(t, 1, 1)
	})
	t.Run("receipt belongs to previous run", func(t *testing.T) {
		f := newDelistedVideoFixture(t, "unbilled")
		f.failOriginal(t, map[string]any{"error": "执行超时", "provider_request_id": "", "channel_model_id": "", "route_run": f.original.RouteRun + 1,
			"input_json": fmt.Sprintf(`{"mode":"video","config":{"channelId":%q,"model":%q}}`, f.channel, f.key)})
		if _, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID); err == nil || f.requests.Load() != 0 {
			t.Fatalf("unproven historic origin was queried: %v requests=%d", err, f.requests.Load())
		}
	})
}

type originalReceiptProbe struct {
	protocol.Adapter
	buildPoll func(protocol.PollContext) error
}

func (p originalReceiptProbe) BuildPoll(_ context.Context, c protocol.PollContext) (protocol.RequestSpec, error) {
	return protocol.RequestSpec{}, p.buildPoll(c)
}

func (p originalReceiptProbe) BuildCreate(context.Context, protocol.RequestContext) (protocol.RequestSpec, error) {
	return protocol.RequestSpec{}, errors.New("unexpected create for original receipt")
}

func TestDelistedSLLVideoReceiptKeepsOriginalOrder(t *testing.T) {
	f := newDelistedVideoFixture(t, "sll")
	f.failOriginal(t, map[string]any{"error": "执行超时"})
	adapter, ok := f.s.protocolRegistry().Resolve(string(sllcatalog.VideoProtocol))
	if !ok {
		t.Fatal("missing SLL video adapter")
	}
	// The SLL catalog anchors identity to a fixed HTTPS origin. Capture the
	// owner-facing query immediately before HTTP and replay only against the
	// local mock using the same persisted configuration and installed adapter.
	stopped := errors.New("fixture stopped before SLL HTTP")
	probe := originalReceiptProbe{Adapter: adapter, buildPoll: func(c protocol.PollContext) error {
		if c.BaseURL != "https://api.lecapi.net" || c.Model != f.key || c.TaskID != delistedReceipt {
			t.Fatalf("wrong original SLL poll: %+v", c)
		}
		return stopped
	}}
	registry, err := protocol.NewRegistry(probe)
	if err != nil {
		t.Fatal(err)
	}
	f.s.pluginRuntime = &pluginRuntime{registry: registry}
	if _, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID); !errors.Is(err, stopped) {
		t.Fatalf("public query did not reach original SLL adapter: %v", err)
	}
	ctx := withProviderAnalytics(t.Context(), f.s, *f.original)
	cfg, pollOnly, err := f.s.resolveVideoReceiptConfig(ctx, f.input.Config, delistedReceipt)
	if err != nil || !pollOnly || cfg.ChannelID != f.channel || cfg.PriceTierID != f.order.PriceTierID || cfg.Model != f.key ||
		cfg.BaseURL != "https://api.lecapi.net" || cfg.APIKey != "fixture-only" {
		t.Fatalf("cannot recover original SLL receipt: pollOnly=%t err=%v", pollOnly, err)
	}
	cfg.BaseURL = f.server.URL
	f.input.Config = cfg
	result, status, err := queryProtocolAdapterVideoTask(ctx, f.input, adapter, delistedReceipt)
	if err != nil || status != "succeeded" || result == nil || f.queries.Load() != 1 {
		t.Fatalf("original SLL wire query: %s, %v", status, err)
	}
	downloads := f.downloads.Load()
	f.recoverOriginalMedia(t)
	f.checkCompletion(t, 1, downloads+1)
}

func TestDelistedVideoReceiptRejectsMismatchedOrigin(t *testing.T) {
	f := newDelistedVideoFixture(t, "system")
	f.failOriginal(t, map[string]any{"error": "执行超时"})
	if _, err := f.s.QueryFailedVideoTask(t.Context(), "other-owner", f.original.ID); err == nil {
		t.Fatal("foreign user queried original receipt")
	}
	// Another channel can sell exactly the same model key. It never owns
	// the original sale or the credentials used to query that sale.
	other := model.ModelChannel{ID: "other-channel", Scope: model.ChannelScopeSystem, Enabled: true, Name: "other fixture", BaseURL: f.server.URL, APIKey: "different-fixture-key"}
	cm := model.ChannelModel{ID: "other-model", ChannelID: other.ID, ModelKey: f.key, ProviderModelKey: f.input.Config.ProviderModelKey, Capability: "video", Enabled: true}
	for _, row := range []any{&other, &cm} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&model.BillingOrder{}).Where("id = ?", f.order.ID).Update("channel_id", other.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID); err == nil {
		t.Fatal("another channel's order authorized the same model key")
	}
	if err := f.db.Model(&model.BillingOrder{}).Where("id = ?", f.order.ID).Updates(map[string]any{"channel_id": f.channel, "provider_request_id": "different-receipt"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID); err == nil {
		t.Fatal("mismatched receipt authorized")
	}
	// A lost response before any durable provider ID is a different case:
	// neither a manual query nor a worker restart may create a fresh job.
	if err := f.db.Model(&model.BillingOrder{}).Where("id = ?", f.order.ID).Update("provider_request_id", "").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.RouteAttempt{}).Where("task_id = ?", f.original.ID).Updates(map[string]any{"provider_request_id": "", "dispatch_state": "submission_unknown"}).Error; err != nil {
		t.Fatal(err)
	}
	f.failOriginal(t, map[string]any{"provider_request_id": "", "poll_stage": ""})
	if _, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID); err == nil {
		t.Fatal("unknown submission without receipt was queried")
	}
	if err := f.db.Model(&model.Task{}).Where("id = ?", f.original.ID).Update("status", model.TaskStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	stored, runErr := runQueuedTask(t, f.s)
	if runErr == nil || stored.Status != model.TaskStatusFailed {
		t.Fatalf("unknown submission did not stop: %v status=%s", runErr, stored.Status)
	}
	var attempts int64
	if err := f.db.Model(&model.RouteAttempt{}).Where("task_id = ?", f.original.ID).Count(&attempts).Error; err != nil || attempts != 1 {
		t.Fatalf("another dispatch was selected for an unknown submission: %d, %v", attempts, err)
	}
	if f.requests.Load() != 0 {
		t.Fatal("provider contacted without a matching owner, order and receipt")
	}
}
