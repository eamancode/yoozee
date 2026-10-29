package drama

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// 小节（M8 卡 F）：一场按戏类规则切成若干小节，一节 = 一次生成 = 一段视频。切节在生成之前由代码做（不调模型）：
// 每个节拍（台词行、动作行）按字速和动作估时，再按戏类的时长口径找切点——
//   - 只在节拍之间切，不跨场；不在接话中间切：上一句是问句、被打断或没说完（？—…，：结尾），下一句换人接话，中间最多隔一个动作时不切。
//   - 文戏 15（7.3 规则）：一节 10～15 秒、最多两句完整台词（三句极短句也行）；7.3 每个片段末尾留 1 秒结束站位，
//     同场后续片段开头再锁 0.5 秒首帧，这两段计入小节时长。
//   - 文戏 30（即梦规则）：一节 4～30 秒，常规 20～30 秒。
//   - 武戏（skill-complete-v7，卡 E）：一节 N 秒 = N 镜（1 秒 1 镜），N 取整秒；打斗动作按武戏的动作密度估时（一行动作拆成好几秒的
//     攻防），一节 4～30 秒。只有用户改成武戏的那段剧本范围（GenreOverride）按武戏切，其余照默认文戏。
//   - 上限再按项目视频模型的单段上限收紧。放不下的（一句台词就超过上限）单独成节并记提醒，装段时报超长、提示拆节。
// 切法是在一场的节拍上做动态规划：节数尽量少，各节长度靠近戏类的目标长度，切点优先落在动作行前后；超上限、台词超额只是代价很大，
// 接话中间的切点直接不许。一场里有改过戏类的范围时，先按戏类把节拍分成连续的几块，每块各自按自己的戏类切，节号在场内接着编。

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
	// Est 是估时（秒，含戏类规定的首帧锁和结束站位；武戏是整秒 N，N 镜）；Max 是这一节的上限（戏类口径与模型上限取小）；
	// First 表示这是本场第一节。
	Est   float64       `json:"est"`
	Max   float64       `json:"max"`
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

// SectionPlan 是一个单元按戏类切出的小节。Genre / MaxSeconds / MinSeconds 是默认戏类（文戏，按模型上限选）的口径；
// 改了戏类的节各自带 Genre 和 Max。
type SectionPlan struct {
	Genre      ShotGenre `json:"genre"`
	MaxSeconds float64   `json:"maxSeconds"`
	MinSeconds float64   `json:"minSeconds"`
	Sections   []Section `json:"sections"`
}

// GenreOverride 是用户把一段剧本范围改成的戏类（分镜制作里把一节改成武戏）：场编号 + 正文行范围（与 Section.Lines 同一套），
// 范围里的节拍按这个戏类切节。
type GenreOverride struct {
	Scene string `json:"scene"`
	Lines [2]int `json:"lines"`
	Genre string `json:"genre"`
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

// Genres 是计划里出现的戏类，按第一次出现的先后。
func (p SectionPlan) Genres() []ShotGenre {
	var out []ShotGenre
	for _, s := range p.Sections {
		if slices.ContainsFunc(out, func(g ShotGenre) bool { return g.ID == s.Genre }) {
			continue
		}
		if g, ok := ShotGenreByID(s.Genre); ok {
			out = append(out, g)
		}
	}
	return out
}

// SectionID 是第 no 节的编号。
func SectionID(scene string, no int) string { return fmt.Sprintf("%s-P%d", scene, no) }

// PlanSections 把一个单元的正文按戏类切成小节；modelMax 是视频模型单段上限（秒，0 不收紧）；overrides 是改过戏类的剧本范围
// （为空时整章按 genre 切）。正文为空返回空计划。
func PlanSections(plainText string, title string, genre ShotGenre, modelMax int, overrides []GenreOverride) SectionPlan {
	lo, hi := genre.SectionLimits(modelMax)
	plan := SectionPlan{Genre: genre, MaxSeconds: hi, MinSeconds: lo, Sections: []Section{}}
	scenes := SplitScenes(plainText, title)
	script := ScriptFromText(plainText, title)
	for i, sc := range scenes {
		if i >= len(script.Scenes) {
			break
		}
		genreAt := func(line int) ShotGenre {
			for _, o := range overrides {
				if o.Scene == sc.ID && line >= o.Lines[0] && line <= o.Lines[1] {
					if g, ok := ShotGenreByID(o.Genre); ok {
						return g
					}
				}
			}
			return genre
		}
		var beats []SectionBeat
		var genres []ShotGenre
		for j, b := range script.Scenes[i].Beats {
			beat := SectionBeat{ID: b.ID, Kind: b.Kind, Speaker: b.Speaker, Text: b.Text}
			if j < len(sc.BodyLines) {
				beat.Line = sc.BodyLines[j]
			}
			g := genreAt(beat.Line)
			beat.Est = sectionBeatSeconds(beat, g)
			beats, genres = append(beats, beat), append(genres, g)
		}
		if len(beats) == 0 {
			continue
		}
		heading := script.Scenes[i].Heading
		no := 0
		for start := 0; start < len(beats); {
			g := genres[start]
			end := start + 1
			for end < len(beats) && genres[end].ID == g.ID {
				end++
			}
			glo, ghi := g.SectionLimits(modelMax)
			for _, part := range splitSectionBeats(beats[start:end], g, glo, ghi, start == 0) {
				no++
				plan.Sections = append(plan.Sections, newSection(sc.ID, heading, no, g, part, ghi))
			}
			start = end
		}
	}
	return plan
}

// GenreOverridesFrom 是一章镜头表存下的各节里不是文戏的那些（用户改成武戏的节）的剧本范围，作为下一次切节的 GenreOverride：
// 小节的戏类就这样按节持久化在 drama_shotlists.sections_json 里，刷新、整章重跑、只重跑几节、重装都按它切。只认对得上当前正文的节
// （场还在、这段正文行里的台词 id 与存下的一致）；正文改过的节回到默认文戏。
func GenreOverridesFrom(outputs []SectionOutput, plainText string, title string) []GenreOverride {
	var out []GenreOverride
	var lines map[string][]SectionBeat
	for _, o := range outputs {
		g, ok := ShotGenreByID(o.Genre)
		if !ok || g.IsWenxi() {
			continue
		}
		if lines == nil {
			lines = sceneLineBeats(plainText, title)
		}
		beats, ok := lines[o.Scene]
		if !ok {
			continue
		}
		var ids []string
		found := false
		for _, b := range beats {
			if b.Line < o.Lines[0] || b.Line > o.Lines[1] {
				continue
			}
			found = true
			if b.isLine() {
				ids = append(ids, b.ID)
			}
		}
		if !found || !slices.Equal(ids, nonNilStrings(o.LineIDs)) {
			continue
		}
		out = append(out, GenreOverride{Scene: o.Scene, Lines: o.Lines, Genre: g.ID})
	}
	return out
}

func nonNilStrings(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

// sceneLineBeats 是各场的节拍（编号、类型、正文行号），按场编号。
func sceneLineBeats(plainText string, title string) map[string][]SectionBeat {
	out := map[string][]SectionBeat{}
	scenes := SplitScenes(plainText, title)
	script := ScriptFromText(plainText, title)
	for i, sc := range scenes {
		if i >= len(script.Scenes) {
			break
		}
		for j, b := range script.Scenes[i].Beats {
			beat := SectionBeat{ID: b.ID, Kind: b.Kind, Speaker: b.Speaker, Text: b.Text}
			if j < len(sc.BodyLines) {
				beat.Line = sc.BodyLines[j]
			}
			out[sc.ID] = append(out[sc.ID], beat)
		}
	}
	return out
}

// newSection 是切好的一节：台词 id、说话人、估时和没满足戏类口径的提醒。
func newSection(scene, heading string, no int, genre ShotGenre, part []SectionBeat, hi float64) Section {
	sec := Section{ID: SectionID(scene, no), Scene: scene, Heading: heading, No: no, Genre: genre.ID, First: no == 1, Beats: part, LineIDs: []string{}, Speakers: []string{}, Max: hi}
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
	sec.Est = pyRound(sectionSeconds(part, genre, sec.First), 1)
	if genre.ID == ShotGenreWuxi {
		// 武戏 N 秒 N 镜：取整秒，不超过上限（超过的照样提醒）。
		sec.Est = math.Max(1, math.Ceil(sec.Est-1e-9))
	}
	if sec.Est > hi+1e-6 {
		sec.Warnings = append(sec.Warnings, fmt.Sprintf("估时 %.1f 秒，超过一节上限 %.0f 秒（台词或动作放不进一节）：装段会报超长，先拆台词或动作再生成", sec.Est, hi))
	}
	if genre.MaxLines > 0 && !linesFit(part, genre) {
		sec.Warnings = append(sec.Warnings, fmt.Sprintf("有 %d 句台词，超过%s一节 %d 句的口径（接话不能拆开）", lines, genre.Label, genre.MaxLines))
	}
	return sec
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
	if genre.ID == ShotGenreWuxi {
		// 武戏一行动作要拆成好几秒的攻防（起手 → 攻击 → 防守 → 变招 → 结果，1 秒 1 镜）：2 秒起，每个动作动词加 1.2 秒，最多算 6 个。
		return 2.0 + float64(min(verbs, 6))*1.2
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

// splitSectionBeats 把一场（或一场里同一戏类的一块）节拍切成小节（动态规划，见文件头）；sceneStart 是这一块从场首开始。
func splitSectionBeats(beats []SectionBeat, genre ShotGenre, lo, hi float64, sceneStart bool) [][]SectionBeat {
	n := len(beats)
	whole := sectionSeconds(beats, genre, sceneStart)
	if whole <= hi && linesFit(beats, genre) {
		return [][]SectionBeat{beats}
	}
	target := genre.TargetSeconds
	if target <= 0 || target > hi {
		target = hi * 0.85
	}
	sectionCost := func(a, b int) float64 {
		part := beats[a:b]
		est := sectionSeconds(part, genre, sceneStart && a == 0)
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
