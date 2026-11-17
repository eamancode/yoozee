package database

import (
	"testing"
	"yingce/backend/internal/model"
)

func TestSLLMigrationFrom100PreservesUnverifiedPrices(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:" + t.Name() + "?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	// These tables deliberately omit v101's nullable evidence columns.
	for _, query := range []string{
		"CREATE TABLE channel_model_price_tiers (id text PRIMARY KEY, unit_price_microcredits bigint)",
		"INSERT INTO channel_model_price_tiers VALUES ('existing', 9000000)",
		"CREATE TABLE api_call_logs (id text PRIMARY KEY)",
		"INSERT INTO api_call_logs VALUES ('existing')",
	} {
		if err := db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range schemaMigrations {
		if m.version != 101 {
			continue
		}
		for i := 0; i < 2; i++ {
			if err := db.Transaction(m.apply); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, row := range []any{&model.SLLCatalogImport{}, &model.SLLCatalogLine{}, &model.SLLMetricHour{}} {
		if !db.Migrator().HasTable(row) {
			t.Fatalf("missing %T", row)
		}
	}
	var count int64
	if err = db.Table("channel_model_price_tiers").Where("id='existing' AND unit_price_microcredits=9000000 AND upstream_request_cost_micro_cny IS NULL").Count(&count).Error; err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err = db.Table("api_call_logs").Where("id='existing' AND video_provider_model_key IS NULL").Count(&count).Error; err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
