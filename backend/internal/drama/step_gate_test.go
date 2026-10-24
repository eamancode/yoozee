package drama

import (
	"encoding/json"
	"strings"
	"testing"
)

func gateResult(chapter string, warns int, est float64) StepEvalChapterResult {
	return StepEvalChapterResult{Chapter: chapter, Label: chapter, Parsed: true, Rows: 10, SpecRows: 10, LinesTotal: 8, LinesExact: 8, Warnings: warns, EstSeconds: est, TargetSeconds: 90}
}

func gateCheckOf(v StepGateVerdict, metric string) StepGateCheck {
	for _, c := range v.Checks {
		if c.Metric == metric {
			return c
		}
	}
	return StepGateCheck{}
}

// chapter.shots 的门槛：七项全部达标才通过；warn 跟对照版本同一组章节比，估时按每章比值的最大值。
func TestChapterShotsGate(t *testing.T) {
	step, _ := StepByID(StepChapterShots)
	g := step.Gate
	base := []StepEvalChapterResult{gateResult("a", 10, 90), gateResult("b", 5, 80)}
	ok := g.Evaluate([]StepEvalChapterResult{gateResult("a", 12, 99), gateResult("b", 6, 60)}, base)
	if !ok.Passed || len(ok.Checks) != 7 {
		t.Fatalf("warn 18 ≤ 15×1.2、估时 99/90 = 1.1 都在门槛内：%+v", ok)
	}
	for name, c := range map[string]struct {
		edit   func(*StepEvalChapterResult)
		metric string
	}{
		"解析失败":    {func(r *StepEvalChapterResult) { *r = StepEvalChapterResult{Chapter: "a", Failure: "没有镜头行"} }, GateParseRate},
		"缺 spec":  {func(r *StepEvalChapterResult) { r.SpecRows = 9 }, GateSpecRate},
		"漏台词":     {func(r *StepEvalChapterResult) { r.LinesExact = 7 }, GateDialogueCoverage},
		"有 error": {func(r *StepEvalChapterResult) { r.Errors = 1 }, GateCheckErrors},
		"warn 多":  {func(r *StepEvalChapterResult) { r.Warnings = 13 }, GateCheckWarnings},
		"估时超":     {func(r *StepEvalChapterResult) { r.EstSeconds = 99.5 }, GateDurationRatio},
		"截断": {func(r *StepEvalChapterResult) {
			*r = StepEvalChapterResult{Chapter: "a", Truncated: true, Failure: "截断"}
		}, GateTruncated},
	} {
		cand := []StepEvalChapterResult{gateResult("a", 12, 90), gateResult("b", 6, 60)}
		c.edit(&cand[0])
		v := g.Evaluate(cand, base)
		if v.Passed || gateCheckOf(v, c.metric).Passed {
			t.Fatalf("%s 应挡在 %s：%+v", name, c.metric, v)
		}
	}
	if v := g.Evaluate([]StepEvalChapterResult{gateResult("c", 0, 80)}, base); v.Passed || !strings.Contains(gateCheckOf(v, GateCheckWarnings).Detail, "没有可对照") {
		t.Fatalf("对照缺这一章就不过：%+v", v)
	}
	if v := g.Evaluate(nil, nil); v.Passed {
		t.Fatal("没有评测章节不能算通过")
	}
	noTarget := gateResult("a", 10, 500)
	noTarget.TargetSeconds = 0
	if c := gateCheckOf(g.Evaluate([]StepEvalChapterResult{noTarget}, base), GateDurationRatio); !c.Passed || !strings.Contains(c.Detail, "没设每集时长") {
		t.Fatalf("没设每集时长不比：%+v", c)
	}
}

// 评测集按风格档案配置：五个档案各有章节，包覆盖的档案的章节先跑，再补清单 eval 点名的（去重）。
func TestStepEvalChapters(t *testing.T) {
	step, _ := StepByID(StepChapterShots)
	covered := map[string]bool{}
	for _, c := range step.EvalSet {
		covered[c.Profile] = true
		if _, err := ReadEvalChapter(c.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range BuiltinProfileIDs() {
		if !covered[id] {
			t.Fatalf("档案 %s 没有评测章节", id)
		}
	}
	got := step.EvalChapters([]string{"anime-3d", "live-realism"}, []string{"qichuang-e01", "dadou-e01", "no-such"})
	ids := []string{}
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "qichuang-e01,kehuan-e01,dadou-e01" {
		t.Fatalf("评测章节：%v", ids)
	}
}

// 解析器与前端 storyboardRowsFromTask 同一套规则：代码块围栏、text 里夹着解释文字、对象形式的文本字段。
func TestStoryboardRowsFromResult(t *testing.T) {
	text := "好的，分镜如下：\n```json\n{\"title\":\"第一集\",\"rows\":[{\"plotDescription\":{\"text\":\"她推门\"},\"dialogue\":[\"林小满：七床？\",\"\"],\"durationSeconds\":4,\"characters\":\"林小满\",\"spec\":{\"id\":\"E01S01-01\"}},\"坏行\"]}\n```\n以上。"
	raw, _ := json.Marshal(map[string]any{"mode": "text", "text": text})
	rows, err := StoryboardRowsFromResult(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["plotDescription"] != "她推门" || rows[0]["dialogue"] != "林小满：七床？" || rows[0]["durationSeconds"] != float64(4) || rows[1]["durationSeconds"] != float64(6) {
		t.Fatalf("规整：%+v", rows)
	}
	if chars, ok := rows[0]["characters"].([]any); !ok || len(chars) != 0 || rows[1]["shotNumber"] != 2 || rows[0]["spec"] == nil {
		t.Fatalf("数组字段与顺序：%+v", rows)
	}
	direct, _ := json.Marshal(map[string]any{"rows": []any{map[string]any{"plotDescription": "x"}}})
	if rows, err := StoryboardRowsFromResult(string(direct)); err != nil || len(rows) != 1 {
		t.Fatalf("结果本身带 rows：%v %v", rows, err)
	}
	for _, bad := range []string{`{"mode":"text","text":"抱歉"}`, `{"mode":"text","text":"{\"rows\":[]}"}`, `{}`} {
		if _, err := StoryboardRowsFromResult(bad); err != ErrNoStoryboardRows {
			t.Fatalf("%s：%v", bad, err)
		}
	}
	if _, err := StoryboardRowsFromResult("not json"); err == nil {
		t.Fatal("结果不是 JSON")
	}
}
