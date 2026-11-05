package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestSystemChannelVideoChainQuoteUploadSubmit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration VideoDurationConfig
		seconds  int
	}{
		{"range preserves 25 seconds", VideoDurationConfig{Selection: "range", Min: 1, Max: 30, Step: 1, Default: 15}, 25},
		{"dual slot selects 30 seconds", VideoDurationConfig{Selection: "enum", Values: []int{15, 30}, Default: 15}, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVideoChainFixture(t, "A", "A")
			var paths []string
			var submitted map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-only" {
					t.Error("system channel mock credential not resolved")
				}
				paths = append(paths, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "POST /v1/media/upload":
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
					}
					file, _, err := r.FormFile("file")
					if err != nil {
						t.Error(err)
					} else {
						_ = file.Close()
					}
					_, _ = fmt.Fprintf(w, `{"url":"https://uploaded.example/system-%d.png"}`, len(paths))
				case "POST /v1/videos":
					if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
						t.Error(err)
					}
					_, _ = w.Write([]byte(`{"id":"system-video-mock","status":"queued"}`))
				case "GET /v1/videos/system-video-mock":
					_, _ = fmt.Fprintf(w, `{"id":"system-video-mock","status":"completed","video_url":%q}`, dataURL("video/mp4", agentAvatarTestMP4()))
				default:
					t.Errorf("unexpected mock request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			profile := DefaultModelCapabilityConfigForModel("seedance-videos-compatible", "seedance-2.5-test")
			profile.Video.Duration = tc.duration
			profile.Video.Operations = []string{"text_to_video", "reference_to_video"}
			profile.Video.References.MaxImages = 9
			capability, _ := json.Marshal(profile)
			selector := fmt.Sprintf(`{"videoSeconds":"%d"}`, tc.seconds)
			for _, row := range []any{
				&model.ModelChannel{ID: "system-video", Scope: model.ChannelScopeSystem, Enabled: true, Name: "Mock system video", BaseURL: server.URL, APIKey: "fixture-only"},
				&model.ChannelModel{ID: "system-model", ChannelID: "system-video", ModelKey: "seedance-2.5-test", ProviderModelKey: "seedance-2.5-mock", Capability: "video", Protocol: model.ChannelInterfaceType("seedance-videos-compatible"), CapabilityConfigJSON: string(capability), Enabled: true, PriceConfigured: true, BillingMode: "fixed_request"},
				&model.ChannelModelPriceTier{ID: "system-tier", ChannelModelID: "system-model", SelectorKey: selector, SelectorJSON: selector, ProviderModelKey: "seedance-2.5-mock", BillingMode: "fixed_request", UnitPriceMicrocredits: 5_000_000, Enabled: true, PriceConfigured: true},
				&model.ChannelModelPriceTier{ID: "initial-tier", ChannelModelID: "system-model", SelectorKey: `{"videoSeconds":"15"}`, SelectorJSON: `{"videoSeconds":"15"}`, ProviderModelKey: "seedance-2.5-mock", BillingMode: "fixed_request", UnitPriceMicrocredits: 1_000_000, Enabled: true, PriceConfigured: true},
				&model.CreditAccount{UserID: "user-1", AvailableMicrocredits: 50_000_000},
			} {
				if err := f.db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			segment := drama.Segment{ID: "S01", Scene: "A", Dur: 25, Mode: drama.ModeLockIdentity, Shots: []drama.SubShot{{At: 0, Size: "中景", Text: "阿婆整理推车，林深站在左边"}}}
			for i := range 8 {
				id := fmt.Sprintf("original-%d", i)
				for _, row := range []any{
					&model.Asset{ID: id, UserID: "user-1", Kind: "image", Category: model.AssetCategoryProp, Title: id, PrimaryVersionID: id + "-v1", Status: model.AssetVersionStatusConfirmed, PayloadJSON: "{}"},
					&model.AssetVersion{ID: id + "-v1", AssetID: id, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: "{}"},
					&model.ProjectAssetLink{ID: newID(), ProjectID: f.projectID, AssetID: id},
					&model.AssetRepresentation{ID: newID(), TaskID: id + "-image-task", AssetVersionID: id + "-v1", ResourceID: f.frameID, MediaType: "image", Role: "primary"},
				} {
					if err := f.db.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
				segment.Refs = append(segment.Refs, drama.Ref{File: id + "-v1", Role: "prop"})
			}
			raw, _ := json.Marshal(segment)
			if err := f.db.Model(&model.ShotRevision{}).Where("id = ?", "rs0").Update("spec_json", string(raw)).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&model.Shot{}).Where("id = ?", "s0").Update("duration_ms", 25000).Error; err != nil {
				t.Fatal(err)
			}
			f.req.Scope = "single"
			f.req.Model.Input = map[string]any{"mode": "video", "config": map[string]any{"channelId": "system-video", "model": "seedance-2.5-test", "size": "9:16", "vquality": "720p", "videoSeconds": "15"}, "capabilityOptions": map[string]any{"videoSeconds": float64(15)}}
			chain := f.start(t)
			if len(paths) != 0 || chain.TotalMicrocredits != 5_000_000 || chain.Steps[0].GenerationSeconds != float64(tc.seconds) || chain.Steps[0].ContentSeconds != 25 || chain.Steps[0].ReferenceCount != 8 {
				t.Fatalf("quote changed content/references or called provider: %+v paths=%v", chain, paths)
			}
			state := f.untilTask(t, chain.ID, 0)
			if len(paths) == 0 {
				t.Fatal("child admitted before reference upload")
			}
			for _, path := range paths {
				if path != "POST /v1/media/upload" {
					t.Fatalf("video submitted during reference preparation: %s", path)
				}
			}
			child, err := f.s.repo.TaskForUser("user-1", state.Steps[0].TaskID)
			if err != nil {
				t.Fatal(err)
			}
			inputJSON, err := f.s.decryptTaskInputJSON(child.InputJSON)
			if err != nil {
				t.Fatal(err)
			}
			var input canvasGenerationInput
			if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
				t.Fatalf("persisted provider config not decodable: %v", err)
			}
			if input.Config.VideoSeconds != fmt.Sprint(tc.seconds) || input.Config.PriceTierID != "system-tier" || len(input.ReferenceImages) != 8 || strings.Count(child.Prompt, dramaSceneEnding) != 1 {
				t.Fatalf("child config/prompt drift: %+v", input.Config)
			}
			// The normal worker submits and polls only the localhost mock; its result
			// is a fixed fixture, without invoking a real video model.
			stored, err := runQueuedTask(t, f.s)
			if err != nil || stored.ID != child.ID || stored.Status != model.TaskStatusSucceeded {
				t.Fatalf("child worker: %v %+v", err, stored)
			}
			if submitted["duration"] != float64(tc.seconds) || submitted["model"] != "seedance-2.5-mock" {
				t.Fatalf("provider payload: %#v", submitted)
			}
			images, _ := submitted["images"].([]any)
			if len(images) != 8 || len(paths) < 3 || paths[len(paths)-2] != "POST /v1/videos" || paths[len(paths)-1] != "GET /v1/videos/system-video-mock" {
				t.Fatalf("missing references/order: images=%v paths=%v", images, paths)
			}
			for _, ref := range images {
				if !strings.HasPrefix(fmt.Sprint(ref), "https://uploaded.example/system-") {
					t.Fatalf("unuploaded reference: %v", ref)
				}
			}
			creates := 0
			for _, path := range paths {
				if path == "POST /v1/videos" {
					creates++
				}
			}
			var orders int64
			f.db.Model(&model.BillingOrder{}).Where("task_id = ?", child.ID).Count(&orders)
			if creates != 1 || orders != 1 {
				t.Fatalf("duplicate submit/billing: creates=%d orders=%d", creates, orders)
			}
			var shot model.Shot
			if err := f.db.First(&shot, "id = ?", "s0").Error; err != nil || shot.DurationMs != 25000 {
				t.Fatalf("content duration mutated: %v %+v", err, shot)
			}
		})
	}
}
