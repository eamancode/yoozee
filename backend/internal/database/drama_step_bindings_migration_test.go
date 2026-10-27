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
