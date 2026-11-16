package drama

import (
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var sssEpisodeCell = regexp.MustCompile("^(?:第\\s*|[Ee])?([0-9]+)\\s*集?$")

func SSSConceptForEpisode(concept string, episodeNo int) string {
	lines := strings.Split(concept, "\n")
	var kept []string
	inSection, inTable, found := false, false, false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			inSection = trimmed == "## 全集结构表"
			inTable = false
		}
		if inSection && strings.HasPrefix(trimmed, "|") {
			cells := strings.Split(strings.Trim(trimmed, "|"), "|")
			first := strings.TrimSpace(cells[0])
			header := !inTable && strings.Contains(first, "集") && !sssEpisodeCell.MatchString(first)
			if header {
				inTable = true
			}
			if inTable {
				m := sssEpisodeCell.FindStringSubmatch(first)
				if m != nil {
					n, _ := strconv.Atoi(m[1])
					if n == episodeNo {
						found = true
					}
					if n < episodeNo-1 || n > episodeNo+1 {
						continue
					}
				} else if strings.Trim(first, "-: ") != "" && !header {
					slog.Warn("全集结构表含无法识别的集号，保留全部材料", "episode", episodeNo)
					return concept
				}
			}
		} else if trimmed != "" {
			inTable = false
		}
		kept = append(kept, line)
	}
	if !found {
		slog.Warn("全集结构表未读到本集，保留全部材料", "episode", episodeNo)
		return concept
	}
	return strings.Join(kept, "\n")
}

var sssSceneCode = regexp.MustCompile(`(?i)^(?:第\s*([0-9一二三四五六七八九十]+)\s*场|(?:E[0-9]+)?S[0-9]+)`)

func SSSDirectForScenes(direct string, scenes []string) string {
	if len(scenes) == 0 {
		return direct
	}
	var kept, found []string
	selected := true
	for _, line := range strings.Split(direct, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "### ") {
			title := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "### "))
			code := sssSceneCode.FindString(title)
			if code == "" {
				slog.Warn("讲戏本小节无法归属场次，保留全文")
				return direct
			}
			selected = false
			for _, scene := range scenes {
				if strings.EqualFold(strings.Join(strings.Fields(code), ""), strings.Join(strings.Fields(scene), "")) || title == scene {
					selected = true
					found = append(found, scene)
				}
			}
		}
		if selected {
			kept = append(kept, line)
		}
	}
	for _, scene := range scenes {
		if !slices.Contains(found, scene) {
			slog.Warn("讲戏本未找到切分表指定的场次，保留全文")
			return direct
		}
	}
	return strings.Join(kept, "\n")
}

func SSSUnitScenes(cut, unit string) []string {
	table, _ := SSSSection(cut, "第四部分")
	column := -1
	for _, cells := range sssTableRows(table) {
		if column < 0 {
			for i, title := range cells {
				if title == "场次" || title == "所在场" || title == "场景" {
					column = i
					break
				}
			}
			continue
		}
		if cells[0] == unit && len(cells) > column {
			scenes := strings.FieldsFunc(cells[column], func(r rune) bool { return strings.ContainsRune("、，,/／", r) })
			for i := range scenes {
				scenes[i] = strings.TrimSpace(scenes[i])
			}
			return scenes
		}
	}
	return nil
}
