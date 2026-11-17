package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

const routingOriginal = "system-video::seedance-2.5-test"

func newProjectRoutingFixture(t *testing.T) *videoChainFixture {
	t.Helper()
	f := newVideoChainFixture(t, "A")
	seedOnlySlotSystemVideo(t, f)
	setShotSeconds(t, f, 21)
	if err := f.db.Model(&model.Project{}).Where("id = ?", f.projectID).Update("type", "short-drama").Error; err != nil {
		t.Fatal(err)
	}
	f.req.Scope = "single"
	f.req.Model = ProjectVideoChainModel{Model: routingOriginal, Input: globalSixSecondsRequest("false")}
	return f
}

func addRoutingAlternative(t *testing.T, f *videoChainFixture, id string, credits int64, outcomes ...model.TaskStatus) {
	t.Helper()
	var item model.ChannelModel
	if err := f.db.First(&item, "id = ?", "system-model").Error; err != nil {
		t.Fatal(err)
	}
	item.ID, item.ChannelID = "model-"+id, id
	item.DisplayName = id
	cost := int64(4_000_000)
	selector := `{"videoSeconds":"30"}`
	tier := model.ChannelModelPriceTier{ID: id + "-30s", ChannelModelID: item.ID, ProviderModelKey: item.ProviderModelKey, SelectorJSON: selector, SelectorKey: selector, VideoSeconds: 30, UpstreamRequestCostMicroCNY: &cost, PriceConfigured: true, Enabled: true, BillingMode: "fixed_request", UnitPriceMicrocredits: credits}
	for _, record := range []any{&model.ModelChannel{ID: id, Scope: model.ChannelScopeSystem, Enabled: true, BaseURL: "https://unused.invalid", APIKey: "fixture"}, &item, &tier} {
		if err := f.db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().UTC().Add(-time.Minute)
	for _, outcome := range outcomes {
		task := model.Task{ID: newID(), UserID: "sample-user", Type: "canvas_video", Status: outcome, CompletedAt: &at}
		log := model.ApiCallLog{ID: newID(), TaskID: task.ID, ChannelID: id, VideoProviderModelKey: item.ProviderModelKey, Source: "backend-task", Capability: "video", RequestKind: "create", Method: "POST"}
		for _, record := range []any{&task, &log} {
			if err := f.db.Create(record).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Persist a mock provider receipt at the same boundary read by the child and
// controller workers. An attributable failure already has a terminal provider query.
func routingReceipt(t *testing.T, f *videoChainFixture, project, channel string, revision int64, status model.TaskStatus) *model.Task {
	t.Helper()
	input := globalSixSecondsRequest("false")
	cfg := input["config"].(map[string]any)
	cfg["channelId"], cfg["providerModelKey"], cfg["videoSeconds"] = channel, "seedance-2.5-mock", "30"
	input["metadata"] = map[string]any{"domainProjectId": project, "shotId": "s0", "videoEditOperation": "text_to_video", "projectVideoRouteRevision": revision, "artifactMetadata": map[string]any{"contentDurationSeconds": 21}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	task := model.Task{ID: newID(), RouteRun: 1, UserID: "user-1", ProjectID: project, Type: "canvas_video", Status: status, InputJSON: string(raw), ProviderRequestID: newID(), CompletedAt: &at}
	if status == model.TaskStatusFailed {
		task.PollStage = "provider_failed"
	}
	if err := f.db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	return &task
}

func requireRoutingStreak(t *testing.T, f *videoChainFixture, project, channel string, want int) {
	t.Helper()
	var streak model.ProjectVideoRouteStreak
	if err := f.db.Where("project_id = ? AND channel_id = ? AND provider_model_key = ?", project, channel, "seedance-2.5-mock").Take(&streak).Error; err != nil || streak.Failures != want {
		t.Fatalf("streak %s %s = %d, want %d: %v", project, channel, streak.Failures, want, err)
	}
}

func TestProjectVideoRoutingWorkerSwitchInvalidatesQuote(t *testing.T) {
	f := newProjectRoutingFixture(t)
	addRoutingAlternative(t, f, "best", 9_000_000, model.TaskStatusSucceeded)
	addRoutingAlternative(t, f, "lower", 1_000_000, model.TaskStatusSucceeded, model.TaskStatusFailed)
	var calls atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/videos" {
			t.Errorf("unexpected provider call %s %s", r.Method, r.URL.Path)
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"default 分组没有可用路由"}}`))
	}))
	t.Cleanup(mock.Close)
	if err := f.db.Model(&model.ModelChannel{}).Where("id = ?", "system-video").Update("base_url", mock.URL).Error; err != nil {
		t.Fatal(err)
	}
	preview, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil || preview.TotalMicrocredits != 3_000_000 {
		t.Fatalf("old quote %v, %v", preview, err)
	}
	for count := 1; count <= 2; count++ {
		receipt := routingReceipt(t, f, f.projectID, "system-video", 0, model.TaskStatusFailed)
		for range 2 {
			if err := f.s.recordProjectVideoTerminal(receipt); err != nil {
				t.Fatal(err)
			}
		}
		requireRoutingStreak(t, f, f.projectID, "system-video", count)
		if route, err := f.s.ProjectVideoRouting("user-1", f.projectID); err != nil || route != nil {
			t.Fatalf("early switch %v %v", route, err)
		}
	}
	chain := f.start(t)
	state := f.untilTask(t, chain.ID, 0)
	child, workerErr := runQueuedTask(t, f.s)
	if workerErr == nil || child.Status != model.TaskStatusFailed || child.ID != state.Steps[0].TaskID || calls.Load() != 1 {
		t.Fatalf("mock 503 receipt %s %s %v, calls %d", child.ID, child.Status, workerErr, calls.Load())
	}
	route, err := f.s.ProjectVideoRouting("user-1", f.projectID)
	if err != nil || route == nil || route.Model != "best::seedance-2.5-test" || route.FromModel != routingOriginal || route.Revision != 1 || route.Status != "auto" || route.SuccessRate == nil || *route.SuccessRate != 100 || route.SampledAt == nil {
		t.Fatalf("worker failed to select highest eligible rate: %+v %v", route, err)
	}
	requireRoutingStreak(t, f, f.projectID, "system-video", 3)
	f.tick(t, chain.ID)
	replayed, err := f.s.ProjectVideoRouting("user-1", f.projectID)
	if err != nil || replayed == nil || replayed.Revision != route.Revision || !replayed.ChangedAt.Equal(route.ChangedAt) {
		t.Fatalf("controller replay changed route %+v %v", replayed, err)
	}
	f.req.ResumeChainID, f.req.RequestID = chain.ID, newID()
	stale := f.req
	stale.PreviewHash = preview.PreviewHash
	if _, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID, stale); err == nil {
		t.Fatal("accepted old route and quote after failover")
	}
	f.req.Model.Model = route.Model
	f.req.Model.Input["config"].(map[string]any)["channelId"] = "best"
	fresh, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil || fresh.PreviewHash == preview.PreviewHash || fresh.TotalMicrocredits != 9_000_000 || len(fresh.Steps) != 1 || fresh.Steps[0].Model != route.Model {
		t.Fatalf("new route quote %+v %v", fresh, err)
	}
	stale = f.req
	stale.RequestID, stale.PreviewHash = newID(), preview.PreviewHash
	if _, err := f.s.RetryProjectVideoChain("user-1", f.projectID, chain.ID, stale); err == nil {
		t.Fatal("old quote bought the new route")
	}
	for _, check := range []struct {
		table any
		want  int64
	}{{&model.ProjectVideoRouteEvent{}, 3}, {&model.BillingOrder{}, 1}, {&model.Task{}, 7}} {
		var n int64
		if err := f.db.Model(check.table).Count(&n).Error; err != nil || n != check.want {
			t.Fatalf("unexpected record count %T = %d, want %d: %v", check.table, n, check.want, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("switch, polling or re-quote generated another video: %d", calls.Load())
	}
}

func TestProjectVideoRoutingManualRevisionAndNoRate(t *testing.T) {
	f := newProjectRoutingFixture(t)
	addRoutingAlternative(t, f, "manual", 2_000_000)
	projectB := model.Project{ID: newID(), Type: "short-drama", UserID: "user-1", Name: "第二项目", Status: model.ProjectStatusActive, GenerationModel: "seedance-2.5"}
	if err := f.db.Create(&projectB).Error; err != nil {
		t.Fatal(err)
	}
	record := func(project, channel string, rev int64, status model.TaskStatus) {
		t.Helper()
		if err := f.s.recordProjectVideoTerminal(routingReceipt(t, f, project, channel, rev, status)); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		status   model.TaskStatus
		failures int
	}{{model.TaskStatusFailed, 1}, {model.TaskStatusFailed, 2}, {model.TaskStatusSucceeded, 0}, {model.TaskStatusFailed, 1}, {model.TaskStatusFailed, 2}} {
		record(f.projectID, "system-video", 0, item.status)
		requireRoutingStreak(t, f, f.projectID, "system-video", item.failures)
	}
	record(projectB.ID, "system-video", 0, model.TaskStatusFailed)
	record(f.projectID, "manual", 0, model.TaskStatusFailed)
	requireRoutingStreak(t, f, projectB.ID, "system-video", 1)
	requireRoutingStreak(t, f, f.projectID, "manual", 1)
	requireRoutingStreak(t, f, f.projectID, "system-video", 2)
	third := routingReceipt(t, f, f.projectID, "system-video", 0, model.TaskStatusFailed)
	var workers sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		workers.Go(func() { errs <- f.s.recordProjectVideoTerminal(third) })
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	requireRoutingStreak(t, f, f.projectID, "system-video", 3)
	route, err := f.s.ProjectVideoRouting("user-1", f.projectID)
	if err != nil || route == nil || route.Status != "unavailable" || route.Model != routingOriginal || route.Revision != 1 || route.SuccessRate != nil || !strings.Contains(route.Reason, "手动") {
		t.Fatalf("missing no-rate reason: %+v %v", route, err)
	}
	if b, err := f.s.ProjectVideoRouting("user-1", projectB.ID); err != nil || b != nil {
		t.Fatalf("project B switched unexpectedly %+v %v", b, err)
	}
	if _, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req); err == nil {
		t.Fatal("unavailable route permitted a new quote")
	}
	manual := "manual::seedance-2.5-test"
	if _, err := f.s.OverrideProjectVideoRouting("other-user", f.projectID, manual, routingOriginal, 1); err == nil {
		t.Fatal("non-owner changed selection")
	}
	if _, err := f.s.OverrideProjectVideoRouting("user-1", f.projectID, manual, routingOriginal, 0); err == nil {
		t.Fatal("stale picker overwrote selection")
	} else {
		var appErr *AppError
		if !errors.As(err, &appErr) || appErr.Status != http.StatusConflict {
			t.Fatalf("stale picker error %v", err)
		}
	}
	chosen, err := f.s.OverrideProjectVideoRouting("user-1", f.projectID, manual, routingOriginal, 1)
	if err != nil || chosen == nil || chosen.Revision != 2 || chosen.Model != manual || chosen.Status != "manual" || chosen.FromModel != routingOriginal {
		t.Fatalf("manual selection %+v %v", chosen, err)
	}
	replay, err := f.s.OverrideProjectVideoRouting("user-1", f.projectID, manual, routingOriginal, 2)
	if err != nil || replay == nil || replay.Revision != 2 {
		t.Fatalf("same manual choice advanced revision %+v %v", replay, err)
	}
	// Same public model, but these outstanding tasks captured the earlier
	// selection. Even a third independent failure must leave the user's revision.
	record(f.projectID, "manual", 0, model.TaskStatusFailed)
	record(f.projectID, "manual", 0, model.TaskStatusFailed)
	route, err = f.s.ProjectVideoRouting("user-1", f.projectID)
	if err != nil || route == nil || route.Model != manual || route.Revision != 2 || route.Status != "manual" || !route.ChangedAt.Equal(chosen.ChangedAt) {
		t.Fatalf("late receipt overwrote manual choice: %+v %v", route, err)
	}
	f.req.Model.Model = manual
	f.req.Model.Input["config"].(map[string]any)["channelId"] = "manual"
	if quote, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req); err != nil || quote.TotalMicrocredits != 2_000_000 {
		t.Fatalf("manual route cannot re-quote: %+v %v", quote, err)
	}
	var orders int64
	if err := f.db.Model(&model.BillingOrder{}).Count(&orders).Error; err != nil || orders != 0 {
		t.Fatalf("route events or manual override charged %d orders: %v", orders, err)
	}
}
