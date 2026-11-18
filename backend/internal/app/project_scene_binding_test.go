package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestSceneReferenceReviewBlocksWrongImageAndStaleSubmission(t *testing.T) {
	f := newVideoChainFixture(t, "E01S01")
	if err := f.db.Model(&model.ProjectUnit{}).Where("id = ?", f.req.UnitID).
		Update("source_text", "## 内·档案室·夜\n林深在档案室停下。\n\n## 外·堆场·日\n林深经过堆场。").Error; err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ id, title string }{{"room", "档案室"}, {"yard", "堆场"}} {
		versionID := fixture.id + "-v1"
		for _, value := range []any{
			&model.Asset{ID: fixture.id, UserID: "user-1", Category: model.AssetCategoryEnvironment, Title: fixture.title, Kind: "entity", PrimaryVersionID: versionID, Status: model.AssetVersionStatusConfirmed},
			&model.AssetVersion{ID: versionID, AssetID: fixture.id, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: "{}"},
			&model.ProjectAssetLink{ID: fixture.id + "-link", ProjectID: f.projectID, AssetID: fixture.id},
			&model.AssetRepresentation{ID: fixture.id + "-rep", TaskID: fixture.id + "-task", AssetVersionID: versionID, ResourceID: f.frameID, Role: "primary", MediaType: "image/png"},
		} {
			if err := f.db.Create(value).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	scenes, err := mergeSceneAssetsDocument("{}", f.req.UnitID, 1, map[string]dramaSceneAssets{
		"E01S01": {UnitID: f.req.UnitID, Environments: []string{"room"}},
		"E01S02": {UnitID: f.req.UnitID, Environments: []string{"yard"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Project{}).Where("id = ?", f.projectID).Update("bible_json", scenes).Error; err != nil {
		t.Fatal(err)
	}
	seg := drama.Segment{ID: "S01", Scene: "E01S01", Dur: 10, Mode: drama.ModeLockIdentity, Lang: "zh",
		Refs:     []drama.Ref{{File: "yard-v1", Role: "location", Note: "堆场"}},
		Shots:    []drama.SubShot{{At: 0, Text: "林深推开木门。", Sound: "保留台词"}},
		Verbatim: "场景背景\n室内档案室。\n生效参考素材\n@图1 为场景地点。\n场景地图\n木门位于正面。\n动作\n林深推开木门。\n声音\n保留台词",
	}
	encoded, _ := json.Marshal(seg)
	if err := f.db.Model(&model.ShotRevision{}).Where("id = ?", "rs0").Update("spec_json", string(encoded)).Error; err != nil {
		t.Fatal(err)
	}
	assertSceneError := func(err error) {
		t.Helper()
		var appErr *AppError
		if !errors.As(err, &appErr) || appErr.Reason != sceneCheckReason {
			t.Fatalf("expected scene review conflict, got %v", err)
		}
	}
	_, err = f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
	assertSceneError(err)
	req := SceneReferenceUpdate{RevisionID: "rs0", SceneID: "E01S01", VersionID: "room-v1", ResourceID: f.frameID,
		Facts: &drama.SceneFacts{InOut: "内", Layout: "柜台靠墙。", Absent: []string{"木门"}, Anchors: []string{"柜台"}}}
	candidate, err := f.s.ProjectSceneReferenceCandidate("user-1", f.projectID, "s0", req.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	req.FactsRevision = candidate.FactsRevision
	_, err = f.s.UpdateProjectSceneReference("user-1", f.projectID, "s0", req)
	assertSceneError(err)
	req.Sections = map[string]string{"场景地图": "柜台位于正面。"}
	req.Shots = map[int]string{0: "林深停在柜台前。"}
	view, err := f.s.UpdateProjectSceneReference("user-1", f.projectID, "s0", req)
	if err != nil || len(view.Issues) != 0 || view.RevisionID == req.RevisionID {
		t.Fatalf("saving reviewed scene: %+v %v", view, err)
	}
	state, err := f.s.prepareProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil || len(state.Steps) != 1 {
		t.Fatalf("quote preview failed: %+v %v", state, err)
	}
	step := &state.Steps[0]
	prompt := stringValue(step.Input["prompt"])
	if !strings.Contains(prompt, "【本场空间】@图片1") || strings.Contains(prompt, "木门") ||
		!strings.Contains(prompt, "保留台词") || !videoChainHasResource(step.Input["referenceImages"].([]any), f.frameID) {
		t.Fatalf("quote inputs do not match: %q %v", prompt, step.Input["referenceImages"])
	}
	shot, err := f.s.repo.ShotForProject(f.projectID, "s0")
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.s.repo.ShotRevisionForShot("s0", shot.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := f.s.repo.ProjectForUser("user-1", f.projectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.enforceSceneReferenceSubmission("user-1", project, shot, current, step.Input, step.Input["metadata"].(map[string]any), nil, prompt, step); err != nil {
		t.Fatal(err)
	}
	assertSceneError(f.s.enforceSceneReferenceSubmission("user-1", project, shot, current, step.Input, step.Input["metadata"].(map[string]any), nil, prompt+"\n另一个场景", step))
	direct := cloneVideoChainInput(step.Input)
	direct["referenceImages"].([]any)[0].(map[string]any)["dataUrl"] = "data:image/png;base64,d3Jvbmc="
	err = f.s.enforceSceneReferenceSubmission("user-1", project, shot, current, direct, direct["metadata"].(map[string]any), nil, prompt, nil)
	if err != nil || direct["referenceImages"].([]any)[0].(map[string]any)["dataUrl"] != nil {
		t.Fatalf("direct submission did not choose server-owned image bytes: %v", err)
	}
	_, err = f.s.UpdateProjectSceneReference("user-1", f.projectID, "s0", req)
	assertSceneError(err)
	if err := f.db.Model(&model.AssetRepresentation{}).Where("id = ?", "room-rep").Update("resource_id", "image-replaced").Error; err != nil {
		t.Fatal(err)
	}
	_, err = f.s.videoChainInput(&model.Task{ID: "test-controller", ProjectID: f.projectID, UserID: "user-1"}, step)
	assertSceneError(err)
	policy, err := f.s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	quotedInput, _ := json.Marshal(step.Input)
	err = f.s.createTaskWithinStorageQuota(&model.Task{ID: "test-stale-submit", ProjectID: f.projectID, UserID: "user-1", Type: "canvas_video", InputJSON: string(quotedInput)}, nil, policy)
	assertSceneError(err)
	_, err = f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
	assertSceneError(err)
	if err := f.db.Model(&model.AssetRepresentation{}).Where("id = ?", "room-rep").Update("resource_id", f.frameID).Error; err != nil {
		t.Fatal(err)
	}
	text := SceneReferenceUpdate{RevisionID: view.RevisionID, SceneID: "E01S01", TextOnly: true}
	view, err = f.s.UpdateProjectSceneReference("user-1", f.projectID, "s0", text)
	if err != nil || !view.TextOnly {
		t.Fatalf("text only: %+v %v", view, err)
	}
	compiled, err := f.s.CompileProjectShot("user-1", f.projectID, "s0", CompileProjectShotRequest{})
	if err != nil || len(compiled.Result.Files) != 0 || strings.Contains(compiled.Result.Prompt, "@图片") {
		t.Fatalf("text only compilation: %+v %v", compiled, err)
	}
	if f.uploads.Load() != 0 {
		t.Fatal("review or quote uploaded a media file")
	}
	var tasks int64
	if err := f.db.Model(&model.Task{}).Count(&tasks).Error; err != nil || tasks != 0 {
		t.Fatalf("unexpected tasks %d: %v", tasks, err)
	}
}

func TestSceneBindingUsesDistinctInsideAndOutsideCandidates(t *testing.T) {
	unit := model.ProjectUnit{ID: "ep", SourceText: "## 内·店铺·日\n进门。\n## 外·店铺·夜\n在路边。"}
	in := sceneEnvironmentCandidate{ID: "indoor", Title: "内·店铺"}
	out := sceneEnvironmentCandidate{ID: "outdoor", Title: "外·店铺"}
	got := unitSceneEnvironments(unit, 2, []sceneEnvironmentCandidate{out, in})
	if got["E02S01"].Environments[0] != in.ID || got["E02S02"].Environments[0] != out.ID {
		t.Fatalf("scene asset match = %#v", got)
	}
	unresolved := unitSceneEnvironments(unit, 2, []sceneEnvironmentCandidate{{ID: "a", Title: "店铺"}, {ID: "b", Title: "店铺"}})
	if len(unresolved) != 0 {
		t.Fatalf("ambiguous asset names must remain unbound: %#v", unresolved)
	}
}
