package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
	"yingce/backend/internal/sllcatalog"
)

func TestSLLQuoteAndExecution(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	ctx := t.Context()
	ch := model.ModelChannel{ID: "sll-fixture", Scope: model.ChannelScopeSystem, Enabled: true, Name: "SLL", BaseURL: "https://api.lecapi.net", APIKey: "fixture-only"}
	if err := f.db.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	allowed := 2
	e := sllcatalog.Entry{
		ModelID: "lec-fixture-seedance-2-5", DisplayName: "Seedance 2.5（夹具）", Group: "default", GroupRatio: 1,
		Tiers: []sllcatalog.Tier{{Resolution: "720p", CostMicroCNY: 6_000_000, PriceCNY: 6}},
		Capabilities: sllcatalog.Capabilities{
			Durations: []int{30}, DurationMin: 30, DurationMax: 30, AspectRatios: []string{"16:9"},
			FixedResolution: "720p", MinImages: 1, MaxImages: &allowed, MaxAudios: &allowed,
			MaxReferences: &allowed, ImageMIMEs: []string{"image/png"}, AudioMIMEs: []string{"audio/mpeg"},
		},
	}
	catalog := sllcatalog.Catalog{Version: "fixture-v1", Entries: []sllcatalog.Entry{e}}
	if _, err := f.s.repo.SLLCatalog().Import(ctx, ch.ID, "default", catalog, time.Now()); err != nil {
		t.Fatal(err)
	}
	cm, err := f.s.repo.ChannelModelByKey(ch.ID, e.ModelID)
	if err != nil || len(cm.PriceTiers) != 1 {
		t.Fatalf("imported route: %+v %v", cm, err)
	}
	// Use a different sale price to prove the quote does not label supplier RMB as credits.
	if err := f.db.Model(&model.ChannelModelPriceTier{}).Where("id = ?", cm.PriceTiers[0].ID).Update("unit_price_microcredits", 8_000_000).Error; err != nil {
		t.Fatal(err)
	}
	req := ChannelModelQuoteRequest{ChannelID: ch.ID, ModelKey: cm.ModelKey, Intent: ModelRequestIntent{
		Capability: "video", Operation: "reference_to_video", Inputs: map[string]int{"image": 1, "audio": 1},
		Options: map[string]any{"videoSeconds": 30, "vquality": "720p", "size": "16:9"},
	}}
	quote, err := f.s.QuoteChannelModel("user-1", req)
	if err != nil || quote.UpstreamCost == nil || quote.UpstreamCost.AmountMicros != 6_000_000 || quote.UpstreamCost.Unit != "request" || quote.AmountMicrocredits != 8_000_000 {
		t.Fatalf("wrong supplier/user quote: %+v %v", quote, err)
	}
	req.Intent.Inputs["image"] = 2
	if _, err := f.s.QuoteChannelModel("user-1", req); err == nil {
		t.Fatal("combined reference cap was ignored")
	}
	req.Intent.Inputs["image"] = 1
	req.Intent.Options["vquality"] = "1080p"
	if _, err := f.s.QuoteChannelModel("user-1", req); err == nil {
		t.Fatal("unsold resolution quoted")
	}

	input := canvasGenerationInput{Mode: "video", Prompt: "fixture",
		Config:          providerConfig{ChannelID: ch.ID, ChannelModelKey: cm.ModelKey, ProviderModelKey: e.ModelID, PriceTierID: cm.PriceTiers[0].ID, Model: e.ModelID, InterfaceType: string(cm.Protocol), VideoSeconds: "30", VQuality: "720p", Size: "16:9"},
		ReferenceImages: []providerMedia{{ID: "actor", MimeType: "image/png", URL: "https://assets.example/actor.png"}},
		ReferenceAudios: []providerMedia{{ID: "voice", MimeType: "audio/mpeg", URL: "https://assets.example/voice.mp3"}},
	}
	if err := f.s.checkSLLVideoInput(ctx, cm.ID, input); err != nil {
		t.Fatal(err)
	}
	input.Metadata = map[string]any{"providerOptions": map[string]any{string(cm.Protocol): map[string]any{"body": map[string]any{"resolution": "1080p"}}}}
	if err := f.s.checkSLLVideoInput(ctx, cm.ID, input); err == nil {
		t.Fatal("unpriced overrides admitted")
	}
	input.Metadata = nil
	if err := f.s.validateResolvedVideoCapability(&input); err != nil {
		t.Fatal(err)
	}
	// Exercise the installed adapter and real HTTP encoding against localhost.
	// The database fixture has no network role. Only the localhost mock receives a request.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/videos" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body struct {
			Model      string   `json:"model"`
			Images     []string `json:"images"`
			Audios     []string `json:"audios"`
			Seconds    *int     `json:"duration"`
			Resolution *string  `json:"resolution"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != e.ModelID || body.Seconds != nil || body.Resolution != nil ||
			len(body.Images) != 1 || body.Images[0] != input.ReferenceImages[0].URL ||
			len(body.Audios) != 1 || body.Audios[0] != input.ReferenceAudios[0].URL {
			t.Errorf("incorrect fixed-spec wire request: %+v %v", body, err)
		}
		_, _ = w.Write([]byte(`{"id":"fixture-job"}`))
	}))
	defer server.Close()
	input.Config.BaseURL, input.Config.APIKey = server.URL, "fixture-only"
	adapter, ok := f.s.protocolRegistry().Resolve(string(cm.Protocol))
	if !ok {
		t.Fatal("missing Seedance adapter")
	}
	payload, err := adapter.BuildCreate(ctx, protocol.RequestContext{BaseURL: server.URL, Request: protocolRequestFromInput(input)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeProtocolRequest(ctx, input.Config, payload); err != nil {
		t.Fatal(err)
	}
	// Revoke the SKU while its references are uploading. Only the fixture
	// upload may reach this server; the paid create must never be sent.
	uploaded := make(chan struct{}, 1)
	withdraw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/media/upload" {
			t.Errorf("paid request after withdrawal: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, err := f.s.repo.SLLCatalog().Import(ctx, ch.ID, "default", sllcatalog.Catalog{Version: "fixture-v2"}, time.Now()); err != nil {
			t.Error(err)
		}
		uploaded <- struct{}{}
		_, _ = w.Write([]byte(`{"url":"https://assets.example/new-image.png"}`))
	}))
	defer withdraw.Close()
	input.Config.BaseURL = withdraw.URL
	input.ReferenceImages[0].URL = ""
	input.ReferenceImages[0].DataURL = dataURL("image/png", mediaTestPNG(t))
	input.authorizeCreate = func(ctx context.Context) error { return f.s.checkSLLVideoInput(ctx, cm.ID, input) }
	if _, err := runProtocolAdapterTask(ctx, input, adapter); err == nil || len(uploaded) != 1 {
		t.Fatalf("expected withdrawal after upload: %v", err)
	}
	old, pollOnly, err := f.s.resolveVideoReceiptConfig(ctx, input.Config, "already-accepted-fixture")
	if err != nil || !pollOnly || old.ChannelID != ch.ID || old.Model != e.ModelID || old.BaseURL != ch.BaseURL {
		t.Fatalf("cannot track original receipt after removal: pollOnly=%t err=%v", pollOnly, err)
	}
}
