package drama

import (
	"strings"
	"testing"
)

func TestParseSceneGenresNormalizesAndFallsBack(t *testing.T) {
	text := "```\n{\"scene\": \"e01s01\", \"genre\": \"对话戏\", \"reason\": \"两人对峙\"}\n说明文字\n{\"scene\": \"E01S02\", \"genre\": \"不知道\"}\n{\"scene\": \"E01S03\", \"genre\": \"打戏\"},\n```"
	genres, recognized := ParseSceneGenres(text, []string{"E01S01", "E01S02", "E01S03", "E01S04"})
	if recognized != 2 || len(genres) != 4 {
		t.Fatalf("判出两场：%d %+v", recognized, genres)
	}
	want := []SceneGenre{
		{Scene: "E01S01", Genre: SceneGenreDrama, Reason: "两人对峙"},
		{Scene: "E01S02", Genre: SceneGenreDrama, Reason: "没判出戏类，按文戏", Fallback: true},
		{Scene: "E01S03", Genre: SceneGenreAction},
		{Scene: "E01S04", Genre: SceneGenreDrama, Reason: "没判出戏类，按文戏", Fallback: true},
	}
	for i := range want {
		if genres[i] != want[i] {
			t.Fatalf("第 %d 场：%+v，应为 %+v", i+1, genres[i], want[i])
		}
	}
}

func TestSceneGenrePromptSectionGivesCardOnce(t *testing.T) {
	skill := StepSkill{Files: []StepSkillFile{
		{Path: "SKILL.md", Content: "x"},
		{Path: SceneGenreCardPath(SceneGenreAction), Content: "武戏的拍法：发起、接触、结果。"},
		{Path: SceneGenreCardPath(SceneGenreSolo), Content: strings.Repeat("情", SceneGenreCardChars+50)},
	}}
	cards := SceneGenreCards(skill)
	if len(cards) != 2 || cards[0].Genre != SceneGenreAction || jsLen(cards[1].Content) != SceneGenreCardChars {
		t.Fatalf("只取包里有的戏类卡，超长截断：%d %+v", len(cards), cards[0])
	}
	g := SceneGenre{Scene: "E01S03", Genre: SceneGenreAction, Reason: "走廊扭打"}
	first := SceneGenrePromptSection(g, &cards[0], false)
	if !strings.Contains(first, "【戏类卡：武戏】本场判为武戏（走廊扭打）") || !strings.Contains(first, "发起、接触、结果") || !strings.Contains(first, `<skill-file path="cards/戏类-武戏.md">`) {
		t.Fatalf("第一次整张带上：%s", first)
	}
	if !SceneGenreCardGiven([]string{"前面", first}, SceneGenreAction) || SceneGenreCardGiven([]string{first}, SceneGenreDrama) {
		t.Fatal("按卡标题认出给过的戏类")
	}
	again := SceneGenrePromptSection(g, &cards[0], true)
	if strings.Contains(again, "发起、接触") || !strings.Contains(again, "按前面给过的【戏类卡：武戏】拍") {
		t.Fatalf("给过的只提一句：%s", again)
	}
	if none := SceneGenrePromptSection(SceneGenre{Genre: SceneGenreSuspense}, nil, false); none != "【戏类：悬疑戏】本场判为悬疑戏。" {
		t.Fatalf("没有卡时只写判定：%s", none)
	}
}

func TestScenePalettePromptListsHex(t *testing.T) {
	e := &ColorScriptEntry{Episode: 2, SceneNo: 1, SharedColors: []PaletteColor{{NameZh: "冷白", Hex: "#E8F0F2", Usage: "顶灯"}}, NewColors: []PaletteColor{{NameZh: "血红", Hex: "#8B0000"}}, Purpose: "压迫"}
	text := ScenePalettePrompt(e)
	if !strings.Contains(text, "- 冷白 #E8F0F2：顶灯") || !strings.Contains(text, "- 血红 #8B0000") || !strings.Contains(text, "这一场的色彩用途：压迫") {
		t.Fatalf("色卡段：%s", text)
	}
	if ScenePalettePrompt(nil) != "" {
		t.Fatal("没有色彩脚本不写")
	}
}

func TestFitPromptBudgetTruncatesLaterSectionsFirst(t *testing.T) {
	sections := []promptSection{
		{"episodeSeconds", "【本集时长】60 秒"},
		{"anchor", "【全局风格锚】\n- 画风：写实"},
		{"assets", "【项目已确认资产】\n- 甲\n- 乙\n- 丙\n- 丁\n- 戊\n- 己\n- 庚"},
	}
	out, cut := fitPromptBudget(sections, 50)
	if out["episodeSeconds"] != sections[0].text || out["anchor"] != sections[1].text {
		t.Fatalf("前面的段整段保留：%+v", out)
	}
	if len(cut) != 1 || cut[0].Input != "assets" || cut[0].Kept >= cut[0].Chars || !strings.HasSuffix(out["assets"], promptBudgetCut) {
		t.Fatalf("资产清单截断并记下：%+v %q", cut, out["assets"])
	}
	if _, cut := fitPromptBudget(sections, 1000); len(cut) != 0 {
		t.Fatalf("预算够不截：%+v", cut)
	}
}

func TestBuildChapterShotsPromptWithAnchor(t *testing.T) {
	skill := StepSkill{ID: "yz-chapter-shots", Files: []StepSkillFile{{Path: "SKILL.md", Content: "技能正文"}}}
	prompt, err := BuildChapterShotsPrompt(ChapterShotsInput{ChapterTitle: "第一集", SourceText: "## 内·病房·夜\n护士：七床呢？", Skill: skill,
		Anchor: &StyleAnchor{Medium: "写实真人", Camera: "手持", Forbidden: []string{"霓虹"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.Text, "【全局风格锚】") || !strings.Contains(prompt.Text, "- 镜头语言：手持") || !strings.Contains(prompt.Text, "- 禁用：霓虹") || len(prompt.Truncated) != 0 {
		t.Fatalf("分镜提示词带风格锚：%s", prompt.Text)
	}
}

func TestPrevizGateEnabledAndBrief(t *testing.T) {
	for _, c := range []struct {
		setting, auth string
		want          bool
	}{{"", AuthStep, true}, {"", "", true}, {PrevizGateAuto, AuthFull, false}, {PrevizGateOn, AuthFull, true}, {PrevizGateOff, AuthStep, false}} {
		if got := PrevizGateEnabled(c.setting, c.auth); got != c.want {
			t.Fatalf("%q × %q：%v", c.setting, c.auth, got)
		}
	}
	if _, err := ParseBrief([]byte(`{"previz_gate": "on"}`)); err != nil {
		t.Fatalf("previz_gate 是需求文档字段：%v", err)
	}
	if _, err := ParseBrief([]byte(`{"previz_gate": "maybe"}`)); err == nil || !strings.Contains(err.Error(), "previz_gate") {
		t.Fatalf("previz_gate 只能是 auto / on / off：%v", err)
	}
}
