package drama

import (
	"strings"
	"testing"
)

const (
	assetsRowLin   = `{"category": "character", "name": "林小满", "aliases": ["小满"], "appearance": "", "prompt_en": "", "first_scene": 1, "palette_hint": ""}`
	assetsRowZhou  = `{"category": "character", "name": "周岸", "aliases": "周哥、阿岸", "appearance": "三十岁男性，病号服", "prompt_en": "character turnaround sheet, front side back, man in striped hospital gown", "first_scene": "S02", "palette_hint": "病号服条纹"}`
	assetsRowHall  = `{"category": "scene", "name": "病房走廊", "aliases": [], "appearance": "狭长走廊，日光灯一盏接一盏", "prompt_en": "empty hospital corridor at night, no people", "first_scene": 1, "palette_hint": "", "anchors": ["护士站柜台", "七床门口"]}`
	assetsRowKnife = `{"category": "prop", "name": "折叠刀", "aliases": [], "appearance": "黑柄折叠刀", "prompt_en": "", "first_scene": 2, "palette_hint": ""}`
)

// 逐行解析：回合标记、说明文字跳过；别名写成一句话、场序写成「S02」、分类写成 scene 都认；场景才读锚点。
func TestAssetsExtractFromText(t *testing.T) {
	text := strings.Join([]string{`{"` + ShotLinesMarkerKey + `": {"index": 1}}`, "下面是资产：", assetsRowLin, assetsRowZhou, "```", assetsRowHall, `{"oops": 1}`, `{"category": "prop", "name": `}, "\n")
	rows, stats := AssetsExtractFromText(text)
	if stats.Rows != 3 || stats.Invalid != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	zhou, hall := rows[1], rows[2]
	if strings.Join(zhou.Aliases, "/") != "周哥/阿岸" || zhou.FirstScene != 2 || zhou.Category != AssetCharacter {
		t.Fatalf("周岸 = %+v", zhou)
	}
	if hall.Category != AssetEnvironment || strings.Join(hall.Anchors, "/") != "护士站柜台/七床门口" {
		t.Fatalf("病房走廊 = %+v", hall)
	}
}

// 当轮校验：至少一行能用；写了外观没写提示词的重试这一轮。
func TestCountAssetsExtractTurn(t *testing.T) {
	if n, err := CountAssetsExtractTurn(assetsRowLin+"\n"+assetsRowHall, TurnContext{}); err != nil || n != 2 {
		t.Fatalf("两行能用：%d %v", n, err)
	}
	if _, err := CountAssetsExtractTurn("这一场没有新资产。", TurnContext{}); err == nil {
		t.Fatal("一行都没有要重试")
	}
	if _, err := CountAssetsExtractTurn(assetsRowKnife, TurnContext{}); err == nil || !strings.Contains(err.Error(), "prompt_en") {
		t.Fatalf("写了外观没写提示词要重试：%v", err)
	}
}

// 检查：与已有资产同名或同别名的标合并（不报错）；同一资产写了几行并成一行（别名合并、空字段补上）；
// 新资产没写提示词的跳过（error，按分类计数）；场序超出、色名不在本集色彩脚本里是 warn。
func TestCheckAssetsExtract(t *testing.T) {
	zhouAgain := `{"category": "character", "name": "阿岸", "aliases": ["周岸", "岸哥"], "appearance": "", "prompt_en": "", "first_scene": 2, "palette_hint": ""}`
	bad := `{"category": "weapon", "name": "铁棍", "aliases": [], "appearance": "x", "prompt_en": "y", "first_scene": 1, "palette_hint": ""}`
	rows, _ := AssetsExtractFromText(strings.Join([]string{assetsRowLin, assetsRowZhou, assetsRowHall, zhouAgain, assetsRowKnife, bad, `{"category": "prop", "name": "", "prompt_en": "x"}`}, "\n"))
	script := []ColorScriptEntry{{Episode: 1, SceneNo: 1, NewColors: []PaletteColor{{NameZh: "护士蓝", NameEn: "Nurse Blue", Hex: "#5B7FA3"}}}}
	got := CheckAssetsExtract(rows, AssetsExtractCheckContext{Known: []AssetsKnown{{Category: AssetCharacter, Name: "林小满", Aliases: []string{"小满护士"}}}, Scenes: 1, ColorScript: script})
	if len(got.Rows) != 3 || got.Skipped != 3 || got.SkippedBy[AssetProp] != 1 {
		t.Fatalf("三行写、三行跳过：%+v", got)
	}
	lin, zhou, hall := got.Rows[0], got.Rows[1], got.Rows[2]
	if !lin.Merge || zhou.Merge || hall.Merge {
		t.Fatalf("林小满是已有资产：%+v %+v %+v", lin, zhou, hall)
	}
	if strings.Join(zhou.Aliases, "/") != "周哥/阿岸/岸哥" {
		t.Fatalf("阿岸并进周岸，别名合并：%+v", zhou.Aliases)
	}
	rules := map[string]int{}
	for _, item := range got.Report.Items {
		rules[item.Rule]++
	}
	if rules[RuleAssetNoPrompt] != 1 || rules[RuleAssetCategory] != 1 || rules[RuleAssetNoName] != 1 || rules[RuleAssetFirstScene] != 1 || rules[RuleAssetPaletteName] != 1 || got.Report.Errors != 3 {
		t.Fatalf("规则：%+v", got.Report.Items)
	}
	if zhou.FirstScene != 0 {
		t.Fatalf("场序超出的清掉：%d", zhou.FirstScene)
	}
	if none := CheckAssetsExtract(nil, AssetsExtractCheckContext{}); none.Report.Errors != 1 || none.Report.Items[0].Rule != RuleAssetNoRows {
		t.Fatalf("一行都没有：%+v", none.Report)
	}
}

// 提示词：技能上下文里是本单元正文；已有资产名录（缺外观的标出）、风格锚、本集色彩脚本、风格档案、画风、契约按顺序；契约一行一个资产。
func TestBuildAssetsExtractPrompt(t *testing.T) {
	prompt, err := BuildAssetsExtractPrompt(AssetsExtractInput{
		UnitTitle: "七床空了", UnitLabel: "第 1 集", UnitNoun: "集", SourceText: "## 内·病房走廊·夜\n林小满：七床？",
		Known:       []AssetsKnown{{Category: AssetCharacter, Name: "林小满", Aliases: []string{"小满"}}, {Category: AssetProp, Name: "病历夹", Incomplete: true}},
		Anchor:      &StyleAnchor{Medium: "真人实拍", Forbidden: []string{"霓虹"}},
		ColorScript: []ColorScriptEntry{{Episode: 1, SceneNo: 1, Scene: "内·病房走廊·夜", SharedColors: []PaletteColor{{NameZh: "日光灯白", Hex: "#E8ECEF", Usage: "顶灯"}}}},
		Profile:     StyleBibleProfile{Label: "真人写实"},
		Style:       StepStyle{Title: "港风写实", Prompt: "【视觉媒介】真人实拍"},
		Skill:       StepSkill{ID: "yz-assets-extract", Files: []StepSkillFile{{Path: "SKILL.md", Kind: "markdown", Content: "# 资产提取"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := prompt.Text
	order := []string{"剧本：第 1 集「七床空了」", "林小满：七床？", "【项目已有资产】", "- 林小满（角色；别名：小满）", "- 病历夹（道具；缺外观）", "【全局风格锚】", "禁用：霓虹", "【本集场次色卡】", "第 1 场 E01S01「内·病房走廊·夜」：日光灯白 #E8ECEF（顶灯）", "【风格档案：真人写实】", "【项目画风：港风写实】", "【受保护输出契约】", "至少输出 1 个资产。", "first_scene（整数", "anchors（字符串数组"}
	at := 0
	for _, part := range order {
		i := strings.Index(text[at:], part)
		if i < 0 {
			t.Fatalf("缺少或顺序不对：%q\n%s", part, text)
		}
		at += i
	}
	plan := AssetsExtractTurnPlan([]ShotsScene{{ID: "E01S01", Heading: "内·病房走廊·夜"}, {ID: "E01S02", Heading: "内·电梯口·夜"}})
	next := AssetsExtractTurnPrompt([]ShotsScene{{ID: "E01S01", Heading: "内·病房走廊·夜"}, {ID: "E01S02", Heading: "内·电梯口·夜"}}, 2)
	if !strings.Contains(plan, "共 2 场") || !strings.Contains(next, "first_scene 写 2") {
		t.Fatalf("分回合：%s / %s", plan, next)
	}
}

// palette_hint 按中文或英文色名对上本集色彩脚本的颜色，去重。
func TestAssetsPaletteColors(t *testing.T) {
	script := []ColorScriptEntry{{Episode: 1, SceneNo: 1, SharedColors: []PaletteColor{{NameZh: "护士蓝", NameEn: "Nurse Blue", Hex: "#5B7FA3"}}, NewColors: []PaletteColor{{NameZh: "应急红", NameEn: "Alarm Red", Hex: "#B3261E"}}}}
	got := AssetsPaletteColors("护士蓝、alarm red、护士蓝、不存在", script, MasterPalette{})
	if len(got) != 2 || got[0].Hex != "#5B7FA3" || got[1].Hex != "#B3261E" {
		t.Fatalf("got = %+v", got)
	}
}

// 「参考齐了」的规则不变，说明里分出资产未提取和有资产还没出图。
func TestRefsDetailDistinguishesAssets(t *testing.T) {
	detail := func(assets AssetsInput) string {
		for _, s := range Derive(ProgressInput{RunMode: RunEveryStep, Assets: assets}).EndStates {
			if s.Key == "refs" {
				return s.Detail
			}
		}
		return ""
	}
	if got := detail(AssetsInput{}); !strings.HasPrefix(got, "资产未提取") {
		t.Fatalf("没有资产：%s", got)
	}
	if got := detail(AssetsInput{Total: 5, Unimaged: 2}); !strings.Contains(got, "2/5 个资产还没出图") {
		t.Fatalf("有资产没出图：%s", got)
	}
	if got := detail(AssetsInput{Total: 5}); strings.Contains(got, "资产") {
		t.Fatalf("都出了图不提资产：%s", got)
	}
}
