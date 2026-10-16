package drama

import (
	"fmt"
	"math"
	"strings"
)

// CompiledFile 是编译结果里的一个参考素材：文件、它在提示词里的编号和用途。
type CompiledFile struct {
	File string `json:"file"`
	Tag  string `json:"tag"`
	Role string `json:"role"`
}

// CompileResult 是一段在某个模型上的编译结果。
type CompileResult struct {
	Segment  string         `json:"segment"`
	Model    string         `json:"model"`
	Duration int            `json:"duration"`
	Prompt   string         `json:"prompt"`
	Files    []CompiledFile `json:"files"`
	Chars    int            `json:"chars"`
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
)

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
		for _, r := range seg.Refs {
			note := strings.TrimSpace(r.Note)
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
		for k, v := range seg.Voices {
			decl = append(decl, fmt.Sprintf("@音频%d为%s的音色参考（只取音色，不复用其中的话）", k+1, v.Character))
			files = append(files, CompiledFile{v.File, fmt.Sprintf("@音频%d", k+1), "voice"})
		}
	}
	dur := SubmitDuration(seg.Dur, m)
	head := ""
	if len(decl) > 0 {
		head = strings.Join(decl, "；") + "；"
	}
	head += fmt.Sprintf("生成%d秒视频。整体风格：%s%s", dur, style, acting)
	if seg.Rules != "" {
		head += strings.TrimSpace(seg.Rules)
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
			var how []string
			if ln.VO {
				how = append(how, "画外音，嘴不动")
			}
			if d := strings.TrimRight(strings.TrimSpace(ln.Delivery), "。，,."); d != "" {
				how = append(how, d)
			}
			if len(how) > 0 {
				text += fmt.Sprintf("%s（%s）：“%s”", ln.Speaker, strings.Join(how, "，"), ln.Text)
			} else {
				text += fmt.Sprintf("%s：“%s”", ln.Speaker, ln.Text)
			}
		}
		if k == anchor {
			text = reanchor + text
		}
		lines = append(lines, fmt.Sprintf("%s-%s秒：%s", secText(start), secText(end), text))
	}
	sound := strings.TrimRight(strings.TrimSpace(seg.Sound), "。")
	if sound == "" {
		sound = "环境声与动作音效"
	}
	lines = append(lines, "声音："+sound+"；不要背景音乐和配乐。")
	constraint := seedanceConstraint
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
	files := []CompiledFile{}
	head := style + " " + acting + " "
	if seg.Rules != "" {
		head += strings.TrimSpace(seg.Rules) + " "
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
		for i, r := range seg.Refs {
			files = append(files, CompiledFile{r.File, fmt.Sprintf("<Picture %d>", i+1), r.Role})
			tpl, ok := h3Role[r.Role]
			if !ok {
				tpl = "%s"
			}
			roles = append(roles, fmt.Sprintf("<Picture %d> ", i+1)+strings.TrimSpace(fmt.Sprintf(tpl, r.Note)))
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
	sound := strings.TrimSpace(seg.Sound)
	if sound == "" {
		sound = "quiet room tone"
	}
	body.WriteString("\noverall_soundscape: " + sound + "\n\nnon_diegetic_music: N/A")
	return body.String(), files, SubmitDuration(seg.Dur, m)
}

// Compile 把一段中性分段编译成模型方言。style/acting 为空时用默认块（真人剧）；口味不同时由调用方传入。
func Compile(seg Segment, m Model, style, acting *string) (CompileResult, error) {
	defaultStyle, defaultActing, err := Blocks(m.Dialect())
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
		prompt, files, dur = compileSeedance(seg, m, s, a)
	} else {
		prompt, files, dur = compileH3(seg, m, s, a)
	}
	chars := len([]rune(prompt))
	if m.PromptMaxChars != nil && chars > *m.PromptMaxChars {
		issues = append(issues, Issue{"error", fmt.Sprintf("%s 提示词 %d 字符，超过 %s 的上限 %d：先压缩风格词和重复场景词，不删台词和关键动作", seg.ID, chars, m.Label, *m.PromptMaxChars)})
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
	return CompileResult{Segment: seg.ID, Model: m.ID, Duration: dur, Prompt: prompt, Files: files, Chars: chars, Issues: issues}, nil
}
