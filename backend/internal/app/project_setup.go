package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"yingce/backend/internal/drama"
)

// projectAspectRatios 是项目画幅的可选值（建项目对话框和项目设置同一份）。
var projectAspectRatios = []string{"9:16", "16:9", "1:1", "4:3"}

func validateProjectAspectRatio(value string) error {
	if !slices.Contains(projectAspectRatios, value) {
		return BadAuthRequest("项目画幅只能是 " + strings.Join(projectAspectRatios, " / "))
	}
	return nil
}

// briefSegmentSeconds 读需求文档的单段最长秒数（brief_json.segment_seconds）；没设返回 0。
func briefSegmentSeconds(briefJSON string) int {
	doc := map[string]any{}
	if raw := strings.TrimSpace(briefJSON); raw != "" {
		_ = json.Unmarshal([]byte(raw), &doc)
	}
	seconds, _ := doc["segment_seconds"].(float64)
	return int(seconds)
}

// checkSegmentSecondsForModel 校验单段秒数：1 ≤ 值 ≤ 生成模型一段的上限（kb-dialects/models.json 的 duration.max）。
func checkSegmentSecondsForModel(seconds int, generationModel string) error {
	m, err := drama.ResolveModel(generationModel)
	if err != nil {
		return BadAuthRequest("不支持的生成模型：" + strings.TrimSpace(generationModel))
	}
	if seconds < 1 || seconds > m.Duration.Max {
		return BadAuthRequest(fmt.Sprintf("单段最长时长要在 1 到 %d 秒之间（%s 一段最长 %d 秒）", m.Duration.Max, m.Label, m.Duration.Max))
	}
	return nil
}

// withBriefSegmentSeconds 把单段秒数写进需求文档，其他字段原样保留；seconds 为 0 时去掉这一项。
// 装段时它只能收紧生成模型的上限，不能放宽。
func withBriefSegmentSeconds(briefJSON string, seconds int) (string, error) {
	doc := map[string]any{}
	if raw := strings.TrimSpace(briefJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return "", BadAuthRequest("需求文档不是合法 JSON，先在需求文档里修好再改单段时长")
		}
	}
	if seconds > 0 {
		doc["segment_seconds"] = seconds
	} else {
		delete(doc, "segment_seconds")
	}
	if len(doc) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
