package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

func TestDramaFlow2SuixingxianDraftAndSceneOwnership(t *testing.T) {
	svc, fake := newDramaTestService(t)
	script, err := os.ReadFile("../drama/testdata/suixingxian.txt")
	if err != nil {
		t.Fatal(err)
	}
	source := strings.TrimSpace(strings.TrimPrefix(strings.ReplaceAll(string(script), "\r\n", "\n"), "\ufeff"))
	var chapters []DramaChapterInput
	for _, line := range strings.Split(source, "\n") {
		if strings.HasPrefix(line, "第") && strings.Contains(line, "场｜") {
			chapters = append(chapters, DramaChapterInput{Title: line})
		} else if len(chapters) > 0 {
			chapter := &chapters[len(chapters)-1]
			chapter.Text += line + "\n"
		}
	}
	if len(chapters) != 8 {
		t.Fatalf("随行鲜剧本场数 = %d", len(chapters))
	}
	project, err := svc.CreateDrama("usr-drama", CreateDramaRequest{
		Name: "随行鲜", SourceFormat: "script", Chapters: chapters, Draft: &DramaDraftForm{SourceText: source},
		Settings: DramaSettings{Models: fakeDramaModels, MediaMode: DramaMediaManual, ReviewMode: DramaReviewAuto},
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := svc.DramaDraft("usr-drama", project.Project.ID)
	if err != nil || draft.Form.SourceText != source {
		t.Fatalf("draft source not retained: %v", err)
	}
	if len(project.Episodes) != len(chapters) {
		t.Fatalf("expected %d episodes, got %d", len(chapters), len(project.Episodes))
	}
	for index, chapter := range chapters {
		if project.Episodes[index].Number != index+1 {
			t.Fatalf("wrong episode order for %s", chapter.Title)
		}
	}
	if len(fake.requests) != 0 {
		t.Fatalf("saving a draft submitted %d jobs", len(fake.requests))
	}

	// 仅制作第一场，其余七场保留。模型返回本场的固定数据，让测试同时覆盖原文选章和真实制作状态机。
	fake.textFixture = func(system string) string {
		switch {
		case strings.Contains(system, "尚未写清外貌的新角色"):
			return `{"characters":[],"props":[],"scenes":[]}`
		case strings.Contains(system, "作品圣经"):
			return `{"logline":"雨夜热橙汁","characters":[{"name":"林深","appearance":"湿透的半大孩子"},{"name":"阿婆","appearance":"围裙上的橙子皮"}],"props":[{"name":"旧保温杯","appearance":"杯盖裂缝"}],"scenes":[{"name":"大学城后街","appearance":"雨中的小吃摊"}]}`
		case strings.Contains(system, "整段视频提示词"):
			return "第一段：大学城后街的暴雨中，暖黄灯泡照着小吃摊，阿婆将掉了漆的保温杯递给林深。镜头停在两人握杯的手上，跟随他的视线抬向摊前的灯，再缓慢拉开。"
		default:
			return `{"shots":[{"scene":"大学城后街","characters":["林深","阿婆"],"props":["旧保温杯"],"speaker":"林深","dialogue":"谢谢阿婆。","action":"林深捧住保温杯","imagePrompt":"雨中暖黄的推车灯","videoPrompt":"阿婆把杯子递给林深","durationSeconds":5}]}`
		}
	}
	settings := DramaSettings{Models: fakeDramaModels, MediaMode: DramaMediaAuto, ReviewMode: DramaReviewEvery, EpisodeMode: DramaEpisodeModeChapter, ChaptersPerEpisode: 1}
	settings.EpisodeSeconds = 60
	quote, err := svc.EstimateDrama("usr-drama", DramaEstimateRequest{Settings: settings, WordCount: len([]rune(source)), ChapterCount: len(chapters)})
	if err != nil || quote.Episodes != 8 || quote.Videos <= 0 || len(fake.requests) > 0 {
		t.Fatalf("quote=%+v err=%v requests=%d", quote, err, len(fake.requests))
	}
	active, err := svc.SaveDramaDraft("usr-drama", project.Project.ID, CreateDramaRequest{
		Name: "随行鲜", SourceFormat: "script", Start: true, Chapters: chapters, SelectedChapters: []int{0}, Settings: settings,
	})
	if err != nil || len(active.Episodes) != 1 {
		t.Fatalf("select first scene: episodes=%d err=%v", len(active.Episodes), err)
	}
	id, episode := active.Project.ID, active.Episodes[0].ID
	runDramaTicks(t, svc, id, 15)
	cast, err := svc.repo.DramaCasts(id)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, item := range cast {
		got[item.Name] = item.Kind
	}
	if got["林深"] != "character" || got["阿婆"] != "character" || got["旧保温杯"] != "prop" || got["大学城后街"] != "scene" || fake.byKind["video"] != 0 {
		t.Fatalf("before approval: assets=%v mocked videos=%d", got, fake.byKind["video"])
	}
	if _, err := svc.ApproveDramaEpisode("usr-drama", id, episode); err != nil {
		t.Fatal(err)
	}
	runDramaTicks(t, svc, id, 15)
	segments, err := svc.repo.DramaSegments(episode)
	if err != nil || len(segments) != 1 || segments[0].VideoResourceID == "" || len(fake.renders) != 1 {
		t.Fatalf("approved scene: segments=%v render=%d err=%v", segments, len(fake.renders), err)
	}
	t.Run("导出含原视频的剪映草稿", func(t *testing.T) {
		// 后端独立分发时不包含工作区内已跟踪的前端示例视频。
		media, err := os.ReadFile("../../../web/public/doll/mascot-wave.mp4")
		if os.IsNotExist(err) {
			t.Skip("当前检出未包含前端示例媒体")
		}
		if err != nil {
			t.Fatal(err)
		}
		svc.dataDir = t.TempDir()
		path := filepath.Join(svc.dataDir, "resources")
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "sample.mp4"), media, 0600); err != nil {
			t.Fatal(err)
		}
		if err := fake.db.Model(&model.Resource{}).Where("id = ?", segments[0].VideoResourceID).Updates(map[string]any{"object_key": "sample.mp4", "provider": "local", "size": len(media), "duration_ms": 5000}).Error; err != nil {
			t.Fatal(err)
		}
		export := DramaJianyingDraftRequest{DraftRoot: "/Users/editor/Drafts", Captions: true}
		preview, err := svc.PreviewDramaJianyingDraft("usr-drama", id, episode, export)
		if err != nil || preview.AvailableSegments != 1 || preview.CaptionCount != 1 {
			t.Fatalf("preview=%+v err=%v", preview, err)
		}
		export.Revision = preview.Revision
		archive, err := svc.PrepareDramaJianyingDraft("usr-drama", id, episode, export)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := svc.WriteDramaArchive(t.Context(), "usr-drama", archive, &output); err != nil {
			t.Fatal(err)
		}
		reader, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range reader.File {
			if !strings.HasSuffix(file.Name, ".mp4") {
				continue
			}
			stream, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			data, err := io.ReadAll(stream)
			if err != nil || !bytes.Equal(data, media) {
				t.Fatalf("exported media mismatch: %v", err)
			}
			return
		}
		t.Fatal("draft archive missing the reviewed movie")
	})
}

func TestDramaFlow2PreviewAccessIsScoped(t *testing.T) {
	svc, fake := newDramaTestService(t)
	t.Setenv("YOOZEE_DRAMA_FLOW2_PREVIEW_USERS", "tester, usr-drama")
	if err := fake.db.Save(&model.SystemSetting{Key: featureAvailabilitySettingKey, ValueJSON: `{"dramaFlow2Enabled":false}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.requireDramaFlow2("usr-drama"); err != nil {
		t.Fatal(err)
	}
	if err := svc.requireDramaFlow2("usr"); err == nil {
		t.Fatal("partial account ID matched")
	}
	production := createTestDrama(t, svc, DramaReviewAuto)
	runDramaTicks(t, svc, production.Project.ID, 1)
	if len(fake.requests) == 0 {
		t.Fatal("preview user's job was not dispatched")
	}
	if err := fake.db.Save(&model.SystemSetting{Key: featureAvailabilitySettingKey, ValueJSON: `{"shortDramaEnabled":false,"dramaFlow2Enabled":true}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.requireDramaFlow2("usr-drama"); err == nil {
		t.Fatal("preview bypassed short-drama feature")
	}
}

func TestDramaFlow2ProjectIsolation(t *testing.T) {
	svc, fake := newDramaTestService(t)
	const owner = "usr-drama"
	official := createTestDrama(t, svc, DramaReviewAuto).Project.ID
	legacy := model.Project{ID: newID(), UserID: owner, Name: "随行鲜旧工作台", Type: "short-drama", Status: model.ProjectStatusActive}
	if err := fake.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	workbench, err := svc.ListProjectsPage(owner, 1, 1)
	if err != nil || workbench.Total != 1 || len(workbench.Projects) != 1 || workbench.Projects[0].Project.ID != legacy.ID {
		t.Fatalf("workbench page=%#v err=%v", workbench, err)
	}
	projects, count, err := svc.repo.DramaProjectsPage(owner, "", "", 1, 1)
	if err != nil || count != 1 || len(projects) != 1 || projects[0].ID != official {
		t.Fatalf("official projects=%v total=%d err=%v", projects, count, err)
	}
	if _, err := svc.UpdateProject(owner, official, UpdateProjectRequest{Name: "错误修改"}); err == nil {
		t.Fatal("workbench mutated an official project")
	}
	if err := svc.DeleteProject(owner, official); err == nil {
		t.Fatal("workbench deleted an official project")
	}
	if _, err := svc.SetDramaRunning(owner, legacy.ID, true); err == nil {
		t.Fatal("official scheduler accepted a legacy project")
	}
	if _, err := svc.SetDramaRunning("another-user", official, true); err == nil {
		t.Fatal("official scheduler accepted another user's project")
	}
	if _, err := svc.ProjectAssets(owner, official); err != nil {
		t.Fatalf("shared project assets: %v", err)
	}
}

func TestDramaFlow2GateStopsNewWork(t *testing.T) {
	svc, fake := newDramaTestService(t)
	id := createTestDrama(t, svc, DramaReviewAuto).Project.ID
	if err := fake.db.Save(&model.SystemSetting{Key: featureAvailabilitySettingKey, ValueJSON: `{"dramaFlow2Enabled":false}`}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateDrama("usr-drama", CreateDramaRequest{Name: "随行鲜新项目"}); err == nil {
		t.Fatal("disabled feature accepted new project")
	}
	if _, err := svc.SetDramaRunning("usr-drama", id, true); err == nil {
		t.Fatal("disabled feature accepted start")
	}
	runDramaTicks(t, svc, id, 2)
	if len(fake.requests) != 0 {
		t.Fatalf("disabled feature submitted %d jobs", len(fake.requests))
	}
}

func TestDramaFlow2IgnoresHistoricalMediaFailures(t *testing.T) {
	svc, _ := newDramaTestService(t)
	svc.dataDir = t.TempDir()
	svc.dramaSchedulerStarted = time.Now()
	checkpoint := mediaCheckpoint{Mode: "image", Items: []mediaCheckpointItem{{Reference: protocol.MediaReference{URL: "https://example.invalid/frame.png"}}}, StartedAt: time.Now()}
	value, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := svc.encryptSettingSecret(string(value))
	if err != nil {
		t.Fatal(err)
	}
	task := &model.Task{Status: model.TaskStatusFailed, UpdatedAt: svc.dramaSchedulerStarted, MediaRecoveryJSON: secret}
	if !svc.dramaTaskMediaRecoveryRetryable(task) {
		t.Fatal("current run lost its valid download checkpoint")
	}
	task.UpdatedAt = svc.dramaSchedulerStarted.Add(-time.Minute)
	if svc.dramaTaskMediaRecoveryRetryable(task) {
		t.Fatal("requeued a failed task from an earlier release")
	}
}
