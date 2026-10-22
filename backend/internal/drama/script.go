package drama

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 章节正文 → 剧本契约（与 script.py from-md 一致）：场标题行以「场」「INT./EXT.」「内·/外·」或 "## " 开头，
// 「人物：台词」是台词，其余是动作；台词按场编 id（E01S03L07），动作编 E01S03B02。

var (
	beatIDPattern      = regexp.MustCompile(`^E\p{Nd}{2}S\p{Nd}{2}[a-z]?[LB]\p{Nd}{2,3}$`)
	beatIDTailPattern  = regexp.MustCompile(`([LB])(\p{Nd}+)$`)
	sceneHeadingPrefix = regexp.MustCompile(`^(第?[一二三四五六七八九十\p{Nd}]+场|场[\s\p{Z}]*\p{Nd}+|INT\.|EXT\.|内[·.]|外[·.])`)
	scriptLinePattern  = regexp.MustCompile(`^([^：:（(]{1,8})(（[^）]*）)?[：:][\s\p{Z}]*[“"]?(.+?)[”"]?$`)
	lineBreakPattern   = regexp.MustCompile(`\r\n|[\n\r\v\f\x1c\x1d\x1e\x{85}\x{2028}\x{2029}]`)
)

// AssignBeatIDs 按场重编节拍 id，已有合规 id 的不动（script.py assign_ids）。
func AssignBeatIDs(script *Script) {
	for si := range script.Scenes {
		sc := &script.Scenes[si]
		nl, nb := 0, 0
		for _, b := range sc.Beats {
			if beatIDPattern.MatchString(b.ID) && strings.HasPrefix(b.ID, sc.ID) {
				m := beatIDTailPattern.FindStringSubmatch(b.ID)
				n, _ := strconv.Atoi(m[2])
				if m[1] == "L" {
					nl = max(nl, n)
				} else {
					nb = max(nb, n)
				}
			}
		}
		for bi := range sc.Beats {
			b := &sc.Beats[bi]
			if beatIDPattern.MatchString(b.ID) && strings.HasPrefix(b.ID, sc.ID) {
				continue
			}
			if b.isLine() {
				nl++
				b.ID = fmt.Sprintf("%sL%02d", sc.ID, nl)
			} else {
				nb++
				b.ID = fmt.Sprintf("%sB%02d", sc.ID, nb)
			}
		}
	}
}

// ScriptFromText 把「人物：台词」格式的纯文本章节粗转成剧本契约（script.py from_md）。
func ScriptFromText(text string, title string) Script {
	script := Script{Title: title, Scenes: []ScriptScene{}}
	var cur *ScriptScene
	for _, raw := range lineBreakPattern.Split(text, -1) {
		ln := strings.TrimSpace(raw)
		if ln == "" {
			continue
		}
		if sceneHeadingPrefix.MatchString(ln) || strings.HasPrefix(ln, "## ") {
			heading := strings.TrimSpace(strings.TrimLeft(ln, "# "))
			script.Scenes = append(script.Scenes, ScriptScene{ID: fmt.Sprintf("E01S%02d", len(script.Scenes)+1), Heading: heading, Characters: []string{}, Beats: []ScriptBeat{}})
			cur = &script.Scenes[len(script.Scenes)-1]
			continue
		}
		if cur == nil {
			script.Scenes = append(script.Scenes, ScriptScene{ID: "E01S01", Heading: "未命名场", Characters: []string{}, Beats: []ScriptBeat{}})
			cur = &script.Scenes[len(script.Scenes)-1]
		}
		m := scriptLinePattern.FindStringSubmatch(ln)
		if m != nil && !strings.HasPrefix(ln, "（") && !strings.HasPrefix(ln, "(") {
			who, said := strings.TrimSpace(m[1]), strings.TrimSpace(m[3])
			kind := "line"
			if strings.Contains(m[2], "画外") || strings.Contains(m[2], "OS") {
				kind = "vo"
			}
			cur.Beats = append(cur.Beats, ScriptBeat{Kind: kind, Speaker: who, Text: said, Tag: "原文事实"})
			if !containsString(cur.Characters, who) {
				cur.Characters = append(cur.Characters, who)
			}
		} else {
			cur.Beats = append(cur.Beats, ScriptBeat{Kind: "action", Text: strings.Trim(ln, "（）()"), Tag: "原文事实"})
		}
	}
	AssignBeatIDs(&script)
	return script
}

// LineCount 是剧本里的台词句数（kind=line/vo）。
func (s *Script) LineCount() int {
	n := 0
	for _, sc := range s.Scenes {
		for _, b := range sc.Beats {
			if b.isLine() {
				n++
			}
		}
	}
	return n
}
