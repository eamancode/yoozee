package app

import (
	"net/http"
	"strings"
	"testing"
)

func TestProviderConcurrencyFailureIsActionableAndRedacted(t *testing.T) {
	for _, raw := range []string{
		`{"error":{"message":"too many concurrent requests: invalid_request_error","api_key":"sk-private"}}`,
		`{"error":{"code":"concurrency_limit","message":"request rejected"}}`,
		`{"message":"当前账号并发已满，请稍后再试"}`,
	} {
		got := (providerHTTPError{StatusCode: http.StatusBadRequest, Body: raw}).Error()
		if !strings.Contains(got, "并发已满") || strings.Contains(got, "sk-private") || strings.Contains(got, "请检查模型和参数") {
			t.Fatalf("incorrect classification: %s", got)
		}
	}
	_, classified := providerPayloadErrorCategory(`{"error":{"message":"unknown error"},"prompt":"too many concurrent requests"}`)
	if classified {
		t.Fatal("echoed prompt must not classify a concurrency failure")
	}
}
