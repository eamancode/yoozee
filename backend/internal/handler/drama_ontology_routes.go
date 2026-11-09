package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"yingce/backend/internal/service"
)

// registerDramaOntologyRoutes 是本体论导演分镜（流水线一）的接口：读世界圣经卡与各集七部分、手改世界圣经卡、对一个段按单元卡返修。
// 生成本身仍走 POST /projects/:id/units/:unitId/drama/steps/chapter.shots/run（流水线一的项目自动用本体论导演）。
func registerDramaOntologyRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/projects/:id/drama/ontology", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		docs, err := svc.ProjectOntologyDocuments(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, docs)
	})
	r.PUT("/projects/:id/drama/ontology/bible", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var req service.UpdateOntologyBibleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("世界圣经卡的请求格式无效"))
			return
		}
		doc, err := svc.UpdateOntologyBible(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, doc)
	})
	r.POST("/projects/:id/units/:unitId/drama/ontology/repair", func(c *gin.Context) {
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
		var req service.OntologyRepairRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("返修请求格式无效"))
			return
		}
		req.TraceID = TraceID(c)
		req.RequestID = RequestID(c)
		result, err := svc.RepairOntologySegment(user.ID, c.Param("id"), c.Param("unitId"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
}
