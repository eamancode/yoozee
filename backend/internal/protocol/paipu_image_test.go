package protocol

import (
	"context"
	"testing"
)

func paipuAdapter(t *testing.T) Adapter {
	t.Helper()
	return officialPackageAdapter(t, "paipu-image.yingce-plugin", "paipu-image")
}

// 参考图节点必须仍然请求 /v1/images/generations：派普只在该端点提供 AC 系列模型，
// 切到 /v1/images/edits 会被上游以 invalid_request 拒绝。
func TestPaipuImageKeepsGenerationRouteWithReferences(t *testing.T) {
	adapter := paipuAdapter(t)
	request := GenerationRequest{Model: "lec-ac-image-2-5-flare", Prompt: "生成一张简洁的产品海报", AspectRatio: "16:9"}
	spec, err := adapter.BuildCreate(context.Background(), RequestContext{BaseURL: "https://api.paipu.net", Request: request})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Path != "/v1/images/generations" {
		t.Fatalf("path = %q", spec.Path)
	}
	body := manifestTestBody(t, spec)
	if body["aspect_ratio"] != "16:9" || body["model"] != request.Model || body["n"] != float64(1) || body["output_format"] != "png" {
		t.Fatalf("body = %#v", body)
	}
	for _, key := range []string{"size", "quality", "response_format"} {
		if _, exists := body[key]; exists {
			t.Fatalf("unsupported %s emitted: %#v", key, body)
		}
	}
	if _, exists := body["images"]; exists {
		t.Fatalf("no-reference request must omit images: %#v", body)
	}

	request.Images = []MediaReference{
		{URL: "https://example.com/b.png", Order: 2},
		{URL: "https://example.com/a.png", Order: 1},
		{URL: "https://example.com/mask.png", Role: "mask"},
	}
	spec, err = adapter.BuildCreate(context.Background(), RequestContext{BaseURL: "https://api.paipu.net", Request: request})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Path != "/v1/images/generations" {
		t.Fatalf("reference request path = %q", spec.Path)
	}
	urls, ok := manifestTestBody(t, spec)["images"].([]any)
	if !ok || len(urls) != 2 || urls[0] != "https://example.com/a.png" || urls[1] != "https://example.com/b.png" {
		t.Fatalf("ordered references = %#v", manifestTestBody(t, spec)["images"])
	}
}

func TestPaipuImageOmitsAutoRatioAndMapsSyncResponse(t *testing.T) {
	adapter := paipuAdapter(t)
	spec, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model: "lec-ac-image-2-5-sunburst", Prompt: "产品海报", AspectRatio: "auto", Resolution: "4k",
	}})
	if err != nil {
		t.Fatal(err)
	}
	body := manifestTestBody(t, spec)
	if _, exists := body["aspect_ratio"]; exists {
		t.Fatalf("auto ratio must be omitted: %#v", body)
	}
	if body["resolution"] != "4K" {
		t.Fatalf("resolution = %#v", body["resolution"])
	}

	result, err := adapter.ParseCreate(context.Background(), []byte(`{"data":[{"url":"https://example.com/out.png"}]}`))
	if err != nil || result.Status != StatusSucceeded || result.Result == nil || len(result.Result.Images) != 1 {
		t.Fatalf("sync result = %#v, %v", result, err)
	}
}
