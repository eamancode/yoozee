// 场次色卡资产（M8 卡 B，由 M7.2 导演风格的色卡改来）：场次色卡收尾按色彩脚本每场建一张（分类 palette，payload
// metadata.palette.kind=scene，直接已确认，不走候选），同一场重跑加版本、不新增资产。色卡图不调图片模型：写库后由代码按色值合成 PNG
// （drama.RenderScenePalettePNG），存成资源绑到这一版（bindProjectAssetImage，与资产出图同一条绑定路径）；没有取色校验。
// 资产页「重新合成」只按当前版本的色板重画图、绑成新版本（镜头上的色卡引用跟到新版本），不调模型。
// 资产 payload 里色卡自己的字段：metadata.palette（kind=scene、场编号、集号、场序、unit_id、切场规则；stale=true 是场次已不存在）。
// 场次结构变了（章节改编成剧集、集数变了）重跑场次色卡后，合并后色彩脚本里不再有的场（这次重写的集里没写到的、对不上当前场次表的），
// 色卡标成场次已不存在（不删，用户自己移出），不计入「色卡齐了」，也不再绑到镜头；之后哪次又写到这一场，加版本时恢复。
// M8 之前导演风格建的色卡（metadata.palette 没有 kind，图由图片模型出）照常显示；重跑场次色卡写到同一场时加版本、改成场次色卡。

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// paletteKindScene 是场次色卡（metadata.palette.kind）；主基调色卡另记在 metadata.masterPalette。
const paletteKindScene = "scene"

// DramaPaletteResynthesizeRequest 是资产页「重新合成」：AssetIDs 为空时重画全部还没有图的场次色卡；点名主基调色卡时重画主色卡图。
type DramaPaletteResynthesizeRequest struct {
	AssetIDs []string `json:"assetIds,omitempty"`
}

// DramaPaletteResynthesizeResult 是重新合成的结果：Rendered 是合成并绑上图的色卡，Failed 是没合成上的（带原因）。
type DramaPaletteResynthesizeResult struct {
	Rendered []string              `json:"rendered"`
	Failed   []DramaPaletteFailure `json:"failed"`
	// FontNote 是合成时没用上中文字体的原因（图上只有 HEX 和英文名）。
	FontNote string `json:"fontNote,omitempty"`
}

// DramaPaletteFailure 是一张没合成上图的色卡。
type DramaPaletteFailure struct {
	AssetID string `json:"assetId"`
	Code    string `json:"code"`
	Error   string `json:"error"`
}

// ProjectAssetPaletteSummary 是色卡资产在资产清单里的摘要（资产页色卡卡片用）。主基调色卡（风格锁定建的）只有 Master，没有场编号。
type ProjectAssetPaletteSummary struct {
	Master *ProjectAssetMasterPaletteSummary `json:"master,omitempty"`
	// Kind 是 scene（场次色卡）；为空是 M8 之前导演风格建的旧色卡。
	Kind  string                 `json:"kind,omitempty"`
	Code  string                 `json:"code"`
	Entry drama.ColorScriptEntry `json:"entry"`
	// Design 是《色卡提示词卡》【色板设计】的结构句。
	Design string `json:"design"`
	// SceneGone 是这一场在当前剧集里已不存在（重跑场次色卡时标的），色卡保留但不计入「色卡齐了」、不绑镜头。
	SceneGone bool `json:"sceneGone,omitempty"`
	// Note 是按新场次重排时记下的说明（挪了编号、并进别的场）。
	Note string `json:"note,omitempty"`
	// ImageStatus 是 bound（主版本有图）或 none（还没合成上，资产页「重新合成」）。
	ImageStatus string `json:"imageStatus"`
}

// paletteMeta 是色卡 payload.metadata 里的场编号与场次状态。
type paletteMeta struct {
	// Kind 是 scene（场次色卡）；为空是 M8 之前导演风格建的旧色卡。
	Kind    string `json:"kind,omitempty"`
	Code    string `json:"code"`
	Episode int    `json:"episode"`
	SceneNo int    `json:"sceneNo"`
	// UnitID 是写这张色卡时这一场所在的单元（剧集，没有剧集时是原文章节）。
	UnitID string `json:"unitId,omitempty"`
	// SceneRule 是写这张色卡时的切场规则（drama.SceneRuleMerged）；没有（0）是规则改变前写的，场编号可能和现在的场次表错位。
	SceneRule int `json:"sceneRule,omitempty"`
	// RenumberedFrom 是按新场次重排（RenumberDramaScenes）之前的场编号；Note 是重排时记下的说明。
	RenumberedFrom string `json:"renumberedFrom,omitempty"`
	Note           string `json:"note,omitempty"`
	// Stale 是场次已不存在：StaleAt 是标上的时间。
	Stale   bool   `json:"stale,omitempty"`
	StaleAt string `json:"staleAt,omitempty"`
}

// paletteDefinition 是色卡版本的设定（asset_versions.definition_json）。
type paletteDefinition struct {
	Kind    string                 `json:"kind,omitempty"`
	Code    string                 `json:"code"`
	Palette drama.ColorScriptEntry `json:"palette"`
	Design  string                 `json:"design"`
}

func paletteDefinitionOf(e drama.ColorScriptEntry) paletteDefinition {
	return paletteDefinition{Kind: paletteKindScene, Code: e.Code(), Palette: e, Design: drama.PaletteDesign(e)}
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

// scenePaletteUsable 是这张色卡可以当场次色卡用（绑镜头、进视频参考、计入「色卡齐了」）：场次色卡、场次还在、没归档、有主版本。
func scenePaletteUsable(asset *model.Asset) bool {
	meta := paletteMetaOf(asset)
	return meta.Kind == paletteKindScene && !meta.Stale && asset.PrimaryVersionID != "" && asset.Status != model.AssetVersionStatusArchived
}

// projectPaletteAssets 是项目里的色卡资产，按场编号；同一场编号有几张时（按新场次重排后，并掉的旧色卡标成场次已不存在、
// 编号留着）取没标的，再取场次色卡，再取最早建的。
func (s *Service) projectPaletteAssets(userID string, projectID string) (map[string]model.Asset, error) {
	assets, err := s.repo.ProjectAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	out := map[string]model.Asset{}
	for _, a := range assets {
		code := paletteCodeOf(&a)
		if code == "" {
			continue
		}
		prev, ok := out[code]
		if !ok {
			out[code] = a
			continue
		}
		meta, prevMeta := paletteMetaOf(&a), paletteMetaOf(&prev)
		if meta.Stale != prevMeta.Stale {
			if !meta.Stale {
				out[code] = a
			}
			continue
		}
		if scene, prevScene := meta.Kind == paletteKindScene, prevMeta.Kind == paletteKindScene; scene != prevScene {
			if scene {
				out[code] = a
			}
			continue
		}
		if a.CreatedAt.Before(prev.CreatedAt) {
			out[code] = a
		}
	}
	return out, nil
}

// dramaPaletteWrites 为这次写的每一场准备场次色卡：已有同一场（按场编号找）的色卡加一个版本并换成主版本（还没有图，写库后合成图绑到
// 这一版），没有的新建资产、版本 1 和项目关联。这次没写到的已有色卡里，retire 认定在运行范围内的（色彩脚本里已经没有这一场），
// 标成场次已不存在（只改 payload，不加版本、不删）。
func (s *Service) dramaPaletteWrites(userID string, project *model.Project, entries []drama.ColorScriptEntry, retire func(paletteMeta) bool) ([]repository.DramaPaletteWrite, DramaPaletteWriteSummary, error) {
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
		definition := paletteDefinitionOf(e)
		encoded, err := json.Marshal(definition)
		if err != nil {
			return nil, summary, err
		}
		title := drama.PaletteTitle(e)
		meta := paletteMeta{Kind: paletteKindScene, Code: e.Code(), Episode: e.Episode, SceneNo: e.SceneNo, UnitID: e.UnitID, SceneRule: e.SceneRule}
		versionID := newID()
		version := model.AssetVersion{ID: versionID, AssetID: "", Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: string(encoded), Note: "场次色卡", CreatedAt: now, UpdatedAt: now}
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
			// M8 之前的出图状态与取色结果不再用。
			delete(metadata, "paletteCheck")
			delete(metadata, "paletteImage")
			metadata["palette"] = meta
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
			"primaryVersionId": versionID, "title": title, "coverUrl": "", "tags": []string{"色卡", "场次色卡"},
			"data":      map[string]any{"definition": definition},
			"metadata":  map[string]any{"palette": meta},
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

// renderScenePaletteImage 按版本的色板合成色卡图、存成资源、绑到这一版（expectedVersionID 是资产当前主版本：主版本还没有图时
// 绑到它自己，已有图时加一个版本，镜头上的引用跟过去）。
func (s *Service) renderScenePaletteImage(userID string, projectID string, assetID string, expectedVersionID string, e drama.ColorScriptEntry) error {
	png, err := drama.RenderScenePalettePNG(e)
	if err != nil {
		return fmt.Errorf("色卡图合成失败：%w", err)
	}
	resource, err := s.UploadResourceFile(userID, "scene-palette-"+strings.ToLower(e.Code())+".png", int64(len(png)), "image", drama.MasterPaletteImageWidth, drama.MasterPaletteImageHeight, 0, bytes.NewReader(png))
	if err != nil {
		return err
	}
	if _, err := s.bindProjectAssetImage(userID, projectID, assetID, BindProjectAssetImageRequest{ResourceID: resource.ID, ExpectedVersionID: expectedVersionID}, newID()); err != nil {
		return err
	}
	return nil
}

// ResynthesizeDramaPalettes 是资产页「重新合成」：按色卡当前版本的色板重画图、绑成新版本，不调模型。
// 认场次色卡（旧色卡要先重新生成场次色卡；场次已不存在的不画）和点名的主基调色卡（服务器补装中文字体后重画旧图用）。
func (s *Service) ResynthesizeDramaPalettes(userID string, projectID string, req DramaPaletteResynthesizeRequest) (DramaPaletteResynthesizeResult, error) {
	result := DramaPaletteResynthesizeResult{Rendered: []string{}, Failed: []DramaPaletteFailure{}}
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return result, err
	}
	palettes, err := s.projectPaletteAssets(userID, project.ID)
	if err != nil {
		return result, err
	}
	byID := map[string]model.Asset{}
	for _, a := range palettes {
		byID[a.ID] = a
	}
	master, err := s.projectMasterPaletteAsset(userID, project.ID)
	if err != nil {
		return result, err
	}
	var targets []model.Asset
	if len(req.AssetIDs) == 0 {
		for _, code := range slices.Sorted(maps.Keys(palettes)) {
			a := palettes[code]
			if scenePaletteUsable(&a) && a.Kind != "image" {
				targets = append(targets, a)
			}
		}
	}
	for _, id := range req.AssetIDs {
		if master != nil && strings.TrimSpace(id) == master.ID {
			if err := s.resynthesizeMasterPalette(userID, project, master); err != nil {
				result.Failed = append(result.Failed, DramaPaletteFailure{AssetID: master.ID, Error: userFacingTaskError(err.Error())})
			} else {
				result.Rendered = append(result.Rendered, master.ID)
			}
			continue
		}
		asset, ok := byID[strings.TrimSpace(id)]
		if !ok {
			return result, BadAuthRequest("所选资产不是这个项目的场次色卡")
		}
		meta := paletteMetaOf(&asset)
		if meta.Kind != paletteKindScene {
			return result, BadAuthRequest(fmt.Sprintf("「%s」是旧版色卡，先在章节页重新生成场次色卡", asset.Title))
		}
		if meta.Stale {
			return result, BadAuthRequest(fmt.Sprintf("「%s」的场次已不存在，不再合成", asset.Title))
		}
		targets = append(targets, asset)
	}
	for _, asset := range targets {
		version, err := s.repo.AssetVersion(asset.PrimaryVersionID)
		if err != nil {
			result.Failed = append(result.Failed, DramaPaletteFailure{AssetID: asset.ID, Code: paletteCodeOf(&asset), Error: err.Error()})
			continue
		}
		var definition paletteDefinition
		_ = json.Unmarshal([]byte(version.DefinitionJSON), &definition)
		entry := definition.Palette
		// 按新场次重排过的色卡没加版本，场编号以 metadata 为准。
		if meta := paletteMetaOf(&asset); meta.Code != "" {
			entry.Episode, entry.SceneNo = meta.Episode, meta.SceneNo
		}
		if err := s.renderScenePaletteImage(userID, project.ID, asset.ID, asset.PrimaryVersionID, entry); err != nil {
			result.Failed = append(result.Failed, DramaPaletteFailure{AssetID: asset.ID, Code: entry.Code(), Error: userFacingTaskError(err.Error())})
			continue
		}
		result.Rendered = append(result.Rendered, asset.ID)
	}
	if len(result.Rendered) > 0 {
		_, result.FontNote = drama.MasterPaletteFontNote()
	}
	return result, nil
}

// resynthesizeMasterPalette 按主色卡当前版本的色板重画主色卡图、绑成新版本；项目锁定记录指着这张主色卡时，
// 记录里的图和字体说明跟着换（写不上不影响已经绑好的图）。
func (s *Service) resynthesizeMasterPalette(userID string, project *model.Project, master *model.Asset) error {
	version, err := s.repo.AssetVersion(master.PrimaryVersionID)
	if err != nil {
		return err
	}
	var definition masterPaletteDefinition
	if json.Unmarshal([]byte(version.DefinitionJSON), &definition) != nil || len(drama.NormalizeMasterPalette(definition.Palette).Groups) == 0 {
		return BadAuthRequest("主色卡这一版读不出色板，到章节页重新锁定风格")
	}
	resource, fontNote, err := s.renderMasterPaletteResource(userID, firstNonEmpty(strings.TrimSpace(definition.Title), project.Name), definition.Palette)
	if err != nil {
		return err
	}
	if _, err := s.bindProjectAssetImage(userID, project.ID, master.ID, BindProjectAssetImageRequest{ResourceID: resource.ID, ExpectedVersionID: master.PrimaryVersionID}, newID()); err != nil {
		return err
	}
	doc := map[string]any{}
	if json.Unmarshal([]byte(project.BibleJSON), &doc) == nil && doc != nil {
		if lock, ok := doc["style_lock"].(map[string]any); ok && lock["asset_id"] == master.ID {
			lock["resource_id"] = resource.ID
			if fontNote == "" {
				delete(lock, "font_note")
			} else {
				lock["font_note"] = fontNote
			}
			if encoded, err := json.Marshal(doc); err == nil {
				_ = s.repo.UpdateProjectDramaDocument(userID, project.ID, "bible_json", string(encoded), time.Now())
			}
		}
	}
	return nil
}

// paletteAssetSummary 是色卡资产的摘要。
func (s *Service) paletteAssetSummary(userID string, asset *model.Asset, versions []model.AssetVersion) *ProjectAssetPaletteSummary {
	status := dramaImageNone
	if asset.Kind == "image" {
		status = dramaImageBound
	}
	if _, ok := masterPaletteMetaOf(asset); ok {
		return &ProjectAssetPaletteSummary{Master: masterPaletteSummary(asset, versions), ImageStatus: status}
	}
	var definition paletteDefinition
	for _, v := range versions {
		if v.ID == asset.PrimaryVersionID {
			_ = json.Unmarshal([]byte(v.DefinitionJSON), &definition)
			break
		}
	}
	meta := paletteMetaOf(asset)
	summary := &ProjectAssetPaletteSummary{Kind: meta.Kind, Code: firstNonEmpty(definition.Code, meta.Code), Entry: definition.Palette, Design: definition.Design, SceneGone: meta.Stale, Note: meta.Note, ImageStatus: status}
	// 按新场次重排过的色卡没加版本（图还是原来那张），场编号以 metadata 为准。
	if meta.RenumberedFrom != "" && meta.Code != "" {
		summary.Code, summary.Entry.Episode, summary.Entry.SceneNo = meta.Code, meta.Episode, meta.SceneNo
	}
	return summary
}

// projectPaletteProgress 是「色卡齐了」要的场次色卡：场编号和已确认主版本有没有合成图。只算场次色卡（旧色卡不算）、
// 场次还在、对得上当前场次表（写它时的单元就是现在这一场的单元，切场规则改变前写的不算）的。
func (s *Service) projectPaletteProgress(userID string, projectID string, table map[string]styleSceneSlot) ([]drama.PaletteInput, error) {
	palettes, err := s.projectPaletteAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]drama.PaletteInput, 0, len(palettes))
	for code, a := range palettes {
		if !scenePaletteUsable(&a) {
			continue
		}
		meta := paletteMetaOf(&a)
		if slot, ok := table[code]; ok && (meta.UnitID != "" && meta.UnitID != slot.UnitID || meta.SceneRule == 0 && slot.LegacyShifted) {
			continue
		}
		out = append(out, drama.PaletteInput{Code: code, Imaged: a.Kind == "image" && a.Status == model.AssetVersionStatusConfirmed})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}
