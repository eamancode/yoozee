// 色卡资产（M7.2 第 4 阶段）：导演风格收尾按色彩脚本每场建一张（分类 palette，直接已确认，不走候选），同一场重跑加版本。
// 出图走已有的资产出图任务（operation=project_asset_image，收尾由 finalizeProjectAssetImageTask 绑到资产版本），任务由服务端直接建，
// 英文提示词由 drama.PalettePrompt 按《色卡提示词卡》模板拼。同时排队的任务数有上限，排不上的色卡记成「等空位」，
// 前面的色卡出完图（任务成功落库之后）自动接着排；出图绑上后按色块取色、算 ΔE2000，写进版本主图的 metadata.paletteCheck
// 和资产 payload（只记警告，不删图）。
// 资产 payload 里色卡自己的字段：metadata.palette（场编号、集号、场序；stale=true 是场次已不存在）、metadata.paletteImage（出图排队状态）、
// metadata.paletteCheck（最近一次取色结果）。
// 场次结构变了（章节改编成剧集、集数变了）重跑导演风格后，运行范围内色彩脚本里不再有的场，色卡标成场次已不存在（不删，用户自己移出），
// 不再出图，也不计入「色卡齐了」；之后哪次又写到这一场，加版本时恢复。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 色卡出图的排队状态（payload.metadata.paletteImage.status）。
const (
	paletteImageNone    = "none"
	paletteImageWaiting = "waiting"
	paletteImageQueued  = "queued"
	paletteImageBound   = "bound"
	paletteImageStale   = "stale"
	paletteImageFailed  = "failed"
)

// paletteImageTaskSource 是色卡出图任务的 metadata.source。
const paletteImageTaskSource = "short-drama-palette"

// paletteQueueMu 让同一进程里的排队互斥（多个色卡同时出完图时只有一个在挑下一批）；跨进程靠 payload 条件更新。
var paletteQueueMu sync.Mutex

// DramaPaletteQueueResult 是一次排色卡出图：Queued 是建了任务的色卡，Waiting 是等空位的，Failed 是没建成的（原因记在色卡上）。
type DramaPaletteQueueResult struct {
	Queued  []string `json:"queued"`
	Waiting []string `json:"waiting"`
	Failed  []string `json:"failed"`
}

// DramaPaletteImagesRequest 是「给色卡出图 / 重新出图」：AssetIDs 为空时排全部还没出图或出图失败的色卡。
type DramaPaletteImagesRequest struct {
	AssetIDs  []string                `json:"assetIds,omitempty"`
	ImageTask *DramaImageTaskTemplate `json:"imageTask"`
}

// ProjectAssetPaletteSummary 是色卡资产在资产清单里的摘要（资产页色卡卡片用）。
type ProjectAssetPaletteSummary struct {
	Code  string                 `json:"code"`
	Entry drama.ColorScriptEntry `json:"entry"`
	// Design 是《色卡提示词卡》【色板设计】的结构句；Prompt 是出图用的英文提示词。
	Design string `json:"design"`
	Prompt string `json:"prompt"`
	// SceneGone 是这一场在当前剧集里已不存在（重跑导演风格时标的），色卡保留但不再出图、不计入「色卡齐了」。
	SceneGone bool `json:"sceneGone,omitempty"`
	// ImageStatus 是 none / waiting / queued / bound / stale / failed；TaskID、ImageError 是最近一次出图任务。
	ImageStatus string              `json:"imageStatus"`
	TaskID      string              `json:"taskId,omitempty"`
	ImageError  string              `json:"imageError,omitempty"`
	Check       *drama.PaletteCheck `json:"check,omitempty"`
}

// paletteMeta 是色卡 payload.metadata 里的场编号与出图状态。
type paletteMeta struct {
	Code    string `json:"code"`
	Episode int    `json:"episode"`
	SceneNo int    `json:"sceneNo"`
	// Stale 是场次已不存在：StaleAt 是标上的时间。
	Stale   bool   `json:"stale,omitempty"`
	StaleAt string `json:"staleAt,omitempty"`
}

type paletteImageState struct {
	Status    string `json:"status"`
	TaskID    string `json:"taskId,omitempty"`
	Error     string `json:"error,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// paletteDefinition 是色卡版本的设定（asset_versions.definition_json）。
type paletteDefinition struct {
	Code    string                 `json:"code"`
	Palette drama.ColorScriptEntry `json:"palette"`
	Design  string                 `json:"design"`
	Prompt  string                 `json:"prompt"`
}

func paletteDefinitionOf(project *model.Project, e drama.ColorScriptEntry) paletteDefinition {
	return paletteDefinition{Code: e.Code(), Palette: e, Design: drama.PaletteDesign(e), Prompt: drama.PalettePrompt(project.Name, e)}
}

func assetPayload(asset *model.Asset) map[string]any {
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(asset.PayloadJSON), &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	return payload
}

func payloadMetadata(payload map[string]any) map[string]any {
	metadata, _ := payload["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		payload["metadata"] = metadata
	}
	return metadata
}

func decodeInto[T any](value any) (T, bool) {
	var out T
	encoded, err := json.Marshal(value)
	if err != nil || value == nil {
		return out, false
	}
	return out, json.Unmarshal(encoded, &out) == nil
}

// paletteMetaOf 是色卡资产的场编号与场次状态；不是色卡时返回零值。
func paletteMetaOf(asset *model.Asset) paletteMeta {
	if asset.Category != model.AssetCategoryPalette {
		return paletteMeta{}
	}
	meta, _ := decodeInto[paletteMeta](payloadMetadata(assetPayload(asset))["palette"])
	return meta
}

// paletteCodeOf 是色卡资产的场编号；不是色卡或没有场编号返回空串。
func paletteCodeOf(asset *model.Asset) string { return paletteMetaOf(asset).Code }

// projectPaletteAssets 是项目里的色卡资产，按场编号。
func (s *Service) projectPaletteAssets(userID string, projectID string) (map[string]model.Asset, error) {
	assets, err := s.repo.ProjectAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	out := map[string]model.Asset{}
	for _, a := range assets {
		if code := paletteCodeOf(&a); code != "" {
			if prev, ok := out[code]; !ok || a.CreatedAt.Before(prev.CreatedAt) {
				out[code] = a
			}
		}
	}
	return out, nil
}

// dramaPaletteWrites 为这次写的每一场准备色卡：已有同一场（按场编号找）的色卡加一个版本并换成主版本（没有图，出图时绑到这一版），
// 没有的新建资产、版本 1 和项目关联。要出图（images）时出图状态记成「等空位」，写库后按空位排；否则记成 none。
// 这次没写到的已有色卡里，retire 认定在运行范围内的（色彩脚本里已经没有这一场），标成场次已不存在（只改 payload，不加版本、不删）。
func (s *Service) dramaPaletteWrites(userID string, project *model.Project, entries []drama.ColorScriptEntry, images bool, retire func(paletteMeta) bool) ([]repository.DramaPaletteWrite, DramaPaletteWriteSummary, error) {
	initial := paletteImageNone
	if images {
		initial = paletteImageWaiting
	}
	var summary DramaPaletteWriteSummary
	existing, err := s.projectPaletteAssets(userID, project.ID)
	if err != nil {
		return nil, summary, err
	}
	folderID, err := s.resolveProjectAssetFolderID(project.ID, nil)
	if err != nil {
		return nil, summary, err
	}
	position, err := s.repo.NextProjectAssetPosition(project.ID, folderID)
	if err != nil {
		return nil, summary, err
	}
	now := time.Now()
	writes := make([]repository.DramaPaletteWrite, 0, len(entries))
	for _, e := range entries {
		definition := paletteDefinitionOf(project, e)
		encoded, err := json.Marshal(definition)
		if err != nil {
			return nil, summary, err
		}
		title := drama.PaletteTitle(e)
		versionID := newID()
		version := model.AssetVersion{ID: versionID, AssetID: "", Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: string(encoded), Prompt: definition.Prompt, Note: "导演风格", CreatedAt: now, UpdatedAt: now}
		if current, ok := existing[e.Code()]; ok {
			versions, err := s.repo.AssetVersions(current.ID)
			if err != nil {
				return nil, summary, err
			}
			if len(versions) > 0 {
				version.Version = versions[0].Version + 1
			}
			version.AssetID = current.ID
			payload := assetPayload(&current)
			metadata := payloadMetadata(payload)
			delete(metadata, "paletteCheck")
			metadata["palette"] = paletteMeta{Code: e.Code(), Episode: e.Episode, SceneNo: e.SceneNo}
			metadata["paletteImage"] = paletteImageState{Status: initial, UpdatedAt: now.Format(time.RFC3339Nano)}
			payload["kind"], payload["status"], payload["primaryVersionId"], payload["title"] = "entity", model.AssetVersionStatusConfirmed, versionID, title
			payload["coverUrl"], payload["updatedAt"] = "", now.Format(time.RFC3339Nano)
			payload["data"] = map[string]any{"definition": definition}
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, summary, err
			}
			asset := current
			asset.Kind, asset.Title, asset.Status, asset.PrimaryVersionID, asset.PayloadJSON, asset.UpdatedAt = "entity", title, model.AssetVersionStatusConfirmed, versionID, string(raw), now
			writes = append(writes, repository.DramaPaletteWrite{Asset: &asset, Version: &version})
			summary.Versioned++
			continue
		}
		assetID := newID()
		version.AssetID = assetID
		payload := map[string]any{
			"id": assetID, "kind": "entity", "category": model.AssetCategoryPalette, "status": model.AssetVersionStatusConfirmed,
			"primaryVersionId": versionID, "title": title, "coverUrl": "", "tags": []string{"色卡"},
			"data":      map[string]any{"definition": definition},
			"metadata":  map[string]any{"palette": paletteMeta{Code: e.Code(), Episode: e.Episode, SceneNo: e.SceneNo}, "paletteImage": paletteImageState{Status: initial, UpdatedAt: now.Format(time.RFC3339Nano)}},
			"createdAt": now.Format(time.RFC3339Nano), "updatedAt": now.Format(time.RFC3339Nano),
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, summary, err
		}
		asset := model.Asset{ID: assetID, UserID: userID, Kind: "entity", Category: model.AssetCategoryPalette, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: versionID, Title: title, PayloadJSON: string(raw), CreatedAt: now, UpdatedAt: now}
		link := model.ProjectAssetLink{ID: newID(), ProjectID: project.ID, AssetID: assetID, FolderID: folderID, Position: position, CreatedAt: now}
		position++
		writes = append(writes, repository.DramaPaletteWrite{Asset: &asset, Version: &version, Link: &link})
		summary.Created++
	}
	written := map[string]bool{}
	for _, e := range entries {
		written[e.Code()] = true
	}
	for _, code := range slices.Sorted(maps.Keys(existing)) {
		current := existing[code]
		meta := paletteMetaOf(&current)
		if written[code] || meta.Stale || retire == nil || !retire(meta) {
			continue
		}
		payload := assetPayload(&current)
		metadata := payloadMetadata(payload)
		meta.Stale, meta.StaleAt = true, now.Format(time.RFC3339Nano)
		metadata["palette"] = meta
		// 等空位的不再排；已排上的任务照常收尾（图绑到原版本）。
		if state, _ := decodeInto[paletteImageState](metadata["paletteImage"]); state.Status == paletteImageWaiting {
			metadata["paletteImage"] = paletteImageState{Status: paletteImageNone, UpdatedAt: now.Format(time.RFC3339Nano)}
		}
		payload["updatedAt"] = now.Format(time.RFC3339Nano)
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, summary, err
		}
		asset := current
		asset.PayloadJSON, asset.UpdatedAt = string(raw), now
		writes = append(writes, repository.DramaPaletteWrite{Asset: &asset})
		summary.Retired++
	}
	return writes, summary, nil
}

// setPaletteImageState 改一张色卡的出图状态（payload 条件更新：别的地方同时改了就重读再改，最多三次）。
func (s *Service) setPaletteImageState(userID string, assetID string, update func(payload map[string]any)) error {
	for attempt := 0; attempt < 3; attempt++ {
		asset, err := s.repo.AssetForUser(userID, assetID)
		if err != nil {
			return err
		}
		payload := assetPayload(asset)
		update(payload)
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		ok, err := s.repo.UpdateAssetPayloadIfUnchanged(userID, assetID, asset.PayloadJSON, string(raw), time.Now())
		if err != nil || ok {
			return err
		}
	}
	return errors.New("色卡同时被别处修改，状态没有写上")
}

func paletteImageStateOf(asset *model.Asset) paletteImageState {
	state, _ := decodeInto[paletteImageState](payloadMetadata(assetPayload(asset))["paletteImage"])
	return state
}

// queueDramaPaletteImages 给色卡排出图任务：only 为空时排全部「等空位」的色卡，否则先把 only 里的色卡改成「等空位」再排。
// 按场编号顺序，空位（同时排队或运行的任务上限减去已有的）用完就停，剩下的保持「等空位」，前面的出完图后接着排。
func (s *Service) queueDramaPaletteImages(userID string, projectID string, template DramaImageTaskTemplate, origin string, only []string) (DramaPaletteQueueResult, error) {
	result := DramaPaletteQueueResult{Queued: []string{}, Waiting: []string{}, Failed: []string{}}
	if !template.usable() {
		return result, BadAuthRequest("色卡出图要先选图片模型")
	}
	paletteQueueMu.Lock()
	defer paletteQueueMu.Unlock()
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return result, err
	}
	palettes, err := s.projectPaletteAssets(userID, projectID)
	if err != nil {
		return result, err
	}
	now := time.Now().Format(time.RFC3339Nano)
	if len(only) > 0 {
		byID := map[string]model.Asset{}
		for _, a := range palettes {
			byID[a.ID] = a
		}
		for _, id := range only {
			asset, ok := byID[strings.TrimSpace(id)]
			if !ok {
				return result, BadAuthRequest("所选资产不是这个项目的色卡")
			}
			if paletteMetaOf(&asset).Stale {
				return result, BadAuthRequest(fmt.Sprintf("「%s」的场次已不存在，不再出图", asset.Title))
			}
			if state := paletteImageStateOf(&asset); state.Status == paletteImageQueued && s.paletteTaskActive(userID, state.TaskID) {
				return result, BadAuthRequest(fmt.Sprintf("「%s」正在出图，等它完成再重出", asset.Title))
			}
			if err := s.setPaletteImageState(userID, asset.ID, func(payload map[string]any) {
				payloadMetadata(payload)["paletteImage"] = paletteImageState{Status: paletteImageWaiting, UpdatedAt: now}
			}); err != nil {
				return result, err
			}
		}
		if palettes, err = s.projectPaletteAssets(userID, projectID); err != nil {
			return result, err
		}
	}
	codes := slices.Sorted(maps.Keys(palettes))
	policy, err := s.RuntimePolicy()
	if err != nil {
		return result, err
	}
	active, err := s.repo.ActiveTaskCountForUser(userID)
	if err != nil {
		return result, err
	}
	slots := int64(policy.Task.ActiveTaskLimit) - active
	for _, code := range codes {
		asset := palettes[code]
		if paletteImageStateOf(&asset).Status != paletteImageWaiting || paletteMetaOf(&asset).Stale {
			continue
		}
		if slots <= 0 {
			result.Waiting = append(result.Waiting, asset.ID)
			continue
		}
		version, err := s.repo.AssetVersion(asset.PrimaryVersionID)
		if err != nil {
			return result, err
		}
		task, createErr := s.CreateTask(userID, dramaPaletteImageTaskRequest(project.ID, asset, version, template, origin))
		if createErr != nil {
			state := paletteImageState{Status: paletteImageFailed, Error: userFacingTaskError(createErr.Error()), UpdatedAt: now}
			// 同时任务数在建任务时才会被别的请求占满：这一张留到下一次接着排。
			if strings.Contains(createErr.Error(), "同时排队或运行的任务最多") {
				state = paletteImageState{Status: paletteImageWaiting, UpdatedAt: now}
				slots = 0
				result.Waiting = append(result.Waiting, asset.ID)
			} else {
				result.Failed = append(result.Failed, asset.ID)
			}
			if err := s.setPaletteImageState(userID, asset.ID, func(payload map[string]any) { payloadMetadata(payload)["paletteImage"] = state }); err != nil {
				return result, err
			}
			continue
		}
		slots--
		result.Queued = append(result.Queued, asset.ID)
		if err := s.setPaletteImageState(userID, asset.ID, func(payload map[string]any) {
			payloadMetadata(payload)["paletteImage"] = paletteImageState{Status: paletteImageQueued, TaskID: task.ID, UpdatedAt: now}
		}); err != nil {
			return result, err
		}
	}
	return result, nil
}

// paletteTaskActive 是这个出图任务还在排队或运行。
func (s *Service) paletteTaskActive(userID string, taskID string) bool {
	if strings.TrimSpace(taskID) == "" {
		return false
	}
	task, err := s.repo.TaskForUser(userID, taskID)
	return err == nil && (task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning)
}

// dramaPaletteImageTaskRequest 是一张色卡的出图任务：模板的模型选择和 input（config 等）照用，换上色卡的英文提示词和资产出图的 metadata
// （与前端场景、道具出图同一形状，收尾由 finalizeProjectAssetImageTask 绑图）；模板本身放进顶层 paletteImageTask，出完图接着排下一张时用。
func dramaPaletteImageTaskRequest(projectID string, asset model.Asset, version *model.AssetVersion, template DramaImageTaskTemplate, origin string) CreateTaskRequest {
	var definition paletteDefinition
	_ = json.Unmarshal([]byte(version.DefinitionJSON), &definition)
	prompt := firstNonEmpty(strings.TrimSpace(version.Prompt), definition.Prompt)
	input := map[string]any{}
	if copied, ok := decodeInto[map[string]any](template.Input); ok && copied != nil {
		input = copied
	}
	for _, key := range []string{"referenceImages", "referenceVideos", "referenceAudios", "mask", "textHistory", "paletteImageTask"} {
		delete(input, key)
	}
	metadata, _ := input["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	}
	maps.Copy(metadata, map[string]any{
		"operation": "project_asset_image", "domainProjectId": projectID, "projectAssetId": asset.ID, "projectAssetVersionId": version.ID,
		"nodeId": "project-asset-image:" + asset.ID, "source": paletteImageTaskSource,
		"dramaPalette": map[string]any{"code": definition.Code, "origin": origin},
	})
	input["mode"], input["prompt"], input["metadata"] = "image", prompt, metadata
	input["paletteImageTask"] = template
	return CreateTaskRequest{
		ProjectID: projectID, Type: "canvas_image", Operation: "image", Prompt: prompt,
		Provider: strings.TrimSpace(template.Provider), Model: strings.TrimSpace(template.Model), LogicalModelID: strings.TrimSpace(template.LogicalModelID), Input: input,
	}
}

// QueueDramaPaletteImages 是资产页「给色卡出图 / 重新出图」。
func (s *Service) QueueDramaPaletteImages(userID string, projectID string, req DramaPaletteImagesRequest) (DramaPaletteQueueResult, error) {
	if req.ImageTask == nil {
		return DramaPaletteQueueResult{}, BadAuthRequest("色卡出图要先选图片模型")
	}
	only := req.AssetIDs
	if len(only) == 0 {
		palettes, err := s.projectPaletteAssets(userID, projectID)
		if err != nil {
			return DramaPaletteQueueResult{}, err
		}
		for _, a := range palettes {
			state := s.paletteImageView(userID, &a)
			if !paletteMetaOf(&a).Stale && state.Status != paletteImageBound && state.Status != paletteImageQueued {
				only = append(only, a.ID)
			}
		}
		if len(only) == 0 {
			return DramaPaletteQueueResult{Queued: []string{}, Waiting: []string{}, Failed: []string{}}, nil
		}
	}
	return s.queueDramaPaletteImages(userID, projectID, *req.ImageTask, "assets", only)
}

// paletteImageView 是色卡出图状态给页面看的样子：记成「已排队」但任务已经失败或取消的，按失败显示（失败不走收尾，状态不会自己变）。
func (s *Service) paletteImageView(userID string, asset *model.Asset) paletteImageState {
	state := paletteImageStateOf(asset)
	if state.Status == "" {
		state.Status = paletteImageNone
	}
	if state.Status != paletteImageQueued || state.TaskID == "" {
		return state
	}
	task, err := s.repo.TaskForUser(userID, state.TaskID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state.Status, state.Error = paletteImageFailed, "出图任务不见了"
		return state
	}
	if err != nil {
		return state
	}
	switch task.Status {
	case model.TaskStatusFailed, model.TaskStatusCancelled:
		state.Status, state.Error = paletteImageFailed, firstNonEmpty(userFacingTaskError(task.Error), "出图任务失败")
	case model.TaskStatusSucceeded:
		// 任务成功但收尾记状态之前：有图就当已绑。
		if asset.Kind == "image" {
			state.Status = paletteImageBound
		}
	}
	return state
}

// paletteAssetSummary 是色卡资产的摘要。
func (s *Service) paletteAssetSummary(userID string, asset *model.Asset, versions []model.AssetVersion) *ProjectAssetPaletteSummary {
	var definition paletteDefinition
	for _, v := range versions {
		if v.ID == asset.PrimaryVersionID {
			_ = json.Unmarshal([]byte(v.DefinitionJSON), &definition)
			definition.Prompt = firstNonEmpty(strings.TrimSpace(v.Prompt), definition.Prompt)
			break
		}
	}
	state := s.paletteImageView(userID, asset)
	meta := paletteMetaOf(asset)
	summary := &ProjectAssetPaletteSummary{Code: firstNonEmpty(definition.Code, meta.Code), Entry: definition.Palette, Design: definition.Design, Prompt: definition.Prompt, SceneGone: meta.Stale, ImageStatus: state.Status, TaskID: state.TaskID, ImageError: state.Error}
	if check, ok := decodeInto[drama.PaletteCheck](payloadMetadata(assetPayload(asset))["paletteCheck"]); ok && check.Blocks != nil && asset.Kind == "image" {
		summary.Check = &check
	}
	return summary
}

// ---------------------------------------------------------------- 出图之后：取色校验、接着排

// afterDramaPaletteImageTask 在色卡出图任务成功落库之后跑（inputJSON 是落库前的任务输入，带着加密的模板）：绑上图的做取色校验，
// 然后接着排「等空位」的色卡。这里的失败只记日志，不影响任务结果。
func (s *Service) afterDramaPaletteImageTask(task model.Task, inputJSON string) {
	if !strings.Contains(inputJSON, `"dramaPalette"`) {
		return
	}
	decrypted, err := s.decryptTaskInputJSON(inputJSON)
	if err != nil {
		_ = s.log(task.UserID, task.ID, "warn", "色卡出图收尾读不出任务输入", err.Error())
		return
	}
	var input struct {
		Metadata struct {
			AssetID   string `json:"projectAssetId"`
			ProjectID string `json:"domainProjectId"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal([]byte(decrypted), &input)
	projectID := firstNonEmpty(task.ProjectID, input.Metadata.ProjectID)
	if input.Metadata.AssetID == "" || projectID == "" {
		return
	}
	var result struct {
		Status string `json:"projectAssetImageStatus"`
	}
	_ = json.Unmarshal([]byte(task.ResultJSON), &result)
	if err := s.recordPaletteImage(task, input.Metadata.AssetID, result.Status); err != nil {
		_ = s.log(task.UserID, task.ID, "warn", "色卡取色校验没有写上", err.Error())
	}
	if template := taskPaletteImageTemplate(decrypted); template != nil {
		if _, err := s.queueDramaPaletteImages(task.UserID, projectID, *template, task.ID, nil); err != nil {
			_ = s.log(task.UserID, task.ID, "warn", "接着排色卡出图失败", err.Error())
		}
	}
}

// recordPaletteImage 记下一张色卡的出图结果：绑上图的按色块取色校验，结果写进版本主图的 metadata.paletteCheck 和资产 payload；
// 设定在出图期间改过（stale）的只记状态。
func (s *Service) recordPaletteImage(task model.Task, assetID string, status string) error {
	asset, err := s.repo.AssetForUser(task.UserID, assetID)
	if err != nil {
		return err
	}
	if asset.Category != model.AssetCategoryPalette {
		return nil
	}
	now := time.Now().Format(time.RFC3339Nano)
	if status != "bound" {
		return s.setPaletteImageState(task.UserID, asset.ID, func(payload map[string]any) {
			payloadMetadata(payload)["paletteImage"] = paletteImageState{Status: paletteImageStale, TaskID: task.ID, Error: "出图期间色卡换了版本，图留在任务结果里，没有绑到当前版本", UpdatedAt: now}
		})
	}
	representations, err := s.repo.AssetRepresentations(asset.PrimaryVersionID)
	if err != nil {
		return err
	}
	var primary *model.AssetRepresentation
	for i := range representations {
		if representations[i].Role == "primary" && representations[i].TaskID == task.ID {
			primary = &representations[i]
		}
	}
	if primary == nil {
		return nil
	}
	version, err := s.repo.AssetVersion(asset.PrimaryVersionID)
	if err != nil {
		return err
	}
	var definition paletteDefinition
	_ = json.Unmarshal([]byte(version.DefinitionJSON), &definition)
	hexes := make([]string, 0)
	for _, c := range definition.Palette.Colors() {
		hexes = append(hexes, c.Hex)
	}
	check := s.samplePaletteResource(task.UserID, primary.ResourceID, hexes)
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(firstNonEmpty(primary.MetadataJSON, "{}")), &metadata)
	metadata["paletteCheck"] = check
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateAssetRepresentationMetadata(primary.ID, string(encoded)); err != nil {
		return err
	}
	if !check.Passed {
		_ = s.log(task.UserID, task.ID, "warn", "色卡取色校验未过："+paletteCheckSummary(check), "")
	}
	return s.setPaletteImageState(task.UserID, asset.ID, func(payload map[string]any) {
		metadata := payloadMetadata(payload)
		metadata["paletteImage"] = paletteImageState{Status: paletteImageBound, TaskID: task.ID, UpdatedAt: now}
		metadata["paletteCheck"] = check
	})
}

// samplePaletteResource 读出图资源做取色校验；读不出图时 passed=false 并写明原因。
func (s *Service) samplePaletteResource(userID string, resourceID string, hexes []string) drama.PaletteCheck {
	_, reader, err := s.OpenResource(userID, resourceID)
	if err != nil {
		return drama.PaletteCheck{Blocks: []drama.PaletteCheckBlock{}, Threshold: drama.PaletteCheckMaxDeltaE, Note: "读不到图片：" + err.Error()}
	}
	defer reader.Close()
	img, _, err := image.Decode(reader)
	if err != nil {
		return drama.PaletteCheck{Blocks: []drama.PaletteCheckBlock{}, Threshold: drama.PaletteCheckMaxDeltaE, Note: "图片格式不支持取色（支持 PNG、JPEG、GIF）"}
	}
	return drama.CheckPaletteImage(img, hexes)
}

func paletteCheckSummary(check drama.PaletteCheck) string {
	if check.Note != "" {
		return check.Note
	}
	var parts []string
	for i, b := range check.Blocks {
		if b.DeltaE < 0 || b.DeltaE > check.Threshold {
			parts = append(parts, fmt.Sprintf("第 %d 块 %s 取到 %s（ΔE %.1f）", i+1, b.Hex, b.Sampled, b.DeltaE))
		}
	}
	return strings.Join(parts, "；")
}

// projectPaletteProgress 是「色卡齐了」要的色卡：场编号和已确认主版本有没有图；场次已不存在的不算。
func (s *Service) projectPaletteProgress(userID string, projectID string) ([]drama.PaletteInput, error) {
	palettes, err := s.projectPaletteAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]drama.PaletteInput, 0, len(palettes))
	for code, a := range palettes {
		if paletteMetaOf(&a).Stale {
			continue
		}
		out = append(out, drama.PaletteInput{Code: code, Imaged: a.Kind == "image" && a.Status == model.AssetVersionStatusConfirmed})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}
