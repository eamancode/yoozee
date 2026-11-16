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

// seedScenePropRefs 给场景挂 n 个已出图的关联道具（每个一张就绪图片），priorities[i] 是第 i 个道具的制作优先级（空串是没标）。
func seedScenePropRefs(t *testing.T, priorities []string) (*Service, string, ProjectAssetSummary, []string) {
	t.Helper()
	s, db, projectID, scene := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
	ensureWorkflowSkillFixture(t, s, db)
	if err := db.Model(&model.Project{}).Where("id = ?", projectID).Update("workflow_id", drama.WorkflowIntegrated).Error; err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(priorities))
	for i, priority := range priorities {
		names[i] = fmt.Sprintf("道具%02d", i+1)
		seedSceneProp(t, db, projectID, i, names[i], priority)
	}
	design, _ := json.Marshal(map[string]any{"description": "铁路职工家属院外", "extractionDesign": map[string]any{"props": names}})
	if err := db.Model(&model.AssetVersion{}).Where("id = ?", scene.PrimaryVersionID).Update("definition_json", string(design)).Error; err != nil {
		t.Fatal(err)
	}
	return s, projectID, scene, names
}

func seedSceneProp(t *testing.T, db *gorm.DB, projectID string, i int, name string, priority string) {
	t.Helper()
	id := fmt.Sprintf("scene-prop-%02d", i+1)
	definition := map[string]any{"appearance": name + "的本体：铸铁、黑色、有使用痕迹"}
	if priority != "" {
		definition["productionPriority"] = priority
	}
	payload, _ := json.Marshal(map[string]any{"data": map[string]any{"definition": definition}})
	resource := model.Resource{ID: id + "-image", UserID: "user-1", Kind: "image", Status: model.ResourceStatusReady, Width: 800, Height: 800, Size: 100, MimeType: "image/png"}
	for _, row := range []any{
		&model.Asset{ID: id, UserID: "user-1", Category: model.AssetCategoryProp, Title: name, PrimaryVersionID: id + "-v1", PayloadJSON: string(payload)},
		&model.AssetVersion{ID: id + "-v1", AssetID: id, Version: 1, DefinitionJSON: `{}`},
		&model.ProjectAssetLink{ID: id + "-link", ProjectID: projectID, AssetID: id},
		&resource,
		&model.AssetRepresentation{ID: id + "-rep", TaskID: id + "-task", AssetVersionID: id + "-v1", Role: "primary", ResourceID: resource.ID, MediaType: "image/png"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func sceneImageInput(sceneID string, maxImages int, level int) map[string]any {
	return map[string]any{"config": map[string]any{}, "metadata": map[string]any{"operation": "project_asset_image", "projectAssetId": sceneID, "assetImageReferencePolicy": map[string]any{"maxImages": maxImages, "level": level}}}
}

func inputReferenceIDs(input map[string]any) []string {
	refs, _ := input["referenceImages"].([]any)
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		key, _ := ref.(map[string]any)["storageKey"].(string)
		ids = append(ids, strings.TrimPrefix(key, "resource:"))
	}
	return ids
}

// 线上「铁路职工家属院外」：场景设计关联 11 个道具（都已出图、都没标优先级），图片模型（Image 2.6）最多 9 张参考图，
// 原先 11 张全算必需，建任务前就被拒「出图必须保留身份或空间参考图片」。现在：没标 / 中优先级的道具在名额不够时按文字还原，
// 带满 9 张图，剩下 2 个写进提示词并记在任务上，不静默截掉。
func TestSceneOverQuotaPropsFallBackToText(t *testing.T) {
	priorities := make([]string, 11)
	s, projectID, scene, names := seedScenePropRefs(t, priorities)
	input := sceneImageInput(scene.ID, 9, dramaRefsAll)
	prompt, err := s.prepareProjectAssetWorkflow("user-1", projectID, input, "")
	if err != nil {
		t.Fatalf("11 ordinary props over a 9-image model must not block the scene: %v", err)
	}
	ids := inputReferenceIDs(input)
	if len(ids) != 9 {
		t.Fatalf("reference images = %d, want the full quota of 9", len(ids))
	}
	for i := range 9 {
		if ids[i] != fmt.Sprintf("scene-prop-%02d-image", i+1) {
			t.Fatalf("kept refs out of design order: %v", ids)
		}
	}
	textOnly := names[9:]
	if !strings.Contains(prompt, "以下关联道具（参考图名额不够，没带图）按文字还原形制与材质") {
		t.Fatal("over-quota props must be described in the prompt")
	}
	for _, name := range textOnly {
		if !strings.Contains(prompt, "「"+name+"」"+name+"的本体：铸铁、黑色、有使用痕迹") {
			t.Fatalf("prop %s not described in text: %s", name, prompt)
		}
	}
	metadata := input["metadata"].(map[string]any)
	if recorded, _ := metadata["assetImageTextOnlyProps"].([]string); !slices.Equal(recorded, textOnly) {
		t.Fatalf("text-only props not recorded on the task: %v", metadata["assetImageTextOnlyProps"])
	}
}

// 高优先级道具（需要图片锁定形制的关键道具）仍是必需参考：放得下时排在最前、一定带图，放不下时整张拒绝，错误写明要几张、
// 是哪几个、模型最多几张；不支持参考图的档（降级到底）同样拒绝。只有普通道具时降级到底也能出（全部按文字还原）。
func TestSceneHighPriorityPropsStayRequired(t *testing.T) {
	priorities := []string{"", "medium", "high", "", "high", "", "", "high", "", "", ""}
	s, projectID, scene, names := seedScenePropRefs(t, priorities)
	input := sceneImageInput(scene.ID, 9, dramaRefsAll)
	if _, err := s.prepareProjectAssetWorkflow("user-1", projectID, input, ""); err != nil {
		t.Fatal(err)
	}
	ids := inputReferenceIDs(input)
	for _, high := range []string{"scene-prop-03-image", "scene-prop-05-image", "scene-prop-08-image"} {
		if !slices.Contains(ids[:3], high) {
			t.Fatalf("high-priority prop %s must be kept first: %v", high, ids)
		}
	}
	if len(ids) != 9 {
		t.Fatalf("reference images = %d", len(ids))
	}

	tight := sceneImageInput(scene.ID, 2, dramaRefsAll)
	_, err := s.prepareProjectAssetWorkflow("user-1", projectID, tight, "")
	if err == nil || !strings.Contains(err.Error(), "出图必须带 3 张身份或关键道具参考图") || !strings.Contains(err.Error(), "最多 2 张参考图") {
		t.Fatalf("too many high-priority props must be refused with counts: %v", err)
	}
	for _, name := range []string{names[2], names[4], names[7]} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error must list %s: %v", name, err)
		}
	}
	none := sceneImageInput(scene.ID, 9, dramaRefsNone)
	if _, err := s.prepareProjectAssetWorkflow("user-1", projectID, none, ""); err == nil || !strings.Contains(err.Error(), "不支持参考图") {
		t.Fatalf("no-reference level with required props: %v", err)
	}

	plain, plainProject, plainScene, plainNames := seedScenePropRefs(t, []string{"", "medium", ""})
	noRefs := sceneImageInput(plainScene.ID, 9, dramaRefsNone)
	prompt, err := plain.prepareProjectAssetWorkflow("user-1", plainProject, noRefs, "")
	if err != nil || len(inputReferenceIDs(noRefs)) != 0 {
		t.Fatalf("ordinary props at the no-reference level: %v %v", err, inputReferenceIDs(noRefs))
	}
	for _, name := range plainNames {
		if !strings.Contains(prompt, "「"+name+"」") {
			t.Fatalf("ordinary prop %s not described: %s", name, prompt)
		}
	}
}

func TestScenePropReferenceUnavailablePreservesRequiredBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		owner  string
		status string
	}{
		{name: "missing"},
		{name: "foreign", owner: "other-user", status: model.ResourceStatusReady},
		{name: "not_ready", owner: "user-1", status: "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, _, _ := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
			if tc.owner != "" {
				resource := model.Resource{ID: "unavailable-prop", UserID: tc.owner, Kind: "image", Status: tc.status, MimeType: "image/png"}
				if err := db.Create(&resource).Error; err != nil {
					t.Fatal(err)
				}
			}
			limit := 9
			ref := dramaImageRef{ResourceID: "unavailable-prop", Name: "铁锅", Text: "黑色铸铁锅", Label: "关联道具铁锅", Degradable: true}
			input := map[string]any{"metadata": map[string]any{}}
			note, err := s.withDramaImageRefs("user-1", input, []dramaImageRef{ref}, DramaImageTaskTemplate{MaxReferenceImages: &limit}, dramaRefsAll)
			if err != nil || len(inputReferenceIDs(input)) != 0 || !strings.Contains(note, "「铁锅」黑色铸铁锅") {
				t.Fatalf("ordinary unavailable reference must fall back to text: %v %s", err, note)
			}
			metadata := input["metadata"].(map[string]any)
			if names, _ := metadata["assetImageTextOnlyProps"].([]string); !slices.Equal(names, []string{"铁锅"}) {
				t.Fatalf("missing text-only audit metadata: %v", metadata)
			}
			ref.Degradable = false
			_, err = s.withDramaImageRefs("user-1", map[string]any{}, []dramaImageRef{ref}, DramaImageTaskTemplate{MaxReferenceImages: &limit}, dramaRefsAll)
			if err == nil || !strings.Contains(err.Error(), "参考图片不可用：铁锅") {
				t.Fatalf("required unavailable reference must block: %v", err)
			}
		})
	}
}
