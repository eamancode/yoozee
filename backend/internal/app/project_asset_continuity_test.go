package app

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestAssetsContinuityReusesIdentityAndLookAcrossEpisodes(t *testing.T) {
	svc, db := newProjectWorkflowV2TestService(t)
	project, _ := seedWorkflowProject(t, db)
	person := seedLinXiaoman(t, svc, db, project.ID)
	scenes := "## 内·电梯里·夜\n（林小满换上深灰西服。）\n林小满：走吧。"
	writesFor := func(unitID string, episode int, name, label string) ([]repository.DramaAssetWrite, DramaAssetsExtractOutcome) {
		t.Helper()
		live, err := svc.repo.ProjectForUser("user-1", project.ID)
		if err != nil {
			t.Fatal(err)
		}
		delta := &drama.AssetVisualDelta{Clothing: "深灰西服", Evidence: "林小满换上深灰西服"}
		if episode == 1 {
			delta.Hair = "齐耳短发"
		}
		unit := &model.ProjectUnit{ID: unitID, Title: "测试剧集", SourceText: plainTextToHTML(scenes)}
		index, err := svc.dramaAssetIndexOf("user-1", project.ID)
		if err != nil {
			t.Fatal(err)
		}
		rows := []drama.AssetsExtractRow{{
			AssetID: person.Asset.ID, Category: drama.AssetCharacter, Name: name, FirstScene: 1, Appearances: []int{1},
			Variants: []drama.AssetsVariant{
				{Label: label, Kind: drama.AssetVariantCostume, Scenes: []int{1}, Appearance: "穿深灰西服", PromptEn: "dark grey suit", Delta: delta},
				{Label: "夜读", Kind: drama.AssetVariantState, Scenes: []int{1}, Appearance: "看书", PromptEn: "reading"},
			},
		}}
		meta := &dramaAssetsMeta{SourceHash: assetExtractionSourceHash(unit), UnitID: unit.ID, Episode: episode, Scenes: 1}
		writes, result, err := svc.dramaAssetWrites("user-1", live, unit, meta, index, drama.AssetsExtractResult{Rows: rows}, nil, drama.MasterPalette{}, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.repo.ApplyDramaAssetsAtRevision("user-1", live.ID, writes, nil, time.Now(), &live.Revision); err != nil {
			t.Fatal(err)
		}
		if err := svc.repo.ApplyDramaAssetsAtRevision("user-1", live.ID, nil, nil, time.Now(), &live.Revision); !errors.Is(err, repository.ErrDramaAssetChanged) {
			t.Fatalf("stale plan committed: %v", err)
		}
		return writes, result
	}
	_, first := writesFor("episode-first", 1, "林小满", "赴约西服")
	_, second := writesFor("episode-second", 2, "林护士", "电梯西装")
	if first.Created != 1 || second.Created != 0 || first.Skipped != 1 || second.Skipped != 1 {
		t.Fatalf("unexpected design counts: first=%+v second=%+v", first, second)
	}
	for _, result := range []DramaAssetsExtractOutcome{first, second} {
		if len(result.Occurrences) != 2 || result.Occurrences[0].AssetID != person.Asset.ID || result.Occurrences[1].AssetID != first.Occurrences[1].AssetID {
			t.Fatalf("identity or look changed: %+v", result.Occurrences)
		}
	}
}

func TestAssetSettingsEditPreservesImageAndPinnedVersion(t *testing.T) {
	svc, db, projectID, asset := seedProjectVisualAsset(t, model.AssetCategoryEnvironment)
	bound, err := svc.BindProjectAssetImage("user-1", projectID, asset.ID, BindProjectAssetImageRequest{ResourceID: "image-1", ExpectedVersionID: asset.PrimaryVersionID})
	if err != nil {
		t.Fatal(err)
	}
	ref := model.ShotAssetReference{ID: "existing-ref", ShotID: "existing-shot", AssetVersionID: bound.PrimaryVersionID, Role: "reference", Status: "linked", CreatedAt: time.Now()}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	edit := UpdateProjectAssetRequest{Settings: &ProjectAssetSettingsPatch{ExpectedVersionID: bound.PrimaryVersionID, Fields: map[string]string{"description": "青砖回廊，右侧三扇窗"}}}
	updated, err := svc.UpdateProjectAsset("user-1", projectID, asset.ID, edit)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PrimaryVersionID == bound.PrimaryVersionID || updated.StorageKey != bound.StorageKey || !updated.SettingsImageOutdated ||
		updated.Visual == nil || updated.Visual.Definition["description"] != "青砖回廊，右侧三扇窗" || updated.Visual.Definition["prompt"] != nil {
		t.Fatalf("wrong edited version: %+v", updated)
	}
	if err := db.First(&ref, "id = ?", ref.ID).Error; err != nil || ref.AssetVersionID != bound.PrimaryVersionID {
		t.Fatalf("pinned reference changed: %+v %v", ref, err)
	}
	old, err := svc.repo.AssetVersion(bound.PrimaryVersionID)
	if err != nil || !strings.Contains(old.DefinitionJSON, "磨损铜印") {
		t.Fatalf("previous settings lost: %+v %v", old, err)
	}
	if _, err := svc.UpdateProjectAsset("user-1", projectID, asset.ID, edit); err == nil {
		t.Fatal("stale edit accepted")
	}
	board, err := svc.storyboardAssets("user-1", projectID)
	if err != nil || len(board) != 1 || board[0].PrimaryVersionID != bound.PrimaryVersionID || board[0].Visual.Definition["description"] != "磨损铜印" {
		t.Fatalf("storyboard picked draft instead of aligned image and text: %+v %v", board, err)
	}
	var count int64
	if err := db.Model(&model.Task{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("editing created model tasks: %d %v", count, err)
	}
}

func TestAssetsContinuityCoverageReplacesOnlyEditedUnit(t *testing.T) {
	raw := `{"style_anchor":{"palette":["blue"]},"asset_occurrences":{"episode-2":[{"assetId":"b","scene":2}]}}`
	unit := &model.ProjectUnit{Title: "同一人物", SourceText: "<p>林小满在电梯里。</p>"}
	source := assetExtractionSourceHash(unit)
	out, err := mergeProjectAssetOccurrences(raw, "episode-1", []projectAssetOccurrence{{AssetID: "a", Scene: 1}}, projectAssetCoverage{SourceHash: source})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Occurrences map[string][]projectAssetOccurrence `json:"asset_occurrences"`
		Coverage    map[string]projectAssetCoverage     `json:"asset_coverage"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Occurrences) != 2 || got.Coverage["episode-1"].SourceHash != source || !strings.Contains(out, "style_anchor") {
		t.Fatalf("unrelated unit or workflow facts lost: %s %v", out, err)
	}
	unit.SourceText = "<p>林小满走出电梯。</p>"
	if assetExtractionSourceHash(unit) == source {
		t.Fatal("edited page did not invalidate coverage")
	}
}

func TestAssetsExtractionQueueSkipsCurrentCoverageInStoryOrder(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, episodes := seedEpisodeProject(t, svc, db)
	seedScenePalettes(t, svc, db, project)
	current, err := svc.repo.ProjectForUser("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := svc.projectAssetExtractionQueue("user-1", current, episodes[1].ID)
	if err != nil || !slices.Equal(queue, []string{episodes[0].ID, episodes[1].ID}) {
		t.Fatalf("initial full draft order: %v %v", queue, err)
	}
	usage, err := svc.DramaStepUsage("user-1", current.ID, drama.StepAssetsExtract, nil)
	if err != nil {
		t.Fatal(err)
	}
	coverage := projectAssetCoverage{SourceHash: assetExtractionSourceHash(&episodes[0]), Contract: usage.Contract, SkillVersion: usage.Skill.ContentHash}
	current.BibleJSON, err = mergeProjectAssetOccurrences(current.BibleJSON, episodes[0].ID, nil, coverage)
	if err != nil {
		t.Fatal(err)
	}
	queue, err = svc.projectAssetExtractionQueue("user-1", current, episodes[0].ID)
	if err != nil || !slices.Equal(queue, []string{episodes[1].ID}) {
		t.Fatalf("covered episode has not been skipped: %v %v", queue, err)
	}
}

func TestAssetsContinuityAgeLookDoesNotInheritChildhoodAppearance(t *testing.T) {
	parent := model.Asset{ID: "xiaoling", Title: "小锡玲"}
	base := map[string]any{
		"appearance": "八九岁的孩子", "identityAppearance": "八九岁，颧骨突出",
		"physique": "八九岁的瘦小身材", "multiViewPrompt": "portrait of a nine-year-old",
	}
	_, variant, err := newVariantAsset("user-1", parent, base, "高中", drama.AssetVariantState, "十六七岁，灰色校服", "", assetVariantMeta{
		VisualDelta: &drama.AssetVisualDelta{AgeStage: "高中阶段"},
	}, dramaImageNone, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(variant.DefinitionJSON, "八九岁") || strings.Contains(variant.DefinitionJSON, "nine-year-old") || !strings.Contains(variant.DefinitionJSON, "高中阶段") {
		t.Fatalf("age-stage instructions conflict: %s", variant.DefinitionJSON)
	}
}
