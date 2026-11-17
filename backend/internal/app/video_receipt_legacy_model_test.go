package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestDelistedVideoReceiptRequiresOriginalProtocolAndModel(t *testing.T) {
	f := newDelistedVideoFixture(t, "unbilled")
	const originalProtocol = "seedance-videos-compatible"
	if err := f.db.Model(&model.ChannelModel{}).Where("id = ?", "system-model").Updates(map[string]any{
		"protocol": "replacement-protocol", "provider_model_key": "replacement-model", "updated_at": time.Now().Add(time.Second),
	}).Error; err != nil {
		t.Fatal(err)
	}

	legacy := fmt.Sprintf(`{"mode":"video","config":{"channelId":%q,"model":%q}}`, f.channel, f.key)
	f.failOriginal(t, map[string]any{"input_json": legacy})
	if _, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID); err == nil || !strings.Contains(err.Error(), "原请求协议") || f.requests.Load() != 0 {
		t.Fatalf("missing original protocol: err=%v requests=%d", err, f.requests.Load())
	}

	legacy = fmt.Sprintf(`{"mode":"video","config":{"channelId":%q,"model":%q,"interfaceType":%q}}`, f.channel, f.key, originalProtocol)
	f.failOriginal(t, map[string]any{"input_json": legacy})
	if _, err := f.s.QueryFailedVideoTask(t.Context(), "user-1", f.original.ID); err == nil || !strings.Contains(err.Error(), "原上游模型") || f.requests.Load() != 0 {
		t.Fatalf("missing original model: err=%v requests=%d", err, f.requests.Load())
	}

	log := model.ApiCallLog{
		ID: newID(), UserID: "user-1", TaskID: f.original.ID, ChannelID: f.channel, Source: "backend-task", Capability: "video",
		RequestKind: "create", Status: model.ApiCallStatusSucceeded, ProviderRequestID: delistedReceipt,
		VideoProviderModelKey: "actual-upstream", UpstreamURL: f.server.URL + "/v1/videos", StartedAt: time.Now(),
	}
	if err := f.db.Create(&log).Error; err != nil {
		t.Fatal(err)
	}
	task, err := f.s.repo.TaskForUser("user-1", f.original.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := withProviderAnalytics(t.Context(), f.s, *task)
	cfg, pollOnly, err := f.s.resolveVideoReceiptConfig(ctx, providerConfig{ChannelID: f.channel, Model: f.key}, delistedReceipt)
	if err != nil || !pollOnly || cfg.Model != log.VideoProviderModelKey || cfg.InterfaceType != originalProtocol {
		t.Fatalf("original create receipt: model=%q protocol=%q pollOnly=%t err=%v", cfg.Model, cfg.InterfaceType, pollOnly, err)
	}
}
