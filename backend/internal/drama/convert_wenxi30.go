package drama

import (
	"regexp"
	"strings"
)

// 文戏 30 转换器：即梦的导演分镜字段（卡 0 文档 §4c / §4d）→ 分镜行。
//
//	小节N：标题
//	生成时长：0-XX秒 / 剧情功能 / 场景 / 人物 / 持续光源 / 持续声音 …（小节头）
//	镜头N（a-b秒）
//	镜头目的：… 镜头人物站位：… 画面：… 构图：… 镜头：… 运镜：… 动作：… 表情：… 台词：… 画外音：… 声音：… 灯光与光影：… 衔接：… 起始画面：… 结束画面：…
//	小节约束 / 小节人物场景站位标注 / 三个连续性总结 / 即梦粘贴提示词（小节尾）
//
// 字段与 spec 几乎一一对应（DESIGN-2.1 的 spec 就是照这份写的）；小节头、小节尾和粘贴提示词只进原样输出。
// 一个字段的值可以跨行，直到下一个认识的字段名；进了小节尾之后不再读镜头字段（粘贴提示词里也有「声音：」）。

var (
	wenxi30Block    = regexp.MustCompile(`^[#*\s]*小节\s*(\d+)\s*[：:]\s*(.*?)\**$`)
	wenxi30ShotHead = regexp.MustCompile(`^[#*\s-]*镜头\s*(\d+)\s*[（(]\s*([\d.]+)\s*[-–—~～至]\s*([\d.]+)\s*秒\s*[）)]`)
	wenxi30Label    = regexp.MustCompile(`^[-*•\s]*\**([\p{Han}]{2,12})\**\s*[：:]\s*(.*)$`)
	wenxi30Speech   = regexp.MustCompile(`([^：:；;\s【】，。“”"]{1,16})[：:]\s*[“"]?([^【】“”"]+?)[”"]?\s*【([^】]*)】`)
	wenxi30Quoted   = regexp.MustCompile(`([^：:；;\s【】，。“”"]{1,16})[：:]\s*[“"]([^”"]+)[”"]`)
	wenxi30Four     = regexp.MustCompile(`(音色|语速|语调|情感)[（(]([^）)]*)[）)]`)
	wenxi30Span     = regexp.MustCompile(`([\d.]+)\s*[-–—~～至]\s*([\d.]+)\s*秒`)
	wenxi30Sentence = regexp.MustCompile(`[；;。]`)
	wenxi30Sizes    = regexp.MustCompile(`起始景别\s*[=＝:：]\s*([^\s，,；;\->→]+)\s*(?:->|→|—>|-)\s*结束景别\s*[=＝:：]\s*([^\s，,；;。]+)`)
)

var (
	wenxi30ShotFields = map[string]bool{
		"镜头目的": true, "镜头人物站位": true, "镜头场景锚点": true, "镜头机位坐标": true, "镜头运动方向": true, "镜头动作连续": true,
		"画面": true, "构图": true, "镜头": true, "运镜": true, "动作": true, "表情": true, "台词": true, "画外音": true, "声音": true,
		"灯光与光影": true, "衔接": true, "起始画面": true, "结束画面": true,
	}
	wenxi30HeadFields = map[string]bool{
		"生成时长": true, "叙事时间跨度": true, "剧情功能": true, "本小节高级视听策略": true, "场景": true, "人物": true, "人物站位": true,
		"空间坐标锁定": true, "运动方向锁定": true, "场景锚点连续锁定": true, "动作连续锁定": true, "持续氛围": true, "持续光源": true,
		"持续声音": true, "群像差异化锁定": true,
	}
	wenxi30TailFields = map[string]bool{
		"小节约束": true, "小节人物场景站位标注": true, "小节动作连续性总结": true, "小节场景连续性总结": true, "小节声音连续性总结": true,
		"即梦粘贴提示词": true, "台词核对摘要": true, "自检摘要": true,
	}
	wenxi30CutPoints = []struct{ word, point string }{
		{"视线", "视线"}, {"目光", "视线"}, {"声音", "声音"}, {"声", "声音"}, {"光", "光线"}, {"锚点", "场景锚点"}, {"背景", "场景锚点"},
		{"道具", "道具"}, {"动作", "动作"}, {"姿态", "动作"}, {"手", "动作"},
	}
)

type wenxi30Shot struct {
	start, end float64
	fields     map[string]string
}

type wenxi30Section struct {
	head  []SectionField
	tail  []SectionField
	shots []wenxi30Shot
}

func (s wenxi30Section) headField(name string) string {
	for _, f := range s.head {
		if f.Name == name {
			return strings.TrimSpace(f.Value)
		}
	}
	return ""
}

func parseWenxi30(raw string) ([]wenxi30Section, int) {
	var out []wenxi30Section
	rejected := 0
	var cur *wenxi30Section
	var shot *wenxi30Shot
	var field *SectionField
	var shotField string
	tail := false
	appendTo := func(text string) {
		switch {
		case shot != nil && shotField != "":
			shot.fields[shotField] = strings.TrimSpace(shot.fields[shotField] + "\n" + text)
		case field != nil:
			field.Value = strings.TrimSpace(field.Value + "\n" + text)
		}
	}
	for _, line := range lineBreakPattern.Split(raw, -1) {
		line = strings.TrimSpace(line)
		if line == "" || line == "---" {
			continue
		}
		if m := wenxi30Block.FindStringSubmatch(line); m != nil {
			out = append(out, wenxi30Section{head: []SectionField{{Name: "小节", Value: strings.TrimSpace(m[2])}}})
			cur, shot, field, shotField, tail = &out[len(out)-1], nil, nil, "", false
			continue
		}
		if cur == nil {
			continue
		}
		if !tail {
			if m := wenxi30ShotHead.FindStringSubmatch(line); m != nil {
				cur.shots = append(cur.shots, wenxi30Shot{start: parseSectionNumber(m[2]), end: parseSectionNumber(m[3]), fields: map[string]string{}})
				shot, field, shotField = &cur.shots[len(cur.shots)-1], nil, ""
				continue
			}
			if strings.HasPrefix(strings.TrimLeft(line, "#*- "), "镜头") && !wenxi30Label.MatchString(line) {
				rejected++
				continue
			}
		}
		if m := wenxi30Label.FindStringSubmatch(line); m != nil {
			name, value := m[1], strings.TrimSpace(m[2])
			switch {
			case wenxi30TailFields[name]:
				tail, shot, shotField = true, nil, ""
				cur.tail = append(cur.tail, SectionField{Name: name, Value: value})
				field = &cur.tail[len(cur.tail)-1]
				continue
			case !tail && shot != nil && wenxi30ShotFields[name]:
				shot.fields[name] = value
				shotField = name
				continue
			case !tail && shot == nil && wenxi30HeadFields[name]:
				cur.head = append(cur.head, SectionField{Name: name, Value: value})
				field = &cur.head[len(cur.head)-1]
				continue
			}
		}
		appendTo(line)
	}
	return out, rejected
}

func convertWenxi30(raw string, ctx SectionContext) SectionShots {
	out := SectionShots{}
	sections, rejected := parseWenxi30(raw)
	out.Stats.Rejected = rejected
	allNames := append(append([]string{}, ctx.Places...), ctx.Props...)
	var shots []sectionShot
	for _, sec := range sections {
		out.Stats.Blocks++
		out.Fields = append(append(out.Fields, sec.head...), sec.tail...)
		var characters []string
		for _, n := range sectionNameSep.Split(sec.headField("人物"), -1) {
			if n = resolveSectionName(strings.Trim(n, "。"), ctx.Characters); n != "" && !containsString(characters, n) {
				characters = append(characters, n)
			}
		}
		total := 0.0
		if m := wenxi30Span.FindStringSubmatch(sec.headField("生成时长")); m != nil {
			total = parseSectionNumber(m[2])
		}
		var starts, ends []float64
		first := len(shots)
		for _, s := range sec.shots {
			sh, n := wenxi30ShotOf(s.fields, characters, sec.headField("持续光源"), ctx)
			out.Stats.MusicStripped += n
			if sh.spec.Size == "" {
				out.Stats.NoSize++
			}
			body := strings.Join([]string{s.fields["画面"], s.fields["动作"], s.fields["镜头人物站位"]}, " ")
			sh.names = append(append([]string{}, characters...), mentionedNames(body+" "+sec.headField("场景"), allNames)...)
			starts, ends = append(starts, s.start), append(ends, s.end)
			shots = append(shots, sh)
		}
		if len(shots) == first {
			continue
		}
		for i, est := range sectionEstimates(starts, ends, total) {
			shots[first+i].spec.Est = flexFloat(est)
		}
	}
	out.Rows = finishSectionShots(shots, ctx, &out.Stats)
	return out
}

// wenxi30ShotOf 读一镜的 19 个字段。
func wenxi30ShotOf(f map[string]string, characters []string, lightSource string, ctx SectionContext) (sectionShot, int) {
	var sh sectionShot
	spec := &sh.spec
	stripped := 0
	clean := func(text string) string {
		text, n := stripMusic(strings.TrimSpace(text))
		stripped += n
		return text
	}
	spec.Purpose = strings.TrimSpace(f["镜头目的"])
	spec.Composition = strings.TrimSpace(f["构图"])
	spec.CameraSide = strings.TrimSpace(f["镜头机位坐标"])
	spec.Action = clean(f["动作"])
	spec.Sfx = clean(f["声音"])
	if vo := strings.TrimSpace(f["画外音"]); vo != "" && strings.Trim(vo, "。") != "无" {
		if m := wenxi30Quoted.FindStringSubmatch(vo); m != nil {
			sh.lines = append(sh.lines, sectionLine{speaker: resolveSectionName(m[1], ctx.Characters), text: m[2], offscreen: true, placement: "画外", voice: "画外音"})
		} else {
			spec.VO = clean(vo)
		}
	}
	spec.StartState = strings.TrimSpace(f["起始画面"])
	spec.EndState = strings.TrimSpace(f["结束画面"])
	if cont := strings.TrimSpace(f["镜头动作连续"]); cont != "" {
		sentences := sectionClauseSep.Split(strings.Trim(cont, "。"), -1)
		if spec.StartState == "" && len(sentences) > 0 {
			spec.StartState = strings.TrimSpace(sentences[0])
		}
		if spec.EndState == "" && len(sentences) > 0 {
			spec.EndState = strings.TrimSpace(sentences[len(sentences)-1])
		}
	}
	extras := map[string]string{}
	for key, name := range map[string]string{"镜头场景锚点": "场景锚点", "镜头运动方向": "运动方向", "镜头动作连续": "动作连续"} {
		if v := strings.TrimSpace(f[key]); v != "" {
			extras[name] = v
		}
	}
	if len(extras) > 0 {
		spec.Extras = extras
	}
	for _, b := range strings.Split(f["表情"], "；") {
		if b = strings.TrimSpace(strings.Trim(b, "。")); b != "" && b != "无" {
			if spec.Performance == nil {
				spec.Performance = &Performance{}
			}
			spec.Performance.Beats = append(spec.Performance.Beats, b)
		}
	}
	// 景别：画面里先写的那个，没写就取运镜里的起始景别；起止景别不同时补推 / 拉。
	move := f["运镜"]
	var from, to string
	if m := wenxi30Sizes.FindStringSubmatch(move); m != nil {
		from, _ = sectionSize(m[1])
		to, _ = sectionSize(m[2])
		move = strings.Replace(move, m[0], "", 1)
	}
	size, _ := sectionSize(f["画面"])
	if size == "" {
		size = from
	}
	spec.Size = size
	spec.Move = sectionMoves(move)
	if from != "" && to != "" && from != to && !containsString(spec.Move, "push") && !containsString(spec.Move, "pull") && len(spec.Move) < 2 {
		spec.Move = slicesWithout(spec.Move, "fixed")
		if sectionCloseSizes[to] > sectionCloseSizes[from] {
			spec.Move = append(spec.Move, "pull")
		} else {
			spec.Move = append(spec.Move, "push")
		}
	}
	spec.Cam = sectionCam(f["画面"]+" "+f["镜头"], size)
	spec.Subject = "person"
	if len(mentionedNames(f["画面"]+f["镜头人物站位"], characters)) == 0 && containsAny(f["画面"], "特写", "空镜", "道具") {
		spec.Subject = "non_person"
	}
	spec.Light = wenxi30Light(f["灯光与光影"], lightSource, ctx)
	if t := strings.TrimSpace(f["衔接"]); t != "" {
		spec.Transition = &Transition{Out: t, CutPoint: wenxi30CutPoint(t)}
	}
	// 站位：本镜人物站位里提到的人物各一条，锚点、朝向、背后取写到这个人的那一句。
	station := f["镜头人物站位"]
	who := mentionedNames(station+" "+f["画面"], characters)
	for _, name := range who {
		base := strings.SplitN(name, "·", 2)[0]
		clause := firstSentenceWith(station, base)
		b := Blocking{Who: name, At: sectionAnchorIn(clause, ctx.Anchors)}
		if i := strings.Index(clause, "面向"); i >= 0 {
			b.Facing = strings.TrimSpace(strings.Split(clause[i:], "，")[0])
		}
		if i := strings.Index(clause, "背后"); i >= 0 {
			b.Behind = strings.TrimSpace(strings.Split(clause[i:], "，")[0])
		}
		spec.Blocking = append(spec.Blocking, b)
	}
	lines, screens := wenxi30Lines(f["台词"], ctx)
	sh.lines = append(lines, sh.lines...)
	addScreenText(spec, screens)
	sh.plot = clean(f["画面"])
	if spec.Action == "" {
		spec.Action = sh.plot
	}
	return sh, stripped
}

// wenxi30Lines 读台词字段：「角色名：原文【音色（…）+语速（…）+语调（…）+情感（…）】；画内」，一个字段可以有几句；「无」是没有台词。
// 冒号前写的是画面文字（「手机屏幕：“…”」）的不算台词，内容放进 screens。
func wenxi30Lines(text string, ctx SectionContext) (out []sectionLine, screens []string) {
	text = strings.TrimSpace(text)
	if text == "" || strings.Trim(text, "。") == "无" {
		return nil, nil
	}
	matches := wenxi30Speech.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		for _, m := range wenxi30Quoted.FindAllStringSubmatch(text, -1) {
			matches = append(matches, []string{m[0], m[1], m[2], ""})
		}
	}
	for _, m := range matches {
		if screenTextSpeaker(m[1], ctx.Characters) {
			screens = append(screens, strings.Trim(strings.TrimSpace(m[2]), "“”\""))
			continue
		}
		ln := sectionLine{speaker: resolveSectionName(m[1], ctx.Characters), text: strings.Trim(strings.TrimSpace(m[2]), "“”\"")}
		for _, q := range wenxi30Four.FindAllStringSubmatch(m[3], -1) {
			v := strings.TrimSpace(q[2])
			switch q[1] {
			case "音色":
				if offscreenVoice(v) {
					ln.offscreen, ln.placement = true, "画外"
				}
			case "语速":
				ln.pace = v
			case "语调":
				ln.tone = v
			case "情感":
				ln.emotion = v
			}
		}
		if !ln.offscreen && strings.Contains(m[0], "画外") {
			ln.offscreen, ln.placement = true, "画外"
		}
		out = append(out, ln)
	}
	return out, screens
}

// wenxi30Light 读灯光与光影：主光 / 辅光 / 轮廓光 / 光比 / 色温 / 阴影 / 曝光 / 变化按分句切；光源锚点取主光分句（没有就用小节的持续光源）。
func wenxi30Light(text string, lightSource string, ctx SectionContext) *Light {
	text = strings.TrimSpace(text)
	if text == "" && lightSource == "" {
		if ctx.Indoor {
			if a := sectionLightAnchor("", ctx.Anchors); a != "" {
				return &Light{SourceAnchor: a}
			}
		}
		return nil
	}
	l := &Light{}
	after := func(clause string, words ...string) string {
		for _, w := range words {
			if i := strings.Index(clause, w); i >= 0 {
				return strings.TrimSpace(strings.TrimLeft(clause[i+len(w):], "为是：: "))
			}
		}
		return strings.TrimSpace(clause)
	}
	keyClause := ""
	for _, c := range sectionClauseSep.Split(text, -1) {
		if c = strings.TrimSpace(c); c == "" {
			continue
		}
		switch {
		case strings.Contains(c, "主光") && l.Key == "":
			l.Key, keyClause = after(c, "主光为", "主光是", "主光"), c
		case strings.Contains(c, "辅光") && l.Fill == "":
			l.Fill = after(c, "辅光为", "辅光是", "辅光")
		case strings.Contains(c, "轮廓光") && l.Rim == "":
			l.Rim = after(c, "轮廓光为", "轮廓光")
		case strings.Contains(c, "光比") && l.Ratio == "":
			l.Ratio = after(c, "光比为", "光比")
		case strings.Contains(c, "色温") && l.Kelvin == "":
			l.Kelvin = after(c, "色温为", "色温")
		case strings.Contains(c, "曝光") && l.Exposure == "":
			l.Exposure = c
		case strings.Contains(c, "变化") && l.Change == "":
			l.Change = c
		case strings.Contains(c, "阴影") && l.Shadow == "":
			l.Shadow = c
		}
	}
	l.SourceAnchor = sectionLightAnchor(firstNonBlank(l.Key, lightSource), ctx.Anchors)
	if a := sectionAnchorIn(keyClause, ctx.Anchors); a != "" {
		l.SourceAnchor = a
	}
	return l
}

func wenxi30CutPoint(text string) string {
	if i := strings.Index(text, "衔接点"); i >= 0 {
		text = text[i:]
	}
	for _, c := range wenxi30CutPoints {
		if strings.Contains(text, c.word) {
			return c.point
		}
	}
	return ""
}

// firstSentenceWith 是第一个含有 word 的句子（按「；。」切，比 firstClauseWith 长一级）。
func firstSentenceWith(text string, word string) string {
	for _, s := range wenxi30Sentence.Split(text, -1) {
		if strings.Contains(s, word) {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func slicesWithout(items flexStrings, drop string) flexStrings {
	out := flexStrings{}
	for _, it := range items {
		if it != drop {
			out = append(out, it)
		}
	}
	return out
}
