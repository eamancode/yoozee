package app

import (
	"encoding/json"
	"testing"

	"yingce/backend/internal/drama"
)

func directorSpec(id string, text string, verbatim string) drama.Segment {
	return drama.Segment{ID: id, Scene: "一", Dur: 10, Mode: drama.ModeTextOnly, Lang: "zh", Shots: []drama.SubShot{{At: 0, Size: "中景", Text: text}, {At: 5, Size: "近景", Text: "林深抬头"}}, Verbatim: verbatim}
}

func rawSpec(t *testing.T, seg drama.Segment) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(seg)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// 第四轮复验 R4-P1-02：导演分镜段（SSS、本体论，spec.verbatim 非空）视频提交用的是 verbatim。普通写段（接口、Agent）
// 改画面 / 参考 / 时长、拆合删、新写 verbatim 都被拒（director_segment_readonly），库里的段不变；原样写回照常；
// 原流程的段照常编辑；导演收尾 / 返修（writeDramaSegments）只替换目标段，别的导演段不动。
func TestDirectorSegmentsRejectOrdinaryEdits(t *testing.T) {
	service, _, project, unit := newProjectDramaTestService(t, "seedance-2.0")
	u1 := directorSpec("S01", "阿婆把温橙汁递给林深", "10秒，雨夜。阿婆把温橙汁递给林深。")
	u2 := directorSpec("S02", "林深接过橙汁", "10秒，林深接过橙汁，低头。")
	plain := drama.Segment{ID: "S09", Scene: "一", Dur: 8, Mode: drama.ModeTextOnly, Shots: []drama.SubShot{{At: 0, Size: "中景", Text: "雨停了"}}}
	// 导演收尾的写法：内部 writeDramaSegments。
	if _, err := service.writeDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, u1), rawSpec(t, u2), rawSpec(t, plain)}}); err != nil {
		t.Fatal(err)
	}
	stored := func(id string) drama.Segment {
		t.Helper()
		views, err := service.DramaSegments("user-1", project.ID, unit.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, view := range views {
			if view.Spec.ID == id {
				return view.Spec
			}
		}
		t.Fatalf("segment %s missing", id)
		return drama.Segment{}
	}
	original := stored("S01")

	edited := original
	edited.Shots = append([]drama.SubShot(nil), original.Shots...)
	edited.Shots[0].Text = "Codex手改一致性探针：只修改画面文字"
	retimed := original
	retimed.Dur = 12
	withRefs := original
	withRefs.Refs = []drama.Ref{{File: "asset-x-v1", Role: "identity"}}
	newVerbatim := plain
	newVerbatim.Verbatim = "8秒，自己写的原文。"
	split := original
	split.ID = "S01b"
	cases := []struct {
		name string
		req  DramaSegmentsWriteRequest
	}{
		{"shot text", DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, edited)}}},
		{"duration", DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, retimed)}}},
		{"refs", DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, withRefs)}}},
		{"merge drops a director unit", DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, original)}, Order: []string{"S01", "S09"}, Inherit: map[string][]string{"S01": {"S02"}}}},
		{"split inherits from a director unit", DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, split)}, Order: []string{"S01", "S01b", "S02", "S09"}, Inherit: map[string][]string{"S01b": {"S01"}}}},
		{"ordinary write invents verbatim", DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, newVerbatim)}}},
	}
	for _, c := range cases {
		if _, err := service.WriteDramaSegments("user-1", project.ID, c.req); appReason(err) != string(ReasonDirectorSegmentReadonly) {
			t.Fatalf("%s: want director_segment_readonly, got %v", c.name, err)
		}
	}
	if got := stored("S01"); got.Shots[0].Text != original.Shots[0].Text || got.Verbatim != original.Verbatim || got.Dur != original.Dur || len(got.Refs) != 0 {
		t.Fatalf("rejected writes changed the director segment: %+v", got)
	}
	if got := stored("S09"); got.Verbatim != "" {
		t.Fatal("ordinary segment must not get a verbatim prompt")
	}
	// 原样写回（带或不带 refs）照常通过。
	if _, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, original)}}); err != nil {
		t.Fatalf("unchanged director segment must be writable: %v", err)
	}
	// 原流程的段照常改。
	plainEdited := stored("S09")
	plainEdited.Shots[0].Text = "雨停了，街灯亮起"
	if _, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, plainEdited)}}); err != nil || stored("S09").Shots[0].Text != "雨停了，街灯亮起" {
		t.Fatalf("ordinary segment edit: %v", err)
	}
	// 返修的写法（内部 writeDramaSegments）只替换目标段：S02 的原文和镜头换掉，S01 不动。
	repaired := directorSpec("S02", "林深接过橙汁，手在抖", "10秒，林深接过橙汁，手在抖。")
	if _, err := service.writeDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, repaired)}}); err != nil {
		t.Fatal(err)
	}
	if got := stored("S02"); got.Verbatim != repaired.Verbatim || got.Shots[0].Text != repaired.Shots[0].Text {
		t.Fatalf("repair did not replace the target: %+v", got)
	}
	if got := stored("S01"); got.Verbatim != original.Verbatim || got.Shots[0].Text != original.Shots[0].Text {
		t.Fatalf("repair touched another director segment: %+v", got)
	}
	// 编译仍按 verbatim（忠实编译不变）。
	if compiled := drama.CompileVerbatim(stored("S01"), drama.Model{ID: "seedance-2.0"}); compiled.Prompt == "" || compiled.Prompt == original.Shots[0].Text {
		t.Fatalf("director segment must compile from verbatim: %q", compiled.Prompt)
	}
}
