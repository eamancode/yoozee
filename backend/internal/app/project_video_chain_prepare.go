package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func (s *Service) prepareProjectVideoChain(userID, projectID string, req ProjectVideoChainRequest) (videoChainState, error) {
	return s.prepareProjectVideoChainWithWhiteModel(userID, projectID, req, nil)
}

func (s *Service) prepareProjectVideoChainWithWhiteModel(userID, projectID string, req ProjectVideoChainRequest, snapshot *bool) (videoChainState, error) {
	return s.prepareVideoChainSteps(userID, projectID, req, snapshot, nil)
}

func (s *Service) prepareVideoChainSteps(userID, projectID string, req ProjectVideoChainRequest, snapshot *bool, completed []videoChainStepState) (videoChainState, error) {
	state := videoChainState{UnitID: req.UnitID, AnchorShotID: req.ShotID, Request: req, Steps: []videoChainStepState{}}
	if req.PrepareOnly && req.Scope != "single" {
		return state, BadAuthRequest("仅准备站位参考必须指定单组范围，不会生成前序视频")
	}
	if req.Scope != "" && req.Scope != "scene" && req.Scope != "single" {
		return state, BadAuthRequest("不支持的镜头生成范围")
	}
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return state, err
	}
	var selectedRoute *model.ProjectVideoRoute
	if !req.PrepareOnly {
		selectedRoute, err = s.repo.ProjectVideoRoute(projectID)
		if err != nil {
			return state, err
		}
		state.RouteRevision = projectVideoRoutingRevision(selectedRoute)
		if selectedRoute != nil && selectedRoute.Status == "unavailable" {
			return state, failedPrecondition(selectedRoute.Reason + "。请先手动选择视频线路")
		}
	}
	enabled := project.WhiteModelStoryboardEnabled
	if snapshot != nil {
		enabled = *snapshot
	}
	state.WhiteModelStoryboardEnabled = &enabled
	workflow, _ := drama.Workflow(project.WorkflowID)
	sss := workflow.StoryboardMode == drama.StoryboardModeSSS
	if req.PrepareOnly && !enabled && !sss {
		return state, failedPrecondition("白模分镜已关闭，无需准备站位参考")
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
	sceneFirst, sceneLast := first, last
	if req.PrepareOnly && anchor == sceneFirst {
		return state, failedPrecondition("当前镜头组没有相邻的同场景前序，无需准备站位参考")
	}
	if req.Scope == "single" {
		first, last = anchor, anchor
	}
	if len(completed) > last-first+1 {
		return state, failedPrecondition("已完成段的分镜顺序已变化，请检查原链的镜头顺序")
	}
	feature, err := s.FeatureEnabled(FeatureFrontendModels)
	if err != nil {
		return state, err
	}
	previousSignature := ""
	for i := first; i <= last; i++ {
		shot := shots[i]
		if index := i - first; index < len(completed) {
			// Completed work is a checkpoint. Its original revision, model and
			// output remain the dependency even when that route is now offline.
			step := completed[index]
			if step.ShotID != shot.ID {
				return state, failedPrecondition("已完成段的分镜顺序已变化，请检查原链的镜头顺序")
			}
			state.Steps = append(state.Steps, step)
			previousSignature = step.Signature
			continue
		}
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
			return state, videoChainSpecError(BadAuthRequest("镜头组时长必须大于 0"), shot.ID, choice.Model, content.Dur, 0, -1, nil)
		}
		compiled, err := s.CompileProjectShot(userID, projectID, shot.ID, CompileProjectShotRequest{ChannelModel: choice.Model})
		if err != nil {
			return state, videoChainSpecError(err, shot.ID, choice.Model, content.Dur, 0, -1, nil)
		}
		input["prompt"] = compiled.Result.Prompt
		images, videos, audios, err := s.videoChainCompiledReferences(userID, projectID, compiled.Result.Files)
		if err != nil {
			return state, videoChainSpecError(err, shot.ID, choice.Model, content.Dur, 0, -1, nil)
		}
		input["referenceImages"], input["referenceVideos"], input["referenceAudios"] = images, videos, audios
		operation := "text_to_video"
		if len(images) > 0 {
			operation = "reference_to_video"
		}
		if len(videos) > 0 {
			operation = "video_to_video"
		}
		hasPosition := enabled && i > sceneFirst
		freezeReference := sss && strings.TrimSpace(compiled.Spec.Verbatim) != ""
		if freezeReference {
			hasPosition = false
			if i > sceneFirst {
				var prev drama.Segment
				if err := json.Unmarshal([]byte(revisions[shots[i-1].ID].SpecJSON), &prev); err != nil {
					return state, err
				}
				hasPosition = drama.SegmentEndsWithFreeze(prev)
			}
			if req.PrepareOnly && !hasPosition {
				return state, failedPrecondition("上一段没有同场景定格，无需准备站位参考")
			}
		}
		var prior *model.ShotArtifact
		referenceWarning := ""
		usePosition := hasPosition
		if hasPosition && req.Scope == "single" {
			prior, err = s.videoChainExistingPredecessor(userID, projectID, shots[i-1], freezeReference)
			if err != nil {
				return state, err
			}
			if prior == nil {
				if freezeReference {
					return state, failedPrecondition("上一段视频尚未完成，无法提交同场景后续段")
				}
				usePosition = false
				referenceWarning = "上一组没有可用的白模来源视频，本次使用原正文与参考图"
			}
		}
		if !hasPosition && len(videos) == 0 {
			for _, item := range images {
				media, _ := item.(map[string]any)
				if role := stringValue(media["role"]); role == "first_frame" || role == "last_frame" {
					operation = "image_to_video"
				}
			}
		}
		input["metadata"] = map[string]any{"domainProjectId": projectID, "unitId": req.UnitID, "shotId": shot.ID, "shotRevisionId": shot.CurrentRevisionID, "artifactType": "video", "role": "output", "source": "project-video-chain", "videoEditOperation": operation, "artifactMetadata": map[string]any{"model": choice.Model, "contentDurationSeconds": content.Dur}}
		if err := s.enforceDramaPrevizGate(userID, input); err != nil {
			return state, err
		}
		// Read the permitted route's profile before matching a price tier.
		// The global preference and the catalog default can both be unsold SKUs.
		delete(config, "videoSeconds")
		if options, ok := input["capabilityOptions"].(map[string]any); ok {
			delete(options, "videoSeconds")
		}
		profile, routed, err := s.videoChainRouteProfile(userID, input, choice, feature, shot.ID, operation, content.Dur, usePosition)
		seconds, referenceCount := 0, len(images)
		specError := func(err error) error {
			return videoChainSpecError(videoChainBindingCapabilityError(err, len(audios)), shot.ID, choice.Model, content.Dur, seconds, referenceCount, profile)
		}
		if err != nil {
			if profile != nil {
				seconds, _ = videoChainGenerationSeconds(profile.Duration, content.Dur)
			}
			return state, specError(err)
		}
		if profile == nil {
			return state, specError(BadAuthRequest("视频模型未声明参考图片能力，暂不能开始连续生成"))
		}
		seconds, err = videoChainGenerationSeconds(profile.Duration, content.Dur)
		if err != nil {
			return state, specError(err)
		}
		if len(audios) > profile.References.MaxAudios {
			return state, specError(videoChainAudioLimit(len(audios), fmt.Sprintf("该线路最多支持 %d 条声音参考", profile.References.MaxAudios)))
		}
		for _, issue := range compiled.Result.Issues {
			if issue.Level == "error" {
				return state, specError(BadAuthRequest(fmt.Sprintf("%s 分段检查失败：%s", shot.Title, issue.Message)))
			}
		}
		generation := float64(seconds)
		config = input["config"].(map[string]any)
		config["videoSeconds"] = fmt.Sprint(generation)
		if options, ok := input["capabilityOptions"].(map[string]any); ok {
			options["videoSeconds"] = generation
		}
		if usePosition && !videoChainPositionFits(profile, len(images), operation) {
			usePosition = false
			referenceWarning = "所选模型不支持额外站位图或参考图容量已满，本次保留原参考图并跳过白模"
		}
		if freezeReference && !usePosition && referenceWarning != "" {
			referenceWarning = "未使用上一段站位参考：所选模型不支持额外参考图或名额已满"
		}
		if usePosition {
			images = append(images, videoChainPendingPosition())
			referenceCount = len(images)
			input["referenceImages"] = images
			if operation == "text_to_video" {
				operation = "reference_to_video"
			}
			input["metadata"].(map[string]any)["videoEditOperation"] = operation
		}
		m, err := drama.ResolveModel(compiled.Result.Model)
		if err != nil {
			return state, err
		}
		tag := ""
		if usePosition {
			tag = drama.ReferenceImageTag(m, len(images))
		}
		original := compiled.Result.Prompt
		finalPrompt := original
		if freezeReference && usePosition {
			finalPrompt = drama.ApplyPositionReference(finalPrompt, tag)
		} else if freezeReference {
			finalPrompt = drama.StripPositionReference(finalPrompt)
		} else if usePosition {
			finalPrompt = videoChainPositionPrompt(tag) + "\n\n" + finalPrompt
		}
		// 本体论单元的粘贴提示词自带 1 秒站位交接镜头，不再追加白模收尾。
		ending := enabled && i < sceneLast && strings.TrimSpace(compiled.Spec.Verbatim) == ""
		if ending && !strings.Contains(finalPrompt, dramaSceneEnding) {
			finalPrompt += "\n\n" + dramaSceneEnding
		}
		input["prompt"] = finalPrompt
		if routed != nil {
			metadata := input["metadata"].(map[string]any)
			metadata["videoChainRouteId"], metadata["videoChainModelRevisionId"] = routed.Route.ID, routed.Revision.ID
			input["capabilityOptions"] = routed.Intent.Options
			input = applyRoutedProviderSelection(input, routed)
		} else {
			_, input, err = s.resolveTaskModelSelection(userID, input, choice.LogicalModelID, "canvas_video", operation, feature)
			if err != nil {
				return state, specError(err)
			}
		}
		if err = s.enforceDramaModelFamily(userID, input); err != nil {
			return state, specError(err)
		}
		if err = s.ValidateTaskCapability(input); err != nil {
			return state, specError(err)
		}
		if err = s.requireCustomChannelsForTaskInput(input); err != nil {
			return state, err
		}
		var quote *LogicalModelQuote
		if !req.PrepareOnly {
			actual, _ := input["config"].(map[string]any)
			selected := stringValue(actual["channelId"]) + "::" + firstNonEmpty(stringValue(actual["channelModelKey"]), stringValue(actual["model"]))
			if selectedRoute != nil && selectedRoute.Model != selected {
				return state, failedPrecondition("本项目的视频线路已更换，请刷新并按当前线路重新报价")
			}
			input["metadata"].(map[string]any)["projectVideoRouteRevision"] = state.RouteRevision
		}
		intent := ModelRequestIntentFromTaskInput(input, "canvas_video", operation)
		if routed != nil {
			access, accessErr := s.userGroupAccess(userID)
			if accessErr != nil {
				return state, accessErr
			}
			quote, err = s.quoteRoutedModel(userID, access, routed, intent)
		} else {
			config = input["config"].(map[string]any)
			channelID := stringValue(config["channelId"])
			if channelID != "" {
				quote, err = s.QuoteChannelModel(userID, ChannelModelQuoteRequest{ChannelID: channelID, ModelKey: stringValue(config["model"]), Intent: intent})
			}
		}
		if err != nil {
			return state, specError(err)
		}
		if !req.PrepareOnly && selectedRoute != nil && selectedRoute.Status == "auto" {
			cost, err := s.projectVideoAutoCost(context.Background(), s.repo, userID, input, selectedRoute)
			if err != nil {
				return state, specError(err)
			}
			quote.UpstreamCost = cost
			input["metadata"].(map[string]any)[projectVideoApprovedCostKey] = cost.AmountMicros
		}
		// The placeholder participates only in capability/quote checks. Runtime
		// adds the actual predecessor frame and compiles its real reference tag.
		if usePosition {
			images = images[:len(images)-1]
		}
		input["referenceImages"] = images
		input["prompt"] = original
		signature := videoChainHash([]any{shot.ID, shot.CurrentRevisionID, scene, content.Dur, ending, input, previousSignature, quote, referenceCount})
		step := videoChainStepState{ProjectVideoChainStep: ProjectVideoChainStep{ShotID: shot.ID, RevisionID: shot.CurrentRevisionID, Title: shot.Title, Scene: scene, ContentSeconds: content.Dur, GenerationSeconds: generation, Model: choice.Model, Status: "waiting", Phase: "waiting", ReferenceCount: len(images), ReferenceTag: tag, Ending: ending, Quote: quote}, Input: input, LogicalModelID: choice.LogicalModelID, Signature: signature, Uploads: map[string]string{}}
		step.ReferenceSkipped, step.ReferenceWarning = hasPosition && !usePosition, referenceWarning
		step.ReferenceAudioCount = len(audios)
		step.CompiledModel = compiled.Result.Model
		step.FreezeReference = freezeReference
		if hasPosition {
			step.SourceShotID = shots[i-1].ID
			step.SourceRevisionID = shots[i-1].CurrentRevisionID
			if prior != nil {
				step.SourceVideoResourceID, step.SourceVideoTaskID = prior.ResourceID, prior.TaskID
				step.Signature = videoChainHash([]any{signature, prior.ID, prior.ResourceID, prior.TaskID})
			}
		}
		state.Steps = append(state.Steps, step)
		previousSignature = signature
	}
	fingerprint := []any{projectID, req.UnitID, req.Scope, req.PrepareOnly, enabled}
	for _, step := range state.Steps {
		fingerprint = append(fingerprint, step.Signature, step.Quote)
	}
	state.PreviewHash = videoChainHash(fingerprint)
	return state, nil
}

func (s *Service) videoChainExistingPredecessor(userID, projectID string, shot model.Shot, fromFreeze bool) (*model.ShotArtifact, error) {
	artifacts, err := s.repo.ProjectUnitShotArtifacts(projectID, shot.UnitID)
	if err != nil {
		return nil, err
	}
	var selected *model.ShotArtifact
	for i := range artifacts {
		item := &artifacts[i]
		if item.ShotID != shot.ID || item.RevisionID != shot.CurrentRevisionID || item.Type != "video" || item.Status != "ready" || item.ResourceID == "" {
			continue
		}
		if selected == nil || item.Selected && !selected.Selected || item.Selected == selected.Selected && item.Version > selected.Version {
			selected = item
		}
	}
	if selected == nil {
		return nil, nil
	}
	task, err := s.repo.TaskForUser(userID, selected.TaskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return nil, nil
	}
	metadata, _ := input["metadata"].(map[string]any)
	submission, _ := metadata[dramaSubmissionKey].(map[string]any)
	// 能当站位参考来源的前序视频：追加过白模收尾的旧段，或自带站位交接镜头的本体论单元。
	whiteModel := submission["endingApplied"] == true && strings.Contains(task.Prompt, "无贴图的哑光白色三维白模")
	if task.Status != model.TaskStatusSucceeded || !fromFreeze && !whiteModel && submission["handoffShot"] != true {
		return nil, nil
	}
	if _, err := s.videoChainReadyResource(userID, selected.ResourceID, "video/"); err != nil {
		var unavailable *AppError
		if errors.As(err, &unavailable) {
			return nil, nil
		}
		return nil, err
	}
	return selected, nil
}
