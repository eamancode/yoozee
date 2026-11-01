package drama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// 镜头表契约（yoozee-skills/contracts/shotlist.schema.json）与剧本契约（script.schema.json）。
// 解析是宽松的：模型写的镜头表常带契约外的字段，检查按 R-SCHEMA 报 warn，不整表拒绝。

// Shotlist 是一章（一集）的镜头表：一个镜头一条，段由装段派生。
type Shotlist struct {
	Profile string `json:"profile"`
	Episode *int   `json:"episode,omitempty"`
	Shots   []Shot `json:"shots"`
}

type CamF struct {
	LensMM     *float64    `json:"lens_mm,omitempty"`
	Dof        string      `json:"dof,omitempty"`
	Distortion string      `json:"distortion,omitempty"`
	Identity   string      `json:"identity,omitempty"`
	Geometry   string      `json:"geometry,omitempty"`
	DutchDeg   json.Number `json:"dutch_deg,omitempty"`
}

type Cam struct {
	Z string `json:"z,omitempty"`
	Y string `json:"y,omitempty"`
	X string `json:"x,omitempty"`
	F *CamF  `json:"f,omitempty"`
}

type CameraEmotion struct {
	Focal  string `json:"focal,omitempty"`
	Type   string `json:"type,omitempty"`
	Phases []struct {
		Beats string `json:"beats"`
		Type  string `json:"type"`
	} `json:"phases,omitempty"`
}

type Blocking struct {
	Who        string `json:"who"`
	At         string `json:"at"`
	Facing     string `json:"facing,omitempty"`
	Behind     string `json:"behind,omitempty"`
	OccludedBy string `json:"occluded_by,omitempty"`
}

type Fight struct {
	Initiator     string   `json:"initiator,omitempty"`
	Path          string   `json:"path,omitempty"`
	Target        string   `json:"target,omitempty"`
	Response      string   `json:"response,omitempty"`
	Contact       string   `json:"contact,omitempty"`
	ForceTransfer string   `json:"force_transfer,omitempty"`
	EndPose       string   `json:"end_pose,omitempty"`
	MartialIDs    []string `json:"martial_ids,omitempty"`
}

func (f *Fight) truthy() bool {
	return f != nil && (f.Initiator != "" || f.Path != "" || f.Target != "" || f.Response != "" || f.Contact != "" || f.ForceTransfer != "" || f.EndPose != "" || len(f.MartialIDs) > 0)
}

type Performance struct {
	Beats     []string `json:"beats,omitempty"`
	Restraint string   `json:"restraint,omitempty"`
	Subtext   string   `json:"subtext,omitempty"`
}

func (p *Performance) truthy() bool {
	return p != nil && (len(p.Beats) > 0 || p.Restraint != "" || p.Subtext != "")
}

func (p *Performance) beats() []string {
	if p == nil {
		return nil
	}
	return p.Beats
}

type Light struct {
	SourceAnchor string `json:"source_anchor,omitempty"`
	Direction    string `json:"direction,omitempty"`
	Policy       string `json:"policy,omitempty"`
	Note         string `json:"note,omitempty"`
	Key          string `json:"key,omitempty"`
	Fill         string `json:"fill,omitempty"`
	Rim          string `json:"rim,omitempty"`
	Ratio        string `json:"ratio,omitempty"`
	Kelvin       string `json:"kelvin,omitempty"`
	Shadow       string `json:"shadow,omitempty"`
	Exposure     string `json:"exposure,omitempty"`
	Change       string `json:"change,omitempty"`
}

type VfxRef struct {
	ID        string      `json:"id"`
	Seconds   json.Number `json:"seconds,omitempty"`
	Attribute string      `json:"attribute,omitempty"`
	Scale     string      `json:"scale,omitempty"`
}

// ShotLine 是镜头里的一句台词：只回指剧本 line_id；对不上原文的台词保留 text / speaker 让检查报出来。
type ShotLine struct {
	LineID    string `json:"line_id,omitempty"`
	Delivery  string `json:"delivery,omitempty"`
	Offscreen bool   `json:"offscreen,omitempty"`
	Pace      string `json:"pace,omitempty"`
	Tone      string `json:"tone,omitempty"`
	Emotion   string `json:"emotion,omitempty"`
	Placement string `json:"placement,omitempty"`
	Text      string `json:"text,omitempty"`
	Speaker   string `json:"speaker,omitempty"`
}

func (c *CameraEmotion) truthy() bool {
	return c != nil && (c.Focal != "" || c.Type != "" || len(c.Phases) > 0)
}

func (a *EmotionArcRef) truthy() bool {
	return a != nil && (a.ID != "" || a.PhaseFrom != 0 || a.PhaseTo != 0 || a.Continues != "" || a.Intensity != "" || a.Trigger != "")
}

type EmotionArcRef struct {
	ID        string  `json:"id"`
	PhaseFrom flexInt `json:"phase_from,omitempty"`
	PhaseTo   flexInt `json:"phase_to,omitempty"`
	Continues string  `json:"continues,omitempty"`
	Intensity string  `json:"intensity,omitempty"`
	Trigger   string  `json:"trigger,omitempty"`
}

type ActionPatternRef struct {
	ID      string `json:"id"`
	Variant string `json:"variant,omitempty"`
	Role    string `json:"role,omitempty"`
}

type Transition struct {
	In       string `json:"in,omitempty"`
	Out      string `json:"out,omitempty"`
	CutPoint string `json:"cut_point,omitempty"`
}

func (t *Transition) truthy() bool {
	return t != nil && (t.In != "" || t.Out != "" || t.CutPoint != "")
}

// Shot 是镜头表的一个镜头。
type Shot struct {
	ID              string            `json:"id"`
	Scene           string            `json:"scene"`
	Purpose         string            `json:"purpose,omitempty"`
	Subject         string            `json:"subject,omitempty"`
	Focus           string            `json:"focus,omitempty"`
	Size            string            `json:"size,omitempty"`
	Cam             Cam               `json:"cam"`
	Move            flexStrings       `json:"move,omitempty"`
	CameraEmotion   *CameraEmotion    `json:"camera_emotion,omitempty"`
	Dof             string            `json:"dof,omitempty"`
	Blocking        []Blocking        `json:"blocking,omitempty"`
	CameraSide      string            `json:"camera_side,omitempty"`
	Action          string            `json:"action,omitempty"`
	Fight           *Fight            `json:"fight,omitempty"`
	Performance     *Performance      `json:"performance,omitempty"`
	State           string            `json:"state,omitempty"`
	Light           *Light            `json:"light,omitempty"`
	Color           string            `json:"color,omitempty"`
	Vfx             []VfxRef          `json:"vfx,omitempty"`
	Lines           []ShotLine        `json:"lines,omitempty"`
	Voice           string            `json:"voice,omitempty"`
	Sfx             string            `json:"sfx,omitempty"`
	Bridge          string            `json:"bridge,omitempty"`
	Strategy        string            `json:"strategy,omitempty"`
	Est             flexFloat         `json:"est,omitempty"`
	StartState      string            `json:"start_state,omitempty"`
	EndState        string            `json:"end_state,omitempty"`
	RefsHint        flexStrings       `json:"refs_hint,omitempty"`
	RiskTags        flexStrings       `json:"risk_tags,omitempty"`
	Tag             string            `json:"tag,omitempty"`
	PlantID         string            `json:"plant_id,omitempty"`
	EmotionArc      *EmotionArcRef    `json:"emotion_arc,omitempty"`
	ExpressionTerms flexStrings       `json:"expression_terms,omitempty"`
	ActionPattern   *ActionPatternRef `json:"action_pattern,omitempty"`
	Composition     string            `json:"composition,omitempty"`
	Transition      *Transition       `json:"transition,omitempty"`
	VO              string            `json:"vo,omitempty"`
	// Section / Genre 是按小节生成时这一镜所在的小节（E01S02-P1）和戏类（wenxi15 / wenxi30 / wuxi）；装段一节一段。
	Section string `json:"section,omitempty"`
	Genre   string `json:"genre,omitempty"`
	// Extras 是技能原生输出里统一镜头表没有对应字段的标签（7.3 的风格、核心情绪，即梦的镜头运动方向、场景锚点等），原样带着给编译和展示用。
	Extras map[string]string `json:"extras,omitempty"`
}

func (s *Shot) light() Light {
	if s.Light == nil {
		return Light{}
	}
	return *s.Light
}

func (s *Shot) camF() CamF {
	if s.Cam.F == nil {
		return CamF{}
	}
	return *s.Cam.F
}

// shotKnownFields 是契约里镜头的全部字段（R-SCHEMA 用）。
var shotKnownFields = map[string]bool{}

func init() {
	for _, k := range []string{"id", "scene", "purpose", "subject", "focus", "size", "cam", "move", "camera_emotion", "dof", "blocking", "camera_side", "action", "fight", "performance", "state", "light", "color", "vfx", "lines", "voice", "sfx", "bridge", "strategy", "est", "start_state", "end_state", "refs_hint", "risk_tags", "tag", "plant_id", "emotion_arc", "expression_terms", "action_pattern", "composition", "transition", "vo", "section", "genre", "extras"} {
		shotKnownFields[k] = true
	}
}

// flexFloat 接受数字或数字字符串（模型偶尔把 est 写成 "5"）；0 等于没写。
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if string(raw) == "null" {
		*f = 0
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return fmt.Errorf("应为数字：%q", s)
		}
		*f = flexFloat(n)
		return nil
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err != nil {
		return err
	}
	*f = flexFloat(n)
	return nil
}

// flexInt 接受整数、整数值的浮点数或数字字符串。
type flexInt int

func (f *flexInt) UnmarshalJSON(raw []byte) error {
	var v flexFloat
	if err := v.UnmarshalJSON(raw); err != nil {
		return err
	}
	*f = flexInt(int(v))
	return nil
}

// flexStrings 接受字符串数组或单个字符串（单个当一项）。
type flexStrings []string

func (f *flexStrings) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if string(raw) == "null" {
		*f = nil
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if strings.TrimSpace(one) == "" {
			*f = nil
		} else {
			*f = flexStrings{one}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return err
	}
	*f = many
	return nil
}

// ParseShot 宽松解析一个镜头；返回契约外的字段名（按出现顺序）。
func ParseShot(raw []byte) (Shot, []string, error) {
	var sh Shot
	if err := json.Unmarshal(raw, &sh); err != nil {
		return Shot{}, nil, err
	}
	fields, err := parseOrderedObject(raw)
	if err != nil {
		return Shot{}, nil, err
	}
	var unknown []string
	for _, f := range fields {
		if !shotKnownFields[f.key] {
			unknown = append(unknown, f.key)
		}
	}
	return sh, unknown, nil
}

// Script 是剧本契约：台词有全局唯一 id，下游只引用 id。
type Script struct {
	Title  string        `json:"title"`
	Scenes []ScriptScene `json:"scenes"`
	// SceneRule 是切场规则（SceneRuleMerged）；没有（0）是规则改变前一个场次头一场切出来的台词表。
	SceneRule  int `json:"scene_rule,omitempty"`
	ScreenText []struct {
		Text string `json:"text"`
	} `json:"screen_text,omitempty"`
}

type ScriptScene struct {
	ID          string       `json:"id"`
	Heading     string       `json:"heading"`
	LocationRef string       `json:"location_ref,omitempty"`
	Characters  []string     `json:"characters"`
	StoryTime   string       `json:"story_time,omitempty"`
	TimeJump    string       `json:"time_jump,omitempty"`
	Function    string       `json:"function,omitempty"`
	Emotion     string       `json:"emotion,omitempty"`
	Kind        string       `json:"kind,omitempty"`
	Beats       []ScriptBeat `json:"beats"`
}

type ScriptBeat struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Speaker string `json:"speaker,omitempty"`
	Text    string `json:"text"`
	Tag     string `json:"tag,omitempty"`
	PlantID string `json:"plant_id,omitempty"`
}

func (b ScriptBeat) isLine() bool { return b.Kind == "line" || b.Kind == "vo" }

// scriptLines 是 {line_id: 节拍}（kind=line/vo），按剧本顺序。
type scriptLines struct {
	order []string
	byID  map[string]ScriptBeat
}

func newScriptLines(script *Script) scriptLines {
	out := scriptLines{byID: map[string]ScriptBeat{}}
	if script == nil {
		return out
	}
	for _, sc := range script.Scenes {
		for _, b := range sc.Beats {
			if b.isLine() {
				if _, ok := out.byID[b.ID]; !ok {
					out.order = append(out.order, b.ID)
				}
				out.byID[b.ID] = b
			}
		}
	}
	return out
}

func (s *Script) scene(id string) *ScriptScene {
	if s == nil {
		return nil
	}
	for i := range s.Scenes {
		if s.Scenes[i].ID == id {
			return &s.Scenes[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------- 与 Python 输出一致的格式化

// pyRound 与 Python 的 round(x, n) 一致（按二进制精确值舍入，恰好一半取偶）。
func pyRound(x float64, n int) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', n, 64), 64)
	return v
}

// pyRepr 与 Python 的 repr(str) 一致（足够覆盖中文与常见标点）。
func pyRepr(s string) string {
	quote := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = `"`
	}
	var b strings.Builder
	b.WriteString(quote)
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case string(r) == quote:
			b.WriteString(`\` + quote)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(quote)
	return b.String()
}

// pyList 与 Python 的 str(list[str]) 一致。
func pyList(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = pyRepr(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// listOr / textOr 是报错文案里的值，没写时换成 fallback（不打 Python 的 None）。
func listOr(items []string, fallback string) string {
	if len(items) == 0 {
		return fallback
	}
	return pyList(items)
}

func textOr(s string, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// pyFloatRepr 与 Python 的 repr(float) 一致（日常数值范围）。
func pyFloatRepr(x float64) string {
	return pyFloat(x)
}

// pyPercent 与 Python 的 f"{x:.0%}" 一致。
func pyPercent(x float64) string {
	return strconv.FormatFloat(x*100, 'f', 0, 64) + "%"
}

// fmtSec 与 pack._fmt 一致：保留 1 位小数再去掉末尾的 0 和小数点。
func fmtSec(x float64) string {
	s := strconv.FormatFloat(x, 'f', 1, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// stripPunct 去掉空白和中文标点（与 re.sub(r"[\s，。！？、“”…—,.!?]", "", s) 一致）。
func stripPunct(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("，。！？、“”…—,.!?", r) {
			return -1
		}
		return r
	}, s)
}

func runeLen(s string) int { return len([]rune(s)) }

func runePrefix(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func ceilInt(x float64) int { return int(math.Ceil(x)) }
