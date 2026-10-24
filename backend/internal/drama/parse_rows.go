package drama

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// 生成分段的解析器：文本任务的模型输出 → 分镜行。与前端 storyboardRowsFromTask（canvas-project-domain.ts）同一套规则，
// 服务端评测（M6 第三期）用它解析真跑出来的结果，再交给 RowsToShotlist、检查和装段。

// storyboardTextFields 是前端 normalizeStoryboardRow 会把对象、数组收成字符串的字段。
var storyboardTextFields = []string{
	"plotDescription", "dialogue", "narrativeIntent", "viewerPOV", "performanceBlocking", "shotSize", "emotion",
	"lightingAndAtmosphere", "audioEffects", "camera", "motion", "timeBeats", "imageGenerationPrompt", "videoMotionPrompt",
	"continuityOut", "negativePrompt",
}

var (
	storyboardFenceStart = regexp.MustCompile("(?i)^```(?:json)?\\s*")
	storyboardFenceEnd   = regexp.MustCompile("```\\s*$")
)

// ErrNoStoryboardRows 是模型输出里找不到镜头行（前端报「分镜任务没有返回镜头行」）。
var ErrNoStoryboardRows = errors.New("分镜任务没有返回镜头行")

// StoryboardRowsFromResult 读文本任务的结果 JSON（{"text": …} 或直接带 rows），返回规整后的分镜行：
// 文本字段收成字符串，characters / mustHave / optionalDetails 不是数组时置空，shotNumber 按顺序重排，缺 durationSeconds 时取 6。
func StoryboardRowsFromResult(resultJSON string) ([]map[string]any, error) {
	payload, err := storyboardRowsPayload(resultJSON)
	if err != nil {
		return nil, err
	}
	rawRows, _ := payload["rows"].([]any)
	if len(rawRows) == 0 {
		return nil, ErrNoStoryboardRows
	}
	rows := make([]map[string]any, 0, len(rawRows))
	for i, raw := range rawRows {
		source, _ := raw.(map[string]any)
		row := map[string]any{"durationSeconds": float64(6)}
		for k, v := range source {
			row[k] = v
		}
		for _, field := range storyboardTextFields {
			row[field] = storyboardText(source[field])
		}
		row["shotNumber"] = i + 1
		for _, field := range []string{"characters", "mustHave", "optionalDetails"} {
			if _, ok := source[field].([]any); !ok {
				row[field] = []any{}
			}
		}
		row["assetBindings"] = normalizeRowAssetBindings(source["assetBindings"])
		rows = append(rows, row)
	}
	return rows, nil
}

// storyboardAssetRoles 是前端 STORYBOARD_ASSET_ROLES。
var storyboardAssetRoles = []string{"character", "environment", "wardrobe", "prop", "weapon", "style", "motion", "audio"}

// normalizeRowAssetBindings 是前端 normalizeStoryboardAssetBindings（不带画布节点）：nodeId 非空、角色认识、按 nodeId 去重，
// 优先级取整夹在 0～100，按优先级从高到低排。
func normalizeRowAssetBindings(value any) []any {
	items, _ := value.([]any)
	type binding struct {
		nodeID, role string
		priority     float64
	}
	var kept []binding
	seen := map[string]bool{}
	for _, item := range items {
		m, _ := item.(map[string]any)
		nodeID := strings.TrimSpace(storyboardText(m["nodeId"]))
		role, _ := m["role"].(string)
		if nodeID == "" || seen[nodeID] || !slices.Contains(storyboardAssetRoles, role) {
			continue
		}
		seen[nodeID] = true
		priority, _ := m["priority"].(float64)
		kept = append(kept, binding{nodeID, role, math.Max(0, math.Min(100, math.Round(priority)))})
	}
	slices.SortStableFunc(kept, func(a, b binding) int { return int(b.priority - a.priority) })
	out := make([]any, 0, len(kept))
	for _, b := range kept {
		out = append(out, map[string]any{"nodeId": b.nodeID, "role": b.role, "priority": b.priority})
	}
	return out
}

// storyboardRowsPayload 是 parseStoryboardRowsPayload：结果本身带 rows 就用它；否则从 text 里剥掉代码块围栏，
// 取第一个「{」到最后一个「}」再解析；text 是对象时递归读。
func storyboardRowsPayload(raw string) (map[string]any, error) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(firstNonEmptyString(raw, "{}")), &parsed); err != nil {
		return nil, fmt.Errorf("分镜任务结果不是 JSON：%w", err)
	}
	if _, ok := parsed["rows"].([]any); ok {
		return parsed, nil
	}
	if text, ok := parsed["text"].(string); ok && strings.TrimSpace(text) != "" {
		inner := storyboardFenceStart.ReplaceAllString(strings.TrimSpace(text), "")
		inner = storyboardFenceEnd.ReplaceAllString(inner, "")
		start, end := strings.Index(inner, "{"), strings.LastIndex(inner, "}")
		if start >= 0 && end > start {
			var innerParsed map[string]any
			if json.Unmarshal([]byte(inner[start:end+1]), &innerParsed) == nil {
				if _, ok := innerParsed["rows"].([]any); ok {
					return innerParsed, nil
				}
			}
		}
	}
	if nested, ok := parsed["text"].(map[string]any); ok {
		encoded, _ := json.Marshal(nested)
		if inner, err := storyboardRowsPayload(string(encoded)); err == nil {
			if _, ok := inner["rows"].([]any); ok {
				return inner, nil
			}
		}
	}
	return parsed, nil
}

// storyboardText 是前端 normalizeStoryboardText：字符串原样，数字、布尔转字符串，数组用「、」连起非空项，
// 对象依次取 text / content / value / description / name。
func storyboardText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return jsNumberString(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := storyboardText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "、")
	case map[string]any:
		for _, key := range []string{"text", "content", "value", "description", "name"} {
			if nested, ok := v[key]; ok {
				if text := storyboardText(nested); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

// jsNumberString 是 JS String(number) 对常见值的写法：整数不带小数点。
func jsNumberString(v float64) string {
	if v == float64(int64(v)) && v > -1e15 && v < 1e15 {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%v", v)
}
