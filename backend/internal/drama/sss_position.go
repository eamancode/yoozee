package drama

import "strings"

func SegmentEndsWithFreeze(seg Segment) bool {
	return strings.TrimSpace(seg.Verbatim) != "" && len(seg.Shots) > 0 && strings.Contains(seg.Shots[len(seg.Shots)-1].Purpose, "定格")
}

func positionToken(raw string) bool { return raw == "@站位参考" || raw == "@站位参考1" }

func ApplyPositionReference(prompt, tag string) string {
	var b strings.Builder
	last := 0
	for _, m := range sssScanTags(prompt, nil) {
		if !positionToken(m.raw) {
			continue
		}
		b.WriteString(prompt[last:m.start])
		b.WriteString(tag)
		last = m.end
	}
	b.WriteString(prompt[last:])
	return b.String()
}

func StripPositionReference(prompt string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(prompt, "\n") {
		if ApplyPositionReference(line, "") != line {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}
