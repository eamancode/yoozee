package app

import (
	"context"
	"encoding/json"
	"strings"

	"yingce/backend/internal/model"
)

type videoChainUploadContextKey struct{}
type videoChainUploadCheckpoint struct {
	IDs  map[string]string
	Save func() error
}

func containsVideoChainUpload(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for k, item := range v {
			if k == "_chainUploadId" || k == "_chainUploadScope" || k == videoChainMetadataKey || containsVideoChainUpload(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if containsVideoChainUpload(item) {
				return true
			}
		}
	}
	return false
}

func cloneVideoChainInput(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	return result
}

func (s *Service) videoChainReadyResource(userID, id, mime string) (*model.Resource, error) {
	resources, err := s.repo.ResourcesForUserIDs(userID, []string{id})
	if err != nil {
		return nil, err
	}
	if len(resources) != 1 || resources[0].Status != model.ResourceStatusReady || !strings.HasPrefix(resources[0].MimeType, mime) {
		return nil, BadAuthRequest("连续生成依赖的素材不存在、不可读或已失效")
	}
	return &resources[0], nil
}

func videoChainReference(resource *model.Resource, name string) map[string]any {
	return map[string]any{"id": resource.ID, "name": name, "storageKey": "resource:" + resource.ID, "type": resource.MimeType, "mimeType": resource.MimeType, "bytes": resource.Size, "width": resource.Width, "height": resource.Height, "durationMs": resource.DurationMs, "role": "reference"}
}

func (s *Service) videoChainInput(task *model.Task, step *videoChainStepState) (map[string]any, error) {
	input := cloneVideoChainInput(step.Input)
	marker := map[string]any{"controllerId": task.ID}
	if step.SourceShotID != "" {
		resource, err := s.videoChainReadyResource(task.UserID, step.FrameResourceID, "image/")
		if err != nil {
			return nil, err
		}
		images, _ := input["referenceImages"].([]any)
		input["referenceImages"] = append(images, videoChainReference(resource, step.ReferenceTag))
		marker["frameResourceId"], marker["referenceTag"] = resource.ID, step.ReferenceTag
		marker["sourceShotId"], marker["sourceVideoResourceId"] = step.SourceShotID, step.SourceVideoResourceID
		marker["sourceRevisionId"], marker["frameAssetId"] = step.SourceRevisionID, step.FrameAssetID
	}
	input[videoChainMetadataKey] = marker
	for _, group := range []string{"referenceImages", "referenceVideos", "referenceAudios"} {
		kind := map[string]string{"referenceImages": "image", "referenceVideos": "video", "referenceAudios": "audio"}[group]
		items, _ := input[group].([]any)
		for _, item := range items {
			media, _ := item.(map[string]any)
			if id := step.Uploads[kind+":"+stringValue(media["storageKey"])]; id != "" {
				media["_chainUploadId"] = id
				media["_chainUploadScope"] = step.UploadScope
			}
		}
	}
	return input, nil
}

// Preflight uses the same adapter/upload implementation as generation. Each
// successful upload is checkpointed before the next request, without creating a video.
func (s *Service) uploadVideoChainReferences(ctx context.Context, task *model.Task, state *videoChainState, step *videoChainStepState, input map[string]any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	var generation canvasGenerationInput
	if err = json.Unmarshal(raw, &generation); err != nil {
		return err
	}
	config, err := s.resolveProviderConfig(generation.Config)
	if err != nil {
		return err
	}
	generation.Config = config
	ctx = withProtocolRegistry(ctx, s.protocolRegistry())
	ctx = ensureOfficialProtocolAdapter(ctx, config.InterfaceType)
	if err = s.hydrateGenerationMedia(task.UserID, &generation, providerMediaHydrationPolicyFor(ctx, generation)); err != nil {
		return err
	}
	adapter, exists := declarativeProtocolAdapterForContext(ctx, config.InterfaceType)
	if !exists {
		return nil
	} // URL-input protocols consume the stored image URL directly.
	scope := videoChainUploadScope(config)
	if step.UploadScope != scope {
		step.Uploads = map[string]string{}
		step.UploadScope = scope
	}
	if step.Uploads == nil {
		step.Uploads = map[string]string{}
	}
	checkpoint := &videoChainUploadCheckpoint{IDs: step.Uploads, Save: func() error {
		raw, err := s.encodeVideoChainState(*state)
		if err != nil {
			return err
		}
		return s.repo.CheckpointProjectVideoChain(task, raw)
	}}
	ctx = context.WithValue(ctx, videoChainUploadContextKey{}, checkpoint)
	request := protocolRequestFromInput(generation)
	return uploadProtocolMedia(ctx, generation, adapter, &request)
}

func videoChainUploadScope(config providerConfig) string {
	return videoChainHash([]string{config.BaseURL, config.Model, config.ChannelID, config.InterfaceType, config.APIKey})
}
