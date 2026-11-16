package app

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"yingce/backend/internal/model"
)

// 任务列表的失败原因（TaskSummary.error）：分镜条和运行记录读它，不再一律写「没写原因」。只给用户可读的分类原因，
// 上游地址、密钥、令牌、Cookie 一律不下发；可读的业务原因（第几轮、超过上限、计时不一致）原样保留；最多 300 字；不是失败不给。
func TestTaskListErrorIsSafeAndReadable(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{`接口请求失败: POST https://api.example.com/v1/chat?key=sk-live-abc123 401 Unauthorized`, []string{"鉴权失败"}},
		{`upstream returned 401: {"error":{"message":"Incorrect API key provided: sk-proj-ABCDEF1234567890"}}`, []string{"Incorrect API key provided"}},
		{`Authorization: Bearer sk-secret-token-xyz rejected`, nil},
		{`x-goog-api-key=AIzaSyD-secret-value-123 cookie=session=abc; token=eyJhbGciOi.eyJzdWIiOjEy.c2lnbmF0dXJl`, nil},
		{`dial tcp 10.0.0.5:443: i/o timeout`, []string{"网络异常"}},
		{`第 6 轮（单元卡 E1-U2）的输入有 310000 字，超过上限 300000 字：本集剧本或单元太多，请把本集拆短后重新生成`, []string{"第 6 轮", "超过上限 300000 字"}},
		{`单元 E1-U3：标题写 8 秒，镜头表合计 7 秒，对不上`, []string{"标题写 8 秒", "合计 7 秒"}},
	}
	for _, c := range cases {
		got := taskListError(model.Task{Status: model.TaskStatusFailed, Error: c.raw})
		if got == "" {
			t.Fatalf("%q: empty reason", c.raw)
		}
		for _, leak := range []string{"sk-live", "sk-proj", "sk-secret", "AIzaSy", "Bearer s", "session=abc", "eyJhbGciOi", "https://", "api.example.com", "10.0.0.5"} {
			if strings.Contains(got, leak) {
				t.Fatalf("%q leaked %q: %q", c.raw, leak, got)
			}
		}
		for _, keep := range c.want {
			if !strings.Contains(got, keep) {
				t.Fatalf("%q lost %q: %q", c.raw, keep, got)
			}
		}
	}
	long := taskListError(model.Task{Status: model.TaskStatusFailed, Error: strings.Repeat("校验没通过，", 200)})
	if utf8.RuneCountInString(long) > taskListErrorLimit {
		t.Fatalf("reason not truncated: %d runes", utf8.RuneCountInString(long))
	}
	for _, status := range []model.TaskStatus{model.TaskStatusSucceeded, model.TaskStatusRunning, model.TaskStatusCancelled} {
		if got := taskListError(model.Task{Status: status, Error: "x"}); got != "" {
			t.Fatalf("%s task got a reason %q", status, got)
		}
	}
	// 列表合同：TaskSummary 序列化后带 error，且是摘要不是原文。
	summary := taskSummaryForOutput(model.Task{ID: "t", Type: "canvas_text", Status: model.TaskStatusFailed, Error: `Authorization: Bearer sk-secret-token-xyz rejected`})
	encoded, _ := json.Marshal(summary)
	if !strings.Contains(string(encoded), `"error":"`) || strings.Contains(string(encoded), "sk-secret") {
		t.Fatalf("task list contract: %s", encoded)
	}
}
