package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// seedLinXiaoman 建一个已确认、已有三视图的角色「林小满」（资产页手动建的，不是资产设计建的）。
func seedLinXiaoman(t *testing.T, svc *Service, db *gorm.DB, projectID string) ProjectCharacterDetail {
	t.Helper()
	created, err := svc.CreateProjectCharacter("user-1", projectID, CreateProjectCharacterRequest{Name: "林小满", Definition: map[string]any{"appearance": "二十多岁护士，短发", "aliases": []any{"林护士"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Resource{ID: "lin-sheet", UserID: "user-1", Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "user-1/lin.png", Width: 1536, Height: 864, MimeType: "image/png", Size: 10}).Error; err != nil {
		t.Fatal(err)
	}
	detail, err := svc.ReplaceProjectCharacterRepresentations("user-1", projectID, created.Asset.ID, ReplaceCharacterRepresentationsRequest{Representations: []CharacterRepresentationInput{{Role: "turnaround_sheet", ResourceID: "lin-sheet"}, {Role: "primary", ResourceID: "lin-sheet"}}})
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

const (
	assetsLin    = `{"category": "character", "name": "小满", "aliases": ["林小满", "小满护士"], "appearance": "", "prompt_en": "", "first_scene": 1, "palette_hint": ""}`
	assetsZhou   = `{"category": "character", "name": "周岸", "aliases": ["周哥"], "appearance": "三十岁男性，条纹病号服，右手缠绷带", "prompt_en": "character turnaround sheet, front view, side view, back view, man in striped hospital gown", "first_scene": 2, "palette_hint": "病号服条纹"}`
	assetsHall   = `{"category": "environment", "name": "病房走廊", "aliases": [], "appearance": "狭长走廊，日光灯一盏接一盏", "prompt_en": "empty hospital corridor at night, fluorescent lights, no people", "first_scene": 1, "palette_hint": "日光灯白", "anchors": ["护士站柜台", "七床门口"]}`
	assetsLift   = `{"category": "environment", "name": "电梯口", "aliases": [], "appearance": "两部老电梯，指示灯一红一绿", "prompt_en": "empty elevator lobby, two old elevators, no people", "first_scene": 2, "palette_hint": "", "anchors": ["左侧电梯门"]}`
	assetsKnife  = `{"category": "prop", "name": "折叠刀", "aliases": [], "appearance": "黑柄折叠刀，刃口有缺", "prompt_en": "a black-handled folding knife, plain background, no people", "first_scene": 2, "palette_hint": ""}`
	assetsBadRow = `{"category": "prop", "name": "病历夹", "aliases": [], "appearance": "", "prompt_en": "", "first_scene": 2, "palette_hint": ""}`
	assetsZhou2  = `{"category": "character", "name": "周岸", "aliases": ["岸哥"], "appearance": "", "prompt_en": "", "first_scene": 1, "palette_hint": ""}`
	assetsInLift = `{"category": "environment", "name": "电梯轿厢", "aliases": ["电梯里"], "appearance": "狭小轿厢，应急灯", "prompt_en": "empty elevator car, emergency light, no people", "first_scene": 1, "palette_hint": ""}`
)

func queueAssetsExtractTask(t *testing.T, svc *Service, db *gorm.DB, project model.Project, unitID string, upstream string, template *DramaImageTaskTemplate) *model.Task {
	t.Helper()
	current, err := svc.repo.ProjectForUser("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := svc.planDramaStep("user-1", current, unitID, drama.StepAssetsExtract)
	if err != nil {
		t.Fatal(err)
	}
	plan.assets.Images = template != nil
	taskReq := dramaStepTaskRequest(project.ID, unitID, plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": upstream, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = newDramaRunMeta(plan, unitID)
	if template != nil {
		taskReq.Input["assetImageTask"] = template
	}
	return insertDramaTestTask(t, svc, db, project.ID, taskReq)
}

func assetsOutcomeOf(t *testing.T, task *model.Task) DramaAssetsExtractOutcome {
	t.Helper()
	var result struct {
		DramaStep DramaAssetsExtractOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	return result.DramaStep
}

func projectAssetsByTitle(t *testing.T, svc *Service, projectID string) map[string]ProjectAssetSummary {
	t.Helper()
	assets, err := svc.ProjectAssets("user-1", projectID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ProjectAssetSummary{}
	for _, a := range assets {
		out[a.Title] = a
	}
	return out
}

// 第 1 集两场：一个任务两轮，收尾把「周岸」「病房走廊」「电梯口」「折叠刀」直接建成已确认资产（候选记成已确认、带出处），
// 「小满」并进已有的「林小满」（补别名、名字和三视图不动），没写提示词的新资产跳过；第 2 集再提取，周岸只合并不重建。
func TestAssetsExtractWritesConfirmedAssetsThroughWorker(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, svc, db)
	lin := seedLinXiaoman(t, svc, db, project.ID)
	bible := `{"style_anchor":{"medium":"真人实拍"},"color_script":[{"episode":1,"scene_no":1,"scene":"内·病房走廊·夜","inherits_from":"","shared_colors":[{"name_zh":"日光灯白","name_en":"Fluorescent White","hex":"#E8ECEF","usage":"顶灯"}],"new_colors":[],"unit_id":"` + episodes[0].ID + `"},{"episode":1,"scene_no":2,"scene":"内·电梯口·夜","inherits_from":"","shared_colors":[{"name_zh":"病号服条纹","name_en":"Ward Stripe","hex":"#7FA7C9","usage":"周岸"}],"new_colors":[],"unit_id":"` + episodes[0].ID + `"}]}`
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("bible_json", bible).Error; err != nil {
		t.Fatal(err)
	}
	upstream := &streamingShotsUpstream{scenes: map[string][]string{
		"E01S01": {assetsLin, assetsHall},
		"E01S02": {assetsZhou, assetsLift, assetsKnife, assetsBadRow},
	}}
	server := httptest.NewServer(upstream)
	defer server.Close()

	queued := queueAssetsExtractTask(t, svc, db, project, episodes[0].ID, server.URL, nil)
	if !strings.Contains(queued.Prompt, "【项目已有资产】") || !strings.Contains(queued.Prompt, "- 林小满（角色；别名：林护士）") || !strings.Contains(queued.Prompt, "日光灯白 #E8ECEF") || !strings.Contains(queued.Prompt, "【全局风格锚】") {
		t.Fatalf("提示词带已有资产、本集色彩脚本、风格锚：\n%s", queued.Prompt)
	}
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("资产设计：%v %s %q", err, done.Status, done.Error)
	}
	if upstream.calls != 2 || upstream.histories[1] != 2 {
		t.Fatalf("一场一轮、第二轮带第一轮对话：%d %v", upstream.calls, upstream.histories)
	}
	outcome := assetsOutcomeOf(t, done)
	if outcome.Step != drama.StepAssetsExtract || outcome.Created != 4 || outcome.Merged != 1 || outcome.Skipped != 1 || outcome.ByCategory["environment"].Created != 2 || outcome.ByCategory["prop"].Skipped != 1 || outcome.ByCategory["character"].Merged != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}

	assets := projectAssetsByTitle(t, svc, project.ID)
	if len(assets) != 5 {
		t.Fatalf("林小满 + 4 个新资产：%d", len(assets))
	}
	merged := assets["林小满"]
	if merged.ID != lin.Asset.ID || merged.Character == nil || merged.Character.VisualStatus != "ready" || merged.VersionCount != lin.Asset.VersionCount+1 || merged.Image != nil {
		t.Fatalf("林小满补别名加一版、三视图搬过去、不记出图状态：%+v %+v", merged, merged.Character)
	}
	if got := definitionAliases(merged.Character.Definition); strings.Join(got, "/") != "林护士/小满护士/小满" || merged.Character.Definition["appearance"] != "二十多岁护士，短发" || merged.Character.Definition["firstScene"] != nil {
		t.Fatalf("别名合并、已有外观不覆盖、只补外观字段：%+v", merged.Character.Definition)
	}
	zhou := assets["周岸"]
	if zhou.Status != model.AssetVersionStatusConfirmed || zhou.Character == nil || zhou.Image == nil || zhou.Image.Status != dramaImageNone {
		t.Fatalf("周岸直接已确认、记出图状态：%+v", zhou)
	}
	if def := zhou.Character.Definition; def["appearance"] != "三十岁男性，条纹病号服，右手缠绷带" || !strings.HasSuffix(def["multiViewPrompt"].(string), "Color accents: Ward Stripe #7FA7C9.") || def["firstScene"] != "E01S02" || def["paletteHint"] != "病号服条纹" {
		t.Fatalf("角色外观提示词写进 appearance / multiViewPrompt：%+v", def)
	}
	hall := assets["病房走廊"]
	if hall.Category != model.AssetCategoryEnvironment || hall.Visual == nil || hall.Visual.Definition["prompt"] == "" || strings.Join(sceneAnchorList(hall.Visual.Definition["anchors"]), "/") != "护士站柜台/七床门口" {
		t.Fatalf("场景设定 description / prompt / anchors：%+v", hall.Visual)
	}
	if _, ok := assets["病历夹"]; ok {
		t.Fatal("没写提示词的新资产不建")
	}
	var candidates []model.ProjectAssetCandidate
	if err := db.Where("project_id = ?", project.ID).Find(&candidates).Error; err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 4 {
		t.Fatalf("新建的四个各一条候选：%d", len(candidates))
	}
	for _, c := range candidates {
		if c.Status != "confirmed" || c.Source != dramaAssetsCandidateSource || c.UnitID != episodes[0].ID || c.ResolvedAssetID == "" {
			t.Fatalf("候选直接已确认、记出处：%+v", c)
		}
	}
	page, err := svc.ProjectAssetCandidatesPage("user-1", project.ID, 1, 20, "", "pending_confirmation", "", "")
	if err != nil || page.Total != 0 {
		t.Fatalf("候选区为空：%v %+v", err, page)
	}
	progress, err := svc.DramaProgress("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range progress.EndStates {
		if s.Key == "refs" && !strings.Contains(s.Detail, "4/5 个资产还没出图") {
			t.Fatalf("参考齐了的说明：%s", s.Detail)
		}
	}

	// 第 2 集：周岸已在第 1 集建过，只补别名，不再建。
	upstream2 := &streamingShotsUpstream{scenes: map[string][]string{"E01S01": {assetsZhou2, assetsInLift}}}
	server2 := httptest.NewServer(upstream2)
	defer server2.Close()
	queueAssetsExtractTask(t, svc, db, project, episodes[1].ID, server2.URL, nil)
	done2, err := runQueuedTask(t, svc)
	if err != nil || done2.Status != model.TaskStatusSucceeded {
		t.Fatalf("第 2 集：%v %q", err, done2.Error)
	}
	if o := assetsOutcomeOf(t, done2); o.Created != 1 || o.Merged != 1 {
		t.Fatalf("第 2 集新增 1、合并 1：%+v", o)
	}
	assets = projectAssetsByTitle(t, svc, project.ID)
	if len(assets) != 6 || assets["周岸"].ID != zhou.ID || strings.Join(definitionAliases(assets["周岸"].Character.Definition), "/") != "周哥/岸哥" {
		t.Fatalf("周岸不重建、补别名：%d %+v", len(assets), assets["周岸"].Character)
	}
}

// 一行能用的资产都没有：任务失败，资产不动。
func TestAssetsExtractFailsWithoutUsableRows(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, svc, db)
	server := httptest.NewServer(&streamingShotsUpstream{scenes: map[string][]string{"E01S01": {assetsBadRow}}})
	defer server.Close()
	task := queueAssetsExtractTask(t, svc, db, project, episodes[1].ID, server.URL, nil)
	done, _ := runQueuedTask(t, svc)
	if done.Status != model.TaskStatusFailed || !strings.Contains(done.Error, "没有能用的资产行") {
		t.Fatalf("没有能用的行要失败：%s %q", done.Status, done.Error)
	}
	if assets, _ := svc.ProjectAssets("user-1", project.ID); len(assets) != 0 {
		t.Fatalf("资产不动：%d", len(assets))
	}
	_ = task
}

// 出图任务：角色走三视图模板（变量里是角色画风小节和设定），场景、道具走概念图（标题、外观、提示词、画风）；模板的 config 照用，
// 参考图去掉，模板放进顶层 assetImageTask。
func TestDramaAssetImageTaskRequests(t *testing.T) {
	project := &model.Project{ID: "p1"}
	style := styleProfileDocument{PresetID: "hk-real", Title: "港风写实", Prompt: "【视觉媒介】真人实拍\n【镜头语言】长焦\n【全局禁用】霓虹"}
	template := DramaImageTaskTemplate{Model: "gpt-image-2", Input: map[string]any{"mode": "image", "config": map[string]any{"apiKey": "k"}, "referenceImages": []any{"x"}, "assetImageTask": "old"}}
	character := model.Asset{ID: "a1", Title: "周岸", Category: model.AssetCategoryCharacter}
	version := &model.AssetVersion{ID: "v1", DefinitionJSON: `{"appearance":"条纹病号服","multiViewPrompt":"turnaround sheet"}`}
	req, err := dramaCharacterImageTaskRequest(project, style, character, version, template, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	metadata := req.Input["metadata"].(map[string]any)
	vars := metadata["promptTemplateVariables"].(map[string]string)
	if req.Type != "canvas_image" || metadata["operation"] != "character_turnaround" || metadata["characterAssetId"] != "a1" || metadata["nodeId"] != "character-turnaround:a1" || metadata["dramaAsset"] == nil {
		t.Fatalf("三视图任务：%+v", metadata)
	}
	if vars["角色设定"] != "条纹病号服；turnaround sheet" || !strings.Contains(vars["项目画风"], "【视觉媒介】真人实拍") || strings.Contains(vars["项目画风"], "长焦") || !strings.Contains(vars["项目画风"], "【全局禁用】霓虹") {
		t.Fatalf("三视图变量：%+v", vars)
	}
	if req.Input["referenceImages"] != nil || req.Input["assetImageTask"].(DramaImageTaskTemplate).Model != "gpt-image-2" || req.Input["config"].(map[string]any)["apiKey"] != "k" {
		t.Fatalf("三视图 input：%+v", req.Input)
	}
	if _, err := dramaCharacterImageTaskRequest(project, style, character, &model.AssetVersion{DefinitionJSON: `{}`}, template, ""); err == nil {
		t.Fatal("没有外观设定出不了三视图")
	}
	prop := model.Asset{ID: "a2", Title: "折叠刀", Category: model.AssetCategoryProp}
	req, err = dramaVisualImageTaskRequest(project, style, prop, &model.AssetVersion{ID: "v2", DefinitionJSON: `{"description":"黑柄折叠刀","prompt":"a folding knife"}`}, template, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	metadata = req.Input["metadata"].(map[string]any)
	if metadata["operation"] != "project_asset_image" || metadata["projectAssetVersionId"] != "v2" || !strings.HasPrefix(req.Prompt, "道具参考图：折叠刀\n\n黑柄折叠刀\n\na folding knife") || !strings.Contains(req.Prompt, "项目画风（必须遵循）") {
		t.Fatalf("概念图任务：%q %+v", req.Prompt, metadata)
	}
}

// 逐轮续写的文本任务跑起来以后（阶段已是「调用生成模型」）也能取消；别的任务照旧拦着。
func TestCancelDramaRunTaskWhileRunning(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, svc, db)
	task := queueAssetsExtractTask(t, svc, db, project, episodes[0].ID, "http://127.0.0.1:1", nil)
	plain := insertDramaTestTask(t, svc, db, project.ID, CreateTaskRequest{Type: "canvas_text", Operation: "storyboard", Prompt: "x", Input: map[string]any{"mode": "text", "metadata": map[string]any{}}})
	for _, id := range []string{task.ID, plain.ID} {
		if err := db.Model(&model.Task{}).Where("id = ?", id).Updates(map[string]any{"status": model.TaskStatusRunning, "stage": "调用生成模型"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	cancelled, err := svc.taskLifecycle().cancelTask(context.Background(), "user-1", task.ID)
	if err != nil || cancelled.Status != model.TaskStatusCancelled {
		t.Fatalf("逐轮续写的任务能取消：%v %+v", err, cancelled)
	}
	if _, err := svc.taskLifecycle().cancelTask(context.Background(), "user-1", plain.ID); err == nil || !strings.Contains(err.Error(), "不可取消") {
		t.Fatalf("普通任务照旧拦着：%v", err)
	}
}
