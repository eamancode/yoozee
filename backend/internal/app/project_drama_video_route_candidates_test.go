package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/sllcatalog"
)

func TestProjectVideoRoutesComparableRates(t *testing.T) {
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	rate := func(source string, pct float64, age time.Duration, count int64) sllcatalog.RateView {
		at := now.Add(-age)
		view := sllcatalog.RateView{Source: source, State: "value", Rate: &pct, CollectedAt: &at, WindowHours: 24}
		if count >= 0 {
			view.SampleCount = &count
		}
		return view
	}
	sll := sllcatalog.Offer{ChannelID: "sll", CostMicroCNY: 5_000_000, Official: rate(sllcatalog.MetricsURL, 99, time.Hour, -1), Platform: rate("platform", 50, 0, 12)}
	other := sllcatalog.Offer{ChannelID: "other", CostMicroCNY: 6_000_000, Platform: rate("platform", 80, 0, 5)}
	unknown := sllcatalog.Offer{ChannelID: "unknown", CostMicroCNY: 1}
	old := sllcatalog.Offer{ChannelID: "stale", Platform: rate("platform", 100, 2*time.Hour, 200)}
	zero := sllcatalog.Offer{ChannelID: "zero", Platform: rate("platform", 0, 0, 1)}
	rows := rankProjectVideoOffers([]sllcatalog.Offer{old, zero, unknown, sll, other}, now)
	if rows[0].ChannelID != other.ChannelID || rows[1].ChannelID != sll.ChannelID || *rows[1].Rate != 50 || rows[2].Rate == nil || *rows[2].Rate != 0 || rows[3].Rate != nil || rows[4].Rate != nil {
		t.Fatalf("mixed suppliers/old/unknown/zero: %+v", rows)
	}
	sll2 := sll
	sll2.ChannelID = "another-sll-credential"
	sll2.Official = rate(sllcatalog.MetricsURL, 98, 30*time.Minute, -1)
	rows = rankProjectVideoOffers([]sllcatalog.Offer{sll2, sll}, now)
	if rows[0].ChannelID != sll.ChannelID || rows[0].Source != sllcatalog.MetricsURL || *rows[0].Rate != 99 || rows[0].SampleCount != nil {
		t.Fatalf("official rates without published sample counts: %+v", rows)
	}
	sll.Official.Stale = true
	sll.Platform = sllcatalog.RateView{}
	rows = rankProjectVideoOffers([]sllcatalog.Offer{sll, sll2}, now)
	if rows[0].ChannelID != sll2.ChannelID || rows[1].Rate != nil {
		t.Fatalf("stale official rate ranked: %+v", rows)
	}
	rows = rankProjectVideoOffers([]sllcatalog.Offer{
		{ChannelID: "c", CostMicroCNY: 2, Platform: rate("platform", 80, 0, 20)},
		{ChannelID: "b", CostMicroCNY: 1, Platform: rate("platform", 80, 0, 10)},
		{ChannelID: "d", CostMicroCNY: 1, Platform: rate("platform", 80, 0, 20)},
		{ChannelID: "a", CostMicroCNY: 1, Platform: rate("platform", 80, 0, 20)},
	}, now)
	var ids []string
	for _, row := range rows {
		ids = append(ids, row.ChannelID)
	}
	if !slices.Equal(ids, []string{"a", "d", "c", "b"}) {
		t.Fatalf("tiebreak order %v", ids)
	}
}

func TestProjectVideoRouteCandidatesCheckActualRequest(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	add := func(id string, cost int64, maxAudio int, audio bool, resolution string) {
		t.Helper()
		profile := DefaultModelCapabilityConfigForModel("seedance-videos-compatible", "seedance-2.5-test")
		profile.Video.Duration = VideoDurationConfig{Selection: "enum", Values: []int{15, 30}, Default: 15}
		profile.Video.References.MaxAudios, profile.Video.References.MaxImages = maxAudio, 7
		profile.Video.Operations = []string{"reference_to_video"}
		profile.Video.DefaultOperation = "reference_to_video"
		profile.Video.GenerateAudio.Supported = audio
		profile.Video.Resolutions, profile.Video.DefaultResolution = []string{resolution}, resolution
		capability, err := json.Marshal(profile)
		if err != nil {
			t.Fatal(err)
		}
		m := model.ChannelModel{ID: "model-" + id, ChannelID: id, ModelKey: "seedance-2.5", ProviderModelKey: "seedance-2.5-sku", DisplayName: "Seedance 2.5", Enabled: true, Capability: "video", Protocol: "seedance-videos-compatible", BillingMode: "fixed_request", PriceConfigured: true, CapabilityConfigJSON: string(capability)}
		selector := `{"videoSeconds":"30"}`
		tier := model.ChannelModelPriceTier{ID: "tier-" + id, ChannelModelID: m.ID, Enabled: true, ProviderModelKey: m.ProviderModelKey, SelectorKey: selector, SelectorJSON: selector, PriceConfigured: true, BillingMode: "fixed_request", UnitPriceMicrocredits: 8_000_000}
		if cost >= 0 {
			tier.UpstreamRequestCostMicroCNY = &cost
		}
		for _, record := range []any{&model.ModelChannel{ID: id, Name: id, Scope: model.ChannelScopeSystem, Enabled: true, BaseURL: "https://" + id + ".invalid", APIKey: "fixture"}, &m, &tier} {
			if err := f.db.Create(record).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	add("failed", 1, 2, true, "720p")
	add("no-voice-ref", 1, 0, true, "720p")
	add("no-generated-sound", 1, 2, false, "720p")
	add("wrong-resolution", 1, 2, true, "480p")
	add("over-budget", 6_010_000, 2, true, "720p")
	add("unverified-cost", -1, 2, true, "720p")
	add("hidden", 1, 2, true, "720p")
	add("fits", 6_000_000, 2, true, "720p")
	add("alternative", 5_000_000, 2, true, "720p")
	group := model.UserGroup{ID: "group", Code: "project-group", ModelOverridesJSON: "[]"}
	if err := f.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"failed", "no-voice-ref", "no-generated-sound", "wrong-resolution", "over-budget", "unverified-cost", "fits", "alternative"} {
		if err := f.db.Create(&model.UserGroupChannel{GroupID: group.ID, ChannelID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&model.User{}).Where("id = ?", "user-1").Update("user_group", group.Code).Error; err != nil {
		t.Fatal(err)
	}
	f.s.invalidateUserGroups()
	input := map[string]any{
		"mode": "video", "metadata": map[string]any{"domainProjectId": f.projectID, "shotId": "s0", "videoEditOperation": "reference_to_video"},
		"config":          map[string]any{"channelId": "failed", "model": "seedance-2.5", "priceTierId": "tier-failed", "videoSeconds": "6", "size": "9:16", "vquality": "720p", "videoGenerateAudio": "true"},
		"referenceImages": make([]any, 7), "referenceAudios": []any{map[string]any{"id": "voice", "mimeType": "audio/mpeg"}},
	}
	get := func() ([]projectVideoRouteCandidate, string) {
		t.Helper()
		rows, reason, err := f.s.projectVideoRouteCandidates(context.Background(), "user-1", f.projectID, "failed", "seedance-2.5-sku", input, 21, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		return rows, reason
	}
	rows, reason := get()
	if len(rows) != 2 || rows[0].ChannelID != "alternative" || rows[0].Rate != nil || rows[1].Rate != nil || !strings.Contains(reason, "手动") || rows[1].CostMicroCNY != 6_000_000 || rows[1].PriceTierID != "tier-fits" {
		t.Fatalf("wrong route, SKU or missing explanation: %+v %s", rows, reason)
	}
	// A channel's high success rate must never compensate for an unsupported
	// audio reference. Multiple logged calls remain one terminal task.
	done := time.Now().UTC().Add(-time.Minute)
	for _, sample := range []struct {
		channel string
		status  model.TaskStatus
	}{{"no-voice-ref", model.TaskStatusSucceeded}, {"over-budget", model.TaskStatusSucceeded}, {"fits", model.TaskStatusSucceeded}, {"fits", model.TaskStatusFailed}, {"alternative", model.TaskStatusFailed}} {
		task := model.Task{ID: newID(), UserID: "user-1", Type: "canvas_video", Status: sample.status, CompletedAt: &done}
		if err := f.db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"create", "create", "poll"} {
			log := model.ApiCallLog{ID: newID(), TaskID: task.ID, ChannelID: sample.channel, VideoProviderModelKey: "seedance-2.5-sku", Capability: "video", RequestKind: kind, Method: "POST", Source: "backend-task"}
			if err := f.db.Create(&log).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	rows, reason = get()
	if len(rows) != 2 || rows[0].ChannelID != "fits" || rows[0].Rate == nil || *rows[0].Rate != 50 || rows[0].SampleCount == nil || *rows[0].SampleCount != 2 || rows[1].Rate == nil || *rows[1].Rate != 0 || reason != "" {
		t.Fatalf("terminal rate ranking: %+v %s", rows, reason)
	}
	if inputConfigValue(input, "priceTierId") != "tier-failed" || inputConfigValue(input, "videoSeconds") != "6" {
		t.Fatal("modified failed request")
	}
	input["referenceImages"] = make([]any, 8)
	rows, reason = get()
	if len(rows) != 0 || !strings.Contains(reason, "8 张参考图") {
		t.Fatalf("missing capacity exclusion %v %s", rows, reason)
	}
}
