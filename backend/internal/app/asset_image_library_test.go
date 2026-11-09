package app

import (
	"encoding/json"
	"testing"
	"yingce/backend/internal/model"
)

func TestImageLibraryReferencesAreCurrentOwnedReadyAndPaged(t *testing.T) {
	svc, db := newProjectWorkflowV2TestService(t)
	for _, resource := range []model.Resource{
		{ID: "current", UserID: "user-1", Kind: "image", Status: model.ResourceStatusReady},
		{ID: "old", UserID: "user-1", Kind: "image", Status: model.ResourceStatusReady},
		{ID: "foreign", UserID: "other", Kind: "image", Status: model.ResourceStatusReady},
		{ID: "pending", UserID: "user-1", Kind: "image", Status: "pending"},
	} {
		if err := db.Create(&resource).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, asset := range []model.Asset{
		{ID: "scene", UserID: "user-1", Kind: "entity", Category: model.AssetCategoryEnvironment, Title: "历史场景", Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: "scene-v2", PayloadJSON: `{"data":{"definition":{}}}`},
		{ID: "upload", UserID: "user-1", Kind: "image", Category: model.AssetCategoryOther, Title: "历史上传", Status: model.AssetVersionStatusConfirmed, PayloadJSON: `{}`},
		{ID: "audio", UserID: "user-1", Kind: "audio", Category: model.AssetCategoryOther, Title: "历史音频", Status: model.AssetVersionStatusConfirmed, PayloadJSON: `{}`},
		{ID: "other", UserID: "other", Kind: "image", Title: "历史他人", Status: model.AssetVersionStatusConfirmed, PayloadJSON: `{}`},
	} {
		if err := db.Create(&asset).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, version := range []model.AssetVersion{{ID: "scene-v1", AssetID: "scene", Version: 1}, {ID: "scene-v2", AssetID: "scene", Version: 2}} {
		if err := db.Create(&version).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, rep := range []model.AssetRepresentation{
		{ID: "rep-old", AssetVersionID: "scene-v1", ResourceID: "old", MediaType: "image", Role: "primary"},
		{ID: "rep-current", AssetVersionID: "scene-v2", ResourceID: "current", MediaType: "image/png", Role: "primary"},
		{ID: "rep-foreign", AssetVersionID: "scene-v2", ResourceID: "foreign", MediaType: "image", Role: "side"},
		{ID: "rep-pending", AssetVersionID: "scene-v2", ResourceID: "pending", MediaType: "image", Role: "back"},
	} {
		rep.TaskID = rep.ID
		if err := db.Create(&rep).Error; err != nil {
			t.Fatal(err)
		}
	}
	page, err := svc.UserAssetsPage("user-1", 1, 1, UserAssetPageFilter{ImageReferences: true, Status: "active", Query: "历史"})
	if err != nil || page.Total != 2 || len(page.Assets) != 1 || !page.HasMore {
		t.Fatalf("page: %+v %v", page, err)
	}
	if page.MatchingCategoryCounts["environment"] != 1 || page.MatchingCategoryCounts["other"] != 1 || page.MatchingCategoryCounts["audio"] != 0 {
		t.Fatalf("counts: %+v", page.MatchingCategoryCounts)
	}
	page, err = svc.UserAssetsPage("user-1", 1, 40, UserAssetPageFilter{ImageReferences: true, Status: "active", Category: "environment"})
	if err != nil || len(page.Assets) != 1 {
		t.Fatalf("scene: %+v %v", page, err)
	}
	var payload struct {
		ImageReferences []struct {
			ResourceID string `json:"resourceId"`
		} `json:"imageReferences"`
	}
	if err := json.Unmarshal(page.Assets[0], &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.ImageReferences) != 1 || payload.ImageReferences[0].ResourceID != "current" {
		t.Fatalf("references: %s", page.Assets[0])
	}
}
