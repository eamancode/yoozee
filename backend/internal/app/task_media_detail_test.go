package app

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestTaskMediaMimeTypeSniffsGenericVideoContainers(t *testing.T) {
	mp4 := append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0, 'i', 's', 'o', 'm', 'i', 's', 'o', '2'}, bytes.Repeat([]byte{1}, 64)...)
	m4v := append([]byte{0, 0, 0, 0x14, 'f', 't', 'y', 'p', 'M', '4', 'V', ' ', 0, 0, 2, 0, 'M', '4', 'V', ' '}, bytes.Repeat([]byte{1}, 64)...)
	mov := append([]byte{0, 0, 0, 0x14, 'f', 't', 'y', 'p', 'q', 't', ' ', ' ', 0, 0, 2, 0, 'q', 't', ' ', ' '}, bytes.Repeat([]byte{1}, 64)...)
	html := []byte("<!doctype html><html><body>expired</body></html>")
	for _, tc := range []struct {
		declared, mode string
		head           []byte
		want           string
	}{
		{"binary/octet-stream", "video", mp4, "video/mp4"},
		{"application/mp4", "video", mp4, "video/mp4"},
		{"", "video", m4v, "video/mp4"},
		{"application/octet-stream", "video", mov, "video/quicktime"},
		{"video/webm", "video", mp4, "video/webm"},
		{"binary/octet-stream", "audio", m4v, "audio/mp4"},
		{"application/octet-stream", "video", html, "text/html"},
		{"text/html; charset=utf-8", "video", html, "text/html"},
	} {
		if got := taskMediaMimeType(tc.declared, tc.head, tc.mode); got != tc.want {
			t.Fatalf("%q/%s: got %s want %s", tc.declared, tc.mode, got, tc.want)
		}
	}
}

func TestMediaFailureDetailNeverEchoesUpstreamURL(t *testing.T) {
	secret := "https://cdn.example/result.mp4?X-Signature=private-test-token"
	for _, tc := range []struct {
		err  error
		want string
	}{
		{providerHTTPError{StatusCode: 403}, "HTTP 403"},
		{providerHTTPError{StatusCode: 502}, "HTTP 502"},
		{mediaDownloadContentError("上游返回的内容不是有效的video媒体（text/html）"), "text/html"},
		{&url.Error{Op: "Get", URL: secret, Err: context.DeadlineExceeded}, "下载超时"},
		{&url.Error{Op: "Get", URL: secret, Err: errors.New("connection refused")}, "无法连接结果地址"},
		{&url.Error{Op: "Get", URL: secret, Err: errors.New("read: connection reset by peer")}, "下载连接中断"},
	} {
		message := (&mediaRecoveryError{stage: "download", cause: tc.err}).Error()
		if !strings.HasPrefix(message, "作品已生成，但保存未完成：下载结果失败（") || !strings.Contains(message, tc.want) || strings.Contains(message, "private-test-token") {
			t.Fatalf("message=%s", message)
		}
	}
	if message := (&mediaRecoveryError{stage: "download", cause: errors.New(secret)}).Error(); message != "作品已生成，但保存未完成：下载结果失败" {
		t.Fatalf("unknown cause leaked: %s", message)
	}
}
