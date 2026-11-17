package app

import (
	"testing"
	"yingce/backend/internal/model"
)

func TestSLLExternalSupplierCostViaChannelEditor(t *testing.T) {
	svc, db := newChannelModelTestService(t)
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	ch := model.ModelChannel{ID: "other-supplier", Scope: model.ChannelScopeSystem, Enabled: true, Name: "其他视频供应商", ModelsJSON: "[]"}
	if err := db.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	const key = "seedance-2.5-fixture"
	const protocol = "seedance-videos-compatible"
	cost := int64(5_950_000)
	req := ChannelModelRequest{ModelKey: key, ProviderModelKey: key, DisplayName: "Seedance 2.5", Capability: "video", Protocol: protocol,
		CapabilityConfig: DefaultModelCapabilityConfigForModel(protocol, key),
		PriceTiers:       []ChannelModelPriceTierRequest{{BillingMode: "fixed_request", PriceConfigured: true, UnitPriceMicrocredits: 17_000_000, UpstreamRequestCostMicroCNY: &cost}}}
	row, err := svc.SaveAdminChannelModel(admin, ch.ID, "", req)
	if err != nil {
		t.Fatal(err)
	}
	read := func() *model.ChannelModelPriceTier {
		t.Helper()
		v, err := svc.repo.ChannelModelByID(ch.ID, row.ID)
		if err != nil || len(v.PriceTiers) != 1 {
			t.Fatalf("price tiers %v %v", v, err)
		}
		return &v.PriceTiers[0]
	}
	tier := read()
	if tier.UpstreamRequestCostMicroCNY == nil || *tier.UpstreamRequestCostMicroCNY != cost || tier.UnitPriceMicrocredits != 17_000_000 {
		t.Fatal(tier)
	}
	req.PriceTiers[0].BillingMode = "per_second"
	if _, err := svc.SaveAdminChannelModel(admin, ch.ID, row.ID, req); err == nil {
		t.Fatal("按秒价档接受了每条成本证据")
	}
	ch.APIKey = "changed-fixture"
	if err := svc.repo.SaveChannelAndInvalidateSupplierCosts(&ch, true); err != nil {
		t.Fatal(err)
	}
	tier = read()
	if tier.UpstreamRequestCostMicroCNY != nil || tier.UnitPriceMicrocredits != 17_000_000 {
		t.Fatalf("凭据变更后成本和售价: %+v", tier)
	}
}
