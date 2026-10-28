package database

import (
	"testing"
	"time"

	"yingce/backend/internal/model"
)

// v63：老项目按需求文档指定或画风对照回填风格档案（不动 updated_at），五个档案的生成分段默认绑 yz-chapter-shots；重跑不重复插入。
func TestDramaStepBindingsMigrationBackfillsProfilesAndSeedsBindings(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:drama-step-bindings?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.DramaStepBinding{}).Where("step = ? AND skill_id = ?", "chapter.shots", "yz-chapter-shots").Count(&count).Error; err != nil || count != 5 {
		t.Fatalf("默认绑定 5 格，得到 %d（%v）", count, err)
	}
	updated := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	projects := []model.Project{
		{ID: "p-anime", UserID: "u", Name: "a", StylePresetID: "v2-xianxia--epic--3d-anime--semi-real", UpdatedAt: updated},
		{ID: "p-brief", UserID: "u", Name: "b", BriefJSON: `{"style_profile":"duanju-shiga"}`, UpdatedAt: updated},
		{ID: "p-none", UserID: "u", Name: "c", UpdatedAt: updated},
		{ID: "p-set", UserID: "u", Name: "d", StylePresetID: "v2-xianxia--epic--3d-anime--semi-real", StyleProfile: "action-epic", UpdatedAt: updated},
	}
	if err := db.Create(&projects).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Project{}).Where("id <> ?", "p-set").UpdateColumn("style_profile", "").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateDramaStepBindings(db); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"p-anime": "anime-3d", "p-brief": "duanju-shiga", "p-none": "live-realism", "p-set": "action-epic"}
	for id, profile := range want {
		var p model.Project
		if err := db.First(&p, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if p.StyleProfile != profile || !p.UpdatedAt.Equal(updated) {
			t.Fatalf("%s：style_profile=%q updated_at=%v，期望 %q 且不动 updated_at", id, p.StyleProfile, p.UpdatedAt, profile)
		}
	}
	if err := db.Model(&model.DramaStepBinding{}).Where("step = ?", "chapter.shots").Count(&count).Error; err != nil || count != 5 {
		t.Fatalf("重跑不重复插入默认绑定：%d（%v）", count, err)
	}
	// 迁移 67 给五个档案种了风格锁定的默认绑定。
	if err := db.Model(&model.DramaStepBinding{}).Where("step = ? AND skill_id = ?", "style.lock", "yz-style-lock").Count(&count).Error; err != nil || count != 5 {
		t.Fatalf("风格锁定默认绑定 5 格，得到 %d（%v）", count, err)
	}
}

// v68：五个档案的场次色卡默认绑 yz-scene-palette，删掉已经没有的 style.bible 步骤的绑定；平台风格库同类型重名的条目
// 在后建的名字后面加序号，再建 (project_type, name) 唯一索引。
func TestDramaScenePaletteMigration(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:drama-scene-palette?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.DramaStepBinding{}).Where("step = ? AND skill_id = ?", "scene.palette", "yz-scene-palette").Count(&count).Error; err != nil || count != 5 {
		t.Fatalf("场次色卡默认绑定 5 格，得到 %d（%v）", count, err)
	}
	// 模拟升级前的库：去掉唯一索引，写进 style.bible 绑定和重名条目，再跑一次迁移。
	if err := db.Migrator().DropIndex(&model.DramaStyleLibraryEntry{}, "idx_drama_style_library_name"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&model.DramaStepBinding{ID: "dsb-old", StyleProfile: "live-realism", Step: "style.bible", SkillID: "yz-style-bible", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	entries := []model.DramaStyleLibraryEntry{
		{ID: "a", ProjectType: "live-action", Name: "冷调悬疑", CreatedAt: now},
		{ID: "b", ProjectType: "live-action", Name: "冷调悬疑", CreatedAt: now.Add(time.Second)},
		{ID: "c", ProjectType: "live-action", Name: "冷调悬疑（2）", CreatedAt: now.Add(2 * time.Second)},
		{ID: "d", ProjectType: "3d-anime", Name: "冷调悬疑", CreatedAt: now.Add(3 * time.Second)},
	}
	if err := db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateDramaScenePalette(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.DramaStepBinding{}).Where("step = ?", "style.bible").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("style.bible 的绑定删掉：%d（%v）", count, err)
	}
	names := map[string]string{}
	var rows []model.DramaStyleLibraryEntry
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		names[r.ID] = r.Name
	}
	if names["a"] != "冷调悬疑" || names["b"] != "冷调悬疑（3）" || names["c"] != "冷调悬疑（2）" || names["d"] != "冷调悬疑" {
		t.Fatalf("重名的后建条目加序号、跳过已占用的名字：%v", names)
	}
	if !db.Migrator().HasIndex(&model.DramaStyleLibraryEntry{}, "idx_drama_style_library_name") {
		t.Fatal("唯一索引没建")
	}
	if err := db.Create(&model.DramaStyleLibraryEntry{ID: "e", ProjectType: "live-action", Name: "冷调悬疑", CreatedAt: now}).Error; err == nil {
		t.Fatal("同类型重名应被唯一索引拦下")
	}
}
