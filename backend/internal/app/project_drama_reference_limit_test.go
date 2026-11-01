package app

import (
	"fmt"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestPackedSegmentPersistsAllNineReferences(t *testing.T) {
	svc, db, project, unit := newProjectDramaTestService(t, "seedance-2.0")
	seg := drama.Segment{ID: "S01", Scene: "一", Mode: drama.ModeTextOnly, Dur: 5, Shots: []drama.SubShot{{Size: "中景", Text: "人物拾起信封"}}}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("asset-%d", i)
		version := id + "-v1"
		for _, row := range []any{
			&model.Asset{ID: id, UserID: project.UserID, Kind: "image", Category: "prop", Status: "ready", PrimaryVersionID: version, Title: id},
			&model.AssetVersion{ID: version, AssetID: id, Version: 1, Status: "ready"},
			&model.ProjectAssetLink{ID: id + "-link", ProjectID: project.ID, AssetID: id},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if i < 9 {
			seg.Refs = append(seg.Refs, drama.Ref{File: version, Role: "prop"})
		}
	}
	input, err := dramaSegmentShotInput(seg, nil)
	if err != nil {
		t.Fatal(err)
	}
	shots, err := svc.ReplaceProjectUnitShots(project.UserID, project.ID, unit.ID, ReplaceProjectUnitShotsRequest{ExpectedShotIDs: []string{}, Shots: []ReplaceProjectUnitShotInput{input}})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.ShotAssetReference{}).Where("shot_id = ?", shots[0].ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 9 {
		t.Fatalf("persisted %d references, want all 9", count)
	}
	refs, err := svc.unitShotReferences(&project, shots[0].ID, input, time.Now())
	if err != nil || len(refs) != 9 {
		t.Fatalf("partial repack: refs=%d err=%v", len(refs), err)
	}
	input.AssetVersionIDs = append(input.AssetVersionIDs, "asset-9-v1")
	if _, err := svc.unitShotReferences(&project, shots[0].ID, input, time.Now()); err == nil {
		t.Fatal("accepted more than model limit")
	}
}
