package drama

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func mustModel(t *testing.T, id string) Model {
	t.Helper()
	m, ok := ModelByID(id)
	if !ok {
		t.Fatalf("模型 %s 不在目录里", id)
	}
	return m
}

func dramaShot(id string, durationMs int64, withScript bool, refs int) ShotInput {
	shot := ShotInput{ID: id, UnitID: "unit-1", UnitTitle: "第一章", Title: id, DurationMs: durationMs, RefCount: refs}
	if withScript {
		shot.Revision = &RevisionInputs{PlotDescription: "护士推开七床的门", DurationMs: durationMs}
	}
	return shot
}

// 「改小生成模型后，超长的段要在概览里被标出来」——这条规则完全由 Derive 推导，不依赖数据库。
func TestDeriveMarksSegmentsOverTheProjectModelLimit(t *testing.T) {
	shots := []ShotInput{
		dramaShot("S01", 8000, true, 2),
		dramaShot("S02", 20000, true, 2),
	}

	// 建项目时选的是 Seedance 2.5（一段 30 秒）：20 秒的段合法。
	long := mustModel(t, "seedance-2.5")
	before := Derive(ProgressInput{Model: long, RunMode: RunEveryStep, Shots: shots})
	if before.Counts.OverLimit != 0 {
		t.Fatalf("Seedance 2.5 下 20 秒的段不应超长：%+v", before.Counts)
	}
	if before.Counts.Script != 2 {
		t.Fatalf("两段都该算剧本已完成：%+v", before.Counts)
	}

	// 设置页把生成模型改小到 Seedance 2.0（一段 15 秒）：同一批段里 20 秒那条变超长。
	short := mustModel(t, "seedance-2.0")
	after := Derive(ProgressInput{Model: short, RunMode: RunEveryStep, Shots: shots})
	if after.Counts.OverLimit != 1 {
		t.Fatalf("改小模型后应有 1 段超长：%+v", after.Counts)
	}
	if !after.Segments[1].OverLimit || after.Segments[0].OverLimit {
		t.Fatalf("只有 20 秒那条该被标超长：%+v", after.Segments)
	}
	// 超长的段不能算「剧本已完成」，否则概览会显示一个假的进度。
	if after.Segments[1].Script {
		t.Fatal("超长的段不应计入剧本完成")
	}
	if after.Counts.Script != 1 {
		t.Fatalf("超长段不计入剧本：%+v", after.Counts)
	}
	if !strings.Contains(after.Line, "1 段超过 Seedance 2.0 的 15 秒上限") {
		t.Fatalf("进度行要点明超长段和上限：%q", after.Line)
	}
	if strings.Contains(before.Line, "超过") {
		t.Fatalf("没有超长段时不该出现超长提示：%q", before.Line)
	}
}

func TestDeriveKeepsTenEndStates(t *testing.T) {
	states := Derive(ProgressInput{Model: mustModel(t, "seedance-2.0"), RunMode: RunUntilVideo}).EndStates
	if len(states) != 10 {
		t.Fatalf("终态应有 10 个，得到 %d", len(states))
	}
	keys := make([]string, 0, len(states))
	for _, state := range states {
		keys = append(keys, state.Key)
	}
	// 「风格定了」「色卡齐了」排在「剧本定了」之后、「参考齐了」之前。
	if got := strings.Join(keys[:5], ","); got != "brief,script,style,palette,refs" {
		t.Fatalf("终态顺序不对：%s", got)
	}
	seen := map[string]bool{}
	for _, state := range states {
		if state.Key == "" || state.Label == "" || state.Detail == "" || state.Skill == "" {
			t.Fatalf("终态字段不能为空：%+v", state)
		}
		if seen[state.Key] {
			t.Fatalf("终态 key 重复：%s", state.Key)
		}
		seen[state.Key] = true
	}
	// 什么都没做时，第一件该做的事是问需求（对应 yz-brief 技能）。
	if states[0].Done {
		t.Fatalf("空项目不应有已完成终态：%+v", states[0])
	}
}

func TestRequiredEvidenceDependsOnDialogue(t *testing.T) {
	withDialogue := RequiredEvidence(true)
	sort.Strings(withDialogue)
	if strings.Join(withDialogue, ",") != "faces,frames,transcript" {
		t.Fatalf("有台词要三项证据（含转写）：%v", withDialogue)
	}
	withoutDialogue := RequiredEvidence(false)
	sort.Strings(withoutDialogue)
	if strings.Join(withoutDialogue, ",") != "faces,frames" {
		t.Fatalf("没台词的段不要求转写：%v", withoutDialogue)
	}
}

func TestMissingEvidenceReportsOnlyUnpassedKinds(t *testing.T) {
	pass, fail := true, false
	items := []EvidenceState{
		{ArtifactID: "take-1", Kind: EvidenceFrames, Passed: &pass, CreatedAt: time.Now()},
		{ArtifactID: "take-1", Kind: EvidenceFaces, Passed: &fail, CreatedAt: time.Now()},
	}
	// 缺哪几项用中文标签回报（前端直接展示，不需要再映射一次）。
	missing := MissingEvidence(items, "take-1", true)
	sort.Strings(missing)
	if strings.Join(missing, ",") != "台词一字不差,同一个人" {
		t.Fatalf("只该报没过的和人没判的：%v", missing)
	}
	if got := MissingEvidence(items, "take-1", false); len(got) != 1 || got[0] != "同一个人" {
		t.Fatalf("没台词的段只缺人脸：%v", got)
	}
	// 证据属于别的候选时不算数。
	if got := MissingEvidence(items, "take-2", false); len(got) != 2 {
		t.Fatalf("别的候选的证据不能算：%v", got)
	}
	// 人还没判（passed 为空）也要算缺。
	unknown := []EvidenceState{{ArtifactID: "take-1", Kind: EvidenceFrames, Passed: nil, CreatedAt: time.Now()}}
	if got := MissingEvidence(unknown, "take-1", false); len(got) != 2 {
		t.Fatalf("未判定的证据算缺：%v", got)
	}
}

func TestValidRunModeAndEvidenceKind(t *testing.T) {
	for _, ok := range RunModes {
		if !ValidRunMode(ok) {
			t.Fatalf("%s 应是合法生成模式", ok)
		}
	}
	for _, bad := range []string{"", "step", "full", "auto", "UNTIL_VIDEO", " until_video"} {
		if ValidRunMode(bad) {
			t.Fatalf("%q 不该是合法生成模式", bad)
		}
	}
	for _, kind := range []string{EvidenceTranscript, EvidenceFrames, EvidenceFaces, EvidenceLevels, EvidenceVoice, EvidencePick, EvidenceGate, EvidenceReview, EvidenceDelivery, EvidenceAcceptance} {
		if !ValidEvidenceKind(kind) {
			t.Fatalf("%s 应是合法证据类型", kind)
		}
	}
	for _, bad := range []string{"", "face", "transcript ", "unknown"} {
		if ValidEvidenceKind(bad) {
			t.Fatalf("%q 不该是合法证据类型", bad)
		}
	}
}

// 提交给上游的整数秒要按模型步长向上取整并夹在范围内：这是「输入框封顶」的最后一道。
func TestSubmitDurationClampsToModelRange(t *testing.T) {
	m := mustModel(t, "seedance-2.0") // 4–15 秒，步长 1
	cases := map[float64]int{0: 4, 1: 4, 3.2: 4, 8: 8, 8.4: 9, 15: 15, 20: 15, 30: 15}
	for input, want := range cases {
		if got := SubmitDuration(input, m); got != want {
			t.Errorf("SubmitDuration(%v, seedance-2.0) = %d，应为 %d", input, got, want)
		}
	}
}

// 超长的段编译成提示词时要报 error 级问题，不能静默截断后当成功。
func TestCompileReportsOverLongSegmentAsError(t *testing.T) {
	seg := Segment{ID: "S01", Scene: "一", Dur: 20, Mode: ModeTextOnly, Shots: []SubShot{{At: 0, Text: "护士推开七床的门"}}}
	result, err := Compile(seg, mustModel(t, "seedance-2.0"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !HasError(result.Issues) {
		t.Fatalf("20 秒的段编译到 Seedance 2.0 应报 error：%+v", result.Issues)
	}
	// 换成 30 秒上限的模型后同一段应该没有问题。
	ok, err := Compile(seg, mustModel(t, "seedance-2.5"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if HasError(ok.Issues) {
		t.Fatalf("Seedance 2.5 下 20 秒不该报 error：%+v", ok.Issues)
	}
	if ok.Duration != 20 {
		t.Fatalf("编译结果要保留段长：%d", ok.Duration)
	}
}

func TestDeriveStyleAndPaletteStates(t *testing.T) {
	m := mustModel(t, "seedance-2.0")
	bible := `{"characters":[{"name":"林小满"}],"style_anchor":{"medium":"真人实拍"},"master_palette":{"groups":[{"key":"protagonist","name":"主角组","colors":[{"name_zh":"护士蓝","hex":"#5B7FA3"}]}]},"style_lock":{"source":"library"},"color_script":[{"episode":1,"scene_no":1},{"episode":1,"scene_no":2}]}`
	scenes := []SceneRef{{1, 1}, {1, 2}}
	state := func(in ProgressInput) map[string]EndState {
		out := map[string]EndState{}
		for _, s := range Derive(in).EndStates {
			out[s.Key] = s
		}
		return out
	}
	empty := state(ProgressInput{Model: m, RunMode: RunEveryStep})
	if empty["style"].Done || empty["palette"].Done {
		t.Fatalf("空项目不该风格定了、色卡齐了：%+v %+v", empty["style"], empty["palette"])
	}
	partial := state(ProgressInput{Model: m, RunMode: RunEveryStep, BibleJSON: bible, StyleScenes: scenes, Palettes: []PaletteInput{{Code: "E01S01", Imaged: true}, {Code: "E01S02"}}})
	if !partial["style"].Done || !strings.Contains(partial["style"].Detail, "取自平台风格库，风格锚与 1 色主基调色卡") || partial["palette"].Done || !strings.Contains(partial["palette"].Detail, "1/2") {
		t.Fatalf("风格锚写了、一张色卡没图：%+v %+v", partial["style"], partial["palette"])
	}
	full := state(ProgressInput{Model: m, RunMode: RunEveryStep, BibleJSON: bible, StyleScenes: scenes, Palettes: []PaletteInput{{Code: "E01S01", Imaged: true}, {Code: "E01S02", Imaged: true}}})
	if !full["palette"].Done || !full["style"].Done {
		t.Fatalf("每场都有出了图的色卡应算色卡齐了：%+v %+v", full["style"], full["palette"])
	}
	noAnchor := state(ProgressInput{Model: m, RunMode: RunEveryStep, BibleJSON: `{"style_anchor":{}}`, StyleScenes: scenes})
	if noAnchor["style"].Done {
		t.Fatal("空的风格锚不算风格定了")
	}
	anchorOnly := state(ProgressInput{Model: m, RunMode: RunEveryStep, BibleJSON: `{"style_anchor":{"medium":"真人实拍"}}`, StyleScenes: scenes})
	if anchorOnly["style"].Done || !strings.Contains(anchorOnly["style"].Detail, "没有主基调色卡") {
		t.Fatalf("只有风格锚、没有主基调色卡不算锁定：%+v", anchorOnly["style"])
	}
	// 风格在建项目时就锁定：还没导入原文时「风格定了」已经成立，色卡（场次）还没有。
	noUnits := state(ProgressInput{Model: m, RunMode: RunEveryStep, BibleJSON: bible, Palettes: []PaletteInput{{Code: "E01S01", Imaged: true}, {Code: "E01S02", Imaged: true}}})
	if !noUnits["style"].Done || noUnits["palette"].Done {
		t.Fatalf("没有剧集或章节时风格已锁定、色卡不算齐：%+v %+v", noUnits["style"], noUnits["palette"])
	}
}

// 只有原文时按 3 章做了场次色卡（每章一场），再改编成 2 集 4 场：色卡缺 2 场、多 1 场，「色卡齐了」变未完成；
// 「风格定了」只看锁定结果（这里没有主基调色卡，一直不算）。「色卡齐了」只看场次色卡，不看 bible 里的色彩脚本。
func TestDeriveStyleStatesFollowCurrentScenes(t *testing.T) {
	m := mustModel(t, "seedance-2.0")
	bible := `{"style_anchor":{"medium":"真人实拍"},"color_script":[{"episode":1,"scene_no":1},{"episode":2,"scene_no":1},{"episode":3,"scene_no":1}]}`
	palettes := []PaletteInput{{Code: "E01S01", Imaged: true}, {Code: "E02S01", Imaged: true}, {Code: "E03S01", Imaged: true}}
	derive := func(scenes []SceneRef, fromChapters bool, palettes []PaletteInput) map[string]EndState {
		out := map[string]EndState{}
		for _, s := range Derive(ProgressInput{Model: m, RunMode: RunEveryStep, BibleJSON: bible, Palettes: palettes, StyleScenes: scenes, StyleFromChapters: fromChapters}).EndStates {
			out[s.Key] = s
		}
		return out
	}
	chapters := derive([]SceneRef{{1, 1}, {2, 1}, {3, 1}}, true, palettes)
	if chapters["style"].Done || !chapters["palette"].Done || !strings.Contains(chapters["palette"].Detail, "3/3") {
		t.Fatalf("按章节做的色卡和章节对得上：%+v %+v", chapters["style"], chapters["palette"])
	}
	episodes := derive([]SceneRef{{1, 1}, {1, 2}, {1, 3}, {2, 1}}, false, palettes)
	style, palette := episodes["style"], episodes["palette"]
	if style.Done || palette.Done {
		t.Fatalf("改编成剧集后色卡对不上，不该算齐：%+v %+v", style, palette)
	}
	for _, want := range []string{"2/4 场有场次色卡", "第 1 集第 2 场、第 1 集第 3 场缺", "第 3 集第 1 场已不在当前剧集里"} {
		if !strings.Contains(palette.Detail, want) {
			t.Fatalf("detail 要写清缺哪场、多哪场（%s）：%q", want, palette.Detail)
		}
	}
	onlyExtra := derive([]SceneRef{{1, 1}, {2, 1}}, false, palettes)
	if onlyExtra["palette"].Done || !strings.Contains(onlyExtra["palette"].Detail, "2/2 场有场次色卡，第 3 集第 1 场已不在当前剧集里") {
		t.Fatalf("多出的场也算没齐：%+v %+v", onlyExtra["style"], onlyExtra["palette"])
	}
	// 有色卡但图没合成上：不算齐，说明里指到「重新合成」。
	unimaged := derive([]SceneRef{{1, 1}, {2, 1}}, false, []PaletteInput{{Code: "E01S01", Imaged: true}, {Code: "E02S01"}})
	if unimaged["palette"].Done || !strings.Contains(unimaged["palette"].Detail, "第 2 集第 1 场的色卡图没合成上") {
		t.Fatalf("没合成图不算齐：%+v", unimaged["palette"])
	}
	// bible 里有色彩脚本、没有场次色卡：不算齐。
	scriptOnly := derive([]SceneRef{{1, 1}, {2, 1}}, false, nil)
	if scriptOnly["palette"].Done || !strings.Contains(scriptOnly["palette"].Detail, "0/2 场有场次色卡") {
		t.Fatalf("只看场次色卡：%+v", scriptOnly["palette"])
	}
}
