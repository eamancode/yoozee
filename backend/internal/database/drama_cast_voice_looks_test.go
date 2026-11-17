package database

import (
	"testing"

	"yingce/backend/internal/model"
)

func TestMigrateDramaVoiceLooksAndDraftPreservesOldRows(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:" + t.Name() + "?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	for _, statement := range []string{
		"CREATE TABLE drama_casts (id text PRIMARY KEY, updated_at text, voice_profile_id text)",
		"CREATE TABLE drama_shots (id text PRIMARY KEY, updated_at text, dialogue text)",
		"CREATE TABLE drama_productions (id text PRIMARY KEY, status text)",
		"INSERT INTO drama_casts VALUES ('actor', 'original-cast-time', 'existing-voice')",
		"INSERT INTO drama_shots VALUES ('shot', 'original-shot-time', '已保存的台词')",
		"INSERT INTO drama_productions VALUES ('production', 'paused')",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	for pass := 0; pass < 2; pass++ {
		for _, m := range schemaMigrations {
			if m.name == "drama_cast_voice_looks" || m.name == "drama_draft_form" {
				if err := m.apply(db); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if !db.Migrator().HasColumn(&model.DramaProduction{}, "DraftJSON") || !db.Migrator().HasIndex(&model.DramaCast{}, "ParentID") {
		t.Fatal("missing draft column or cast parent index")
	}
	for _, query := range []string{
		"SELECT count(*) FROM drama_casts WHERE id='actor' AND updated_at='original-cast-time' AND voice_profile_id='existing-voice' AND look_from_chapter=0 AND parent_id=''",
		"SELECT count(*) FROM drama_shots WHERE id='shot' AND updated_at='original-shot-time' AND dialogue='已保存的台词' AND look_cast_ids_json=''",
		"SELECT count(*) FROM drama_productions WHERE id='production' AND status='paused' AND draft_json IS NULL",
	} {
		var count int
		if err := db.Raw(query).Scan(&count).Error; err != nil || count != 1 {
			t.Fatalf("original row or additive defaults differ, count=%d: %v", count, err)
		}
	}
}
