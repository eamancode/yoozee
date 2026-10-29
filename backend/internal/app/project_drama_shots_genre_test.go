package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// qichuangPaletteHex 是各场色卡独有的一个颜色（断言提示词里只出现本场的）。
var qichuangPaletteHex = []string{"#1A2B3C", "#4D5E6F", "#7A8B9C"}

// seedQichuangStyle 给七床第一章写风格锚和两场色彩脚本（新规则：三个场次头并成两场），并建两张色卡（与导演风格收尾同一条写库路径）。
func seedQichuangStyle(t *testing.T, svc *Service, project model.Project, unit model.ProjectUnit) []drama.ColorScriptEntry {
	t.Helper()
	scenes := drama.ChapterShotsScenes(chapterPlainText(unit.SourceText), unit.Title)
	if len(scenes) != 2 {
		t.Fatalf("七床第一章两场：%d", len(scenes))
	}
	entries := make([]drama.ColorScriptEntry, 0, len(scenes))
	for i, sc := range scenes {
		entries = append(entries, drama.ColorScriptEntry{
			Episode: 1, SceneNo: i + 1, Scene: sc.Heading, UnitID: unit.ID, SceneRule: drama.SceneRuleMerged, Purpose: "第" + sc.ID + "场的冷暖对比",
			NewColors: []drama.PaletteColor{{NameZh: "主色", NameEn: "key", Hex: qichuangPaletteHex[i], Usage: "墙面"}, {NameZh: "辅色", NameEn: "fill", Hex: "#F0F0F0", Usage: "灯"}, {NameZh: "点缀", NameEn: "accent", Hex: "#C0392B", Usage: "呼叫灯"}},
		})
	}
	writeQichuangPalettes(t, svc, project.ID, entries)
	return entries
}

// writeQichuangPalettes 写 bible_json（风格锚 + 色彩脚本）和色卡：已有同一场的色卡加版本。
func writeQichuangPalettes(t *testing.T, svc *Service, projectID string, entries []drama.ColorScriptEntry) {
	t.Helper()
	bible, _ := json.Marshal(map[string]any{
		"style_anchor": map[string]any{"medium": "写实真人短剧", "lighting": "夜班冷光为主", "camera": "手持跟拍、少用航拍", "forbidden": []string{"霓虹"}},
		"color_script": entries,
	})
	current, err := svc.repo.ProjectForUser("user-1", projectID)
	if err != nil {
		t.Fatal(err)
	}
	writes, _, err := svc.dramaPaletteWrites("user-1", current, entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.ApplyDramaScenePalettes("user-1", projectID, func(string) (string, error) { return string(bible), nil }, writes, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func shotReferencesByRole(t *testing.T, db *gorm.DB, shotID string, role string) []model.ShotAssetReference {
	t.Helper()
	var refs []model.ShotAssetReference
	if err := db.Where("shot_id = ? AND role = ?", shotID, role).Find(&refs).Error; err != nil {
		t.Fatal(err)
	}
	return refs
}

func sceneNoOf(t *testing.T, scene string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(scene, "E01S%02d", &n); err != nil {
		t.Fatalf("场编号 %q：%v", scene, err)
	}
	return n
}

// 角色换定妆（加版本）：引用旧版本的镜头换到新版本、产物过期；没引用它的镜头不动。
func TestCharacterVersionFollowsShotReferences(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	var character model.Asset
	if err := db.Where("category = ? AND id IN (?)", model.AssetCategoryCharacter, db.Model(&model.ProjectAssetLink{}).Select("asset_id").Where("project_id = ?", project.ID)).First(&character).Error; err != nil {
		t.Fatal(err)
	}
	oldVersion := character.PrimaryVersionID
	linked := model.Shot{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, Title: "S01", Description: "段", Status: "draft"}
	other := model.Shot{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, Title: "S02", Description: "段", Status: "draft", Position: 1}
	for _, shot := range []*model.Shot{&linked, &other} {
		if err := db.Create(shot).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.LinkShotAsset("user-1", project.ID, linked.ID, LinkShotAssetRequest{AssetVersionID: oldVersion, Role: "reference"}); err != nil {
		t.Fatal(err)
	}
	for _, shot := range []model.Shot{linked, other} {
		if err := db.Create(&model.ShotArtifact{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, ShotID: shot.ID, Type: "video", Version: 1, Status: "ready", Selected: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	next, err := svc.createNextCharacterVersion(project.ID, &character, character.Title, "", nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if refs := shotReferencesByRole(t, db, linked.ID, "reference"); len(refs) != 1 || refs[0].AssetVersionID != next.ID {
		t.Fatalf("镜头引用换到新版本：%+v", refs)
	}
	var artifacts []model.ShotArtifact
	if err := db.Where("project_id = ?", project.ID).Find(&artifacts).Error; err != nil {
		t.Fatal(err)
	}
	for _, a := range artifacts {
		if (a.ShotID == linked.ID) != (a.Status == "stale") {
			t.Fatalf("只有引用这个角色的镜头产物过期：%s %s", a.ShotID, a.Status)
		}
	}
}

func referenceKeys(t *testing.T, db *gorm.DB, shotID string) []string {
	t.Helper()
	var refs []model.ShotAssetReference
	if err := db.Where("shot_id = ? AND role <> ?", shotID, "output").Order("asset_version_id, role").Find(&refs).Error; err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(refs))
	for _, ref := range refs {
		keys = append(keys, ref.AssetVersionID+"|"+ref.Role)
	}
	return keys
}

// 拆段：新段继承原段的人物引用和本场色卡（不继承产出登记），之后色卡重出图加版本时新段跟到新版本、产物过期；
// 合段：留下的段并上被并掉的段的引用，同一版本同一用途只留一条。
func TestSegmentSplitInheritsReferencesAndMergeUnionsThem(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	entries := seedQichuangStyle(t, svc, project, unit)
	palettes, err := svc.projectPaletteAssets("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	var character model.Asset
	if err := db.Where("category = ? AND id IN (?)", model.AssetCategoryCharacter, db.Model(&model.ProjectAssetLink{}).Select("asset_id").Where("project_id = ?", project.ID)).First(&character).Error; err != nil {
		t.Fatal(err)
	}
	views, err := svc.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{dramaSpec("S01", 10, ""), dramaSpec("S02", 6, "")}})
	if err != nil || len(views) != 2 {
		t.Fatalf("写两段：%v %d", err, len(views))
	}
	s01, s02 := views[0].ShotID, views[1].ShotID
	for _, link := range []struct{ shot, version, role string }{
		{s01, character.PrimaryVersionID, "reference"},
		{s01, palettes["E01S01"].PrimaryVersionID, shotAssetRoleStyle},
		{s01, character.PrimaryVersionID, "output"},
		{s02, character.PrimaryVersionID, "reference"},
		{s02, palettes["E01S02"].PrimaryVersionID, shotAssetRoleStyle},
	} {
		if _, err := svc.LinkShotAsset("user-1", project.ID, link.shot, LinkShotAssetRequest{AssetVersionID: link.version, Role: link.role}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := svc.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{dramaSpec("S01", 6, ""), dramaSpec("S01b", 4, "")}, Order: []string{"S01", "S01b", "S02"}, Inherit: map[string][]string{"S01b": {"S02x"}}}); err == nil || !strings.Contains(err.Error(), "S02x") {
		t.Fatalf("继承来源不是本章已有的段要拒绝：%v", err)
	}
	views, err = svc.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{dramaSpec("S01", 6, ""), dramaSpec("S01b", 4, "")}, Order: []string{"S01", "S01b", "S02"}, Inherit: map[string][]string{"S01b": {"S01"}}})
	if err != nil || len(views) != 3 || views[1].Spec.ID != "S01b" {
		t.Fatalf("拆段：%v %+v", err, views)
	}
	tail := views[1].ShotID
	if got, want := referenceKeys(t, db, tail), referenceKeys(t, db, s01); len(want) != 2 || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("新段的引用与原段一致（含色卡）：%v vs %v", got, want)
	}
	if outputs := shotReferencesByRole(t, db, tail, "output"); len(outputs) != 0 {
		t.Fatalf("产出登记不继承：%+v", outputs)
	}
	if err := db.Create(&model.ShotArtifact{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, ShotID: tail, Type: "storyboard", Version: 1, Status: "ready", Selected: true}).Error; err != nil {
		t.Fatal(err)
	}
	entries[0].NewColors[0].Hex = "#654321"
	writeQichuangPalettes(t, svc, project.ID, entries[0:1])
	palettes, _ = svc.projectPaletteAssets("user-1", project.ID)
	if refs := shotReferencesByRole(t, db, tail, shotAssetRoleStyle); len(refs) != 1 || refs[0].AssetVersionID != palettes["E01S01"].PrimaryVersionID {
		t.Fatalf("新段跟到第 1 场色卡新版本：%+v", refs)
	}
	var artifact model.ShotArtifact
	if err := db.Where("shot_id = ?", tail).First(&artifact).Error; err != nil || artifact.Status != "stale" {
		t.Fatalf("色卡换版本后新段产物过期：%v %s", err, artifact.Status)
	}

	views, err = svc.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{dramaSpec("S01b", 10, "")}, Order: []string{"S01", "S01b"}, Inherit: map[string][]string{"S01b": {"S02"}}})
	if err != nil || len(views) != 2 {
		t.Fatalf("合段：%v %d", err, len(views))
	}
	merged := referenceKeys(t, db, tail)
	want := []string{character.PrimaryVersionID + "|reference", palettes["E01S01"].PrimaryVersionID + "|" + shotAssetRoleStyle, palettes["E01S02"].PrimaryVersionID + "|" + shotAssetRoleStyle}
	if len(merged) != len(want) {
		t.Fatalf("合段取并集去重：%v", merged)
	}
	for _, key := range want {
		if !slices.Contains(merged, key) {
			t.Fatalf("合段后缺 %s：%v", key, merged)
		}
	}
}
