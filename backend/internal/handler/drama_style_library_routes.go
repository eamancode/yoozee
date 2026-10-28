package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"yingce/backend/internal/service"
)

// registerDramaStyleLibraryRoutes 是 M8 卡 A 的接口：「锁定此风格」（风格分析的候选或平台风格库的条目写进项目），
// 和平台风格库（团队共享：登录用户能看、能存；改、删限管理员或创建者，权限在 service 里判）。风格分析本身走项目级步骤
// POST /projects/:id/drama/steps/style.lock/run。
func registerDramaStyleLibraryRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/projects/:id/drama/style-lock", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<16)
		var req service.DramaStyleLockRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("锁定风格的请求格式无效"))
			return
		}
		result, err := svc.LockDramaStyle(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.GET("/drama/style-library", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		list, err := svc.DramaStyleLibrary(user, c.Query("projectType"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, list)
	})
	r.POST("/drama/style-library", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<16)
		var req service.DramaStyleLibrarySaveRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("存进风格库的请求格式无效"))
			return
		}
		item, err := svc.SaveDramaStyleToLibrary(user, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, item)
	})
	r.PATCH("/drama/style-library/:libraryId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<18)
		var req service.DramaStyleLibraryUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("修改风格的请求格式无效"))
			return
		}
		item, err := svc.UpdateDramaStyleLibraryEntry(user, c.Param("libraryId"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, item)
	})
	r.DELETE("/drama/style-library/:libraryId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.DeleteDramaStyleLibraryEntry(user, c.Param("libraryId")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"deleted": true})
	})
	r.GET("/drama/style-library/:libraryId/cover", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		delivery, err := svc.PrepareDramaStyleLibraryCoverDelivery(user, c.Param("libraryId"), resourceAccessOptions(c), c.GetHeader("Range"))
		if err != nil {
			failService(c, err)
			return
		}
		serveResourceDelivery(c, delivery, "private, no-cache", "")
	})
}
