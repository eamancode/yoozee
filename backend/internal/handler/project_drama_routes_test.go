package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// 短剧制作线的全部路由：少一条前端就会 404，多一条没人用就是死接口。
var dramaRoutes = []string{
	"GET /api/drama/models",
	"GET /api/projects/:id/drama/brief",
	"PUT /api/projects/:id/drama/brief",
	"GET /api/projects/:id/drama/bible",
	"PUT /api/projects/:id/drama/bible",
	"GET /api/projects/:id/drama/segments",
	"PUT /api/projects/:id/drama/segments",
	"GET /api/projects/:id/drama/scenes",
	"GET /api/projects/:id/units/:unitId/drama/scenes",
	"POST /api/projects/:id/drama/scenes/renumber",
	"GET /api/projects/:id/units/:unitId/drama/shotlist",
	"POST /api/projects/:id/units/:unitId/drama/shotlist/repack",
	"GET /api/projects/:id/drama/progress",
	"POST /api/projects/:id/drama/pipeline/next",
	"GET /api/projects/:id/drama/evidence",
	"POST /api/projects/:id/drama/evidence",
	"GET /api/projects/:id/drama/delivery",
	"POST /api/projects/:id/drama/gate",
	"POST /api/projects/:id/shots/:shotId/compile",
	"POST /api/projects/:id/artifacts/:artifactId/pick",
	"POST /api/projects/:id/artifacts/:artifactId/transcribe",
	"POST /api/projects/:id/artifacts/:artifactId/frames",
}

func TestDramaRoutesAreRegisteredUnderAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCanvasAPI(router.Group("/api"), &service.Service{})

	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, want := range dramaRoutes {
		if !registered[want] {
			t.Errorf("短剧路由没注册：%s", want)
		}
	}
	// 镜头表由生成分段任务收尾时服务端写，前端没有写镜头表的接口。
	if registered["PUT /api/projects/:id/units/:unitId/drama/shotlist"] {
		t.Error("镜头表写接口应已删除")
	}
	// 生成模型目录是登录后就能读的，不允许出现匿名可用的短剧写接口。
	if registered["GET /api/drama/models"] && registered["POST /api/drama/models"] {
		t.Error("模型目录不该有写接口")
	}
}

func TestDramaRoutesRejectAnonymousRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCanvasAPI(router.Group("/api"), &service.Service{})

	// 不带任何会话信息时，短剧接口必须拒绝，而不是回落到默认用户或 500。
	anonymous := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/drama/models"},
		{http.MethodGet, "/api/projects/project-1/drama/progress"},
		{http.MethodPost, "/api/projects/project-1/drama/pipeline/next"},
		{http.MethodGet, "/api/projects/project-1/drama/brief"},
		{http.MethodPut, "/api/projects/project-1/drama/brief"},
		{http.MethodGet, "/api/projects/project-1/drama/bible"},
		{http.MethodPut, "/api/projects/project-1/drama/bible"},
		{http.MethodGet, "/api/projects/project-1/drama/segments"},
		{http.MethodPut, "/api/projects/project-1/drama/segments"},
		{http.MethodGet, "/api/projects/project-1/units/unit-1/drama/shotlist"},
		{http.MethodPost, "/api/projects/project-1/units/unit-1/drama/shotlist/repack"},
		{http.MethodGet, "/api/projects/project-1/drama/evidence"},
		{http.MethodPost, "/api/projects/project-1/drama/evidence"},
		{http.MethodGet, "/api/projects/project-1/drama/delivery"},
		{http.MethodPost, "/api/projects/project-1/drama/gate"},
		{http.MethodPost, "/api/projects/project-1/shots/shot-1/compile"},
		{http.MethodPost, "/api/projects/project-1/artifacts/artifact-1/pick"},
		{http.MethodPost, "/api/projects/project-1/artifacts/artifact-1/transcribe"},
		{http.MethodPost, "/api/projects/project-1/artifacts/artifact-1/frames"},
	}
	for _, probe := range anonymous {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(probe.method, probe.path, nil)
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 匿名请求应返回 401，得到 %d：%s", probe.method, probe.path, recorder.Code, recorder.Body.String())
		}
	}
}

// 步骤用法和场次表的渠道段长查询参数：与生成分段 run 请求的 rule 同口径，不带时为 nil，非正整数拒绝。
func TestDramaRuleQuery(t *testing.T) {
	read := func(query string) (*service.DramaShotlistRule, error) {
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = httptest.NewRequest("GET", "/api/projects/p/drama/steps/chapter.shots"+query, nil)
		return dramaRuleQuery(context)
	}
	if rule, err := read(""); err != nil || rule != nil {
		t.Fatalf("不带参数：%+v %v", rule, err)
	}
	if rule, err := read("?maxSeconds=15&minSeconds=4"); err != nil || rule == nil || rule.MaxSeconds != 15 || rule.MinSeconds != 4 {
		t.Fatalf("15 秒渠道：%+v %v", rule, err)
	}
	if rule, err := read("?maxSeconds=30"); err != nil || rule == nil || rule.MaxSeconds != 30 || rule.MinSeconds != 0 {
		t.Fatalf("只带上限：%+v %v", rule, err)
	}
	for _, query := range []string{"?maxSeconds=abc", "?maxSeconds=0", "?minSeconds=-1", "?maxSeconds=1.5"} {
		if rule, err := read(query); err == nil {
			t.Fatalf("%s 未被拒绝：%+v", query, rule)
		}
	}
}
