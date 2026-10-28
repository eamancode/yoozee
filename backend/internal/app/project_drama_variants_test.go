package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"image/color"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// seedScenePalettes 跑一遍场次色卡（假上游写第 1 集两场、第 2 集一场，代码合成色卡图），不接着提取资产。
func seedScenePalettes(t *testing.T, svc *Service, db *gorm.DB, project model.Project) {
	t.Helper()
	server := httptest.NewServer(scenePaletteUpstream())
	defer server.Close()
	queueScenePaletteTask(t, svc, db, project, server.URL, DramaProjectStepRunRequest{})
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("场次色卡：%v %s %q", err, done.Status, done.Error)
	}
}

// routedUpstream 按请求正文分给不同的假上游：场次色卡、第 N 集的资产设计各自续写自己的对话。
type routedUpstream struct {
	routes []struct {
		marker string
		up     http.Handler
	}
}

func (u *routedUpstream) route(marker string, up http.Handler) *routedUpstream {
	u.routes = append(u.routes, struct {
		marker string
		up     http.Handler
	}{marker, up})
	return u
}

func (u *routedUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(raw, &body)
	text := ""
	for _, m := range body.Messages {
		text += m.Content
	}
	for _, route := range u.routes {
		if strings.Contains(text, route.marker) {
			r.Body = io.NopCloser(bytes.NewReader(raw))
			route.up.ServeHTTP(w, r)
			return
		}
	}
	w.WriteHeader(http.StatusBadRequest)
}

const (
	variantLinMain = `{"category": "character", "name": "林小满", "aliases": [], "appearance": "二十五岁女护士，齐耳短发，护士蓝 #5B7FA3 护士服", "prompt_en": "character turnaround sheet, young nurse, Nurse Blue #5B7FA3 uniform", "first_scene": 1, "palette_hint": "护士蓝", "variants": []}`
	variantLinWet  = `{"category": "character", "name": "林小满", "aliases": [], "appearance": "", "prompt_en": "", "first_scene": 2, "palette_hint": "", "variants": [{"label": "淋湿", "kind": "state", "scenes": [2], "appearance": "护士服湿透贴身，发梢滴水", "prompt_en": "same nurse, soaking wet uniform, dripping hair"}]}`
	variantLinEp2  = `{"category": "character", "name": "林小满", "aliases": [], "appearance": "", "prompt_en": "", "first_scene": 1, "palette_hint": "", "variants": [{"label": "淋湿", "kind": "state", "scenes": [1], "appearance": "", "prompt_en": ""}, {"label": "西服", "kind": "costume", "scenes": [1], "appearance": "深灰西服套装", "prompt_en": "same woman in a dark grey suit"}]}`
)

// 还没有场次色卡时提取资产：400 palette_required；场次表接口标出这一集还没有色卡。做完场次色卡后两集都能提取。
func TestAssetsExtractRequiresScenePalette(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, svc, db)
	_, err := svc.planDramaStep("user-1", &project, episodes[0].ID, drama.StepAssetsExtract)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Reason != ReasonPaletteRequired || appErr.Status != http.StatusBadRequest || !strings.Contains(appErr.Message, "先生成场次色卡") {
		t.Fatalf("没有场次色卡：%v", err)
	}
	scenes, err := svc.DramaScenes("user-1", project.ID, episodes[0].ID)
	if err != nil || scenes.PaletteReady || scenes.Scenes[0].PaletteAssetID != "" {
		t.Fatalf("场次表标出没有色卡：%v %+v", err, scenes)
	}
	seedScenePalettes(t, svc, db, project)
	for _, ep := range episodes {
		current, _ := svc.repo.ProjectForUser("user-1", project.ID)
		if _, err := svc.planDramaStep("user-1", current, ep.ID, drama.StepAssetsExtract); err != nil {
			t.Fatalf("有了场次色卡就能提取：%v", err)
		}
		view, err := svc.DramaScenes("user-1", project.ID, ep.ID)
		if err != nil || !view.PaletteReady || view.Scenes[0].PaletteAssetID == "" {
			t.Fatalf("场次表带上色卡：%v %+v", err, view)
		}
	}
	current, _ := svc.repo.ProjectForUser("user-1", project.ID)
	plan, err := svc.planDramaStep("user-1", current, episodes[0].ID, drama.StepAssetsExtract)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"【主基调色卡】", "护士蓝 / Nurse Blue #5B7FA3", "【本集场次色卡】", "E01S02", "【用色要求】"} {
		if !strings.Contains(plan.prompt.Text, part) {
			t.Fatalf("提示词带色卡约束：缺 %q", part)
		}
	}
}

// 场次色卡写完后自动按集依次提取（第 1 集提完再起第 2 集）；林小满在第 1 集第 2 场淋湿、第 2 集换西服：
// 子卡「林小满·淋湿」「林小满·西服」继承主卡外貌，第 2 集再写淋湿只补场序不重建；场景资产按地点回填到各场。
func TestScenePaletteChainsAssetsExtractWithVariants(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, svc, db)
	palette := scenePaletteUpstream()
	ep1 := &streamingShotsUpstream{scenes: map[string][]string{"E01S01": {variantLinMain, assetsHall}, "E01S02": {variantLinWet, assetsLift}}}
	ep2 := &streamingShotsUpstream{scenes: map[string][]string{"E01S01": {variantLinEp2, assetsInLift}}}
	server := httptest.NewServer((&routedUpstream{}).route("剧本：第 1 集", ep1).route("剧本：第 2 集", ep2).route("场次色卡", palette))
	defer server.Close()

	step, _ := drama.StepByID(drama.StepScenePalette)
	current, _ := svc.repo.ProjectForUser("user-1", project.ID)
	plan, err := svc.planScenePalette(current, step, DramaProjectStepRunRequest{})
	if err != nil {
		t.Fatal(err)
	}
	plan.palette.ChainAssets = true
	taskReq := dramaStepTaskRequest(project.ID, "", plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": server.URL, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = newDramaRunMeta(plan, "")
	queued := insertDramaTestTask(t, svc, db, project.ID, taskReq)
	// 接着建的任务走 CreateTask：测试里没有计费用的渠道模型，关掉计费才建得成。
	if _, err := svc.UpdateFeatureAvailability(&model.User{ID: "admin", Role: "admin"}, FeatureAvailability{CustomChannelsEnabled: true, TaskCenterEnabled: true}); err != nil {
		t.Fatal(err)
	}
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("场次色卡：%v %q", err, done.Error)
	}
	chained := scenePaletteOutcomeOf(t, done).Chained
	if chained == nil || chained.TaskID == "" || chained.UnitID != episodes[0].ID || chained.Remaining != 1 {
		t.Fatalf("色卡写完接着提取第 1 集：%+v", chained)
	}
	first, err := svc.repo.Task(chained.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if am := taskDramaAssets(first.InputJSON); am == nil || am.Chain == nil || am.Chain.From != queued.ID || !slices.Equal(am.Chain.Queue, []string{episodes[1].ID}) || !strings.Contains(first.InputJSON, `"chainedFrom":"`+queued.ID+`"`) {
		t.Fatalf("第 1 集提取任务记下这一串：%s", first.InputJSON)
	}

	done1, err := runQueuedTask(t, svc)
	if err != nil || done1.ID != first.ID || done1.Status != model.TaskStatusSucceeded {
		t.Fatalf("第 1 集提取：%v %q", err, done1.Error)
	}
	out1 := assetsOutcomeOf(t, done1)
	if out1.Variants != 1 || out1.Created != 4 || out1.Scenes != 2 || out1.Chained == nil || out1.Chained.TaskID == "" || out1.Chained.UnitID != episodes[1].ID {
		t.Fatalf("第 1 集：主卡、子卡、走廊、电梯口，两场回填，接着第 2 集：%+v", out1)
	}
	assets := projectAssetsByTitle(t, svc, project.ID)
	lin, wet := assets["林小满"], assets["林小满·淋湿"]
	if wet.Variant == nil || wet.Variant.ParentID != lin.ID || wet.Variant.Label != "淋湿" || wet.Variant.Kind != drama.AssetVariantState || !slices.Equal(wet.Variant.Scenes, []string{"E01S02"}) {
		t.Fatalf("子卡指向主卡、记变体和场：%+v", wet.Variant)
	}
	def := wet.Character.Definition
	if def["appearance"] != lin.Character.Definition["appearance"] || !strings.Contains(def["clothing"].(string), "状态「淋湿」：护士服湿透贴身") || !strings.Contains(def["consistencyPrompt"].(string), "与主卡「林小满」是同一人") || def["multiViewPrompt"] != "same nurse, soaking wet uniform, dripping hair" {
		t.Fatalf("子卡继承主卡外貌、只换状态：%+v", def)
	}
	if wet.Image == nil || lin.Variant != nil {
		t.Fatalf("子卡记出图状态、主卡不是子卡：%+v %+v", wet.Image, lin.Variant)
	}

	done2, err := runQueuedTask(t, svc)
	if err != nil || done2.Status != model.TaskStatusSucceeded {
		t.Fatalf("第 2 集提取：%v %q", err, done2.Error)
	}
	if out2 := assetsOutcomeOf(t, done2); out2.Variants != 1 || out2.Chained != nil {
		t.Fatalf("第 2 集只新建西服子卡，是最后一集不再接：%+v", out2)
	}
	assets = projectAssetsByTitle(t, svc, project.ID)
	if again := assets["林小满·淋湿"]; again.ID != wet.ID || !slices.Equal(again.Variant.Scenes, []string{"E01S02", "E02S01"}) || again.VersionCount != wet.VersionCount {
		t.Fatalf("淋湿只补场序、不重建不加版本：%+v", again.Variant)
	}
	if suit := assets["林小满·西服"]; suit.Variant == nil || suit.Variant.Kind != drama.AssetVariantCostume || !strings.HasPrefix(suit.Character.Definition["clothing"].(string), "服装「西服」：深灰西服套装") {
		t.Fatalf("西服子卡：%+v", suit)
	}
	if assets["林小满"].VersionCount != lin.VersionCount {
		t.Fatal("主卡不因为子卡加版本")
	}

	// 场景回填：第 1 集两场按地点绑走廊、电梯口，第 2 集「电梯里」对上别名绑电梯轿厢；场次表接口返回。
	bound := projectSceneAssets(mustProjectBible(t, svc, "user-1", project.ID))
	if bound["E01S01"].Environments[0] != assets["病房走廊"].ID || bound["E01S02"].Environments[0] != assets["电梯口"].ID || bound["E02S01"].Environments[0] != assets["电梯轿厢"].ID || bound["E02S01"].UnitID != episodes[1].ID {
		t.Fatalf("场景回填：%+v", bound)
	}
	view, err := svc.DramaScenes("user-1", project.ID, episodes[0].ID)
	if err != nil || len(view.Scenes) != 2 || len(view.Scenes[1].Environments) != 1 || view.Scenes[1].Environments[0].Title != "电梯口" {
		t.Fatalf("场次表第 2 场返回电梯口：%v %+v", err, view)
	}

	// 资产页：子卡不单独占格，挂在主卡下面。
	page, err := svc.ProjectAssetsPage("user-1", project.ID, 1, 40, "character", "", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Assets) != 1 || page.Assets[0].ID != lin.ID || len(page.Assets[0].Variants) != 2 || page.Total != 1 || page.CategoryCounts["character"] != 1 {
		t.Fatalf("资产页主卡带两张子卡：%+v", page)
	}
}

// seedMasterPaletteAsset 给项目建一张有图的主基调色卡资产（风格锁定建的那种）。
func seedMasterPaletteAsset(t *testing.T, svc *Service, db *gorm.DB, projectID string) string {
	t.Helper()
	resource := uploadTestImage(t, svc, "user-1", color.White)
	now := time.Now()
	asset := model.Asset{ID: newID(), UserID: "user-1", Kind: "image", Category: model.AssetCategoryPalette, Status: model.AssetVersionStatusConfirmed, Title: "主基调色卡", CreatedAt: now, UpdatedAt: now,
		PayloadJSON: `{"metadata":{"masterPalette":{"kind":"master","source":"library"}}}`}
	version := model.AssetVersion{ID: newID(), AssetID: asset.ID, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: `{"kind":"master"}`, CreatedAt: now, UpdatedAt: now}
	asset.PrimaryVersionID = version.ID
	rep := model.AssetRepresentation{ID: newID(), AssetVersionID: version.ID, ResourceID: resource, MediaType: "image/png", Role: "primary", CreatedAt: now}
	link := model.ProjectAssetLink{ID: newID(), ProjectID: projectID, AssetID: asset.ID, CreatedAt: now}
	for _, row := range []any{&asset, &version, &rep, &link} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return resource
}

// 出图参考图：子卡带主卡三视图（最前）和主基调色卡；场景带本场色卡和主基调色卡；道具带主基调色卡。名额不够时色卡先让出
// （主卡三视图不让），让出的色卡把色值写进提示词；降级档去掉色卡图。
func TestDramaAssetImageRefs(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, svc, db)
	lin := seedLinXiaoman(t, svc, db, project.ID)
	seedScenePalettes(t, svc, db, project)
	master := seedMasterPaletteAsset(t, svc, db, project.ID)
	created, err := svc.CreateDramaAssetVariant("user-1", project.ID, lin.Asset.ID, DramaAssetVariantRequest{Label: "淋湿", Look: "护士服湿透", Scenes: []string{"e01s02"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.Asset.Title != "林小满·淋湿" || created.Asset.Variant.Kind != drama.AssetVariantState || !slices.Equal(created.Asset.Variant.Scenes, []string{"E01S02"}) {
		t.Fatalf("手动新增子卡：%+v", created.Asset)
	}
	current, _ := svc.repo.ProjectForUser("user-1", project.ID)
	refsOf := func(assetID string) []dramaImageRef {
		t.Helper()
		asset, err := svc.repo.AssetForUser("user-1", assetID)
		if err != nil {
			t.Fatal(err)
		}
		version, _ := svc.repo.AssetVersion(asset.PrimaryVersionID)
		definition := map[string]any{}
		_ = json.Unmarshal([]byte(version.DefinitionJSON), &definition)
		refs, err := svc.dramaAssetImageRefs("user-1", current, *asset, definition)
		if err != nil {
			t.Fatal(err)
		}
		return refs
	}
	child := refsOf(created.Asset.ID)
	if len(child) != 2 || child[0].ResourceID != "lin-sheet" || child[0].Palette || !strings.Contains(child[0].Label, "主卡「林小满」") || child[1].ResourceID != master || !child[1].Palette {
		t.Fatalf("子卡参考图：主卡三视图、主基调色卡：%+v", child)
	}
	if main := refsOf(lin.Asset.ID); len(main) != 1 || main[0].ResourceID != master {
		t.Fatalf("主卡只带主基调色卡：%+v", main)
	}

	// 场景：回填到 E01S02 的电梯口带这一场的色卡图。
	palettes := paletteSummaries(t, svc, project.ID)
	scenePaletteResource, _ := svc.versionImageResource(palettes["E01S02"].PrimaryVersionID, "primary")
	env := model.Asset{ID: newID(), UserID: "user-1", Kind: "entity", Category: model.AssetCategoryEnvironment, Status: model.AssetVersionStatusConfirmed, Title: "电梯口", PayloadJSON: `{}`}
	if err := db.Create(&env).Error; err != nil {
		t.Fatal(err)
	}
	bible, _ := mergeSceneAssetsDocument(current.BibleJSON, episodes[0].ID, 1, map[string]dramaSceneAssets{"E01S02": {UnitID: episodes[0].ID, Environments: []string{env.ID}}})
	current.BibleJSON = bible
	refs, err := svc.dramaAssetImageRefs("user-1", current, env, map[string]any{})
	if err != nil || len(refs) != 2 || refs[0].ResourceID != scenePaletteResource || !strings.Contains(refs[0].Label, "本场色卡 E01S02") || refs[1].ResourceID != master {
		t.Fatalf("场景参考图：本场色卡、主基调色卡：%v %+v", err, refs)
	}

	// 名额 1：场景只带本场色卡，主基调色卡让出、色值写进提示词；子卡只带主卡三视图。
	input := map[string]any{}
	one := 1
	note, err := svc.withDramaImageRefs("user-1", input, refs, DramaImageTaskTemplate{MaxReferenceImages: &one}, dramaRefsAll)
	if err != nil {
		t.Fatal(err)
	}
	images := input["referenceImages"].([]any)
	if len(images) != 1 || images[0].(map[string]any)["storageKey"] != "resource:"+scenePaletteResource || !strings.Contains(note, "参考图 1：本场色卡 E01S02") || !strings.Contains(note, "主基调色卡（参考图名额不够，没带图）色值：护士蓝 #5B7FA3") {
		t.Fatalf("名额不够色卡让出：%+v\n%s", images, note)
	}
	input = map[string]any{}
	note, _ = svc.withDramaImageRefs("user-1", input, child, DramaImageTaskTemplate{MaxReferenceImages: &one}, dramaRefsAll)
	if images := input["referenceImages"].([]any); len(images) != 1 || images[0].(map[string]any)["storageKey"] != "resource:lin-sheet" {
		t.Fatalf("色卡不挤掉主卡三视图：%+v", images)
	}
	input = map[string]any{}
	note, _ = svc.withDramaImageRefs("user-1", input, child, DramaImageTaskTemplate{}, dramaRefsNoPalette)
	if images := input["referenceImages"].([]any); len(images) != 1 || !strings.Contains(note, "主基调色卡（参考图名额不够") {
		t.Fatalf("降级去掉色卡图：%+v %s", images, note)
	}
	input = map[string]any{}
	_, _ = svc.withDramaImageRefs("user-1", input, child, DramaImageTaskTemplate{}, dramaRefsNone)
	if input["referenceImages"] != nil {
		t.Fatal("最后一档不带参考图")
	}

	// 子卡的三视图任务：角色设定里写明参考图，input 带主卡三视图和主色卡。
	childAsset, _ := svc.repo.AssetForUser("user-1", created.Asset.ID)
	version, _ := svc.repo.AssetVersion(childAsset.PrimaryVersionID)
	req, err := svc.dramaCharacterImageTaskRequest("user-1", current, projectStyleSnapshot(current), *childAsset, version, DramaImageTaskTemplate{Model: "img"}, "test", child, dramaRefsAll)
	if err != nil {
		t.Fatal(err)
	}
	vars := req.Input["metadata"].(map[string]any)["promptTemplateVariables"].(map[string]string)
	if len(req.Input["referenceImages"].([]any)) != 2 || !strings.Contains(vars["角色设定"], "参考图 1：主卡「林小满」已确认的三视图") || !strings.Contains(vars["角色设定"], "与主卡「林小满」是同一人") {
		t.Fatalf("子卡三视图任务：%+v", vars)
	}
}

// 资产页改、删子卡：改外观加一版、旧三视图不搬；被镜头引用的子卡不能删（列出镜头），解除引用后能删（移出项目）。
// 子卡换版本只让引用子卡的镜头产物过期，引用主卡的不动。
func TestDramaAssetVariantEditDeleteAndFollow(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, svc, db)
	lin := seedLinXiaoman(t, svc, db, project.ID)
	created, err := svc.CreateDramaAssetVariant("user-1", project.ID, lin.Asset.ID, DramaAssetVariantRequest{Label: "西服", Kind: "costume", Look: "深灰西服"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateDramaAssetVariant("user-1", project.ID, lin.Asset.ID, DramaAssetVariantRequest{Label: "西服", Look: "x"}); err == nil {
		t.Fatal("同主卡同变体名不能再建")
	}
	if _, err := svc.CreateDramaAssetVariant("user-1", project.ID, created.Asset.ID, DramaAssetVariantRequest{Label: "x", Look: "x"}); err == nil {
		t.Fatal("子卡下不能再建子卡")
	}
	if _, err := svc.ReplaceProjectCharacterRepresentations("user-1", project.ID, created.Asset.ID, ReplaceCharacterRepresentationsRequest{Representations: []CharacterRepresentationInput{{Role: "turnaround_sheet", ResourceID: "lin-sheet"}}}); err != nil {
		t.Fatal(err)
	}
	child, _ := svc.repo.AssetForUser("user-1", created.Asset.ID)
	if meta := assetVariantMetaOf(child); meta.Kind != drama.AssetVariantCostume || child.ParentAssetID != lin.Asset.ID {
		t.Fatalf("换定妆加版本后子卡标记还在：%+v %s", meta, child.ParentAssetID)
	}
	childShot := model.Shot{ID: newID(), ProjectID: project.ID, UnitID: episodes[0].ID, Title: "E01S02-01", Description: "段", Status: "draft"}
	mainShot := model.Shot{ID: newID(), ProjectID: project.ID, UnitID: episodes[0].ID, Title: "E01S01-01", Description: "段", Status: "draft", Position: 1}
	for _, shot := range []*model.Shot{&childShot, &mainShot} {
		if err := db.Create(shot).Error; err != nil {
			t.Fatal(err)
		}
	}
	mainAsset, _ := svc.repo.AssetForUser("user-1", lin.Asset.ID)
	if _, err := svc.LinkShotAsset("user-1", project.ID, childShot.ID, LinkShotAssetRequest{AssetVersionID: child.PrimaryVersionID, Role: "reference"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.LinkShotAsset("user-1", project.ID, mainShot.ID, LinkShotAssetRequest{AssetVersionID: mainAsset.PrimaryVersionID, Role: "reference"}); err != nil {
		t.Fatal(err)
	}
	for _, shot := range []model.Shot{childShot, mainShot} {
		if err := db.Create(&model.ShotArtifact{ID: newID(), ProjectID: project.ID, UnitID: episodes[0].ID, ShotID: shot.ID, Type: "video", Version: 1, Status: "ready", Selected: true}).Error; err != nil {
			t.Fatal(err)
		}
	}

	updated, err := svc.UpdateDramaAssetVariant("user-1", project.ID, child.ID, DramaAssetVariantRequest{Label: "正装", Kind: "costume", Look: "黑色正装", Scenes: []string{"E02S01"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Asset.Title != "林小满·正装" || updated.Asset.Variant.Label != "正装" || !slices.Equal(updated.Asset.Variant.Scenes, []string{"E02S01"}) || updated.Asset.Character.VisualStatus == "ready" || updated.Asset.VersionCount != 3 {
		t.Fatalf("改子卡：改名、改场、外观变了加一版且旧图不搬：%+v %+v", updated.Asset, updated.Asset.Character)
	}
	if refs := shotReferencesByRole(t, db, childShot.ID, "reference"); len(refs) != 1 || refs[0].AssetVersionID != updated.Asset.PrimaryVersionID {
		t.Fatalf("引用子卡的镜头跟到新版本：%+v", refs)
	}
	var artifacts []model.ShotArtifact
	if err := db.Where("project_id = ?", project.ID).Find(&artifacts).Error; err != nil {
		t.Fatal(err)
	}
	for _, a := range artifacts {
		if (a.ShotID == childShot.ID) != (a.Status == "stale") {
			t.Fatalf("只有引用子卡的镜头产物过期：%s %s", a.ShotID, a.Status)
		}
	}

	err = svc.DeleteDramaAssetVariant("user-1", project.ID, child.ID)
	if err == nil || !strings.Contains(err.Error(), "还被镜头引用：E01S02-01") {
		t.Fatalf("被引用的子卡不能删：%v", err)
	}
	if err := db.Where("shot_id = ?", childShot.ID).Delete(&model.ShotAssetReference{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDramaAssetVariant("user-1", project.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDramaAssetVariant("user-1", project.ID, lin.Asset.ID); err == nil {
		t.Fatal("主卡不走删子卡")
	}
	if _, ok := projectAssetsByTitle(t, svc, project.ID)["林小满·正装"]; ok {
		t.Fatal("删掉的子卡移出项目")
	}
}

// 装段按场换资产：第 2 场有子卡时，这一场绑主卡的行换成子卡、refs_hint 的主卡名换成「名·变体」，没绑场景的行补上本场的场景；
// 第 1 场不动。圣经里子卡并进主卡那个人物当变体（文件是子卡的版本），refs_hint「林小满·淋湿」取到子卡的图。
func TestApplySceneCastAndBible(t *testing.T) {
	assets := []ProjectAssetSummary{
		{ID: "lin", Title: "林小满", Category: model.AssetCategoryCharacter, PrimaryVersionID: "lin-v", Character: &CharacterCardSummary{VersionID: "lin-v"}},
		{ID: "wet", Title: "林小满·淋湿", Category: model.AssetCategoryCharacter, PrimaryVersionID: "wet-v", Character: &CharacterCardSummary{VersionID: "wet-v"}, Variant: &ProjectAssetVariantSummary{ParentID: "lin", ParentTitle: "林小满", Label: "淋湿", Kind: drama.AssetVariantState, Scenes: []string{"E02S02"}}},
		{ID: "lift", Title: "电梯口", Category: model.AssetCategoryEnvironment, PrimaryVersionID: "lift-v"},
	}
	units := []model.ProjectUnit{{ID: "ep1", Kind: model.ProjectUnitKindEpisode, Position: 0}, {ID: "ep2", Kind: model.ProjectUnitKindEpisode, Position: 1}}
	bible := `{"scene_assets":{"E02S02":{"unit_id":"ep2","environments":["lift"]},"E01S02":{"unit_id":"ep1","environments":["lift"]}}}`
	cast := sceneCastOf(bible, units, "ep2", assets)
	if cast.Variants["E01S02"]["lin"] != "wet" || len(cast.Environments["E01S02"]) != 1 || len(cast.Variants["E01S01"]) != 0 {
		t.Fatalf("第 2 集第 2 场：子卡、电梯口：%+v", cast)
	}
	if prompts := sceneCastPrompts(cast); !strings.Contains(prompts["E01S02"], "林小满 用「林小满·淋湿」") || prompts["E01S01"] != "" {
		t.Fatalf("本场角色子卡提示：%+v", prompts)
	}
	row := func(id string) shotlistRow {
		var r shotlistRow
		r.AssetBindings = []drama.RowAssetBinding{{NodeID: id, Role: "character", Priority: 100}}
		r.Characters = append(r.Characters, struct {
			CharacterName    string `json:"characterName"`
			CharacterAssetID string `json:"characterAssetId"`
		}{"林小满", id})
		return r
	}
	rows := []shotlistRow{row("lin"), row("lin")}
	shots := []drama.Shot{{ID: "E01S01-01", Scene: "E01S01", Focus: "林小满", RefsHint: []string{"林小满"}}, {ID: "E01S02-01", Scene: "E01S02", Focus: "林小满", RefsHint: []string{"林小满"}}}
	applySceneCast(rows, shots, cast)
	if rows[0].AssetBindings[0].NodeID != "lin" || len(rows[0].AssetBindings) != 1 || shots[0].RefsHint[0] != "林小满" {
		t.Fatalf("第 1 场不动：%+v %+v", rows[0], shots[0])
	}
	if rows[1].AssetBindings[0].NodeID != "wet" || rows[1].Characters[0].CharacterAssetID != "wet" || rows[1].AssetBindings[1].NodeID != "lift" || rows[1].AssetBindings[1].Role != "environment" || shots[1].RefsHint[0] != "林小满·淋湿" {
		t.Fatalf("第 2 场换子卡、补场景：%+v %+v", rows[1], shots[1])
	}
	attachRefsHints(shots, rows, assets, nil)
	if !slices.Equal(shots[1].RefsHint, []string{"林小满·淋湿", "电梯口"}) {
		t.Fatalf("refs_hint 不重复挂子卡：%+v", shots[1].RefsHint)
	}
	bibleOut := dramaShotlistBible(shots, rows, assets, map[string]bool{"lin": true, "wet": true, "lift": true}, 6)
	if len(bibleOut.Characters) != 1 || bibleOut.Characters[0].Name != "林小满" {
		t.Fatalf("子卡并进主卡那个人物：%+v", bibleOut.Characters)
	}
	var labels []string
	for _, v := range bibleOut.Characters[0].Variants {
		labels = append(labels, v.Label+"="+v.Sheet)
	}
	if !slices.Equal(labels, []string{"=lin-v", "淋湿=wet-v"}) {
		t.Fatalf("主卡是 base、子卡是变体：%v", labels)
	}
}

// 七床第一集真跑生成分段：林小满在第 2 场（电梯口）有子卡「林小满·淋湿」（有图），第 2 场那一轮提示词写明用子卡，
// 装段后第 2 场的段引用子卡的版本，第 1 场的段不引用；第 2 场的段补上回填到这一场的场景资产。
func TestChapterShotsBindsSceneVariant(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	seedQichuangStyle(t, svc, project, unit)
	assets := projectAssetsByTitle(t, svc, project.ID)
	created, err := svc.CreateDramaAssetVariant("user-1", project.ID, assets["林小满"].ID, DramaAssetVariantRequest{Label: "淋湿", Look: "护士服湿透贴身", Scenes: []string{"E01S02"}})
	if err != nil {
		t.Fatal(err)
	}
	sheet := uploadTestImage(t, svc, "user-1", color.White)
	if _, err := svc.ReplaceProjectCharacterRepresentations("user-1", project.ID, created.Asset.ID, ReplaceCharacterRepresentationsRequest{Representations: []CharacterRepresentationInput{{Role: "turnaround_sheet", ResourceID: sheet}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BindProjectAssetImage("user-1", project.ID, assets["电梯口"].ID, BindProjectAssetImageRequest{ResourceID: uploadTestImage(t, svc, "user-1", color.Black), ExpectedVersionID: assets["电梯口"].PrimaryVersionID}); err != nil {
		t.Fatal(err)
	}
	current, _ := svc.repo.ProjectForUser("user-1", project.ID)
	bible, err := mergeSceneAssetsDocument(current.BibleJSON, unit.ID, 1, map[string]dramaSceneAssets{"E01S02": {UnitID: unit.ID, Environments: []string{assets["电梯口"].ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("bible_json", bible).Error; err != nil {
		t.Fatal(err)
	}
	current, _ = svc.repo.ProjectForUser("user-1", project.ID)
	upstream := &streamingShotsUpstream{scenes: qichuangSceneRows(t), genres: qichuangGenreReply}
	server := httptest.NewServer(upstream)
	defer server.Close()
	queueDramaStepTask(t, svc, db, *current, unit, server.URL, "")
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("生成分段：%v %s %q", err, done.Status, done.Error)
	}
	if len(upstream.prompts) != 2 || strings.Contains(upstream.prompts[0], "【本场角色子卡】这一场") || !strings.Contains(upstream.prompts[0], "- 林小满·淋湿（角色子卡）：林小满的状态「淋湿」") || !strings.Contains(upstream.prompts[1], "【本场角色子卡】这一场林小满 用「林小满·淋湿」") {
		t.Fatalf("只有第 2 场的提示词写明用子卡：%d %.600s", len(upstream.prompts), upstream.prompts[len(upstream.prompts)-1])
	}
	child, _ := svc.repo.AssetForUser("user-1", created.Asset.ID)
	lift, _ := svc.repo.AssetForUser("user-1", assets["电梯口"].ID)
	var shots []model.Shot
	if err := db.Where("project_id = ? AND unit_id = ?", project.ID, unit.ID).Find(&shots).Error; err != nil {
		t.Fatal(err)
	}
	scene2Palette := paletteSummaries(t, svc, project.ID)["E01S02"].PrimaryVersionID
	scene2 := 0
	for _, shot := range shots {
		keys := strings.Join(referenceKeys(t, db, shot.ID), ",")
		// 段按场装，带本场色卡（role=style）：带第 2 场色卡的就是第 2 场的段。
		inScene2 := strings.Contains(keys, scene2Palette+"|style")
		if inScene2 {
			scene2++
		}
		if strings.Contains(keys, child.PrimaryVersionID) != inScene2 {
			t.Fatalf("段 %s 引用子卡与否：%s", shot.Title, keys)
		}
		if inScene2 && !strings.Contains(keys, lift.PrimaryVersionID) {
			t.Fatalf("第 2 场的段 %s 绑电梯口：%s", shot.Title, keys)
		}
	}
	if scene2 == 0 {
		t.Fatalf("没有第 2 场的段：%+v", shots)
	}
}
