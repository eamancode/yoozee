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

func TestArchivedProjectRejectsEveryEpisodeMutation(t *testing.T) {
	r := storyboardTaskTestRepo(t)
	if err := r.db.Model(&model.Project{}).Where("id = ?", "project").Update("status", model.ProjectStatusArchived).Error; err != nil {
		t.Fatal(err)
	}
	existing, err := r.ProjectUnit("project", "episode")
	if err != nil {
		t.Fatal(err)
	}
	newUnit := model.ProjectUnit{ID: "new", ProjectID: "project", Kind: model.ProjectUnitKindEpisode}
	edits := []struct {
		name string
		run  func() error
	}{
		{"create", func() error { return r.CreateProjectUnit("user", &newUnit, nil) }},
		{"import", func() error { return r.ImportProjectUnits("user", "project", []model.ProjectUnit{newUnit}) }},
		{"update", func() error { return r.UpdateProjectUnit("user", existing, true) }},
		{"reorder", func() error { return r.ReorderProjectUnits("user", "project", []string{"episode"}) }},
		{"delete", func() error { return r.DeleteProjectUnit("user", "project", "episode") }},
		{"director busy", func() error { return r.CheckProjectUnitDeletion("user", "project", "episode") }},
		{"replace", func() error {
			return r.ApplyDramaEpisodes("user", "project", "adapt", []string{"episode"}, []model.ProjectUnit{newUnit}, nil, []string{"new"}, nil)
		}},
	}
	for _, edit := range edits {
		t.Run(edit.name, func(t *testing.T) {
			if err := edit.run(); !errors.Is(err, ErrProjectNotActive) {
				t.Fatalf("archived project write: %v", err)
			}
		})
	}
	units, err := r.ProjectUnits("project")
	if err != nil || len(units) != 1 || units[0].ID != existing.ID {
		t.Fatalf("units after rejected mutations: %+v, err=%v", units, err)
	}
}
