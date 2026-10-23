package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"yingce/backend/internal/service"
)

// registerBuiltinSkillVersionRoutes 是内置技能在线覆盖的管理员接口：版本历史、读文件、新建在线版本、启用、回退。
func registerBuiltinSkillVersionRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/builtin-skills", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		items, err := svc.AdminBuiltinSkills(actor)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"skills": items})
	})
	r.GET("/admin/builtin-skills/:id/versions", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		skill, versions, err := svc.AdminBuiltinSkillVersions(actor, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"skill": skill, "versions": versions})
	})
	r.GET("/admin/builtin-skills/:id/versions/:versionId/files", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		files, err := svc.AdminBuiltinSkillVersionFiles(actor, c.Param("id"), c.Param("versionId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"files": files})
	})
	r.GET("/admin/builtin-skills/:id/versions/:versionId/file", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		file, err := svc.AdminBuiltinSkillVersionFile(actor, c.Param("id"), c.Param("versionId"), c.Query("path"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"file": file})
	})
	r.POST("/admin/builtin-skills/:id/versions", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
		var req service.BuiltinSkillVersionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("技能版本请求格式无效"))
			return
		}
		skill, version, err := svc.CreateBuiltinSkillVersion(actor, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"skill": skill, "version": version})
	})
	r.POST("/admin/builtin-skills/:id/activate", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			VersionID string `json:"versionId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("请求格式无效"))
			return
		}
		skill, err := svc.ActivateBuiltinSkillVersion(actor, c.Param("id"), req.VersionID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"skill": skill})
	})
	r.POST("/admin/builtin-skills/:id/revert", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		skill, err := svc.RevertBuiltinSkill(actor, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"skill": skill})
	})
}
