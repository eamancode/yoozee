package app

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestDeleteSSSUnitUpdatesLatestDocumentAndRejectsStaleRepair(t *testing.T) {
	s, p, u, base, m := seedSSSEpisode(t)
	repair := sssRepairFor(t, s, p, u, base, "S02", "E1-U2")
	target := dramaStepTaskTarget{ProjectID: p.ID, UnitID: u.ID}
	if err := s.DeleteProjectShot(p.UserID, p.ID, repair.Repair.ShotID); err != nil {
		t.Fatal(err)
	}
	_, err := s.applySSSRepair(model.Task{ID: "stale-unit-repair", UserID: p.UserID}, target, repair, sssRepaired("E1-U2", 8), m, DramaStepTaskOutcome{})
	if err == nil {
		t.Fatal("repair resurrected deleted unit")
	}
	segs, cards, content := sssEpisodeState(t, s, p, u)
	if len(segs) != 1 || segs["E1-U1"] != 12 || len(cards) != 1 || cards["E1-U1"] != 12 || strings.Contains(content, "E1-U2") {
		t.Fatal(segs, cards, content)
	}
	usage, skill, err := s.sssStepSkill(&p, mustWorkflow(t, p.WorkflowID), mustStep(t, drama.StepChapterShots))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.planSSSShots(p.UserID, &p, &u, usage, skill, nil, nil, sssShotOptions{})
	if err != nil || plan.sss.UnitEditVersion != 2 {
		t.Fatalf("new director run failed to pick up the current manual version: %v, %v", plan, err)
	}
}

func TestDeleteDirectorUnitRetiresOnlyMatchingFramesAndBlocksOldAssembly(t *testing.T) {
	s, db, p, u := newProjectDramaTestService(t, "seedance-2.5")
	if err := db.Model(&p).Update("white_model_storyboard_enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	meta := &dramaOntologyMeta{Brief: drama.OntologyBrief{EpisodeSeconds: 60, MaxUnitSeconds: 15}}
	input, _ := json.Marshal(map[string]any{"metadata": map[string]any{"domainProjectId": p.ID, "chapterId": u.ID}})
	task := model.Task{ID: "director-initial", UserID: p.UserID, ProjectID: p.ID, InputJSON: string(input)}
	result := ontologyTestResult(3, 10, 0)
	if _, err := s.applyOntologyShotsTask(task, nil, meta, result); err != nil {
		t.Fatal(err)
	}
	shots, err := s.repo.ProjectUnitShots(p.ID, u.ID)
	if err != nil || len(shots) != 3 {
		t.Fatal(shots, err)
	}
	prev, removed, next := shots[0], shots[1], shots[2]
	for _, r := range []any{
		&model.Asset{ID: "tail", UserID: p.UserID, Category: model.AssetCategoryStoryboardReference, PrimaryVersionID: "tail-version"},
		&model.AssetVersion{ID: "tail-version", AssetID: "tail", Version: 1, DefinitionJSON: `{"sourceShotId":"` + removed.ID + `"}`},
		&model.ProjectAssetLink{ID: "library-link", ProjectID: p.ID, AssetID: "tail"},
		&model.Resource{ID: "tail-image", UserID: p.UserID, Kind: "image"},
		&model.ShotAssetReference{ID: "frame-ref", ShotID: next.ID, AssetVersionID: "tail-version", Role: "continuity", Status: "linked"},
		&model.ShotAssetReference{ID: "other-ref", ShotID: next.ID, AssetVersionID: "actor-version", Role: "identity", Status: "linked"},
		&model.ShotArtifact{ID: "frame", ProjectID: p.ID, UnitID: u.ID, ShotID: next.ID, Type: "storyboard", Version: 1, ResourceID: "tail-image", Status: "ready", Selected: true,
			MetadataJSON: `{"role":"continuity_reference","continuityReference":{"sourceShotId":"` + removed.ID + `"}}`},
		&model.ShotArtifact{ID: "other-storyboard", ProjectID: p.ID, UnitID: u.ID, ShotID: next.ID, Type: "storyboard", Version: 2, Status: "ready"},
		&model.ShotArtifact{ID: "other-video", ProjectID: p.ID, UnitID: u.ID, ShotID: next.ID, Type: "video", Version: 1, Status: "ready", Selected: true},
	} {
		if err := db.Create(r).Error; err != nil {
			t.Fatal(err)
		}
	}
	doc, err := s.repo.LatestOntologyDocument(p.ID, u.ID, model.OntologyDocEpisode)
	if err != nil {
		t.Fatal(err)
	}
	gateVersion := func(content string) {
		t.Helper()
		if err := s.repo.AppendOntologyDocument(&model.ProjectOntologyDocument{ID: newID(), ProjectID: p.ID, UnitID: u.ID, Kind: model.OntologyDocGate, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	gateVersion("invalid gate fixture")
	if err := s.DeleteProjectShot(p.UserID, p.ID, removed.ID); appReason(err) != string(ReasonConflict) {
		t.Fatal("invalid gate should roll back episode and shot together", err)
	}
	unchanged, err := s.repo.LatestOntologyDocument(p.ID, u.ID, model.OntologyDocEpisode)
	if err != nil || unchanged.ID != doc.ID {
		t.Fatal("failed deletion left a new episode version", unchanged, err)
	}
	gateVersion(`["s01-u2 H1","s01-u3 H2"]`)
	if err := s.DeleteProjectShot(p.UserID, p.ID, removed.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.repo.LatestOntologyDocument(p.ID, u.ID, model.OntologyDocEpisode)
	if err != nil || current.Version != doc.Version+1 || strings.Contains(current.Content, "s01-u2") {
		t.Fatal(current, err)
	}
	var history model.ProjectOntologyDocument
	if err := db.First(&history, "id = ?", doc.ID).Error; err != nil || history.Content != doc.Content {
		t.Fatal("original document was overwritten", err)
	}
	gate, err := s.repo.LatestOntologyDocument(p.ID, u.ID, model.OntologyDocGate)
	if err != nil || !slices.Equal(ontologyGateOpen(gate.Content), []string{"s01-u3 H2"}) {
		t.Fatal(gate, err)
	}
	for _, want := range []string{"frame-ref", "other-ref"} {
		var ref model.ShotAssetReference
		if err := db.First(&ref, "id = ?", want).Error; err != nil || (ref.Status == "linked") != (want == "other-ref") {
			t.Fatal(want, ref.Status, err)
		}
	}
	for _, id := range []string{"frame", "other-storyboard", "other-video"} {
		var a model.ShotArtifact
		if err := db.First(&a, "id = ?", id).Error; err != nil || (a.Status == "ready") != (id != "frame") || a.Selected != (id == "other-video") {
			t.Fatal(id, a.Status, a.Selected, err)
		}
	}
	for _, row := range []struct {
		record any
		id     string
	}{{&model.Resource{}, "tail-image"}, {&model.AssetVersion{}, "tail-version"}, {&model.ProjectAssetLink{}, "library-link"}} {
		if err := db.First(row.record, "id = ?", row.id).Error; err != nil {
			t.Fatal(row.id, err)
		}
	}
	if _, err := s.applyOntologyShotsTask(task, nil, meta, result); appReason(err) != string(ReasonConflict) {
		t.Fatal("old director output can recreate the removed unit", err)
	}
	after, err := s.repo.ProjectUnitShots(p.ID, u.ID)
	if err != nil || len(after) != 2 || after[0].ID != prev.ID || after[1].ID != next.ID ||
		after[0].CurrentRevisionID != prev.CurrentRevisionID || after[1].CurrentRevisionID != next.CurrentRevisionID {
		t.Fatal(after, err)
	}
	if _, err := s.repo.ShotForProject(p.ID, removed.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("removed stable ID still exists", err)
	}
	stale := map[string]any{
		"mode": "video", "config": map[string]any{},
		"metadata":            map[string]any{"domainProjectId": p.ID, "shotId": next.ID},
		videoChainMetadataKey: map[string]any{"sourceShotId": removed.ID, "sourceRevisionId": removed.CurrentRevisionID, "sourceVideoResourceId": "old-video", "frameResourceId": "tail-image"},
	}
	if _, err := s.prepareDramaVideoSubmission(p.UserID, stale, "下一段", false); appReason(err) != string(ReasonFailedPrecondition) {
		t.Fatal("actual submission preparation must refuse a cached frame from the deleted predecessor", err)
	}
}
