package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

const onlineQichuangDir = "../drama/testdata/online-qichuang"

func TestChapterPlainTextKeepsOneLinePerParagraph(t *testing.T) {
	got := chapterPlainText("<h2>内·病房走廊·夜</h2><p>（她推门。）</p><p>林小满：七床&nbsp;的人呢？<br>周岸：别喊。</p>")
	want := "内·病房走廊·夜\n（她推门。）\n林小满：七床 的人呢？\n周岸：别喊。"
	if got != want {
		t.Fatalf("章节正文还原成逐行纯文本：%q", got)
	}
	script := drama.ScriptFromText("## "+strings.SplitN(got, "\n", 2)[0]+"\n"+strings.SplitN(got, "\n", 2)[1], "t")
	if len(script.Scenes) != 1 || script.LineCount() != 2 {
		t.Fatalf("还原后能转出台词表：%+v", script)
	}
}

func TestShotlistRefsResolveToBoundProjectAssets(t *testing.T) {
	assets := []ProjectAssetSummary{
		{ID: "char-1", Title: "林小满", Category: model.AssetCategoryCharacter, PrimaryVersionID: "char-1-v1"},
		{ID: "loc-1", Title: "病房走廊", Category: model.AssetCategoryEnvironment, PrimaryVersionID: "loc-1-v1"},
		{ID: "prop-1", Title: "病历夹", Category: model.AssetCategoryProp, PrimaryVersionID: "prop-1-v1"},
		{ID: "draft-1", Title: "未确认", Category: model.AssetCategoryProp},
	}
	rows := []shotlistRow{{StoryboardRow: drama.StoryboardRow{AssetBindings: []drama.RowAssetBinding{{NodeID: "char-1"}, {NodeID: "loc-1"}, {NodeID: "prop-1"}, {NodeID: "draft-1"}}}}}
	shots := []drama.Shot{{ID: "E01S01-01", Scene: "E01S01", Subject: "person", Size: "全景", Cam: drama.Cam{Z: "Z7", Y: "Y4", X: "X2"}, Move: []string{"fixed"}, Action: "林小满走来", RefsHint: []string{"林小满·夜班", "病历夹·夹在腋下", "七床门牌"}, StartState: "a", EndState: "b"}}
	attachRefsHints(shots, rows, assets, nil)
	if got := strings.Join(shots[0].RefsHint, "|"); got != "林小满·夜班|病历夹·夹在腋下|七床门牌|病房走廊" {
		t.Fatalf("模型写的 refs_hint 保留，绑定了但没写的资产补在后面：%s", got)
	}
	bible := dramaShotlistBible(shots, rows, assets, map[string]bool{"char-1": true, "loc-1": true, "prop-1": true}, 6)
	m, _ := drama.ModelByID("seedance-2.5")
	segs, problems := drama.Pack(drama.PackInput{Shotlist: drama.Shotlist{Profile: "live-realism", Shots: shots}, Bible: bible, Model: m, ProfileID: "live-realism", FileExists: func(string) bool { return true }})
	if len(problems) > 0 || len(segs) != 1 {
		t.Fatalf("装段：%v %v", segs, problems)
	}
	var files []string
	for _, r := range segs[0].Refs {
		files = append(files, r.Role+":"+r.File)
	}
	if got := strings.Join(files, ","); got != "identity:char-1-v1,prop:prop-1-v1,location:loc-1-v1" || segs[0].Mode != drama.ModeLockIdentity {
		t.Fatalf("参考应落到资产主版本（人物带变体也挂同一版本，未确认的资产不挂）：%s %s", got, segs[0].Mode)
	}
	if len(segs[0].Cast) != 0 {
		t.Fatalf("有身份参考图的人物不写外貌声明：%+v", segs[0].Cast)
	}
	if !strings.Contains(segs[0].Shots[0].Anchors, "状态：林小满·夜班、病历夹·夹在腋下") {
		t.Fatalf("refs_hint 的状态进装段的锚点句：%s", segs[0].Shots[0].Anchors)
	}
}

func TestDramaShotlistRuleOnlyTightensProjectModel(t *testing.T) {
	project := &model.Project{GenerationModel: "seedance-2.5"}
	m, err := dramaShotlistModel(project, &DramaShotlistRule{MaxSeconds: 15, MinSeconds: 5})
	if err != nil || m.Duration.Max != 15 || m.Duration.Min != 5 {
		t.Fatalf("渠道规则收紧段长：%+v %v", m.Duration, err)
	}
	if m, _ := dramaShotlistModel(project, &DramaShotlistRule{MaxSeconds: 60}); m.Duration.Max != 30 {
		t.Fatalf("渠道规则不能放宽项目生成模型的上限：%+v", m.Duration)
	}
}

func loadOnlineRowsRaw(t *testing.T) []json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(onlineQichuangDir + "/rows.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rows []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Rows
}

func chapterHTML(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(onlineQichuangDir + "/chapter.md")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			b.WriteString("<p>" + line + "</p>")
		}
	}
	return b.String()
}

func TestWriteDramaShotlistChecksPacksAndStores(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.5")
	if err := db.Model(&model.ProjectUnit{}).Where("id = ?", unit.ID).Update("source_text", chapterHTML(t)).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, a := range []struct {
		id, title string
		category  model.AssetCategory
	}{{"char-1", "林小满", model.AssetCategoryCharacter}, {"char-2", "周岸", model.AssetCategoryCharacter}, {"loc-1", "病房走廊", model.AssetCategoryEnvironment}} {
		version := a.id + "-v1"
		if err := db.Create(&model.Asset{ID: a.id, UserID: "user-1", Kind: "image", Category: a.category, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: version, Title: a.title, PayloadJSON: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.AssetVersion{ID: version, AssetID: a.id, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ProjectAssetLink{ID: newID(), ProjectID: project.ID, AssetID: a.id, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 只有林小满有三视图；周岸、病房走廊只有文字设定，不能成参考。
	if err := db.Create(&model.AssetRepresentation{ID: newID(), AssetVersionID: "char-1-v1", ResourceID: "res-char-1", MediaType: "image", Role: "turnaround_sheet", MetadataJSON: "{}", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	rows := loadOnlineRowsRaw(t)
	// 线上实测输出是切场规则改变前写的（三场，E01S03 是第三个场次头），按旧规则的台词表装（与重装旧镜头表同一条路）。
	result, err := service.WriteDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: rows, LegacyScenes: true})
	if err != nil {
		t.Fatalf("线上实测输出应能保存：%v", err)
	}
	if result.Stats.Shots != 29 || result.Stats.LineExact != 16 || result.Check.Errors != 0 || result.Shotlist.Version != 1 {
		t.Fatalf("29 镜、16 句全对上、0 error、版本 1：%+v %+v", result.Stats, result.Check)
	}
	if len(result.Segments) != 5 {
		t.Fatalf("Seedance 2.5 按场装成 5 段（与 pack.py 一致），得到 %d", len(result.Segments))
	}
	first := result.Segments[0]
	if first.Derived || len(first.Spec.ShotIDs) == 0 || first.Spec.Profile != "live-realism" || first.Spec.Shots[0].LightFull == "" || first.Spec.Shots[0].Positions == "" {
		t.Fatalf("段版本写 2.1 段（shot_ids、profile、灯光七项、站位）：%+v", first.Spec)
	}
	if len(first.Spec.Refs) == 0 || first.Spec.Refs[0].File != "char-1-v1" {
		t.Fatalf("参考取分镜行绑定的项目资产：%+v", first.Spec.Refs)
	}
	for _, view := range result.Segments {
		for _, r := range view.Spec.Refs {
			if r.File != "char-1-v1" {
				t.Fatalf("%s 挂了没有图的资产 %s：%+v", view.Spec.ID, r.File, view.Spec.Refs)
			}
		}
	}
	compiled, err := service.CompileProjectShot("user-1", project.ID, first.ShotID, CompileProjectShotRequest{})
	if err != nil || drama.HasError(compiled.Result.Issues) || !strings.Contains(compiled.Result.Prompt, "灯光只用场景内实际存在的光源") {
		t.Fatalf("2.1 段按档案风格块编译：%v %+v", err, compiled.Result.Issues)
	}
	view, err := service.DramaShotlist("user-1", project.ID, unit.ID)
	if err != nil || view == nil || len(view.Shotlist.Shots) != 29 || view.Script == nil || view.Script.LineCount() != 16 {
		t.Fatalf("读回镜头表与台词表：%v %+v", err, view)
	}
	again, err := service.WriteDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: rows, LegacyScenes: true})
	if err != nil || again.Shotlist.Version != 2 {
		t.Fatalf("重写镜头表版本加一：%v %+v", err, again.Shotlist.Version)
	}

	// 改坏一镜：情绪弧 id 不在词库里，整批拒绝并指出镜头；原来的镜头表与段不动。
	var bad map[string]any
	if err := json.Unmarshal(rows[0], &bad); err != nil {
		t.Fatal(err)
	}
	bad["spec"].(map[string]any)["emotion_arc"] = map[string]any{"id": "arc-not-exist"}
	broken, _ := json.Marshal(bad)
	badRows := append([]json.RawMessage{broken}, rows[1:]...)
	_, err = service.WriteDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: badRows})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Reason != ReasonShotlistCheckFailed {
		t.Fatalf("检查不过应返回 shotlist_check_failed：%v", err)
	}
	report, ok := appErr.Details["check"].(drama.CheckReport)
	hit := false
	for _, item := range report.Items {
		hit = hit || (item.Level == "error" && item.Rule == "R-SHOT-025" && item.ShotID == "E01S01-01")
	}
	if !ok || report.Errors != 1 || !hit {
		t.Fatalf("details.check 带规则项和镜头：%+v", appErr.Details)
	}
	if view, _ := service.DramaShotlist("user-1", project.ID, unit.ID); view == nil || view.Version != 2 {
		t.Fatal("检查不过不落库")
	}
	state := &cloudAgentRuntime{Request: CloudAgentRequest{DramaProjectID: project.ID}}
	toolResult, err := service.cloudAgentDramaTool("user-1", state, dramaToolCall("drama_shotlist_write", map[string]any{"unitId": unit.ID, "rows": badRows}))
	if payload, ok := toolResult.(map[string]any); err != nil || !ok || payload["saved"] != false || payload["check"] == nil {
		t.Fatalf("Agent 写镜头表检查不过时拿到规则项而不是一句报错：%v %+v", err, toolResult)
	}
	if _, err := service.WriteDramaShotlist("user-2", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: rows}); err == nil {
		t.Fatal("别人的项目不能写")
	}
}

// 线上实测 on3（切场规则改变前写的）：一章两场都叫「内·电梯口·夜」（E01S02 对话、E01S03 打斗），模型给两场绑的是同一个场景资产。
// 段按镜头表场号装、参考取各自镜头，对话场的段不能挂只在打斗场出现的阿坤。按旧规则的台词表装（重装旧镜头表的路径）。
func TestShotlistSameNamedScenesKeepTheirOwnCast(t *testing.T) {
	const dir = "../drama/testdata/online-qichuang-on3"
	var doc struct {
		Rows []json.RawMessage `json:"rows"`
	}
	raw, err := os.ReadFile(dir + "/rows.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	rows, err := parseShotlistRows(doc.Rows)
	if err != nil {
		t.Fatal(err)
	}
	var stub []struct {
		ID       string              `json:"id"`
		Title    string              `json:"title"`
		Category model.AssetCategory `json:"category"`
	}
	raw, err = os.ReadFile(dir + "/assets.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stub); err != nil {
		t.Fatal(err)
	}
	assets := make([]ProjectAssetSummary, 0, len(stub))
	for _, a := range stub {
		assets = append(assets, ProjectAssetSummary{ID: a.ID, Title: a.Title, Category: a.Category, PrimaryVersionID: a.ID + "-v1"})
	}
	body, err := os.ReadFile(onlineQichuangDir + "/chapter.md")
	if err != nil {
		t.Fatal(err)
	}
	script := drama.LegacyScriptFromText(string(body), "七床")
	if script.Scenes[1].Heading != script.Scenes[2].Heading {
		t.Fatalf("夹具前提：第二、三场同名，得到 %q / %q", script.Scenes[1].Heading, script.Scenes[2].Heading)
	}
	m, _ := drama.ModelByID("seedance-2.5")
	imaged := map[string]bool{}
	for _, a := range assets {
		imaged[a.ID] = true
	}
	plan, err := planDramaShotlist(rows, &script, assets, imaged, m, nil, dramaProjectContext{})
	if err != nil {
		t.Fatalf("on3 应能保存：%v", err)
	}
	if plan.Stats.Shots != 41 || plan.Stats.LineExact != 16 || plan.Report.Errors != 0 {
		t.Fatalf("41 镜、16 句全对上、0 error：%+v %d", plan.Stats, plan.Report.Errors)
	}
	scenes := map[string][]string{}
	for _, seg := range plan.Segments {
		var files []string
		for _, r := range seg.Refs {
			files = append(files, r.File)
		}
		scenes[seg.Scene] = append(scenes[seg.Scene], strings.Join(files, ","))
		hasAkun := strings.Contains(strings.Join(files, ","), "char-3-v1")
		switch seg.Scene {
		case "E01S01", "E01S02":
			if hasAkun {
				t.Fatalf("%s（%s）是阿坤出场前的戏，不该挂阿坤：%v", seg.ID, seg.Scene, files)
			}
		case "E01S03":
			if !hasAkun {
				t.Fatalf("%s（%s）是打斗场，应挂阿坤：%v", seg.ID, seg.Scene, files)
			}
		}
		if !strings.Contains(strings.Join(files, ","), "loc-2-v1") && seg.Scene != "E01S01" {
			t.Fatalf("%s 应挂电梯口场景：%v", seg.ID, files)
		}
	}
	if len(scenes["E01S02"]) == 0 || len(scenes["E01S03"]) == 0 {
		t.Fatalf("两场同名的戏应各自装段：%v", scenes)
	}
}

func TestAttachRefsHintsSkipsBoundCharacterNotInShot(t *testing.T) {
	assets := []ProjectAssetSummary{
		{ID: "char-3", Title: "阿坤", Category: model.AssetCategoryCharacter, PrimaryVersionID: "char-3-v1"},
		{ID: "loc-2", Title: "电梯口", Category: model.AssetCategoryEnvironment, PrimaryVersionID: "loc-2-v1"},
	}
	rows := []shotlistRow{{StoryboardRow: drama.StoryboardRow{AssetBindings: []drama.RowAssetBinding{{NodeID: "char-3"}, {NodeID: "loc-2"}}}}}
	shots := []drama.Shot{{ID: "E01S02-03", Focus: "林小满", Blocking: []drama.Blocking{{Who: "林小满", At: "电梯口"}}}}
	attachRefsHints(shots, rows, assets, nil)
	if got := strings.Join(shots[0].RefsHint, "|"); got != "电梯口" {
		t.Fatalf("绑定了但这一镜不在场的人物不挂，场景照挂：%s", got)
	}
	shots[0].Blocking = append(shots[0].Blocking, drama.Blocking{Who: "阿坤", At: "楼梯间"})
	shots[0].RefsHint = nil
	attachRefsHints(shots, rows, assets, nil)
	if got := strings.Join(shots[0].RefsHint, "|"); got != "阿坤|电梯口" {
		t.Fatalf("站位里有他就挂：%s", got)
	}
}

func loadOn3(t *testing.T) ([]shotlistRow, []ProjectAssetSummary, drama.Script) {
	t.Helper()
	const dir = "../drama/testdata/online-qichuang-on3"
	var doc struct {
		Rows []json.RawMessage `json:"rows"`
	}
	raw, err := os.ReadFile(dir + "/rows.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	rows, err := parseShotlistRows(doc.Rows)
	if err != nil {
		t.Fatal(err)
	}
	var stub []struct {
		ID       string              `json:"id"`
		Title    string              `json:"title"`
		Category model.AssetCategory `json:"category"`
		Prompt   string              `json:"prompt"`
	}
	if raw, err = os.ReadFile(dir + "/assets.json"); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stub); err != nil {
		t.Fatal(err)
	}
	assets := make([]ProjectAssetSummary, 0, len(stub))
	for _, a := range stub {
		// 线上实测的资产都是只有文字设定的实体（mediaType entity）：有主版本、没有图。
		assets = append(assets, ProjectAssetSummary{ID: a.ID, Title: a.Title, Category: a.Category, MediaType: "entity", PrimaryVersionID: a.ID + "-v1", PreviewText: a.Prompt})
	}
	body, err := os.ReadFile(onlineQichuangDir + "/chapter.md")
	if err != nil {
		t.Fatal(err)
	}
	// on3 是切场规则改变前的输出（三场），台词表按旧规则切。
	return rows, assets, drama.LegacyScriptFromText(string(body), "七床")
}

// 线上复测：资产都只有文字设定时，段参考里不能有它们（编译出 @图片N 却没有文件，提交会错号或缺文件）；
// 人物改写「人物·X」外貌声明，场景、道具写进镜头描述，段是 text_only。
func TestShotlistImagelessAssetsCompileWithoutPictures(t *testing.T) {
	rows, assets, script := loadOn3(t)
	m, _ := drama.ModelByID("seedance-2.5")
	plan, err := planDramaShotlist(rows, &script, assets, map[string]bool{}, m, nil, dramaProjectContext{})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := drama.BuiltinProfile(drama.DefaultProfileID)
	if err != nil {
		t.Fatal(err)
	}
	declared, described := 0, 0
	for _, seg := range plan.Segments {
		if len(seg.Refs) != 0 || seg.Mode != drama.ModeTextOnly {
			t.Fatalf("%s 没图的资产不该成参考：mode=%s refs=%+v", seg.ID, seg.Mode, seg.Refs)
		}
		if seg.ContinuityBridge != nil && len(seg.ContinuityBridge.SharedRefs) != 0 {
			t.Fatalf("%s 的一致性桥不该带参考：%+v", seg.ID, seg.ContinuityBridge)
		}
		input, err := dramaSegmentShotInput(seg, map[string]shotlistRow{})
		if err != nil || len(input.AssetVersionIDs) != 0 {
			t.Fatalf("%s 段镜头不绑没图的资产版本：%v %v", seg.ID, err, input.AssetVersionIDs)
		}
		res, err := drama.CompileWithProfile(seg, m, nil, nil, profile)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(res.Prompt, "@图片") || len(res.Files) != 0 {
			t.Fatalf("%s 编译稿不该有 @图片：%v", seg.ID, res.Files)
		}
		if len(seg.Cast) == 0 {
			t.Fatalf("%s 有人物在场却没有外貌声明", seg.ID)
		}
		for _, c := range seg.Cast {
			if !strings.Contains(res.Prompt, "人物·"+c.Name+"：") {
				t.Fatalf("%s 编译稿缺「人物·%s」声明", seg.ID, c.Name)
			}
			declared++
		}
		if strings.Contains(res.Prompt, "人物外貌全程与开头的人物声明一致") {
			described++
		}
		if seg.Scene == "E01S02" || seg.Scene == "E01S01" {
			for _, c := range seg.Cast {
				if c.Name == "阿坤" {
					t.Fatalf("%s 阿坤出场前的段不该声明阿坤", seg.ID)
				}
			}
		}
	}
	if declared == 0 || described != len(plan.Segments) {
		t.Fatalf("每段都应写人物声明并改用声明约束：%d %d/%d", declared, described, len(plan.Segments))
	}
	var allText strings.Builder
	for _, seg := range plan.Segments {
		for _, sh := range seg.Shots {
			allText.WriteString(sh.Text)
		}
	}
	for _, want := range []string{"场景·电梯口：", "场景·病房走廊："} {
		if !strings.Contains(allText.String(), want) {
			t.Fatalf("没图的场景、道具应写进镜头描述：缺「%s」", want)
		}
	}
}

func TestShotlistImagedAndImagelessAssetsMix(t *testing.T) {
	rows, assets, script := loadOn3(t)
	m, _ := drama.ModelByID("seedance-2.5")
	// 只有林小满有三视图：她挂参考，不写声明；其他人写声明；场景、道具都没图。
	plan, err := planDramaShotlist(rows, &script, assets, map[string]bool{"char-1": true}, m, nil, dramaProjectContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, seg := range plan.Segments {
		for _, r := range seg.Refs {
			if r.File != "char-1-v1" {
				t.Fatalf("%s 只有林小满有图，却挂了 %s", seg.ID, r.File)
			}
		}
		for _, c := range seg.Cast {
			if c.Name == "林小满" {
				t.Fatalf("%s 有参考图的林小满不该再写外貌声明", seg.ID)
			}
		}
	}
}

func TestAttachRefsHintsKeepsOneLocationPerShot(t *testing.T) {
	assets := []ProjectAssetSummary{
		{ID: "loc-1", Title: "病房走廊（夜）", Category: model.AssetCategoryEnvironment, PrimaryVersionID: "loc-1-v1"},
		{ID: "loc-2", Title: "电梯口（夜）", Category: model.AssetCategoryEnvironment, PrimaryVersionID: "loc-2-v1"},
		{ID: "loc-3", Title: "七床病房（夜）", Category: model.AssetCategoryEnvironment, PrimaryVersionID: "loc-3-v1"},
	}
	script := &drama.Script{Scenes: []drama.ScriptScene{{ID: "E01S01", Heading: "内·病房走廊·夜"}, {ID: "E01S02", Heading: "内·电梯口·夜"}}}
	shots := []drama.Shot{
		{ID: "E01S01-01", Scene: "E01S01", RefsHint: []string{"病房走廊（夜）"}},
		{ID: "E01S01-05", Scene: "E01S01", RefsHint: []string{"七床病房（夜）", "病历夹"}},
		{ID: "E01S01-11", Scene: "E01S01", RefsHint: []string{"七床病房（夜）", "病房走廊（夜）"}},
		{ID: "E01S02-03", Scene: "E01S02", RefsHint: []string{"林小满", "病房走廊（夜）", "电梯口（夜）"}},
	}
	attachRefsHints(shots, nil, assets, script)
	got := []string{}
	for _, sh := range shots {
		got = append(got, strings.Join(sh.RefsHint, "|"))
	}
	want := []string{"病房走廊（夜）", "病房走廊（夜）|病历夹", "病房走廊（夜）", "林小满|电梯口（夜）"}
	if strings.Join(got, " / ") != strings.Join(want, " / ") {
		t.Fatalf("一镜只留一个场景，并且只认本场标题里的地点（spec.scene 为准）：got %v want %v", got, want)
	}
}

// 线上已存的段（第二期上线时装出的）参考全指向没图的资产：编译时兜住，不出 @图片，人物改写声明。
func TestCompileDropsImagelessRefsFromStoredSegments(t *testing.T) {
	rows, assets, script := loadOn3(t)
	m, _ := drama.ModelByID("seedance-2.5")
	imaged := map[string]bool{}
	for _, a := range assets {
		imaged[a.ID] = true // 模拟旧逻辑：所有有主版本的资产都当成有图，装出带参考的段
	}
	plan, err := planDramaShotlist(rows, &script, assets, imaged, m, nil, dramaProjectContext{})
	if err != nil {
		t.Fatal(err)
	}
	byFile := map[string]ProjectAssetSummary{}
	for _, a := range assets {
		byFile[a.PrimaryVersionID] = a
	}
	lookup := func(file string) dramaRefAsset {
		a, ok := byFile[file]
		return dramaRefAsset{asset: a, known: ok, hasImage: false}
	}
	profile, _ := drama.BuiltinProfile(drama.DefaultProfileID)
	for _, stored := range plan.Segments {
		if len(stored.Refs) == 0 {
			t.Fatalf("%s 夹具前提：旧段带参考", stored.ID)
		}
		seg, issues := dropImagelessRefs(stored, lookup, 6)
		if len(seg.Refs) != 0 || seg.Mode != drama.ModeTextOnly || len(issues) == 0 {
			t.Fatalf("%s 去掉没图的参考后应是 text_only：%+v %v", seg.ID, seg.Refs, issues)
		}
		if b := seg.ContinuityBridge; b != nil && len(b.SharedRefs) != 0 {
			t.Fatalf("%s 桥里也去掉没图的参考：%v", seg.ID, b.SharedRefs)
		}
		res, err := drama.CompileWithProfile(seg, m, nil, nil, profile)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(res.Prompt, "@图片") || len(res.Files) != 0 {
			t.Fatalf("%s 编译稿不该有 @图片：%v", seg.ID, res.Files)
		}
		for _, r := range stored.Refs {
			if r.Role == "identity" && !strings.Contains(res.Prompt, "人物·"+byFile[r.File].Title+"：") {
				t.Fatalf("%s 去掉的人物参考「%s」应改写外貌声明", seg.ID, byFile[r.File].Title)
			}
		}
	}
	// 有图的、认不出的参考原样保留
	seg := drama.Segment{ID: "S01", Scene: "E01S01", Dur: 5, Mode: drama.ModeLockIdentity, Shots: []drama.SubShot{{At: 0, Text: "x"}}, Refs: []drama.Ref{{File: "has-image", Role: "identity"}, {File: "agent-file", Role: "location"}}}
	kept, issues := dropImagelessRefs(seg, func(file string) dramaRefAsset {
		return dramaRefAsset{known: file == "has-image", hasImage: true}
	}, 6)
	if len(kept.Refs) != 2 || kept.Mode != drama.ModeLockIdentity || issues != nil {
		t.Fatalf("有图和认不出的参考不动：%+v %v", kept, issues)
	}
}

// 剧本优化替换掉的旧剧集被删后，页面还可能读它的镜头表：返回 404（not_found），不是 500；在的单元照常读。
func TestDramaShotlistOfDeletedUnitIsNotFound(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.5")
	if view, err := service.DramaShotlist("user-1", project.ID, unit.ID); err != nil || view != nil {
		t.Fatalf("还没有镜头表的章节返回空：%v %+v", err, view)
	}
	if err := db.Delete(&model.ProjectUnit{}, "id = ?", unit.ID).Error; err != nil {
		t.Fatal(err)
	}
	_, err := service.DramaShotlist("user-1", project.ID, unit.ID)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != http.StatusNotFound || appErr.Reason != ReasonNotFound {
		t.Fatalf("已删除的单元返回 404 not_found：%v", err)
	}
	// 项目 id 不存在时读镜头表、单元列表也是 404，不是 500。
	for name, call := range map[string]func() error{
		"镜头表":  func() error { _, err := service.DramaShotlist("user-1", "no-such-project", unit.ID); return err },
		"单元列表": func() error { _, err := service.ProjectUnitSummaries("user-1", "no-such-project"); return err },
	} {
		var notFound *AppError
		if err := call(); !errors.As(err, &notFound) || notFound.Status != http.StatusNotFound {
			t.Fatalf("%s：项目不存在应返回 404：%v", name, err)
		}
	}
}
