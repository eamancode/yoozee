package sllcatalog

import (
	"encoding/json"
	"errors"
	"slices"
)

type mediaModel struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Identity struct {
		FamilyID string `json:"family_id"`
		Version  string `json:"version"`
	} `json:"identity"`
	Availability struct {
		Visible     bool   `json:"visible"`
		Routable    bool   `json:"routable"`
		PublicModel string `json:"public_model"`
	} `json:"availability"`
	Operations []json.RawMessage `json:"operations"`
}

type mediaParameter struct {
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Required    bool            `json:"required"`
	Values      json.RawMessage `json:"values"`
	Default     json.RawMessage `json:"default"`
	Min         int             `json:"min"`
	Max         int             `json:"max"`
	Step        int             `json:"step"`
	Constraints struct {
		MinItems int      `json:"min_items"`
		MaxItems *int     `json:"max_items"`
		MaxBytes int64    `json:"max_bytes"`
		MIMEs    []string `json:"mime_types"`
	} `json:"constraints"`
}

type mediaOperation struct {
	Name          string           `json:"name"`
	Endpoint      string           `json:"endpoint"`
	CreateRequest bool             `json:"create_request"`
	ExecutionMode string           `json:"execution_mode"`
	Parameters    []mediaParameter `json:"parameters"`
	Fixed         struct {
		BillingUnit     string   `json:"billing_unit"`
		Resolution      string   `json:"resolution"`
		Resolutions     []string `json:"allowed_resolutions"`
		AspectRatios    []string `json:"allowed_aspect_ratios"`
		Duration        int      `json:"duration"`
		DurationDefault int      `json:"default_duration"`
		MinSeconds      int      `json:"min_seconds"`
		MaxSeconds      int      `json:"max_seconds"`
		Durations       []int    `json:"allowed_durations"`
		MaxPromptChars  int      `json:"max_prompt_chars"`
		MinImages       int      `json:"min_reference_images"`
		MaxImages       *int     `json:"max_reference_images"`
		MaxVideos       *int     `json:"max_reference_videos"`
		MaxAudios       *int     `json:"max_reference_audios"`
		MaxReferences   *int     `json:"max_reference_total"`
		MaxVideoSeconds *int     `json:"max_reference_video_seconds"`
	} `json:"fixed_capabilities"`
}

func upperBound(a, b *int) *int {
	if a == nil || b != nil && *b < *a {
		return b
	}
	return a
}

func (m mediaModel) capabilities() (Capabilities, []string, error) {
	var caps Capabilities
	if m.Kind != "video" || m.Identity.FamilyID != "seedance-2.5" || m.Identity.Version != "2.5" {
		return caps, nil, errors.New("不是 Seedance 2.5 视频生成线路")
	}
	if m.Status != "supported" || !m.Availability.Visible || !m.Availability.Routable || m.Availability.PublicModel != m.ID {
		return caps, nil, errors.New("供应商未开放该模型路由")
	}
	var op mediaOperation
	if len(m.Operations) != 1 || json.Unmarshal(m.Operations[0], &op) != nil ||
		op.Name != "video_generation" || op.Endpoint != "/v1/videos" || !op.CreateRequest || op.ExecutionMode != "async_task" {
		return caps, nil, errors.New("生成端点或参数协议待核实")
	}
	f := op.Fixed
	if f.BillingUnit != "" && f.BillingUnit != "request" && f.BillingUnit != "mixed" {
		return caps, nil, errors.New("生成规格未按条计费")
	}
	caps.MinImages = f.MinImages
	caps.MaxReferences, caps.MaxVideoSeconds = f.MaxReferences, f.MaxVideoSeconds
	caps.MaxPromptChars, caps.DurationDefault = f.MaxPromptChars, f.DurationDefault
	resolutions := f.Resolutions
	params := make(map[string]mediaParameter)
	for _, p := range op.Parameters {
		if _, exists := params[p.Name]; exists {
			return caps, nil, errors.New("生成参数定义重复")
		}
		params[p.Name] = p
		switch p.Name {
		case "model", "prompt":
		case "duration":
			caps.SendDuration = true
			if p.Type != "integer" || !decodeValues(p.Values, &caps.Durations) {
				return caps, nil, errors.New("生成时长类型或档位待核实")
			}
			if len(caps.Durations) == 0 {
				caps.Durations = f.Durations
			}
			caps.DurationMin, caps.DurationMax = p.Min, p.Max
			if p.Step > 1 && len(caps.Durations) == 0 {
				return caps, nil, errors.New("不支持该时长步长")
			}
			if caps.DurationDefault == 0 {
				_ = json.Unmarshal(p.Default, &caps.DurationDefault)
			}
		case "aspect_ratio":
			if p.Type != "enum" || !decodeValues(p.Values, &caps.AspectRatios) {
				return caps, nil, errors.New("画幅档位待核实")
			}
			if len(caps.AspectRatios) == 0 {
				caps.AspectRatios = f.AspectRatios
			}
		case "resolution":
			caps.SendResolution = true
			if p.Type != "enum" || !decodeValues(p.Values, &resolutions) {
				return caps, nil, errors.New("分辨率档位待核实")
			}
		case "images", "videos", "audios":
			if p.Type != "array" || (p.Name != "images" && (p.Required || p.Constraints.MinItems > 0)) {
				return caps, nil, errors.New("参考素材输入要求未支持")
			}
			switch p.Name {
			case "images":
				caps.MinImages = max(caps.MinImages, p.Constraints.MinItems)
				if p.Required {
					caps.MinImages = max(1, caps.MinImages)
				}
				caps.MaxImages = upperBound(p.Constraints.MaxItems, f.MaxImages)
				caps.MaxImageBytes, caps.ImageMIMEs = p.Constraints.MaxBytes, p.Constraints.MIMEs
			case "videos":
				caps.MaxVideos = upperBound(p.Constraints.MaxItems, f.MaxVideos)
				caps.MaxVideoBytes, caps.VideoMIMEs = p.Constraints.MaxBytes, p.Constraints.MIMEs
			case "audios":
				caps.MaxAudios = upperBound(p.Constraints.MaxItems, f.MaxAudios)
				caps.MaxAudioBytes, caps.AudioMIMEs = p.Constraints.MaxBytes, p.Constraints.MIMEs
			}
		case "generate_audio":
			if p.Type == "boolean" {
				available := true
				caps.GenerateAudio = &available
			}
		default:
			if p.Required {
				return caps, nil, errors.New("生成需要暂不支持的参数 " + p.Name)
			}
		}
	}
	if _, ok := params["model"]; !ok || params["prompt"].Name == "" {
		return caps, nil, errors.New("缺少可提交的模型 ID 或提示词参数")
	}
	// A positive source capability does not enable an undeclared request field.
	// Preserve explicit zero and distinguish it from a missing published limit.
	if f.MaxImages != nil && *f.MaxImages == 0 {
		caps.MaxImages = f.MaxImages
	}
	if f.MaxVideos != nil && *f.MaxVideos == 0 {
		caps.MaxVideos = f.MaxVideos
	}
	if f.MaxAudios != nil && *f.MaxAudios == 0 {
		caps.MaxAudios = f.MaxAudios
	}
	if !caps.SendDuration {
		seconds := f.Duration
		if seconds == 0 {
			seconds = f.DurationDefault
		}
		if seconds <= 0 {
			return caps, nil, errors.New("不可传时长的线路缺少已核实的固定时长")
		}
		caps.Durations = []int{seconds}
	}
	if len(caps.Durations) > 0 {
		slices.Sort(caps.Durations)
		caps.DurationMin, caps.DurationMax = caps.Durations[0], caps.Durations[len(caps.Durations)-1]
	} else {
		if caps.DurationMin == 0 {
			caps.DurationMin = f.MinSeconds
		}
		if caps.DurationMax == 0 {
			caps.DurationMax = f.MaxSeconds
		}
	}
	if caps.DurationMin < 1 || caps.DurationMax < caps.DurationMin || len(caps.AspectRatios) == 0 {
		return caps, nil, errors.New("缺少可提交的时长或画幅规格")
	}
	if !caps.SendResolution {
		if f.Resolution != "" && f.Resolution != "multi" {
			caps.FixedResolution = f.Resolution
		} else if len(resolutions) == 1 {
			caps.FixedResolution = resolutions[0]
		} else {
			return caps, nil, errors.New("无法约束实际提交的分辨率")
		}
		resolutions = []string{caps.FixedResolution}
	}
	return caps, resolutions, nil
}

func decodeValues[T any](raw json.RawMessage, dest *[]T) bool {
	return len(raw) == 0 || json.Unmarshal(raw, dest) == nil
}
