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
	for name, seg := range map[string]Segment{"改台词": changedLine, "换参考图": changedRefs, "改镜头文字": changedText} {
		if hash(seg, ArtifactVideo) == base {
			t.Fatalf("%s 后视频指纹应变", name)
		}
	}
	// 首镜没写镜头目的的小节段：标题由装段兜底，改了首镜动作后保存和重装写的标题不同，都不让指纹变。
	if hash(withNew(old, "林小满一把推开七床的门。"), ArtifactVideo) != base {
		t.Fatal("小节段的兜底标题变了，视频指纹应不变")
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

// 剧情描述：制作台保存曾经把段标题拼在派生剧情前面（「标题；各镜文字」），装段只写各镜文字。两种写法指纹一样；
// 之前按「标题；各镜文字」算过指纹的产物（main 上制作台保存过的即梦段）在新版本装成「各镜文字」后也不过期；手写的剧情照旧按原文。
func TestPlotDerivedFormsShareFingerprint(t *testing.T) {
	seg := Segment{ID: "S02", Scene: "E01S01", Title: "建立走廊与人物。", Dur: 24, Mode: ModeTextOnly, Section: "E01S01-P1", Genre: ShotGenreWenxi30,
		Shots: []SubShot{{At: 0, Size: "全景", Purpose: "建立走廊与人物。", Text: "林小满抱着病历夹走来"}, {At: 6, Size: "中景", Text: "林小满推门"}}}
	raw, _ := json.Marshal(seg)
	plain, titled := SegmentPlot(seg), seg.Title+"；"+SegmentPlot(seg)
	rev := func(plot string) RevisionInputs {
		return RevisionInputs{PlotDescription: plot, Dialogue: "林小满：七床？", SpecJSON: string(raw)}
	}
	for _, kind := range []string{ArtifactStoryboard, ArtifactActionBoard, ArtifactVideo} {
		if InputHash(kind, rev(plain), nil) != InputHash(kind, rev(titled), nil) {
			t.Fatalf("%s：两种派生剧情指纹应一样", kind)
		}
		// main 上制作台保存的版本：剧情「标题；各镜文字」、段没有每镜 refs，产物按当时的写法（剧情原文）存了指纹。
		legacy := inputHash(kind, rev(titled), nil, false)
		if legacy == InputHash(kind, rev(titled), nil) {
			t.Fatalf("%s：用例要覆盖旧指纹与新指纹不同的情况", kind)
		}
		upgraded := seg
		upgraded.Shots = []SubShot{seg.Shots[0], seg.Shots[1]}
		upgraded.Shots[0].Refs = []string{"v-lin"}
		upRaw, _ := json.Marshal(upgraded)
		next := RevisionInputs{PlotDescription: plain, Dialogue: "林小满：七床？", SpecJSON: string(upRaw)}
		if stale := StaleArtifacts([]ArtifactInput{{ID: "a", Type: kind, InputHash: legacy}}, next, nil); len(stale) != 0 {
			t.Fatalf("%s：main 上保存过的段重装成新写法后产物不应过期", kind)
		}
		changed := next
		changed.PlotDescription = "林小满冲进病房"
		if stale := StaleArtifacts([]ArtifactInput{{ID: "a", Type: kind, InputHash: legacy}}, changed, nil); len(stale) != 1 {
			t.Fatalf("%s：手写剧情变了产物应过期", kind)
		}
	}
	if InputHash(ArtifactVideo, rev("雨夜，林小满在走廊"), nil) == InputHash(ArtifactVideo, rev(plain), nil) {
		t.Fatal("手写剧情照旧按原文进指纹")
	}
}
