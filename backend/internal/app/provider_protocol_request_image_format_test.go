package app

import (
	"testing"
)

// 图片任务：模型能力显式声明支持 output_format 时，宿主补默认 PNG，让画布/Agent 这类
// 不传 providerOptions 的入口也拿到 PNG（与创作页直连路径一致）。
func TestProtocolRequestInjectsPNGWhenImageCapabilitySupportsIt(t *testing.T) {
	input := canvasGenerationInput{
		Mode:   "image",
		Prompt: "海报",
		Config: providerConfig{InterfaceType: "paipu-image", Model: "lec-ac-image-2-5-flare", Count: "1"},
		ImageCapability: &ImageCapabilityConfig{
			OutputFormat: ParameterSupport{Supported: true},
		},
	}
	request := protocolRequestFromInput(input)
	if got := request.ProviderOptions["paipu-image"]["output_format"]; got != "png" {
		t.Fatalf("output_format = %#v, want png", got)
	}
}

// 派普各模型支持面不同：声明不支持时绝不能补，否则 lec-ty-seedream-5-pro 会被上游 400 拒绝。
func TestProtocolRequestKeepsOutputFormatEmptyWhenUnsupported(t *testing.T) {
	input := canvasGenerationInput{
		Mode:   "image",
		Prompt: "电影感人物肖像",
		Config: providerConfig{InterfaceType: "paipu-image", Model: "lec-ty-seedream-5-pro", Count: "1"},
		ImageCapability: &ImageCapabilityConfig{
			OutputFormat: ParameterSupport{Supported: false},
		},
	}
	request := protocolRequestFromInput(input)
	if options, exists := request.ProviderOptions["paipu-image"]; exists {
		if _, has := options["output_format"]; has {
			t.Fatalf("unsupported output_format must not be injected: %#v", options)
		}
	}
}

// 调用方显式给的值优先，宿主不覆盖。
func TestProtocolRequestKeepsCallerOutputFormat(t *testing.T) {
	input := canvasGenerationInput{
		Mode:   "image",
		Prompt: "海报",
		Config: providerConfig{InterfaceType: "paipu-image", Model: "lec-ac-image-2-5-flare", Count: "1"},
		Metadata: map[string]interface{}{
			"providerOptions": map[string]any{"paipu-image": map[string]any{"output_format": "jpeg"}},
		},
		ImageCapability: &ImageCapabilityConfig{
			OutputFormat: ParameterSupport{Supported: true},
		},
	}
	request := protocolRequestFromInput(input)
	if got := request.ProviderOptions["paipu-image"]["output_format"]; got != "jpeg" {
		t.Fatalf("caller value must win: %#v", got)
	}
}
