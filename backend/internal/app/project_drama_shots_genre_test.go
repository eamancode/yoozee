package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// qichuangGenreReply 是戏类判定的回复：第 1 场文戏、第 2 场悬疑戏、第 3 场武戏。
const qichuangGenreReply = `{"scene": "E01S01", "genre": "文戏", "reason": "护士和家属对话"}
{"scene": "E01S02", "genre": "悬疑", "reason": "电梯口的异响"}
{"scene": "E01S03", "genre": "武戏", "reason": "走廊里扭打"}`

// qichuangPaletteHex 是三场色卡各自独有的一个颜色（断言提示词里只出现本场的）。
var qichuangPaletteHex = []string{"#1A2B3C", "#4D5E6F", "#7A8B9C"}

// seedQichuangStyle 给七床第一章写风格锚和三场色彩脚本，并建三张色卡（与导演风格收尾同一条写库路径）。
func seedQichuangStyle(t *testing.T, svc *Service, project model.Project, unit model.ProjectUnit) []drama.ColorScriptEntry {
	t.Helper()
	scenes := drama.ChapterShotsScenes(chapterPlainText(unit.SourceText), unit.Title)
	if len(scenes) != 3 {
		t.Fatalf("七床第一章三场：%d", len(scenes))
	}
	entries := make([]drama.ColorScriptEntry, 0, len(scenes))
	for i, sc := range scenes {
		entries = append(entries, drama.ColorScriptEntry{
			Episode: 1, SceneNo: i + 1, Scene: sc.Heading, UnitID: unit.ID, Purpose: "第" + sc.ID + "场的冷暖对比",
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
	writes, _, err := svc.dramaPaletteWrites("user-1", current, entries, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.ApplyDramaStyleBible("user-1", projectID, string(bible), writes, time.Now()); err != nil {
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

// 七床第一集（已有色卡、风格锚）生成分段：第一轮之前判出三场戏类，每一轮只带本场的戏类卡和本场色卡；
// 装段后每段以 role=style 绑本场色卡；重跑导演风格让第 2 场色卡加版本后，只有第 2 场的段换到新版本、产物过期。
func TestChapterShotsInjectsSceneGenreAndPaletteThenFollowsPaletteVersion(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	entries := seedQichuangStyle(t, svc, project, unit)
	if fresh, err := svc.repo.ProjectForUser("user-1", project.ID); err == nil {
		project = *fresh
	}
	upstream := &streamingShotsUpstream{scenes: qichuangSceneRows(t), genres: qichuangGenreReply}
	server := httptest.NewServer(upstream)
	defer server.Close()

	queued := queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if shots := taskDramaShots(queued.InputJSON); shots == nil || len(shots.Cards) != 4 || len(shots.Palettes) != 3 {
		t.Fatalf("任务带上四张戏类卡和三场色卡：%+v", shots)
	}
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("生成分段：%v %s %q", err, done.Status, done.Error)
	}
	if upstream.genreCalls != 1 || upstream.calls != 3 {
		t.Fatalf("判一次戏类、三场三轮：%d %d", upstream.genreCalls, upstream.calls)
	}
	wantGenre := []string{"文戏", "悬疑戏", "武戏"}
	for i, prompt := range upstream.prompts {
		if !strings.Contains(prompt, "【戏类卡："+wantGenre[i]+"】") {
			t.Fatalf("第 %d 场带%s卡：%.300s", i+1, wantGenre[i], prompt[max(0, len(prompt)-3000):])
		}
		for j, other := range wantGenre {
			if j != i && strings.Contains(prompt, "【戏类卡："+other+"】") {
				t.Fatalf("第 %d 场不带%s卡", i+1, other)
			}
			if j != i && strings.Contains(prompt, qichuangPaletteHex[j]) {
				t.Fatalf("第 %d 场不带第 %d 场的色卡", i+1, j+1)
			}
		}
		if !strings.Contains(prompt, "【本场色卡】") || !strings.Contains(prompt, qichuangPaletteHex[i]) {
			t.Fatalf("第 %d 场带本场色卡 HEX", i+1)
		}
	}
	if !strings.Contains(upstream.prompts[0], "【全局风格锚】") || !strings.Contains(upstream.prompts[0], "手持跟拍") {
		t.Fatalf("第一轮带风格锚（含镜头语言）")
	}
	var result struct {
		DramaRun  dramaRunProgress     `json:"dramaRun"`
		DramaStep DramaStepTaskOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(done.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.DramaRun.Genres) != 3 || result.DramaRun.Genres[1].Genre != "悬疑戏" || result.DramaRun.Genres[1].Fallback {
		t.Fatalf("结果记下各场戏类：%+v", result.DramaRun.Genres)
	}
	if result.DramaRun.Scenes[2].Genre != "武戏" || result.DramaRun.Turns[2].Genre != "武戏" {
		t.Fatalf("进度里每场标戏类：%+v %+v", result.DramaRun.Scenes, result.DramaRun.Turns)
	}
	deltas, err := svc.repo.TaskTextDeltas("user-1", done.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) == 0 || !strings.Contains(deltas[0].Content, `"genre":"文戏"`) {
		t.Fatalf("第一行回合标记带戏类：%.200s", deltas[0].Content)
	}

	// 装段绑色卡：每段 role=style 一条，指向本场色卡的主版本。
	palettes, err := svc.projectPaletteAssets("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	segments, err := svc.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil || len(segments) == 0 {
		t.Fatalf("段：%v %d", err, len(segments))
	}
	var scene1Shot, scene2Shot string
	for _, seg := range segments {
		refs := shotReferencesByRole(t, db, seg.ShotID, shotAssetRoleStyle)
		code := drama.SceneCode(1, sceneNoOf(t, seg.Spec.Scene))
		if len(refs) != 1 || refs[0].AssetVersionID != palettes[code].PrimaryVersionID {
			t.Fatalf("段 %s（%s）绑本场色卡：%+v", seg.Spec.ID, seg.Spec.Scene, refs)
		}
		switch seg.Spec.Scene {
		case "E01S01":
			scene1Shot = seg.ShotID
		case "E01S02":
			scene2Shot = seg.ShotID
		}
	}
	for _, shotID := range []string{scene1Shot, scene2Shot} {
		if err := db.Create(&model.ShotArtifact{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, ShotID: shotID, Type: "storyboard", Version: 1, Status: "ready", Selected: true}).Error; err != nil {
			t.Fatal(err)
		}
	}

	// 重跑导演风格：第 2 场色卡加版本。
	oldVersion := palettes["E01S02"].PrimaryVersionID
	entries[1].NewColors[0].Hex = "#123456"
	writeQichuangPalettes(t, svc, project.ID, entries[1:2])
	palettes, _ = svc.projectPaletteAssets("user-1", project.ID)
	if palettes["E01S02"].PrimaryVersionID == oldVersion {
		t.Fatal("第 2 场色卡加了版本")
	}
	if refs := shotReferencesByRole(t, db, scene2Shot, shotAssetRoleStyle); len(refs) != 1 || refs[0].AssetVersionID != palettes["E01S02"].PrimaryVersionID {
		t.Fatalf("第 2 场的段跟到新版本：%+v", refs)
	}
	var states []model.ShotArtifact
	if err := db.Where("shot_id IN ?", []string{scene1Shot, scene2Shot}).Find(&states).Error; err != nil {
		t.Fatal(err)
	}
	for _, a := range states {
		want := "ready"
		if a.ShotID == scene2Shot {
			want = "stale"
		}
		if a.Status != want {
			t.Fatalf("只有引用第 2 场色卡的段过期：%s %s", a.ShotID, a.Status)
		}
	}
}

func sceneNoOf(t *testing.T, scene string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(scene, "E01S%02d", &n); err != nil {
		t.Fatalf("场编号 %q：%v", scene, err)
	}
	return n
}

// 戏类判定调用失败时不让任务失败：各场按文戏写完。
func TestChapterShotsGenreFailureFallsBackToDrama(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	upstream := &streamingShotsUpstream{scenes: qichuangSceneRows(t), genreStatus: http.StatusBadRequest}
	server := httptest.NewServer(upstream)
	defer server.Close()
	queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("判定失败照样写完：%v %s %q", err, done.Status, done.Error)
	}
	progress := taskDramaRunProgress(done.ResultJSON)
	for _, g := range progress.Genres {
		if g.Genre != drama.SceneGenreDrama || !g.Fallback {
			t.Fatalf("各场按文戏兜底：%+v", progress.Genres)
		}
	}
	if !strings.Contains(upstream.prompts[0], "【戏类卡：文戏】") || !strings.Contains(upstream.prompts[1], "按前面给过的【戏类卡：文戏】拍") || strings.Contains(upstream.prompts[1], "<skill-file path=\"cards/戏类-文戏.md\">") {
		t.Fatalf("文戏卡只在第一场整张带上，之后只提一句")
	}
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
