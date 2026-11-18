package drama

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// SceneSpace describes the space used by a scene. Confirmed facts belong to the
// exact image resource, not to the asset title or to its generation prompt.
type SceneSpace struct {
	SceneID       string      `json:"sceneId"`
	Heading       string      `json:"heading"`
	Location      string      `json:"location"`
	InOut         string      `json:"inOut,omitempty"`
	AssetID       string      `json:"assetId,omitempty"`
	VersionID     string      `json:"versionId,omitempty"`
	ResourceID    string      `json:"resourceId,omitempty"`
	Label         string      `json:"label,omitempty"`
	Ambiguous     bool        `json:"ambiguous,omitempty"`
	Defined       bool        `json:"defined,omitempty"`
	Facts         *SceneFacts `json:"facts,omitempty"`
	FactsRevision string      `json:"factsRevision,omitempty"`
}

type SceneFacts struct {
	InOut   string   `json:"inOut,omitempty"`
	Layout  string   `json:"layout,omitempty"`
	Anchors []string `json:"anchors,omitempty"`
	Absent  []string `json:"absent,omitempty"`
	Unknown []string `json:"unknown,omitempty"`
}

// SceneBinding travels with the immutable shot revision.
type SceneBinding struct {
	SceneID    string `json:"sceneId"`
	Location   string `json:"location,omitempty"`
	InOut      string `json:"inOut,omitempty"`
	VersionID  string `json:"versionId,omitempty"`
	ResourceID string `json:"resourceId,omitempty"`
	Override   bool   `json:"override,omitempty"`
	TextOnly   bool   `json:"textOnly,omitempty"`
	Pending    bool   `json:"pending,omitempty"`
}

var sceneNumber = regexp.MustCompile(`(?i)^(?:S0*([1-9][0-9]*)|第\s*([1-9][0-9]*)\s*场)$`)

func MatchSceneSpace(spaces []SceneSpace, key string) (SceneSpace, bool) {
	key = strings.TrimSpace(key)
	for _, scene := range spaces {
		if key != "" && strings.EqualFold(key, scene.SceneID) {
			return scene, true
		}
	}
	if match := sceneNumber.FindStringSubmatch(key); match != nil {
		n, _ := strconv.Atoi(firstNonBlank(match[1], match[2]))
		if n > 0 && n <= len(spaces) {
			return spaces[n-1], true
		}
		return SceneSpace{}, false
	}
	heading := ParseSceneHeading(key)
	place := heading.Location
	if place == "" {
		place = key
	}
	match := -1
	for i, scene := range spaces {
		if key == scene.Heading || (scene.Location != "" && AssetNameKey(place) == AssetNameKey(scene.Location) &&
			(heading.InOut == "" || heading.InOut == scene.InOut)) {
			if match >= 0 {
				return SceneSpace{}, false
			}
			match = i
		}
	}
	if match >= 0 {
		return spaces[match], true
	}
	return SceneSpace{}, false
}

func SceneSpacePrompt(spaces []SceneSpace, preferred SceneSpace, label string) string {
	if len(spaces) == 0 {
		return ""
	}
	var text strings.Builder
	text.WriteString("【平台场次与空间】按剧本场次编号定位，每单元只属于一场。先确认本场后再写场景背景、场景地图、人物走位与画面。光照、时间、天气和机位可按剧情变化。只引用本场的场景资产，空间结构不明确时用相对走位，并保留剧情所需的动作。\n")
	if preferred.SceneID != "" {
		spaces = []SceneSpace{preferred}
	}
	for _, scene := range spaces {
		fmt.Fprintf(&text, "%s｜%s｜%s", scene.SceneID, scene.Heading, scene.InOut)
		ref := label
		if preferred.SceneID == "" {
			ref = scene.Label
		}
		if ref != "" {
			fmt.Fprintf(&text, "｜主场景 %s", ref)
		}
		if scene.Ambiguous {
			text.WriteString("｜需要选择主场景，请使用剧本地点写草稿")
		} else if scene.Facts != nil {
			fmt.Fprintf(&text, "｜已核对布局 %s｜锚点 %s｜确认不存在 %s｜未知 %s", scene.Facts.Layout,
				strings.Join(scene.Facts.Anchors, "、"), strings.Join(scene.Facts.Absent, "、"), strings.Join(scene.Facts.Unknown, "、"))
		} else {
			text.WriteString("｜图片空间尚未核对，勿补写固定建筑与陈设")
		}
		text.WriteByte('\n')
	}
	return text.String()
}

var sceneSectionNames = []string{"场景背景", "场景地图", "首帧与走位", "空间地图", "空间调度", "开场画面"}

func sceneSectionName(line string) string {
	name := strings.TrimSpace(strings.Trim(line, "#【】*：: \t\r"))
	if slices.Contains(sceneSectionNames, name) {
		return name
	}
	return ""
}

func ScenePromptSections(prompt string) map[string]string {
	out := map[string]string{}
	key := ""
	for _, line := range strings.Split(prompt, "\n") {
		if name := sceneSectionName(line); name != "" {
			key = name
			continue
		}
		if isPromptSection(line) {
			key = ""
		}
		if key != "" {
			out[key] += line + "\n"
		}
	}
	for key, body := range out {
		out[key] = strings.TrimSpace(body)
	}
	return out
}

func isPromptSection(line string) bool {
	line = strings.TrimSpace(line)
	return slices.Contains(SSSPromptSections, sssSectionName(line)) ||
		strings.HasPrefix(line, "#") || (strings.HasPrefix(line, "【") && strings.HasSuffix(line, "】"))
}

// ReplaceSceneShotText updates the prompt and storyboard together when the
// source sentence is identifiable in a visual block.
func ReplaceSceneShotText(prompt, before, after string) (string, bool) {
	if prompt == "" || before == after {
		return prompt, true
	}
	lines := strings.Split(prompt, "\n")
	visual := false
	matches := 0
	for i, line := range lines {
		if isPromptSection(line) || sceneSectionName(line) != "" {
			name := sssSectionName(line)
			visual = sceneSectionName(line) != "" || name == "动作" || name == "格式模式" || name == "表演"
			continue
		}
		if visual && strings.Contains(line, before) {
			lines[i] = strings.ReplaceAll(line, before, after)
			matches++
		}
	}
	return strings.Join(lines, "\n"), matches > 0
}

// StripSceneDeclaration removes a standalone line for a superseded scene
// image, leaving adjacent character, prop, and continuity descriptions intact.
func StripSceneDeclaration(prompt, ref string) string {
	lines := strings.Split(prompt, "\n")
	referenceBlock := false
	out := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if i := strings.Index(line, "素材映射"); i >= 0 {
			labelEnd := i + len("素材映射")
			tail := line[labelEnd:]
			if rest, ok := strings.CutPrefix(tail, "："); ok {
				line = line[:labelEnd] + "：" + stripInlineSceneReference(rest, ref)
			} else if rest, ok := strings.CutPrefix(tail, ":"); ok {
				line = line[:labelEnd] + ":" + stripInlineSceneReference(rest, ref)
			}
		}
		if isPromptSection(line) || strings.HasPrefix(trimmed, "素材映射") {
			name := sssSectionName(line)
			referenceBlock = name == "生效参考素材" || strings.Contains(line, "素材映射")
		}
		if referenceBlock {
			for _, label := range []string{ref, strings.Replace(ref, "@图", "@图片", 1)} {
				if tail, ok := strings.CutPrefix(trimmed, label); ok && !strings.Contains(tail, "@") &&
					(strings.HasPrefix(tail, "：") || strings.HasPrefix(tail, ":") || strings.HasPrefix(tail, " ")) {
					trimmed = ""
					break
				}
			}
			if trimmed == "" && strings.TrimSpace(line) != "" {
				continue
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func stripInlineSceneReference(text, ref string) string {
	var out strings.Builder
	for text != "" {
		end := strings.IndexAny(text, "；;。")
		if end < 0 {
			end = len(text)
		}
		next := end
		mappingEnds := strings.HasPrefix(text[end:], "。")
		if end < len(text) {
			if text[end] == ';' {
				next++
			} else {
				next += len("；")
			}
		}
		clause := strings.TrimSpace(text[:end])
		remove := false
		for _, label := range []string{ref, strings.Replace(ref, "@图", "@图片", 1)} {
			if rest, ok := strings.CutPrefix(clause, label); ok && !strings.Contains(rest, "@") &&
				(strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "：") || strings.HasPrefix(rest, ":")) {
				remove = true
			}
		}
		if !remove {
			out.WriteString(text[:next])
		}
		text = text[next:]
		// A full stop ends the inline mapping.
		if mappingEnds {
			out.WriteString(text)
			break
		}
	}
	return out.String()
}

// RewriteScenePrompt only replaces the named spatial paragraphs. References,
// dialogue, sound, camera instructions and every other paragraph stay intact.
func RewriteScenePrompt(prompt string, changes map[string]string) (string, error) {
	existing := ScenePromptSections(prompt)
	for key, value := range changes {
		if _, ok := existing[key]; !ok || strings.TrimSpace(value) == "" || len([]rune(value)) > 2500 {
			return "", fmt.Errorf("无效的场景字段：%s", key)
		}
		for _, line := range strings.Split(value, "\n") {
			if sceneSectionName(line) != "" || isPromptSection(line) || strings.HasPrefix(strings.TrimSpace(line), "```") {
				return "", fmt.Errorf("%s 只能编辑当前段落正文", key)
			}
		}
	}
	lines := strings.Split(prompt, "\n")
	out := make([]string, 0, len(lines))
	replacing := false
	for _, line := range lines {
		key := sceneSectionName(line)
		if key != "" || isPromptSection(line) {
			replacing = false
		}
		if replacement, ok := changes[key]; ok {
			out = append(out, line, strings.TrimSpace(replacement), "")
			replacing = true
		} else if !replacing {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n"), nil
}

// SceneConflicts only reports evidence of an explicit contradiction. Cropping,
// occlusion, changing light and camera-relative left/right are valid choices.
func SceneConflicts(seg Segment, space SceneSpace) []string {
	if space.Facts == nil {
		return nil
	}
	sections := ScenePromptSections(seg.Verbatim)
	names := append(slices.Clone(sceneSectionNames), "镜头画面")
	for _, shot := range seg.Shots {
		sections["镜头画面"] += shot.Text + "\n"
	}
	for _, name := range []string{"格式模式", "动作", "表演"} {
		if body, found := SSSSection(seg.Verbatim, name); found {
			sections["镜头画面"] += body + "\n"
		}
	}
	var conflicts []string
	for _, name := range names {
		for _, line := range strings.FieldsFunc(sections[name], func(r rune) bool { return strings.ContainsRune("\n。；", r) }) {
			line = strings.TrimSpace(line)
			// Absence means verified absence in the physical set, not merely
			// a feature that happens to be outside the current crop.
			for _, anchor := range space.Facts.Absent {
				if anchor != "" && strings.Contains(line, anchor) && !deniesSceneAnchor(line, anchor) {
					conflicts = append(conflicts, fmt.Sprintf("%s「%s」使用了场景图核对为不存在的「%s」", name, line, anchor))
				}
			}
			if name == "场景背景" || name == "场景地图" {
				in, out := strings.Contains(line, "室内"), strings.Contains(line, "室外") || strings.Contains(line, "露天")
				if (space.Facts.InOut == "内" && out && !in) || (space.Facts.InOut == "外" && in && !out) {
					conflicts = append(conflicts, fmt.Sprintf("%s「%s」与图片核对的内外景冲突", name, line))
				}
			}
		}
	}
	return slices.Compact(conflicts)
}

func deniesSceneAnchor(line, anchor string) bool {
	for _, prefix := range []string{"无", "没有", "未见", "看不见", "看不到"} {
		if strings.Contains(line, prefix+anchor) {
			return true
		}
	}
	return false
}
