package drama

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const ontologyBuiltinDir = "../../builtin/yz-ontology-director"

// ontologyExampleCard 把技能示例 01（写实·当铺）拼成每集输出里单元卡的标准写法：标题行、【导演分镜卡】、【粘贴提示词】代码块。
func ontologyExampleCard(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(ontologyBuiltinDir, "examples", "01_写实_当铺.md"))
	if err != nil {
		t.Fatal(err)
	}
	fences := regexp.MustCompile("(?s)```\\n(.*?)\\n```").FindAllStringSubmatch(string(raw), -1)
	var card, paste string
	for _, f := range fences {
		switch {
		case strings.Contains(f[1], "### 单元卡"):
			card = f[1]
		case strings.Contains(f[1], "素材映射"):
			paste = f[1]
		}
	}
	if card == "" || paste == "" {
		t.Fatal("示例 01 里找不到单元卡或粘贴提示词")
	}
	return card + "\n\n【粘贴提示词】\n```\n" + paste + "\n```\n"
}

func ontologyMaterials(n int) []OntologyMaterial {
	cats := []string{"character", "character", "environment", "prop"}
	out := make([]OntologyMaterial, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, OntologyMaterial{Index: i, Name: "素材" + string(rune('0'+i)), Category: cats[(i-1)%len(cats)], VersionID: "ver-" + string(rune('0'+i))})
	}
	return out
}

func TestParseOntologyUnitCardFromSkillExample(t *testing.T) {
	text := "## 五、单元卡\n" + ontologyExampleCard(t) + "\n## 六、本集交接卡\n本集无\n"
	cards, err := ParseOntologyUnitCards(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("cards = %d", len(cards))
	}
	c := cards[0]
	if c.ID != "s02-u1" || c.Scene != "s02" || c.TotalSeconds != 29 || c.NarrativeSeconds != 28 || c.Shots != 10 || c.Tempo != "短剧" {
		t.Fatalf("header = %+v", c)
	}
	if !strings.HasPrefix(c.Paste, "29秒") && !strings.Contains(c.Paste[:80], "秒") {
		t.Fatalf("paste should start with the duration sentence: %q", c.Paste[:60])
	}
	if strings.Contains(c.Text, "## 六") || strings.Contains(c.Paste, "```") {
		t.Fatal("card must stop at the next section and paste must exclude fences")
	}
	if err := ValidateOntologyUnit(c, "s02-u1", OntologyUnitLimits{MaxSeconds: 30, Materials: 4}); err != nil {
		t.Fatalf("example should pass: %v", err)
	}
	if err := ValidateOntologyUnit(c, "s02-u1", OntologyUnitLimits{MaxSeconds: 15, Materials: 4}); err == nil || !strings.Contains(err.Error(), "总时长") {
		t.Fatalf("15s model must reject a 29s unit: %v", err)
	}
	if err := ValidateOntologyUnit(c, "s02-u1", OntologyUnitLimits{MaxSeconds: 30, Materials: 3}); err == nil || !strings.Contains(err.Error(), "@图4") {
		t.Fatalf("missing material must be reported: %v", err)
	}

	seg, err := OntologySegment(c, 3, ontologyMaterials(4))
	if err != nil {
		t.Fatal(err)
	}
	if seg.ID != "S03" || seg.Title != "s02-u1" || seg.Scene != "s02" || seg.Dur != 29 || seg.Mode != ModeLockIdentity {
		t.Fatalf("segment = %+v", seg)
	}
	if len(seg.Refs) != 4 || seg.Refs[0].File != "ver-1" || seg.Refs[2].Role != "location" || seg.Refs[3].Role != "prop" {
		t.Fatalf("refs = %+v", seg.Refs)
	}
	if len(seg.Shots) != 11 || seg.Shots[1].At != 2 || seg.Shots[10].Purpose != "站位交接" {
		t.Fatalf("shots = %d %+v", len(seg.Shots), seg.Shots)
	}
	if _, err := ParseSegment(mustJSON(t, seg)); err != nil {
		t.Fatalf("segment must round-trip through the contract: %v", err)
	}
}

func TestOntologySegmentRenumbersImagesByUse(t *testing.T) {
	card := OntologyUnitCard{ID: "s01-u2", Scene: "s01", TotalSeconds: 12, Shots: 3, Card: "无表",
		Paste: "12秒。素材映射：@图3 为当铺；@图1 为林夏。林夏与@图1一致，站在@图3的柜台前。本段不使用背景音乐。"}
	seg, err := OntologySegment(card, 1, ontologyMaterials(4))
	if err != nil {
		t.Fatal(err)
	}
	if len(seg.Refs) != 2 || seg.Refs[0].File != "ver-3" || seg.Refs[1].File != "ver-1" {
		t.Fatalf("refs = %+v", seg.Refs)
	}
	if !strings.Contains(seg.Verbatim, "@图1 为当铺；@图2 为林夏。林夏与@图2一致，站在@图1的柜台前") {
		t.Fatalf("verbatim = %s", seg.Verbatim)
	}
	if len(seg.Shots) != 1 || seg.Shots[0].Text != "无表" {
		t.Fatalf("fallback shot = %+v", seg.Shots)
	}
	m, err := ResolveModel("")
	if err != nil {
		t.Fatal(err)
	}
	res, err := CompileWith(seg, m, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 || res.Files[0].File != "ver-3" || res.Files[0].Tag != ReferenceImageTag(m, 1) {
		t.Fatalf("files = %+v", res.Files)
	}
	if !strings.Contains(res.Prompt, ReferenceImageTag(m, 2)+"一致") || strings.Contains(res.Prompt, "@图3") {
		t.Fatalf("prompt = %s", res.Prompt)
	}
	if _, err := OntologySegment(OntologyUnitCard{ID: "s01-u3", Scene: "s01", TotalSeconds: 5, Paste: "@图9"}, 1, ontologyMaterials(2)); err == nil {
		t.Fatal("unknown material must fail")
	}
}

func TestValidateOntologyUnitRules(t *testing.T) {
	base := OntologyUnitCard{ID: "s01-u1", Scene: "s01", TotalSeconds: 10, Shots: 2, Paste: "10秒。" + OntologyNoMusicLine + "。"}
	if err := ValidateOntologyUnit(base, "s01-u1", OntologyUnitLimits{Materials: 0}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]OntologyUnitCard{
		"本段不使用背景音乐": {ID: "s01-u1", TotalSeconds: 10, Shots: 2, Paste: "10秒。"},
		"场次或单元编号":   {ID: "s01-u1", TotalSeconds: 10, Shots: 2, Paste: "接 s01-u0。" + OntologyNoMusicLine},
		"同上":        {ID: "s01-u1", TotalSeconds: 10, Shots: 2, Paste: "服装同上。" + OntologyNoMusicLine},
		"@音频":       {ID: "s01-u1", TotalSeconds: 10, Shots: 2, Paste: "音色参考@音频1。" + OntologyNoMusicLine},
		"单元编号应为":    {ID: "s01-u2", TotalSeconds: 10, Shots: 2, Paste: OntologyNoMusicLine},
		"超过上限":      {ID: "s01-u1", TotalSeconds: 10, Shots: 2, Paste: strings.Repeat("字", OntologyPromptMaxChars) + OntologyNoMusicLine},
	}
	for want, card := range cases {
		if err := ValidateOntologyUnit(card, "s01-u1", OntologyUnitLimits{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	tooMany := base
	tooMany.Paste = "@图1@图2@图3。" + OntologyNoMusicLine
	if err := ValidateOntologyUnit(tooMany, "s01-u1", OntologyUnitLimits{Materials: 3, MaxImages: 2}); err == nil || !strings.Contains(err.Error(), "上限 2") {
		t.Fatalf("image cap: %v", err)
	}
}

func TestOntologyPlanSectionsAndRhythmUnits(t *testing.T) {
	plan := "## 一、本集定位\n本集一句话：讨债\n\n## 二、台词对照表\n本集无\n\n## 三、节奏总表\n| 单元 | 所属场 |\n|---|---|\n| s01-u1 | s01 |\n| 【s01-u2】 | s01 |\n| s02-u1 | s02 |\n| s01-u1 | s01 |\n"
	if err := OntologyRequireSections(plan, "一、本集定位", "二、台词对照表", "三、节奏总表"); err != nil {
		t.Fatal(err)
	}
	if err := OntologyRequireSections(plan, "四、讲戏本"); err == nil || !strings.Contains(err.Error(), "## 四、讲戏本") {
		t.Fatalf("missing section: %v", err)
	}
	units, err := OntologyRhythmUnits(plan)
	if err != nil || strings.Join(units, ",") != "s01-u1,s01-u2,s02-u1" {
		t.Fatalf("units = %v %v", units, err)
	}
	if _, err := OntologyRhythmUnits("## 三、节奏总表\n| 单元 |\n|---|\n"); err == nil {
		t.Fatal("empty rhythm table must fail")
	}
}

func TestOntologyEpisodeDocumentAndHardFailures(t *testing.T) {
	check := "## 六、本集交接卡\n【集交接卡】第1集 → 第2集\n\n## 七、自检结果与待确认清单\n" +
		"| 单元 | H1 | H2 | H3 | 表演分 | 备注 |\n|---|---|---|---|---|---|\n| s01-u1 | ✓ | ✓ | ✓ | 5 | — |\n| s01-u2 | ✓ | ✗ | ✓ | 3 | 多了路人 |\n\n" +
		"| 编号 | 位置 | 剧本缺什么 | 我的导演提案 | 理由 | 如果你不同意，会影响哪里 |\n|---|---|---|---|---|---|\n| Q1 | s01-u1 | 时段 | 夜 | X 光 | ✗ |\n"
	if got := OntologyHardFailures(check); strings.Join(got, ",") != "s01-u2 H2" {
		t.Fatalf("hard failures = %v", got)
	}
	doc := OntologyEpisodeDocument("## 一、本集定位\nA\n## 三、节奏总表\nC", "## 四、讲戏本\nD", []OntologyUnitCard{{Text: "### 单元卡 s01-u1｜…"}}, check)
	for _, title := range []string{"## 一、本集定位\nA", "## 二、台词对照表\n本集无", "## 三、节奏总表\nC", "## 四、讲戏本\nD", "## 五、单元卡\n### 单元卡 s01-u1", "## 六、本集交接卡\n【集交接卡】", "## 七、自检结果与待确认清单"} {
		if !strings.Contains(doc, title) {
			t.Errorf("document missing %q", title)
		}
	}
}

func TestOntologyStageFilesFromBuiltinPackage(t *testing.T) {
	skill := StepSkill{ID: OntologySkillID}
	err := filepath.WalkDir(ontologyBuiltinDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(ontologyBuiltinDir, p)
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		skill.Files = append(skill.Files, StepSkillFile{Path: filepath.ToSlash(rel), Kind: "markdown", Content: string(content)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{OntologyTurnProject, OntologyTurnPlan, OntologyTurnDirect, OntologyTurnUnit, OntologyTurnCheck, OntologyTurnRepair} {
		ctx, uses, err := OntologySkillContext(skill, kind)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if uses[0].Path != "SKILL.md" || uses[1].Path != "params.md" {
			t.Fatalf("%s: SKILL.md and params.md must lead: %+v", kind, uses)
		}
		for _, u := range uses {
			if strings.HasPrefix(u.Path, "archive/") || strings.HasPrefix(u.Path, "dist/") || strings.HasPrefix(u.Path, "tests/") {
				t.Fatalf("%s loads excluded %s", kind, u.Path)
			}
			full := skill.file(u.Path).Content
			if !strings.Contains(ctx, full) {
				t.Fatalf("%s: %s must be loaded verbatim, never truncated", kind, u.Path)
			}
		}
		if OntologyTurnInstruction(kind, "s01-u1") == "" {
			t.Fatalf("%s has no instruction", kind)
		}
	}
	if _, err := OntologyStageFiles(skill, "nope"); err == nil {
		t.Fatal("unknown turn kind must fail")
	}
}

func TestOntologyMaterialPackage(t *testing.T) {
	pkg := OntologyMaterialPackage(OntologyBrief{Genre: "悬疑", AspectRatio: "9:16", Medium: "写实", EpisodeSeconds: 120, MaxUnitSeconds: 15}, ontologyMaterials(2), "", "上一集交接")
	for _, want := range []string{"画幅：竖屏9:16", "背景音乐：不要", "按 15 秒执行", "@图1 = 素材1 人物", "@图2 = 素材2 人物", "【世界圣经卡】\n无", "【集交接卡】\n上一集交接", "不得出现 @音频"} {
		if !strings.Contains(pkg, want) {
			t.Errorf("package missing %q:\n%s", want, pkg)
		}
	}
	if strings.Contains(OntologyMaterialPackage(OntologyBrief{MaxUnitSeconds: 30}, nil, "", ""), "平台硬约束覆盖") {
		t.Error("30s models must not override P01")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
