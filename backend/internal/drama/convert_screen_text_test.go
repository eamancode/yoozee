package drama

import (
	"strings"
	"testing"
)

// M8 线上验收第 5 条：7.3 输出里「手机屏幕清晰显示画面文字：“…”」被当成台词（说话人「手机屏幕清晰显示画面文字」），
// 对不上剧本触发 R-SHOT-001，报错里还带 None。画面文字留在该镜画面动作里、记进 extras，不当台词；真台词照常落镜。
func TestConvertWenxi15ScreenTextNotLine(t *testing.T) {
	beats := []SectionBeat{
		{ID: "E01S01L07", Kind: "line", Speaker: "周桐", Text: "又是房东？"},
		{ID: "E01S01L08", Kind: "line", Speaker: "林小满", Text: "嗯，催房租。"},
		{ID: "E01S01A09", Kind: "action", Text: "画面文字：明早八点前把房租交了，不然别怪我换锁。"},
		{ID: "E01S01L10", Kind: "line", Speaker: "林小满", Text: "我明天就去找工作。"},
	}
	ctx := SectionContext{Section: Section{ID: "E01S01-P4", Scene: "E01S01", Genre: ShotGenreWenxi15, Beats: beats}, ShotOffset: 20, Characters: []string{"林小满", "周桐"}, Places: []string{"老街出租屋"}, Props: []string{"手机", "泡面"}, Indoor: true}
	conv, err := ConvertSection(ShotGenreWenxi15, readSectionSample(t, "online-m8-e01s01-p4.wenxi15.md"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Rows) != 6 || conv.Stats.Lines != 3 || conv.Stats.LineUnmatched != 0 || conv.Stats.LineMissing != 0 {
		t.Fatalf("6 镜 3 句台词全部对上：%d 镜 %+v", len(conv.Rows), conv.Stats)
	}
	var shots []Shot
	for _, row := range conv.Rows {
		sh := rowSpec(t, row)
		shots = append(shots, sh)
		for _, ln := range sh.Lines {
			if strings.Contains(ln.Text, "房租交了") || strings.Contains(ln.Speaker, "画面文字") {
				t.Fatalf("%s 画面文字被当成台词：%+v", sh.ID, ln)
			}
		}
	}
	s5 := shots[4]
	if s5.ID != "E01S01-25" || len(s5.Lines) != 0 {
		t.Fatalf("S5 不应有台词：%s %+v", s5.ID, s5.Lines)
	}
	if !strings.Contains(s5.Action, "手机屏幕清晰显示画面文字：“明早八点前把房租交了，不然别怪我换锁。”") || !strings.Contains(s5.Action, "拇指停在屏幕上方") {
		t.Fatalf("画面文字应留在 S5 画面动作里：%q", s5.Action)
	}
	if s5.Extras["画面文字"] != "明早八点前把房租交了，不然别怪我换锁。" || s5.Extras["核心情绪"] != "窒息" {
		t.Fatalf("S5 extras：%+v", s5.Extras)
	}
	if s5.Size != "大特写" || !slicesContains(s5.Move, "push") {
		t.Fatalf("S5 镜头语言：%s %v", s5.Size, s5.Move)
	}
	if len(shots[1].Lines) != 1 || shots[1].Lines[0].LineID != "E01S01L07" || len(shots[5].Lines) != 1 || shots[5].Lines[0].LineID != "E01S01L10" {
		t.Fatalf("真台词照常落镜：%+v %+v", shots[1].Lines, shots[5].Lines)
	}

	script := &Script{Scenes: []ScriptScene{{ID: "E01S01", Characters: []string{"林小满", "周桐"}, Beats: []ScriptBeat{
		{ID: "E01S01L07", Kind: "line", Speaker: "周桐", Text: "又是房东？"},
		{ID: "E01S01L08", Kind: "line", Speaker: "林小满", Text: "嗯，催房租。"},
		{ID: "E01S01A09", Kind: "action", Text: "画面文字：明早八点前把房租交了，不然别怪我换锁。"},
		{ID: "E01S01L10", Kind: "line", Speaker: "林小满", Text: "我明天就去找工作。"},
	}}}}
	report := CheckShots(Shotlist{Shots: shots}, CheckContext{Script: script})
	for _, item := range report.Items {
		if item.Rule == "R-SHOT-001" {
			t.Fatalf("不应有 R-SHOT-001：%+v", item)
		}
		if strings.Contains(item.Message, "None") {
			t.Fatalf("报错文案带 None：%+v", item)
		}
	}
}

// 冒号前是角色名的照常是台词，哪怕台词里说到「屏幕」「短信」；角色名本身不会被当成画面文字。
func TestScreenTextSpeaker(t *testing.T) {
	chars := []string{"林小满", "周桐·西装", "字幕组长"}
	for _, c := range []struct {
		speaker string
		want    bool
	}{
		{"手机屏幕清晰显示画面文字", true},
		{"画面文字", true},
		{"屏幕显示", true},
		{"字幕", true},
		{"纸条上写着", true},
		{"短信", true},
		{"林小满", false},
		{"周桐", false},
		{"字幕组长", false},
		{"旁白", false},
		{"", false},
	} {
		if got := screenTextSpeaker(c.speaker, chars); got != c.want {
			t.Fatalf("%q：%v", c.speaker, got)
		}
	}
}

// 即梦 / 武戏的台词格里写了画面文字（「手机屏幕：“…”」）也不当台词，内容进 extras；同格的真台词保留。
func TestConvertScreenTextInLineField(t *testing.T) {
	ctx := SectionContext{Characters: []string{"林小满"}}
	lines, screens := wenxi30Lines("手机屏幕：“明早八点前交房租”【无】；林小满：“我知道了。”【音色（女声）+语速（慢）+语调（平）+情感（压抑）】", ctx)
	if len(lines) != 1 || lines[0].speaker != "林小满" || len(screens) != 1 || screens[0] != "明早八点前交房租" {
		t.Fatalf("即梦：%+v %v", lines, screens)
	}
	lines, screens = wuxiLines("短信：“快跑”；林小满（低声）：“走！”", ctx)
	if len(lines) != 1 || lines[0].text != "走！" || len(screens) != 1 || screens[0] != "快跑" {
		t.Fatalf("武戏：%+v %v", lines, screens)
	}
	lines, screens = wuxiLines("屏幕显示：倒计时三秒", ctx)
	if len(lines) != 0 || len(screens) != 1 || screens[0] != "倒计时三秒" {
		t.Fatalf("武戏不加引号：%+v %v", lines, screens)
	}
	var sh Shot
	addScreenText(&sh, []string{"甲"})
	addScreenText(&sh, []string{"乙"})
	if sh.Extras["画面文字"] != "甲；乙" {
		t.Fatalf("extras 画面文字：%+v", sh.Extras)
	}
}

// 报错文案里的空值换成中文说明，不再打印 Python 的 None。
func TestShotCheckMessagesNoNone(t *testing.T) {
	script := &Script{Scenes: []ScriptScene{{ID: "E01S01", Beats: []ScriptBeat{{ID: "E01S01L01", Kind: "line", Speaker: "林", Text: "七床的人呢？"}}}}}
	shotlist := Shotlist{Shots: []Shot{
		{ID: "E01S01-01", Scene: "E01S01", Lines: []ShotLine{{LineID: "E01S01L01", Text: "七床的人呢？"}}},
		{ID: "E01S01-02", Scene: "E01S01", Lines: []ShotLine{{Speaker: "林", Text: "这句剧本里没有。", Pace: "快"}}},
		{ID: "E01S01-03", Scene: "E01S01"},
		{ID: "E01S01-04", Scene: "E01S01"},
		{ID: "E01S01-05", Scene: "E01S01"},
	}}
	report := CheckShots(shotlist, CheckContext{Script: script})
	found := false
	for _, item := range report.Items {
		if strings.Contains(item.Message, "None") {
			t.Fatalf("报错文案带 None：%+v", item)
		}
		if item.Rule == "R-SHOT-001" && item.ShotID == "E01S01-02" {
			found = true
			if !strings.Contains(item.Message, "这句剧本里没有") {
				t.Fatalf("没对上的台词应写出原文：%q", item.Message)
			}
		}
	}
	if !found {
		t.Fatalf("没对上剧本的台词应报 R-SHOT-001：%+v", report.Items)
	}
}
