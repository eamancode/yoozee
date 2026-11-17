package app

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/outbound/outboundtest"
)

func TestVideoChainPrepareOnlyUsesReferenceUploadKinds(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	seedOnlySlotSystemVideo(t, f)
	if err := f.db.Model(&model.ModelChannel{}).Where("id = ?", "system-video").Update("base_url", "https://prepare-only.invalid").Error; err != nil {
		t.Fatal(err)
	}
	// Seedance uploads images and consumes audio references directly.
	wav := []byte("RIFF\x26\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x40\x1f\x00\x00\x40\x1f\x00\x00\x01\x00\x08\x00data\x01\x00\x00\x00\x80\x00")
	voice, err := f.s.UploadResourceFile("user-1", "voice.wav", int64(len(wav)), "audio", 0, 0, 1, bytes.NewReader(wav))
	if err != nil {
		t.Fatal(err)
	}
	outboundtest.PublicDNS(t)
	var lookups atomic.Int32
	dial := net.DefaultResolver.Dial
	net.DefaultResolver.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		lookups.Add(1)
		return dial(ctx, network, address)
	}
	state := videoChainState{Request: ProjectVideoChainRequest{PrepareOnly: true}}
	input := map[string]any{
		"mode":            "video",
		"config":          map[string]any{"channelId": "system-video", "model": "seedance-2.5-test", "interfaceType": "seedance-videos-compatible"},
		"referenceAudios": []any{map[string]any{"storageKey": "resource:" + voice.ID}},
	}
	prepare := func(user string) error {
		return f.s.uploadVideoChainReferences(context.Background(), &model.Task{UserID: user}, &state, &videoChainStepState{}, input)
	}
	if err := prepare("user-1"); err != nil {
		t.Fatalf("prepare owned audio without uploading: %v", err)
	}
	if err := prepare("other-user"); err == nil || !strings.Contains(err.Error(), "读取任务参考资源失败") {
		t.Fatalf("foreign audio passed local preparation: %v", err)
	}
	if lookups.Load() != 0 || f.uploads.Load() != 0 {
		t.Fatalf("audio preparation made %d DNS connections and %d uploads", lookups.Load(), f.uploads.Load())
	}
	input["referenceImages"] = []any{map[string]any{"storageKey": "resource:" + f.frameID}}
	if err := prepare("user-1"); err == nil || !strings.Contains(err.Error(), "DNS") || lookups.Load() == 0 || f.uploads.Load() != 0 {
		t.Fatalf("image upload must pass outbound validation: err=%v DNS=%d uploads=%d", err, lookups.Load(), f.uploads.Load())
	}
}
