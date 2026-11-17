package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/sllcatalog"
)

type VideoUpstreamCost struct {
	Currency     string `json:"currency"`
	Unit         string `json:"unit"`
	AmountMicros int64  `json:"amountMicros"`
}

func (s *Service) videoUpstreamQuote(modelID, upstream, tierID string, intent ModelRequestIntent) (*VideoUpstreamCost, error) {
	seconds, _ := numericScalar(intent.Options["videoSeconds"])
	spec := sllcatalog.RequestSpec{
		PriceTierID: tierID,
		Seconds:     int(seconds), Resolution: stringValue(normalizeModelRequestOption("vquality", intent.Options["vquality"])), AspectRatio: stringValue(intent.Options["size"]),
		Images: intent.Inputs["image"], Videos: intent.Inputs["video"], Audios: intent.Inputs["audio"],
		LongestVideoSeconds: intent.LongestVideoSeconds, GenerateAudio: parseBool(fmt.Sprint(intent.Options["videoGenerateAudio"]), false),
	}
	micros, err := s.repo.SLLCatalog().CheckRequest(context.Background(), modelID, upstream, spec)
	if err != nil {
		return nil, ModelCapabilityNotSupported(err.Error())
	}
	if micros == nil {
		return nil, nil
	}
	return &VideoUpstreamCost{Currency: "CNY", Unit: "request", AmountMicros: *micros}, nil
}

func (s *Service) checkSLLVideoInput(ctx context.Context, modelID string, input canvasGenerationInput) error {
	c := input.Config
	seconds, _ := numericScalar(c.VideoSeconds)
	spec := sllcatalog.RequestSpec{
		PriceTierID: c.PriceTierID,
		Seconds:     int(seconds), Resolution: stringValue(normalizeModelRequestOption("vquality", c.VQuality)), AspectRatio: c.Size,
		Images: len(input.ReferenceImages), Videos: len(input.ReferenceVideos), Audios: len(input.ReferenceAudios),
		GenerateAudio: parseBool(c.VideoGenerateAudio, false),
	}
	for _, video := range input.ReferenceVideos {
		if video.DurationMs <= 0 {
			spec.LongestVideoSeconds = 0
			break
		}
		spec.LongestVideoSeconds = max(spec.LongestVideoSeconds, float64(video.DurationMs)/1000)
	}
	cost, err := s.repo.SLLCatalog().CheckRequest(ctx, modelID, firstNonEmpty(c.ProviderModelKey, c.Model), spec)
	if err != nil {
		return ModelCapabilityNotSupported(err.Error())
	}
	if cost == nil {
		return nil
	}
	if c.InterfaceType != string(sllcatalog.VideoProtocol) {
		return ModelCapabilityNotSupported("SLL 目录协议已变化，请重新同步")
	}
	if parseBool(c.VideoWatermark, false) {
		return ModelCapabilityNotSupported("SLL 线路尚未核实水印参数")
	}
	if options, _ := input.Metadata["providerOptions"].(map[string]any); len(options) > 0 {
		return ModelCapabilityNotSupported("SLL 目录线路仅允许已核实的请求参数")
	}
	return nil
}

func validateSLLMedia(input canvasGenerationInput, wire *sllcatalog.WireOptions) error {
	for _, group := range []struct {
		items []providerMedia
		mimes []string
		label string
	}{
		{input.ReferenceImages, wire.ImageMIMEs, "图片"},
		{input.ReferenceVideos, wire.VideoMIMEs, "视频"},
		{input.ReferenceAudios, wire.AudioMIMEs, "音频"},
	} {
		for _, media := range group.items {
			mime := strings.ToLower(strings.TrimSpace(strings.Split(firstNonEmpty(media.MimeType, media.Type), ";")[0]))
			if len(group.mimes) > 0 && !slices.Contains(group.mimes, mime) {
				return ModelCapabilityNotSupported("SLL 参考" + group.label + "格式须为 " + strings.Join(group.mimes, "、"))
			}
		}
	}
	return nil
}

// Polling uses the channel and upstream model captured by an accepted task.
// Removing a sale tier may prevent a new order, but must not strand this receipt.
func (s *Service) resolveVideoReceiptConfig(ctx context.Context, c providerConfig, jobID string) (providerConfig, bool, error) {
	identity, _ := ctx.Value(providerAnalyticsKey{}).(providerAnalyticsContext)
	if strings.TrimSpace(jobID) == "" || identity.TaskType != "" && !isVideoTaskType(identity.TaskType) {
		resolved, err := s.resolveProviderConfig(c)
		return resolved, false, err
	}
	mismatch := failedPrecondition("无法核实原视频任务的渠道、模型、订单或受理号，请核对原回执")
	// Standalone personal-provider callers cannot acquire system credentials.
	if identity.TaskID == "" && c.ChannelID == "" && systemChannelIDFromBaseURL(c.BaseURL) == "" {
		resolved, err := s.resolveProviderConfig(c)
		return resolved, false, err
	}
	if identity.Service != s || identity.UserID == "" || identity.TaskID == "" || identity.ProviderRequestID != jobID {
		return providerConfig{}, false, mismatch
	}
	task, err := s.repo.TaskForUser(identity.UserID, identity.TaskID)
	if err != nil {
		return providerConfig{}, false, err
	}
	if !isVideoTaskType(task.Type) || task.Type != identity.TaskType || task.ProjectID != identity.ProjectID ||
		task.RouteRun != identity.RouteRun || task.BillingOrderID != identity.BillingOrderID {
		return providerConfig{}, false, mismatch
	}
	var saved struct {
		Config providerConfig `json:"config"`
	}
	raw, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		return providerConfig{}, false, err
	}
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		return providerConfig{}, false, err
	}
	frozen := saved.Config
	frozen.ChannelID = firstNonEmpty(frozen.ChannelID, systemChannelIDFromBaseURL(frozen.BaseURL))
	if firstNonEmpty(c.ChannelID, systemChannelIDFromBaseURL(c.BaseURL)) != frozen.ChannelID {
		return providerConfig{}, false, mismatch
	}
	if frozen.ChannelID == "" {
		// Read the personal endpoint and credentials from the original task.
		d, err := s.videoChainAttemptDiagnostic(task)
		if err != nil || d.originalRequestID != jobID {
			if err != nil {
				return providerConfig{}, false, err
			}
			return providerConfig{}, false, mismatch
		}
		resolved, err := s.resolveProviderConfig(saved.Config)
		return resolved, true, err
	}
	key := firstNonEmpty(frozen.ChannelModelKey, frozen.Model)
	modelID, tierID := task.ChannelModelID, frozen.PriceTierID
	var tierVersion int64
	receipt := task.ProviderRequestID
	if task.BillingOrderID != "" {
		order, err := s.repo.BillingOrder(task.BillingOrderID)
		if err != nil {
			return providerConfig{}, false, err
		}
		if order.TaskID != task.ID || order.UserID != task.UserID || order.Capability != "video" ||
			order.ChannelID != frozen.ChannelID || order.PriceTierID != "" && tierID != "" && order.PriceTierID != tierID ||
			task.ChannelModelID != "" && order.ChannelModelID != task.ChannelModelID ||
			task.LogicalModelID == "" && order.Model != key ||
			order.ProviderRequestID != "" && order.ProviderRequestID != jobID {
			return providerConfig{}, false, mismatch
		}
		modelID, tierID = firstNonEmpty(modelID, order.ChannelModelID), firstNonEmpty(tierID, order.PriceTierID)
		tierVersion = order.PriceTierVersion
		receipt = firstNonEmpty(receipt, order.ProviderRequestID)
	}
	attempts, err := s.repo.RouteAttempts(task.ID, task.RouteRun)
	if err != nil {
		return providerConfig{}, false, err
	}
	var since time.Time
	if len(attempts) != 0 {
		a := attempts[len(attempts)-1]
		since = a.StartedAt
		if a.ChannelID != "" && a.ChannelID != frozen.ChannelID || a.ChannelModelID != "" && modelID != "" && a.ChannelModelID != modelID ||
			a.RouteID != "" && task.RouteID != "" && a.RouteID != task.RouteID ||
			a.ProviderRequestID != "" && a.ProviderRequestID != jobID {
			return providerConfig{}, false, mismatch
		}
		receipt = firstNonEmpty(receipt, a.ProviderRequestID)
		modelID = firstNonEmpty(modelID, a.ChannelModelID)
	}
	if since.IsZero() {
		if task.StartedAt != nil {
			since = *task.StartedAt
		} else if task.RouteRun <= 1 {
			since = task.CreatedAt
		}
	}
	if receipt == "" && (task.RouteRun <= 1 || !since.IsZero()) {
		receipt, err = s.repo.VideoChainProviderRequestID(task.UserID, task.ID, task.BillingOrderID, since, frozen.ChannelID)
		if err != nil {
			return providerConfig{}, false, err
		}
	}
	if receipt != jobID || key == "" {
		return providerConfig{}, false, mismatch
	}
	item, err := s.repo.VideoReceiptModel(frozen.ChannelID, modelID, key)
	if err != nil {
		return providerConfig{}, false, err
	}
	if frozen.ProviderModelKey == "" && tierID != "" {
		tier, err := s.repo.VideoReceiptPriceTier(item.ID, tierID)
		if err != nil {
			return providerConfig{}, false, err
		}
		if tierVersion > 0 && tier.PriceVersion != tierVersion {
			return providerConfig{}, false, failedPrecondition("原视频规格档的版本已变化，无法核实原上游模型")
		}
		frozen.ProviderModelKey = tier.ProviderModelKey
	}
	frozen.ChannelModelKey, frozen.PriceTierID = key, tierID
	frozen.ProviderModelKey = firstNonEmpty(frozen.ProviderModelKey, item.ProviderModelKey, key)
	frozen.InterfaceType = firstNonEmpty(frozen.InterfaceType, string(item.Protocol))
	if frozen.InterfaceType == "" ||
		c.ChannelModelKey != "" && c.ChannelModelKey != key || c.PriceTierID != "" && c.PriceTierID != tierID ||
		c.ProviderModelKey != "" && c.ProviderModelKey != frozen.ProviderModelKey || c.InterfaceType != "" && c.InterfaceType != frozen.InterfaceType ||
		c.Model != "" && c.Model != frozen.Model && c.Model != key && c.Model != frozen.ProviderModelKey {
		return providerConfig{}, false, mismatch
	}
	ch, err := s.repo.AdminSystemChannel(frozen.ChannelID)
	if err != nil {
		return providerConfig{}, false, err
	}
	owned := false
	if frozen.InterfaceType == string(sllcatalog.VideoProtocol) {
		owned, err = s.repo.SLLCatalog().HasReceiptRoute(ctx, ch.ID, frozen.ChannelModelKey, frozen.ProviderModelKey)
		if err != nil {
			return providerConfig{}, false, err
		}
		if owned && !sllcatalog.IsSLLChannel(*ch) {
			return providerConfig{}, false, InvalidModelSelection("原 SLL 渠道地址已变化，无法查询原任务")
		}
	}
	if !owned {
		if err := s.validateVideoReceiptDestination(task, ch, jobID, since); err != nil {
			return providerConfig{}, false, err
		}
	}
	if err := s.decryptSystemChannelSecrets(ch); err != nil {
		return providerConfig{}, false, err
	}
	// Only the stored selection controls protocol, identifiers and destination.
	// The current channel supplies its rotated credentials, never a new sale tier.
	c = frozen
	c.ChannelID, c.ChannelModelKey, c.PriceTierID = ch.ID, key, tierID
	c.Model, c.ProviderModelKey, c.InterfaceType = frozen.ProviderModelKey, frozen.ProviderModelKey, frozen.InterfaceType
	c.APIFormat = firstNonEmpty(frozen.APIFormat, channelAPIFormatForProtocol(ch.APIFormat, model.ChannelInterfaceType(c.InterfaceType)))
	c.BaseURL, c.APIKey, c.SecretKey = ch.BaseURL, ch.APIKey, ch.SecretKey
	c.Headers, err = ParseOutboundHeadersJSON(ch.HeadersJSON)
	return c, true, err
}
