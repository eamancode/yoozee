package drama

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// 编译档案（M5）：装段和编译里影响质量的文字——镜头画面句、表情链、灯光七项、台词四元、人物声明、
// 参考声明、约束、⚠️ 风险文案、风格块与表演块、机位词表——从代码搬到可在线编辑、带版本的 JSON。
// 代码只保留结构：分秒、字段顺序、参考编号、预算与切点算法。
//
// 模板用 {name} 占位符，每个模板允许的占位符由 vars 标签固定，req 标签是必须保留的占位符，drop 标签标出可以设成空串去掉的片段；
// 可选片段（光源方向、特效持续时间等）拆成单独模板，由代码决定拼不拼，文案改坏也不会改坏结构。
// 版本 0 是内置默认（compile_profile_default.json），逐字等于 M5 之前代码里的文字。

//go:embed compile_profile_default.json
var defaultCompileProfileJSON []byte

// CompileProfile 是一份编译档案。存库的版本只需写要改的项，读取时叠在内置默认上。
type CompileProfile struct {
	Blocks     CompileBlocks      `json:"blocks"`
	Camera     CameraWords        `json:"camera"`
	Shot       ShotPhrases        `json:"shot"`
	Expression ExpressionPhrases  `json:"expression"`
	Light      LightPhrases       `json:"light"`
	Segment    SegmentPhrases     `json:"segment"`
	Cast       CastPhrases        `json:"cast"`
	Line       LinePhrases        `json:"line"`
	Seedance   SeedancePhrases    `json:"seedance"`
	H3         H3Phrases          `json:"h3"`
	Wuxi       WuxiPhrases        `json:"wuxi"`
	Risk       map[string]string  `json:"risk"`
	meta       compileProfileMeta `json:"-"`
}

type compileProfileMeta struct {
	version int
	name    string
}

// Version 是档案版本号，0 表示内置默认。
func (p *CompileProfile) Version() int {
	if p == nil {
		return 0
	}
	return p.meta.version
}

// Name 是档案名称（内置默认为「内置默认」）。
func (p *CompileProfile) Name() string {
	if p == nil || p.meta.name == "" {
		return "内置默认"
	}
	return p.meta.name
}

// WithMeta 返回带版本号和名称的副本（存库版本加载后由调用方标记）。
func (p *CompileProfile) WithMeta(version int, name string) *CompileProfile {
	out := *p
	out.meta = compileProfileMeta{version: version, name: name}
	return &out
}

// CompileBlocks 覆盖风格块与表演块：按方言（没有风格档案时）和按风格档案 ID。空串表示取原来源
// （kb-dialects/blocks、yz-producer/profiles 的 style_block / acting_block）。
type CompileBlocks struct {
	Seedance DialectBlocks            `json:"seedance"`
	H3       DialectBlocks            `json:"h3"`
	Profiles map[string]ProfileBlocks `json:"profiles"`
	// ProjectStyle 是项目选了画风时的风格段：取画风执行规范里 sections 列出的几节，合计不超过 max_chars 字，填进 template。
	ProjectStyle ProjectStylePhrases `json:"project_style"`
}

type ProjectStylePhrases struct {
	Template string   `json:"template" vars:"style" req:"style"`
	Sections []string `json:"sections"`
	MaxChars int      `json:"max_chars"`
}

type DialectBlocks struct {
	Style  string `json:"style"`
	Acting string `json:"acting"`
}

type ProfileBlocks struct {
	Style  LangBlock `json:"style"`
	Acting LangBlock `json:"acting"`
}

// CameraWords 是机位与运镜词表（镜头的景别后、画面句前那一串）。
type CameraWords struct {
	X           map[string]string `json:"x"`
	Y           map[string]string `json:"y"`
	Move        map[string]string `json:"move"`
	Composition map[string]string `json:"composition"`
	Dutch       string            `json:"dutch" vars:"deg" req:"deg"`
	Lens        string            `json:"lens" vars:"mm" req:"mm"`
	DofShallow  string            `json:"dof_shallow"`
	DofDeep     string            `json:"dof_deep"`
}

// ShotPhrases 是一镜画面句（装段时写进段的 shots[].text）。
type ShotPhrases struct {
	ActionPattern   string            `json:"action_pattern" vars:"name,items" req:"items"`
	Expression      string            `json:"expression" vars:"text" req:"text"`
	CoreTerms       string            `json:"core_terms" vars:"terms" req:"terms" drop:"ok"`
	Performance     string            `json:"performance" vars:"beats" req:"beats" drop:"ok"`
	State           string            `json:"state" vars:"state" req:"state" drop:"ok"`
	LightSource     string            `json:"light_source" vars:"anchor,direction" req:"anchor" drop:"ok"`
	LightDirection  string            `json:"light_direction" vars:"direction" req:"direction" drop:"ok"`
	LightFull       string            `json:"light_full" vars:"text" req:"text"`
	Fight           string            `json:"fight" vars:"parts" req:"parts"`
	FightInitiator  string            `json:"fight_initiator" vars:"initiator" req:"initiator"`
	FightPath       string            `json:"fight_path" vars:"path" req:"path" drop:"ok"`
	FightTarget     string            `json:"fight_target" vars:"target" req:"target" drop:"ok"`
	FightResponse   string            `json:"fight_response" vars:"response" req:"response" drop:"ok"`
	FightEndPose    string            `json:"fight_end_pose" vars:"pose" req:"pose" drop:"ok"`
	FightContact    map[string]string `json:"fight_contact"`
	Martial         string            `json:"martial" vars:"names" req:"names"`
	Vfx             string            `json:"vfx" vars:"name,items,seconds" req:"items"`
	VfxScale        string            `json:"vfx_scale" vars:"scale" req:"scale" drop:"ok"`
	VfxSeconds      string            `json:"vfx_seconds" vars:"seconds" req:"seconds" drop:"ok"`
	SoundBridgeIn   string            `json:"sound_bridge_in"`
	SoundBridgeOut  string            `json:"sound_bridge_out"`
	MotionStill     string            `json:"motion_still"`
	MotionChange    string            `json:"motion_change" vars:"from,to"`
	MotionCamera    string            `json:"motion_camera" vars:"moves" req:"moves" drop:"ok"`
	PositionAt      string            `json:"position_at" vars:"who,at"`
	PositionBehind  string            `json:"position_behind" vars:"behind" req:"behind" drop:"ok"`
	PositionOcclude string            `json:"position_occlude" vars:"by" req:"by" drop:"ok"`
	PositionCamera  string            `json:"position_camera" vars:"side" req:"side" drop:"ok"`
}

// ExpressionPhrases 是情绪弧展开成的分秒表情链。
type ExpressionPhrases struct {
	Phase       string            `json:"phase" vars:"from,to,signals" req:"signals"`
	LineMarker  string            `json:"line_marker"`
	Hidden7Tail string            `json:"hidden7_tail"`
	Beats       string            `json:"beats" vars:"beats" req:"beats"`
	FamilyTerms map[string]string `json:"family_default_terms"`
}

// LightPhrases 是灯光七项（加光线变化）的标签，顺序由代码固定。
type LightPhrases struct {
	Item     string `json:"item" vars:"label,value" req:"value"`
	Key      string `json:"key"`
	Fill     string `json:"fill"`
	Rim      string `json:"rim"`
	Ratio    string `json:"ratio"`
	Kelvin   string `json:"kelvin"`
	Shadow   string `json:"shadow"`
	Exposure string `json:"exposure"`
	Change   string `json:"change"`
}

// SegmentPhrases 是段末连戏和同场后段的再锚点。
type SegmentPhrases struct {
	EndState         string `json:"end_state" vars:"rules" req:"rules"`
	Gaze             string `json:"gaze" vars:"gaze" req:"gaze" drop:"ok"`
	GazeToward       string `json:"gaze_toward" vars:"who,target" req:"target" drop:"ok"`
	Reanchor         string `json:"reanchor"`
	ReanchorWithPrev string `json:"reanchor_with_prev" vars:"prev,state" req:"state"`
}

// CastPhrases 是没有身份参考图的人物外貌声明。
type CastPhrases struct {
	Decl    string `json:"decl" vars:"name,lock" req:"lock"`
	LockMax int    `json:"lock_max"`
}

// LinePhrases 是台词写法：三元齐全时写即梦四元，否则写 delivery 一句话。
type LinePhrases struct {
	VO       string `json:"vo"`
	Timbre   string `json:"timbre" vars:"value" req:"value"`
	Pace     string `json:"pace" vars:"value" req:"value"`
	Tone     string `json:"tone" vars:"value" req:"value"`
	Emotion  string `json:"emotion" vars:"value" req:"value"`
	WithHow  string `json:"with_how" vars:"speaker,how,text" req:"text"`
	Plain    string `json:"plain" vars:"speaker,text" req:"text"`
	H3VO     string `json:"h3_vo"`
	H3Line   string `json:"h3_line" vars:"vo,speaker,how,lang,text" req:"text"`
	H3NoHow  string `json:"h3_line_plain" vars:"vo,speaker,lang,text" req:"text"`
	Language string `json:"default_lang"`
}

// SeedancePhrases 是即梦（Seedance）方言的参考声明、开头、声音与约束。{ref} 是代码编的参考编号（@图片1）。
type SeedancePhrases struct {
	FirstFrame      string `json:"first_frame" vars:"ref" req:"ref"`
	LastFrame       string `json:"last_frame" vars:"ref" req:"ref"`
	RoleIdentity    string `json:"role_identity" vars:"ref,note" req:"ref"`
	RoleLocation    string `json:"role_location" vars:"ref,note" req:"ref"`
	RolePlate       string `json:"role_plate" vars:"ref,note" req:"ref"`
	RoleProp        string `json:"role_prop" vars:"ref,note" req:"ref"`
	RoleFrame       string `json:"role_frame" vars:"ref,note" req:"ref"`
	RoleMotion      string `json:"role_motion" vars:"ref,note" req:"ref"`
	RoleVoice       string `json:"role_voice" vars:"ref,name" req:"ref"`
	NotePrefix      string `json:"note_prefix" vars:"note" req:"note"`
	IdentityDefault string `json:"identity_default"`
	RefState        string `json:"ref_state" vars:"state" req:"state"`
	BridgeFrameNote string `json:"bridge_frame_note"`
	BridgeClipNote  string `json:"bridge_clip_note"`
	Head            string `json:"head" vars:"dur,style,acting" req:"dur"`
	ReanchorMid     string `json:"reanchor_mid"`
	Sound           string `json:"sound" vars:"sound" req:"sound"`
	SoundDefault    string `json:"sound_default"`
	SharedBed       string `json:"shared_bed" vars:"bed" req:"bed"`
	ScreenText      string `json:"screen_text" vars:"texts" req:"texts"`
	GroupVariation  string `json:"group_variation" vars:"text" req:"text"`
	Constraint      string `json:"constraint" vars:"identity,text"`
	IdentityRefs    string `json:"identity_refs"`
	IdentityCast    string `json:"identity_cast"`
	TextRefs        string `json:"text_refs"`
	TextRegistered  string `json:"text_registered"`
	Lang            string `json:"lang" vars:"lang" req:"lang"`
}

// WuxiPhrases 是武戏节（skill-complete-v7，M8 卡 E）编译视频提示词的武戏段落结构（卡 0 文档 §5f）：每镜按
// 景别 / 焦段 / 运镜 → 摄影机目标 → 站位 → 动作链（力度，受击，战损，闪帧）→ 表情 → 特效 → 环境反馈 → 光影 → 下一镜连接 写，
// 段头加本节核心观看体验，约束前加一行武戏约束。值取镜头 spec.extras 里武戏转换器存的标签；文戏节不用这一组。
type WuxiPhrases struct {
	Target     string `json:"target" vars:"target" req:"target" drop:"ok"`
	Station    string `json:"station" vars:"station" req:"station" drop:"ok"`
	Action     string `json:"action" vars:"action,tags" req:"action"`
	Tags       string `json:"tags" vars:"tags" req:"tags" drop:"ok"`
	Face       string `json:"face" vars:"face" req:"face" drop:"ok"`
	Fx         string `json:"fx" vars:"fx" req:"fx" drop:"ok"`
	Env        string `json:"env" vars:"env" req:"env" drop:"ok"`
	Light      string `json:"light" vars:"light" req:"light" drop:"ok"`
	Next       string `json:"next" vars:"next" req:"next" drop:"ok"`
	Experience string `json:"experience" vars:"text" req:"text" drop:"ok"`
	Constraint string `json:"constraint" drop:"ok"`
}

// H3Phrases 是 MiniMax-H3 方言的英文参考声明与首尾帧说明。{ref} 是代码编的参考编号（<Picture 1>）。
type H3Phrases struct {
	FirstFrame      string            `json:"first_frame"`
	LastFrame       string            `json:"last_frame"`
	Roles           map[string]string `json:"roles"`
	RoleOther       string            `json:"role_other" vars:"note"`
	RefState        string            `json:"ref_state" vars:"state" req:"state"`
	BridgeFrameNote string            `json:"bridge_frame_note"`
	RoleVoice       string            `json:"role_voice" vars:"ref,name,speaker" req:"ref"`
	DeliveryNote    string            `json:"delivery_note"`
	SoundDefault    string            `json:"sound_default"`
}

var (
	defaultCompileProfileOnce sync.Once
	defaultCompileProfile     *CompileProfile
	defaultCompileProfileErr  error
	placeholderPattern        = regexp.MustCompile(`\{([a-z_]+)\}`)
)

// DefaultCompileProfile 是内置默认档案（版本 0）。返回值共享，不能修改。
func DefaultCompileProfile() (*CompileProfile, error) {
	defaultCompileProfileOnce.Do(func() {
		var p CompileProfile
		dec := json.NewDecoder(bytes.NewReader(defaultCompileProfileJSON))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			defaultCompileProfileErr = fmt.Errorf("内置编译档案无效: %w", err)
			return
		}
		if err := p.Validate(); err != nil {
			defaultCompileProfileErr = fmt.Errorf("内置编译档案无效: %w", err)
			return
		}
		defaultCompileProfile = &p
	})
	return defaultCompileProfile, defaultCompileProfileErr
}

// DefaultCompileProfileJSON 是内置默认档案的原文。
func DefaultCompileProfileJSON() []byte { return slices.Clone(defaultCompileProfileJSON) }

// mustCompileProfile 给装段、编译用：nil 取内置默认（内置默认在测试里校验过，读不出来是构建错误）。
func mustCompileProfile(p *CompileProfile) *CompileProfile {
	if p != nil {
		return p
	}
	d, err := DefaultCompileProfile()
	if err != nil {
		panic(err)
	}
	return d
}

// ParseCompileProfile 把存库的档案内容叠在内置默认上；strict 时拒绝未知字段（保存时用），读取时宽松。
func ParseCompileProfile(raw []byte, strict bool) (*CompileProfile, error) {
	if _, err := DefaultCompileProfile(); err != nil {
		return nil, err
	}
	// 从默认原文重新解码一份再叠加，避免改到共享的默认值（map 解码是逐键合并）。
	var p CompileProfile
	if err := json.Unmarshal(defaultCompileProfileJSON, &p); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return &p, nil
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("编译档案必须是 JSON 对象")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("编译档案格式无效: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate 检查模板的占位符：不认识的占位符和缺了必须保留的占位符都拒绝；风险文案不能删成空。
func (p *CompileProfile) Validate() error {
	if err := validateTemplates(reflect.ValueOf(*p), ""); err != nil {
		return err
	}
	if p.Cast.LockMax < 1 || p.Cast.LockMax > 20 {
		return fmt.Errorf("cast.lock_max 必须在 1 到 20 之间")
	}
	if p.Blocks.ProjectStyle.MaxChars < 40 || p.Blocks.ProjectStyle.MaxChars > 2000 {
		return fmt.Errorf("blocks.project_style.max_chars 必须在 40 到 2000 之间")
	}
	for tag, text := range p.Risk {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("risk.%s 不能为空（不想要这条提醒就别在镜头里写这个风险标签）", tag)
		}
	}
	return nil
}

func validateTemplates(v reflect.Value, prefix string) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		value := v.Field(i)
		switch value.Kind() {
		case reflect.Struct:
			if err := validateTemplates(value, path); err != nil {
				return err
			}
		case reflect.String:
			allowed := splitTag(field.Tag.Get("vars"))
			text := value.String()
			for _, m := range placeholderPattern.FindAllStringSubmatch(text, -1) {
				if !slices.Contains(allowed, m[1]) {
					if len(allowed) == 0 {
						return fmt.Errorf("%s 不支持占位符，出现了 {%s}", path, m[1])
					}
					return fmt.Errorf("%s 只能用占位符 %s，出现了 {%s}", path, braceList(allowed), m[1])
				}
			}
			// drop:"ok" 的模板是可去掉的片段：设成空串（或只有空白）就不写这一片，拼接时跳过，不用保留占位符。
			if strings.TrimSpace(text) == "" && field.Tag.Get("drop") == "ok" {
				continue
			}
			for _, need := range splitTag(field.Tag.Get("req")) {
				if !strings.Contains(text, "{"+need+"}") {
					return fmt.Errorf("%s 必须保留占位符 {%s}", path, need)
				}
			}
		}
	}
	return nil
}

func splitTag(tag string) []string {
	if tag == "" {
		return nil
	}
	return strings.Split(tag, ",")
}

func braceList(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "{" + n + "}"
	}
	return strings.Join(out, " ")
}

// fill 替换模板里的占位符（一次扫描，值里的花括号不会被再次替换）。
// 只有空白的模板等于空串（编译档案里去掉的片段），不写进句子。
func fill(tpl string, kv ...string) string {
	if strings.TrimSpace(tpl) == "" {
		return ""
	}
	if len(kv) == 0 || !strings.Contains(tpl, "{") {
		return tpl
	}
	pairs := make([]string, 0, len(kv))
	for i := 0; i+1 < len(kv); i += 2 {
		pairs = append(pairs, "{"+kv[i]+"}", kv[i+1])
	}
	return strings.NewReplacer(pairs...).Replace(tpl)
}

// blocksFor 是编译用的风格块和表演块：档案覆盖优先，没覆盖取原来源。
func (p *CompileProfile) blocksFor(style *Profile, dialect string) (string, string, error) {
	var s, a string
	if style != nil {
		merged := Profile{StyleBlock: style.StyleBlock, ActingBlock: style.ActingBlock}
		if o, ok := p.Blocks.Profiles[style.ID]; ok {
			// 按语言逐项覆盖：只改了中文块时英文块仍取来源文件。
			merged.StyleBlock = LangBlock{Zh: firstNonEmptyString(o.Style.Zh, style.StyleBlock.Zh), En: firstNonEmptyString(o.Style.En, style.StyleBlock.En)}
			merged.ActingBlock = LangBlock{Zh: firstNonEmptyString(o.Acting.Zh, style.ActingBlock.Zh), En: firstNonEmptyString(o.Acting.En, style.ActingBlock.En)}
		}
		s, a = profileBlocks(&merged, dialect)
		return s, a, nil
	}
	var err error
	if s, a, err = Blocks(dialect); err != nil {
		return "", "", err
	}
	o := p.Blocks.H3
	if dialect == "seedance" {
		o = p.Blocks.Seedance
	}
	return firstNonEmptyString(o.Style, s), firstNonEmptyString(o.Acting, a), nil
}

// ResolvedDefaultCompileProfile 是给后台「基于默认新建」看的默认档案：风格块和表演块按来源文件填好。
func ResolvedDefaultCompileProfile() (*CompileProfile, error) {
	p, err := ParseCompileProfile(nil, false)
	if err != nil {
		return nil, err
	}
	for dialect, target := range map[string]*DialectBlocks{"seedance": &p.Blocks.Seedance, "h3": &p.Blocks.H3} {
		s, a, err := Blocks(dialect)
		if err != nil {
			return nil, err
		}
		target.Style, target.Acting = s, a
	}
	if p.Blocks.Profiles == nil {
		p.Blocks.Profiles = map[string]ProfileBlocks{}
	}
	for _, id := range BuiltinProfileIDs() {
		prof, err := BuiltinProfile(id)
		if err != nil {
			return nil, err
		}
		p.Blocks.Profiles[id] = ProfileBlocks{Style: prof.StyleBlock, Acting: prof.ActingBlock}
	}
	return p, nil
}

var (
	styleSectionHeader = regexp.MustCompile(`【([^】]{1,20})】`)
	styleSentenceEnd   = regexp.MustCompile(`[。！？；!?;]`)
)

// ProjectStyleBlock 把项目画风的执行规范写成视频提示词的风格段；spec 为空返回空串（调用方改用风格档案的 style_block）。
// 规范按「【节名】正文」分节时只取 project_style.sections 列出的几节（按档案里的顺序），没分节就用全文；
// 合计超过 max_chars 时整节舍去，第一节就超时截到最后一个句末标点。
func (p *CompileProfile) ProjectStyleBlock(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	ps := mustCompileProfile(p).Blocks.ProjectStyle
	var parts []string
	if headers := styleSectionHeader.FindAllStringSubmatchIndex(spec, -1); len(headers) > 0 {
		bodies := map[string]string{}
		for i, h := range headers {
			end := len(spec)
			if i+1 < len(headers) {
				end = headers[i+1][0]
			}
			bodies[spec[h[2]:h[3]]] = strings.Join(strings.Fields(spec[h[1]:end]), "")
		}
		for _, name := range ps.Sections {
			if body := bodies[name]; body != "" {
				parts = append(parts, body)
			}
		}
		if len(parts) == 0 {
			parts = []string{strings.Join(strings.Fields(styleSectionHeader.ReplaceAllString(spec, "")), "")}
		}
	} else {
		parts = []string{strings.Join(strings.Fields(spec), " ")}
	}
	var text string
	for i, part := range parts {
		if !hasSentenceEnd(part) {
			part += "。"
		}
		if len([]rune(text+part)) <= ps.MaxChars {
			text += part
			continue
		}
		if i == 0 {
			text = truncateAtSentence(part, ps.MaxChars)
		}
		break
	}
	if text == "" {
		return ""
	}
	return fill(ps.Template, "style", text)
}

// truncateAtSentence 截到 limit 字以内的最后一个句末标点；一句都放不下就硬截。
func truncateAtSentence(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	head := string(runes[:limit])
	if locs := styleSentenceEnd.FindAllStringIndex(head, -1); len(locs) > 0 {
		return head[:locs[len(locs)-1][1]]
	}
	return head
}
