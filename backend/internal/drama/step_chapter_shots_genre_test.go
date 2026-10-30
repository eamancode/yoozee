package drama

import (
	"strings"
	"testing"
)

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
	}{{"", RunEveryStep, true}, {"", "", true}, {PrevizGateAuto, RunUntilVideo, false}, {PrevizGateAuto, RunReviewFromAssets, true}, {PrevizGateOn, RunUntilVideo, true}, {PrevizGateOff, RunEveryStep, false}} {
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
