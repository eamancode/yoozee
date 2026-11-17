package drama

import (
	"fmt"
	"regexp"
	"strings"
)

// RemoveDirectorUnit edits the current episode's card section and unit-indexed
// tables. Other units keep their original Markdown, including their paste blocks.
func RemoveDirectorUnit(doc, unit string) (string, error) {
	headers := sssH2.FindAllStringSubmatchIndex(doc, -1)
	var out strings.Builder
	found := 0
	for i, h := range headers {
		if i == 0 {
			out.WriteString(doc[:h[0]])
		}
		end := len(doc)
		if i+1 < len(headers) {
			end = headers[i+1][0]
		}
		title := sssCompact(doc[h[2]:h[3]])
		if title == "返修记录"+unit {
			continue
		}
		out.WriteString(doc[h[0]:h[1]])
		body := doc[h[1]:end]
		switch {
		case strings.HasPrefix(title, "第五部分"), strings.HasPrefix(title, "五、单元卡"):
			pattern := sssCardHead
			if strings.HasPrefix(title, "五、") {
				pattern = ontologyUnitHeader
			}
			cards := pattern.FindAllStringSubmatchIndex(body, -1)
			start := 0
			for j, c := range cards {
				if body[c[2]:c[3]] != unit {
					continue
				}
				found++
				out.WriteString(body[start:c[0]])
				start = len(body)
				if j+1 < len(cards) {
					start = cards[j+1][0]
				}
				if len(cards) == 1 {
					out.WriteString("本集无\n\n")
				}
			}
			out.WriteString(body[start:])
		case strings.HasPrefix(title, "第四部分"):
			out.WriteString(withoutDirectorTableRow(body, unit, sssUnitID))
		case strings.HasPrefix(title, "三、节奏总表"), strings.HasPrefix(title, "七、自检结果"):
			out.WriteString(withoutDirectorTableRow(body, unit, ontologyUnitID))
		default:
			out.WriteString(body)
		}
	}
	if found != 1 {
		return "", fmt.Errorf("导演文档中应有一张编号为 %s 的单元卡，实际找到 %d 张", unit, found)
	}
	return out.String(), nil
}

func withoutDirectorTableRow(body, unit string, pattern *regexp.Regexp) string {
	var out strings.Builder
	for line := range strings.SplitAfterSeq(body, "\n") {
		if cells, row := strings.CutPrefix(strings.TrimSpace(line), "|"); row {
			first, _, _ := strings.Cut(cells, "|")
			if pattern.FindString(first) == unit {
				continue
			}
		}
		out.WriteString(line)
	}
	return out.String()
}
