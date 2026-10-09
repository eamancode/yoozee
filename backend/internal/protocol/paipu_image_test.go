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

// 派普只接受自己图床上的参考素材地址：宿主必须先上传内联参考图，再用返回的 URL 发创建请求。
func TestPaipuImageUploadsInlineReferenceToProviderHost(t *testing.T) {
	adapter := paipuAdapter(t)
	if adapter.Metadata().RequiresPublicMediaURLs {
		t.Fatal("paipu-image must keep references inline so the host can upload them")
	}
	uploader, ok := adapter.(MediaUploader)
	if !ok {
		t.Fatal("paipu-image must declare a media upload operation")
	}
	plan, ok := uploader.MediaUploadPlan()
	if !ok || plan.URLPath != "url" || len(plan.Kinds) != 1 || plan.Kinds[0] != "image" {
		t.Fatalf("plan = %#v", plan)
	}

	media := MediaReference{Name: "reference.png", MIMEType: "image/png", DataURL: "data:image/png;base64,AAAA"}
	spec, err := uploader.BuildMediaUpload(
		RequestContext{BaseURL: "https://api.paipu.net", Request: GenerationRequest{Model: "lec-ac-image-2-5-flare", Prompt: "海报"}},
		media,
	)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Method != "POST" || spec.Path != "/v1/media/upload" || spec.ContentType != "multipart/form-data" {
		t.Fatalf("upload spec = %#v", spec)
	}
	if spec.Auth.Type != "bearer" || spec.Auth.Field != "apiKey" {
		t.Fatalf("upload auth = %#v", spec.Auth)
	}
	if len(spec.Files) != 1 {
		t.Fatalf("upload files = %#v", spec.Files)
	}
	file := spec.Files[0]
	if file.Name != "file" || file.Filename != "reference.png" || file.MIMEType != "image/png" || file.Reference.DataURL != media.DataURL {
		t.Fatalf("upload file part = %#v", file)
	}

	url, err := MediaUploadURL([]byte(`{"url":"https://example.com/uploads/reference.png"}`), plan.URLPath)
	if err != nil || url != "https://example.com/uploads/reference.png" {
		t.Fatalf("uploaded url = %q, %v", url, err)
	}
	for _, body := range []string{`{}`, `{"url":""}`, `{"url":"/relative.png"}`, `{"url":123}`, `not-json`} {
		if _, err := MediaUploadURL([]byte(body), plan.URLPath); err == nil {
			t.Fatalf("invalid upload response accepted: %s", body)
		}
	}
}

// 画布给每个节点（含图片节点）都带 vquality（视频遗留字段，默认 720），它会被宿主放进
// request.resolution。分辨率档位必须以图片的 quality 为准，否则 2K/4K 会被 720 静默吞掉，
// 用户选 2K 实际拿到 1K 的图。
func TestPaipuImagePrefersQualityOverStaleVideoResolution(t *testing.T) {
	adapter := paipuAdapter(t)
	build := func(quality string, resolution string) map[string]any {
		t.Helper()
		spec, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
			Model: "lec-ac-image-2-5-flare", Prompt: "海报", AspectRatio: "1:1", Quality: quality, Resolution: resolution,
		}})
		if err != nil {
			t.Fatal(err)
		}
		return manifestTestBody(t, spec)
	}

	if body := build("2K", "720"); body["resolution"] != "2K" {
		t.Fatalf("quality tier must win over stale video resolution: %#v", body["resolution"])
	}
	if body := build("", "4k"); body["resolution"] != "4K" {
		t.Fatalf("resolution fallback = %#v, want 4K", body["resolution"])
	}
	if body := build("auto", "720"); body["resolution"] != nil {
		t.Fatalf("unknown tiers must be omitted: %#v", body)
	}
}
