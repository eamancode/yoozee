package drama

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"slices"
	"strings"
	"testing"
)

// scenePaletteTurn1 是第 1 集两场：第 2 场承接第 1 场 2 色、新增 1 色；每场一行，scene 写场编号。
const scenePaletteTurn1 = `{"scene": "E01S01", "inherits_from": "", "shared_colors": [{"name_zh": "日光灯白", "name_en": "Fluorescent White", "hex": "#e8ecef", "usage": "走廊顶灯", "group": "environment"}, {"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙面", "group": "环境组"}], "new_colors": [{"name_zh": "护士蓝", "name_en": "Nurse Blue", "hex": "5B7FA3", "usage": "林小满制服", "group": "protagonist"}]}
{"scene": "e1s2", "inherits_from": "e1s1", "shared_colors": [{"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙面", "group": "environment"}, {"name_zh": "护士蓝", "name_en": "Nurse Blue", "hex": "#5B7FA3", "usage": "制服", "group": "protagonist"}], "new_colors": [{"name_zh": "电梯钢", "name_en": "Elevator Steel", "hex": "#9AA3A8", "usage": "电梯门", "group": "props"}]}
`

func TestScenePaletteFromText(t *testing.T) {
	out, stats := ScenePaletteFromText("```json\n"+`{"style_anchor": {"medium": "真人实拍"}}`+"\n"+scenePaletteTurn1+"```\n说明文字\n{坏的 json}\n", 0)
	if len(out.Script) != 2 || stats.Scenes != 2 || stats.Episodes != 1 || stats.Invalid != 1 || stats.AnchorIgnored != 1 {
		t.Fatalf("应读出 2 场、1 行坏 JSON、忽略 1 行风格锚：%+v %+v", out.Script, stats)
	}
	first, second := out.Script[0], out.Script[1]
	if first.Code() != "E01S01" || first.Scene != "" || first.SharedColors[0].Hex != "#E8ECEF" || first.NewColors[0].Hex != "#5B7FA3" {
		t.Fatalf("场编号读出集号场序、HEX 规整成大写 #RRGGBB：%+v", first)
	}
	if first.SharedColors[1].Group != PaletteGroupEnvironment || first.NewColors[0].Group != PaletteGroupProtagonist {
		t.Fatalf("分组按主色卡的分组键规整：%+v", first)
	}
	if second.Code() != "E01S02" || second.InheritsFrom != "E01S01" {
		t.Fatalf("场编号应规整：%+v", second)
	}
	// 旧写法：JSON 头下面每场写 scene_no，没写 episode 的场归到前面的 JSON 头。
	loose, _ := ScenePaletteFromText(`{"episode": 3}`+"\n"+`{"scene_no": 1, "scene": "外·天台·日", "shared_colors": [], "new_colors": []}`, 0)
	if len(loose.Script) != 1 || loose.Script[0].Code() != "E03S01" || loose.Script[0].Scene != "外·天台·日" {
		t.Fatalf("没写 episode 的场应归到第 3 集：%+v", loose.Script)
	}
	// scene 和 episode / scene_no 写得不一致时按 scene，记一条降级说明。
	mixed, _ := ScenePaletteFromText(`{"scene": "E02S03", "episode": 1, "scene_no": 1, "shared_colors": [], "new_colors": []}`, 0)
	if mixed.Script[0].Code() != "E02S03" || len(mixed.Script[0].Coerced) != 1 {
		t.Fatalf("按 scene 读：%+v", mixed.Script)
	}
}

func TestCountScenePaletteTurn(t *testing.T) {
	turn := TurnContext{Index: 1, Turn: ShotsScene{ID: "1", Heading: "七床空了"}}
	if n, err := CountScenePaletteTurn(scenePaletteTurn1, turn); err != nil || n != 2 {
		t.Fatalf("第一轮应合格、写了 2 场：%d %v", n, err)
	}
	turn2 := TurnContext{Index: 2, Turn: ShotsScene{ID: "2"}, Prior: scenePaletteTurn1}
	ok := `{"scene": "E02S01", "inherits_from": "E01S02", "shared_colors": [{"name_zh": "电梯钢", "name_en": "Elevator Steel", "hex": "#9AA3A8", "usage": "轿厢", "group": "props"}], "new_colors": [{"name_zh": "应急红", "name_en": "Alarm Red", "hex": "#B3261E", "usage": "应急灯", "group": "environment"}, {"name_zh": "暗影", "name_en": "Shadow", "hex": "#1C1C1C", "usage": "角落", "group": "environment"}]}`
	if n, err := CountScenePaletteTurn(ok, turn2); err != nil || n != 1 {
		t.Fatalf("第二轮承接上一集应合格：%d %v", n, err)
	}
	for name, text := range map[string]string{
		"承接色 HEX 改了": strings.Replace(ok, `"hex": "#9AA3A8"`, `"hex": "#9AA3A9"`, 1),
		"不到三色":       strings.Replace(ok, `, {"name_zh": "暗影", "name_en": "Shadow", "hex": "#1C1C1C", "usage": "角落", "group": "environment"}`, "", 1),
		"HEX 不合法":    strings.Replace(ok, `"#B3261E"`, `"red"`, 1),
		"集号不对":       strings.Replace(ok, `"E02S01"`, `"E03S01"`, 1),
		"一场都没有":      `{"episode": 2}`,
	} {
		if _, err := CountScenePaletteTurn(text, turn2); err == nil {
			t.Errorf("%s 应重试这一轮", name)
		}
	}
}

func TestCheckScenePalette(t *testing.T) {
	out, _ := ScenePaletteFromText(scenePaletteTurn1, 0)
	ctx := ScenePaletteCheckContext{Episodes: []ScenePaletteEpisode{{No: 1, Title: "七床空了", Scenes: []ScenePaletteScene{{No: 1, Heading: "内·病房走廊·夜"}, {No: 2, Heading: "内·电梯口·夜"}, {No: 3, Heading: "外·医院门口·夜"}}}}}
	report := CheckScenePalette(out, ctx)
	if report.Errors != 0 || report.Warnings != 1 || report.Items[0].Rule != RuleScenePaletteScenes || report.Items[0].ShotID != "E01S03" {
		t.Fatalf("漏写第 3 场只该是 warn：%+v", report)
	}
	// 承接本次没写、已有色彩脚本里也没有的场：error。
	bad, _ := ScenePaletteFromText(strings.Replace(scenePaletteTurn1, `"inherits_from": "e1s1"`, `"inherits_from": "E09S01"`, 1), 0)
	if r := CheckScenePalette(bad, ctx); r.Errors != 1 || r.Items[0].Rule != RuleScenePaletteInherit {
		t.Fatalf("承接不存在的场应报 error：%+v", r)
	}
	// 承接已有色彩脚本里的场：承接色要对得上。
	existing := []ColorScriptEntry{{Episode: 9, SceneNo: 1, Scene: "外·天台·夜", SharedColors: []PaletteColor{{NameZh: "水泥灰", Hex: "#6E6E6E"}}, NewColors: []PaletteColor{{NameZh: "护士蓝", Hex: "#5B7FA4"}}}}
	r := CheckScenePalette(bad, ScenePaletteCheckContext{Episodes: ctx.Episodes, Existing: existing})
	if r.Errors != 1 || r.Items[0].Rule != RuleScenePaletteSharedHex || !strings.Contains(r.Items[0].Message, "#5B7FA4") {
		t.Fatalf("护士蓝 HEX 与承接场不一致应报 error：%+v", r)
	}
	if r := CheckScenePalette(ScenePaletteOutput{}, ctx); r.Errors != 1 || r.Items[0].Rule != RuleScenePaletteNoScript {
		t.Fatalf("没有色彩脚本是 error：%+v", r)
	}
	// 与主基调色卡同名色 HEX 不一致：warn；没写分组：warn；多写的风格锚：warn。
	master := MasterPalette{Groups: []MasterPaletteGroup{{Key: PaletteGroupProtagonist, Colors: []PaletteColor{{NameZh: "护士蓝", NameEn: "Nurse Blue", Hex: "#5B7FA0"}}}}}
	ungrouped := strings.Replace(scenePaletteTurn1, `, "group": "props"`, "", 1)
	withAnchor, _ := ScenePaletteFromText(`{"style_anchor": {"medium": "真人实拍"}}`+"\n"+ungrouped, 0)
	r = CheckScenePalette(withAnchor, ScenePaletteCheckContext{Master: master})
	rules := map[string]int{}
	for _, item := range r.Items {
		rules[item.Rule]++
	}
	if r.Errors != 0 || rules[RuleScenePaletteMasterHex] != 2 || rules[RuleScenePaletteNoGroup] != 1 || rules[RuleScenePaletteAnchor] != 1 {
		t.Fatalf("主色卡同名色、缺分组、风格锚都是 warn：%+v", r.Items)
	}
	// 超过合成图能画的色数：error。
	many := ScenePaletteOutput{Script: []ColorScriptEntry{{Episode: 1, SceneNo: 1}}}
	for range ScenePaletteMaxColors + 1 {
		many.Script[0].NewColors = append(many.Script[0].NewColors, PaletteColor{NameZh: "灰", NameEn: "Grey", Hex: "#777777", Group: PaletteGroupEnvironment})
	}
	if r := CheckScenePalette(many, ScenePaletteCheckContext{}); r.Errors != 1 || r.Items[0].Rule != RuleScenePaletteManyColors {
		t.Fatalf("超过 %d 色应报 error：%+v", ScenePaletteMaxColors, r.Items)
	}
}

func TestScenePaletteTitlesAndGroups(t *testing.T) {
	out, _ := ScenePaletteFromText(scenePaletteTurn1, 0)
	e := out.Script[1]
	e.Scene = "内·电梯口·夜"
	if PaletteTitle(e) != "E01S02 色卡 · 内·电梯口·夜" || PaletteHeading(e) != "E01S02 · 内·电梯口·夜" {
		t.Fatalf("色卡标题：%s / %s", PaletteTitle(e), PaletteHeading(e))
	}
	if PaletteDesign(e) != "3色复合色板=2色跨段共享 + 1色本段新增（承接 E01S01）" || PaletteDesign(out.Script[0]) != "3色场景基础色板=2色基础色 + 1色新增色" {
		t.Fatalf("色板设计句：%s / %s", PaletteDesign(e), PaletteDesign(out.Script[0]))
	}
	groups := ScenePaletteGroups(e)
	keys := make([]string, 0, len(groups.Groups))
	for _, g := range groups.Groups {
		keys = append(keys, g.Key)
	}
	if !slices.Equal(keys, []string{PaletteGroupProtagonist, PaletteGroupProps, PaletteGroupEnvironment}) || len(groups.Colors()) != 3 {
		t.Fatalf("按主色卡分组顺序排：%v", keys)
	}
	if line := ScenePaletteColorsLine(e.Colors()); line != "本场色值：水泥灰 #6E6E6E、护士蓝 #5B7FA3、电梯钢 #9AA3A8。" {
		t.Fatalf("色值行：%s", line)
	}
}

// 场次色卡图：1920×1080，每块色块中心像素与 HEX 一致（按版式算位置，与主色卡同一版式：一组一行）。
func TestRenderScenePalettePNG(t *testing.T) {
	out, _ := ScenePaletteFromText(scenePaletteTurn1, 0)
	e := out.Script[1]
	e.Scene = "内·电梯口·夜"
	raw, err := RenderScenePalettePNG(e)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != MasterPaletteImageWidth || b.Dy() != MasterPaletteImageHeight {
		t.Fatalf("尺寸：%v", b)
	}
	groups := ScenePaletteGroups(e).Groups
	checkPaletteSwatches(t, img, groups)
	if _, err := RenderScenePalettePNG(ColorScriptEntry{Episode: 1, SceneNo: 1, NewColors: []PaletteColor{{Hex: "bad"}}}); err == nil {
		t.Fatal("没有能画的颜色应报错")
	}
}

// checkPaletteSwatches 按 renderPalettePNG 的版式算每块色块的中心，取像素与 HEX 比（每个通道差 ≤2）。
func checkPaletteSwatches(t *testing.T, img image.Image, groups []MasterPaletteGroup) {
	t.Helper()
	const margin, top, labelW, gap = 72, 176, 236, 16
	bottom := MasterPaletteImageHeight - 56
	right := MasterPaletteImageWidth - margin
	rowH := min(280, (bottom-top)/len(groups))
	for i, g := range groups {
		y := top + i*rowH
		n := len(g.Colors)
		w := min(220, (right-(margin+labelW)-(n-1)*gap)/n)
		h := max(48, rowH-84-22)
		for j, c := range g.Colors {
			x := margin + labelW + j*(w+gap)
			want, _ := hexRGBA(c.Hex)
			r, gg, b, _ := img.At(x+w/2, y+h/2).RGBA()
			got := [3]int{int(r >> 8), int(gg >> 8), int(b >> 8)}
			for k, v := range [3]int{int(want.R), int(want.G), int(want.B)} {
				if d := got[k] - v; d > 2 || d < -2 {
					t.Fatalf("%s 第 %d 块中心像素 %v，应为 %s", g.Key, j+1, got, c.Hex)
				}
			}
		}
	}
}

func TestBuildScenePalettePrompt(t *testing.T) {
	scenes := ScenePaletteScenes("## 内·病房走廊·夜\n（走廊。）\n林小满：七床？\n## 内·电梯口·夜\n（电梯门开了。）", "第 1 集", 300)
	if len(scenes) != 2 || scenes[0].Heading != "内·病房走廊·夜" || scenes[1].Location != "电梯口" || !strings.Contains(scenes[0].Excerpt, "林小满：七床？") {
		t.Fatalf("按场次头切场：%+v", scenes)
	}
	if one := ScenePaletteScenes("没有场次头的小说正文。", "第一章", 300); len(one) != 1 || one[0].Heading != "第一章" {
		t.Fatalf("没有场次头时整章一场：%+v", one)
	}
	if ScenePaletteExcerptLimit(10) != 300 || ScenePaletteExcerptLimit(1000) != ScenePaletteMinExcerptChars {
		t.Fatal("摘录字数按场数在 80 到 300 之间")
	}
	skill := StepSkill{ID: "yz-scene-palette", Name: "场次色卡", Files: []StepSkillFile{{Path: "SKILL.md", Kind: "markdown", Content: "# 场次色卡"}}}
	master := MasterPalette{Groups: []MasterPaletteGroup{
		{Key: PaletteGroupProtagonist, Colors: []PaletteColor{{NameZh: "护士蓝", NameEn: "Nurse Blue", Hex: "#5B7FA3", Usage: "林小满制服"}}},
		{Key: PaletteGroupEnvironment, Colors: []PaletteColor{{NameZh: "日光灯白", NameEn: "Fluorescent White", Hex: "#E8ECEF", Usage: "医院顶灯"}}},
	}}
	prompt, err := BuildScenePalettePrompt(ScenePaletteInput{
		Episodes: []ScenePaletteEpisode{{No: 1, Title: "七床空了", Scenes: scenes}},
		Anchor:   &StyleAnchor{Medium: "真人实拍", Grading: "低饱和冷调", Forbidden: []string{"霓虹"}},
		Master:   master,
		Profile:  StyleBibleProfile{Label: "真人剧（默认）", StyleBlock: "真人剧质感", LightPolicy: "practicals_only"},
		Existing: []ColorScriptEntry{{Episode: 2, SceneNo: 1, Scene: "外·天台·夜", NewColors: []PaletteColor{{NameZh: "夜蓝", NameEn: "Night Blue", Hex: "#1B2A41"}}}},
		Skill:    skill,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"E01S01 场次头：内·病房走廊·夜（内外：内；地点：病房走廊；时间：夜）", "E01S02 场次头：内·电梯口·夜",
		"【项目风格锚】", "- 调色：低饱和冷调", "- 禁用：霓虹", "本步骤只写色彩脚本，不改风格锚",
		"【主基调色卡】", "主角组（group: protagonist）：", "- 护士蓝 / Nurse Blue #5B7FA3：林小满制服", "环境组（group: environment）：",
		"【风格档案：真人剧（默认）】", "光线政策：practicals_only", "【已有场次色卡】", "E02S01「外·天台·夜」：夜蓝/Night Blue #1B2A41",
		"【受保护输出契约】", "inherits_from", "至少输出 1 场",
	} {
		if !strings.Contains(prompt.Text, want) {
			t.Errorf("提示词缺「%s」", want)
		}
	}
	turns := ScenePaletteTurns([]ScenePaletteEpisode{{No: 1, Title: "a"}, {No: 3, Title: "c"}})
	if len(turns) != 2 || turns[1].ID != "3" || !strings.Contains(ScenePaletteTurnPrompt(turns, 2), "E03S") || !strings.Contains(ScenePaletteTurnPlan(turns), "共 2 集") {
		t.Fatalf("逐集轮次：%+v", turns)
	}
}

// 本地仓库的 yz-scene-palette 包要能过校验，且和内置种子逐字一致；SKILL.md 要点名色板设计那张卡，让它注入技能上下文。
// 技能正文不改：「CL 色彩光影设计卡」一节与 yz-style-lock（同一段《一线导演剧本创作 Skill》原文）逐字一致，色板设计卡是原卡。
func TestScenePalettePackageMatchesBuiltinSeed(t *testing.T) {
	local := readPackageDir(t, "../../../yoozee-skills/packages/yz-scene-palette")
	seed := readPackageDir(t, "../../builtin/yz-scene-palette")
	if len(local) != len(seed) {
		t.Fatalf("文件数不一致：本地 %d，内置 %d", len(local), len(seed))
	}
	for p, content := range local {
		if string(seed[p]) != string(content) {
			t.Fatalf("%s 与内置种子不一致", p)
		}
	}
	if problems := checkStepPackageForTest(local[StepPackageManifestFile], local); len(problems) > 0 {
		t.Fatalf("yz-scene-palette 包没过校验：%+v", problems)
	}
	m, _ := ParseStepPackageManifest(local[StepPackageManifestFile])
	if m.ID != "yz-scene-palette" || m.Step != StepScenePalette || m.Contract != ContractScenePalette || !slices.Equal(m.Profiles, BuiltinProfileIDs()) {
		t.Fatalf("清单：%+v", m)
	}
	skill := StepSkill{ID: m.ID, Name: "场次色卡"}
	for p, content := range local {
		skill.Files = append(skill.Files, StepSkillFile{Path: p, Kind: "markdown", Content: string(content)})
	}
	_, files, ok := StepSkillPrompt(skill, ScenePaletteTask(nil, false), 32_000, 3)
	if !ok || !slices.ContainsFunc(files, func(f StepSkillFileUse) bool { return f.Path == "cards/色卡提示词卡-色板设计.md" }) {
		t.Fatalf("色板设计卡没有注入技能上下文：%+v", files)
	}
	lock, err := os.ReadFile("../../builtin/yz-style-lock/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(local["SKILL.md"])
	start := strings.Index(body, "### 3. CL 色彩光影设计卡与 ST 的关系")
	end := strings.Index(body, "\n---\n\n## 柚子流程接入说明")
	if start < 0 || end < start || !strings.Contains(string(lock), body[start:end]) {
		t.Fatal("「CL 色彩光影设计卡」一节应与 yz-style-lock 的原文逐字一致")
	}
	for _, want := range []string{"【用途】", "【色板设计】", "【视觉影视参考预案】"} {
		if !strings.Contains(string(local["cards/色卡提示词卡-色板设计.md"]), want) {
			t.Fatalf("色板设计卡缺「%s」", want)
		}
	}
}
