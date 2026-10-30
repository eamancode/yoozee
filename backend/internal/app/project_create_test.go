package app

import (
	"encoding/json"
	"errors"
	"image/color"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// newTestProject 按新建项合同建一个空项目（默认仿真人、交给导演分析），再按旧测试的需要补来源类型和画风快照
// （风格档案按画风对照表推，和改版前建项一致）。
func newTestProject(t *testing.T, svc *Service, db *gorm.DB, userID string, req CreateProjectRequest, sourceType string, stylePresetID string, styleProfileJSON string) model.Project {
	t.Helper()
	if req.VisualType == "" {
		req.VisualType = drama.VisualLiveAction
	}
	project, err := svc.CreateProject(userID, req)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := drama.RecommendProfile(stylePresetID, styleProfileJSON, "")
	updates := map[string]any{"source_type": sourceType, "style_preset_id": stylePresetID, "style_profile_json": styleProfileJSON, "style_profile": profile}
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Updates(updates).Error; err != nil {
		t.Fatal(err)
	}
	project.SourceType, project.StylePresetID, project.StyleProfileJSON, project.StyleProfile = sourceType, stylePresetID, styleProfileJSON, profile
	return project
}

func appErrorReason(err error) ErrorReason {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Reason
	}
	return ""
}

// 建项各段的校验：错误按段给 reason，页面据此把错误落到对应的段。
func TestCreateProjectValidatesBySection(t *testing.T) {
	svc, _ := newProjectSetupTestService(t)
	cases := []struct {
		name string
		req  CreateProjectRequest
		want ErrorReason
	}{
		{"没选画面类型", CreateProjectRequest{Name: "a", SourceAs: ProjectSourceBlank}, ReasonProjectVisualTypeRequired},
		{"画面类型不认识", CreateProjectRequest{Name: "b", SourceAs: ProjectSourceBlank, VisualType: "live-action"}, ReasonProjectVisualTypeRequired},
		{"来源不认识", CreateProjectRequest{Name: "c", SourceAs: "text", VisualType: drama.VisualLiveAction}, ReasonProjectSourceInvalid},
		{"创意没写", CreateProjectRequest{Name: "d", SourceAs: ProjectSourceIdea, VisualType: drama.VisualLiveAction, IdeaPlan: &ProjectIdeaPlan{Episodes: 5, EpisodeSeconds: 60}}, ReasonProjectSourceInvalid},
		{"创意集数超了", CreateProjectRequest{Name: "e", SourceAs: ProjectSourceIdea, Source: ProjectSourceRequest{Text: "失忆的快递员"}, VisualType: drama.VisualLiveAction, IdeaPlan: &ProjectIdeaPlan{Episodes: 99, EpisodeSeconds: 60}}, ReasonProjectSourceInvalid},
		{"小说没有章", CreateProjectRequest{Name: "f", SourceAs: ProjectSourceNovel, VisualType: drama.VisualLiveAction}, ReasonProjectSourceInvalid},
		{"风格库条目不存在", CreateProjectRequest{Name: "g", SourceAs: ProjectSourceBlank, VisualType: drama.VisualLiveAction, Style: model.ProjectStyleSource{Mode: model.ProjectStyleLibrary, StyleID: "nope"}}, ReasonProjectStyleInvalid},
		{"画风来源不认识", CreateProjectRequest{Name: "h", SourceAs: ProjectSourceBlank, VisualType: drama.VisualLiveAction, Style: model.ProjectStyleSource{Mode: "preset"}}, ReasonProjectStyleInvalid},
		{"参考图不是本人的", CreateProjectRequest{Name: "i", SourceAs: ProjectSourceBlank, VisualType: drama.VisualLiveAction, Style: model.ProjectStyleSource{Mode: model.ProjectStyleDirector, RefImageIDs: []string{"missing"}}}, ReasonProjectStyleInvalid},
		{"画幅不认识", CreateProjectRequest{Name: "j", SourceAs: ProjectSourceBlank, VisualType: drama.VisualLiveAction, AspectRatio: "21:9"}, ReasonProjectProductionInvalid},
		{"30 秒配 15 秒模型", CreateProjectRequest{Name: "k", SourceAs: ProjectSourceBlank, VisualType: drama.VisualLiveAction, GenerationModel: "seedance-2.0", SegmentSeconds: 30}, ReasonProjectProductionInvalid},
		{"生成模式是旧档位", CreateProjectRequest{Name: "l", SourceAs: ProjectSourceBlank, VisualType: drama.VisualLiveAction, RunMode: "full"}, ReasonProjectProductionInvalid},
	}
	for _, c := range cases {
		_, err := svc.CreateProject("user-a", c.req)
		if err == nil || appErrorReason(err) != c.want {
			t.Fatalf("%s：应按 %s 拒绝，得到 %v（%s）", c.name, c.want, err, appErrorReason(err))
		}
	}
}

// 带 authMode 的请求不再有任何效果：字段已删，生成模式按 runMode（不给是跑到出视频前）。
func TestCreateProjectIgnoresAuthMode(t *testing.T) {
	svc, _ := newProjectSetupTestService(t)
	var req CreateProjectRequest
	if err := json.Unmarshal([]byte(`{"name":"旧字段","sourceAs":"blank","visualType":"anime_3d","authMode":"step"}`), &req); err != nil {
		t.Fatal(err)
	}
	project, err := svc.CreateProject("user-a", req)
	if err != nil {
		t.Fatal(err)
	}
	if project.RunMode != drama.RunUntilVideo || project.VisualType != drama.VisualAnime3D || project.StyleProfile != "anime-3d" {
		t.Fatalf("authMode 不该有效果，类型推出风格档案：%+v", project)
	}
	if project.StyleSource.Mode != model.ProjectStyleDirector || project.AspectRatio != "9:16" || briefSegmentSeconds(project.BriefJSON) != 15 {
		t.Fatalf("默认值：画风交给导演分析、9:16、每段 15 秒：%+v %s", project.StyleSource, project.BriefJSON)
	}
}

// 小说的章、剧本的集随建项写进项目；创意把集数和每集时长写进需求文档。
func TestCreateProjectWritesSource(t *testing.T) {
	svc, _ := newProjectSetupTestService(t)
	units := []CreateProjectUnitRequest{{Title: "第1集", SourceText: "<p>第1场</p>"}, {Title: "第2集", SourceText: "<p>第2场</p>"}}
	script, err := svc.CreateProject("user-a", CreateProjectRequest{Name: "剧本", SourceAs: ProjectSourceScript, Source: ProjectSourceRequest{Units: units}, VisualType: drama.VisualLiveAction})
	if err != nil {
		t.Fatal(err)
	}
	written, err := svc.repo.ProjectUnits(script.ID)
	if err != nil || len(written) != 2 || written[0].Kind != model.ProjectUnitKindEpisode || script.SourceType != ProjectSourceScript {
		t.Fatalf("剧本导入成剧集：%v %+v", err, written)
	}
	novel, err := svc.CreateProject("user-a", CreateProjectRequest{Name: "小说", SourceAs: ProjectSourceNovel, Source: ProjectSourceRequest{Units: units}, VisualType: drama.VisualLiveAction})
	if err != nil {
		t.Fatal(err)
	}
	if written, _ = svc.repo.ProjectUnits(novel.ID); len(written) != 2 || written[0].Kind != model.ProjectUnitKindChapter {
		t.Fatalf("小说导入成原文章节：%+v", written)
	}
	idea, err := svc.CreateProject("user-a", CreateProjectRequest{Name: "创意", SourceAs: ProjectSourceIdea, Source: ProjectSourceRequest{Text: "一个失忆的快递员每天收到十年前的信"}, IdeaPlan: &ProjectIdeaPlan{Episodes: 5, EpisodeSeconds: 60}, VisualType: drama.VisualLiveAction})
	if err != nil {
		t.Fatal(err)
	}
	if idea.Description != "一个失忆的快递员每天收到十年前的信" || briefEpisodeCount(idea.BriefJSON) != 5 || briefEpisodeSeconds(idea.BriefJSON) != 60 {
		t.Fatalf("创意写进简介和需求文档：%q %s", idea.Description, idea.BriefJSON)
	}
}

// 从风格中心挑的风格：建项时直接锁定（不跑风格分析），类型不对时拒绝；参考图要是本人已就绪的图片。
func TestCreateProjectWithLibraryStyleLocksImmediately(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	entry := seedStyleLibraryEntry(t, svc, "admin", "冷调悬疑")
	if _, err := svc.CreateProject("user-1", CreateProjectRequest{Name: "动漫", SourceAs: ProjectSourceBlank, VisualType: drama.VisualAnime2D, Style: model.ProjectStyleSource{Mode: model.ProjectStyleLibrary, StyleID: entry.ID}}); appErrorReason(err) != ReasonProjectStyleInvalid {
		t.Fatalf("风格只适用于仿真人，2D 动漫建项应拒绝：%v", err)
	}
	project, err := svc.CreateProject("user-1", CreateProjectRequest{Name: "仿真人", SourceAs: ProjectSourceBlank, VisualType: drama.VisualLiveAction, Style: model.ProjectStyleSource{Mode: model.ProjectStyleLibrary, StyleID: entry.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, record, locked := projectLockedStyle(project.BibleJSON); !locked || record.Source != drama.StyleLockSourceLibrary || record.LibraryID != entry.ID {
		t.Fatalf("建项时直接锁定所选风格：%v %+v", locked, record)
	}
	var tasks int64
	db.Model(&model.Task{}).Count(&tasks)
	if tasks != 0 {
		t.Fatalf("从风格中心挑的不跑风格分析：%d 个任务", tasks)
	}
	ref := uploadTestImage(t, svc, "user-1", color.White)
	director, err := svc.CreateProject("user-1", CreateProjectRequest{Name: "导演", SourceAs: ProjectSourceBlank, VisualType: drama.VisualLiveAction, Style: model.ProjectStyleSource{Mode: model.ProjectStyleDirector, Brief: "冷调", RefImageIDs: []string{ref, ref}}})
	if err != nil || director.StyleSource.Brief != "冷调" || len(director.StyleSource.RefImageIDs) != 1 {
		t.Fatalf("交给导演分析：描述和参考图（去重）存进画风来源：%v %+v", err, director.StyleSource)
	}
}
