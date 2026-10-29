package app

import (
	"encoding/json"
	"image/color"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// addArtifact 给一段挂一个按当前版本算好指纹的产物（ready）。
func addArtifact(t *testing.T, svc *Service, db *gorm.DB, projectID string, view DramaSegmentView, kind string) string {
	t.Helper()
	id := newID()
	artifact := model.ShotArtifact{ID: id, ProjectID: projectID, UnitID: view.UnitID, ShotID: view.ShotID, RevisionID: view.RevisionID, Type: kind, Version: 1, Status: "ready", Selected: true,
		InputHash: svc.artifactInputHash(view.ShotID, view.RevisionID, kind), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

// 卡 G 之前装的段（镜头没有 refs、首镜没写镜头目的时段标题为空）带着分镜图和视频：部署后第一次只重跑别的节、改别的场的戏类时，
// 保留下来的段装成新格式（每镜 refs、兜底标题），分镜图和视频都不过期；之后改这一段的镜头文字，分镜图和视频才过期。
func TestKeptSegmentsStayFreshAcrossCardGFormat(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	project = qichuangOnModel(t, svc, db, project, "seedance-2.0")
	// 角色有三视图，段才有人物参考（每镜 refs 才有内容）。
	for _, a := range projectAssetsByTitle(t, svc, project.ID) {
		if a.Category == "character" {
			if _, err := svc.ReplaceProjectCharacterRepresentations("user-1", project.ID, a.ID, ReplaceCharacterRepresentationsRequest{Representations: []CharacterRepresentationInput{{Role: "turnaround_sheet", ResourceID: uploadTestImage(t, svc, "user-1", color.Gray{Y: 128})}}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	upstream := &sectionShotsUpstream{}
	server := httptest.NewServer(upstream)
	defer server.Close()
	queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("整章文戏 15：%v %q", err, done.Error)
	}
	segments, err := svc.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 还原成卡 G 之前的格式，再按这个版本出分镜图和视频。
	type pair struct{ storyboard, video string }
	arts := map[string]pair{}
	sectionOf := map[string]string{}
	for _, seg := range segments {
		spec := seg.Spec
		if len(spec.Shots) == 0 || strings.TrimSpace(spec.Shots[0].Purpose) != "" {
			t.Fatalf("七床文戏 15 的段首镜没有镜头目的：%+v", spec.Shots)
		}
		if spec.Title == "" {
			t.Fatalf("新格式的段标题带首镜动作：%s", spec.ID)
		}
		spec.Title = ""
		for i := range spec.Shots {
			spec.Shots[i].Refs = nil
		}
		raw, _ := json.Marshal(spec)
		if err := db.Model(&model.ShotRevision{}).Where("id = ?", seg.RevisionID).Update("spec_json", string(raw)).Error; err != nil {
			t.Fatal(err)
		}
		arts[seg.ShotID] = pair{addArtifact(t, svc, db, project.ID, seg, drama.ArtifactStoryboard), addArtifact(t, svc, db, project.ID, seg, drama.ArtifactVideo)}
		sectionOf[seg.ShotID] = spec.Section
	}
	if len(segments) < 4 {
		t.Fatalf("七床 2.0 至少 4 节：%d", len(segments))
	}
	fresh := func(path string, atLeastOne bool, skip func(shotID string) bool) {
		t.Helper()
		checked := 0
		for shotID, a := range arts {
			if skip(shotID) {
				continue
			}
			if s, v := artifactStatus(t, db, a.storyboard), artifactStatus(t, db, a.video); s != "ready" || v != "ready" {
				t.Fatalf("%s：没改的段 %s 分镜图 %s、视频 %s，应都还是 ready", path, sectionOf[shotID], s, v)
			}
			checked++
		}
		if atLeastOne && checked == 0 {
			t.Fatalf("%s：没有可核对的段", path)
		}
	}

	// ① 只重跑最后一节：其余各段（格式升级成每镜 refs + 兜底标题）分镜图、视频都不过期。
	last := sectionOf[segments[len(segments)-1].ShotID]
	plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.onlyScenesRun(project.ID, unit.ID, plan, newDramaRunMeta(plan, unit.ID), nil, []string{last})
	if err != nil {
		t.Fatal(err)
	}
	req := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": server.URL, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	req.Input["metadata"].(map[string]any)["dramaRun"] = run
	insertDramaTestTask(t, svc, db, project.ID, req)
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("只重跑 %s：%v %q", last, err, done.Error)
	}
	fresh("只重跑一节", true, func(shotID string) bool { return sectionOf[shotID] == last })
	upgraded, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	withRefs := false
	for _, seg := range upgraded {
		for _, sh := range seg.Spec.Shots {
			withRefs = withRefs || len(sh.Refs) > 0
		}
	}
	if upgraded[0].Spec.Title == "" || !withRefs {
		t.Fatalf("保留的段已装成新格式（兜底标题、每镜 refs）：%q refs=%v", upgraded[0].Spec.Title, withRefs)
	}

	// ② 把第 2 场一节改成武戏：第 1 场的段（别的场）分镜图、视频都不过期。
	queueRegenreTask(t, svc, db, project, unit, server.URL, "E01S02-P2", "wuxi")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("改武戏：%v %q", err, done.Error)
	}
	fresh("改戏类", true, func(shotID string) bool { return !strings.HasPrefix(sectionOf[shotID], "E01S01-") })

	// ③ 逐张改第 1 段一个镜头的动作：这一段的分镜图和视频过期，别的段不动。
	after, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	target := after[0]
	view, err := svc.DramaShotlist("user-1", project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	var edited json.RawMessage
	for _, sh := range view.Shotlist.Shots {
		if sh.ID == target.Spec.Shots[0].ShotID {
			sh.Action = "林小满一把推开七床的门。"
			edited, _ = json.Marshal(sh)
		}
	}
	segRaw, _ := json.Marshal(target.Spec)
	if _, err := svc.WriteDramaSegmentsChecked("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{segRaw}, Shots: []json.RawMessage{edited}, ShotlistVersion: view.Version}); err != nil {
		t.Fatal(err)
	}
	if a := arts[target.ShotID]; artifactStatus(t, db, a.video) != "stale" || artifactStatus(t, db, a.storyboard) != "stale" {
		t.Fatalf("改了镜头动作，这一段的分镜图 %s、视频 %s 应过期", artifactStatus(t, db, a.storyboard), artifactStatus(t, db, a.video))
	}
	fresh("改别的段", false, func(shotID string) bool {
		return shotID == target.ShotID || !strings.HasPrefix(sectionOf[shotID], "E01S01-")
	})
}
