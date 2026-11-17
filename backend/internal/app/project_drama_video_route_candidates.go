package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"yingce/backend/internal/sllcatalog"
)

type projectVideoRouteCandidate struct {
	ChannelID        string    `json:"channelId"`
	ModelKey         string    `json:"modelKey"`
	ProviderModelKey string    `json:"providerModelKey"`
	DisplayName      string    `json:"displayName"`
	PriceTierID      string    `json:"priceTierId"`
	CostMicroCNY     int64     `json:"costMicroCny"`
	Source           string    `json:"source"`
	Rate             *float64  `json:"rate"`
	SampleCount      *int64    `json:"sampleCount"`
	CollectedAt      time.Time `json:"collectedAt,omitzero"`
}

// input is the terminal child's actual request, including its compiled references.
// The old request, billing order and controller are never modified during discovery.
func (s *Service) projectVideoRouteCandidates(ctx context.Context, userID, projectID, failedChannelID, failedProviderModelKey string, input map[string]any, contentSeconds float64, now time.Time) ([]projectVideoRouteCandidate, string, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return nil, "", err
	}
	if target, shot := dramaTaskTarget(input); target != projectID || shot == "" || failedChannelID == "" || failedProviderModelKey == "" || !isPositiveVideoSeconds(contentSeconds) {
		return nil, "缺少可核对的原任务线路、镜头或正文时长，请重新预览", nil
	}
	access, err := s.userGroupAccess(userID)
	if err != nil {
		return nil, "", err
	}
	channels, err := s.repo.WithContext(ctx).SystemChannels(false)
	if err != nil {
		return nil, "", err
	}
	var allowed []string
	for _, channel := range channels {
		if access.allowsChannel(channel.ID) {
			allowed = append(allowed, channel.ID)
		}
	}
	offers, err := s.repo.SLLCatalog().Offers(ctx, allowed, now.UTC())
	if err != nil {
		return nil, "", err
	}
	eligible := make([]sllcatalog.Offer, 0, len(offers))
	reasons := make(map[string]bool)
	for _, offer := range offers {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if offer.ChannelID == failedChannelID && offer.ProviderModelKey == failedProviderModelKey {
			continue
		}
		reason, err := s.projectVideoOfferMismatch(userID, access, input, contentSeconds, offer, false)
		if err != nil {
			return nil, "", err
		}
		if reason != "" {
			reasons[reason] = true
			continue
		}
		eligible = append(eligible, offer)
	}
	if len(eligible) == 0 {
		if len(reasons) > 0 {
			return nil, "没有能承载本段实发规格的其他线路：" + strings.Join(slices.Sorted(maps.Keys(reasons)), "；"), nil
		}
		return nil, "当前分组没有其他已启用、可报价且上游单条成本不超过 6 元的 Seedance 2.5 线路", nil
	}
	candidates := rankProjectVideoOffers(eligible, now)
	if candidates[0].Rate == nil {
		return candidates, "合格线路缺少近期可比较的 24 小时成功率，可手动选择线路", nil
	}
	return candidates, "", nil
}

func (s *Service) projectVideoOfferMismatch(userID string, access *userGroupAccess, input map[string]any, contentSeconds float64, offer sllcatalog.Offer, confirmed bool) (string, error) {
	if offer.ChannelID == "" || offer.ModelKey == "" || offer.PriceTierID == "" || offer.ProviderModelKey == "" || offer.CostMicroCNY < 0 || offer.CostMicroCNY > 6_000_000 {
		return "上游按条成本或价档未核实", nil
	}
	profile, err := DecodeModelCapabilityConfig(offer.CapabilityConfigJSON)
	if err != nil || profile == nil || profile.Video == nil {
		return "视频能力未核实", nil
	}
	seconds, err := videoChainGenerationSeconds(profile.Video.Duration, contentSeconds)
	if err != nil {
		return err.Error(), nil
	}
	// Rebuild only public creative options. Old credentials, resolved SKU and
	// upload authorization belong exclusively to the failed route.
	oldConfig, _ := input["config"].(map[string]any)
	config := map[string]any{"channelId": offer.ChannelID, "model": offer.ModelKey}
	for key, value := range oldConfig {
		if isCapabilityOptionFor("video", key) {
			config[key] = value
		}
	}
	options, _ := input["capabilityOptions"].(map[string]any)
	for key, value := range options {
		key = canonicalModelOptionName("video", key)
		if isCapabilityOptionFor("video", key) {
			config[key] = providerConfigOptionValue(value)
		}
	}
	if confirmed {
		if submitted, ok := numericScalar(config["videoSeconds"]); !ok || submitted != float64(seconds) {
			return "已确认的生成时长与当前线路档位不一致", nil
		}
	}
	config["videoSeconds"] = strconv.Itoa(seconds)
	requestedRatio := stringValue(config["size"])
	if requestedRatio != "" && !videoRatioAllowed(profile.Video.Ratios, requestedRatio) {
		return "画幅不支持 " + requestedRatio, nil
	}
	requestedResolution := stringValue(config["vquality"])
	if !isAutomaticVideoResolution(requestedResolution) && videoResolutionNameRequest(profile.Video, requestedResolution) == "" {
		return "分辨率不支持 " + requestedResolution, nil
	}
	if parseBool(stringValue(config["videoGenerateAudio"]), false) && !profile.Video.GenerateAudio.Supported {
		return "不支持本段要求的同步音频", nil
	}
	next := maps.Clone(input)
	next["config"] = config
	delete(next, "capabilityOptions")
	metadata, _ := next["metadata"].(map[string]any)
	operation := stringValue(metadata["videoEditOperation"])
	intent := ModelRequestIntentFromTaskInput(next, "canvas_video", operation)
	if operation == "" {
		operation = "text_to_video"
		if intent.Inputs["image"] > 0 {
			operation = "reference_to_video"
		}
		if intent.Inputs["video"] > 0 {
			operation = "video_to_video"
		}
		metadata = maps.Clone(metadata)
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata["videoEditOperation"] = operation
		next["metadata"] = metadata
	}
	limits := profile.Video.References
	if n := intent.Inputs["image"]; n < limits.MinImages || n > limits.MaxImages {
		return fmt.Sprintf("本段 %d 张参考图，线路支持 %d 至 %d 张", n, limits.MinImages, limits.MaxImages), nil
	}
	if n := intent.Inputs["video"]; n > limits.MaxVideos {
		return fmt.Sprintf("本段 %d 段视频参考，线路最多支持 %d 段", n, limits.MaxVideos), nil
	}
	if n := intent.Inputs["audio"]; n > limits.MaxAudios {
		return fmt.Sprintf("本段 %d 段音频参考，线路最多支持 %d 段", n, limits.MaxAudios), nil
	}
	next, err = s.resolveSystemChannelModelSelection(access, next, "canvas_video", operation)
	if err != nil {
		return projectVideoOfferError(err)
	}
	config = next["config"].(map[string]any)
	if config["priceTierId"] != offer.PriceTierID || config["providerModelKey"] != offer.ProviderModelKey {
		return "实际请求命中其他价档", nil
	}
	if err = s.ValidateTaskCapability(next); err != nil {
		return projectVideoOfferError(err)
	}
	if err = s.enforceDramaModelFamily(userID, next, offer.ProviderModelKey); err != nil {
		return projectVideoOfferError(err)
	}
	quote, err := s.QuoteChannelModel(userID, ChannelModelQuoteRequest{ChannelID: offer.ChannelID, ModelKey: offer.ModelKey, Intent: ModelRequestIntentFromTaskInput(next, "canvas_video", operation)})
	if err != nil {
		return projectVideoOfferError(err)
	}
	if quote == nil {
		return "当前规格没有有效报价", nil
	}
	if cost := quote.UpstreamCost; cost != nil && (cost.Currency != "CNY" || cost.Unit != "request" || cost.AmountMicros != offer.CostMicroCNY) {
		return "实际报价的上游按条成本已更新", nil
	}
	return "", nil
}

func projectVideoOfferError(err error) (string, error) {
	var modelErr *ModelError
	var appErr *AppError
	if errors.As(err, &modelErr) {
		return modelErr.Error(), nil
	}
	if errors.As(err, &appErr) && appErr.Status >= 400 && appErr.Status < 500 {
		return appErr.Message, nil
	}
	return "", err
}

func usableProjectVideoRate(view sllcatalog.RateView, now time.Time) bool {
	return view.WindowHours == 24 && view.Source != "" && !view.Stale && view.Rate != nil && !math.IsNaN(*view.Rate) && *view.Rate >= 0 && *view.Rate <= 100 &&
		(view.SampleCount == nil || *view.SampleCount > 0) && view.CollectedAt != nil && !view.CollectedAt.After(now) && now.Sub(*view.CollectedAt) < 2*time.Hour
}

func rankProjectVideoOffers(offers []sllcatalog.Offer, now time.Time) []projectVideoRouteCandidate {
	// A common metric is chosen only after capability and cost filtering.
	// Never fill a missing platform rate with an official supplier percentage.
	allSLL, haveOfficial := len(offers) > 0, false
	for _, offer := range offers {
		allSLL = allSLL && offer.Official.Source == sllcatalog.MetricsURL
		haveOfficial = haveOfficial || usableProjectVideoRate(offer.Official, now)
	}
	rows := make([]projectVideoRouteCandidate, 0, len(offers))
	allCounts := make(map[float64]bool)
	for _, offer := range offers {
		rate := offer.Platform
		if allSLL && haveOfficial {
			rate = offer.Official
		}
		row := projectVideoRouteCandidate{ChannelID: offer.ChannelID, ModelKey: offer.ModelKey, ProviderModelKey: offer.ProviderModelKey, PriceTierID: offer.PriceTierID, DisplayName: offer.DisplayName, CostMicroCNY: offer.CostMicroCNY, Source: rate.Source}
		if rate.CollectedAt != nil {
			row.CollectedAt = *rate.CollectedAt
		}
		if usableProjectVideoRate(rate, now) {
			row.Rate, row.SampleCount = rate.Rate, rate.SampleCount
			known, seen := allCounts[*rate.Rate]
			allCounts[*rate.Rate] = (!seen || known) && rate.SampleCount != nil
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Rate == nil || b.Rate == nil {
			if (a.Rate == nil) != (b.Rate == nil) {
				return a.Rate != nil
			}
		} else {
			if *a.Rate != *b.Rate {
				return *a.Rate > *b.Rate
			}
			// Comparing sample counts pairwise only when present is non-transitive
			// with three tied routes. Use counts when the whole tie has counts.
			if allCounts[*a.Rate] && *a.SampleCount != *b.SampleCount {
				return *a.SampleCount > *b.SampleCount
			}
		}
		if a.CostMicroCNY != b.CostMicroCNY {
			return a.CostMicroCNY < b.CostMicroCNY
		}
		if a.ChannelID != b.ChannelID {
			return a.ChannelID < b.ChannelID
		}
		if a.ProviderModelKey != b.ProviderModelKey {
			return a.ProviderModelKey < b.ProviderModelKey
		}
		return a.PriceTierID < b.PriceTierID
	})
	return rows
}
