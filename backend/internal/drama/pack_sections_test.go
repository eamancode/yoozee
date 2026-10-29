package drama

import (
	"strings"
	"testing"
)

func sectionTestShots(section string, scene string, ests ...float64) []Shot {
	var out []Shot
	for i, est := range ests {
		out = append(out, Shot{ID: scene + "-" + string(rune('a'+len(out))) + section[len(section)-1:], Scene: scene, Section: section, Genre: ShotGenreWenxi30, Size: []string{"中景", "近景", "全景"}[i%3], Cam: Cam{Z: "Z5", Y: "Y4", X: "X2"}, Est: flexFloat(est), Action: "她走到窗边", Light: &Light{SourceAnchor: "台灯"}})
	}
	return out
}

// 一节一段：同一场三节装成三段，节内不按时长再切，后段带一致性桥；段记小节和戏类。
func TestPackOneSegmentPerSection(t *testing.T) {
	var shots []Shot
	shots = append(shots, sectionTestShots("E01S01-P1", "E01S01", 8, 9, 8)...)
	shots = append(shots, sectionTestShots("E01S01-P2", "E01S01", 12, 12)...)
	shots = append(shots, sectionTestShots("E01S01-P3", "E01S01", 5, 6, 6, 6)...)
	m, _ := ModelByID("seedance-2.5")
	segs, problems := Pack(PackInput{Shotlist: Shotlist{Profile: DefaultProfileID, Shots: shots}, Model: m, Profile: testProfile(t, "live-realism"), ProfileID: DefaultProfileID, Vocab: testVocab(t)})
	if len(segs) != 3 || len(problems) != 0 {
		t.Fatalf("应装成 3 段：%d 段，问题 %v", len(segs), problems)
	}
	for i, seg := range segs {
		want := []string{"E01S01-P1", "E01S01-P2", "E01S01-P3"}[i]
		if seg.Section != want || seg.Genre != ShotGenreWenxi30 || seg.Dur > 30 {
			t.Fatalf("第 %d 段：小节 %s 戏类 %s 时长 %.0f", i+1, seg.Section, seg.Genre, seg.Dur)
		}
		if i > 0 && seg.ContinuityBridge == nil {
			t.Fatalf("同场后段要带一致性桥：%+v", seg)
		}
	}
}

// 一节超过模型上限：照样装成一段（不切碎），装段问题和 R-SEG-001 都提示拆节。
func TestPackSectionOverLimit(t *testing.T) {
	shots := sectionTestShots("E01S01-P1", "E01S01", 6, 6, 6)
	m, _ := ModelByID("seedance-2.0")
	segs, problems := Pack(PackInput{Shotlist: Shotlist{Profile: DefaultProfileID, Shots: shots}, Model: m, Profile: testProfile(t, "live-realism"), ProfileID: DefaultProfileID, Vocab: testVocab(t)})
	if len(segs) != 1 || len(problems) != 1 || !strings.Contains(problems[0], "拆节") {
		t.Fatalf("超长的节不切碎：%d 段，问题 %v", len(segs), problems)
	}
	var report CheckReport
	report.CheckSectionDurations(Shotlist{Shots: shots}, nil, m, nil)
	if report.Errors != 1 || report.Items[0].Rule != "R-SEG-001" || !strings.Contains(report.Items[0].Message, "拆成两节") {
		t.Fatalf("R-SEG-001：%+v", report)
	}
}

// 按戏类放宽：R-SHOT-006 的次数配额从 error 记成 warn。
func TestCheckShotsGenreRelaxed(t *testing.T) {
	var shots []Shot
	for i := 0; i < 6; i++ {
		// live-realism 的重技法配额 orbit ≤ 3 次。
		shots = append(shots, Shot{ID: "E01S01-0" + string(rune('1'+i)), Scene: "E01S01", Size: "中景", Cam: Cam{Z: "Z5", Y: "Y4", X: "X2"}, Move: flexStrings{"orbit"}, Est: 2, Action: "她转身"})
	}
	profile := testProfile(t, "live-realism")
	rules, _ := BuiltinShotRules()
	strict := CheckShots(Shotlist{Shots: shots}, CheckContext{Profile: profile, Vocab: testVocab(t), Rules: rules})
	relaxed := CheckShots(Shotlist{Shots: shots}, CheckContext{Profile: profile, Vocab: testVocab(t), Rules: rules, WarnOnly: GenreRelaxedRules(ShotGenreWenxi15)})
	count := func(r CheckReport, level string) int {
		n := 0
		for _, it := range r.Items {
			if it.Rule == "R-SHOT-006" && it.Level == level {
				n++
			}
		}
		return n
	}
	if count(strict, "error") == 0 {
		t.Fatalf("没放宽时 orbit 超配额应是 error：%+v", strict.Items)
	}
	if count(relaxed, "error") != 0 || count(relaxed, "warn") < count(strict, "error") {
		t.Fatalf("放宽后不该有 R-SHOT-006 error：%+v", relaxed.Items)
	}
}

// 文戏 15 的段编译视频提示词时去掉配乐分句；其余戏类不动。
func TestCompileWenxi15WithoutMusic(t *testing.T) {
	seg := Segment{ID: "S01", Scene: "E01S01", Dur: 12, Mode: ModeTextOnly, Genre: ShotGenreWenxi15, Section: "E01S01-P1",
		Shots: []SubShot{{At: 0, Size: "中景", Text: "林小满推门走进病房，低音古琴与BGM缓缓进入。", Sound: "输液管滴水声，背景音乐渐强"}}}
	m, _ := ModelByID("seedance-2.0")
	got, err := Compile(seg, m, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Prompt, "BGM") || strings.Contains(got.Prompt, "背景音乐渐强") || !strings.Contains(got.Prompt, "林小满推门走进病房") || !strings.Contains(got.Prompt, "输液管滴水声") {
		t.Fatalf("文戏 15 编译应去掉配乐：\n%s", got.Prompt)
	}
	seg.Genre = ShotGenreWenxi30
	got, _ = Compile(seg, m, nil, nil)
	if !strings.Contains(got.Prompt, "BGM") {
		t.Fatal("别的戏类不改段文字")
	}
}

// 段标题段号后面带内容说明：首镜写了镜头目的（即梦）就用它；按小节生成的武戏节取核心观看体验、文戏 15 取首镜动作；旧镜头表只认镜头目的。
func TestSegmentTitleForSections(t *testing.T) {
	for _, tc := range []struct {
		name string
		shot Shot
		want string
	}{
		{"即梦镜头目的", Shot{Section: "E01S01-P1", Purpose: "建立走廊与人物。", Action: "林小满走来"}, "建立走廊与人物。"},
		{"武戏核心观看体验", Shot{Section: "E01S02-P2", Genre: ShotGenreWuxi, Extras: map[string]string{"核心观看体验": "护人者以伤换位"}, Action: "阿坤冲出"}, "护人者以伤换位"},
		{"文戏 15 首镜动作", Shot{Section: "E01S01-P1", Genre: ShotGenreWenxi15, Action: "林小满转身看向门口。"}, "林小满转身看向门口。"},
		{"旧镜头表没写目的", Shot{Action: "林小满转身"}, ""},
	} {
		shot := tc.shot
		if got := segmentTitle([]*Shot{&shot}); got != tc.want {
			t.Fatalf("%s：%q，想要 %q", tc.name, got, tc.want)
		}
	}
	long := Shot{Section: "E01S01-P1", Action: strings.Repeat("长", 60)}
	if got := segmentTitle([]*Shot{&long}); len([]rune(got)) != 40 {
		t.Fatalf("标题最多 40 字：%d", len([]rune(got)))
	}
}
