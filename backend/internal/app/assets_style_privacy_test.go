package app

import (
	"encoding/json"
	"strings"
	"testing"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestStyleLibraryPrivacy(t *testing.T) {
	for _, source := range []string{DramaStyleLibrarySourceDirector, DramaStyleLibrarySourceAnalysis} {
		entry := &model.DramaStyleLibraryEntry{Source: source, CreatedBy: "owner"}
		if !canReadDramaStyleLibrary("owner", entry) || canReadDramaStyleLibrary("other", entry) || canReadDramaStyleLibrary("", entry) {
			t.Fatalf("private visibility failed for %s", source)
		}
		if canEditDramaStyleLibrary(&model.User{ID: "admin", Role: model.UserRoleAdmin}, entry) {
			t.Fatal("admin must not access another user's personal director style")
		}
	}
	for _, source := range []string{DramaStyleLibrarySourceSeed, DramaStyleLibrarySourceManual} {
		entry := &model.DramaStyleLibraryEntry{Source: source, CreatedBy: "owner"}
		if !canReadDramaStyleLibrary("other", entry) || canReadDramaStyleLibrary("", entry) {
			t.Fatalf("public authenticated visibility failed for %s", source)
		}
	}
}

func TestCombinedDirectorProfilePreservesPublicFieldsAndReplacesDirectorSuffix(t *testing.T) {
	base := &model.Project{StyleProfileJSON: `{"schemaVersion":1,"presetId":"film","source":"system","revision":3,"title":"公共","description":"基础","tags":["胶片"],"prompt":"公共九项规格","negativePrompt":"不要美颜","assets":[],"selection":{"medium":"live-action"}}`}
	anchor := &drama.StyleAnchor{ArtStyle: "导演新规格"}
	first, err := combinedDirectorProfile(base, anchor, drama.MasterPalette{}, "导演结果", "组合说明", "one", nil)
	if err != nil {
		t.Fatal(err)
	}
	var profile map[string]any
	if err := json.Unmarshal([]byte(first), &profile); err != nil {
		t.Fatal(err)
	}
	if profile["negativePrompt"] != "不要美颜" || profile["selection"].(map[string]any)["medium"] != "live-action" || !strings.Contains(profile["prompt"].(string), "公共九项规格") {
		t.Fatalf("lost public fields: %+v", profile)
	}
	base.StyleProfileJSON = first
	second, err := combinedDirectorProfile(base, anchor, drama.MasterPalette{}, "新名称", "新说明", "one", nil)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(second), &profile)
	if strings.Count(profile["prompt"].(string), "【项目导演风格】") != 1 || profile["title"] != "新名称" {
		t.Fatalf("stale or duplicated director profile: %+v", profile)
	}
}

func TestPropPriorityPromotesWithoutLosingBodyOrState(t *testing.T) {
	row := drama.AssetsExtractRow{Category: drama.AssetProp, Name: "旧推车", Appearance: "台面堆着橙子，阿婆站在后方", PromptEn: "empty old cart", Design: &drama.AssetDesign{ProductionPriority: "low", BodyDescription: "掉漆的旧推车，带抽屉和把手", Props: []string{"橙子"}}}
	definition := assetsRowDefinition(row, 1, nil, drama.MasterPalette{})
	if definition["description"] != row.Design.BodyDescription || definition["productionPriority"] != "low" {
		t.Fatalf("body polluted: %+v", definition)
	}
	encoded, _ := json.Marshal(definition)
	var stored map[string]any
	json.Unmarshal(encoded, &stored)
	entry := &dramaAssetEntry{asset: model.Asset{Category: model.AssetCategoryProp}, definition: stored}
	next := drama.AssetsExtractRow{Category: drama.AssetProp, Name: row.Name, Design: &drama.AssetDesign{ProductionPriority: "high", States: []drama.AssetState{{Location: "数日后", State: "更换车轮"}}}}
	mergeIntoEntry(entry, next, assetsRowDefinition(next, 1, nil, drama.MasterPalette{}))
	design := entry.definition["extractionDesign"].(*drama.AssetDesign)
	if entry.definition["productionPriority"] != "high" || design.BodyDescription != row.Design.BodyDescription || len(design.States) != 1 || len(design.Props) != 1 {
		t.Fatalf("merge lost production design: %+v", entry.definition)
	}
}

func TestCharacterRepresentationsManualUploadReady(t *testing.T) {
	ordinary := []model.AssetRepresentation{{Role: "primary", MetadataJSON: `{}`}}
	if characterRepresentationsReady(ordinary) {
		t.Fatal("ordinary primary is not a confirmed reference")
	}
	manual := []model.AssetRepresentation{{Role: "primary", MetadataJSON: `{"source":"manual-upload"}`}}
	if !characterRepresentationsReady(manual) {
		t.Fatal("manual image must be usable as the selected reference")
	}
	library := []model.AssetRepresentation{{Role: "primary", MetadataJSON: `{"source":"asset-library"}`}}
	if !characterRepresentationsReady(library) {
		t.Fatal("explicitly selected library image must be usable without model generation")
	}
}
