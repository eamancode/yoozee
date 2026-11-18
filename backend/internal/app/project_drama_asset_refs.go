// 资产出图的参考图（M8 卡 D）：角色、子卡、道具带主基调色卡图；场景带本场色卡图和主基调色卡图；子卡另带主卡已确认的三视图
// （排在最前，保证脸一致）。图片模型的参考图名额（模板里前端按模型能力写的 maxReferenceImages）不够时色卡图先让出（先让主基调色卡，
// 再让本场色卡），让出的色卡把色值写进提示词；主卡参考图不可降级丢弃。

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
// Degradable 是名额不够时可以改成文字的参考（场景关联的中、未标优先级道具）：让出后把 Text（道具本体描述）写进提示词，
// 排在色卡之后让出、从后往前（spatialPropRefs 已把高优先级排在前面）。既不是色卡也不可降级的（子卡的主卡图、
// 高优先级道具、用户补充参考）是必需参考，名额装不下时整张拒绝，不截掉。
type dramaImageRef struct {
	ResourceID string
	Label      string
	Palette    bool
	Colors     []drama.PaletteColor
	Degradable bool
	Name       string
	Text       string
}

// requiredRef 是必须带图的参考（不能让成文字）。
func (r dramaImageRef) requiredRef() bool {
	return !r.Palette && !r.Degradable
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
	if asset.Category == model.AssetCategoryEnvironment {
		propRefs, err := s.spatialPropRefs(userID, project, definition)
		if err != nil {
			return nil, err
		}
		refs = append(refs, propRefs...)
	}
	if asset.ParentAssetID != "" {
		parent, err := s.repo.AssetForUser(userID, asset.ParentAssetID)
		if err != nil {
			return nil, err
		}
		reference := parent
		age, _ := definition["age"].(string)
		if age != "" {
			reference, err = s.variantReferenceAtAge(userID, project.ID, asset, parent, age)
			if err != nil {
				return nil, err
			}
		}
		id, err := s.versionImageResource(confirmedImageVersion(reference), "turnaround_sheet", "front", "primary")
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, BadAuthRequest(fmt.Sprintf("请先完成主卡「%s」的三视图，再重试子卡出图", parent.Title))
		}
		label := fmt.Sprintf("主卡「%s」已确认的三视图：脸型、五官、发型、肤色、体型与它完全一致，只换服装或状态", parent.Title)
		if drama.IntegratedAssets(project.WorkflowID) {
			label = fmt.Sprintf("主卡「%s」已确认的四宫格：保持同一人物的面部辨识与肤色；年龄阶段、服装、发型及临时状态以当前子卡明确差异为准，不叠加主卡默认服装", parent.Title)
		}
		if age != "" {
			label = fmt.Sprintf("同一人物的已确认参考图「%s」：保留可辨识的面部关联。年龄、发型、服装及体型以本子卡的设定为准。", reference.Title)
		}
		refs = append(refs, dramaImageRef{ResourceID: id, Label: label})
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

func confirmedImageVersion(asset *model.Asset) string {
	pinned, _ := payloadMetadata(assetPayload(asset))[assetSettingsImageVersion].(string)
	return firstNonEmpty(pinned, asset.PrimaryVersionID)
}

func (s *Service) variantReferenceAtAge(userID, projectID string, asset model.Asset, parent *model.Asset, age string) (*model.Asset, error) {
	siblings, err := s.projectVariants(userID, projectID, parent.ID)
	if err != nil {
		return nil, err
	}
	for _, sibling := range siblings {
		if sibling.ID == asset.ID || sibling.Status == model.AssetVersionStatusArchived || confirmedImageVersion(&sibling) != sibling.PrimaryVersionID {
			continue
		}
		delta := assetVariantMetaOf(&sibling).VisualDelta
		if delta == nil || drama.AssetNameKey(delta.AgeStage) != drama.AssetNameKey(age) {
			continue
		}
		image, err := s.versionImageResource(sibling.PrimaryVersionID, "turnaround_sheet", "front", "primary")
		if err != nil {
			return nil, err
		}
		if image != "" {
			return &sibling, nil
		}
	}
	return parent, nil
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

// pickDramaImageRefs 按名额（小于 0 是不知道、不限）和降级档挑参考图：名额不够时先从后往前让出色卡图，再从后往前让出
// 可降级的道具图（必需参考不让）；返回带的和让出的（让出的写进提示词：色卡写色值，道具写本体描述）。
func pickDramaImageRefs(refs []dramaImageRef, maxImages int, level int) ([]dramaImageRef, []dramaImageRef) {
	var kept, dropped []dramaImageRef
	for _, r := range refs {
		switch {
		case level >= dramaRefsNone:
			if !r.requiredRef() {
				dropped = append(dropped, r)
			}
		case level >= dramaRefsNoPalette && r.Palette:
			dropped = append(dropped, r)
		default:
			kept = append(kept, r)
		}
	}
	for _, yield := range []func(dramaImageRef) bool{func(r dramaImageRef) bool { return r.Palette }, func(r dramaImageRef) bool { return r.Degradable }} {
		for maxImages >= 0 && len(kept) > maxImages {
			i := len(kept) - 1
			for i >= 0 && !yield(kept[i]) {
				i--
			}
			if i < 0 {
				break
			}
			dropped = append(dropped, kept[i])
			kept = slices.Delete(kept, i, i+1)
		}
	}
	if maxImages >= 0 && len(kept) > maxImages {
		kept = kept[:maxImages]
	}
	return kept, dropped
}

// dramaImageRefInputs 把参考图换成任务 input.referenceImages（resource:ID，执行前按资源读图）；资源不在或没就绪时：
// 色卡跳过，可降级的道具改成文字（放进 skipped），必需参考报错。
func (s *Service) dramaImageRefInputs(userID string, refs []dramaImageRef) ([]any, []dramaImageRef, []dramaImageRef, error) {
	inputs := make([]any, 0, len(refs))
	var used, skipped []dramaImageRef
	for _, r := range refs {
		resource, err := s.repo.ResourceForUser(userID, r.ResourceID)
		if err != nil || resource.Status != model.ResourceStatusReady || resource.Kind != "image" || !strings.HasPrefix(resource.MimeType, "image/") {
			if r.Degradable {
				skipped = append(skipped, r)
				continue
			}
			if r.requiredRef() {
				return nil, nil, nil, BadAuthRequest(fmt.Sprintf("参考图片不可用：%s。请先修复这张图片再重试", firstNonEmpty(r.Name, r.Label)))
			}
			continue
		}
		inputs = append(inputs, map[string]any{"id": resource.ID, "type": "image", "name": r.Label, "storageKey": "resource:" + resource.ID, "mimeType": resource.MimeType, "width": resource.Width, "height": resource.Height, "bytes": resource.Size})
		used = append(used, r)
	}
	return inputs, used, skipped, nil
}

// dramaImageRefsNote 是写进出图提示词的参考图说明：第几张是什么；让出的色卡写色值。
func dramaImageRefsNote(kept []dramaImageRef, dropped []dramaImageRef) string {
	var lines []string
	for i, r := range kept {
		lines = append(lines, fmt.Sprintf("参考图 %d：%s。", i+1, r.Label))
	}
	var props []string
	for _, r := range dropped {
		if r.Degradable {
			props = append(props, fmt.Sprintf("「%s」%s", r.Name, strings.TrimSpace(r.Text)))
		}
	}
	if len(props) > 0 {
		lines = append(lines, "以下关联道具（参考图名额不够，没带图）按文字还原形制与材质，位置以当前场景布局为准："+strings.Join(props, "；")+"。")
	}
	for _, r := range dropped {
		if !r.Palette {
			continue
		}
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
	limit := -1
	if template.MaxReferenceImages != nil {
		limit = *template.MaxReferenceImages
	}
	// 必需参考（子卡的主卡图、高优先级道具、用户补充参考）承载身份或关键形制，不能像色卡、普通道具一样退化为文字。
	var required []string
	for _, ref := range refs {
		if ref.requiredRef() {
			required = append(required, firstNonEmpty(ref.Name, ref.Label))
		}
	}
	if len(required) > 0 && (level >= dramaRefsNone || (limit >= 0 && limit < len(required))) {
		supported := "不支持参考图"
		if level < dramaRefsNone && limit >= 0 {
			supported = fmt.Sprintf("最多 %d 张参考图", limit)
		}
		return "", BadAuthRequest(fmt.Sprintf("出图必须带 %d 张身份或关键道具参考图（%s），当前图片模型%s：请换支持更多参考图的模型，或把部分道具改为中、低优先级后重试", len(required), strings.Join(required, "、"), supported))
	}
	kept, dropped := pickDramaImageRefs(refs, limit, level)
	inputs, used, skipped, err := s.dramaImageRefInputs(userID, kept)
	if err != nil {
		return "", err
	}
	if len(inputs) > 0 {
		input["referenceImages"] = inputs
	}
	dropped = append(dropped, skipped...)
	// 让出的按原来的排列写（名额不够时是从后往前让的）。
	key := func(r dramaImageRef) string { return r.ResourceID + "\x00" + r.Label }
	order := make(map[string]int, len(refs))
	for i, ref := range refs {
		if _, seen := order[key(ref)]; !seen {
			order[key(ref)] = i
		}
	}
	slices.SortStableFunc(dropped, func(a, b dramaImageRef) int { return order[key(a)] - order[key(b)] })
	if names := degradedPropNames(dropped); len(names) > 0 {
		// 记在任务上：哪些道具这次按文字还原（不是静默截掉）。
		if metadata, ok := input["metadata"].(map[string]any); ok {
			metadata["assetImageTextOnlyProps"] = names
		}
	}
	return dramaImageRefsNote(used, dropped), nil
}

// degradedPropNames 是这次改成文字的道具名。
func degradedPropNames(dropped []dramaImageRef) []string {
	var names []string
	for _, r := range dropped {
		if r.Degradable {
			names = append(names, r.Name)
		}
	}
	return names
}

// modelCapabilityError 是建任务时模型说不支持这次的输入（比如参考图太多）。
func modelCapabilityError(err error) bool {
	var modelErr *ModelError
	return errors.As(err, &modelErr) && modelErr.ErrorCode == ErrCodeModelCapabilityNotSupported
}

// dramaRefsLevelNote 是降级后记在日志里的说明。
var dramaRefsLevelNote = map[int]string{dramaRefsNoPalette: "图片模型不支持这么多参考图，去掉色卡图（色值写进提示词）", dramaRefsNone: "图片模型不支持参考图，不带参考图出图"}
