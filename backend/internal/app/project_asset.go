package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// 资产来源：媒体导入合成/画布产物自动同步。画布产物用 canvas 以便素材面板分组。
const (
	AssetSourceUploaded = "uploaded"
	AssetSourceCanvas   = "canvas"
)

type LinkProjectAssetRequest struct {
	AssetID  string  `json:"assetId"`
	Category string  `json:"category"`
	FolderID *string `json:"folderId"`
	// Title 媒体导入场景下由前端携带原始文件名；已有资产时忽略。
	Title string `json:"title"`
	// Source 仅媒体导入合成时生效：uploaded（默认）或 canvas（画布产物自动同步）。
	Source string `json:"source"`
}

type UpdateProjectAssetRequest struct {
	Category *string `json:"category"`
	FolderID *string `json:"folderId"`
	// Anchors 是场景资产的空间锚点（人物能站、能走到的位置）：写进主版本设定的 anchors，分镜提示词和镜头表检查 R-SHOT-012 读它。
	Anchors *[]string `json:"anchors"`
}

const (
	sceneAnchorMax    = 20
	sceneAnchorMaxLen = 24
)

type CreateAssetVersionRequest struct {
	Prompt         string `json:"prompt"`
	DefinitionJSON string `json:"definitionJson"`
	Note           string `json:"note"`
}

type ProjectAssetSummary struct {
	ID               string                     `json:"id"`
	Title            string                     `json:"title"`
	MediaType        string                     `json:"mediaType"`
	Category         model.AssetCategory        `json:"category"`
	Status           model.AssetVersionStatus   `json:"status"`
	PrimaryVersionID string                     `json:"primaryVersionId,omitempty"`
	VersionCount     int                        `json:"versionCount"`
	Usages           []string                   `json:"usages"`
	FolderID         string                     `json:"folderId,omitempty"`
	Position         int                        `json:"position"`
	StorageKey       string                     `json:"storageKey,omitempty"`
	DurationMs       int64                      `json:"durationMs,omitempty"`
	PreviewText      string                     `json:"previewText,omitempty"`
	UpdatedAt        time.Time                  `json:"updatedAt"`
	Source           string                     `json:"source,omitempty"`
	Character        *CharacterCardSummary      `json:"character,omitempty"`
	Visual           *ProjectAssetVisualSummary `json:"visual,omitempty"`
	// Palette 是色卡（category=palette）的色彩脚本、出图状态和取色校验。
	Palette *ProjectAssetPaletteSummary `json:"palette,omitempty"`
	// Image 是资产设计建的角色、场景、道具的出图排队状态（别的资产没有）。
	Image *ProjectAssetImageSummary `json:"image,omitempty"`
	// Variant 是角色子卡（M8 卡 D）的主卡、变体名、种类和场；Variants 是资产页分页里挂在主卡下面的子卡（别处不填）。
	Variant  *ProjectAssetVariantSummary `json:"variant,omitempty"`
	Variants []ProjectAssetSummary       `json:"variants,omitempty"`
}

type ProjectAssetFilter struct {
	Category  string
	MediaType string
	Status    string
	Usage     string
}

type ConfirmProjectAssetCandidateRequest struct {
	AssetID string `json:"assetId"`
}

func (s *Service) FilterProjectAssets(userID string, projectID string, filter ProjectAssetFilter) ([]ProjectAssetSummary, error) {
	assets, err := s.ProjectAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	result := make([]ProjectAssetSummary, 0, len(assets))
	for _, asset := range assets {
		if filter.Category != "" && string(asset.Category) != filter.Category {
			continue
		}
		if filter.MediaType != "" && asset.MediaType != filter.MediaType {
			continue
		}
		if filter.Status != "" && string(asset.Status) != filter.Status {
			continue
		}
		if filter.Usage != "" && !containsString(asset.Usages, filter.Usage) {
			continue
		}
		result = append(result, asset)
	}
	return result, nil
}

func (s *Service) ProjectAssets(userID string, projectID string) ([]ProjectAssetSummary, error) {
	assets, err := s.repo.ProjectAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	result := make([]ProjectAssetSummary, 0, len(assets))
	for _, asset := range assets {
		summary, summaryErr := s.projectAssetSummary(userID, projectID, &asset)
		if summaryErr != nil {
			return nil, summaryErr
		}
		result = append(result, summary)
	}
	return result, nil
}

func (s *Service) LinkProjectAsset(userID string, projectID string, req LinkProjectAssetRequest) (ProjectAssetSummary, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return ProjectAssetSummary{}, err
	}
	assetID := strings.TrimSpace(req.AssetID)
	source := strings.TrimSpace(req.Source)
	if source != AssetSourceCanvas {
		source = AssetSourceUploaded
	}
	asset, err := s.repo.AssetForUser(userID, assetID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return ProjectAssetSummary{}, err
	}
	if asset == nil {
		// 媒体导入只落 resources 表；首次链接时按资源元数据合成资产记录，
		// 避免“资源存在但无资产记录”导致导入永远失败。
		asset, err = s.assetFromUploadedResource(userID, assetID, req.Title, source)
		if err != nil {
			return ProjectAssetSummary{}, err
		}
	}
	category := model.AssetCategory(strings.TrimSpace(req.Category))
	if category == "" {
		category = asset.Category
	}
	if !validAssetCategory(category) {
		return ProjectAssetSummary{}, BadAuthRequest("不支持的资产业务分类")
	}
	if category == model.AssetCategoryPalette && asset.Category != model.AssetCategoryPalette {
		return ProjectAssetSummary{}, BadAuthRequest("色卡由导演风格按色彩脚本生成，不能把上传的素材加成色卡")
	}
	folderID, err := s.resolveProjectAssetFolderID(projectID, req.FolderID)
	if err != nil {
		return ProjectAssetSummary{}, err
	}
	linked, err := s.repo.ProjectAssetLinked(projectID, asset.ID)
	if err != nil {
		return ProjectAssetSummary{}, err
	}
	if linked {
		return s.projectAssetSummary(userID, projectID, asset)
	}
	now := time.Now()
	asset.Category = category
	if asset.Status == "" {
		asset.Status = model.AssetVersionStatusConfirmed
	}
	versions, err := s.repo.AssetVersions(asset.ID)
	if err != nil {
		return ProjectAssetSummary{}, err
	}
	var initialVersion *model.AssetVersion
	if len(versions) == 0 {
		version := model.AssetVersion{ID: newID(), AssetID: asset.ID, Version: 1, Status: asset.Status, DefinitionJSON: "{}", CreatedAt: now, UpdatedAt: now}
		initialVersion = &version
		asset.PrimaryVersionID = version.ID
		versions = append(versions, version)
	}
	asset.UpdatedAt = now
	position, err := s.repo.NextProjectAssetPosition(projectID, folderID)
	if err != nil {
		return ProjectAssetSummary{}, err
	}
	link := model.ProjectAssetLink{ID: newID(), ProjectID: projectID, AssetID: asset.ID, FolderID: folderID, Position: position, CreatedAt: now}
	created, err := s.repo.LinkProjectAsset(asset, initialVersion, &link)
	if err != nil {
		return ProjectAssetSummary{}, err
	}
	if !created {
		current, currentErr := s.repo.AssetForUser(userID, asset.ID)
		if currentErr != nil {
			return ProjectAssetSummary{}, currentErr
		}
		return s.projectAssetSummary(userID, projectID, current)
	}
	return s.projectAssetSummary(userID, projectID, asset)
}

// assetFromUploadedResource 把媒体导入上传的 Resource 合成为资产记录（内存构造，不落库）。
// 资产创建与项目链接由 LinkProjectAsset 事务原子提交，避免失败留下孤立资产。
func (s *Service) assetFromUploadedResource(userID string, resourceID string, title string, source string) (*model.Asset, error) {
	resource, err := s.repo.ResourceForUser(userID, resourceID)
	if err != nil {
		return nil, err
	}
	if resource.Kind == "" {
		return nil, BadAuthRequest("资源缺少媒体类型，无法导入")
	}
	payload, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"storageKey": "resource:" + resource.ID,
			"mimeType":   resource.MimeType,
			"kind":       resource.Kind,
			"bytes":      resource.Size,
			"width":      resource.Width,
			"height":     resource.Height,
			"durationMs": resource.DurationMs,
			"status":     resource.Status,
			"mediaType":  resource.Kind,
			"source":     source,
		},
	})
	if err != nil {
		return nil, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = mediaTitleFallback(resource)
	}
	asset := &model.Asset{
		ID:          resource.ID,
		UserID:      userID,
		Kind:        resource.Kind,
		Category:    model.NormalizeAssetCategory("", resource.Kind),
		Status:      model.AssetVersionStatusConfirmed,
		Title:       title,
		PayloadJSON: string(payload),
		CreatedAt:   resource.CreatedAt,
		UpdatedAt:   resource.UpdatedAt,
	}
	asset.PayloadJSON = string(clientAssetPayload(*asset))
	// 不在此预落库：资产创建交给 LinkProjectAsset 的事务统一提交，
	// 事务失败时与链接一起回滚，避免留下“有资产无链接”的孤儿记录。
	return asset, nil
}

// mediaTitleFallback 资源无文件名时的展示标题：未命名 + 媒体类型 + 分辨率。
func mediaTitleFallback(resource *model.Resource) string {
	base := "未命名" + resource.Kind
	if resource.Width > 0 && resource.Height > 0 {
		base += fmt.Sprintf(" %dx%d", resource.Width, resource.Height)
	}
	return base
}

// UnlinkProjectAsset 把资产移出项目。角色主卡带着这个项目里的子卡（M8 卡 D）一起移出：主卡或任一子卡还被画布、镜头引用时
// 整批拒绝（镜头引用时 409，列出镜头），不留下看不见的子卡。
func (s *Service) UnlinkProjectAsset(userID string, projectID string, assetID string) error {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return err
	}
	asset, err := s.repo.AssetForUser(userID, assetID)
	if err != nil {
		return err
	}
	targets := []model.Asset{*asset}
	if asset.Category == model.AssetCategoryCharacter && asset.ParentAssetID == "" {
		children, err := s.projectVariants(userID, projectID, asset.ID)
		if err != nil {
			return err
		}
		targets = append(targets, children...)
	}
	if asset.Category == model.AssetCategoryCharacter {
		canvases, canvasErr := s.repo.ProjectCanvasDocuments(userID, projectID)
		if canvasErr != nil {
			return canvasErr
		}
		for _, canvas := range canvases {
			for _, target := range targets {
				referenced, parseErr := canvasReferencesCharacterAsset(canvas.PayloadJSON, target.ID)
				if parseErr != nil {
					return BadAuthRequest("画布数据格式错误，无法确认角色引用")
				}
				if referenced {
					return BadAuthRequest(fmt.Sprintf("角色「%s」仍被项目画布引用，请先删除对应角色卡节点", target.Title))
				}
			}
		}
	}
	if len(targets) == 1 {
		references, err := s.repo.ProjectAssetShotReferenceCount(projectID, assetID)
		if err != nil {
			return err
		}
		if references > 0 {
			return BadAuthRequest("素材仍被项目镜头引用，请先解除镜头用途")
		}
	} else if err := s.variantShotReferencesConflict(projectID, targets); err != nil {
		return err
	}
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ID)
	}
	if err := s.repo.DeleteProjectAssetLinks(projectID, ids); err != nil {
		return err
	}
	return s.repo.BumpProjectRevision(projectID)
}

func canvasReferencesCharacterAsset(payloadJSON string, assetID string) (bool, error) {
	var payload struct {
		Nodes []struct {
			Metadata struct {
				WorkflowKind     string `json:"workflowKind"`
				CharacterAssetID string `json:"characterAssetId"`
			} `json:"metadata"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return false, err
	}
	for _, node := range payload.Nodes {
		if node.Metadata.WorkflowKind == "character" && node.Metadata.CharacterAssetID == assetID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) UpdateProjectAsset(userID string, projectID string, assetID string, req UpdateProjectAssetRequest) (ProjectAssetSummary, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return ProjectAssetSummary{}, err
	}
	asset, err := s.repo.AssetForUser(userID, strings.TrimSpace(assetID))
	if err != nil {
		return ProjectAssetSummary{}, err
	}
	linked, err := s.repo.ProjectAssetLinked(projectID, asset.ID)
	if err != nil {
		return ProjectAssetSummary{}, err
	}
	if !linked {
		return ProjectAssetSummary{}, BadAuthRequest("素材尚未加入当前项目")
	}
	if countSet(req.Category != nil, req.FolderID != nil, req.Anchors != nil) > 1 {
		return ProjectAssetSummary{}, BadAuthRequest("一次只能修改素材分类、所在文件夹或场景锚点中的一项")
	}
	if req.Anchors != nil {
		if err := s.updateSceneAnchors(projectID, asset, *req.Anchors); err != nil {
			return ProjectAssetSummary{}, err
		}
	} else if req.Category != nil {
		category := model.AssetCategory(strings.TrimSpace(*req.Category))
		if !validAssetCategory(category) {
			return ProjectAssetSummary{}, BadAuthRequest("不支持的资产业务分类")
		}
		// 色卡带着场编号和色彩脚本，改分类会让「色卡齐了」和重跑时的按场查找对不上。
		if (category == model.AssetCategoryPalette) != (asset.Category == model.AssetCategoryPalette) {
			return ProjectAssetSummary{}, BadAuthRequest("色卡和其他分类之间不能互改")
		}
		asset.Category = category
		asset.UpdatedAt = time.Now()
		if err := s.repo.UpdateAssetDomain(asset); err != nil {
			return ProjectAssetSummary{}, err
		}
		if err := s.repo.BumpProjectRevision(projectID); err != nil {
			return ProjectAssetSummary{}, err
		}
	} else if req.FolderID != nil {
		folderID, folderErr := s.resolveProjectAssetFolderID(projectID, req.FolderID)
		if folderErr != nil {
			return ProjectAssetSummary{}, folderErr
		}
		position, positionErr := s.repo.NextProjectAssetPosition(projectID, folderID)
		if positionErr != nil {
			return ProjectAssetSummary{}, positionErr
		}
		if err := s.repo.MoveProjectAsset(projectID, asset.ID, folderID, position); err != nil {
			return ProjectAssetSummary{}, err
		}
	} else {
		return ProjectAssetSummary{}, BadAuthRequest("没有可更新的素材字段")
	}
	return s.projectAssetSummary(userID, projectID, asset)
}

func countSet(flags ...bool) int {
	n := 0
	for _, f := range flags {
		if f {
			n++
		}
	}
	return n
}

// updateSceneAnchors 原地改场景主版本设定里的 anchors：锚点只描述空间，不影响已出的场景图，也不该让镜头引用的版本失效，所以不建新版本。
func (s *Service) updateSceneAnchors(projectID string, asset *model.Asset, anchors []string) error {
	if dramaRefRole(asset.Category) != "location" {
		return BadAuthRequest("只有场景资产能写锚点")
	}
	if asset.PrimaryVersionID == "" {
		return BadAuthRequest("场景资产还没有确认的版本")
	}
	cleaned := sceneAnchorList(anchors)
	if len(cleaned) > sceneAnchorMax {
		return BadAuthRequest(fmt.Sprintf("锚点最多 %d 个", sceneAnchorMax))
	}
	for _, a := range cleaned {
		if len([]rune(a)) > sceneAnchorMaxLen {
			return BadAuthRequest(fmt.Sprintf("锚点「%s」太长，写成 %d 字以内的位置名", a, sceneAnchorMaxLen))
		}
	}
	version, err := s.repo.AssetVersion(asset.PrimaryVersionID)
	if err != nil {
		return err
	}
	definition := map[string]any{}
	if strings.TrimSpace(version.DefinitionJSON) != "" {
		if err := json.Unmarshal([]byte(version.DefinitionJSON), &definition); err != nil || definition == nil {
			return BadAuthRequest("场景设定格式无效，不能写锚点")
		}
	}
	if len(cleaned) == 0 {
		delete(definition, "anchors")
	} else {
		definition["anchors"] = cleaned
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateAssetVersionDefinition(asset.ID, version.ID, string(encoded), time.Now()); err != nil {
		return err
	}
	return s.repo.BumpProjectRevision(projectID)
}

func (s *Service) CreateProjectAssetVersion(userID string, projectID string, assetID string, req CreateAssetVersionRequest) (model.AssetVersion, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return model.AssetVersion{}, err
	}
	asset, err := s.repo.AssetForUser(userID, assetID)
	if err != nil {
		return model.AssetVersion{}, err
	}
	linked, err := s.repo.ProjectAssetLinked(projectID, assetID)
	if err != nil {
		return model.AssetVersion{}, err
	}
	if !linked {
		return model.AssetVersion{}, BadAuthRequest("素材尚未加入当前项目")
	}
	versions, err := s.repo.AssetVersions(assetID)
	if err != nil {
		return model.AssetVersion{}, err
	}
	nextVersion := 1
	if len(versions) > 0 {
		nextVersion = versions[0].Version + 1
	}
	definition := strings.TrimSpace(req.DefinitionJSON)
	if definition == "" {
		definition = "{}"
	}
	if !json.Valid([]byte(definition)) {
		return model.AssetVersion{}, BadAuthRequest("资产版本设定必须是有效 JSON")
	}
	now := time.Now()
	version := model.AssetVersion{ID: newID(), AssetID: assetID, Version: nextVersion, Status: model.AssetVersionStatusDraft, DefinitionJSON: definition, Prompt: strings.TrimSpace(req.Prompt), Note: strings.TrimSpace(req.Note), CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreateAssetVersion(&version); err != nil {
		return model.AssetVersion{}, err
	}
	asset.PrimaryVersionID = version.ID
	asset.Status = model.AssetVersionStatusDraft
	asset.UpdatedAt = now
	if err := s.repo.UpdateAssetDomain(asset); err != nil {
		return model.AssetVersion{}, err
	}
	if err := s.repo.BumpProjectRevision(projectID); err != nil {
		return model.AssetVersion{}, err
	}
	return version, nil
}

func (s *Service) ConfirmProjectAssetCandidate(userID string, projectID string, candidateID string, req ConfirmProjectAssetCandidateRequest) (ProjectAssetSummary, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return ProjectAssetSummary{}, err
	}
	candidate, err := s.repo.ProjectAssetCandidate(projectID, candidateID)
	if err != nil {
		return ProjectAssetSummary{}, err
	}
	if candidate.Status != "pending_confirmation" {
		return ProjectAssetSummary{}, BadAuthRequest("资产候选已处理")
	}
	now := time.Now()
	assetID := strings.TrimSpace(req.AssetID)
	if assetID != "" && candidate.Category == model.AssetCategoryCharacter {
		asset, assetErr := s.repo.ProjectCharacterAsset(userID, projectID, assetID)
		if assetErr != nil {
			return ProjectAssetSummary{}, assetErr
		}
		current, versionErr := s.repo.AssetVersion(asset.PrimaryVersionID)
		if versionErr != nil {
			return ProjectAssetSummary{}, versionErr
		}
		definition, mergeErr := mergeCharacterCandidateDefinition(current.DefinitionJSON, candidate.DetailsJSON, asset.Title, candidate.Name)
		if mergeErr != nil {
			return ProjectAssetSummary{}, mergeErr
		}
		nextAsset, nextVersion, representations, voice, prepareErr := s.prepareNextCharacterVersion(asset, asset.Title, definition, nil, nil, false)
		if prepareErr != nil {
			return ProjectAssetSummary{}, prepareErr
		}
		candidate.Status = "confirmed"
		candidate.ResolvedAssetID = asset.ID
		candidate.UpdatedAt = now
		if saveErr := s.repo.ConfirmProjectCharacterCandidate(candidate, &nextAsset, &nextVersion, representations, voice); saveErr != nil {
			return ProjectAssetSummary{}, saveErr
		}
		return s.projectAssetSummary(userID, projectID, &nextAsset)
	}
	createAsset := assetID == ""
	var asset model.Asset
	var version model.AssetVersion
	if createAsset {
		var buildErr error
		if asset, version, buildErr = assetFromCandidate(userID, candidate, now); buildErr != nil {
			return ProjectAssetSummary{}, buildErr
		}
		assetID = asset.ID
	} else {
		existing, assetErr := s.repo.AssetForUser(userID, assetID)
		if assetErr != nil {
			return ProjectAssetSummary{}, assetErr
		}
		asset = *existing
	}
	candidate.Status = "confirmed"
	candidate.ResolvedAssetID = assetID
	candidate.UpdatedAt = now
	folderID, folderErr := s.resolveProjectAssetFolderID(projectID, nil)
	if folderErr != nil {
		return ProjectAssetSummary{}, folderErr
	}
	position, positionErr := s.repo.NextProjectAssetPosition(projectID, folderID)
	if positionErr != nil {
		return ProjectAssetSummary{}, positionErr
	}
	link := model.ProjectAssetLink{ID: newID(), ProjectID: projectID, AssetID: assetID, FolderID: folderID, Position: position, CreatedAt: now}
	if err := s.repo.ConfirmProjectAssetCandidate(candidate, &asset, &version, &link, createAsset); err != nil {
		return ProjectAssetSummary{}, err
	}
	return s.projectAssetSummary(userID, projectID, &asset)
}

// assetFromCandidate 按候选建一个已确认资产和它的第 1 个版本（资产页确认候选、资产设计自动确认共用）：角色、场景、道具是 entity，
// 设定就是候选详情；其余分类建成空文本资产。
func assetFromCandidate(userID string, candidate *model.ProjectAssetCandidate, now time.Time) (model.Asset, model.AssetVersion, error) {
	assetID, versionID := newID(), newID()
	kind := "text"
	var payload []byte
	var err error
	if candidate.Category == model.AssetCategoryCharacter {
		kind = "entity"
		var characterPayload string
		characterPayload, err = characterAssetPayload(assetID, versionID, candidate.Name, json.RawMessage(candidate.DetailsJSON), now, now)
		payload = []byte(characterPayload)
	} else {
		if projectVisualCategory(candidate.Category) {
			kind = "entity"
		}
		data := map[string]any{"content": ""}
		if kind == "entity" {
			data = map[string]any{"definition": json.RawMessage(candidate.DetailsJSON)}
		}
		payload, err = json.Marshal(map[string]any{
			"id": assetID, "kind": kind, "category": candidate.Category, "status": model.AssetVersionStatusConfirmed,
			"primaryVersionId": versionID, "title": candidate.Name, "coverUrl": "", "tags": []string{}, "data": data,
			"createdAt": now.Format(time.RFC3339Nano), "updatedAt": now.Format(time.RFC3339Nano),
		})
	}
	if err != nil {
		return model.Asset{}, model.AssetVersion{}, err
	}
	asset := model.Asset{ID: assetID, UserID: userID, Kind: kind, Category: candidate.Category, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: versionID, Title: candidate.Name, PayloadJSON: string(payload), CreatedAt: now, UpdatedAt: now}
	version := model.AssetVersion{ID: versionID, AssetID: assetID, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: candidate.DetailsJSON, CreatedAt: now, UpdatedAt: now}
	return asset, version, nil
}

func mergeCharacterCandidateDefinition(currentJSON string, candidateJSON string, currentName string, candidateName string) (string, error) {
	current := map[string]any{}
	candidate := map[string]any{}
	if err := json.Unmarshal([]byte(currentJSON), &current); err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(candidateJSON), &candidate); err != nil {
		return "", BadAuthRequest("角色候选设定格式无效")
	}
	for key, value := range candidate {
		if key == "aliases" || !emptyCharacterDefinitionValue(current[key]) {
			continue
		}
		current[key] = value
	}
	mergeAssetAliases(current, currentName, current["aliases"], candidate["aliases"], candidateName)
	encoded, err := json.Marshal(current)
	return string(encoded), err
}

// mergeAssetAliases 把别名并进设定的 aliases：按给的先后（已有的在前），去掉空的、重复的（不分大小写）和与资产名相同的。
// values 每一项是别名数组（[]any、[]string）或单个别名。
func mergeAssetAliases(definition map[string]any, assetName string, values ...any) {
	aliases := make([]string, 0)
	seen := map[string]bool{}
	for _, value := range values {
		for _, text := range aliasStrings(value) {
			text = strings.TrimSpace(text)
			key := strings.ToLower(text)
			if text == "" || seen[key] || strings.EqualFold(text, strings.TrimSpace(assetName)) {
				continue
			}
			seen[key] = true
			aliases = append(aliases, text)
		}
	}
	definition["aliases"] = aliases
}

// aliasStrings 把别名字段读成字符串列表：[]any 里的字符串、[]string，或单个字符串。
func aliasStrings(value any) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func emptyCharacterDefinitionValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	default:
		return false
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validAssetCategory(category model.AssetCategory) bool {
	switch category {
	case model.AssetCategoryCharacter, model.AssetCategoryEnvironment, model.AssetCategoryProp, model.AssetCategoryMaterial, model.AssetCategoryOther, model.AssetCategoryPalette:
		return true
	default:
		return false
	}
}

func (s *Service) projectAssetSummary(userID string, projectID string, asset *model.Asset) (ProjectAssetSummary, error) {
	versions, err := s.repo.AssetVersions(asset.ID)
	if err != nil {
		return ProjectAssetSummary{}, err
	}
	usages, err := s.repo.ProjectAssetUsageRoles(projectID, asset.ID)
	if err != nil {
		return ProjectAssetSummary{}, err
	}
	link, err := s.repo.ProjectAssetLink(projectID, asset.ID)
	if err != nil {
		return ProjectAssetSummary{}, err
	}
	storageKey, previewText, source, durationMs := projectAssetPreview(asset.PayloadJSON)
	summary := ProjectAssetSummary{ID: asset.ID, Title: asset.Title, MediaType: asset.Kind, Category: asset.Category, Status: asset.Status, PrimaryVersionID: asset.PrimaryVersionID, VersionCount: len(versions), Usages: usages, FolderID: link.FolderID, Position: link.Position, StorageKey: storageKey, PreviewText: previewText, DurationMs: durationMs, UpdatedAt: asset.UpdatedAt, Source: source}
	if asset.Category == model.AssetCategoryCharacter && asset.PrimaryVersionID != "" {
		card, cardErr := s.characterCard(userID, asset)
		if cardErr != nil {
			return ProjectAssetSummary{}, cardErr
		}
		summary.Character = &card
	}
	if asset.Category == model.AssetCategoryPalette {
		summary.Palette = s.paletteAssetSummary(userID, asset, versions)
	}
	if asset.Category == model.AssetCategoryCharacter && summary.Character != nil {
		summary.Variant = s.assetVariantSummary(userID, asset, summary.Character.Definition)
	}
	if asset.Category == model.AssetCategoryCharacter || projectVisualCategory(asset.Category) {
		imaged := asset.Kind == "image"
		if summary.Character != nil {
			imaged = summary.Character.VisualStatus == "ready"
		}
		summary.Image = s.assetImageSummary(userID, asset, imaged)
	}
	if projectVisualCategory(asset.Category) && asset.PrimaryVersionID != "" {
		for _, version := range versions {
			if version.ID != asset.PrimaryVersionID {
				continue
			}
			var definition map[string]any
			if err := json.Unmarshal([]byte(version.DefinitionJSON), &definition); err != nil {
				return ProjectAssetSummary{}, err
			}
			// 普通上传图片仍走媒体卡；有提取设定的场景、道具提供出图流程。
			if asset.Kind == "entity" || definition["prompt"] != nil {
				status := "missing"
				if asset.Kind == "image" && storageKey != "" {
					status = "ready"
				}
				summary.Visual = &ProjectAssetVisualSummary{Definition: definition, Status: status}
			}
			break
		}
	}
	return summary, nil
}

func projectAssetPreview(payloadJSON string) (string, string, string, int64) {
	var payload struct {
		Data struct {
			StorageKey string `json:"storageKey"`
			Content    string `json:"content"`
			Source     string `json:"source"`
			DurationMs int64  `json:"durationMs"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(payloadJSON), &payload) != nil {
		return "", "", "", 0
	}
	previewRunes := []rune(strings.TrimSpace(payload.Data.Content))
	if len(previewRunes) > 240 {
		previewRunes = append(previewRunes[:240], '…')
	}
	return strings.TrimSpace(payload.Data.StorageKey), string(previewRunes), strings.TrimSpace(payload.Data.Source), payload.Data.DurationMs

}
