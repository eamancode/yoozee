package app

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// seedEpisodeProject 在小说项目上加两集剧本：第 1 集两场，第 2 集一场；连戏表里已有角色和一项风格锚。
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
	bible := `{"characters":[{"name":"林小满","anchors":["护士服"]}],"style_anchor":{"era":"当代"}}`
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

func queueStyleBibleTask(t *testing.T, svc *Service, db *gorm.DB, project model.Project, upstream string, req DramaProjectStepRunRequest) *model.Task {
	t.Helper()
	step, _ := drama.StepByID(drama.StepStyleBible)
	current, err := svc.repo.ProjectForUser("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := svc.planStyleBible(current, step, req)
	if err != nil {
		t.Fatal(err)
	}
	plan.style.Images = req.PaletteImages != nil
	taskReq := dramaStepTaskRequest(project.ID, "", plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": upstream, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = newDramaRunMeta(plan, "")
	if req.PaletteImages != nil {
		taskReq.Input["paletteImageTask"] = req.PaletteImages
	}
	return insertDramaTestTask(t, svc, db, project.ID, taskReq)
}

const (
	styleAnchorLine = `{"style_anchor": {"medium": "真人实拍", "lighting": "实景光源，冷暖分区", "camera": "定焦，克制", "grading": "低饱和冷调", "forbidden": ["霓虹"]}}`
	styleScene1     = `{"episode": 1, "scene_no": 1, "scene": "内·病房走廊·夜", "inherits_from": "", "shared_colors": [{"name_zh": "日光灯白", "name_en": "Fluorescent White", "hex": "#E8ECEF", "usage": "顶灯"}, {"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙面"}], "new_colors": [{"name_zh": "护士蓝", "name_en": "Nurse Blue", "hex": "#5B7FA3", "usage": "制服"}], "purpose": "开场建立医院夜班", "references": "《白色巨塔》走廊"}`
	styleScene2     = `{"episode": 1, "scene_no": 2, "scene": "内·电梯口·夜", "inherits_from": "E01S01", "shared_colors": [{"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙面"}], "new_colors": [{"name_zh": "电梯钢", "name_en": "Elevator Steel", "hex": "#9AA3A8", "usage": "电梯门"}, {"name_zh": "病号服条纹", "name_en": "Ward Stripe", "hex": "#7FA7C9", "usage": "周岸"}]}`
	styleScene3     = `{"episode": 2, "scene_no": 1, "scene": "内·电梯里·夜", "inherits_from": "E01S02", "shared_colors": [{"name_zh": "电梯钢", "name_en": "Elevator Steel", "hex": "#9AA3A8", "usage": "轿厢"}], "new_colors": [{"name_zh": "应急红", "name_en": "Alarm Red", "hex": "#B3261E", "usage": "应急灯"}, {"name_zh": "暗影", "name_en": "Deep Shadow", "hex": "#1C1C1C", "usage": "角落"}]}`
)

func styleBibleUpstream() *streamingShotsUpstream {
	return &streamingShotsUpstream{scenes: map[string][]string{
		"E01S01": {styleAnchorLine, `{"episode": 1, "title": "七床空了"}`, styleScene1, styleScene2},
		"E01S02": {`{"episode": 2, "title": "现在信了"}`, styleScene3},
	}}
}

func styleBibleOutcomeOf(t *testing.T, task *model.Task) DramaStyleBibleOutcome {
	t.Helper()
	var result struct {
		DramaStep DramaStyleBibleOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	return result.DramaStep
}

// 两集三场：一个任务两轮，收尾把风格锚、色彩脚本合并进 bible（characters 和旧的 era 原样），每场一张已确认的色卡；
// 重跑同一场加版本、不多建资产；总览「风格定了」成立、「色卡齐了」等出图。
func TestStyleBibleWritesBibleAndPalettesThroughWorker(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, svc, db)
	upstream := styleBibleUpstream()
	server := httptest.NewServer(upstream)
	defer server.Close()

	queueStyleBibleTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{})
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("导演风格：%v %s %q", err, done.Status, done.Error)
	}
	if upstream.calls != 2 || upstream.histories[1] != 2 {
		t.Fatalf("一集一轮、第二轮带第一轮对话：%d %v", upstream.calls, upstream.histories)
	}
	outcome := styleBibleOutcomeOf(t, done)
	if outcome.Step != drama.StepStyleBible || outcome.Status != DramaStepOutcomeWritten || outcome.Episodes != 2 || outcome.Scenes != 3 || outcome.Errors != 0 || outcome.Palettes.Created != 3 {
		t.Fatalf("outcome = %+v", outcome)
	}
	bible, err := svc.ProjectBible("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	anchor := bible["style_anchor"].(map[string]any)
	if anchor["medium"] != "真人实拍" || anchor["era"] != "当代" || len(bible["characters"].([]any)) != 1 {
		t.Fatalf("风格锚合并、其他字段原样：%+v", bible)
	}
	script := bible["color_script"].([]any)
	if len(script) != 3 || script[2].(map[string]any)["inherits_from"] != "E01S02" {
		t.Fatalf("色彩脚本：%+v", script)
	}
	assets, err := svc.FilterProjectAssets("user-1", project.ID, ProjectAssetFilter{Category: string(model.AssetCategoryPalette)})
	if err != nil || len(assets) != 3 {
		t.Fatalf("每场一张色卡：%v %d", err, len(assets))
	}
	byCode := map[string]ProjectAssetSummary{}
	for _, a := range assets {
		byCode[a.Palette.Code] = a
	}
	second := byCode["E01S02"]
	if second.Title != "E01S02 色卡 · 内·电梯口·夜" || second.Status != model.AssetVersionStatusConfirmed || second.Palette.ImageStatus != paletteImageNone || len(second.Palette.Entry.Colors()) != 3 {
		t.Fatalf("色卡：%+v %+v", second, second.Palette)
	}
	if !strings.Contains(second.Palette.Prompt, "1. Concrete Grey #6E6E6E") || !strings.Contains(second.Palette.Prompt, "/ EP01 / SC02 COLOR REFERENCE") || second.Palette.Design != "3色复合色板=1色跨段共享 + 2色本段新增（承接 E01S01）" {
		t.Fatalf("色卡提示词与色板设计：%+v", second.Palette)
	}
	progress, err := svc.DramaProgress("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]drama.EndState{}
	for _, e := range progress.EndStates {
		states[e.Key] = e
	}
	if !states["style"].Done || states["palette"].Done || !strings.Contains(states["palette"].Detail, "0/3") {
		t.Fatalf("风格定了、色卡未齐：%+v %+v", states["style"], states["palette"])
	}

	// 重跑：同一场的色卡加版本，不多建资产；只重跑第 2 集时第 1 集的色彩脚本保留、可被承接。
	upstream.calls, upstream.histories = 0, nil
	queueStyleBibleTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{})
	if again, err := runQueuedTask(t, svc); err != nil || again.Status != model.TaskStatusSucceeded || styleBibleOutcomeOf(t, again).Palettes.Versioned != 3 {
		t.Fatalf("重跑：%v %q %+v", err, again.Error, styleBibleOutcomeOf(t, again))
	}
	after, _ := svc.FilterProjectAssets("user-1", project.ID, ProjectAssetFilter{Category: string(model.AssetCategoryPalette)})
	if len(after) != 3 || after[0].VersionCount != 2 {
		t.Fatalf("重跑加版本不加资产：%d %+v", len(after), after)
	}
	upstream.calls, upstream.histories = 0, nil
	upstream.scenes = map[string][]string{"E01S01": {styleAnchorLine, `{"episode": 2, "title": "现在信了"}`, styleScene3}}
	queueStyleBibleTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{SourceUnitIDs: []string{episodes[1].ID}})
	if only, err := runQueuedTask(t, svc); err != nil || only.Status != model.TaskStatusSucceeded {
		t.Fatalf("只重跑第 2 集：%v %q", err, only.Error)
	}
	bible, _ = svc.ProjectBible("user-1", project.ID)
	if len(bible["color_script"].([]any)) != 3 {
		t.Fatalf("只重跑第 2 集时第 1 集的色彩脚本保留：%+v", bible["color_script"])
	}

	// 承接本次没重写的第 1 集、共享色 HEX 改了：收尾检查报 error，任务失败，bible 和色卡都不动。
	before, _ := svc.ProjectBible("user-1", project.ID)
	upstream.calls, upstream.histories = 0, nil
	upstream.scenes = map[string][]string{"E01S01": {styleAnchorLine, `{"episode": 2}`, strings.Replace(styleScene3, `"hex": "#9AA3A8"`, `"hex": "#9AA3A9"`, 1)}}
	queueStyleBibleTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{SourceUnitIDs: []string{episodes[1].ID}})
	failed, _ := runQueuedTask(t, svc)
	if failed.Status != model.TaskStatusFailed || !strings.Contains(failed.Error, "E02S01 共享色「电梯钢」写成了 #9AA3A9，承接场 E01S02 里是 #9AA3A8") {
		t.Fatalf("承接色不一致要失败：%s %q", failed.Status, failed.Error)
	}
	if kept, _ := svc.ProjectBible("user-1", project.ID); !jsonEqual(t, kept, before) {
		t.Fatalf("失败时 bible 不动：%+v", kept)
	}

	// 本次前面写过的场被承接、HEX 改了：这一轮当轮就判不合格，重试三次后失败。
	upstream.calls, upstream.histories = 0, nil
	upstream.scenes = map[string][]string{
		"E01S01": {styleAnchorLine, `{"episode": 1, "title": "七床空了"}`, styleScene1, styleScene2},
		"E01S02": {`{"episode": 2}`, strings.Replace(styleScene3, `"hex": "#9AA3A8"`, `"hex": "#9AA3A9"`, 1)},
	}
	queueStyleBibleTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{})
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

// 没有剧集的项目按原文章节定风格（一章当一集），不报错。
func TestStyleBibleFallsBackToChapters(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, chapters := seedNovelProject(t, svc, db)
	step, _ := drama.StepByID(drama.StepStyleBible)
	plan, err := svc.planStyleBible(&project, step, DramaProjectStepRunRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.style.FromChapters || len(plan.style.Episodes) != 3 || plan.style.Episodes[2].UnitID != chapters[2].ID || len(plan.scenes) != 3 {
		t.Fatalf("按原文章节：%+v", plan.style)
	}
	if !strings.Contains(plan.prompt.Text, "章原文（项目还没有剧集") || !strings.Contains(plan.prompt.Text, "E03S01 场次头：第三章 刀") {
		t.Fatalf("提示词：%s", plan.prompt.Text)
	}
	if _, err := svc.planStyleBible(&project, step, DramaProjectStepRunRequest{SourceUnitIDs: []string{"nope"}}); err == nil {
		t.Fatal("点名不存在的单元要拒绝")
	}
}

// 出图任务按模板建（换上色卡提示词和资产出图的 metadata，模板留在顶层给下一张用）；绑图后取色校验写进主图 metadata 和资产 payload，
// 画错一块时 passed=false 但图保留。
func TestPaletteImageTaskAndColorCheck(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedEpisodeProject(t, svc, db)
	server := httptest.NewServer(styleBibleUpstream())
	defer server.Close()
	queueStyleBibleTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{})
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("导演风格：%v %q", err, done.Error)
	}
	palettes, err := svc.projectPaletteAssets("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	asset := palettes["E01S01"]
	version, err := svc.repo.AssetVersion(asset.PrimaryVersionID)
	if err != nil {
		t.Fatal(err)
	}
	template := DramaImageTaskTemplate{Model: "gpt-image-2", Input: map[string]any{"mode": "image", "config": map[string]any{"channelId": "c1", "apiKey": "k", "size": "16:9"}, "referenceImages": []any{"x"}, "metadata": map[string]any{"providerOptions": map[string]any{"a": 1}}}}
	req := dramaPaletteImageTaskRequest(project.ID, asset, version, template, "style-task")
	metadata := req.Input["metadata"].(map[string]any)
	if req.Type != "canvas_image" || req.Model != "gpt-image-2" || metadata["operation"] != "project_asset_image" || metadata["projectAssetVersionId"] != version.ID || metadata["nodeId"] != "project-asset-image:"+asset.ID || metadata["providerOptions"] == nil {
		t.Fatalf("出图任务：%+v", req)
	}
	if req.Input["referenceImages"] != nil || req.Input["config"].(map[string]any)["size"] != "16:9" || !strings.HasPrefix(req.Prompt, "【prompt】") || req.Input["paletteImageTask"] == nil {
		t.Fatalf("出图任务 input：%+v", req.Input)
	}

	bind := func(taskID string, hexes []string) ProjectAssetSummary {
		t.Helper()
		resourceID := "palette-" + taskID
		var buf bytes.Buffer
		if err := png.Encode(&buf, drawPaletteCard(1600, 900, hexes)); err != nil {
			t.Fatal(err)
		}
		key := "user-1/" + resourceID + ".png"
		path := filepath.Join(svc.dataDir, "resources", filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.Resource{ID: resourceID, UserID: "user-1", Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: key, Width: 1600, Height: 900, MimeType: "image/png", Size: int64(buf.Len())}).Error; err != nil {
			t.Fatal(err)
		}
		current, _ := svc.repo.AssetForUser("user-1", asset.ID)
		if _, err := svc.bindProjectAssetImage("user-1", project.ID, asset.ID, BindProjectAssetImageRequest{ResourceID: resourceID, ExpectedVersionID: current.PrimaryVersionID}, taskID); err != nil {
			t.Fatal(err)
		}
		if err := svc.recordPaletteImage(model.Task{ID: taskID, UserID: "user-1", ProjectID: project.ID}, asset.ID, "bound"); err != nil {
			t.Fatal(err)
		}
		summaries, _ := svc.FilterProjectAssets("user-1", project.ID, ProjectAssetFilter{Category: string(model.AssetCategoryPalette)})
		for _, s := range summaries {
			if s.ID == asset.ID {
				return s
			}
		}
		t.Fatal("色卡不见了")
		return ProjectAssetSummary{}
	}
	hexes := []string{"#E8ECEF", "#6E6E6E", "#5B7FA3"}
	good := bind("task-good", hexes)
	if good.MediaType != "image" || good.Palette.ImageStatus != paletteImageBound || good.Palette.Check == nil || !good.Palette.Check.Passed || len(good.Palette.Check.Blocks) != 3 {
		t.Fatalf("按 HEX 画的色卡取色通过：%+v %+v", good, good.Palette)
	}
	var rep model.AssetRepresentation
	if err := db.First(&rep, "task_id = ? AND role = ?", "task-good", "primary").Error; err != nil || !strings.Contains(rep.MetadataJSON, `"paletteCheck"`) {
		t.Fatalf("取色结果写进主图 metadata：%v %s", err, rep.MetadataJSON)
	}
	bad := bind("task-bad", []string{"#E8ECEF", "#2E7D32", "#5B7FA3"})
	if bad.MediaType != "image" || bad.Palette.Check == nil || bad.Palette.Check.Passed || bad.Palette.Check.Blocks[1].DeltaE <= drama.PaletteCheckMaxDeltaE {
		t.Fatalf("画错一块：取色不过但图保留：%+v %+v", bad, bad.Palette.Check)
	}
	progress, _ := svc.DramaProgress("user-1", project.ID)
	for _, e := range progress.EndStates {
		if e.Key == "palette" && (e.Done || !strings.Contains(e.Detail, "1/3")) {
			t.Fatalf("一张出了图：%+v", e)
		}
	}
}

// drawPaletteCard 按取色位置画色卡：白底、中部 80% 等分。
func drawPaletteCard(w, h int, hexes []string) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	block := float64(w) * 0.8 / float64(len(hexes))
	for i, hex := range hexes {
		v, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
		r, g, b := uint8(v>>16), uint8(v>>8), uint8(v)
		x0 := int(float64(w)*0.1 + float64(i)*block + block*0.06)
		x1 := int(float64(w)*0.1 + float64(i+1)*block - block*0.06)
		for y := h/2 - int(block/2); y < h/2+int(block/2); y++ {
			for x := x0; x < x1; x++ {
				img.Set(x, y, color.RGBA{r, g, b, 255})
			}
		}
	}
	return img
}

// 合并时保留这次没重写的集；集号超过项目现在集数的（剧集删了、改编成更少的集）去掉。
func TestKeptColorScriptDropsVanishedEpisodes(t *testing.T) {
	script := []drama.ColorScriptEntry{{Episode: 1, SceneNo: 1}, {Episode: 2, SceneNo: 1}, {Episode: 3, SceneNo: 1}}
	kept := keptColorScript(script, map[int]bool{1: true}, 2)
	if len(kept) != 1 || kept[0].Code() != "E02S01" {
		t.Fatalf("只留第 2 集：%+v", kept)
	}
	if all := keptColorScript(script, map[int]bool{}, 0); len(all) != 3 {
		t.Fatalf("不知道集数时不按集数筛：%+v", all)
	}
}

// 评测：七床第一集当第 1 集定风格；三场都写了、有风格锚才算解析成功（闸门只看解析率）。
func TestStyleBibleEvalResult(t *testing.T) {
	step, _ := drama.StepByID(drama.StepStyleBible)
	c, ok := step.EvalChapter("qichuang-e01")
	if !ok {
		t.Fatal("评测集缺 qichuang-e01")
	}
	in, err := loadDramaEvalChapter(c)
	if err != nil {
		t.Fatal(err)
	}
	scene := func(no int, inherits string) string {
		return strings.NewReplacer("SCENE_NO", strconv.Itoa(no), "INHERITS", inherits).Replace(`{"episode": 1, "scene_no": SCENE_NO, "scene": "内·电梯口·夜", "inherits_from": "INHERITS", "shared_colors": [{"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙"}], "new_colors": [{"name_zh": "护士蓝", "name_en": "Nurse Blue", "hex": "#5B7FA3", "usage": "制服"}, {"name_zh": "夜色", "name_en": "Night Navy", "hex": "#1B2A41", "usage": "窗外"}]}`)
	}
	full := strings.Join([]string{styleAnchorLine, `{"episode": 1}`, scene(1, ""), scene(2, "E01S01"), scene(3, "E01S02")}, "\n")
	r := styleBibleEvalResult(drama.StepEvalChapterResult{Chapter: c.ID}, in, full)
	if !r.Parsed || r.Rows != 3 || r.Shots != 3 || r.Errors != 0 {
		t.Fatalf("三场齐、有风格锚应解析成功：%+v", r)
	}
	short := strings.Join([]string{styleAnchorLine, `{"episode": 1}`, scene(1, "")}, "\n")
	if r := styleBibleEvalResult(drama.StepEvalChapterResult{Chapter: c.ID}, in, short); r.Parsed || !strings.Contains(r.Failure, "1/3") {
		t.Fatalf("只写一场不算解析成功：%+v", r)
	}
	if verdict := step.Gate.Evaluate([]drama.StepEvalChapterResult{styleBibleEvalResult(drama.StepEvalChapterResult{Chapter: c.ID}, in, full)}, nil); !verdict.Passed {
		t.Fatalf("闸门只看解析率：%+v", verdict)
	}
	plan, err := styleBiblePlan(in.project, []model.ProjectUnit{*in.unit}, map[string]int{in.unit.ID: 1}, false, DramaStepUsage{Step: step.ID}, drama.StepSkill{ID: "yz-style-bible", Files: []drama.StepSkillFile{{Path: "SKILL.md", Kind: "markdown", Content: "# 导演风格"}}})
	if err != nil || len(plan.style.Episodes[0].Scenes) != 3 || !strings.Contains(plan.prompt.Text, "E01S03 场次头：内·电梯口·夜") {
		t.Fatalf("评测章节组装提示词：%v %+v", err, plan)
	}
}
