package drama

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// 风格锚与色彩脚本的共用类型：风格锚（bible_json.style_anchor，由风格锁定 style.lock 写）、颜色、逐场色彩脚本
// （bible_json.color_script，由场次色卡 scene.palette 写，每场一张代码合成的色卡资产）。

// StyleAnchor 是全局风格锚（bible_json.style_anchor）：契约 bible 原有 art_style / render_quality / era / genre，
// 风格锁定加 medium、lighting、camera、grading、forbidden。
type StyleAnchor struct {
	Medium        string   `json:"medium,omitempty"`
	ArtStyle      string   `json:"art_style,omitempty"`
	Lighting      string   `json:"lighting,omitempty"`
	Camera        string   `json:"camera,omitempty"`
	Grading       string   `json:"grading,omitempty"`
	Forbidden     []string `json:"forbidden,omitempty"`
	Era           string   `json:"era,omitempty"`
	Genre         string   `json:"genre,omitempty"`
	RenderQuality string   `json:"render_quality,omitempty"`
	// Coerced 是解析时字段类型不符、降级读出的说明（检查报 warn），不写库。
	Coerced []string `json:"-"`
}

// Empty 表示风格锚一个字段都没写。
func (a *StyleAnchor) Empty() bool {
	if a == nil {
		return true
	}
	for _, v := range []string{a.Medium, a.ArtStyle, a.Lighting, a.Camera, a.Grading, a.Era, a.Genre, a.RenderQuality} {
		if jsTrim(v) != "" {
			return false
		}
	}
	return len(a.Forbidden) == 0
}

// anchorProblem 说明风格锚缺什么；有可用的风格锚时返回空串。unreadable 表示有一行写了 style_anchor，但补过括号也读不出来。
func anchorProblem(anchor *StyleAnchor, unreadable bool) string {
	switch {
	case !anchor.Empty():
		return ""
	case anchor != nil:
		detail := "medium、art_style、lighting、camera、grading、forbidden 都没写"
		if i := slices.IndexFunc(anchor.Coerced, func(n string) bool { return strings.Contains(n, "字段名一个都不认识") }); i >= 0 {
			detail = anchor.Coerced[i]
		}
		return "全局风格锚是空的（" + detail + "）"
	case unreadable:
		return "全局风格锚那一行不是合法的 JSON，读不出来：要单独一行写 {\"style_anchor\": {…}}，整个对象写在同一行、括号配对"
	}
	return "没有写全局风格锚：要单独一行写 {\"style_anchor\": {…}}"
}

// PaletteColor 是色板里的一个颜色。Group 是场次色卡里这个颜色归哪一组（主色卡的分组键 protagonist / supporting / props /
// environment / other，见 MasterPaletteGroupKey）；主色卡的颜色已经按组放好，不写它。
type PaletteColor struct {
	NameZh string `json:"name_zh"`
	NameEn string `json:"name_en"`
	Hex    string `json:"hex"`
	Usage  string `json:"usage"`
	Group  string `json:"group,omitempty"`
}

// ColorScriptEntry 是一场的色彩脚本（bible_json.color_script 的一项，也是这一场场次色卡资产的设定）。
type ColorScriptEntry struct {
	Episode int `json:"episode"`
	SceneNo int `json:"scene_no"`
	// Scene 是场次头原文（不带「## 」）：场次色卡收尾时由服务端按当前场次表填。
	Scene        string         `json:"scene"`
	InheritsFrom string         `json:"inherits_from"`
	SharedColors []PaletteColor `json:"shared_colors"`
	NewColors    []PaletteColor `json:"new_colors"`
	Purpose      string         `json:"purpose,omitempty"`
	// References 是视觉影视参考，多个用「；」分隔；模型写成字符串数组时解析拼成一个字符串。
	References string `json:"references,omitempty"`
	// UnitID 是写这一场时它所在的单元（剧集或原文章节），收尾时由服务端填；改编出剧集后，原文章节版同编号的条目靠它认出来。
	UnitID string `json:"unit_id,omitempty"`
	// SceneRule 是写这一场时的切场规则（SceneRuleMerged，收尾时由服务端填）；没有（0）是规则改变前一个场次头一场时写的，
	// 场编号可能和现在的场次表错位，见 LegacySceneCurrent。
	SceneRule int `json:"scene_rule,omitempty"`
	// Coerced 是解析时字段类型不符、降级读出的说明（检查报 warn），不写库。
	Coerced []string `json:"-"`
}

// SceneCode 是场编号 E01S02（第 1 集第 2 场）。
func SceneCode(episode, sceneNo int) string { return fmt.Sprintf("E%02dS%02d", episode, sceneNo) }

// Code 是这一场的场编号。
func (e ColorScriptEntry) Code() string { return SceneCode(e.Episode, e.SceneNo) }

// Colors 是色卡上的全部颜色：承接色（或场景基础色）在前，本场新增色在后。
func (e ColorScriptEntry) Colors() []PaletteColor {
	return append(slices.Clone(e.SharedColors), e.NewColors...)
}

var (
	hexColorPattern = regexp.MustCompile(`^#[0-9A-F]{6}$`)
	bareHexPattern  = regexp.MustCompile(`^[0-9A-Fa-f]{6}$`)
	sceneCodeInput  = regexp.MustCompile(`(?i)^E(\d{1,3})S(\d{1,3})$`)
)

// ValidHexColor 是 #RRGGBB（大写，规整之后）。
func ValidHexColor(hex string) bool { return hexColorPattern.MatchString(hex) }

// NormalizeHexColor 把常见写法规整成大写 #RRGGBB：去空白、补 #；其余原样（检查会报不合法）。
func NormalizeHexColor(hex string) string {
	hex = strings.ToUpper(jsTrim(hex))
	if bareHexPattern.MatchString(hex) {
		hex = "#" + hex
	}
	return hex
}

// NormalizeSceneCode 把 e1s2、E01S02 规整成 E01S02；不是场编号返回原样（去空白）。
func NormalizeSceneCode(code string) string {
	code = jsTrim(code)
	if m := sceneCodeInput.FindStringSubmatch(code); m != nil {
		ep, _ := strconv.Atoi(m[1])
		sc, _ := strconv.Atoi(m[2])
		return SceneCode(ep, sc)
	}
	return code
}

// ParseSceneCode 读场编号的集号和场序；不是场编号时 ok 为 false。
func ParseSceneCode(code string) (episode, sceneNo int, ok bool) {
	m := sceneCodeInput.FindStringSubmatch(jsTrim(code))
	if m == nil {
		return 0, 0, false
	}
	episode, _ = strconv.Atoi(m[1])
	sceneNo, _ = strconv.Atoi(m[2])
	return episode, sceneNo, episode > 0 && sceneNo > 0
}

// normalize 规整一场：HEX、承接场编号、颜色分组键、场次头去掉「## 」，颜色数组不为 nil。
func (e *ColorScriptEntry) normalize() {
	e.Scene = jsTrim(strings.TrimLeft(jsTrim(e.Scene), "# "))
	e.InheritsFrom = NormalizeSceneCode(e.InheritsFrom)
	for _, list := range []*[]PaletteColor{&e.SharedColors, &e.NewColors} {
		if *list == nil {
			*list = []PaletteColor{}
		}
		for i := range *list {
			c := &(*list)[i]
			c.Hex, c.NameZh, c.NameEn, c.Usage = NormalizeHexColor(c.Hex), jsTrim(c.NameZh), jsTrim(c.NameEn), jsTrim(c.Usage)
			if jsTrim(c.Group) != "" {
				c.Group = MasterPaletteGroupKey(c.Group)
			}
		}
	}
	e.Purpose, e.References = jsTrim(e.Purpose), jsTrim(e.References)
}

// StyleBibleProfile 是项目风格档案里写给风格锁定、场次色卡和资产设计看的部分。
type StyleBibleProfile struct {
	Label       string
	StyleBlock  string
	LightPolicy string
}

// stylePromptProfileChars 是【风格档案】段画面质感的字数上限。
const stylePromptProfileChars = 600

// styleBibleStylePrompt 是【项目画风】段（风格锁定用）：风格锚的 medium 与它一致，色板在它的色彩与光影基线里取色；预设的选项另起一行。
func styleBibleStylePrompt(style StepStyle) string {
	out := stepStylePrompt(style, "风格锚的 medium 与这个画风一致，不改媒介；色板的颜色在它的色彩与光影基线里取，不另起一套色系。")
	if len(style.Selection) == 0 {
		return out
	}
	keys := make([]string, 0, len(style.Selection))
	for k := range style.Selection {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if v := jsTrim(style.Selection[k]); v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	if len(parts) == 0 {
		return out
	}
	line := "画风选项：" + strings.Join(parts, "；")
	if out == "" {
		title := jsTrim(style.Title)
		if title == "" {
			title = "未命名画风"
		}
		return "【项目画风：" + title + "】" + line
	}
	return out + "\n" + line
}

// styleBibleProfilePrompt 是【风格档案】段：档案名、画面质感段和光线政策。
func styleBibleProfilePrompt(p StyleBibleProfile) string {
	if jsTrim(p.Label) == "" && jsTrim(p.StyleBlock) == "" {
		return ""
	}
	lines := []string{"【风格档案：" + jsTrim(p.Label) + "】后面的分镜和视频按这个档案执行，风格锚和色板不要和它冲突。"}
	if block := jsCompact(p.StyleBlock); block != "" {
		lines = append(lines, "- 画面质感："+jsTruncate(block, stylePromptProfileChars))
	}
	if policy := jsTrim(p.LightPolicy); policy != "" {
		lines = append(lines, "- 光线政策："+policy)
	}
	return strings.Join(lines, "\n")
}
