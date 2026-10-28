package drama

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func readSectionSample(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/sections/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func rowSpec(t *testing.T, row map[string]any) Shot {
	t.Helper()
	encoded, _ := json.Marshal(row["spec"])
	sh, unknown, err := ParseShot(encoded)
	if err != nil || len(unknown) > 0 {
		t.Fatalf("spec 解析：%v %v", err, unknown)
	}
	return sh
}

func qichuangSection(t *testing.T, genre string, id string) Section {
	t.Helper()
	g, _ := ShotGenreByID(genre)
	plan := PlanSections(qichuangChapter(t), "第一集", g, int(g.MaxSeconds))
	sec, ok := plan.Section(id)
	if !ok {
		t.Fatalf("没有小节 %s", id)
	}
	return sec
}

// 卡 0 文档抄的 7.3 原样示例（同场连续两段）：两个片段 8 镜，片段级站位、光、SFX 下沉到镜头，BGM 不进可编译字段。
func TestConvertWenxi15Card0Example(t *testing.T) {
	ctx := SectionContext{Section: Section{ID: "E01S01-P1", Scene: "E01S01", Genre: ShotGenreWenxi15}, Characters: []string{"甲", "乙"}, Places: []string{"庭院"}, Props: []string{"石桌", "茶盏"}}
	conv, err := ConvertSection(ShotGenreWenxi15, readSectionSample(t, "card0-wenxi15-example.md"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if conv.Stats.Blocks != 2 || len(conv.Rows) != 8 {
		t.Fatalf("应 2 段 8 镜：%+v", conv.Stats)
	}
	var shots []Shot
	total := 0.0
	for _, row := range conv.Rows {
		sh := rowSpec(t, row)
		shots = append(shots, sh)
		total += float64(sh.Est)
		for _, field := range []string{sh.Sfx, sh.Voice, sh.Action} {
			if containsAny(field, bgmWords...) {
				t.Fatalf("%s 的可编译字段里有配乐字样：%q", sh.ID, field)
			}
		}
		if sh.Section != "E01S01-P1" || sh.Genre != ShotGenreWenxi15 || sh.Scene != "E01S01" {
			t.Fatalf("小节字段：%+v", sh)
		}
	}
	if total != 23 {
		t.Fatalf("两段合计应为 12+11 秒，得 %.1f", total)
	}
	if shots[0].ID != "E01S01-01" || shots[7].ID != "E01S01-08" {
		t.Fatalf("镜头 id：%s %s", shots[0].ID, shots[7].ID)
	}
	if shots[0].Size != "全景" || shots[0].Cam.Z != "Z7" || shots[0].Cam.F == nil || shots[0].Cam.F.Dof != "deep" {
		t.Fatalf("S1 景别与景深：%+v %+v", shots[0].Size, shots[0].Cam)
	}
	if shots[1].Cam.X != "X2" || shots[1].Cam.F == nil || shots[1].Cam.F.Identity != "ots" {
		t.Fatalf("S2 过肩：%+v", shots[1].Cam)
	}
	if shots[3].Size != "特写" || !slicesContains(shots[3].Move, "push") {
		t.Fatalf("S4 特写推近：%s %v", shots[3].Size, shots[3].Move)
	}
	if shots[5].Size != "特写" || shots[5].Subject != "non_person" {
		t.Fatalf("片段 2 S2 茶盏特写：%s %s", shots[5].Size, shots[5].Subject)
	}
	if float64(shots[4].Est) != 3 {
		t.Fatalf("同场后续段 S1 从 0.5 秒起，首帧锁并进第一镜：%.1f", float64(shots[4].Est))
	}
	if shots[0].Light == nil || !strings.Contains(shots[0].Light.SourceAnchor, "主光源") || !strings.Contains(shots[0].Light.Key, "深秋斜阳") {
		t.Fatalf("光源下沉：%+v", shots[0].Light)
	}
	if shots[0].Sfx == "" || shots[1].Sfx != "" || !strings.Contains(shots[0].StartState, "石桌左侧") || !strings.Contains(shots[3].EndState, "茶盏") || !strings.Contains(shots[4].StartState, "逐项复制片段1-S4") {
		t.Fatalf("SFX 写第一镜、起止状态：%q %q %q %q", shots[0].Sfx, shots[0].StartState, shots[3].EndState, shots[4].StartState)
	}
	if len(shots[0].Blocking) != 2 || shots[0].Blocking[0].Who != "甲" {
		t.Fatalf("没点名人物的镜头 who 取全部角色：%+v", shots[0].Blocking)
	}
	if conv.Stats.MusicStripped < 2 {
		t.Fatalf("两段的 BGM 都应计入剥除：%+v", conv.Stats)
	}
	assets, _ := conv.Rows[0]["assets"].([]any)
	if len(assets) < 3 {
		t.Fatalf("行 assets 应带角色、场景、道具：%v", assets)
	}
}

// 后加规则的镜头行（带风格、核心情绪标签，台词写「无」）：标签进 spec.extras，起止景别不同的景别取起始。
func TestConvertWenxi15TaggedLine(t *testing.T) {
	raw := "## 片段 1（时长：2秒）\n\n" + readSectionSample(t, "card0-wenxi15-tagged-line.md")
	conv, err := ConvertSection(ShotGenreWenxi15, raw, SectionContext{Section: Section{ID: "E01S01-P1", Scene: "E01S01", Genre: ShotGenreWenxi15}})
	if err != nil {
		t.Fatal(err)
	}
	sh := rowSpec(t, conv.Rows[0])
	if sh.Size != "特写" || sh.Extras["风格"] != "硬核打斗" || sh.Extras["核心情绪"] != "紧张" || len(sh.Lines) != 0 {
		t.Fatalf("%+v", sh)
	}
	if !strings.HasPrefix(sh.Action, "夹克男侧身迎敌") || strings.Contains(sh.Action, "风格") || strings.Contains(sh.Action, "无") {
		t.Fatalf("动作：%q", sh.Action)
	}
}

// 七床 E01S01-P2 的文戏 15 输出：两句台词各落一镜，画外持续的续镜合回第一镜，台词表全部写到。
func TestConvertWenxi15QichuangLines(t *testing.T) {
	sec := qichuangSection(t, ShotGenreWenxi15, "E01S01-P2")
	ctx := SectionContext{Section: sec, ShotOffset: 4, Characters: []string{"林小满", "周岸"}, Places: []string{"病房走廊"}, Props: []string{"病历夹"}, Anchors: []string{"日光灯", "七床门牌"}, Indoor: true}
	conv, err := ConvertSection(ShotGenreWenxi15, readSectionSample(t, "qichuang-e01s01-p2.wenxi15.md"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if conv.Stats.Lines != 2 || conv.Stats.LineMerged != 1 || conv.Stats.LineMissing != 0 || conv.Stats.LineUnmatched != 0 {
		t.Fatalf("台词统计：%+v", conv.Stats)
	}
	ids := map[string]int{}
	for _, row := range conv.Rows {
		sh := rowSpec(t, row)
		for _, ln := range sh.Lines {
			ids[ln.LineID]++
		}
		if sh.Light == nil || sh.Light.SourceAnchor != "日光灯" {
			t.Fatalf("%s 室内光源锚点：%+v", sh.ID, sh.Light)
		}
		if containsAny(sh.Sfx, bgmWords...) {
			t.Fatalf("SFX 里的背景音乐没剥掉：%q", sh.Sfx)
		}
	}
	if ids["E01S01L02"] != 1 || ids["E01S01L03"] != 1 {
		t.Fatalf("每句只落一镜：%v", ids)
	}
	if first := rowSpec(t, conv.Rows[0]); first.ID != "E01S01-05" {
		t.Fatalf("镜头 id 接着本场前面的镜头编：%s", first.ID)
	}
	if conv.Rows[0]["dialogue"] != "林小满：七床的人呢？" {
		t.Fatalf("行 dialogue：%v", conv.Rows[0]["dialogue"])
	}
}

// 卡 0 文档抄的即梦原样示例（小节 1 镜头 1）：19 字段一对一进 spec，台词四元落三元，声音里的「禁止BGM/配乐」剥掉。
func TestConvertWenxi30Card0Example(t *testing.T) {
	beats := []SectionBeat{{ID: "E01S01L01", Kind: "line", Speaker: "林渊", Text: "你们的速度比我想象的慢。"}}
	ctx := SectionContext{Section: Section{ID: "E01S01-P1", Scene: "E01S01", Genre: ShotGenreWenxi30, Beats: beats}, Characters: []string{"林渊", "追兵"}, Anchors: []string{"斑驳墙", "巷口红蓝霓虹"}}
	conv, err := ConvertSection(ShotGenreWenxi30, readSectionSample(t, "card0-wenxi30-example.txt"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if conv.Stats.Blocks != 1 || len(conv.Rows) != 1 || conv.Stats.Lines != 1 || conv.Stats.LineMissing != 0 {
		t.Fatalf("%+v", conv.Stats)
	}
	sh := rowSpec(t, conv.Rows[0])
	if sh.Size != "远景" || sh.Cam.Y != "Y2" || sh.Cam.X != "X1" || sh.Cam.F == nil || sh.Cam.F.Dof != "deep" || sh.Cam.F.LensMM == nil || *sh.Cam.F.LensMM != 24 {
		t.Fatalf("画面与镜头：%s %+v", sh.Size, sh.Cam)
	}
	if len(sh.Move) != 1 || sh.Move[0] != "fixed" || float64(sh.Est) != 9 {
		t.Fatalf("运镜与估时（唯一一镜到小节末）：%v %.1f", sh.Move, float64(sh.Est))
	}
	if len(sh.Lines) != 1 || sh.Lines[0].LineID != "E01S01L01" || sh.Lines[0].Pace != "中偏慢" || sh.Lines[0].Tone != "尾音下沉" || sh.Lines[0].Emotion != "克制" {
		t.Fatalf("台词：%+v", sh.Lines)
	}
	if containsAny(sh.Sfx, bgmWords...) || !strings.Contains(sh.Sfx, "雨声持续") {
		t.Fatalf("声音：%q", sh.Sfx)
	}
	l := sh.Light
	if l == nil || l.SourceAnchor != "巷口红蓝霓虹" || !strings.HasPrefix(l.Key, "巷口红蓝霓虹") || l.Fill == "" || l.Rim == "" || l.Ratio != "高反差" || l.Kelvin != "冷暖对冲" || l.Shadow == "" || l.Exposure == "" || l.Change == "" {
		t.Fatalf("灯光：%+v", l)
	}
	if sh.Transition == nil || sh.Transition.CutPoint != "视线" || sh.Purpose != "建立空间与人物站位。" || sh.Performance == nil || len(sh.Performance.Beats) != 2 {
		t.Fatalf("衔接、目的、表情：%+v %q %+v", sh.Transition, sh.Purpose, sh.Performance)
	}
	if len(sh.Blocking) != 2 || sh.Blocking[0].Who != "林渊" || sh.Blocking[0].At != "斑驳墙" || sh.Blocking[1].At != "巷口红蓝霓虹" {
		t.Fatalf("站位：%+v", sh.Blocking)
	}
	if sh.Extras["场景锚点"] == "" || sh.StartState == "" || sh.EndState == "" {
		t.Fatalf("锚点进 extras、起止画面：%+v", sh)
	}
	if conv.Rows[0]["plotDescription"] == "" || !strings.Contains(conv.Rows[0]["plotDescription"].(string), "远景") {
		t.Fatalf("行画面：%v", conv.Rows[0]["plotDescription"])
	}
}

// 七床 E01S01 的文戏 30 输出：三句台词各落一镜，小节尾和粘贴提示词不当镜头读，粘贴提示词里的「声音：」不覆盖镜头声音。
func TestConvertWenxi30QichuangLines(t *testing.T) {
	sec := qichuangSection(t, ShotGenreWenxi30, "E01S01-P1")
	ctx := SectionContext{Section: sec, Characters: []string{"林小满", "周岸"}, Places: []string{"病房走廊"}, Anchors: []string{"日光灯", "七床门牌", "床头阅读灯"}, Indoor: true}
	conv, err := ConvertSection(ShotGenreWenxi30, readSectionSample(t, "qichuang-e01s01-p1.wenxi30.txt"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Rows) != 3 || conv.Stats.Lines != 3 || conv.Stats.LineMissing != 0 || conv.Stats.NoLight != 0 {
		t.Fatalf("%+v", conv.Stats)
	}
	total := 0.0
	for i, row := range conv.Rows {
		sh := rowSpec(t, row)
		total += float64(sh.Est)
		if len(sh.Lines) != 1 || containsAny(sh.Sfx, bgmWords...) {
			t.Fatalf("第 %d 镜：%+v %q", i+1, sh.Lines, sh.Sfx)
		}
		if strings.Contains(sh.Sfx, "脚步声、日光灯低鸣") && i == 2 {
			t.Fatal("粘贴提示词的声音串进了最后一镜")
		}
	}
	if total != 20 {
		t.Fatalf("合计应为 20 秒：%.1f", total)
	}
	third := rowSpec(t, conv.Rows[2])
	if third.Size != "特写" || !slicesContains(third.Move, "push") || third.Cam.Y != "Y6" || third.Light.SourceAnchor != "床头阅读灯" {
		t.Fatalf("第 3 镜：%s %v %+v %+v", third.Size, third.Move, third.Cam, third.Light)
	}
	var tail []string
	for _, f := range conv.Fields {
		tail = append(tail, f.Name)
	}
	if !slicesContains(tail, "即梦粘贴提示词") || !slicesContains(tail, "剧情功能") {
		t.Fatalf("小节头尾进原样字段：%v", tail)
	}
}

func TestConvertSectionNoShots(t *testing.T) {
	if _, err := ConvertSection(ShotGenreWenxi15, "这一节我先解释一下思路……", SectionContext{}); err == nil {
		t.Fatal("读不出镜头应报错")
	}
	if _, err := ConvertSection(ShotGenreWuxi, "镜 1｜t0-1s", SectionContext{}); err == nil {
		t.Fatal("武戏还没有转换器")
	}
}

func TestSectionMovesWhip(t *testing.T) {
	if got := sectionMoves("甩镜接特写"); len(got) != 1 || got[0] != "whip" {
		t.Fatalf("%v", got)
	}
	if got := sectionMoves("固定机位，缓推"); len(got) != 1 || got[0] != "push" {
		t.Fatalf("fixed 不与别的同列：%v", got)
	}
	if got := sectionMoves("推近后拉远"); len(got) != 1 || got[0] != "push" {
		t.Fatalf("push / pull 只留先出现的：%v", got)
	}
}

func slicesContains[T comparable](items []T, v T) bool {
	for _, it := range items {
		if it == v {
			return true
		}
	}
	return false
}
