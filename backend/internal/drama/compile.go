package drama

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// CompiledFile 是编译结果里的一个参考素材：文件、它在提示词里的编号和用途。
type CompiledFile struct {
	File string `json:"file"`
	Tag  string `json:"tag"`
	Role string `json:"role"`
}

// CompileResult 是一段在某个模型上的编译结果。Paste 是即梦网页粘贴版（Seedance：提示词 + 上传清单；其他模型同 Prompt）。
type CompileResult struct {
	Segment  string         `json:"segment"`
	Model    string         `json:"model"`
	Duration int            `json:"duration"`
	Prompt   string         `json:"prompt"`
	Paste    string         `json:"paste"`
	Files    []CompiledFile `json:"files"`
	Chars    int            `json:"chars"`
	Warnings []string       `json:"warnings"`
	Issues   []Issue        `json:"issues"`
}

// softPromptBudget 是模型字数上限未写也未实测时的软预算（只警告）。
const softPromptBudget = 4000

// riskTags 是本段要追加 ⚠️ 的风险标签：段级 warnings 在前，镜头 risk_tags 按顺序接上，去重；只认档案里有文案的标签。
// 文案在编译档案 risk 里，只追加本段真有的风险，不堆通用负面词。
func riskTags(seg Segment, cp *CompileProfile) []string {
	tags := slices.Clone(seg.Warnings)
	for _, sh := range seg.Shots {
		tags = append(tags, sh.RiskTags...)
	}
	var out []string
	for _, t := range tags {
		if _, ok := cp.Risk[t]; ok && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// lineText 写一句台词：三元齐全时写即梦四元「音色 + 语速 + 语调 + 情感」，否则沿用 delivery 一句话。
func lineText(ln Line, cp *CompileProfile) string {
	lp := cp.Line
	vo := ""
	if ln.VO {
		vo = lp.VO
	}
	if ln.Pace != "" && ln.Tone != "" && ln.Emotion != "" {
		timbre := ""
		if ln.Timbre != "" {
			timbre = fill(lp.Timbre, "value", ln.Timbre)
		}
		how := joinNonEmpty([]string{vo, timbre, fill(lp.Pace, "value", ln.Pace), fill(lp.Tone, "value", ln.Tone), fill(lp.Emotion, "value", ln.Emotion), ln.Placement}, "；")
		return fill(lp.WithHow, "speaker", ln.Speaker, "how", how, "text", ln.Text)
	}
	how := joinNonEmpty([]string{vo, strings.TrimRight(strings.TrimSpace(ln.Delivery), "。，,.")}, "，")
	if how != "" {
		return fill(lp.WithHow, "speaker", ln.Speaker, "how", how, "text", ln.Text)
	}
	return fill(lp.Plain, "speaker", ln.Speaker, "text", ln.Text)
}

func castDecl(c CastLine, cp *CompileProfile) string {
	return fill(cp.Cast.Decl, "name", c.Name, "lock", strings.Join(c.Lock, "，"))
}

// seedanceRoleTemplate 取一种参考用途的声明模板；不认识的用途按构图参考写。
func seedanceRoleTemplate(sp SeedancePhrases, role string) string {
	switch role {
	case "identity":
		return sp.RoleIdentity
	case "location":
		return sp.RoleLocation
	case "plate":
		return sp.RolePlate
	case "prop":
		return sp.RoleProp
	default:
		return sp.RoleFrame
	}
}

// secText 与 compile.py 的 _sec 一致：四舍五入到 0.1 秒，整数不带小数点。
func secText(x float64) string {
	r := math.RoundToEven(x*10) / 10
	if r == math.Trunc(r) {
		return fmt.Sprintf("%d", int64(r))
	}
	return fmt.Sprintf("%.1f", r)
}

func mmss(sec float64) string {
	return fmt.Sprintf("%02d:%06.3f", int(sec/60), math.Mod(sec, 60))
}

// SubmitDuration 是实际提交的整数秒：按步长向上取整并夹在模型范围内。
func SubmitDuration(dur float64, m Model) int {
	step := m.Duration.Step
	if step <= 0 {
		step = 1
	}
	rounded := math.Round(dur*1000) / 1000
	n := int(math.Ceil(rounded/float64(step))) * step
	return max(m.Duration.Min, min(m.Duration.Max, n))
}

func hasSentenceEnd(text string) bool {
	for _, suffix := range []string{"。", "！", "？", ".", "!", "?"} {
		if strings.HasSuffix(text, suffix) {
			return true
		}
	}
	return false
}

func compileSeedance(seg Segment, m Model, style, acting string, cp *CompileProfile) (string, []CompiledFile, int) {
	sp := cp.Seedance
	notePart := func(note string) string {
		if note == "" {
			return ""
		}
		return fill(sp.NotePrefix, "note", note)
	}
	bridge := seg.ContinuityBridge
	if bridge == nil {
		bridge = &ContinuityBridge{}
	}
	var decl []string
	files := []CompiledFile{}
	switch seg.Mode {
	case ModeLockFirstFrame:
		decl = append(decl, fill(sp.FirstFrame, "ref", "@图片1"))
		files = append(files, CompiledFile{seg.FirstFrame, "@图片1", "first_frame"})
		if seg.LastFrame != "" {
			decl = append(decl, fill(sp.LastFrame, "ref", "@图片2"))
			files = append(files, CompiledFile{seg.LastFrame, "@图片2", "last_frame"})
		}
	case ModeLockIdentity:
		i, j := 0, 0
		for _, c := range seg.Cast {
			decl = append(decl, castDecl(c, cp))
		}
		for _, r := range seg.Refs {
			note := strings.TrimSpace(r.Note)
			if r.State != "" {
				if note != "" {
					note += "；"
				}
				note += fill(sp.RefState, "state", strings.TrimSpace(r.State))
			}
			if r.Role == "motion" {
				j++
				ref := fmt.Sprintf("@视频%d", j)
				decl = append(decl, fill(sp.RoleMotion, "ref", ref, "note", notePart(note)))
				files = append(files, CompiledFile{r.File, ref, "motion"})
				continue
			}
			i++
			ref := fmt.Sprintf("@图片%d", i)
			if r.Role == "identity" {
				who := note
				if who == "" {
					who = sp.IdentityDefault
				}
				decl = append(decl, fill(sp.RoleIdentity, "ref", ref, "note", who))
			} else {
				decl = append(decl, fill(seedanceRoleTemplate(sp, r.Role), "ref", ref, "note", notePart(note)))
			}
			files = append(files, CompiledFile{r.File, ref, r.Role})
		}
		if bridge.BridgeFrame != "" {
			i++
			ref := fmt.Sprintf("@图片%d", i)
			decl = append(decl, fill(sp.RoleFrame, "ref", ref, "note", notePart(sp.BridgeFrameNote)))
			files = append(files, CompiledFile{bridge.BridgeFrame, ref, "frame"})
		}
		if bridge.BridgeClip != "" {
			j++
			ref := fmt.Sprintf("@视频%d", j)
			decl = append(decl, fill(sp.RoleMotion, "ref", ref, "note", notePart(sp.BridgeClipNote)))
			files = append(files, CompiledFile{bridge.BridgeClip, ref, "motion"})
		}
		for k, v := range seg.Voices {
			ref := fmt.Sprintf("@音频%d", k+1)
			decl = append(decl, fill(sp.RoleVoice, "ref", ref, "name", v.Character))
			files = append(files, CompiledFile{v.File, ref, "voice"})
		}
	}
	if seg.Mode != ModeLockIdentity { // 纯文字 / 首帧模式：没有身份参考图的人物用外貌声明兜底
		for _, c := range seg.Cast {
			decl = append(decl, castDecl(c, cp))
		}
	}
	dur := SubmitDuration(seg.Dur, m)
	head := ""
	if len(decl) > 0 {
		head = strings.Join(decl, "；") + "；"
	}
	head += fill(sp.Head, "dur", fmt.Sprintf("%d", dur), "style", style, "acting", acting)
	if text := strings.TrimSpace(bridge.ReanchorText); text != "" {
		head += text
		if !(strings.HasSuffix(text, "。") || strings.HasSuffix(text, "！") || strings.HasSuffix(text, "？")) {
			head += "。"
		}
	}
	if rules := seg.RulesText(); rules != "" {
		head += rules
	}
	lines := []string{head}
	anchor := -1
	if m.ID == "seedance-2.5" && seg.Dur > 15 && len(seg.Shots) > 1 {
		mid := seg.Dur / 2
		best := math.Inf(1)
		for k := 1; k < len(seg.Shots); k++ {
			if d := math.Abs(seg.Shots[k].At - mid); d < best {
				best, anchor = d, k
			}
		}
	}
	for k, sh := range seg.Shots {
		start := sh.At
		end := seg.Dur
		if k+1 < len(seg.Shots) {
			end = seg.Shots[k+1].At
		}
		text := strings.TrimSpace(sh.Text)
		var lead []string
		for _, x := range []string{sh.Size, sh.Camera} {
			if t := strings.TrimSpace(x); t != "" {
				lead = append(lead, t)
			}
		}
		if len(lead) > 0 {
			text = strings.Join(lead, "，") + "。" + text
		}
		if !hasSentenceEnd(text) {
			text += "。"
		}
		for _, ln := range sh.Lines {
			text += lineText(ln, cp)
		}
		if k == anchor {
			text = sp.ReanchorMid + text
		}
		lines = append(lines, fmt.Sprintf("%s-%s秒：%s", secText(start), secText(end), text))
	}
	sound := strings.TrimRight(seg.SoundText("；"), "。")
	if bed := strings.TrimRight(strings.TrimSpace(bridge.SharedBed), "。"); bed != "" {
		parts := []string{fill(sp.SharedBed, "bed", bed)}
		for _, x := range strings.Split(sound, "；") {
			if x = strings.TrimSpace(x); x != "" && x != bed {
				parts = append(parts, x)
			}
		}
		sound = strings.Join(parts, "；")
	}
	if sound == "" {
		sound = sp.SoundDefault
	}
	lines = append(lines, fill(sp.Sound, "sound", sound))
	for _, t := range riskTags(seg, cp) {
		if t == "readable_text" && len(seg.ScreenText) > 0 {
			quoted := make([]string, len(seg.ScreenText))
			for i, x := range seg.ScreenText {
				quoted[i] = "「" + x + "」"
			}
			lines = append(lines, fill(sp.ScreenText, "texts", strings.Join(quoted, "、")))
		} else {
			lines = append(lines, cp.Risk[t])
		}
	}
	if seg.Jimeng != nil && seg.Jimeng.GroupVariation != "" {
		lines = append(lines, fill(sp.GroupVariation, "text", seg.Jimeng.GroupVariation))
	}
	identity, readable := sp.IdentityRefs, sp.TextRefs
	if len(seg.ScreenText) > 0 {
		readable = sp.TextRegistered
	}
	if len(seg.Cast) > 0 {
		identity = sp.IdentityCast
	}
	constraint := fill(sp.Constraint, "identity", identity, "text", readable)
	if lang := strings.TrimSpace(seg.Lang); lang != "" {
		switch strings.ToLower(lang) {
		case "chinese", "中文", "zh":
		default:
			constraint += fill(sp.Lang, "lang", lang)
		}
	}
	lines = append(lines, constraint)
	return strings.Join(lines, "\n"), files, dur
}

func compileH3(seg Segment, m Model, style, acting string, cp *CompileProfile) (string, []CompiledFile, int) {
	hp := cp.H3
	bridge := seg.ContinuityBridge
	if bridge == nil {
		bridge = &ContinuityBridge{}
	}
	files := []CompiledFile{}
	head := style + " " + acting + " "
	if text := strings.TrimSpace(bridge.ReanchorText); text != "" {
		head += text + " "
	}
	if rules := seg.RulesText(); rules != "" {
		head += rules + " "
	}
	var body strings.Builder
	body.WriteString("integrated_multimodal_description:\n")
	var speakers []string
	switch seg.Mode {
	case ModeLockFirstFrame:
		files = append(files, CompiledFile{seg.FirstFrame, "first_frame", "first_frame"})
		head += hp.FirstFrame
		if seg.LastFrame != "" {
			files = append(files, CompiledFile{seg.LastFrame, "last_frame", "last_frame"})
			head += hp.LastFrame
		}
	case ModeLockIdentity:
		var roles []string
		refs := slices.Clone(seg.Refs)
		if bridge.BridgeFrame != "" {
			refs = append(refs, Ref{File: bridge.BridgeFrame, Role: "frame", Note: hp.BridgeFrameNote})
		}
		for i, r := range refs {
			note := r.Note
			if r.State != "" {
				if note != "" {
					note += "; "
				}
				note += fill(hp.RefState, "state", strings.TrimSpace(r.State))
			}
			files = append(files, CompiledFile{r.File, fmt.Sprintf("<Picture %d>", i+1), r.Role})
			tpl, ok := hp.Roles[r.Role]
			if !ok {
				tpl = hp.RoleOther
			}
			roles = append(roles, fmt.Sprintf("<Picture %d> ", i+1)+strings.TrimSpace(fill(tpl, "note", note)))
		}
		for k, v := range seg.Voices {
			speakers = append(speakers, v.Character)
			ref := fmt.Sprintf("<Audio %d>", k+1)
			files = append(files, CompiledFile{v.File, ref, "voice"})
			roles = append(roles, fill(hp.RoleVoice, "ref", ref, "name", v.Character, "speaker", fmt.Sprintf("S%d", k+1)))
		}
		if len(roles) > 0 {
			body.WriteString("Reference roles: " + strings.Join(roles, "; ") + ".")
			if len(speakers) > 0 {
				body.WriteString(hp.DeliveryNote)
			}
			body.WriteString("\n")
		}
	}
	lang := seg.Lang
	if lang == "" {
		lang = cp.Line.Language
	}
	for k, sh := range seg.Shots {
		line := ""
		if k == 0 {
			line = head
		}
		line += strings.TrimSpace(sh.Text)
		for _, ln := range sh.Lines {
			idx := 1
			for i, s := range speakers {
				if s == ln.Speaker {
					idx = i + 1
					break
				}
			}
			vo := ""
			if ln.VO {
				vo = cp.Line.H3VO
			}
			speaker := fmt.Sprintf("S%d", idx)
			how := strings.TrimRight(strings.TrimSpace(ln.Delivery), ".,。，")
			if how != "" {
				line += fill(cp.Line.H3Line, "vo", vo, "speaker", speaker, "how", how, "lang", lang, "text", ln.Text)
			} else {
				line += fill(cp.Line.H3NoHow, "vo", vo, "speaker", speaker, "lang", lang, "text", ln.Text)
			}
		}
		if k == 0 {
			body.WriteString("[Shot 1] " + line + "\n")
		} else {
			body.WriteString(fmt.Sprintf("[Shot %d] At %s, %s\n", k+1, mmss(sh.At), line))
		}
	}
	sound := seg.SoundText("; ")
	if sound == "" {
		sound = hp.SoundDefault
	}
	body.WriteString("\noverall_soundscape: " + sound + "\n\nnon_diegetic_music: N/A")
	var warns []string
	for _, t := range riskTags(seg, cp) {
		warns = append(warns, cp.Risk[t])
	}
	if len(warns) > 0 {
		body.WriteString("\n\nconstraints: " + strings.Join(warns, " "))
	}
	return body.String(), files, SubmitDuration(seg.Dur, m)
}

// Compile 把一段中性分段编译成模型方言。style/acting 为空时用默认块（真人剧）；口味不同时由调用方传入。
func Compile(seg Segment, m Model, style, acting *string) (CompileResult, error) {
	return CompileWithProfile(seg, m, style, acting, nil)
}

// profileBlocks 取档案的风格块和表演块：Seedance 优先中文，MiniMax-H3 优先英文（compile.blocks）。
func profileBlocks(p *Profile, dialect string) (string, string) {
	if dialect == "seedance" {
		return firstNonEmptyString(p.StyleBlock.Zh, p.StyleBlock.En), firstNonEmptyString(p.ActingBlock.Zh, p.ActingBlock.En)
	}
	return firstNonEmptyString(p.StyleBlock.En, p.StyleBlock.Zh), firstNonEmptyString(p.ActingBlock.En, p.ActingBlock.Zh)
}

// CompileWithProfile 同 Compile；给了风格档案时默认块取档案的 style_block / acting_block（2.1 段写了 profile）。
func CompileWithProfile(seg Segment, m Model, style, acting *string, profile *Profile) (CompileResult, error) {
	return CompileWith(seg, m, style, acting, profile, nil)
}

// CompileWith 同 CompileWithProfile，文字取编译档案 phrases（nil 用内置默认）；风格块和表演块档案有覆盖时优先。
func CompileWith(seg Segment, m Model, style, acting *string, profile *Profile, phrases *CompileProfile) (CompileResult, error) {
	cp := mustCompileProfile(phrases)
	defaultStyle, defaultActing, err := cp.blocksFor(profile, m.Dialect())
	if err != nil {
		return CompileResult{}, err
	}
	s, a := defaultStyle, defaultActing
	if style != nil {
		s = *style
	}
	if acting != nil {
		a = *acting
	}
	issues := Issues(seg, m)
	var prompt string
	var files []CompiledFile
	var dur int
	if m.Dialect() == "seedance" {
		prompt, files, dur = compileSeedance(seg, m, s, a, cp)
	} else {
		prompt, files, dur = compileH3(seg, m, s, a, cp)
	}
	chars := len([]rune(prompt))
	limit := m.PromptMaxChars
	if m.PromptMaxCharsMeasured != nil {
		limit = m.PromptMaxCharsMeasured
	}
	if limit == nil && chars > softPromptBudget {
		issues = append(issues, Issue{"warn", fmt.Sprintf("%s 提示词 %d 字符，超过软预算 %d（%s 上限未实测，先按软预算控长）", seg.ID, chars, softPromptBudget, m.Label)})
	}
	if limit != nil && chars > *limit {
		issues = append(issues, Issue{"error", fmt.Sprintf("%s 提示词 %d 字符，超过 %s 的上限 %d：先压缩风格词和重复场景词，不删台词和关键动作", seg.ID, chars, m.Label, *limit)})
	}
	if seg.Dur != float64(dur) {
		tooLong := false
		for _, i := range issues {
			if i.Level == "error" && strings.Contains(i.Message, "超过") && strings.Contains(i.Message, "时长") {
				tooLong = true
			}
		}
		if !tooLong {
			step := m.Duration.Step
			if step <= 0 {
				step = 1
			}
			issues = append(issues, Issue{"warn", fmt.Sprintf("%s 按 %d 秒提交（模型时长步长为 %d 秒）", seg.ID, dur, step)})
		}
	}
	if issues == nil {
		issues = []Issue{}
	}
	warnings := []string{}
	for _, t := range riskTags(seg, cp) {
		warnings = append(warnings, cp.Risk[t])
	}
	result := CompileResult{Segment: seg.ID, Model: m.ID, Duration: dur, Prompt: prompt, Files: files, Chars: chars, Warnings: warnings, Issues: issues}
	result.Paste = prompt
	if m.Dialect() == "seedance" {
		result.Paste = PasteVersion(result)
	}
	return result, nil
}

// PasteVersion 是即梦网页粘贴版：提示词 + 上传清单（哪个文件对应哪个编号）+ 时长。
func PasteVersion(r CompileResult) string {
	ups := make([]string, 0, len(r.Files))
	for _, f := range r.Files {
		ups = append(ups, fmt.Sprintf("%s ← %s（%s）", f.Tag, f.File, f.Role))
	}
	out := r.Prompt
	if len(ups) > 0 {
		out += "\n\n【上传素材，按编号顺序】" + strings.Join(ups, "；")
	}
	return out + fmt.Sprintf("\n【时长】%d 秒", r.Duration)
}
