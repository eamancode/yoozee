package drama

import (
	"regexp"
	"strings"
)

// 文戏 15 转换器：7.3 的 Markdown 片段（卡 0 文档 §3c / §3d）→ 分镜行。
//
//	## 片段 X（时长：XX秒）
//	**要素提取** [角色：…；场景：…；道具：…；BGM背景音乐：…；摄影基调：…]；Audio：SFX：…
//	**当前空间站位** [...]
//	**衔接参考首帧**：…           （同场后续片段）
//	**参考首帧（0s-0.5s）真人写实脸**：…
//	S1，0.5s-3.0s，景别、运镜、机位。画面动作。风格：X。核心情绪：Y。角色名（台词）：“原文”
//	**最后一镜结束站位** [XXs-XXs，…]   （或「12s-13s，全景，**最后一镜结束站位** […]」）
//	**下一片段继承点** [...]
//
// 一节按 7.3 口径就是一个片段；模型多写了片段（按台词把一节拆成两段）时按顺序都转，镜头都算这一节的。
// 片段级的角色、场景、道具、站位、摄影基调、SFX 下沉到每一镜；BGM 只留在原样输出里。

var (
	wenxi15Header   = regexp.MustCompile(`^#{1,4}\s*片段\s*(\d+)\s*[（(]\s*时长[：:]\s*([\d.]+)\s*秒?\s*[）)]`)
	wenxi15Field    = regexp.MustCompile(`\*\*([^*]+)\*\*\s*[：:]?\s*(.*)$`)
	wenxi15Shot     = regexp.MustCompile(`^[-*\s]*\**S(\d+)\**\s*[，,、]\s*([\d.]+)\s*s?\s*[-–—~～至]\s*([\d.]+)\s*s\s*[，,、]\s*(.*)$`)
	wenxi15EndTime  = regexp.MustCompile(`([\d.]+)\s*s?\s*[-–—~～至]\s*([\d.]+)\s*s`)
	wenxi15Speech   = regexp.MustCompile(`([^\s，。；：:“”"（）()、]{1,16})(?:[（(]([^）)]{1,16})[）)])?\s*[：:]\s*[“"]([^”"]+)[”"]`)
	wenxi15Tag      = regexp.MustCompile(`(风格|核心情绪)[：:]\s*([^。]+)。?`)
	wenxi15Elements = regexp.MustCompile(`(角色|场景|道具|BGM背景音乐|BGM|摄影基调)[：:]\s*([^；;\]]*)`)
	wenxi15SFX      = regexp.MustCompile(`SFX[：:]\s*(.+)$`)
	wenxi15NoLines  = regexp.MustCompile(`(台词|旁白)(/旁白)?[：:]\s*无。?`)
)

// voiceSpeakers 是声音身份标签里括号写的是身份、外面写的是声音类别的写法（旁白（身份）、广播（身份）、电话声（角色名））。
var voiceSpeakers = map[string]bool{"旁白": true, "广播": true, "电话声": true, "系统音": true}

type wenxi15Block struct {
	dur      float64
	fields   []SectionField
	shots    []string
	endTime  [2]float64
	rejected int
}

func parseWenxi15Blocks(raw string) []wenxi15Block {
	var blocks []wenxi15Block
	var cur *wenxi15Block
	for _, line := range lineBreakPattern.Split(raw, -1) {
		line = strings.TrimSpace(line)
		if line == "" || line == "---" {
			continue
		}
		if m := wenxi15Header.FindStringSubmatch(line); m != nil {
			blocks = append(blocks, wenxi15Block{dur: parseSectionNumber(m[2])})
			cur = &blocks[len(blocks)-1]
			continue
		}
		if cur == nil {
			continue
		}
		if wenxi15Shot.MatchString(line) {
			cur.shots = append(cur.shots, line)
			continue
		}
		if m := wenxi15Field.FindStringSubmatch(line); m != nil {
			name := strings.Join(strings.Fields(m[1]), "")
			value := strings.TrimSpace(m[2])
			if strings.Contains(name, "最后一镜结束站位") {
				if t := wenxi15EndTime.FindStringSubmatch(line); t != nil {
					cur.endTime = [2]float64{parseSectionNumber(t[1]), parseSectionNumber(t[2])}
				}
			}
			cur.fields = append(cur.fields, SectionField{Name: name, Value: value})
			continue
		}
		if strings.HasPrefix(line, "S") && len(line) > 1 && line[1] >= '0' && line[1] <= '9' {
			cur.rejected++
		}
	}
	return blocks
}

func (b wenxi15Block) field(prefix string) string {
	for _, f := range b.fields {
		if strings.HasPrefix(f.Name, prefix) {
			return strings.Trim(f.Value, "[]【】 ")
		}
	}
	return ""
}

func convertWenxi15(raw string, ctx SectionContext) SectionShots {
	out := SectionShots{}
	allNames := append(append(append([]string{}, ctx.Characters...), ctx.Places...), ctx.Props...)
	var shots []sectionShot
	for _, b := range parseWenxi15Blocks(raw) {
		out.Stats.Blocks++
		out.Stats.Rejected += b.rejected
		out.Fields = append(out.Fields, b.fields...)
		elements := map[string]string{}
		var sfx string
		if el := b.field("要素提取"); el != "" {
			for _, m := range wenxi15Elements.FindAllStringSubmatch(el, -1) {
				elements[m[1]] = strings.TrimSpace(m[2])
			}
			if m := wenxi15SFX.FindStringSubmatch(el); m != nil {
				sfx = strings.TrimSpace(m[1])
			}
		}
		if _, ok := elements["BGM背景音乐"]; ok {
			out.Stats.MusicStripped++
		}
		var characters []string
		for _, n := range sectionNameSep.Split(elements["角色"], -1) {
			if n = resolveSectionName(n, ctx.Characters); n != "" && !containsString(characters, n) {
				characters = append(characters, n)
			}
		}
		var blockNames []string
		for _, key := range []string{"场景", "道具"} {
			for _, n := range sectionNameSep.Split(elements[key], -1) {
				if n = resolveSectionName(n, allNames); n != "" && !containsString(blockNames, n) {
					blockNames = append(blockNames, n)
				}
			}
		}
		station := b.field("当前空间站位")
		lightClause := firstClauseWith(station, "主光", "光源", "光从", "照")
		grading := elements["摄影基调"]
		start := firstNonBlank(b.field("衔接参考首帧"), station)
		end := b.field("最后一镜结束站位")
		sfx, stripped := stripMusic(sfx)
		out.Stats.MusicStripped += stripped

		var starts, ends []float64
		first := len(shots)
		for _, line := range b.shots {
			m := wenxi15Shot.FindStringSubmatch(line)
			sh, n := wenxi15ShotOf(m[4], characters, station, ctx)
			out.Stats.MusicStripped += n
			if sh.spec.Size == "" {
				out.Stats.NoSize++
			}
			sh.names = append(append([]string{}, characters...), blockNames...)
			light := &Light{Key: grading}
			if ctx.Indoor || lightClause != "" {
				light.SourceAnchor = sectionLightAnchor(lightClause, ctx.Anchors)
			}
			if light.Key != "" || light.SourceAnchor != "" {
				sh.spec.Light = light
			}
			starts = append(starts, parseSectionNumber(m[2]))
			ends = append(ends, parseSectionNumber(m[3]))
			shots = append(shots, sh)
		}
		if len(shots) == first {
			continue
		}
		total := b.dur
		if b.endTime[1] > total {
			total = b.endTime[1]
		}
		for i, est := range sectionEstimates(starts, ends, total) {
			shots[first+i].spec.Est = flexFloat(est)
		}
		if sfx != "" {
			shots[first].spec.Sfx = sfx
		}
		shots[first].spec.StartState = start
		shots[len(shots)-1].spec.EndState = end
	}
	out.Rows = finishSectionShots(shots, ctx, &out.Stats)
	return out
}

// wenxi15ShotOf 读一行镜头（时间码之后的部分）：第一句是镜头语言，之后是画面动作、风格 / 核心情绪标签和台词。
func wenxi15ShotOf(body string, characters []string, station string, ctx SectionContext) (sectionShot, int) {
	var sh sectionShot
	spec := &sh.spec
	for _, m := range wenxi15Speech.FindAllStringSubmatch(body, -1) {
		speaker, tag, text := strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), strings.TrimSpace(m[3])
		if voiceSpeakers[speaker] {
			if speaker == "电话声" && tag != "" {
				speaker = tag
			}
			tag = firstNonBlank(tag, speaker) + "·" + m[1]
		}
		ln := sectionLine{speaker: resolveSectionName(speaker, ctx.Characters), text: text, voice: tag, offscreen: offscreenVoice(tag) || voiceSpeakers[m[1]]}
		if ln.offscreen {
			ln.placement = "画外"
		}
		sh.lines = append(sh.lines, ln)
	}
	rest := wenxi15Speech.ReplaceAllString(body, "")
	rest = wenxi15NoLines.ReplaceAllString(rest, "")
	extras := map[string]string{}
	for _, m := range wenxi15Tag.FindAllStringSubmatch(rest, -1) {
		extras[m[1]] = strings.TrimSpace(m[2])
	}
	rest = wenxi15Tag.ReplaceAllString(rest, "")
	if len(extras) > 0 {
		spec.Extras = extras
	}
	camera, action, _ := strings.Cut(rest, "。")
	if !containsAny(camera, sectionSizeWords...) && sectionMoves(camera) == nil {
		// 第一句不是镜头语言（只写了画面）：整句当动作。
		camera, action = "", rest
	}
	size, to := sectionSize(camera)
	spec.Size = size
	spec.Move = sectionMoves(camera)
	if to != "" && len(spec.Move) == 0 {
		if sectionCloseSizes[to] > sectionCloseSizes[size] {
			spec.Move = flexStrings{"pull"}
		} else {
			spec.Move = flexStrings{"push"}
		}
	}
	spec.Cam = sectionCam(camera, size)
	spec.Subject = "person"
	if size != "" && strings.Index(camera, size) > 0 && !containsAny(camera[:strings.Index(camera, size)], characters...) {
		// 「茶盏特写」：景别前写的是物件。
		spec.Subject = "non_person"
	}
	action = strings.TrimSpace(strings.Trim(action, "。 "))
	action, n := stripMusic(action)
	if action != "" && !hasSentenceEnd(action) {
		action += "。"
	}
	spec.Action = action
	who := mentionedNames(body, characters)
	if len(who) == 0 && spec.Subject == "person" {
		who = characters
	}
	for _, name := range who {
		clause := firstClauseWith(station, strings.SplitN(name, "·", 2)[0])
		spec.Blocking = append(spec.Blocking, Blocking{Who: name, At: sectionAnchorIn(clause, ctx.Anchors)})
	}
	return sh, n
}
