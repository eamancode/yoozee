package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

type variantOccurrence struct {
	UnitID     string `json:"unitId"`
	Scene      int    `json:"scene"`
	StartAfter string `json:"startAfter,omitempty"`
}

type projectAssetOccurrence struct {
	AssetID    string `json:"assetId"`
	Scene      int    `json:"scene"`
	StartAfter string `json:"startAfter,omitempty"`
}

func assetExtractionSourceHash(unit *model.ProjectUnit) string {
	sum := sha256.Sum256([]byte(unit.Title + "\x00" + chapterPlainText(unit.SourceText)))
	return hex.EncodeToString(sum[:])
}

func seriesSpeakerHints(unit *model.ProjectUnit, units []model.ProjectUnit) []string {
	sorted, numbers, _ := sceneUnits(units)
	if numbers[unit.ID] == 0 || len(numbers) < 2 {
		return nil
	}
	speakers := drama.ScriptSpeakers(drama.ScriptFromText(chapterPlainText(unit.SourceText), unit.Title))
	names := make(map[string][]int, len(speakers))
	for _, name := range speakers {
		if !ambiguousCharacterAlias(name) && drama.AssetNameKey(name) != "" {
			names[drama.AssetNameKey(name)] = nil
		}
	}
	if len(names) == 0 {
		return nil
	}
	for _, other := range sorted {
		episode := numbers[other.ID]
		if episode == 0 || other.ID == unit.ID {
			continue
		}
		for _, speaker := range drama.ScriptSpeakers(drama.ScriptFromText(chapterPlainText(other.SourceText), other.Title)) {
			key := drama.AssetNameKey(speaker)
			if seen, ok := names[key]; ok && !slices.Contains(seen, episode) {
				names[key] = append(seen, episode)
			}
		}
	}
	var hints []string
	for _, name := range speakers {
		if episodes := names[drama.AssetNameKey(name)]; len(episodes) > 0 {
			hints = append(hints, fmt.Sprintf("- %s：还在这些制作用单元中出现为说话者 %v", name, episodes))
		}
	}
	return hints
}

type projectAssetCoverage struct {
	SourceHash   string `json:"sourceHash"`
	Contract     string `json:"contract"`
	SkillVersion string `json:"skillVersion"`
}

type projectAssetContinuityDocument struct {
	Coverage    map[string]projectAssetCoverage     `json:"asset_coverage"`
	Occurrences map[string][]projectAssetOccurrence `json:"asset_occurrences"`
}

func (s *Service) projectAssetExtractionQueue(userID string, project *model.Project, selectedID string) ([]string, error) {
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return nil, err
	}
	sorted, numbers, _ := sceneUnits(units)
	if numbers[selectedID] == 0 {
		return nil, BadAuthRequest("请选择制作用剧集或章节")
	}
	usage, err := s.DramaStepUsage(userID, project.ID, drama.StepAssetsExtract, nil)
	if err != nil {
		return nil, err
	}
	var bible projectAssetContinuityDocument
	if err := json.Unmarshal([]byte(project.BibleJSON), &bible); err != nil && strings.TrimSpace(project.BibleJSON) != "" {
		return nil, err
	}
	palettes, err := s.projectPaletteAssets(userID, project.ID)
	if err != nil {
		return nil, err
	}
	_, colorScript := projectStyleBible(project.BibleJSON)
	table := styleSceneTable(units)
	paletteReady := map[string]bool{}
	for _, entry := range colorScript {
		if asset, ok := palettes[entry.Code()]; ok && scenePaletteUsable(&asset) && colorScriptCurrent(entry, table) {
			paletteReady[table[entry.Code()].UnitID] = true
		}
	}
	queue := make([]string, 0, len(sorted))
	missingPalette := false
	for _, unit := range sorted {
		if numbers[unit.ID] == 0 || chapterPlainText(unit.SourceText) == "" {
			continue
		}
		covered := bible.Coverage[unit.ID]
		if covered == (projectAssetCoverage{SourceHash: assetExtractionSourceHash(&unit), Contract: usage.Contract, SkillVersion: usage.Skill.ContentHash}) {
			continue
		}
		if !paletteReady[unit.ID] {
			missingPalette = true
			continue
		}
		queue = append(queue, unit.ID)
	}
	if len(queue) == 0 {
		if missingPalette {
			return nil, BadAuthRequest("尚未覆盖的剧集缺少场次色卡，请先生成场次色卡")
		}
		return nil, BadAuthRequest("全剧已提取的内容没有变化。可选择仅处理本集，重新提取当前剧集")
	}
	return queue, nil
}

func appendVariantOccurrences(meta *assetVariantMeta, unitID string, scenes []int, startAfter string, episode int) bool {
	if len(meta.Occurrences) == 0 {
		for _, code := range meta.Scenes {
			if ep, scene, ok := drama.ParseSceneCode(code); ok && ep == episode {
				meta.Occurrences = append(meta.Occurrences, variantOccurrence{UnitID: unitID, Scene: scene, StartAfter: meta.StartAfter})
			}
		}
	}
	changed := false
	for _, scene := range scenes {
		occurrence := variantOccurrence{UnitID: unitID, Scene: scene, StartAfter: startAfter}
		if scene > 0 && !slices.Contains(meta.Occurrences, occurrence) {
			meta.Occurrences = append(meta.Occurrences, occurrence)
			changed = true
		}
	}
	return changed
}

func (x *dramaAssetIndex) resolveLook(parentID string, variant drama.AssetsVariant) (*dramaAssetEntry, bool) {
	var labelMatches, visualMatches []*dramaAssetEntry
	for _, child := range x.children[parentID] {
		if variant.AssetID != "" {
			if variant.AssetID == child.asset.ID {
				if variant.Delta.Key() != "" && !variant.Delta.Matches(assetVariantMetaOf(&child.asset).VisualDelta) {
					return nil, true
				}
				return child, false
			}
			continue
		}
		meta := assetVariantMetaOf(&child.asset)
		if drama.AssetVariantKey(child.asset.VariantLabel) == drama.AssetVariantKey(variant.Label) {
			labelMatches = append(labelMatches, child)
		}
		look, _ := variantLookOf(meta, child.definition)
		incoming, stored := variant.Delta.Key(), meta.VisualDelta.Key()
		if incoming != "" && stored != "" {
			if variant.Delta.Matches(meta.VisualDelta) {
				visualMatches = append(visualMatches, child)
			}
		} else if incoming != "" && drama.AssetNameKey(look) != "" && drama.AssetNameKey(look) == drama.AssetNameKey(variant.Appearance) {
			visualMatches = append(visualMatches, child)
		}
	}
	if variant.AssetID != "" || len(visualMatches) > 1 {
		return nil, true
	}
	if len(visualMatches) == 1 {
		return visualMatches[0], false
	}
	if len(labelMatches) == 1 && variant.Delta.Key() == "" && variant.Appearance == "" && variant.PromptEn == "" {
		return labelMatches[0], false
	}
	return nil, len(labelMatches) > 0
}

func ambiguousCharacterAlias(name string) bool {
	switch drama.AssetNameKey(name) {
	case "他", "她", "男人", "女人", "孩子", "妈", "妈妈", "母亲", "爸", "爸爸", "父亲", "老师", "工友", "店员", "顾客":
		return true
	default:
		return false
	}
}

func (x *dramaAssetIndex) needsIdentityReview(row drama.AssetsExtractRow) bool {
	if ambiguousCharacterAlias(row.Name) {
		return true
	}
	for _, name := range append([]string{row.Name}, row.Aliases...) {
		if entry, exists := x.byKey[row.Category+":"+drama.AssetNameKey(name)]; exists && entry == nil {
			return true
		}
	}
	return false
}

func supportedVisualDelta(variant drama.AssetsVariant, scenes []drama.ShotsScene, source string) bool {
	if variant.Delta.Key() == "" || strings.TrimSpace(variant.Delta.Evidence) == "" {
		return false
	}
	evidence := normalizedAssetEvidence(variant.Delta.Evidence)
	if evidence == "" {
		return false
	}
	for _, scene := range variant.Scenes {
		body := source
		if scene > 0 && scene <= len(scenes) {
			body = scenes[scene-1].AssetSourceText
		}
		if strings.Contains(normalizedAssetEvidence(body), evidence) {
			return true
		}
	}
	return false
}

func variantBoundaryInScenes(variant drama.AssetsVariant, scenes []drama.ShotsScene, source string) bool {
	anchor := normalizedAssetEvidence(variant.StartAfter)
	if anchor == "" {
		return true
	}
	if len(scenes) == 0 {
		return slices.Equal(variant.Scenes, []int{1}) && strings.Contains(normalizedAssetEvidence(source), anchor)
	}
	for _, scene := range variant.Scenes {
		if scene < 1 || scene > len(scenes) || !strings.Contains(normalizedAssetEvidence(scenes[scene-1].AssetSourceText), anchor) {
			return false
		}
	}
	return true
}

func normalizedAssetEvidence(value string) string {
	return strings.Join(strings.Fields(strings.Trim(strings.TrimSpace(value), "「」“”\"")), "")
}

func mergeProjectAssetOccurrences(raw, unitID string, occurrences []projectAssetOccurrence, coverage projectAssetCoverage) (string, error) {
	doc := map[string]json.RawMessage{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return "", err
		}
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	byUnit := map[string][]projectAssetOccurrence{}
	if rawOccurrences := doc["asset_occurrences"]; len(rawOccurrences) != 0 {
		if err := json.Unmarshal(rawOccurrences, &byUnit); err != nil {
			return "", err
		}
		if byUnit == nil {
			byUnit = map[string][]projectAssetOccurrence{}
		}
	}
	byUnit[unitID] = occurrences
	encoded, err := json.Marshal(byUnit)
	if err != nil {
		return "", err
	}
	doc["asset_occurrences"] = encoded
	if coverage.SourceHash != "" {
		byUnit := map[string]projectAssetCoverage{}
		if rawCoverage := doc["asset_coverage"]; len(rawCoverage) != 0 {
			if err := json.Unmarshal(rawCoverage, &byUnit); err != nil {
				return "", err
			}
			if byUnit == nil {
				byUnit = map[string]projectAssetCoverage{}
			}
		}
		byUnit[unitID] = coverage
		doc["asset_coverage"], err = json.Marshal(byUnit)
		if err != nil {
			return "", err
		}
	}
	output, err := json.Marshal(doc)
	return string(output), err
}
