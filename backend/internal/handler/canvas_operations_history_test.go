package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func canvasHistoryHTTP(t *testing.T, router *gin.Engine, path, body string, wantStatus int) int64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "session.test-token"})
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	if out.Code != wantStatus {
		t.Fatalf("PATCH %s: status %d, want %d: %s", path, out.Code, wantStatus, out.Body.String())
	}
	var response struct {
		Code int `json:"code"`
		Data struct {
			Project struct {
				Revision int64 `json:"revision"`
			} `json:"project"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if (wantStatus == http.StatusOK) != (response.Code == 0) {
		t.Fatalf("HTTP and business result disagree: %s", out.Body.String())
	}
	return response.Data.Project.Revision
}

func canvasHistoryRows(t *testing.T, db *gorm.DB, id string) []model.CanvasSnapshot {
	t.Helper()
	var rows []model.CanvasSnapshot
	if err := db.Where("canvas_id = ?", id).Order("revision ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestCanvasOperationsHTTPAutomaticHistory(t *testing.T) {
	router, db := newCanvasOperationsHTTPTest(t)
	const path = "/api/canvas-projects/history/operations"
	create := `{"mutationId":"create","operations":[{"type":"create_canvas","patch":{"title":"History"}},{"type":"create_node","node":{"id":"text","type":"text","metadata":{"content":"original"}}}]}`
	if got := canvasHistoryHTTP(t, router, path, create, http.StatusOK); got != 1 {
		t.Fatalf("create revision = %d", got)
	}
	if rows := canvasHistoryRows(t, db, "history"); len(rows) != 0 {
		t.Fatalf("new canvas has no preimage, got %d snapshots", len(rows))
	}
	edit := func(id, before, after string) string {
		return fmt.Sprintf(`{"mutationId":%q,"operations":[{"type":"patch_node","id":"text","patch":{"metadata.content":%q},"before":{"metadata.content":%q}}]}`, id, after, before)
	}
	first := edit("first", "original", "first edit")
	if got := canvasHistoryHTTP(t, router, path, first, http.StatusOK); got != 2 {
		t.Fatalf("first edit revision = %d", got)
	}
	rows := canvasHistoryRows(t, db, "history")
	if len(rows) != 1 || rows[0].Revision != 1 || rows[0].UserID != "user" || rows[0].Reason != "automatic" || !strings.Contains(rows[0].PayloadJSON, `"content":"original"`) {
		t.Fatalf("first edit did not immediately preserve its owned preimage: %+v", rows)
	}
	if got := canvasHistoryHTTP(t, router, path, edit("second", "first edit", "second edit"), http.StatusOK); got != 3 {
		t.Fatalf("second edit revision = %d", got)
	}
	if got := len(canvasHistoryRows(t, db, "history")); got != 1 {
		t.Fatalf("edits inside history interval created %d snapshots", got)
	}
	// Age the existing snapshot instead of sleeping; the next request still uses
	// the real service clock and repository cooldown decision.
	if err := db.Model(&model.CanvasSnapshot{}).Where("id = ?", rows[0].ID).Update("created_at", time.Now().UTC().Add(-6*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if got := canvasHistoryHTTP(t, router, path, first, http.StatusOK); got != 2 {
		t.Fatalf("receipt replay lost original revision: %d", got)
	}
	if got := len(canvasHistoryRows(t, db, "history")); got != 1 {
		t.Fatalf("receipt replay after cooldown duplicated history: %d", got)
	}
	if got := canvasHistoryHTTP(t, router, path, edit("unchanged", "second edit", "second edit"), http.StatusOK); got != 4 {
		t.Fatalf("unchanged operation revision = %d", got)
	}
	if got := len(canvasHistoryRows(t, db, "history")); got != 1 {
		t.Fatalf("unchanged content created history: %d", got)
	}
	if got := canvasHistoryHTTP(t, router, path, edit("third", "second edit", "third edit"), http.StatusOK); got != 5 {
		t.Fatalf("third edit revision = %d", got)
	}
	rows = canvasHistoryRows(t, db, "history")
	if len(rows) != 2 || rows[1].Revision != 4 || !strings.Contains(rows[1].PayloadJSON, `"content":"second edit"`) {
		t.Fatalf("edit after cooldown lost its latest preimage: %+v", rows)
	}
	for _, row := range rows {
		var doc struct {
			ID       string `json:"id"`
			Revision int64  `json:"revision"`
		}
		if err := json.Unmarshal([]byte(row.PayloadJSON), &doc); err != nil || doc.ID != "history" || doc.Revision != row.Revision {
			t.Fatalf("snapshot identity/revision mismatch: %+v, %v", doc, err)
		}
	}
}

func TestCanvasOperationsHTTPHistoryRollsBackWithReceipt(t *testing.T) {
	router, db := newCanvasOperationsHTTPTest(t)
	const path = "/api/canvas-projects/rollback/operations"
	canvasHistoryHTTP(t, router, path, `{"mutationId":"create","operations":[{"type":"create_canvas","patch":{"title":"Before"}}]}`, http.StatusOK)
	var before model.CanvasProject
	if err := db.First(&before, "id = ?", "rollback").Error; err != nil {
		t.Fatal(err)
	}
	const callback = "test:reject-history-receipt"
	var snapshotsBeforeFailure int64
	if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "canvas_mutation_receipts" {
			return
		}
		if err := tx.Session(&gorm.Session{NewDB: true}).Model(&model.CanvasSnapshot{}).Where("canvas_id = ?", "rollback").Count(&snapshotsBeforeFailure).Error; err != nil {
			tx.AddError(err)
			return
		}
		tx.AddError(errors.New("injected receipt finalization failure"))
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
	edit := `{"mutationId":"rename","operations":[{"type":"set_field","field":"title","value":"After","beforeValue":"Before"}]}`
	canvasHistoryHTTP(t, router, path, edit, http.StatusInternalServerError)
	if snapshotsBeforeFailure != 1 {
		t.Fatalf("failure did not exercise outer rollback after snapshot insert: %d", snapshotsBeforeFailure)
	}
	var after model.CanvasProject
	if err := db.First(&after, "id = ?", "rollback").Error; err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Title != before.Title || after.PayloadJSON != before.PayloadJSON {
		t.Fatal("receipt failure committed the canvas")
	}
	if got := len(canvasHistoryRows(t, db, "rollback")); got != 0 {
		t.Fatalf("receipt failure committed %d snapshots", got)
	}
	var receipts int64
	if err := db.Model(&model.CanvasMutationReceipt{}).Where("canvas_id = ? AND mutation_id = ?", "rollback", "rename").Count(&receipts).Error; err != nil || receipts != 0 {
		t.Fatalf("failed receipt persisted: %d, %v", receipts, err)
	}
	if err := db.Callback().Update().Remove(callback); err != nil {
		t.Fatal(err)
	}
	if got := canvasHistoryHTTP(t, router, path, edit, http.StatusOK); got != 2 {
		t.Fatalf("failed request could not be retried once: %d", got)
	}
	canvasHistoryHTTP(t, router, path, edit, http.StatusOK)
	if got := len(canvasHistoryRows(t, db, "rollback")); got != 1 {
		t.Fatalf("retry/replay produced %d snapshots", got)
	}
}

func TestCanvasOperationsHTTPHistoryProtectsRemovedResource(t *testing.T) {
	router, db := newCanvasOperationsHTTPTest(t)
	for _, row := range []any{
		&model.Resource{ID: "image-resource", UserID: "user", Status: model.ResourceStatusReady, Kind: "image"},
		&model.Asset{ID: "image-asset", UserID: "user", Kind: "image", PayloadJSON: `{"storageKey":"resource:image-resource"}`},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	const path = "/api/canvas-projects/resources/operations"
	const node = `{"id":"image","type":"image","metadata":{"assetId":"image-asset","storageKey":"resource:image-resource"}}`
	canvasHistoryHTTP(t, router, path, `{"mutationId":"create","operations":[{"type":"create_canvas","patch":{"title":"Resources"}},{"type":"create_node","node":`+node+`}]}`, http.StatusOK)
	canvasHistoryHTTP(t, router, path, `{"mutationId":"remove","operations":[{"type":"remove_node","id":"image","expected":`+node+`}]}`, http.StatusOK)
	rows := canvasHistoryRows(t, db, "resources")
	if len(rows) != 1 || rows[0].NodeCount != 1 || rows[0].Revision != 1 {
		t.Fatalf("removed resource preimage not captured: %+v", rows)
	}
	var refs []model.CanvasSnapshotResource
	if err := db.Where("snapshot_id = ?", rows[0].ID).Find(&refs).Error; err != nil || len(refs) != 1 || refs[0].ResourceID != "image-resource" {
		t.Fatalf("snapshot resource protection missing: %+v, %v", refs, err)
	}
	if err := repository.New(db).RequireNoCanvasHistoryReferences([]string{"image-resource"}); !errors.Is(err, repository.ErrCanvasHistoryResourceReferenced) {
		t.Fatalf("removed resource was not protected by automatic history: %v", err)
	}
}
