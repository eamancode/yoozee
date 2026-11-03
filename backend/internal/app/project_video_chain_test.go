package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

type videoChainFixture struct {
	s          *Service
	db         *gorm.DB
	req        ProjectVideoChainRequest
	projectID  string
	uploads    atomic.Int32
	failUpload atomic.Bool
	frames     atomic.Int32
	failFrame  atomic.Bool
	frameID    string
}

func newVideoChainFixture(t *testing.T, scenes ...string) *videoChainFixture {
	t.Helper()
	f := &videoChainFixture{}
	f.s, f.db = newDramaWorkerTestService(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/media/upload" {
			t.Errorf("unexpected model call: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		count := f.uploads.Add(1)
		if f.failUpload.Swap(false) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"upload fixture failure"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"url":"https://uploaded.example/ref-%d.png"}`, count)
	}))
	t.Cleanup(server.Close)
	p := model.Project{ID: newID(), UserID: "user-1", Name: "连续生成测试", Status: model.ProjectStatusActive, GenerationModel: "seedance-2.5", BriefJSON: `{"previz_gate":"off"}`}
	u := model.ProjectUnit{ID: newID(), ProjectID: p.ID, Kind: model.ProjectUnitKindChapter, Title: "第一场"}
	for _, value := range []any{&p, &u, &model.User{ID: "user-1", Role: model.UserRoleUser}} {
		if err := f.db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, scene := range scenes {
		id := fmt.Sprint("s", i)
		segment := drama.Segment{ID: fmt.Sprintf("S%02d", i+1), Scene: scene, Dur: 10, Mode: drama.ModeTextOnly, Shots: []drama.SubShot{{At: 0, Size: "中景", Text: "阿婆整理推车，林深站在左边"}}}
		raw, _ := json.Marshal(segment)
		shot := model.Shot{ID: id, ProjectID: p.ID, UnitID: u.ID, Position: i, DurationMs: 10000, Title: id, CurrentRevisionID: "r" + id}
		revision := model.ShotRevision{ID: shot.CurrentRevisionID, ShotID: id, SpecJSON: string(raw), VideoPrompt: "原始提示词"}
		for _, value := range []any{&shot, &revision} {
			if err := f.db.Create(value).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	profile := DefaultModelCapabilityConfigForModel("seedance-videos-compatible", "seedance-2.5-test")
	profile.Video.Duration = VideoDurationConfig{Selection: "enum", Values: []int{15, 30}, Default: 15}
	profile.Video.Operations = []string{"text_to_video", "reference_to_video"}
	f.projectID = p.ID
	f.req = ProjectVideoChainRequest{UnitID: u.ID, ShotID: "s0", RequestID: "same-request", Model: ProjectVideoChainModel{Model: "seedance-2.5-test", Input: map[string]any{"mode": "video", "config": map[string]any{"baseUrl": server.URL, "apiKey": "fixture-only", "interfaceType": "seedance-videos-compatible", "model": "seedance-2.5-test", "videoSeconds": "15", "size": "9:16", "vquality": "720p", "capabilityConfig": profile}}}}
	png := mediaTestPNG(t)
	frame, err := f.s.UploadResourceFile("user-1", "frame.png", int64(len(png)), "image", 2, 3, 0, bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	f.frameID = frame.ID
	f.s.dramaTailFrameHook = func(user, video string) (string, error) {
		f.frames.Add(1)
		if f.failFrame.Swap(false) {
			return "", errors.New("末帧读取失败 fixture")
		}
		if user != "user-1" || video == "" {
			t.Fatal("missing source lineage")
		}
		return f.frameID, nil
	}
	return f
}

func (f *videoChainFixture) start(t *testing.T) ProjectVideoChainView {
	t.Helper()
	preview, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil {
		t.Fatal(err)
	}
	f.req.PreviewHash = preview.PreviewHash
	view, err := f.s.CreateProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func (f *videoChainFixture) tick(t *testing.T, id string) videoChainState {
	t.Helper()
	expires := time.Now().Add(time.Minute)
	if err := f.db.Model(&model.Task{}).Where("id = ? AND status IN ?", id, []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning}).Updates(map[string]any{"status": model.TaskStatusRunning, "lease_owner": "chain-test", "lease_expires_at": expires, "next_poll_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	task, err := f.s.repo.TaskForUser("user-1", id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status == model.TaskStatusRunning {
		if err := f.s.processProjectVideoChain(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	task, _ = f.s.repo.TaskForUser("user-1", id)
	state, err := f.s.videoChainState(task)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func (f *videoChainFixture) untilTask(t *testing.T, id string, index int) videoChainState {
	t.Helper()
	for range 10 {
		state := f.tick(t, id)
		if _, err := f.s.repo.TaskForUser("user-1", state.Steps[index].TaskID); err == nil {
			return state
		}
		task, _ := f.s.repo.TaskForUser("user-1", id)
		if task.Status == model.TaskStatusFailed {
			t.Fatalf("chain paused: %s", task.Error)
		}
	}
	t.Fatal("child not submitted")
	return videoChainState{}
}

func (f *videoChainFixture) complete(t *testing.T, taskID string) string {
	t.Helper()
	id := "video-" + taskID
	if err := f.db.Create(&model.Resource{ID: id, UserID: "user-1", Kind: "video", MimeType: "video/mp4", Status: model.ResourceStatusReady}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": fmt.Sprintf(`{"video":{"resourceId":%q,"storageKey":%q}}`, id, "resource:"+id)}).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func TestVideoChainABCOrderingFramesAndIdempotency(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A", "A", "B", "A")
	chain := f.start(t)
	if len(chain.Steps) != 3 || !chain.Steps[0].Ending || !chain.Steps[1].Ending || chain.Steps[2].Ending {
		t.Fatalf("scene chain: %+v", chain)
	}
	again, err := f.s.CreateProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil || again.ID != chain.ID {
		t.Fatal("duplicate controller", err)
	}
	for i := range 3 {
		state := f.untilTask(t, chain.ID, i)
		for range 3 {
			f.tick(t, chain.ID)
		}
		var count int64
		f.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&count)
		if count != int64(i+1) {
			t.Fatalf("submitted early or duplicated: %d", count)
		}
		child, _ := f.s.repo.TaskForUser("user-1", state.Steps[i].TaskID)
		wantPrefix := 0
		if i > 0 {
			wantPrefix = 1
		}
		wantEnding := 0
		if i < 2 {
			wantEnding = 1
		}
		if strings.Count(child.Prompt, "【站位参考】") != wantPrefix || strings.Count(child.Prompt, dramaSceneEnding) != wantEnding {
			t.Fatalf("prompt order/suffix: %s", child.Prompt)
		}
		var input map[string]any
		raw, _ := f.s.decryptTaskInputJSON(child.InputJSON)
		_ = json.Unmarshal([]byte(raw), &input)
		images, _ := input["referenceImages"].([]any)
		if len(images) != wantPrefix {
			t.Fatal("reference duplicated/missing", images)
		}
		if i > 0 {
			if !strings.HasPrefix(child.Prompt, videoChainPositionPrompt("@图片1")) || state.Steps[i].FrameResourceID != f.frameID || state.Steps[i].SourceVideoResourceID != state.Steps[i-1].VideoResourceID {
				t.Fatal("frame lineage or tag mismatch")
			}
		}
		f.complete(t, child.ID)
		f.tick(t, chain.ID)
	}
	f.tick(t, chain.ID)
	parent, _ := f.s.repo.TaskForUser("user-1", chain.ID)
	if parent.Status != model.TaskStatusSucceeded || f.frames.Load() != 2 || f.uploads.Load() != 2 {
		t.Fatalf("not complete/repeated work: %s %d %d %s", parent.Status, f.frames.Load(), f.uploads.Load(), parent.Error)
	}
	if strings.Contains(parent.InputJSON, "fixture-only") {
		t.Fatal("plaintext credential persisted")
	}
}

func TestVideoChainFailureResumesFrameAndUpload(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A", "A")
	chain := f.start(t)
	state := f.untilTask(t, chain.ID, 0)
	f.complete(t, state.Steps[0].TaskID)
	f.tick(t, chain.ID)
	f.failFrame.Store(true)
	f.tick(t, chain.ID)
	parent, _ := f.s.repo.TaskForUser("user-1", chain.ID)
	if parent.Status != model.TaskStatusFailed {
		t.Fatal("frame failure did not pause")
	}
	if _, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID); err != nil {
		t.Fatal(err)
	}
	f.tick(t, chain.ID) // successful extraction
	f.failUpload.Store(true)
	f.tick(t, chain.ID)
	parent, _ = f.s.repo.TaskForUser("user-1", chain.ID)
	if parent.Status != model.TaskStatusFailed {
		t.Fatal("upload failure did not pause")
	}
	if _, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID); err != nil {
		t.Fatal(err)
	}
	state = f.untilTask(t, chain.ID, 1)
	if f.frames.Load() != 2 || f.uploads.Load() != 2 || state.Steps[1].FrameResourceID != f.frameID {
		t.Fatal("successful frame repeated or discarded")
	}
	// An already generated A remains the same task; only B is submitted.
	var count int64
	f.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&count)
	if count != 2 {
		t.Fatal("recreated successful task", count)
	}
	if _, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID); err == nil {
		t.Fatal("concurrent retry admitted")
	}
}

func TestVideoChainCancelFencesQueuedChildrenAndForeignAccess(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	chain := f.start(t)
	state := f.untilTask(t, chain.ID, 0)
	if _, err := f.s.CancelProjectVideoChain("other-user", f.projectID, chain.ID); err == nil {
		t.Fatal("foreign cancel allowed")
	}
	if _, err := f.s.CancelProjectVideoChain("user-1", f.projectID, chain.ID); err != nil {
		t.Fatal(err)
	}
	child, _ := f.s.repo.TaskForUser("user-1", state.Steps[0].TaskID)
	if child.Status != model.TaskStatusCancelled {
		t.Fatalf("queued child still active: %s", child.Status)
	}
	f.tick(t, chain.ID)
	var count int64
	f.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&count)
	if count != 1 {
		t.Fatal("submitted after cancel")
	}
	if _, err := f.s.RetryTask("user-1", child.ID); err == nil {
		t.Fatal("canceled chain child requeued")
	}
}

func TestVideoChainSingleAndUnsupportedReference(t *testing.T) {
	f := newVideoChainFixture(t, "A", "B", "A")
	preview, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil || len(preview.Steps) != 1 || preview.Steps[0].Ending || preview.Steps[0].SourceShotID != "" {
		t.Fatal("cross-scene chain", preview, err)
	}
	f2 := newVideoChainFixture(t, "A", "A")
	config := f2.req.Model.Input["config"].(map[string]any)
	config["capabilityConfig"].(*ModelCapabilityConfig).Video.References.MaxImages = 0
	if _, err := f2.s.PreviewProjectVideoChain("user-1", f2.projectID, f2.req); err == nil {
		t.Fatal("ignored unsupported spatial image")
	}
	for _, input := range []map[string]any{{videoChainMetadataKey: map[string]any{"controllerId": "forged"}}, {"referenceImages": []any{map[string]any{"_chainUploadId": "forged"}}}} {
		if _, err := f.s.CreateTask("user-1", CreateTaskRequest{Type: "canvas_video", Input: input}); err == nil {
			t.Fatal("forged server metadata accepted")
		}
	}
}

func TestVideoChainUploadCheckpointReusesPartialSuccess(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 2 {
			w.WriteHeader(400)
			return
		}
		fmt.Fprintf(w, `{"url":"https://uploaded.example/%d.png"}`, n)
	}))
	defer server.Close()
	raw, err := os.ReadFile("../../../plugin-packages/seedance-videos-compatible/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := protocol.LoadManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	input := canvasGenerationInput{Mode: "video", Config: providerConfig{BaseURL: server.URL, APIKey: "fixture", Model: "seedance-2.5", InterfaceType: "seedance-videos-compatible"}, ReferenceImages: []providerMedia{{ID: "a", StorageKey: "resource:a", DataURL: "data:image/png;base64,eA=="}, {ID: "b", StorageKey: "resource:b", DataURL: "data:image/png;base64,eA=="}}}
	// Different image payloads avoid the per-request byte cache coalescing them.
	input.ReferenceImages[1].DataURL = "data:image/png;base64,eQ=="
	saved := 0
	ids := map[string]string{}
	ctx := context.WithValue(context.Background(), videoChainUploadContextKey{}, &videoChainUploadCheckpoint{IDs: ids, Save: func() error { saved++; return nil }})
	request := protocolRequestFromInput(input)
	if err := uploadProtocolMedia(ctx, input, adapter, &request); err == nil {
		t.Fatal("upload failure swallowed")
	}
	if saved != 1 || len(ids) != 1 {
		t.Fatal("partial upload not checkpointed")
	}
	request = protocolRequestFromInput(input)
	if err := uploadProtocolMedia(ctx, input, adapter, &request); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || saved != 2 {
		t.Fatal("successful upload repeated", calls.Load(), saved)
	}
	for i := range input.ReferenceImages {
		media := &input.ReferenceImages[i]
		media.ChainUploadID = ids["image:"+media.StorageKey]
		media.ChainUploadScope = videoChainUploadScope(input.Config)
	}
	request = protocolRequestFromInput(input)
	if err := uploadProtocolMedia(context.Background(), input, adapter, &request); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("submission repeated prepared uploads")
	}
	input.Config.APIKey = "different-account"
	request = protocolRequestFromInput(input)
	if err := uploadProtocolMedia(context.Background(), input, adapter, &request); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Fatal("wrong-account uploads reused")
	}
}

func TestVideoChainReferencesAndPromptRemainIdempotent(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A", "A")
	chain := f.start(t)
	parent, _ := f.s.repo.TaskForUser("user-1", chain.ID)
	state, _ := f.s.videoChainState(parent)
	step := &state.Steps[1]
	step.FrameResourceID = f.frameID
	step.SourceVideoResourceID = "previous-video"
	step.ReferenceTag = "@图片2"
	step.Input["referenceImages"] = []any{map[string]any{"storageKey": "resource:original-character", "role": "reference"}}
	for range 3 {
		input, err := f.s.videoChainInput(parent, step)
		if err != nil {
			t.Fatal(err)
		}
		images := input["referenceImages"].([]any)
		if len(images) != 2 || images[0].(map[string]any)["storageKey"] != "resource:original-character" || images[1].(map[string]any)["role"] != "reference" {
			t.Fatal("original refs replaced or forced first frame", images)
		}
		prompt, err := f.s.prepareDramaVideoSubmission("user-1", input, stringValue(input["prompt"]), false)
		if err != nil {
			t.Fatal(err)
		}
		for range 3 {
			prompt, err = f.s.prepareDramaVideoSubmission("user-1", input, prompt, true)
			if err != nil {
				t.Fatal(err)
			}
		}
		if strings.Count(prompt, "【站位参考】") != 1 || strings.Count(prompt, dramaSceneEnding) != 1 || !strings.HasPrefix(prompt, videoChainPositionPrompt("@图片2")) {
			t.Fatal("duplicate prompt", prompt)
		}
	}
	if len(step.Input["referenceImages"].([]any)) != 1 {
		t.Fatal("stored prompt/refs mutated")
	}
}

func TestVideoChainVideoFailureNeedsExplicitRetryAndKeepsTask(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	chain := f.start(t)
	state := f.untilTask(t, chain.ID, 0)
	id := state.Steps[0].TaskID
	f.db.Model(&model.Task{}).Where("id = ?", id).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "模拟生成失败"})
	f.tick(t, chain.ID)
	if _, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID); err != nil {
		t.Fatal(err)
	}
	f.tick(t, chain.ID)
	child, _ := f.s.repo.TaskForUser("user-1", id)
	if child.Status != model.TaskStatusQueued || child.RouteRun != 2 || strings.Count(child.Prompt, dramaSceneEnding) != 1 {
		t.Fatalf("retry state: %+v", child)
	}
	f.db.Model(&model.Task{}).Where("id = ?", id).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "再次失败"})
	f.tick(t, chain.ID)
	f.tick(t, chain.ID)
	child, _ = f.s.repo.TaskForUser("user-1", id)
	if child.RouteRun != 2 || child.Status != model.TaskStatusFailed {
		t.Fatal("automatically regenerated failed video")
	}
}

func TestVideoChainCancelKeepsSubmittedVideoAndStopsAfterRestart(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	chain := f.start(t)
	state := f.untilTask(t, chain.ID, 0)
	id := state.Steps[0].TaskID
	f.db.Model(&model.Task{}).Where("id = ?", id).Updates(map[string]any{"status": model.TaskStatusRunning, "provider_request_id": "already-submitted"})
	if _, err := f.s.CancelProjectVideoChain("user-1", f.projectID, chain.ID); err != nil {
		t.Fatal(err)
	}
	f.s = &Service{repo: f.s.repo, dataDir: f.s.dataDir, activeCancels: make(map[string]context.CancelFunc)}
	child, _ := f.s.repo.TaskForUser("user-1", id)
	if child.Status != model.TaskStatusRunning {
		t.Fatal("inflight video discarded")
	}
	f.complete(t, id)
	f.tick(t, chain.ID)
	views, err := f.s.ProjectVideoChains("user-1", f.projectID, f.req.UnitID)
	if err != nil || len(views) != 1 || views[0].Steps[0].Status != "succeeded" || views[0].Steps[0].VideoResourceID == "" {
		t.Fatal("canceled chain hid a completed inflight video", views, err)
	}
	var count int64
	f.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&count)
	if count != 1 {
		t.Fatal("restart submitted after cancellation")
	}
	parent, _ := f.s.repo.TaskForUser("user-1", chain.ID)
	if err := f.s.repo.CheckpointProjectVideoChain(parent, parent.InputJSON); err == nil {
		t.Fatal("late checkpoint revived canceled controller")
	}
}

func TestVideoChainControllerDoesNotStarveQueuedVideo(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	chain := f.start(t)
	state := f.untilTask(t, chain.ID, 0)
	f.db.Model(&model.Task{}).Where("id = ?", chain.ID).Update("next_poll_at", time.Now().Add(-time.Second))
	claimed, err := f.s.repo.ClaimNextTask("single-worker", time.Minute)
	if err != nil || claimed == nil || claimed.ID != state.Steps[0].TaskID {
		t.Fatal("controller starved its video", claimed, err)
	}
}

func TestVideoChainStaleControllerCannotAdmitChild(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	chain := f.start(t)
	expires := time.Now().Add(time.Minute)
	f.db.Model(&model.Task{}).Where("id = ?", chain.ID).Updates(map[string]any{"status": model.TaskStatusRunning, "lease_owner": "new-worker", "lease_expires_at": expires})
	child := &model.Task{ID: newID(), UserID: "user-1", ProjectID: f.projectID, Type: "canvas_video", Status: model.TaskStatusQueued, ParentControllerID: chain.ID, ParentControllerLease: "old-worker"}
	if err := f.s.repo.CreateTaskWithActiveLimit(child, 10); err == nil {
		t.Fatal("stale controller admitted a video")
	}
	child.ParentControllerLease = "new-worker"
	if err := f.s.repo.CreateTaskWithActiveLimit(child, 10); err != nil {
		t.Fatal(err)
	}
}
