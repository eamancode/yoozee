package app

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const assetSettingsImageVersion = "assetSettingsImageVersion"
const assetSettingsKeepShotVersions = "assetSettingsKeepShotVersions"
const assetSettingsEdited = "assetSettingsEdited"

type ProjectAssetSettingsPatch struct {
	ExpectedVersionID string            `json:"expectedVersionId"`
	Fields            map[string]string `json:"fields"`
}

func (s *Service) saveProjectAssetSettings(userID, projectID string, asset *model.Asset, patch ProjectAssetSettingsPatch) error {
	category := asset.Category
	character := category == model.AssetCategoryCharacter
	allowed := []string{"description", "prompt", "generationPrompt"}
	visual := allowed
	if character && asset.ParentAssetID == "" {
		allowed = []string{"role", "appearance", "identityAppearance", "clothing", "personality", "multiViewPrompt", "generationPrompt"}
		visual = []string{"appearance", "identityAppearance", "clothing", "multiViewPrompt"}
	} else if !projectVisualCategory(category) {
		return BadAuthRequest("请选择项目的人物主卡、场景或道具")
	}
	if patch.ExpectedVersionID == "" || len(patch.Fields) == 0 {
		return BadAuthRequest("请提供资产版本和已修改的设定")
	}
	if patch.ExpectedVersionID != asset.PrimaryVersionID {
		return assetSettingsConflict()
	}
	current, err := s.repo.AssetVersion(asset.PrimaryVersionID)
	if err != nil {
		return err
	}
	if current.AssetID != asset.ID {
		return BadAuthRequest("资产版本归属不一致")
	}
	definition := map[string]any{}
	if err := json.Unmarshal([]byte(current.DefinitionJSON), &definition); err != nil {
		return err
	}
	if definition == nil {
		definition = map[string]any{}
	}
	before := maps.Clone(definition)
	visualChanged := false
	for key, raw := range patch.Fields {
		if !slices.Contains(allowed, key) {
			return BadAuthRequest("不支持编辑的资产设定字段：" + key)
		}
		value := strings.TrimSpace(raw)
		if len(utf16.Encode([]rune(value))) > 16000 {
			return BadAuthRequest("每项资产设定最多 16000 字符")
		}
		if value == "" {
			delete(definition, key)
		} else {
			definition[key] = value
		}
		visualChanged = visualChanged || (slices.Contains(visual, key) && !reflect.DeepEqual(before[key], definition[key]))
	}
	shortPrompt := "prompt"
	structured := []string{"description"}
	if character {
		shortPrompt = "multiViewPrompt"
		structured = []string{"appearance", "identityAppearance", "clothing"}
	}
	if _, edited := patch.Fields[shortPrompt]; !edited && slices.ContainsFunc(structured, func(key string) bool {
		return !reflect.DeepEqual(before[key], definition[key])
	}) {
		delete(definition, shortPrompt)
	}
	if visualChanged {
		if _, edited := patch.Fields["generationPrompt"]; !edited {
			if before["generationPromptSource"] == "manual" && before["generationPrompt"] != nil {
				return BadAuthRequest("请同时更新或清除手工完整出图提示词")
			}
			delete(definition, "generationPrompt")
			delete(definition, "generationPromptSource")
		}
	}
	if _, edited := patch.Fields["generationPrompt"]; edited && !reflect.DeepEqual(before["generationPrompt"], definition["generationPrompt"]) {
		visualChanged = true
		if definition["generationPrompt"] != nil {
			definition["generationPromptSource"] = "manual"
		} else {
			delete(definition, "generationPromptSource")
		}
	}
	oldJSON, err := json.Marshal(before)
	if err != nil {
		return err
	}
	newJSON, err := json.Marshal(definition)
	if err != nil || string(oldJSON) == string(newJSON) {
		return err
	}
	now := time.Now()
	write := repository.DramaAssetWrite{ExpectedVersionID: current.ID, ExpectedUpdatedAt: &asset.UpdatedAt, KeepShotVersions: true}
	next := *asset
	var version model.AssetVersion
	if character {
		next, version, write.Representations, write.Voice, err = s.prepareNextCharacterVersion(asset, asset.Title, string(newJSON), nil, nil, false)
		if err != nil {
			return err
		}
	} else {
		versions, err := s.repo.AssetVersions(asset.ID)
		if err != nil {
			return err
		}
		number := current.Version + 1
		if len(versions) > 0 {
			number = versions[0].Version + 1
		}
		version = model.AssetVersion{ID: newID(), AssetID: asset.ID, Version: number, Status: current.Status, DefinitionJSON: string(newJSON), CreatedAt: now, UpdatedAt: now}
		write.Representations, err = s.repo.AssetRepresentations(current.ID)
		if err != nil {
			return err
		}
		op := newID()
		for i := range write.Representations {
			rep := &write.Representations[i]
			rep.ID, rep.TaskID, rep.AssetVersionID, rep.CreatedAt = newID(), op, version.ID, now
		}
		next.PrimaryVersionID, next.UpdatedAt = version.ID, now
	}
	version.Note = "编辑资产设定"
	payload := assetPayload(&next)
	payload["primaryVersionId"], payload["updatedAt"] = version.ID, next.UpdatedAt.Format(time.RFC3339Nano)
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
		payload["data"] = data
	}
	data["definition"] = definition
	payloadMetadata(payload)[assetSettingsKeepShotVersions] = true
	payloadMetadata(payload)[assetSettingsEdited] = true
	rememberAssetImageSettings(payload, current.ID, visualChanged, write.Representations)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	next.PayloadJSON = string(encoded)
	write.Asset, write.Version = &next, &version
	if err := s.repo.ApplyDramaAssetsExtract(userID, projectID, []repository.DramaAssetWrite{write}, nil, now); err != nil {
		if errors.Is(err, repository.ErrDramaAssetChanged) {
			return assetSettingsConflict()
		}
		return err
	}
	*asset = next
	return nil
}

func rememberAssetImageSettings(payload map[string]any, previousVersion string, changed bool, reps []model.AssetRepresentation) {
	if !changed || !slices.ContainsFunc(reps, func(rep model.AssetRepresentation) bool {
		return rep.ResourceID != "" && strings.HasPrefix(rep.MediaType, "image")
	}) {
		return
	}
	meta := payloadMetadata(payload)
	if meta[assetSettingsImageVersion] == nil {
		meta[assetSettingsImageVersion] = previousVersion
	}
}

func assetSettingsConflict() *AppError {
	return NewAppError(http.StatusConflict, "资产设定已被更新，请刷新后比较并保存")
}

func assetKeepsShotVersions(asset *model.Asset) bool {
	keep, _ := payloadMetadata(assetPayload(asset))[assetSettingsKeepShotVersions].(bool)
	return keep
}

// storyboardAssets uses the last aligned image and visual definition until a
// newly edited look has an image. Explicit shot bindings still select a version.
func (s *Service) storyboardAssets(userID, projectID string) ([]ProjectAssetSummary, error) {
	assets, err := s.ProjectAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, asset := range assets {
		if asset.ConfirmedImageVersion != "" {
			ids = append(ids, asset.ConfirmedImageVersion)
		}
	}
	if len(ids) == 0 {
		return assets, nil
	}
	versions, err := s.repo.ProjectAssetVersionsByIDs(projectID, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]model.AssetVersion, len(versions))
	for _, version := range versions {
		byID[version.ID] = version
	}
	for i, asset := range assets {
		version, ok := byID[asset.ConfirmedImageVersion]
		if !ok || version.AssetID != asset.ID {
			continue
		}
		var definition map[string]any
		if err := json.Unmarshal([]byte(version.DefinitionJSON), &definition); err != nil {
			return nil, err
		}
		if asset.Character != nil {
			asset.Character.Definition = definition
		}
		if asset.Visual != nil {
			asset.Visual.Definition = definition
		}
		asset.PrimaryVersionID = version.ID
		assets[i] = asset
	}
	return assets, nil
}

func (s *Service) saveProjectVariantSettings(userID, projectID string, child *model.Asset, version *model.AssetVersion, current, next map[string]any, meta assetVariantMeta, req DramaAssetVariantRequest, lookChanged bool) (DramaAssetVariantResult, error) {
	result := DramaAssetVariantResult{Images: emptyDramaImageQueueResult()}
	prompt, _ := current["generationPrompt"].(string)
	source, _ := current["generationPromptSource"].(string)
	promptChanged := false
	if req.GenerationPrompt != nil {
		replacement := strings.TrimSpace(*req.GenerationPrompt)
		if len(utf16.Encode([]rune(replacement))) > 16000 {
			return result, BadAuthRequest("完整出图提示词最多 16000 字符")
		}
		promptChanged = replacement != prompt
		prompt, source = replacement, "manual"
	} else if lookChanged {
		if source == "manual" && prompt != "" {
			return result, BadAuthRequest("请同时更新或清除手工完整出图提示词")
		}
		prompt, source = "", ""
	}
	edited := lookChanged || promptChanged || child.VariantLabel != req.Label || meta.Kind != req.Kind ||
		meta.StartAfter != req.StartAfter || !slices.Equal(meta.Scenes, req.Scenes)
	if !edited {
		var err error
		result.Asset, err = s.projectAssetSummary(userID, projectID, child)
		return result, err
	}
	if prompt != "" {
		next["generationPrompt"] = prompt
		if source != "" {
			next["generationPromptSource"] = source
		}
	}
	if !lookChanged {
		// Changes to the label and scene assignments do not recompile the inherited visual identity.
		clothing, _ := next["clothing"].(string)
		for key, value := range current {
			if key != "name" && key != "generationPrompt" && key != "generationPromptSource" {
				next[key] = value
			}
		}
		if child.VariantLabel != req.Label || meta.Kind != req.Kind {
			if prior, _ := current["clothing"].(string); variantLookPrefix.MatchString(prior) {
				next["clothing"] = clothing
			}
		}
	} else {
		for key, value := range current {
			if key == "age" || (next["age"] != nil && slices.Contains([]string{"appearance", "identityAppearance", "physique"}, key)) {
				continue
			}
			if _, inherited := next[key]; !inherited && key != "generationPrompt" && key != "generationPromptSource" {
				next[key] = value
			}
		}
		meta.VisualDelta = nil
		if age, _ := next["age"].(string); age != "" {
			meta.VisualDelta = &drama.AssetVisualDelta{AgeStage: age}
		}
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return result, err
	}
	title, _ := next["name"].(string)
	updated, revision := *child, *version
	now := time.Now()
	updated.UpdatedAt, updated.Title, updated.VariantLabel = now, title, req.Label
	revision.DefinitionJSON = string(raw)
	var reps []model.AssetRepresentation
	var voice *model.CharacterVoiceBinding
	visualChanged := lookChanged || promptChanged
	if visualChanged {
		updated, revision, reps, voice, err = s.prepareNextCharacterVersion(child, title, string(raw), nil, nil, false)
		if err != nil {
			return result, err
		}
		updated.VariantLabel = req.Label
	}
	meta.Kind, meta.Look, meta.PromptEn, meta.GenerationPrompt = req.Kind, req.Look, req.PromptEn, prompt
	if !slices.Equal(meta.Scenes, req.Scenes) || meta.StartAfter != req.StartAfter {
		meta.Occurrences = nil
		meta.Source = "manual"
	}
	meta.Scenes, meta.StartAfter = req.Scenes, req.StartAfter
	payload := assetPayload(&updated)
	payload["title"], payload["updatedAt"] = title, updated.UpdatedAt.Format(time.RFC3339Nano)
	if data, ok := payload["data"].(map[string]any); ok {
		data["definition"] = next
	}
	payloadMetadata(payload)[assetVariantKey] = meta
	payloadMetadata(payload)[assetSettingsKeepShotVersions] = true
	payloadMetadata(payload)[assetSettingsEdited] = true
	rememberAssetImageSettings(payload, version.ID, visualChanged, reps)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return result, err
	}
	updated.PayloadJSON = string(encoded)
	write := repository.DramaAssetWrite{Asset: &updated, Version: &revision, ExpectedVersionID: version.ID, ExpectedUpdatedAt: &child.UpdatedAt, Representations: reps, Voice: voice, KeepShotVersions: true, DefinitionOnly: !visualChanged}
	if err := s.repo.ApplyDramaAssetsExtract(userID, projectID, []repository.DramaAssetWrite{write}, nil, updated.UpdatedAt); err != nil {
		if errors.Is(err, repository.ErrDramaAssetChanged) {
			return result, assetSettingsConflict()
		}
		return result, err
	}
	if visualChanged && req.ImageTask.usable() {
		if result.Images, err = s.queueDramaAssetImages(userID, projectID, *req.ImageTask, "variant", []string{child.ID}); err != nil {
			return result, err
		}
	}
	asset, err := s.repo.AssetForUser(userID, child.ID)
	if err != nil {
		return result, err
	}
	result.Asset, err = s.projectAssetSummary(userID, projectID, asset)
	return result, err
}
