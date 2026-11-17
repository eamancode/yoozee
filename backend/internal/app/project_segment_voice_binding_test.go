package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func seedVoiceBindingActors(t *testing.T, db *gorm.DB, p model.Project) {
	t.Helper()
	insert := func(row any) {
		t.Helper()
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, sample := range []string{"shared", "current", "recast", "listener"} {
		insert(&model.Resource{ID: "sample-" + sample, UserID: p.UserID, Kind: "audio", MimeType: "audio/wav", Status: model.ResourceStatusReady})
		insert(&model.VoiceProfile{ID: "profile-" + sample, UserID: p.UserID, Name: sample, Provider: "user_upload", VoiceKey: sample, SampleResourceID: "sample-" + sample, Status: "active"})
	}
	for _, card := range []struct {
		id, name, parent string
		samples          []string
	}{
		{"actor", "甲", "", []string{"shared", "current"}},
		{"costume", "甲·雨衣", "actor", []string{""}},
		{"clerk", "乙", "", []string{"shared"}},
		{"recast", "新演员", "", []string{"recast"}},
		{"listener", "丙", "", []string{"listener"}},
	} {
		asset := model.Asset{ID: card.id, UserID: p.UserID, Title: card.name, Kind: "entity", Category: model.AssetCategoryCharacter,
			Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: fmt.Sprintf("%s-v%d", card.id, len(card.samples)), ParentAssetID: card.parent, PayloadJSON: "{}"}
		if card.parent != "" {
			asset.VariantLabel = "雨衣"
		}
		insert(&asset)
		insert(&model.ProjectAssetLink{ID: "link-" + card.id, ProjectID: p.ID, AssetID: card.id})
		for i, sample := range card.samples {
			id := fmt.Sprintf("%s-v%d", card.id, i+1)
			insert(&model.AssetVersion{ID: id, AssetID: card.id, Version: i + 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: "{}"})
			insert(&model.Resource{ID: "image-" + id, UserID: p.UserID, Kind: "image", MimeType: "image/png", Status: model.ResourceStatusReady})
			insert(&model.AssetRepresentation{ID: "rep-" + id, AssetVersionID: id, TaskID: "image-task-" + id, ResourceID: "image-" + id, MediaType: "image/png", Role: "primary"})
			if sample != "" {
				insert(&model.CharacterVoiceBinding{ID: "voice-" + id, AssetVersionID: id, VoiceProfileID: "profile-" + sample})
			}
		}
	}
}

func TestSegmentVoiceBindingEditsRefreshWorkspaceAndCompile(t *testing.T) {
	s, db, p, u := newProjectDramaTestService(t, "seedance-2.5")
	seedVoiceBindingActors(t, db, p)
	seg := drama.Segment{ID: "S01", Scene: "客厅", Dur: 15, Mode: drama.ModeLockIdentity,
		Refs:     []drama.Ref{{File: "actor-v1", Role: "identity"}, {File: "clerk-v1", Role: "identity"}, {File: "costume-v1", Role: "identity"}},
		Shots:    []drama.SubShot{{Text: "甲与乙说话，丙听着。", Sound: "甲（画外）：“开门。”；乙：“来了。”"}},
		Verbatim: "生效参考素材\n@图1、@图2、@图3。\n声音\n本段不使用背景音乐。"}
	raw, err := json.Marshal(seg)
	if err != nil {
		t.Fatal(err)
	}
	shot, err := s.CreateProjectShot(p.UserID, p.ID, CreateProjectShotRequest{UnitID: u.ID, Title: "导演单元", Revision: ShotRevisionInput{Spec: raw}})
	if err != nil {
		t.Fatal(err)
	}
	link := func(version, role, replaceID string) model.ShotAssetReference {
		t.Helper()
		row, err := s.LinkShotAsset(p.UserID, p.ID, shot.ID, LinkShotAssetRequest{AssetVersionID: version, Role: role, ReplaceReferenceID: replaceID})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	style := link("costume-v1", "style", "")
	link("listener-v1", "reference", "")
	versionSamples := map[string]string{"actor-v1": "sample-shared", "actor-v2": "sample-current", "clerk-v1": "sample-shared",
		"costume-v1": "", "listener-v1": "sample-listener", "recast-v1": "sample-recast"}
	check := func(visuals, audio []string, sent []DramaVoiceBinding) map[string]string {
		t.Helper()
		ws, err := s.ProjectUnitWorkspace(p.UserID, p.ID, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		refs := map[string]string{}
		keepStyle := false
		for _, ref := range ws.ShotReferences {
			if ref.Role == "style" {
				keepStyle = ref.ID == style.ID && ref.AssetVersionID == style.AssetVersionID
				continue
			}
			v := ref.ReferencedVersion
			if !slices.Contains(visuals, v.ID) || v.SampleResourceID != versionSamples[v.ID] || len(v.Representations) != 1 || v.Representations[0].ResourceID != "image-"+v.ID {
				t.Fatalf("workspace pinned binding = %+v", v)
			}
			refs[v.ID] = ref.ID
		}
		if !keepStyle || len(refs) != len(visuals) {
			t.Fatalf("workspace lost a slot: %v, style %v", refs, keepStyle)
		}
		compiled, err := s.CompileProjectShot(p.UserID, p.ID, shot.ID, CompileProjectShotRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if compiled.Derived || drama.HasError(compiled.Result.Issues) || !slices.Equal(compiled.VoiceBindings, sent) {
			t.Fatalf("compiled bindings = %+v, issues = %+v", compiled.VoiceBindings, compiled.Result.Issues)
		}
		var images, sounds []string
		for _, f := range compiled.Result.Files {
			var tag string
			if f.Role == "voice" {
				sounds = append(sounds, f.File)
				tag = fmt.Sprintf("@音频%d", len(sounds))
			} else {
				images = append(images, f.File)
				tag = fmt.Sprintf("@图片%d", len(images))
			}
			if f.Tag != tag || !strings.Contains(compiled.Result.Prompt, tag) {
				t.Fatalf("file %+v missing its prompt slot %s", f, tag)
			}
		}
		if !slices.Equal(images, visuals) || !slices.Equal(sounds, audio) {
			t.Fatalf("manifest images=%v sounds=%v", images, sounds)
		}
		return refs
	}
	const first, second = "@音频1", "@音频2"
	images := []string{"actor-v1", "clerk-v1", "costume-v1", "listener-v1"}
	refs := check(images, []string{"sample-shared"}, []DramaVoiceBinding{{"actor-v1", "sample-shared", first}, {"clerk-v1", "sample-shared", first}})
	for _, next := range []string{"actor-v2", "recast-v1"} {
		link(next, "reference", refs[images[0]])
		images[0] = next
		refs = check(images, []string{versionSamples[next], "sample-shared"}, []DramaVoiceBinding{{next, versionSamples[next], first}, {"clerk-v1", "sample-shared", second}})
	}
	if err := s.UnlinkShotAsset(p.UserID, p.ID, shot.ID, refs["recast-v1"]); err != nil {
		t.Fatal(err)
	}
	check(images[1:], []string{"sample-shared"}, []DramaVoiceBinding{{"clerk-v1", "sample-shared", first}})
}

func TestManualShotVoiceBindingsUsePinnedImagedVersionsWithoutDialogue(t *testing.T) {
	s, db, p, u := newProjectDramaTestService(t, "seedance-2.5")
	seedVoiceBindingActors(t, db, p)
	shot, err := s.CreateProjectShot(p.UserID, p.ID, CreateProjectShotRequest{UnitID: u.ID, Title: "旧镜头", Description: "众人站定。", DurationMs: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Where("asset_version_id = ?", "listener-v1").Delete(&model.AssetRepresentation{}).Error; err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"actor-v1", "clerk-v1", "costume-v1", "listener-v1"} {
		if _, err := s.LinkShotAsset(p.UserID, p.ID, shot.ID, LinkShotAssetRequest{AssetVersionID: version, Role: "reference"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.CompileProjectShot(p.UserID, p.ID, shot.ID, CompileProjectShotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	want := []DramaVoiceBinding{{"actor-v1", "sample-shared", "@音频1"}, {"clerk-v1", "sample-shared", "@音频1"}}
	if !got.Derived || !slices.Equal(got.VoiceBindings, want) || len(got.Spec.Voices) != 1 || got.Spec.Voices[0].File != "sample-shared" {
		t.Fatalf("legacy pinned reference audio = %+v, bindings %+v", got.Spec.Voices, got.VoiceBindings)
	}
	h3, err := s.CompileProjectShot(p.UserID, p.ID, shot.ID, CompileProjectShotRequest{ChannelModel: "minimax-h3"})
	if err != nil || len(h3.VoiceBindings) != 2 || h3.VoiceBindings[0].Tag != "<Audio 1>" || !strings.Contains(h3.Result.Prompt, "<Audio 1>") {
		t.Fatalf("selected route dialect: bindings %+v, err %v", h3.VoiceBindings, err)
	}
}
