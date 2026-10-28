// 资产出图的参考图（M8 卡 D）：角色、子卡、道具带主基调色卡图；场景带本场色卡图和主基调色卡图；子卡另带主卡已确认的三视图
// （排在最前，保证脸一致）。图片模型的参考图名额（模板里前端按模型能力写的 maxReferenceImages）不够时色卡图先让出（先让主基调色卡，
// 再让本场色卡），让出的色卡把色值写进提示词；建任务时模型说不支持这么多参考图，就去掉色卡图再建，还不行就不带参考图。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// dramaImageRef 是出图任务的一张参考图：资源、给图片模型看的说明；Palette 是色卡图（名额不够时先让出，让出后写 Colors 的色值）。
type dramaImageRef struct {
	ResourceID string
	Label      string
	Palette    bool
	Colors     []drama.PaletteColor
}

// 参考图的降级档：0 全带，1 不带色卡图（色值写进提示词），2 不带参考图。
const (
	dramaRefsAll = iota
	dramaRefsNoPalette
	dramaRefsNone
)

// versionImageResource 是一个资产版本绑的图（按 roles 的先后取第一个有的）；没有返回空串。
func (s *Service) versionImageResource(versionID string, roles ...string) (string, error) {
	if versionID == "" {
		return "", nil
	}
	reps, err := s.repo.AssetRepresentations(versionID)
	if err != nil {
		return "", err
	}
	for _, role := range roles {
		for _, r := range reps {
			if r.Role == role && r.ResourceID != "" && strings.HasPrefix(r.MediaType, "image") {
				return r.ResourceID, nil
			}
		}
	}
	return "", nil
}

// dramaAssetImageRefs 是一个资产出图要带的参考图（按优先级排：主卡三视图、本场色卡、主基调色卡）。
func (s *Service) dramaAssetImageRefs(userID string, project *model.Project, asset model.Asset, definition map[string]any) ([]dramaImageRef, error) {
	var refs []dramaImageRef
	if asset.ParentAssetID != "" {
		parent, err := s.repo.AssetForUser(userID, asset.ParentAssetID)
		if err == nil {
			id, err := s.versionImageResource(parent.PrimaryVersionID, "turnaround_sheet", "front", "primary")
			if err != nil {
				return nil, err
			}
			if id != "" {
				refs = append(refs, dramaImageRef{ResourceID: id, Label: fmt.Sprintf("主卡「%s」已确认的三视图：脸型、五官、发型、肤色、体型与它完全一致，只换服装或状态", parent.Title)})
			}
		}
	}
	if asset.Category == model.AssetCategoryEnvironment {
		ref, err := s.sceneEnvironmentPaletteRef(userID, project, asset, definition)
		if err != nil {
			return nil, err
		}
		if ref != nil {
			refs = append(refs, *ref)
		}
	}
	master, err := s.projectMasterPaletteAsset(userID, project.ID)
	if err != nil {
		return nil, err
	}
	if master != nil {
		id, err := s.versionImageResource(master.PrimaryVersionID, "primary")
		if err != nil {
			return nil, err
		}
		_, palette, _, _ := projectLockedStyle(project.BibleJSON)
		use := "角色服装、饰品和道具的颜色从这张色卡取"
		if asset.Category == model.AssetCategoryEnvironment {
			use = "场景的基础色调从这张色卡的环境组取"
		}
		if id != "" {
			refs = append(refs, dramaImageRef{ResourceID: id, Label: "主基调色卡：" + use, Palette: true, Colors: palette.Colors()})
		}
	}
	return refs, nil
}

// sceneEnvironmentPaletteRef 是场景资产的本场色卡：场景回填绑到的第一场（场编号排在最前的），没绑时取提取时记的第一次出现的场。
func (s *Service) sceneEnvironmentPaletteRef(userID string, project *model.Project, asset model.Asset, definition map[string]any) (*dramaImageRef, error) {
	var codes []string
	for code, entry := range projectSceneAssets(project.BibleJSON) {
		if slices.Contains(entry.Environments, asset.ID) {
			codes = append(codes, code)
		}
	}
	slices.Sort(codes)
	if first, _ := definition["firstScene"].(string); first != "" {
		codes = append(codes, strings.ToUpper(strings.TrimSpace(first)))
	}
	if len(codes) == 0 {
		return nil, nil
	}
	palettes, err := s.projectPaletteAssets(userID, project.ID)
	if err != nil {
		return nil, err
	}
	for _, code := range codes {
		palette, ok := palettes[code]
		if !ok || !scenePaletteUsable(&palette) {
			continue
		}
		version, err := s.repo.AssetVersion(palette.PrimaryVersionID)
		if err != nil {
			return nil, err
		}
		var def paletteDefinition
		_ = json.Unmarshal([]byte(version.DefinitionJSON), &def)
		id, err := s.versionImageResource(version.ID, "primary")
		if err != nil {
			return nil, err
		}
		if id == "" {
			continue
		}
		return &dramaImageRef{ResourceID: id, Label: fmt.Sprintf("本场色卡 %s：这个场景的颜色从这张色卡取", code), Palette: true, Colors: def.Palette.Colors()}, nil
	}
	return nil, nil
}

// pickDramaImageRefs 按名额和降级档挑参考图：名额不够时从后往前让出色卡图（主卡三视图不让）；返回带的和让出的色卡。
func pickDramaImageRefs(refs []dramaImageRef, maxImages int, level int) ([]dramaImageRef, []dramaImageRef) {
	var kept, dropped []dramaImageRef
	for _, r := range refs {
		switch {
		case level >= dramaRefsNone:
			if r.Palette {
				dropped = append(dropped, r)
			}
		case level >= dramaRefsNoPalette && r.Palette:
			dropped = append(dropped, r)
		default:
			kept = append(kept, r)
		}
	}
	for maxImages > 0 && len(kept) > maxImages {
		i := len(kept) - 1
		for i >= 0 && !kept[i].Palette {
			i--
		}
		if i < 0 {
			kept = kept[:maxImages]
			break
		}
		dropped = append(dropped, kept[i])
		kept = slices.Delete(kept, i, i+1)
	}
	return kept, dropped
}

// dramaImageRefInputs 把参考图换成任务 input.referenceImages（resource:ID，执行前按资源读图）；资源不在或没就绪的跳过。
func (s *Service) dramaImageRefInputs(userID string, refs []dramaImageRef) ([]any, []dramaImageRef, error) {
	inputs := make([]any, 0, len(refs))
	var used []dramaImageRef
	for _, r := range refs {
		resource, err := s.repo.ResourceForUser(userID, r.ResourceID)
		if err != nil || resource.Status != model.ResourceStatusReady {
			continue
		}
		inputs = append(inputs, map[string]any{"id": resource.ID, "type": "image", "name": r.Label, "storageKey": "resource:" + resource.ID, "mimeType": resource.MimeType, "width": resource.Width, "height": resource.Height, "bytes": resource.Size})
		used = append(used, r)
	}
	return inputs, used, nil
}

// dramaImageRefsNote 是写进出图提示词的参考图说明：第几张是什么；让出的色卡写色值。
func dramaImageRefsNote(kept []dramaImageRef, dropped []dramaImageRef) string {
	var lines []string
	for i, r := range kept {
		lines = append(lines, fmt.Sprintf("参考图 %d：%s。", i+1, r.Label))
	}
	for _, r := range dropped {
		colors := make([]string, 0, len(r.Colors))
		for _, c := range r.Colors {
			colors = append(colors, strings.TrimSpace(firstNonEmpty(c.NameZh, c.NameEn)+" "+c.Hex))
		}
		if len(colors) == 0 {
			continue
		}
		name, _, _ := strings.Cut(r.Label, "：")
		lines = append(lines, fmt.Sprintf("%s（参考图名额不够，没带图）色值：%s。", name, strings.Join(colors, "、")))
	}
	return strings.Join(lines, "\n")
}

// withDramaImageRefs 给出图任务挂上参考图：按模板的名额和降级档挑图，input.referenceImages 写上，返回写进提示词的说明。
func (s *Service) withDramaImageRefs(userID string, input map[string]any, refs []dramaImageRef, template DramaImageTaskTemplate, level int) (string, error) {
	kept, dropped := pickDramaImageRefs(refs, template.MaxReferenceImages, level)
	inputs, used, err := s.dramaImageRefInputs(userID, kept)
	if err != nil {
		return "", err
	}
	if len(inputs) > 0 {
		input["referenceImages"] = inputs
	}
	return dramaImageRefsNote(used, dropped), nil
}

// modelCapabilityError 是建任务时模型说不支持这次的输入（比如参考图太多）。
func modelCapabilityError(err error) bool {
	var modelErr *ModelError
	return errors.As(err, &modelErr) && modelErr.ErrorCode == ErrCodeModelCapabilityNotSupported
}

// dramaRefsLevelNote 是降级后记在日志里的说明。
var dramaRefsLevelNote = map[int]string{dramaRefsNoPalette: "图片模型不支持这么多参考图，去掉色卡图（色值写进提示词）", dramaRefsNone: "图片模型不支持参考图，不带参考图出图"}
