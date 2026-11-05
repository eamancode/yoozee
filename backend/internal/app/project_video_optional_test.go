package app

import (
	"encoding/json"
	"errors"
	"testing"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestVideoChainFailedHistoryDoesNotBlockNewChainOrResumeAlongsideActive(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	old := f.start(t)
	if err := f.db.Model(&model.Task{}).Where("id = ?", old.ID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "fixture stopped"}).Error; err != nil {
		t.Fatal(err)
	}
	before, _ := f.s.repo.TaskForUser("user-1", old.ID)
	f.req.RequestID = "new-explicit-request"
	fresh := f.start(t)
	if fresh.ID == old.ID {
		t.Fatal("failed chain still occupies chapter")
	}
	if _, err := f.s.RetryProjectVideoChain("user-1", f.projectID, old.ID); err == nil {
		t.Fatal("failed retry bypassed active chain lock")
	}
	after, _ := f.s.repo.TaskForUser("user-1", old.ID)
	if after.Status != model.TaskStatusFailed || after.RouteRun != before.RouteRun || after.InputJSON != before.InputJSON {
		t.Fatal("historical failure was automatically resumed or rewritten")
	}
	duplicate, err := f.s.CreateProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil || duplicate.ID != fresh.ID {
		t.Fatal("duplicate controller admitted", err)
	}
	conflicting := f.req
	conflicting.Scope, conflicting.ShotID, conflicting.RequestID = "single", "s1", "different-plan"
	preview, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, conflicting)
	if err != nil {
		t.Fatal(err)
	}
	conflicting.PreviewHash = preview.PreviewHash
	_, err = f.s.CreateProjectVideoChain("user-1", f.projectID, conflicting)
	var conflict *AppError
	if !errors.As(err, &conflict) || conflict.Status != 409 || conflict.Details["activeVideoChainId"] != fresh.ID {
		t.Fatal("active conflict not actionable", err)
	}
}

func TestVideoChainFullReferenceCapacityKeepsOriginalAndSkipsWhiteFrame(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	f.req.Model.Input["config"].(map[string]any)["capabilityConfig"].(*ModelCapabilityConfig).Video.References.MaxImages = 1
	segment := drama.Segment{ID: "S02", Scene: "A", Dur: 10, Mode: drama.ModeLockIdentity, Refs: []drama.Ref{{File: f.frameID, Role: "identity"}}, Shots: []drama.SubShot{{At: 0, Size: "中景", Text: "林深站在推车左侧"}}}
	raw, _ := json.Marshal(segment)
	if err := f.db.Model(&model.ShotRevision{}).Where("id = ?", "rs1").Update("spec_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	chain := f.start(t)
	first := f.untilTask(t, chain.ID, 0)
	f.complete(t, first.Steps[0].TaskID)
	f.tick(t, chain.ID)
	state := f.untilTask(t, chain.ID, 1)
	step := state.Steps[1]
	child, _ := f.s.repo.TaskForUser("user-1", step.TaskID)
	inputJSON, _ := f.s.decryptTaskInputJSON(child.InputJSON)
	var input canvasGenerationInput
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		t.Fatal(err)
	}
	if !step.ReferenceSkipped || len(input.ReferenceImages) != 1 || input.ReferenceImages[0].StorageKey != "resource:"+f.frameID || f.frames.Load() != 0 {
		t.Fatal("optional frame replaced original or triggered extraction", step, input.ReferenceImages)
	}
}

func TestVideoChainOriginalReferenceFailureStillPausesBeforeSubmission(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	var revision model.ShotRevision
	if err := f.db.First(&revision, "id = ?", "rs0").Error; err != nil {
		t.Fatal(err)
	}
	var segment drama.Segment
	if err := json.Unmarshal([]byte(revision.SpecJSON), &segment); err != nil {
		t.Fatal(err)
	}
	segment.Mode, segment.Refs = drama.ModeLockIdentity, []drama.Ref{{File: f.frameID, Role: "identity"}}
	raw, _ := json.Marshal(segment)
	if err := f.db.Model(&revision).Update("spec_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	chain := f.start(t)
	f.failUpload.Store(true)
	for range 5 {
		f.tick(t, chain.ID)
	}
	parent, _ := f.s.repo.TaskForUser("user-1", chain.ID)
	state, _ := f.s.videoChainState(parent)
	var children int64
	f.db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&children)
	if parent.Status != model.TaskStatusFailed || state.Steps[0].ReferenceSkipped || children != 0 || f.uploads.Load() != 1 {
		t.Fatal("required original reference ignored or retried", parent.Status, children, f.uploads.Load())
	}
}

func TestVideoChainWhiteFrameAttachmentPreservesExistingStoryboard(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	existing := model.ShotArtifact{ID: "user-storyboard", ProjectID: f.projectID, UnitID: f.req.UnitID, ShotID: "s1", RevisionID: "rs1", Type: "storyboard", Version: 1, Status: "ready", Selected: true, ResourceID: f.frameID}
	if err := f.db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}
	chain := f.start(t)
	first := f.untilTask(t, chain.ID, 0)
	f.complete(t, first.Steps[0].TaskID)
	f.tick(t, chain.ID)
	state := f.untilTask(t, chain.ID, 1)
	for range 3 {
		f.tick(t, chain.ID)
	}
	var artifacts []model.ShotArtifact
	if err := f.db.Where("shot_id = ? AND type = ?", "s1", "storyboard").Order("version").Find(&artifacts).Error; err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 || !artifacts[0].Selected || artifacts[1].Selected || artifacts[1].ResourceID != f.frameID || state.Steps[1].FrameAssetID == "" {
		t.Fatal("white frame missing/duplicated/replaced existing storyboard", artifacts)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(artifacts[1].MetadataJSON), &meta); err != nil || meta["role"] != "continuity_reference" {
		t.Fatal("missing frame provenance", err)
	}
}
