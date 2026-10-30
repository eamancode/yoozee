package app

import (
	"errors"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// 预演闸门：每步都问我（auto）时没有选中的黑白预演就拒绝视频任务；选中预演、闸门关掉或跑到出视频前（auto）都放行，跑到出视频前但闸门 on 也拦。
func TestDramaPrevizGate(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	setProjectRunMode(t, db, project.ID, drama.RunEveryStep)
	shot := model.Shot{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, Title: "S01", Description: "段", Status: "draft"}
	if err := db.Create(&shot).Error; err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"mode": "video", "metadata": map[string]any{"domainProjectId": project.ID, "shotId": shot.ID}}
	gate := func() error { return svc.enforceDramaPrevizGate("user-1", input) }
	var appErr *AppError
	if err := gate(); !errors.As(err, &appErr) || appErr.Reason != ReasonPrevizRequired {
		t.Fatalf("每步都问我、没有预演：%v", err)
	}
	if err := svc.enforceDramaPrevizGate("user-1", map[string]any{"mode": "image", "metadata": map[string]any{"domainProjectId": project.ID, "shotId": shot.ID}}); err != nil {
		t.Fatalf("预演本身（图片任务）不拦：%v", err)
	}
	artifact := model.ShotArtifact{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, ShotID: shot.ID, Type: drama.ArtifactActionBoard, Version: 1, Status: "stale", Selected: true}
	if err := db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	if err := gate(); !errors.As(err, &appErr) {
		t.Fatalf("过期的预演不算：%v", err)
	}
	if err := db.Model(&artifact).Update("status", "ready").Error; err != nil {
		t.Fatal(err)
	}
	if err := gate(); err != nil {
		t.Fatalf("有选中的预演放行：%v", err)
	}
	if err := db.Model(&artifact).Update("selected", false).Error; err != nil {
		t.Fatal(err)
	}
	off := "off"
	if _, err := svc.UpdateProject("user-1", project.ID, UpdateProjectRequest{PrevizGate: &off}); err != nil {
		t.Fatal(err)
	}
	if err := gate(); err != nil {
		t.Fatalf("闸门关掉放行：%v", err)
	}
	full, auto, on := drama.RunUntilVideo, "auto", "on"
	if _, err := svc.UpdateProject("user-1", project.ID, UpdateProjectRequest{PrevizGate: &auto, RunMode: &full}); err != nil {
		t.Fatal(err)
	}
	if err := gate(); err != nil {
		t.Fatalf("跑到出视频前、auto 放行：%v", err)
	}
	updated, err := svc.UpdateProject("user-1", project.ID, UpdateProjectRequest{PrevizGate: &on})
	if err != nil {
		t.Fatal(err)
	}
	if err := gate(); !errors.As(err, &appErr) || appErr.Reason != ReasonPrevizRequired {
		t.Fatalf("跑到出视频前但闸门 on 照样拦：%v", err)
	}
	progress, err := svc.DramaProgress("user-1", project.ID)
	if err != nil || !progress.PrevizGate || progress.PrevizGateSetting != "on" || !strings.Contains(updated.BriefJSON, `"previz_gate":"on"`) {
		t.Fatalf("进度带闸门状态：%v %+v %s", err, progress.PrevizGate, updated.BriefJSON)
	}
}
