package drama

import (
	"slices"
	"strings"
	"testing"
)

const styleLockOutput = `{"style_anchor": {"medium": "真人实拍", "lighting": "实景光源，冷暖分区", "camera": "克制，定焦", "grading": "低饱和冷调", "forbidden": ["霓虹", "HDR"]}}
{"group": "主角组", "colors": [{"name_zh": "护士蓝", "name_en": "Nurse Blue", "hex": "5b7fa3", "usage": "林小满制服"}, {"name_zh": "苍白肤", "name_en": "Pale Skin", "hex": "#E3D3C4", "usage": "主角肤色"}]}
{"group": "配角", "colors": [{"name_zh": "病号条纹", "name_en": "Patient Stripe", "hex": "#8DA1B9", "usage": "病人服"}]}
{"group": "道具组", "colors": [{"name_zh": "铁锈红", "name_en": "Rust Red", "hex": "#7A3B2E", "usage": "七床床牌"}]}
{"group": "环境组", "colors": [{"name_zh": "日光灯白", "name_en": "Fluorescent White", "hex": "#E8ECEF", "usage": "走廊顶灯"}, {"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": ["#6E6E6E"], "usage": "墙面"}]
`

func TestStyleLockFromText(t *testing.T) {
	out, stats := StyleLockFromText("```json\n" + styleLockOutput + "```\n说明文字\n")
	if out.Anchor == nil || out.Anchor.Medium != "真人实拍" || !stats.Anchor {
		t.Fatalf("风格锚没读出来：%+v", out.Anchor)
	}
	if stats.Groups != 4 || stats.Colors != 6 || stats.Repaired != 1 {
		t.Fatalf("应读出 4 组 6 色（环境组那行缺括号补齐）：%+v", stats)
	}
	keys := make([]string, 0, len(out.Palette.Groups))
	for _, g := range out.Palette.Groups {
		keys = append(keys, g.Key)
	}
	if !slices.Equal(keys, []string{PaletteGroupProtagonist, PaletteGroupSupporting, PaletteGroupProps, PaletteGroupEnvironment}) || out.Palette.Groups[1].Name != "配角组" {
		t.Fatalf("分组应按固定顺序、组名规整：%+v", out.Palette.Groups)
	}
	if out.Palette.Groups[0].Colors[0].Hex != "#5B7FA3" || out.Palette.Groups[3].Colors[1].Hex != "#6E6E6E" {
		t.Fatalf("HEX 应规整，数组 HEX 取第一个：%+v", out.Palette.Groups)
	}
	report := CheckStyleLock(out)
	if report.Errors != 0 || !slices.ContainsFunc(report.Items, func(i CheckItem) bool { return i.Rule == RuleLockFieldType }) {
		t.Fatalf("应无 error、hex 写成数组报 R-LOCK-006：%+v", report)
	}
	if n, err := CountStyleLockTurn(styleLockOutput, TurnContext{Index: 1}); err != nil || n != 6 {
		t.Fatalf("这一轮应合格、6 色：%d %v", n, err)
	}
}

func TestStyleLockWholePaletteLineAndChecks(t *testing.T) {
	text := `{"medium": "3D 动画", "art_style": "半写实"}
{"master_palette": {"groups": [{"group": "主角组", "colors": [{"name_zh": "暖橙", "name_en": "Warm Orange", "hex": "#E07A2E", "usage": "主角外套"}]}, {"group": "主角", "colors": [{"name_zh": "", "name_en": "Deep Navy", "hex": "#1B2A41", "usage": "主角裤子"}]}]}}`
	out, stats := StyleLockFromText(text)
	if !stats.Anchor || stats.Groups != 1 || stats.Colors != 2 {
		t.Fatalf("没包 style_anchor 的风格锚、整张色板一行都应读出，同组合并：%+v %+v", stats, out.Palette)
	}
	report := CheckStyleLock(out)
	rules := map[string]int{}
	for _, item := range report.Items {
		rules[item.Rule]++
	}
	if report.Errors != 1 || rules[RuleLockFewColors] != 1 || rules[RuleLockGroupMissing] != 1 || rules[RuleLockColorName] != 1 {
		t.Fatalf("2 色不够（error），缺环境组、缺中文名（warn）：%+v", report.Items)
	}
	if _, err := CountStyleLockTurn(text, TurnContext{Index: 1}); err == nil || !strings.Contains(err.Error(), "至少 4 个") {
		t.Fatalf("色数不够这一轮应重试：%v", err)
	}
	if _, err := CountStyleLockTurn(`{"group": "主角组", "colors": []}`, TurnContext{Index: 1}); err == nil || !strings.Contains(err.Error(), "风格锚") {
		t.Fatalf("没有风格锚这一轮应重试：%v", err)
	}
	if err := ValidateMasterPalette(MasterPalette{Groups: []MasterPaletteGroup{{Key: PaletteGroupProps, Colors: []PaletteColor{{NameZh: "坏", Hex: "#12"}}}}}); err == nil {
		t.Fatal("HEX 不合法的色板应拒绝写库")
	}
}

func TestBuildStyleLockPrompt(t *testing.T) {
	skill := StepSkill{ID: "yz-style-lock", Files: []StepSkillFile{{Path: "SKILL.md", Kind: "markdown", Content: "# 风格锁定\n先锁风格。"}}}
	prompt, err := BuildStyleLockPrompt(StyleLockInput{
		ProjectType: "live-action", Text: "都市冷调悬疑，雨夜霓虹少用", Images: 3,
		Style:   StepStyle{Title: "悬疑犯罪 · 真人实拍", Prompt: "【色彩与光影】冷色调", Selection: map[string]string{"medium": "live-action"}},
		Profile: StyleBibleProfile{Label: "真人剧（默认）", StyleBlock: "实拍质感", LightPolicy: "practicals_only"},
		Skill:   skill,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"【项目类型】仿真人", "【用户的风格描述】", "都市冷调悬疑", "附了 3 张参考图", "【项目画风：悬疑犯罪 · 真人实拍】", "以用户为准", "【风格档案：真人剧（默认）】", "【受保护输出契约】", "主角组", "group（分组名"} {
		if !strings.Contains(prompt.Text, want) {
			t.Errorf("提示词缺「%s」", want)
		}
	}
	bare, err := BuildStyleLockPrompt(StyleLockInput{ProjectType: "2d-anime", Skill: skill})
	if err != nil || !strings.Contains(bare.Text, "用户没有写风格描述，也没有上传参考图") || strings.Contains(bare.Text, "【项目画风：") {
		t.Fatalf("只按画风定时应写明，没有画风不写画风段：%v\n%s", err, bare.Text)
	}
	if got := StyleLockAnchorPrompt(&StyleAnchor{Medium: "真人实拍", Forbidden: []string{"霓虹"}}, "改编时守住。"); !strings.Contains(got, "- 画风 / 媒介：真人实拍") || !strings.Contains(got, "- 禁用：霓虹") {
		t.Fatalf("风格锚段：%s", got)
	}
	if StyleLockAnchorPrompt(nil, "x") != "" {
		t.Fatal("没有锁定风格时不写")
	}
}

// 本地仓库的 yz-style-lock 包要能过校验、和内置种子逐字一致；正文与 yz-style-bible 的「六A、视觉风格锁定系统」同一段原文。
func TestStyleLockPackageMatchesBuiltinSeed(t *testing.T) {
	local := readPackageDir(t, "../../../yoozee-skills/packages/yz-style-lock")
	seed := readPackageDir(t, "../../builtin/yz-style-lock")
	if len(local) != len(seed) {
		t.Fatalf("文件数不一致：本地 %d，内置 %d", len(local), len(seed))
	}
	for p, content := range local {
		if string(seed[p]) != string(content) {
			t.Fatalf("%s 与内置种子不一致", p)
		}
	}
	if problems := checkStepPackageForTest(local[StepPackageManifestFile], local); len(problems) > 0 {
		t.Fatalf("yz-style-lock 包没过校验：%+v", problems)
	}
	m, _ := ParseStepPackageManifest(local[StepPackageManifestFile])
	if m.ID != "yz-style-lock" || m.Step != StepStyleLock || m.Contract != ContractStyleLock || !slices.Equal(m.Profiles, BuiltinProfileIDs()) {
		t.Fatalf("清单：%+v", m)
	}
	section := func(body string) string {
		start := strings.Index(body, "## 六A、视觉风格锁定系统")
		end := strings.Index(body, "## 柚子流程接入说明")
		if start < 0 || end < start {
			t.Fatal("正文缺「六A」或接入说明")
		}
		return body[start:end]
	}
	bible := readPackageDir(t, "../../builtin/yz-style-bible")
	if section(string(local["SKILL.md"])) != section(string(bible["SKILL.md"])) {
		t.Fatal("yz-style-lock 的风格锁定正文应与 yz-style-bible 一字不差")
	}
}
