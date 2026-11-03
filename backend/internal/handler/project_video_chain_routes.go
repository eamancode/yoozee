package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerProjectVideoChainRoutes(r *gin.RouterGroup, svc *service.Service) {
	base := "/projects/:id/video-chains"
	r.GET(base, func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		chains, err := svc.ProjectVideoChains(user.ID, c.Param("id"), c.Query("unitId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"chains": chains})
	})
	for _, preview := range []bool{true, false} {
		path := base
		if preview {
			path += "/preview"
		}
		r.POST(path, func(c *gin.Context) {
			user, err := currentUser(c, svc)
			if err != nil {
				failService(c, err)
				return
			}
			var req service.ProjectVideoChainRequest
			if err := c.ShouldBindJSON(&req); err != nil {
				failService(c, service.BadAuthRequest("连续生成参数无效"))
				return
			}
			if preview {
				chain, err := svc.PreviewProjectVideoChain(user.ID, c.Param("id"), req)
				if err != nil {
					failService(c, err)
					return
				}
				ok(c, gin.H{"chain": chain})
			} else {
				chain, err := svc.CreateProjectVideoChain(user.ID, c.Param("id"), req)
				if err != nil {
					failService(c, err)
					return
				}
				ok(c, gin.H{"chain": chain})
			}
		})
	}
	for _, action := range []string{"retry", "cancel"} {
		r.POST(base+"/:chainId/"+action, func(c *gin.Context) {
			user, err := currentUser(c, svc)
			if err != nil {
				failService(c, err)
				return
			}
			if action == "retry" {
				chain, err := svc.RetryProjectVideoChain(user.ID, c.Param("id"), c.Param("chainId"))
				if err != nil {
					failService(c, err)
					return
				}
				ok(c, gin.H{"chain": chain})
			} else {
				chain, err := svc.CancelProjectVideoChain(user.ID, c.Param("id"), c.Param("chainId"))
				if err != nil {
					failService(c, err)
					return
				}
				ok(c, gin.H{"chain": chain})
			}
		})
	}
}
