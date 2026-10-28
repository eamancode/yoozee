// 场次上的资产（M8 卡 D）：资产设计写库后，按场次表把场景资产（category=environment）和每场的地点对上（名字或别名对上
// drama.SplitScenes 的 Location），回填到 bible_json.scene_assets（场编号 → {unit_id, environments}）；场次表接口返回每场绑的
// 场景资产。装段时（planDramaShotlistWith）这一场的镜头：没绑场景的补上本场的场景资产；绑了主卡、而这个角色在这一场有子卡的
// （子卡的场含这一场）换成子卡，镜头 refs_hint 里的主卡名换成「名·变体」。生成分段每一轮的提示词里写明这一场用哪张子卡。

package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// dramaSceneAssets 是 bible_json.scene_assets 的一场：所在单元和绑的场景资产。
type dramaSceneAssets struct {
	UnitID       string   `json:"unit_id"`
	Environments []string `json:"environments"`
}

// projectSceneAssets 读 bible_json.scene_assets（读不出时当作没有）。
func projectSceneAssets(bibleJSON string) map[string]dramaSceneAssets {
	var doc struct {
		SceneAssets map[string]dramaSceneAssets `json:"scene_assets"`
	}
	if raw := strings.TrimSpace(bibleJSON); raw != "" {
		var loose map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &loose)
		_ = json.Unmarshal(loose["scene_assets"], &doc.SceneAssets)
	}
	if doc.SceneAssets == nil {
		return map[string]dramaSceneAssets{}
	}
	return doc.SceneAssets
}

// sceneEnvironmentCandidate 是能绑到场次的一个场景资产：名字、别名，和提取时记的第一次出现的场编号。
type sceneEnvironmentCandidate struct {
	ID         string
	Title      string
	Aliases    []string
	FirstScene string
}

// unitSceneEnvironments 是这个单元每一场（项目场编号）绑的场景资产：地点对得上名字或别名的（完全相同的优先，其次互相包含），
// 都对不上时取 first_scene 记的是这一场的。一场只绑一个。
func unitSceneEnvironments(unit model.ProjectUnit, episode int, envs []sceneEnvironmentCandidate) map[string]dramaSceneAssets {
	out := map[string]dramaSceneAssets{}
	if episode <= 0 {
		return out
	}
	for _, sc := range drama.SplitScenes(chapterPlainText(unit.SourceText), unit.Title) {
		code := drama.SceneCode(episode, sc.No)
		pick := ""
		for _, e := range envs {
			if drama.AssetNameKey(dramaLocationKey(e.Title)) == drama.AssetNameKey(sc.Location) && drama.AssetNameKey(sc.Location) != "" {
				pick = e.ID
				break
			}
		}
		if pick == "" {
			for _, e := range envs {
				if drama.AssetMatchesLocation(e.Title, e.Aliases, sc.Location) {
					pick = e.ID
					break
				}
			}
		}
		if pick == "" {
			for _, e := range envs {
				if strings.EqualFold(e.FirstScene, code) {
					pick = e.ID
					break
				}
			}
		}
		if pick != "" {
			out[code] = dramaSceneAssets{UnitID: unit.ID, Environments: []string{pick}}
		}
	}
	return out
}

// mergeSceneAssetsDocument 把一个单元的场景绑定写进 bible_json.scene_assets：这个单元原来的条目换成这次的，别的单元的原样保留。
func mergeSceneAssetsDocument(bibleJSON string, unitID string, episode int, bindings map[string]dramaSceneAssets) (string, error) {
	doc := map[string]any{}
	if raw := strings.TrimSpace(bibleJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return "", fmt.Errorf("项目的连戏表不是合法 JSON，没有回填场景：%w", err)
		}
	}
	current := projectSceneAssets(bibleJSON)
	for code, entry := range current {
		ep, _, _ := drama.ParseSceneCode(code)
		if entry.UnitID == unitID || (entry.UnitID == "" && ep == episode) {
			delete(current, code)
		}
	}
	maps.Copy(current, bindings)
	encoded, _ := json.Marshal(current)
	var object map[string]any
	_ = json.Unmarshal(encoded, &object)
	doc["scene_assets"] = object
	if err := validateDramaBibleDocument(doc); err != nil {
		return "", err
	}
	out, err := json.Marshal(doc)
	return string(out), err
}

// ---------------------------------------------------------------- 装段：按场绑场景、子卡

// dramaSceneCast 是一个单元装段时按场换的资产：键是单元内场编号（E01S02，与镜头 spec.scene 同一套）。
type dramaSceneCast struct {
	// Variants 是这一场里主卡 ID → 子卡 ID。
	Variants map[string]map[string]string
	// Environments 是这一场绑的场景资产 ID（场景资产回填）。
	Environments map[string][]string
	// Fallback 是还没有三视图、而主卡有图的子卡 → 主卡：这些子卡不换上去，镜头里直接绑了它的也退回主卡（不留成没有人物参考图）。
	Fallback map[string]string
	// titles 是资产 ID → 标题（换 refs_hint 用），parents 是子卡 ID → 主卡 ID。
	titles  map[string]string
	parents map[string]string
}

func (c *dramaSceneCast) empty() bool {
	return c == nil || (len(c.Variants) == 0 && len(c.Environments) == 0 && len(c.Fallback) == 0)
}

// sceneCastOf 组一个单元的按场资产：子卡按它记的场（项目场编号）落到本单元的场，场景按 bible_json.scene_assets。不读库。
// 同一角色在同一场有几张子卡时取最具体的：用到的场最少的，再取状态（淋湿、受伤，通常叠在某套服装上）先于服装，再按标题。
// 子卡还没有三视图、主卡有图时不换（记进 Fallback）；主卡也没图时照样换（子卡的文字设定更贴这一场）。
func sceneCastOf(bibleJSON string, units []model.ProjectUnit, unitID string, assets []ProjectAssetSummary) *dramaSceneCast {
	_, numbers, _ := sceneUnits(units)
	episode := numbers[unitID]
	cast := &dramaSceneCast{Variants: map[string]map[string]string{}, Environments: map[string][]string{}, Fallback: map[string]string{}, titles: map[string]string{}, parents: map[string]string{}}
	imaged := map[string]bool{}
	for _, a := range assets {
		imaged[a.ID] = a.Character != nil && a.Character.VisualStatus == "ready"
	}
	live := map[string]bool{}
	byID := map[string]ProjectAssetSummary{}
	specific := func(a, b ProjectAssetSummary) bool {
		if len(a.Variant.Scenes) != len(b.Variant.Scenes) {
			return len(a.Variant.Scenes) < len(b.Variant.Scenes)
		}
		if (a.Variant.Kind == drama.AssetVariantState) != (b.Variant.Kind == drama.AssetVariantState) {
			return a.Variant.Kind == drama.AssetVariantState
		}
		return a.Title < b.Title
	}
	for _, a := range assets {
		if string(a.Status) == string(model.AssetVersionStatusArchived) || a.PrimaryVersionID == "" {
			continue
		}
		live[a.ID] = true
		cast.titles[a.ID] = a.Title
		if a.Variant == nil || episode <= 0 {
			continue
		}
		cast.parents[a.ID] = a.Variant.ParentID
		byID[a.ID] = a
		if !imaged[a.ID] && imaged[a.Variant.ParentID] {
			cast.Fallback[a.ID] = a.Variant.ParentID
			continue
		}
		for _, code := range a.Variant.Scenes {
			ep, no, ok := drama.ParseSceneCode(code)
			if !ok || ep != episode {
				continue
			}
			local := drama.SceneCode(1, no)
			if cast.Variants[local] == nil {
				cast.Variants[local] = map[string]string{}
			}
			if taken, ok := cast.Variants[local][a.Variant.ParentID]; !ok || specific(a, byID[taken]) {
				cast.Variants[local][a.Variant.ParentID] = a.ID
			}
		}
	}
	if episode > 0 {
		for code, entry := range projectSceneAssets(bibleJSON) {
			ep, no, ok := drama.ParseSceneCode(code)
			if !ok || ep != episode || (entry.UnitID != "" && entry.UnitID != unitID) {
				continue
			}
			for _, id := range entry.Environments {
				if live[id] {
					cast.Environments[drama.SceneCode(1, no)] = append(cast.Environments[drama.SceneCode(1, no)], id)
				}
			}
		}
	}
	return cast
}

// applySceneCast 按场换分镜行绑的资产（rows 与 shots 一一对应，原地改）：绑了主卡、这一场有子卡的换成子卡（characters 一起换），
// 镜头 refs_hint 里的主卡名换成子卡名；这一行一个场景都没绑的补上本场绑的场景资产。
func applySceneCast(rows []shotlistRow, shots []drama.Shot, cast *dramaSceneCast) {
	if cast.empty() {
		return
	}
	for i := range rows {
		if i >= len(shots) {
			break
		}
		scene := strings.ToUpper(strings.TrimSpace(shots[i].Scene))
		swaps := cast.Variants[scene]
		hasScene := false
		for child, parent := range cast.Fallback {
			for k, hint := range shots[i].RefsHint {
				if title := cast.titles[child]; title != "" && strings.TrimSpace(hint) == title {
					shots[i].RefsHint[k] = cast.titles[parent]
				}
			}
		}
		for j := range rows[i].Characters {
			if parent, ok := cast.Fallback[rows[i].Characters[j].CharacterAssetID]; ok {
				rows[i].Characters[j].CharacterAssetID = parent
			}
		}
		for j := range rows[i].AssetBindings {
			b := &rows[i].AssetBindings[j]
			if parent, ok := cast.Fallback[b.NodeID]; ok {
				b.NodeID = parent
			}
			if child, ok := swaps[b.NodeID]; ok {
				b.NodeID = child
			}
			if b.Role == "environment" || b.Role == "scene" {
				hasScene = true
			}
		}
		for j := range rows[i].Characters {
			c := &rows[i].Characters[j]
			if child, ok := swaps[c.CharacterAssetID]; ok {
				c.CharacterAssetID = child
			}
		}
		for parent, child := range swaps {
			parentTitle, childTitle := cast.titles[parent], cast.titles[child]
			if parentTitle == "" || childTitle == "" {
				continue
			}
			for k, hint := range shots[i].RefsHint {
				if strings.TrimSpace(hint) == parentTitle {
					shots[i].RefsHint[k] = childTitle
				}
			}
		}
		if envs := cast.Environments[scene]; !hasScene && len(envs) > 0 {
			for _, id := range envs {
				if !slices.ContainsFunc(rows[i].AssetBindings, func(b drama.RowAssetBinding) bool { return b.NodeID == id }) {
					rows[i].AssetBindings = append(rows[i].AssetBindings, drama.RowAssetBinding{NodeID: id, Role: "environment", Priority: 1})
				}
			}
		}
	}
}

// sceneCastPrompts 是生成分段每一轮带的【本场角色子卡】：这一场哪些角色用哪张子卡（键是单元内场编号）。
func sceneCastPrompts(cast *dramaSceneCast) map[string]string {
	out := map[string]string{}
	if cast.empty() {
		return out
	}
	for scene, swaps := range cast.Variants {
		var parts []string
		for _, parent := range slices.Sorted(maps.Keys(swaps)) {
			child := swaps[parent]
			if cast.titles[parent] != "" && cast.titles[child] != "" {
				parts = append(parts, fmt.Sprintf("%s 用「%s」", cast.titles[parent], cast.titles[child]))
			}
		}
		if len(parts) == 0 {
			continue
		}
		sort.Strings(parts)
		out[scene] = "【本场角色子卡】这一场" + strings.Join(parts, "；") + "：assets、characters 写子卡名（照抄「名·变体」），外貌按子卡的服装 / 状态写。"
	}
	return out
}

// unitSceneCast 读项目单元，组这个单元装段时按场换的资产。
func (s *Service) unitSceneCast(project *model.Project, unitID string, assets []ProjectAssetSummary) (*dramaSceneCast, error) {
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return nil, err
	}
	return sceneCastOf(project.BibleJSON, units, unitID, assets), nil
}
