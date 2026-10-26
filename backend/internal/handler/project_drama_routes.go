// 短剧制作线（yoozee 1.0 skills）接口：生成模型目录、需求文档、连戏表、分段、方言编译、证据、选中、进度、门禁与交付包。

package handler

import (
	"encoding/json"
	"net/http"
	"net/url"

	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

func registerProjectDramaRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/drama/models", func(c *gin.Context) {
		if _, err := currentUser(c, svc); err != nil {
			failService(c, err)
			return
		}
		models, err := svc.DramaModels()
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"models": models})
	})
	r.GET("/projects/:id/drama/progress", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		progress, err := svc.DramaProgress(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"progress": progress})
	})
	document := func(read func(userID, projectID string) (map[string]any, error), write func(userID, projectID string, raw json.RawMessage) (map[string]any, error)) (gin.HandlerFunc, gin.HandlerFunc) {
		return func(c *gin.Context) {
				user, err := currentUser(c, svc)
				if err != nil {
					failService(c, err)
					return
				}
				doc, err := read(user.ID, c.Param("id"))
				if err != nil {
					failService(c, err)
					return
				}
				ok(c, gin.H{"document": doc})
			}, func(c *gin.Context) {
				user, err := currentUser(c, svc)
				if err != nil {
					failService(c, err)
					return
				}
				c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 600<<10)
				var req service.UpdateProjectDramaDocumentRequest
				if err := c.ShouldBindJSON(&req); err != nil {
					fail(c, http.StatusBadRequest, err)
					return
				}
				doc, err := write(user.ID, c.Param("id"), req.Document)
				if err != nil {
					failService(c, err)
					return
				}
				ok(c, gin.H{"document": doc})
			}
	}
	briefGet, briefPut := document(svc.ProjectBrief, svc.UpdateProjectBrief)
	r.GET("/projects/:id/drama/brief", briefGet)
	r.PUT("/projects/:id/drama/brief", briefPut)
	bibleGet, biblePut := document(svc.ProjectBible, svc.UpdateProjectBible)
	r.GET("/projects/:id/drama/bible", bibleGet)
	r.PUT("/projects/:id/drama/bible", biblePut)
	r.GET("/projects/:id/drama/segments", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		segments, err := svc.DramaSegments(user.ID, c.Param("id"), c.Query("unitId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"segments": segments})
	})
	r.PUT("/projects/:id/drama/segments", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		var req service.DramaSegmentsWriteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		segments, err := svc.WriteDramaSegments(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"segments": segments})
	})
	r.GET("/projects/:id/units/:unitId/drama/shotlist", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		shotlist, err := svc.DramaShotlist(user.ID, c.Param("id"), c.Param("unitId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"shotlist": shotlist})
	})
	r.POST("/projects/:id/units/:unitId/drama/shotlist/repack", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.DramaShotlistRepackRequest
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&req); err != nil {
				fail(c, http.StatusBadRequest, err)
				return
			}
		}
		result, err := svc.RepackDramaShotlist(user.ID, c.Param("id"), c.Param("unitId"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/projects/:id/shots/:shotId/compile", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req service.CompileProjectShotRequest
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&req); err != nil {
				fail(c, http.StatusBadRequest, err)
				return
			}
		}
		result, err := svc.CompileProjectShot(user.ID, c.Param("id"), c.Param("shotId"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"compiled": result})
	})
	r.GET("/projects/:id/drama/evidence", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		items, err := svc.ProjectEvidence(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"evidence": items})
	})
	r.POST("/projects/:id/drama/evidence", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
		var req service.CreateShotEvidenceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		evidence, err := svc.CreateShotEvidence(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"evidence": evidence})
	})
	r.POST("/projects/:id/artifacts/:artifactId/pick", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var req service.PickShotArtifactRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		artifact, err := svc.PickShotArtifact(user.ID, c.Param("id"), c.Param("artifactId"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"artifact": artifact})
	})
	r.POST("/projects/:id/artifacts/:artifactId/transcribe", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		task, err := svc.DramaTranscribe(user.ID, c.Param("id"), c.Param("artifactId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"task": task})
	})
	r.POST("/projects/:id/artifacts/:artifactId/frames", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		evidence, err := svc.DramaFrames(user.ID, c.Param("id"), c.Param("artifactId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"evidence": evidence})
	})
	r.POST("/projects/:id/drama/gate", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var req service.DramaGateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.DramaGate(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"gate": result})
	})
	r.GET("/projects/:id/drama/delivery", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		pkg, err := svc.DramaDelivery(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(pkg.FileName))
		c.Data(http.StatusOK, "application/zip", pkg.Data)
	})
}
