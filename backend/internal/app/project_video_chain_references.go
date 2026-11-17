package app

import (
	"errors"
	"fmt"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func videoChainBindingCapabilityError(err error, audioCount int) error {
	var mismatch *ModelError
	if audioCount > 0 && errors.As(err, &mismatch) && mismatch.ErrorCode == ErrCodeModelCapabilityNotSupported {
		issues, _ := mismatch.Details["capabilityIssues"].([]CapabilityIssue)
		for _, issue := range issues {
			if issue.Kind == "input" && issue.Name == "audio" {
				voiceErr := videoChainAudioLimit(audioCount, issue.Reason)
				voiceErr.Details["capabilityIssues"] = issues
				return voiceErr
			}
		}
	}
	return err
}

func videoChainAudioLimit(count int, reason string) *ModelError {
	message := fmt.Sprintf("本段需发送 %d 条声音参考，%s。请更换视频线路或调整本段角色绑定", count, reason)
	return NewModelError(ErrCodeModelCapabilityNotSupported, message).WithDetails(map[string]any{
		"field": "referenceAudios", "issue": "item_count", "message": message,
	})
}

func (s *Service) videoChainCompiledReferences(userID, projectID string, files []drama.CompiledFile) ([]any, []any, []any, error) {
	fileIDs := make([]string, 0, len(files))
	for _, file := range files {
		fileIDs = append(fileIDs, file.File)
	}
	versions, err := s.repo.ProjectAssetVersionsByIDs(projectID, fileIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	versionByID := make(map[string]*model.AssetVersion, len(versions))
	for i := range versions {
		versionByID[versions[i].ID] = &versions[i]
	}
	media := map[string][]any{"image": {}, "video": {}, "audio": {}}
	for _, file := range files {
		kind := "image"
		switch file.Role {
		case "voice":
			kind = "audio"
		case "motion":
			kind = "video"
		}
		id := file.File
		if version := versionByID[id]; version != nil {
			id, err = s.videoChainVersionResourceID(userID, projectID, version, kind)
			if err != nil {
				return nil, nil, nil, err
			}
		}
		resource, err := s.videoChainReadyResource(userID, id, kind+"/")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s：%w", file.Tag, err)
		}
		reference := videoChainReference(resource, file.Tag)
		if file.Role == "first_frame" || file.Role == "last_frame" {
			reference["role"] = file.Role
		}
		media[kind] = append(media[kind], reference)
	}
	return media["image"], media["video"], media["audio"], nil
}

func (s *Service) videoChainVersionResourceID(userID, projectID string, version *model.AssetVersion, kind string) (string, error) {
	if kind == "audio" {
		// Older manifests name the character version in a voice slot. The binding
		// on that version is authoritative even after the current version changes.
		samples, err := s.repo.CharacterVoiceSamples(userID, projectID, []string{version.ID})
		return samples[version.ID], err
	}
	representations, err := s.repo.AssetRepresentations(version.ID)
	if err != nil {
		return "", err
	}
	// Keep the same priority as projectAssetReferenceImage in the shot editor.
	for _, role := range []string{"turnaround_sheet", "primary", "front", ""} {
		for _, rep := range representations {
			if (role == "" || rep.Role == role) && strings.HasPrefix(rep.MediaType, kind) && rep.ResourceID != "" {
				return rep.ResourceID, nil
			}
		}
	}
	asset, err := s.repo.AssetForUser(userID, version.AssetID)
	if err != nil || asset.PrimaryVersionID != version.ID || asset.Kind != kind {
		return "", err
	}
	storageKey, _, _, _ := projectAssetPreview(asset.PayloadJSON)
	if id, ok := strings.CutPrefix(storageKey, "resource:"); ok {
		return id, nil
	}
	return "", nil
}
