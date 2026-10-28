package drama

import "strings"

// 生成分段每一场的【本场色卡】段（M7.2 第 6 阶段起每一轮带本场色卡；M8 卡 F 按小节生成后放在本场第一节）。
// M7.2 第 6 阶段的戏类判定（武戏 / 文戏 / 悬疑戏 / 单人情绪戏）和戏类卡注入已经去掉：M8 不自动判戏类，戏类按项目视频模型
// 单段上限选（shot_genre.go），一个戏类一整套技能。

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
