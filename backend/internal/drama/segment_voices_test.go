package drama

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestSegmentVoicesPinnedShotBindings(t *testing.T) {
	seg := Segment{
		Refs: []Ref{
			{File: "v-costume", Role: "identity"},
			{File: "v-recast", Role: "identity", Speaker: "店员"},
			{File: "v-no-sample", Role: "identity"},
			{File: "v-silent", Role: "identity"},
			{File: "v-cannot-speak", Role: "identity"},
			{File: "v-new", Role: "identity"},
		},
		Shots: []SubShot{
			{Refs: []string{"v-costume", "v-recast", "v-no-sample", "v-cannot-speak"}, Lines: []Line{
				{Speaker: "林夏", Text: "在这里。"}, {Speaker: "店员", Text: "谢谢。"},
				{Speaker: "朋友", Text: "再会。"}, {Speaker: "姊姊", Text: "等一等。"}, {Speaker: "客人", Text: "走了。"},
			}},
			{Refs: []string{"v-silent"}, Sound: "只有风声。"},
		},
	}
	samples := map[string]BoundVoiceSample{
		"v-costume":      {Character: "林夏·礼服", Speaker: "林夏", ResourceID: "sample-shared", Speaks: true},
		"v-recast":       {Character: "演员乙", ResourceID: "sample-shared", Speaks: true},
		"v-no-sample":    {Character: "朋友", Speaks: true},
		"v-other-parent": {Character: "朋友", ResourceID: "should-not-be-borrowed", Speaks: true},
		"v-silent":       {Character: "林夏", ResourceID: "silent-costume", Speaks: true},
		"v-cannot-speak": {Character: "姊姊", ResourceID: "silent-sample"},
		"v-new":          {Character: "客人", ResourceID: "sample-new", Speaks: true},
	}
	voices, sent := SegmentVoices(seg, samples, false)
	if !slices.Equal(voices, []Voice{{Character: "林夏、店员", File: "sample-shared"}, {Character: "客人", File: "sample-new"}}) ||
		!maps.Equal(sent, map[string]int{"v-costume": 1, "v-recast": 1, "v-new": 2}) {
		t.Fatal(voices, sent)
	}
	seg.Refs = seg.Refs[1:]
	voices, sent = SegmentVoices(seg, samples, false)
	if len(voices) != 2 || voices[0].Character != "店员" || sent["v-recast"] != 1 {
		t.Fatal("shared sample lost on unbind", voices, sent)
	}
	seg.Refs = seg.Refs[1:]
	voices, sent = SegmentVoices(seg, samples, false)
	if len(voices) != 1 || sent["v-new"] != 1 {
		t.Fatal("audio numbering after last user unbinds", voices, sent)
	}
	voices, sent = SegmentVoices(seg, samples, true)
	if len(voices) != 3 || sent["v-silent"] != 1 || sent["v-cannot-speak"] != 2 || sent["v-new"] != 3 {
		t.Fatal("legacy unstructured shot uses each bound imaged character's own sample", voices, sent)
	}
}

func TestDirectorVoiceManifest(t *testing.T) {
	sss := SSSUnitCard{ID: "E1-U1", Scene: "房间", TotalSeconds: 5, Shots: 1,
		Paste: "生效参考素材\n@房间、@阿明和@明。\n声音\n" + SSSNoMusicLine,
		Body:  "#### 镜头表\n| 镜 | 时间 | 画面与表演 | 台词/声音 |\n| 1 | 0–5 | 明安静地看着阿明。 | 钟响；阿明（画外，声音放轻，最后一个字拖得长）：“时间到了。” |",
	}
	sssSeg, err := SSSSegment(sss, 1, []SSSMaterial{
		{Name: "房间", Category: "environment", VersionID: "room"},
		{Name: "明", Category: "character", VersionID: "silent"},
		{Name: "阿明", Category: "character", VersionID: "actor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ot := OntologyUnitCard{ID: "s01-u1", Scene: "房间", TotalSeconds: 5, Shots: 1,
		Paste: "素材映射：@图1 房间，@图2 阿明，@图3 明。\n" + OntologyNoMusicLine,
		Card:  "| 镜头 | 时间 | 动作与表情 | 台词与声音 |\n| 1 | 0–5 秒 | 明安静地看着阿明。 | 阿明：“时间到了。” |",
	}
	ontology, err := OntologySegment(ot, 1, []OntologyMaterial{
		{Index: 1, Category: "environment", VersionID: "room"},
		{Index: 2, Name: "阿明", Category: "character", VersionID: "actor"},
		{Index: 3, Name: "明", Category: "character", VersionID: "silent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, seg := range []Segment{sssSeg, ontology} {
		samples := map[string]BoundVoiceSample{
			"actor":  {Character: "阿明", ResourceID: "audio-42", Speaks: true},
			"silent": {Character: "明", ResourceID: "silent-voice", Speaks: true},
		}
		seg.Refs = append(seg.Refs, Ref{File: "prop", Role: "prop", Note: "钟"})
		var sent map[string]int
		seg.Voices, sent = SegmentVoices(seg, samples, false)
		if !maps.Equal(sent, map[string]int{"actor": 1}) {
			t.Fatal("the silent character's name is a suffix of the speaker's name", sent)
		}
		for _, modelID := range []string{"seedance-2.5", "minimax-h3"} {
			m, _ := ModelByID(modelID)
			got := CompileVerbatim(seg, m)
			want := []CompiledFile{
				{"room", ReferenceImageTag(m, 1), "location"}, {"actor", ReferenceImageTag(m, 2), "identity"},
				{"silent", ReferenceImageTag(m, 3), "identity"}, {"prop", ReferenceImageTag(m, 4), "prop"},
				{"audio-42", ReferenceAudioTag(m, 1), "voice"},
			}
			if !slices.Equal(got.Files, want) || !strings.Contains(got.Prompt, want[4].Tag+" 为阿明") ||
				!strings.Contains(got.Prompt, want[3].Tag+" 为钟") {
				t.Fatal(modelID, got.Files, got.Prompt)
			}
			zero := 0
			m.Refs.Audios = &zero
			rejected := CompileVerbatim(seg, m)
			if !HasError(rejected.Issues) || !slices.Equal(rejected.Files, want) {
				t.Fatal("unsupported audio must report a preflight error on the same manifest", rejected)
			}
		}
	}
}

func TestDirectorVoiceUsesAttributedDialogue(t *testing.T) {
	seg := Segment{Verbatim: "只有房间里的风声。", Shots: []SubShot{{}}, Refs: []Ref{{File: "v", Role: "identity"}}}
	samples := map[string]BoundVoiceSample{"v": {Character: "明", ResourceID: "sample", Speaks: true}}
	for _, tc := range []struct {
		sound string
		send  bool
	}{
		{"钟响；明：开门。", true},
		{"明：无台词，只有喘气声。", false},
		{"阿明：“明天见。”", false},
		{"明台词", false},
		{"明压低声音，门吱呀响。", false},
	} {
		seg.Shots[0].Sound = tc.sound
		voices, _ := SegmentVoices(seg, samples, false)
		if (len(voices) > 0) != tc.send {
			t.Fatalf("%s: %v", tc.sound, voices)
		}
	}
	seg.Shots[0].Sound = ""
	seg.Verbatim = "她看着门。明（低声）说：“等等。”"
	if voices, _ := SegmentVoices(seg, samples, false); len(voices) != 1 {
		t.Fatal("old ontology prompt has an explicit attributed quote", voices)
	}
}

func TestH3SharedSampleRoutesBothSpeakersToItsSlot(t *testing.T) {
	seg := Segment{ID: "S01", Scene: "客厅", Lang: "zh", Dur: 5, Mode: ModeLockIdentity,
		Voices: []Voice{{Character: "丙", File: "alone"}, {Character: "甲、乙", File: "shared"}},
		Shots: []SubShot{{Text: "众人说话。", Lines: []Line{
			{Speaker: "甲", Text: "头一句。"}, {Speaker: "乙", Text: "第二句。"},
			{Speaker: "丙", Text: "第三句。"}, {Speaker: "丁", Text: "第四句。"},
		}}},
	}
	m, _ := ModelByID("minimax-h3")
	compiled, err := Compile(seg, m, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"(S2):\n<d>[zh] 头一句。", "(S2):\n<d>[zh] 第二句。",
		"(S1):\n<d>[zh] 第三句。", "(S3):\n<d>[zh] 第四句。"} {
		if !strings.Contains(compiled.Prompt, line) {
			t.Errorf("missing %q in %s", line, compiled.Prompt)
		}
	}
}
