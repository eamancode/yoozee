package drama

import (
	"encoding/json"
	"testing"
)

// 卡 G 装段新加的每镜 refs 和段标题兜底不进视频指纹：卡 G 之前装的段（没有 refs、首镜没写镜头目的时标题为空）和之后装出的
// 同内容段指纹一样，产物不过期；段 refs、镜头文字、台词、手写标题变了仍然过期。
func TestInputHashIgnoresShotRefsAndFallbackTitle(t *testing.T) {
	old := Segment{
		ID: "S01", Scene: "E01S01", Dur: 8, Mode: ModeLockIdentity, Section: "E01S01-P1", Genre: ShotGenreWuxi,
		Refs: []Ref{{File: "v-wet", Role: "identity", Note: "林小满（淋湿）"}, {File: "v-station", Role: "location", Note: "护士站"}},
		Shots: []SubShot{
			{At: 0, Size: "全景", Camera: "平视", Text: "摄影机对准周岸。动作链：周岸持刀前冲→直刺林小满", Extras: map[string]string{"核心观看体验": "以身挡刀 + 贴身缠斗"}, Lines: []Line{{Speaker: "林小满", Text: "锁好了。", LineID: "E01S01L01"}}},
			{At: 1, Size: "中景", Text: "林小满格挡下劈"},
		},
	}
	wenxi15 := Segment{ID: "S02", Scene: "E01S02", Dur: 12, Mode: ModeTextOnly, Section: "E01S02-P1", Genre: ShotGenreWenxi15,
		Shots: []SubShot{{At: 0, Size: "全景", Text: "林小满转身看向门口。（表情：眉头微皱）"}, {At: 5, Size: "中近景", Text: "林小满抬头"}}}
	hash := func(seg Segment, kind string) string {
		raw, _ := json.Marshal(seg)
		return InputHash(kind, RevisionInputs{PlotDescription: "画面", SpecJSON: string(raw)}, []string{"v-wet"})
	}
	withNew := func(seg Segment, title string, refs ...[]string) Segment {
		seg.Title = title
		seg.Shots = append([]SubShot(nil), seg.Shots...)
		for i := range seg.Shots {
			if i < len(refs) {
				seg.Shots[i].Refs = refs[i]
			}
		}
		return seg
	}
	for _, kind := range []string{ArtifactVideo, ArtifactStoryboard, ArtifactActionBoard} {
		if hash(old, kind) != hash(withNew(old, "以身挡刀 + 贴身缠斗", []string{"v-wet", "v-station"}, []string{"v-station"}), kind) {
			t.Fatalf("%s：武戏段加了每镜 refs 和核心观看体验标题，指纹应不变", kind)
		}
		if hash(wenxi15, kind) != hash(withNew(wenxi15, "林小满转身看向门口。", nil, nil), kind) {
			t.Fatalf("%s：文戏 15 段加了首镜动作标题，指纹应不变", kind)
		}
	}
	base := hash(old, ArtifactVideo)
	changedLine := withNew(old, "")
	changedLine.Shots[0].Lines = []Line{{Speaker: "林小满", Text: "门锁好了。", LineID: "E01S01L01"}}
	changedRefs := withNew(old, "")
	changedRefs.Refs = []Ref{{File: "v-lin", Role: "identity", Note: "林小满"}, {File: "v-station", Role: "location", Note: "护士站"}}
	changedText := withNew(old, "")
	changedText.Shots[1].Text = "林小满侧身闪开"
	manualTitle := withNew(old, "雨夜护士站对峙")
	for name, seg := range map[string]Segment{"改台词": changedLine, "换参考图": changedRefs, "改镜头文字": changedText, "手写标题": manualTitle} {
		if hash(seg, ArtifactVideo) == base {
			t.Fatalf("%s 后视频指纹应变", name)
		}
	}
	purposeTitle := withNew(old, "推进剧情。")
	purposeTitle.Shots[0].Purpose = "推进剧情。"
	purposeOld := withNew(old, "推进剧情。")
	purposeOld.Shots[0].Purpose = "推进剧情。"
	purposeOld.Title = "推进剧情"
	if hash(purposeTitle, ArtifactVideo) == hash(purposeOld, ArtifactVideo) {
		t.Fatal("写了镜头目的的段，标题照旧按原文进指纹")
	}
}
