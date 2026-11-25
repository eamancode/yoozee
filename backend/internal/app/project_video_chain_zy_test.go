package app

import (
	"encoding/json"
	"testing"
	"yingce/backend/internal/model"
)

func TestVideoChainZYDurationQuote(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	seedOnlySlotSystemVideo(t, f)
	const zy = "lec-zy-seedance-2-5-discount2"
	const ch = "CHANNEL_000001"
	const cost int64 = 2625000
	p := DefaultModelCapabilityConfigForModel("seedance-videos-compatible", zy)
	p.Video.Duration = VideoDurationConfig{Selection: "range", Min: 4, Max: 30, Step: 1, Default: 30}
	p.Video.Operations = []string{"text_to_video", "reference_to_video"}
	p.Video.References.MaxImages = 9
	p.Video.GenerateAudio = VideoBooleanConfig{}
	raw, _ := json.Marshal(p)
	if err := f.db.Model(&model.ModelChannel{}).Where("id = ?", "system-video").Update("id", ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.ChannelModel{}).Where("id = ?", "system-model").Updates(map[string]any{"channel_id": ch, "model_key": zy, "provider_model_key": zy, "capability_config_json": string(raw)}).Error; err != nil {
		t.Fatal(err)
	}
	selector := `{"vquality":"720p"}`
	if err := f.db.Create(&model.ChannelModelPriceTier{ID: "zy-720p", ChannelModelID: "system-model", SelectorKey: selector, SelectorJSON: selector, ProviderModelKey: zy, BillingMode: "fixed_request", UnitPriceMicrocredits: cost, Enabled: true, PriceConfigured: true}).Error; err != nil {
		t.Fatal(err)
	}
	original := addHTTPVideoChainShot(t, f, "有效参考素材\n@图1、@图2、@图3、@图4、@图5、@图6、@图7、@图8。\n镜头内容\n阿婆整理推车，林深站在左边。", 20.5, 8)
	f.req.Scope, f.req.ShotID, f.req.RecoveryV2 = "single", "s1", true
	f.req.Model.Model = ch + "::" + zy
	f.req.Model.Input = globalSixSecondsRequest("false")
	cfg := f.req.Model.Input["config"].(map[string]any)
	cfg["channelId"], cfg["model"], cfg["vquality"] = ch, zy, "720p"
	view, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil || view.PreviewHash == "" || len(view.Steps) != 1 || view.TotalMicrocredits != cost {
		t.Fatalf("wrong quote: %+v %v", view, err)
	}
	step := view.Steps[0]
	if step.ContentSeconds != 20.5 || step.GenerationSeconds != 21 || step.ReferenceCount != 8 || step.Quote == nil || step.Quote.BillingMode != "fixed_request" || step.Quote.Quantity != 1 {
		t.Fatalf("wrong duration/reference split: %+v", step)
	}
	repeated, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil || repeated.TotalMicrocredits != view.TotalMicrocredits || repeated.Steps[0].GenerationSeconds != 21 {
		t.Fatalf("quote changed for same input: %v %+v", err, repeated)
	}
	f.req.PreviewHash, f.req.QuoteExpiresAt = "stale-duration-quote", view.QuoteExpiresAt
	if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, f.req); err == nil {
		t.Fatal("stale quote accepted")
	}
	assertVideoChainPreflightOnly(t, f)
	var ledger int64
	if err := f.db.Model(&model.CreditLedgerEntry{}).Count(&ledger).Error; err != nil || ledger != 0 {
		t.Fatalf("preview ledger writes %d: %v", ledger, err)
	}
	f.req.PreviewHash = view.PreviewHash
	controller, err := f.s.CreateProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil || controller.ID == "" || controller.Steps[0].GenerationSeconds != 21 {
		t.Fatalf("confirmation lost the 21-second quote: %+v %v", controller, err)
	}
	var saved model.Task
	if err := f.db.First(&saved, "id = ?", controller.ID).Error; err != nil {
		t.Fatal(err)
	}
	state, err := f.s.videoChainState(&saved)
	if err != nil || len(state.Steps) != 1 {
		t.Fatalf("confirmation did not bind one step: %v", err)
	}
	savedStep := state.Steps[0]
	savedConfig := savedStep.Input["config"].(map[string]any)
	if stringValue(savedConfig["videoSeconds"]) != "21" || stringValue(savedConfig["priceTierId"]) != "zy-720p" || savedStep.Quote.AmountMicrocredits != cost {
		t.Fatal("confirmation did not preserve the authorized route, duration, and charge")
	}
	receipt, err := f.s.ProjectVideoChainReceipt("user-1", f.projectID, f.req.UnitID, f.req.RequestID)
	if err != nil || receipt == nil || receipt.ID != controller.ID || receipt.Steps[0].GenerationSeconds != 21 {
		t.Fatalf("receipt did not recover confirmed duration: %+v %v", receipt, err)
	}
	if f.uploads.Load() != 0 {
		t.Fatal("quote or confirmation contacted the provider")
	}
	if err := f.db.Model(&model.CreditLedgerEntry{}).Count(&ledger).Error; err != nil || ledger != 0 {
		t.Fatalf("no worker ran, but there were ledger writes: %d %v", ledger, err)
	}
	var row model.ShotRevision
	if err := f.db.First(&row, "id = ?", "rs1").Error; err != nil || row.SpecJSON != original {
		t.Fatalf("original storyboard changed: %v", err)
	}
}
func TestVideoChainZYDurationBoundaries(t *testing.T) {
	zy := &model.ChannelModel{ChannelID: "CHANNEL_000001", ModelKey: "lec-zy-seedance-2-5-discount2", ProviderModelKey: "lec-zy-seedance-2-5-discount2"}
	other := &model.ChannelModel{ChannelID: "CHANNEL_000001", ProviderModelKey: "other"}
	flex := VideoDurationConfig{Selection: "range", Min: 4, Max: 30, Step: 1}
	for _, tc := range []struct {
		name    string
		item    *model.ChannelModel
		profile VideoDurationConfig
		content float64
		want    int
	}{
		{"minimum", zy, flex, 4, 4}, {"fractional", zy, flex, 20.5, 21}, {"exact", zy, flex, 21, 21}, {"near maximum", zy, flex, 29.99, 30}, {"maximum", zy, flex, 30, 30}, {"under minimum", zy, flex, 3.9, 0}, {"over maximum", zy, flex, 30.01, 0}, {"other route", other, flex, 20.5, 0}, {"untrusted route", nil, flex, 20.5, 0}, {"step two unchanged", zy, VideoDurationConfig{Selection: "range", Min: 4, Max: 30, Step: 2}, 20.5, 0}, {"legacy slots unchanged", zy, VideoDurationConfig{Selection: "enum", Values: []int{15, 30}}, 20.5, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := videoChainGenerationSecondsForRoute(tc.profile, tc.content, tc.item)
			if got != tc.want || (err == nil) != (tc.want != 0) {
				t.Errorf("got %d %v, want %d", got, err, tc.want)
			}
		})
	}
}
