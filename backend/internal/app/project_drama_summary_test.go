package app

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestDramaSummariesReadOwnedProjectsAndListOthersAsMissing(t *testing.T) {
	service, db, project, unit := newProjectDramaTestService(t, "seedance-2.0")
	if _, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{dramaSpec("S01", 8, ""), dramaSpec("S02", 8, "")}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	other := model.Project{ID: "project-other", UserID: "user-2", Name: "别人的", Status: model.ProjectStatusActive, GenerationModel: "seedance-2.0", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}

	result, err := service.DramaSummaries("user-1", []string{project.ID, " ", project.ID, other.ID, "nope"})
	if err != nil {
		t.Fatal(err)
	}
	summary, found := result.Summaries[project.ID]
	if !found || len(result.Summaries) != 1 {
		t.Fatalf("只读自己的项目，重复 id 只算一次：%+v", result.Summaries)
	}
	if summary.Counts.Segments != 2 || summary.Episodes != 0 || summary.Chapters != 1 || summary.FailedImages != 0 || summary.FailedVideos != 0 || summary.RunningVideos != 0 {
		t.Fatalf("摘要计数不对：%+v", summary)
	}
	if fmt.Sprint(result.Missing) != "[project-other nope]" {
		t.Fatalf("别人的项目和不存在的项目都按缺失返回：%v", result.Missing)
	}
}

func TestDramaSummariesCountEpisodesAndChaptersSeparately(t *testing.T) {
	service, db, project, first := newProjectDramaTestService(t, "seedance-2.0")
	now := time.Now()
	units := []model.ProjectUnit{
		{ID: "chapter-2", ProjectID: project.ID, Kind: model.ProjectUnitKindChapter, Title: "第二章", Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now},
		{ID: "episode-1", ProjectID: project.ID, Kind: model.ProjectUnitKindEpisode, Title: "第1集", Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now},
		{ID: "episode-2", ProjectID: project.ID, Kind: model.ProjectUnitKindEpisode, Title: "第2集", Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&units).Error; err != nil {
		t.Fatal(err)
	}
	result, err := service.DramaSummaries("user-1", []string{project.ID})
	if err != nil {
		t.Fatal(err)
	}
	summary := result.Summaries[project.ID]
	if summary.Episodes != 2 || summary.Chapters != 2 {
		t.Fatalf("剧集和原文章节分开数（章节含 %s）：episodes=%d chapters=%d", first.ID, summary.Episodes, summary.Chapters)
	}
}

func TestDramaSummariesRejectTooManyIDs(t *testing.T) {
	service, _, _, _ := newProjectDramaTestService(t, "seedance-2.0")
	ids := make([]string, dramaSummaryLimit+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("p-%d", i)
	}
	if _, err := service.DramaSummaries("user-1", ids); err == nil {
		t.Fatal("超过一批上限要拒绝")
	}
}

func TestLatestVideoStepCountsOnlyLooksAtNewestRunPerShot(t *testing.T) {
	chains := []ProjectVideoChainView{
		// 最新：s1 重试成功，s2 正在出，s3 还在等
		{ID: "c3", Status: "running", Steps: []ProjectVideoChainStep{{ShotID: "s1", Status: "succeeded"}, {ShotID: "s2", Status: "running"}, {ShotID: "s3", Status: "waiting"}}},
		// 预览和只准备参考的不算
		{ID: "", Status: "preview", Steps: []ProjectVideoChainStep{{ShotID: "s4", Status: "failed"}}},
		{ID: "c2", Status: "succeeded", PrepareOnly: true, Steps: []ProjectVideoChainStep{{ShotID: "s5", Status: "failed"}}},
		// 旧的：s1 曾失败；s6 失败后没再出；s7 在失败的链里没轮到
		{ID: "c1", Status: "failed", Steps: []ProjectVideoChainStep{{ShotID: "s1", Status: "failed"}, {ShotID: "s6", Status: "failed"}, {ShotID: "s7", Status: "waiting"}}},
		// 取消的链里没出的段不算失败也不算进行中
		{ID: "c0", Status: "cancelled", Steps: []ProjectVideoChainStep{{ShotID: "s8", Status: "waiting"}}},
	}
	failed, running := latestVideoStepCounts(chains)
	if failed != 2 || running != 2 {
		t.Fatalf("失败 s6、s7，进行中 s2、s3：failed=%d running=%d", failed, running)
	}
}
