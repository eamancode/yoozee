package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// 第九轮 R8-01：费用预览时，客户端带来的全局时长不能在选档前把合法镜头拒掉；
// 同步音频等参数仍按所选模型能力完整校验，不放宽。

// seedOnlySlotSystemVideo 建一个只有 15/30 秒两档、不支持同步音频的系统视频渠道（不调上游，只做预览）。
func seedOnlySlotSystemVideo(t *testing.T, f *videoChainFixture) {
	t.Helper()
	profile := DefaultModelCapabilityConfigForModel("seedance-videos-compatible", "seedance-2.5-test")
	profile.Video.Duration = VideoDurationConfig{Selection: "enum", Values: []int{15, 30}, Default: 15}
	profile.Video.Operations = []string{"text_to_video", "reference_to_video"}
	profile.Video.GenerateAudio = VideoBooleanConfig{Supported: false, Default: false}
	capability, _ := json.Marshal(profile)
	rows := []any{
		&model.ModelChannel{ID: "system-video", Scope: model.ChannelScopeSystem, Enabled: true, Name: "Mock system video", BaseURL: "https://upstream.invalid", APIKey: "fixture-only"},
		&model.ChannelModel{ID: "system-model", ChannelID: "system-video", ModelKey: "seedance-2.5-test", ProviderModelKey: "seedance-2.5-mock", Capability: "video", Protocol: model.ChannelInterfaceType("seedance-videos-compatible"), CapabilityConfigJSON: string(capability), Enabled: true, PriceConfigured: true, BillingMode: "fixed_request"},
		&model.CreditAccount{UserID: "user-1", AvailableMicrocredits: 50_000_000},
	}
	for _, seconds := range []int{15, 30} {
		selector := fmt.Sprintf(`{"videoSeconds":"%d"}`, seconds)
		rows = append(rows, &model.ChannelModelPriceTier{ID: fmt.Sprintf("tier-%d", seconds), ChannelModelID: "system-model", SelectorKey: selector, SelectorJSON: selector, ProviderModelKey: "seedance-2.5-mock", BillingMode: "fixed_request", UnitPriceMicrocredits: int64(seconds) * 100_000, Enabled: true, PriceConfigured: true})
	}
	for _, row := range rows {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func setShotSeconds(t *testing.T, f *videoChainFixture, seconds float64) {
	t.Helper()
	segment := drama.Segment{ID: "S01", Scene: "A", Dur: seconds, Mode: drama.ModeTextOnly, Shots: []drama.SubShot{{At: 0, Size: "中景", Text: "阿婆整理推车，林深站在左边"}}}
	raw, _ := json.Marshal(segment)
	if err := f.db.Model(&model.ShotRevision{}).Where("id = ?", "rs0").Update("spec_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Shot{}).Where("id = ?", "s0").Update("duration_ms", int(seconds*1000)).Error; err != nil {
		t.Fatal(err)
	}
}

// 前端按所选模型能力对齐后的请求：同步音频按模型能力置 false；时长仍是全局偏好里的 6 秒（不是任何一档）。
func globalSixSecondsRequest(audio string) map[string]any {
	return map[string]any{"mode": "video", "config": map[string]any{"channelId": "system-video", "model": "seedance-2.5-test", "size": "9:16", "vquality": "720", "videoSeconds": "6", "videoGenerateAudio": audio, "videoWatermark": "false"}}
}

func TestVideoChainPreviewUsesFinalSlotNotGlobalSeconds(t *testing.T) {
	for _, tc := range []struct {
		name       string
		content    float64
		generation float64
	}{
		{"20 秒镜头按 30 秒档报价，正文保留 20 秒", 20, 30},
		{"恰好 30 秒可用", 30, 30},
		// 既有 15/30 秒适配规则：两档都有时，正文满 15 秒就用 30 秒档，短于 15 秒用 15 秒档（本轮不改）。
		{"15 秒镜头按既有规则用 30 秒档", 15, 30},
		{"10 秒镜头按 15 秒档", 10, 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVideoChainFixture(t, "A")
			seedOnlySlotSystemVideo(t, f)
			setShotSeconds(t, f, tc.content)
			f.req.Scope = "single"
			f.req.Model.Input = globalSixSecondsRequest("false")
			preview, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
			if err != nil {
				t.Fatalf("preview rejected a valid shot because of the global 6 seconds: %v", err)
			}
			step := preview.Steps[0]
			if step.GenerationSeconds != tc.generation || step.ContentSeconds != tc.content {
				t.Fatalf("generation/content = %v/%v, want %v/%v", step.GenerationSeconds, step.ContentSeconds, tc.generation, tc.content)
			}
			if want := int64(tc.generation) * 100_000; preview.TotalMicrocredits != want {
				t.Fatalf("quote = %d, want the %v-second tier price %d", preview.TotalMicrocredits, tc.generation, want)
			}
		})
	}
}

func TestVideoChainPreviewRejectsShotLongerThanLargestSlot(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	seedOnlySlotSystemVideo(t, f)
	setShotSeconds(t, f, 31)
	f.req.Scope = "single"
	f.req.Model.Input = globalSixSecondsRequest("false")
	if _, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req); err == nil {
		t.Fatal("a 31-second shot must be rejected when the largest slot is 30 seconds")
	}
}

func TestVideoChainPreviewStillRejectsUnsupportedAudioWithDetails(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	seedOnlySlotSystemVideo(t, f)
	setShotSeconds(t, f, 15)
	f.req.Scope = "single"
	f.req.Model.Input = globalSixSecondsRequest("true")
	_, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
	// handler 也是先按 ModelError 取，再投影它嵌入的 AppError。
	var modelErr *ModelError
	if !errors.As(err, &modelErr) || modelErr.AppError == nil || modelErr.Reason != ErrorReason(ErrCodeModelCapabilityNotSupported) {
		t.Fatalf("unsupported audio must still be rejected as model_capability_not_supported, got %v", err)
	}
	appErr := modelErr.AppError
	// R8-02：详情要在 HTTP 投影读的那一份（AppError.Details）里，指出是哪个参数。
	issues, _ := json.Marshal(appErr.Details["capabilityIssues"])
	if appErr.Details["field"] != "videoGenerateAudio" || !strings.Contains(string(issues), "videoGenerateAudio") {
		t.Fatalf("capability details missing from AppError: %#v", appErr.Details)
	}
	for _, secret := range []string{"fixture-only", "upstream.invalid"} {
		if raw, _ := json.Marshal(appErr.Details); strings.Contains(string(raw), secret) {
			t.Fatalf("details leaked channel config %q: %s", secret, raw)
		}
	}
}

func TestModelErrorDetailsLiveOnAppError(t *testing.T) {
	err := modelCapabilityMismatchError(CapabilityMatch{Issues: []CapabilityIssue{{Kind: "option", Name: "videoGenerateAudio", Reason: "参数 同步音频超出支持范围"}}})
	var modelErr *ModelError
	if !errors.As(err, &modelErr) || modelErr.AppError == nil {
		t.Fatalf("expected ModelError, got %T", err)
	}
	if modelErr.AppError.Details["field"] != "videoGenerateAudio" || modelErr.Details["message"] != "参数 同步音频超出支持范围" {
		t.Fatalf("details must be stored on the projected AppError: %#v", modelErr.AppError.Details)
	}
	if plain := NewModelError(ErrCodeModelRouteUnavailable, "没有可用供应线路"); len(plain.AppError.Details) != 0 {
		t.Fatalf("a model error without details must project none: %#v", plain.AppError.Details)
	}
}

// 无参考图的镜头：最少参考图为 0 的模型可以预览；最少 1 张时明确说缺参考图，不伪造参考图。
func TestVideoChainPreviewReferenceMinimum(t *testing.T) {
	for _, tc := range []struct {
		name      string
		minImages int
	}{{"最少 0 张可以预览", 0}, {"最少 1 张时明确提示缺图", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVideoChainFixture(t, "A")
			seedOnlySlotSystemVideo(t, f)
			var row model.ChannelModel
			if err := f.db.First(&row, "id = ?", "system-model").Error; err != nil {
				t.Fatal(err)
			}
			var profile ModelCapabilityConfig
			if err := json.Unmarshal([]byte(row.CapabilityConfigJSON), &profile); err != nil {
				t.Fatal(err)
			}
			profile.Video.References.MinImages = tc.minImages
			raw, _ := json.Marshal(profile)
			if err := f.db.Model(&model.ChannelModel{}).Where("id = ?", "system-model").Update("capability_config_json", string(raw)).Error; err != nil {
				t.Fatal(err)
			}
			setShotSeconds(t, f, 15)
			f.req.Scope = "single"
			f.req.Model.Input = globalSixSecondsRequest("false")
			preview, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
			if tc.minImages == 0 {
				if err != nil || preview.Steps[0].ReferenceCount != 0 {
					t.Fatalf("text-only shot should preview without references: %v %+v", err, preview)
				}
				return
			}
			if err == nil {
				t.Fatal("a model that needs a reference image must not preview a text-only shot")
			}
			var modelErr *ModelError
			detail := err.Error()
			if errors.As(err, &modelErr) && modelErr.AppError != nil {
				raw, _ := json.Marshal(modelErr.AppError.Details)
				detail += string(raw)
			}
			if !strings.Contains(detail, "参考图") && !strings.Contains(detail, "image") {
				t.Fatalf("missing-reference rejection should say a reference image is needed: %s", detail)
			}
		})
	}
}
