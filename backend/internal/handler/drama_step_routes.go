package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"yingce/backend/internal/service"
)

// registerDramaStepRoutes 是短剧流程步骤（M6）的接口：风格档案列表、一步用哪个技能、服务端组装提示词并建任务、
// 管理员读写「风格档案 × 步骤 → 技能」绑定。
func registerDramaStepRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/drama/style-profiles", func(c *gin.Context) {
		if _, err := currentUser(c, svc); err != nil {
			failService(c, err)
			return
		}
		profiles, err := svc.DramaProfiles()
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, profiles)
	})
	r.GET("/projects/:id/drama/steps/:step", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		usage, err := svc.DramaStepUsage(user.ID, c.Param("id"), c.Param("step"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"usage": usage})
	})
	r.GET("/projects/:id/units/:unitId/drama/steps/:step/prompt", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		prompt, err := svc.DramaStepPrompt(user.ID, c.Param("id"), c.Param("unitId"), c.Param("step"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, prompt)
	})
	r.POST("/projects/:id/units/:unitId/drama/steps/:step/run", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "tasks:"+user.ID, policy.Request.TaskCreatePerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var req service.DramaStepRunRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("运行步骤的请求格式无效"))
			return
		}
		req.TraceID = TraceID(c)
		req.RequestID = RequestID(c)
		result, err := svc.RunDramaStep(user.ID, c.Param("id"), c.Param("unitId"), c.Param("step"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.GET("/admin/drama/step-bindings", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		matrix, err := svc.AdminDramaStepBindings(actor)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, matrix)
	})
	r.PUT("/admin/drama/step-bindings", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req service.DramaStepBindingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("绑定请求格式无效"))
			return
		}
		matrix, err := svc.UpdateDramaStepBindings(actor, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, matrix)
	})
}
