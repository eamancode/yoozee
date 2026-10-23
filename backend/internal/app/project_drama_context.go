// 短剧装段、检查、编译用到的项目级输入：项目画风（对照出的 yoozee 风格档案、视频提示词的风格段）、
// 每集目标时长（需求文档 brief_json）、场景资产的锚点（R-SHOT-012 的空间图）。

package app

import (
	"encoding/json"
	"slices"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// dramaProjectContext 是一个项目的短剧上下文；零值等于「没选画风、没设时长」：默认档案、档案 style_block、不查 R-SEG-008。
type dramaProjectContext struct {
	// ProfileID 是 yoozee 风格档案：项目的 style_profile；没填时需求文档 style_profile 手动指定的优先，否则按项目画风对照（style_map.json）。
	ProfileID string
	// ProfileRule 是档案的来由（「项目设置」「需求文档指定」或对照命中的规则说明），给装段说明看。
	ProfileRule string
	// StyleSpec 是项目画风的执行规范原文；为空时视频提示词的风格段用档案 style_block。
	StyleSpec string
	// HasStyle 是项目选没选画风（只存了预设 ID 的老项目也算选了）。
	HasStyle bool
	Brief    drama.Brief
	// Episode 是这一章在项目里的集号（从 1 起），按集号读 brief.episodes[].target_seconds；0 表示不按集。
	Episode int
}

func (pc dramaProjectContext) profileID() string {
	return firstNonEmpty(strings.TrimSpace(pc.ProfileID), drama.DefaultProfileID)
}

// targetSeconds 是这一章的目标片长（R-SEG-008）。
func (pc dramaProjectContext) targetSeconds() float64 {
	return pc.Brief.EpisodeTargetSeconds(pc.Episode)
}

// projectDramaContext 从项目字段读短剧上下文。风格档案取项目的 style_profile；没填（只有测试和内部调用方会这样建项目）
// 时按 RecommendProfile 推荐。需求文档按非严格方式读：早期写进来的不合契约字段不影响这里要的几项。
func projectDramaContext(project *model.Project) dramaProjectContext {
	pc := dramaProjectContext{}
	if raw := strings.TrimSpace(project.BriefJSON); raw != "" {
		_ = json.Unmarshal([]byte(raw), &pc.Brief)
	}
	var style styleProfileDocument
	if raw := strings.TrimSpace(project.StyleProfileJSON); raw != "" {
		_ = json.Unmarshal([]byte(raw), &style)
	}
	pc.StyleSpec = strings.TrimSpace(style.Prompt)
	pc.HasStyle = pc.StyleSpec != "" || strings.TrimSpace(project.StylePresetID) != ""
	if id := strings.TrimSpace(project.StyleProfile); drama.ValidProfileID(id) {
		pc.ProfileID, pc.ProfileRule = id, "项目设置"
		return pc
	}
	pc.ProfileID, pc.ProfileRule = drama.RecommendProfile(project.StylePresetID, project.StyleProfileJSON, project.BriefJSON)
	return pc
}

// projectStyleOverride 是编译时的风格段：项目选了画风，就用执行规范按编译档案 blocks.project_style 写成的风格段；
// 没选返回 nil，编译取风格档案的 style_block。
func projectStyleOverride(phrases *drama.CompileProfile, pc dramaProjectContext) *string {
	text := phrases.ProjectStyleBlock(pc.StyleSpec)
	if text == "" {
		return nil
	}
	return &text
}

// dramaChapterEpisode 是章节在项目里的集号：同类章节按顺序数，从 1 起；找不到返回 0。
func (s *Service) dramaChapterEpisode(projectID string, unit *model.ProjectUnit) int {
	units, err := s.repo.ProjectUnitSummaries(projectID)
	if err != nil {
		return 0
	}
	n := 0
	for _, u := range units {
		if u.Kind != unit.Kind {
			continue
		}
		n++
		if u.ID == unit.ID {
			return n
		}
	}
	return 0
}

// dramaSceneAnchors 是场景资产定义里的锚点（资产页可编辑，提取场景时由模型填）：资产标题 → 锚点。
func dramaSceneAnchors(assets []ProjectAssetSummary) map[string][]string {
	out := map[string][]string{}
	for _, a := range assets {
		if dramaRefRole(a.Category) != "location" || a.Visual == nil {
			continue
		}
		if anchors := sceneAnchorList(a.Visual.Definition["anchors"]); len(anchors) > 0 {
			out[strings.TrimSpace(a.Title)] = anchors
		}
	}
	return out
}

// sceneAnchorList 把定义里的 anchors 读成去重、去空的字符串数组（也接受顿号、逗号分隔的一句话）。
func sceneAnchorList(value any) []string {
	var items []string
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if text, ok := item.(string); ok {
				items = append(items, text)
			}
		}
	case []string:
		items = v
	case string:
		items = strings.FieldsFunc(v, func(r rune) bool { return strings.ContainsRune("、，,；;\n", r) })
	}
	var out []string
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" && !slices.Contains(out, item) {
			out = append(out, item)
		}
	}
	return out
}

// dramaCheckScene 给检查用的剧本和资产圣经补上场景锚点：剧本场标题里的地点对上场景资产名，就把这一场的 location_ref
// 指向它，资产的锚点当这一场的空间图（R-SHOT-012）。剧本原样不动，返回副本。
func dramaCheckScene(script *drama.Script, assets []ProjectAssetSummary) (*drama.Script, *drama.Bible) {
	anchors := dramaSceneAnchors(assets)
	if script == nil || len(anchors) == 0 {
		return script, nil
	}
	bible := &drama.Bible{}
	for name, list := range anchors {
		bible.Locations = append(bible.Locations, drama.BibleLocation{Name: name, Anchors: list})
	}
	slices.SortFunc(bible.Locations, func(a, b drama.BibleLocation) int { return strings.Compare(a.Name, b.Name) })
	copied := *script
	copied.Scenes = slices.Clone(script.Scenes)
	for i := range copied.Scenes {
		if copied.Scenes[i].LocationRef != "" {
			continue
		}
		if name := headingLocation(copied.Scenes[i].Heading, anchors); name != "" {
			copied.Scenes[i].LocationRef = name
		}
	}
	return &copied, bible
}

// headingLocation 是场标题（「内·电梯口·夜」）里能对上的场景资产名（括注不计；同名时取排序靠前的）。
func headingLocation[T any](heading string, byName map[string]T) string {
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, part := range strings.Split(heading, "·") {
		key := dramaLocationKey(part)
		if key == "" {
			continue
		}
		for _, name := range names {
			if dramaLocationKey(name) == key {
				return name
			}
		}
	}
	return ""
}
