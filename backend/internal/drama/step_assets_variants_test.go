package drama

import (
	"strings"
	"testing"
)

const (
	variantsLin      = `{"category": "character", "name": "林小满", "aliases": [], "appearance": "二十五岁女护士，齐耳短发，护士蓝 #5B7FA3 护士服", "prompt_en": "turnaround sheet, nurse uniform #5B7FA3", "first_scene": 1, "palette_hint": "护士蓝", "variants": [{"label": "淋湿", "kind": "state", "scenes": [2], "appearance": "护士服湿透贴身，发梢滴水", "prompt_en": "same nurse, soaking wet uniform"}, {"label": "", "kind": "costume", "scenes": [1], "appearance": "x", "prompt_en": "y"}]}`
	variantsLinAgain = `{"category": "character", "name": "林小满", "aliases": [], "appearance": "", "prompt_en": "", "first_scene": 2, "palette_hint": "", "variants": [{"label": "淋湿", "kind": "状态", "scenes": ["E01S03", 9], "appearance": "", "prompt_en": ""}, {"label": "西服", "scenes": "1、2", "appearance": "深灰西服 #123456", "prompt_en": "grey suit"}]}`
	variantsTitled   = `{"category": "character", "name": "林小满·受伤", "aliases": [], "appearance": "额头缠纱布", "prompt_en": "bandaged forehead", "first_scene": 3, "palette_hint": ""}`
	variantsChart    = `{"category": "prop", "name": "病历夹", "aliases": [], "appearance": "蓝灰病历夹 #3A5F7C，夹着日光灯白 #E8ECEF 的病历纸", "prompt_en": "a clipboard #3A5F7C", "first_scene": 2, "palette_hint": ""}`
	variantsKnife    = `{"category": "prop", "name": "折叠刀", "aliases": [], "appearance": "金柄 #C9A227 折叠刀", "prompt_en": "a folding knife", "first_scene": 2, "palette_hint": ""}`
	variantsHall     = `{"category": "environment", "name": "电梯口", "aliases": ["电梯间"], "appearance": "两部老电梯，电梯钢 #9AA3A8，墙面水泥灰 #6E6E6E，警示 #FF0000", "prompt_en": "empty elevator lobby", "first_scene": 2, "palette_hint": "电梯钢", "variants": [{"label": "夜", "appearance": "x"}]}`
)

// 角色行的 variants：读出变体名、种类（写成中文或认不出时按变体名猜）、场序（数字、场编号、一句话都认）；场景写了 variants 不读。
func TestAssetsVariantsParse(t *testing.T) {
	rows, _ := AssetsExtractFromText(strings.Join([]string{variantsLin, variantsLinAgain, variantsHall}, "\n"))
	lin, again, hall := rows[0], rows[1], rows[2]
	if len(lin.Variants) != 2 || lin.Variants[0].Label != "淋湿" || lin.Variants[0].Kind != AssetVariantState || lin.Variants[0].Scenes[0] != 2 {
		t.Fatalf("林小满的子卡：%+v", lin.Variants)
	}
	if again.Variants[0].Kind != AssetVariantState {
		t.Fatalf("种类写成「状态」也认：%+v", again.Variants[0])
	}
	if got := again.Variants[0].Scenes; len(got) != 2 || got[0] != 3 || got[1] != 9 {
		t.Fatalf("场序写成场编号也认：%v", got)
	}
	if v := again.Variants[1]; v.Kind != AssetVariantCostume || len(v.Scenes) != 2 {
		t.Fatalf("没写种类的西服算服装、场序一句话：%+v", v)
	}
	if len(hall.Variants) != 0 {
		t.Fatalf("场景不读 variants：%+v", hall.Variants)
	}
	if NormalizeAssetVariantKind("", "受伤") != AssetVariantState || NormalizeAssetVariantKind("", "护士服") != AssetVariantCostume {
		t.Fatal("按变体名猜种类")
	}
}

// 检查：同一角色几行的子卡按变体名并起来（场序合并、空字段补上）；没有变体名的、场序超出的去掉（warn）；
// 角色名写成「林小满·受伤」的行并成林小满的子卡。
func TestCheckAssetsVariants(t *testing.T) {
	rows, _ := AssetsExtractFromText(strings.Join([]string{variantsLin, variantsLinAgain, variantsTitled}, "\n"))
	got := CheckAssetsExtract(rows, AssetsExtractCheckContext{Scenes: 3})
	if len(got.Rows) != 1 {
		t.Fatalf("三行并成一个林小满：%+v", got.Rows)
	}
	variants := got.Rows[0].Variants
	if len(variants) != 3 || variants[0].Label != "淋湿" || variants[1].Label != "西服" || variants[2].Label != "受伤" {
		t.Fatalf("子卡：%+v", variants)
	}
	if wet := variants[0]; len(wet.Scenes) != 2 || wet.Scenes[0] != 2 || wet.Scenes[1] != 3 || wet.Appearance != "护士服湿透贴身，发梢滴水" {
		t.Fatalf("淋湿合并场序、超出本单元的去掉：%+v", wet)
	}
	if hurt := variants[2]; hurt.Kind != AssetVariantState || hurt.Appearance != "额头缠纱布" || len(hurt.Scenes) != 1 || hurt.Scenes[0] != 3 {
		t.Fatalf("「林小满·受伤」并成子卡：%+v", hurt)
	}
	rules := map[string]int{}
	for _, item := range got.Report.Items {
		rules[item.Rule]++
	}
	if rules[RuleAssetVariant] != 2 {
		t.Fatalf("没有变体名、场序超出各一条：%+v", got.Report.Items)
	}
}

// R-ASSET-009：角色外观里的 HEX 要在主基调色卡里；场景查它出现的各场（地点对得上的场和 first_scene）的场次色卡加主基调色卡。
func TestCheckAssetsHexes(t *testing.T) {
	master := MasterPalette{Groups: []MasterPaletteGroup{{Key: "protagonist", Name: "主角组", Colors: []PaletteColor{{NameZh: "护士蓝", NameEn: "Nurse Blue", Hex: "#5B7FA3"}}}, {Key: "environment", Name: "环境组", Colors: []PaletteColor{{NameZh: "水泥灰", Hex: "#6E6E6E"}}}}}
	script := []ColorScriptEntry{
		{Episode: 1, SceneNo: 1, NewColors: []PaletteColor{{NameZh: "日光灯白", Hex: "#E8ECEF"}}},
		{Episode: 1, SceneNo: 2, NewColors: []PaletteColor{{NameZh: "电梯钢", Hex: "#9AA3A8"}, {NameZh: "病历蓝灰", Hex: "#3A5F7C"}}},
	}
	rows, _ := AssetsExtractFromText(strings.Join([]string{variantsLin, variantsLinAgain, variantsHall, variantsChart, variantsKnife}, "\n"))
	got := CheckAssetsExtract(rows, AssetsExtractCheckContext{Scenes: 2, ColorScript: script, Master: master, SceneLocations: []string{"病房走廊", "电梯口"}})
	var stray []string
	for _, item := range got.Report.Items {
		if item.Rule == RuleAssetPaletteHex {
			stray = append(stray, item.ShotID+"："+item.Message)
		}
	}
	// 道具认主基调色卡和本集任一场次色卡：病历夹用第 2 场、第 1 场色卡里的颜色不报，折叠刀的金色两边都没有才报。
	if len(stray) != 3 || !strings.Contains(stray[0], "林小满·西服：外观里的 #123456 不在主基调色卡里") || !strings.Contains(stray[1], "电梯口：外观里的 #FF0000 不在本场色卡和主基调色卡里") ||
		!strings.Contains(stray[2], "折叠刀：外观里的 #C9A227 不在主基调色卡和本集场次色卡里") {
		t.Fatalf("HEX 检查：%v", stray)
	}
	if none := CheckAssetsExtract(rows, AssetsExtractCheckContext{Scenes: 2}); strings.Contains(strings.Join(func() []string {
		var rules []string
		for _, item := range none.Report.Items {
			rules = append(rules, item.Rule)
		}
		return rules
	}(), ","), RuleAssetPaletteHex) {
		t.Fatal("没有色卡时不查 HEX")
	}
}

// 提示词带主基调色卡（用于角色、道具）、本集场次色卡（用于场景）和用色要求；契约写了 variants 和 HEX 的规则。
func TestBuildAssetsExtractPromptWithPalettes(t *testing.T) {
	prompt, err := BuildAssetsExtractPrompt(AssetsExtractInput{
		UnitTitle: "七床空了", UnitLabel: "第 1 集", UnitNoun: "集", SourceText: "## 内·病房走廊·夜\n林小满：七床？",
		Known:       []AssetsKnown{{Category: AssetCharacter, Name: "林小满", Variants: []string{"护士服", "淋湿"}}},
		Master:      MasterPalette{Groups: []MasterPaletteGroup{{Key: "protagonist", Name: "主角组", Colors: []PaletteColor{{NameZh: "护士蓝", NameEn: "Nurse Blue", Hex: "#5B7FA3"}}}}},
		ColorScript: []ColorScriptEntry{{Episode: 1, SceneNo: 1, Scene: "内·病房走廊·夜", SharedColors: []PaletteColor{{NameZh: "日光灯白", Hex: "#E8ECEF"}}}},
		Skill:       StepSkill{ID: "yz-assets-extract", Files: []StepSkillFile{{Path: "SKILL.md", Kind: "markdown", Content: "# 资产提取"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	order := []string{"- 林小满（角色；子卡：护士服、淋湿）", "【主基调色卡】", "用于角色和道具", "护士蓝 / Nurse Blue #5B7FA3", "【本集场次色卡】", "用于这一场发生地的场景资产", "E01S01「内·病房走廊·夜」", "【用色要求】角色服装用色从【主基调色卡】取；道具用色从【主基调色卡】或它所在场的本场色卡取；场景用色从本场色卡", "【受保护输出契约】", "HEX 照抄色卡，不写色卡以外的 HEX。", "variants 只有角色写"}
	at := 0
	for _, part := range order {
		i := strings.Index(prompt.Text[at:], part)
		if i < 0 {
			t.Fatalf("缺少或顺序不对：%q\n%s", part, prompt.Text)
		}
		at += i
	}
}

// 场景资产对场次地点：比对键相同，或互相包含（至少两个字）；括注去掉。
func TestAssetMatchesLocation(t *testing.T) {
	cases := []struct {
		name     string
		aliases  []string
		location string
		want     bool
	}{
		{"电梯口", nil, "电梯口", true},
		{"电梯口（夜）", nil, "电梯口", true},
		{"医院电梯口", nil, "电梯口", true},
		{"病房", []string{"七床病房"}, "七床病房", true},
		{"走廊", nil, "电梯口", false},
		{"门", nil, "门口", false},
	}
	for _, c := range cases {
		if got := AssetMatchesLocation(c.name, c.aliases, c.location); got != c.want {
			t.Errorf("%s / %s：%v", c.name, c.location, got)
		}
	}
}
