package drama

import (
	"os"
	"strings"
	"testing"
)

func qichuangChapter(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../app/testdata/qichuang-chapter-shots/chapter.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// 七床第一集：文戏 15 每节不超过 15 秒、最多两句台词（极短三句也行），不在问句和接话之间切；文戏 30 每节不超过 30 秒。
func TestPlanSectionsQichuang(t *testing.T) {
	text := qichuangChapter(t)
	for _, tc := range []struct {
		genre string
		max   int
	}{{ShotGenreWenxi15, 15}, {ShotGenreWenxi30, 30}} {
		g, _ := ShotGenreByID(tc.genre)
		plan := PlanSections(text, "第一集", g, tc.max, nil)
		if len(plan.Sections) == 0 {
			t.Fatalf("%s 没切出小节", tc.genre)
		}
		seen := map[string]bool{}
		covered := map[string]bool{}
		for _, s := range plan.Sections {
			t.Logf("%s %s %.1fs 行%v 台词%v %v", tc.genre, s.ID, s.Est, s.Lines, s.LineIDs, s.Warnings)
			if seen[s.ID] {
				t.Fatalf("小节编号重复：%s", s.ID)
			}
			seen[s.ID] = true
			if !strings.HasPrefix(s.ID, s.Scene+"-P") || s.Genre != tc.genre {
				t.Fatalf("小节编号或戏类不对：%+v", s)
			}
			if s.Est > plan.MaxSeconds && len(s.Beats) > 1 {
				t.Fatalf("%s 估时 %.1f 超过上限 %.0f", s.ID, s.Est, plan.MaxSeconds)
			}
			if tc.genre == ShotGenreWenxi15 && !linesFit(s.Beats, g) && len(s.Warnings) == 0 {
				t.Fatalf("%s 台词超额却没提醒", s.ID)
			}
			for _, id := range s.LineIDs {
				covered[id] = true
			}
		}
		script := ScriptFromText(text, "第一集")
		for _, sc := range script.Scenes {
			for _, b := range sc.Beats {
				if b.isLine() && !covered[b.ID] {
					t.Fatalf("%s 台词 %s 没落进任何小节", tc.genre, b.ID)
				}
			}
		}
		// 小节首尾接得上：同一场相邻两节的正文行连续。
		for i := 1; i < len(plan.Sections); i++ {
			a, b := plan.Sections[i-1], plan.Sections[i]
			if a.Scene == b.Scene && b.Beats[0].Line <= a.Beats[len(a.Beats)-1].Line {
				t.Fatalf("%s 与 %s 重叠", a.ID, b.ID)
			}
		}
	}
}

func TestSectionCutNotMidExchange(t *testing.T) {
	beats := []SectionBeat{
		{Kind: "line", Speaker: "林小满", Text: "你是七床？"},
		{Kind: "action", Text: "他抬起右手示意她小声"},
		{Kind: "line", Speaker: "周岸", Text: "别喊。"},
		{Kind: "line", Speaker: "林小满", Text: "病号服……你住院了？"},
		{Kind: "line", Speaker: "周岸", Text: "我不是来住院的。"},
	}
	for i, want := range []bool{false, false, true, false} {
		if got := sectionCutAllowed(beats, i); got != want {
			t.Fatalf("第 %d 拍后切：%v，应为 %v", i, got, want)
		}
	}
}

// 72 秒一场、上限 30 秒的文戏 30 切成三节，每节在 20～30 秒。
func TestPlanSectionsWenxi30Even(t *testing.T) {
	var lines []string
	lines = append(lines, "## 内·客厅·夜")
	for i := 0; i < 24; i++ {
		lines = append(lines, "（她走到窗边，拉上窗帘，转身坐下。）")
	}
	g, _ := ShotGenreByID(ShotGenreWenxi30)
	plan := PlanSections(strings.Join(lines, "\n"), "x", g, 30, nil)
	if len(plan.Sections) != 3 {
		t.Fatalf("应切成 3 节：%+v", plan.Sections)
	}
	for _, s := range plan.Sections {
		if s.Est < 20 || s.Est > 30 {
			t.Fatalf("%s 估时 %.1f 不在 20～30", s.ID, s.Est)
		}
	}
}

// 文戏 15 连着接话超过两句台词：不在接话中间切，整段落在一节里，只记提醒（warn），本节输出要求里说明台词句数。
func TestPlanSectionsWenxi15LongExchangeWarns(t *testing.T) {
	text := "## 内·诊室·夜\n林小满：你是谁？\n周岸：你不认识我了？\n林小满：我该认识你吗？\n周岸：你真的忘了？"
	g, _ := ShotGenreByID(ShotGenreWenxi15)
	plan := PlanSections(text, "t", g, 15, nil)
	if len(plan.Sections) != 1 || len(plan.Sections[0].LineIDs) != 4 {
		t.Fatalf("问句接话不拆：%+v", plan.Sections)
	}
	sec := plan.Sections[0]
	if len(sec.Warnings) != 1 || !strings.Contains(sec.Warnings[0], "4 句台词") {
		t.Fatalf("台词超额只记提醒：%v", sec.Warnings)
	}
	if rule := SectionTurnPrompt(plan, 1, "", nil, nil, ""); !strings.Contains(rule, "本节有 4 句台词") || !strings.Contains(rule, "按技能规则写在 10～15 秒") {
		t.Fatalf("本节输出要求：%s", rule)
	}
	if out := NewSectionOutput(sec, "", SectionShots{}); len(out.Warnings) != 1 {
		t.Fatal("原样输出记录带上提醒")
	}
}
