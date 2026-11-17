package handler

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestStoryboardDeleteHTTPConcurrentConfirmations(t *testing.T) {
	router, db, cookie := profileHandlerFixture(t)
	registerProjectWorkflowRoutes(router.Group("/api"), runtimeService)
	for _, fixture := range []any{
		&model.Project{ID: "project", UserID: "profile-user", Name: "测试项目", Status: model.ProjectStatusActive},
		&model.Project{ID: "foreign", UserID: "other-user", Name: "其他项目"},
		&model.Shot{ID: "target", ProjectID: "project", UnitID: "episode", CurrentRevisionID: "rev"},
		&model.ShotRevision{ID: "rev", ShotID: "target", Version: 1},
		&model.Shot{ID: "neighbor", ProjectID: "project", UnitID: "episode", Position: 1},
		&model.Shot{ID: "foreign-target", ProjectID: "foreign", UnitID: "episode"},
		&model.ShotArtifact{ID: "take", ProjectID: "project", UnitID: "episode", ShotID: "target", Type: "video", Version: 1, ResourceID: "file"},
		&model.Resource{ID: "file", UserID: "profile-user"},
		&model.Task{ID: "job", ProjectID: "project", UserID: "profile-user", Type: "canvas_video", Status: model.TaskStatusRunning, InputJSON: `{"domainProjectId":"project","unitId":"episode","shotId":"target"}`},
	} {
		if err := db.Create(fixture).Error; err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/projects/project/shots/target"
	body := `{"expectedRevisionId":"rev","expectedArtifactIds":["take"]}`
	check := func(url, jsonBody, token string, want int, fragment string) {
		t.Helper()
		rec := profileRequest(router, http.MethodDelete, url, jsonBody, token)
		if rec.Code != want || !strings.Contains(rec.Body.String(), fragment) {
			t.Fatalf("DELETE %s = %d %s", url, rec.Code, rec.Body.String())
		}
	}
	check(path, body, "", 401, "")
	check("/api/projects/foreign/shots/foreign-target", body, cookie, 404, "")
	check("/api/projects/project/shots/foreign-target", body, cookie, 404, "")
	check(path, "{", cookie, 400, "")
	check(path, body, cookie, 409, "job")
	if err := db.Model(&model.Task{}).Where("id = ?", "job").Update("status", model.TaskStatusSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	check(path, `{"expectedArtifactIds":[]}`, cookie, 409, "产物")
	start := make(chan struct{})
	results := make(chan int, 2)
	for range 2 {
		go func() {
			<-start
			results <- profileRequest(router, http.MethodDelete, path, body, cookie).Code
		}()
	}
	close(start)
	statuses := []int{<-results, <-results}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{200, 404}) {
		t.Fatalf("two concurrent confirmations: %v", statuses)
	}
	check(path, body, cookie, 404, "已删除")
	for _, row := range []struct {
		model any
		id    string
		want  int64
	}{
		{&model.Shot{}, "target", 0}, {&model.ShotArtifact{}, "take", 0}, {&model.ShotRevision{}, "rev", 0},
		{&model.Shot{}, "neighbor", 1}, {&model.Shot{}, "foreign-target", 1}, {&model.Resource{}, "file", 1}, {&model.Task{}, "job", 1},
	} {
		var count int64
		if err := db.Model(row.model).Where("id = ?", row.id).Count(&count).Error; err != nil || count != row.want {
			t.Fatalf("%T id=%s got %d want %d: %v", row.model, row.id, count, row.want, err)
		}
	}
}
