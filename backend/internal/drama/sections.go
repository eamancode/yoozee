package drama

import (
	"fmt"
	"math"
	"strings"
)

// 小节（M8 卡 F）：一场按戏类规则切成若干小节，一节 = 一次生成 = 一段视频。切节在生成之前由代码做（不调模型）：
// 每个节拍（台词行、动作行）按字速和动作估时，再按戏类的时长口径找切点——
//   - 只在节拍之间切，不跨场；不在接话中间切：上一句是问句、被打断或没说完（？—…，：结尾），下一句换人接话，中间最多隔一个动作时不切。
//   - 文戏 15（7.3 规则）：一节 10～15 秒、最多两句完整台词（三句极短句也行）；7.3 每个片段末尾留 1 秒结束站位，
//     同场后续片段开头再锁 0.5 秒首帧，这两段计入小节时长。
//   - 文戏 30（即梦规则）：一节 4～30 秒，常规 20～30 秒。
//   - 上限再按项目视频模型的单段上限收紧。放不下的（一句台词就超过上限）单独成节并记提醒，装段时报超长、提示拆节。
// 切法是在一场的节拍上做动态规划：节数尽量少，各节长度靠近戏类的目标长度，切点优先落在动作行前后；超上限、台词超额只是代价很大，
// 接话中间的切点直接不许。

// SectionBeat 是小节里的一个节拍（剧本台词表的一项）。
type SectionBeat struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`
	Speaker string  `json:"speaker,omitempty"`
	Text    string  `json:"text"`
	Line    int     `json:"line"`
	Est     float64 `json:"est"`
}

// Section 是一个小节。
type Section struct {
	// ID 是小节编号：单元内场编号加「-P」和场内序号（E01S02-P1）。
	ID    string `json:"id"`
	Scene string `json:"scene"`
	// Heading 是所在场的场次头。
	Heading string `json:"heading"`
	No      int    `json:"no"`
	Genre   string `json:"genre"`
	// Lines 是这一节覆盖的正文行范围 [首行, 末行]（只数非空行，从 1 数，与场次表 lines 同一套）。
	Lines [2]int `json:"lines"`
	// LineIDs 是这一节的台词 id（台词表 E01S02L03，含画外音）；Speakers 是说话的人（按先后）。
	LineIDs  []string `json:"lineIds"`
	Speakers []string `json:"speakers"`
	// Est 是估时（秒，含戏类规定的首帧锁和结束站位）；First 表示这是本场第一节。
	Est   float64       `json:"est"`
	First bool          `json:"first,omitempty"`
	Beats []SectionBeat `json:"beats"`
	// Warnings 是切节没能满足戏类口径的地方（一句台词就超上限、台词超额）。
	Warnings []string `json:"warnings,omitempty"`
}

// Text 是这一节的剧本原文（正文行按原样，一行一个节拍；动作行带回括号）。
func (s Section) Text() string {
	lines := make([]string, 0, len(s.Beats))
	for _, b := range s.Beats {
		lines = append(lines, b.source())
	}
	return strings.Join(lines, "\n")
}

func (b SectionBeat) source() string {
	if b.Kind == "action" {
		return "（" + b.Text + "）"
	}
	if b.Kind == "vo" {
		return b.Speaker + "（画外）：" + b.Text
	}
	return b.Speaker + "：" + b.Text
}

func (b SectionBeat) isLine() bool { return b.Kind == "line" || b.Kind == "vo" }

// SectionPlan 是一个单元按戏类切出的小节。
type SectionPlan struct {
	Genre      ShotGenre `json:"genre"`
	MaxSeconds float64   `json:"maxSeconds"`
	MinSeconds float64   `json:"minSeconds"`
	Sections   []Section `json:"sections"`
}

// ScenesOf 是各场的小节，按场编号分组（场次表接口用）。
func (p SectionPlan) ScenesOf() map[string][]Section {
	out := map[string][]Section{}
	for _, s := range p.Sections {
		out[s.Scene] = append(out[s.Scene], s)
	}
	return out
}

// Section 查一个小节。
func (p SectionPlan) Section(id string) (Section, bool) {
	for _, s := range p.Sections {
		if s.ID == id {
			return s, true
		}
	}
	return Section{}, false
}

// SectionID 是第 no 节的编号。
func SectionID(scene string, no int) string { return fmt.Sprintf("%s-P%d", scene, no) }

// PlanSections 把一个单元的正文按戏类切成小节；modelMax 是视频模型单段上限（秒，0 不收紧）。正文为空返回空计划。
func PlanSections(plainText string, title string, genre ShotGenre, modelMax int) SectionPlan {
	lo, hi := genre.SectionLimits(modelMax)
	plan := SectionPlan{Genre: genre, MaxSeconds: hi, MinSeconds: lo, Sections: []Section{}}
	scenes := SplitScenes(plainText, title)
	script := ScriptFromText(plainText, title)
	for i, sc := range scenes {
		if i >= len(script.Scenes) {
			break
		}
		beats := make([]SectionBeat, 0, len(sc.Body))
		for j, b := range script.Scenes[i].Beats {
			beat := SectionBeat{ID: b.ID, Kind: b.Kind, Speaker: b.Speaker, Text: b.Text}
			if j < len(sc.BodyLines) {
				beat.Line = sc.BodyLines[j]
			}
			beat.Est = sectionBeatSeconds(beat, genre)
			beats = append(beats, beat)
		}
		if len(beats) == 0 {
			continue
		}
		heading := script.Scenes[i].Heading
		for k, part := range splitSectionBeats(beats, genre, lo, hi) {
			sec := Section{ID: SectionID(sc.ID, k+1), Scene: sc.ID, Heading: heading, No: k + 1, Genre: genre.ID, First: k == 0, Beats: part, LineIDs: []string{}, Speakers: []string{}}
			sec.Lines = [2]int{part[0].Line, part[len(part)-1].Line}
			lines := 0
			for _, b := range part {
				if !b.isLine() {
					continue
				}
				lines++
				sec.LineIDs = append(sec.LineIDs, b.ID)
				if !containsString(sec.Speakers, b.Speaker) {
					sec.Speakers = append(sec.Speakers, b.Speaker)
				}
			}
			sec.Est = pyRound(sectionSeconds(part, genre, k == 0), 1)
			if sec.Est > hi+1e-6 {
				sec.Warnings = append(sec.Warnings, fmt.Sprintf("估时 %.1f 秒，超过一节上限 %.0f 秒（台词或动作放不进一节）：装段会报超长，先拆台词或动作再生成", sec.Est, hi))
			}
			if genre.MaxLines > 0 && !linesFit(part, genre) {
				sec.Warnings = append(sec.Warnings, fmt.Sprintf("有 %d 句台词，超过%s一节 %d 句的口径（接话不能拆开）", lines, genre.Label, genre.MaxLines))
			}
			plan.Sections = append(plan.Sections, sec)
		}
	}
	return plan
}

var (
	sectionNotePrefixes  = []string{"音效", "画面文字", "字幕", "画外音效", "声音"}
	sectionOpenLineEnds  = []string{"？", "?", "—", "-", "…", "，", ",", "：", ":", "、"}
	sectionLineRate      = map[string]float64{ShotGenreWenxi15: 3.5, ShotGenreWenxi30: 4.5, ShotGenreWuxi: 4.5}
	sectionVoiceOverRate = 3.0
)

// sectionBeatSeconds 是一个节拍的估时：台词按字速（文戏 15 按 7.3 语速表取中，比平台估时慢）加半秒反应，
// 动作按动作动词数（与镜头估时同一套：1 秒起，每个动作动词加 0.6 秒，最多算 4 个），音效、画面文字这类提示行算半秒。
func sectionBeatSeconds(b SectionBeat, genre ShotGenre) float64 {
	if b.isLine() {
		rate := sectionLineRate[genre.ID]
		if rate == 0 {
			rate = 4.5
		}
		if b.Kind == "vo" {
			rate = min(rate, sectionVoiceOverRate+0.5)
		}
		n := float64(runeLen(stripPunct(b.Text)))
		return math.Max(1.0, n/rate) + 0.5
	}
	text := strings.TrimSpace(b.Text)
	for _, p := range sectionNotePrefixes {
		if strings.HasPrefix(text, p+"：") || strings.HasPrefix(text, p+":") {
			return 0.5
		}
	}
	verbs := 0
	for _, v := range actionVerbs {
		if strings.Contains(text, v) {
			verbs++
		}
	}
	return 1.0 + float64(min(verbs, 4))*0.6
}

// sectionSeconds 是一节的估时：节拍合计，加戏类规定的固定段（文戏 15：结束站位 1 秒，同场后续节再加首帧锁 0.5 秒；其余戏类头尾各留 0.5 秒）。
func sectionSeconds(beats []SectionBeat, genre ShotGenre, first bool) float64 {
	t := 0.0
	for _, b := range beats {
		t += b.Est
	}
	switch genre.ID {
	case ShotGenreWenxi15:
		t += 1.0
		if !first {
			t += 0.5
		}
	default:
		t += 1.0
	}
	return t
}

// linesFit 是一节的台词句数在戏类口径内：不超过 MaxLines 句，或多一句但几句合计不超过 ShortLinesChars 字（极短句）。
func linesFit(beats []SectionBeat, genre ShotGenre) bool {
	if genre.MaxLines <= 0 {
		return true
	}
	n, chars := 0, 0
	for _, b := range beats {
		if b.isLine() {
			n++
			chars += runeLen(stripPunct(b.Text))
		}
	}
	return n <= genre.MaxLines || (n == genre.MaxLines+1 && chars <= genre.ShortLinesChars)
}

// sectionCutAllowed 是能不能在第 i 个节拍之后切：不在接话中间切。
func sectionCutAllowed(beats []SectionBeat, i int) bool {
	prev, next := -1, -1
	for k := i; k >= 0; k-- {
		if beats[k].isLine() {
			prev = k
			break
		}
	}
	for k := i + 1; k < len(beats); k++ {
		if beats[k].isLine() {
			next = k
			break
		}
	}
	if prev < 0 || next < 0 || beats[prev].Speaker == beats[next].Speaker || next-prev-1 > 1 {
		return true
	}
	text := strings.TrimSpace(beats[prev].Text)
	for _, end := range sectionOpenLineEnds {
		if strings.HasSuffix(text, end) {
			return false
		}
	}
	return true
}

// sectionCutCost 是在第 i 个节拍之后切的代价：两边都是台词（对白连着说）时代价高，挨着动作行切最好。
func sectionCutCost(beats []SectionBeat, i int) float64 {
	if beats[i].isLine() && beats[i+1].isLine() {
		if beats[i].Speaker == beats[i+1].Speaker {
			return 8
		}
		return 15
	}
	return 0
}

// splitSectionBeats 把一场的节拍切成小节（动态规划，见文件头）。
func splitSectionBeats(beats []SectionBeat, genre ShotGenre, lo, hi float64) [][]SectionBeat {
	n := len(beats)
	whole := sectionSeconds(beats, genre, true)
	if whole <= hi && linesFit(beats, genre) {
		return [][]SectionBeat{beats}
	}
	target := genre.TargetSeconds
	if target <= 0 || target > hi {
		target = hi * 0.85
	}
	sectionCost := func(a, b int) float64 {
		part := beats[a:b]
		est := sectionSeconds(part, genre, a == 0)
		cost := 100.0
		if est > hi+1e-6 {
			cost += 10000 + (est-hi)*100
		}
		if est < lo && !(a == 0 && b == n) {
			cost += (lo - est) * 20
		}
		if !linesFit(part, genre) {
			lines := 0
			for _, x := range part {
				if x.isLine() {
					lines++
				}
			}
			cost += 300 * float64(max(1, lines-genre.MaxLines))
		}
		d := est - target
		return cost + d*d*0.5
	}
	best := make([]float64, n+1)
	from := make([]int, n+1)
	for j := 1; j <= n; j++ {
		best[j] = math.Inf(1)
		for i := 0; i < j; i++ {
			if math.IsInf(best[i], 1) {
				continue
			}
			cut := 0.0
			if i > 0 {
				if !sectionCutAllowed(beats, i-1) {
					continue
				}
				cut = sectionCutCost(beats, i-1)
			}
			if c := best[i] + cut + sectionCost(i, j); c < best[j] {
				best[j], from[j] = c, i
			}
		}
	}
	var bounds []int
	for j := n; j > 0; j = from[j] {
		bounds = append([]int{j}, bounds...)
	}
	out := make([][]SectionBeat, 0, len(bounds))
	start := 0
	for _, end := range bounds {
		out = append(out, beats[start:end])
		start = end
	}
	return out
}
