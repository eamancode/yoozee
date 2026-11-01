package drama

import (
	"fmt"
	"slices"
	"testing"
)

// refsCapBible 是一场两个人、一个场景、十二个道具，全都有图。
func refsCapBible() *Bible {
	b := &Bible{
		Characters: []BibleCharacter{
			{Name: "林小满", Variants: []BibleVariant{{Label: "base", Sheet: "char-lin", Crops: []string{"char-lin-正面"}}}},
			{Name: "周桐", Variants: []BibleVariant{{Label: "base", Sheet: "char-zhou", Crops: []string{"char-zhou-正面"}}}},
		},
		Locations: []BibleLocation{{Name: "老街出租屋", Plate: "loc-room"}},
	}
	for i := 1; i <= 12; i++ {
		b.Props = append(b.Props, BibleProp{Name: fmt.Sprintf("道具%02d", i), Image: fmt.Sprintf("prop-%02d", i)})
	}
	return b
}

// refsCapShots 是一节的镜头：每镜都有两个人和场景，道具按 props 一镜一个往下挂。
func refsCapShots(section string, from int, props ...int) []Shot {
	var out []Shot
	for i, p := range props {
		hints := []string{"林小满", "周桐", "老街出租屋", fmt.Sprintf("道具%02d", p)}
		out = append(out, Shot{ID: fmt.Sprintf("E01S01-%02d", from+i), Scene: "E01S01", Section: section, Genre: ShotGenreWenxi15, Size: "中景", Cam: Cam{Z: "Z5", Y: "Y4", X: "X2"}, Est: 2, Action: "她看向桌面", RefsHint: hints, Light: &Light{SourceAnchor: "台灯"}})
	}
	return out
}

// 装段按视频模型的参考图名额截断（M8 验收 A·S04 装出 11 张、保存按 Seedance 2.0 上限 9 校验失败）：同场后一节沿用前一节的参考
// 再加本节新出现的，合计超过名额时按人物 > 场景 > 道具的优先级留，同级先留本节镜头自己用到的；每镜 refs 只记留下的，
// 一致性桥的共用参考也只记留下的；装出的每一段都能原样过保存时的校验。
func TestPackCapsMergedRefsToModelLimit(t *testing.T) {
	m, _ := ModelByID("seedance-2.0")
	var shots []Shot
	shots = append(shots, refsCapShots("E01S01-P1", 1, 1, 2, 3, 4, 5, 6, 7)...)
	shots = append(shots, refsCapShots("E01S01-P2", 8, 8, 9, 10, 11, 12)...)
	segs, _ := Pack(PackInput{Shotlist: Shotlist{Profile: DefaultProfileID, Shots: shots}, Bible: refsCapBible(), Model: m, Profile: testProfile(t, "live-realism"), ProfileID: DefaultProfileID, Vocab: testVocab(t), FileExists: func(string) bool { return true }})
	if len(segs) != 2 {
		t.Fatalf("两节两段：%d", len(segs))
	}
	limit := *m.Refs.Images
	for _, seg := range segs {
		files := make([]string, 0, len(seg.Refs))
		for _, r := range seg.Refs {
			files = append(files, r.File)
		}
		if len(seg.Refs) > limit {
			t.Fatalf("%s 参考图 %d 张，超过 %s 的名额 %d：%v", seg.ID, len(seg.Refs), m.Label, limit, files)
		}
		for _, must := range []string{"char-lin-正面", "char-zhou-正面", "loc-room"} {
			if !slices.Contains(files, must) {
				t.Fatalf("%s 人物、场景优先留：缺 %s（%v）", seg.ID, must, files)
			}
		}
		if issues := Issues(seg, m); HasError(issues) {
			t.Fatalf("%s 装出来要能原样过保存校验：%v", seg.ID, issues)
		}
		for _, sub := range seg.Shots {
			for _, f := range sub.Refs {
				if !slices.Contains(files, f) {
					t.Fatalf("%s 镜头 %s 的 refs 只记段里留下的：%s 不在 %v", seg.ID, sub.ShotID, f, files)
				}
			}
		}
		if seg.ContinuityBridge != nil {
			for _, f := range seg.ContinuityBridge.SharedRefs {
				if !slices.Contains(files, f) {
					t.Fatalf("%s 共用参考只记留下的：%s", seg.ID, f)
				}
			}
		}
	}
	// 第二段先留本节自己用到的道具（8–12），名额剩下的才给前一节沿用来的。
	var second []string
	for _, r := range segs[1].Refs {
		second = append(second, r.File)
	}
	for p := 8; p <= 12; p++ {
		if want := fmt.Sprintf("prop-%02d", p); !slices.Contains(second, want) {
			t.Fatalf("第二段本节道具优先：缺 %s（%v）", want, second)
		}
	}
	if len(second) != limit {
		t.Fatalf("第二段名额用满：%d / %d（%v）", len(second), limit, second)
	}
	// 被截掉的道具这一镜的 refs 里就没有它。
	for _, sub := range segs[0].Shots {
		if len(sub.Refs) == 0 {
			t.Fatalf("第一段每镜都至少有人物和场景：%s", sub.ShotID)
		}
	}
}

// 名额够时不截、顺序与之前一致：后一段前一节的参考在前、本节新增的接在后面。
func TestPackKeepsRefsOrderUnderLimit(t *testing.T) {
	m, _ := ModelByID("seedance-2.5")
	var shots []Shot
	shots = append(shots, refsCapShots("E01S01-P1", 1, 1, 2)...)
	shots = append(shots, refsCapShots("E01S01-P2", 3, 3)...)
	segs, _ := Pack(PackInput{Shotlist: Shotlist{Profile: DefaultProfileID, Shots: shots}, Bible: refsCapBible(), Model: m, Profile: testProfile(t, "live-realism"), ProfileID: DefaultProfileID, Vocab: testVocab(t), FileExists: func(string) bool { return true }})
	var got []string
	for _, r := range segs[1].Refs {
		got = append(got, r.File)
	}
	want := []string{"char-lin-正面", "char-zhou-正面", "loc-room", "prop-01", "prop-02", "prop-03"}
	if !slices.Equal(got, want) {
		t.Fatalf("名额够时不截、前一节的在前：%v", got)
	}
}
