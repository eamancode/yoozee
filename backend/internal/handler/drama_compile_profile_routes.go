package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"yingce/backend/internal/service"
)

// registerDramaCompileProfileRoutes 是编译档案的管理员接口：版本列表（含内置默认全文）、新建、启用、回退到内置默认、删除历史版本。
func registerDramaCompileProfileRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/drama/compile-profiles", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		list, err := svc.AdminDramaCompileProfiles(actor)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, list)
	})
	r.POST("/admin/drama/compile-profiles", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10)
		var req service.DramaCompileProfileRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("编译档案请求格式无效"))
			return
		}
		profile, err := svc.CreateDramaCompileProfile(actor, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"profile": profile})
	})
	r.POST("/admin/drama/compile-profiles/revert", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.RevertDramaCompileProfile(actor); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})
	r.POST("/admin/drama/compile-profiles/:id/activate", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.ActivateDramaCompileProfile(actor, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})
	r.DELETE("/admin/drama/compile-profiles/:id", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.DeleteDramaCompileProfile(actor, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})
}
