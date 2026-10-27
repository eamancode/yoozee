package drama

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// 场次切分（M8）：全系统只有 SplitScenes 一个切场入口——台词表（ScriptFromText）、生成分段的逐场续写（ChapterShotsScenes）、
// 戏类判定（ChapterShotsGenreScenes）、导演风格的场次表（StyleBibleScenes）和场次表接口都从这里取场。
//
// 规则：
//   - 场次头：「## 」开头的行，或以「第N场」「场 N」「INT.」「EXT.」「内·」「外·」「【场景N】」开头的行（【场景1】地点｜内｜夜
//     是剧本优化技能的原生写法，剧本优化写回剧集前已规整成「## 内·地点·夜」，导入的剧本可能还是原样）。
//   - 同一集里相邻的两个场次头，地点相同（去空白、全角转半角、英文不分大小写后相同）且内外相同就并成一场（「外·医院门口」
//     接「内·医院门口」不合并）；时间不要求相同，时间变化记在 Times 里、写进合并后的场次头（「内·电梯口·夜→晨」）。不相邻的同地点（走廊→电梯口→走廊）不合并。
//     读不出地点的场次头（只有「第3场」）不和任何场合并。
//   - 第一个场次头之前有正文时，这段正文单独算一场「未命名场」（与原来的台词表一致，不并进第一场）。
//   - 整章没有场次头时整章算一场，场次头用标题。
//
// 场编号：单元内是 E01S01、E01S02…（与镜头 spec.scene、台词 id 同一套）；导演风格、色卡用的项目级场编号把 E 后换成集号，场序相同。
// 规则改变前（SceneRuleLegacy，一个场次头一场）写下的数据，靠 Scene.LegacyNos 对到新场次。

// SceneRuleMerged 是当前的切场规则（连续同地点合并）；写进色彩脚本条目、色卡 metadata 和台词表，0 表示规则改变前写的数据。
const SceneRuleMerged = 2

// UntitledSceneHeading 是第一个场次头之前那段正文的场次头。
const UntitledSceneHeading = "未命名场"

// SceneHeading 是正文里的一个场次头。
type SceneHeading struct {
	// Text 是场次头原文（去掉「## 」）。
	Text     string `json:"text"`
	InOut    string `json:"inOut,omitempty"`
	Location string `json:"location,omitempty"`
	Time     string `json:"time,omitempty"`
	// Line 是它在正文里是第几行（只数非空行，从 1 数）。
	Line int `json:"line"`
	// bodyAt 是这个场次头之后的正文从所在场 Body 的第几行开始（并场时拆回旧规则的场用）。
	bodyAt int
}

// Scene 是切出来的一场。
type Scene struct {
	No int
	// ID 是单元内场编号 E01S02。
	ID string
	// Heading 是展示用的场次头：并进来的场次头都一样时就是原文，不一样时按「内外·地点·时间1→时间2」重写；
	// 没有场次头时是「未命名场」或标题。
	Heading  string
	InOut    string
	Location string
	// Times 是这一场依次出现过的时间（相邻重复的只记一次）。
	Times []string
	// Sources 是并进这一场的原始场次头，按正文顺序；未命名场、整章一场时为空。
	Sources []SceneHeading
	// LegacyNos 是这一场在规则改变前（一个场次头一场）的场序。
	LegacyNos []int
	// Lines 是这一场在正文里的行范围 [首行, 末行]（只数非空行，从 1 数，含场次头）。
	Lines [2]int
	// Body 是这一场的正文行（去掉首尾空白、不含场次头）。
	Body []string
}

var (
	sceneHeadingPrefix = regexp.MustCompile(`^(第?[一二三四五六七八九十\p{Nd}]+场|场[\s\p{Z}]*\p{Nd}+|INT\.|EXT\.|内[·.]|外[·.])`)
	sceneNumberPrefix  = regexp.MustCompile(`^(第?[一二三四五六七八九十\p{Nd}]+场|场[\s\p{Z}]*\p{Nd}+)[\s\p{Z}：:、.。,，-]*`)
	sceneSkillHeading  = regexp.MustCompile(`^【场景[\s\p{Z}]*[一二三四五六七八九十\p{Nd}]*】[\s\p{Z}]*(.*)$`)
	sceneEnglishPrefix = regexp.MustCompile(`(?i)^(INT\./EXT\.|INT\.|EXT\.|I/E\.?)[\s\p{Z}]*`)
	sceneFieldSep      = regexp.MustCompile(`[·・.｜|，,、\s\p{Z}]+`)
	sceneTimeParen     = regexp.MustCompile(`^(.+?)[（(]([^（）()]+)[）)]$`)
	sceneInOutWords    = map[string]string{"内": "内", "外": "外", "内外": "内外", "内/外": "内外", "外/内": "内外", "INT": "内", "EXT": "外", "INT/EXT": "内外", "I/E": "内外"}
	sceneTimeWords     = map[string]bool{
		"日": true, "夜": true, "晨": true, "昏": true, "晚": true, "午": true, "早": true,
		"白天": true, "黑夜": true, "夜晚": true, "晚上": true, "深夜": true, "午夜": true, "凌晨": true, "清晨": true, "早晨": true, "早上": true,
		"上午": true, "中午": true, "下午": true, "傍晚": true, "黄昏": true, "黎明": true, "拂晓": true, "日出": true, "日落": true,
		"同时": true, "连续": true, "稍后": true, "片刻后": true, "次日": true,
		"DAY": true, "NIGHT": true, "MORNING": true, "EVENING": true, "DUSK": true, "DAWN": true, "CONTINUOUS": true, "LATER": true,
	}
)

// isSceneHeadingLine 是切场认作场次头的行（line 已去首尾空白）。
func isSceneHeadingLine(line string) bool {
	return strings.HasPrefix(line, "## ") || sceneHeadingPrefix.MatchString(line) || sceneSkillHeading.MatchString(line)
}

// ParseSceneHeading 读一个场次头（可以带「## 」）的内外、地点和时间；读不出的字段为空。
func ParseSceneHeading(line string) SceneHeading {
	text := jsTrim(strings.TrimLeft(jsTrim(line), "# "))
	h := SceneHeading{Text: text}
	body := text
	if m := sceneSkillHeading.FindStringSubmatch(body); m != nil {
		body = m[1]
	} else {
		body = sceneNumberPrefix.ReplaceAllString(body, "")
	}
	if m := sceneEnglishPrefix.FindStringSubmatch(body); m != nil {
		h.InOut = sceneInOutWords[strings.ToUpper(strings.TrimSuffix(m[1], "."))]
		body = body[len(m[0]):]
		// 英文场次头「HOSPITAL CORRIDOR - NIGHT」：地点里有空格，按「 - 」分地点和时间。
		if i := strings.LastIndex(body, " - "); i >= 0 {
			h.Location, h.Time = jsTrim(body[:i]), jsTrim(body[i+3:])
		} else {
			h.Location = jsTrim(body)
		}
		return h
	}
	var location []string
	for _, token := range sceneFieldSep.Split(body, -1) {
		token = jsTrim(token)
		if token == "" {
			continue
		}
		if v, ok := sceneInOutWords[strings.ToUpper(token)]; ok && h.InOut == "" {
			h.InOut = v
			continue
		}
		if sceneTimeWord(token) && h.Time == "" {
			h.Time = token
			continue
		}
		location = append(location, token)
	}
	h.Location = strings.Join(location, "·")
	// 「走廊（夜）」：括号里是时间时拆出来。
	if m := sceneTimeParen.FindStringSubmatch(h.Location); m != nil && h.Time == "" && sceneTimeWords[strings.ToUpper(jsTrim(m[2]))] {
		h.Location, h.Time = jsTrim(m[1]), jsTrim(m[2])
	}
	return h
}

// sceneTimeWord 是时间词，或时间词后面跟括号补充（「夜（雨）」）。
func sceneTimeWord(token string) bool {
	if sceneTimeWords[strings.ToUpper(token)] {
		return true
	}
	if m := sceneTimeParen.FindStringSubmatch(token); m != nil {
		return sceneTimeWords[strings.ToUpper(jsTrim(m[1]))]
	}
	return false
}

// SceneLocationKey 是判断「同一地点」用的地点：全角转半角、去掉空白和分隔点、英文转大写。
func SceneLocationKey(location string) string {
	var b strings.Builder
	for _, r := range location {
		switch {
		case r == '　':
			continue
		case r >= '！' && r <= '～':
			r -= 0xFEE0
		}
		if unicode.IsSpace(r) || r == '·' || r == '・' || r == '.' {
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// SplitScenes 按场次头切一集（或一章）的纯文本正文，相邻同地点的场次头并成一场（规则见文件头）；正文为空返回 nil。
func SplitScenes(plainText string, title string) []Scene {
	var scenes []Scene
	lineNo := 0
	legacy := 0
	for _, raw := range lineBreakPattern.Split(plainText, -1) {
		ln := strings.TrimSpace(raw)
		if ln == "" {
			continue
		}
		lineNo++
		if isSceneHeadingLine(ln) {
			legacy++
			h := ParseSceneHeading(ln)
			h.Line = lineNo
			if n := len(scenes); n > 0 && len(scenes[n-1].Sources) > 0 && sameSceneLocation(scenes[n-1].Sources[0], h) {
				cur := &scenes[n-1]
				h.bodyAt = len(cur.Body)
				cur.Sources = append(cur.Sources, h)
				cur.LegacyNos = append(cur.LegacyNos, legacy)
				cur.Lines[1] = lineNo
				continue
			}
			scenes = append(scenes, Scene{Location: h.Location, Sources: []SceneHeading{h}, LegacyNos: []int{legacy}, Lines: [2]int{lineNo, lineNo}, Body: []string{}})
			continue
		}
		if len(scenes) == 0 {
			legacy++
			scenes = append(scenes, Scene{LegacyNos: []int{legacy}, Lines: [2]int{lineNo, lineNo}, Body: []string{}})
		}
		cur := &scenes[len(scenes)-1]
		cur.Body = append(cur.Body, ln)
		cur.Lines[1] = lineNo
	}
	headed := slices.ContainsFunc(scenes, func(sc Scene) bool { return len(sc.Sources) > 0 })
	for i := range scenes {
		sc := &scenes[i]
		sc.No, sc.ID = i+1, SceneCode(1, i+1)
		switch {
		case len(sc.Sources) > 0:
			sc.Heading, sc.InOut, sc.Times = mergedSceneHeading(sc.Sources, sc.Location)
		case headed:
			sc.Heading = UntitledSceneHeading
		default:
			sc.Heading = firstNonBlank(jsTrim(title), UntitledSceneHeading)
		}
	}
	return scenes
}

// sameSceneLocation 是两个场次头可以并成一场：地点相同（读不出地点的不算相同）且内外相同（都没写内外也算相同）；时间不比。
func sameSceneLocation(a, b SceneHeading) bool {
	ka := SceneLocationKey(a.Location)
	return ka != "" && ka == SceneLocationKey(b.Location) && a.InOut == b.InOut
}

// mergedSceneHeading 是并在一起的几个场次头的展示名、内外和时间变化：都一样时用第一个场次头原文。
func mergedSceneHeading(sources []SceneHeading, location string) (string, string, []string) {
	var inOuts, times []string
	same := true
	for i, h := range sources {
		if h.InOut != "" && !slices.Contains(inOuts, h.InOut) {
			inOuts = append(inOuts, h.InOut)
		}
		if h.Time != "" && (len(times) == 0 || times[len(times)-1] != h.Time) {
			times = append(times, h.Time)
		}
		if i > 0 && jsCompact(h.Text) != jsCompact(sources[0].Text) {
			same = false
		}
	}
	inOut := strings.Join(inOuts, "/")
	if same {
		return sources[0].Text, inOut, times
	}
	return joinTruthy([]string{inOut, location, strings.Join(times, "→")}, "·"), inOut, times
}

// SceneOfLegacy 是规则改变前（一个场次头一场）的第 legacyNo 场现在在哪一场；对不上返回 nil。
func SceneOfLegacy(scenes []Scene, legacyNo int) *Scene {
	for i := range scenes {
		if slices.Contains(scenes[i].LegacyNos, legacyNo) {
			return &scenes[i]
		}
	}
	return nil
}

// ScenesRenumbered 是这份正文按新规则切出的场次和旧规则不一样（有并起来的场，旧场编号会错位）。
func ScenesRenumbered(scenes []Scene) bool {
	return slices.ContainsFunc(scenes, func(sc Scene) bool { return len(sc.LegacyNos) > 1 })
}

// LegacySceneCurrent 是规则改变前写的第 legacyNo 场的数据（场编号 S 后是 legacyNo），在新场次表里编号没有错位：
// 它现在所在的场就是第 legacyNo 场。并场之前的场编号不变，照常可用；并场之后的旧编号错位，要重新生成或按 RenumberColorScript 重排。
func LegacySceneCurrent(scenes []Scene, legacyNo int) bool {
	sc := SceneOfLegacy(scenes, legacyNo)
	return sc != nil && sc.No == legacyNo
}

// sceneBeats 按台词表的规则读正文行：「人物：台词」是台词（括号里有画外 / OS 的是画外音），其余是动作（去掉外层括号）。
func sceneBeats(lines []string) ([]ScriptBeat, []string) {
	beats := make([]ScriptBeat, 0, len(lines))
	characters := []string{}
	for _, ln := range lines {
		m := scriptLinePattern.FindStringSubmatch(ln)
		if m != nil && !strings.HasPrefix(ln, "（") && !strings.HasPrefix(ln, "(") {
			who, said := strings.TrimSpace(m[1]), strings.TrimSpace(m[3])
			kind := "line"
			if strings.Contains(m[2], "画外") || strings.Contains(m[2], "OS") {
				kind = "vo"
			}
			beats = append(beats, ScriptBeat{Kind: kind, Speaker: who, Text: said, Tag: "原文事实"})
			if !containsString(characters, who) {
				characters = append(characters, who)
			}
			continue
		}
		beats = append(beats, ScriptBeat{Kind: "action", Text: strings.Trim(ln, "（）()"), Tag: "原文事实"})
	}
	return beats, characters
}

// sceneExcerpt 是一场正文的摘录：台词写成「人物：台词」，与动作一起压成一行，截到 limit 个字。
func sceneExcerpt(sc Scene, limit int) string {
	beats, _ := sceneBeats(sc.Body)
	parts := make([]string, 0, len(beats))
	for _, b := range beats {
		text := b.Text
		if b.Speaker != "" {
			text = b.Speaker + "：" + text
		}
		parts = append(parts, text)
	}
	return jsTruncate(jsCompact(strings.Join(parts, " ")), limit)
}

// SceneSourceLabel 是并场的说明：「正文里连续 2 个同地点场次头（内·电梯口·夜、内·电梯口·夜）并成一场」；没有并场返回空串。
func SceneSourceLabel(sc Scene) string {
	if len(sc.Sources) < 2 {
		return ""
	}
	texts := make([]string, 0, len(sc.Sources))
	for _, h := range sc.Sources {
		texts = append(texts, h.Text)
	}
	return fmt.Sprintf("正文里连续 %d 个同地点场次头（%s）并成一场", len(sc.Sources), strings.Join(texts, "、"))
}

// SceneRenumberEpisode 是一集（导演风格的集号）按现在的规则切出的场次：RenumberColorScript 用。
type SceneRenumberEpisode struct {
	UnitID string
	Scenes []Scene
}

// SceneMove 是规则改变前写的一场色彩脚本按新场次重排的结果：Moved 是换了编号（或编号不变、只补上规则标记），
// Dropped 是并进了已有色彩脚本的场、去掉了，其余（Note 非空）是对不上、原样保留。
type SceneMove struct {
	From    string `json:"from"`
	To      string `json:"to,omitempty"`
	Moved   bool   `json:"moved,omitempty"`
	Dropped bool   `json:"dropped,omitempty"`
	Note    string `json:"note,omitempty"`
}

// RenumberColorScript 把规则改变前（一个场次头一场）写的色彩脚本条目按新场次重排：旧第 k 场找到它现在所在的场（SceneOfLegacy），
// 地点和内外也对得上（条目记的场次头读出的地点、内外与现在这一场相同；条目没记场次头时不比）才换成新编号、补上规则标记；
// 新编号已经有色彩脚本（新规则写的，或排在前面的旧场先占了）时这一场去掉，以已有的为准；找不到、地点或内外对不上、
// 记的单元不是这一集的保留原样并记 note。新规则写的条目不动；inherits_from 跟着换编号，换完指向自己时清空。
// 返回重排后的条目（按集号、场序排好）和每个旧条目的去向。
func RenumberColorScript(entries []ColorScriptEntry, episodes map[int]SceneRenumberEpisode) ([]ColorScriptEntry, []SceneMove) {
	claimed := map[string]bool{}
	for _, e := range entries {
		if e.SceneRule != 0 {
			claimed[e.Code()] = true
		}
	}
	order := make([]int, 0, len(entries))
	for i := range entries {
		order = append(order, i)
	}
	slices.SortStableFunc(order, func(a, b int) int {
		if entries[a].Episode != entries[b].Episode {
			return entries[a].Episode - entries[b].Episode
		}
		return entries[a].SceneNo - entries[b].SceneNo
	})
	renamed := map[string]string{}
	dropped := map[int]bool{}
	out := slices.Clone(entries)
	var moves []SceneMove
	for _, i := range order {
		e := &out[i]
		if e.SceneRule != 0 {
			continue
		}
		move := SceneMove{From: e.Code()}
		ep, ok := episodes[e.Episode]
		var target *Scene
		if ok {
			target = SceneOfLegacy(ep.Scenes, e.SceneNo)
		}
		switch {
		case !ok || target == nil:
			move.Note = "现在的场次表里没有这一场，原样保留"
		case e.UnitID != "" && e.UnitID != ep.UnitID:
			move.Note = "写它时所在的单元已经不是这一集，原样保留"
		case e.Scene != "" && !sameSceneLocation(ParseSceneHeading(e.Scene), SceneHeading{Location: target.Location, InOut: target.InOut}):
			move.Note = fmt.Sprintf("地点或内外对不上：色彩脚本写的是「%s」，现在的 %s 是「%s」，原样保留", e.Scene, SceneCode(e.Episode, target.No), target.Heading)
		default:
			move.To = SceneCode(e.Episode, target.No)
			renamed[move.From] = move.To
			if claimed[move.To] {
				move.Dropped = true
				move.Note = fmt.Sprintf("并进了 %s，%s 已有色彩脚本，以它为准", move.To, move.To)
				dropped[i] = true
				break
			}
			claimed[move.To] = true
			move.Moved = true
			e.SceneNo, e.SceneRule, e.Scene = target.No, SceneRuleMerged, target.Heading
			if e.UnitID == "" {
				e.UnitID = ep.UnitID
			}
		}
		moves = append(moves, move)
	}
	kept := make([]ColorScriptEntry, 0, len(out))
	for i, e := range out {
		if dropped[i] {
			continue
		}
		if to, ok := renamed[e.InheritsFrom]; ok {
			e.InheritsFrom = to
		}
		if e.InheritsFrom == e.Code() {
			e.InheritsFrom = ""
		}
		kept = append(kept, e)
	}
	slices.SortStableFunc(kept, func(a, b ColorScriptEntry) int {
		if a.Episode != b.Episode {
			return a.Episode - b.Episode
		}
		return a.SceneNo - b.SceneNo
	})
	return kept, moves
}

// LegacyScriptFromText 是规则改变前（一个场次头一场）的台词表：场次仍取自 SplitScenes，并起来的场按原来的场次头拆回去，
// 场编号、台词 id 按旧规则编（与 script.py from-md 一致）。只给旧规则写下的镜头表重装段、重评测用（镜头行里的 spec.scene 是旧编号），
// 新生成的数据一律用 ScriptFromText。
func LegacyScriptFromText(text string, title string) Script {
	script := Script{Title: title, Scenes: []ScriptScene{}}
	add := func(no int, heading string, body []string) {
		beats, characters := sceneBeats(body)
		script.Scenes = append(script.Scenes, ScriptScene{ID: SceneCode(1, no), Heading: heading, Characters: characters, Beats: beats})
	}
	for _, sc := range SplitScenes(text, title) {
		if len(sc.Sources) == 0 {
			add(sc.LegacyNos[0], UntitledSceneHeading, sc.Body)
			continue
		}
		for j, h := range sc.Sources {
			end := len(sc.Body)
			if j+1 < len(sc.Sources) {
				end = sc.Sources[j+1].bodyAt
			}
			add(sc.LegacyNos[j], h.Text, sc.Body[h.bodyAt:end])
		}
	}
	AssignBeatIDs(&script)
	return script
}
