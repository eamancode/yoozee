package sllcatalog

import (
	"encoding/json"
	"errors"
	"slices"

	"yingce/backend/internal/model"
)

const VideoProtocol model.ChannelInterfaceType = "seedance-videos-compatible"

// WireOptions records which output fields the upstream operation accepts. A
// fixed resolution or duration still participates in pricing and admission.
type WireOptions struct {
	SendDuration   bool     `json:"sendDuration"`
	SendResolution bool     `json:"sendResolution"`
	ImageMIMEs     []string `json:"imageMimes,omitempty"`
	VideoMIMEs     []string `json:"videoMimes,omitempty"`
	AudioMIMEs     []string `json:"audioMimes,omitempty"`
}

func ExecutionConfig(e Entry) (model.ChannelInterfaceType, string, error) {
	c := e.Capabilities
	resolutions := make([]string, 0, len(e.Tiers))
	for _, t := range e.Tiers {
		if t.CostMicroCNY >= 0 && t.CostMicroCNY <= maxCostMicroCNY && t.Resolution != "" && t.Resolution != "*" && !slices.Contains(resolutions, t.Resolution) {
			resolutions = append(resolutions, t.Resolution)
		}
	}
	if len(resolutions) == 0 || (!c.SendResolution && (len(resolutions) > 1 || resolutions[0] != c.FixedResolution)) {
		return "", "", errors.New("缺少可实际选择的合格分辨率")
	}
	slices.Sort(resolutions)
	if len(c.AspectRatios) == 0 {
		return "", "", errors.New("上游画幅未核实")
	}
	if !c.SendDuration && (len(c.Durations) > 1 || (len(c.Durations) == 0 && c.DurationMin != c.DurationMax)) {
		return "", "", errors.New("上游未声明可选时长字段，实际生成档位无法约束")
	}
	duration := map[string]any{}
	if len(c.Durations) > 0 {
		seconds := c.DurationDefault
		if !slices.Contains(c.Durations, seconds) {
			seconds = c.Durations[0]
		}
		duration = map[string]any{"selection": "enum", "values": c.Durations, "default": seconds}
	} else if c.DurationMin > 0 && c.DurationMax >= c.DurationMin {
		seconds := c.DurationDefault
		if seconds < c.DurationMin || seconds > c.DurationMax {
			seconds = c.DurationMin
		}
		duration = map[string]any{"selection": "range", "min": c.DurationMin, "max": c.DurationMax, "step": 1, "default": seconds}
	} else {
		return "", "", errors.New("上游生成时长未核实")
	}
	maxImages, maxVideos, maxAudios := limit(c.MaxImages), limit(c.MaxVideos), limit(c.MaxAudios)
	operations := []string{}
	if c.MinImages == 0 {
		operations = append(operations, "text_to_video")
	}
	if maxImages > 0 {
		operations = append(operations, "image_to_video", "reference_to_video")
	}
	if maxVideos > 0 {
		operations = append(operations, "video_to_video")
	}
	if maxAudios > 0 {
		operations = append(operations, "audio_to_video")
	}
	if len(operations) == 0 || c.MinImages > maxImages {
		return "", "", errors.New("参考素材要求无法满足")
	}
	promptMax := c.MaxPromptChars
	if promptMax == 0 {
		promptMax = 8000 // Platform video input limit when no upstream limit is published.
	}
	video := map[string]any{
		"references": map[string]any{
			"minImages": c.MinImages, "maxImages": maxImages, "maxVideos": maxVideos, "maxAudios": maxAudios,
			"maxImageBytes": c.MaxImageBytes, "maxVideoBytes": c.MaxVideoBytes, "maxAudioBytes": c.MaxAudioBytes,
			"maxVideoDurationSeconds": limit(c.MaxVideoSeconds), "promptMaxChars": promptMax,
		},
		"duration": duration, "ratios": c.AspectRatios, "defaultRatio": c.AspectRatios[0],
		"resolutions": resolutions, "defaultResolution": resolutions[0],
		"operations": operations, "defaultOperation": operations[0],
		"generateAudio": map[string]bool{"supported": c.GenerateAudio != nil && *c.GenerateAudio},
		"sllWire":       WireOptions{c.SendDuration, c.SendResolution, c.ImageMIMEs, c.VideoMIMEs, c.AudioMIMEs},
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "video": video})
	return VideoProtocol, string(raw), err
}

func limit(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
