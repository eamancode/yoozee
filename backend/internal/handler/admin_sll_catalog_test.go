package handler

import (
	"net/http"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestSLLCatalogAdminRoutes(t *testing.T) {
	router, db, cookie := profileHandlerFixture(t)
	RegisterAdminRoutes(router.Group("/api"), runtimeService)
	channel := model.ModelChannel{ID: "sll", Scope: model.ChannelScopeSystem, Name: "SLL", BaseURL: "https://api.lecapi.net/v1", ModelsJSON: "[]"}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	const path = "/api/admin/channels/sll/sll-catalog"
	actions := []struct{ method, path string }{
		{http.MethodGet, path}, {http.MethodPost, path + "/sync"}, {http.MethodPost, path + "/metrics"},
	}
	for _, action := range actions {
		for _, tc := range []struct {
			cookie string
			want   int
		}{{"", http.StatusUnauthorized}, {cookie, http.StatusForbidden}} {
			w := profileRequest(router, action.method, action.path, `{"group":"default"}`, tc.cookie)
			if w.Code != tc.want {
				t.Fatalf("%s %s: got %d, want %d", action.method, action.path, w.Code, tc.want)
			}
		}
	}
	if err := db.Model(&model.User{}).Where("id = ?", "profile-user").Update("role", model.UserRoleAdmin).Error; err != nil {
		t.Fatal(err)
	}
	w := profileRequest(router, http.MethodGet, path, "", cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"rows"`) {
		t.Fatalf("empty catalog: %d %s", w.Code, w.Body)
	}
	w = profileRequest(router, http.MethodPost, path+"/sync", `{"group":""}`, cookie)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing group: %d %s", w.Code, w.Body)
	}
	if err := db.Model(&channel).Update("base_url", "https://example.invalid/v1").Error; err != nil {
		t.Fatal(err)
	}
	for _, action := range actions {
		w = profileRequest(router, action.method, action.path, `{"group":"default"}`, cookie)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("different vendor: %s %s returned %d, %s", action.method, action.path, w.Code, w.Body)
		}
	}
}
