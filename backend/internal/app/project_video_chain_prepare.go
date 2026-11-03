package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func (s *Service) prepareProjectVideoChain(userID, projectID string, req ProjectVideoChainRequest) (videoChainState, error) {
	state := videoChainState{UnitID: req.UnitID, AnchorShotID: req.ShotID, Request: req, Steps: []videoChainStepState{}}
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return state, err
	}
	if _, err := s.repo.ProjectUnit(projectID, req.UnitID); err != nil {
		return state, err
	}
	shots, err := s.repo.ProjectUnitShots(projectID, req.UnitID)
	if err != nil {
		return state, err
	}
	revisions := make(map[string]*model.ShotRevision, len(shots))
	anchor := -1
	for i := range shots {
		revision, err := s.repo.ShotRevisionForShot(shots[i].ID, shots[i].CurrentRevisionID)
		if err != nil {
			return state, err
		}
		revisions[shots[i].ID] = revision
		if shots[i].ID == req.ShotID {
			anchor = i
		}
	}
	if anchor < 0 {
		return state, BadAuthRequest("选中的镜头组不属于当前章节")
	}
	scene := segmentSubmissionContent(shots[anchor], revisions[shots[anchor].ID]).Scene
	first, last := anchor, anchor
	if scene != "" {
		for first > 0 && segmentSubmissionContent(shots[first-1], revisions[shots[first-1].ID]).Scene == scene {
			first--
		}
		for last+1 < len(shots) && segmentSubmissionContent(shots[last+1], revisions[shots[last+1].ID]).Scene == scene {
			last++
		}
	}
	if last-first+1 > 200 {
		return state, BadAuthRequest("本场镜头组超过 200 组，请先按叙事划分场景")
	}
	feature, err := s.FeatureEnabled(FeatureFrontendModels)
	if err != nil {
		return state, err
	}
	previousSignature := ""
	for i := first; i <= last; i++ {
		shot := shots[i]
		revision := revisions[shot.ID]
		choice := req.Model
		if selected, ok := req.Models[shot.ID]; ok {
			choice = selected
		}
		if strings.TrimSpace(choice.Model) == "" {
			return state, BadAuthRequest("请为每个镜头组选定视频模型")
		}
		raw, err := json.Marshal(choice.Input)
		if err != nil {
			return state, err
		}
		var input map[string]any
		if err = json.Unmarshal(raw, &input); err != nil || input == nil {
			return state, BadAuthRequest("连续生成的模型配置无效")
		}
		input["mode"] = "video"
		delete(input, "metadata")
		delete(input, videoChainMetadataKey)
		if containsVideoChainUpload(input) {
			return state, BadAuthRequest("不能指定连续生成内部上传标识")
		}
		delete(input, "referenceImages")
		delete(input, "referenceVideos")
		delete(input, "referenceAudios")
		config, _ := input["config"].(map[string]any)
		if config == nil {
			return state, BadAuthRequest("连续生成缺少视频模型配置")
		}
		if config["channelId"] == nil {
			config["channelId"] = ""
		}
		content := segmentSubmissionContent(shot, revision)
		if !isPositiveVideoSeconds(content.Dur) {
			return state, BadAuthRequest("镜头组时长必须大于 0")
		}
		compiled, err := s.CompileProjectShot(userID, projectID, shot.ID, CompileProjectShotRequest{})
		if err != nil {
			return state, err
		}
		for _, issue := range compiled.Result.Issues {
			if issue.Level == "error" {
				return state, BadAuthRequest(fmt.Sprintf("%s 分段检查失败：%s", shot.Title, issue.Message))
			}
		}
		input["prompt"] = compiled.Result.Prompt
		images, videos, audios, err := s.videoChainCompiledReferences(userID, compiled.Result.Files)
		if err != nil {
			return state, err
		}
		input["referenceImages"], input["referenceVideos"], input["referenceAudios"] = images, videos, audios
		operation := "text_to_video"
		if len(images) > 0 {
			operation = "reference_to_video"
		}
		if len(videos) > 0 {
			operation = "video_to_video"
		}
		hasPosition := i > first
		if !hasPosition && len(videos) == 0 {
			for _, item := range images {
				media, _ := item.(map[string]any)
				if role := stringValue(media["role"]); role == "first_frame" || role == "last_frame" {
					operation = "image_to_video"
				}
			}
		}
		if hasPosition && operation == "text_to_video" {
			operation = "reference_to_video"
		}
		if hasPosition {
			images = append(images, map[string]any{"id": "pending-position", "role": "reference", "type": "image/png", "mimeType": "image/png"})
			input["referenceImages"] = images
		}
		input["metadata"] = map[string]any{"domainProjectId": projectID, "unitId": req.UnitID, "shotId": shot.ID, "shotRevisionId": shot.CurrentRevisionID, "artifactType": "video", "role": "output", "source": "project-video-chain", "videoEditOperation": operation, "artifactMetadata": map[string]any{"model": choice.Model, "contentDurationSeconds": content.Dur}}
		if err := s.enforceDramaPrevizGate(userID, input); err != nil {
			return state, err
		}
		// Route resolution owns channel credentials and capabilities. The second
		// resolution uses the normalized slot to keep routing and price tiers aligned.
		_, input, err = s.resolveTaskModelSelection(userID, input, choice.LogicalModelID, "canvas_video", operation, feature)
		if err != nil {
			return state, err
		}
		profile, err := s.projectTaskVideoProfile(input)
		if err != nil {
			return state, err
		}
		if profile == nil {
			return state, BadAuthRequest("视频模型未声明参考图片能力，暂不能开始连续生成")
		}
		slots := projectVideoDurationSlots(profile.Duration)
		seconds, err := projectVideoSubmissionSeconds(slots, content.Dur)
		if err != nil {
			return state, err
		}
		generation := content.Dur
		if seconds > 0 {
			generation = float64(seconds)
		}
		config = input["config"].(map[string]any)
		config["videoSeconds"] = fmt.Sprint(generation)
		if options, ok := input["capabilityOptions"].(map[string]any); ok {
			options["videoSeconds"] = generation
		}
		if hasPosition {
			if !containsCapabilityString(profile.Operations, "reference_to_video") && operation != "video_to_video" {
				return state, BadAuthRequest(fmt.Sprintf("%s 的模型不支持站位参考图输入，请选择支持参考生视频的模型", shot.Title))
			}
		}
		if len(images) > profile.References.MaxImages {
			return state, BadAuthRequest(fmt.Sprintf("%s 需要保留 %d 张原参考图并新增 1 张站位图，超过模型最多 %d 张的限制；请调整参考图或模型", shot.Title, len(images)-1, profile.References.MaxImages))
		}
		m, err := projectDramaModel(project)
		if err != nil {
			return state, err
		}
		tag := ""
		if hasPosition {
			tag = drama.ReferenceImageTag(m, len(images))
		}
		original := compiled.Result.Prompt
		finalPrompt := original
		if hasPosition {
			finalPrompt = videoChainPositionPrompt(tag) + "\n\n" + finalPrompt
		}
		ending := i < last
		if ending && !strings.Contains(finalPrompt, dramaSceneEnding) {
			finalPrompt += "\n\n" + dramaSceneEnding
		}
		input["prompt"] = finalPrompt
		_, input, err = s.resolveTaskModelSelection(userID, input, choice.LogicalModelID, "canvas_video", operation, feature)
		if err != nil {
			return state, err
		}
		if err = s.enforceDramaModelFamily(userID, input); err != nil {
			return state, err
		}
		if err = s.ValidateTaskCapability(input); err != nil {
			return state, err
		}
		if err = s.requireCustomChannelsForTaskInput(input); err != nil {
			return state, err
		}
		var quote *LogicalModelQuote
		intent := ModelRequestIntentFromTaskInput(input, "canvas_video", operation)
		if choice.LogicalModelID != "" {
			quote, err = s.QuoteLogicalModel(userID, choice.LogicalModelID, intent)
		} else {
			config = input["config"].(map[string]any)
			channelID := stringValue(config["channelId"])
			if channelID != "" {
				quote, err = s.QuoteChannelModel(userID, ChannelModelQuoteRequest{ChannelID: channelID, ModelKey: stringValue(config["model"]), Intent: intent})
			}
		}
		if err != nil {
			return state, err
		}
		// The placeholder participates only in capability/quote checks. Runtime
		// adds the actual predecessor frame and compiles its real reference tag.
		if hasPosition {
			images = images[:len(images)-1]
		}
		input["referenceImages"] = images
		input["prompt"] = original
		signature := videoChainHash([]any{shot.ID, shot.CurrentRevisionID, scene, content.Dur, ending, input, previousSignature})
		step := videoChainStepState{ProjectVideoChainStep: ProjectVideoChainStep{ShotID: shot.ID, RevisionID: shot.CurrentRevisionID, Title: shot.Title, Scene: scene, ContentSeconds: content.Dur, GenerationSeconds: generation, Model: choice.Model, Status: "waiting", Phase: "waiting", ReferenceCount: len(images), ReferenceTag: tag, Ending: ending, Quote: quote}, Input: input, LogicalModelID: choice.LogicalModelID, Signature: signature, Uploads: map[string]string{}}
		if hasPosition {
			step.SourceShotID = shots[i-1].ID
		}
		state.Steps = append(state.Steps, step)
		previousSignature = signature
	}
	fingerprint := []any{projectID, req.UnitID}
	for _, step := range state.Steps {
		fingerprint = append(fingerprint, step.Signature, step.Quote)
	}
	state.PreviewHash = videoChainHash(fingerprint)
	return state, nil
}

func (s *Service) videoChainCompiledReferences(userID string, files []drama.CompiledFile) ([]any, []any, []any, error) {
	images, videos, audios := []any{}, []any{}, []any{}
	for _, file := range files {
		var resource *model.Resource
		direct, err := s.repo.ResourcesForUserIDs(userID, []string{file.File})
		if err != nil {
			return nil, nil, nil, err
		}
		if len(direct) > 0 {
			resource = &direct[0]
		} else {
			representations, err := s.repo.AssetRepresentationsByVersionIDs([]string{file.File})
			if err != nil {
				return nil, nil, nil, err
			}
			for _, representation := range representations {
				candidates, err := s.repo.ResourcesForUserIDs(userID, []string{representation.ResourceID})
				if err != nil {
					return nil, nil, nil, err
				}
				if len(candidates) == 0 {
					continue
				}
				candidate := candidates[0]
				wanted := "image/"
				if file.Role == "motion" {
					wanted = "video/"
				}
				if file.Role == "voice" {
					wanted = "audio/"
				}
				if strings.HasPrefix(candidate.MimeType, wanted) {
					resource = &candidate
					if representation.Role == "turnaround_sheet" || representation.Role == "primary" {
						break
					}
				}
			}
		}
		if resource == nil || resource.Status != model.ResourceStatusReady {
			return nil, nil, nil, BadAuthRequest("镜头参考素材尚未就绪，不能省略后继续生成")
		}
		reference := map[string]any{"id": file.File, "name": file.Tag, "storageKey": "resource:" + resource.ID, "type": resource.MimeType, "mimeType": resource.MimeType, "bytes": resource.Size, "width": resource.Width, "height": resource.Height, "durationMs": resource.DurationMs, "role": "reference"}
		if file.Role == "first_frame" || file.Role == "last_frame" {
			reference["role"] = file.Role
		}
		switch {
		case strings.HasPrefix(resource.MimeType, "image/"):
			images = append(images, reference)
		case strings.HasPrefix(resource.MimeType, "video/"):
			videos = append(videos, reference)
		case strings.HasPrefix(resource.MimeType, "audio/"):
			audios = append(audios, reference)
		default:
			return nil, nil, nil, BadAuthRequest("镜头参考素材类型不受支持")
		}
	}
	return images, videos, audios, nil
}
