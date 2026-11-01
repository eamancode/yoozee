package drama

import (
	"regexp"
	"strings"
)

// 武戏转换器（M8 卡 E）：skill-complete-v7 的分镜骨架（卡 0 文档 §5c / §5d）→ 分镜行。认两种写法（§6.3：接入说明钉的是第一种，
// 模型照成品示例写成第二种也收）：
//
//	v7.0 4.29 每镜标准格式（多行块，字段名打头）：
//	  【核心观看体验】…
//	  镜 1｜0-1s
//	  景别 / 焦段 / 运镜：MS / 35mm / TRACK
//	  摄影机目标：… 站位：… 动作链：… 力度：… 受击：… 战损：… 闪帧：… 特效：… 环境反馈：… 表情：… 台词：… 光影：… 下一镜连接：…
//	v6.1 八栏单行（v6.0 成品示例）：镜 1｜t0-1s｜MCU 急推｜站位｜动作｜A 微表情｜B 微表情｜力度｜伤痕｜光影材质天气
//
// 统一镜头表没有的打斗标签（P/R/L/F 力度、受击、战损、闪帧、特效层、摄影机目标、站位原文、环境反馈、下一镜连接、核心观看体验）
// 原样进 spec.extras，编译武戏结构时用（pack.go 的 wuxiShotText）；镜头表结构不变。一镜 1 秒；没写台词列、零拟声，声音按默认。

var (
	wuxiShotHead       = regexp.MustCompile(`^[#*>\s-]*\**镜\s*(\d+)\s*\**\s*[｜|]\s*\**\s*[tT]?\s*([\d.]+)\s*[-–—~～至]\s*([\d.]+)\s*(?:s|S|秒)?\s*\**\s*(.*)$`)
	wuxiLabel          = regexp.MustCompile(`^[-*•\s]*\**([^：:*]{1,20}?)\**\s*[：:]\s*(.*)$`)
	wuxiExperienceLine = regexp.MustCompile(`^[-*•\s]*\**【?(?:本场动作戏的)?核心观看体验】?\**\s*[：:=＝]?\s*(.*)$`)
	wuxiSizeAbbr       = regexp.MustCompile(`\b(ECU|BCU|MCU|CU|MLS|MFS|MS|FS|LS|WS|EWS|ELS|XLS)\b`)
	wuxiKelvin         = regexp.MustCompile(`(\d{4,5})\s*K`)
	wuxiPower          = regexp.MustCompile(`P[1-4]`)
	wuxiReaction       = regexp.MustCompile(`R[1-4]`)
	wuxiDamage         = regexp.MustCompile(`L[1-5](?:\s*(?:→|->|—>)\s*L[1-5])?`)
	wuxiFlash          = regexp.MustCompile(`F[1-5]`)
	wuxiArrow          = regexp.MustCompile(`\s*(?:→|->|—>|⇒)\s*`)
	wuxiNameHead       = regexp.MustCompile(`^[\p{Han}A-Za-z·]{1,12}`)
	wuxiFacing         = regexp.MustCompile(`(面向[^·，,；;。\s]{0,8}|背向[^·，,；;。\s]{0,6}|侧向[^·，,；;。\s]{0,6}|正面[^·，,；;。\s]{0,4}|相对|相背)`)
	wuxiQuoted         = regexp.MustCompile(`([^：:；;\s【】，。“”"（）()]{1,16})\s*(?:[（(]([^）)]{0,12})[）)])?\s*[：:]\s*[“"「]([^”"」]+)[”"」]`)
)

var (
	wuxiSizeWords = map[string]string{"ECU": "大特写", "BCU": "大特写", "CU": "特写", "MCU": "中近景", "MS": "中景", "MLS": "中全景", "MFS": "中全景", "FS": "全景", "LS": "远景", "WS": "远景", "EWS": "大远景", "ELS": "大远景", "XLS": "大远景"}
	// wuxiMoveWords 是英文运镜缩写（v7 写大写英文）；中文运镜词走 sectionMoveWords。WHIP 是甩镜，单独一项，不再并成 pan。
	wuxiMoveWords = []struct{ word, move string }{
		{"WHIP", "whip"}, {"PUSH", "push"}, {"PULL", "pull"}, {"TRACKING", "track"}, {"TRACK", "track"}, {"ORBIT", "orbit"}, {"360°", "orbit"},
		{"FPV", "fpv"}, {"DOLLY", "truck"}, {"PAN", "pan"}, {"TILT", "tilt"}, {"CRANE", "crane_up"}, {"ZOOM", "zoom"}, {"HANDHELD", "handheld"},
		{"STATIC", "fixed"}, {"急升", "crane_up"}, {"震屏", "handheld_violent"}, {"震镜", "handheld_violent"}, {"短震", "handheld_violent"},
	}
	// wuxiFieldKeys 是字段名（去掉空格和斜杠）→ 统一键。
	wuxiFieldKeys = map[string]string{
		"景别焦段运镜": "camera", "景别运镜": "camera", "景别焦段": "camera", "镜头语言": "camera",
		"摄影机目标": "target", "CAMERATARGET": "target", "摄影机": "target",
		"站位": "station", "双方站位": "station",
		"动作链": "action", "动作": "action", "核心内容": "action", "动作因果链": "action",
		"力度": "power", "受击": "reaction", "受击反馈": "reaction", "战损": "damage", "伤痕": "damage", "伤痕等级": "damage",
		"闪帧": "flash", "特效": "fx", "特效层": "fx", "环境反馈": "env",
		"表情": "face", "微表情": "face", "A微表情": "face", "B微表情": "face",
		"台词": "lines", "光影": "light", "光影材质天气": "light", "灯光": "light",
		"下一镜连接": "next", "下一镜连接方式": "next", "连接": "next", "转场": "next",
		"剧本底": "subtext", "潜台词": "subtext",
	}
	// wuxiExtras 是进 spec.extras 的字段（统一键 → extras 键）。
	wuxiExtras = []struct{ key, name string }{
		{"power", "力度"}, {"reaction", "受击"}, {"damage", "战损"}, {"flash", "闪帧"}, {"fx", "特效"}, {"target", "摄影机目标"},
		{"station", "站位"}, {"env", "环境反馈"}, {"next", "下一镜连接"}, {"subtext", "剧本底"},
	}
	wuxiCutPoints = []struct{ word, point string }{
		{"OBJECT WIPE", "道具"}, {"遮挡", "道具"}, {"J-CUT", "光线"}, {"光", "光线"}, {"视线", "视线"}, {"声", "声音"},
		{"MATCH", "动作"}, {"IMPACT", "动作"}, {"动作", "动作"}, {"方向", "动作"},
	}
	wuxiContactWords = []struct{ word, contact string }{
		{"未接触", "none"}, {"无接触", "none"}, {"落空", "miss"}, {"闪避", "miss"}, {"闪开", "miss"}, {"躲开", "miss"}, {"侧闪", "miss"},
		{"擦", "graze"}, {"格挡", "blocked"}, {"架住", "blocked"}, {"挡住", "blocked"}, {"挡下", "blocked"}, {"卸力", "blocked"},
		{"命中", "hit"}, {"击中", "hit"}, {"打中", "hit"}, {"砸中", "hit"}, {"贯穿", "hit"}, {"破防", "hit"},
	}
)

type wuxiShot struct {
	start, end float64
	fields     map[string]string
}

// parseWuxi 读出各镜的字段；experience 是【核心观看体验】，rejected 是像镜头头却读不出时间码的行。
func parseWuxi(raw string) (shots []wuxiShot, experience string, rejected int) {
	var cur *wuxiShot
	key := ""
	for _, line := range lineBreakPattern.Split(raw, -1) {
		line = strings.TrimSpace(strings.Trim(line, "`"))
		if line == "" || line == "---" || line == "…" || line == "..." {
			continue
		}
		if m := wuxiShotHead.FindStringSubmatch(line); m != nil {
			shots = append(shots, wuxiShot{start: parseSectionNumber(m[2]), end: parseSectionNumber(m[3]), fields: map[string]string{}})
			cur, key = &shots[len(shots)-1], ""
			if rest := strings.TrimSpace(m[4]); rest != "" {
				wuxiColumns(cur, rest)
			}
			continue
		}
		if cur == nil {
			if m := wuxiExperienceLine.FindStringSubmatch(line); m != nil && strings.Contains(line, "核心观看体验") {
				experience = strings.TrimSpace(m[1])
			}
			continue
		}
		if strings.HasPrefix(strings.TrimLeft(line, "#*>- "), "镜") && wuxiHeadLike(line) {
			rejected++
			continue
		}
		if m := wuxiLabel.FindStringSubmatch(line); m != nil {
			if k, ok := wuxiFieldKeys[wuxiFieldName(m[1])]; ok {
				value := strings.TrimSpace(m[2])
				if prev := cur.fields[k]; prev != "" && k == "face" {
					value = prev + "；" + value
				}
				cur.fields[k], key = value, k
				continue
			}
		}
		if key != "" {
			cur.fields[key] = strings.TrimSpace(cur.fields[key] + " " + line)
		}
	}
	return shots, experience, rejected
}

// wuxiHeadLike 是一行像镜头头（「镜 N｜」起头）却没读出时间码。
func wuxiHeadLike(line string) bool {
	t := strings.TrimLeft(line, "#*>- ")
	t = strings.TrimPrefix(t, "镜")
	t = strings.TrimLeft(t, " 0123456789")
	return strings.HasPrefix(t, "｜") || strings.HasPrefix(t, "|")
}

func wuxiFieldName(name string) string {
	name = strings.TrimSpace(strings.Trim(name, "*【】[]"))
	return strings.NewReplacer(" ", "", "/", "", "／", "", "+", "", "·", "", "、", "").Replace(name)
}

// wuxiColumns 读单行写法里时间码后面的栏：八栏（景别+运镜｜站位｜动作｜A 微表情｜B 微表情｜力度｜伤痕｜光影）按位置取，
// 两栏是景别+运镜｜核心内容，其余前三栏按景别、站位、动作，后面的并进表情。
func wuxiColumns(sh *wuxiShot, rest string) {
	cols := strings.FieldsFunc(rest, func(r rune) bool { return r == '｜' || r == '|' })
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i])
	}
	set := func(k string, v string) {
		if v = strings.TrimSpace(v); v != "" && v != "—" && v != "-" {
			sh.fields[k] = v
		}
	}
	switch {
	case len(cols) >= 8:
		set("camera", cols[0])
		set("station", cols[1])
		set("action", cols[2])
		set("face", joinNonEmpty([]string{wuxiFace(cols[3]), wuxiFace(cols[4])}, "；"))
		set("power", cols[5])
		set("damage", cols[6])
		set("light", strings.Join(cols[7:], "；"))
	case len(cols) == 2:
		set("camera", cols[0])
		set("action", cols[1])
	case len(cols) == 1:
		set("camera", cols[0])
	default:
		set("camera", cols[0])
		set("station", cols[1])
		set("action", cols[2])
		var faces []string
		for _, c := range cols[3:] {
			faces = append(faces, wuxiFace(c))
		}
		set("face", joinNonEmpty(faces, "；"))
	}
}

// wuxiFace 去掉「不再给」这类占位（成品示例里 A 已出画）。
func wuxiFace(text string) string {
	if t := strings.TrimSpace(text); t != "不再给" && t != "—" && t != "无" {
		return t
	}
	return ""
}

func convertWuxi(raw string, ctx SectionContext) SectionShots {
	out := SectionShots{}
	parsed, experience, rejected := parseWuxi(raw)
	out.Stats.Rejected = rejected
	if len(parsed) == 0 {
		return out
	}
	out.Stats.Blocks = 1
	if experience != "" {
		out.Fields = append(out.Fields, SectionField{Name: "核心观看体验", Value: experience})
	}
	allNames := append(append([]string{}, ctx.Places...), ctx.Props...)
	var shots []sectionShot
	var starts, ends []float64
	for _, p := range parsed {
		sh, n := wuxiShotOf(p.fields, ctx)
		out.Stats.MusicStripped += n
		if sh.spec.Size == "" {
			out.Stats.NoSize++
		}
		body := strings.Join([]string{p.fields["station"], p.fields["action"], p.fields["env"], p.fields["target"]}, " ")
		sh.names = append(blockingNames(sh.spec.Blocking), mentionedNames(body+" "+ctx.Section.Heading, allNames)...)
		starts, ends = append(starts, p.start), append(ends, p.end)
		shots = append(shots, sh)
	}
	total := ctx.Section.Est
	if last := ends[len(ends)-1]; last > total {
		total = last
	}
	for i, est := range sectionEstimates(starts, ends, total) {
		shots[i].spec.Est = flexFloat(est)
	}
	if experience != "" {
		shots[0].spec.Extras["核心观看体验"] = experience
	}
	out.Rows = finishSectionShots(shots, ctx, &out.Stats)
	return out
}

func blockingNames(blocking []Blocking) []string {
	var out []string
	for _, b := range blocking {
		if b.Who != "" && !containsString(out, b.Who) {
			out = append(out, b.Who)
		}
	}
	return out
}

// wuxiShotOf 读一镜：景别 / 焦段 / 运镜 → size、cam、move；站位 → blocking；动作链 → action 与 fight；表情 → performance；
// 台词 → lines；光影 → light；下一镜连接 → transition；打斗标签原样进 extras。
func wuxiShotOf(f map[string]string, ctx SectionContext) (sectionShot, int) {
	var sh sectionShot
	spec := &sh.spec
	stripped := 0
	clean := func(text string) string {
		text, n := stripMusic(strings.TrimSpace(text))
		stripped += n
		return text
	}
	camera := f["camera"]
	size, _ := sectionSize(camera)
	if m := wuxiSizeAbbr.FindStringSubmatch(strings.ToUpper(camera)); m != nil && (size == "" || strings.Index(strings.ToUpper(camera), m[1]) < strings.Index(camera, size)) {
		size = wuxiSizeWords[m[1]]
	}
	spec.Size = size
	spec.Move = wuxiMoves(camera)
	spec.Cam = sectionCam(camera, size)
	spec.Subject = "person"
	spec.Extras = map[string]string{}
	for _, e := range wuxiExtras {
		if v := clean(f[e.key]); v != "" {
			spec.Extras[e.name] = v
		}
	}
	// 力度 / 受击 / 战损 / 闪帧没单独写一行时（单行写法、写进动作链里），从整镜文字里认标签。
	whole := strings.Join([]string{f["action"], f["power"], f["reaction"], f["damage"], f["flash"]}, " ")
	for _, tag := range []struct {
		name string
		re   *regexp.Regexp
	}{{"力度", wuxiPower}, {"受击", wuxiReaction}, {"战损", wuxiDamage}, {"闪帧", wuxiFlash}} {
		if spec.Extras[tag.name] == "" {
			if m := tag.re.FindString(whole); m != "" {
				spec.Extras[tag.name] = m
			}
		}
	}
	// 摄影机目标是人物时记成焦点。
	if t := strings.TrimSpace(f["target"]); t != "" {
		if name := resolveSectionName(wuxiNameHead.FindString(t), ctx.Characters); containsString(ctx.Characters, name) {
			spec.Focus = name
		}
	}
	spec.Blocking = wuxiBlocking(f["station"], ctx)
	action := clean(f["action"])
	spec.Action = action
	sh.plot = action
	if env := clean(f["env"]); env != "" && sh.plot != "" {
		sh.plot = strings.TrimRight(sh.plot, "。") + "；" + env
	}
	spec.Fight = wuxiFight(action, f["reaction"], spec.Blocking, ctx)
	for _, b := range strings.Split(f["face"], "；") {
		if b = strings.TrimSpace(strings.Trim(b, "。")); b != "" && b != "无" && b != "不再给" {
			if spec.Performance == nil {
				spec.Performance = &Performance{}
			}
			spec.Performance.Beats = append(spec.Performance.Beats, b)
		}
	}
	var screens []string
	sh.lines, screens = wuxiLines(f["lines"], ctx)
	addScreenText(spec, screens)
	spec.Light = wuxiLight(clean(f["light"]), ctx)
	if next := strings.TrimSpace(f["next"]); next != "" {
		spec.Transition = &Transition{Out: next, CutPoint: wuxiCutPoint(next)}
	}
	if spec.Focus == "" && len(spec.Blocking) > 0 {
		spec.Focus = spec.Blocking[0].Who
	}
	return sh, stripped
}

// wuxiMoves 是运镜：英文缩写（PUSH、WHIP、ORBIT…）与中文运镜词按出现先后，最多两项，fixed 不与别的同列。
func wuxiMoves(text string) flexStrings {
	upper := strings.ToUpper(text)
	type hit struct {
		at   int
		move string
	}
	var hits []hit
	for _, w := range wuxiMoveWords {
		if i := strings.Index(upper, strings.ToUpper(w.word)); i >= 0 {
			hits = append(hits, hit{i, w.move})
		}
	}
	for _, m := range sectionMoves(text) {
		hits = append(hits, hit{len(upper) + len(hits), m})
	}
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].at < hits[j-1].at; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	var moves flexStrings
	for _, h := range hits {
		if !containsString(moves, h.move) {
			moves = append(moves, h.move)
		}
	}
	if len(moves) > 1 {
		moves = slicesWithout(moves, "fixed")
	}
	if len(moves) > 2 {
		moves = moves[:2]
	}
	return moves
}

// wuxiBlocking 读站位：按「；」分人，每段认出人物（资产原名）、朝向和场景锚点；距离、高度、画面位置留在 extras 的站位原文里。
func wuxiBlocking(station string, ctx SectionContext) []Blocking {
	var out []Blocking
	for _, part := range strings.FieldsFunc(station, func(r rune) bool { return r == '；' || r == ';' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		who := ""
		if names := mentionedNames(part, ctx.Characters); len(names) > 0 {
			who = names[0]
		} else if head := wuxiNameHead.FindString(part); head != "" && !strings.HasPrefix(head, "相距") && !strings.HasPrefix(head, "双方") && !strings.HasPrefix(head, "距") {
			who = resolveSectionName(head, ctx.Characters)
			if !containsString(ctx.Characters, who) {
				continue
			}
		}
		if who == "" || slicesContainsWho(out, who) {
			continue
		}
		b := Blocking{Who: who, At: sectionAnchorIn(part, ctx.Anchors)}
		if m := wuxiFacing.FindString(part); m != "" {
			b.Facing = m
		}
		out = append(out, b)
	}
	return out
}

func slicesContainsWho(items []Blocking, who string) bool {
	for _, b := range items {
		if b.Who == who {
			return true
		}
	}
	return false
}

// wuxiFight 把动作链（起手→攻击→防守→变招→结果）拆进 fight：发起者是第一段打头的人物，攻击路径是第二段，防守是第三段，
// 结束姿态是最后一段；接触判定按动作与受击的用词（命中 / 擦 / 格挡 / 闪避 / 无接触），认不出时 none（R-SHOT-019 每镜要有）。
func wuxiFight(action, reaction string, blocking []Blocking, ctx SectionContext) *Fight {
	f := &Fight{Contact: "none"}
	parts := wuxiArrow.Split(strings.TrimSpace(action), -1)
	var kept []string
	for _, p := range parts {
		if p = strings.TrimSpace(strings.Trim(p, "。；")); p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) > 0 {
		if names := mentionedNames(kept[0], ctx.Characters); len(names) > 0 {
			f.Initiator = names[0]
		}
	}
	if f.Initiator == "" && len(blocking) > 0 {
		f.Initiator = blocking[0].Who
	}
	for _, b := range blocking {
		if b.Who != f.Initiator {
			f.Target = b.Who
			break
		}
	}
	switch len(kept) {
	case 0:
	case 1:
		f.Path = kept[0]
	case 2:
		f.Path, f.EndPose = kept[0], kept[1]
	default:
		f.Path, f.Response, f.EndPose = kept[1], kept[2], kept[len(kept)-1]
		if len(kept) == 3 {
			f.Path = kept[0] + "→" + kept[1]
		}
	}
	f.ForceTransfer = strings.TrimSpace(reaction)
	text := action + " " + reaction
	for _, w := range wuxiContactWords {
		if strings.Contains(text, w.word) {
			f.Contact = w.contact
			break
		}
	}
	return f
}

// wuxiLines 读台词：「人物（语气）：“原文”」，一镜可以有几句；「无」「（接上句）」是这一镜不另起台词。
// 冒号前写的是画面文字（「手机屏幕：“…”」）的不算台词，内容放进 screens。
func wuxiLines(text string, ctx SectionContext) (out []sectionLine, screens []string) {
	text = strings.TrimSpace(text)
	if text == "" || strings.Trim(text, "。") == "无" || strings.Contains(text, "接上句") && !strings.ContainsAny(text, "“\"「") {
		return nil, nil
	}
	quoted := wuxiQuoted.FindAllStringSubmatch(text, -1)
	for _, m := range quoted {
		if screenTextSpeaker(m[1], ctx.Characters) {
			screens = append(screens, strings.TrimSpace(m[3]))
			continue
		}
		ln := sectionLine{speaker: resolveSectionName(m[1], ctx.Characters), text: strings.TrimSpace(m[3]), voice: strings.TrimSpace(m[2])}
		if offscreenVoice(m[1] + m[2]) {
			ln.offscreen, ln.placement = true, "画外"
		}
		out = append(out, ln)
	}
	if len(quoted) == 0 {
		// 没加引号：整格当一句，说话人取冒号前。
		if speaker, line, ok := strings.Cut(text, "："); ok && strings.TrimSpace(line) != "" {
			line = strings.Trim(strings.TrimSpace(line), "“”\"「」")
			if screenTextSpeaker(speaker, ctx.Characters) {
				return nil, []string{line}
			}
			out = append(out, sectionLine{speaker: resolveSectionName(speaker, ctx.Characters), text: line})
		}
	}
	return out, screens
}

// wuxiLight 读光影：整格进 key，色温认「7500K」；光源锚点取光影里的场景锚点（室内补场景里像光源的锚点）。
func wuxiLight(text string, ctx SectionContext) *Light {
	if text == "" {
		if ctx.Indoor {
			if a := sectionLightAnchor("", ctx.Anchors); a != "" {
				return &Light{SourceAnchor: a}
			}
		}
		return nil
	}
	l := &Light{Key: text}
	if m := wuxiKelvin.FindStringSubmatch(text); m != nil {
		l.Kelvin = m[1] + "K"
	}
	l.SourceAnchor = sectionLightAnchor(firstClauseWith(text, sectionLightWords...), ctx.Anchors)
	if a := sectionAnchorIn(text, ctx.Anchors); a != "" {
		l.SourceAnchor = a
	}
	return l
}

func wuxiCutPoint(text string) string {
	upper := strings.ToUpper(text)
	for _, c := range wuxiCutPoints {
		if strings.Contains(upper, c.word) {
			return c.point
		}
	}
	return ""
}
