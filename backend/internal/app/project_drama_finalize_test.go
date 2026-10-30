package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// seedOnlineQichuang 是七床第一章的正文和三个已确认资产（只有林小满有三视图）。
func seedOnlineQichuang(t *testing.T, db *gorm.DB, project model.Project, unit model.ProjectUnit) {
	t.Helper()
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
	if err := db.Create(&model.AssetRepresentation{ID: newID(), AssetVersionID: "char-1-v1", ResourceID: "res-char-1", MediaType: "image", Role: "turnaround_sheet", MetadataJSON: "{}", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
}

// onlineOutputLines 把七床线上输出（@2 的整段 JSON）改写成一镜一行（@3）。
func onlineOutputLines(t *testing.T) ([]map[string]any, string) {
	t.Helper()
	var payload struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal([]byte(readTestdata(t, "model-output.txt")), &payload); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, len(payload.Rows))
	for _, row := range payload.Rows {
		encoded, _ := json.Marshal(row)
		lines = append(lines, string(encoded))
	}
	return payload.Rows, strings.Join(lines, "\n") + "\n"
}

func dramaStepTask(project model.Project, unit model.ProjectUnit, extra map[string]any) model.Task {
	step := map[string]any{"step": drama.StepChapterShots, "contract": drama.ContractChapterShots}
	for k, v := range extra {
		step[k] = v
	}
	input, _ := json.Marshal(map[string]any{"mode": "text", "metadata": map[string]any{"domainProjectId": project.ID, "chapterId": unit.ID, "dramaStep": step}})
	return model.Task{ID: newID(), UserID: "user-1", ProjectID: project.ID, Type: "canvas_text", Operation: "storyboard", InputJSON: string(input)}
}

// 收尾解析一镜一行的输出、按名字绑资产、写镜头表和段：与把同一批分镜行（@2 整段 JSON 解析、同一套绑定）直接写镜头表逐字段一致。
func TestFinalizeDramaStepTaskWritesShotlistLikeDirectWrite(t *testing.T) {
	direct, directDB, directProject, directUnit := newProjectDramaTestService(t, "seedance-2.5")
	seedOnlineQichuang(t, directDB, directProject, directUnit)
	rows, err := drama.StoryboardRowsFromResult(testJSON(t, map[string]any{"text": readTestdata(t, "model-output.txt")}))
	if err != nil {
		t.Fatal(err)
	}
	assets, err := direct.ProjectAssets("user-1", directProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	bindDramaRowAssets(rows, assets)
	raws := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		raws = append(raws, json.RawMessage(testJSON(t, row)))
	}
	want, err := direct.WriteDramaShotlist("user-1", directProject.ID, directUnit.ID, DramaShotlistWriteRequest{Rows: raws})
	if err != nil {
		t.Fatal(err)
	}

	service, db, project, unit := newProjectDramaTestService(t, "seedance-2.5")
	seedOnlineQichuang(t, db, project, unit)
	original, text := onlineOutputLines(t)
	result := map[string]any{"mode": "text", "text": text}
	applied, err := service.finalizeDramaStepTask(dramaStepTask(project, unit, nil), result)
	if err != nil || !applied {
		t.Fatalf("收尾应写库：%v %v", applied, err)
	}
	outcome := result["dramaStep"].(DramaStepTaskOutcome)
	if outcome.Status != DramaStepOutcomeWritten || outcome.Parse.Rows != len(original) || outcome.Parse.Rate != 1 || outcome.Segments != len(want.Segments) || outcome.Errors != 0 {
		t.Fatalf("摘要：%+v", outcome)
	}
	got, err := service.DramaShotlist("user-1", project.ID, unit.ID)
	if err != nil || got == nil || got.Draft {
		t.Fatalf("读回镜头表：%v %+v", err, got)
	}
	wantView, err := direct.DramaShotlist("user-1", directProject.ID, directUnit.ID)
	if err != nil || testJSON(t, got.Shotlist) != testJSON(t, wantView.Shotlist) || testJSON(t, got.Script) != testJSON(t, wantView.Script) {
		t.Fatalf("镜头表、台词表应与直接写入一致：%v", err)
	}
	if got.Check.Parse == nil || got.Check.Parse.Rows != len(original) {
		t.Fatalf("解析统计写进 check_json：%+v", got.Check.Parse)
	}
	segments, err := service.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil || len(segments) != len(want.Segments) {
		t.Fatalf("段数：%v %d", err, len(segments))
	}
	for i := range segments {
		if !reflect.DeepEqual(segments[i].Spec, want.Segments[i].Spec) || segments[i].Title != want.Segments[i].Title || segments[i].DurationMs != want.Segments[i].DurationMs {
			t.Fatalf("第 %d 段与直接写入不一致：\n%+v\n%+v", i+1, segments[i].Spec, want.Segments[i].Spec)
		}
	}
	var evidence int64
	db.Model(&model.ShotEvidence{}).Count(&evidence)
	if evidence != 0 {
		t.Fatalf("收尾不写证据：%d", evidence)
	}

	// 不是流程步骤的任务不处理。
	plain := model.Task{InputJSON: `{"mode":"text","metadata":{"source":"x"}}`}
	if applied, err := service.finalizeDramaStepTask(plain, map[string]any{}); applied || err != nil {
		t.Fatalf("普通文本任务：%v %v", applied, err)
	}
	// 解析不出镜头行：任务失败。
	if _, err := service.finalizeDramaStepTask(dramaStepTask(project, unit, nil), map[string]any{"text": "抱歉，我无法完成"}); err == nil {
		t.Fatal("没有镜头行应失败")
	}
}

// 检查有 error：行和检查结果存成草稿，不装段、本章原有的段不动；重装仍要过检查。
func TestFinalizeDramaStepTaskKeepsDraftOnCheckErrors(t *testing.T) {
	service, db, project, unit := newProjectDramaTestService(t, "seedance-2.5")
	seedOnlineQichuang(t, db, project, unit)
	original, text := onlineOutputLines(t)
	if _, err := service.finalizeDramaStepTask(dramaStepTask(project, unit, nil), map[string]any{"text": text}); err != nil {
		t.Fatal(err)
	}
	before, err := service.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil || len(before) == 0 {
		t.Fatalf("先有一版段：%v", err)
	}
	bad := map[string]any{}
	for k, v := range original[0] {
		bad[k] = v
	}
	spec := map[string]any{}
	for k, v := range original[0]["spec"].(map[string]any) {
		spec[k] = v
	}
	spec["emotion_arc"] = map[string]any{"id": "arc-not-exist"}
	bad["spec"] = spec
	lines := strings.SplitN(text, "\n", 2)
	broken := testJSON(t, bad) + "\n" + lines[1]
	result := map[string]any{"text": broken}
	if _, err := service.finalizeDramaStepTask(dramaStepTask(project, unit, nil), result); err != nil {
		t.Fatalf("检查不过不算任务失败：%v", err)
	}
	outcome := result["dramaStep"].(DramaStepTaskOutcome)
	if outcome.Status != DramaStepOutcomeDraft || outcome.Errors != 1 || outcome.Segments != 0 {
		t.Fatalf("草稿摘要：%+v", outcome)
	}
	view, err := service.DramaShotlist("user-1", project.ID, unit.ID)
	if err != nil || view == nil || !view.Draft || view.Version != 2 || view.Check.Errors != 1 {
		t.Fatalf("草稿落库：%v %+v", err, view)
	}
	after, _ := service.DramaSegments("user-1", project.ID, unit.ID)
	if len(after) != len(before) || after[0].ShotID != before[0].ShotID {
		t.Fatal("草稿不动本章原有的段")
	}
	if _, err := service.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{}); appReason(err) != string(ReasonShotlistCheckFailed) {
		t.Fatalf("草稿重装仍要过检查：%v", err)
	}
}

// 需求文档的单段秒数（brief_json.segment_seconds）收紧装段上限；起任务时带的渠道规则一起生效，取更严的。
func TestFinalizeDramaStepTaskUsesBriefSegmentSeconds(t *testing.T) {
	service, db, project, unit := newProjectDramaTestService(t, "seedance-2.5")
	seedOnlineQichuang(t, db, project, unit)
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("brief_json", `{"segment_seconds":10}`).Error; err != nil {
		t.Fatal(err)
	}
	_, text := onlineOutputLines(t)
	if _, err := service.finalizeDramaStepTask(dramaStepTask(project, unit, map[string]any{"rule": map[string]any{"maxSeconds": 12, "minSeconds": 4}}), map[string]any{"text": text}); err != nil {
		t.Fatal(err)
	}
	segments, err := service.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil || len(segments) == 0 {
		t.Fatal(err)
	}
	for _, seg := range segments {
		if seg.Spec.Dur > 10 {
			t.Fatalf("%s 超过需求文档的 10 秒：%.1f", seg.Spec.ID, seg.Spec.Dur)
		}
	}
	if rule := briefSegmentRule(`{"segment_seconds":30}`, &DramaShotlistRule{MaxSeconds: 15, MinSeconds: 5}); rule.MaxSeconds != 15 || rule.MinSeconds != 5 {
		t.Fatalf("需求文档的秒数不能放宽渠道规则：%+v", rule)
	}
	if rule := briefSegmentRule(`{}`, nil); rule != nil {
		t.Fatalf("没写秒数不加规则：%+v", rule)
	}
}

func testJSON(t *testing.T, v any) string {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
