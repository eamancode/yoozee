package app

import (
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// 所有生成模式及历史预演偏好均不再强制阻止视频提交。
func TestDramaPrevizGate(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	shot := model.Shot{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, Title: "S01", Status: "draft"}
	if err := db.Create(&shot).Error; err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"mode": "video", "metadata": map[string]any{"domainProjectId": project.ID, "shotId": shot.ID}}
	for _, mode := range drama.RunModes {
		for _, setting := range []string{"auto", "on", "off"} {
			if _, err := svc.UpdateProject("user-1", project.ID, UpdateProjectRequest{RunMode: &mode, PrevizGate: &setting}); err != nil {
				t.Fatal(err)
			}
			if err := svc.enforceDramaPrevizGate("user-1", input); err != nil {
				t.Fatalf("optional previs blocked %s/%s: %v", mode, setting, err)
			}
		}
	}
	artifact := model.ShotArtifact{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, ShotID: shot.ID, Type: drama.ArtifactActionBoard, Version: 1, Status: "stale", Selected: true}
	if err := db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.enforceDramaPrevizGate("user-1", input); err != nil {
		t.Fatal("stale optional previs blocked video", err)
	}
	progress, err := svc.DramaProgress("user-1", project.ID)
	if err != nil || progress.PrevizGate {
		t.Fatal("progress still advertises hard gate", err)
	}
}
