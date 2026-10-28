package drama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// 风格锁定、场次色卡输出的逐字段读法：模型偶尔把字段写成别的 JSON 类型（references 写成字符串数组、hex 写成 ["#1A1518"]、
// usage 写成对象），整行 json.Unmarshal 会因为一个字段把整场丢掉。这里逐字段读，类型不符时只降级这一个字段——
// 单值字段（hex、名字、场次头、inherits_from）取第一个元素，自由文本字段把元素用「；」拼起来，对象转成「键：值」——
// 并记一条说明（Coerced），检查时报 warn（R-STYLE-012）。references 写成字符串数组是契约允许的写法，拼接时不记。

// styleTextSep 是自由文本字段里多项之间的分隔符。
const styleTextSep = "；"

// styleAnchorKeys 是风格锚的字段；一行里有两个以上、又不像一场的，当成没包 style_anchor 的风格锚。
var styleAnchorKeys = []string{"medium", "art_style", "lighting", "camera", "grading", "forbidden", "era", "genre", "render_quality"}

var styleListSplit = regexp.MustCompile(`[；;\n]+`)

// parseJSONObjectLine 读一行 JSON 对象；读不出时补齐结尾缺的括号（或去掉多出来的结尾括号）再读一次，repaired 表示补过。
func parseJSONObjectLine(line string) (obj map[string]json.RawMessage, repaired bool, ok bool) {
	if json.Unmarshal([]byte(line), &obj) == nil && obj != nil {
		return obj, false, true
	}
	if fixed, changed := balanceJSONLine(line); changed && json.Unmarshal([]byte(fixed), &obj) == nil && obj != nil {
		return obj, true, true
	}
	// 结尾多了括号：第一个值读得出、后面只剩括号和空白。
	dec := json.NewDecoder(strings.NewReader(line))
	var first map[string]json.RawMessage
	if dec.Decode(&first) == nil && first != nil {
		rest := strings.TrimFunc(line[dec.InputOffset():], func(r rune) bool { return r == '}' || r == ']' || r == ',' || jsSpace(r) })
		if rest == "" {
			return first, true, true
		}
	}
	return nil, false, false
}

// balanceJSONLine 按引号外的括号栈补齐结尾缺的 } 和 ]；括号交错、字符串没收尾时不补。
func balanceJSONLine(line string) (string, bool) {
	var stack []byte
	inString, escaped := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != c {
				return line, false
			}
			stack = stack[:len(stack)-1]
		}
	}
	if inString || len(stack) == 0 {
		return line, false
	}
	slices.Reverse(stack)
	return line + string(stack), true
}

// jsonKind 是一个 JSON 值的类型名（写进说明里）。
func jsonKind(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "空"
	}
	switch raw[0] {
	case '"':
		return "字符串"
	case '[':
		return "数组"
	case '{':
		return "对象"
	case 'n':
		return "null"
	case 't', 'f':
		return "布尔值"
	}
	return "数字"
}

// jsonTextParts 把一个 JSON 值摊成文字：字符串原样、数字和布尔值写字面量、数组逐项、对象逐个「键：值」（保持原顺序）；
// null 和空字符串不出项。
func jsonTextParts(raw json.RawMessage) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil || jsTrim(s) == "" {
			return nil
		}
		return []string{jsTrim(s)}
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return nil
		}
		var out []string
		for _, item := range items {
			out = append(out, jsonTextParts(item)...)
		}
		return out
	case '{':
		dec := json.NewDecoder(bytes.NewReader(raw))
		if _, err := dec.Token(); err != nil {
			return nil
		}
		var out []string
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return out
			}
			var value json.RawMessage
			if dec.Decode(&value) != nil {
				return out
			}
			if text := strings.Join(jsonTextParts(value), styleTextSep); text != "" {
				out = append(out, fmt.Sprint(key)+"："+text)
			}
		}
		return out
	case 'n':
		return nil
	}
	return []string{string(raw)}
}

// styleFieldReader 逐字段读一个 JSON 对象，类型不符时记说明；label 是说明的前缀（如「风格锚」「「冷白荧光」的」）。
type styleFieldReader struct {
	obj   map[string]json.RawMessage
	label string
	notes []string
}

func (r *styleFieldReader) note(format string, args ...any) {
	r.notes = append(r.notes, r.label+fmt.Sprintf(format, args...))
}

// text 读字符串字段。join 为 true 是自由文本：数组用「；」拼起来（joinQuiet 时不记说明）；为 false 是单值：数组取第一个元素。
func (r *styleFieldReader) text(key string, join, joinQuiet bool) string {
	raw, ok := r.obj[key]
	if !ok {
		return ""
	}
	kind := jsonKind(raw)
	parts := jsonTextParts(raw)
	switch kind {
	case "字符串", "null":
		return strings.Join(parts, "")
	case "数组":
		if !join {
			if len(parts) == 0 {
				r.note("%s 写成了空数组", key)
				return ""
			}
			r.note("%s 写成了数组，取第一个「%s」", key, parts[0])
			return parts[0]
		}
		if !joinQuiet {
			r.note("%s 写成了数组，用「%s」拼成一句", key, styleTextSep)
		}
		return strings.Join(parts, styleTextSep)
	case "对象":
		r.note("%s 写成了对象，转成文字", key)
		return strings.Join(parts, styleTextSep)
	}
	r.note("%s 写成了%s「%s」，按文字读", key, kind, strings.Join(parts, ""))
	return strings.Join(parts, "")
}

// integer 读整数字段：数字取整，数字写成字符串也认；读不出记说明、返回 0。
func (r *styleFieldReader) integer(key string) int {
	raw, ok := r.obj[key]
	if !ok {
		return 0
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	switch x := v.(type) {
	case float64:
		return int(x)
	case nil:
		return 0
	case string:
		if n, err := strconv.Atoi(jsTrim(x)); err == nil {
			r.note("%s 写成了字符串「%s」，按整数读", key, x)
			return n
		}
	}
	r.note("%s 不是整数（写成了%s），没读", key, jsonKind(raw))
	return 0
}

// list 读字符串数组字段：元素摊成文字；整个写成字符串时按「；」拆开。
func (r *styleFieldReader) list(key string) []string {
	raw, ok := r.obj[key]
	if !ok {
		return nil
	}
	switch kind := jsonKind(raw); kind {
	case "null":
		return nil
	case "数组":
		var items []json.RawMessage
		_ = json.Unmarshal(raw, &items)
		out := make([]string, 0, len(items))
		for _, item := range items {
			if k := jsonKind(item); k != "字符串" && k != "null" {
				r.note("%s 里有一项写成了%s，转成文字", key, k)
			}
			if text := strings.Join(jsonTextParts(item), styleTextSep); text != "" {
				out = append(out, text)
			}
		}
		return out
	case "字符串":
		r.note("%s 写成了字符串，按「%s」拆开", key, styleTextSep)
	default:
		r.note("%s 写成了%s，转成文字", key, kind)
	}
	var out []string
	for _, part := range styleListSplit.Split(strings.Join(jsonTextParts(raw), styleTextSep), -1) {
		if part = jsTrim(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// colors 读颜色数组：元素是对象逐个读；元素写成 HEX 字符串时只留 HEX；整个写成一个对象时当一个颜色。
func (r *styleFieldReader) colors(key string) []PaletteColor {
	raw, ok := r.obj[key]
	if !ok {
		return []PaletteColor{}
	}
	var items []json.RawMessage
	switch kind := jsonKind(raw); kind {
	case "null":
		return []PaletteColor{}
	case "数组":
		_ = json.Unmarshal(raw, &items)
	case "对象":
		r.note("%s 写成了一个对象，当一个颜色", key)
		items = []json.RawMessage{raw}
	default:
		r.note("%s 写成了%s，没读", key, kind)
		return []PaletteColor{}
	}
	out := make([]PaletteColor, 0, len(items))
	for i, item := range items {
		switch kind := jsonKind(item); kind {
		case "对象":
			var obj map[string]json.RawMessage
			_ = json.Unmarshal(item, &obj)
			c, notes := decodePaletteColor(obj)
			r.notes = append(r.notes, prefixNotes(r.label+key+" ", notes)...)
			out = append(out, c)
		case "字符串":
			text := strings.Join(jsonTextParts(item), "")
			if hex := NormalizeHexColor(text); ValidHexColor(hex) {
				r.note("%s 第 %d 个颜色只写了 HEX「%s」", key, i+1, text)
				out = append(out, PaletteColor{Hex: hex})
				continue
			}
			r.note("%s 第 %d 个颜色写成了字符串「%s」，没读", key, i+1, text)
		default:
			r.note("%s 第 %d 个颜色写成了%s，没读", key, i+1, kind)
		}
	}
	return out
}

func prefixNotes(prefix string, notes []string) []string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		out = append(out, prefix+n)
	}
	return out
}

// decodePaletteColor 逐字段读一个颜色；说明以「「色名」的」开头。
func decodePaletteColor(obj map[string]json.RawMessage) (PaletteColor, []string) {
	r := &styleFieldReader{obj: obj}
	c := PaletteColor{NameZh: r.text("name_zh", false, false), NameEn: r.text("name_en", false, false)}
	name := firstNonBlank(c.NameZh, c.NameEn, "颜色")
	r.label = "「" + name + "」的 "
	for i := range r.notes {
		r.notes[i] = r.label + r.notes[i]
	}
	c.Hex = r.text("hex", false, false)
	c.Usage = r.text("usage", true, false)
	c.Group = r.text("group", false, false)
	return c, r.notes
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if jsTrim(v) != "" {
			return jsTrim(v)
		}
	}
	return ""
}

// decodeColorScriptEntry 逐字段读一场的色彩脚本；类型不符的说明记在 Coerced。scene 是场编号（E01S02）时集号和场序从它读；
// 也认旧写法：episode、scene_no 两个整数，scene 写场次头。
func decodeColorScriptEntry(obj map[string]json.RawMessage) ColorScriptEntry {
	r := &styleFieldReader{obj: obj}
	e := ColorScriptEntry{
		Episode:      r.integer("episode"),
		SceneNo:      r.integer("scene_no"),
		Scene:        r.text("scene", false, false),
		InheritsFrom: r.text("inherits_from", false, false),
		SharedColors: r.colors("shared_colors"),
		NewColors:    r.colors("new_colors"),
		Purpose:      r.text("purpose", true, false),
		References:   r.text("references", true, true),
		UnitID:       r.text("unit_id", false, false),
	}
	if episode, sceneNo, ok := ParseSceneCode(e.Scene); ok {
		if e.Episode > 0 && e.Episode != episode || e.SceneNo > 0 && e.SceneNo != sceneNo {
			r.notes = append(r.notes, fmt.Sprintf("scene 写的是 %s，episode / scene_no 写的是第 %d 集第 %d 场，按 scene 读", SceneCode(episode, sceneNo), e.Episode, e.SceneNo))
		}
		e.Episode, e.SceneNo, e.Scene = episode, sceneNo, ""
	}
	e.Coerced = r.notes
	return e
}

// decodeStyleAnchor 逐字段读风格锚；一个认识的字段都没有时，说明里列出写了哪些字段名。
func decodeStyleAnchor(obj map[string]json.RawMessage) *StyleAnchor {
	r := &styleFieldReader{obj: obj, label: "风格锚 "}
	a := &StyleAnchor{
		Medium:        r.text("medium", true, false),
		ArtStyle:      r.text("art_style", true, false),
		Lighting:      r.text("lighting", true, false),
		Camera:        r.text("camera", true, false),
		Grading:       r.text("grading", true, false),
		Forbidden:     r.list("forbidden"),
		Era:           r.text("era", true, false),
		Genre:         r.text("genre", true, false),
		RenderQuality: r.text("render_quality", true, false),
	}
	if a.Empty() && len(obj) > 0 {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		r.notes = append(r.notes, "风格锚的字段名一个都不认识："+strings.Join(keys, "、"))
	}
	a.Coerced = r.notes
	return a
}

// looksLikeStyleAnchor 表示这一行是没包 style_anchor 的风格锚：至少两个风格锚字段，没有场的字段。
func looksLikeStyleAnchor(obj map[string]json.RawMessage) bool {
	n := 0
	for _, k := range styleAnchorKeys {
		if _, ok := obj[k]; ok {
			n++
		}
	}
	return n >= 2
}
