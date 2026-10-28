package app

import (
	"encoding/json"
	"fmt"
	"image/png"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// testLockedStyle 是测试项目锁定的风格：风格锚和主基调色卡（护士蓝在主角组）。
const testLockedStyle = `"style_anchor":{"medium":"真人实拍","grading":"低饱和冷调","era":"当代"},"master_palette":{"groups":[{"key":"protagonist","name":"主角组","colors":[{"name_zh":"护士蓝","name_en":"Nurse Blue","hex":"#5B7FA3","usage":"林小满制服"}]},{"key":"environment","name":"环境组","colors":[{"name_zh":"日光灯白","name_en":"Fluorescent White","hex":"#E8ECEF","usage":"医院顶灯"},{"name_zh":"水泥灰","name_en":"Concrete Grey","hex":"#6E6E6E","usage":"墙面"}]}]},"style_lock":{"source":"library"}`

// lockTestProjectStyle 给项目写上锁定的风格（风格锚 + 主基调色卡），其余字段原样。
func lockTestProjectStyle(t *testing.T, db *gorm.DB, projectID string) {
	t.Helper()
	var project model.Project
	if err := db.First(&project, "id = ?", projectID).Error; err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if strings.TrimSpace(project.BibleJSON) != "" {
		if err := json.Unmarshal([]byte(project.BibleJSON), &doc); err != nil {
			t.Fatal(err)
		}
	}
	var locked map[string]any
	if err := json.Unmarshal([]byte("{"+testLockedStyle+"}"), &locked); err != nil {
		t.Fatal(err)
	}
	for k, v := range locked {
		doc[k] = v
	}
	encoded, _ := json.Marshal(doc)
	if err := db.Model(&model.Project{}).Where("id = ?", projectID).Update("bible_json", string(encoded)).Error; err != nil {
		t.Fatal(err)
	}
}

// seedEpisodeProject 在小说项目上加两集剧本：第 1 集两场，第 2 集一场；连戏表里已有角色，风格已锁定。
func seedEpisodeProject(t *testing.T, svc *Service, db *gorm.DB) (model.Project, []model.ProjectUnit) {
	t.Helper()
	project, _ := seedNovelProject(t, svc, db)
	units, err := svc.ImportProjectUnits("user-1", project.ID, ImportProjectUnitsRequest{Units: []CreateProjectUnitRequest{
		{Kind: "episode", Title: "七床空了", SourceText: plainTextToHTML("## 内·病房走廊·夜\n（日光灯一盏接一盏。）\n林小满：七床？\n## 内·电梯口·夜\n周岸：小满。")},
		{Kind: "episode", Title: "现在信了", SourceText: plainTextToHTML("## 内·电梯里·夜\n（应急灯闪了一下。）\n周岸：退后！")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	bible := `{"characters":[{"name":"林小满","anchors":["护士服"]}],` + testLockedStyle + `}`
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("bible_json", bible).Error; err != nil {
		t.Fatal(err)
	}
	project.BibleJSON = bible
	var episodes []model.ProjectUnit
	for _, u := range units {
		if u.Kind == model.ProjectUnitKindEpisode {
			episodes = append(episodes, u)
		}
	}
	return project, episodes
}

func queueScenePaletteTask(t *testing.T, svc *Service, db *gorm.DB, project model.Project, upstream string, req DramaProjectStepRunRequest) *model.Task {
	t.Helper()
	step, _ := drama.StepByID(drama.StepScenePalette)
	current, err := svc.repo.ProjectForUser("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := svc.planScenePalette(current, step, req)
	if err != nil {
		t.Fatal(err)
	}
	taskReq := dramaStepTaskRequest(project.ID, "", plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": upstream, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = newDramaRunMeta(plan, "")
	return insertDramaTestTask(t, svc, db, project.ID, taskReq)
}

const (
	styleAnchorLine = `{"style_anchor": {"medium": "动画", "lighting": "霓虹"}}`
	paletteScene1   = `{"scene": "E01S01", "inherits_from": "", "shared_colors": [{"name_zh": "日光灯白", "name_en": "Fluorescent White", "hex": "#E8ECEF", "usage": "顶灯", "group": "environment"}, {"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙面", "group": "environment"}], "new_colors": [{"name_zh": "护士蓝", "name_en": "Nurse Blue", "hex": "#5B7FA3", "usage": "制服", "group": "protagonist"}], "purpose": "开场建立医院夜班", "references": "《白色巨塔》走廊"}`
	paletteScene2   = `{"scene": "E01S02", "inherits_from": "E01S01", "shared_colors": [{"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙面", "group": "environment"}], "new_colors": [{"name_zh": "电梯钢", "name_en": "Elevator Steel", "hex": "#9AA3A8", "usage": "电梯门", "group": "props"}, {"name_zh": "病号服条纹", "name_en": "Ward Stripe", "hex": "#7FA7C9", "usage": "周岸", "group": "supporting"}], "references": ["《追击者》电梯口", "《白日焰火》"]}`
	paletteScene3   = `{"scene": "E02S01", "inherits_from": "E01S02", "shared_colors": [{"name_zh": "电梯钢", "name_en": "Elevator Steel", "hex": "#9AA3A8", "usage": "轿厢", "group": "props"}], "new_colors": [{"name_zh": "应急红", "name_en": "Alarm Red", "hex": "#B3261E", "usage": "应急灯", "group": "environment"}, {"name_zh": "暗影", "name_en": "Deep Shadow", "hex": "#1C1C1C", "usage": "角落", "group": "environment"}]}`
)

func scenePaletteUpstream() *streamingShotsUpstream {
	return &streamingShotsUpstream{scenes: map[string][]string{
		// 第一轮多写一行风格锚：忽略，不改项目的风格锚。
		"E01S01": {styleAnchorLine, paletteScene1, paletteScene2},
		"E01S02": {paletteScene3},
	}}
}

func scenePaletteOutcomeOf(t *testing.T, task *model.Task) DramaScenePaletteOutcome {
	t.Helper()
	var result struct {
		DramaStep DramaScenePaletteOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	return result.DramaStep
}

func paletteSummaries(t *testing.T, svc *Service, projectID string) map[string]ProjectAssetSummary {
	t.Helper()
	assets, err := svc.FilterProjectAssets("user-1", projectID, ProjectAssetFilter{Category: string(model.AssetCategoryPalette)})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ProjectAssetSummary{}
	for _, a := range assets {
		if a.Palette != nil && a.Palette.Master == nil {
			out[a.Palette.Code] = a
		}
	}
	return out
}

func endStates(t *testing.T, svc *Service, projectID string) map[string]drama.EndState {
	t.Helper()
	progress, err := svc.DramaProgress("user-1", projectID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]drama.EndState{}
	for _, e := range progress.EndStates {
		out[e.Key] = e
	}
	return out
}

// 两集三场：一个任务两轮，收尾把色彩脚本合并进 bible（风格锚、主基调色卡、characters 原样），每场一张已确认的场次色卡，
// 色卡图由代码合成并绑上（不建图片任务）；重跑同一场加版本、不多建资产；总览「色卡齐了」按当前场次表成立。
func TestScenePaletteWritesColorScriptAndPalettesThroughWorker(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, svc, db)
	upstream := scenePaletteUpstream()
	server := httptest.NewServer(upstream)
	defer server.Close()
	before, _ := svc.ProjectBible("user-1", project.ID)

	queueScenePaletteTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{})
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("场次色卡：%v %s %q", err, done.Status, done.Error)
	}
	if upstream.calls != 2 || upstream.histories[1] != 2 {
		t.Fatalf("一集一轮、第二轮带第一轮对话：%d %v", upstream.calls, upstream.histories)
	}
	outcome := scenePaletteOutcomeOf(t, done)
	if outcome.Step != drama.StepScenePalette || outcome.Status != DramaStepOutcomeWritten || outcome.Episodes != 2 || outcome.Scenes != 3 || outcome.Errors != 0 || outcome.Palettes.Created != 3 || outcome.Rendered != 3 || len(outcome.ImageFailures) != 0 || outcome.Parse.AnchorIgnored != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	bible, err := svc.ProjectBible("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, bible["style_anchor"], before["style_anchor"]) || !jsonEqual(t, bible["master_palette"], before["master_palette"]) || len(bible["characters"].([]any)) != 1 {
		t.Fatalf("风格锚、主基调色卡、其他字段原样：%+v", bible)
	}
	script := bible["color_script"].([]any)
	third := script[2].(map[string]any)
	if len(script) != 3 || third["inherits_from"] != "E01S02" || third["scene"] != "内·电梯里·夜" || third["unit_id"] != episodes[1].ID || third["scene_rule"] != float64(drama.SceneRuleMerged) {
		t.Fatalf("色彩脚本（场次头、单元、切场规则由服务端填）：%+v", script)
	}
	if refs := script[1].(map[string]any)["references"]; refs != "《追击者》电梯口；《白日焰火》" {
		t.Fatalf("references 数组应拼成一个字符串：%#v", refs)
	}
	if group := third["new_colors"].([]any)[0].(map[string]any)["group"]; group != drama.PaletteGroupEnvironment {
		t.Fatalf("颜色带分组：%v", group)
	}
	palettes := paletteSummaries(t, svc, project.ID)
	second := palettes["E01S02"]
	if len(palettes) != 3 || second.Title != "E01S02 色卡 · 内·电梯口·夜" || second.Status != model.AssetVersionStatusConfirmed || second.MediaType != "image" || second.VersionCount != 1 || second.Palette.Kind != paletteKindScene || second.Palette.ImageStatus != dramaImageBound || len(second.Palette.Entry.Colors()) != 3 {
		t.Fatalf("场次色卡：%+v %+v", second, second.Palette)
	}
	if second.Palette.Design != "3色复合色板=1色跨段共享 + 2色本段新增（承接 E01S01）" {
		t.Fatalf("色板设计：%+v", second.Palette)
	}
	// 合成图：1920×1080 的 PNG，绑在这一版的主图上；payload 记场编号、单元、切场规则。
	raw, _ := svc.repo.AssetForUser("user-1", second.ID)
	meta := paletteMetaOf(raw)
	if meta.Kind != paletteKindScene || meta.UnitID != episodes[0].ID || meta.SceneRule != drama.SceneRuleMerged || meta.Code != "E01S02" {
		t.Fatalf("payload 的场次色卡标记：%+v", meta)
	}
	reps, _ := svc.repo.AssetRepresentations(raw.PrimaryVersionID)
	if len(reps) != 1 || reps[0].Role != "primary" {
		t.Fatalf("合成图绑到这一版：%+v", reps)
	}
	_, reader, err := svc.OpenResource("user-1", reps[0].ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(reader)
	reader.Close()
	if err != nil || img.Bounds().Dx() != drama.MasterPaletteImageWidth || img.Bounds().Dy() != drama.MasterPaletteImageHeight {
		t.Fatalf("合成图：%v %v", err, img.Bounds())
	}
	// 不调图片模型：除了这个文本任务没有别的任务。
	var others int64
	db.Model(&model.Task{}).Where("id <> ?", done.ID).Count(&others)
	if others != 0 {
		t.Fatalf("不该建图片任务：%d", others)
	}
	states := endStates(t, svc, project.ID)
	if !states["style"].Done || !states["palette"].Done || states["palette"].Detail != "3/3 场有场次色卡" {
		t.Fatalf("风格定了、色卡齐了：%+v %+v", states["style"], states["palette"])
	}

	// 重跑：同一场的色卡加一个版本（合成图绑到这一版），不多建资产；只重跑第 2 集时第 1 集的色彩脚本保留、可被承接。
	upstream.calls, upstream.histories = 0, nil
	queueScenePaletteTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{})
	if again, err := runQueuedTask(t, svc); err != nil || again.Status != model.TaskStatusSucceeded || scenePaletteOutcomeOf(t, again).Palettes.Versioned != 3 {
		t.Fatalf("重跑：%v %q %+v", err, again.Error, scenePaletteOutcomeOf(t, again))
	}
	after := paletteSummaries(t, svc, project.ID)
	if len(after) != 3 || after["E01S02"].VersionCount != 2 || after["E01S02"].ID != second.ID || after["E01S02"].MediaType != "image" {
		t.Fatalf("重跑加一个版本、不加资产：%+v", after)
	}
	upstream.calls, upstream.histories = 0, nil
	upstream.scenes = map[string][]string{"E01S01": {paletteScene3}}
	queueScenePaletteTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{SourceUnitIDs: []string{episodes[1].ID}})
	if only, err := runQueuedTask(t, svc); err != nil || only.Status != model.TaskStatusSucceeded {
		t.Fatalf("只重跑第 2 集：%v %q", err, only.Error)
	}
	bible, _ = svc.ProjectBible("user-1", project.ID)
	if len(bible["color_script"].([]any)) != 3 {
		t.Fatalf("只重跑第 2 集时第 1 集的色彩脚本保留：%+v", bible["color_script"])
	}

	// 承接本次没重写的第 1 集、承接色 HEX 改了：收尾检查报 error，任务失败，bible 和色卡都不动。
	kept, _ := svc.ProjectBible("user-1", project.ID)
	upstream.calls, upstream.histories = 0, nil
	upstream.scenes = map[string][]string{"E01S01": {strings.Replace(paletteScene3, `"hex": "#9AA3A8"`, `"hex": "#9AA3A9"`, 1)}}
	queueScenePaletteTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{SourceUnitIDs: []string{episodes[1].ID}})
	failed, _ := runQueuedTask(t, svc)
	if failed.Status != model.TaskStatusFailed || !strings.Contains(failed.Error, "E02S01 承接色「电梯钢」写成了 #9AA3A9，承接场 E01S02 里是 #9AA3A8") {
		t.Fatalf("承接色不一致要失败：%s %q", failed.Status, failed.Error)
	}
	if now, _ := svc.ProjectBible("user-1", project.ID); !jsonEqual(t, now, kept) {
		t.Fatalf("失败时 bible 不动：%+v", now)
	}

	// 本次前面写过的场被承接、HEX 改了：这一轮当轮就判不合格，重试三次后失败。
	upstream.calls, upstream.histories = 0, nil
	upstream.scenes = map[string][]string{
		"E01S01": {paletteScene1, paletteScene2},
		"E01S02": {strings.Replace(paletteScene3, `"hex": "#9AA3A8"`, `"hex": "#9AA3A9"`, 1)},
	}
	queueScenePaletteTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{})
	retried, _ := runQueuedTask(t, svc)
	if retried.Status != model.TaskStatusFailed || upstream.calls != 4 || !strings.Contains(retried.Error, "可从第 2 集继续") {
		t.Fatalf("当轮不合格要重试：%s %d %q", retried.Status, upstream.calls, retried.Error)
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// 项目没锁定风格时不能生成场次色卡，提示先锁定风格。
func TestScenePaletteRequiresLockedStyle(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedNovelProject(t, svc, db)
	step, _ := drama.StepByID(drama.StepScenePalette)
	if _, err := svc.planScenePalette(&project, step, DramaProjectStepRunRequest{}); err == nil || !strings.Contains(err.Error(), "还没有锁定风格") {
		t.Fatalf("没锁定风格要拒绝：%v", err)
	}
	if _, err := svc.RunDramaProjectStep("user-1", project.ID, drama.StepScenePalette, DramaProjectStepRunRequest{Model: "test"}); err == nil || !strings.Contains(err.Error(), "还没有锁定风格") {
		t.Fatalf("接口同样拒绝：%v", err)
	}
}

// 没有剧集的项目按原文章节做色卡（一章当一集），不报错；提示词带锁定的风格锚和主基调色卡。
func TestScenePaletteFallsBackToChapters(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, chapters := seedNovelProject(t, svc, db)
	lockTestProjectStyle(t, db, project.ID)
	current, _ := svc.repo.ProjectForUser("user-1", project.ID)
	step, _ := drama.StepByID(drama.StepScenePalette)
	plan, err := svc.planScenePalette(current, step, DramaProjectStepRunRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.palette.FromChapters || len(plan.palette.Episodes) != 3 || plan.palette.Episodes[2].UnitID != chapters[2].ID || len(plan.scenes) != 3 {
		t.Fatalf("按原文章节：%+v", plan.palette)
	}
	for _, want := range []string{"章原文（项目还没有剧集", "E03S01 场次头：第三章 刀", "【项目风格锚】", "- 调色：低饱和冷调", "【主基调色卡】", "- 护士蓝 / Nurse Blue #5B7FA3：林小满制服"} {
		if !strings.Contains(plan.prompt.Text, want) {
			t.Fatalf("提示词缺「%s」：%s", want, plan.prompt.Text)
		}
	}
	if _, err := svc.planScenePalette(current, step, DramaProjectStepRunRequest{SourceUnitIDs: []string{"nope"}}); err == nil {
		t.Fatal("点名不存在的单元要拒绝")
	}
}

// chapterPaletteScene 是按原文章节做色卡时第 n 章的一场（不承接）。
func chapterPaletteScene(n int) string {
	return fmt.Sprintf(`{"scene": "E%02dS01", "inherits_from": "", "shared_colors": [{"name_zh": "日光灯白", "name_en": "Fluorescent White", "hex": "#E8ECEF", "usage": "顶灯", "group": "environment"}], "new_colors": [{"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙面", "group": "environment"}, {"name_zh": "护士蓝", "name_en": "Nurse Blue", "hex": "#5B7FA3", "usage": "制服", "group": "protagonist"}]}`, n)
}

// 只有原文时按 3 章做色卡（3 张），再改编成 2 集 3 场：「色卡齐了」变未完成并写清缺哪场、多哪场（原文章节版同编号的
// 色卡不算剧集的场）；按剧集重跑后第 3 章那张色卡标成场次已不存在（不删、不加版本、不合成），「色卡齐了」按当前 3 场算。
func TestScenePaletteRetiresPalettesOfVanishedScenes(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedNovelProject(t, svc, db)
	lockTestProjectStyle(t, db, project.ID)
	server := httptest.NewServer(&streamingShotsUpstream{scenes: map[string][]string{
		"E01S01": {chapterPaletteScene(1)},
		"E01S02": {chapterPaletteScene(2)},
		"E01S03": {chapterPaletteScene(3)},
	}})
	defer server.Close()
	queueScenePaletteTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{})
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded || scenePaletteOutcomeOf(t, done).Palettes.Created != 3 {
		t.Fatalf("按章节做色卡：%v %q", err, done.Error)
	}
	if s := endStates(t, svc, project.ID); !s["palette"].Done || s["palette"].Detail != "3/3 场有场次色卡" {
		t.Fatalf("按章节：%+v", s["palette"])
	}

	if _, err := svc.ImportProjectUnits("user-1", project.ID, ImportProjectUnitsRequest{Units: []CreateProjectUnitRequest{
		{Kind: "episode", Title: "七床空了", SourceText: plainTextToHTML("## 内·病房走廊·夜\n（日光灯一盏接一盏。）\n林小满：七床？\n## 内·电梯口·夜\n周岸：小满。")},
		{Kind: "episode", Title: "现在信了", SourceText: plainTextToHTML("## 内·电梯里·夜\n（应急灯闪了一下。）\n周岸：退后！")},
	}}); err != nil {
		t.Fatal(err)
	}
	s := endStates(t, svc, project.ID)
	for _, want := range []string{"0/3 场有场次色卡", "第 1 集第 1 场、第 1 集第 2 场、第 2 集第 1 场缺", "第 3 集第 1 场已不在当前剧集里"} {
		if s["palette"].Done || !strings.Contains(s["palette"].Detail, want) {
			t.Fatalf("detail 要写清（%s）：%q", want, s["palette"].Detail)
		}
	}

	server2 := httptest.NewServer(scenePaletteUpstream())
	defer server2.Close()
	queueScenePaletteTask(t, svc, db, project, server2.URL, DramaProjectStepRunRequest{})
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("按剧集重跑：%v %q", err, done.Error)
	}
	if got := scenePaletteOutcomeOf(t, done).Palettes; got != (DramaPaletteWriteSummary{Created: 1, Versioned: 2, Retired: 1}) {
		t.Fatalf("新建 E01S02、E01S01 和 E02S01 加版本、E03S01 标过期：%+v", got)
	}
	palettes := paletteSummaries(t, svc, project.ID)
	gone := palettes["E03S01"]
	if len(palettes) != 4 || !gone.Palette.SceneGone || gone.VersionCount != 1 || gone.Status != model.AssetVersionStatusConfirmed {
		t.Fatalf("E03S01 标成场次已不存在、不加版本、状态不动：%d %+v %+v", len(palettes), gone, gone.Palette)
	}
	if s := endStates(t, svc, project.ID); !s["palette"].Done || s["palette"].Detail != "3/3 场有场次色卡" {
		t.Fatalf("重跑后按当前 3 场算、过期的不计：%+v", s["palette"])
	}
	if _, err := svc.ResynthesizeDramaPalettes("user-1", project.ID, DramaPaletteResynthesizeRequest{AssetIDs: []string{gone.ID}}); err == nil || !strings.Contains(err.Error(), "场次已不存在") {
		t.Fatalf("场次已不存在的色卡不再合成：%v", err)
	}

	// 之后又写到这一场（比如改回三集）：加版本时恢复。
	current, _ := svc.repo.ProjectForUser("user-1", project.ID)
	writes, summary, err := svc.dramaPaletteWrites("user-1", current, []drama.ColorScriptEntry{{Episode: 3, SceneNo: 1, Scene: "内·走廊·夜"}}, nil)
	if err != nil || summary.Versioned != 1 || len(writes) != 1 || writes[0].Version == nil || writes[0].Asset.ID != gone.ID || strings.Contains(writes[0].Asset.PayloadJSON, `"stale"`) {
		t.Fatalf("又写到这一场时加版本并恢复：%v %+v %+v", err, summary, writes)
	}
}

// 「重新合成」只重画图、绑成新版本，不调模型；镜头上的色卡引用跟到新版本，产物过期。旧色卡（M8 之前图片模型出的）要先重新生成场次色卡。
func TestResynthesizeDramaPalettesFollowsShots(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedEpisodeProject(t, svc, db)
	server := httptest.NewServer(scenePaletteUpstream())
	defer server.Close()
	queueScenePaletteTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{})
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("场次色卡：%v %q", err, done.Error)
	}
	palette := paletteSummaries(t, svc, project.ID)["E01S01"]
	now := time.Now()
	if err := db.Create(&model.ShotAssetReference{ID: "ref-style", ShotID: "shot-1", AssetVersionID: palette.PrimaryVersionID, Role: shotAssetRoleStyle, Status: "linked", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ShotArtifact{ID: "artifact-1", ProjectID: project.ID, ShotID: "shot-1", Type: "video", Status: "ready", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := svc.ResynthesizeDramaPalettes("user-1", project.ID, DramaPaletteResynthesizeRequest{AssetIDs: []string{palette.ID}})
	if err != nil || len(result.Rendered) != 1 || len(result.Failed) != 0 {
		t.Fatalf("重新合成：%v %+v", err, result)
	}
	again := paletteSummaries(t, svc, project.ID)["E01S01"]
	if again.VersionCount != 2 || again.PrimaryVersionID == palette.PrimaryVersionID || again.Palette.ImageStatus != dramaImageBound || len(again.Palette.Entry.Colors()) != 3 {
		t.Fatalf("新版本、色板照旧：%+v %+v", again, again.Palette)
	}
	var ref model.ShotAssetReference
	db.First(&ref, "id = ?", "ref-style")
	var artifact model.ShotArtifact
	db.First(&artifact, "id = ?", "artifact-1")
	if ref.AssetVersionID != again.PrimaryVersionID || artifact.Status != "stale" {
		t.Fatalf("镜头引用跟到新版本、产物过期：%+v %+v", ref, artifact)
	}
	// 不点名时只画还没有图的；全都有图时什么都不做。
	if result, err := svc.ResynthesizeDramaPalettes("user-1", project.ID, DramaPaletteResynthesizeRequest{}); err != nil || len(result.Rendered) != 0 {
		t.Fatalf("都有图时不重画：%v %+v", err, result)
	}
	var tasks int64
	db.Model(&model.Task{}).Where("type = ? OR operation = ?", "canvas_image", "image").Count(&tasks)
	if tasks != 0 {
		t.Fatalf("不建图片任务：%d", tasks)
	}
}

// 视频编译：本段绑的场次色卡排在参考图最后；名额满了不带图、色值写进提示词。
func TestShotScenePaletteForCompile(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedEpisodeProject(t, svc, db)
	server := httptest.NewServer(scenePaletteUpstream())
	defer server.Close()
	queueScenePaletteTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{})
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("场次色卡：%v %q", err, done.Error)
	}
	palette := paletteSummaries(t, svc, project.ID)["E01S02"]
	if ref, err := svc.shotScenePalette("user-1", "shot-none"); err != nil || ref != nil {
		t.Fatalf("没绑色卡：%v %+v", err, ref)
	}
	if err := db.Create(&model.ShotAssetReference{ID: "ref-style", ShotID: "shot-1", AssetVersionID: palette.PrimaryVersionID, Role: shotAssetRoleStyle, Status: "linked", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	ref, err := svc.shotScenePalette("user-1", "shot-1")
	if err != nil || ref == nil || ref.File != palette.PrimaryVersionID || ref.Code != "E01S02" || len(ref.Colors) != 3 {
		t.Fatalf("本段的场次色卡：%v %+v", err, ref)
	}
}

// 合并时保留这次没重写的集；场次表里已经没有的场（集删了、改编成更少的集）去掉；剧集写出来以后，原文章节版同编号的条目
// （记的单元是章节）去掉；没记单元的旧条目按场次头对。不知道场次表（评测）时只按运行范围筛。
func TestKeptColorScriptMatchesCurrentScenes(t *testing.T) {
	table := map[string]styleSceneSlot{
		"E01S01": {UnitID: "ep1", Heading: "内·病房·夜"},
		"E02S01": {UnitID: "ep2", Heading: "内·走廊·夜"},
		"E02S02": {UnitID: "ep2", Heading: "外·天台·夜"},
	}
	script := []drama.ColorScriptEntry{
		{Episode: 1, SceneNo: 1, UnitID: "ep1"},
		{Episode: 2, SceneNo: 1, UnitID: "chapter2", Scene: "内·走廊·夜"},
		{Episode: 2, SceneNo: 2, Scene: "外·天台·夜"},
		{Episode: 3, SceneNo: 1, UnitID: "ep3"},
		{Episode: 2, SceneNo: 3, Scene: "第二章"},
	}
	kept := keptColorScript(script, map[int]bool{1: true}, table)
	if len(kept) != 1 || kept[0].Code() != "E02S02" {
		t.Fatalf("只留第 2 集第 2 场（旧条目按场次头对上）：%+v", kept)
	}
	if all := keptColorScript(script, map[int]bool{}, nil); len(all) != 5 {
		t.Fatalf("不知道场次表时不筛：%+v", all)
	}
}

// 评测：七床第一集当第 1 集做色卡（三个场次头，后两个都是电梯口，并成两场）；两场都写了、检查没有 error 才算解析成功（闸门只看解析率）。
func TestScenePaletteEvalResult(t *testing.T) {
	step, _ := drama.StepByID(drama.StepScenePalette)
	c, ok := step.EvalChapter("qichuang-e01")
	if !ok {
		t.Fatal("评测集缺 qichuang-e01")
	}
	in, err := loadDramaEvalChapter(c)
	if err != nil {
		t.Fatal(err)
	}
	scene := func(no int, inherits string) string {
		return strings.NewReplacer("SCENE_NO", strconv.Itoa(no), "INHERITS", inherits).Replace(`{"scene": "E01S0SCENE_NO", "inherits_from": "INHERITS", "shared_colors": [{"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙", "group": "environment"}], "new_colors": [{"name_zh": "护士蓝", "name_en": "Nurse Blue", "hex": "#5B7FA3", "usage": "制服", "group": "protagonist"}, {"name_zh": "夜色", "name_en": "Night Navy", "hex": "#1B2A41", "usage": "窗外", "group": "environment"}]}`)
	}
	full := strings.Join([]string{scene(1, ""), scene(2, "E01S01")}, "\n")
	r := scenePaletteEvalResult(drama.StepEvalChapterResult{Chapter: c.ID}, in, full)
	if !r.Parsed || r.Rows != 2 || r.Shots != 2 || r.Errors != 0 {
		t.Fatalf("两场齐应解析成功：%+v", r)
	}
	if r := scenePaletteEvalResult(drama.StepEvalChapterResult{Chapter: c.ID}, in, scene(1, "")); r.Parsed || !strings.Contains(r.Failure, "1/2") {
		t.Fatalf("只写一场不算解析成功：%+v", r)
	}
	// references 写成字符串数组、hex 写成单元素数组：两场照样解析成功，hex 降级只报 warn。
	arrays := strings.NewReplacer(`"usage": "墙", "group": "environment"}]`, `"usage": "墙", "group": "environment"}], "references": ["《误杀》雨夜", "《白日焰火》"]`, `"hex": "#1B2A41"`, `"hex": ["#1B2A41"]`).Replace(full)
	if r := scenePaletteEvalResult(drama.StepEvalChapterResult{Chapter: c.ID}, in, arrays); !r.Parsed || r.Rows != 2 || r.Errors != 0 || r.Rules[drama.RuleScenePaletteFieldType] != 2 {
		t.Fatalf("references 数组、hex 数组的变体应解析成功：%+v", r)
	}
	if verdict := step.Gate.Evaluate([]drama.StepEvalChapterResult{scenePaletteEvalResult(drama.StepEvalChapterResult{Chapter: c.ID}, in, full)}, nil); !verdict.Passed {
		t.Fatalf("闸门只看解析率：%+v", verdict)
	}
	plan, err := scenePalettePlan(in.project, []model.ProjectUnit{*in.unit}, map[string]int{in.unit.ID: 1}, false, nil, DramaStepUsage{Step: step.ID}, drama.StepSkill{ID: "yz-scene-palette", Files: []drama.StepSkillFile{{Path: "SKILL.md", Kind: "markdown", Content: "# 场次色卡"}}})
	if err != nil || len(plan.palette.Episodes[0].Scenes) != 2 || !strings.Contains(plan.prompt.Text, "E01S02 场次头：内·电梯口·夜") || strings.Contains(plan.prompt.Text, "E01S03") {
		t.Fatalf("评测章节组装提示词：%v %+v", err, plan)
	}
}
