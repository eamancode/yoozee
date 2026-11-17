package sllcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"yingce/backend/internal/model"
)

type RequestSpec struct {
	PriceTierID                     string
	Resolution, AspectRatio         string
	Seconds, Images, Videos, Audios int
	GenerateAudio                   bool
	LongestVideoSeconds             float64
}

func (s *Store) CheckRequest(ctx context.Context, channelModelID, providerModelKey string, spec RequestSpec) (*int64, error) {
	db := s.db.WithContext(ctx)
	var item model.ChannelModel
	if err := db.First(&item, "id = ?", channelModelID).Error; err != nil {
		return nil, err
	}
	var ch model.ModelChannel
	if err := db.First(&ch, "id = ?", item.ChannelID).Error; err != nil {
		return nil, err
	}
	// Unmanaged channels use their established execution path. Managed identities
	// remain fenced if an operator edits the channel URL.
	if !IsSLLChannel(ch) && !(item.CatalogModelKey != "" && seedance25.MatchString(item.CatalogModelKey)) {
		return nil, nil
	}
	var state model.SLLCatalogImport
	if err := db.Where("channel_id = ?", ch.ID).Find(&state).Error; err != nil {
		return nil, err
	}
	if state.ChannelID == "" {
		if IsSLLChannel(ch) && item.CatalogModelKey == providerModelKey && strings.Contains(item.CapabilityConfigJSON, `"sllWire"`) {
			return nil, errors.New("请先为此渠道凭据确认分组并同步 SLL 目录")
		}
		return nil, nil
	}
	var line model.SLLCatalogLine
	if err := db.Where("channel_id = ? AND channel_model_id = ?", ch.ID, item.ID).Find(&line).Error; err != nil {
		return nil, err
	}
	if line.ModelID == "" {
		if seedance25.MatchString(providerModelKey) || seedance25.MatchString(item.ProviderModelKey) || item.ProviderModelKey == "" && seedance25.MatchString(item.ModelKey) {
			return nil, errors.New("该上游模型尚无合格的 SLL 价档，请同步目录")
		}
		return nil, nil
	}
	if !ch.Enabled || !item.Enabled || !item.PriceConfigured || !line.Available || !IsSLLChannel(ch) || state.CredentialDigest != credentialDigest(ch) || providerModelKey != line.ModelID || item.ProviderModelKey != line.ModelID {
		return nil, errors.New("SLL 线路已停用、下架或实际调用凭据/上游 ID 已变化，请重新选线或同步")
	}
	var e Entry
	if err := json.Unmarshal([]byte(line.EntryJSON), &e); err != nil {
		return nil, err
	}
	if e.Group != state.SupplierGroup {
		return nil, errors.New("上游分组不一致，请重新同步目录")
	}
	if err := checkExecutionConfig(item, e); err != nil {
		return nil, err
	}
	cost, err := e.checkSpec(spec)
	if err != nil {
		return nil, err
	}
	_, key, err := model.CanonicalSKUSelector(map[string]string{"vquality": spec.Resolution})
	if err != nil {
		return nil, err
	}
	var tier model.ChannelModelPriceTier
	if err := db.Where("channel_model_id = ? AND selector_key = ? AND provider_model_key = ? AND billing_mode = ? AND enabled = ? AND price_configured = ?", item.ID, key, e.ModelID, "fixed_request", true, true).Find(&tier).Error; err != nil {
		return nil, err
	}
	if tier.ID == "" || spec.PriceTierID != "" && spec.PriceTierID != tier.ID {
		return nil, errors.New("该分辨率的按条售价尚未配置或已停用")
	}
	return cost, nil
}

func checkExecutionConfig(item model.ChannelModel, e Entry) error {
	proto, config, err := ExecutionConfig(e)
	if err != nil {
		return err
	}
	// The server owns the upstream wire contract. A price-only admin edit may
	// normalize the other JSON fields without changing this contract.
	type envelope struct {
		Video struct {
			Wire        *WireOptions `json:"sllWire"`
			Resolutions []string     `json:"resolutions"`
			References  struct {
				MaxImageBytes  int64 `json:"maxImageBytes"`
				MaxVideoBytes  int64 `json:"maxVideoBytes"`
				MaxAudioBytes  int64 `json:"maxAudioBytes"`
				PromptMaxChars int64 `json:"promptMaxChars"`
			} `json:"references"`
		} `json:"video"`
	}
	var actual, want envelope
	if item.Protocol != proto || json.Unmarshal([]byte(item.CapabilityConfigJSON), &actual) != nil || json.Unmarshal([]byte(config), &want) != nil ||
		actual.Video.Wire == nil || want.Video.Wire == nil || !wireEqual(*actual.Video.Wire, *want.Video.Wire) {
		return errors.New("SLL 协议或上游参数声明已变化，请重新同步目录")
	}
	// The worker may apply a fixed resolution after admission. Its set must match
	// the supplier projection or it could replace the resolution on the quote.
	slices.Sort(actual.Video.Resolutions)
	slices.Sort(want.Video.Resolutions)
	a, w := actual.Video.References, want.Video.References
	widens := func(current, published int64) bool { return published > 0 && (current <= 0 || current > published) }
	if !slices.Equal(actual.Video.Resolutions, want.Video.Resolutions) || widens(a.MaxImageBytes, w.MaxImageBytes) || widens(a.MaxVideoBytes, w.MaxVideoBytes) || widens(a.MaxAudioBytes, w.MaxAudioBytes) || widens(a.PromptMaxChars, w.PromptMaxChars) {
		return errors.New("SLL 画质或素材上限与供应商目录不一致，请重新同步")
	}
	return nil
}

func wireEqual(a, b WireOptions) bool {
	return a.SendDuration == b.SendDuration && a.SendResolution == b.SendResolution && slices.Equal(a.ImageMIMEs, b.ImageMIMEs) && slices.Equal(a.VideoMIMEs, b.VideoMIMEs) && slices.Equal(a.AudioMIMEs, b.AudioMIMEs)
}

func (e Entry) checkSpec(spec RequestSpec) (*int64, error) {
	c := e.Capabilities
	if spec.Seconds <= 0 || (len(c.Durations) > 0 && !slices.Contains(c.Durations, spec.Seconds)) || (len(c.Durations) == 0 && (spec.Seconds < c.DurationMin || spec.Seconds > c.DurationMax)) {
		return nil, fmt.Errorf("SLL 不支持请求时长 %d 秒", spec.Seconds)
	}
	if !slices.Contains(c.AspectRatios, strings.TrimSpace(spec.AspectRatio)) {
		return nil, fmt.Errorf("SLL 未核实该画幅 %q", spec.AspectRatio)
	}
	if spec.Images < c.MinImages {
		return nil, fmt.Errorf("SLL 线路至少需要 %d 张参考图", c.MinImages)
	}
	for _, m := range []struct {
		name string
		n    int
		max  *int
	}{{"图片", spec.Images, c.MaxImages}, {"视频", spec.Videos, c.MaxVideos}, {"音频", spec.Audios, c.MaxAudios}} {
		if m.n < 0 || m.n > 0 && (m.max == nil || m.n > *m.max) {
			return nil, fmt.Errorf("SLL 无法承载 %d 个%s参考", m.n, m.name)
		}
	}
	if c.MaxReferences != nil && spec.Images+spec.Videos+spec.Audios > *c.MaxReferences {
		return nil, fmt.Errorf("SLL 参考总数超过 %d", *c.MaxReferences)
	}
	if spec.Videos > 0 && c.MaxVideoSeconds != nil && (math.IsNaN(spec.LongestVideoSeconds) || spec.LongestVideoSeconds <= 0 || spec.LongestVideoSeconds > float64(*c.MaxVideoSeconds)) {
		return nil, fmt.Errorf("SLL 要求每段参考视频时长已知且不超过 %d 秒", *c.MaxVideoSeconds)
	}
	if spec.GenerateAudio && (c.GenerateAudio == nil || !*c.GenerateAudio) {
		return nil, errors.New("该 SLL 线路未核实生成音频参数")
	}
	for _, t := range e.Tiers {
		if t.Resolution == spec.Resolution && t.CostMicroCNY >= 0 && t.CostMicroCNY <= maxCostMicroCNY {
			return &t.CostMicroCNY, nil
		}
	}
	return nil, fmt.Errorf("该分辨率 %q 没有不超过 6 元的 SLL 人民币按条价档", spec.Resolution)
}
