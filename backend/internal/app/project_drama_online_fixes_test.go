package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// 线上复测（M4.1 第三期）：单场参考、场景锚点、每集时长、项目画风。

// on3RawRows 读七床 on3 的分镜行原文，edit 可以先改再解析。
func on3RawRows(t *testing.T, edit func(rows []map[string]any)) []shotlistRow {
	t.Helper()
	var doc struct {
		Rows []map[string]any `json:"rows"`
	}
	raw, err := os.ReadFile("../drama/testdata/online-qichuang-on3/rows.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(doc.Rows)
	}
	encoded := make([]json.RawMessage, 0, len(doc.Rows))
	for _, r := range doc.Rows {
		b, _ := json.Marshal(r)
		encoded = append(encoded, b)
	}
	rows, err := parseShotlistRows(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func segmentLocations(seg drama.Segment) []string {
	var out []string
	for _, r := range seg.Refs {
		if r.Role == "location" || r.Role == "plate" {
			out = append(out, r.File)
		}
	}
	return out
}

// 线上 S02（第二场「内·电梯口·夜」）整段都在电梯口，段首镜头的 assets 里写了病房走廊，段就多挂了走廊参考。
// 段的场景参考只取本段 spec.scene 对应的场景资产：同一镜写了两个场景时以 spec.scene 为准。
func TestShotlistSegmentKeepsOnlyItsSceneLocation(t *testing.T) {
	rows := on3RawRows(t, func(rows []map[string]any) {
		for _, r := range rows {
			spec := r["spec"].(map[string]any)
			if spec["id"] != "E01S02-01" {
				continue
			}
			r["assets"] = append(r["assets"].([]any), "病房走廊（夜）")
			r["assetBindings"] = append(r["assetBindings"].([]any), map[string]any{"nodeId": "loc-1", "role": "environment", "priority": 50})
			spec["refs_hint"] = append(spec["refs_hint"].([]any), "病房走廊（夜）")
		}
	})
	_, assets, script := loadOn3(t)
	imaged := map[string]bool{}
	for _, a := range assets {
		imaged[a.ID] = true
	}
	want := map[string]string{"E01S01": "loc-1-v1", "E01S02": "loc-2-v1", "E01S03": "loc-2-v1"}
	for _, modelID := range []string{"seedance-2.5", "seedance-2.0"} {
		m, _ := drama.ModelByID(modelID)
		plan, err := planDramaShotlist(rows, &script, assets, imaged, m, nil, dramaProjectContext{})
		if err != nil {
			t.Fatal(err)
		}
		firstOfScene := map[string]bool{}
		for _, seg := range plan.Segments {
			locs := segmentLocations(seg)
			if len(locs) != 1 || locs[0] != want[seg.Scene] {
				t.Fatalf("%s %s（%s）只应有本场的场景参考 %s：%v", modelID, seg.ID, seg.Scene, want[seg.Scene], locs)
			}
			if seg.ContinuityBridge != nil {
				for _, f := range seg.ContinuityBridge.SharedRefs {
					if strings.HasPrefix(f, "loc-") && f != want[seg.Scene] {
						t.Fatalf("%s %s 的一致性桥也不带别场的场景：%v", modelID, seg.ID, seg.ContinuityBridge.SharedRefs)
					}
				}
			}
			if seg.Scene == "E01S02" && !firstOfScene[seg.Scene] {
				firstOfScene[seg.Scene] = true
				if seg.ShotIDs[0] != "E01S02-01" {
					t.Fatalf("夹具前提：第二场的第一段从 E01S02-01 开始：%v", seg.ShotIDs)
				}
			}
		}
		if !firstOfScene["E01S02"] {
			t.Fatalf("%s 没有装出第二场的段", modelID)
		}
	}
}

func withSceneAnchors(assets []ProjectAssetSummary, title string, anchors []any) []ProjectAssetSummary {
	out := make([]ProjectAssetSummary, len(assets))
	copy(out, assets)
	for i := range out {
		if out[i].Title == title {
			out[i].Visual = &ProjectAssetVisualSummary{Definition: map[string]any{"prompt": out[i].PreviewText, "anchors": anchors}, Status: "missing"}
		}
	}
	return out
}

// 场景资产的 anchors 当这一场的空间图：blocking.at 不在锚点里报 R-SHOT-012（warn，不拦保存）。
func TestShotlistSceneAnchorsFeedBlockingCheck(t *testing.T) {
	rows, assets, script := loadOn3(t)
	m, _ := drama.ModelByID("seedance-2.5")
	countR012 := func(plan dramaShotlistPlan) (int, []string) {
		n := 0
		var shots []string
		for _, item := range plan.Report.Items {
			if item.Rule == "R-SHOT-012" {
				n++
				shots = append(shots, item.ShotID)
			}
		}
		return n, shots
	}
	plain, err := planDramaShotlist(rows, &script, assets, map[string]bool{}, m, nil, dramaProjectContext{})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := countR012(plain); n != 0 {
		t.Fatalf("场景没写锚点时没有空间图，R-SHOT-012 不触发：%d", n)
	}
	anchored := withSceneAnchors(assets, "电梯口", []any{"电梯门", "楼梯间防火门", "灭火器箱"})
	plan, err := planDramaShotlist(rows, &script, anchored, map[string]bool{}, m, nil, dramaProjectContext{})
	if err != nil {
		t.Fatalf("R-SHOT-012 是 warn，不拦保存：%v", err)
	}
	n, shots := countR012(plan)
	if n == 0 {
		t.Fatal("电梯口写了锚点后，站位用了别的地方（如「走廊中段」）应报 R-SHOT-012")
	}
	for _, id := range shots {
		if !strings.HasPrefix(id, "E01S02-") && !strings.HasPrefix(id, "E01S03-") {
			t.Fatalf("只查标题对上电梯口的两场：%v", shots)
		}
	}
	if script.Scenes[1].LocationRef != "" {
		t.Fatal("检查用的是剧本副本，原剧本不动")
	}
}

// 每集时长（brief_json.episode_seconds）进 R-SEG-008：分段合计超过 N×1.1 提醒并镜，不足 60% 提醒戏不够。
func TestShotlistSegmentTotalUsesEpisodeSeconds(t *testing.T) {
	rows, assets, script := loadOn3(t)
	m, _ := drama.ModelByID("seedance-2.5")
	r008 := func(seconds float64) []drama.CheckItem {
		pc := dramaProjectContext{Brief: drama.Brief{EpisodeSeconds: &seconds}}
		plan, err := planDramaShotlist(rows, &script, assets, map[string]bool{}, m, nil, pc)
		if err != nil {
			t.Fatal(err)
		}
		var out []drama.CheckItem
		for _, item := range plan.Report.Items {
			if item.Rule == "R-SEG-008" {
				out = append(out, item)
			}
		}
		return out
	}
	if items := r008(90); len(items) != 1 || items[0].Level != "warn" || !strings.Contains(items[0].Message, "超过目标片长 90 秒") {
		t.Fatalf("七床 on3 第一章 2.5 装段 123 秒，超过 90×1.1：%+v", items)
	}
	if items := r008(120); len(items) != 0 {
		t.Fatalf("120 秒目标在余量内：%+v", items)
	}
	if items := r008(240); len(items) != 1 || !strings.Contains(items[0].Message, "不足目标片长 240 秒的 60%") {
		t.Fatalf("不足 60%% 提醒：%+v", items)
	}
}

const testSuspenseStyleJSON = `{"schemaVersion":1,"presetId":"v2-suspense--dark--live-action--realistic","title":"悬疑犯罪 · 暗黑诡谲 · 真人实拍","tags":["悬疑犯罪","暗黑诡谲","真人实拍","写实人物"],` +
	`"selection":{"world":"suspense","tone":"dark","medium":"live-action","character":"realistic"},"assets":[],"executionPolicy":"compatible-fallback","source":"builtin","revision":1,` +
	`"prompt":"【风格组合】题材世界：悬疑犯罪；叙事气质：暗黑诡谲；视觉媒介：真人实拍；角色造型：写实人物。\n【叙事气质】叙事气质危险、克制、带未知感；恐惧来自空间、规则和人物反应。\n【视觉媒介】采用真人影视拍摄媒介：真实人物比例、自然皮肤与发丝、可信镜头光学和现场光源。\n【项目色彩与光影】基础色彩来自炭灰、冷水泥、旧木、脏黄实景灯与少量危险强调色。\n【全局禁用】黑成不可读、霓虹灯海。"}`

func TestProjectDramaContextMapsStyleToProfile(t *testing.T) {
	pc := projectDramaContext(&model.Project{StyleProfileJSON: testSuspenseStyleJSON})
	if pc.profileID() != "live-realism" || pc.ProfileRule == "" || !strings.Contains(pc.StyleSpec, "【视觉媒介】") {
		t.Fatalf("悬疑犯罪·暗黑诡谲·真人实拍 → live-realism，执行规范进风格段：%+v", pc)
	}
	if pc := projectDramaContext(&model.Project{StylePresetID: "v2-xianxia--epic--3d-anime--semi-real"}); pc.profileID() != "anime-3d" || pc.StyleSpec != "" || !pc.HasStyle {
		t.Fatalf("只有预设 ID 的老项目按 ID 里的媒介对照，风格段仍用档案兜底：%+v", pc)
	}
	pc = projectDramaContext(&model.Project{StyleProfileJSON: testSuspenseStyleJSON, BriefJSON: `{"style_profile":"duanju-shiga","episode_seconds":90}`})
	if pc.profileID() != "duanju-shiga" || pc.targetSeconds() != 90 {
		t.Fatalf("需求文档手动指定档案优先，每集时长读 episode_seconds：%+v", pc)
	}
	if pc := projectDramaContext(&model.Project{}); pc.profileID() != drama.DefaultProfileID || pc.HasStyle || projectStyleOverride(nil, pc) != nil {
		t.Fatalf("没选画风：默认档案、风格段用档案 style_block：%+v", pc)
	}
}

// 项目选了画风：镜头表按对照出的档案装段，视频提示词的风格段是画风执行规范，不再是档案 style_block。
func TestCompileProjectShotUsesProjectStyle(t *testing.T) {
	service, db, project, unit := newProjectDramaTestService(t, "seedance-2.5")
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Updates(map[string]any{"style_profile_json": testSuspenseStyleJSON, "brief_json": `{"title":"夜班","episode_seconds":60}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ProjectUnit{}).Where("id = ?", unit.ID).Update("source_text", chapterHTML(t)).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&model.Asset{ID: "char-1", UserID: "user-1", Kind: "image", Category: model.AssetCategoryCharacter, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: "char-1-v1", Title: "林小满", PayloadJSON: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AssetVersion{ID: "char-1-v1", AssetID: "char-1", Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ProjectAssetLink{ID: newID(), ProjectID: project.ID, AssetID: "char-1", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := service.WriteDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: loadOnlineRowsRaw(t), LegacyScenes: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Segments[0].Spec.Profile != "live-realism" || !strings.HasPrefix(result.Profile, "真人剧") || !strings.Contains(result.Profile, "真人实拍") {
		t.Fatalf("按画风对照的档案装段，结果写明档案和来由：%s %q", result.Segments[0].Spec.Profile, result.Profile)
	}
	hasR008 := false
	for _, item := range result.Check.Items {
		hasR008 = hasR008 || item.Rule == "R-SEG-008"
	}
	if !hasR008 {
		t.Fatalf("每集 60 秒，分段合计超过 66 秒应报 R-SEG-008：%+v", result.Check.Items)
	}
	compiled, err := service.CompileProjectShot("user-1", project.ID, result.Segments[0].ShotID, CompileProjectShotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.SplitN(compiled.Result.Prompt, "\n", 2)[0]
	if !strings.Contains(head, "整体风格：采用真人影视拍摄媒介") || !strings.Contains(head, "炭灰、冷水泥") || strings.Contains(head, "灯光只用场景内实际存在的光源") || strings.Contains(head, "霓虹灯海") {
		t.Fatalf("风格段应是项目画风执行规范（视觉媒介、叙事气质、色彩与光影）：\n%s", head)
	}
	override := "胶片质感。"
	custom, err := service.CompileProjectShot("user-1", project.ID, result.Segments[0].ShotID, CompileProjectShotRequest{Style: &override})
	if err != nil || !strings.Contains(custom.Result.Prompt, "整体风格：胶片质感。") {
		t.Fatalf("请求里给了风格块仍以请求为准：%v", err)
	}
}

// 资产页编辑场景锚点：原地写主版本设定的 anchors（不建新版本，镜头引用的版本不变），只有场景能写。
func TestUpdateProjectAssetSceneAnchors(t *testing.T) {
	service, db, project, _ := newProjectDramaTestService(t, "seedance-2.5")
	now := time.Now()
	for _, a := range []struct {
		id, title string
		category  model.AssetCategory
	}{{"loc-2", "电梯口", model.AssetCategoryEnvironment}, {"char-1", "林小满", model.AssetCategoryCharacter}} {
		if err := db.Create(&model.Asset{ID: a.id, UserID: "user-1", Kind: "entity", Category: a.category, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: a.id + "-v1", Title: a.title, PayloadJSON: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.AssetVersion{ID: a.id + "-v1", AssetID: a.id, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: `{"name":"` + a.title + `","prompt":"不锈钢电梯门"}`, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ProjectAssetLink{ID: newID(), ProjectID: project.ID, AssetID: a.id, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	anchors := []string{" 电梯门前 ", "楼梯间防火门", "电梯门前", ""}
	summary, err := service.UpdateProjectAsset("user-1", project.ID, "loc-2", UpdateProjectAssetRequest{Anchors: &anchors})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Visual == nil || strings.Join(sceneAnchorList(summary.Visual.Definition["anchors"]), "、") != "电梯门前、楼梯间防火门" || summary.Visual.Definition["prompt"] != "不锈钢电梯门" {
		t.Fatalf("锚点去空去重后写进设定，其余设定不动：%+v", summary.Visual)
	}
	if summary.PrimaryVersionID != "loc-2-v1" || summary.VersionCount != 1 {
		t.Fatalf("不建新版本：%s v%d", summary.PrimaryVersionID, summary.VersionCount)
	}
	if got := dramaSceneAnchors([]ProjectAssetSummary{summary})["电梯口"]; len(got) != 2 {
		t.Fatalf("镜头表检查读得到锚点：%v", got)
	}
	empty := []string{}
	if summary, err = service.UpdateProjectAsset("user-1", project.ID, "loc-2", UpdateProjectAssetRequest{Anchors: &empty}); err != nil || summary.Visual.Definition["anchors"] != nil {
		t.Fatalf("清空锚点：%v %+v", err, summary.Visual)
	}
	if _, err := service.UpdateProjectAsset("user-1", project.ID, "char-1", UpdateProjectAssetRequest{Anchors: &anchors}); err == nil {
		t.Fatal("人物不能写场景锚点")
	}
	category := string(model.AssetCategoryProp)
	if _, err := service.UpdateProjectAsset("user-1", project.ID, "loc-2", UpdateProjectAssetRequest{Anchors: &anchors, Category: &category}); err == nil {
		t.Fatal("一次只改一项")
	}
	if _, err := service.UpdateProjectAsset("user-2", project.ID, "loc-2", UpdateProjectAssetRequest{Anchors: &anchors}); err == nil {
		t.Fatal("别人的项目不能改")
	}
}
