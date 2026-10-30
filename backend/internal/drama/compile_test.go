package drama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	builtinAssets "yingce/backend/builtin"
)

// 与 yoozee-skills/tests/test_compile.py 共用样例和期望结果：两边编译必须逐字一致。
var compileCases = [][2]string{
	{"segment-dialogue", "seedance-2.0"}, {"segment-dialogue", "seedance-2.5"}, {"segment-dialogue", "minimax-h3"},
	{"segment-long", "seedance-2.5"}, {"segment-long", "seedance-2.0"}, {"segment-long", "minimax-h3"},
	{"segment-firstframe", "seedance-2.0"}, {"segment-firstframe", "minimax-h3"},
	{"segment-shots", "seedance-2.0"}, {"segment-shots", "seedance-2.5"}, {"segment-shots", "minimax-h3"},
}

const skillsTestDir = "../../../yoozee-skills/tests"

func renderCompileResult(r CompileResult) string {
	files := make([]string, 0, len(r.Files))
	for _, f := range r.Files {
		files = append(files, fmt.Sprintf("%s\t%s\t%s", f.Tag, f.File, f.Role))
	}
	issues := make([]string, 0, len(r.Issues))
	for _, i := range r.Issues {
		issues = append(issues, fmt.Sprintf("%s: %s", i.Level, i.Message))
	}
	return fmt.Sprintf("model: %s\nduration: %d\n--- prompt\n%s\n--- files\n%s\n--- issues\n%s\n", r.Model, r.Duration, r.Prompt, strings.Join(files, "\n"), strings.Join(issues, "\n"))
}

func TestCompileMatchesSkillPackageGoldens(t *testing.T) {
	for _, c := range compileCases {
		raw, err := os.ReadFile(filepath.Join(skillsTestDir, "fixtures", c[0]+".json"))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		seg, err := ParseSegment(raw)
		if err != nil {
			t.Fatalf("%s: %v", c[0], err)
		}
		m, ok := ModelByID(c[1])
		if !ok {
			t.Fatalf("model %s missing", c[1])
		}
		result, err := Compile(seg, m, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(skillsTestDir, "expected", c[0]+"."+c[1]+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if got := renderCompileResult(result); got != string(want) {
			t.Errorf("%s × %s 与技能包的编译结果不一致\n--- got\n%s\n--- want\n%s", c[0], c[1], got, want)
		}
	}
}

func TestBuiltinModelsMatchSkillPackage(t *testing.T) {
	builtin, err := builtinAssets.FS.Open(modelsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer builtin.Close()
	var a, b bytes.Buffer
	if _, err := a.ReadFrom(builtin); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("../../../yoozee-skills/kb/kb-dialects/models.json")
	if err != nil {
		t.Fatal(err)
	}
	b.Write(src)
	if a.String() != b.String() {
		t.Fatal("backend/builtin/kb-dialects/models.json 与 yoozee-skills 源文件不一致：运行 python3 yoozee-skills/build.py")
	}
}

func TestSegmentCapsFollowModel(t *testing.T) {
	cases := map[string]int{"seedance-2.0": 15, "minimax-h3": 15, "seedance-2.5": 30}
	for id, want := range cases {
		m, ok := ModelByID(id)
		if !ok || m.Duration.Max != want {
			t.Fatalf("%s 一段上限应为 %d 秒，得到 %+v", id, want, m.Duration)
		}
	}
	reg, err := Models()
	if err != nil || reg.Default != "seedance-2.0" {
		t.Fatalf("默认模型应为 seedance-2.0：%v %v", reg.Default, err)
	}
}

func TestMatchChannelModelPrefersLongestFragment(t *testing.T) {
	for name, want := range map[string]string{
		"doubao-seedance-2-0-260128": "seedance-2.0",
		"doubao-seedance-2-5-pro":    "seedance-2.5",
		"MiniMax-H3":                 "minimax-h3",
	} {
		m, ok := MatchChannelModel(name)
		if !ok || m.ID != want {
			t.Errorf("%s → %s, got %v %v", name, want, m.ID, ok)
		}
	}
	if _, ok := MatchChannelModel("kling-v2"); ok {
		t.Error("kling 不应匹配任何模型族")
	}
}

func TestParseSegmentRejectsUnknownFieldsAndBadIDs(t *testing.T) {
	if _, err := ParseSegment([]byte(`{"id":"S01","scene":"一","dur":5,"mode":"lock_identity","shots":[{"at":0,"text":"x"}],"extra":1}`)); err == nil {
		t.Error("未知字段应报错")
	}
	if _, err := ParseSegment([]byte(`{"id":"shot-1","scene":"一","dur":5,"mode":"lock_identity","shots":[{"at":0,"text":"x"}]}`)); err == nil {
		t.Error("非法段号应报错")
	}
	var seg Segment
	if err := json.Unmarshal([]byte(`{"id":"S01","scene":"一","dur":5,"mode":"text_only","shots":[{"at":0,"text":"x","lines":[{"speaker":"A","text":"你好"}]}]}`), &seg); err != nil || seg.Dialogue() != "你好" {
		t.Errorf("台词拼接错误: %v %q", err, seg.Dialogue())
	}
}

func TestSegmentSoundAndRulesFollowShots(t *testing.T) {
	seg, err := ParseSegment([]byte(`{"id":"S01","scene":"走廊","dur":8,"mode":"text_only","shots":[{"at":0,"text":"a","sound":"门轴轻响。","end_state":"停在门口"},{"at":4,"text":"b","sound":"门轴轻响","end_state":"背对镜头"},{"at":6,"text":"c","sound":"低鸣；"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := seg.SoundText("；"); got != "门轴轻响；低鸣" {
		t.Errorf("段没写 sound 时按镜头接、去重：%q", got)
	}
	if got := seg.RulesText(); got != "" {
		t.Errorf("最后一镜没写 end_state 时不该拿中间镜头的：%q", got)
	}
	seg.Shots = seg.Shots[:2]
	if got := seg.RulesText(); got != "背对镜头" {
		t.Errorf("rules 取最后一镜的 end_state：%q", got)
	}
	seg.Sound, seg.Rules = " 走廊的低鸣 ", "走廊方向固定"
	if seg.SoundText("；") != "走廊的低鸣" || seg.RulesText() != "走廊方向固定" {
		t.Errorf("段级 sound / rules 写了就以段为准：%q %q", seg.SoundText("；"), seg.RulesText())
	}
}

func TestVisualHashIgnoresMissingEndState(t *testing.T) {
	old := RevisionInputs{SpecJSON: `{"id":"S01","scene":"一","dur":4,"mode":"text_only","shots":[{"at":0,"text":"a"}]}`}
	withEnd := RevisionInputs{SpecJSON: `{"id":"S01","scene":"一","dur":4,"mode":"text_only","shots":[{"at":0,"text":"a","end_state":"停在门口"}]}`}
	withSound := RevisionInputs{SpecJSON: `{"id":"S01","scene":"一","dur":4,"mode":"text_only","shots":[{"at":0,"text":"a","sound":"门响"}]}`}
	if InputHash(ArtifactStoryboard, old, nil) == InputHash(ArtifactStoryboard, withEnd, nil) {
		t.Error("镜头结尾状态变了，分镜图应过期")
	}
	if InputHash(ArtifactStoryboard, old, nil) != InputHash(ArtifactStoryboard, withSound, nil) {
		t.Error("只改镜头声音，分镜图不该过期")
	}
	if InputHash(ArtifactVideo, old, nil) == InputHash(ArtifactVideo, withSound, nil) {
		t.Error("镜头声音变了，视频应过期")
	}
}

func TestNormalizeBriefKeepsLegacyContentInContractShape(t *testing.T) {
	doc := map[string]any{"title": "夜班", "video_model": "seedance-2.0", "run_mode": "every_step", "purpose": "第一集验收", "duration_seconds": 40, "aspect_ratio": "9:16", "questions": []any{"真人还是动画？"}}
	normalized := NormalizeBrief(doc)
	raw, _ := json.Marshal(normalized)
	if _, err := ParseBrief(raw); err != nil {
		t.Fatalf("整理后应符合契约: %v\n%s", err, raw)
	}
	if !strings.Contains(string(raw), "aspect_ratio") || !strings.Contains(string(raw), "duration_seconds") {
		t.Fatalf("不认识的字段应挪进 constraints 而不是丢掉: %s", raw)
	}
}

func TestBriefKeepsSegmentSeconds(t *testing.T) {
	doc := NormalizeBrief(map[string]any{"title": "七床", "segment_seconds": float64(30)})
	if doc["segment_seconds"] != float64(30) {
		t.Fatalf("单段秒数是契约字段，读的时候不能挪进 constraints: %v", doc)
	}
	raw, _ := json.Marshal(doc)
	brief, err := ParseBrief(raw)
	if err != nil || brief.SegmentSeconds == nil || *brief.SegmentSeconds != 30 {
		t.Fatalf("segment_seconds 应能按契约解析: %+v err=%v", brief, err)
	}
	for _, bad := range []string{`{"segment_seconds": 0}`, `{"segment_seconds": 999}`} {
		if _, err := ParseBrief([]byte(bad)); err == nil || !strings.Contains(err.Error(), "segment_seconds") {
			t.Fatalf("%s 应被拒绝，得到 %v", bad, err)
		}
	}
}

// 本场色卡排在角色 / 场景 / 道具之后：参考图 3 张、上限 4 张时带上色卡图（@图片4）；上限 3 张时不带图、色值写进提示词并给 warn；
// 首尾帧模式和没有合成图时也只写色值。
func TestWithScenePalette(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(skillsTestDir, "fixtures", "segment-dialogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	seg, err := ParseSegment(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := ModelByID("seedance-2.0")
	base, err := Compile(seg, m, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := ScenePaletteRef{File: "palette-version", Code: "E01S02", Colors: []PaletteColor{{NameZh: "日光灯白", Hex: "#E8ECEF"}, {NameZh: "护士蓝", Hex: "#5B7FA3"}}}
	with := WithScenePalette(base, m, ref, 4)
	last := with.Files[len(with.Files)-1]
	if len(with.Files) != len(base.Files)+1 || last.Tag != "@图片4" || last.File != "palette-version" || last.Role != "palette" {
		t.Fatalf("色卡图应排在最后、编号 @图片4：%+v", with.Files)
	}
	if !strings.Contains(with.Prompt, "@图片4 是本场色卡（E01S02）") || strings.Contains(with.Prompt, "本场色值") || !strings.Contains(with.Paste, "@图片4 ← palette-version（palette）") {
		t.Fatalf("带图时提示词写明色卡：%s", with.Prompt)
	}
	if len(base.Files) != 4 || base.Files[3].Tag != "@音频1" {
		t.Fatalf("不应改到原结果：%+v", base.Files)
	}
	full := WithScenePalette(base, m, ref, 3)
	if len(full.Files) != len(base.Files) || !strings.HasSuffix(full.Prompt, "本场色值：日光灯白 #E8ECEF、护士蓝 #5B7FA3。") {
		t.Fatalf("名额满了不带图、写色值：%+v\n%s", full.Files, full.Prompt)
	}
	if n := len(full.Issues); n == 0 || full.Issues[n-1].Level != "warn" || !strings.Contains(full.Issues[n-1].Message, "上限 3 张") {
		t.Fatalf("名额满了给 warn：%+v", full.Issues)
	}
	// 没给上限时取模型的图片参考上限（seedance-2.0 是 9 张）。
	if def := WithScenePalette(base, m, ref, 0); def.Files[len(def.Files)-1].Role != "palette" {
		t.Fatalf("模型上限 9 张时应带图：%+v", def.Files)
	}
	noImage := WithScenePalette(base, m, ScenePaletteRef{Code: "E01S02", Colors: ref.Colors}, 9)
	if len(noImage.Files) != len(base.Files) || !strings.Contains(noImage.Prompt, "本场色值") || !strings.Contains(noImage.Issues[len(noImage.Issues)-1].Message, "还没有合成图") {
		t.Fatalf("没有合成图只写色值：%+v", noImage.Issues)
	}
	h3, _ := ModelByID("minimax-h3")
	h3Base, _ := Compile(seg, h3, nil, nil)
	if got := WithScenePalette(h3Base, h3, ref, 9); got.Files[len(got.Files)-1].Tag != "<Picture 4>" || !strings.Contains(got.Prompt, "<Picture 4> is the scene color palette card") {
		t.Fatalf("H3 用 <Picture N>：%+v", got.Files)
	}
	// H3 只有色卡 / 资产 + 色卡 / 只有资产：图片编号从 1 连续编到最后，提示词里每个编号都有对应文件。
	bare := seg
	bare.Refs, bare.Voices = nil, nil
	h3Bare, err := Compile(bare, h3, nil, nil)
	if err != nil || len(h3Bare.Files) != 0 {
		t.Fatalf("没有资产参考的 H3 段：%v %+v", err, h3Bare.Files)
	}
	for name, got := range map[string]CompileResult{"只有色卡": WithScenePalette(h3Bare, h3, ref, 9), "资产 + 色卡": WithScenePalette(h3Base, h3, ref, 9), "只有资产": h3Base} {
		n := 0
		for _, f := range got.Files {
			if f.Role == "voice" {
				continue
			}
			n++
			if want := fmt.Sprintf("<Picture %d>", n); f.Tag != want || !strings.Contains(got.Prompt, want) {
				t.Fatalf("H3 %s：第 %d 张图编号应是 %s：%+v\n%s", name, n, want, got.Files, got.Prompt)
			}
		}
		if n == 0 || strings.Contains(got.Prompt, "@图片") {
			t.Fatalf("H3 %s 不应出现即梦编号：%+v", name, got.Files)
		}
	}
}
