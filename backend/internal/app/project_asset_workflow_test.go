package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

func TestWorkflowSkillRoutingRemainsProjectScoped(t *testing.T) {
	s, db := newDramaWorkerTestService(t)
	project, _ := seedQichuang(t, s, db)
	for _, wf := range []string{drama.WorkflowIntegrated, drama.WorkflowOriginal} {
		if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("workflow_id", wf).Error; err != nil {
			t.Fatal(err)
		}
		usage, err := s.DramaStepUsage("user-1", project.ID, drama.StepAssetsExtract, nil)
		if err != nil {
			t.Fatal(err)
		}
		if wf == drama.WorkflowIntegrated {
			if usage.Skill.ID != drama.IntegratedAssetsSkill || usage.Skill.Version != "1.0.1" || usage.Contract != drama.ContractIntegratedAssets {
				t.Fatal(usage)
			}
		} else if usage.Skill.ID != "yz-assets-extract" || usage.Contract != drama.ContractAssetsExtract {
			t.Fatal(usage)
		}
	}
}

func TestSpatialCompilerReminderUsesActualVersionAndTag(t *testing.T) {
	s, db, projectID, scene := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
	ensureWorkflowSkillFixture(t, s, db)
	if err := db.Model(&model.AssetVersion{}).Where("id = ?", scene.PrimaryVersionID).Update("definition_json", `{"imageLayout":"spatial-master"}`).Error; err != nil {
		t.Fatal(err)
	}
	project, err := s.repo.ProjectForUser("user-1", projectID)
	if err != nil {
		t.Fatal(err)
	}
	project.WorkflowID = drama.WorkflowIntegrated
	note, err := s.spatialCompiledReferenceNote("user-1", project, []drama.CompiledFile{{File: scene.PrimaryVersionID, Tag: "@图片7"}})
	if err != nil || !strings.Contains(note, "@图片7") || !strings.Contains(note, "真实尺度") || strings.Contains(note, "@图片1") {
		t.Fatal(note, err)
	}
	project.WorkflowID = drama.WorkflowOriginal
	if note, err = s.spatialCompiledReferenceNote("user-1", project, []drama.CompiledFile{{File: scene.PrimaryVersionID, Tag: "@图片7"}}); err != nil || note != "" {
		t.Fatal("original workflow changed", note, err)
	}
}

func TestWorkflowImageRatiosUseDeclaredProviderPixelMapping(t *testing.T) {
	profile := DefaultModelCapabilityConfigForModel("opaque-provider", "opaque-model")
	profile.Image.Parameters = []protocol.ImageParameter{{Name: "size", Label: "Size", Type: "string", Values: []any{"1024x1536", "1024x1024", "1536x864"}, Default: "1024x1024", Binding: "aspectRatio", Encoding: "pixels"}}
	profile.Image.Size.Presets = []ImageSizePreset{{Tier: "1k", Ratio: "2:3", Width: 1024, Height: 1536, Size: "1024x1536"}, {Tier: "1k", Ratio: "1:1", Width: 1024, Height: 1024, Size: "1024x1024"}, {Tier: "1k", Ratio: "16:9", Width: 1536, Height: 864, Size: "1536x864"}}
	if err := projectImageParameters(profile.Image); err != nil {
		t.Fatal(err)
	}
	for ratio, pixels := range map[string]string{"2:3": "1024x1536", "1:1": "1024x1024", "16:9": "1536x864"} {
		config := providerConfig{InterfaceType: "opaque-provider", Size: ratio, ImageParameters: map[string]any{"size": ratio}}
		if err := applyImageParameterValues(&config, profile.Image); err != nil {
			t.Fatal(ratio, err)
		}
		if config.Size != pixels || config.ImageParameters["size"] != pixels {
			t.Fatalf("ratio/submit parameters differ: %+v", config)
		}
	}
}

func ensureWorkflowSkillFixture(t *testing.T, s *Service, db *gorm.DB) {
	t.Helper()
	if s.dataDir == "" {
		s.dataDir = t.TempDir()
	}
	if err := db.AutoMigrate(&model.Skill{}, &model.UserSkillState{}, &model.SkillVersion{}, &model.SkillFile{}, &model.BuiltinSkillTombstone{}); err != nil {
		t.Fatal(err)
	}
	if err := s.skillDomain().EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowReferenceMergeOwnershipOrderAndCapacity(t *testing.T) {
	s, _, _, _ := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
	canonical := []dramaImageRef{{ResourceID: "image-1", Label: "人物主卡"}}
	input := map[string]any{"referenceImages": []any{
		map[string]any{"storageKey": "resource:image-1"},
		map[string]any{"storageKey": "resource:image-1"},
	}}
	refs, err := s.mergeOwnedWorkflowReferences("user-1", input, canonical)
	if err != nil || len(refs) != 1 || refs[0].Label != "人物主卡" {
		t.Fatal("canonical identity reference was duplicated or replaced", refs, err)
	}
	for _, key := range []string{"resource:foreign-image", "resource:pending-image", "resource:video-1", "https://example.invalid/image.png"} {
		input["referenceImages"] = []any{map[string]any{"storageKey": key}}
		if _, err = s.mergeOwnedWorkflowReferences("user-1", input, canonical); err == nil {
			t.Fatalf("invalid supplementary reference accepted: %s", key)
		}
	}
	zero := 0
	if _, err = s.withDramaImageRefs("user-1", input, canonical, DramaImageTaskTemplate{MaxReferenceImages: &zero}, dramaRefsAll); err == nil {
		t.Fatal("required reference silently dropped for limited model")
	}
}

func TestIntegratedCharacterVariantActuallyReceivesParentSheet(t *testing.T) {
	s, db, projectID, asset := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
	ensureWorkflowSkillFixture(t, s, db)
	if err := db.Model(&model.Asset{}).Where("id = ?", asset.ID).Update("category", model.AssetCategoryCharacter).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Project{}).Where("id = ?", projectID).Update("workflow_id", drama.WorkflowIntegrated).Error; err != nil {
		t.Fatal(err)
	}
	parent := model.Asset{ID: "parent-person", UserID: "user-1", Category: model.AssetCategoryCharacter, Title: "林深", PrimaryVersionID: "parent-version"}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AssetVersion{ID: parent.PrimaryVersionID, AssetID: parent.ID, Version: 1, DefinitionJSON: `{}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AssetRepresentation{ID: "parent-sheet", AssetVersionID: parent.PrimaryVersionID, Role: "turnaround_sheet", ResourceID: "image-1", MediaType: "image/png"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Asset{}).Where("id = ?", asset.ID).Update("parent_asset_id", parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AssetVersion{}).Where("id = ?", asset.PrimaryVersionID).Update("definition_json", `{"appearance":"同一人物五年前，19岁，雨夜湿发","generationPrompt":"19岁青年林深，湿透衬衫，完整四宫格设定"}`).Error; err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"config": map[string]any{}, "metadata": map[string]any{"operation": "character_turnaround", "characterAssetId": asset.ID, "promptTemplateOperation": "character_turnaround"}}
	prompt, err := s.prepareProjectAssetWorkflow("user-1", projectID, input, "old")
	if err != nil {
		t.Fatal(err)
	}
	refs := input["referenceImages"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["storageKey"] != "resource:image-1" || !strings.Contains(prompt, "四宫格") || !strings.Contains(prompt, "年龄阶段") {
		t.Fatal("parent sheet not transmitted with age delta", input, prompt)
	}
	if _, exists := input["metadata"].(map[string]any)["promptTemplateOperation"]; exists {
		t.Fatal("old three-view template can overwrite four-panel recipe")
	}
	if input["config"].(map[string]any)["size"] != "2:3" {
		t.Fatal("four-panel ratio lost")
	}
}

func TestIntegratedSceneRequiresAndTransmitsDesignedProp(t *testing.T) {
	s, db, projectID, scene := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
	ensureWorkflowSkillFixture(t, s, db)
	if err := db.Model(&model.Project{}).Where("id = ?", projectID).Update("workflow_id", drama.WorkflowIntegrated).Error; err != nil {
		t.Fatal(err)
	}
	prop := model.Asset{ID: "designed-prop", UserID: "user-1", Category: model.AssetCategoryProp, Title: "随行鲜", PrimaryVersionID: "prop-version"}
	for _, row := range []any{&prop, &model.AssetVersion{ID: prop.PrimaryVersionID, AssetID: prop.ID, Version: 1, DefinitionJSON: `{}`}, &model.ProjectAssetLink{ID: "prop-link", ProjectID: projectID, AssetID: prop.ID}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&model.AssetVersion{}).Where("id = ?", scene.PrimaryVersionID).Update("definition_json", `{"description":"开放市场","extractionDesign":{"props":["随行鲜"]}}`).Error; err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"config": map[string]any{}, "metadata": map[string]any{"operation": "project_asset_image", "projectAssetId": scene.ID}}
	if _, err := s.prepareProjectAssetWorkflow("user-1", projectID, input, ""); err == nil {
		t.Fatal("missing designed prop silently omitted")
	}
	if err := db.Create(&model.AssetRepresentation{ID: "prop-image", AssetVersionID: prop.PrimaryVersionID, Role: "primary", ResourceID: "image-1", MediaType: "image/png"}).Error; err != nil {
		t.Fatal(err)
	}
	prompt, err := s.prepareProjectAssetWorkflow("user-1", projectID, input, "")
	if err != nil || !strings.Contains(prompt, "关联道具「随行鲜」") {
		t.Fatal(prompt, err)
	}
	if input["referenceImages"].([]any)[0].(map[string]any)["storageKey"] != "resource:image-1" {
		t.Fatal("prop image not transmitted")
	}
}

func TestAssetWorkflowCreateDefaultsAndExplicitSelection(t *testing.T) {
	s, _ := newDramaWorkerTestService(t)
	for _, id := range []string{"", drama.WorkflowIntegrated, drama.WorkflowOriginal} {
		project, err := s.CreateProject("user-1", CreateProjectRequest{Name: "工作流" + id, VisualType: "live_action", WorkflowID: id})
		if err != nil {
			t.Fatal(err)
		}
		want := id
		if want == "" {
			want = drama.WorkflowIntegrated
		}
		if project.WorkflowID != want || project.WorkflowVersion != drama.WorkflowVersion {
			t.Fatalf("wrong workflow: %+v", project)
		}
	}
	if _, err := s.CreateProject("user-1", CreateProjectRequest{Name: "错误工作流", VisualType: "live_action", WorkflowID: "forged"}); err == nil {
		t.Fatal("invalid workflow accepted")
	}
}

func TestIntegratedSceneAdmissionPreservesFullDesignAndOriginalFlow(t *testing.T) {
	s, db, projectID, asset := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
	ensureWorkflowSkillFixture(t, s, db)
	full := strings.Repeat("铁皮棚为半室外，过道左右开放。", 180)
	definition, _ := json.Marshal(map[string]any{"description": "便民市场", "generationPrompt": full})
	if err := db.Model(&model.AssetVersion{}).Where("id = ?", asset.PrimaryVersionID).Update("definition_json", string(definition)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Project{}).Where("id = ?", projectID).Update("workflow_id", drama.WorkflowIntegrated).Error; err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"mode": "image", "config": map[string]any{"size": "9:16"}, "metadata": map[string]any{"operation": "project_asset_image", "domainProjectId": projectID, "projectAssetId": asset.ID}}
	prompt, err := s.prepareProjectAssetWorkflow("user-1", projectID, input, "old concept prompt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, full) || !strings.Contains(prompt, "微缩空间总览母版") || input["config"].(map[string]any)["size"] != "1:1" {
		t.Fatal("integrated design lost")
	}
	if _, err = s.prepareProjectAssetWorkflow("user-2", projectID, input, "x"); err == nil {
		t.Fatal("foreign project accepted")
	}
	if err = db.Model(&model.Project{}).Where("id = ?", projectID).Update("workflow_id", drama.WorkflowOriginal).Error; err != nil {
		t.Fatal(err)
	}
	original := map[string]any{"mode": "image", "config": map[string]any{"size": "9:16"}, "metadata": map[string]any{"operation": "project_asset_image", "projectAssetId": asset.ID}}
	got, err := s.prepareProjectAssetWorkflow("user-1", projectID, original, "original concept prompt")
	if err != nil || got != "original concept prompt" || original["config"].(map[string]any)["size"] != "9:16" {
		t.Fatal(got, err)
	}
}

func TestSceneSpatialMotherGridRedrawAndIdempotentBinding(t *testing.T) {
	s, db, projectID, asset := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
	ensureWorkflowSkillFixture(t, s, db)
	if err := db.Model(&model.Project{}).Where("id = ?", projectID).Update("workflow_id", drama.WorkflowIntegrated).Error; err != nil {
		t.Fatal(err)
	}
	bound, err := s.BindProjectAssetImage("user-1", projectID, asset.ID, BindProjectAssetImageRequest{ResourceID: "image-1", ExpectedVersionID: asset.PrimaryVersionID})
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"mode": "image", "config": map[string]any{}, "metadata": map[string]any{"operation": "project_spatial_reference", "domainProjectId": projectID, "projectAssetId": asset.ID, "projectAssetVersionId": bound.PrimaryVersionID, "spatialKind": "grid"}}
	if _, err = s.prepareProjectAssetWorkflow("user-1", projectID, input, "grid"); err != nil {
		t.Fatal(err)
	}
	if refs := input["referenceImages"].([]any); len(refs) != 1 || refs[0].(map[string]any)["storageKey"] != "resource:image-1" {
		t.Fatal(refs)
	}
	grid := model.Resource{ID: "grid-image", UserID: "user-1", Kind: "image", Status: model.ResourceStatusReady, MimeType: "image/png", Width: 1600, Height: 900}
	if err = db.Create(&grid).Error; err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(input)
	task := model.Task{ID: "spatial-grid-task", UserID: "user-1", ProjectID: projectID, InputJSON: string(encoded)}
	result := map[string]interface{}{"images": []any{map[string]any{"resourceId": grid.ID}}}
	for i := 0; i < 2; i++ {
		applied, err := s.finalizeProjectSpatialImage(task, result)
		if err != nil || applied != (i == 0) {
			t.Fatal(applied, err)
		}
	}
	items, err := s.ProjectSpatialReferences("user-1", projectID, asset.ID)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	current, err := s.repo.AssetForUser("user-1", asset.ID)
	if err != nil || current.PrimaryVersionID != bound.PrimaryVersionID {
		t.Fatal("grid replaced mother", err)
	}
	metadata := input["metadata"].(map[string]any)
	metadata["spatialKind"], metadata["gridResourceId"], metadata["cell"] = "redraw", grid.ID, 1
	if _, err = s.prepareProjectAssetWorkflow("user-1", projectID, input, "redraw"); err != nil {
		t.Fatal(err)
	}
	if len(input["referenceImages"].([]any)) != 2 {
		t.Fatal("redraw did not receive mother and grid")
	}
	metadata["cell"] = 5
	if _, err = s.prepareProjectAssetWorkflow("user-1", projectID, input, "redraw"); err == nil {
		t.Fatal("mother cell accepted as real scene")
	}
	metadata["cell"], metadata["gridResourceId"] = 1, "foreign-image"
	if _, err = s.prepareProjectAssetWorkflow("user-1", projectID, input, "redraw"); err == nil {
		t.Fatal("unrelated grid accepted")
	}
	if _, err = s.ProjectSpatialReferences("user-2", projectID, asset.ID); err == nil {
		t.Fatal("foreign references leaked")
	}
}

func TestSpatialAdmissionKeyAndCancelledTaskFence(t *testing.T) {
	s, db, projectID, _ := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
	input := map[string]any{"metadata": map[string]any{"operation": "project_spatial_reference", "projectAssetId": "scene", "projectAssetVersionId": "version", "spatialKind": "grid"}}
	key := spatialTaskKey(projectID, input)
	if key == "" || key != spatialTaskKey(projectID, input) {
		t.Fatal("unstable submission key")
	}
	raw, _ := json.Marshal(input)
	task := model.Task{ID: "active-spatial", UserID: "user-1", ProjectID: projectID, Type: "canvas_image", Status: model.TaskStatusRunning, InputJSON: string(raw), CreatedAt: time.Now()}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	active, err := s.repo.ActiveSpatialTask("user-1", projectID, key)
	if err != nil || active == nil {
		t.Fatal(active, err)
	}
	if err = db.Model(&task).Update("status", model.TaskStatusCancelled).Error; err != nil {
		t.Fatal(err)
	}
	active, err = s.repo.ActiveSpatialTask("user-1", projectID, key)
	if err != nil || active != nil {
		t.Fatal("cancelled task considered active", err)
	}
}
