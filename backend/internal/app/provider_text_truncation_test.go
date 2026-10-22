package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func assertTextOutputTruncated(t *testing.T, err error) {
	t.Helper()
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Reason != ReasonTextOutputTruncated {
		t.Fatalf("err = %v, want reason %s", err, ReasonTextOutputTruncated)
	}
}

func TestTextPayloadFinishReasonCoversEachProtocol(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		payload  map[string]interface{}
		want     string
	}{
		{"chat length", "chat-completion", map[string]interface{}{"choices": []interface{}{map[string]interface{}{"finish_reason": "stop"}, map[string]interface{}{"finish_reason": "length"}}}, "length"},
		{"chat wrapped in data", "chat-completion", map[string]interface{}{"data": map[string]interface{}{"choices": []interface{}{map[string]interface{}{"finish_reason": "length"}}}}, "length"},
		{"gemini candidates", "chat-completion", map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"finishReason": "MAX_TOKENS"}}}, "MAX_TOKENS"},
		{"claude", "claude-api", map[string]interface{}{"stop_reason": "max_tokens"}, "max_tokens"},
		{"responses incomplete", "responses", map[string]interface{}{"status": "incomplete", "incomplete_details": map[string]interface{}{"reason": "max_output_tokens"}}, "max_output_tokens"},
		{"responses completed", "responses", map[string]interface{}{"status": "completed"}, ""},
	}
	for _, tc := range cases {
		if got := textPayloadFinishReason(tc.payload, tc.protocol); got != tc.want {
			t.Errorf("%s: finish reason = %q, want %q", tc.name, got, tc.want)
		}
	}
	for _, reason := range []string{"length", "max_tokens", "MAX_TOKENS", "max_output_tokens"} {
		if !textFinishTruncated(reason) {
			t.Errorf("%q 应判为截断", reason)
		}
	}
	for _, reason := range []string{"", "stop", "end_turn", "tool_calls", "content_filter"} {
		if textFinishTruncated(reason) {
			t.Errorf("%q 不应判为截断", reason)
		}
	}
}

func TestStreamingAgentParserRecordsTruncation(t *testing.T) {
	streams := map[string]string{
		"chat-completion": "data: {\"choices\":[{\"delta\":{\"content\":\"[{\\\"镜头\\\"\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n",
		"claude-api":      "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"[{\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n",
		"responses":       "event: response.output_text.delta\ndata: {\"delta\":\"[{\"}\n\nevent: response.incomplete\ndata: {\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"output\":[]}}\n\n",
	}
	for protocol, stream := range streams {
		parser := newStreamingAgentParser(protocol, nil)
		parser.consume("text/event-stream", []byte(stream))
		parser.flush()
		result, err := parser.result()
		if err != nil {
			t.Fatalf("%s: result() error = %v", protocol, err)
		}
		if !textFinishTruncated(stringField(result, "finishReason")) {
			t.Fatalf("%s: finishReason = %q", protocol, stringField(result, "finishReason"))
		}
	}
	// 推理把额度用完、一个字正文都没有时，报截断而不是「没有返回内容」。
	parser := newStreamingAgentParser("chat-completion", nil)
	parser.consume("text/event-stream", []byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"想\"},\"finish_reason\":\"length\"}]}\n\n"))
	parser.flush()
	_, err := parser.result()
	assertTextOutputTruncated(t, err)
}

func TestTextTaskFailsWhenOutputIsTruncated(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	stream := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"[{\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[{\"镜头\""},"finish_reason":"length"}]}`))
	}))
	defer server.Close()
	config := providerConfig{BaseURL: server.URL, APIKey: "test-key"}

	_, err := requestTextProvider(context.Background(), config, "/chat/completions", map[string]interface{}{"model": "m"}, "chat-completion", false, nil)
	assertTextOutputTruncated(t, err)

	stream = true
	_, err = requestTextProvider(context.Background(), config, "/chat/completions", map[string]interface{}{"model": "m"}, "chat-completion", true, nil)
	assertTextOutputTruncated(t, err)
}

func TestProtocolTextFinishReasonReadsPluginShapes(t *testing.T) {
	if got := protocolTextFinishReason("claude-api", []byte(`{"stop_reason":"max_tokens"}`)); got != "max_tokens" {
		t.Fatalf("claude = %q", got)
	}
	if got := protocolTextFinishReason("aws-bedrock-converse", []byte(`{"stopReason":"max_tokens"}`)); got != "max_tokens" {
		t.Fatalf("bedrock = %q", got)
	}
	if got := protocolTextFinishReason("cohere-chat-v2", []byte(`{"choices":[{"finish_reason":"stop"}]}`)); got != "stop" {
		t.Fatalf("openai compatible = %q", got)
	}
	if got := protocolTextFinishReason("chat-completion", []byte(`not json`)); got != "" {
		t.Fatalf("non json = %q", got)
	}
}
