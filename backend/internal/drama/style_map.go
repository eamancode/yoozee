package drama

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// 柚子项目画风 → yoozee 风格档案的对照表（style_map.json）。画风决定检查和装段用哪份档案的配额、光线政策与表演块；
// 视频提示词的风格段另取项目画风的执行规范（CompileProfile.ProjectStyleBlock），档案的 style_block 只在项目没选画风时兜底。

//go:embed style_map.json
var styleMapJSON []byte

// StyleMapRule 是对照表的一条：medium / world 是柚子画风选择（canvas-style-system.ts 的 id），keywords 匹配自定义画风的标题和标签；
// 写了的条件都要满足，先写的规则先匹配。
type StyleMapRule struct {
	Label    string   `json:"label"`
	Medium   []string `json:"medium,omitempty"`
	World    []string `json:"world,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Profile  string   `json:"profile"`
}

type StyleMap struct {
	Default string         `json:"default"`
	Rules   []StyleMapRule `json:"rules"`
}

// ProjectStyle 是对照用的项目画风：选择项（系统预设组合）和标题、标签（自定义画风只有这些）。
type ProjectStyle struct {
	World  string
	Medium string
	Title  string
	Tags   []string
}

var (
	styleMapOnce sync.Once
	styleMap     StyleMap
	styleMapErr  error
)

// BuiltinStyleMap 是内置对照表；每条规则的档案都必须是内置档案。
func BuiltinStyleMap() (StyleMap, error) {
	styleMapOnce.Do(func() {
		if err := json.Unmarshal(styleMapJSON, &styleMap); err != nil {
			styleMapErr = fmt.Errorf("画风对照表格式错误: %w", err)
			return
		}
		ids := []string{styleMap.Default}
		for _, r := range styleMap.Rules {
			ids = append(ids, r.Profile)
		}
		for _, id := range ids {
			if _, err := BuiltinProfile(id); err != nil {
				styleMapErr = fmt.Errorf("画风对照表引用了不存在的风格档案 %s", id)
				return
			}
		}
	})
	return styleMap, styleMapErr
}

// ProfileFor 返回画风对照出的档案 ID 和命中的规则说明；都没命中时取默认档案，说明为空。
func (m StyleMap) ProfileFor(style ProjectStyle) (string, string) {
	text := style.Title + " " + strings.Join(style.Tags, " ")
	for _, r := range m.Rules {
		if len(r.Medium) > 0 && !slices.Contains(r.Medium, style.Medium) {
			continue
		}
		if len(r.World) > 0 && !slices.Contains(r.World, style.World) {
			continue
		}
		if len(r.Keywords) > 0 && !slices.ContainsFunc(r.Keywords, func(k string) bool { return strings.Contains(text, k) }) {
			continue
		}
		return r.Profile, r.Label
	}
	return m.Default, ""
}
