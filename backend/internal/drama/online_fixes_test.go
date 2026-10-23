package drama

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 线上复测（M4.1 第三期）：项目画风、每集时长、打斗句式与段末标点。

const suspenseStyleSpec = "【风格组合】题材世界：悬疑犯罪；叙事气质：暗黑诡谲；视觉媒介：真人实拍；角色造型：写实人物。以下四项必须同时成立。\n" +
	"【项目定位】面向“悬疑犯罪”题材的暗黑诡谲真人实拍项目。\n" +
	"【叙事气质】叙事气质危险、克制、带未知感；恐惧来自空间、规则和人物反应，不依赖血浆、全黑画面或持续尖叫。\n" +
	"【视觉媒介】采用真人影视拍摄媒介：真实人物比例、自然皮肤与发丝、可信镜头光学和现场光源。 成片保持高清可读的主体轮廓。\n" +
	"【项目色彩与光影】基础色彩来自炭灰、冷水泥、旧木、脏黄实景灯与少量危险强调色；本项目采用低调但可读的冷暖分区。\n" +
	"【全局禁用】黑成不可读、霓虹灯海、过量烟雾。"

func TestProjectStyleBlockTakesListedSectionsWithinLimit(t *testing.T) {
	cp := mustCompileProfile(nil)
	got := cp.ProjectStyleBlock(suspenseStyleSpec)
	if !strings.HasPrefix(got, "采用真人影视拍摄媒介") || !strings.Contains(got, "叙事气质危险") || !strings.Contains(got, "炭灰、冷水泥") {
		t.Fatalf("风格段应按 视觉媒介、叙事气质、项目色彩与光影 的顺序取：%s", got)
	}
	for _, leak := range []string{"【", "风格组合", "项目定位", "全局禁用", "霓虹"} {
		if strings.Contains(got, leak) {
			t.Fatalf("没列出的节和节名不进风格段（%s）：%s", leak, got)
		}
	}
	if n := utf8.RuneCountInString(got); n > cp.Blocks.ProjectStyle.MaxChars {
		t.Fatalf("风格段 %d 字，超过上限 %d", n, cp.Blocks.ProjectStyle.MaxChars)
	}
	tight, err := ParseCompileProfile([]byte(`{"blocks":{"project_style":{"max_chars":60}}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	short := tight.ProjectStyleBlock(suspenseStyleSpec)
	if n := utf8.RuneCountInString(short); n > 60 || !strings.HasSuffix(short, "。") || strings.Contains(short, "叙事气质") {
		t.Fatalf("超长时整节舍去、首节截到句末：%q（%d 字）", short, n)
	}
	if plain := cp.ProjectStyleBlock("胶片质感，暖调，手持跟拍"); plain != "胶片质感，暖调，手持跟拍。" {
		t.Fatalf("没分节的自定义画风用全文：%q", plain)
	}
	if cp.ProjectStyleBlock("  ") != "" {
		t.Fatal("没选画风返回空串，编译改用档案 style_block")
	}
}

func TestCompileUsesProjectStyleAsStyleBlock(t *testing.T) {
	seg := seg25Scene(t)
	m, _ := ModelByID("seedance-2.5")
	prof := testProfile(t, "live-realism")
	style := mustCompileProfile(nil).ProjectStyleBlock(suspenseStyleSpec)
	got, err := CompileWith(seg, m, &style, nil, prof, nil)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.SplitN(got.Prompt, "\n", 2)[0]
	if !strings.Contains(head, "整体风格："+style) || strings.Contains(head, prof.StyleBlock.Zh) {
		t.Fatalf("风格段应换成项目画风，档案 style_block 不再出现：\n%s", head)
	}
	if !strings.Contains(head, prof.ActingBlock.Zh) {
		t.Fatal("表演块仍取档案")
	}
}

func TestStyleMapCoversProjectStylePresets(t *testing.T) {
	sm, err := BuiltinStyleMap()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		style ProjectStyle
		want  string
	}{
		{ProjectStyle{World: "suspense", Medium: "live-action"}, "live-realism"},
		{ProjectStyle{World: "urban", Medium: "live-action"}, "live-realism"},
		{ProjectStyle{World: "historical", Medium: "live-action"}, "live-realism"},
		{ProjectStyle{World: "xianxia", Medium: "3d-anime"}, "anime-3d"},
		{ProjectStyle{World: "pastoral", Medium: "3d-cartoon"}, "anime-3d"},
		{ProjectStyle{World: "historical", Medium: "2d-guoman"}, "anime-3d"},
		{ProjectStyle{Title: "我的风格", Tags: []string{"3D", "国风"}}, "anime-3d"},
		{ProjectStyle{Title: "港风真人实拍"}, "live-realism"},
		{ProjectStyle{Title: "说不清"}, DefaultProfileID},
	}
	for _, c := range cases {
		if got, _ := sm.ProfileFor(c.style); got != c.want {
			t.Errorf("%+v → %s，应为 %s", c.style, got, c.want)
		}
	}
}

func TestEpisodeTargetSecondsAndSegmentTotal(t *testing.T) {
	ninety := 90.0
	brief := Brief{Duration: &BriefFact{Value: "全剧 10 集，每集约 2 分钟"}, EpisodeSeconds: &ninety, Episodes: []BriefEpisode{{No: 2, TargetSeconds: 60}}}
	if got := brief.EpisodeTargetSeconds(2); got != 60 {
		t.Fatalf("分集目标优先：%v", got)
	}
	if got := brief.EpisodeTargetSeconds(1); got != 90 {
		t.Fatalf("其次每集时长：%v", got)
	}
	brief.EpisodeSeconds = nil
	if got := brief.EpisodeTargetSeconds(1); got != 120 {
		t.Fatalf("最后读 duration 的「数字 + 单位」：%v", got)
	}
	segs := []Segment{{Scene: "E01S01", Dur: 60}, {Scene: "E01S02", Dur: 50}}
	if SegmentsEpisode(segs) != 1 {
		t.Fatal("集号取场号")
	}
	rules, err := BuiltinSegmentRules()
	if err != nil {
		t.Fatal(err)
	}
	var over CheckReport
	over.CheckSegmentTotal(segs, 90, rules)
	if len(over.Items) != 1 || over.Items[0].Rule != "R-SEG-008" || over.Items[0].Level != "warn" || !strings.Contains(over.Items[0].Message, "超过目标片长 90 秒的 10% 余量（99 秒）") {
		t.Fatalf("110 秒超过 90×1.1：%+v", over.Items)
	}
	var short CheckReport
	short.CheckSegmentTotal(segs, 200, rules)
	if len(short.Items) != 1 || !strings.Contains(short.Items[0].Message, "不足目标片长 200 秒的 60%") {
		t.Fatalf("110 秒不足 200 的 60%%：%+v", short.Items)
	}
	var ok CheckReport
	ok.CheckSegmentTotal(segs, 100, rules)
	ok.CheckSegmentTotal(segs, 0, rules)
	if len(ok.Items) != 0 {
		t.Fatalf("在范围内或没设目标不报：%+v", ok.Items)
	}
}

func TestFightTextAvoidsRepeatsAndStrayDuifang(t *testing.T) {
	cp := mustCompileProfile(nil)
	people := []string{"阿坤", "周岸"}
	cases := []struct {
		name   string
		fight  Fight
		want   string
		absent []string
	}{
		{"路径已写到目标", Fight{Initiator: "阿坤", Path: "从右上方斜劈向周岸左肩", Target: "周岸左肩", Response: "侧身让开", Contact: "graze", ForceTransfer: "刀尖擦过左袖口"},
			"动作：阿坤发起，从右上方斜劈向周岸左肩；对方侧身让开；刀尖擦过左袖口。", []string{"打向", "；擦过；"}},
		{"没有接触不重复", Fight{Initiator: "阿坤", Path: "挥刀虚晃", Target: "周岸", Response: "后撤半步", Contact: "none", ForceTransfer: "无直接接触"},
			"动作：阿坤发起，挥刀虚晃，打向周岸；对方后撤半步；无直接接触。", []string{"没有接触"}},
		{"目标不是人", Fight{Initiator: "周岸", Path: "抬脚踹门", Target: "楼梯间防火门", Response: "门板弹开撞墙", Contact: "hit"},
			"动作：周岸发起，抬脚踹门，打向楼梯间防火门；门板弹开撞墙；命中。", []string{"对方"}},
		{"反应自带主语", Fight{Initiator: "阿坤", Path: "直刺", Target: "周岸胸口", Response: "周岸右前臂下压格挡", Contact: "blocked"},
			"动作：阿坤发起，直刺，打向周岸胸口；周岸右前臂下压格挡；被挡下。", []string{"对方"}},
	}
	for _, c := range cases {
		got := fightText(&c.fight, people, cp)
		if got != c.want {
			t.Errorf("%s：\n got %s\nwant %s", c.name, got, c.want)
		}
		for _, a := range c.absent {
			if strings.Contains(got, a) {
				t.Errorf("%s 不该出现「%s」：%s", c.name, a, got)
			}
		}
	}
}

func TestSegmentEndRulesReadable(t *testing.T) {
	shots := []Shot{{ID: "E01S02-01", Scene: "E01S02", Size: "中景", Cam: Cam{Z: "Z5", Y: "Y4", X: "X2"}, Est: 5, Action: "林小满停在电梯口",
		EndState: "林小满停在电梯门前，病历夹抱在胸前。", Blocking: []Blocking{{Who: "林小满", At: "电梯门前", Facing: "电梯门"}, {Who: "周岸", At: "楼梯间防火门", Facing: "朝林小满"}}}}
	m, _ := ModelByID("seedance-2.5")
	segs, _ := Pack(PackInput{Shotlist: Shotlist{Profile: DefaultProfileID, Shots: shots}, Model: m, Profile: testProfile(t, "live-realism"), ProfileID: DefaultProfileID, Vocab: testVocab(t)})
	if len(segs) != 1 {
		t.Fatalf("应装出一段：%d", len(segs))
	}
	want := "本段结束时：林小满停在电梯门前，病历夹抱在胸前；视线：林小满看向电梯门；周岸朝林小满。"
	if segs[0].Rules != want {
		t.Fatalf("段末规则：\n got %s\nwant %s", segs[0].Rules, want)
	}
}
