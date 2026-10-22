package app

import (
	"encoding/json"
	"errors"
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
	attachRefsHints(shots, rows, assets)
	if got := strings.Join(shots[0].RefsHint, "|"); got != "林小满·夜班|病历夹·夹在腋下|七床门牌|病房走廊" {
		t.Fatalf("模型写的 refs_hint 保留，绑定了但没写的资产补在后面：%s", got)
	}
	bible := dramaShotlistBible(shots, rows, assets)
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
		if err := db.Create(&model.Asset{ID: a.id, UserID: "user-1", Kind: "image", Category: a.category, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: version, Title: a.title, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.AssetVersion{ID: version, AssetID: a.id, Version: 1, Status: model.AssetVersionStatusConfirmed, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ProjectAssetLink{ID: newID(), ProjectID: project.ID, AssetID: a.id, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows := loadOnlineRowsRaw(t)
	result, err := service.WriteDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: rows})
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
	compiled, err := service.CompileProjectShot("user-1", project.ID, first.ShotID, CompileProjectShotRequest{})
	if err != nil || drama.HasError(compiled.Result.Issues) || !strings.Contains(compiled.Result.Prompt, "灯光只用场景内实际存在的光源") {
		t.Fatalf("2.1 段按档案风格块编译：%v %+v", err, compiled.Result.Issues)
	}
	view, err := service.DramaShotlist("user-1", project.ID, unit.ID)
	if err != nil || view == nil || len(view.Shotlist.Shots) != 29 || view.Script == nil || view.Script.LineCount() != 16 {
		t.Fatalf("读回镜头表与台词表：%v %+v", err, view)
	}
	again, err := service.WriteDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: rows})
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
