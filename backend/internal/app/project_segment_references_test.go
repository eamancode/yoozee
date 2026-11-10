package app

import (
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"reflect"
	"strings"
	"testing"
	"time"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func seedSegmentBindings(t *testing.T) (*Service, *gorm.DB, model.Project, model.ProjectUnit, model.Shot) {
	t.Helper()
	s, db := newProjectWorkbenchReadTestService(t)
	if err := db.AutoMigrate(&model.DramaShotlist{}); err != nil {
		t.Fatal(err)
	}
	p := seedWorkbenchProject(t, db)
	u := model.ProjectUnit{ID: "segment-unit", ProjectID: p.ID, Title: "第一集", Status: model.ProjectUnitStatusReady}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	seg := drama.Segment{ID: "S01", Scene: "s01", Title: "单元", Dur: 15, Mode: "lock_identity", Lang: "zh", Shots: []drama.SubShot{{At: 0, Text: "人物拿起钥匙", Size: "中景"}}, Verbatim: "15秒，@图1人物、@图2服装、@图3场景、@图4道具。本段不使用背景音乐。"}
	for i, category := range []model.AssetCategory{model.AssetCategoryCharacter, model.AssetCategoryCharacter, model.AssetCategoryEnvironment, model.AssetCategoryProp} {
		id := fmt.Sprintf("binding-asset-%d", i)
		a := model.Asset{ID: id, UserID: p.UserID, Category: category, Kind: "entity", Title: id, PrimaryVersionID: id + "-v1", Status: model.AssetVersionStatusConfirmed, PayloadJSON: `{}`}
		if i == 1 {
			a.ParentAssetID = "binding-asset-0"
			a.VariantLabel = "夜值服装"
		}
		for _, record := range []any{&a, &model.ProjectAssetLink{ID: id + "-link", ProjectID: p.ID, AssetID: id}} {
			if err := db.Create(record).Error; err != nil {
				t.Fatal(err)
			}
		}
		for n := 1; n <= 2; n++ {
			v := fmt.Sprintf("%s-v%d", id, n)
			for _, record := range []any{&model.AssetVersion{ID: v, AssetID: id, Version: n, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: `{"appearance":"测试资产"}`}, &model.AssetRepresentation{ID: v + "-rep", TaskID: v + "-task", AssetVersionID: v, ResourceID: v + "-image", MediaType: "image/png", Role: "primary"}} {
				if err := db.Create(record).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
		seg.Refs = append(seg.Refs, drama.Ref{File: a.PrimaryVersionID, Role: map[bool]string{true: "identity", false: "prop"}[i < 2], Note: id})
	}
	raw, _ := json.Marshal(seg)
	shot, err := s.CreateProjectShot(p.UserID, p.ID, CreateProjectShotRequest{UnitID: u.ID, Title: "S01", Revision: ShotRevisionInput{Spec: raw}})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, p, u, shot
}

func TestSegmentAssetBindingsLifecycle(t *testing.T) {
	s, db, p, u, shot := seedSegmentBindings(t)
	ws, err := s.ProjectUnitWorkspace(p.UserID, p.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.ShotReferences) != 4 || len(ws.Assets) != 4 {
		t.Fatalf("initial bindings/assets: %d/%d", len(ws.ShotReferences), len(ws.Assets))
	}
	for _, ref := range ws.ShotReferences {
		if ref.Source != "segment" || len(ref.ReferencedVersion.Representations) != 1 {
			t.Fatalf("missing pinned preview: %+v", ref)
		}
	}
	for i := 0; i < 4; i++ {
		t.Run(fmt.Sprintf("kind-%d", i), func(t *testing.T) {
			version := fmt.Sprintf("binding-asset-%d-v1", i)
			refs, _ := s.repo.ShotAssetReferencesForShot(shot.ID)
			var old model.ShotAssetReference
			for _, r := range refs {
				if r.AssetVersionID == version {
					old = r
				}
			}
			if old.ID == "" {
				t.Fatal("old reference missing")
			}
			newVersion := fmt.Sprintf("binding-asset-%d-v2", i)
			r, err := s.LinkShotAsset(p.UserID, p.ID, shot.ID, LinkShotAssetRequest{AssetVersionID: newVersion, Role: "reference", ReplaceReferenceID: old.ID})
			if err != nil {
				t.Fatal(err)
			}
			if r.AssetVersionID != newVersion {
				t.Fatalf("replacement: %+v", r)
			}
			ws, err := s.ProjectUnitWorkspace(p.UserID, p.ID, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(ws.ShotReferences) != 4 {
				t.Fatalf("replacement stacked refs: %d", len(ws.ShotReferences))
			}
			if err := s.UnlinkShotAsset(p.UserID, p.ID, shot.ID, r.ID); err != nil {
				t.Fatal(err)
			}
			views, err := s.DramaSegments(p.UserID, p.ID, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(views[0].Spec.Refs) != 3 {
				t.Fatal("cancel did not update spec")
			}
			compiled := drama.CompileVerbatim(views[0].Spec, drama.Model{ID: "seedance-2.5"})
			if len(compiled.Files) != 3 || strings.Contains(views[0].Spec.Verbatim, "@图4") {
				t.Fatalf("cancel left stale labels: %+v", compiled.Files)
			}
			if _, err := s.LinkShotAsset(p.UserID, p.ID, shot.ID, LinkShotAssetRequest{AssetVersionID: version, Role: "reference"}); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Independent palette/output associations survive later segment edits.
	extra := model.ShotAssetReference{ID: "independent-style", ShotID: shot.ID, AssetVersionID: "binding-asset-0-v1", Role: "style", Status: "linked", CreatedAt: time.Now()}
	if err := db.Create(&extra).Error; err != nil {
		t.Fatal(err)
	}
	refs, _ := s.repo.ShotAssetReferencesForShot(shot.ID)
	for _, r := range refs {
		if r.Source == "segment" {
			if err := s.UnlinkShotAsset(p.UserID, p.ID, shot.ID, r.ID); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	var count int64
	db.Model(&model.ShotAssetReference{}).Where("id = ?", extra.ID).Count(&count)
	if count != 1 {
		t.Fatal("independent reference removed")
	}
}

func TestSegmentAssetBindingsRollbackAndOwnership(t *testing.T) {
	s, db, p, u, shot := seedSegmentBindings(t)
	before, _ := s.repo.ShotForProject(p.ID, shot.ID)
	refsBefore, _ := s.repo.ShotAssetReferencesForShot(shot.ID)
	if err := db.Exec(`CREATE TRIGGER fail_segment_bind BEFORE INSERT ON shot_asset_references BEGIN SELECT RAISE(ABORT, 'test storage failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkShotAsset(p.UserID, p.ID, shot.ID, LinkShotAssetRequest{AssetVersionID: "binding-asset-0-v2", Role: "reference", ReplaceReferenceID: refsBefore[0].ID}); err == nil {
		t.Fatal("storage failure accepted")
	}
	after, _ := s.repo.ShotForProject(p.ID, shot.ID)
	refsAfter, _ := s.repo.ShotAssetReferencesForShot(shot.ID)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(refsBefore, refsAfter) {
		t.Fatal("partial revision/binding change")
	}
	db.Exec(`DROP TRIGGER fail_segment_bind`)
	if _, err := s.LinkShotAsset("other-user", p.ID, shot.ID, LinkShotAssetRequest{AssetVersionID: "binding-asset-0-v2", Role: "reference"}); err == nil {
		t.Fatal("foreign owner accepted")
	}
	if _, err := s.WriteDramaSegments(p.UserID, p.ID, DramaSegmentsWriteRequest{UnitID: u.ID, Segments: []json.RawMessage{json.RawMessage(`{"id":"S02","scene":"s01","dur":15,"mode":"lock_identity","refs":[{"file":"not-in-project","role":"identity"}],"shots":[{"at":0,"text":"人物"}]}`)}}); err == nil {
		t.Fatal("foreign reference accepted")
	}
	var count int64
	db.Model(&model.Shot{}).Where("project_id = ?", p.ID).Count(&count)
	if count != 1 {
		t.Fatal("invalid segment partially saved")
	}
}
