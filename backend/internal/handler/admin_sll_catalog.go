package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerAdminSLLCatalogRoutes(r *gin.RouterGroup, svc *service.Service) {
	const path = "/admin/channels/:id/sll-catalog"
	r.GET(path, func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		result, err := svc.AdminSLLCatalog(c.Request.Context(), user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST(path+"/sync", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			Group string `json:"group" binding:"required,max=128"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.SyncAdminSLLCatalog(c.Request.Context(), user, c.Param("id"), req.Group)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST(path+"/metrics", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.RefreshAdminSLLMetrics(c.Request.Context(), user, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{})
	})
}
