package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newDramaTestService(t *testing.T, generationModel string) (*Service, *gorm.DB, model.Project, model.ProjectUnit) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+newID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.Project{}, &model.ProjectUnit{}, &model.CanvasProject{}, &model.Asset{}, &model.AssetVersion{}, &model.ProjectAssetLink{}, &model.ProjectAssetCandidate{},
		&model.AssetRepresentation{}, &model.CharacterVoiceBinding{}, &model.ProjectAssetFolder{},
		&model.Shot{}, &model.ShotRevision{}, &model.ShotArtifact{}, &model.ShotAssetReference{}, &model.ShotEvidence{}, &model.DramaShotlist{}, &model.DramaCompileProfile{},
		&model.WorkflowTemplateVersion{}, &model.WorkflowInstance{}, &model.WorkflowStepInstance{}, &model.WorkflowStepTask{},
		&model.ProductionTaskLink{}, &model.Task{}, &model.Resource{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	project := model.Project{ID: "project-drama", UserID: "user-1", Name: "夜班", Status: model.ProjectStatusActive, GenerationModel: generationModel, AuthMode: drama.AuthFull, Revision: 1, CreatedAt: now, UpdatedAt: now}
	unit := model.ProjectUnit{ID: "unit-1", ProjectID: project.ID, Kind: model.ProjectUnitKindChapter, Title: "第一章", Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	return &Service{repo: repository.New(db)}, db, project, unit
}

func dramaSpec(id string, dur float64, line string) json.RawMessage {
	seg := drama.Segment{ID: id, Scene: "一", Dur: dur, Mode: drama.ModeTextOnly, Shots: []drama.SubShot{{At: 0, Size: "中景", Text: "护士推开七床的门"}}}
	if line != "" {
		seg.Shots[0].Lines = []drama.Line{{Speaker: "林小满", Text: line}}
	}
	raw, _ := json.Marshal(seg)
	return raw
}

func appReason(err error) string {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return string(appErr.Reason)
	}
	return ""
}

func TestSegmentLengthFollowsProjectGenerationModel(t *testing.T) {
	service, _, project, unit := newDramaTestService(t, "seedance-2.0")
	_, err := service.CreateProjectShot("user-1", project.ID, CreateProjectShotRequest{UnitID: unit.ID, Title: "S01", Description: "长段", DurationMs: 20000})
	if appReason(err) != string(ReasonSegmentTooLong) {
		t.Fatalf("Seedance 2.0 项目里 20 秒的段应被拒绝，得到 %v", err)
	}
	if _, err := service.CreateProjectShot("user-1", project.ID, CreateProjectShotRequest{UnitID: unit.ID, Title: "S01", Revision: ShotRevisionInput{Spec: dramaSpec("S01", 16, "")}}); appReason(err) != string(ReasonSegmentTooLong) {
		t.Fatalf("分段 JSON 里 16 秒也应按 15 秒上限拒绝，得到 %v", err)
	}
	if _, err := service.CreateProjectShot("user-1", project.ID, CreateProjectShotRequest{UnitID: unit.ID, Title: "S01", Description: "15 秒", DurationMs: 15000}); err != nil {
		t.Fatalf("15 秒应允许: %v", err)
	}

	long, _, longProject, longUnit := newDramaTestService(t, "seedance-2.5")
	if _, err := long.CreateProjectShot("user-1", longProject.ID, CreateProjectShotRequest{UnitID: longUnit.ID, Title: "S01", Revision: ShotRevisionInput{Spec: dramaSpec("S01", 28, "")}}); err != nil {
		t.Fatalf("Seedance 2.5 项目里 28 秒的段应允许: %v", err)
	}
	if _, err := long.CreateProjectShot("user-1", longProject.ID, CreateProjectShotRequest{UnitID: longUnit.ID, Title: "S02", Description: "31 秒", DurationMs: 31000}); appReason(err) != string(ReasonSegmentTooLong) {
		t.Fatalf("31 秒超过 Seedance 2.5 的 30 秒上限，得到 %v", err)
	}
}

func TestVideoTaskDurationGuardUsesProjectModel(t *testing.T) {
	service, _, project, _ := newDramaTestService(t, "minimax-h3")
	input := map[string]any{"mode": "video", "config": map[string]any{"videoSeconds": "20"}, "metadata": map[string]any{"domainProjectId": project.ID, "shotId": "shot-x"}}
	if err := service.enforceDramaSegmentTaskDuration("user-1", input); appReason(err) != string(ReasonSegmentTooLong) {
		t.Fatalf("MiniMax-H3 项目的 20 秒视频任务应被拒绝，得到 %v", err)
	}
	input["config"] = map[string]any{"videoSeconds": "12"}
	if err := service.enforceDramaSegmentTaskDuration("user-1", input); err != nil {
		t.Fatalf("12 秒应放行: %v", err)
	}
	canvasTask := map[string]any{"mode": "video", "config": map[string]any{"videoSeconds": "60"}}
	if err := service.enforceDramaSegmentTaskDuration("user-1", canvasTask); err != nil {
		t.Fatalf("不属于项目镜头的任务不受段长限制: %v", err)
	}
}

func TestNewRevisionOnlyStalesArtifactsWhoseInputsChanged(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.0")
	shot, err := service.CreateProjectShot("user-1", project.ID, CreateProjectShotRequest{UnitID: unit.ID, Title: "S01", Revision: ShotRevisionInput{Spec: dramaSpec("S01", 8, "七床的人呢？")}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, kind := range []string{drama.ArtifactStoryboard, drama.ArtifactVideo} {
		artifact := model.ShotArtifact{ID: "artifact-" + kind, ProjectID: project.ID, UnitID: unit.ID, ShotID: shot.ID, RevisionID: shot.CurrentRevisionID, Type: kind, Status: "ready", Selected: true, MetadataJSON: "{}", InputHash: service.artifactInputHash(shot.ID, shot.CurrentRevisionID, kind), CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&artifact).Error; err != nil {
			t.Fatal(err)
		}
	}
	legacy := model.ShotArtifact{ID: "artifact-legacy", ProjectID: project.ID, UnitID: unit.ID, ShotID: shot.ID, Type: drama.ArtifactActionBoard, Status: "ready", MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	// 只改台词：视频过期，分镜图不动；没有指纹的旧产物一律过期。
	if _, _, err := service.CreateShotRevision("user-1", project.ID, shot.ID, ShotRevisionInput{Spec: dramaSpec("S01", 8, "七床？七床的人去哪了？")}); err != nil {
		t.Fatal(err)
	}
	status := func(id string) string {
		var a model.ShotArtifact
		if err := db.First(&a, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		return a.Status
	}
	if got := status("artifact-storyboard"); got != "ready" {
		t.Fatalf("只改台词时分镜图不应过期，得到 %s", got)
	}
	if got := status("artifact-video"); got != "stale" {
		t.Fatalf("改台词后视频应过期，得到 %s", got)
	}
	if got := status("artifact-legacy"); got != "stale" {
		t.Fatalf("没有指纹的旧产物应过期，得到 %s", got)
	}
	// 改画面：分镜图也过期。
	changed := drama.Segment{ID: "S01", Scene: "一", Dur: 8, Mode: drama.ModeTextOnly, Shots: []drama.SubShot{{At: 0, Size: "近景", Text: "护士在门口停住"}}}
	raw, _ := json.Marshal(changed)
	if _, _, err := service.CreateShotRevision("user-1", project.ID, shot.ID, ShotRevisionInput{Spec: raw}); err != nil {
		t.Fatal(err)
	}
	if got := status("artifact-storyboard"); got != "stale" {
		t.Fatalf("改画面后分镜图应过期，得到 %s", got)
	}
}

func TestPickRequiresEvidenceAndProgressFollowsIt(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.0")
	shot, err := service.CreateProjectShot("user-1", project.ID, CreateProjectShotRequest{UnitID: unit.ID, Title: "S01", Revision: ShotRevisionInput{Spec: dramaSpec("S01", 8, "七床的人呢？")}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	artifact := model.ShotArtifact{ID: "take-1", ProjectID: project.ID, UnitID: unit.ID, ShotID: shot.ID, Type: drama.ArtifactVideo, Status: "ready", ResourceID: "res-1", MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.PickShotArtifact("user-1", project.ID, artifact.ID, PickShotArtifactRequest{Reason: "眼神对"}); appReason(err) != string(ReasonEvidenceMissing) {
		t.Fatalf("没有证据时不能选中，得到 %v", err)
	}
	pass := true
	for _, kind := range []string{drama.EvidenceFaces, drama.EvidenceFrames} {
		if _, err := service.CreateShotEvidence("user-1", project.ID, CreateShotEvidenceRequest{ArtifactID: artifact.ID, Kind: kind, Passed: &pass, Note: "看过"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.CreateShotEvidence("user-1", project.ID, CreateShotEvidenceRequest{ArtifactID: artifact.ID, Kind: drama.EvidenceTranscript, Passed: &pass}); err == nil {
		t.Fatal("没有转写任务的听写证据应被拒绝")
	}
	task := model.Task{ID: "stt-1", UserID: "user-1", ProjectID: project.ID, Type: model.TaskTypeTimelineTranscription, Status: model.TaskStatusSucceeded, ResultJSON: `{"segments":[{"startMs":0,"endMs":1500,"text":"七床的人呢"}],"srt":"","language":"zh"}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	transcript, err := service.CreateShotEvidence("user-1", project.ID, CreateShotEvidenceRequest{ArtifactID: artifact.ID, Kind: drama.EvidenceTranscript, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Passed == nil || !*transcript.Passed || !strings.Contains(transcript.MetricsJSON, `"cer":0`) {
		t.Fatalf("听写与台词一致时应通过: %+v", transcript)
	}
	picked, err := service.PickShotArtifact("user-1", project.ID, artifact.ID, PickShotArtifactRequest{Reason: "眼神先空后紧"})
	if err != nil || !picked.Selected {
		t.Fatalf("三项证据齐后应能选中: %v", err)
	}
	progress, err := service.DramaProgress("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Counts.Segments != 1 || progress.Counts.Reviewed != 1 || progress.Counts.Generated != 1 {
		t.Fatalf("进度应由证据推导：%+v", progress.Counts)
	}
	if !strings.HasPrefix(progress.Line, "1 段 · 剧本 1") {
		t.Fatalf("进度行格式不对: %s", progress.Line)
	}
	if progress.Next == nil || progress.Next.Key != "brief" {
		t.Fatalf("需求文档没写时下一件事应是问需求，得到 %+v", progress.Next)
	}
}

func TestBriefWriteSyncsProjectModelAndSegmentsUpsertByID(t *testing.T) {
	service, _, project, unit := newDramaTestService(t, "seedance-2.0")
	doc := json.RawMessage(`{"title":"夜班","video_model":"seedance-2.5","auth_mode":"step","purpose":{"value":"投放","status":"已确认"}}`)
	brief, err := service.UpdateProjectBrief("user-1", project.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if brief["video_model"] != "seedance-2.5" || brief["auth_mode"] != "step" {
		t.Fatalf("需求文档里的模型和档位应同步到项目: %+v", brief)
	}
	if _, err := service.UpdateProjectBrief("user-1", project.ID, json.RawMessage(`{"video_model":"kling"}`)); err == nil {
		t.Fatal("未知模型应被拒绝")
	}
	views, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{dramaSpec("S01", 24, "第一句"), dramaSpec("S02", 6, "")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || views[0].Spec.ID != "S01" || views[0].Derived {
		t.Fatalf("应建两段: %+v", views)
	}
	firstShot := views[0].ShotID
	views, err = service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{dramaSpec("S01", 20, "改过的第一句")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || views[0].ShotID != firstShot || views[0].Spec.Dialogue() != "改过的第一句" {
		t.Fatalf("同段号应写新版本而不是新建: %+v", views)
	}
	compiled, err := service.CompileProjectShot("user-1", project.ID, firstShot, CompileProjectShotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Result.Model != "seedance-2.5" || compiled.Result.Duration != 20 || !strings.Contains(compiled.Result.Prompt, "改过的第一句") {
		t.Fatalf("编译应按项目模型: %+v", compiled.Result)
	}
	h3, err := service.CompileProjectShot("user-1", project.ID, firstShot, CompileProjectShotRequest{Model: "minimax-h3"})
	if err != nil {
		t.Fatal(err)
	}
	if !drama.HasError(h3.Result.Issues) {
		t.Fatal("20 秒的段编译到 MiniMax-H3 应报超长")
	}
}

func TestDeliveryPackageContainsContractsAndRecordsDelivery(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.0")
	if _, err := service.CreateProjectShot("user-1", project.ID, CreateProjectShotRequest{UnitID: unit.ID, Title: "S01", Revision: ShotRevisionInput{Spec: dramaSpec("S01", 8, "七床的人呢？")}}); err != nil {
		t.Fatal(err)
	}
	pkg, err := service.DramaDelivery("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(pkg.Data), int64(len(pkg.Data)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range reader.File {
		names[f.Name] = true
		if strings.Contains(f.Name, ".env") {
			t.Fatalf("交付包里不应有密钥文件: %s", f.Name)
		}
	}
	for _, want := range []string{"README.md", "需求文档.json", "连戏表.json", "分段表.json", "分段表.csv", "资产清单.csv", "生成参数.json", "证据.json", "验收单.md"} {
		if !names[want] {
			t.Fatalf("交付包缺 %s: %v", want, names)
		}
	}
	var count int64
	if err := db.Model(&model.ShotEvidence{}).Where("project_id = ? AND kind = ?", project.ID, drama.EvidenceDelivery).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("应记一条交付证据: %d %v", count, err)
	}
}

func TestCloudAgentExposesDramaToolsOnlyForProjectCanvases(t *testing.T) {
	plain := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}}
	if cloudAgentToolAllowed(plain, "drama_progress_read") {
		t.Fatal("不属于项目的画布不应看到短剧工具")
	}
	project := plain
	project.DramaProjectID = "project-1"
	if !cloudAgentToolAllowed(project, "drama_segments_write") || !cloudAgentToolAllowed(project, "drama_progress_read") {
		t.Fatal("项目画布应看到短剧工具")
	}
	readOnly := project
	readOnly.PermissionMode = "read_only"
	if cloudAgentToolAllowed(readOnly, "drama_segments_write") || !cloudAgentToolAllowed(readOnly, "drama_compile_prompt") {
		t.Fatal("只读模式只给读工具")
	}
}

func TestDramaVideoTaskRejectsOtherModelFamily(t *testing.T) {
	service, _, project, _ := newDramaTestService(t, "seedance-2.0")
	input := map[string]any{"mode": "video", "config": map[string]any{"videoSeconds": "10"}, "metadata": map[string]any{"domainProjectId": project.ID, "shotId": "shot-x"}}
	if err := service.enforceDramaModelFamily("user-1", input, "doubao-seedance-2-5-pro"); err == nil || !strings.Contains(err.Error(), "Seedance 2.5") {
		t.Fatalf("Seedance 2.0 项目用 2.5 模型应被拒绝，得到 %v", err)
	}
	if err := service.enforceDramaModelFamily("user-1", input, "doubao-seedance-2-0-260128"); err != nil {
		t.Fatalf("同族模型应放行: %v", err)
	}
	if err := service.enforceDramaModelFamily("user-1", input, "some-unknown-video"); err != nil {
		t.Fatalf("认不出模型族时放行: %v", err)
	}
	canvasTask := map[string]any{"mode": "video", "config": map[string]any{"videoSeconds": "10"}}
	if err := service.enforceDramaModelFamily("user-1", canvasTask, "doubao-seedance-2-5-pro"); err != nil {
		t.Fatalf("画布任务不受项目模型族限制: %v", err)
	}
}

func TestDramaChaptersReadPagesSourceText(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.0")
	text := strings.Repeat("七床", dramaChapterPageRunes)
	if err := db.Model(&model.ProjectUnit{}).Where("id = ?", unit.ID).Updates(map[string]any{"source_text": text, "word_count": len([]rune(text))}).Error; err != nil {
		t.Fatal(err)
	}
	list, err := service.dramaChaptersRead("user-1", project.ID, "", 0)
	if err != nil || len(list.(map[string]any)["chapters"].([]map[string]any)) != 1 {
		t.Fatalf("章节列表 %v %v", list, err)
	}
	page, err := service.dramaChaptersRead("user-1", project.ID, unit.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	first := page.(map[string]any)
	if len([]rune(first["text"].(string))) != dramaChapterPageRunes || first["nextOffset"] != dramaChapterPageRunes {
		t.Fatalf("第一页应截 %d 字并给 nextOffset: %v", dramaChapterPageRunes, first["nextOffset"])
	}
	rest, err := service.dramaChaptersRead("user-1", project.ID, unit.ID, dramaChapterPageRunes)
	if err != nil || rest.(map[string]any)["nextOffset"] != nil {
		t.Fatalf("第二页应读完: %v %v", rest, err)
	}
	if _, err := service.dramaChaptersRead("user-2", project.ID, unit.ID, 0); err == nil {
		t.Fatal("别人的项目不能读")
	}
}

func TestDramaCanvasGetsSkillReadersWithoutPickedSkills(t *testing.T) {
	req := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, DramaProjectID: "project-1"}
	if !cloudAgentToolAllowed(req, "skill_read_file") || !cloudAgentToolAllowed(req, "drama_chapters_read") {
		t.Fatal("短剧项目画布自动带入 yoozee 技能，应能读技能和章节")
	}
	plain := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}}
	if cloudAgentToolAllowed(plain, "skill_read_file") {
		t.Fatal("没选技能的普通画布不给读技能工具")
	}
}

func TestBriefWriteFollowsContract(t *testing.T) {
	service, _, project, _ := newDramaTestService(t, "seedance-2.0")
	// 线上 Agent 写过的形状：事实写成字符串、自造字段名。必须拒绝并说清楚怎么写。
	bad := json.RawMessage(`{"purpose":"第一集验收","duration_seconds":40,"aspect_ratio":"9:16"}`)
	_, err := service.UpdateProjectBrief("user-1", project.ID, bad)
	if err == nil || !strings.Contains(err.Error(), "需求文档不符合契约") {
		t.Fatalf("不合契约的需求文档应被拒绝，得到 %v", err)
	}
	// Agent 要能看到原因自己改，不能被净化成笼统的「工具执行失败」。
	if message := cloudAgentSafeToolError(err); message != err.Error() {
		t.Fatalf("契约错误应原样给 Agent，得到 %q", message)
	}
	if _, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: "EP-01", Segments: []json.RawMessage{dramaSpec("S01", 8, "")}}); err == nil || cloudAgentSafeToolError(err) != err.Error() || !strings.Contains(err.Error(), "drama_chapters_read") {
		t.Fatalf("章节 ID 写错要给出可读原因，得到 %v", err)
	}
	if _, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: "unit-1", Segments: []json.RawMessage{json.RawMessage(`{"shotId":"SH-001","durationSeconds":13,"prompt":"x"}`)}}); err == nil || cloudAgentSafeToolError(err) != err.Error() {
		t.Fatalf("分段形状不对要给出可读原因，得到 %v", err)
	}
	good := json.RawMessage(`{"purpose":{"value":"第一集验收","status":"已确认","quote":"先按第一集正文写需求文档"},"duration":{"value":"约 40 秒","status":"已确认"},"questions":[{"id":"Q-001","question":"真人还是动画？","blocking":true,"status":"待答"}]}`)
	doc, err := service.UpdateProjectBrief("user-1", project.ID, good)
	if err != nil {
		t.Fatal(err)
	}
	if doc["title"] != project.Name || doc["video_model"] != "seedance-2.0" {
		t.Fatalf("标题和模型应由项目补齐: %v", doc)
	}
}

func TestTextOnlySegmentNeedsReferencesWhenSomeoneSpeaks(t *testing.T) {
	service, _, project, unit := newDramaTestService(t, "seedance-2.0")
	if _, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{dramaSpec("S01", 6, ""), dramaSpec("S02", 8, "七床的人呢？")}}); err != nil {
		t.Fatal(err)
	}
	progress, err := service.DramaProgress("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]bool{}
	for _, row := range progress.Segments {
		refs[row.SegmentID] = row.Refs
	}
	if !refs["S01"] || refs["S02"] {
		t.Fatalf("空镜不需要参考、有人说话的段要人物参考，得到 %v", refs)
	}
}

func TestDramaGateWithoutFilmAsksForExport(t *testing.T) {
	service, _, project, _ := newDramaTestService(t, "seedance-2.0")
	_, err := service.DramaGate("user-1", project.ID, DramaGateRequest{})
	if err == nil || !strings.Contains(err.Error(), "还没有导出的成片") {
		t.Fatalf("gate without film err = %v", err)
	}
}

func TestDramaCanvasInstallsBuiltinSuiteAndDropsLegacySkill(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	if err := s.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.cloudAgentSkills("user", nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy = append(legacy, cloudAgentSkill{ID: legacyDramaSkillID})
	// 新用户没往技能库加过 yoozee 技能：短剧项目画布也要带上，并把旧六步技能拿掉。
	snapshots, err := s.withCloudAgentDramaSuite("user", legacy)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, skill := range snapshots {
		ids[skill.ID] = true
	}
	if !ids["yz-producer"] || !ids["kb-dialects"] {
		t.Fatalf("应自动带上内置 yoozee 技能，得到 %v", ids)
	}
	if ids[legacyDramaSkillID] {
		t.Fatal("旧六步技能不该进短剧项目画布的快照")
	}
	detail, err := s.SkillDetail("user", "yz-producer")
	if err != nil || !detail.IsAdded {
		t.Fatalf("内置技能应已加入用户技能库：%v %+v", err, detail)
	}
}
