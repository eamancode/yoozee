package protocol

import (
	"context"
	"testing"
)

func TestSeedanceVideosCompatibleReadsMetadataVideoURL(t *testing.T) {
	adapter := officialPackageAdapter(t, "seedance-videos-compatible.yingce-plugin", "seedance-videos-compatible")
	for _, tc := range []struct {
		name, payload, url string
	}{
		{"root metadata", `{"status":"completed","metadata":{"url":"https://cdn.example/root.mp4"}}`, "https://cdn.example/root.mp4"},
		{"nested metadata", `{"status":"completed","data":{"metadata":{"url":"https://cdn.example/nested.mp4"}}}`, "https://cdn.example/nested.mp4"},
		{"existing field precedence", `{"status":"completed","video_url":"https://cdn.example/old.mp4","metadata":{"url":"https://cdn.example/metadata.mp4"}}`, "https://cdn.example/old.mp4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, err := adapter.ParsePoll(context.Background(), PollContext{TaskID: "existing-task"}, []byte(tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			if state.Status != StatusSucceeded || state.Result == nil || len(state.Result.Videos) != 1 || state.Result.Videos[0].URL != tc.url {
				t.Fatalf("poll state = %#v; want completed video %s", state, tc.url)
			}
		})
	}
}
