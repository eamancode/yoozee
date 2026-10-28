package app

import (
	"slices"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func legacyQichuangEntries(unitID string) []drama.ColorScriptEntry {
	headings := []string{"内·病房走廊·夜", "内·电梯口·夜", "内·电梯口·夜"}
	entries := make([]drama.ColorScriptEntry, 0, len(headings))
	for i, h := range headings {
		entries = append(entries, drama.ColorScriptEntry{
			Episode: 1, SceneNo: i + 1, Scene: h, UnitID: unitID,
			NewColors: []drama.PaletteColor{{NameZh: "主色", NameEn: "key", Hex: qichuangPaletteHex[i], Usage: "墙面"}, {NameZh: "辅色", NameEn: "fill", Hex: "#F0F0F0", Usage: "灯"}, {NameZh: "点缀", NameEn: "accent", Hex: "#C0392B", Usage: "呼叫灯"}},
		})
	}
	entries[2].InheritsFrom, entries[2].SharedColors = "E01S02", []drama.PaletteColor{{NameZh: "主色", NameEn: "key", Hex: qichuangPaletteHex[1], Usage: "墙面"}}
	return entries
}

// 七床第 1 集：三个场次头（走廊、电梯口、电梯口）的场次表是两场；规则改变前写的三场色彩脚本、色卡和镜头表保持原编号，
// 编号没错位的（第 1、2 场）照常绑，错位的（第 3 场）不绑、场次表带提示；重装旧镜头表不换编号；按新场次重排后色彩脚本两场。
func TestDramaScenesLegacyDataNoticeAndRenumber(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	writeQichuangPalettes(t, svc, project.ID, legacyQichuangEntries(unit.ID))
	if _, err := svc.WriteDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: loadOnlineRowsRaw(t), LegacyScenes: true}); err != nil {
		t.Fatalf("旧镜头表：%v", err)
	}

	view, err := svc.DramaScenes("user-1", project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Scenes) != 2 || view.Episode != 1 || !view.FromChapters || view.Scenes[0].ID != "E01S01" || view.Scenes[1].ID != "E01S02" || view.Scenes[1].ShotScene != "E01S02" {
		t.Fatalf("场次表两场：%+v", view)
	}
	second := view.Scenes[1]
	if second.Location != "电梯口" || len(second.SourceHeadings) != 2 || second.Heading != "内·电梯口·夜" || second.LineCount != second.Lines[1]-second.Lines[0]+1 || !slices.Equal(second.LegacyNos, []int{2, 3}) {
		t.Fatalf("第 2 场并了两个场次头：%+v", second)
	}
	notice := view.Notice
	if notice == nil || notice.LegacyScenes != 3 || !slices.Equal(notice.ColorScript, []string{"E01S03"}) || !slices.Equal(notice.Palettes, []string{"E01S03"}) || !notice.Shotlist || !strings.Contains(notice.Message, "从 3 场变成 2 场") ||
		!strings.Contains(notice.Message, "镜头表请重新生成分段；色彩脚本与色卡请到章节页点「生成场次色卡」重新生成") {
		t.Fatalf("旧数据编号错位的提示：%+v", notice)
	}
	if units, err := svc.ProjectDramaScenes("user-1", project.ID); err != nil || len(units) != 1 || units[0].UnitID != unit.ID {
		t.Fatalf("项目场次表：%v %+v", err, units)
	}
	if _, err := svc.DramaScenes("someone-else", project.ID, unit.ID); err == nil {
		t.Fatal("别人的项目读不到场次表")
	}

	// 旧色彩脚本：第 1、2 场编号没错位，照常绑色卡；第 3 场错位，不绑。
	fresh, err := svc.repo.ProjectForUser("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	palettes, err := svc.unitScenePalettes("user-1", fresh, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	assets, _ := svc.projectPaletteAssets("user-1", project.ID)
	if len(palettes) != 2 || palettes["E01S01"] != assets["E01S01"].PrimaryVersionID || palettes["E01S02"] != assets["E01S02"].PrimaryVersionID {
		t.Fatalf("编号没错位的旧色卡照常绑：%+v", palettes)
	}
	units, _ := svc.repo.ProjectUnits(project.ID)
	if n := legacyShiftedColorScript(mustProjectBible(t, svc, "user-1", project.ID), units); n != 1 {
		t.Fatalf("错位的旧色彩脚本 1 场：%d", n)
	}
	progress, err := svc.DramaProgress("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	style := ""
	for _, e := range progress.EndStates {
		if e.Key == "palette" {
			style = e.Detail
		}
	}
	if !strings.Contains(style, "场次规则已更新") {
		t.Fatalf("总览「色卡齐了」说明提示场次规则更新：%q", style)
	}

	// 重装旧镜头表：仍按旧规则切台词表，不换编号；第 3 场的段绑它现在所在那一场（电梯口）的色卡。
	repacked, err := svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{})
	if err != nil {
		t.Fatalf("重装旧镜头表：%v", err)
	}
	scenes := map[string]bool{}
	for _, seg := range repacked.Segments {
		scenes[seg.Spec.Scene] = true
	}
	if !scenes["E01S03"] {
		t.Fatalf("重装不换编号：%v", scenes)
	}
	stored, _ := svc.DramaShotlist("user-1", project.ID, unit.ID)
	if stored.Script == nil || stored.Script.SceneRule != 0 || len(stored.Script.Scenes) != 3 {
		t.Fatalf("重装后台词表仍是旧规则三场：%+v", stored.Script)
	}
	for _, seg := range repacked.Segments {
		if seg.Spec.Scene != "E01S03" {
			continue
		}
		if refs := shotReferencesByRole(t, db, seg.ShotID, shotAssetRoleStyle); len(refs) != 1 || refs[0].AssetVersionID != assets["E01S02"].PrimaryVersionID {
			t.Fatalf("旧第 3 场的段绑现在第 2 场（电梯口）的色卡：%+v", refs)
		}
	}

	// 按新场次重排：先预览不写库，再写。
	preview, err := svc.RenumberDramaScenes("user-1", project.ID, DramaSceneRenumberRequest{})
	if err != nil || preview.Applied || len(preview.Moves) != 3 {
		t.Fatalf("预览：%v %+v", err, preview)
	}
	if after, _ := svc.DramaScenes("user-1", project.ID, unit.ID); after.Notice == nil || len(after.Notice.ColorScript) != 1 {
		t.Fatal("预览不改数据")
	}
	applied, err := svc.RenumberDramaScenes("user-1", project.ID, DramaSceneRenumberRequest{Apply: true})
	if err != nil || !applied.Applied {
		t.Fatalf("重排：%v %+v", err, applied)
	}
	dropped := slices.IndexFunc(applied.Moves, func(m drama.SceneMove) bool { return m.From == "E01S03" })
	if dropped < 0 || !applied.Moves[dropped].Dropped || applied.Moves[dropped].To != "E01S02" {
		t.Fatalf("旧第 3 场并进第 2 场：%+v", applied.Moves)
	}
	retired := slices.IndexFunc(applied.Palettes, func(p DramaPaletteRenumber) bool { return p.From == "E01S03" })
	if retired < 0 || !applied.Palettes[retired].Retired || applied.Palettes[retired].Note == "" {
		t.Fatalf("旧第 3 场的色卡标成场次已不存在：%+v", applied.Palettes)
	}
	_, script := projectStyleBible(mustProjectBible(t, svc, "user-1", project.ID))
	if len(script) != 2 || script[0].SceneRule != drama.SceneRuleMerged || script[1].SceneRule != drama.SceneRuleMerged {
		t.Fatalf("重排后色彩脚本两场、带规则标记：%+v", script)
	}
	after, _ := svc.DramaScenes("user-1", project.ID, unit.ID)
	if after.Notice == nil || len(after.Notice.ColorScript) != 0 || len(after.Notice.Palettes) != 0 || !after.Notice.Shotlist || !strings.Contains(after.Notice.Message, "重新生成分段") || strings.Contains(after.Notice.Message, "场次色卡") {
		t.Fatalf("重排后只剩镜头表要重新生成：%+v", after.Notice)
	}
	summaries, err := svc.ProjectAssets("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range summaries {
		if a.Palette != nil && a.Palette.Code == "E01S03" && (!a.Palette.SceneGone || a.Palette.Note == "") {
			t.Fatalf("资产页旧第 3 场色卡显示场次已不存在和说明：%+v", a.Palette)
		}
	}
}

// 并场之后的场编号往前挪：走廊、电梯口、电梯口、天台 → 三场。旧规则的第 2 场还在第 2 场（对得上），旧第 3 场并进第 2 场、
// 旧第 4 场（天台）现在是第 3 场：旧数据的 E01S03 不能当成天台用；新规则写的 E01S03 照常用。
func TestColorScriptCurrentRejectsShiftedLegacyCodes(t *testing.T) {
	text := "<p>## 内·走廊·夜</p><p>（走。）</p><p>## 内·电梯口·夜</p><p>（等。）</p><p>## 内·电梯口·夜</p><p>（打。）</p><p>## 外·天台·夜</p><p>（风。）</p>"
	units := []model.ProjectUnit{{ID: "ep1", Kind: model.ProjectUnitKindEpisode, Title: "第 1 集", SourceText: text}}
	table := styleSceneTable(units)
	if len(table) != 3 || table["E01S02"].LegacyShifted || !table["E01S03"].LegacyShifted || table["E01S03"].Heading != "外·天台·夜" {
		t.Fatalf("场次表三场，第 3 场的旧编号错位：%+v", table)
	}
	legacy := func(no int, scene string) drama.ColorScriptEntry {
		return drama.ColorScriptEntry{Episode: 1, SceneNo: no, Scene: scene, UnitID: "ep1"}
	}
	if !colorScriptCurrent(legacy(2, "内·电梯口·夜"), table) || colorScriptCurrent(legacy(3, "内·电梯口·夜"), table) || colorScriptCurrent(legacy(4, "外·天台·夜"), table) {
		t.Fatal("旧第 2 场对得上，旧第 3、4 场对不上")
	}
	fresh := legacy(3, "外·天台·夜")
	fresh.SceneRule = drama.SceneRuleMerged
	if !colorScriptCurrent(fresh, table) {
		t.Fatal("新规则写的第 3 场对得上")
	}
	// 重跑别的集时，这一集编号错位的旧条目原样留着（旧场还在，并进了前面的场）；旧场已经不存在的照常去掉；重跑这一集时不留。
	script := []drama.ColorScriptEntry{legacy(2, "内·电梯口·夜"), legacy(3, "内·电梯口·夜"), legacy(4, "外·天台·夜"), legacy(5, "外·街口·夜"), fresh}
	kept := legacyShiftedKept(script, map[int]bool{2: true}, units, table)
	if len(kept) != 2 || kept[0].SceneNo != 3 || kept[1].SceneNo != 4 || kept[0].SceneRule != 0 {
		t.Fatalf("留下旧第 3、4 场：%+v", kept)
	}
	if again := legacyShiftedKept(script, map[int]bool{1: true}, units, table); len(again) != 0 {
		t.Fatalf("重跑这一集时不留：%+v", again)
	}
	if n := legacyShiftedColorScript(`{"color_script": [{"episode": 1, "scene_no": 3, "unit_id": "ep1"}, {"episode": 1, "scene_no": 4, "unit_id": "ep1"}, {"episode": 1, "scene_no": 2, "unit_id": "ep1"}]}`, units); n != 2 {
		t.Fatalf("错位的旧色彩脚本两场：%d", n)
	}
}
