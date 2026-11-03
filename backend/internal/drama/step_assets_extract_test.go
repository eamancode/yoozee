package drama

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 固定真实文本模型输出，防止解析、别名合并及子卡处理变化重新丢失剧情资产。
func TestAssetsSkillSuixingxianAcceptance(t *testing.T) {
	raw, err := os.ReadFile("testdata/assets-skill-p0/suixingxian.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row AssetsExtractRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("资产行必须是合法 JSON：%v", err)
		}
		if jsLen(row.Appearance) > assetsAppearanceChars || jsLen(row.PromptEn) > assetsPromptChars {
			t.Fatalf("%s 的关键设计会被截断", row.Name)
		}
	}
	rows, stats := AssetsExtractFromText(string(raw))
	if stats.Invalid != 0 {
		t.Fatalf("真实输出不应丢行：%+v", stats)
	}
	result := CheckAssetsExtract(rows, AssetsExtractCheckContext{Scenes: 7})
	if result.Report.Errors != 0 || result.Skipped != 0 {
		t.Fatalf("真实输出应全部可入库：%+v", result.Report)
	}
	assets := map[string]AssetsExtractRow{}
	for _, row := range result.Rows {
		for _, name := range append([]string{row.Name}, row.Aliases...) {
			assets[row.Category+":"+AssetNameKey(name)] = row
		}
	}
	for _, name := range []string{"林深", "阿婆", "小杨", "管理员", "顾客A", "顾客B", "顾客C"} {
		if _, ok := assets[AssetCharacter+":"+AssetNameKey(name)]; !ok {
			t.Errorf("有台词的出镜角色不能漏掉或误合并：%s", name)
		}
	}
	for _, name := range []string{"旧保温杯", "随行鲜便携榨汁杯", "插线板"} {
		if _, ok := assets[AssetProp+":"+AssetNameKey(name)]; !ok {
			t.Errorf("关键道具不能漏掉或误合并：%s", name)
		}
	}
	// 原文只给出外观，没有固定毛巾名；允许模型使用描述性名称，但仍须是一张独立道具卡。
	towel := false
	for _, row := range result.Rows {
		towel = towel || (row.Category == AssetProp && strings.Contains(row.Name, "毛巾"))
	}
	if !towel {
		t.Error("动作交互中的毛巾不能漏提")
	}
	if assets[AssetProp+":旧保温杯"].Name == assets[AssetProp+":随行鲜便携榨汁杯"].Name {
		t.Error("保温杯与产品杯必须是两张独立道具卡")
	}
	seenCustomers := map[string]bool{}
	for _, name := range []string{"顾客A", "顾客B", "顾客C"} {
		seenCustomers[assets[AssetCharacter+":"+AssetNameKey(name)].Name] = true
	}
	if len(seenCustomers) != 3 {
		t.Error("三个有独立台词的顾客必须保持独立身份")
	}
	cup := assets[AssetProp+":随行鲜便携榨汁杯"]
	productPrompt := strings.ToLower(cup.PromptEn)
	if (!strings.Contains(productPrompt, "type-c") && !strings.Contains(productPrompt, "usb-c")) || !strings.Contains(productPrompt, "green") || !strings.Contains(productPrompt, "white") {
		t.Errorf("产品英文设计缺少原文关键外观：%s", cup.PromptEn)
	}
	apron := false
	for _, v := range assets[AssetCharacter+":林深"].Variants {
		if strings.Contains(v.Appearance+v.PromptEn, "围裙") || strings.Contains(strings.ToLower(v.PromptEn), "apron") {
			for _, scene := range v.Scenes {
				apron = apron || scene == 4
			}
		}
	}
	if !apron {
		t.Error("原第四、五场合并为运行场 4，围裙换装必须作为对应子卡保留")
	}
}

const (
	assetsRowLin   = `{"category": "character", "name": "林小满", "aliases": ["小满"], "appearance": "", "prompt_en": "", "first_scene": 1, "palette_hint": ""}`
	assetsRowZhou  = `{"category": "character", "name": "周岸", "aliases": "周哥、阿岸", "appearance": "三十岁男性，病号服", "prompt_en": "character turnaround sheet, front side back, man in striped hospital gown", "first_scene": "S02", "palette_hint": "病号服条纹"}`
	assetsRowHall  = `{"category": "scene", "name": "病房走廊", "aliases": [], "appearance": "狭长走廊，日光灯一盏接一盏", "prompt_en": "empty hospital corridor at night, no people", "first_scene": 1, "palette_hint": "", "anchors": ["护士站柜台", "七床门口"]}`
	assetsRowKnife = `{"category": "prop", "name": "折叠刀", "aliases": [], "appearance": "黑柄折叠刀", "prompt_en": "", "first_scene": 2, "palette_hint": ""}`
)

func TestAssetsExtractSharedAliasDoesNotMergeDistinctProps(t *testing.T) {
	thermos := AssetsExtractRow{Category: AssetProp, Name: "保温桶", Aliases: []string{"桶"}, Appearance: "密封盛热橙汁", PromptEn: "insulated juice container"}
	bucket := AssetsExtractRow{Category: AssetProp, Name: "水桶", Appearance: "敞口洗抹布", PromptEn: "open cleaning bucket"}
	reused := AssetsExtractRow{Category: AssetProp, Name: "水桶", Aliases: []string{"桶"}}
	for _, rows := range [][]AssetsExtractRow{{thermos, bucket, reused}, {bucket, reused, thermos}} {
		got := CheckAssetsExtract(rows, AssetsExtractCheckContext{})
		if len(got.Rows) != 2 || got.Report.Errors != 0 || got.Report.Warnings != 1 {
			t.Fatalf("shared generic alias must retain two props: %+v", got)
		}
		for _, row := range got.Rows {
			if len(row.Aliases) != 0 {
				t.Fatalf("ambiguous alias must not be persisted: %+v", row)
			}
		}
	}
	got := CheckAssetsExtract([]AssetsExtractRow{bucket, reused}, AssetsExtractCheckContext{Known: []AssetsKnown{{Category: AssetProp, Name: "保温桶", Aliases: []string{"桶"}}}})
	if len(got.Rows) != 1 || got.Rows[0].Merge || got.Rows[0].Name != "水桶" {
		t.Fatalf("new bucket must not merge into existing thermos: %+v", got)
	}
	if thermos.Aliases[0] != "桶" {
		t.Fatal("checking must not mutate source rows")
	}
}

func TestAssetsExtractMergedSceneScope(t *testing.T) {
	text := "第一场｜后街-夜-外\n阿婆：进来躲雨。\n第二场｜市场果汁摊-日-内\n管理员：交摊位费。\n第三场｜市场果汁摊-日-内\n△林深系上围裙。\n顾客A：这杯子哪买的？\n第四场｜市场角落-日-内\n阿婆：这是角落里的话。"
	turns := AssetsExtractScenes(text, "测试")
	if len(turns) != 3 || len(turns[1].SourceHeadings) != 2 {
		t.Fatalf("必须沿用统一同地点合场规则：%+v", turns)
	}
	prompt := AssetsExtractTurnPrompt(turns, 2)
	for _, want := range []string{"第二场", "第三场", "管理员：交摊位费", "林深系上围裙", "顾客A：这杯子哪买的", "first_scene 写 2", "scenes 只写 [2]", "首次建卡仍须通读完整剧本", "不能倒填到初始外观", "design.evidence", "design.states", "start_after 逐字记录触发动作"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("续场必须包含完整合并范围 %q：%s", want, prompt)
		}
	}
	plan := AssetsExtractTurnPlan(turns)
	for _, want := range []string{"对应原文：第二场", "第三场", "首轮 scenes 只写 [1]", "不能只补别名", "start_after"} {
		if !strings.Contains(plan, want) {
			t.Errorf("首轮必须保留附件分析及原文到运行场序的映射 %q：%s", want, plan)
		}
	}
	if strings.Contains(prompt, "进来躲雨") || strings.Contains(prompt, "这是角落里的话") {
		t.Fatal("本轮范围不能混入前后场正文")
	}
	if one := AssetsExtractScenes("第一场｜后街-夜-外\n阿婆：进来躲雨。", "测试"); len(one) != 0 {
		t.Fatal("单场仍直接使用完整正文，不增加续场")
	}
	if other := ChapterShotsScenes(text, "测试"); other[1].AssetSourceText != "" {
		t.Fatal("资产范围不能改变分镜步骤的轮次数据")
	}
}

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

// 连续场只复用发生地时，逐轮校验仍通过，收尾合并不能创建第二张场景卡或覆盖已有外观。
func TestAssetsExtractReusedSceneTurn(t *testing.T) {
	first := `{"category":"environment","name":"城西便民市场","appearance":"铁皮棚，左右摊位夹住过道","prompt_en":"empty covered market, metal roof, stalls on both sides of an aisle, no people","first_scene":3,"anchors":["果汁摊前","过道中央"]}`
	reused := `{"category":"environment","name":"城西便民市场","first_scene":4}`
	if n, err := CountAssetsExtractTurn(reused, TurnContext{}); err != nil || n != 1 {
		t.Fatalf("复用发生地必须作为有效回合：%d %v", n, err)
	}
	rows, _ := AssetsExtractFromText(first + "\n" + reused)
	got := CheckAssetsExtract(rows, AssetsExtractCheckContext{Scenes: 8})
	if len(got.Rows) != 1 || got.Skipped != 0 || got.Report.Errors != 0 || got.Rows[0].FirstScene != 3 || got.Rows[0].Appearance != "铁皮棚，左右摊位夹住过道" || len(got.Rows[0].Anchors) != 2 {
		t.Fatalf("复用回合应保留首场完整设计：%+v", got)
	}
	knownRows, _ := AssetsExtractFromText(reused)
	merged := CheckAssetsExtract(knownRows, AssetsExtractCheckContext{Scenes: 8, Known: []AssetsKnown{{Category: AssetEnvironment, Name: "城西便民市场"}}})
	if len(merged.Rows) != 1 || !merged.Rows[0].Merge || merged.Skipped != 0 || merged.Rows[0].Appearance != "" {
		t.Fatalf("已有资产重跑不补写或覆盖外观：%+v", merged)
	}
	scenes := []ShotsScene{{Heading: "城西便民市场-日-内"}, {Heading: "城西便民市场-黄昏-内"}}
	for _, prompt := range []string{AssetsExtractTurnPlan(scenes), AssetsExtractTurnPrompt(scenes, 2)} {
		if !strings.Contains(prompt, "每场至少输出发生地的 environment 行") || !strings.Contains(prompt, "复用资产照抄已有 name") || !strings.Contains(prompt, "first_scene") || !strings.Contains(prompt, "design") {
			t.Fatalf("逐场指令必须允许复用发生地：%s", prompt)
		}
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
