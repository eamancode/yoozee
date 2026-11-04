package app

import (
	"image/color"
	"testing"
	"yingce/backend/internal/model"
)

func TestChildManualUploadPreservesVariantAndFencesOldCompletion(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedEpisodeProject(t, svc, db)
	parent := seedLinXiaoman(t, svc, db, project.ID)
	child, err := svc.CreateDramaAssetVariant("user-1", project.ID, parent.Asset.ID, DramaAssetVariantRequest{Label: "淋湿", Look: "护士服湿透", Scenes: []string{"E01S01"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.putDramaImageState("user-1", child.Asset.ID, assetImageKey, dramaImageState{Status: dramaImageQueued, TaskID: "old-image", Error: "old error"}); err != nil {
		t.Fatal(err)
	}
	imageID := uploadTestImage(t, svc, "user-1", color.White)
	input := ReplaceCharacterRepresentationsRequest{Representations: []CharacterRepresentationInput{{Role: "primary", ResourceID: imageID, Metadata: map[string]any{"source": "manual-upload"}}}}
	replaced, err := svc.ReplaceProjectCharacterRepresentations("user-1", project.ID, child.Asset.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Character.VersionID == child.Asset.PrimaryVersionID || replaced.Character.VisualStatus != "ready" || replaced.Asset.Variant == nil || replaced.Asset.Variant.Label != "淋湿" {
		t.Fatalf("variant lost: %+v", replaced)
	}
	if err := svc.finishDramaImageState("user-1", child.Asset.ID, dramaImageState{Status: dramaImageFailed, TaskID: "old-image", Error: "late failure"}); err != nil {
		t.Fatal(err)
	}
	asset, _ := svc.repo.AssetForUser("user-1", child.Asset.ID)
	state := dramaImageStateOf(asset, assetImageKey)
	if state.Status != dramaImageBound || state.Error != "" || state.TaskID != "" {
		t.Fatalf("old callback overwrote upload: %+v", state)
	}
	if _, err := svc.ReplaceProjectCharacterRepresentations("other", project.ID, child.Asset.ID, input); err == nil {
		t.Fatal("cross owner replacement accepted")
	}
	input.Representations[0].ResourceID = "missing"
	if _, err := svc.ReplaceProjectCharacterRepresentations("user-1", project.ID, child.Asset.ID, input); err == nil {
		t.Fatal("unreadable replacement accepted")
	}
	var current model.Asset
	if err := db.First(&current, "id = ?", child.Asset.ID).Error; err != nil || current.PrimaryVersionID != asset.PrimaryVersionID {
		t.Fatalf("failed upload changed primary: %+v %v", current, err)
	}
}
