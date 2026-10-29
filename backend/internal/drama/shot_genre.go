package drama

import "strings"

// 戏类（M8 卡 F）：生成分段按小节写，每个小节有一个戏类，戏类决定用哪一整套分镜技能、怎么切小节、怎么把技能原生输出转成统一镜头表。
// 不做自动判断：每场默认文戏，按项目视频模型单段上限选文戏 15（≤15 秒，7.3.json）或文戏 30（>15 秒，即梦分镜）；
// 武戏（skill-complete-v7，M8 卡 E）由用户在分镜制作里把某一节改过去：这一节的剧本范围按武戏口径重切重写（GenreOverride），
// 改回文戏同样重切重写。一章可以同时有文戏节和武戏节，一个戏类一条对话（见 step_section_shots.go）。
//
// 戏类和技能的绑定沿用 drama_step_bindings（风格档案 × 步骤），步骤 ID 用「chapter.shots#<戏类>」后缀，不改表。

const (
	ShotGenreWenxi15 = "wenxi15"
	ShotGenreWenxi30 = "wenxi30"
	ShotGenreWuxi    = "wuxi"
)

// ShotGenre 是一个戏类：一节的时长口径、绑定用的步骤 ID 和输出契约。
type ShotGenre struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// MaxSeconds / MinSeconds 是一节的时长口径（再按模型单段上限收紧）；TargetSeconds 是切节时尽量靠近的长度。
	MaxSeconds    float64 `json:"maxSeconds"`
	MinSeconds    float64 `json:"minSeconds"`
	TargetSeconds float64 `json:"targetSeconds"`
	// MaxLines 是一节最多几句完整台词（0 不限）；极短的三句（合计不超过 ShortLinesChars 字）也算放得下。
	MaxLines        int `json:"maxLines,omitempty"`
	ShortLinesChars int `json:"-"`
	// Step 是绑定表里这个戏类的步骤 ID；Contract 是这个戏类的输出契约（技能原生格式，平台只规定小节边界）。
	Step     string `json:"step"`
	Contract string `json:"contract"`
}

var shotGenres = []ShotGenre{
	{ID: ShotGenreWenxi15, Label: "文戏 15", MaxSeconds: 15, MinSeconds: 10, TargetSeconds: 12.5, MaxLines: 2, ShortLinesChars: 15, Step: StepChapterShots + "#" + ShotGenreWenxi15, Contract: "section.wenxi15@1"},
	{ID: ShotGenreWenxi30, Label: "文戏 30", MaxSeconds: 30, MinSeconds: 4, TargetSeconds: 25, Step: StepChapterShots + "#" + ShotGenreWenxi30, Contract: "section.wenxi30@1"},
	{ID: ShotGenreWuxi, Label: "武戏", MaxSeconds: 30, MinSeconds: 4, TargetSeconds: 15, Step: StepChapterShots + "#" + ShotGenreWuxi, Contract: "section.wuxi@1"},
}

// ShotGenres 是全部戏类（后台流程技能页按这个顺序列）。
func ShotGenres() []ShotGenre { return append([]ShotGenre(nil), shotGenres...) }

// ShotGenreByID 查一个戏类。
func ShotGenreByID(id string) (ShotGenre, bool) {
	for _, g := range shotGenres {
		if g.ID == strings.TrimSpace(id) {
			return g, true
		}
	}
	return ShotGenre{}, false
}

// ShotGenreOfStep 是步骤 ID「chapter.shots#<戏类>」对应的戏类。
func ShotGenreOfStep(stepID string) (ShotGenre, bool) {
	base, genre, ok := strings.Cut(stepID, "#")
	if !ok || base != StepChapterShots {
		return ShotGenre{}, false
	}
	return ShotGenreByID(genre)
}

// DefaultShotGenre 是一节默认的戏类：视频模型单段上限 ≤15 秒用文戏 15，更长用文戏 30。
func DefaultShotGenre(maxSeconds int) ShotGenre {
	id := ShotGenreWenxi30
	if maxSeconds <= 15 {
		id = ShotGenreWenxi15
	}
	g, _ := ShotGenreByID(id)
	return g
}

// IsWenxi 是文戏（文戏 15 / 文戏 30）：改回文戏、默认戏类都按模型上限在这两个里选。
func (g ShotGenre) IsWenxi() bool { return g.ID == ShotGenreWenxi15 || g.ID == ShotGenreWenxi30 }

// WuxiLiveProfiles 是武戏用精简切片（yz-shots-wuxi-live：去掉 CG 画质串、仙法、魔物、能量化伤痕）的风格档案：真人剧、短剧视感。
// 其余档案用完整切片 yz-shots-wuxi。绑定表里配了的格以绑定为准。
var WuxiLiveProfiles = []string{"duanju-shiga", "live-realism"}

// SectionLimits 是这个戏类在给定模型单段上限下一节的时长范围 [lo, hi]。
func (g ShotGenre) SectionLimits(modelMax int) (float64, float64) {
	hi := g.MaxSeconds
	if modelMax > 0 && float64(modelMax) < hi {
		hi = float64(modelMax)
	}
	lo := min(g.MinSeconds, hi)
	return lo, hi
}

// sectionGenreRelaxed 是按小节生成时放宽成 warn 的检查（§5 第 10 条）：技能不产出的表演字段（camera_emotion、表演节拍、
// 情绪弧）和档案配额不拦流程。
var sectionGenreRelaxed = []string{"R-SHOT-006", "R-SHOT-022", "R-SHOT-024", "R-SHOT-026"}

// GenreRelaxedRules 是这个戏类放宽成 warn 的规则；不认识的戏类（旧镜头表）不放宽。
func GenreRelaxedRules(genre string) []string {
	if _, ok := ShotGenreByID(genre); !ok {
		return nil
	}
	return append([]string(nil), sectionGenreRelaxed...)
}

// ShotlistGenre 是一张镜头表按小节生成时的戏类（取第一个写了戏类的镜头）；旧镜头表返回空串。
func ShotlistGenre(shots []Shot) string {
	for _, sh := range shots {
		if sh.Genre != "" {
			return sh.Genre
		}
	}
	return ""
}
