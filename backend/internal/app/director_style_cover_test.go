package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func lockDirectorFixture(t *testing.T, s *Service, project model.Project, template *DramaImageTaskTemplate) model.StyleProfile {
	t.Helper()
	output, _ := drama.StyleLockFromText(styleLockReply)
	result, err := s.applyDramaStyleLock("user-1", &project, styleLockInput{ProfileFields: output.StyleProfile, Anchor: output.Anchor, Palette: output.Palette, ImageTask: template, Record: drama.StyleLockRecord{Source: drama.StyleLockSourceDirector, ProjectType: drama.VisualLiveAction}})
	if err != nil || result.PersonalStyle == nil {
		t.Fatal("director style was not saved in original personal center", err, result)
	}
	return *result.PersonalStyle
}

func TestDirectorStylePersonalSourceAndIndependentCoverLifecycle(t *testing.T) {
	s, db, project, template, calls := assetQueueFixture(t, func(w http.ResponseWriter, r *http.Request, _ int32) {})
	profile := lockDirectorFixture(t, s, project, nil)
	if profile.SourceProjectID != project.ID || profile.CoverStatus != "pending" || profile.CoverURL != "" {
		t.Fatal(profile)
	}
	again, err := s.SaveProjectDirectorStyle("user-1", project.ID, DirectorStyleSaveRequest{})
	if err != nil || again.ID != profile.ID || again.Revision != profile.Revision {
		t.Fatal("replayed completion made a duplicate version", err, again)
	}
	saved, err := s.GenerateStyleProfileCover("user-1", profile.ID, DirectorStyleSaveRequest{ImageTask: &template})
	if err != nil || saved.CoverTaskID == "" {
		t.Fatal(err, saved)
	}
	duplicate, err := s.GenerateStyleProfileCover("user-1", profile.ID, DirectorStyleSaveRequest{ImageTask: &template})
	if err != nil || duplicate.CoverTaskID != saved.CoverTaskID || calls.Load() != 0 {
		t.Fatal("cover submission duplicated or called an upstream in this test", err)
	}
	child, err := s.repo.TaskForUser("user-1", saved.CoverTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(child.Prompt, "完整成品图") || strings.Contains(child.InputJSON, "test-key") {
		t.Fatal("cover prompt or credential protection missing")
	}
	png := mediaTestPNG(t)
	resource, err := s.UploadResourceFile("user-1", "cover.png", int64(len(png)), "image", 2, 3, 0, bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]interface{}{"images": []any{map[string]any{"resourceId": resource.ID}}}
	if err := s.finalizeDirectorStyleCover(*child, result); err != nil {
		t.Fatal(err)
	}
	ready, _ := s.repo.StyleProfileForUser("user-1", profile.ID)
	if ready.CoverStatus != "ready" || ready.CoverResourceID != resource.ID || ready.CoverURL == "" {
		t.Fatal(ready)
	}
	if _, err := s.GenerateStyleProfileCover("user-2", profile.ID, DirectorStyleSaveRequest{ImageTask: &template}); err == nil {
		t.Fatal("cross-user cover access")
	}
	var document map[string]any
	json.Unmarshal([]byte(ready.ProfileJSON), &document)
	document["title"] = "我手动编辑的风格"
	raw, _ := json.Marshal(document)
	edited, err := s.UpdateStyleProfile("user-1", profile.ID, StyleProfileRequest{ProfileJSON: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.finalizeDirectorStyleCover(*child, result); err != nil {
		t.Fatal(err)
	}
	current, _ := s.repo.StyleProfileForUser("user-1", profile.ID)
	if current.Revision != edited.Revision || current.Name != "我手动编辑的风格" || current.CoverStatus != "pending" || current.CoverTaskID != "" {
		t.Fatal("late cover overwrote edited style", current)
	}
	var legacyCount int64
	db.Model(&model.DramaStyleLibraryEntry{}).Count(&legacyCount)
	if legacyCount != 0 {
		t.Fatal("director result still writes a parallel personal library")
	}
}

func TestDirectorStyleCoverFailureRetriesWithoutTextOrDuplicateCard(t *testing.T) {
	s, db, project, template, _ := assetQueueFixture(t)
	profile := lockDirectorFixture(t, s, project, &template)
	if profile.CoverTaskID == "" {
		t.Fatal(profile)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", profile.CoverTaskID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "图片渠道不可用"}).Error; err != nil {
		t.Fatal(err)
	}
	profiles, err := s.ListStyleProfiles("user-1")
	if err != nil || len(profiles) != 1 || profiles[0].CoverStatus != "failed" {
		t.Fatal(err, profiles)
	}
	retry, err := s.GenerateStyleProfileCover("user-1", profile.ID, DirectorStyleSaveRequest{ImageTask: &template})
	if err != nil || retry.CoverTaskID == profile.CoverTaskID || retry.Revision != profile.Revision {
		t.Fatal("failed cover cannot retry independently", err, retry)
	}
	var textCount, styleCount, imageCount int64
	db.Model(&model.Task{}).Where("type = ?", "canvas_text").Count(&textCount)
	db.Model(&model.StyleProfile{}).Count(&styleCount)
	db.Model(&model.Task{}).Where("type = ?", "canvas_image").Count(&imageCount)
	if textCount != 0 || styleCount != 1 || imageCount != 2 {
		t.Fatal(textCount, styleCount, imageCount)
	}
}

func TestDirectorStylePaidCoverReservesOnceAndExplicitZeroBudgetStillRejects(t *testing.T) {
	s, db, project, template, calls := assetQueueFixture(t)
	policy := defaultRuntimePolicy()
	policy.Task.ActiveTaskLimit = 2
	if _, err := s.UpdateRuntimePolicySetting(&model.User{ID: "admin", Role: model.UserRoleAdmin}, policy); err != nil {
		t.Fatal(err)
	}
	config := template.Input["config"].(map[string]any)
	capability, _ := json.Marshal(DefaultModelCapabilityConfigForModel("openai-image", "queue-image"))
	for _, row := range []any{
		&model.User{ID: "user-1", Username: "cover-user", Status: model.UserStatusActive, Role: model.UserRoleUser},
		&model.ModelChannel{ID: "cover-channel", Scope: model.ChannelScopeSystem, Enabled: true, Name: "cover test", BaseURL: config["baseUrl"].(string)},
		&model.ChannelModel{ID: "cover-model", ChannelID: "cover-channel", ModelKey: "queue-image", Capability: "image", Protocol: model.ChannelInterfaceOpenAIImage, CapabilityConfigJSON: string(capability), Enabled: true, PriceConfigured: true, BillingMode: "fixed_request"},
		&model.ChannelModelPriceTier{ID: "cover-tier", ChannelModelID: "cover-model", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 1_000_000, Enabled: true, PriceConfigured: true},
		&model.CreditAccount{UserID: "user-1", AvailableMicrocredits: 5_000_000},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	config["channelId"] = "cover-channel"
	profile := lockDirectorFixture(t, s, project, nil)
	cover, err := s.GenerateStyleProfileCover("user-1", profile.ID, DirectorStyleSaveRequest{ImageTask: &template})
	if err != nil || cover.CoverTaskID == "" {
		t.Fatal("priced cover was rejected by an invented Agent budget", err, cover)
	}
	again, err := s.GenerateStyleProfileCover("user-1", profile.ID, DirectorStyleSaveRequest{ImageTask: &template})
	if err != nil || again.CoverTaskID != cover.CoverTaskID || calls.Load() != 0 {
		t.Fatal("admission duplicated a cover or called upstream", err)
	}
	var account model.CreditAccount
	if err := db.First(&account, "user_id = ?", "user-1").Error; err != nil {
		t.Fatal(err)
	}
	if account.AvailableMicrocredits != 4_000_000 || account.ReservedMicrocredits != 1_000_000 {
		t.Fatal("cover did not use ordinary credit reservation exactly once", account)
	}
	input, _ := dramaImageTaskInput(template)
	req := dramaImageTaskRequestOf(project.ID, template, "zero-budget test", input)
	req.admission = &taskAdmission{ID: "missing-budget-cover", AgentRunID: "agent-run"}
	if _, err := s.CreateTask("user-1", req); err == nil || !strings.Contains(err.Error(), "缺少本轮报价上限") {
		t.Fatal("Agent request without an explicit budget was accepted", err)
	}
	req.admission = &taskAdmission{ID: "zero-budget-cover", MaxCharge: ptr(int64(0))}
	if _, err := s.CreateTask("user-1", req); err == nil || !strings.Contains(err.Error(), "积分上限") {
		t.Fatal("explicit zero Agent budget was bypassed", err)
	}
	var tasks, orders int64
	db.Model(&model.Task{}).Where("type = ?", "canvas_image").Count(&tasks)
	db.Model(&model.BillingOrder{}).Where("user_id = ?", "user-1").Count(&orders)
	if tasks != 1 || orders != 1 {
		t.Fatal("duplicate billing or task after a rejected request", tasks, orders)
	}
}

func TestDirectorStyleLegacyMigrationPrivateAndIdempotent(t *testing.T) {
	s, db := newDramaWorkerTestService(t)
	output, _ := drama.StyleLockFromText(styleLockReply)
	anchor, _ := json.Marshal(output.Anchor)
	palette, _ := json.Marshal(output.Palette)
	for _, row := range []model.DramaStyleLibraryEntry{
		{ID: "mine-old", ScopeOwner: "user-1", CreatedBy: "user-1", Source: "director", ProjectType: drama.VisualLiveAction, Name: "旧个人导演风格", StyleAnchorJSON: string(anchor), MasterPaletteJSON: string(palette), CreatedAt: time.Now()},
		{ID: "other-old", ScopeOwner: "user-2", CreatedBy: "user-2", Source: "analysis", ProjectType: drama.VisualLiveAction, Name: "其他人的风格", StyleAnchorJSON: string(anchor), MasterPaletteJSON: string(palette)},
		{ID: "public-old", CreatedBy: "admin", Source: "manual", ProjectType: drama.VisualLiveAction, Name: "公共预置", StyleAnchorJSON: string(anchor), MasterPaletteJSON: string(palette)},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		mine, err := s.ListStyleProfiles("user-1")
		if err != nil || len(mine) != 1 || mine[0].SourceLibraryID != "mine-old" || mine[0].CoverURL != "" {
			t.Fatal("migration duplicates/leaks or misrepresents palette as generated cover", err, mine)
		}
	}
}

func TestDirectorStyleCoverWorkerRegistersImageAndProjectSnapshot(t *testing.T) {
	s, _, project, template, calls := assetQueueFixture(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"b64_json": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="}}})
	})
	profile := lockDirectorFixture(t, s, project, &template)
	completed, _ := runQueuedTask(t, s)
	if completed.ID != profile.CoverTaskID || completed.Status != model.TaskStatusSucceeded || calls.Load() != 1 {
		t.Fatal("cover worker did not finish through normal image protocol", completed.Status, completed.Error, calls.Load())
	}
	current, _ := s.repo.StyleProfileForUser("user-1", profile.ID)
	snapshot, _ := s.repo.ProjectForUser("user-1", project.ID)
	if current.CoverStatus != "ready" || current.CoverImageRevision != current.Revision || !strings.Contains(snapshot.StyleProfileJSON, current.CoverURL) {
		t.Fatal("image completion not persisted atomically", current, snapshot.StyleProfileJSON)
	}
	reused, err := s.CreateProject("user-1", CreateProjectRequest{Name: "复用我的导演风格", VisualType: drama.VisualLiveAction, Style: model.ProjectStyleSource{Mode: "profile", StyleID: profile.ID}})
	if err != nil || !strings.Contains(reused.StyleProfileJSON, current.CoverURL) {
		t.Fatal("personal style not reusable as a frozen snapshot", err)
	}
	profiles, err := s.ListStyleProfiles("user-1")
	if err != nil || len(profiles) != 1 || calls.Load() != 1 {
		t.Fatal("applying personal style reran analysis/cover or duplicated profile", err, len(profiles), calls.Load())
	}
}

func TestDirectorStyleLateAnalysisDoesNotOverwriteManualEdit(t *testing.T) {
	s, db, project, _, _ := assetQueueFixture(t)
	profile := lockDirectorFixture(t, s, project, nil)
	previous := profile.Revision
	var document map[string]any
	json.Unmarshal([]byte(profile.ProfileJSON), &document)
	document["title"] = "保留我的编辑"
	raw, _ := json.Marshal(document)
	edited, err := s.UpdateStyleProfile("user-1", profile.ID, StyleProfileRequest{ProfileJSON: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	document["prompt"] = "新分析的导演风格"
	raw, _ = json.Marshal(document)
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("style_profile_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProjectDirectorStyle("user-1", project.ID, DirectorStyleSaveRequest{ExpectedRevision: &previous}); err == nil {
		t.Fatal("late analysis overwrote manually edited personal profile")
	}
	current, _ := s.repo.StyleProfileForUser("user-1", profile.ID)
	if current.Name != edited.Name || current.Revision != edited.Revision {
		t.Fatal("manual edit lost", current)
	}
}
