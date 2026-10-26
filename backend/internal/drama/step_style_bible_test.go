package drama

import (
	"slices"
	"strings"
	"testing"
)

const styleBibleTurn1 = `{"style_anchor": {"medium": "真人实拍", "lighting": "实景光源，冷暖分区", "camera": "克制，定焦", "grading": "低饱和冷调", "forbidden": ["霓虹", "HDR"]}}
{"episode": 1, "title": "七床空了"}
{"episode": 1, "scene_no": 1, "scene": "内·病房走廊·夜", "inherits_from": "", "shared_colors": [{"name_zh": "日光灯白", "name_en": "Fluorescent White", "hex": "#e8ecef", "usage": "走廊顶灯"}, {"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙面"}], "new_colors": [{"name_zh": "护士蓝", "name_en": "Nurse Blue", "hex": "5B7FA3", "usage": "林小满制服"}]}
{"episode": 1, "scene_no": 2, "scene": "内·电梯口·夜", "inherits_from": "e1s1", "shared_colors": [{"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙面"}, {"name_zh": "护士蓝", "name_en": "Nurse Blue", "hex": "#5B7FA3", "usage": "制服"}], "new_colors": [{"name_zh": "电梯钢", "name_en": "Elevator Steel", "hex": "#9AA3A8", "usage": "电梯门"}]}
`

func TestStyleBibleFromText(t *testing.T) {
	out, stats := StyleBibleFromText("```json\n"+styleBibleTurn1+"```\n说明文字\n{坏的 json}\n", 0)
	if out.Anchor == nil || out.Anchor.Medium != "真人实拍" || len(out.Anchor.Forbidden) != 2 {
		t.Fatalf("风格锚没读出来：%+v", out.Anchor)
	}
	if len(out.Script) != 2 || stats.Scenes != 2 || stats.Episodes != 1 || stats.Invalid != 1 || !stats.Anchor {
		t.Fatalf("应读出 2 场、1 行坏 JSON：%+v %+v", out.Script, stats)
	}
	first, second := out.Script[0], out.Script[1]
	if first.Code() != "E01S01" || first.SharedColors[0].Hex != "#E8ECEF" || first.NewColors[0].Hex != "#5B7FA3" {
		t.Fatalf("HEX 应规整成大写 #RRGGBB：%+v", first)
	}
	if second.InheritsFrom != "E01S01" || out.Titles[1] != "七床空了" {
		t.Fatalf("场编号应规整、集标题应读出：%+v %v", second, out.Titles)
	}
	// 没写 episode 的场归到前面的 JSON 头。
	loose, _ := StyleBibleFromText(`{"episode": 3}`+"\n"+`{"scene_no": 1, "scene": "外·天台·日", "shared_colors": [], "new_colors": []}`, 0)
	if len(loose.Script) != 1 || loose.Script[0].Code() != "E03S01" {
		t.Fatalf("没写 episode 的场应归到第 3 集：%+v", loose.Script)
	}
}

func TestCountStyleBibleTurn(t *testing.T) {
	turn := TurnContext{Index: 1, Turn: ShotsScene{ID: "1", Heading: "七床空了"}}
	if n, err := CountStyleBibleTurn(styleBibleTurn1, turn); err != nil || n != 2 {
		t.Fatalf("第一轮应合格、写了 2 场：%d %v", n, err)
	}
	lines := strings.SplitN(styleBibleTurn1, "\n", 2)
	if _, err := CountStyleBibleTurn(lines[1], turn); err == nil || !strings.Contains(err.Error(), "风格锚") {
		t.Fatalf("第一轮没写风格锚要重试：%v", err)
	}
	turn2 := TurnContext{Index: 2, Turn: ShotsScene{ID: "2"}, Prior: styleBibleTurn1}
	ok := `{"episode": 2, "title": "电梯"}` + "\n" + `{"episode": 2, "scene_no": 1, "scene": "内·电梯里·夜", "inherits_from": "E01S02", "shared_colors": [{"name_zh": "电梯钢", "name_en": "Elevator Steel", "hex": "#9AA3A8", "usage": "轿厢"}], "new_colors": [{"name_zh": "应急红", "name_en": "Alarm Red", "hex": "#B3261E", "usage": "应急灯"}, {"name_zh": "暗影", "name_en": "Shadow", "hex": "#1C1C1C", "usage": "角落"}]}`
	if n, err := CountStyleBibleTurn(ok, turn2); err != nil || n != 1 {
		t.Fatalf("第二轮承接上一集应合格：%d %v", n, err)
	}
	for name, text := range map[string]string{
		"共享色 HEX 改了": strings.Replace(ok, `"hex": "#9AA3A8"`, `"hex": "#9AA3A9"`, 1),
		"不到三色":       strings.Replace(ok, `, {"name_zh": "暗影", "name_en": "Shadow", "hex": "#1C1C1C", "usage": "角落"}`, "", 1),
		"HEX 不合法":    strings.Replace(ok, `"#B3261E"`, `"red"`, 1),
		"集号不对":       strings.ReplaceAll(ok, `"episode": 2`, `"episode": 3`),
		"一场都没有":      `{"episode": 2}`,
	} {
		if _, err := CountStyleBibleTurn(text, turn2); err == nil {
			t.Errorf("%s 应重试这一轮", name)
		}
	}
}

func TestCheckStyleBible(t *testing.T) {
	out, _ := StyleBibleFromText(styleBibleTurn1, 0)
	ctx := StyleBibleCheckContext{Episodes: []StyleBibleEpisode{{No: 1, Title: "七床空了", Scenes: []StyleBibleScene{{No: 1, Heading: "内·病房走廊·夜"}, {No: 2, Heading: "内·电梯口·夜"}, {No: 3, Heading: "外·医院门口·夜"}}}}}
	report := CheckStyleBible(out, ctx)
	if report.Errors != 0 || report.Warnings != 1 || report.Items[0].Rule != RuleStyleScenes || report.Items[0].ShotID != "E01S03" {
		t.Fatalf("漏写第 3 场只该是 warn：%+v", report)
	}
	// 承接本次没写、已有色彩脚本里也没有的场：error。
	bad, _ := StyleBibleFromText(strings.Replace(styleBibleTurn1, `"inherits_from": "e1s1"`, `"inherits_from": "E09S01"`, 1), 0)
	if r := CheckStyleBible(bad, ctx); r.Errors != 1 || r.Items[0].Rule != RuleStyleInherit {
		t.Fatalf("承接不存在的场应报 error：%+v", r)
	}
	// 承接已有色彩脚本里的场：共享色要对得上。
	existing := []ColorScriptEntry{{Episode: 9, SceneNo: 1, Scene: "外·天台·夜", SharedColors: []PaletteColor{{NameZh: "水泥灰", Hex: "#6E6E6E"}}, NewColors: []PaletteColor{{NameZh: "护士蓝", Hex: "#5B7FA4"}}}}
	r := CheckStyleBible(bad, StyleBibleCheckContext{Episodes: ctx.Episodes, Existing: existing})
	if r.Errors != 1 || r.Items[0].Rule != RuleStyleSharedHex || !strings.Contains(r.Items[0].Message, "#5B7FA4") {
		t.Fatalf("护士蓝 HEX 与承接场不一致应报 error：%+v", r)
	}
	if r := CheckStyleBible(StyleBibleOutput{}, ctx); r.Errors != 2 {
		t.Fatalf("没有风格锚、没有色彩脚本都是 error：%+v", r)
	}
}

func TestPalettePromptUsesColorScript(t *testing.T) {
	out, _ := StyleBibleFromText(styleBibleTurn1, 0)
	e := out.Script[1]
	prompt := PalettePrompt("七床", e)
	for _, want := range []string{
		"【prompt】", "【Subject】", "【Color Specifications】", "【Composition】", "【Lighting】", "【Style】", "【Quality】", "【Constraints】",
		"A horizontal arrangement of 3 evenly-sized", "The 3 swatches from left to right are:",
		"1. Concrete Grey #6E6E6E (墙面; shared color inherited from E01S01)",
		"3. Elevator Steel #9AA3A8 (电梯门; new color in this scene)",
		`"七床 / EP01 / SC02 COLOR REFERENCE"`, "central 80% of the canvas",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("色卡提示词缺「%s」：\n%s", want, prompt)
		}
	}
	if PaletteTitle(e) != "E01S02 色卡 · 内·电梯口·夜" {
		t.Fatalf("色卡标题：%s", PaletteTitle(e))
	}
	if PaletteDesign(e) != "3色复合色板=2色跨段共享 + 1色本段新增（承接 E01S01）" || PaletteDesign(out.Script[0]) != "3色场景基础色板=2色基础色 + 1色新增色" {
		t.Fatalf("色板设计句：%s / %s", PaletteDesign(e), PaletteDesign(out.Script[0]))
	}
}

func TestBuildStyleBiblePrompt(t *testing.T) {
	scenes := StyleBibleScenes("## 内·病房走廊·夜\n（走廊。）\n林小满：七床？\n## 内·电梯口·夜\n（电梯门开了。）", "第 1 集", 300)
	if len(scenes) != 2 || scenes[0].Heading != "内·病房走廊·夜" || !strings.Contains(scenes[0].Excerpt, "林小满：七床？") {
		t.Fatalf("按场次头切场：%+v", scenes)
	}
	if one := StyleBibleScenes("没有场次头的小说正文。", "第一章", 300); len(one) != 1 || one[0].Heading != "第一章" {
		t.Fatalf("没有场次头时整章一场：%+v", one)
	}
	if StyleBibleExcerptLimit(10) != 300 || StyleBibleExcerptLimit(1000) != StyleBibleMinExcerptChars {
		t.Fatal("摘录字数按场数在 80 到 300 之间")
	}
	skill := StepSkill{ID: "yz-style-bible", Name: "导演风格", Files: []StepSkillFile{{Path: "SKILL.md", Kind: "markdown", Content: "# 导演风格"}}}
	prompt, err := BuildStyleBiblePrompt(StyleBibleInput{
		Episodes:  []StyleBibleEpisode{{No: 1, Title: "七床空了", Scenes: scenes}},
		BriefJSON: `{"title": "七床"}`,
		Style:     StepStyle{Title: "悬疑犯罪 · 真人实拍", Prompt: "【视觉媒介】采用真人影视拍摄媒介。", Selection: map[string]string{"medium": "live-action"}},
		Profile:   StyleBibleProfile{Label: "真人剧（默认）", StyleBlock: "真人剧质感", LightPolicy: "practicals_only"},
		Anchor:    &StyleAnchor{Medium: "真人实拍"},
		Existing:  []ColorScriptEntry{{Episode: 2, SceneNo: 1, Scene: "外·天台·夜", NewColors: []PaletteColor{{NameZh: "夜蓝", NameEn: "Night Blue", Hex: "#1B2A41"}}}},
		Skill:     skill,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"E01S01 场次头：内·病房走廊·夜", "【项目需求】", "【项目画风：悬疑犯罪 · 真人实拍】", "画风选项：medium=live-action", "【风格档案：真人剧（默认）】", "光线政策：practicals_only", "【已有风格锚】", "E02S01「外·天台·夜」：夜蓝/Night Blue #1B2A41", "【受保护输出契约】", "inherits_from"} {
		if !strings.Contains(prompt.Text, want) {
			t.Errorf("提示词缺「%s」", want)
		}
	}
	turns := StyleBibleTurns([]StyleBibleEpisode{{No: 1, Title: "a"}, {No: 3, Title: "c"}})
	if len(turns) != 2 || turns[1].ID != "3" || !strings.Contains(StyleBibleTurnPrompt(turns, 2), "episode 写 3") || !strings.Contains(StyleBibleTurnPlan(turns), "共 2 集") {
		t.Fatalf("逐集轮次：%+v", turns)
	}
}

// 本地仓库的 yz-style-bible 包要能过校验，且和内置种子逐字一致；SKILL.md 要点名色板设计那张卡，让它注入技能上下文。
func TestStyleBiblePackageMatchesBuiltinSeed(t *testing.T) {
	local := readPackageDir(t, "../../../yoozee-skills/packages/yz-style-bible")
	seed := readPackageDir(t, "../../builtin/yz-style-bible")
	if len(local) != len(seed) {
		t.Fatalf("文件数不一致：本地 %d，内置 %d", len(local), len(seed))
	}
	for p, content := range local {
		if string(seed[p]) != string(content) {
			t.Fatalf("%s 与内置种子不一致", p)
		}
	}
	if problems := checkStepPackageForTest(local[StepPackageManifestFile], local); len(problems) > 0 {
		t.Fatalf("yz-style-bible 包没过校验：%+v", problems)
	}
	m, _ := ParseStepPackageManifest(local[StepPackageManifestFile])
	if m.ID != "yz-style-bible" || m.Step != StepStyleBible || m.Contract != ContractStyleBible || !slices.Equal(m.Profiles, BuiltinProfileIDs()) {
		t.Fatalf("清单：%+v", m)
	}
	skill := StepSkill{ID: m.ID, Name: "导演风格"}
	for p, content := range local {
		skill.Files = append(skill.Files, StepSkillFile{Path: p, Kind: "markdown", Content: string(content)})
	}
	_, files, ok := StepSkillPrompt(skill, StyleBibleTask(nil, false), 32_000, 3)
	if !ok || !slices.ContainsFunc(files, func(f StepSkillFileUse) bool { return f.Path == "cards/色卡提示词卡-色板设计.md" }) {
		t.Fatalf("色板设计卡没有注入技能上下文：%+v", files)
	}
}
