package drama

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 小节输出转换器（M8 卡 F / 卡 E）：按小节生成时模型用技能原生格式写（文戏 15 是 7.3 的 Markdown 片段，文戏 30 是即梦的导演分镜字段，
// 武戏是 v7 的每镜标准格式），
// 每个戏类一个转换器把一节的原样输出转成统一镜头表的分镜行（行字段 + spec，chapter.shots@3 的一镜），之后绑资产、检查、装段、编译照旧。
// 共同规则（卡 0 文档 §6.3）：
//   - 台词按原文对本节台词表落 line_id；一句被拆到相邻两镜时合回第一镜，不相邻的重复留给 R-SHOT-001 报；没写到的台词记进统计。
//   - 片段级（小节级）的站位、灯光、音效下沉到每一镜；室内镜头补光源锚点（技能的主光分句 → 场景锚点里的光源）。
//   - 「BGM / 配乐 / 背景音乐」这类字样不进可编译字段（sfx / voice / action），只留在原样输出里。
//   - blocking.who、行 characters 用资产原名（子卡写「名·变体」），findRefs 靠它挑身份参考图。
//   - 解析不出的字段留空并记进统计，不编。

// SectionContext 是转换一节输出要用的上下文：小节本身（场号、台词表），本场前面各节已经写了几镜（镜头 id 接着编），
// 项目资产名和本场场景锚点（按名字对资产、补光源锚点）。
type SectionContext struct {
	Section    Section
	ShotOffset int
	Characters []string
	Places     []string
	Props      []string
	Anchors    []string
	// Indoor 是本场是内景（场次头写了「内」）：每一镜都要有光源锚点。
	Indoor bool
}

// SectionConvertStats 是一节的转换统计（任务结果、镜头表检查的解析统计里按节列出）。
type SectionConvertStats struct {
	// Blocks 是读到的片段 / 小节头数；Shots 是转出的镜头；Rejected 是像镜头行却读不出时间码的行。
	Blocks   int `json:"blocks"`
	Shots    int `json:"shots"`
	Rejected int `json:"rejected,omitempty"`
	// Lines 是写进镜头的台词句数；LineUnmatched 是对不上本节台词表原文的；LineMerged 是一句拆在相邻两镜、合回第一镜的；
	// LineDuplicate 是同一句落了不相邻的两镜（留给 R-SHOT-001 报）；LineMissing 是本节台词表里没写到的。
	Lines         int `json:"lines"`
	LineUnmatched int `json:"lineUnmatched,omitempty"`
	LineMerged    int `json:"lineMerged,omitempty"`
	LineDuplicate int `json:"lineDuplicate,omitempty"`
	LineMissing   int `json:"lineMissing,omitempty"`
	// NoLight 是室内镜头补不出光源锚点的镜数；MusicStripped 是从可编译字段里剥掉的配乐字样处数；NoSize 是认不出景别的镜数。
	NoLight       int `json:"noLight,omitempty"`
	MusicStripped int `json:"musicStripped,omitempty"`
	NoSize        int `json:"noSize,omitempty"`
}

// SectionShots 是一节转出的分镜行。Fields 是小节级的原样字段（7.3 的要素提取、当前空间站位，即梦的小节头和小节尾），按出现顺序。
type SectionShots struct {
	Section string              `json:"section"`
	Genre   string              `json:"genre"`
	Rows    []map[string]any    `json:"-"`
	Stats   SectionConvertStats `json:"stats"`
	Fields  []SectionField      `json:"fields,omitempty"`
}

// SectionField 是一个原样字段。
type SectionField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ErrNoSectionShots 是一节的输出里读不出镜头（模型写成了解释文字、换了格式）。
var ErrNoSectionShots = errors.New("这一节的输出里读不出镜头")

// ConvertSection 把一节的原样输出转成分镜行；genre 不认识或读不出镜头时返回错误。
func ConvertSection(genre string, raw string, ctx SectionContext) (SectionShots, error) {
	var out SectionShots
	switch genre {
	case ShotGenreWenxi15:
		out = convertWenxi15(raw, ctx)
	case ShotGenreWenxi30:
		out = convertWenxi30(raw, ctx)
	case ShotGenreWuxi:
		out = convertWuxi(raw, ctx)
	default:
		return SectionShots{}, fmt.Errorf("戏类 %s 还没有转换器", genre)
	}
	out.Section, out.Genre = ctx.Section.ID, genre
	if len(out.Rows) == 0 {
		return out, ErrNoSectionShots
	}
	return out, nil
}

// CountSectionTurn 是按小节生成一轮的当轮校验：读得出镜头就算这一轮写成了，返回镜头数。
func CountSectionTurn(genre string) func(text string, turn TurnContext) (int, error) {
	return func(text string, turn TurnContext) (int, error) {
		conv, err := ConvertSection(genre, text, SectionContext{Section: Section{ID: turn.Turn.Section, Scene: turn.Turn.ID}})
		if err != nil {
			return 0, err
		}
		return len(conv.Rows), nil
	}
}

// sectionShot 是转换器读出的一镜（转成分镜行之前）。
type sectionShot struct {
	spec Shot
	// lines 是这一镜写的台词（说话人、原文、声音身份）。
	lines []sectionLine
	// plot 是行 plotDescription（画面；不写时用动作）；names 是这一镜提到的资产名（角色、场景、道具）。
	plot  string
	names []string
	// dialogue 是行 dialogue（「人物：台词」一行一句），finishSectionShots 填。
	dialogue string
}

type sectionLine struct {
	speaker, text, voice string
	offscreen            bool
	pace, tone, emotion  string
	placement            string
}

var (
	sectionSizeWords = []string{"大远景", "大特写", "中全景", "中近景", "远景", "全景", "中景", "近景", "特写"}
	sectionMoveWords = []struct{ word, move string }{
		{"甩镜", "whip"}, {"甩", "whip"}, {"快摇", "whip"},
		{"推近", "push"}, {"推镜", "push"}, {"推进", "push"}, {"推向", "push"}, {"急推", "push"}, {"缓推", "push"}, {"慢推", "push"},
		{"拉远", "pull"}, {"拉镜", "pull"}, {"拉开", "pull"}, {"后拉", "pull"}, {"急拉", "pull"},
		{"焦点转移", "rack_focus"}, {"转焦", "rack_focus"}, {"拉焦", "rack_focus"}, {"焦点转换", "rack_focus"}, {"虚实转换", "rack_focus"},
		{"横移", "truck"}, {"移镜", "truck"}, {"平移", "truck"},
		{"跟拍", "track"}, {"跟移", "track"}, {"跟随", "track"}, {"跟镜", "track"},
		{"环绕", "orbit"}, {"绕拍", "orbit"},
		{"升起", "crane_up"}, {"上升", "crane_up"}, {"升镜", "crane_up"},
		{"下降", "crane_down"}, {"降镜", "crane_down"},
		{"上摇", "tilt"}, {"下摇", "tilt"}, {"俯仰摇", "tilt"}, {"摇移", "pan"}, {"摇镜", "pan"}, {"横摇", "pan"}, {"摇", "pan"},
		{"变焦", "zoom"},
		{"手持剧烈", "handheld_violent"}, {"剧烈晃动", "handheld_violent"}, {"手持微晃", "handheld_micro"}, {"手持", "handheld"},
		{"固定机位", "fixed"}, {"固定", "fixed"}, {"定镜", "fixed"}, {"静止", "fixed"},
	}
	sectionYWords = []struct{ word, y string }{
		{"虫视", "Y1"}, {"极低机位", "Y1"}, {"微仰", "Y3"}, {"仰拍", "Y2"}, {"低机位", "Y2"}, {"低角度", "Y2"},
		{"顶视", "Y7"}, {"鸟瞰", "Y7"}, {"俯瞰", "Y7"}, {"微俯", "Y5"}, {"俯拍", "Y6"}, {"高机位", "Y6"}, {"平视", "Y4"},
	}
	sectionXWords = []struct{ word, x string }{
		{"过肩", "X2"}, {"侧45", "X2"}, {"斜侧", "X2"}, {"侧面", "X3"}, {"侧拍", "X3"}, {"正侧", "X3"}, {"背面", "X4"}, {"背后", "X4"}, {"背影", "X4"}, {"正面", "X1"},
	}
	sectionLensPattern   = regexp.MustCompile(`(\d{2,3})\s*mm`)
	sectionClauseSep     = regexp.MustCompile(`[，,；;。]`)
	sectionNameSep       = regexp.MustCompile(`[，,、；;/／\s]+`)
	sectionLightWords    = []string{"灯", "窗", "光", "烛", "火", "屏"}
	sectionSpeechQuoted  = regexp.MustCompile(`[“"]([^”"]+)[”"]`)
	sectionNumber        = regexp.MustCompile(`[\d.]+`)
	sectionWideSizes     = map[string]bool{"大远景": true, "远景": true, "全景": true, "中全景": true}
	sectionCloseSizes    = map[string]int{"大远景": 9, "远景": 8, "全景": 7, "中全景": 6, "中景": 5, "中近景": 4, "近景": 3, "特写": 2, "大特写": 1}
	sectionOffscreenTags = []string{"画外", "旁白", "内心独白", "独白", "广播", "电话", "系统音", "离画", "OS", "VO"}
)

// sectionSize 是一段镜头语言里的景别（先出现的那个）；「特写至中景」这种起止景别不同的，第二个返回 to。
func sectionSize(text string) (string, string) {
	best, at := "", -1
	for _, w := range sectionSizeWords {
		if i := strings.Index(text, w); i >= 0 && (at < 0 || i < at || (i == at && len(w) > len(best))) {
			best, at = w, i
		}
	}
	if best == "" {
		return "", ""
	}
	rest := text[at+len(best):]
	for _, sep := range []string{"至", "到", "->", "→", "—>", "转"} {
		if i := strings.Index(rest, sep); i >= 0 && i <= 3 {
			if to, _ := sectionSize(rest[i+len(sep):]); to != "" && to != best {
				return best, to
			}
		}
	}
	return best, ""
}

// sectionMoves 是镜头语言里的运镜（最多两项；fixed 不与别的同列，push / pull 只留先出现的）。
func sectionMoves(text string) []string {
	type hit struct {
		at   int
		move string
	}
	var hits []hit
	taken := make([]bool, len(text))
	for _, w := range sectionMoveWords {
		from := 0
		for {
			i := strings.Index(text[from:], w.word)
			if i < 0 {
				break
			}
			i += from
			free := true
			for k := i; k < i+len(w.word); k++ {
				if taken[k] {
					free = false
					break
				}
			}
			if free {
				for k := i; k < i+len(w.word); k++ {
					taken[k] = true
				}
				hits = append(hits, hit{i, w.move})
			}
			from = i + len(w.word)
		}
	}
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].at < hits[j-1].at; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	var moves []string
	for _, h := range hits {
		if containsString(moves, h.move) {
			continue
		}
		if (h.move == "push" && containsString(moves, "pull")) || (h.move == "pull" && containsString(moves, "push")) {
			continue
		}
		moves = append(moves, h.move)
	}
	if len(moves) > 1 {
		kept := moves[:0]
		for _, m := range moves {
			if m != "fixed" {
				kept = append(kept, m)
			}
		}
		moves = kept
	}
	if len(moves) > 2 {
		moves = moves[:2]
	}
	return moves
}

// sectionCam 是镜头语言里的机位：高度、方位、焦段、景深和过肩。
func sectionCam(text string, size string) Cam {
	cam := Cam{Z: zOfSize[size], Y: "Y4"}
	for _, w := range sectionYWords {
		if strings.Contains(text, w.word) {
			cam.Y = w.y
			break
		}
	}
	for _, w := range sectionXWords {
		if strings.Contains(text, w.word) {
			cam.X = w.x
			break
		}
	}
	f := CamF{}
	if m := sectionLensPattern.FindStringSubmatch(text); m != nil {
		if mm, err := strconv.ParseFloat(m[1], 64); err == nil {
			f.LensMM = &mm
		}
	}
	switch {
	case containsAny(text, "浅景深", "大光圈", "背景虚化"):
		f.Dof = "shallow"
	case containsAny(text, "深景深", "大景深"):
		f.Dof = "deep"
	}
	if strings.Contains(text, "过肩") {
		f.Identity = "ots"
	} else if containsAny(text, "主观视角", "主观镜头", "POV") {
		f.Identity = "pov"
	}
	if f.LensMM != nil || f.Dof != "" || f.Identity != "" {
		cam.F = &f
	}
	return cam
}

// stripMusic 去掉文字里提到配乐的分句（含「禁止 BGM / 配乐」这类否定句：R-SHOT-017 按子串查，否定句也会命中），返回剩下的文字和去掉了几处。
func stripMusic(text string) (string, int) {
	if !containsAny(text, bgmWords...) {
		return text, 0
	}
	var kept []string
	n := 0
	for _, p := range sectionClauseSep.Split(text, -1) {
		switch p = strings.TrimSpace(p); {
		case p == "":
		case containsAny(p, bgmWords...):
			n++
		default:
			kept = append(kept, p)
		}
	}
	out := strings.Join(kept, "，")
	if out != "" && hasSentenceEnd(strings.TrimSpace(text)) {
		out += "。"
	}
	return out, n
}

// resolveSectionName 把技能写的名字对到资产原名：同名优先，其次唯一的包含关系（「林小满」对「林小满·病号服」只有一张子卡时）；对不上原样返回。
func resolveSectionName(name string, names []string) string {
	name = strings.Trim(strings.TrimSpace(name), "@【】[]「」")
	if name == "" {
		return ""
	}
	for _, n := range names {
		if n == name {
			return n
		}
	}
	var hits []string
	for _, n := range names {
		if strings.Contains(n, name) || strings.Contains(name, n) {
			hits = append(hits, n)
		}
	}
	if len(hits) == 1 {
		return hits[0]
	}
	// 写的是本名、资产里本名和子卡都有时用本名。
	for _, n := range hits {
		if base, _, _ := strings.Cut(n, "·"); base == name {
			return n
		}
	}
	return name
}

// mentionedNames 是文字里出现的名字（按 names 的顺序，去重）。
func mentionedNames(text string, names []string) []string {
	var out []string
	for _, n := range names {
		base, _, _ := strings.Cut(n, "·")
		if n != "" && (strings.Contains(text, n) || strings.Contains(text, base)) && !containsString(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// sectionAnchorIn 是一段文字里出现的场景锚点（最长的那个）。
func sectionAnchorIn(text string, anchors []string) string {
	best := ""
	for _, a := range anchors {
		if a != "" && strings.Contains(text, a) && len(a) > len(best) {
			best = a
		}
	}
	return best
}

// sectionLightAnchor 是室内镜头的光源锚点：先取技能写的主光分句里的场景锚点（没有锚点就用分句原文），
// 再取场景锚点里像光源的（灯、窗……）；都没有返回空串。
func sectionLightAnchor(clause string, anchors []string) string {
	if clause = strings.TrimSpace(clause); clause != "" {
		if a := sectionAnchorIn(clause, anchors); a != "" {
			return a
		}
		return jsTruncate(clause, 40)
	}
	for _, a := range anchors {
		if containsAny(a, sectionLightWords...) {
			return a
		}
	}
	return ""
}

// firstClauseWith 是第一个含有任一关键词的分句。
func firstClauseWith(text string, words ...string) string {
	for _, c := range sectionClauseSep.Split(text, -1) {
		if containsAny(c, words...) {
			return strings.TrimSpace(c)
		}
	}
	return ""
}

// finishSectionShots 是两个转换器共用的收尾：台词落 line_id（相邻重复合回第一镜）、镜头 id 接着本场往下编、分镜行成形。
func finishSectionShots(shots []sectionShot, ctx SectionContext, stats *SectionConvertStats) []map[string]any {
	beats := map[string]SectionBeat{}
	var order []string
	for _, b := range ctx.Section.Beats {
		if b.isLine() {
			beats[b.ID] = b
			order = append(order, b.ID)
		}
	}
	lastShot := map[string]int{}
	for i := range shots {
		sh := &shots[i]
		var kept []ShotLine
		var dialogue []string
		for _, ln := range sh.lines {
			if normLine(ln.text) == "" {
				continue
			}
			id := matchSectionLine(ln, order, beats)
			out := ShotLine{Pace: ln.pace, Tone: ln.tone, Emotion: ln.emotion, Placement: ln.placement, Offscreen: ln.offscreen, Delivery: ln.voice}
			speaker := ln.speaker
			if id != "" {
				if prev, ok := lastShot[id]; ok {
					if prev == i-1 || prev == i {
						stats.LineMerged++
						continue
					}
					stats.LineDuplicate++
				}
				lastShot[id] = i
				out.LineID = id
				speaker = beats[id].Speaker
			} else {
				stats.LineUnmatched++
				out.Text, out.Speaker = ln.text, ln.speaker
			}
			stats.Lines++
			kept = append(kept, out)
			text := ln.text
			if id != "" {
				text = beats[id].Text
			}
			dialogue = append(dialogue, speaker+"："+text)
		}
		sh.spec.Lines = kept
		sh.dialogue = strings.Join(dialogue, "\n")
	}
	for _, id := range order {
		if _, ok := lastShot[id]; !ok {
			stats.LineMissing++
		}
	}
	rows := make([]map[string]any, 0, len(shots))
	for i, sh := range shots {
		spec := sh.spec
		spec.ID = fmt.Sprintf("%s-%02d", ctx.Section.Scene, ctx.ShotOffset+i+1)
		spec.Scene, spec.Section, spec.Genre = ctx.Section.Scene, ctx.Section.ID, ctx.Section.Genre
		if ctx.Indoor && spec.light().SourceAnchor == "" {
			stats.NoLight++
		}
		encoded, _ := json.Marshal(spec)
		var specMap map[string]any
		_ = json.Unmarshal(encoded, &specMap)
		characters := []any{}
		for _, b := range spec.Blocking {
			if b.Who != "" && !containsAnyString(characters, b.Who) {
				characters = append(characters, b.Who)
			}
		}
		assets := []any{}
		for _, n := range sh.names {
			if !containsAnyString(assets, n) {
				assets = append(assets, n)
			}
		}
		rows = append(rows, map[string]any{
			"shotNumber": ctx.ShotOffset + i + 1, "durationSeconds": float64(spec.Est), "plotDescription": firstNonBlank(sh.plot, spec.Action, spec.Purpose),
			"dialogue": sh.dialogue, "shotSize": spec.Size, "characters": characters, "assets": assets, "spec": specMap,
		})
	}
	stats.Shots = len(rows)
	return rows
}

func containsAnyString(items []any, value string) bool {
	for _, it := range items {
		if s, ok := it.(string); ok && s == value {
			return true
		}
	}
	return false
}

// matchSectionLine 把一句台词对到本节台词表：去标点后同文优先（同文的几句按说话人挑），其次互相包含、说话人相同的。
func matchSectionLine(ln sectionLine, order []string, beats map[string]SectionBeat) string {
	key := normLine(ln.text)
	var exact []string
	for _, id := range order {
		if normLine(beats[id].Text) == key {
			exact = append(exact, id)
		}
	}
	pick := func(ids []string) string {
		for _, id := range ids {
			if speakerMatches(beats[id].Speaker, ln.speaker) {
				return id
			}
		}
		if len(ids) > 0 {
			return ids[0]
		}
		return ""
	}
	if id := pick(exact); id != "" {
		return id
	}
	var near []string
	for _, id := range order {
		k := normLine(beats[id].Text)
		if k != "" && (strings.Contains(k, key) || strings.Contains(key, k)) && speakerMatches(beats[id].Speaker, ln.speaker) {
			near = append(near, id)
		}
	}
	return pick(near)
}

func speakerMatches(scriptSpeaker, written string) bool {
	if written == "" || scriptSpeaker == "" {
		return true
	}
	base, _, _ := strings.Cut(written, "·")
	return scriptSpeaker == written || scriptSpeaker == base || strings.Contains(written, scriptSpeaker) || strings.Contains(scriptSpeaker, base)
}

// offscreenVoice 是声音身份标签说的是画外（旁白、内心独白、广播、电话、画外持续……）。
func offscreenVoice(tag string) bool { return containsAny(tag, sectionOffscreenTags...) }

// sectionEstimates 按时间码算每镜估时：第一镜从 0 算起（文戏 15 的首帧锁 0.5 秒并进第一镜），最后一镜到 total 为止（结束站位并进最后一镜）。
func sectionEstimates(starts, ends []float64, total float64) []float64 {
	n := len(starts)
	out := make([]float64, n)
	for i := range starts {
		a, b := starts[i], ends[i]
		if i == 0 {
			a = 0
		}
		if i == n-1 && total > b {
			b = total
		}
		out[i] = pyRound(maxFloat(0.5, b-a), 1)
	}
	return out
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func parseSectionNumber(s string) float64 {
	m := sectionNumber.FindString(s)
	v, _ := strconv.ParseFloat(strings.Trim(m, "."), 64)
	return v
}

// SectionOutput 是按小节生成时一节的原样输出（drama_shotlists.sections_json 的一项）：分镜制作按戏类版式显示技能原本的卡片用，
// 「重新生成问题小节」时不重写的节也拿它放进对话历史。
type SectionOutput struct {
	ID      string   `json:"id"`
	Scene   string   `json:"scene"`
	Heading string   `json:"heading"`
	No      int      `json:"no"`
	Genre   string   `json:"genre"`
	Lines   [2]int   `json:"lines"`
	LineIDs []string `json:"lineIds"`
	Est     float64  `json:"est"`
	// Raw 是模型这一节的原样输出（含 BGM 等不进镜头表的内容）；Fields 是读出的小节级原样字段；Stats 是转换统计。
	Raw    string              `json:"raw"`
	Fields []SectionField      `json:"fields,omitempty"`
	Stats  SectionConvertStats `json:"stats"`
	// Shots 是这一节转出的镜头（镜头表 id，写库时按转换后的镜头表填）；TaskID 是写出这一节的任务。
	Shots  []string `json:"shots"`
	TaskID string   `json:"taskId,omitempty"`
	// Warnings 是切节时没满足戏类口径的提醒（如文戏 15 连着接话超过两句台词）：只提醒，不算检查 error。
	Warnings []string `json:"warnings,omitempty"`
}

// NewSectionOutput 是一节的原样输出记录。
func NewSectionOutput(sec Section, raw string, conv SectionShots) SectionOutput {
	return SectionOutput{ID: sec.ID, Scene: sec.Scene, Heading: sec.Heading, No: sec.No, Genre: sec.Genre, Lines: sec.Lines, LineIDs: sec.LineIDs, Est: sec.Est,
		Raw: raw, Fields: conv.Fields, Stats: conv.Stats, Shots: []string{}, Warnings: sec.Warnings}
}
