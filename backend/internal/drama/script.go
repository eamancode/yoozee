package drama

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 章节正文 → 剧本契约（script.py from-md）：切场走 SplitScenes（场次头规则见 scenes.go，连续同地点并成一场，这点与 script.py 不同），
// 「人物：台词」是台词，其余是动作；台词按场编 id（E01S03L07），动作编 E01S03B02。

var (
	beatIDPattern     = regexp.MustCompile(`^E\p{Nd}{2}S\p{Nd}{2}[a-z]?[LB]\p{Nd}{2,3}$`)
	beatIDTailPattern = regexp.MustCompile(`([LB])(\p{Nd}+)$`)
	scriptLinePattern = regexp.MustCompile(`^([^：:（(]{1,8})(（[^）]*）)?[：:][\s\p{Z}]*[“"]?(.+?)[”"]?$`)
	lineBreakPattern  = regexp.MustCompile(`\r\n|[\n\r\v\f\x1c\x1d\x1e\x{85}\x{2028}\x{2029}]`)
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

// ScriptFromText 把「人物：台词」格式的纯文本章节粗转成剧本契约（script.py from_md）：场取自 SplitScenes，
// 没有场次头的场（整章一场、第一个场次头前的开场）场次头写「未命名场」；并场时后面几个场次头行不进节拍。
func ScriptFromText(text string, title string) Script {
	script := Script{Title: title, Scenes: []ScriptScene{}, SceneRule: SceneRuleMerged}
	for _, sc := range SplitScenes(text, title) {
		heading := sc.Heading
		if len(sc.Sources) == 0 {
			heading = UntitledSceneHeading
		}
		beats, characters := sceneBeats(sc.Body)
		script.Scenes = append(script.Scenes, ScriptScene{ID: sc.ID, Heading: heading, Characters: characters, Beats: beats})
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
