package app

import (
	"bytes"
	"encoding/json"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"yingce/backend/internal/model"
)

func TestVideoChainSingleRequiresAndTransfersCurrentPredecessor(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A", "A", "B")
	f.req.Scope = "single"
	first := f.start(t)
	if len(first.Steps) != 1 || !first.Steps[0].Ending {
		t.Fatal("single generation must retain the real successor", first)
	}
	state := f.untilTask(t, first.ID, 0)
	videoID := f.complete(t, state.Steps[0].TaskID)
	artifact := model.ShotArtifact{ID: newID(), ProjectID: f.projectID, UnitID: f.req.UnitID, ShotID: "s0", RevisionID: "rs0", TaskID: state.Steps[0].TaskID, Type: "video", Version: 1, ResourceID: videoID, Status: "ready", Selected: true}
	if err := f.db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	f.tick(t, first.ID)
	f.tick(t, first.ID)
	f.req.ShotID, f.req.RequestID = "s1", "single-second"
	second := f.start(t)
	if len(second.Steps) != 1 || second.Steps[0].SourceShotID != "s0" || !second.Steps[0].Ending {
		t.Fatal(second)
	}
	state = f.untilTask(t, second.ID, 0)
	step := state.Steps[0]
	child, _ := f.s.repo.TaskForUser("user-1", step.TaskID)
	if !strings.HasPrefix(child.Prompt, videoChainPositionPrompt("@图片1")) || strings.Count(child.Prompt, dramaSceneEnding) != 1 {
		t.Fatal("single entry bypassed continuity", child.Prompt)
	}
	var input map[string]any
	raw, err := f.s.decryptTaskInputJSON(child.InputJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	refs := input["referenceImages"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["storageKey"] != "resource:"+f.frameID {
		t.Fatal("actual task input does not contain predecessor frame", refs)
	}
	assets, err := f.s.FilterProjectAssets("user-1", f.projectID, ProjectAssetFilter{Category: "storyboard_reference"})
	if err != nil || len(assets) != 1 || assets[0].ID != step.FrameAssetID || assets[0].StoryboardReference.SourceVideoResourceID != videoID || assets[0].StoryboardReference.SourceVideoTaskID != artifact.TaskID {
		t.Fatal("missing durable asset lineage", err, assets)
	}
	bindings, err := f.s.repo.ShotAssetReferencesForShot("s1")
	if err != nil || len(bindings) != 1 || bindings[0].Role != "continuity" || bindings[0].AssetVersionID != assets[0].PrimaryVersionID {
		t.Fatal("frame not bound to its consumer", err, bindings)
	}
	compiled, err := f.s.CompileProjectShot("user-1", f.projectID, "s1", CompileProjectShotRequest{})
	if err != nil || len(compiled.Result.Files) != 0 {
		t.Fatal("automatic frame polluted base references", err, compiled.Result.Files)
	}
	for range 3 {
		f.tick(t, second.ID)
	}
	var count int64
	f.db.Model(&model.Asset{}).Where("category = ?", "storyboard_reference").Count(&count)
	if count != 1 || f.frames.Load() != 1 || f.uploads.Load() != 1 {
		t.Fatal("replayed extraction/upload/asset", count, f.frames.Load(), f.uploads.Load())
	}
	var videos int64
	f.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&videos)
	if videos != 2 {
		t.Fatal("single request generated an unselected group", videos)
	}
}

func TestVideoChainSingleMissingOrStalePredecessorNeverSubmits(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	f.req.Scope, f.req.ShotID = "single", "s1"
	if _, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req); err == nil || !strings.Contains(err.Error(), "上一镜头组") {
		t.Fatal("missing predecessor silently ignored", err)
	}
	input := cloneVideoChainInput(f.req.Model.Input)
	input["metadata"] = map[string]any{"domainProjectId": f.projectID, "shotId": "s1"}
	if _, err := f.s.prepareDramaVideoSubmission("user-1", input, "完整正文", false); err == nil {
		t.Fatal("generic entry bypassed dependency")
	}
	var count int64
	f.db.Model(&model.Task{}).Count(&count)
	if count != 0 || f.frames.Load() != 0 || f.uploads.Load() != 0 {
		t.Fatal("dependency failure caused a paid task or preparation")
	}
}

func TestVideoChainPrepareOnlyCreatesReferenceWithoutVideoSubmission(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	for _, row := range []any{
		&model.Task{ID: "historic-task", UserID: "user-1", ProjectID: f.projectID, Type: "canvas_video", Status: model.TaskStatusSucceeded, Prompt: dramaSceneEnding, InputJSON: `{"metadata":{"_dramaVideoSubmission":{"endingApplied":true}}}`},
		&model.Resource{ID: "historic-video", UserID: "user-1", Kind: "video", Status: model.ResourceStatusReady, MimeType: "video/mp4"},
		&model.ShotArtifact{ID: "historic-artifact", ProjectID: f.projectID, UnitID: f.req.UnitID, ShotID: "s0", RevisionID: "rs0", TaskID: "historic-task", Type: "video", Version: 1, ResourceID: "historic-video", Status: "ready", Selected: true},
	} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.req.Scope, f.req.ShotID, f.req.PrepareOnly = "single", "s1", true
	chain := f.start(t)
	if !chain.PrepareOnly || chain.TotalMicrocredits != 0 {
		t.Fatal("preparation charged a generation quote", chain)
	}
	for range 8 {
		f.tick(t, chain.ID)
	}
	parent, _ := f.s.repo.TaskForUser("user-1", chain.ID)
	state, _ := f.s.videoChainState(parent)
	var count int64
	f.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&count)
	if parent.Status != model.TaskStatusSucceeded || count != 1 || state.Steps[0].FrameAssetID == "" || state.Steps[0].VideoResourceID != "" {
		t.Fatal("preparation submitted a video or lost reference", parent.Error, count, state)
	}
}

func TestTailFrameObservationUsesActualLastDecodedTimestamp(t *testing.T) {
	var observation dramaTailFrameObservation
	observeTailFrame([]byte("n:0 pts:100 pts_time:28.900\nn:1 pts:101 pts_time:29.967\ncorrupt packet"), "closest-readable-tail-frame", &observation)
	if observation.TimeSeconds == nil || *observation.TimeSeconds != 29.967 || observation.Method != "closest-readable-tail-frame" {
		t.Fatal(observation)
	}
}

func TestVideoChainLocalTailFrameIsActualFinalFrame(t *testing.T) {
	binary, err := renderFfmpegBinary()
	if err != nil {
		t.Skip("ffmpeg unavailable:", err)
	}
	if _, err := exec.LookPath(binary); err != nil {
		t.Skip("ffmpeg unavailable:", err)
	}
	path := filepath.Join(t.TempDir(), "two-colors.mp4")
	cmd := exec.Command(binary, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=16x16:r=2:d=1", "-f", "lavfi", "-i", "color=c=red:s=16x16:r=2:d=1", "-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("local video fixture: %v %s", err, out)
	}
	s, _ := newDramaWorkerTestService(t)
	video, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := s.UploadResourceFile("user-1", "two-colors.mp4", int64(len(video)), "video", 16, 16, 2000, bytes.NewReader(video))
	if err != nil {
		t.Fatal(err)
	}
	var observation dramaTailFrameObservation
	frameID, err := s.saveDramaTailFrame("user-1", resource, path, &observation)
	if err != nil {
		t.Fatal(err)
	}
	_, body, err := s.OpenResource("user-1", frameID)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	frame, _, err := image.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	red, _, blue, _ := frame.At(8, 8).RGBA()
	if red < 50000 || blue > 10000 || observation.TimeSeconds == nil || *observation.TimeSeconds < 1.49 {
		t.Fatal("extraction used an earlier blue frame or invented its timestamp", red, blue, observation)
	}
}
