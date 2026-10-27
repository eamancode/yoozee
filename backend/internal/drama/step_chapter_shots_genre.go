package drama

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 生成分段按场注入（M7.2 第 6 阶段）：第一轮之前用同一个模型做一次轻量调用，给每一场判戏类（武戏 / 文戏 / 悬疑戏 / 单人情绪戏），
// 之后每一轮（一场）除了续写指令，只带本场的色卡（导演风格色彩脚本里这一场的颜色）和本场戏类对应的卡。戏类卡在 yz-chapter-shots
// 包的 cards/戏类-<戏类>.md，按戏类由代码挑，不靠模型自己读目录；判定失败或判不出的场按文戏兜底。

// 四种戏类。
const (
	SceneGenreAction   = "武戏"
	SceneGenreDrama    = "文戏"
	SceneGenreSuspense = "悬疑戏"
	SceneGenreSolo     = "单人情绪戏"
	// SceneGenreFallback 是判定失败、没判到或写了不认识的戏类时用的戏类。
	SceneGenreFallback = SceneGenreDrama
)

// SceneGenres 是全部戏类（判定提示词里按这个顺序列）。
var SceneGenres = []string{SceneGenreAction, SceneGenreDrama, SceneGenreSuspense, SceneGenreSolo}

var sceneGenreHints = map[string]string{
	SceneGenreAction:   "打斗、追逐、搏斗、动作对抗为主",
	SceneGenreDrama:    "两人及以上的对话、争吵、谈判、告白为主",
	SceneGenreSuspense: "悬疑、恐惧、惊吓、潜行、信息揭示为主",
	SceneGenreSolo:     "一个人在场、以情绪和内心变化为主（独处、崩溃、回忆、做决定）",
}

var sceneGenreAliases = map[string]string{
	"武戏": SceneGenreAction, "动作戏": SceneGenreAction, "打戏": SceneGenreAction, "动作": SceneGenreAction, "打斗戏": SceneGenreAction,
	"文戏": SceneGenreDrama, "对话戏": SceneGenreDrama, "对白戏": SceneGenreDrama, "对手戏": SceneGenreDrama,
	"悬疑戏": SceneGenreSuspense, "悬疑": SceneGenreSuspense, "惊悚戏": SceneGenreSuspense, "恐怖戏": SceneGenreSuspense,
	"单人情绪戏": SceneGenreSolo, "情绪戏": SceneGenreSolo, "独角戏": SceneGenreSolo, "单人戏": SceneGenreSolo, "单人情绪": SceneGenreSolo,
}

// NormalizeSceneGenre 把模型写的戏类名归到四种之一；认不出返回 false。
func NormalizeSceneGenre(value string) (string, bool) {
	genre, ok := sceneGenreAliases[strings.Join(strings.Fields(value), "")]
	return genre, ok
}

// SceneGenreCardPath 是戏类卡在 yz-chapter-shots 包里的路径。
func SceneGenreCardPath(genre string) string { return "cards/戏类-" + genre + ".md" }

// SceneGenre 是一场的戏类判定（写进任务结果 result.dramaRun.scenes[i].genre 与 genres）。
type SceneGenre struct {
	Scene  string `json:"scene"`
	Genre  string `json:"genre"`
	Reason string `json:"reason,omitempty"`
	// Fallback 表示判定没给出这一场（调用失败、解析不出、写了不认识的戏类），按文戏兜底。
	Fallback bool `json:"fallback,omitempty"`
}

// SceneGenreScene 是交给戏类判定的一场：场编号（与镜头 spec.scene 同一套）、场次头和开头摘录。
type SceneGenreScene struct {
	ID      string `json:"scene"`
	Heading string `json:"heading"`
	Excerpt string `json:"excerpt"`
}

const (
	sceneGenreExcerptChars = 240
	// SceneGenreCardChars 是一张戏类卡注入时的字数上限（按 JS 字符串长度计）。
	SceneGenreCardChars = 8000
)

// ChapterShotsGenreScenes 是戏类判定用的场次表：切场与 ChapterShotsScenes 同一套（ScriptFromText），没有场次头的正文算一场 E01S01。
func ChapterShotsGenreScenes(plainText string, title string) []SceneGenreScene {
	script := ScriptFromText(plainText, title)
	out := make([]SceneGenreScene, 0, len(script.Scenes))
	for _, sc := range script.Scenes {
		parts := make([]string, 0, len(sc.Beats))
		for _, b := range sc.Beats {
			text := b.Text
			if b.Speaker != "" {
				text = b.Speaker + "：" + text
			}
			parts = append(parts, text)
		}
		out = append(out, SceneGenreScene{ID: sc.ID, Heading: sc.Heading, Excerpt: jsTruncate(jsCompact(strings.Join(parts, " ")), sceneGenreExcerptChars)})
	}
	return out
}

// SceneGenrePrompt 是戏类判定的提示词：列出各场，要求一场一行 JSON。
func SceneGenrePrompt(title string, scenes []SceneGenreScene) string {
	var b strings.Builder
	fmt.Fprintf(&b, "下面是「%s」的 %d 场戏。分镜开写之前，先给每一场判戏类，只能从下面四种里选一种：\n", jsTrim(title), len(scenes))
	for _, g := range SceneGenres {
		fmt.Fprintf(&b, "- %s：%s\n", g, sceneGenreHints[g])
	}
	b.WriteString("一场里几种都有时，按这一场最主要、最占篇幅的戏来判。\n\n")
	for _, s := range scenes {
		fmt.Fprintf(&b, "%s「%s」：%s\n", s.ID, s.Heading, s.Excerpt)
	}
	b.WriteString("\n只输出判定结果，一场一行 JSON，按场的顺序，不要代码块、不要解释：\n")
	b.WriteString(`{"scene": "场编号", "genre": "武戏 / 文戏 / 悬疑戏 / 单人情绪戏 之一", "reason": "一句话理由"}`)
	return b.String()
}

// ParseSceneGenres 逐行读判定结果，按 scenes 的顺序返回每一场的戏类：没判到、戏类认不出的场按文戏兜底（Fallback）。
// recognized 是判到了的场数。
func ParseSceneGenres(text string, scenes []string) ([]SceneGenre, int) {
	got := map[string]SceneGenre{}
	for _, raw := range lineBreakPattern.Split(text, -1) {
		line := strings.TrimSuffix(strings.TrimSpace(raw), ",")
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var row struct {
			Scene  string `json:"scene"`
			Genre  string `json:"genre"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		genre, ok := NormalizeSceneGenre(row.Genre)
		id := strings.ToUpper(strings.TrimSpace(row.Scene))
		if !ok || id == "" {
			continue
		}
		if _, seen := got[id]; !seen {
			got[id] = SceneGenre{Scene: id, Genre: genre, Reason: jsTruncate(jsCompact(row.Reason), 80)}
		}
	}
	out := make([]SceneGenre, 0, len(scenes))
	recognized := 0
	for _, id := range scenes {
		if g, ok := got[strings.ToUpper(id)]; ok {
			g.Scene = id
			out = append(out, g)
			recognized++
			continue
		}
		out = append(out, SceneGenre{Scene: id, Genre: SceneGenreFallback, Reason: "没判出戏类，按文戏", Fallback: true})
	}
	return out, recognized
}

// FallbackSceneGenres 是判定调用失败时每一场都按文戏兜底。
func FallbackSceneGenres(scenes []string, reason string) []SceneGenre {
	out := make([]SceneGenre, 0, len(scenes))
	for _, id := range scenes {
		out = append(out, SceneGenre{Scene: id, Genre: SceneGenreFallback, Reason: reason, Fallback: true})
	}
	return out
}

// SceneGenreCard 是一张戏类卡（技能包里 cards/戏类-<戏类>.md 的正文，按 SceneGenreCardChars 截过）。
type SceneGenreCard struct {
	Genre   string `json:"genre"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256,omitempty"`
	Content string `json:"content"`
}

// SceneGenreCards 从技能版本里取四种戏类卡；包里没有的戏类不返回（老版本包没有戏类卡时为空）。
func SceneGenreCards(skill StepSkill) []SceneGenreCard {
	var out []SceneGenreCard
	for _, genre := range SceneGenres {
		f := skill.file(SceneGenreCardPath(genre))
		if f == nil || f.Binary || jsTrim(f.Content) == "" {
			continue
		}
		out = append(out, SceneGenreCard{Genre: genre, Path: f.Path, SHA256: f.SHA256, Content: boundedSkillText(f.Content, SceneGenreCardChars)})
	}
	return out
}

// sceneGenreCardTitle 是一张戏类卡在提示词里的标题；续写时按它认出这种卡已经给过。
func sceneGenreCardTitle(genre string) string { return "【戏类卡：" + genre + "】" }

// SceneGenreCardGiven 判断前面各轮的提示词里是否已经给过这种戏类的卡。
func SceneGenreCardGiven(prompts []string, genre string) bool {
	title := sceneGenreCardTitle(genre)
	for _, p := range prompts {
		if strings.Contains(p, title) {
			return true
		}
	}
	return false
}

// ScenePalettePrompt 是【本场色卡】段：导演风格给这一场定的颜色（中文名、HEX、用途）。没有色彩脚本时为空。
func ScenePalettePrompt(e *ColorScriptEntry) string {
	if e == nil || len(e.Colors()) == 0 {
		return ""
	}
	lines := []string{"【本场色卡】导演风格给这一场定的颜色：画面描述、光影和 imageGenerationPrompt 的主色落在这些颜色上（写色名，需要时带 HEX），不另起一套色系："}
	for _, c := range e.Colors() {
		line := "- " + jsTrim(c.NameZh) + " " + c.Hex
		if u := jsCompact(c.Usage); u != "" {
			line += "：" + u
		}
		lines = append(lines, line)
	}
	if p := jsCompact(e.Purpose); p != "" {
		lines = append(lines, "这一场的色彩用途："+p)
	}
	return strings.Join(lines, "\n")
}

// SceneGenrePromptSection 是一轮的【戏类】段：这种戏类第一次出现时带整张卡，之后同戏类的场只提一句（卡已在对话里）；
// 技能包里没有这种戏类的卡时只写判定结果。
func SceneGenrePromptSection(g SceneGenre, card *SceneGenreCard, given bool) string {
	if g.Genre == "" {
		return ""
	}
	verdict := "本场判为" + g.Genre
	if r := jsTrim(g.Reason); r != "" {
		verdict += "（" + r + "）"
	}
	switch {
	case card == nil:
		return "【戏类：" + g.Genre + "】" + verdict + "。"
	case given:
		return "【戏类：" + g.Genre + "】" + verdict + "，按前面给过的" + sceneGenreCardTitle(g.Genre) + "拍。"
	}
	return sceneGenreCardTitle(g.Genre) + verdict + "。下面这张卡只管这一场怎么拍；输出仍按【受保护输出契约】一镜一行，卡里另有的格式、编号和字段要求不适用：\n" +
		"<skill-file path=\"" + escapeSkillAttribute(card.Path) + "\">\n" + card.Content + "\n</skill-file>"
}
