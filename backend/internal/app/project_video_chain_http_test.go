package app_test

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/app"
	"yingce/backend/internal/handler"
)

func TestUnknownVideoChainHTTPPreviewAndRetry(t *testing.T) {
	app.CheckUnknownVideoChainHTTP(t, func(s *app.Service) http.Handler {
		router := gin.New()
		handler.RegisterCanvasAPI(router.Group("/api"), s)
		return router
	})
}
