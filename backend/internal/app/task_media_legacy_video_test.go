package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/platform"
)

func TestMediaTaskProjectFollowsTaskCreationRule(t *testing.T) {
	s, db := newMediaRecoveryTestService(t)
	task := seedMediaTask(t, db, providerConfig{})
	// 只存在浏览器缓存、尚未同步到服务端的画布 ID：创建任务时允许，登记作品也必须允许。
	task.ProjectID = "rokoiI1TE9TJCKXmxa6Gw"
	if projectID, err := s.mediaTaskProject(task); err != nil || projectID != "" {
		t.Fatalf("unsynced canvas: project=%q err=%v", projectID, err)
	}
	if err := s.ensureTaskProjectActive(task.UserID, task.ProjectID); err != nil {
		t.Fatalf("creation rule changed: %v", err)
	}
	archived := model.Project{ID: newID(), UserID: task.UserID, Name: "archived", Status: model.ProjectStatusArchived}
	if err := db.Create(&archived).Error; err != nil {
		t.Fatal(err)
	}
	task.ProjectID = archived.ID
	if _, err := s.mediaTaskProject(task); err == nil {
		t.Fatal("archived project accepted")
	}
}

func TestSeedanceVideoResultDownloadFailureIsRecoverableNotGenerationFailure(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	s, db := newMediaRecoveryTestService(t)
	s.coordinator = platform.NewCoordinatorWithRedis(nil, "legacy-video-recovery-test")
	mp4 := append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0, 'i', 's', 'o', 'm', 'i', 's', 'o', '2'}, bytes.Repeat([]byte{1}, 64)...)
	var creates, polls, downloads atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos":
			creates.Add(1)
			_, _ = w.Write([]byte(`{"id":"vid-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/vid-1":
			polls.Add(1)
			// 结果地址放在嵌套字段里，且在另一路径上（模拟 CDN 签名地址）。
			_, _ = w.Write([]byte(`{"status":"completed","content":{"video_url":"` + server.URL + `/cdn/vid-1.mp4?sig=private-test-token"}}`))
		case r.URL.Path == "/cdn/vid-1.mp4":
			if downloads.Add(1) == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "binary/octet-stream")
			_, _ = w.Write(mp4)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	config := providerConfig{BaseURL: server.URL + "/v1", APIKey: "test-key", Model: "seedance-test"}
	task := seedMediaTask(t, db, config)
	ctx := context.WithValue(withProviderAnalytics(context.Background(), s, *task), mediaExecutionTaskKey{}, *task)

	_, err := runSeedanceVideosTask(ctx, canvasGenerationInput{Mode: "video", Config: config, Prompt: "test"}, fastVideoPollPolicy())
	var failure *mediaRecoveryError
	if !errors.As(err, &failure) || failure.stage != "download" {
		t.Fatalf("download failure not routed to media recovery: %v", err)
	}
	if message := err.Error(); strings.Contains(message, "模型或模型接口不存在") || !strings.Contains(message, "HTTP 404") || strings.Contains(message, "private-test-token") {
		t.Fatalf("message=%s", message)
	}
	if creates.Load() != 1 || polls.Load() != 1 {
		t.Fatalf("delivery failure re-entered polling: creates=%d polls=%d", creates.Load(), polls.Load())
	}
	// 404（签名对象过期）不自动重试：任务以“可重试保存”结束，而不是生成失败。
	if err := s.handleMediaRecoveryFailure(task, err); err == nil {
		t.Fatal("terminal delivery failure swallowed")
	}
	failed, _ := s.repo.Task(task.ID)
	if failed.Status != model.TaskStatusFailed || !taskForOutput(*failed).CanRecoverMedia || strings.Contains(failed.Error, "模型或模型接口不存在") {
		t.Fatalf("not recoverable: %s %s", failed.Status, failed.Error)
	}
	if _, err := s.RecoverTaskMedia(task.UserID, task.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.repo.ClaimNextTask("worker-b", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.taskWorker().processClaimedTask(claimed, nil); err != nil {
		t.Fatal(err)
	}
	completed, _ := s.repo.Task(task.ID)
	if completed.Status != model.TaskStatusSucceeded || completed.MediaStage != "completed" {
		t.Fatalf("not delivered: %s %s %s", completed.Status, completed.MediaStage, completed.Error)
	}
	if creates.Load() != 1 || polls.Load() != 1 || downloads.Load() != 2 {
		t.Fatalf("recovery regenerated: creates=%d polls=%d downloads=%d", creates.Load(), polls.Load(), downloads.Load())
	}
	if !strings.Contains(completed.ResultJSON, "video/mp4") {
		t.Fatalf("result=%s", completed.ResultJSON)
	}
}
