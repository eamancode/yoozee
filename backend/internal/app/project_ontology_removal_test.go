package app

import (
	"errors"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 整集替换删旧段：事务里逐段复核产物后立即删除。有未确认产物、或确认后产物增加的段让整个删除回滚，一段都不删。
func TestOntologyRemovalRechecksArtifactsInsideTransaction(t *testing.T) {
	svc, db := newChapterShotsTestService(t)
	now := time.Now()
	project := model.Project{ID: "project-removal", UserID: "user-1", Name: "删段", Status: model.ProjectStatusActive, GenerationModel: "seedance-2.5", WorkflowID: drama.WorkflowOntology, WorkflowVersion: drama.WorkflowVersion, Revision: 1, CreatedAt: now, UpdatedAt: now}
	unit := model.ProjectUnit{ID: "unit-removal", ProjectID: project.ID, Kind: model.ProjectUnitKindChapter, Title: "第一集", Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	shots := []model.Shot{
		{ID: "shot-plain", ProjectID: project.ID, UnitID: unit.ID, Title: "S04", Position: 3, CreatedAt: now, UpdatedAt: now},
		{ID: "shot-video", ProjectID: project.ID, UnitID: unit.ID, Title: "S05", Position: 4, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&shots).Error; err != nil {
		t.Fatal(err)
	}
	addVideo := func(version int) {
		t.Helper()
		artifact := model.ShotArtifact{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, ShotID: "shot-video", Type: drama.ArtifactVideo, Version: version, Status: "ready", MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&artifact).Error; err != nil {
			t.Fatal(err)
		}
	}
	addVideo(1)
	removed := []string{"shot-plain", "shot-video"}
	exists := func(id string) bool {
		var count int64
		db.Model(&model.Shot{}).Where("id = ?", id).Count(&count)
		return count > 0
	}
	required := func(err error) bool {
		var appErr *AppError
		return errors.As(err, &appErr) && appErr.Status == 409
	}

	// 没确认过整集替换：带视频的段拦下，没产物的段也一起回滚。
	if err := svc.deleteOntologyRemovedShots(project.ID, unit.ID, removed, &dramaOntologyMeta{}); !required(err) {
		t.Fatalf("unconfirmed artifacts must block: %v", err)
	}
	if !exists("shot-plain") || !exists("shot-video") {
		t.Fatal("a blocked removal must roll back every deletion")
	}
	// 确认时只有 1 个视频，之后又多了 1 个：仍然拦下。
	addVideo(2)
	if err := svc.deleteOntologyRemovedShots(project.ID, unit.ID, removed, &dramaOntologyMeta{Discard: true, Confirmed: map[string]int{"shot-video": 1}}); !required(err) {
		t.Fatalf("artifacts added after confirmation must block: %v", err)
	}
	if !exists("shot-plain") || !exists("shot-video") {
		t.Fatal("a blocked removal must roll back every deletion")
	}
	// 确认数与现在一致：两段都删。
	if err := svc.deleteOntologyRemovedShots(project.ID, unit.ID, removed, &dramaOntologyMeta{Discard: true, Confirmed: map[string]int{"shot-video": 2}}); err != nil {
		t.Fatal(err)
	}
	if exists("shot-plain") || exists("shot-video") {
		t.Fatal("confirmed removal must delete both shots")
	}
	var left int64
	db.Model(&model.ShotArtifact{}).Where("shot_id = ?", "shot-video").Count(&left)
	if left != 0 {
		t.Fatalf("artifacts of the deleted shot remain: %d", left)
	}
	// 段删掉之后才完成的生成任务：登记产物时看到镜头不在，放弃，不留孤立产物。
	late := model.ShotArtifact{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, ShotID: "shot-video", TaskID: "late-task", Type: drama.ArtifactVideo, Status: "ready", MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := svc.repo.RegisterTaskOutputRecords(project.ID, nil, nil, &late); !errors.Is(err, repository.ErrShotGone) {
		t.Fatalf("registering an artifact for a deleted shot must fail with ErrShotGone: %v", err)
	}
	if err := svc.repo.CreateShotArtifact(&late); !errors.Is(err, repository.ErrShotGone) {
		t.Fatalf("CreateShotArtifact for a deleted shot must fail with ErrShotGone: %v", err)
	}
	db.Model(&model.ShotArtifact{}).Where("shot_id = ?", "shot-video").Count(&left)
	if left != 0 {
		t.Fatalf("orphan artifact written: %d", left)
	}
}
