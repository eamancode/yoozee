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
