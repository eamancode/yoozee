package protocol

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSeedanceUsesHostUpload(t *testing.T) {
	raw, err := os.ReadFile("../../../plugin-packages/seedance-videos-compatible/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := LoadManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	upload, ok := adapter.(MediaUploadAdapter)
	if !ok || len(upload.UploadKinds()) != 1 || upload.UploadKinds()[0] != "image" {
		t.Fatal("seedance upload operation missing")
	}
	media := MediaReference{Kind: "image", DataURL: "data:image/png;base64,eA==", Name: "reference.png", MIMEType: "image/png"}
	request, err := upload.BuildUpload(context.Background(), RequestContext{BaseURL: "https://provider.example"}, media)
	if err != nil || request.Method != "POST" || request.Path != "/v1/media/upload" || len(request.Files) != 1 || request.Files[0].Reference.DataURL != media.DataURL {
		t.Fatalf("upload request: %+v %v", request, err)
	}
	id, err := upload.ParseUpload(context.Background(), []byte(`{"url":"https://media.example/reference.png"}`))
	if err != nil || id != "https://media.example/reference.png" {
		t.Fatalf("upload id %q: %v", id, err)
	}
	if _, err := upload.ParseUpload(context.Background(), []byte(`{"url":""}`)); err == nil {
		t.Fatal("accepted empty upload result")
	}
	media.Metadata = map[string]any{MediaUploadIDKey:id}
	created,err:=adapter.BuildCreate(context.Background(),RequestContext{BaseURL:"https://provider.example",Request:GenerationRequest{Capability:CapabilityVideo,Model:"seedance-2.0",Prompt:"test",Images:[]MediaReference{media}}})
	if err!=nil{t.Fatal(err)}
	body,err:=json.Marshal(created.Body)
	if err!=nil || !strings.Contains(string(body),id) || strings.Contains(string(body),media.DataURL){t.Fatalf("create did not use uploaded URL: %s %v",body,err)}
}
