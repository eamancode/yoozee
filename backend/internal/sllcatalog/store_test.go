package sllcatalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
)

func newTestStore(t *testing.T) (*Store, model.ModelChannel, time.Time) {
	t.Helper()
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: "file:" + t.Name() + "?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&model.ModelChannel{}, &model.ChannelModel{}, &model.ChannelModelPriceTier{}, &model.IDSequence{}, &model.Task{}, &model.ApiCallLog{}, &model.SLLCatalogImport{}, &model.SLLCatalogLine{}, &model.SLLMetricHour{}); err != nil {
		t.Fatal(err)
	}
	ch := model.ModelChannel{ID: "sll", Scope: model.ChannelScopeSystem, Enabled: true, BaseURL: "https://api.lecapi.net/v1"}
	if err := db.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	return NewStore(db), ch, time.Date(2026, 11, 10, 12, 5, 0, 0, time.UTC)
}

func testEntry(key string) Entry {
	maxImages, maxVideos, maxAudios, maxReferences, maxVideoSeconds := 8, 1, 2, 8, 15
	return Entry{ModelID: key, DisplayName: "Seedance 2.5 同名", Group: "default", GroupRatio: 1,
		Tiers:        []Tier{{Resolution: "480p", CostMicroCNY: 3_000_000}, {Resolution: "720p", CostMicroCNY: 6_000_000}},
		Capabilities: Capabilities{DurationMin: 5, DurationMax: 30, AspectRatios: []string{"9:16"}, MinImages: 1, MaxImages: &maxImages, MaxVideos: &maxVideos, MaxAudios: &maxAudios, MaxReferences: &maxReferences, MaxVideoSeconds: &maxVideoSeconds, SendDuration: true, SendResolution: true}}
}

func TestStoreReimportChangesAndManualDisable(t *testing.T) {
	s, ch, at := newTestStore(t)
	ctx := context.Background()
	a, b := testEntry("lec-seedance-2-5-one"), testEntry("lec-seedance-2-5-two")
	c := Catalog{Entries: []Entry{a, b}, Version: "one"}
	sync := func(c Catalog) View {
		t.Helper()
		out, err := s.Import(ctx, ch.ID, "default", c, at)
		if err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Minute)
		return out
	}
	first := sync(c)
	if first.Added != 2 || len(first.Rows) != 2 || first.Rows[0].ChannelModelID == first.Rows[1].ChannelModelID {
		t.Fatalf("first %+v", first)
	}
	second := sync(c)
	if second.Added != 0 || second.Updated != 0 {
		t.Fatalf("unchanged %+v", second)
	}
	id := first.Rows[0].ChannelModelID
	var tier model.ChannelModelPriceTier
	if err := s.db.First(&tier, "channel_model_id = ? AND resolution = ?", id, "480p").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&tier).Update("unit_price_microcredits", 8_000_000).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&model.ChannelModel{}).Where("id = ?", first.Rows[1].ChannelModelID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	a.Tiers = a.Tiers[:1]
	a.Tiers[0].CostMicroCNY = 4_700_000
	c.Entries = []Entry{a, b}
	c.Excluded = []Exclusion{{ModelID: a.ModelID, Resolution: "720p", Reason: "超过6元"}}
	v := sync(c)
	if v.Rows[1].Enabled || len(v.Excluded) != 1 || v.Updated != 1 {
		t.Fatalf("change %+v", v)
	}
	offers, err := s.Offers(ctx, []string{ch.ID}, at)
	if err != nil || len(offers) != 1 || offers[0].CostMicroCNY != 4_700_000 || offers[0].PriceTierID != tier.ID {
		t.Fatalf("offers %+v %v", offers, err)
	}
	if err = s.db.First(&tier, "id = ?", tier.ID).Error; err != nil || tier.UnitPriceMicrocredits != 8_000_000 {
		t.Fatal("售价未保留", err)
	}
	sync(Catalog{Version: "withdrawn", Entries: []Entry{}})
	if err = s.db.Unscoped().First(&tier, "id = ?", tier.ID).Error; err != nil || !tier.DeletedAt.Valid {
		t.Fatal("历史价档无法追溯", err)
	}
	if offers, err = s.Offers(ctx, []string{ch.ID}, at); err != nil || len(offers) != 0 {
		t.Fatalf("withdrawn %+v %v", offers, err)
	}
	v = sync(c)
	if v.Rows[1].Enabled || !v.Rows[0].Available {
		t.Fatalf("relisted %+v", v)
	}
	if err = s.db.Model(&ch).Update("api_key", "rotated-fixture").Error; err != nil {
		t.Fatal(err)
	}
	ch.APIKey = ""
	if _, err = s.Import(ctx, ch.ID, "default", c, at, ch); err == nil {
		t.Fatal("旧凭据的同步覆盖了当前凭据")
	}
	if offers, err = s.Offers(ctx, []string{ch.ID}, at); err != nil || len(offers) != 0 {
		t.Fatalf("credential change %+v %v", offers, err)
	}
}

func TestStoreRequestUsesExactProviderAndCapabilities(t *testing.T) {
	s, ch, now := newTestStore(t)
	ctx := context.Background()
	e := testEntry("lec-seedance-2-5")
	v, err := s.Import(ctx, ch.ID, "default", Catalog{Entries: []Entry{e}}, now)
	if err != nil {
		t.Fatal(err)
	}
	id := v.Rows[0].ChannelModelID
	valid := RequestSpec{Resolution: "720p", AspectRatio: "9:16", Seconds: 26, Images: 7, Audios: 1}
	cost, err := s.CheckRequest(ctx, id, e.ModelID, valid)
	if err != nil || cost == nil || *cost != 6_000_000 {
		t.Fatal(cost, err)
	}
	for _, mutate := range []func(*RequestSpec){
		func(r *RequestSpec) { r.PriceTierID = "other-specific-sku" },
		func(r *RequestSpec) { r.Resolution = "1080p" }, func(r *RequestSpec) { r.AspectRatio = "1:1" }, func(r *RequestSpec) { r.Seconds = 31 },
		func(r *RequestSpec) { r.Images = 0 }, func(r *RequestSpec) { r.Images = 9 }, func(r *RequestSpec) { r.Audios = 2 },
		func(r *RequestSpec) { r.Videos = 1; r.Images = 1; r.LongestVideoSeconds = 15.1 }, func(r *RequestSpec) { r.GenerateAudio = true },
	} {
		r := valid
		mutate(&r)
		if _, err = s.CheckRequest(ctx, id, e.ModelID, r); err == nil {
			t.Errorf("accepted %+v", r)
		}
	}
	if _, err = s.CheckRequest(ctx, id, "different", valid); err == nil {
		t.Fatal("wrong SKU accepted")
	}
	if err = s.db.Model(&model.ChannelModel{}).Where("id = ?", id).Update("capability_config_json", "{}").Error; err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckRequest(ctx, id, e.ModelID, valid); err == nil {
		t.Fatal("wire contract drift accepted")
	}
}

func TestStoreAdoptsLegacyScalarSaleAndSkipsConflictingAlias(t *testing.T) {
	s, ch, at := newTestStore(t)
	e, conflict := testEntry("lec-seedance-2-5-a"), testEntry("lec-seedance-2-5-b")
	old := model.ChannelModel{ID: "legacy", ChannelID: ch.ID, ModelKey: "legacy-alias", ProviderModelKey: e.ModelID, Capability: "video", Enabled: false, PriceCurrency: "credits", BillingMode: "fixed_request", PriceConfigured: true, UnitPriceMicrocredits: 17_000_000}
	other := model.ChannelModel{ID: "conflict", ChannelID: ch.ID, ModelKey: conflict.ModelID, ProviderModelKey: "different", Capability: "video"}
	for _, item := range []*model.ChannelModel{&old, &other} {
		if err := s.db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	v, err := s.Import(t.Context(), ch.ID, "default", Catalog{Entries: []Entry{conflict, e}}, at)
	if err != nil || len(v.Rows) != 1 || len(v.Excluded) != 1 || v.Rows[0].ChannelModelID != old.ID || v.Rows[0].Enabled {
		t.Fatal(v, err)
	}
	var tiers []model.ChannelModelPriceTier
	if err = s.db.Where("channel_model_id = ?", old.ID).Find(&tiers).Error; err != nil || len(tiers) != 2 {
		t.Fatal(tiers, err)
	}
	for _, tier := range tiers {
		if !tier.PriceConfigured || tier.UnitPriceMicrocredits != 17_000_000 {
			t.Fatal(tier)
		}
	}
}

func TestStoreHoursAndCrossSupplierTerminalRates(t *testing.T) {
	s, ch, at := newTestStore(t)
	ctx := context.Background()
	e := testEntry("lec-seedance-2-5")
	if _, err := s.Import(ctx, ch.ID, "default", Catalog{Entries: []Entry{e}}, at); err != nil {
		t.Fatal(err)
	}
	zero, ninety := 0.0, 90.0
	record := func(t0 time.Time, m map[string]Metric, err error) {
		t.Helper()
		if fail := s.RecordMetrics(ctx, t0, m, err); fail != nil {
			t.Fatal(fail)
		}
	}
	view := func(t0 time.Time) RateView {
		t.Helper()
		v, err := s.View(ctx, ch.ID, t0)
		if err != nil {
			t.Fatal(err)
		}
		return v.Rows[0].Metric
	}
	record(at, nil, errors.New("unavailable"))
	if v := view(at); v.State != "fetch_failed" || v.CollectedAt != nil {
		t.Fatal(v)
	}
	record(at.Add(time.Minute), map[string]Metric{e.ModelID: {Rate: &zero}}, nil)
	record(at.Add(2*time.Minute), map[string]Metric{e.ModelID: {Rate: &ninety}}, nil)
	next := at.Add(time.Hour)
	if v := view(next); v.State != "value" || v.Rate == nil || *v.Rate != 0 || v.SampleCount != nil {
		t.Fatal(v)
	}
	record(next, nil, errors.New("offline"))
	if v := view(at.Add(3 * time.Hour)); v.State != "fetch_failed" || !v.Stale || !v.CollectedAt.Equal(at.Add(time.Minute)) {
		t.Fatal(v)
	}
	record(next.Add(time.Minute), map[string]Metric{"other-id": {Rate: &ninety}}, nil)
	if v := view(next.Add(time.Minute)); v.State != "unavailable" || v.Rate != nil {
		t.Fatal(v)
	}
	var n int64
	if err := s.db.Model(&model.SLLMetricHour{}).Count(&n).Error; err != nil || n != 2 {
		t.Fatal(n, err)
	}
	other := model.ModelChannel{ID: "another", Enabled: true, Scope: model.ChannelScopeSystem, BaseURL: "https://supplier.invalid"}
	item := model.ChannelModel{ID: "other-model", ChannelID: other.ID, ModelKey: e.ModelID, ProviderModelKey: e.ModelID, Enabled: true, PriceConfigured: true, Capability: "video"}
	micros := int64(4_000_000)
	tier := model.ChannelModelPriceTier{ID: "other-tier", ChannelModelID: item.ID, ProviderModelKey: e.ModelID, SelectorJSON: "{}", SelectorKey: "{}", Resolution: "*", Enabled: true, PriceConfigured: true, BillingMode: "fixed_request", UpstreamRequestCostMicroCNY: &micros}
	for _, row := range []any{&other, &item, &tier} {
		if err := s.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, status := range []model.TaskStatus{model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusFailed} {
		id := []string{"done", "failed", "ambiguous"}[i]
		if err := s.db.Create(&model.Task{ID: id, Type: "canvas_video", Status: status, CompletedAt: &at}).Error; err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"first", "duplicate", "poll"} {
			key := "create"
			if kind == "poll" {
				key = "poll"
			}
			channel := other.ID
			if id == "ambiguous" && kind == "duplicate" {
				channel = ch.ID
			}
			log := model.ApiCallLog{ID: id + kind, TaskID: id, ChannelID: channel, VideoProviderModelKey: e.ModelID, Capability: "video", Source: "backend-task", RequestKind: key, Method: "POST"}
			if err := s.db.Create(&log).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	offers, err := s.Offers(ctx, []string{other.ID}, next)
	if err != nil || len(offers) != 1 {
		t.Fatal(offers, err)
	}
	if v := offers[0]; v.Official.Rate != nil || v.Platform.Rate == nil || *v.Platform.Rate != 50 || *v.Platform.SampleCount != 2 {
		t.Fatalf("%+v", v)
	}
}
