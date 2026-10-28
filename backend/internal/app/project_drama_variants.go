// 角色子卡（M8 卡 D）：同一角色的不同服装（costume，护士服、西服）或状态（state，受伤、淋湿）各是一张独立的角色资产
// （标题「主卡名·变体名」，assets.parent_asset_id 指向主卡、variant_label 记变体名），设定从主卡继承脸型、体型、发型等不变的部分，
// 只换服装 / 状态；payload.metadata.assetVariant 记变体种类和用这套形象的场（项目场编号 E02S01）。资产设计提取时按角色行的
// variants 自动建（同主卡同变体名的只补场序，不重建），资产页可以手动新增、改、删（被镜头引用时不能删）。子卡出图带主卡已确认的
// 三视图作参考（保证脸一致），装段时这一场的镜头绑子卡（project_drama_scene_cast.go）。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// assetVariantKey 是子卡在 payload.metadata 里的键。
const assetVariantKey = "assetVariant"

// assetVariantMeta 是子卡 payload.metadata.assetVariant：变体种类、用这套形象的场编号、怎么来的（extract / manual），
// 和生成设定时用的服装 / 状态描述、英文提示词原文（改子卡时按它们判断外观变没变；早期建的子卡没有，从设定里拆）。
type assetVariantMeta struct {
	Kind     string   `json:"kind"`
	Scenes   []string `json:"scenes"`
	Source   string   `json:"source,omitempty"`
	Look     string   `json:"look,omitempty"`
	PromptEn string   `json:"promptEn,omitempty"`
}

func assetVariantMetaOf(asset *model.Asset) assetVariantMeta {
	meta, _ := decodeInto[assetVariantMeta](payloadMetadata(assetPayload(asset))[assetVariantKey])
	if meta.Scenes == nil {
		meta.Scenes = []string{}
	}
	return meta
}

// ProjectAssetVariantSummary 是子卡在资产清单里的摘要：主卡、变体名、种类（costume / state）、用这套形象的场，
// 和服装 / 状态描述、用户写的英文提示词（编辑对话框带出原值；自动生成的提示词是空）。
type ProjectAssetVariantSummary struct {
	ParentID    string   `json:"parentId"`
	ParentTitle string   `json:"parentTitle,omitempty"`
	Label       string   `json:"label"`
	Kind        string   `json:"kind"`
	Scenes      []string `json:"scenes"`
	Look        string   `json:"look"`
	PromptEn    string   `json:"promptEn"`
}

// assetVariantSummary 是子卡的摘要（definition 是主版本的设定）；不是子卡返回 nil。
func (s *Service) assetVariantSummary(userID string, asset *model.Asset, definition map[string]any) *ProjectAssetVariantSummary {
	if asset.ParentAssetID == "" {
		return nil
	}
	meta := assetVariantMetaOf(asset)
	summary := &ProjectAssetVariantSummary{ParentID: asset.ParentAssetID, Label: asset.VariantLabel, Kind: drama.NormalizeAssetVariantKind(meta.Kind, asset.VariantLabel), Scenes: meta.Scenes}
	summary.Look, summary.PromptEn = variantLookOf(meta, definition)
	if parent, err := s.repo.AssetForUser(userID, asset.ParentAssetID); err == nil {
		summary.ParentTitle = parent.Title
	}
	return summary
}

// attachProjectAssetVariants 给资产页这一页的主卡挂上它们的子卡（子卡不单独占格，见 ProjectAssetsPage）。
func (s *Service) attachProjectAssetVariants(userID string, projectID string, summaries []ProjectAssetSummary) error {
	parents := map[string]int{}
	for i, a := range summaries {
		if a.Category == model.AssetCategoryCharacter && a.Variant == nil {
			parents[a.ID] = i
		}
	}
	if len(parents) == 0 {
		return nil
	}
	assets, err := s.repo.ProjectAssets(userID, projectID)
	if err != nil {
		return err
	}
	slices.SortStableFunc(assets, func(a, b model.Asset) int { return a.CreatedAt.Compare(b.CreatedAt) })
	for i := range assets {
		parent, ok := parents[assets[i].ParentAssetID]
		if !ok {
			continue
		}
		child, err := s.projectAssetSummary(userID, projectID, &assets[i])
		if err != nil {
			return err
		}
		summaries[parent].Variants = append(summaries[parent].Variants, child)
	}
	return nil
}

// variantSceneCodes 把本单元的场序换成项目场编号（不在导演风格编号里的单元记「第 N 场」）。
func variantSceneCodes(scenes []int, episode int) []string {
	out := []string{}
	for _, n := range scenes {
		code := fmt.Sprintf("第 %d 场", n)
		if episode > 0 {
			code = drama.SceneCode(episode, n)
		}
		if !slices.Contains(out, code) {
			out = append(out, code)
		}
	}
	return out
}

// variantIdentityKeys 是子卡从主卡继承的设定字段：身份、外貌（脸型、五官、发型）、体型、性格、道具；服装和状态由子卡自己写。
var variantIdentityKeys = []string{"role", "appearance", "physique", "personality", "props", "age", "gender"}

// variantDefinition 是子卡的设定：继承主卡的不变部分，clothing 写这套服装 / 状态，consistencyPrompt 写明与主卡同一人，
// multiViewPrompt 是这一套的三视图提示词（没写时用主卡的接上变体说明）。
func variantDefinition(parent map[string]any, parentTitle string, label string, kind string, look string, promptEn string) map[string]any {
	out := map[string]any{"aliases": []string{}}
	for _, key := range variantIdentityKeys {
		if value, ok := parent[key]; ok && !emptyCharacterDefinitionValue(value) {
			out[key] = value
		}
	}
	parentClothing, _ := parent["clothing"].(string)
	look = strings.TrimSpace(look)
	if kind == drama.AssetVariantState {
		clothing := "状态「" + label + "」：" + look
		if c := strings.TrimSpace(parentClothing); c != "" {
			clothing += "；服装同主卡：" + c
		}
		out["clothing"] = clothing
	} else {
		out["clothing"] = "服装「" + label + "」：" + look
	}
	consistency := fmt.Sprintf("与主卡「%s」是同一人：脸型、五官、发型、肤色、体型与主卡三视图一致，只换成这一套%s。", parentTitle, drama.AssetVariantKindName(kind))
	if c, _ := parent["consistencyPrompt"].(string); strings.TrimSpace(c) != "" {
		consistency += strings.TrimSpace(c)
	}
	out["consistencyPrompt"] = consistency
	prompt := strings.TrimSpace(promptEn)
	if prompt == "" {
		base, _ := parent["multiViewPrompt"].(string)
		prompt = strings.TrimSpace(strings.TrimSpace(base) + variantDefaultPromptMarker + map[string]string{drama.AssetVariantState: "condition", drama.AssetVariantCostume: "outfit"}[kind] + " changes: " + label + ".")
	}
	out["multiViewPrompt"] = prompt
	return out
}

// variantDefaultPromptMarker 是子卡没写英文提示词时、用主卡提示词接上的那段说明的开头（认出提示词是不是自动生成的）。
const variantDefaultPromptMarker = " Same character, same face and body; only the "

var variantLookPrefix = regexp.MustCompile(`^(服装|状态)「[^」]*」：`)

// variantLookOf 是子卡当前的服装 / 状态描述和用户写的英文提示词（自动生成的提示词算空）：metadata 里记了原文就用原文，
// 否则从设定的 clothing 里去掉「服装「X」：」前缀和「；服装同主卡：…」后缀。
func variantLookOf(meta assetVariantMeta, definition map[string]any) (string, string) {
	if meta.Look != "" {
		return meta.Look, meta.PromptEn
	}
	clothing, _ := definition["clothing"].(string)
	look := variantLookPrefix.ReplaceAllString(strings.TrimSpace(clothing), "")
	if i := strings.Index(look, "；服装同主卡："); i >= 0 {
		look = look[:i]
	}
	prompt, _ := definition["multiViewPrompt"].(string)
	if strings.Contains(prompt, variantDefaultPromptMarker) {
		prompt = ""
	}
	return strings.TrimSpace(look), strings.TrimSpace(prompt)
}

// variantPayload 写子卡 payload 的 metadata：变体种类和场、出图排队状态（imageState 为空时不写）。
func variantPayload(asset *model.Asset, meta assetVariantMeta, imageState string, now time.Time) error {
	payload := assetPayload(asset)
	metadata := payloadMetadata(payload)
	metadata[assetVariantKey] = meta
	if imageState != "" {
		metadata[assetImageKey] = dramaImageState{Status: imageState, UpdatedAt: now.Format(time.RFC3339Nano)}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	asset.PayloadJSON = string(raw)
	return nil
}

// newVariantAsset 组一张子卡（不落库）：资产、第 1 个版本。
func newVariantAsset(userID string, parent model.Asset, parentDefinition map[string]any, label string, kind string, look string, promptEn string, meta assetVariantMeta, imageState string, now time.Time) (model.Asset, model.AssetVersion, error) {
	definition := variantDefinition(parentDefinition, parent.Title, label, kind, look, promptEn)
	title := drama.AssetVariantTitle(parent.Title, label)
	definition["name"] = title
	encoded, err := json.Marshal(definition)
	if err != nil {
		return model.Asset{}, model.AssetVersion{}, err
	}
	candidate := model.ProjectAssetCandidate{Name: title, Category: model.AssetCategoryCharacter, DetailsJSON: string(encoded)}
	asset, version, err := assetFromCandidate(userID, &candidate, now)
	if err != nil {
		return model.Asset{}, model.AssetVersion{}, err
	}
	asset.ParentAssetID, asset.VariantLabel = parent.ID, label
	meta.Kind, meta.Look, meta.PromptEn = kind, strings.TrimSpace(look), strings.TrimSpace(promptEn)
	if err := variantPayload(&asset, meta, imageState, now); err != nil {
		return model.Asset{}, model.AssetVersion{}, err
	}
	version.Note = "角色子卡"
	return asset, version, nil
}

// mergeVariantScenes 把场并进子卡的场（去重，按先后）；返回有没有加。
func mergeVariantScenes(meta *assetVariantMeta, codes []string) bool {
	added := false
	for _, code := range codes {
		if !slices.Contains(meta.Scenes, code) {
			meta.Scenes = append(meta.Scenes, code)
			added = true
		}
	}
	return added
}

// ---------------------------------------------------------------- 资产页：新增、改、删子卡

// DramaAssetVariantRequest 是资产页新增 / 改子卡：变体名、种类（costume / state）、这套服装或状态的描述、英文三视图提示词（可空，
// 空时用主卡的接上变体说明）、用这套形象的场编号；ImageTask 给了时建完（或改了外观后）接着出三视图（带主卡三视图作参考）。
type DramaAssetVariantRequest struct {
	Label     string                  `json:"label"`
	Kind      string                  `json:"kind"`
	Look      string                  `json:"look"`
	PromptEn  string                  `json:"promptEn,omitempty"`
	Scenes    []string                `json:"scenes,omitempty"`
	ImageTask *DramaImageTaskTemplate `json:"imageTask,omitempty"`
}

// DramaAssetVariantResult 是新增 / 改子卡的结果：子卡和出图排队。
type DramaAssetVariantResult struct {
	Asset  ProjectAssetSummary   `json:"asset"`
	Images DramaImageQueueResult `json:"images"`
}

const (
	variantLabelMaxChars = 24
	variantLookMaxChars  = 600
)

func normalizeVariantRequest(req *DramaAssetVariantRequest) error {
	req.Label = strings.TrimSpace(req.Label)
	req.Look = strings.TrimSpace(req.Look)
	req.PromptEn = strings.TrimSpace(req.PromptEn)
	if req.Label == "" {
		return BadAuthRequest("请写子卡的变体名（如 西服、淋湿）")
	}
	if len([]rune(req.Label)) > variantLabelMaxChars || strings.ContainsAny(req.Label, "·・•") {
		return BadAuthRequest(fmt.Sprintf("变体名最多 %d 个字，不要带「·」", variantLabelMaxChars))
	}
	if req.Look == "" {
		return BadAuthRequest("请写这套服装或状态的描述")
	}
	if len([]rune(req.Look)) > variantLookMaxChars {
		return BadAuthRequest(fmt.Sprintf("服装或状态描述最多 %d 个字", variantLookMaxChars))
	}
	req.Kind = drama.NormalizeAssetVariantKind(req.Kind, req.Label)
	scenes := []string{}
	for _, code := range req.Scenes {
		code = strings.ToUpper(strings.TrimSpace(code))
		if _, _, ok := drama.ParseSceneCode(code); !ok {
			return BadAuthRequest(fmt.Sprintf("场编号「%s」不对，要写 E02S01 这样的", code))
		}
		if !slices.Contains(scenes, code) {
			scenes = append(scenes, code)
		}
	}
	req.Scenes = scenes
	return nil
}

// projectVariantParent 读项目里的主卡：要是角色、不是子卡、有主版本。
func (s *Service) projectVariantParent(userID string, projectID string, parentID string) (*model.Asset, map[string]any, error) {
	parent, err := s.repo.ProjectCharacterAsset(userID, projectID, strings.TrimSpace(parentID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, NotFound("角色不在这个项目里")
	}
	if err != nil {
		return nil, nil, err
	}
	if parent.ParentAssetID != "" {
		return nil, nil, BadAuthRequest("子卡下面不能再建子卡，请在主卡上新增")
	}
	version, err := s.repo.AssetVersion(parent.PrimaryVersionID)
	if err != nil {
		return nil, nil, err
	}
	definition := map[string]any{}
	_ = json.Unmarshal([]byte(firstNonEmpty(version.DefinitionJSON, "{}")), &definition)
	return parent, definition, nil
}

// projectVariants 是项目里这张主卡的子卡。
func (s *Service) projectVariants(userID string, projectID string, parentID string) ([]model.Asset, error) {
	assets, err := s.repo.ProjectAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(assets, func(a model.Asset) bool { return a.ParentAssetID != parentID }), nil
}

// CreateDramaAssetVariant 是资产页「新增子卡」：按主卡当前设定继承生成子卡定义、建资产（直接已确认），带了图片模板时接着出三视图。
func (s *Service) CreateDramaAssetVariant(userID string, projectID string, parentID string, req DramaAssetVariantRequest) (DramaAssetVariantResult, error) {
	result := DramaAssetVariantResult{Images: emptyDramaImageQueueResult()}
	if err := normalizeVariantRequest(&req); err != nil {
		return result, err
	}
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return result, err
	}
	parent, definition, err := s.projectVariantParent(userID, project.ID, parentID)
	if err != nil {
		return result, err
	}
	siblings, err := s.projectVariants(userID, project.ID, parent.ID)
	if err != nil {
		return result, err
	}
	if slices.ContainsFunc(siblings, func(a model.Asset) bool {
		return drama.AssetVariantKey(a.VariantLabel) == drama.AssetVariantKey(req.Label)
	}) {
		return result, BadAuthRequest(fmt.Sprintf("「%s」已经有子卡「%s」了，直接改它", parent.Title, req.Label))
	}
	folderID, err := s.resolveProjectAssetFolderID(project.ID, nil)
	if err != nil {
		return result, err
	}
	position, err := s.repo.NextProjectAssetPosition(project.ID, folderID)
	if err != nil {
		return result, err
	}
	now := time.Now()
	asset, version, err := newVariantAsset(userID, *parent, definition, req.Label, req.Kind, req.Look, req.PromptEn, assetVariantMeta{Scenes: req.Scenes, Source: "manual"}, dramaImageNone, now)
	if err != nil {
		return result, err
	}
	link := model.ProjectAssetLink{ID: newID(), ProjectID: project.ID, AssetID: asset.ID, FolderID: folderID, Position: position, CreatedAt: now}
	if err := s.repo.CreateProjectAssetVariant(project.ID, &asset, &version, &link, now); err != nil {
		return result, err
	}
	if req.ImageTask.usable() {
		if result.Images, err = s.queueDramaAssetImages(userID, project.ID, *req.ImageTask, "variant", []string{asset.ID}); err != nil {
			return result, err
		}
	}
	current, err := s.repo.AssetForUser(userID, asset.ID)
	if err != nil {
		return result, err
	}
	result.Asset, err = s.projectAssetSummary(userID, project.ID, current)
	return result, err
}

// UpdateDramaAssetVariant 是资产页改子卡：只改变体名、种类、场时原地改（设定里的变体名跟着换，不加版本，三视图和镜头产物都不动）；
// 服装 / 状态描述或英文提示词改了时按主卡当前设定重新生成定义、加一个版本（旧三视图不再搬过去，引用这张子卡的镜头产物过期），
// 带了图片模板时接着重出三视图。比较外观只看描述和提示词原文，不看带着变体名的整段设定。
func (s *Service) UpdateDramaAssetVariant(userID string, projectID string, variantID string, req DramaAssetVariantRequest) (DramaAssetVariantResult, error) {
	result := DramaAssetVariantResult{Images: emptyDramaImageQueueResult()}
	if err := normalizeVariantRequest(&req); err != nil {
		return result, err
	}
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return result, err
	}
	child, err := s.projectVariantAsset(userID, project.ID, variantID)
	if err != nil {
		return result, err
	}
	parent, definition, err := s.projectVariantParent(userID, project.ID, child.ParentAssetID)
	if err != nil {
		return result, err
	}
	siblings, err := s.projectVariants(userID, project.ID, parent.ID)
	if err != nil {
		return result, err
	}
	if slices.ContainsFunc(siblings, func(a model.Asset) bool {
		return a.ID != child.ID && drama.AssetVariantKey(a.VariantLabel) == drama.AssetVariantKey(req.Label)
	}) {
		return result, BadAuthRequest(fmt.Sprintf("「%s」已经有子卡「%s」了", parent.Title, req.Label))
	}
	version, err := s.repo.AssetVersion(child.PrimaryVersionID)
	if err != nil {
		return result, err
	}
	current := map[string]any{}
	_ = json.Unmarshal([]byte(firstNonEmpty(version.DefinitionJSON, "{}")), &current)
	meta := assetVariantMetaOf(child)
	now := time.Now()
	title := drama.AssetVariantTitle(parent.Title, req.Label)
	next := variantDefinition(definition, parent.Title, req.Label, req.Kind, req.Look, req.PromptEn)
	next["name"] = title
	oldLook, oldPrompt := variantLookOf(meta, current)
	lookChanged := req.Look != oldLook || req.PromptEn != oldPrompt
	encoded, err := json.Marshal(next)
	if err != nil {
		return result, err
	}
	if lookChanged {
		// 外观改了：旧三视图对不上新设定，不搬到新版本。
		if _, err := s.createNextCharacterVersion(project.ID, child, title, string(encoded), []model.AssetRepresentation{}, nil, false); err != nil {
			return result, err
		}
	} else if err := s.repo.UpdateAssetVersionDefinition(child.ID, child.PrimaryVersionID, string(encoded), now); err != nil {
		// 只改了变体名、种类或场：原地换设定里的文字，版本、三视图和引用它的镜头都不动。
		return result, err
	}
	meta.Kind, meta.Scenes, meta.Look, meta.PromptEn = req.Kind, req.Scenes, req.Look, req.PromptEn
	if err := s.setDramaImageState(userID, child.ID, func(payload map[string]any) {
		metadata := payloadMetadata(payload)
		metadata[assetVariantKey] = meta
		if lookChanged {
			metadata[assetImageKey] = dramaImageState{Status: dramaImageNone, UpdatedAt: now.Format(time.RFC3339Nano)}
		}
		if data, ok := payload["data"].(map[string]any); ok {
			data["definition"] = next
		}
		payload["title"] = title
	}); err != nil {
		return result, err
	}
	if err := s.repo.UpdateAssetVariantLabel(userID, child.ID, title, req.Label, now); err != nil {
		return result, err
	}
	if err := s.repo.BumpProjectRevision(project.ID); err != nil {
		return result, err
	}
	if lookChanged && req.ImageTask.usable() {
		if result.Images, err = s.queueDramaAssetImages(userID, project.ID, *req.ImageTask, "variant", []string{child.ID}); err != nil {
			return result, err
		}
	}
	updated, err := s.repo.AssetForUser(userID, child.ID)
	if err != nil {
		return result, err
	}
	result.Asset, err = s.projectAssetSummary(userID, project.ID, updated)
	return result, err
}

// DeleteDramaAssetVariant 是资产页删子卡：被这个项目的镜头引用时拒绝并列出镜头；否则移出项目（资产留在个人素材库）。
func (s *Service) DeleteDramaAssetVariant(userID string, projectID string, variantID string) error {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return err
	}
	child, err := s.projectVariantAsset(userID, project.ID, variantID)
	if err != nil {
		return err
	}
	if err := s.variantShotReferencesConflict(project.ID, []model.Asset{*child}); err != nil {
		return err
	}
	return s.UnlinkProjectAsset(userID, project.ID, child.ID)
}

// variantShotReferencesConflict 是这几张角色卡（主卡和它的子卡，或单张子卡）还被这个项目的镜头引用时的 409：列出是哪张卡、哪些镜头。
func (s *Service) variantShotReferencesConflict(projectID string, assets []model.Asset) error {
	var parts []string
	for _, a := range assets {
		titles, err := s.repo.ProjectAssetShotReferenceTitles(projectID, a.ID)
		if err != nil {
			return err
		}
		if len(titles) == 0 {
			continue
		}
		shown := titles[:min(8, len(titles))]
		more := ""
		if len(titles) > len(shown) {
			more = fmt.Sprintf(" 等 %d 个镜头", len(titles))
		}
		parts = append(parts, fmt.Sprintf("「%s」：%s%s", a.Title, strings.Join(shown, "、"), more))
	}
	if len(parts) == 0 {
		return nil
	}
	kind := "子卡"
	if len(assets) > 1 {
		kind = "角色和它的子卡"
	}
	return NewAppError(http.StatusConflict, fmt.Sprintf("%s还被镜头引用，没有移出：%s。先在分镜制作里换掉，或重新生成分段", kind, strings.Join(parts, "；")))
}

// projectVariantAsset 读项目里的子卡。
func (s *Service) projectVariantAsset(userID string, projectID string, variantID string) (*model.Asset, error) {
	child, err := s.repo.ProjectCharacterAsset(userID, projectID, strings.TrimSpace(variantID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("子卡不在这个项目里")
	}
	if err != nil {
		return nil, err
	}
	if child.ParentAssetID == "" {
		return nil, BadAuthRequest("这张角色卡不是子卡")
	}
	return child, nil
}
