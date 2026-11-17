package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

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
	if jobID != "" && c.ChannelID != "" && c.InterfaceType == string(sllcatalog.VideoProtocol) {
		key := firstNonEmpty(c.ChannelModelKey, c.Model)
		upstream := firstNonEmpty(c.ProviderModelKey, c.Model)
		owned, err := s.repo.SLLCatalog().HasReceiptRoute(ctx, c.ChannelID, key, upstream)
		if err != nil {
			return providerConfig{}, false, err
		}
		if owned {
			ch, err := s.adminSystemChannel(c.ChannelID)
			if err != nil {
				return providerConfig{}, false, err
			}
			if !sllcatalog.IsSLLChannel(*ch) {
				return providerConfig{}, false, InvalidModelSelection("原 SLL 渠道地址已变化，无法查询原任务")
			}
			c.APIFormat = channelAPIFormatForProtocol(ch.APIFormat, sllcatalog.VideoProtocol)
			c.BaseURL, c.APIKey, c.SecretKey = ch.BaseURL, ch.APIKey, ch.SecretKey
			c.ChannelModelKey, c.ProviderModelKey, c.Model = key, upstream, upstream
			c.Headers, err = ParseOutboundHeadersJSON(ch.HeadersJSON)
			return c, true, err
		}
	}
	resolved, err := s.resolveProviderConfig(c)
	return resolved, false, err
}
