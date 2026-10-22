package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const builtinShortDramaWorkflowKey = "short-drama-production"
const builtinShortDramaWorkflowVersion = 2

type workflowStepDefinition struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

var builtinShortDramaSteps = []workflowStepDefinition{
	{Key: "story", Name: "剧情与章节"},
	{Key: "assets", Name: "资产拆分"},
	{Key: "storyboard", Name: "分镜脚本"},
	{Key: "previz", Name: "黑白动作预演"},
	{Key: "video", Name: "视频生成"},
	{Key: "delivery", Name: "交付与打包"},
}

type ProjectWorkflowDetail struct {
	Instance model.WorkflowInstance       `json:"instance"`
	Steps    []model.WorkflowStepInstance `json:"steps"`
}

type UpdateWorkflowStepRequest struct {
	Status     string `json:"status"`
	OutputJSON string `json:"outputJson"`
	Error      string `json:"error"`
}

type RegisterTaskOutputRequest struct {
	TaskID         string `json:"taskId"`
	CanvasID       string `json:"canvasId"`
	UnitID         string `json:"unitId"`
	ShotID         string `json:"shotId"`
	ShotRevisionID string `json:"shotRevisionId"`
	ArtifactType   string `json:"artifactType"`
	AssetVersionID string `json:"assetVersionId"`
	ResourceID     string `json:"resourceId"`
	MediaType      string `json:"mediaType"`
	Role           string `json:"role"`
	MetadataJSON   string `json:"metadataJson"`
	OutputJSON     string `json:"outputJson"`
}

func (s *Service) EnsureBuiltinProjectWorkflowTemplate() error {
	if _, err := s.repo.WorkflowTemplateVersion(builtinShortDramaWorkflowKey, builtinShortDramaWorkflowVersion); err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	definition, err := json.Marshal(map[string]any{"scope": []string{"project", "unit"}, "steps": builtinShortDramaSteps})
	if err != nil {
		return err
	}
	template := model.WorkflowTemplateVersion{ID: newID(), TemplateKey: builtinShortDramaWorkflowKey, Name: "短剧分镜工作流", Version: builtinShortDramaWorkflowVersion, DefinitionJSON: string(definition), CreatedAt: time.Now()}
	return s.repo.CreateWorkflowTemplateVersion(&template)
}

func (s *Service) ProjectWorkflows(projectID string) ([]ProjectWorkflowDetail, error) {
	instances, err := s.repo.ProjectWorkflowInstances(projectID)
	if err != nil {
		return nil, err
	}
	result := make([]ProjectWorkflowDetail, 0, len(instances))
	for _, instance := range instances {
		steps, stepsErr := s.repo.WorkflowSteps(instance.ID)
		if stepsErr != nil {
			return nil, stepsErr
		}
		result = append(result, ProjectWorkflowDetail{Instance: instance, Steps: steps})
	}
	return result, nil
}

// errWorkflowReadOnly：旧六步流程已改为只读。进度由段、产物和证据推导（见 internal/drama），
// 不再创建流程实例、不再切换步骤状态；已有记录仍可读，供旧项目查看。
func errWorkflowReadOnly() error {
	return BadAuthRequest("旧六步流程已改为只读：进度按段、产物和证据自动推导，不再手动建流程或切换阶段")
}

func (s *Service) CreateUnitWorkflow(userID string, projectID string, unitID string) (ProjectWorkflowDetail, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return ProjectWorkflowDetail{}, err
	}
	if _, err := s.repo.ProjectUnit(projectID, unitID); err != nil {
		return ProjectWorkflowDetail{}, err
	}
	return ProjectWorkflowDetail{}, errWorkflowReadOnly()
}

func (s *Service) UpdateWorkflowStep(userID string, projectID string, stepID string, _ UpdateWorkflowStepRequest) (model.WorkflowStepInstance, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return model.WorkflowStepInstance{}, err
	}
	if _, err := s.repo.WorkflowStepForProject(projectID, stepID); err != nil {
		return model.WorkflowStepInstance{}, err
	}
	return model.WorkflowStepInstance{}, errWorkflowReadOnly()
}

// RegisterTaskOutput 是旧接口：按步骤登记产物。步骤只用来校验归属和推断产物类型，状态不再推进。
func (s *Service) RegisterTaskOutput(userID string, projectID string, stepID string, req RegisterTaskOutputRequest) (model.WorkflowStepInstance, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return model.WorkflowStepInstance{}, err
	}
	step, err := s.repo.WorkflowStepForProject(projectID, stepID)
	if err != nil {
		return model.WorkflowStepInstance{}, err
	}
	if _, err := s.registerTaskOutput(userID, projectID, step.ID, workflowArtifactType(step.StepKey), req); err != nil {
		return model.WorkflowStepInstance{}, err
	}
	return *step, nil
}

// RegisterShotOutput 把一条成功任务的产物登记到段上，不经旧六步流程。
func (s *Service) RegisterShotOutput(userID string, projectID string, req RegisterTaskOutputRequest) error {
	if strings.TrimSpace(req.ShotID) == "" {
		return BadAuthRequest("登记产物需要指定段")
	}
	_, err := s.registerTaskOutput(userID, projectID, "", "", req)
	return err
}

func (s *Service) registerTaskOutput(userID string, projectID string, stepID string, fallbackArtifactType string, req RegisterTaskOutputRequest) (*model.ShotArtifact, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	task, err := s.repo.TaskForUser(userID, strings.TrimSpace(req.TaskID))
	if err != nil {
		return nil, err
	}
	canvasID := strings.TrimSpace(req.CanvasID)
	if task.ProjectID != projectID {
		canvas, canvasErr := s.repo.CanvasProjectForUser(userID, task.ProjectID)
		if canvasErr != nil || canvas.ProjectID != projectID {
			return nil, BadAuthRequest("任务不属于当前项目")
		}
		if canvasID != "" && canvasID != canvas.ID {
			return nil, BadAuthRequest("任务画布与产物画布不一致")
		}
		canvasID = canvas.ID
	}
	if canvasID != "" {
		canvas, canvasErr := s.repo.CanvasProjectForUser(userID, canvasID)
		if canvasErr != nil || canvas.ProjectID != projectID {
			return nil, BadAuthRequest("画布不属于当前项目")
		}
	}
	if task.Status != model.TaskStatusSucceeded {
		return nil, BadAuthRequest("只有成功任务才能登记产物")
	}
	unitID := strings.TrimSpace(req.UnitID)
	shotID := strings.TrimSpace(req.ShotID)
	var shot *model.Shot
	if shotID != "" {
		shot, err = s.repo.ShotForProject(projectID, shotID)
		if err != nil {
			return nil, err
		}
		if unitID == "" {
			unitID = shot.UnitID
		} else if unitID != shot.UnitID {
			return nil, BadAuthRequest("镜头不属于指定章节")
		}
	} else if unitID != "" {
		if _, err := s.repo.ProjectUnit(projectID, unitID); err != nil {
			return nil, err
		}
	}
	shotRevisionID := strings.TrimSpace(req.ShotRevisionID)
	if shot != nil {
		if shotRevisionID == "" {
			shotRevisionID = shot.CurrentRevisionID
		} else if _, revisionErr := s.repo.ShotRevisionForShot(shot.ID, shotRevisionID); revisionErr != nil {
			return nil, revisionErr
		}
	} else if shotRevisionID != "" {
		return nil, BadAuthRequest("镜头版本缺少所属镜头")
	}
	if versionID := strings.TrimSpace(req.AssetVersionID); versionID != "" {
		if _, err := s.repo.AssetVersionForProject(projectID, versionID); err != nil {
			return nil, err
		}
	}
	if resourceID := strings.TrimSpace(req.ResourceID); resourceID != "" {
		if _, err := s.repo.ResourceForUser(userID, resourceID); err != nil {
			return nil, err
		}
	}
	metadata := strings.TrimSpace(req.MetadataJSON)
	if metadata == "" {
		metadata = "{}"
	}
	if !json.Valid([]byte(metadata)) {
		return nil, BadAuthRequest("产物元数据必须是有效 JSON")
	}
	now := time.Now()
	var representation *model.AssetRepresentation
	if strings.TrimSpace(req.AssetVersionID) != "" {
		role := strings.TrimSpace(req.Role)
		if role == "" {
			role = "output"
		}
		if !validShotAssetRole(role) {
			return nil, BadAuthRequest("不支持的产物用途")
		}
		representation = &model.AssetRepresentation{ID: newID(), TaskID: task.ID, AssetVersionID: strings.TrimSpace(req.AssetVersionID), ResourceID: strings.TrimSpace(req.ResourceID), MediaType: strings.TrimSpace(req.MediaType), Role: role, MetadataJSON: metadata, CreatedAt: now}
	}
	artifactType := strings.TrimSpace(req.ArtifactType)
	if artifactType == "" && shotID != "" && strings.TrimSpace(req.ResourceID) != "" {
		artifactType = fallbackArtifactType
	}
	productionLink := &model.ProductionTaskLink{ID: newID(), TaskID: task.ID, ProjectID: projectID, CanvasID: canvasID, UnitID: unitID, ShotID: shotID, WorkflowStepID: stepID, ArtifactType: artifactType, CreatedAt: now, UpdatedAt: now}
	var artifact *model.ShotArtifact
	if shot != nil && strings.TrimSpace(req.ResourceID) != "" && artifactType != "" {
		artifact = &model.ShotArtifact{ID: newID(), ProjectID: projectID, UnitID: shot.UnitID, ShotID: shot.ID, RevisionID: shotRevisionID, TaskID: task.ID, Type: artifactType, ResourceID: strings.TrimSpace(req.ResourceID), Status: "ready", Selected: true, MetadataJSON: metadata, CreatedAt: now, UpdatedAt: now}
		artifact.InputHash = s.artifactInputHash(shot.ID, shotRevisionID, artifactType)
	}
	if err := s.repo.RegisterTaskOutputRecords(projectID, representation, productionLink, artifact); err != nil {
		return nil, err
	}
	return artifact, nil
}

func workflowArtifactType(stepKey string) string {
	switch strings.TrimSpace(stepKey) {
	case "storyboard":
		return "storyboard"
	case "previz":
		return "action_board"
	case "video":
		return "video"
	case "delivery":
		return "delivery"
	default:
		return ""
	}
}

func (s *Service) RegisterTaskOutputFromTask(task model.Task) error {
	if strings.TrimSpace(task.ProjectID) == "" || task.Status != model.TaskStatusSucceeded {
		return nil
	}
	if strings.TrimSpace(task.InputJSON) == "" {
		return nil
	}
	decrypted, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		return err
	}
	var input struct {
		WorkflowStepID  string         `json:"workflowStepId"`
		DomainProjectID string         `json:"domainProjectId"`
		CanvasID        string         `json:"canvasId"`
		UnitID          string         `json:"unitId"`
		ShotID          string         `json:"shotId"`
		ShotRevisionID  string         `json:"shotRevisionId"`
		ArtifactType    string         `json:"artifactType"`
		AssetVersionID  string         `json:"assetVersionId"`
		ResourceID      string         `json:"resourceId"`
		MediaType       string         `json:"mediaType"`
		Role            string         `json:"role"`
		MetadataJSON    string         `json:"metadataJson"`
		Metadata        map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(decrypted), &input); err != nil {
		return err
	}
	if input.Metadata != nil {
		if input.WorkflowStepID == "" {
			input.WorkflowStepID, _ = input.Metadata["workflowStepId"].(string)
		}
		if input.DomainProjectID == "" {
			input.DomainProjectID, _ = input.Metadata["domainProjectId"].(string)
		}
		if input.AssetVersionID == "" {
			input.AssetVersionID, _ = input.Metadata["assetVersionId"].(string)
		}
		if input.CanvasID == "" {
			input.CanvasID, _ = input.Metadata["canvasId"].(string)
		}
		if input.UnitID == "" {
			input.UnitID, _ = input.Metadata["unitId"].(string)
		}
		if input.ShotID == "" {
			input.ShotID, _ = input.Metadata["shotId"].(string)
		}
		if input.ShotRevisionID == "" {
			input.ShotRevisionID, _ = input.Metadata["shotRevisionId"].(string)
		}
		if input.ArtifactType == "" {
			input.ArtifactType, _ = input.Metadata["artifactType"].(string)
		}
		if input.ResourceID == "" {
			input.ResourceID, _ = input.Metadata["resourceId"].(string)
		}
		if input.MediaType == "" {
			input.MediaType, _ = input.Metadata["mediaType"].(string)
		}
		if input.Role == "" {
			input.Role, _ = input.Metadata["role"].(string)
		}
		if input.MetadataJSON == "" {
			if artifactMetadata, ok := input.Metadata["artifactMetadata"]; ok {
				if encoded, encodeErr := json.Marshal(artifactMetadata); encodeErr == nil {
					input.MetadataJSON = string(encoded)
				}
			}
		}
	}
	if strings.TrimSpace(input.ResourceID) == "" {
		input.ResourceID, input.MediaType = taskOutputResource(task.ResultJSON, task.Type)
	}
	if strings.TrimSpace(input.MediaType) == "" && strings.TrimSpace(input.ResourceID) != "" {
		input.MediaType = taskOutputMediaType(task.Type)
	}
	// 旧任务带 workflowStepId；按段生成的任务只带 domainProjectId 与 shotId。两者都没有就不是短剧产物。
	if strings.TrimSpace(input.WorkflowStepID) == "" && (strings.TrimSpace(input.ShotID) == "" || strings.TrimSpace(input.DomainProjectID) == "") {
		return nil
	}
	projectID := strings.TrimSpace(input.DomainProjectID)
	if projectID == "" {
		if _, projectErr := s.repo.ProjectForUser(task.UserID, task.ProjectID); projectErr == nil {
			projectID = task.ProjectID
		}
	}
	if projectID == "" {
		return errors.New("任务未提供短剧项目 ID，无法登记产物")
	}
	if strings.TrimSpace(input.ResourceID) != "" && strings.TrimSpace(input.AssetVersionID) == "" && strings.TrimSpace(input.ShotID) != "" {
		assetVersionID, assetErr := s.ensureGeneratedProjectAsset(task, projectID, input.ShotID, input.ResourceID, input.MediaType)
		if assetErr != nil {
			return assetErr
		}
		input.AssetVersionID = assetVersionID
	}
	request := RegisterTaskOutputRequest{TaskID: task.ID, CanvasID: input.CanvasID, UnitID: input.UnitID, ShotID: input.ShotID, ShotRevisionID: input.ShotRevisionID, ArtifactType: input.ArtifactType, AssetVersionID: input.AssetVersionID, ResourceID: input.ResourceID, MediaType: input.MediaType, Role: input.Role, MetadataJSON: input.MetadataJSON, OutputJSON: task.ResultJSON}
	if strings.TrimSpace(input.WorkflowStepID) == "" {
		return s.RegisterShotOutput(task.UserID, projectID, request)
	}
	_, err = s.RegisterTaskOutput(task.UserID, projectID, input.WorkflowStepID, request)
	return err
}

func taskOutputMediaType(taskType string) string {
	if strings.Contains(strings.ToLower(taskType), "video") {
		return "video"
	}
	return "image"
}

func taskOutputResource(raw string, taskType string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", ""
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return "", ""
	}
	return findTaskOutputResource(value, taskOutputMediaType(taskType))
}

func findTaskOutputResource(value any, mediaType string) (string, string) {
	switch item := value.(type) {
	case []any:
		for _, child := range item {
			if id, kind := findTaskOutputResource(child, mediaType); id != "" {
				return id, kind
			}
		}
	case map[string]any:
		for _, key := range []string{"resourceId", "storageKey", "url", "dataUrl", "resultUrl", "outputUrl"} {
			if raw, ok := item[key].(string); ok {
				text := strings.TrimSpace(raw)
				if strings.HasPrefix(text, "resource:") {
					return strings.TrimPrefix(text, "resource:"), mediaType
				}
				if id := resourceIDFromFileURL(text); id != "" {
					return id, mediaType
				}
			}
		}
		for _, child := range item {
			if id, kind := findTaskOutputResource(child, mediaType); id != "" {
				return id, kind
			}
		}
	}
	return "", ""
}

func (s *Service) ensureGeneratedProjectAsset(task model.Task, projectID string, shotID string, resourceID string, mediaType string) (string, error) {
	representations, err := s.repo.AssetRepresentationsForTask(task.ID)
	if err != nil {
		return "", err
	}
	for _, representation := range representations {
		if representation.Role == "output" && representation.ResourceID == resourceID && representation.AssetVersionID != "" {
			return representation.AssetVersionID, nil
		}
	}
	resource, err := s.repo.ResourceForUser(task.UserID, resourceID)
	if err != nil {
		return "", err
	}
	if resource.Status != model.ResourceStatusReady {
		return "", BadAuthRequest("生成资源尚未就绪，无法登记项目素材")
	}
	shot, err := s.repo.ShotForProject(projectID, shotID)
	if err != nil {
		return "", err
	}
	now := time.Now()
	assetID := workflowGeneratedEntityID("asset", task.ID)
	versionID := workflowGeneratedEntityID("version", task.ID)
	label := "图片"
	if mediaType == "video" {
		label = "视频"
	}
	title := strings.TrimSpace(shot.Title)
	if title == "" {
		title = "镜头产物"
	}
	title += " · " + label
	width := resource.Width
	height := resource.Height
	if width <= 0 {
		width = 1
	}
	if height <= 0 {
		height = 1
	}
	resourceURL := "/api/resources/" + resourceID + "/file"
	data := map[string]any{
		"storageKey": "resource:" + resourceID,
		"mimeType":   resource.MimeType,
		"bytes":      resource.Size,
		"width":      width,
		"height":     height,
	}
	if mediaType == "image" {
		data["dataUrl"] = resourceURL
	} else {
		data["url"] = resourceURL
		if resource.DurationMs > 0 {
			data["durationMs"] = resource.DurationMs
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"id": assetID, "kind": mediaType, "category": model.AssetCategoryMaterial, "status": model.AssetVersionStatusConfirmed,
		"primaryVersionId": versionID, "title": title, "coverUrl": resourceURL, "tags": []string{},
		"createdAt": now.UTC().Format(time.RFC3339Nano), "updatedAt": now.UTC().Format(time.RFC3339Nano),
		"data": data, "metadata": map[string]any{"source": "short-drama-workflow", "taskId": task.ID, "shotId": shot.ID, "projectIds": []string{projectID}},
	})
	asset := &model.Asset{ID: assetID, UserID: task.UserID, Kind: mediaType, Category: model.AssetCategoryMaterial, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: versionID, Title: title, PayloadJSON: string(payload), CreatedAt: now, UpdatedAt: now}
	version := &model.AssetVersion{ID: versionID, AssetID: assetID, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: "{}", Prompt: task.Prompt, Note: "工作流生成产物", CreatedAt: now, UpdatedAt: now}
	folderID, err := s.resolveProjectAssetFolderID(projectID, nil)
	if err != nil {
		return "", err
	}
	position, err := s.repo.NextProjectAssetPosition(projectID, folderID)
	if err != nil {
		return "", err
	}
	link := &model.ProjectAssetLink{ID: newID(), ProjectID: projectID, AssetID: assetID, FolderID: folderID, Position: position, CreatedAt: now}
	if err := s.repo.UpsertAsset(asset); err != nil {
		return "", err
	}
	if _, err := s.repo.LinkProjectAsset(asset, version, link); err != nil {
		return "", err
	}
	return versionID, nil
}

// 工作流产物重试必须复用同一实体 ID，同时需要满足素材版本及外键的 varchar(36) 约束。
func workflowGeneratedEntityID(namespace string, taskID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(namespace) + ":" + strings.TrimSpace(taskID)))
	return hex.EncodeToString(sum[:16])
}
