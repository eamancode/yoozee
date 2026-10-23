package drama

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// 装段（与 pack.py 一致）：镜头表 → 段。确定性代码，与 Python 共用 tests/sample 做逐字对照。
// - 按场分组，按台词字速 + 动作 + 表情估时（与检查同一算法）。
// - 一段上限 ≥30 秒（Seedance 2.5）时一场合计不超过上限就装成一段；其他模型按 15～25 秒目标装。
// - 切点：动作完成点 > 台词句末 > 景别变化处；接话不拆；同场后段带一致性桥。
// - refs 由镜头 refs_hint 在资产圣经里查文件取并集，按模型上限截断。

// PackInput 是装段的输入。Script、Bible 可为空；FileExists 判断身份参考图是否已经出了（没出的人物写外貌声明）。
type PackInput struct {
	Shotlist   Shotlist
	Script     *Script
	Bible      *Bible
	Model      Model
	Profile    *Profile
	ProfileID  string
	Target     [2]float64
	Vocab      *Vocab
	FileExists func(file string) bool
	// Phrases 是编译档案（镜头画面句、表情链、灯光七项等文字）；nil 用内置默认。
	Phrases *CompileProfile
}

var (
	nilWords = map[string]bool{"": true, "无": true, "——": true, "-": true, "none": true, "None": true}

	bigSignals     = []string{"掩面", "嘶吼", "大哭", "捶地", "瘫坐", "跪倒", "狂笑", "嚎啕", "怒吼", "尖叫", "扑", "剧烈", "失控", "决堤", "滚落", "大颗", "涌出", "夺眶", "甩落", "瘫"}
	forbidTags     = [][2]string{{"穿模", "model_clip"}, {"穿透", "model_clip"}, {"穿帮", "bg_extras"}, {"虚化过度", "bg_extras"}, {"反物理", "physics"}, {"脚离地", "physics"}, {"同手同脚", "physics"}, {"步伐僵硬", "physics"}, {"扭曲", "limb_distortion"}, {"僵硬", "limb_distortion"}, {"畸形", "limb_distortion"}, {"多指", "limb_distortion"}, {"少指", "limb_distortion"}, {"多余手指", "limb_distortion"}, {"比例", "proportion"}}
	functionMap    = [][2]string{{"建立", "建立空间"}, {"反转", "反转"}, {"揭", "信息揭示"}, {"真相", "信息揭示"}, {"发现", "信息揭示"}, {"先知", "信息揭示"}, {"悬", "信息揭示"}, {"交手", "动作推进"}, {"打斗", "动作推进"}, {"搏", "动作推进"}, {"追", "动作推进"}, {"穿越", "动作推进"}, {"崩溃", "情绪爆发"}, {"爆发", "情绪爆发"}, {"兑付", "情绪爆发"}, {"告白", "情绪爆发"}, {"钩", "收束留白"}, {"留白", "收束留白"}, {"对话", "对话拉扯"}, {"拉扯", "对话拉扯"}, {"施压", "对话拉扯"}}
	atmosMap       = [][2]string{{"压", "压抑"}, {"紧", "紧张"}, {"冷", "冷寂"}, {"暖", "温暖"}, {"燃", "高燃"}, {"疑", "神秘"}, {"荒", "荒诞"}, {"甜", "浪漫"}, {"惊", "紧张"}, {"怕", "紧张"}, {"悲", "压抑"}, {"追", "紧张"}, {"护", "紧张"}}
	hidden7Skip    = []string{"极度", "嚎啕", "暴怒", "狂喜", "目瞪口呆", "惊掉"}
	gazePrefix     = regexp.MustCompile(`^(面向|背对|转|看|正面|被|退|面朝|朝|侧)`)
	soundFragSplit = regexp.MustCompile(`[；;]`)
)

func cameraText(sh *Shot, cp *CompileProfile) string {
	words := cp.Camera
	f := sh.camF()
	parts := []string{words.X[sh.Cam.X], words.Y[sh.Cam.Y]}
	if f.Identity != "" {
		parts = append(parts, lookupOr(words.Composition, f.Identity))
	}
	if f.Geometry != "" {
		g := lookupOr(words.Composition, f.Geometry)
		if d := string(f.DutchDeg); d != "" && d != "0" && d != "0.0" {
			g += fill(words.Dutch, "deg", pyNumber(d))
		}
		parts = append(parts, g)
	}
	for _, m := range sh.Move {
		parts = append(parts, lookupOr(words.Move, m))
	}
	if f.LensMM != nil && *f.LensMM != 0 {
		parts = append(parts, fill(words.Lens, "mm", fmt.Sprintf("%d", int(*f.LensMM))))
	}
	dof := sh.Dof
	if dof == "" {
		dof = f.Dof
	}
	if dof == "shallow" {
		parts = append(parts, words.DofShallow)
	} else if dof == "deep" {
		parts = append(parts, words.DofDeep)
	}
	return joinNonEmpty(parts, "，")
}

func lookupOr(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return key
}

// pyNumber 把 JSON 数字字面量写成 Python 打印的样子（整数不带小数点，浮点数至少一位小数）。
func pyNumber(literal string) string {
	if strings.ContainsAny(literal, "eE") {
		var f float64
		fmt.Sscan(literal, &f)
		return pyFloatRepr(f)
	}
	return literal
}

// joinNonEmpty 拼片段时跳过空片段（含只有空白的）：编译档案里把某个模板设成空串就是去掉这一片，不会留下「；；」。
func joinNonEmpty(parts []string, sep string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func fightValue(v string) string {
	v = strings.TrimSpace(v)
	if nilWords[v] {
		return ""
	}
	return v
}

// fightText 把因果动作单元写成一句话：发起 → 路径 → 反应 → 接触 → 力的传递 → 结束姿态。
// fightText 把因果动作单元写成一句：发起 → 路径 → 反应 → 接触 → 力的传递 → 结束姿态。
// 路径已写到目标就不再写「打向」；反应前的「对方」只在目标是镜头里的人物、反应又没自带主语时写；
// 接触判定与反应、力的传递重复（「擦过」「刀尖擦过袖口」，「没有接触」「无直接接触」）时不写判定。
func fightText(f *Fight, people []string, cp *CompileProfile) string {
	ph := cp.Shot
	initiator, path, target, response := fightValue(f.Initiator), fightValue(f.Path), fightValue(f.Target), fightValue(f.Response)
	startsWithPerson := func(text string) bool {
		return text != "" && slices.ContainsFunc(people, func(p string) bool { return p != "" && strings.HasPrefix(text, p) })
	}
	var parts []string
	if initiator != "" {
		p := fill(ph.FightInitiator, "initiator", initiator)
		if path != "" {
			p += fill(ph.FightPath, "path", path)
		}
		if target != "" && !strings.Contains(path, target) {
			p += fill(ph.FightTarget, "target", target)
		}
		parts = append(parts, p)
	} else if path != "" {
		parts = append(parts, path)
	}
	if response != "" {
		if initiator != "" && startsWithPerson(target) && !startsWithPerson(response) {
			parts = append(parts, fill(ph.FightResponse, "response", response))
		} else {
			parts = append(parts, response)
		}
	}
	ft := fightValue(f.ForceTransfer)
	if c, ok := ph.FightContact[f.Contact]; ok {
		said := response + ft
		if !strings.Contains(said, c) && !((f.Contact == "none" || f.Contact == "miss") && strings.Contains(said, "接触")) {
			parts = append(parts, c)
		}
	}
	if ft != "" {
		parts = append(parts, ft)
	}
	if ep := fightValue(f.EndPose); ep != "" {
		parts = append(parts, fill(ph.FightEndPose, "pose", ep))
	}
	if len(parts) == 0 {
		return ""
	}
	joined := joinNonEmpty(parts, "；")
	if joined == "" {
		return ""
	}
	return fill(ph.Fight, "parts", joined)
}

// fightPeople 是一镜里的人物：焦点、站位里的人和发起者（判断打击目标、反应主语是不是人）。
func fightPeople(sh *Shot) []string {
	people := []string{}
	add := func(name string) {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(people, name) {
			people = append(people, name)
		}
	}
	add(sh.Focus)
	for _, b := range sh.Blocking {
		add(b.Who)
	}
	if sh.Fight != nil {
		add(sh.Fight.Initiator)
	}
	return people
}

type mergedPhase struct {
	label   string
	dur     float64
	signals []string
}

// ExpandArc 把情绪弧展开成分秒表演链（pack.expand_arc）：返回文本、用到的段号、弧的情绪族。cp 为 nil 用内置默认文字。
func ExpandArc(sh *Shot, est float64, profile *Profile, vocab *Vocab, prevPhaseEnd int, cp *CompileProfile) (string, []int, string) {
	cp = mustCompileProfile(cp)
	ref := sh.EmotionArc
	if !ref.truthy() || vocab == nil {
		return "", nil, ""
	}
	arc := vocab.Arc(ref.ID)
	if arc == nil || len(arc.Phases) == 0 {
		return "", nil, ""
	}
	phases := arc.Phases
	from := int(ref.PhaseFrom)
	if from == 0 {
		if prevPhaseEnd != 0 {
			from = prevPhaseEnd + 1
		} else {
			from = 1
		}
	}
	a := from - 1
	b := int(ref.PhaseTo)
	if b == 0 {
		b = len(phases)
	}
	a = max(0, min(a, len(phases)-1))
	b = max(a+1, min(b, len(phases)))
	chosen := phases[a:b]
	if est < 3 && len(chosen) > 2 {
		chosen = chosen[:2]
	}
	minSec := profile.Rules.expressionMinSec()
	total := 0.0
	for _, ph := range chosen {
		total += ph.To - ph.From
	}
	if total == 0 {
		total = 1.0
	}
	scale := est / total
	var merged []mergedPhase
	for _, ph := range chosen {
		d := (ph.To - ph.From) * scale
		if len(merged) > 0 && d < minSec {
			last := &merged[len(merged)-1]
			last.dur += d
			last.signals = append(slices.Clone(last.signals), ph.Signals...)
			last.label += "→" + ph.Label
		} else {
			merged = append(merged, mergedPhase{label: ph.Label, dur: d, signals: slices.Clone(ph.Signals)})
		}
	}
	if len(merged) > 1 && merged[0].dur < minSec {
		merged[1].dur += merged[0].dur
		merged[1].signals = append(slices.Clone(merged[0].signals), merged[1].signals...)
		merged[1].label = merged[0].label + "→" + merged[1].label
		merged = merged[1:]
	}
	intensity := ref.Intensity
	if intensity == "" && sh.Performance != nil {
		intensity = sh.Performance.Restraint
	}
	if intensity == "" {
		intensity = "normal"
	}
	nSig := 1
	if profile.PerformanceGrain == "micro" {
		nSig = 2
	}
	if intensity == "open" {
		nSig++
	}
	lineAt := -1
	if len(sh.Lines) > 0 {
		lineAt = min(len(merged)-1, max(0, int(math.RoundToEven(float64(len(merged))*0.4))))
	}
	var parts []string
	t := 0.0
	for i, m := range merged {
		sig := m.signals
		if intensity == "hidden7" {
			var kept []string
			for _, x := range sig {
				if !containsAny(x, bigSignals...) {
					kept = append(kept, x)
				}
			}
			if len(kept) == 0 && len(sig) > 0 {
				kept = sig[:1]
			}
			sig = kept
		}
		// 挑信号：一条取段末那一下（关键动作多在后面），两条取首尾，三条首中尾。
		if len(sig) > nSig {
			switch nSig {
			case 1:
				sig = []string{sig[len(sig)-1]}
			case 2:
				sig = []string{sig[0], sig[len(sig)-1]}
			default:
				sig = []string{sig[0], sig[len(sig)/2], sig[len(sig)-1]}
			}
		}
		seg := fill(cp.Expression.Phase, "from", fmtSec(t), "to", fmtSec(t+m.dur), "signals", strings.Join(sig, "，"))
		if i == lineAt {
			seg += cp.Expression.LineMarker
		}
		parts = append(parts, seg)
		t += m.dur
	}
	text := joinNonEmpty(parts, "；")
	if intensity == "hidden7" {
		text += cp.Expression.Hidden7Tail
	}
	used := make([]int, 0, len(chosen))
	for i := range chosen {
		used = append(used, a+1+i)
	}
	return text, used, arc.Family
}

// expressionTerms 取表情核心词：镜头写了用镜头的；否则按弧段的可见信号在词典里匹配，命中太少按情绪族取温和的默认词。
func expressionTerms(sh *Shot, family string, used []int, vocab *Vocab, cp *CompileProfile) []string {
	if len(sh.ExpressionTerms) > 0 {
		return []string(sh.ExpressionTerms)[:min(3, len(sh.ExpressionTerms))]
	}
	if family == "" || vocab == nil {
		return nil
	}
	var phases []ArcPhase
	if sh.EmotionArc != nil {
		if arc := vocab.Arc(sh.EmotionArc.ID); arc != nil {
			phases = arc.Phases
		}
	}
	var sel []ArcPhase
	for _, i := range used {
		if i > 0 && i <= len(phases) {
			sel = append(sel, phases[i-1])
		}
	}
	if len(sel) == 0 {
		sel = phases
	}
	var hay strings.Builder
	for _, ph := range sel {
		for _, s := range ph.Signals {
			hay.WriteString(s)
		}
	}
	h := hay.String()
	intensity := "normal"
	if sh.EmotionArc != nil && sh.EmotionArc.Intensity != "" {
		intensity = sh.EmotionArc.Intensity
	}
	var best *Expression
	score := 0
	for i := range vocab.Expressions {
		e := &vocab.Expressions[i]
		if intensity == "hidden7" && containsAny(e.Name, hidden7Skip...) {
			continue
		}
		keys := []string{e.Name}
		for _, t := range e.ZhTerms {
			if runeLen(t) >= 2 {
				keys = append(keys, t)
			}
		}
		sc := 0
		for _, k := range keys {
			if strings.Contains(h, k) {
				sc += 2
			}
		}
		for _, k := range keys {
			r := []rune(k)
			if len(r) < 3 {
				continue
			}
			for i := 0; i+1 < len(r); i++ {
				if strings.Contains(h, string(r[i:i+2])) {
					sc++
					break
				}
			}
		}
		if sc > score {
			best, score = e, sc
		}
	}
	if best != nil && score >= 3 {
		return best.ZhTerms[:min(2, len(best.ZhTerms))]
	}
	name := cp.Expression.FamilyTerms[family]
	for i := range vocab.Expressions {
		if vocab.Expressions[i].Name == name {
			terms := vocab.Expressions[i].ZhTerms
			return terms[:min(2, len(terms))]
		}
	}
	return nil
}

func forbidRiskTags(forbid []string) []string {
	var out []string
	for _, f := range forbid {
		for _, kv := range forbidTags {
			if strings.Contains(f, kv[0]) && !slices.Contains(out, kv[1]) {
				out = append(out, kv[1])
			}
		}
	}
	return out
}

// actionPatternText 把动作模式写成动作句片段，并返回它的禁止项。
func actionPatternText(sh *Shot, vocab *Vocab, cp *CompileProfile) (string, []string) {
	ap := sh.ActionPattern
	if ap == nil || vocab == nil {
		return "", nil
	}
	pat := vocab.Action(ap.ID)
	if pat == nil {
		return "", nil
	}
	var bits []string
	if pat.Parent == "" {
		// 泛泛的动作清单不进提示词，禁止项仍进 ⚠️。
		for _, x := range pat.Process {
			if !strings.Contains(x, "{{") && strings.Count(x, "、") < 3 && !strings.HasPrefix(x, "电影级") {
				bits = append(bits, x)
			}
		}
	}
	if ap.Variant != "" {
		bits = []string{ap.Variant}
	} else if len(pat.Variants) > 0 && len(bits) == 0 && pat.Parent == "" {
		for _, v := range pat.Variants {
			if strings.Count(v, "、") < 3 {
				bits = append(bits, v)
				break
			}
		}
	}
	if ap.Role != "" && len(pat.Roles) > 0 {
		want := "男主"
		if ap.Role == "主动" {
			want = "女主"
		}
		for _, r := range pat.Roles {
			if strings.HasPrefix(r, want) {
				bits = append(bits, r)
				break
			}
		}
	}
	var kept []string
	for _, b := range bits {
		if b != "" {
			kept = append(kept, b)
		}
	}
	kept = kept[:min(4, len(kept))]
	text := ""
	if len(kept) > 0 {
		text = fill(cp.Shot.ActionPattern, "name", strings.SplitN(pat.Name, "-", 2)[0], "items", strings.Join(kept, "；"))
	}
	return text, slices.Clone(pat.Forbid)
}

// expressionText 是这一镜的「表情」：弧展开 + 手写节拍；返回文本、核心词、用到的弧段号。
func expressionText(sh *Shot, est float64, profile *Profile, vocab *Vocab, prevPhaseEnd int, cp *CompileProfile) (string, []string, []int) {
	arcText, used, family := ExpandArc(sh, est, profile, vocab, prevPhaseEnd, cp)
	beats := sh.Performance.beats()
	text := arcText
	if len(beats) > 0 {
		if text != "" {
			text += "；"
		}
		text += fill(cp.Expression.Beats, "beats", strings.Join(beats, "；"))
	}
	var terms []string
	if arcText != "" || len(beats) > 0 {
		terms = expressionTerms(sh, family, used, vocab, cp)
	}
	return text, terms, used
}

// LightFullText 是灯光七项句（即梦格式）；一项都没写返回空。cp 为 nil 用内置默认标签。
func LightFullText(l Light, cp *CompileProfile) string {
	lp := mustCompileProfile(cp).Light
	var parts []string
	for _, kv := range [][2]string{{l.Key, lp.Key}, {l.Fill, lp.Fill}, {l.Rim, lp.Rim}, {l.Ratio, lp.Ratio}, {l.Kelvin, lp.Kelvin}, {l.Shadow, lp.Shadow}, {l.Exposure, lp.Exposure}, {l.Change, lp.Change}} {
		if kv[0] != "" {
			parts = append(parts, fill(lp.Item, "label", kv[1], "value", kv[0]))
		}
	}
	return joinNonEmpty(parts, "；")
}

func positionsText(sh *Shot, cp *CompileProfile) string {
	ph := cp.Shot
	var bits []string
	for _, b := range sh.Blocking {
		t := fill(ph.PositionAt, "who", b.Who, "at", b.At)
		if b.Facing != "" {
			t += "，" + b.Facing
		}
		if b.Behind != "" {
			t += fill(ph.PositionBehind, "behind", b.Behind)
		}
		if o := b.OccludedBy; o != "" {
			t += fill(ph.PositionOcclude, "by", o)
			if !(strings.HasSuffix(o, "挡住") || strings.HasSuffix(o, "遮住") || strings.HasSuffix(o, "挡") || strings.HasSuffix(o, "遮")) {
				t += "挡住一部分"
			}
		}
		bits = append(bits, t)
	}
	if sh.CameraSide != "" {
		bits = append(bits, fill(ph.PositionCamera, "side", sh.CameraSide))
	}
	return joinNonEmpty(bits, "；")
}

func dedupeStrings(items []string) []string {
	var out []string
	for _, it := range items {
		if !slices.Contains(out, it) {
			out = append(out, it)
		}
	}
	return out
}

func anchorsText(sh *Shot, scene *ScriptScene, bible *Bible) string {
	var loc *BibleLocation
	if bible != nil && scene != nil {
		for i := range bible.Locations {
			if bible.Locations[i].Name == scene.LocationRef {
				loc = &bible.Locations[i]
			}
		}
	}
	known := map[string]bool{}
	if loc != nil {
		for _, a := range loc.Anchors {
			known[a] = true
		}
	}
	var seen, behind []string
	for _, b := range sh.Blocking {
		if b.At != "" && !slices.Contains(seen, b.At) {
			seen = append(seen, b.At)
		}
		if bh := b.Behind; bh != "" {
			if known[bh] || !slices.Contains(behind, bh) {
				if known[bh] && !slices.Contains(seen, bh) {
					seen = append(seen, bh)
				} else {
					behind = append(behind, bh)
				}
			}
		}
	}
	var extra []string
	for _, h := range sh.RefsHint {
		if strings.Contains(h, "·") {
			extra = append(extra, h)
		}
	}
	var parts []string
	if len(seen) > 0 {
		parts = append(parts, "锚点："+strings.Join(seen, "、"))
	}
	if len(behind) > 0 {
		// 与参考实现一致：「身后」句写两遍。
		parts = append(parts, "身后："+strings.Join(dedupeStrings(behind), "、"))
		parts = append(parts, "身后："+strings.Join(dedupeStrings(behind), "、"))
	}
	if len(extra) > 0 {
		parts = append(parts, "状态："+strings.Join(extra, "、"))
	}
	if loc != nil && loc.LightSource != "" {
		parts = append(parts, "光源："+loc.LightSource)
	}
	return strings.Join(parts, "；")
}

func motionText(sh *Shot, cp *CompileProfile) string {
	var bits []string
	if f := sh.Fight; f != nil {
		if f.Initiator != "" && f.Path != "" && f.Initiator != "无" {
			bits = append(bits, f.Initiator+f.Path)
		}
		if f.Response != "" && f.Response != "无" {
			bits = append(bits, f.Response)
		}
	}
	var moves []string
	for _, m := range sh.Move {
		switch m {
		case "fixed", "handheld_micro", "handheld", "handheld_violent", "rack_focus":
		default:
			moves = append(moves, lookupOr(cp.Camera.Move, m))
		}
	}
	if len(moves) > 0 {
		bits = append(bits, fill(cp.Shot.MotionCamera, "moves", strings.Join(moves, "、")))
	}
	if len(bits) == 0 {
		if sh.StartState == sh.EndState {
			bits = append(bits, cp.Shot.MotionStill)
		} else {
			bits = append(bits, fill(cp.Shot.MotionChange, "from", sh.StartState, "to", sh.EndState))
		}
	}
	return joinNonEmpty(bits, "；")
}

func vfxText(v VfxRef, vocab *Vocab, cp *CompileProfile) string {
	name, exe := v.ID, ""
	if vocab != nil {
		if e := vocab.VfxEntry(v.ID); e != nil {
			if e.Name != "" {
				name = e.Name
			}
			exe = e.AIExec
			if exe == "" {
				exe = e.Definition
			}
			exe = strings.SplitN(exe, "。", 2)[0]
		}
	}
	var bits []string
	if exe != "" {
		bits = append(bits, exe)
	}
	if v.Attribute != "" {
		bits = append(bits, v.Attribute)
	}
	if v.Scale != "" {
		bits = append(bits, fill(cp.Shot.VfxScale, "scale", v.Scale))
	}
	seconds := ""
	if s := string(v.Seconds); s != "" && s != "0" {
		seconds = fill(cp.Shot.VfxSeconds, "seconds", pyNumber(s))
	}
	return fill(cp.Shot.Vfx, "name", name, "items", joinNonEmpty(bits, "，"), "seconds", seconds)
}

// shotText 是一镜的画面句：动作 + 动作模式 + 表情 / 表演 + 状态 + 光 + 灯光七项 + 动作单元 + 流派 + 特效。
func shotText(sh *Shot, est float64, profile *Profile, vocab *Vocab, prevPhaseEnd int, cp *CompileProfile) string {
	ph := cp.Shot
	text := strings.TrimSpace(sh.Action)
	if pat, _ := actionPatternText(sh, vocab, cp); pat != "" {
		text += pat
	}
	if sh.EmotionArc.truthy() {
		expr, terms, _ := expressionText(sh, est, profile, vocab, prevPhaseEnd, cp)
		if expr != "" {
			text += fill(ph.Expression, "text", expr)
		}
		if len(terms) > 0 {
			text += fill(ph.CoreTerms, "terms", strings.Join(terms, "、"))
		}
	} else if beats := sh.Performance.beats(); len(beats) > 0 {
		text += fill(ph.Performance, "beats", strings.Join(beats, "；"))
	}
	if sh.State != "" {
		text += fill(ph.State, "state", sh.State)
	}
	light := sh.light()
	if light.SourceAnchor != "" {
		direction := ""
		if light.Direction != "" {
			direction = fill(ph.LightDirection, "direction", light.Direction)
		}
		text += fill(ph.LightSource, "anchor", light.SourceAnchor, "direction", direction)
	}
	if lf := LightFullText(light, cp); lf != "" {
		text += fill(ph.LightFull, "text", lf)
	}
	if sh.Fight.truthy() {
		text += fightText(sh.Fight, fightPeople(sh), cp)
	}
	if sh.Fight != nil && len(sh.Fight.MartialIDs) > 0 && vocab != nil {
		var names []string
		for _, id := range sh.Fight.MartialIDs {
			if e := vocab.MartialEntry(id); e != nil && e.Name != "" {
				names = append(names, e.Name)
			}
		}
		if len(names) > 0 {
			text += fill(ph.Martial, "names", strings.Join(names, "、"))
		}
	}
	for _, v := range sh.Vfx {
		text += vfxText(v, vocab, cp)
	}
	return text
}

func soundText(sh *Shot, cp *CompileProfile) string {
	s := strings.TrimSpace(sh.Sfx)
	appendPart := func(part string) {
		if s != "" {
			s += "；"
		}
		s += part
	}
	if sh.Voice != "" {
		appendPart(sh.Voice)
	}
	switch sh.Bridge {
	case "in":
		appendPart(cp.Shot.SoundBridgeIn)
	case "out":
		appendPart(cp.Shot.SoundBridgeOut)
	}
	return s
}

func bibleVariantBase(ch *BibleCharacter) *BibleVariant {
	for i := range ch.Variants {
		kind := ch.Variants[i].Kind
		if kind == "" {
			kind = "base"
		}
		if kind == "base" {
			return &ch.Variants[i]
		}
	}
	if len(ch.Variants) > 0 {
		return &ch.Variants[0]
	}
	return nil
}

func firstContaining(items []string, sub string) string {
	for _, it := range items {
		if strings.Contains(it, sub) {
			return it
		}
	}
	return ""
}

// findRefs 按 refs_hint（人物名 / 人物·变体 / 场景名 / 道具名）在资产圣经里找文件；没写 hint 时按站位里的人物和场的 location_ref。
func findRefs(sh *Shot, bible *Bible, scene *ScriptScene, lines scriptLines) ([]Ref, []Voice) {
	hints := []string(slices.Clone(sh.RefsHint))
	if len(hints) == 0 {
		for _, b := range sh.Blocking {
			hints = append(hints, b.Who)
		}
		if scene != nil && scene.LocationRef != "" {
			hints = append(hints, scene.LocationRef)
		}
	}
	var refs []Ref
	var voices []Voice
	close := sh.Cam.Z == "Z1" || sh.Cam.Z == "Z2" || sh.Cam.Z == "Z3" || sh.Cam.Z == "Z4"
	for _, h := range hints {
		name, variant, _ := strings.Cut(h, "·")
		var ch *BibleCharacter
		for i := range bible.Characters {
			if bible.Characters[i].Name == name {
				ch = &bible.Characters[i]
				break
			}
		}
		if ch != nil {
			var v *BibleVariant
			if variant != "" {
				for i := range ch.Variants {
					if ch.Variants[i].Label == variant {
						v = &ch.Variants[i]
						break
					}
				}
			} else {
				v = bibleVariantBase(ch)
			}
			if v != nil {
				pick := ""
				fallback := v.Sheet
				if len(v.Crops) > 0 {
					fallback = v.Crops[0]
				}
				if close {
					pick = firstContaining(v.Crops, "特写")
				} else {
					pick = firstContaining(v.Crops, "正面")
				}
				if pick == "" {
					pick = fallback
				}
				if pick != "" {
					note := ch.Name
					if variant != "" {
						note += "（" + variant + "）"
					}
					if len(v.LockCard) > 0 {
						note += "，" + strings.Join(v.LockCard[:min(3, len(v.LockCard))], "；")
					}
					refs = append(refs, Ref{File: pick, Role: "identity", Note: note})
				}
				if ch.Speaks && v.Voice != "" {
					for _, ln := range sh.Lines {
						if beat, ok := lines.byID[ln.LineID]; ok && beat.Speaker == ch.Name {
							voices = append(voices, Voice{Character: ch.Name, File: v.Voice})
							break
						}
					}
				}
			}
			continue
		}
		var lo *BibleLocation
		for i := range bible.Locations {
			if bible.Locations[i].Name == name {
				lo = &bible.Locations[i]
				break
			}
		}
		if lo != nil {
			side := strings.ToUpper(sh.CameraSide)
			file := lo.Plate
			if strings.Contains(side, "A") {
				file = lo.SideA
			} else if strings.Contains(side, "B") {
				file = lo.SideB
			}
			if variant != "" {
				for _, ver := range lo.Versions {
					if ver.Label == variant {
						if ver.Plate != "" {
							file = ver.Plate
						}
						break
					}
				}
			}
			if file != "" {
				which := ""
				if file == lo.SideA {
					which = "A 侧机位身后"
				} else if file == lo.SideB {
					which = "B 侧机位身后"
				}
				role := "location"
				if which != "" {
					role = "plate"
				}
				note := lo.Name
				if variant != "" {
					note += "（" + variant + "）"
				}
				if which != "" {
					note += "，" + which
				}
				refs = append(refs, Ref{File: file, Role: role, Note: note})
			}
			continue
		}
		for i := range bible.Props {
			pr := &bible.Props[i]
			if pr.Name != name {
				continue
			}
			file := pr.Image
			if variant != "" {
				for _, st := range pr.States {
					if st.When == variant {
						if st.Image != "" {
							file = st.Image
						}
						break
					}
				}
			}
			if file != "" {
				note := pr.Name
				if variant != "" {
					note += "（" + variant + "）"
				}
				refs = append(refs, Ref{File: file, Role: "prop", Note: note})
			}
			break
		}
	}
	return refs, voices
}

func refPerson(r Ref) string {
	name := strings.SplitN(r.Note, "，", 2)[0]
	return strings.SplitN(name, "（", 2)[0]
}

// dedupeIdentity 同一个人在一段里只挂一张：段里有近景就挂特写，否则挂全身正面；其他参考按文件去重。
func dedupeIdentity(refs []Ref) []Ref {
	byPerson := map[string]Ref{}
	for _, r := range refs {
		if r.Role != "identity" {
			continue
		}
		name := refPerson(r)
		cur, ok := byPerson[name]
		if !ok || (strings.Contains(r.File, "特写") && !strings.Contains(cur.File, "特写")) {
			byPerson[name] = r
		}
	}
	seen := map[string]bool{}
	var out []Ref
	for _, r := range refs {
		if r.Role == "identity" {
			pick := byPerson[refPerson(r)]
			if !seen[pick.File] {
				seen[pick.File] = true
				out = append(out, pick)
			}
		} else if !seen[r.File] {
			seen[r.File] = true
			out = append(out, r)
		}
	}
	return out
}

// cutScore 是在镜头 a 之后切、b 之前开始的分数：越高越好；ok=false 表示不许切（接话不拆）。
func cutScore(a, b *Shot) (int, bool) {
	if len(a.Lines) > 0 && len(b.Lines) > 0 && !hasActionVerb(a.Action) {
		return 0, false
	}
	score := 0
	if len(a.Lines) == 0 {
		score += 3
	} else {
		score += 2
	}
	if a.Size != b.Size {
		score++
	}
	if a.EndState != "" {
		score++
	}
	return score, true
}

func sumRange(xs []float64, from, to int) float64 {
	t := 0.0
	for _, x := range xs[from:to] {
		t += x
	}
	return t
}

// splitScene 返回切点下标（在该下标之后切）；整场不超过 hi 不切；找不到合法切点返回 nil, false。
func splitScene(shots []*Shot, ests []float64, hi, lo float64) ([]int, bool) {
	total := sumRange(ests, 0, len(ests))
	if total <= hi {
		return []int{}, true
	}
	nMin := int(math.Ceil(total / hi))
	type candidate struct {
		n      int
		spread float64
		cuts   []int
	}
	var best *candidate
	less := func(a, b candidate) bool {
		if a.n != b.n {
			return a.n < b.n
		}
		if a.spread != b.spread {
			return a.spread < b.spread
		}
		return slices.Compare(a.cuts, b.cuts) < 0
	}
	for n := nMin; n <= min(len(shots), nMin+3); n++ {
		var cuts []int
		last := 0
		ok := true
		for i := 1; i < n; i++ {
			goal := total * float64(i) / float64(n)
			type cand struct {
				dist  float64
				score int
				k     int
			}
			var cands []cand
			for k := last; k < len(shots)-1; k++ {
				accK := sumRange(ests, last, k+1)
				if accK > hi {
					break
				}
				sc, allowed := cutScore(shots[k], shots[k+1])
				if !allowed || accK < lo {
					continue
				}
				cands = append(cands, cand{math.Abs(sumRange(ests, 0, k+1) - goal), -sc, k})
			}
			if len(cands) == 0 {
				ok = false
				break
			}
			sort.SliceStable(cands, func(x, y int) bool {
				if cands[x].dist != cands[y].dist {
					return cands[x].dist < cands[y].dist
				}
				if cands[x].score != cands[y].score {
					return cands[x].score < cands[y].score
				}
				return cands[x].k < cands[y].k
			})
			k := cands[0].k
			cuts = append(cuts, k)
			last = k + 1
		}
		if !ok {
			continue
		}
		rest := sumRange(ests, last, len(ests))
		if rest > hi || rest < lo {
			continue
		}
		spread := 0.0
		for i := range cuts {
			start := 0
			if i > 0 {
				start = cuts[i-1] + 1
			}
			spread = math.Max(spread, sumRange(ests, start, cuts[i]+1))
		}
		c := candidate{n, spread, cuts}
		if best == nil || less(c, *best) {
			best = &c
		}
	}
	if best == nil {
		return nil, false
	}
	return best.cuts, true
}

func mapFirst(text string, table [][2]string, def string) string {
	for _, kv := range table {
		if strings.Contains(text, kv[0]) {
			return kv[1]
		}
	}
	return def
}

// castLines 是没有身份参考图（或图还没出）的在场人物的外貌声明（lock_card 前 6 条）。
func castLines(shots []*Shot, refs []Ref, bible *Bible, exists func(string) bool, lockMax int) []CastLine {
	have := map[string]bool{}
	for _, r := range refs {
		if r.Role == "identity" && exists != nil && exists(r.File) {
			have[refPerson(r)] = true
		}
	}
	var names []string
	for _, sh := range shots {
		for _, b := range sh.Blocking {
			if b.Who != "" && !slices.Contains(names, b.Who) {
				names = append(names, b.Who)
			}
		}
		if sh.Focus != "" && !slices.Contains(names, sh.Focus) {
			names = append(names, sh.Focus)
		}
	}
	var out []CastLine
	if bible == nil {
		return out
	}
	for _, ch := range bible.Characters {
		if !slices.Contains(names, ch.Name) || have[ch.Name] {
			continue
		}
		var lock []string
		if len(ch.Variants) > 0 {
			lock = ch.Variants[0].LockCard
		}
		if len(lock) == 0 {
			lock = ch.Anchors
		}
		if len(lock) > 0 {
			out = append(out, CastLine{Name: ch.Name, Lock: slices.Clone(lock[:min(lockMax, len(lock))])})
		}
	}
	return out
}

func screenTextsFor(shots []*Shot, scene *ScriptScene) []string {
	if scene == nil {
		return nil
	}
	var hay strings.Builder
	for _, sh := range shots {
		hay.WriteString(sh.Action)
	}
	var out []string
	for _, b := range scene.Beats {
		if b.Kind == "screen_text" && b.Text != "" && !slices.Contains(out, b.Text) && strings.Contains(hay.String(), b.Text) {
			out = append(out, b.Text)
		}
	}
	return out
}

// mostCommonFirst 返回出现次数最多的项，平手取先出现的（装段确定性）。
func mostCommonFirst(items []string) (string, int) {
	best, bestN := "", 0
	counts := map[string]int{}
	for _, it := range items {
		counts[it]++
	}
	for _, it := range items {
		if counts[it] > bestN {
			best, bestN = it, counts[it]
		}
	}
	return best, bestN
}

func segSharedBed(seg []SubShot) string {
	var frags []string
	for _, s := range seg {
		for _, part := range soundFragSplit.Split(s.Sound, -1) {
			part = strings.Trim(part, "。 ")
			if part != "" {
				frags = append(frags, part)
			}
		}
	}
	best, n := mostCommonFirst(frags)
	if n >= 2 {
		return best
	}
	return ""
}

func jimengBlock(scene *ScriptScene, shots []*Shot, bible *Bible, segShots []SubShot) *Jimeng {
	if scene == nil {
		return nil
	}
	span := firstNonEmptyString(scene.TimeJump, scene.StoryTime, "与生成时长同步，无时间跳跃")
	kind := scene.Kind
	if kind == "" {
		kind = "dialogue"
	}
	def := "动作推进"
	if kind == "dialogue" {
		def = "对话拉扯"
	}
	out := &Jimeng{NarrativeSpan: span, Function: mapFirst(scene.Function, functionMap, def)}
	var strategies, srcs []string
	for _, sh := range shots {
		if sh.Strategy != "" && !slices.Contains(strategies, sh.Strategy) {
			strategies = append(strategies, sh.Strategy)
		}
		if a := sh.light().SourceAnchor; a != "" {
			srcs = append(srcs, a)
		}
	}
	parts := strings.Split(scene.Emotion, "→")
	emo := strings.TrimSpace(parts[len(parts)-1])
	atmosDef := emo
	if atmosDef == "" {
		atmosDef = "紧张"
		if scene.Kind == "action" {
			atmosDef = "高燃"
		}
	}
	out.Atmosphere = mapFirst(emo, atmosMap, atmosDef)
	out.Strategy = strings.Join(strategies, "；")
	out.LightSource, _ = mostCommonFirst(srcs)
	out.SoundBed = segSharedBed(segShots)
	if bible != nil {
		var gv []string
		for _, c := range bible.Characters {
			if c.GroupVariation != "" && slices.Contains(scene.Characters, c.Name) {
				gv = append(gv, c.Name+"："+c.GroupVariation)
			}
		}
		out.GroupVariation = strings.Join(gv, "；")
	}
	return out
}

type packState struct {
	in     PackInput
	cp     *CompileProfile
	lines  scriptLines
	arcEnd map[string]int
}

func (p *packState) buildSegment(segID, sceneID string, scene *ScriptScene, shots []*Shot, ests []float64, prev *Segment) Segment {
	m := p.in.Model
	durRaw := 0.0
	for _, e := range ests {
		durRaw += e
	}
	step := m.Duration.Step
	if step <= 0 {
		step = 1
	}
	dur := max(m.Duration.Min, min(m.Duration.Max, int(math.Ceil(durRaw/float64(step)))*step))
	bible := p.in.Bible
	if bible == nil {
		bible = &Bible{}
	}
	profile := p.in.Profile
	cp := p.cp
	voiceProfiles := map[string]BibleCharacter{}
	for _, c := range bible.Characters {
		voiceProfiles[c.Name] = c
	}
	at := 0.0
	var segShots []SubShot
	var refs []Ref
	var voices []Voice
	var risk []string
	for i, sh := range shots {
		e := ests[i]
		var lines []Line
		for _, ln := range sh.Lines {
			beat, ok := p.lines.byID[ln.LineID]
			if !ok {
				continue
			}
			item := Line{Speaker: beat.Speaker, Text: beat.Text, LineID: beat.ID, Delivery: ln.Delivery, Pace: ln.Pace, Tone: ln.Tone, Emotion: ln.Emotion, Placement: ln.Placement}
			if beat.Kind == "vo" || ln.Offscreen {
				item.VO = true
			}
			if vp, ok := voiceProfiles[item.Speaker]; ok && vp.VoiceProfile.Timbre != "" {
				item.Timbre = vp.VoiceProfile.Timbre
				if vp.VoiceProfile.LineQuality != "" {
					item.Timbre += "，" + vp.VoiceProfile.LineQuality
				}
			}
			lines = append(lines, item)
		}
		prevEnd := 0
		if sh.EmotionArc != nil {
			prevEnd = p.arcEnd[sh.EmotionArc.Continues]
		}
		s := SubShot{At: pyRound(at, 1), Size: sh.Size, Camera: cameraText(sh, cp), Text: shotText(sh, e, profile, p.in.Vocab, prevEnd, cp), ShotID: sh.ID}
		expr, terms, used := expressionText(sh, e, profile, p.in.Vocab, prevEnd, cp)
		if len(used) > 0 {
			p.arcEnd[sh.ID] = used[len(used)-1]
		}
		s.Expression = expr
		s.ExpressionTerms = terms
		s.Purpose, s.Composition, s.VO, s.StartState = sh.Purpose, sh.Composition, sh.VO, sh.StartState
		if sh.Transition.truthy() {
			tr := *sh.Transition
			s.Transition = &tr
		}
		s.Positions = positionsText(sh, cp)
		s.Motion = motionText(sh, cp)
		s.Anchors = anchorsText(sh, scene, p.in.Bible)
		s.LightFull = LightFullText(sh.light(), cp)
		_, forbid := actionPatternText(sh, p.in.Vocab, cp)
		for _, tag := range forbidRiskTags(forbid) {
			if !slices.Contains(risk, tag) {
				risk = append(risk, tag)
			}
		}
		s.Lines = lines
		s.Sound = soundText(sh, cp)
		s.EndState = sh.EndState
		if len(sh.RiskTags) > 0 {
			s.RiskTags = slices.Clone([]string(sh.RiskTags))
			risk = append(risk, sh.RiskTags...)
		}
		segShots = append(segShots, s)
		r, v := findRefs(sh, bible, scene, p.lines)
		refs = append(refs, r...)
		voices = append(voices, v...)
		at += e
	}
	if durRaw != 0 && float64(dur) < durRaw {
		scale := float64(dur) / durRaw
		for i := range segShots {
			segShots[i].At = pyRound(segShots[i].At*scale, 1)
		}
	}
	refs = dedupeIdentity(refs)
	var dedupVoices []Voice
	seenVoice := map[string]bool{}
	for _, v := range voices {
		if !seenVoice[v.File] {
			seenVoice[v.File] = true
			dedupVoices = append(dedupVoices, v)
		}
	}
	voices = dedupVoices
	if lim := m.Refs.Images; lim != nil && len(refs) > *lim {
		var ident, other []Ref
		for _, r := range refs {
			if r.Role == "identity" {
				ident = append(ident, r)
			} else {
				other = append(other, r)
			}
		}
		refs = append(ident, other...)[:*lim]
	}
	if lim := m.Refs.Audios; lim != nil && len(voices) > *lim {
		voices = voices[:*lim]
	}
	last := shots[len(shots)-1]
	// 结束状态末尾的标点去掉再接视线，模板负责收句（不出「。；」）。
	rules := strings.TrimRight(strings.TrimSpace(last.EndState), "。；;，,！!？? ")
	var gaze []string
	for _, b := range last.Blocking {
		if b.Facing == "" {
			continue
		}
		if gazePrefix.MatchString(b.Facing) {
			gaze = append(gaze, b.Who+b.Facing)
		} else {
			gaze = append(gaze, fill(cp.Segment.GazeToward, "who", b.Who, "target", b.Facing))
		}
	}
	if g := joinNonEmpty(gaze, "；"); g != "" {
		rules = joinNonEmpty([]string{rules, fill(cp.Segment.Gaze, "gaze", g)}, "；")
	}
	if rules != "" {
		// 这是段末连戏状态，不是开场状态；编译稿放在风格块之后。
		rules = fill(cp.Segment.EndState, "rules", rules)
	}
	mode := ModeTextOnly
	if len(refs) > 0 {
		mode = ModeLockIdentity
	}
	shotIDs := make([]string, 0, len(shots))
	for _, sh := range shots {
		shotIDs = append(shotIDs, sh.ID)
	}
	seg := Segment{ID: segID, Scene: sceneID, Title: runePrefix(shots[0].Purpose, 40), Dur: float64(dur), Mode: mode, Shots: segShots, ShotIDs: shotIDs, Profile: p.in.ProfileID}
	seg.Jimeng = jimengBlock(scene, shots, p.in.Bible, segShots)
	seg.Cast = castLines(shots, refs, p.in.Bible, p.in.FileExists, cp.Cast.LockMax)
	seg.ScreenText = screenTextsFor(shots, scene)
	seg.Refs = refs
	seg.Voices = voices
	seg.Rules = rules
	seg.Hook = last.Purpose
	if scene != nil {
		seg.StorySpan = scene.TimeJump
	}
	if len(risk) > 0 {
		warnings := dedupeStrings(risk)
		sort.Strings(warnings)
		seg.Warnings = warnings
	}
	if prev != nil {
		prevEnd := ""
		if n := len(prev.Shots); n > 0 {
			prevEnd = prev.Shots[n-1].EndState
		}
		if prevEnd == "" {
			prevEnd = prev.Rules
		}
		prevEnd = strings.TrimSpace(prevEnd)
		var shared []string
		for _, r := range prev.Refs {
			if r.Role != "motion" {
				shared = append(shared, r.File)
			}
		}
		// 后段参考与前段同一套、同顺序：前段有的放前面，本段新增的接在后面。
		if len(shared) > 0 {
			var merged []Ref
			for _, f := range shared {
				for _, r := range prev.Refs {
					if r.File == f {
						merged = append(merged, r)
						break
					}
				}
			}
			for _, r := range refs {
				if !slices.Contains(shared, r.File) {
					merged = append(merged, r)
				}
			}
			seg.Refs = merged
		}
		if len(seg.Refs) == 0 {
			seg.Refs = nil
			seg.Mode = ModeTextOnly
		}
		var frags []string
		for _, sh := range prev.Shots {
			for _, f := range strings.Split(sh.Sound, "；") {
				if f = strings.TrimSpace(f); f != "" {
					frags = append(frags, f)
				}
			}
		}
		bed, _ := mostCommonFirst(frags)
		reanchor := cp.Segment.Reanchor
		if prevEnd != "" {
			reanchor = fill(cp.Segment.ReanchorWithPrev, "prev", prev.ID, "state", prevEnd)
		}
		if shared == nil {
			shared = []string{}
		}
		seg.ContinuityBridge = &ContinuityBridge{PrevSegment: prev.ID, HandoffState: prevEnd, SharedRefs: shared, ReanchorText: reanchor, SharedBed: bed}
	}
	return seg
}

// Pack 把镜头表装成段；problems 是找不到合法切点的场（这时整场装成一段，由用户改估时或并镜）。
func Pack(in PackInput) ([]Segment, []string) {
	if in.Profile == nil {
		in.Profile = &Profile{}
	}
	if in.Target == [2]float64{} {
		in.Target = [2]float64{15, 25}
	}
	p := &packState{in: in, cp: mustCompileProfile(in.Phrases), lines: newScriptLines(in.Script), arcEnd: map[string]int{}}
	hi, lo := float64(in.Model.Duration.Max), float64(in.Model.Duration.Min)
	is25 := hi >= 30
	var order []string
	groups := map[string][]*Shot{}
	for i := range in.Shotlist.Shots {
		sh := &in.Shotlist.Shots[i]
		if _, ok := groups[sh.Scene]; !ok {
			order = append(order, sh.Scene)
		}
		groups[sh.Scene] = append(groups[sh.Scene], sh)
	}
	segments := []Segment{}
	var problems []string
	n := 0
	for _, sceneID := range order {
		shots := groups[sceneID]
		scene := in.Script.scene(sceneID)
		ests := make([]float64, len(shots))
		for i, sh := range shots {
			ests[i] = EstSeconds(sh, p.lines)
		}
		var cuts []int
		var ok bool
		if is25 {
			cuts, ok = splitScene(shots, ests, hi, lo)
		} else {
			cuts, ok = splitScene(shots, ests, math.Min(hi, in.Target[1]), lo)
		}
		if !ok {
			problems = append(problems, fmt.Sprintf("场 %s 合计 %s 秒，找不到合法切点（接话不拆、每段在 [%d,%d] 内）：改镜头 est 或并镜，不删台词", sceneID, strings.TrimSuffix(fmt.Sprintf("%.1f", sumRange(ests, 0, len(ests))), ""), in.Model.Duration.Min, in.Model.Duration.Max))
			cuts = []int{}
		}
		bounds := append(append([]int{-1}, cuts...), len(shots)-1)
		var prev *Segment
		for i := 0; i+1 < len(bounds); i++ {
			a, b := bounds[i]+1, bounds[i+1]+1
			n++
			seg := p.buildSegment(fmt.Sprintf("S%02d", n), sceneID, scene, shots[a:b], ests[a:b], prev)
			segments = append(segments, seg)
			prev = &segments[len(segments)-1]
		}
	}
	return segments, problems
}
