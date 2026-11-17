package repository

import (
	"errors"
	"slices"
	"testing"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
)

func TestEpisodeReplacementExcludesOnlyItsOwnTask(t *testing.T) {
	r := storyboardTaskTestRepo(t)
	if err := r.db.AutoMigrate(database.Models()...); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.Task{ID: "adapt", UserID: "user", ProjectID: "project", Type: "canvas_text", Status: model.TaskStatusRunning, InputJSON: `{"metadata":{"source":"short-drama-script-adapt","domainProjectId":"project","dramaAdapt":{"replaceUnitIds":["episode"]}}}`},
		&model.Task{ID: "ended-parent", UserID: "user", ProjectID: "project", Type: model.TaskTypeProjectVideoChain, Status: model.TaskStatusCancelled, InputJSON: `{"unitId":"episode","steps":[]}`},
		&model.Task{ID: "original-child", UserID: "user", ProjectID: "project", Type: "canvas_video", Status: model.TaskStatusRunning, InputJSON: `{"_projectVideoChain":{"controllerId":"ended-parent"}}`},
		&model.Task{ID: "audit", UserID: "user", ProjectID: "detached-canvas", Type: model.TaskTypeTimelineTranscription, Status: model.TaskStatusQueued, InputJSON: `{}`},
		&model.Task{ID: "resource-only", UserID: "user", ProjectID: "canvas", Type: model.TaskTypeTimelineTranscription, Status: model.TaskStatusQueued, InputJSON: `{"resourceId":"resource"}`},
		&model.ShotEvidence{ID: "measure", ProjectID: "project", ShotID: "target", ResourceID: "resource", TaskID: "audit"},
	} {
		if err := r.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	apply := func() error {
		return r.ApplyDramaEpisodes("user", "project", "adapt", []string{"episode"}, nil, nil, nil, nil)
	}
	var conflict *EpisodeActiveTasksError
	if err := apply(); !errors.As(err, &conflict) || conflict.UnitID != "episode" {
		t.Fatalf("expected scoped replacement conflict, got %v", err)
	}
	var ids []string
	for _, task := range conflict.Tasks {
		ids = append(ids, task.ID)
	}
	if !slices.Equal(ids, []string{"audit", "original-child"}) {
		t.Fatalf("unexpected blocking tasks: %v", ids)
	}
	if err := r.db.Model(&model.Task{}).Where("id IN ?", ids).Update("status", model.TaskStatusCancelled).Error; err != nil {
		t.Fatal(err)
	}
	if err := apply(); err != nil {
		t.Fatalf("replacement blocked by itself or unrelated resource task: %v", err)
	}
	for _, row := range []any{&model.ProjectUnit{}, &model.Shot{}, &model.ShotEvidence{}} {
		var count int64
		if err := r.db.Model(row).Where("project_id = ?", "project").Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%T remains after replacement: count=%d err=%v", row, count, err)
		}
	}
}
