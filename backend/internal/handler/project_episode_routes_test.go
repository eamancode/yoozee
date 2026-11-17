package handler

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"yingce/backend/internal/model"
)

func TestProjectEpisodeLifecycleHTTP(t *testing.T) {
	router, db, cookie := profileHandlerFixture(t)
	RegisterProjectRoutes(router.Group("/api"), runtimeService)
	const projectID = "episode-fixture"
	const projectURL = "/api/projects/" + projectID
	for _, row := range []any{
		&model.Project{ID: projectID, Name: "三集夹具", UserID: "profile-user", Status: model.ProjectStatusActive},
		&model.Project{ID: "foreign", Name: "他人项目", UserID: "other"},
		&model.ProjectUnit{ID: "foreign-unit", ProjectID: "foreign", Kind: model.ProjectUnitKindEpisode},
		&model.ProjectUnit{ID: "source", ProjectID: projectID, Kind: model.ProjectUnitKindChapter, Position: 4, SourceText: "原文章节"},
		&model.CanvasProject{ID: "canvas", ProjectID: projectID, UserID: "profile-user", PayloadJSON: `{"nodes":[]}`},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	call := func(method, path, body string, status int, target any) {
		t.Helper()
		rec := profileRequest(router, method, projectURL+path, body, cookie)
		var envelope struct {
			Code int
			Data json.RawMessage
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil || rec.Code != status || (envelope.Code == 0) != (status == http.StatusOK) {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		if target != nil {
			if err := json.Unmarshal(envelope.Data, target); err != nil {
				t.Fatal(err)
			}
		}
	}
	type unitResponse struct{ Unit model.ProjectUnit }
	create := func(title string, body string) model.ProjectUnit {
		t.Helper()
		data, err := json.Marshal(map[string]string{"kind": "episode", "title": title, "sourceText": body})
		if err != nil {
			t.Fatal(err)
		}
		var got unitResponse
		call("POST", "/units", string(data), 200, &got)
		if got.Unit.ID == "" || got.Unit.SourceText != body || got.Unit.Kind != model.ProjectUnitKindEpisode {
			t.Fatalf("created unit = %+v", got.Unit)
		}
		return got.Unit
	}
	checkOrder := func(want ...string) {
		t.Helper()
		var result struct{ Episodes []struct{ UnitID string } }
		call("GET", "/drama/episodes", "", 200, &result)
		ids := make([]string, 0, len(result.Episodes))
		for _, episode := range result.Episodes {
			ids = append(ids, episode.UnitID)
		}
		if !slices.Equal(ids, want) {
			t.Fatalf("episode IDs %v, want %v", ids, want)
		}
	}
	first := create("开局", "<p>首集</p>")
	middle := create("中篇", "<p>待删</p>")
	last := create("合流", "<p>末集</p>")
	for _, unit := range []model.ProjectUnit{first, middle, last} {
		for _, row := range []any{
			&model.Shot{ID: "shot-" + unit.ID, ProjectID: projectID, UnitID: unit.ID, Title: unit.Title},
			&model.DramaShotlist{ID: "list-" + unit.ID, ProjectID: projectID, UnitID: unit.ID, ShotlistJSON: unit.Title},
			&model.CanvasUnitLink{ID: "link-" + unit.ID, ProjectID: projectID, CanvasID: "canvas", UnitID: unit.ID},
			&model.ProjectOntologyDocument{ID: "doc-" + unit.ID, ProjectID: projectID, UnitID: unit.ID, Kind: model.OntologyDocHandoff, Version: 1, Content: unit.Title},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	fourth := create("后记", "")
	if first.Position != 5 || fourth.Position != 8 {
		t.Fatalf("append positions %d, %d", first.Position, fourth.Position)
	}
	checkOrder(first.ID, middle.ID, last.ID, fourth.ID)
	var edited unitResponse
	call("PATCH", "/units/"+fourth.ID, `{"title":"后记","sourceText":"<p>补写</p>"}`, 200, &edited)
	if edited.Unit.ID != fourth.ID || edited.Unit.SourceText != "<p>补写</p>" {
		t.Fatalf("editing appended episode: %+v", edited.Unit)
	}
	call("POST", "/units", `{"kind":"episode","title":"  "}`, 400, nil)
	call("DELETE", "/units/foreign-unit", "", 404, nil)
	// 在级联删除执行到最后一步时故意让数据库拒绝，校验镜头表、交接与导航一同回滚。
	if err := db.Exec(`CREATE TRIGGER deny_delete BEFORE DELETE ON project_units BEGIN SELECT RAISE(ABORT,'fixture refusal'); END`).Error; err != nil {
		t.Fatal(err)
	}
	call("DELETE", "/units/"+middle.ID, "", 500, nil)
	checkOrder(first.ID, middle.ID, last.ID, fourth.ID)
	if err := db.Exec("DROP TRIGGER deny_delete").Error; err != nil {
		t.Fatal(err)
	}
	checkContent := func(unit model.ProjectUnit, want int64) {
		t.Helper()
		for _, row := range []any{&model.Shot{}, &model.DramaShotlist{}, &model.CanvasUnitLink{}, &model.ProjectOntologyDocument{}} {
			var count int64
			if err := db.Model(row).Where("project_id = ? AND unit_id = ?", projectID, unit.ID).Count(&count).Error; err != nil || count != want {
				t.Fatalf("%T for %s count=%d want=%d err=%v", row, unit.Title, count, want, err)
			}
		}
	}
	checkContent(middle, 1)
	call("DELETE", "/units/"+middle.ID, "", 200, nil)
	checkOrder(first.ID, last.ID, fourth.ID)
	call("GET", "/units/"+middle.ID, "", 404, nil)
	checkContent(middle, 0)
	for _, unit := range []model.ProjectUnit{first, last} {
		var got unitResponse
		call("GET", "/units/"+unit.ID, "", 200, &got)
		if got.Unit.ID != unit.ID || got.Unit.SourceText != unit.SourceText {
			t.Fatalf("surviving unit changed: %+v", got.Unit)
		}
		checkContent(unit, 1)
		var shot model.Shot
		if err := db.First(&shot, "project_id = ? AND unit_id = ?", projectID, unit.ID).Error; err != nil || shot.ID != "shot-"+unit.ID || shot.Title != unit.Title {
			t.Fatalf("surviving shot = %+v, err=%v", shot, err)
		}
	}
	for _, unit := range []model.ProjectUnit{fourth, last, first} {
		call("DELETE", "/units/"+unit.ID, "", 200, nil)
	}
	checkOrder()
	var summary struct{ Units []model.ProjectUnit }
	call("GET", "/units", "", 200, &summary)
	if len(summary.Units) != 1 || summary.Units[0].ID != "source" || summary.Units[0].Position != 0 {
		t.Fatalf("source chapter after deleting episodes: %+v", summary.Units)
	}
	var canvas model.CanvasProject
	if err := db.First(&canvas, "id = ?", "canvas").Error; err != nil || canvas.ProjectID != projectID || canvas.PayloadJSON != `{"nodes":[]}` {
		t.Fatalf("canvas = %+v, err=%v", canvas, err)
	}
	var tasks int64
	if err := db.Model(&model.Task{}).Count(&tasks).Error; err != nil || tasks != 0 {
		t.Fatalf("episode mutations created tasks: %d, err=%v", tasks, err)
	}
}
