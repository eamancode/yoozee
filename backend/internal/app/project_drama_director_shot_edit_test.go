package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"yingce/backend/internal/drama"
)

// 第五轮复验 R5-P1-01：分镜卡编辑（Shots）会按镜头表补出实际待写的段。导演只读要按这份实际待写集合检查，
// 而且要在保存镜头表之前——走完整写入口 WriteDramaSegmentsChecked（接口、Agent 都经过它），不只测守卫函数或 Segments 数组。
func TestDirectorGuardCoversShotEditExpansion(t *testing.T) {
	service, db, project, unit := newProjectDramaTestService(t, "seedance-2.5")
	seedOnlineQichuang(t, db, project, unit)
	if _, err := service.finalizeDramaStepTask(dramaStepTask(project, unit, nil), map[string]any{"text": brokenQichuangLines(t)}); err != nil {
		t.Fatal(err)
	}
	if packed, err := service.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{AcceptIssues: true}); err != nil || len(packed.Segments) < 2 {
		t.Fatalf("seed segments: %v", err)
	}
	segments, err := service.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 第一段改成导演段（带 verbatim，镜头仍带 shot_id 投影），用导演收尾 / 返修的内部写法写入；第二段保持原流程。
	director := segments[0].Spec
	director.Verbatim = "导演锁定原文：人物走位及镜头投影不可普通手改。"
	if _, err := service.writeDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, director)}}); err != nil {
		t.Fatal(err)
	}
	var plain drama.Segment
	for _, view := range segments[1:] {
		if view.Spec.Verbatim == "" && len(view.Spec.Shots) > 0 && view.Spec.Shots[0].ShotID != "" && view.Spec.Shots[0].ShotID != director.Shots[0].ShotID {
			plain = view.Spec
			break
		}
	}
	if plain.ID == "" || director.Shots[0].ShotID == "" {
		t.Fatal("fixture needs a director segment and a plain segment with shot ids")
	}

	type snapshot struct {
		version  int
		shotlist string
		segments string
	}
	take := func() snapshot {
		t.Helper()
		view, err := service.DramaShotlist("user-1", project.ID, unit.ID)
		if err != nil {
			t.Fatal(err)
		}
		all, err := service.DramaSegments("user-1", project.ID, unit.ID)
		if err != nil {
			t.Fatal(err)
		}
		specs := make([]drama.Segment, 0, len(all))
		for _, seg := range all {
			specs = append(specs, seg.Spec)
		}
		shots, _ := json.Marshal(view.Shotlist)
		segs, _ := json.Marshal(specs)
		return snapshot{view.Version, string(shots), string(segs)}
	}
	shotOf := func(id string) (drama.Shot, int) {
		t.Helper()
		view, err := service.DramaShotlist("user-1", project.ID, unit.ID)
		if err != nil {
			t.Fatal(err)
		}
		return shotByID(t, view.Shotlist.Shots, id), view.Version
	}

	before := take()
	edited, version := shotOf(director.Shots[0].ShotID)
	edited.Action = "写入守卫回归：演员抬起手臂，指向门边。"
	// 库里存的导演段（内部写入后的样子），用来做「Segments 原样带来」的那一种。
	var storedDirector drama.Segment
	current, err := service.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, seg := range current {
		if seg.Spec.ID == director.ID {
			storedDirector = seg.Spec
		}
	}
	if storedDirector.Verbatim != director.Verbatim {
		t.Fatal("director segment not stored")
	}
	cases := []struct {
		name string
		req  DramaSegmentsWriteRequest
	}{
		{"shots only, no segments", DramaSegmentsWriteRequest{UnitID: unit.ID, Shots: []json.RawMessage{rawShot(t, edited)}, ShotlistVersion: version}},
		{"segments unchanged, shots changed", DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, storedDirector)}, Shots: []json.RawMessage{rawShot(t, edited)}, ShotlistVersion: version}},
	}
	for _, c := range cases {
		if _, err := service.WriteDramaSegmentsChecked("user-1", project.ID, c.req); appReason(err) != string(ReasonDirectorSegmentReadonly) {
			t.Fatalf("%s: want director_segment_readonly, got %v", c.name, err)
		}
		// 被拒时什么都没写：镜头表内容和版本、各段内容（含 verbatim）都和之前一样——不是先存镜头表再拒。
		if after := take(); !reflect.DeepEqual(after, before) {
			t.Fatalf("%s: rejected write changed state: version %d→%d, shotlist changed=%v, segments changed=%v", c.name, before.version, after.version, after.shotlist != before.shotlist, after.segments != before.segments)
		}
	}

	// 同一集里只改原流程段的镜头：照常保存，镜头表版本往上走，原流程段文字跟着重编，导演段一字不动。
	plainShot, version := shotOf(plain.Shots[0].ShotID)
	plainShot.Action = "原流程段照常改：雨停了，街灯亮起。"
	if _, err := service.WriteDramaSegmentsChecked("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Shots: []json.RawMessage{rawShot(t, plainShot)}, ShotlistVersion: version}); err != nil {
		t.Fatalf("plain segment shot edit must still work: %v", err)
	}
	if after := take(); after.version <= before.version || after.shotlist == before.shotlist {
		t.Fatalf("plain shot edit did not save the shotlist: version %d→%d", before.version, after.version)
	}
	all, _ := service.DramaSegments("user-1", project.ID, unit.ID)
	for _, seg := range all {
		if seg.Spec.ID == director.ID && (!reflect.DeepEqual(seg.Spec, storedDirector)) {
			t.Fatalf("plain edit touched the director segment: %+v", seg.Spec)
		}
	}

	// 已有的显式写段 409 和原样写回不变。
	retimed := storedDirector
	retimed.Dur++
	if _, err := service.WriteDramaSegmentsChecked("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, retimed)}}); appReason(err) != string(ReasonDirectorSegmentReadonly) {
		t.Fatalf("explicit retime: %v", err)
	}
	if _, err := service.WriteDramaSegmentsChecked("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, storedDirector)}}); err != nil {
		t.Fatalf("unchanged director segment write-back: %v", err)
	}

	// 导演收尾 / 症状返修的内部写法照常只换目标段（原流程段保持刚才的修改）。
	repaired := storedDirector
	repaired.Verbatim = "导演返修后的原文：人物先停在门边，再抬手。"
	if _, err := service.writeDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{rawSpec(t, repaired)}}); err != nil {
		t.Fatal(err)
	}
	all, _ = service.DramaSegments("user-1", project.ID, unit.ID)
	for _, seg := range all {
		switch seg.Spec.ID {
		case director.ID:
			if seg.Spec.Verbatim != repaired.Verbatim {
				t.Fatalf("repair did not replace the director segment: %q", seg.Spec.Verbatim)
			}
		case plain.ID:
			if seg.Spec.Verbatim != "" {
				t.Fatal("repair wrote verbatim into the plain segment")
			}
		}
	}
}

func rawShot(t *testing.T, shot drama.Shot) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(shot)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
