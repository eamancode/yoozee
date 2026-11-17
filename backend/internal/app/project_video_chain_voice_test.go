package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestVideoChainBoundVoiceQuoteAndProviderRequest(t *testing.T) {
	f := newVideoChainFixture(t, "A")
	seedOnlySlotSystemVideo(t, f)
	f.req.Scope = "single"
	f.req.Model.Input = globalSixSecondsRequest("false")
	f.req.Model.Input["referenceAudios"] = []any{map[string]any{"storageKey": "resource:client-stale"}}
	if err := f.db.Model(&model.Project{}).Where("id = ?", f.projectID).Update("workflow_id", drama.WorkflowSSSDrama).Error; err != nil {
		t.Fatal(err)
	}

	seg := drama.Segment{ID: "S01", Scene: "A", Dur: 10, Mode: drama.ModeLockIdentity, Verbatim: "@图1 开口，@图2 回答。"}
	sub := drama.SubShot{At: 0, Size: "中景", Text: "两人交谈"}
	var images, audios, audioURLs []string
	imageByBytes := map[string]string{}
	for i, name := range []string{"阿青", "阿白"} {
		id := fmt.Sprintf("character-%d", i)
		v := id + "-v1"
		pixels := color.Gray{Y: byte(80 + i)}
		imageID := uploadTestImage(t, f.s, "user-1", pixels)
		imageByBytes[string(testPNG(t, pixels))] = imageID
		// A 1-sample, 8-bit mono PCM WAV. Each sample has different bytes.
		wav := append([]byte("RIFF\x26\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x40\x1f\x00\x00\x40\x1f\x00\x00\x01\x00\x08\x00data\x01\x00\x00\x00"), byte(128+i), 0)
		voice, err := f.s.UploadResourceFile("user-1", "sample.wav", int64(len(wav)), "audio", 0, 0, 1, bytes.NewReader(wav))
		if err != nil {
			t.Fatal(err)
		}
		images = append(images, imageID)
		audios = append(audios, voice.ID)
		audioURLs = append(audioURLs, dataURL(voice.MimeType, wav))
		for _, row := range []any{
			&model.Asset{ID: id, UserID: "user-1", Title: name, Kind: "image", Category: model.AssetCategoryCharacter, PrimaryVersionID: id + "-v2", PayloadJSON: "{}"},
			&model.ProjectAssetLink{ID: newID(), ProjectID: f.projectID, AssetID: id},
			&model.AssetVersion{ID: v, AssetID: id, Version: 1, DefinitionJSON: "{}"},
			&model.AssetVersion{ID: id + "-v2", AssetID: id, Version: 2, DefinitionJSON: "{}"},
			&model.AssetRepresentation{ID: newID(), TaskID: id, AssetVersionID: v, ResourceID: f.frameID, Role: "primary", MediaType: "image"},
			&model.AssetRepresentation{ID: newID(), TaskID: id, AssetVersionID: v, ResourceID: imageID, Role: "turnaround_sheet", MediaType: "image"},
			&model.VoiceProfile{ID: "vp-" + id, UserID: "user-1", VoiceKey: id, Status: "active", SampleResourceID: voice.ID},
			&model.CharacterVoiceBinding{ID: newID(), AssetVersionID: v, VoiceProfileID: "vp-" + id},
			&model.ShotAssetReference{ID: newID(), ShotID: "s0", AssetVersionID: v, Role: "reference", Status: "linked", Source: "segment", Position: i},
		} {
			if err := f.db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		seg.Refs = append(seg.Refs, drama.Ref{File: v, Role: "identity", Note: name})
		sub.Refs = append(sub.Refs, v)
		sub.Lines = append(sub.Lines, drama.Line{Speaker: name, Text: "好。"})
	}
	seg.Shots = []drama.SubShot{sub}
	_, _, legacyVoices, err := f.s.videoChainCompiledReferences("user-1", f.projectID, []drama.CompiledFile{{File: seg.Refs[0].File, Tag: "@音频1", Role: "voice"}})
	if err != nil || len(legacyVoices) != 1 || legacyVoices[0].(map[string]any)["id"] != audios[0] {
		t.Fatalf("version-based voice manifest: %v %v", legacyVoices, err)
	}
	for _, wrongFile := range []string{images[0], "character-0-v2"} {
		if _, _, _, err := f.s.videoChainCompiledReferences("user-1", f.projectID, []drama.CompiledFile{{File: wrongFile, Tag: "@音频1", Role: "voice"}}); err == nil {
			t.Fatalf("invalid voice accepted: %s", wrongFile)
		}
	}
	spec, err := json.Marshal(seg)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.ShotRevision{}).Where("id = ?", "rs0").Update("spec_json", string(spec)).Error; err != nil {
		t.Fatal(err)
	}

	var providerBody struct {
		Prompt string   `json:"prompt"`
		Images []string `json:"images"`
		Audios []string `json:"audios"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/media/upload":
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Errorf("upload: %v", err)
				w.WriteHeader(400)
				return
			}
			defer file.Close()
			data, err := io.ReadAll(file)
			id := imageByBytes[string(data)]
			if err != nil || id == "" {
				t.Errorf("unexpected uploaded image: %v", err)
				w.WriteHeader(400)
				return
			}
			fmt.Fprintf(w, `{"url":"http://%s/image/%s"}`, r.Host, id)
		case "/v1/videos":
			if err := json.NewDecoder(r.Body).Decode(&providerBody); err != nil {
				t.Errorf("decode create: %v", err)
			}
			fmt.Fprintf(w, `{"id":"fixture-video","status":"succeeded","video_url":"http://%s/result.mp4"}`, r.Host)
		case "/result.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(agentAvatarTestMP4())
		default:
			t.Errorf("unexpected mock request: %s", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(server.Close)
	if err := f.db.Model(&model.ModelChannel{}).Where("id = ?", "system-video").Update("base_url", server.URL).Error; err != nil {
		t.Fatal(err)
	}
	var item model.ChannelModel
	if err := f.db.First(&item, "id = ?", "system-model").Error; err != nil {
		t.Fatal(err)
	}
	var capability ModelCapabilityConfig
	if err := json.Unmarshal([]byte(item.CapabilityConfigJSON), &capability); err != nil {
		t.Fatal(err)
	}
	setLimit := func(n int) {
		capability.Video.References.MaxAudios = n
		raw, err := json.Marshal(capability)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.db.Model(&item).Update("capability_config_json", string(raw)).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []int{0, 1} {
		setLimit(n)
		_, quoteErr := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
		_, submitErr := f.s.CreateProjectVideoChain("user-1", f.projectID, f.req)
		for _, err := range []error{quoteErr, submitErr} {
			var mismatch *ModelError
			if !errors.As(err, &mismatch) || mismatch.Details["field"] != "referenceAudios" || !strings.Contains(err.Error(), "2 条声音参考") {
				t.Fatalf("audio limit %d: %v", n, err)
			}
		}
	}
	setLimit(2)
	preview := f.start(t)
	step := preview.Steps[0]
	if step.ReferenceCount != 2 || step.ReferenceAudioCount != 2 || step.Quote == nil || step.Quote.AmountMicrocredits != 1_500_000 {
		t.Fatalf("preview: %+v", step)
	}
	created := f.untilTask(t, preview.ID, 0)
	child, err := f.s.repo.TaskForUser("user-1", created.Steps[0].TaskID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := f.s.decryptTaskInputJSON(child.InputJSON)
	if err != nil {
		t.Fatal(err)
	}
	var payload canvasGenerationInput
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	for _, group := range []struct {
		items []providerMedia
		ids   []string
	}{{payload.ReferenceImages, images}, {payload.ReferenceAudios, audios}} {
		if len(group.items) != len(group.ids) {
			t.Fatalf("task inputs: %+v", group.items)
		}
		for i, id := range group.ids {
			if group.items[i].ID != id || group.items[i].StorageKey != "resource:"+id {
				t.Fatalf("media %d: %+v", i+1, group.items[i])
			}
		}
	}
	if _, err := f.s.processCanvasGenerationTask(context.Background(), "user-1", f.projectID, child.Type, child.Prompt, raw); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(providerBody.Images, []string{server.URL + "/image/" + images[0], server.URL + "/image/" + images[1]}) || !slices.Equal(providerBody.Audios, audioURLs) {
		t.Fatalf("provider images=%v audios=%v", providerBody.Images, providerBody.Audios)
	}
	for _, tag := range []string{"@图片1", "@图片2", "@音频1", "@音频2"} {
		if !strings.Contains(providerBody.Prompt, tag) {
			t.Fatalf("missing %s in %s", tag, providerBody.Prompt)
		}
	}
}

func TestVideoChainFrameUsesConfirmedPromptDialect(t *testing.T) {
	f := newVideoChainFixture(t, "A", "A")
	chain := f.start(t)
	parent, err := f.s.repo.TaskForUser("user-1", chain.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.s.videoChainState(parent)
	if err != nil {
		t.Fatal(err)
	}
	step := &state.Steps[1]
	if step.CompiledModel != "seedance-2.5" || step.ReferenceTag != "@图片1" {
		t.Fatalf("confirmed dialect %s, tag %s", step.CompiledModel, step.ReferenceTag)
	}
	if err := f.db.Model(&model.Project{}).Where("id = ?", f.projectID).Update("generation_model", "minimax-h3").Error; err != nil {
		t.Fatal(err)
	}
	step.SourceVideoResourceID, step.FrameResourceID = "fixture-predecessor", f.frameID
	input, err := f.s.videoChainInput(parent, step)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := f.s.prepareDramaVideoSubmission("user-1", input, stringValue(input["prompt"]), false)
	if err != nil || !strings.Contains(prompt, "@图片1") {
		t.Fatalf("confirmed frame reference after project default changed: %q, %v", prompt, err)
	}
}
