package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"yingce/backend/internal/model"
	"yingce/backend/internal/service"
)

// registerDramaStepRoutes 是短剧流程步骤（M6）的接口：风格档案列表、一步用哪个技能、服务端组装提示词并建任务（按章节，
// 或项目级的剧本优化）、项目的剧集与来源、
// 管理员读写「风格档案 × 步骤 → 技能」绑定，以及流程技能包的上传（随即真跑评测）、版本列表、重跑评测和启用（回退就是启用历史版本；
// 评测没通过的要强制并写明原因）。
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
		rule, err := dramaRuleQuery(c)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		usage, err := svc.DramaStepUsage(user.ID, c.Param("id"), c.Param("step"), rule)
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
	// 项目级步骤（剧本优化）：来源单元、改编方式、集数由请求点名，一个运行一个任务。
	r.POST("/projects/:id/drama/steps/:step/run", func(c *gin.Context) {
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
		var req service.DramaProjectStepRunRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("运行步骤的请求格式无效"))
			return
		}
		req.TraceID = TraceID(c)
		req.RequestID = RequestID(c)
		result, err := svc.RunDramaProjectStep(user.ID, c.Param("id"), c.Param("step"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	// 资产页「重新合成」场次色卡：按色卡当前版本的色板由代码重画图、绑成新版本，不调模型。
	r.POST("/projects/:id/drama/palettes/resynthesize", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var req service.DramaPaletteResynthesizeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("重新合成色卡的请求格式无效"))
			return
		}
		result, err := svc.ResynthesizeDramaPalettes(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	// 资产页「继续出图」：资产设计建的角色出三视图、场景和道具出概念图（同时任务数满时剩下的排在后面）。
	r.POST("/projects/:id/drama/assets/images", func(c *gin.Context) {
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
		var req service.DramaAssetImagesRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("资产出图的请求格式无效"))
			return
		}
		result, err := svc.QueueDramaAssetImages(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	// 角色子卡（M8 卡 D）：资产页在主卡下新增子卡（按主卡继承生成设定，带了图片模板时接着出三视图）、改、删（被镜头引用时拒删）。
	variantRequest := func(c *gin.Context) (*model.User, service.DramaAssetVariantRequest, bool) {
		var req service.DramaAssetVariantRequest
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return nil, req, false
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("子卡的请求格式无效"))
			return nil, req, false
		}
		return user, req, true
	}
	r.POST("/projects/:id/drama/assets/:assetId/variants", func(c *gin.Context) {
		user, req, valid := variantRequest(c)
		if !valid {
			return
		}
		result, err := svc.CreateDramaAssetVariant(user.ID, c.Param("id"), c.Param("assetId"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.PATCH("/projects/:id/drama/variants/:variantId", func(c *gin.Context) {
		user, req, valid := variantRequest(c)
		if !valid {
			return
		}
		result, err := svc.UpdateDramaAssetVariant(user.ID, c.Param("id"), c.Param("variantId"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.DELETE("/projects/:id/drama/variants/:variantId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.DeleteDramaAssetVariant(user.ID, c.Param("id"), c.Param("variantId")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"deleted": true})
	})
	r.GET("/projects/:id/drama/episodes", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.DramaEpisodes(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
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
	r.GET("/admin/drama/packages", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		packages, err := svc.AdminFlowPackages(actor)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"packages": packages})
	})
	r.POST("/admin/drama/packages", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.RequireAdmin(actor); err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "admin-drama-package-upload:"+actor.ID, policy.Request.ResourceUploadPerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.SkillPackageUploadMaxBytes)
		file, err := c.FormFile("file")
		if c.Request.MultipartForm != nil {
			defer c.Request.MultipartForm.RemoveAll()
		}
		if err != nil {
			failService(c, service.BadAuthRequest("请选择 build.py --package 构建的技能包 ZIP"))
			return
		}
		// 评测用的文本模型：后台页面带管理员设置里的默认文本模型（model / logicalModelId / config 与「生成分段」同一套字段）；
		// 不带就只存版本、不评测。
		sel := service.DramaStepEvalModel{Model: c.PostForm("model"), LogicalModelID: c.PostForm("logicalModelId")}
		if raw := c.PostForm("config"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &sel.Config); err != nil {
				failService(c, service.BadAuthRequest("评测模型配置 config 不是合法 JSON 对象"))
				return
			}
		}
		result, err := svc.UploadFlowPackage(actor, file, c.PostForm("note"), sel)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.GET("/admin/drama/packages/:id/versions", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		pkg, versions, err := svc.AdminFlowPackageVersions(actor, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"package": pkg, "versions": versions})
	})
	r.POST("/admin/drama/packages/:id/versions/:vid/activate", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var req service.FlowPackageActivateRequest
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&req); err != nil {
				failService(c, service.BadAuthRequest("启用请求格式无效"))
				return
			}
		}
		pkg, err := svc.ActivateFlowPackageVersion(actor, c.Param("id"), c.Param("vid"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"package": pkg})
	})
	r.POST("/admin/drama/packages/:id/versions/:vid/eval", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var req service.DramaStepEvalModel
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("评测请求格式无效"))
			return
		}
		eval, err := svc.StartDramaStepEval(actor, c.Param("id"), c.Param("vid"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"eval": eval})
	})
}
