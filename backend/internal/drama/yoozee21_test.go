package drama

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	builtinAssets "yingce/backend/builtin"
)

// 2.1 移植（vocab / script / rows / check / pack / compile）与 Python 参考实现的黄金对照。
// testdata/golden 由 yoozee-skills/tests/go_goldens.py 生成；tests/sample、tests/fixtures、tests/expected 与 Python 测试共用。

const (
	goldenDir = "testdata/golden"
	onlineDir = "testdata/online-qichuang"
	sampleDir = skillsTestDir + "/sample/剧本"
)

func readJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

// normalizeJSON 去掉 null、空串、false、空数组和空对象（Python 不写空字段，Go 用 omitempty），再比内容。
func normalizeJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, item := range x {
			n := normalizeJSON(item)
			if isEmptyJSON(n) {
				continue
			}
			out[k] = n
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, normalizeJSON(item))
		}
		return out
	default:
		return v
	}
}

func isEmptyJSON(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func asJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return normalizeJSON(out)
}

func diffJSON(t *testing.T, label string, got, want any) {
	t.Helper()
	g, w := asJSON(t, got), asJSON(t, want)
	if reflect.DeepEqual(g, w) {
		return
	}
	gb, _ := json.MarshalIndent(g, "", " ")
	wb, _ := json.MarshalIndent(w, "", " ")
	gl, wl := strings.Split(string(gb), "\n"), strings.Split(string(wb), "\n")
	for i := 0; i < max(len(gl), len(wl)); i++ {
		var a, b string
		if i < len(gl) {
			a = gl[i]
		}
		if i < len(wl) {
			b = wl[i]
		}
		if a != b {
			lo := max(0, i-6)
			t.Fatalf("%s 与 Python 不一致（第 %d 行）\n--- got\n%s\n--- want\n%s", label, i+1, strings.Join(gl[lo:min(len(gl), i+6)], "\n"), strings.Join(wl[lo:min(len(wl), i+6)], "\n"))
		}
	}
}

func testVocab(t *testing.T) *Vocab {
	t.Helper()
	v, err := BuiltinVocab()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func testProfile(t *testing.T, id string) *Profile {
	t.Helper()
	p, err := BuiltinProfile(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testShotRules(t *testing.T) RuleLevels {
	t.Helper()
	r, err := BuiltinShotRules()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestVocabCountsMatchKnowledgePackages(t *testing.T) {
	v := testVocab(t)
	got := []int{len(v.Arcs), len(v.Expressions), len(v.Actions), len(v.Vfx), len(v.Martial)}
	if want := []int{32, 101, 22, 120, 108}; !slices.Equal(got, want) {
		t.Fatalf("词库条数（情绪弧 / 表情 / 动作模式 / 特效 / 武学）= %v，应为 %v", got, want)
	}
	if v.Arc("arc-sadness-001") == nil || v.Action("act-turn") == nil {
		t.Fatal("按 id 查词库失败")
	}
}

func TestBuiltinVocabMatchesSkillSource(t *testing.T) {
	for _, rel := range []string{vocabArcsPath, vocabExpressionsPath, vocabActionsPath, vocabVfxPath, vocabMartialPath} {
		built, err := builtinAssets.FS.Open(rel)
		if err != nil {
			t.Fatal(err)
		}
		var a strings.Builder
		buf := make([]byte, 64<<10)
		for {
			n, err := built.Read(buf)
			a.Write(buf[:n])
			if err != nil {
				break
			}
		}
		built.Close()
		src, err := os.ReadFile(filepath.Join("../../../yoozee-skills/kb", rel))
		if err != nil {
			t.Fatal(err)
		}
		if a.String() != string(src) {
			t.Errorf("backend/builtin/%s 与 yoozee-skills/kb 源文件不一致", rel)
		}
	}
}

func TestProfileExtendsMergesQuotasAndRules(t *testing.T) {
	files := fstest.MapFS{
		profilesDir + "/base.json":  {Data: []byte(`{"id":"base","label":"父","style_block":{"zh":"父风格","en":"base"},"quotas":{"dutch_max":1,"consecutive_same_size_max":2,"heavy_moves":{"orbit":3,"roll":1}},"rules":{"max_move_axes":3}}`)},
		profilesDir + "/child.json": {Data: []byte(`{"id":"child","extends":"base","style_block":{"zh":"子风格"},"quotas":{"dutch_max":4},"rules":{"standing_talk_max_sec":5}}`)},
	}
	p, err := LoadProfile(files, "child")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "child" || p.Label != "父" || *p.Quotas.DutchMax != 4 || *p.Quotas.ConsecutiveSameSizeMax != 2 || len(p.Quotas.HeavyMoves) != 2 || p.Quotas.HeavyMoves[0].Key != "orbit" {
		t.Fatalf("quotas 逐键合并、其余字段子档案覆盖: %+v", p)
	}
	if p.Rules.maxMoveAxes() != 3 || p.Rules.standingTalkMaxSec() != 5 || p.StyleBlock.En != "" || p.StyleBlock.Zh != "子风格" {
		t.Fatalf("rules 逐键合并，style_block 整体覆盖: %+v %+v", p.Rules, p.StyleBlock)
	}
	game := testProfile(t, "game-ip")
	if game.Quotas.ConsecutiveSameMoveMax == nil || *game.Quotas.ConsecutiveSameMoveMax != 3 {
		t.Fatalf("game-ip 应继承 action-epic 的配额: %+v", game.Quotas)
	}
	live := testProfile(t, "live-realism")
	if live.Rules.maxMoveAxes() != 2 || live.Rules.standingTalkMaxSec() != 4 || len(live.Quotas.HeavyMoves) != 4 || live.Quotas.HeavyMoves[0].Key != "dolly_zoom" {
		t.Fatalf("live-realism 档案读取错误: %+v", live.Quotas.HeavyMoves)
	}
	if _, err := BuiltinProfile("../etc"); err == nil {
		t.Fatal("非法档案 id 应报错")
	}
}

// 七床第 1 集三个场次头（走廊、电梯口、电梯口）按新规则是两场：第 2 场是旧台词表（golden，Python from-md 一个场次头一场）的
// 第 2、3 场连起来，台词、动作按合并后的场重编号；第 1 场不变。
func TestScriptFromTextMergesConsecutiveLocation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(onlineDir, "chapter.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := ScriptFromText(string(body), "七床")
	var legacy Script
	readJSONFile(t, filepath.Join(goldenDir, "online-script.json"), &legacy)
	if len(got.Scenes) != 2 || got.LineCount() != 16 || got.SceneRule != SceneRuleMerged || len(legacy.Scenes) != 3 {
		t.Fatalf("七床 E01 应并成 2 场 16 句，得到 %d 场 %d 句", len(got.Scenes), got.LineCount())
	}
	diffJSON(t, "第 1 场与旧台词表一致", got.Scenes[0], legacy.Scenes[0])
	merged := got.Scenes[1]
	want := append(slices.Clone(legacy.Scenes[1].Beats), legacy.Scenes[2].Beats...)
	if merged.ID != "E01S02" || merged.Heading != "内·电梯口·夜" || len(merged.Beats) != len(want) {
		t.Fatalf("第 2 场应是旧第 2、3 场连起来：%s %s %d 拍", merged.ID, merged.Heading, len(merged.Beats))
	}
	for i, b := range merged.Beats {
		if b.Kind != want[i].Kind || b.Speaker != want[i].Speaker || b.Text != want[i].Text {
			t.Fatalf("第 %d 拍不同：%+v vs %+v", i, b, want[i])
		}
	}
	if merged.Beats[len(merged.Beats)-2].ID != "E01S02L13" {
		t.Fatalf("合并后台词接着编号：%s", merged.Beats[len(merged.Beats)-2].ID)
	}
}

func TestScriptFromTextKeepsExistingIDsAndVoiceOver(t *testing.T) {
	s := ScriptFromText("第一场 内·客厅·夜\n张三：你好\n李四（画外）：嗯。\n（门开了）\n场 2\n张三：走", "t")
	if len(s.Scenes) != 2 || s.Scenes[0].Beats[1].Kind != "vo" || s.Scenes[0].Beats[2].ID != "E01S01B01" || s.Scenes[1].Beats[0].ID != "E01S02L01" {
		t.Fatalf("场次 / 台词 / 画外 / 编号不对: %+v", s)
	}
	s.Scenes[0].Beats = append([]ScriptBeat{{ID: "E01S01L07", Kind: "line", Speaker: "王五", Text: "先说"}}, s.Scenes[0].Beats...)
	s.Scenes[0].Beats[1].ID = ""
	AssignBeatIDs(&s)
	if s.Scenes[0].Beats[0].ID != "E01S01L07" || s.Scenes[0].Beats[1].ID != "E01S01L08" {
		t.Fatalf("已有合规 id 不动，新 id 接着编: %+v", s.Scenes[0].Beats)
	}
}

// 线上实测夹具：on1 是第一次线上实测（29 镜）；on3 是第一期补丁上线后（41 镜，两场同名「内·电梯口·夜」）。两次是同一章正文。
var onlineCases = []struct {
	dir, tag string
	shots    int
}{{onlineDir, "online", 29}, {"testdata/online-qichuang-on3", "online-on3", 41}}

func loadOnlineRows(t *testing.T, dir string) []StoryboardRow {
	t.Helper()
	var doc struct {
		Rows []StoryboardRow `json:"rows"`
	}
	readJSONFile(t, filepath.Join(dir, "rows.json"), &doc)
	return doc.Rows
}

// onlineScript 是线上那次生成分段存下的台词表（旧规则一个场次头一场，与 Python from-md 一致）：夹具里的镜头行是按它写的，
// 线上已存的镜头表重检也是读存下的台词表，不按正文重切。
func onlineScript(t *testing.T) *Script {
	t.Helper()
	var s Script
	readJSONFile(t, filepath.Join(goldenDir, "online-script.json"), &s)
	return &s
}

func TestRowsToShotlistMatchesPython(t *testing.T) {
	for _, c := range onlineCases {
		result := RowsToShotlist(loadOnlineRows(t, c.dir), onlineScript(t), "")
		var want struct {
			Shotlist json.RawMessage `json:"shotlist"`
			Stats    RowsStats       `json:"stats"`
		}
		readJSONFile(t, filepath.Join(goldenDir, c.tag+"-rows.json"), &want)
		var wantShotlist any
		if err := json.Unmarshal(want.Shotlist, &wantShotlist); err != nil {
			t.Fatal(err)
		}
		diffJSON(t, c.tag+" rows → shotlist", result.Shotlist, wantShotlist)
		if result.Stats != want.Stats {
			t.Fatalf("%s 统计不同：%+v vs %+v", c.tag, result.Stats, want.Stats)
		}
		if result.Stats.Shots != c.shots || result.Stats.LineExact != 16 || result.Stats.LineFuzzy+result.Stats.LineUnmatched != 0 || len(result.SchemaIssues) != 0 {
			t.Fatalf("%s 应为 %d 镜、16 句全对上：%+v %v", c.tag, c.shots, result.Stats, result.SchemaIssues)
		}
	}
}

func TestRowsToShotlistDisambiguatesSameTextAndFallsBack(t *testing.T) {
	script := &Script{Scenes: []ScriptScene{
		{ID: "E01S01", Beats: []ScriptBeat{{ID: "E01S01L01", Kind: "line", Speaker: "甲", Text: "七床？"}, {ID: "E01S01L02", Kind: "line", Speaker: "乙", Text: "七床。"}}},
		{ID: "E01S02", Beats: []ScriptBeat{{ID: "E01S02L01", Kind: "line", Speaker: "甲", Text: "七床！"}}},
	}}
	rows := []StoryboardRow{
		{Spec: json.RawMessage(`{"id":"E01S01-01","scene":"E01S01","lines":[{"text":"七床","speaker":"乙"}],"extra_field":1}`)},
		{Spec: json.RawMessage(`{"id":"E01S02-01","scene":"E01S01","lines":[{"text":"七床","speaker":"甲"}]}`)},
		{DurationSeconds: 4, PlotDescription: "她转身", Dialogue: "甲：七床的人呢"},
		{Spec: json.RawMessage(`{"id":"x","move":{"bad":1}}`), PlotDescription: "坏 spec"},
	}
	r := RowsToShotlist(rows, script, "")
	ids := func(i int) string {
		if len(r.Shotlist.Shots[i].Lines) == 0 {
			return ""
		}
		return r.Shotlist.Shots[i].Lines[0].LineID
	}
	if ids(0) != "E01S01L02" {
		t.Errorf("同文台词按说话人消歧：%s", ids(0))
	}
	if ids(1) != "E01S01L01" {
		t.Errorf("说话人相同再按场号消歧（模型写的场号）：%s", ids(1))
	}
	if r.Shotlist.Shots[2].ID != "E01S00-03" || float64(r.Shotlist.Shots[2].Est) != 4 || r.Stats.NoSpec != 2 {
		t.Errorf("无 spec 的行用平铺字段兜底：%+v %+v", r.Shotlist.Shots[2], r.Stats)
	}
	if len(r.SchemaIssues) != 2 || r.SchemaIssues[0].Level != "warn" || r.SchemaIssues[1].Level != "error" {
		t.Errorf("契约外字段记 warn、坏 spec 记 error：%+v", r.SchemaIssues)
	}
}

type pyCheckReport struct {
	Errors   int         `json:"errors"`
	Warnings int         `json:"warnings"`
	Items    []CheckItem `json:"items"`
}

func compareCheck(t *testing.T, label string, got CheckReport, golden string) {
	t.Helper()
	var want pyCheckReport
	readJSONFile(t, filepath.Join(goldenDir, golden), &want)
	if got.Errors != want.Errors || got.Warnings != want.Warnings || len(got.Items) != len(want.Items) {
		t.Errorf("%s：Go %d error %d warn，Python %d error %d warn", label, got.Errors, got.Warnings, want.Errors, want.Warnings)
	}
	for i := 0; i < max(len(got.Items), len(want.Items)); i++ {
		var g, w CheckItem
		if i < len(got.Items) {
			g = got.Items[i]
			g.ShotID = ""
		}
		if i < len(want.Items) {
			w = want.Items[i]
		}
		if g != w {
			t.Fatalf("%s 第 %d 条不同\n got: %+v\nwant: %+v", label, i+1, g, w)
		}
	}
}

func sampleScriptBible(t *testing.T) (*Script, *Bible) {
	t.Helper()
	var script Script
	var bible Bible
	readJSONFile(t, filepath.Join(sampleDir, "剧本.json"), &script)
	readJSONFile(t, filepath.Join(sampleDir, "资产圣经.json"), &bible)
	return &script, &bible
}

func TestCheckShotsMatchesPython(t *testing.T) {
	script, bible := sampleScriptBible(t)
	ctx := func(profile string) CheckContext {
		return CheckContext{Script: script, Bible: bible, Profile: testProfile(t, profile), Vocab: testVocab(t), Rules: testShotRules(t)}
	}
	var sample Shotlist
	readJSONFile(t, filepath.Join(sampleDir, "镜头表.json"), &sample)
	compareCheck(t, "样例项目", CheckShots(sample, ctx("live-realism")), "check-sample.json")
	for _, name := range []string{"check-bad-shots", "check-bad-shots21"} {
		var fixture struct {
			Shotlist
			Expect []string `json:"expect"`
		}
		readJSONFile(t, filepath.Join(skillsTestDir, "fixtures", name+".json"), &fixture)
		report := CheckShots(fixture.Shotlist, ctx(fixture.Profile))
		compareCheck(t, name, report, name+".json")
		for _, rule := range fixture.Expect {
			if !slices.ContainsFunc(report.Items, func(i CheckItem) bool { return i.Rule == rule }) {
				t.Errorf("%s 应命中 %s", name, rule)
			}
		}
	}
}

func TestOnlineOutputEndToEnd(t *testing.T) {
	script := onlineScript(t)
	for _, c := range onlineCases {
		rows := RowsToShotlist(loadOnlineRows(t, c.dir), script, "")
		report := CheckShots(rows.Shotlist, CheckContext{Script: script, Profile: testProfile(t, "live-realism"), Vocab: testVocab(t), Rules: testShotRules(t)})
		compareCheck(t, c.tag+"（无资产圣经）", report, "check-"+c.tag+".json")
		if report.HasErrors() {
			t.Fatalf("%s 应 0 error：%+v", c.tag, report.Items)
		}
		for _, modelID := range []string{"seedance-2.0", "seedance-2.5"} {
			m, ok := ModelByID(modelID)
			if !ok {
				t.Fatal(modelID)
			}
			segs, problems := Pack(PackInput{Shotlist: rows.Shotlist, Script: script, Model: m, Profile: testProfile(t, "live-realism"), ProfileID: "live-realism", Vocab: testVocab(t)})
			var want struct {
				Segments []any    `json:"segments"`
				Problems []string `json:"problems"`
			}
			readJSONFile(t, filepath.Join(goldenDir, c.tag+"-pack-"+modelID+".json"), &want)
			diffJSON(t, c.tag+" 装段 "+modelID, segs, want.Segments)
			if len(problems) != len(want.Problems) {
				t.Fatalf("%s %s 装段问题不同：%v vs %v", c.tag, modelID, problems, want.Problems)
			}
			for _, seg := range segs {
				raw, _ := json.Marshal(seg)
				if _, err := ParseSegment(raw); err != nil {
					t.Fatalf("装出的段应能被严格解析：%v", err)
				}
				if _, err := CompileWithProfile(seg, m, nil, nil, testProfile(t, "live-realism")); err != nil {
					t.Fatal(err)
				}
				// 段按镜头表的场号装：一段的镜头都在同一场（两场同名「内·电梯口·夜」也各装各的）。
				for _, id := range seg.ShotIDs {
					if !strings.HasPrefix(id, seg.Scene+"-") {
						t.Fatalf("%s %s 段 %s（%s）混进了别的场的镜头 %s", c.tag, modelID, seg.ID, seg.Scene, id)
					}
				}
			}
		}
	}
}

func TestPackMatchesPythonSample(t *testing.T) {
	script, bible := sampleScriptBible(t)
	var shotlist Shotlist
	readJSONFile(t, filepath.Join(sampleDir, "镜头表.json"), &shotlist)
	m25, _ := ModelByID("seedance-2.5")
	in := PackInput{Shotlist: shotlist, Script: script, Bible: bible, Model: m25, Profile: testProfile(t, "live-realism"), ProfileID: "live-realism", Vocab: testVocab(t), FileExists: func(string) bool { return false }}
	segs, problems := Pack(in)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	var want []any
	readJSONFile(t, filepath.Join(sampleDir, "分段.json"), &want)
	diffJSON(t, "样例项目 2.5 装段", segs, want)
	m20, _ := ModelByID("seedance-2.0")
	in.Model = m20
	segs20, problems20 := Pack(in)
	if len(problems20) > 0 || len(segs20) <= len(segs) {
		t.Fatalf("2.0（15 秒上限）应比 2.5 装出更多段：%d vs %d %v", len(segs20), len(segs), problems20)
	}
	for _, s := range segs20 {
		if s.Dur < 4 || s.Dur > 15 {
			t.Fatalf("2.0 的 %s 时长 %v 不在范围", s.ID, s.Dur)
		}
	}
}

func TestExpandArcRulesMatchPython(t *testing.T) {
	v, prof := testVocab(t), testProfile(t, "live-realism")
	shot := func(arc EmotionArcRef, lines bool) *Shot {
		s := &Shot{ID: "T-01", EmotionArc: &arc}
		if lines {
			s.Lines = []ShotLine{{LineID: "X"}}
		}
		return s
	}
	txt, used, fam := ExpandArc(shot(EmotionArcRef{ID: "arc-sadness-001"}, false), 3.5, prof, v, 0, nil)
	if !(strings.HasSuffix(txt, "眼神空洞") || strings.Contains(txt, "3.5秒")) || !slices.Equal(used, []int{1, 2, 3, 4}) || fam != "sadness" {
		t.Errorf("缩放：%s %v %s", txt, used, fam)
	}
	if txt, used, _ := ExpandArc(shot(EmotionArcRef{ID: "arc-sadness-001"}, false), 2.5, prof, v, 0, nil); !slices.Equal(used, []int{1, 2}) || !strings.Contains(txt, "2.5秒") {
		t.Errorf("短镜前两段：%s %v", txt, used)
	}
	if _, used, _ := ExpandArc(shot(EmotionArcRef{ID: "arc-cold-001", PhaseFrom: 3, PhaseTo: 5}, false), 6, prof, v, 0, nil); !slices.Equal(used, []int{3, 4, 5}) {
		t.Errorf("截段：%v", used)
	}
	if _, used, _ := ExpandArc(shot(EmotionArcRef{ID: "arc-shock-002", Continues: "P"}, false), 3, prof, v, 2, nil); !slices.Equal(used, []int{3, 4}) {
		t.Errorf("续接：%v", used)
	}
	if txt, _, _ := ExpandArc(shot(EmotionArcRef{ID: "arc-cold-001", Intensity: "hidden7"}, false), 8, prof, v, 0, nil); strings.Contains(txt, "掩面") || !strings.Contains(txt, "藏七露三") {
		t.Errorf("hidden7：%s", txt)
	}
	if txt, _, _ := ExpandArc(shot(EmotionArcRef{ID: "arc-sadness-001"}, true), 6, prof, v, 0, nil); !strings.Contains(txt, "台词落在这一段") {
		t.Errorf("台词落段：%s", txt)
	}
	if txt, _, _ := ExpandArc(shot(EmotionArcRef{ID: "arc-love-001"}, false), 2.0, prof, v, 0, nil); strings.Count(txt, "秒：") > 2 {
		t.Errorf("短段合并：%s", txt)
	}
	pat, forbid := actionPatternText(&Shot{ActionPattern: &ActionPatternRef{ID: "act-turn", Variant: "被叫住后回头：肩膀先停，再缓缓侧脸"}}, v, mustCompileProfile(nil))
	if !strings.Contains(pat, "动作模式·转身回头") || !slices.Contains(forbid, "头发穿模") {
		t.Errorf("动作模式：%s %v", pat, forbid)
	}
}

func renderCompile21(r CompileResult) string {
	files := make([]string, 0, len(r.Files))
	for _, f := range r.Files {
		files = append(files, fmt.Sprintf("%s\t%s\t%s", f.Tag, f.File, f.Role))
	}
	issues := make([]string, 0, len(r.Issues))
	for _, i := range r.Issues {
		issues = append(issues, fmt.Sprintf("%s: %s", i.Level, i.Message))
	}
	return fmt.Sprintf("model: %s\nduration: %d\n--- prompt\n%s\n--- paste\n%s\n--- files\n%s\n--- issues\n%s\n", r.Model, r.Duration, r.Prompt, r.Paste, strings.Join(files, "\n"), strings.Join(issues, "\n"))
}

// 与 yoozee-skills/tests/test_compile25.py 共用样例和期望结果。
func TestCompile21MatchesSkillPackageGoldens(t *testing.T) {
	cases := [][3]string{{"seg25-scene", "seedance-2.5", "live-realism"}, {"seg25-bridge", "seedance-2.5", "live-realism"}, {"seg25-bridge", "minimax-h3", "live-realism"}, {"seg25-bridge", "seedance-2.5", "duanju-shiga"}}
	for _, c := range cases {
		raw, err := os.ReadFile(filepath.Join(skillsTestDir, "fixtures", c[0]+".json"))
		if err != nil {
			t.Fatal(err)
		}
		seg, err := ParseSegment(raw)
		if err != nil {
			t.Fatalf("%s: %v", c[0], err)
		}
		m, _ := ModelByID(c[1])
		res, err := CompileWithProfile(seg, m, nil, nil, testProfile(t, c[2]))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(skillsTestDir, "expected", c[0]+"."+c[1]+"."+c[2]+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if got := renderCompile21(res); got != string(want) {
			t.Errorf("%s × %s × %s 与技能包不一致\n--- got\n%s\n--- want\n%s", c[0], c[1], c[2], got, want)
		}
	}
}
