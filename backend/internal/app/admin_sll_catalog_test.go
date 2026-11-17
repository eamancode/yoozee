package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

type sllPublicFixtureTransport struct {
	t       *testing.T
	pricing atomic.Int32
	metrics atomic.Int32
}

func (f *sllPublicFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Helper()
	if req.Method != http.MethodGet || req.URL.Host != "api.lecapi.net" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
		f.t.Fatalf("unexpected public catalog request: %s %s", req.Method, req.URL.Path)
	}
	var filename string
	switch req.URL.Path {
	case "/api/pricing":
		filename = "pricing.json"
		f.pricing.Add(1)
	case "/api/status":
		filename = "status.json"
	case "/api/media-models/catalog":
		filename = "media.json"
	case "/api/perf-metrics/public":
		filename = "metrics.json"
		f.metrics.Add(1)
	default:
		f.t.Fatalf("unexpected endpoint: %s", req.URL.Path)
	}
	file, err := os.Open(filepath.Join("..", "sllcatalog", "testdata", filename))
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: file, Header: http.Header{"Content-Type": {"application/json"}}}, nil
}

func TestSLLAdminSyncAndHourlyMetricsAreIndependent(t *testing.T) {
	svc, db := newChannelModelTestService(t)
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(&model.SLLCatalogImport{}, &model.SLLCatalogLine{}, &model.SLLMetricHour{}); err != nil {
		t.Fatal(err)
	}
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	channel := model.ModelChannel{ID: "sll", Name: "SLL", Scope: model.ChannelScopeSystem, Enabled: true, BaseURL: "https://api.lecapi.net/v1", APIKey: "fixture-never-sent", ModelsJSON: "[]"}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	source := &sllPublicFixtureTransport{t: t}
	svc.sllPublicClient = &http.Client{Transport: source}
	ctx := context.Background()
	if err := svc.refreshSLLMetrics(ctx, time.Now().UTC()); err != nil || source.metrics.Load() != 0 {
		t.Fatalf("worker before first import: %v, fetches %d", err, source.metrics.Load())
	}
	view, err := svc.SyncAdminSLLCatalog(ctx, admin, channel.ID, "default")
	if err != nil || view.Added == 0 || source.pricing.Load() != 1 || source.metrics.Load() != 0 {
		t.Fatalf("sync: added=%d, prices=%d, metrics=%d, err=%v", view.Added, source.pricing.Load(), source.metrics.Load(), err)
	}
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Rows []map[string]json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(body, &wire); err != nil || len(wire.Rows) == 0 || wire.Rows[0]["modelId"] == nil {
		t.Fatalf("missing admin model ID in JSON: %s", body)
	}
	var tiers []map[string]json.RawMessage
	if err := json.Unmarshal(wire.Rows[0]["tiers"], &tiers); err != nil || len(tiers) == 0 || tiers[0]["costMicroCny"] == nil {
		t.Fatalf("missing upstream cost in JSON: %s", wire.Rows[0]["tiers"])
	}
	if err := db.Exec("CREATE TRIGGER block_sll_names BEFORE UPDATE OF models_json ON model_channels BEGIN SELECT RAISE(ABORT, 'fixture channel projection failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SyncAdminSLLCatalog(ctx, admin, channel.ID, "vip"); err == nil || !strings.Contains(err.Error(), "fixture channel projection failure") {
		t.Fatalf("expected channel projection failure, got %v", err)
	}
	current, err := svc.AdminSLLCatalog(ctx, admin, channel.ID)
	if err != nil || current.Group != "default" || current.CollectedAt == nil || view.CollectedAt == nil || !current.CollectedAt.Equal(*view.CollectedAt) {
		t.Fatalf("catalog changed after failed projection: %+v, %v", current, err)
	}
	worker := svc.backgroundWorkers()
	worker.Start()
	t.Cleanup(func() {
		stopping, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := worker.Stop(stopping); err != nil {
			t.Error(err)
		}
	})
	svc.startSLLMetricsWorker()
	var initial model.SLLMetricHour
	for deadline := time.Now().Add(5 * time.Second); initial.Hour.IsZero(); {
		if err := db.Limit(1).Find(&initial).Error; err != nil {
			t.Fatal(err)
		}
		if initial.Hour.IsZero() {
			if time.Now().After(deadline) {
				t.Fatal("the hourly worker did not collect its initial snapshot")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	start := initial.Hour
	for _, at := range []time.Time{start.Add(10 * time.Minute), start.Add(time.Hour)} {
		if err := svc.refreshSLLMetrics(ctx, at); err != nil {
			t.Fatal(err)
		}
	}
	var snapshots int64
	if err := db.Model(&model.SLLMetricHour{}).Count(&snapshots).Error; err != nil || snapshots != 2 || source.pricing.Load() != 2 {
		t.Fatalf("hourly snapshots=%d, price requests=%d, err=%v", snapshots, source.pricing.Load(), err)
	}
	latest, err := svc.repo.SLLCatalog().View(ctx, channel.ID, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range latest.Rows {
		if line.ModelID == "lec-hc-seedance-2-5-3010" && line.Metric.Rate != nil && *line.Metric.Rate == 0 {
			return
		}
	}
	t.Fatal("fixture model's real zero rate was lost")
}
