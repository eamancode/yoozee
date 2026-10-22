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

var seedanceRole = map[string]string{
	"identity": "@图片%d为人物参考（%s；脸、发型、小特征和服装照此，忽略灰色背景）",
	"location": "@图片%d为场景参考（布局、材质、颜色和光线照此，图中无人）%s",
	"plate":    "@图片%d为这一机位身后的背景%s",
	"prop":     "@图片%d为道具参考%s",
	"frame":    "@图片%d只作构图参考（取景、光线、位置），人物的脸以人物参考为准%s",
}

var h3Role = map[string]string{
	"identity": "is the exact identity reference of %s: match the face, hairstyle and distinguishing marks exactly and wear exactly what the reference wears; ignore its plain grey background",
	"location": "is the location reference: keep its layout, materials, colours and light; it contains no people. %s",
	"plate":    "is the background for one camera side: %s",
	"prop":     "is the exact prop: %s",
	"frame":    "is a composition reference only (framing, light, seating); the faces must follow the identity references, not this picture. %s",
}

const (
	seedanceConstraint = "约束：人物与参考一致、场景连续、光源方向不变、口型与台词同步、无字幕、无水印、除参考里已有的字外不出现可读文字。"
	reanchor           = "（再锚点：人物形象、服装与光源方向与开头一致）"
	bridgeFrameNote    = "上一段的末帧：构图、人物位置与此刻状态照此，人脸以人物参考为准"
	bridgeClipNote     = "上一段末尾的运镜与姿态连续参考，不复刻其中的人物与场景"
	// softPromptBudget 是模型字数上限未写也未实测时的软预算（只警告）。
	softPromptBudget = 4000
)

// riskWarnings 把 risk_tags 写成 ⚠️ 文案：只追加本段真有的风险，不堆通用负面词。顺序即 compile.py 的 RISK_WARNINGS。
var riskWarnings = map[string]string{
	"limb_distortion":        "⚠️肢体结构正确：五指清晰、关节不畸形、身体不僵硬、不扭曲。",
	"model_clip":             "⚠️武器、头发、身体不穿模，膝盖不穿透地面，身体不互相穿透。",
	"physics":                "⚠️动作符合物理：脚踏实地、不同手同脚、重心真实、步伐不僵硬。",
	"bg_extras":              "⚠️背景不出现无关人物，背景虚化适度。",
	"proportion":             "⚠️两人身体比例与服装风格一致。",
	"handle_contamination":   "⚠️各参考只按声明的用途使用：人物参考只取人物，场景参考只取场景，不把人物参考里的背景或场景参考里的任何人形带进画面。",
	"identity_drift":         "⚠️人物面部、发型、小特征与服装全程与人物参考一致，不随景别和光线变化。",
	"light_spill":            "⚠️屏幕、灯具只作为画面里发光的物件，不向人物皮肤与服装投射大面积色光。",
	"prop_misplace":          "⚠️道具位置与状态按描述固定，不凭空出现或消失，不复制成多件。",
	"insert_focus_drift":     "⚠️插入镜头的焦点落在道具与手上，不漂到背景。",
	"closeup_rotation":       "⚠️近景时机位不做大幅环绕或旋转，运动幅度小、匀速。",
	"three_axis":             "⚠️每个时间段只做一到两种运镜，不同时推拉、升降、环绕。",
	"phantom_across_barrier": "⚠️河流、墙、门、车辆等阻隔物的另一侧没有人，除非描述里写了。",
	"readable_text":          "⚠️除参考里已有的字外不出现任何可读文字、字幕、水印。",
	"group_same_face":        "⚠️群像人物面孔各不相同，按描述的差异化特征区分，不复制同一张脸。",
	"big_action":             "⚠️大动作（走路、起身、转身）完整、连贯、重心真实，不穿模、不多出肢体。",
	"long_take_drift":        "⚠️段落较长：人物形象、服装、光源方向在后半段与开头完全一致。",
}

// riskTags 是本段要追加 ⚠️ 的风险标签：段级 warnings 在前，镜头 risk_tags 按顺序接上，去重。
func riskTags(seg Segment) []string {
	tags := slices.Clone(seg.Warnings)
	for _, sh := range seg.Shots {
		tags = append(tags, sh.RiskTags...)
	}
	var out []string
	for _, t := range tags {
		if _, ok := riskWarnings[t]; ok && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// lineText 写一句台词：三元齐全时写即梦四元「音色 + 语速 + 语调 + 情感」，否则沿用 delivery 一句话。
func lineText(ln Line) string {
	vo := ""
	if ln.VO {
		vo = "画外音，嘴不动"
	}
	if ln.Pace != "" && ln.Tone != "" && ln.Emotion != "" {
		timbre := ""
		if ln.Timbre != "" {
			timbre = "音色：" + ln.Timbre
		}
		how := joinNonEmpty([]string{vo, timbre, "语速：" + ln.Pace, "语调：" + ln.Tone, "情感：" + ln.Emotion, ln.Placement}, "；")
		return fmt.Sprintf("%s（%s）：“%s”", ln.Speaker, how, ln.Text)
	}
	how := joinNonEmpty([]string{vo, strings.TrimRight(strings.TrimSpace(ln.Delivery), "。，,.")}, "，")
	if how != "" {
		return fmt.Sprintf("%s（%s）：“%s”", ln.Speaker, how, ln.Text)
	}
	return fmt.Sprintf("%s：“%s”", ln.Speaker, ln.Text)
}

func castDecl(c CastLine) string { return "人物·" + c.Name + "：" + strings.Join(c.Lock, "，") }

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

func compileSeedance(seg Segment, m Model, style, acting string) (string, []CompiledFile, int) {
	bridge := seg.ContinuityBridge
	if bridge == nil {
		bridge = &ContinuityBridge{}
	}
	var decl []string
	files := []CompiledFile{}
	switch seg.Mode {
	case ModeLockFirstFrame:
		decl = append(decl, "以@图片1为首帧（构图、人物、服装和光线照此）")
		files = append(files, CompiledFile{seg.FirstFrame, "@图片1", "first_frame"})
		if seg.LastFrame != "" {
			decl = append(decl, "以@图片2为尾帧，视频结束在这一画面")
			files = append(files, CompiledFile{seg.LastFrame, "@图片2", "last_frame"})
		}
	case ModeLockIdentity:
		i, j := 0, 0
		for _, c := range seg.Cast {
			decl = append(decl, castDecl(c))
		}
		for _, r := range seg.Refs {
			note := strings.TrimSpace(r.Note)
			if r.State != "" {
				if note != "" {
					note += "；"
				}
				note += "此刻" + strings.TrimSpace(r.State)
			}
			if r.Role == "motion" {
				j++
				text := fmt.Sprintf("@视频%d为运镜参考", j)
				if note != "" {
					text += "：" + note
				}
				decl = append(decl, text)
				files = append(files, CompiledFile{r.File, fmt.Sprintf("@视频%d", j), "motion"})
				continue
			}
			i++
			tpl, ok := seedanceRole[r.Role]
			if !ok {
				tpl = seedanceRole["frame"]
			}
			if r.Role == "identity" {
				who := note
				if who == "" {
					who = "该人物"
				}
				decl = append(decl, fmt.Sprintf(tpl, i, who))
			} else {
				tail := ""
				if note != "" {
					tail = "：" + note
				}
				decl = append(decl, fmt.Sprintf(tpl, i, tail))
			}
			files = append(files, CompiledFile{r.File, fmt.Sprintf("@图片%d", i), r.Role})
		}
		if bridge.BridgeFrame != "" {
			i++
			decl = append(decl, fmt.Sprintf(seedanceRole["frame"], i, "："+bridgeFrameNote))
			files = append(files, CompiledFile{bridge.BridgeFrame, fmt.Sprintf("@图片%d", i), "frame"})
		}
		if bridge.BridgeClip != "" {
			j++
			decl = append(decl, fmt.Sprintf("@视频%d为运镜参考：%s", j, bridgeClipNote))
			files = append(files, CompiledFile{bridge.BridgeClip, fmt.Sprintf("@视频%d", j), "motion"})
		}
		for k, v := range seg.Voices {
			decl = append(decl, fmt.Sprintf("@音频%d为%s的音色参考（只取音色，不复用其中的话）", k+1, v.Character))
			files = append(files, CompiledFile{v.File, fmt.Sprintf("@音频%d", k+1), "voice"})
		}
	}
	if seg.Mode != ModeLockIdentity { // 纯文字 / 首帧模式：没有身份参考图的人物用外貌声明兜底
		for _, c := range seg.Cast {
			decl = append(decl, castDecl(c))
		}
	}
	dur := SubmitDuration(seg.Dur, m)
	head := ""
	if len(decl) > 0 {
		head = strings.Join(decl, "；") + "；"
	}
	head += fmt.Sprintf("生成%d秒视频。整体风格：%s%s", dur, style, acting)
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
			text += lineText(ln)
		}
		if k == anchor {
			text = reanchor + text
		}
		lines = append(lines, fmt.Sprintf("%s-%s秒：%s", secText(start), secText(end), text))
	}
	sound := strings.TrimRight(seg.SoundText("；"), "。")
	if bed := strings.TrimRight(strings.TrimSpace(bridge.SharedBed), "。"); bed != "" {
		parts := []string{bed + "（与上一段相同、不中断）"}
		for _, x := range strings.Split(sound, "；") {
			if x = strings.TrimSpace(x); x != "" && x != bed {
				parts = append(parts, x)
			}
		}
		sound = strings.Join(parts, "；")
	}
	if sound == "" {
		sound = "环境声与动作音效"
	}
	lines = append(lines, "声音："+sound+"；不要背景音乐和配乐。")
	for _, t := range riskTags(seg) {
		if t == "readable_text" && len(seg.ScreenText) > 0 {
			quoted := make([]string, len(seg.ScreenText))
			for i, x := range seg.ScreenText {
				quoted[i] = "「" + x + "」"
			}
			lines = append(lines, "⚠️画面里只出现这些登记过的文字："+strings.Join(quoted, "、")+"，写法要正确；其他地方不出现任何可读文字、字幕、水印。")
		} else {
			lines = append(lines, riskWarnings[t])
		}
	}
	if seg.Jimeng != nil && seg.Jimeng.GroupVariation != "" {
		lines = append(lines, "⚠️群像差异化："+seg.Jimeng.GroupVariation+"；禁止同脸。")
	}
	constraint := seedanceConstraint
	if len(seg.ScreenText) > 0 {
		constraint = strings.Replace(constraint, "除参考里已有的字外不出现可读文字", "除登记的文字外不出现可读文字", 1)
	}
	if len(seg.Cast) > 0 {
		constraint = strings.Replace(constraint, "人物与参考一致", "人物外貌全程与开头的人物声明一致", 1)
	}
	if lang := strings.TrimSpace(seg.Lang); lang != "" {
		switch strings.ToLower(lang) {
		case "chinese", "中文", "zh":
		default:
			constraint += "台词用" + lang + "说。"
		}
	}
	lines = append(lines, constraint)
	return strings.Join(lines, "\n"), files, dur
}

func compileH3(seg Segment, m Model, style, acting string) (string, []CompiledFile, int) {
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
		head += "Keep the exact framing, faces, wardrobe and lighting of the first frame. "
		if seg.LastFrame != "" {
			files = append(files, CompiledFile{seg.LastFrame, "last_frame", "last_frame"})
			head += "The video must end exactly on the last frame. "
		}
	case ModeLockIdentity:
		var roles []string
		refs := slices.Clone(seg.Refs)
		if bridge.BridgeFrame != "" {
			refs = append(refs, Ref{File: bridge.BridgeFrame, Role: "frame", Note: "last frame of the previous segment: keep framing, positions and the current state; faces follow the identity references"})
		}
		for i, r := range refs {
			note := r.Note
			if r.State != "" {
				if note != "" {
					note += "; "
				}
				note += "current state: " + strings.TrimSpace(r.State)
			}
			files = append(files, CompiledFile{r.File, fmt.Sprintf("<Picture %d>", i+1), r.Role})
			tpl, ok := h3Role[r.Role]
			if !ok {
				tpl = "%s"
			}
			roles = append(roles, fmt.Sprintf("<Picture %d> ", i+1)+strings.TrimSpace(fmt.Sprintf(tpl, note)))
		}
		for k, v := range seg.Voices {
			speakers = append(speakers, v.Character)
			files = append(files, CompiledFile{v.File, fmt.Sprintf("<Audio %d>", k+1), "voice"})
			roles = append(roles, fmt.Sprintf("<Audio %d> gives only the sound of the voice (timbre) of %s (S%d)", k+1, v.Character, k+1))
		}
		if len(roles) > 0 {
			body.WriteString("Reference roles: " + strings.Join(roles, "; ") + ".")
			if len(speakers) > 0 {
				body.WriteString(" How each line is delivered (volume, pitch, emotion, pace, breath) comes only from the acting described in the scene, never from the audio references; do not reuse any words from them.")
			}
			body.WriteString("\n")
		}
	}
	lang := seg.Lang
	if lang == "" {
		lang = "Chinese"
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
				vo = " [off-screen voiceover]"
			}
			how := strings.TrimRight(strings.TrimSpace(ln.Delivery), ".,。，")
			if how != "" {
				line += fmt.Sprintf("%s (S%d), %s:\n<d>[%s] %s</d>", vo, idx, how, lang, ln.Text)
			} else {
				line += fmt.Sprintf("%s (S%d):\n<d>[%s] %s</d>", vo, idx, lang, ln.Text)
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
		sound = "quiet room tone"
	}
	body.WriteString("\noverall_soundscape: " + sound + "\n\nnon_diegetic_music: N/A")
	var warns []string
	for _, t := range riskTags(seg) {
		warns = append(warns, riskWarnings[t])
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
	var defaultStyle, defaultActing string
	if profile != nil {
		defaultStyle, defaultActing = profileBlocks(profile, m.Dialect())
	} else {
		var err error
		if defaultStyle, defaultActing, err = Blocks(m.Dialect()); err != nil {
			return CompileResult{}, err
		}
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
		prompt, files, dur = compileSeedance(seg, m, s, a)
	} else {
		prompt, files, dur = compileH3(seg, m, s, a)
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
	for _, t := range riskTags(seg) {
		warnings = append(warnings, riskWarnings[t])
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
