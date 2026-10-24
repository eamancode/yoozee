package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 后台「技能展示」关闭后，普通用户不能再通过接口读到技能详情与内容；
// 技能列表、安装、收藏这些管理动作不受影响，管理员也不受限。
func TestSkillContentVisibilityGuardsDetailRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err := db.AutoMigrate(&model.User{}, &model.AuthSession{}, &model.Skill{}, &model.SystemSetting{}, &model.AdminAuditEvent{}); err != nil {
		t.Fatal(err)
	}
	for _, role := range []model.UserRole{model.UserRoleAdmin, model.UserRoleUser} {
		db.Create(&model.User{ID: string(role), Username: string(role), Role: role, Status: model.UserStatusActive})
		db.Create(&model.AuthSession{ID: string(role), UserID: string(role), TokenHash: auth.HashToken("test-token"), ExpiresAt: time.Now().Add(time.Hour)})
	}

	svc := service.New(repository.New(db), t.TempDir())
	router := gin.New()
	RegisterSkillRoutes(router.Group("/api"), svc)

	call := func(role, method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if role != "" {
			req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: role + ".test-token"})
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}

	// 默认开启：普通用户不会被守卫拦下（技能不存在时是 404，不是 403）。
	if got := call("user", "GET", "/api/skills/missing"); got.Code == http.StatusForbidden {
		t.Fatalf("默认应允许查看技能详情，got 403 %s", got.Body.String())
	}

	admin := &model.User{ID: string(model.UserRoleAdmin), Role: model.UserRoleAdmin}
	current, err := svc.AdminFeatureAvailability(admin)
	if err != nil {
		t.Fatal(err)
	}
	value := current.FeatureAvailability
	value.SkillContentVisible = false
	if _, err := svc.UpdateFeatureAvailability(admin, value); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/api/skills/missing",
		"/api/skills/missing/files",
		"/api/skills/missing/file",
		"/api/skills/missing/file/raw",
		"/api/skills/missing/bundle",
		"/api/skills/missing/search",
	} {
		got := call("user", "GET", path)
		if got.Code != http.StatusForbidden {
			t.Fatalf("%s: want 403, got %d %s", path, got.Code, got.Body.String())
		}
		if !strings.Contains(got.Body.String(), "技能详情暂未开放") {
			t.Fatalf("%s: 缺少关闭提示: %s", path, got.Body.String())
		}
	}

	// 列表照常可见，管理动作不受影响。
	if got := call("user", "GET", "/api/skills"); got.Code != http.StatusOK {
		t.Fatalf("技能列表应仍可访问: %d %s", got.Code, got.Body.String())
	}
	// 管理员不受限，否则关掉展示后无法维护技能。
	if got := call("admin", "GET", "/api/skills/missing"); got.Code == http.StatusForbidden {
		t.Fatalf("管理员不应被技能展示开关挡住: %s", got.Body.String())
	}
}
