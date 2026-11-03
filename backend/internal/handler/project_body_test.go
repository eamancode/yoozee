package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestProjectSourceBodyCapacity(t *testing.T) {
	for _, source := range []string{strings.Repeat("雨", 150000), strings.Repeat(`\u96e8`, 150000)} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/projects", strings.NewReader(`{"source":"`+source+`"}`))
		var target struct {
			Source string `json:"source"`
		}
		if !bindProjectSourceJSON(c, &target) || len([]rune(target.Source)) != 150000 {
			t.Fatalf("150000 characters must bind: status %d", recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/projects", strings.NewReader(`{"source":"`+strings.Repeat("x", projectSourceBodyBytes)+`"}`))
	var target any
	if bindProjectSourceJSON(c, &target) || recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), "project_source_invalid") {
		t.Fatalf("oversize: %d %s", recorder.Code, recorder.Body.String())
	}
}
