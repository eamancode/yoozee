package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// The external test supplies the actual HTTP router. Keeping worker and database
// setup here lets it use the same local providers as the other video-chain tests.
func CheckUnknownVideoChainHTTP(t *testing.T, routerFor func(*Service) http.Handler) {
	t.Helper()
	f := newVideoChainFixture(t, "A", "A")
	features, err := f.s.FeatureAvailability()
	if err != nil {
		t.Fatal(err)
	}
	features.ShortDramaEnabled = true
	if _, err := f.s.UpdateFeatureAvailability(&model.User{ID: "admin", Role: model.UserRoleAdmin}, features.FeatureAvailability); err != nil {
		t.Fatal(err)
	}
	seedOnlySlotSystemVideo(t, f)
	const script = "生效参考素材\n@图1、@图2、@图3、@图4、@图5、@图6、@图7。\n镜头正文\n阿婆整理推车，林深站在左边。"
	segmentJSON := addHTTPRecoveryShot(t, f, script)

	var originalRequests, originalCreates, replacementRequests atomic.Int32
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originalRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/media/upload":
			fmt.Fprintf(w, `{"url":"http://%s/ref-%d.png"}`, r.Host, originalRequests.Load())
		case "POST /v1/videos":
			if originalCreates.Add(1) == 1 {
				fmt.Fprint(w, `{"id":"first-job","status":"queued"}`)
				return
			}
			var body struct {
				Prompt string
				Images []string
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !strings.Contains(body.Prompt, "阿婆整理推车，林深站在左边。") || len(body.Images) != 7 {
				t.Errorf("original request lost its script or references: %v, %+v", err, body)
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":{"message":"Service unavailable"}}`)
		case "GET /v1/videos/first-job":
			fmt.Fprintf(w, `{"id":"first-job","status":"completed","video_url":%q}`, dataURL("video/mp4", agentAvatarTestMP4()))
		default:
			t.Errorf("unexpected original request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(original.Close)
	replacement := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		replacementRequests.Add(1)
		t.Errorf("replacement contacted before confirmation: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(replacement.Close)
	profile := DefaultModelCapabilityConfigForModel("seedance-videos-compatible", "seedance-2.5-test")
	profile.Video.Duration = VideoDurationConfig{Selection: "enum", Values: []int{15, 30}, Default: 15}
	profile.Video.Operations = []string{"text_to_video", "reference_to_video"}
	profile.Video.References.MaxImages = 9
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.ChannelModel{}).Where("id = ?", "system-model").Update("capability_config_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.ModelChannel{}).Where("id = ?", "system-video").Update("base_url", original.URL).Error; err != nil {
		t.Fatal(err)
	}
	const newKey = "seedance-2.5-replacement"
	const newPrice = 4_200_000
	for _, row := range []any{
		&model.ModelChannel{ID: "replacement-video", Name: "replacement", Scope: model.ChannelScopeSystem, Enabled: true, BaseURL: replacement.URL, APIKey: "fixture-only"},
		&model.ChannelModel{ID: "replacement-model", ChannelID: "replacement-video", ModelKey: newKey, ProviderModelKey: newKey, Capability: "video", Protocol: model.ChannelInterfaceType("seedance-videos-compatible"), Enabled: true, BillingMode: "fixed_request", PriceConfigured: true, CapabilityConfigJSON: string(raw)},
		&model.ChannelModelPriceTier{ID: "replacement-tier", ChannelModelID: "replacement-model", SelectorKey: `{"videoSeconds":"30"}`, SelectorJSON: `{"videoSeconds":"30"}`, ProviderModelKey: newKey, Enabled: true, BillingMode: "fixed_request", PriceConfigured: true, UnitPriceMicrocredits: newPrice},
		&model.AuthSession{ID: "video-owner", TokenHash: auth.HashToken("fixture-token"), UserID: "user-1", ExpiresAt: time.Now().Add(time.Hour)},
	} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&model.User{}).Where("id = ?", "user-1").Update("status", model.UserStatusActive).Error; err != nil {
		t.Fatal(err)
	}
	originalRoute, err := f.s.OverrideProjectVideoRouting("user-1", f.projectID, "system-video::seedance-2.5-test", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	f.req.Model.Input = globalSixSecondsRequest("false")
	chain := f.start(t)
	first := f.untilTask(t, chain.ID, 0).Steps[0]
	completed, err := runQueuedTask(t, f.s)
	if err != nil || first.TaskID != completed.ID || completed.Status != model.TaskStatusSucceeded {
		t.Fatalf("original first shot: %v, %+v", err, completed)
	}
	paused := f.untilTask(t, chain.ID, 1)
	second := paused.Steps[1]
	if second.Quote == nil || second.Quote.AmountMicrocredits != 3_000_000 {
		t.Fatalf("unexpected original second-shot quote: %+v", second.Quote)
	}
	failed, err := runQueuedTask(t, f.s)
	if err == nil || failed.Status != model.TaskStatusFailed || failed.ID != second.TaskID || originalCreates.Load() != 2 {
		t.Fatalf("expected original 503 for second shot: %v, %+v", err, failed)
	}
	paused = f.tick(t, chain.ID)
	firstVideo := paused.Steps[0].VideoResourceID
	if firstVideo == "" || paused.Steps[0].Status != "succeeded" || paused.Steps[1].Status != "failed" {
		t.Fatalf("incorrect original checkpoints: %+v", paused.Steps)
	}
	if _, err := f.s.OverrideProjectVideoRouting("user-1", f.projectID, "replacement-video::"+newKey, originalRoute.Model, originalRoute.Revision); err != nil {
		t.Fatal(err)
	}
	beforeRequests := originalRequests.Load()
	before, err := f.s.repo.TaskForUser("user-1", chain.ID)
	if err != nil {
		t.Fatal(err)
	}
	router := routerFor(f.s)
	req := f.req
	req.ResumeChainID, req.RequestID, req.PreviewHash = chain.ID, "http-recovery-offer", ""
	input := globalSixSecondsRequest("false")
	input["config"].(map[string]any)["channelId"] = "replacement-video"
	input["config"].(map[string]any)["model"] = newKey
	req.Model = ProjectVideoChainModel{Model: newKey, Input: input}
	path := "/api/projects/" + f.projectID + "/video-chains"
	post := func(path string, req ProjectVideoChainRequest, wantStatus int, response any) {
		t.Helper()
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		in := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		in.Header.Set("Content-Type", "application/json")
		in.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "video-owner.fixture-token"})
		out := httptest.NewRecorder()
		router.ServeHTTP(out, in)
		if out.Code != wantStatus {
			t.Fatalf("POST %s: got %d, want %d: %s", path, out.Code, wantStatus, out.Body)
		}
		if err := json.Unmarshal(out.Body.Bytes(), response); err != nil {
			t.Fatal(err)
		}
	}
	var offer struct {
		Code int
		Data struct {
			Chain ProjectVideoChainView
		}
	}
	post(path+"/preview", req, http.StatusOK, &offer)
	next := offer.Data.Chain
	if offer.Code != CodeOK || next.ConfirmationBlockedReason == "" || next.PreviewHash == "" || next.PreviewHash == chain.PreviewHash || len(next.Steps) != 2 || next.TotalMicrocredits != newPrice {
		t.Fatalf("replacement preview did not quote remaining work and block confirmation: %+v", next)
	}
	done, pending := next.Steps[0], next.Steps[1]
	if done.ShotID != first.ShotID || done.Status != "succeeded" || done.TaskID != first.TaskID || done.VideoResourceID != firstVideo || done.Model != first.Model {
		t.Fatalf("replacement preview modified completed prefix: %+v", done)
	}
	if pending.ShotID != second.ShotID || pending.RevisionID != second.RevisionID || pending.TaskID != failed.ID || pending.ContentSeconds != 26 || pending.GenerationSeconds != 30 ||
		pending.ReferenceCount != 7 || pending.Model != newKey || pending.Quote == nil || pending.Quote.AmountMicrocredits != newPrice {
		t.Fatalf("replacement preview changed original content or priced the wrong route: %+v", pending)
	}
	d := pending.Diagnostic
	if d == nil || d.SubmissionOutcome != "unknown" || d.UpstreamCode != "HTTP 503" || d.SafeToResubmit || d.ChannelID != "system-video" || d.UpstreamModel != "seedance-2.5-mock" || d.QuoteID != chain.PreviewHash {
		t.Fatalf("replacement preview lost the original 503: %+v", d)
	}
	checkReadOnly := func() {
		t.Helper()
		for _, expected := range []struct {
			table any
			count int64
		}{{&model.Task{}, 3}, {&model.BillingOrder{}, 2}, {&model.RouteAttempt{}, 2}} {
			var count int64
			if err := f.db.Model(expected.table).Count(&count).Error; err != nil || count != expected.count {
				t.Fatalf("unexpected %T rows: %d, %v", expected.table, count, err)
			}
		}
		stored, err := f.s.repo.TaskForUser("user-1", chain.ID)
		if err != nil || stored.InputJSON != before.InputJSON || stored.Status != model.TaskStatusFailed {
			t.Fatalf("original controller modified: %v", err)
		}
		version, err := f.s.repo.ShotRevisionForShot("s1", second.RevisionID)
		if err != nil || version.SpecJSON != segmentJSON {
			t.Fatalf("recovery altered original shot script or reference bindings: %v", err)
		}
		var refs []model.ShotAssetReference
		if err := f.db.Where("shot_id = ?", second.ShotID).Order("position").Find(&refs).Error; err != nil || len(refs) != 7 {
			t.Fatalf("wrong number of original reference bindings: %d, %v", len(refs), err)
		}
		for i, ref := range refs {
			if ref.AssetVersionID != fmt.Sprintf("recovery-ref-%d-v", i) || ref.Position != i || ref.Status != "linked" {
				t.Fatalf("reference binding %d changed: %+v", i, ref)
			}
		}
		if originalRequests.Load() != beforeRequests || replacementRequests.Load() != 0 {
			t.Fatalf("preview/retry contacted a provider: %d/%d", originalRequests.Load()-beforeRequests, replacementRequests.Load())
		}
	}
	checkReadOnly()
	req.PreviewHash = next.PreviewHash
	for range 2 {
		var denial struct {
			Reason  ErrorReason
			Msg     string
			Details struct {
				ResumeChainID string
				ShotID        string
				TaskID        string
				Diagnostic    videoChainAttemptDiagnostic
			}
		}
		post(path+"/"+chain.ID+"/retry", req, http.StatusConflict, &denial)
		if denial.Reason != ReasonFailedPrecondition || denial.Msg == "" || denial.Details.ResumeChainID != chain.ID || denial.Details.TaskID != failed.ID || denial.Details.ShotID != second.ShotID ||
			denial.Details.Diagnostic.SubmissionOutcome != "unknown" || denial.Details.Diagnostic.SafeToResubmit {
			t.Fatalf("invalid retry denial: %+v", denial)
		}
		checkReadOnly()
	}
}

func addHTTPRecoveryShot(t *testing.T, f *videoChainFixture, script string) string {
	t.Helper()
	return addHTTPVideoChainShot(t, f, script, 26, 7)
}

func addHTTPVideoChainShot(t *testing.T, f *videoChainFixture, script string, seconds float64, refs int) string {
	t.Helper()
	if err := f.db.Model(&model.Project{}).Where("id = ?", f.projectID).Update("workflow_id", drama.WorkflowSSSDrama).Error; err != nil {
		t.Fatal(err)
	}
	seg := drama.Segment{ID: "S02", Scene: "A", Dur: seconds, Mode: drama.ModeLockIdentity, Verbatim: script,
		Shots: []drama.SubShot{{At: 0, Size: "中景", Text: "阿婆整理推车，林深站在左边。"}}}
	for i := range refs {
		id := fmt.Sprintf("recovery-ref-%d", i)
		src := image.NewRGBA(image.Rect(0, 0, 2, 3))
		src.SetRGBA(0, 0, color.RGBA{R: uint8(i + 1), A: 255})
		var data bytes.Buffer
		if err := png.Encode(&data, src); err != nil {
			t.Fatal(err)
		}
		file, err := f.s.UploadResourceFile("user-1", id+".png", int64(data.Len()), "image", 2, 3, 0, bytes.NewReader(data.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range []any{
			&model.Asset{ID: id, UserID: "user-1", Kind: "image", Category: model.AssetCategoryProp, Title: id, PrimaryVersionID: id + "-v", Status: model.AssetVersionStatusConfirmed, PayloadJSON: "{}"},
			&model.AssetVersion{ID: id + "-v", AssetID: id, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: "{}"},
			&model.ProjectAssetLink{ID: newID(), ProjectID: f.projectID, AssetID: id},
			&model.ShotAssetReference{ID: newID(), ShotID: "s1", AssetVersionID: id + "-v", Role: "reference", Status: "linked", Source: "segment", Position: i},
			&model.AssetRepresentation{ID: newID(), TaskID: id + "-image", AssetVersionID: id + "-v", ResourceID: file.ID, MediaType: "image", Role: "primary"},
		} {
			if err := f.db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		seg.Refs = append(seg.Refs, drama.Ref{File: id + "-v", Role: "prop"})
	}
	encoded, err := json.Marshal(seg)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.ShotRevision{}).Where("id = ?", "rs1").Update("spec_json", string(encoded)).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Shot{}).Where("id = ?", "s1").Update("duration_ms", int(seconds*1000)).Error; err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
