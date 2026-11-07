package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// Both automatic and manual image requests pass this admission boundary.
func (s *Service) prepareProjectAssetWorkflow(userID, projectID string, input map[string]any, prompt string) (string, error) {
	metadata, _ := input["metadata"].(map[string]any)
	if metadata == nil {
		return prompt, nil
	}
	domainID, _ := metadata["domainProjectId"].(string)
	op, _ := metadata["operation"].(string)
	assetImage := op == "character_turnaround" || op == "project_asset_image" || op == "project_spatial_reference"
	if op == "character_turnaround" && domainID == "" && projectID == "" {
		return prompt, nil
	}
	if domainID == "" && !assetImage {
		return prompt, nil
	}
	if domainID == "" {
		domainID = projectID
	}
	if domainID == "" {
		return "", BadAuthRequest("资产出图缺少项目标识")
	}
	if assetImage && projectID != "" && projectID != domainID {
		return "", BadAuthRequest("任务项目与资产项目不一致")
	}
	project, err := s.repo.ProjectForUser(userID, domainID)
	if err != nil {
		return "", err
	}
	wf, ok := drama.Workflow(project.WorkflowID)
	if !ok || (project.WorkflowVersion != "" && project.WorkflowVersion != wf.Version) {
		return "", BadAuthRequest("项目工作流版本不可用")
	}
	metadata["assetWorkflow"] = wf
	if op == "project_spatial_reference" && wf.ID != drama.WorkflowIntegrated {
		return "", BadAuthRequest("空间母版派生仅适用于流水线一")
	}
	if !assetImage || wf.ID != drama.WorkflowIntegrated {
		return prompt, nil
	}
	idKey := "projectAssetId"
	if op == "character_turnaround" {
		idKey = "characterAssetId"
	}
	assetID, _ := metadata[idKey].(string)
	asset, err := s.repo.AssetForUser(userID, assetID)
	if err != nil {
		return "", err
	}
	linked, err := s.repo.ProjectAssetLinked(domainID, assetID)
	if err != nil {
		return "", err
	}
	if !linked {
		return "", BadAuthRequest("资产不属于当前项目")
	}
	version, err := s.repo.AssetVersion(asset.PrimaryVersionID)
	if err != nil {
		return "", err
	}
	if version.AssetID != asset.ID {
		return "", BadAuthRequest("资产版本归属不一致")
	}
	if op == "project_asset_image" {
		if expected, _ := metadata["projectAssetVersionId"].(string); expected != "" && expected != version.ID {
			return "", BadAuthRequest("资产版本已变化，请刷新后再生成")
		}
	}
	if op == "project_spatial_reference" {
		return s.prepareSpatialImage(userID, project, asset, version, input, metadata)
	}
	definition := map[string]any{}
	if err := json.Unmarshal([]byte(version.DefinitionJSON), &definition); err != nil {
		return "", err
	}
	full, _ := definition["generationPrompt"].(string)
	if len(utf16.Encode([]rune(full))) > 16000 {
		return "", BadAuthRequest("完整资产提示词超过16000字符，请精简后提交")
	}
	style := projectStyleSnapshot(project)
	var recipe, setting, ratio string
	if op == "character_turnaround" {
		if asset.Category != model.AssetCategoryCharacter {
			return "", BadAuthRequest("请选择人物资产")
		}
		recipe, setting, ratio = "character-four-panel", characterTurnaroundSetting(definition), "2:3"
		metadata["imageLayout"] = "four-panel"
		metadata["resolvedCharacterVersions"] = []map[string]string{{"assetId": asset.ID, "versionId": version.ID}}
		delete(metadata, "promptTemplateOperation")
		delete(metadata, "promptTemplateVariables")
	} else if asset.Category == model.AssetCategoryEnvironment {
		recipe, ratio = "scene-spatial-master", "1:1"
		setting, _ = definition["description"].(string)
		metadata["imageLayout"] = "spatial-master"
	} else if asset.Category == model.AssetCategoryProp {
		recipe = "prop-body"
		setting, _ = definition["description"].(string)
		if summary, _ := definition["prompt"].(string); summary != "" {
			setting += "\n" + summary
		}
		metadata["imageLayout"] = "prop-body"
	} else {
		return prompt, nil
	}
	prefix, err := s.assetWorkflowRecipe(wf, metadata, recipe)
	if err != nil {
		return "", err
	}
	if full != "" {
		setting = full
	}
	if strings.TrimSpace(setting) == "" {
		return "", BadAuthRequest("资产缺少可执行外观设计")
	}
	refs, err := s.dramaAssetImageRefs(userID, project, *asset, definition)
	if err != nil {
		return "", err
	}
	refs, err = s.mergeOwnedWorkflowReferences(userID, input, refs)
	if err != nil {
		return "", err
	}
	// Resolve labels from owned resources, keeping valid user supplements once.
	delete(input, "referenceImages")
	var policy struct {
		MaxImages *int `json:"maxImages"`
		Level     int  `json:"level"`
	}
	encodedPolicy, _ := json.Marshal(metadata["assetImageReferencePolicy"])
	_ = json.Unmarshal(encodedPolicy, &policy)
	policy.Level = min(max(policy.Level, dramaRefsAll), dramaRefsNone)
	note, err := s.withDramaImageRefs(userID, input, refs, DramaImageTaskTemplate{MaxReferenceImages: policy.MaxImages}, policy.Level)
	if err != nil {
		return "", err
	}
	config, _ := input["config"].(map[string]any)
	if config == nil {
		config = map[string]any{}
		input["config"] = config
	}
	if ratio != "" {
		config["size"] = ratio
	}
	parameters, _ := config["imageParameters"].(map[string]any)
	if parameters == nil {
		parameters = map[string]any{}
		config["imageParameters"] = parameters
	}
	if ratio != "" {
		parameters["size"] = ratio
	}
	metadata["resolvedAssetVersionId"] = version.ID
	compiled := strings.Join([]string{prefix, "【当前资产】" + asset.Title, setting, "【项目画风】\n" + style.Prompt, note}, "\n\n")
	input["prompt"] = compiled
	return compiled, nil
}

func (s *Service) mergeOwnedWorkflowReferences(userID string, input map[string]any, refs []dramaImageRef) ([]dramaImageRef, error) {
	seen := map[string]bool{}
	for _, ref := range refs {
		seen[ref.ResourceID] = true
	}
	raw, err := json.Marshal(input["referenceImages"])
	if err != nil {
		return nil, err
	}
	var original []map[string]any
	if err = json.Unmarshal(raw, &original); err != nil {
		return nil, BadAuthRequest("参考图片格式无效")
	}
	for _, ref := range original {
		key, _ := ref["storageKey"].(string)
		if !strings.HasPrefix(key, "resource:") {
			return nil, BadAuthRequest("请先将补充参考图片保存为当前账号资源")
		}
		id := strings.TrimPrefix(key, "resource:")
		if seen[id] {
			continue
		}
		resource, err := s.repo.ResourceForUser(userID, id)
		if err != nil || resource.Kind != "image" || resource.Status != model.ResourceStatusReady {
			return nil, BadAuthRequest("补充参考图片不可用或不属于当前账号")
		}
		seen[id] = true
		refs = append(refs, dramaImageRef{ResourceID: id, Label: "用户补充参考，结合当前设定使用，项目画风与本阶段身份约束优先"})
	}
	return refs, nil
}

func assetDesignProps(definition map[string]any) []string {
	raw, _ := json.Marshal(definition["extractionDesign"])
	var design drama.AssetDesign
	_ = json.Unmarshal(raw, &design)
	return design.Props
}

// The compiler's tags refer to the actual ordered inputs, never a guessed image number.
func (s *Service) spatialCompiledReferenceNote(userID string, project *model.Project, files []drama.CompiledFile) (string, error) {
	if project.WorkflowID != drama.WorkflowIntegrated {
		return "", nil
	}
	var notes []string
	for _, file := range files {
		version, err := s.repo.AssetVersion(file.File)
		if err != nil {
			continue
		}
		asset, err := s.repo.AssetForUser(userID, version.AssetID)
		if err != nil || asset.Category != model.AssetCategoryEnvironment {
			continue
		}
		var definition map[string]any
		if json.Unmarshal([]byte(version.DefinitionJSON), &definition) != nil || definition["imageLayout"] != "spatial-master" {
			continue
		}
		workflow, _ := drama.Workflow(project.WorkflowID)
		note, err := s.assetWorkflowRecipe(workflow, map[string]any{}, "scene-reference")
		if err != nil {
			return "", err
		}
		notes = append(notes, strings.ReplaceAll(note, "{{reference}}", file.Tag))
	}
	return strings.Join(notes, "\n"), nil
}

func (s *Service) spatialPropRefs(userID string, project *model.Project, definition map[string]any) ([]dramaImageRef, error) {
	if project.WorkflowID != drama.WorkflowIntegrated {
		return nil, nil
	}
	names := assetDesignProps(definition)
	if len(names) == 0 {
		return nil, nil
	}
	assets, err := s.repo.ProjectAssets(userID, project.ID)
	if err != nil {
		return nil, err
	}
	var refs []dramaImageRef
	for _, name := range names {
		found := false
		for _, item := range assets {
			if item.Category != model.AssetCategoryProp || drama.AssetNameKey(item.Title) != drama.AssetNameKey(name) {
				continue
			}
			found = true
			if lowPriorityProp(&item) {
				break
			}
			id, err := s.versionImageResource(item.PrimaryVersionID, "primary")
			if err != nil {
				return nil, err
			}
			if id == "" {
				return nil, assetWorkflowDependencyError(name)
			}
			refs = append(refs, dramaImageRef{ResourceID: id, Label: "关联道具「" + name + "」：只复用其几何结构与材质，位置以当前场景布局为准"})
			break
		}
		if !found {
			return nil, BadAuthRequest("场景引用的道具尚未建档：" + name)
		}
	}
	return refs, nil
}

func assetWorkflowLayoutMetadata(layout string) map[string]any {
	out := map[string]any{"layout": layout}
	if layout == "four-panel" {
		out["regions"] = []map[string]any{
			{"role": "front_head", "rect": []float64{0, 0, .5, .38}},
			{"role": "profile_head", "rect": []float64{.5, 0, .5, .38}},
			{"role": "front_body_cropped", "rect": []float64{0, .38, .5, .62}},
			{"role": "back_body_full", "rect": []float64{.5, .38, .5, .62}},
		}
	}
	return out
}

func assetWorkflowDependencyError(name string) error {
	return BadAuthRequest(fmt.Sprintf("请先完成关联道具「%s」的图片，再继续微缩场景图", name))
}
