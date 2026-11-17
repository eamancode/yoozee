package repository

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

func TestVideoChainProviderRequestIDUsesOriginalAttemptInstant(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.ApiCallLog{}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 12, 8, 0, 0, 200000000, time.UTC)
	insert := func(id, job string, at time.Time) {
		t.Helper()
		if err := db.Create(&model.ApiCallLog{ID: id, UserID: "owner", TaskID: "child", Capability: "video",
			RequestKind: "create", ProviderRequestID: job, CreatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
	}
	insert("old", "previous-job", start.Add(-time.Millisecond).In(time.FixedZone("east", 8*3600)))
	insert("current", "original-job", start.Add(time.Millisecond).In(time.FixedZone("west", -8*3600)))
	if id, err := New(db).VideoChainProviderRequestID("owner", "child", "", start, ""); err != nil || id != "original-job" {
		t.Fatalf("original request = %q, %v", id, err)
	}
	insert("inconsistent", "different-job", start.Add(time.Second))
	if id, err := New(db).VideoChainProviderRequestID("owner", "child", "", start, ""); err == nil || id != "" {
		t.Fatalf("conflicting receipts returned %q, %v", id, err)
	}
}
