package drama

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seg25Scene(t *testing.T) Segment {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(skillsTestDir, "fixtures", "seg25-scene.json"))
	if err != nil {
		t.Fatal(err)
	}
	seg, err := ParseSegment(raw)
	if err != nil {
		t.Fatal(err)
	}
	return seg
}

// 默认档案序列化 → 严格解析 → 编译，与不带档案时逐字一致（存一份完整默认档案也不会改变输出）。
func TestDefaultCompileProfileRoundTripsWithoutChangingOutput(t *testing.T) {
	def, err := DefaultCompileProfile()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCompileProfile(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	seg := seg25Scene(t)
	for _, model := range []string{"seedance-2.5", "seedance-2.0", "minimax-h3"} {
		m, _ := ModelByID(model)
		want, err := CompileWithProfile(seg, m, nil, nil, testProfile(t, "live-realism"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := CompileWith(seg, m, nil, nil, testProfile(t, "live-realism"), parsed)
		if err != nil {
			t.Fatal(err)
		}
		if got.Prompt != want.Prompt || got.Paste != want.Paste {
			t.Fatalf("%s: round-tripped default profile changed the prompt", model)
		}
	}
	resolved, err := ResolvedDefaultCompileProfile()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Blocks.Seedance.Style == "" || resolved.Blocks.Profiles["live-realism"].Style.Zh == "" {
		t.Fatal("resolved default must fill blocks from source files")
	}
	raw, _ = json.Marshal(resolved)
	full, err := ParseCompileProfile(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := ModelByID("seedance-2.5")
	want, _ := CompileWithProfile(seg, m, nil, nil, testProfile(t, "live-realism"))
	got, _ := CompileWith(seg, m, nil, nil, testProfile(t, "live-realism"), full)
	if got.Prompt != want.Prompt {
		t.Fatal("saving the resolved default (blocks filled in) must not change the prompt")
	}
}

func TestCompileProfileOverridesOnlyTheWording(t *testing.T) {
	content := `{
		"line": {"timbre": "嗓音：{value}"},
		"seedance": {"head": "请生成{dur}秒的视频。风格：{style}{acting}"},
		"risk": {"identity_drift": "⚠️人物长相全程不变。"},
		"blocks": {"profiles": {"live-realism": {"style": {"zh": "胶片质感。"}}}}
	}`
	cp, err := ParseCompileProfile([]byte(content), true)
	if err != nil {
		t.Fatal(err)
	}
	// 没写的项取默认。
	if cp.Line.Pace != "语速：{value}" || cp.Risk["three_axis"] == "" || cp.Cast.LockMax != 6 {
		t.Fatalf("partial profile lost defaults: %#v", cp.Line)
	}
	def, _ := DefaultCompileProfile()
	if def.Line.Timbre != "音色：{value}" {
		t.Fatal("parsing a profile must not mutate the shared default")
	}
	seg := seg25Scene(t)
	m, _ := ModelByID("seedance-2.5")
	base, _ := CompileWithProfile(seg, m, nil, nil, testProfile(t, "live-realism"))
	got, err := CompileWith(seg, m, nil, nil, testProfile(t, "live-realism"), cp)
	if err != nil {
		t.Fatal(err)
	}
	if head := strings.SplitN(got.Prompt, "\n", 2)[0]; !strings.Contains(head, "请生成") || strings.Contains(head, "整体风格") || !strings.Contains(head, "风格：胶片质感。") {
		t.Fatalf("head / style block override missing:\n%s", got.Prompt)
	}
	// 结构不变：分秒行数、参考清单、时长一样。
	if strings.Count(got.Prompt, "秒：") != strings.Count(base.Prompt, "秒：") || len(got.Files) != len(base.Files) || got.Duration != base.Duration {
		t.Fatal("overriding wording changed the structure")
	}
	if strings.Contains(base.Prompt, "音色：") && !strings.Contains(got.Prompt, "嗓音：") {
		t.Fatal("line timbre override missing")
	}
	// H3 的英文风格块没覆盖，仍取来源文件。
	h3, _ := ModelByID("minimax-h3")
	baseH3, _ := CompileWithProfile(seg, h3, nil, nil, testProfile(t, "live-realism"))
	gotH3, _ := CompileWith(seg, h3, nil, nil, testProfile(t, "live-realism"), cp)
	if strings.Contains(gotH3.Prompt, "胶片质感") || !strings.Contains(gotH3.Prompt, strings.SplitN(baseH3.Prompt, "\n", 3)[1][:20]) {
		t.Fatal("zh-only style override leaked into the English block")
	}
}

func TestCompileProfileValidation(t *testing.T) {
	bad := map[string]string{
		"unknown placeholder":   `{"line": {"timbre": "音色：{voice}"}}`,
		"missing required":      `{"line": {"with_how": "{speaker}（{how}）"}}`,
		"unknown field":         `{"line": {"timbre2": "x"}}`,
		"placeholder forbidden": `{"segment": {"reanchor": "{prev}"}}`,
		"lock_max range":        `{"cast": {"lock_max": 0}}`,
		"empty risk":            `{"risk": {"three_axis": " "}}`,
		"not an object":         `[1]`,
	}
	for name, content := range bad {
		if _, err := ParseCompileProfile([]byte(content), true); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
	if _, err := ParseCompileProfile([]byte(`{"line": {"timbre2": "x"}}`), false); err != nil {
		t.Errorf("lenient parse should ignore unknown fields: %v", err)
	}
	if got := fill("{a}{b}{a}", "a", "{b}", "b", "x"); got != "{b}x{b}" {
		t.Errorf("fill must not re-expand values: %q", got)
	}
}

// 装段用档案：镜头画面句、灯光七项、续段再锚点按档案写。
func TestPackUsesCompileProfilePhrases(t *testing.T) {
	cp, err := ParseCompileProfile([]byte(`{"light": {"key": "主光源"}, "segment": {"reanchor_with_prev": "接着{prev}：{state}。"}, "shot": {"state": "状态：{state}。"}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	script, bible := sampleScriptBible(t)
	var shotlist Shotlist
	readJSONFile(t, filepath.Join(sampleDir, "镜头表.json"), &shotlist)
	m20, _ := ModelByID("seedance-2.0")
	in := PackInput{Shotlist: shotlist, Script: script, Bible: bible, Model: m20, Profile: testProfile(t, "live-realism"), ProfileID: "live-realism", Vocab: testVocab(t), FileExists: func(string) bool { return false }}
	base, _ := Pack(in)
	in.Phrases = cp
	got, _ := Pack(in)
	if len(got) != len(base) {
		t.Fatalf("wording changed the number of segments: %d vs %d", len(got), len(base))
	}
	joined := func(segs []Segment) string {
		raw, _ := json.Marshal(segs)
		return string(raw)
	}
	g, b := joined(got), joined(base)
	if strings.Contains(b, "主光：") && !strings.Contains(g, "主光源：") {
		t.Error("light label override missing")
	}
	if strings.Contains(b, "承接上一段") && (!strings.Contains(g, "接着S") || strings.Contains(g, "承接上一段")) {
		t.Error("reanchor override missing")
	}
	if strings.Contains(b, "此刻人物状态") && strings.Contains(g, "此刻人物状态") {
		t.Error("shot state override missing")
	}
}
