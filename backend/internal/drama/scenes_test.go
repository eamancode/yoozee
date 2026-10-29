package drama

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sceneIDs(scenes []Scene) []string {
	out := make([]string, 0, len(scenes))
	for _, sc := range scenes {
		out = append(out, sc.ID+" "+sc.Heading)
	}
	return out
}

func TestSplitScenesMergesConsecutiveSameLocation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(onlineDir, "chapter.md"))
	if err != nil {
		t.Fatal(err)
	}
	scenes := SplitScenes(string(body), "七床")
	if got := sceneIDs(scenes); !slices.Equal(got, []string{"E01S01 内·病房走廊·夜", "E01S02 内·电梯口·夜"}) {
		t.Fatalf("七床第 1 集 3 个场次头应并成 2 场：%v", got)
	}
	first, second := scenes[0], scenes[1]
	if first.Location != "病房走廊" || first.InOut != "内" || !slices.Equal(first.Times, []string{"夜"}) || first.Lines != [2]int{1, 11} || !slices.Equal(first.LegacyNos, []int{1}) {
		t.Fatalf("第 1 场：%+v", first)
	}
	if len(second.Sources) != 2 || second.Sources[1].Line != 30 || second.Lines != [2]int{12, 42} || !slices.Equal(second.LegacyNos, []int{2, 3}) {
		t.Fatalf("第 2 场应并了两个场次头、行 12–42：%+v", second)
	}
	if slices.Contains(second.Body, "## 内·电梯口·夜") || len(second.Body) != 29 {
		t.Fatalf("正文行不含场次头：%d 行", len(second.Body))
	}
	if !ScenesRenumbered(scenes) || SceneSourceLabel(second) == "" || SceneSourceLabel(first) != "" {
		t.Fatal("并场标记不对")
	}
	if !LegacySceneCurrent(scenes, 1) || !LegacySceneCurrent(scenes, 2) || LegacySceneCurrent(scenes, 3) || LegacySceneCurrent(scenes, 4) {
		t.Fatal("旧第 1、2 场编号不变，旧第 3 场并进了第 2 场（编号错位），旧第 4 场不存在")
	}
}

func TestSplitScenesKeepsNonAdjacentSameLocation(t *testing.T) {
	text := "## 内·走廊·夜\n（走。）\n## 内·电梯口·夜\n（等。）\n## 内·走廊·夜\n（回。）"
	if got := sceneIDs(SplitScenes(text, "t")); !slices.Equal(got, []string{"E01S01 内·走廊·夜", "E01S02 内·电梯口·夜", "E01S03 内·走廊·夜"}) {
		t.Fatalf("不相邻的同地点不合并：%v", got)
	}
}

func TestSplitScenesSingleHeadingAndNoHeading(t *testing.T) {
	one := SplitScenes("## 外·天台·日\n张三：风好大。", "第 3 集")
	if len(one) != 1 || one[0].Heading != "外·天台·日" || one[0].Location != "天台" || one[0].Lines != [2]int{1, 2} {
		t.Fatalf("只有一个场次头是 1 场：%+v", one)
	}
	none := SplitScenes("没有场次头的小说正文。\n\n第二段。", "第一章")
	if len(none) != 1 || none[0].ID != "E01S01" || none[0].Heading != "第一章" || len(none[0].Sources) != 0 || none[0].Lines != [2]int{1, 2} {
		t.Fatalf("没有场次头的原文整章一场：%+v", none)
	}
	if ScenesRenumbered(none) || !LegacySceneCurrent(none, 1) {
		t.Fatal("整章一场没有并场")
	}
	if SplitScenes(" \n\n", "空") != nil {
		t.Fatal("空正文没有场")
	}
}

func TestSplitScenesMergeRecordsTimeChange(t *testing.T) {
	text := "（开场字幕。）\n【场景1】电梯口｜内｜夜\n（门开。）\n## 内·电梯口 ·晨\n（天亮了。）\n第3场\n（黑场。）\n场 4\n（再黑场。）"
	scenes := SplitScenes(text, "t")
	if got := sceneIDs(scenes); !slices.Equal(got, []string{"E01S01 未命名场", "E01S02 内·电梯口·夜→晨", "E01S03 第3场", "E01S04 场 4"}) {
		t.Fatalf("开场单独一场；技能写法与 ## 写法同地点合并、时间变化写进场次头；读不出地点的不合并：%v", got)
	}
	if !slices.Equal(scenes[1].Times, []string{"夜", "晨"}) || !slices.Equal(scenes[1].LegacyNos, []int{2, 3}) || !slices.Equal(scenes[3].LegacyNos, []int{5}) {
		t.Fatalf("时间与旧场序：%+v", scenes)
	}
}

func TestParseSceneHeading(t *testing.T) {
	cases := []struct{ line, inOut, location, time string }{
		{"## 内·病房走廊·夜", "内", "病房走廊", "夜"},
		{"外.医院门口.日", "外", "医院门口", "日"},
		{"第一场 内·客厅·夜", "内", "客厅", "夜"},
		{"【场景2】住院部·七楼走廊｜内｜深夜", "内", "住院部·七楼走廊", "深夜"},
		{"## 内外·车里·黄昏（雨）", "内外", "车里", "黄昏（雨）"},
		{"场 3：走廊（夜）", "", "走廊", "夜"},
		{"INT. HOSPITAL CORRIDOR - NIGHT", "内", "HOSPITAL CORRIDOR", "NIGHT"},
		{"第3场", "", "", ""},
	}
	for _, c := range cases {
		h := ParseSceneHeading(c.line)
		if h.InOut != c.inOut || h.Location != c.location || h.Time != c.time {
			t.Errorf("%s → %q %q %q", c.line, h.InOut, h.Location, h.Time)
		}
	}
	if SceneLocationKey("ＡＢ 走廊") != SceneLocationKey("ab走廊") || SceneLocationKey("病房·走廊") != SceneLocationKey("病房走廊") {
		t.Fatal("地点去空白、全角转半角、不分大小写")
	}
}

func TestSceneConsumersShareSplit(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(onlineDir, "chapter.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	turns := ChapterShotsScenes(text, "七床")
	if len(turns) != 2 || turns[1].ID != "E01S02" || len(turns[1].SourceHeadings) != 2 || turns[1].Opening == "" {
		t.Fatalf("生成分段 2 轮：%+v", turns)
	}
	if plan := ChapterShotsTurnPlan(turns); !strings.Contains(plan, "共 2 场") || !strings.Contains(plan, "连续 2 个同地点的场次头") {
		t.Fatalf("分回合说明：%s", plan)
	}
	if style := ScenePaletteScenes(text, "七床", 300); len(style) != 2 || style[1].No != 2 || style[1].Heading != "内·电梯口·夜" || !strings.Contains(style[1].Excerpt, "周岸：小满") {
		t.Fatalf("导演风格 2 场：%+v", style)
	}
	if plan := PlanSections(text, "七床", mustShotGenre(ShotGenreWenxi30), 30, nil); len(plan.Sections) == 0 || plan.Sections[len(plan.Sections)-1].Scene != "E01S02" {
		t.Fatalf("小节按 2 场切：%+v", plan.Sections)
	}
	if script := ScriptFromText(text, "七床"); len(script.Scenes) != 2 {
		t.Fatalf("台词表 2 场：%d", len(script.Scenes))
	}
	if ChapterShotsScenes("## 内·走廊·夜\n（走。）\n## 内·走廊·夜\n（还在走。）", "t") != nil {
		t.Fatal("并成一场后整章一轮写完")
	}
}

func TestRenumberColorScriptAlignsLegacyByLocation(t *testing.T) {
	text := "## 内·走廊·夜\n（走。）\n## 内·电梯口·夜\n（等。）\n## 内·电梯口·夜\n（打。）\n## 外·天台·夜\n（风。）"
	episodes := map[int]SceneRenumberEpisode{1: {UnitID: "ep1", Scenes: SplitScenes(text, "t")}, 2: {UnitID: "ep2", Scenes: SplitScenes("## 内·厨房·日\n（煮。）", "t2")}}
	colors := []PaletteColor{{NameZh: "灰", Hex: "#808080"}}
	entries := []ColorScriptEntry{
		{Episode: 1, SceneNo: 4, Scene: "外·天台·夜", InheritsFrom: "E01S03", SharedColors: colors},
		{Episode: 1, SceneNo: 1, Scene: "内·走廊·夜", NewColors: colors},
		{Episode: 1, SceneNo: 2, Scene: "内·电梯口·夜", InheritsFrom: "E01S01", NewColors: colors},
		{Episode: 1, SceneNo: 3, Scene: "内·电梯口·夜", InheritsFrom: "E01S02", NewColors: colors},
		{Episode: 2, SceneNo: 1, Scene: "内·厨房·日", SceneRule: SceneRuleMerged, UnitID: "ep2"},
		{Episode: 2, SceneNo: 2, Scene: "内·客厅·日"},
		{Episode: 1, SceneNo: 5, Scene: "外·街口·夜"},
	}
	out, moves := RenumberColorScript(entries, episodes)
	codes := make([]string, 0, len(out))
	for _, e := range out {
		codes = append(codes, e.Code()+"<"+e.InheritsFrom)
	}
	if !slices.Equal(codes, []string{"E01S01<", "E01S02<E01S01", "E01S03<E01S02", "E01S05<", "E02S01<", "E02S02<"}) {
		t.Fatalf("旧第 3 场并进第 2 场去掉、旧第 4 场挪到第 3 场并跟着改承接；找不到的原样保留：%v", codes)
	}
	byFrom := map[string]SceneMove{}
	for _, m := range moves {
		byFrom[m.From] = m
	}
	if m := byFrom["E01S03"]; !m.Dropped || m.To != "E01S02" || m.Note == "" {
		t.Fatalf("冲突记 note：%+v", m)
	}
	if m := byFrom["E01S04"]; !m.Moved || m.To != "E01S03" {
		t.Fatalf("旧第 4 场 → 第 3 场：%+v", m)
	}
	if m := byFrom["E01S05"]; m.Moved || m.Note == "" || byFrom["E02S02"].Note == "" {
		t.Fatalf("找不到的场记 note、原样保留：%+v", moves)
	}
	if _, ok := byFrom["E02S01"]; ok {
		t.Fatal("新规则写的条目不动")
	}
	if out[2].SceneRule != SceneRuleMerged || out[2].UnitID != "ep1" || out[2].Scene != "外·天台·夜" {
		t.Fatalf("挪过的条目补上规则标记和单元：%+v", out[2])
	}
	mismatch, moved := RenumberColorScript([]ColorScriptEntry{{Episode: 1, SceneNo: 3, Scene: "内·厨房·夜"}}, episodes)
	if mismatch[0].SceneNo != 3 || mismatch[0].SceneRule != 0 || !strings.Contains(moved[0].Note, "地点或内外对不上") {
		t.Fatalf("地点或内外对不上原样保留：%+v %+v", mismatch, moved)
	}
}

// 旧规则的台词表从同一次切场拆回来，与 Python from-md（golden）逐字一致：旧镜头表重装段、重评测仍按旧编号对台词。
func TestLegacyScriptFromTextMatchesPython(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(onlineDir, "chapter.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := LegacyScriptFromText(string(body), "七床")
	var want Script
	readJSONFile(t, filepath.Join(goldenDir, "online-script.json"), &want)
	diffJSON(t, "legacy script from-md", got, want)
	if legacy := LegacyScriptFromText("（开场。）\n## 内·走廊·夜\n张三：走。\n## 内·走廊·晨\n李四：到。", "t"); len(legacy.Scenes) != 3 || legacy.Scenes[2].ID != "E01S03" || legacy.Scenes[2].Beats[0].ID != "E01S03L01" || legacy.Scenes[0].Heading != UntitledSceneHeading {
		t.Fatalf("开场一场、并起来的两场拆回去：%+v", legacy.Scenes)
	}
}

// 并场要求地点和内外都相同，时间可以不同：门口外接门口内是两场；同是内景、夜接晨是一场。
func TestSplitScenesRequiresSameInOut(t *testing.T) {
	split := SplitScenes("## 外·医院门口·夜\n（雨。）\n## 内·医院门口·夜\n（进门。）", "t")
	if got := sceneIDs(split); !slices.Equal(got, []string{"E01S01 外·医院门口·夜", "E01S02 内·医院门口·夜"}) {
		t.Fatalf("内外不同的同地点不合并：%v", got)
	}
	merged := SplitScenes("## 内·医院门口·夜\n（等。）\n## 内·医院门口·晨\n（天亮。）", "t")
	if got := sceneIDs(merged); !slices.Equal(got, []string{"E01S01 内·医院门口·夜→晨"}) || !slices.Equal(merged[0].Times, []string{"夜", "晨"}) {
		t.Fatalf("内外相同、时间不同合并成一场：%v %+v", got, merged)
	}
	if got := sceneIDs(SplitScenes("第1场 医院门口\n（等。）\n第2场 医院门口 夜\n（还在等。）", "t")); len(got) != 1 {
		t.Fatalf("都没写内外、地点相同合并：%v", got)
	}
}

func mustShotGenre(id string) ShotGenre {
	g, _ := ShotGenreByID(id)
	return g
}
