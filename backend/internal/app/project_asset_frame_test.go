package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

// 空间母版的输出画幅与项目视频画幅无关：电影 16:9、短剧 9:16 项目的场景母版，三处尺寸字段、模型匹配意图和最终按渠道声明换算的像素都是方形；
// 资产设定和项目画风里的画幅标注换成方形，末尾补母版输出规格；重复提交不叠加。
func TestSpatialMasterFrameIsSquareForEveryProjectRatio(t *testing.T) {
	s, db, projectID, asset := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
	ensureWorkflowSkillFixture(t, s, db)
	definition := `{"description":"夜市一角","generationPrompt":"landscape 16:9 夜市雨棚，一辆旧推车式工作台，围合摊位"}`
	if err := db.Model(&model.AssetVersion{}).Where("id = ?", asset.PrimaryVersionID).Update("definition_json", definition).Error; err != nil {
		t.Fatal(err)
	}
	profile := DefaultModelCapabilityConfigForModel("opaque-provider", "opaque-model")
	profile.Image.Parameters = []protocol.ImageParameter{{Name: "size", Label: "Size", Type: "string", Values: []any{"1024x1024", "1536x864", "864x1536"}, Default: "864x1536", Binding: "aspectRatio", Encoding: "pixels"}}
	profile.Image.Size.Presets = []ImageSizePreset{{Tier: "1k", Ratio: "9:16", Width: 864, Height: 1536, Size: "864x1536"}, {Tier: "1k", Ratio: "1:1", Width: 1024, Height: 1024, Size: "1024x1024"}, {Tier: "1k", Ratio: "16:9", Width: 1536, Height: 864, Size: "1536x864"}}
	if err := projectImageParameters(profile.Image); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ workflow, ratio, style string }{
		{drama.WorkflowSSSFilm, "16:9", `{"title":"电影","prompt":"【影像与动态基线】横屏16:9，电影宽画幅"}`},
		{drama.WorkflowSSSDrama, "9:16", `{"title":"短剧","prompt":"【影像与动态基线】竖屏9:16，近景为主"}`},
	} {
		t.Run(c.workflow, func(t *testing.T) {
			if err := db.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"workflow_id": c.workflow, "aspect_ratio": c.ratio, "style_profile_json": c.style}).Error; err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"mode": "image", "capabilityOptions": map[string]any{"size": c.ratio}, "config": map[string]any{"size": c.ratio, "imageParameters": map[string]any{"size": c.ratio}}, "metadata": map[string]any{"operation": "project_asset_image", "projectAssetId": asset.ID}}
			prompt, err := s.prepareProjectAssetWorkflow("user-1", projectID, input, "old")
			if err != nil {
				t.Fatal(err)
			}
			config := input["config"].(map[string]any)
			metadata := input["metadata"].(map[string]any)
			if config["size"] != "1:1" || config["imageParameters"].(map[string]any)["size"] != "1:1" || input["capabilityOptions"].(map[string]any)["size"] != "1:1" || metadata["assetImageRatio"] != "1:1" {
				t.Fatalf("size fields = %v %v %v", config, input["capabilityOptions"], metadata["assetImageRatio"])
			}
			if intent := ModelRequestIntentFromTaskInput(input, "canvas_image", "image"); intent.Options["size"] != "1:1" {
				t.Fatal("model matching and pricing intent kept the project ratio", intent)
			}
			wire := providerConfig{InterfaceType: "opaque-provider", Size: "1:1", ImageParameters: map[string]any{"size": "1:1"}}
			if err := applyImageParameterValues(&wire, profile.Image); err != nil || wire.Size != "1024x1024" || wire.ImageParameters["size"] != "1024x1024" {
				t.Fatalf("final protocol parameters = %+v %v", wire, err)
			}
			for _, banned := range []string{"landscape 16:9", "横屏16:9", "竖屏9:16"} {
				if strings.Contains(prompt, banned) {
					t.Fatalf("project frame %q leaked into the spatial master prompt", banned)
				}
			}
			if !strings.Contains(prompt, "方形1:1 夜市雨棚，一辆旧推车式工作台") || !strings.HasSuffix(prompt, drama.SpatialMasterOutputNote) || !strings.Contains(prompt, "四面墙完整保留") {
				t.Fatal(prompt)
			}
			if second, err := s.prepareProjectAssetWorkflow("user-1", projectID, input, prompt); err != nil || second != prompt {
				t.Fatal("repeated admission stacked format instructions", err)
			}
		})
	}
}

// 道具本体图：去掉继承来的画幅标注，末尾写明只画这一件道具；参考图只有主基调色卡这类风格参考，不带别的独立物体。
func TestPropBodyPromptKeepsTheItemIndependent(t *testing.T) {
	s, db, projectID, asset := seedProjectVisualAsset(t, model.AssetCategoryProp)
	ensureWorkflowSkillFixture(t, s, db)
	definition := `{"description":"三张湿纸币","generationPrompt":"landscape 16:9 雨夜摊位上，三张湿透的纸币压在保温杯和毛巾旁边，旁边有一个橙子"}`
	if err := db.Model(&model.AssetVersion{}).Where("id = ?", asset.PrimaryVersionID).Update("definition_json", definition).Error; err != nil {
		t.Fatal(err)
	}
	for _, workflow := range []string{drama.WorkflowSSSFilm, drama.WorkflowSSSDrama, drama.WorkflowIntegrated} {
		if err := db.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"workflow_id": workflow, "aspect_ratio": "16:9"}).Error; err != nil {
			t.Fatal(err)
		}
		input := map[string]any{"mode": "image", "config": map[string]any{"size": "1:1"}, "metadata": map[string]any{"operation": "project_asset_image", "projectAssetId": asset.ID}}
		prompt, err := s.prepareProjectAssetWorkflow("user-1", projectID, input, "old")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(prompt, "landscape 16:9") || !strings.HasSuffix(prompt, drama.PropBodyOutputNote(asset.Title)) || !strings.Contains(prompt, "只呈现「"+asset.Title+"」这一件道具本体") {
			t.Fatal(workflow, prompt)
		}
		if refs, _ := input["referenceImages"].([]any); len(refs) > 0 {
			for _, ref := range refs {
				if label, _ := ref.(map[string]any)["label"].(string); !strings.Contains(label, "主基调色卡") {
					t.Fatalf("%s: prop body carried a non-style reference: %v", workflow, ref)
				}
			}
		}
	}
}

// 收尾核对实际返回比例：941×1672 对 1:1 不符（提示、记任务告警，不挡绑定），对 9:16 在误差内相符；像素未知、画幅读不出时不判。
func TestAssetImageAspectCheck(t *testing.T) {
	for _, c := range []struct {
		requested     string
		width, height int
		ok, matches   bool
	}{
		{"1:1", 941, 1672, true, false},
		{"9:16", 941, 1672, true, true},
		{"16:9", 1672, 941, true, true},
		{"1:1", 1024, 1024, true, true},
		{"1:1", 1672, 941, true, false},
		{"", 1024, 1024, false, false},
		{"1:1", 0, 0, false, false},
	} {
		got, ok := compareAssetImageAspect(c.requested, c.width, c.height)
		if ok != c.ok || (ok && got.Matches != c.matches) || (ok && !got.Matches && !strings.Contains(got.Message, "没有裁切，也没有自动重新生成")) {
			t.Fatalf("%+v → %+v %v", c, got, ok)
		}
	}
	s, _, projectID, _ := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
	result := map[string]interface{}{}
	task := model.Task{ID: "task-aspect", UserID: "user-1", ProjectID: projectID}
	s.checkAssetImageAspect(task, `{"config":{"size":"9:16"},"metadata":{"assetImageRatio":"1:1"}}`, "image-1", result)
	check, ok := result["imageAspect"].(AssetImageAspect)
	if !ok || check.Requested != "1:1" || check.Width != 800 || check.Height != 600 || check.Matches {
		t.Fatalf("aspect check = %+v", result["imageAspect"])
	}
}
