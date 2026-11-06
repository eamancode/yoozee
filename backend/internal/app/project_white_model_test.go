package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestWhiteModelPositionPromptUsesExactActualReference(t *testing.T) {
	for _, tag := range []string{"@图片1", "@图片15", "[image_3]"} {
		want := "【站位参考】请以「" + tag + "」该图中的人物位置、朝向、彼此距离，以及场景结构和道具位置，作为本镜起始状态的空间参考；"
		if got := videoChainPositionPrompt(tag); got != want {
			t.Fatal(got)
		}
	}
}

func TestWhiteModelProjectCreateUpdateAndOwnership(t *testing.T) {
	s, _ := newDramaWorkerTestService(t)
	p, err := s.CreateProject("user-1", CreateProjectRequest{Name: "白模默认关闭", VisualType: "live_action"})
	if err != nil || p.WhiteModelStoryboardEnabled {
		t.Fatal(p, err)
	}
	for _, enabled := range []bool{true, false} {
		p, err = s.UpdateProject("user-1", p.ID, UpdateProjectRequest{WhiteModelStoryboardEnabled: &enabled})
		if err != nil || p.WhiteModelStoryboardEnabled != enabled {
			t.Fatal(p, err)
		}
		stored, err := s.repo.ProjectForUser("user-1", p.ID)
		if err != nil || stored.WhiteModelStoryboardEnabled != enabled {
			t.Fatal(stored, err)
		}
		if _, err := s.UpdateProject("other-user", p.ID, UpdateProjectRequest{WhiteModelStoryboardEnabled: &enabled}); err == nil {
			t.Fatal("cross-user update admitted")
		}
	}
	p, err = s.CreateProject("user-1", CreateProjectRequest{Name: "白模明确开启", VisualType: "live_action", WhiteModelStoryboardEnabled: true})
	if err != nil || !p.WhiteModelStoryboardEnabled {
		t.Fatal(p, err)
	}
}

func TestWhiteModelDisabledChainDoesNotExtractOrUploadContinuity(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A", "A")
	segment := drama.Segment{ID: "S02", Scene: "A", Dur: 10, Mode: drama.ModeLockIdentity, Refs: []drama.Ref{{File: f.frameID, Role: "identity"}}, Shots: []drama.SubShot{{At: 0, Size: "中景", Text: "林深站在推车左侧"}}}
	spec, err := json.Marshal(segment)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.ShotRevision{}).Where("id = ?", "rs1").Update("spec_json", string(spec)).Error; err != nil {
		t.Fatal(err)
	}
	existing := model.ShotArtifact{ID: "old-white", ProjectID: f.projectID, UnitID: f.req.UnitID, ShotID: "s1", RevisionID: "rs1", Type: "storyboard", Version: 1, Status: "ready", Selected: true, ResourceID: f.frameID, MetadataJSON: `{"role":"continuity_reference"}`}
	if err := f.db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Project{}).Where("id = ?", f.projectID).Update("white_model_storyboard_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	chain := f.start(t)
	if chain.WhiteModelStoryboardEnabled {
		t.Fatal("preview still enabled")
	}
	for i := range 3 {
		state := f.untilTask(t, chain.ID, i)
		step := state.Steps[i]
		child, err := f.s.repo.TaskForUser("user-1", step.TaskID)
		if err != nil || step.Ending || step.SourceShotID != "" || strings.Contains(child.Prompt, "【站位参考】") || strings.Contains(child.Prompt, dramaSceneEnding) {
			t.Fatal(step, child, err)
		}
		inputJSON, err := f.s.decryptTaskInputJSON(child.InputJSON)
		if err != nil {
			t.Fatal(err)
		}
		var input canvasGenerationInput
		if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
			t.Fatal(err)
		}
		wantRefs := 0
		if i == 1 {
			wantRefs = 1
		}
		if len(input.ReferenceImages) != wantRefs {
			t.Fatal("original references changed", i, input.ReferenceImages)
		}
		f.complete(t, step.TaskID)
		f.tick(t, chain.ID)
	}
	if f.frames.Load() != 0 || f.uploads.Load() != 1 {
		t.Fatal("disabled chain performed white-frame work", f.frames.Load(), f.uploads.Load())
	}
	var count int64
	f.db.Model(&model.ShotArtifact{}).Where("type = ?", "storyboard").Count(&count)
	if count != 1 {
		t.Fatal("disabled chain changed historical attachments")
	}
	request := f.req
	request.Scope, request.PrepareOnly, request.ShotID = "single", true, "s1"
	if _, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, request); err == nil {
		t.Fatal("prepare-only bypassed disabled switch")
	}
}

func TestWhiteModelPreviewInvalidatesAndAcceptedChainKeepsSnapshot(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	preview, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := f.s.UpdateProject("user-1", f.projectID, UpdateProjectRequest{WhiteModelStoryboardEnabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	f.req.PreviewHash = preview.PreviewHash
	if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, f.req); err == nil {
		t.Fatal("stale preview accepted")
	}
	chain := f.start(t) // accepted with disabled snapshot
	enabled := true
	if _, err := f.s.UpdateProject("user-1", f.projectID, UpdateProjectRequest{WhiteModelStoryboardEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	first := f.untilTask(t, chain.ID, 0)
	child, _ := f.s.repo.TaskForUser("user-1", first.Steps[0].TaskID)
	if strings.Contains(child.Prompt, dramaSceneEnding) {
		t.Fatal("running chain adopted changed project setting")
	}
	if err := f.db.Model(child).Update("status", model.TaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	f.tick(t, chain.ID)
	resumed, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID)
	if err != nil || resumed.WhiteModelStoryboardEnabled {
		t.Fatal("explicit resume changed snapshot", resumed, err)
	}
}

func TestWhiteModelSingleRetryKeepsSnapshotAndNewSubmissionUsesSetting(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	if err := f.db.Model(&model.Project{}).Where("id = ?", f.projectID).Update("white_model_storyboard_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"mode": "video", "metadata": map[string]any{"domainProjectId": f.projectID, "shotId": "s0"}, "config": map[string]any{"videoSeconds": "10"}}
	prompt, err := f.s.prepareDramaVideoSubmission("user-1", input, "原正文", false)
	if err != nil || prompt != "原正文" {
		t.Fatal(prompt, err)
	}
	if err := f.db.Model(&model.Project{}).Where("id = ?", f.projectID).Update("white_model_storyboard_enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	prompt, err = f.s.prepareDramaVideoSubmission("user-1", input, prompt, true)
	if err != nil || prompt != "原正文" {
		t.Fatal("retry changed snapshot", prompt, err)
	}
	prompt, err = f.s.prepareDramaVideoSubmission("user-1", input, "原正文", false)
	if err != nil || strings.Count(prompt, dramaSceneEnding) != 1 {
		t.Fatal("new submission ignored switch", prompt, err)
	}
	raw, _ := json.Marshal(input["metadata"])
	if !strings.Contains(string(raw), `"whiteModelStoryboardEnabled":true`) {
		t.Fatal("missing task snapshot", string(raw))
	}
}

func TestWhiteModelLegacyChainRetainsEnabledSnapshot(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	chain := f.start(t)
	parent, err := f.s.repo.TaskForUser("user-1", chain.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.s.videoChainState(parent)
	if err != nil {
		t.Fatal(err)
	}
	state.WhiteModelStoryboardEnabled = nil
	raw, err := f.s.encodeVideoChainState(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(parent).Update("input_json", raw).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Project{}).Where("id = ?", f.projectID).Update("white_model_storyboard_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	first := f.untilTask(t, chain.ID, 0)
	child, err := f.s.repo.TaskForUser("user-1", first.Steps[0].TaskID)
	if err != nil || strings.Count(child.Prompt, dramaSceneEnding) != 1 {
		t.Fatal("legacy chain changed behavior", child, err)
	}
}
