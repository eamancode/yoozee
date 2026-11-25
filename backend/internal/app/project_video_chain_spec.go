package app

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"yingce/backend/internal/model"
)

// Carry diagnostics from this preview only. Keep the original HTTP reason and
// capabilityIssues so callers can still act on the specific mismatch.
func videoChainSpecError(err error, shotID, model string, content float64, seconds, images int, profile *VideoCapabilityConfig) error {
	var modelErr *ModelError
	var appErr *AppError
	if errors.As(err, &modelErr) {
		appErr = modelErr.AppError
	} else if !errors.As(err, &appErr) {
		return err
	}
	if appErr == nil {
		return err
	}
	if appErr.Details == nil {
		appErr.Details = map[string]any{}
	}
	details := appErr.Details
	details["operation"], details["shotId"], details["model"] = "video_chain_preview", shotID, model
	details["contentSeconds"] = content
	var argument *modelCapabilityArgumentError
	if errors.As(err, &argument) {
		details["field"], details["issue"] = argument.Field, argument.Issue
	}
	if images >= 0 {
		details["referenceCount"] = images
	}
	if seconds > 0 {
		details["generationSeconds"] = seconds
	}
	if profile != nil {
		details["minReferenceImages"], details["maxReferenceImages"] = profile.References.MinImages, profile.References.MaxImages
		duration := profile.Duration
		if duration.Selection == "enum" {
			details["allowedSeconds"] = duration.Values
		} else if duration.Selection == "range" {
			details["allowedDurationRange"] = map[string]int{"min": duration.Min, "max": duration.Max, "step": duration.Step}
		}
	}
	return err
}

func videoChainPositionFits(profile *VideoCapabilityConfig, images int, operation string) bool {
	return images < profile.References.MaxImages && (operation == "video_to_video" || containsCapabilityString(profile.Operations, "reference_to_video"))
}

func (s *Service) videoChainRouteProfile(userID string, input map[string]any, choice ProjectVideoChainModel, feature bool, shotID, operation string, content float64, position bool) (*VideoCapabilityConfig, *RoutedModel, *model.ChannelModel, error) {
	if choice.LogicalModelID == "" && !taskInputUsesSystemChannel(input) {
		profile, err := s.projectTaskVideoProfile(input)
		return profile, nil, nil, err
	}
	access, err := s.userGroupAccess(userID)
	if err != nil {
		return nil, nil, nil, err
	}
	if taskInputUsesSystemChannel(input) {
		config, _ := input["config"].(map[string]any)
		_, item, err := s.selectableSystemChannelModel(access, config)
		if err != nil {
			return nil, nil, nil, err
		}
		profile, err := normalizedChannelModelCapability(item, s.protocolRegistry())
		if err != nil || profile == nil {
			return nil, nil, nil, err
		}
		return profile.Video, nil, item, nil
	}
	if !feature || taskInputUsesCustomChannel(input) {
		return nil, nil, nil, ModelCatalogMismatch("模型目录已更新，请重新选择")
	}
	catalog, err := s.routeCatalogSnapshot()
	if err != nil {
		return nil, nil, nil, err
	}
	product, ok := catalog.Models[choice.LogicalModelID]
	if !ok {
		return nil, nil, nil, InvalidModelSelection("所选模型不可用")
	}
	type option struct {
		profile *VideoCapabilityConfig
		routed  *RoutedModel
	}
	var options []option
	var mismatch error
	var lastProfile *VideoCapabilityConfig
	for _, candidate := range product.Routes {
		if !access.allowsChannel(candidate.ChannelModel.ChannelID) || !candidate.Route.Enabled || candidate.Route.Weight <= 0 || s.logicalRouteBlocked(candidate) {
			continue
		}
		profile, err := normalizedChannelModelCapability(&candidate.ChannelModel, s.protocolRegistry())
		if err != nil || profile == nil || profile.Video == nil {
			continue
		}
		lastProfile = profile.Video
		seconds, err := videoChainGenerationSeconds(profile.Video.Duration, content)
		if err != nil {
			mismatch = err
			continue
		}
		intent := ModelRequestIntentFromTaskInput(input, "canvas_video", operation)
		intent.Options["videoSeconds"] = seconds
		if position && videoChainPositionFits(profile.Video, intent.Inputs["image"], operation) {
			intent.Inputs["image"]++
			if operation == "text_to_video" {
				intent.Operation = "reference_to_video"
			}
		}
		resolvedIntent := resolveModelRequestIntent(product.ProductSpec, intent, product.Defaults)
		if match := MatchCapability(candidate.CapabilitySpec, resolvedIntent); !match.Matched {
			mismatch = modelCapabilityMismatchError(match)
			continue
		}
		pinned := product
		pinned.Routes = []cachedLogicalRoute{candidate}
		routed, err := s.resolveCatalogModel(access, pinned, intent)
		if err != nil {
			mismatch = err
			continue
		}
		if len(options) == 0 || routed.Route.Priority > options[0].routed.Route.Priority {
			options = options[:0]
		}
		if len(options) == 0 || routed.Route.Priority == options[0].routed.Route.Priority {
			options = append(options, option{profile.Video, routed})
		}
	}
	if len(options) == 0 {
		if mismatch == nil {
			mismatch = InvalidModelSelection("当前模型没有可用且获授权的视频线路")
		}
		return lastProfile, nil, nil, mismatch
	}
	// Keep the weighted choice stable across preview, confirmation and leases.
	slices.SortFunc(options, func(a, b option) int { return strings.Compare(a.routed.Route.ID, b.routed.Route.ID) })
	var total uint64
	for _, item := range options {
		total += uint64(item.routed.Route.Weight)
	}
	draw, _ := strconv.ParseUint(videoChainHash([]string{shotID, product.Revision.ID})[:16], 16, 64)
	draw %= total
	for _, item := range options {
		if draw < uint64(item.routed.Route.Weight) {
			return item.profile, item.routed, nil, nil
		}
		draw -= uint64(item.routed.Route.Weight)
	}
	return nil, nil, nil, InvalidModelSelection("当前模型没有可用的视频线路")
}

func (s *Service) videoChainPinnedLogicalModel(userID, logicalID string, input map[string]any, operation string) (*RoutedModel, error) {
	catalog, err := s.routeCatalogSnapshot()
	if err != nil {
		return nil, err
	}
	product, ok := catalog.Models[logicalID]
	metadata, _ := input["metadata"].(map[string]any)
	if !ok || stringValue(metadata["videoChainModelRevisionId"]) != product.Revision.ID {
		return nil, ModelCatalogMismatch("已确认的模型版本变化，请重新报价")
	}
	access, err := s.userGroupAccess(userID)
	if err != nil {
		return nil, err
	}
	for _, candidate := range product.Routes {
		if candidate.Route.ID == stringValue(metadata["videoChainRouteId"]) {
			product.Routes = []cachedLogicalRoute{candidate}
			intent := ModelRequestIntentFromTaskInput(input, "canvas_video", operation)
			return s.resolveCatalogModel(access, product, intent)
		}
	}
	return nil, ModelCatalogMismatch("已确认的线路不再可用，请重新报价")
}
