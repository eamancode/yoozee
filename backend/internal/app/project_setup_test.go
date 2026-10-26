package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
)

func newProjectSetupTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db := newSQLiteTestDB(t)
	if err := db.AutoMigrate(database.Models()...); err != nil {
		t.Fatal(err)
	}
	return &Service{repo: repository.New(db)}, db
}

func TestProjectSetupAspectRatioAndSegmentSeconds(t *testing.T) {
	svc, db := newProjectSetupTestService(t)
	if _, err := svc.CreateProject("user-a", CreateProjectRequest{Name: "宽银幕", AspectRatio: "21:9"}); err == nil {
		t.Fatal("不支持的画幅应被拒绝")
	}
	if _, err := svc.CreateProject("user-a", CreateProjectRequest{Name: "三十秒", GenerationModel: "seedance-2.0", SegmentSeconds: 30}); err == nil || !strings.Contains(err.Error(), "15") {
		t.Fatalf("单段 30 秒超过 Seedance 2.0 的上限应被拒绝，得到 %v", err)
	}
	project, err := svc.CreateProject("user-a", CreateProjectRequest{Name: "四比三", AspectRatio: "4:3", GenerationModel: "seedance-2.5", SegmentSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	if project.AspectRatio != "4:3" || briefSegmentSeconds(project.BriefJSON) != 30 {
		t.Fatalf("应保存 4:3 并把 30 秒写进需求文档：%s %s", project.AspectRatio, project.BriefJSON)
	}

	// 项目设置改小：写回需求文档，其他字段不动。
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("brief_json", `{"title":"四比三","segment_seconds":30}`).Error; err != nil {
		t.Fatal(err)
	}
	fifteen := 15
	updated, err := svc.UpdateProject("user-a", project.ID, UpdateProjectRequest{SegmentSeconds: &fifteen})
	if err != nil || briefSegmentSeconds(updated.BriefJSON) != 15 || !strings.Contains(updated.BriefJSON, `"title":"四比三"`) {
		t.Fatalf("改单段秒数应只改这一项：%s err=%v", updated.BriefJSON, err)
	}
	// 换成一段最长 15 秒的模型时，已存的 15 秒还能用；再改回 30 秒就超了。
	model20 := "seedance-2.0"
	if _, err := svc.UpdateProject("user-a", project.ID, UpdateProjectRequest{GenerationModel: &model20}); err != nil {
		t.Fatal(err)
	}
	thirty := 30
	if _, err := svc.UpdateProject("user-a", project.ID, UpdateProjectRequest{SegmentSeconds: &thirty}); err == nil {
		t.Fatal("单段秒数不能超过项目生成模型的上限")
	}
	zero := 0
	cleared, err := svc.UpdateProject("user-a", project.ID, UpdateProjectRequest{SegmentSeconds: &zero})
	if err != nil || briefSegmentSeconds(cleared.BriefJSON) != 0 || !strings.Contains(cleared.BriefJSON, "四比三") {
		t.Fatalf("0 应去掉单段秒数并保留其他字段：%s err=%v", cleared.BriefJSON, err)
	}
	// 模型上限比已存的单段秒数小时，换模型要拒绝。
	if _, err := svc.UpdateProject("user-a", project.ID, UpdateProjectRequest{SegmentSeconds: &fifteen}); err != nil {
		t.Fatal(err)
	}
	model25 := "seedance-2.5"
	if _, err := svc.UpdateProject("user-a", project.ID, UpdateProjectRequest{GenerationModel: &model25, SegmentSeconds: &thirty}); err != nil {
		t.Fatalf("同时换模型和秒数应按新模型校验：%v", err)
	}
	if _, err := svc.UpdateProject("user-a", project.ID, UpdateProjectRequest{GenerationModel: &model20}); err == nil {
		t.Fatal("已存 30 秒时换成一段最长 15 秒的模型应被拒绝")
	}
}
