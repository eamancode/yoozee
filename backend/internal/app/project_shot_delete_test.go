package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// Six same-scene cards, including the three-shot S05, are packed through the
// ordinary writer. The test also exercises the source used by a later repack.
func TestStoryboardDeletePrunesOnlyConfirmedUnitAndRepackSource(t *testing.T) {
	svc, db, project, unit := newProjectDramaTestService(t, "seedance-2.5")
	var rows []json.RawMessage
	var sections []drama.SectionOutput
	for card := 1; card <= 6; card++ {
		id := fmt.Sprintf("E01S01-P%d", card)
		sections = append(sections, drama.SectionOutput{ID: id, Scene: "E01S01", No: card, Lines: [2]int{card, card}, Genre: drama.ShotGenreWenxi30})
		count := 1
		if card == 5 {
			count = 3
		}
		for sub := 1; sub <= count; sub++ {
			sid := fmt.Sprintf("u%d-shot%d", card, sub)
			row, err := json.Marshal(map[string]any{"spec": map[string]any{
				"id": sid, "section": id, "scene": "E01S01", "genre": drama.ShotGenreWenxi30, "est": 3,
				"action": "人物推开门 " + sid, "end_state": sid + " 门开", "size": "中景",
			}})
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, row)
		}
	}
	packed, err := svc.WriteDramaShotlist(project.UserID, project.ID, unit.ID, DramaShotlistWriteRequest{Rows: rows, Sections: sections, AcceptIssues: true})
	if err != nil || len(packed.Segments) != 6 {
		t.Fatalf("pack six fixture cards: %d, %v", len(packed.Segments), err)
	}
	target, next := packed.Segments[4], packed.Segments[5]
	if target.Spec.ID != "S05" || len(target.Spec.Shots) != 3 || next.Spec.ContinuityBridge == nil || next.Spec.ContinuityBridge.PrevSegment != "S05" {
		t.Fatalf("invalid fixture targets: %+v, %+v", target.Spec, next.Spec)
	}
	art := addStoryboardArtifact(t, svc, db, project.ID, target)
	confirmed := DeleteProjectShotRequest{&target.RevisionID, []string{art}}
	otherUnit := model.ProjectUnit{ID: "second-episode", ProjectID: project.ID, Title: "第二集"}
	if err := db.Create(&otherUnit).Error; err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateProjectShot(project.UserID, project.ID, CreateProjectShotRequest{UnitID: otherUnit.ID, Title: "S05", Description: "第二集的同编号镜头"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stale := range []DeleteProjectShotRequest{{&target.RevisionID, []string{}}, {ptr("obsolete-revision"), []string{art}}, {ptr(""), []string{art}}} {
		if err := svc.DeleteProjectShotChecked(project.UserID, project.ID, target.ShotID, stale); err == nil {
			t.Fatal("stale confirmation removed the card")
		}
	}

	// Fail at the final DELETE, after both source and successor have been revised.
	if err := db.Exec("CREATE TRIGGER fail_fixture_delete BEFORE DELETE ON shots BEGIN SELECT RAISE(ABORT, 'fixture failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteProjectShotChecked(project.UserID, project.ID, target.ShotID, confirmed); err == nil {
		t.Fatal("fault injection did not abort deletion")
	}
	before, err := svc.DramaShotlist(project.UserID, project.ID, unit.ID)
	if err != nil || before.Version != packed.Shotlist.Version || len(before.Shotlist.Shots) != len(rows) {
		t.Fatalf("source not rolled back: %+v %v", before, err)
	}
	storedNext, err := svc.repo.ShotForProject(project.ID, next.ShotID)
	if err != nil || storedNext.CurrentRevisionID != next.RevisionID {
		t.Fatalf("successor rollback: %+v %v", storedNext, err)
	}
	if err := db.Exec("DROP TRIGGER fail_fixture_delete").Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteProjectShotChecked(project.UserID, project.ID, target.ShotID, confirmed); err != nil {
		t.Fatal(err)
	}
	after, err := svc.DramaSegments(project.UserID, project.ID, unit.ID)
	if err != nil || len(after) != 5 || after[4].ShotID != next.ShotID || after[4].Position != 4 {
		t.Fatalf("remaining cards: %+v %v", after, err)
	}
	for i := 0; i < 4; i++ {
		if after[i].ShotID != packed.Segments[i].ShotID || after[i].RevisionID != packed.Segments[i].RevisionID {
			t.Fatalf("unrelated S%02d changed", i+1)
		}
	}
	if b := after[4].Spec.ContinuityBridge; b == nil || b.PrevSegment != "S04" || !strings.Contains(b.HandoffState, "u4-shot1") || b.BridgeFrame != "" || b.BridgeClip != "" {
		t.Fatalf("repaired successor bridge: %+v", b)
	}
	if _, err := svc.CompileProjectShot(project.UserID, project.ID, target.ShotID, CompileProjectShotRequest{}); err == nil {
		t.Fatal("deleted shot can still compile")
	}
	if err := svc.repo.CreateShotEvidence(&model.ShotEvidence{ID: "late-evidence", ProjectID: project.ID, ShotID: target.ShotID}); !errors.Is(err, repository.ErrShotGone) {
		t.Fatalf("late evidence write after delete: %v", err)
	}
	if inserted, err := svc.repo.CreateProjectAssetCandidate(&model.ProjectAssetCandidate{ID: "late-candidate", ProjectID: project.ID, ShotID: target.ShotID}); inserted || !errors.Is(err, repository.ErrShotGone) {
		t.Fatalf("late candidate write after delete: inserted=%v err=%v", inserted, err)
	}
	ref := &model.ShotAssetReference{ID: "late-binding", ShotID: target.ShotID, AssetVersionID: "shared", Role: "style"}
	if err := svc.repo.UpsertShotAssetReferenceAndInvalidate(project.ID, ref, time.Now()); !errors.Is(err, repository.ErrShotGone) {
		t.Fatalf("late binding after delete: %v", err)
	}
	pr, err := svc.DramaProgress(project.UserID, project.ID)
	if err != nil || slices.ContainsFunc(pr.Segments, func(row drama.SegmentProgress) bool { return row.ShotID == target.ShotID }) {
		t.Fatalf("deleted shot in progress: %+v %v", pr, err)
	}
	if _, err := svc.repo.ShotForProject(project.ID, other.ID); err != nil {
		t.Fatal("same-numbered card in second episode was removed", err)
	}
	stored, err := svc.repo.DramaShotlistForUnit(project.ID, unit.ID)
	if err != nil || stored.Version != before.Version+1 || strings.Contains(stored.RowsJSON, "u5-shot") || strings.Contains(stored.ShotlistJSON, "u5-shot") || strings.Contains(stored.PackedSectionsJSON, "E01S01-P5") {
		t.Fatalf("pruned source: %+v %v", stored, err)
	}
	_, err = svc.WriteDramaShotlist(project.UserID, project.ID, unit.ID, DramaShotlistWriteRequest{
		Rows: rows, Sections: sections, KeepSegments: true, AcceptIssues: true, sourceVersion: before.Version,
	})
	if err == nil {
		t.Fatal("concurrently prepared repack restored stale rows")
	}
	repacked, err := svc.RepackDramaShotlist(project.UserID, project.ID, unit.ID, DramaShotlistRepackRequest{AcceptIssues: true})
	if err != nil || len(repacked.Segments) != 5 {
		t.Fatalf("repack: %d segments, %v", len(repacked.Segments), err)
	}
	for i, v := range repacked.Segments {
		if v.ShotID != after[i].ShotID || strings.Contains(v.Spec.Canonical(), "u5-shot") {
			t.Fatalf("repacked neighbor %d lost its identity or revived a shot: %+v", i, v)
		}
	}
}
