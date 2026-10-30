package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
)

// brokenQichuangLines 是七床线上输出改出两类台词问题：第 1 场「七床的人呢？」那一镜（E01S01-07）不写台词，
// 第 2 场「你怎么在这？」同时写进 E01S02-05 和 E01S02-06。
func brokenQichuangLines(t *testing.T) string {
	t.Helper()
	rows, _ := onlineOutputLines(t)
	lines := make([]string, 0, len(rows))
	var asked any
	for _, row := range rows {
		spec := map[string]any{}
		for k, v := range row["spec"].(map[string]any) {
			spec[k] = v
		}
		switch spec["id"] {
		case "E01S01-07":
			spec["lines"] = []any{}
		case "E01S02-05":
			asked = spec["lines"].([]any)[0]
		case "E01S02-06":
			spec["lines"] = []any{asked}
		}
		copied := map[string]any{}
		for k, v := range row {
			copied[k] = v
		}
		copied["spec"] = spec
		lines = append(lines, testJSON(t, copied))
	}
	return strings.Join(lines, "\n") + "\n"
}

func shotByID(t *testing.T, shots []drama.Shot, id string) drama.Shot {
	t.Helper()
	at := slices.IndexFunc(shots, func(sh drama.Shot) bool { return sh.ID == id })
	if at < 0 {
		t.Fatalf("镜头表里没有 %s", id)
	}
	return shots[at]
}

// 草稿「先装段」：带着 error 装段，问题挂到对应镜头和段上；逐张补台词、只保留一处后重检清零，标记消失，之后重装段也不丢改动。
func TestAcceptIssuesThenEditShotsClearsMarks(t *testing.T) {
	service, db, project, unit := newProjectDramaTestService(t, "seedance-2.5")
	seedOnlineQichuang(t, db, project, unit)
	result := map[string]any{"text": brokenQichuangLines(t)}
	if _, err := service.finalizeDramaStepTask(dramaStepTask(project, unit, nil), result); err != nil {
		t.Fatal(err)
	}
	if outcome := result["dramaStep"].(DramaStepTaskOutcome); outcome.Status != DramaStepOutcomeDraft || outcome.Errors != 2 {
		t.Fatalf("两处台词问题存成草稿：%+v", outcome)
	}
	draft, _ := service.DramaShotlist("user-1", project.ID, unit.ID)
	if !draft.Draft || draft.Accepted || len(draft.Issues) != 2 {
		t.Fatalf("草稿视图：draft=%v accepted=%v issues=%+v", draft.Draft, draft.Accepted, draft.Issues)
	}
	// 还没装段的草稿不能按分镜卡编辑（段是上一次装出的）。
	if _, err := service.WriteDramaSegmentsChecked("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Shots: []json.RawMessage{json.RawMessage(testJSON(t, draft.Shotlist.Shots[0]))}}); err == nil || !strings.Contains(err.Error(), "还没装段") {
		t.Fatalf("草稿应拒绝分镜卡编辑：%v", err)
	}

	accepted, err := service.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{AcceptIssues: true})
	if err != nil || !accepted.Accepted || accepted.Draft || len(accepted.Segments) == 0 || accepted.Check.Errors != 2 || !accepted.Check.Accepted {
		t.Fatalf("带问题装段：%v %+v", err, accepted.Check)
	}
	view, _ := service.DramaShotlist("user-1", project.ID, unit.ID)
	if view.Draft || !view.Accepted {
		t.Fatalf("装段后不是草稿、记成已接受：%+v", view)
	}
	issues := map[string]drama.ShotIssue{}
	for _, issue := range view.Issues {
		issues[issue.Fix] = issue
	}
	missing, twice := issues[drama.ShotIssueFixAddLine], issues[drama.ShotIssueFixKeepLine]
	if !slices.Equal(missing.Shots, []string{"E01S01-04"}) || missing.Scene != "E01S01" || !strings.Contains(missing.Message, "没有落在任何镜头") {
		t.Fatalf("没落镜的台词挂在前一句台词那镜：%+v", missing)
	}
	if !slices.Equal(twice.Shots, []string{"E01S02-05", "E01S02-06"}) || twice.Scene != "E01S02" {
		t.Fatalf("落在两镜的台词挂在两镜上：%+v", twice)
	}
	segments, err := service.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	marked := 0
	for _, seg := range segments {
		marked += min(1, len(seg.Issues))
	}
	if marked < 2 {
		t.Fatalf("两处问题所在的段都标上：%d 段", marked)
	}
	progress, err := service.DramaProgress("user-1", project.ID)
	if err != nil || progress.Counts.Issues != marked || !strings.Contains(progress.Line, "带问题的段") {
		t.Fatalf("进度里数带问题的段：%v %d %q", err, progress.Counts.Issues, progress.Line)
	}

	// 情绪弧只能从索引里选；版本对不上拒绝。
	bad := shotByID(t, view.Shotlist.Shots, "E01S01-04")
	bad.EmotionArc = &drama.EmotionArcRef{ID: "arc-made-up"}
	if _, err := service.WriteDramaSegmentsChecked("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Shots: []json.RawMessage{json.RawMessage(testJSON(t, bad))}}); err == nil || !strings.Contains(err.Error(), "情绪弧") {
		t.Fatalf("自造情绪弧应拒绝：%v", err)
	}
	same := shotByID(t, view.Shotlist.Shots, "E01S01-04")
	if _, err := service.WriteDramaSegmentsChecked("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, ShotlistVersion: view.Version - 1, Shots: []json.RawMessage{json.RawMessage(testJSON(t, same))}}); appReason(err) != string(ReasonConflict) {
		t.Fatalf("镜头表版本对不上应拒绝：%v", err)
	}

	// 补进这个镜头：只改一镜，检查少一个 error，这一段的标记跟着变。
	fix := shotByID(t, view.Shotlist.Shots, "E01S01-04")
	fix.Lines = append(fix.Lines, drama.ShotLine{LineID: missing.LineID})
	first, err := service.WriteDramaSegmentsChecked("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, ShotlistVersion: view.Version, Shots: []json.RawMessage{json.RawMessage(testJSON(t, fix))}})
	if err != nil || first.Check == nil || first.Check.Errors != 1 || !first.Check.Accepted {
		t.Fatalf("补台词后重检：%v %+v", err, first.Check)
	}
	for _, seg := range first.Segments {
		for _, sub := range seg.Spec.Shots {
			if sub.ShotID == "E01S01-04" {
				if len(seg.Issues) != 0 || !slices.ContainsFunc(sub.Lines, func(ln drama.Line) bool { return ln.LineID == missing.LineID && ln.Text == "七床的人呢？" }) {
					t.Fatalf("补进的台词编进段、这一段不再标：%+v %+v", sub.Lines, seg.Issues)
				}
			}
		}
	}

	// 只保留在这个镜头：去掉另一镜里的那句，请求里不带段也会补上那一段。
	view, _ = service.DramaShotlist("user-1", project.ID, unit.ID)
	drop := shotByID(t, view.Shotlist.Shots, "E01S02-06")
	drop.Lines = nil
	second, err := service.WriteDramaSegmentsChecked("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, ShotlistVersion: view.Version, Shots: []json.RawMessage{json.RawMessage(testJSON(t, drop))}})
	if err != nil || second.Check.Errors != 0 || second.Check.Accepted {
		t.Fatalf("问题清零：%v %+v", err, second.Check)
	}
	for _, seg := range second.Segments {
		if len(seg.Issues) != 0 {
			t.Fatalf("清零后不再标：%s %+v", seg.Spec.ID, seg.Issues)
		}
	}
	view, _ = service.DramaShotlist("user-1", project.ID, unit.ID)
	if view.Draft || view.Accepted || view.Check.Errors != 0 || len(view.Issues) != 0 {
		t.Fatalf("镜头表视图清零：%+v", view.Check)
	}
	// 改动写进了分镜行原文：不带 acceptIssues 重装也过检查。
	repacked, err := service.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{})
	if err != nil || repacked.Check.Errors != 0 {
		t.Fatalf("重装段保留改动：%v", err)
	}
}

// 带问题装段仍拒绝会让装段本身做不成的问题：镜头时长不是正数。
func TestAcceptIssuesRejectsUnpackableShots(t *testing.T) {
	service, db, project, unit := newProjectDramaTestService(t, "seedance-2.5")
	seedOnlineQichuang(t, db, project, unit)
	rows, text := onlineOutputLines(t)
	spec := map[string]any{}
	for k, v := range rows[0]["spec"].(map[string]any) {
		spec[k] = v
	}
	spec["est"] = -2
	spec["emotion_arc"] = map[string]any{"id": "arc-not-exist"}
	first := map[string]any{}
	for k, v := range rows[0] {
		first[k] = v
	}
	first["spec"] = spec
	broken := testJSON(t, first) + "\n" + strings.SplitN(text, "\n", 2)[1]
	if _, err := service.finalizeDramaStepTask(dramaStepTask(project, unit, nil), map[string]any{"text": broken}); err != nil {
		t.Fatal(err)
	}
	_, err := service.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{AcceptIssues: true})
	if err == nil || !strings.Contains(err.Error(), "装段本身做不成") || !strings.Contains(err.Error(), "E01S01-01") {
		t.Fatalf("时长为负的镜头不能带问题装段：%v", err)
	}
	if view, _ := service.DramaShotlist("user-1", project.ID, unit.ID); view == nil || !view.Draft {
		t.Fatal("拒绝后草稿不变")
	}
}
