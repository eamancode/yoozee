package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

// Unicode escapes and rich-text markup need more room than the visible text.
const projectSourceBodyBytes = 8 << 20

func bindProjectSourceJSON(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, projectSourceBodyBytes)
	if err := c.ShouldBindJSON(target); err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			writeFailure(c, http.StatusRequestEntityTooLarge, http.StatusRequestEntityTooLarge, service.ReasonProjectSourceInvalid, "正文请求过大：支持 15 万字，请减少额外格式或分章导入")
		} else {
			fail(c, http.StatusBadRequest, err)
		}
		return false
	}
	return true
}
