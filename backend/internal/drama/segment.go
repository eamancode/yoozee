package drama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Segment 是契约 segment（yoozee-skills/contracts/segment.schema.json）：一段 = 一次生成，段内多镜头。
type Segment struct {
	ID         string    `json:"id"`
	Scene      string    `json:"scene"`
	Title      string    `json:"title,omitempty"`
	Dur        float64   `json:"dur"`
	Mode       string    `json:"mode"`
	FirstFrame string    `json:"first_frame,omitempty"`
	LastFrame  string    `json:"last_frame,omitempty"`
	Refs       []Ref     `json:"refs,omitempty"`
	Voices     []Voice   `json:"voices,omitempty"`
	Shots      []SubShot `json:"shots"`
	Sound      string    `json:"sound,omitempty"`
	Rules      string    `json:"rules,omitempty"`
	Hook       string    `json:"hook,omitempty"`
	Lang       string    `json:"lang,omitempty"`
}

type Ref struct {
	File string `json:"file"`
	Role string `json:"role"`
	Note string `json:"note,omitempty"`
}

type Voice struct {
	Character string `json:"character"`
	File      string `json:"file"`
}

// SubShot 是段内的一个镜头。
type SubShot struct {
	At     float64 `json:"at"`
	Size   string  `json:"size,omitempty"`
	Camera string  `json:"camera,omitempty"`
	Text   string  `json:"text"`
	Lines  []Line  `json:"lines,omitempty"`
}

type Line struct {
	Speaker  string `json:"speaker"`
	Text     string `json:"text"`
	VO       bool   `json:"vo,omitempty"`
	Delivery string `json:"delivery,omitempty"`
}

const (
	ModeLockIdentity   = "lock_identity"
	ModeLockFirstFrame = "lock_first_frame"
	ModeTextOnly       = "text_only"
	// SpecMaxBytes 防止把整部剧本塞进一段。
	SpecMaxBytes = 64 << 10
)

var segmentIDPattern = regexp.MustCompile(`^S[0-9]{2,3}[a-z]?$`)

// SegmentShape 是写给 Agent 和人看的分段字段说明（契约 segment）。
const SegmentShape = "每段字段：id（S01、S12b）；scene（场次）；title?；dur（秒）；mode（lock_identity / lock_first_frame / text_only）；first_frame?、last_frame?（lock_first_frame 用）；refs?:[{file, role(identity/location/plate/prop/frame), note?}]，file 填柚子资产 ID；voices?:[{character, file}]；shots:[{at（段内第几秒）, size?, camera?, text, lines?:[{speaker, text, vo?, delivery?}]}]；sound?、rules?、hook?、lang?。不认识的字段会被拒绝。"

var refRoles = map[string]bool{"identity": true, "location": true, "plate": true, "prop": true, "frame": true, "motion": true}

// ParseSegment 严格解析并校验一段中性分段（未知字段、缺字段、非法枚举都报错）。
func ParseSegment(raw []byte) (Segment, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return Segment{}, fmt.Errorf("分段 JSON 为空")
	}
	if len(raw) > SpecMaxBytes {
		return Segment{}, fmt.Errorf("分段 JSON 超过 %d KB", SpecMaxBytes>>10)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var seg Segment
	if err := decoder.Decode(&seg); err != nil {
		message := err.Error()
		if strings.HasPrefix(message, "json: unknown field ") {
			return Segment{}, fmt.Errorf("分段不符合契约 segment：不认识的字段 %s（字段写法见工具说明）", strings.TrimPrefix(message, "json: unknown field "))
		}
		return Segment{}, fmt.Errorf("分段不符合契约 segment：%s（字段写法见工具说明）", message)
	}
	if err := seg.Validate(); err != nil {
		return Segment{}, err
	}
	return seg, nil
}

// Validate 校验契约本身（不含模型边界；模型边界见 Issues）。
func (s Segment) Validate() error {
	if !segmentIDPattern.MatchString(s.ID) {
		return fmt.Errorf("段号 %q 无效：应为 S01、S12b 这样的格式", s.ID)
	}
	if strings.TrimSpace(s.Scene) == "" {
		return fmt.Errorf("%s 缺少场景", s.ID)
	}
	if s.Dur <= 0 {
		return fmt.Errorf("%s 的时长必须大于 0", s.ID)
	}
	switch s.Mode {
	case ModeLockIdentity, ModeLockFirstFrame, ModeTextOnly:
	default:
		return fmt.Errorf("%s 的模式 %q 无效", s.ID, s.Mode)
	}
	if len(s.Shots) == 0 {
		return fmt.Errorf("%s 至少要有一个镜头", s.ID)
	}
	for i, r := range s.Refs {
		if strings.TrimSpace(r.File) == "" || !refRoles[r.Role] {
			return fmt.Errorf("%s 的第 %d 个参考缺少文件或用途无效", s.ID, i+1)
		}
	}
	for i, v := range s.Voices {
		if strings.TrimSpace(v.Character) == "" || strings.TrimSpace(v.File) == "" {
			return fmt.Errorf("%s 的第 %d 个音色参考缺少人物或文件", s.ID, i+1)
		}
	}
	for i, sh := range s.Shots {
		if sh.At < 0 || strings.TrimSpace(sh.Text) == "" {
			return fmt.Errorf("%s 的第 %d 个镜头缺少画面或开始秒无效", s.ID, i+1)
		}
		for j, ln := range sh.Lines {
			if strings.TrimSpace(ln.Speaker) == "" || strings.TrimSpace(ln.Text) == "" {
				return fmt.Errorf("%s 第 %d 个镜头的第 %d 句台词缺少说话人或内容", s.ID, i+1, j+1)
			}
		}
	}
	return nil
}

// Dialogue 把这一段所有台词按顺序拼成一个字符串（用于字错率）。
func (s Segment) Dialogue() string {
	var b strings.Builder
	for _, sh := range s.Shots {
		for _, ln := range sh.Lines {
			b.WriteString(ln.Text)
		}
	}
	return b.String()
}

// HasDialogue 这一段有没有台词。
func (s Segment) HasDialogue() bool { return utf8.RuneCountInString(s.Dialogue()) > 0 }

// Canonical 返回稳定的 JSON（用于哈希和存储）。
func (s Segment) Canonical() string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// Issue 是编译或校验发现的问题；Level 为 error 时不能提交生成。
type Issue struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

func pyFloat(x float64) string {
	// 与 Python 的 f"{x}" 一致：整数值也带 ".0"。
	text := fmt.Sprintf("%v", x)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	return text
}

// Issues 按模型边界检查一段（与 compile.py 的 _issues 一致）。
func Issues(seg Segment, m Model) []Issue {
	var out []Issue
	lo, hi := m.Duration.Min, m.Duration.Max
	if seg.Dur > float64(hi) {
		out = append(out, Issue{"error", fmt.Sprintf("%s 时长 %s 秒，超过 %s 一段的上限 %d 秒：拆成两段（yz-adapt）", seg.ID, secText(seg.Dur), m.Label, hi)})
	} else if seg.Dur < float64(lo) {
		out = append(out, Issue{"warn", fmt.Sprintf("%s 时长 %s 秒，低于 %s 的下限 %d 秒：按 %d 秒出再剪", seg.ID, secText(seg.Dur), m.Label, lo, lo)})
	}
	last := -1.0
	for k, sh := range seg.Shots {
		at := sh.At
		if at <= last && k > 0 {
			out = append(out, Issue{"error", fmt.Sprintf("%s 第 %d 个镜头的开始秒 %s 不大于上一个", seg.ID, k+1, pyFloat(at))})
		}
		if at >= seg.Dur && seg.Dur > 0 {
			out = append(out, Issue{"error", fmt.Sprintf("%s 第 %d 个镜头从 %s 秒开始，超出段长 %s 秒", seg.ID, k+1, pyFloat(at), secText(seg.Dur))})
		}
		last = at
	}
	hasRefs := len(seg.Refs) > 0 || len(seg.Voices) > 0
	if seg.Mode == ModeLockFirstFrame {
		if strings.TrimSpace(seg.FirstFrame) == "" {
			out = append(out, Issue{"error", fmt.Sprintf("%s 是锁首帧模式但没有 first_frame", seg.ID)})
		}
		if hasRefs {
			switch {
			case m.FramesExclusiveWithRefs != nil && *m.FramesExclusiveWithRefs:
				out = append(out, Issue{"error", fmt.Sprintf("%s 锁首帧时不能同时挂参考图、参考视频或参考音频（%s 互斥）", seg.ID, m.Label)})
			case m.FramesExclusiveWithRefs == nil:
				out = append(out, Issue{"warn", fmt.Sprintf("%s 锁首帧又挂了参考：%s 是否互斥尚未校准，参考可能被忽略", seg.ID, m.Label)})
			}
		}
		return out
	}
	images, videos := 0, 0
	for _, r := range seg.Refs {
		if r.Role == "motion" {
			videos++
		} else {
			images++
		}
	}
	audios := len(seg.Voices)
	for _, c := range []struct {
		name string
		have int
		cap  *int
	}{{"参考图", images, m.Refs.Images}, {"参考视频", videos, m.Refs.Videos}, {"参考音频", audios, m.Refs.Audios}} {
		if c.cap != nil && c.have > *c.cap {
			out = append(out, Issue{"error", fmt.Sprintf("%s %s %d 个，超过 %s 的上限 %d", seg.ID, c.name, c.have, m.Label, *c.cap)})
		}
	}
	if m.Refs.Total != nil && images+videos+audios > *m.Refs.Total {
		out = append(out, Issue{"error", fmt.Sprintf("%s 参考素材合计 %d 个，超过 %s 的上限 %d", seg.ID, images+videos+audios, m.Label, *m.Refs.Total)})
	}
	return out
}

// HasError 是否有 error 级问题。
func HasError(issues []Issue) bool {
	for _, i := range issues {
		if i.Level == "error" {
			return true
		}
	}
	return false
}
