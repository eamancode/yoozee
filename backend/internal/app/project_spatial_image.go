package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

var spatialAdmissionMu sync.Mutex

func spatialTaskKey(projectID string, input map[string]any) string {
	metadata, _ := input["metadata"].(map[string]any)
	if metadata["operation"] != "project_spatial_reference" {
		return ""
	}
	parts := []string{projectID}
	for _, key := range []string{"projectAssetId", "projectAssetVersionId", "spatialKind"} {
		parts = append(parts, fmt.Sprint(metadata[key]))
	}
	if metadata["spatialKind"] == "redraw" {
		parts = append(parts, fmt.Sprint(metadata["gridResourceId"]), fmt.Sprint(metadata["cell"]))
	}
	key := videoChainHash(parts)[:32]
	metadata["spatialSourceKey"] = key
	return key
}

type spatialImageSource struct {
	Kind             string `json:"kind"`
	AssetID          string `json:"assetId"`
	AssetVersionID   string `json:"assetVersionId"`
	MotherResourceID string `json:"motherResourceId"`
	GridResourceID   string `json:"gridResourceId,omitempty"`
	Cell             int    `json:"cell,omitempty"`
}

func (s *Service) prepareSpatialImage(userID string, project *model.Project, asset *model.Asset, version *model.AssetVersion, input, metadata map[string]any) (string, error) {
	if asset.Category != model.AssetCategoryEnvironment {
		return "", BadAuthRequest("空间参考仅支持当前项目的场景")
	}
	if expected, _ := metadata["projectAssetVersionId"].(string); expected != version.ID {
		return "", BadAuthRequest("场景版本已变化，请刷新后生成空间参考")
	}
	kind, _ := metadata["spatialKind"].(string)
	if kind != "grid" && kind != "redraw" {
		return "", BadAuthRequest("请选择九宫格或选格重绘")
	}
	mother, err := s.versionImageResource(version.ID, "primary")
	if err != nil {
		return "", err
	}
	if mother == "" {
		return "", BadAuthRequest("请先完成当前场景的微缩母版")
	}
	source := spatialImageSource{Kind: kind, AssetID: asset.ID, AssetVersionID: version.ID, MotherResourceID: mother}
	refs := []dramaImageRef{{ResourceID: mother, Label: "场景微缩母版，仅作为空间几何与布局依据，不将微缩机位和模型观感带入实景画面"}}
	workflow, _ := drama.Workflow(project.WorkflowID)
	prompt, err := s.assetWorkflowRecipe(workflow, metadata, "scene-grid")
	if err != nil {
		return "", err
	}
	if kind == "redraw" {
		gridID, _ := metadata["gridResourceId"].(string)
		cellNumber, _ := metadata["cell"].(float64)
		if n, ok := metadata["cell"].(int); ok {
			cellNumber = float64(n)
		}
		cell := int(cellNumber)
		if cellNumber != float64(cell) || cell < 1 || cell > 9 || cell == 5 {
			return "", BadAuthRequest("请选择01～09中的实景格，05是母版锚点")
		}
		valid := false
		reps, err := s.repo.AssetRepresentations(version.ID)
		if err != nil {
			return "", err
		}
		for _, rep := range reps {
			var previous spatialImageSource
			if rep.Role == "scene_grid" && rep.ResourceID == gridID && json.Unmarshal([]byte(rep.MetadataJSON), &previous) == nil && previous.MotherResourceID == mother {
				valid = true
				break
			}
		}
		if !valid {
			return "", BadAuthRequest("所选九宫格不属于当前母版，请先生成有效九宫格")
		}
		source.GridResourceID, source.Cell = gridID, cell
		refs = append(refs, dramaImageRef{ResourceID: gridID, Label: fmt.Sprintf("空间九宫格，选择第%02d格，按从左到右从上到下排序", cell)})
		prompt, err = s.assetWorkflowRecipe(workflow, metadata, "scene-redraw")
		if err != nil {
			return "", err
		}
		prompt = strings.ReplaceAll(prompt, "{{cell}}", fmt.Sprintf("%02d", cell))
	}
	refs, err = s.mergeOwnedWorkflowReferences(userID, input, refs)
	if err != nil {
		return "", err
	}
	note, err := s.withDramaImageRefs(userID, input, refs, DramaImageTaskTemplate{}, dramaRefsAll)
	if err != nil {
		return "", err
	}
	metadata["spatialSource"], metadata["imageLayout"] = source, "scene-"+kind
	config, _ := input["config"].(map[string]any)
	if config == nil {
		config = map[string]any{}
		input["config"] = config
	}
	config["size"] = "16:9"
	parameters, _ := config["imageParameters"].(map[string]any)
	if parameters == nil {
		parameters = map[string]any{}
		config["imageParameters"] = parameters
	}
	parameters["size"] = "16:9"
	compiled := strings.Join([]string{prompt, "【项目画风】\n" + projectStyleSnapshot(project).Prompt, note}, "\n\n")
	input["prompt"] = compiled
	return compiled, nil
}

func (s *Service) ProjectSpatialReferences(userID, projectID, assetID string) ([]map[string]any, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	asset, err := s.repo.AssetForUser(userID, assetID)
	if err != nil {
		return nil, err
	}
	linked, err := s.repo.ProjectAssetLinked(projectID, assetID)
	if err != nil {
		return nil, err
	}
	if !linked || asset.Category != model.AssetCategoryEnvironment {
		return nil, BadAuthRequest("请选择当前项目场景")
	}
	reps, err := s.repo.AssetRepresentations(asset.PrimaryVersionID)
	if err != nil {
		return nil, err
	}
	mother, err := s.versionImageResource(asset.PrimaryVersionID, "primary")
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, rep := range reps {
		var source spatialImageSource
		if (rep.Role != "scene_grid" && rep.Role != "scene_redraw") || json.Unmarshal([]byte(rep.MetadataJSON), &source) != nil || source.MotherResourceID != mother {
			continue
		}
		out = append(out, map[string]any{"resourceId": rep.ResourceID, "taskId": rep.TaskID, "kind": source.Kind, "cell": source.Cell, "source": source, "createdAt": rep.CreatedAt})
	}
	return out, nil
}

func (s *Service) finalizeProjectSpatialImage(task model.Task, result map[string]interface{}) (bool, error) {
	if !strings.Contains(task.InputJSON, "project_spatial_reference") {
		return false, nil
	}
	raw, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		return false, err
	}
	var in struct {
		Metadata struct {
			Operation string             `json:"operation"`
			Source    spatialImageSource `json:"spatialSource"`
		} `json:"metadata"`
	}
	if err = json.Unmarshal([]byte(raw), &in); err != nil {
		return false, err
	}
	if in.Metadata.Operation != "project_spatial_reference" {
		return false, nil
	}
	source := in.Metadata.Source
	if source.AssetID == "" || source.AssetVersionID == "" || (source.Kind != "grid" && source.Kind != "redraw") {
		return false, BadAuthRequest("空间参考任务缺少有效来源")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return false, err
	}
	var output struct {
		Images []struct {
			ResourceID string `json:"resourceId"`
		} `json:"images"`
	}
	if err = json.Unmarshal(encoded, &output); err != nil {
		return false, err
	}
	if len(output.Images) != 1 || output.Images[0].ResourceID == "" {
		return false, BadAuthRequest("空间参考必须返回一张已保存图片")
	}
	resource, err := s.repo.ResourceForUser(task.UserID, output.Images[0].ResourceID)
	if err != nil {
		return false, err
	}
	if resource.Kind != "image" || resource.Status != model.ResourceStatusReady || resource.Width <= 0 || resource.Height <= 0 || !strings.HasPrefix(resource.MimeType, "image/") {
		return false, BadAuthRequest("空间参考图片不可用")
	}
	metadata, _ := json.Marshal(source)
	rep := model.AssetRepresentation{ID: videoChainHash([]string{"spatial", task.ID})[:32], TaskID: task.ID, AssetVersionID: source.AssetVersionID, ResourceID: resource.ID, MediaType: "image", Role: "scene_" + source.Kind, MetadataJSON: string(metadata), CreatedAt: time.Now()}
	applied, err := s.repo.SaveProjectSpatialImage(task.UserID, task.ProjectID, source.AssetID, source.AssetVersionID, &rep)
	if errors.Is(err, repository.ErrProjectAssetVersionChanged) {
		result["spatialImageStatus"] = "stale"
		return false, nil
	}
	if err == nil {
		result["spatialImageStatus"] = "bound"
	}
	return applied, err
}
