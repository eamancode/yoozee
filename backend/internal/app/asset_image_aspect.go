package app

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"yingce/backend/internal/model"
)

// assetImageAspectTolerance 是实际图片比例与要求比例的相对误差上限（上游常按 64 像素对齐，941×1672 这类尺寸在误差内仍算 9:16）。
const assetImageAspectTolerance = 0.04

// AssetImageAspect 是资产出图收尾时的比例核对，写进任务结果 imageAspect：要求的画幅、实际像素、是否相符。
type AssetImageAspect struct {
	Requested string `json:"requested"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Matches   bool   `json:"matches"`
	Message   string `json:"message,omitempty"`
}

// assetImageRatio 读「1:1」「9:16」这样的画幅；读不出返回 0。
func assetImageRatio(size string) float64 {
	w, h, ok := strings.Cut(strings.TrimSpace(size), ":")
	if !ok {
		return 0
	}
	a, err1 := strconv.ParseFloat(strings.TrimSpace(w), 64)
	b, err2 := strconv.ParseFloat(strings.TrimSpace(h), 64)
	if err1 != nil || err2 != nil || a <= 0 || b <= 0 {
		return 0
	}
	return a / b
}

// compareAssetImageAspect 比较实际像素与要求的画幅；要求读不出或像素未知时 ok=false（不判）。
func compareAssetImageAspect(requested string, width, height int) (AssetImageAspect, bool) {
	ratio := assetImageRatio(requested)
	if ratio == 0 || width <= 0 || height <= 0 {
		return AssetImageAspect{}, false
	}
	actual := float64(width) / float64(height)
	out := AssetImageAspect{Requested: requested, Width: width, Height: height, Matches: math.Abs(actual-ratio)/ratio <= assetImageAspectTolerance}
	if !out.Matches {
		out.Message = fmt.Sprintf("要求的画幅是 %s，上游实际返回 %d×%d，比例不符。图片已按原样保存、没有裁切，也没有自动重新生成；需要时请换图片模型或线路后手动重新出图", requested, width, height)
	}
	return out, true
}

// checkAssetImageAspect 在资产出图收尾时核对实际返回比例：按任务里 config.size（服务端按资产类型写的画幅）比，结果写进
// result.imageAspect，不符时另记一条任务告警。只提示，不裁图、不重发计费请求，也不挡绑定。
func (s *Service) checkAssetImageAspect(task model.Task, inputJSON string, resourceID string, result map[string]interface{}) {
	var input struct {
		Config struct {
			Size string `json:"size"`
		} `json:"config"`
		Metadata struct {
			Ratio string `json:"assetImageRatio"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil {
		return
	}
	requested := input.Metadata.Ratio
	if requested == "" {
		requested = input.Config.Size
	}
	resource, err := s.repo.ResourceForUser(task.UserID, strings.TrimSpace(resourceID))
	if err != nil {
		return
	}
	check, ok := compareAssetImageAspect(requested, resource.Width, resource.Height)
	if !ok {
		return
	}
	result["imageAspect"] = check
	if !check.Matches {
		_ = s.log(task.UserID, task.ID, "warn", "资产图片比例与要求不符", check.Message)
	}
}
