package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerSceneReferenceRoutes(r *gin.RouterGroup, svc *service.Service) {
	const path = "/projects/:id/drama/shots/:shotId/scene-reference"
	r.GET(path, func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if versionID := c.Query("versionId"); versionID != "" {
			candidate, err := svc.ProjectSceneReferenceCandidate(user.ID, c.Param("id"), c.Param("shotId"), versionID)
			if err != nil {
				failService(c, err)
				return
			}
			ok(c, gin.H{"candidate": candidate})
			return
		}
		review, err := svc.ProjectSceneReferenceReview(user.ID, c.Param("id"), c.Param("shotId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"review": review})
	})
	r.PUT(path, func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var request service.SceneReferenceUpdate
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "场景核对内容格式无效", "reason": "bad_request"})
			return
		}
		review, err := svc.UpdateProjectSceneReference(user.ID, c.Param("id"), c.Param("shotId"), request)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"review": review})
	})
}
