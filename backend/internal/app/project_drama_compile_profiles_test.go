package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

func seedOnlineQichuangAssets(t *testing.T, db *gorm.DB, project model.Project, unit model.ProjectUnit) {
	t.Helper()
	if err := db.Model(&model.ProjectUnit{}).Where("id = ?", unit.ID).Update("source_text", chapterHTML(t)).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, a := range []struct {
		id, title string
		category  model.AssetCategory
	}{{"char-1", "林小满", model.AssetCategoryCharacter}, {"char-2", "周岸", model.AssetCategoryCharacter}, {"loc-1", "病房走廊", model.AssetCategoryEnvironment}} {
		version := a.id + "-v1"
		if err := db.Create(&model.Asset{ID: a.id, UserID: "user-1", Kind: "image", Category: a.category, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: version, Title: a.title, PayloadJSON: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.AssetVersion{ID: version, AssetID: a.id, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ProjectAssetLink{ID: newID(), ProjectID: project.ID, AssetID: a.id, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func segmentsText(t *testing.T, views []DramaSegmentView) string {
	t.Helper()
	raw, err := json.Marshal(views)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestDramaCompileProfileVersionsDriveShotlistAndCompile(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.5")
	seedOnlineQichuangAssets(t, db, project, unit)
	admin := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	member := &model.User{ID: "user-1", Role: model.UserRoleUser}

	list, err := service.AdminDramaCompileProfiles(admin)
	if err != nil || list.ActiveVersion != 0 || len(list.Profiles) != 0 || !strings.Contains(string(list.Default), `"seedance"`) || !strings.Contains(string(list.Default), "live-realism") {
		t.Fatalf("初始只有内置默认，默认全文带风格块：%v %+v", err, list)
	}

	rows := loadOnlineRowsRaw(t)
	base, err := service.WriteDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	if base.Shotlist.CompileProfile.Version != 0 || !strings.Contains(segmentsText(t, base.Segments), "主光：") {
		t.Fatalf("默认档案装段：%+v", base.Shotlist.CompileProfile)
	}
	var stored model.DramaShotlist
	if err := db.First(&stored, "unit_id = ?", unit.ID).Error; err != nil || stored.CompileProfileVersion != 0 || !strings.Contains(stored.RowsJSON, "spec") {
		t.Fatalf("镜头表记下分镜行原文与档案版本：%v %d", err, stored.CompileProfileVersion)
	}
	compiled, err := service.CompileProjectShot("user-1", project.ID, base.Segments[0].ShotID, CompileProjectShotRequest{})
	if err != nil || compiled.CompileProfile.Version != 0 || !strings.Contains(compiled.Result.Prompt, "约束：") {
		t.Fatalf("默认档案编译：%v %+v", err, compiled.CompileProfile)
	}

	if _, err := service.CreateDramaCompileProfile(member, DramaCompileProfileRequest{Name: "x", Note: "x", Content: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("非管理员不能新建编译档案")
	}
	if _, err := service.CreateDramaCompileProfile(admin, DramaCompileProfileRequest{Name: "坏", Note: "未知占位符", Content: json.RawMessage(`{"light": {"key": "{oops}"}}`)}); err == nil {
		t.Fatal("未知占位符应被拒绝")
	}
	content := json.RawMessage(`{"light": {"key": "主光源"}, "seedance": {"constraint": "要求：{identity}、{text}。"}}`)
	v1, err := service.CreateDramaCompileProfile(admin, DramaCompileProfileRequest{Name: "光源写法", Note: "主光改叫主光源", Content: content, Enabled: true})
	if err != nil || v1.Version != 1 || !v1.Enabled {
		t.Fatalf("新建并启用 v1：%v %+v", err, v1)
	}
	if err := service.DeleteDramaCompileProfile(admin, v1.ID); err == nil {
		t.Fatal("启用中的版本不能删")
	}

	// 编译期的文字下次编译就生效；镜头画面句要重新装段。
	compiled, err = service.CompileProjectShot("user-1", project.ID, base.Segments[0].ShotID, CompileProjectShotRequest{})
	if err != nil || compiled.CompileProfile.Version != 1 || compiled.CompileProfile.Name != "光源写法" || !strings.Contains(compiled.Result.Prompt, "要求：") || strings.Contains(compiled.Result.Prompt, "约束：") {
		t.Fatalf("v1 编译：%v %+v", err, compiled.CompileProfile)
	}
	again, err := service.WriteDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: rows})
	if err != nil || again.Shotlist.CompileProfile.Version != 1 || !strings.Contains(segmentsText(t, again.Segments), "主光源：") || len(again.Segments) != len(base.Segments) {
		t.Fatalf("v1 装段：%v %+v", err, again.Shotlist.CompileProfile)
	}
	view, err := service.DramaShotlist("user-1", project.ID, unit.ID)
	if err != nil || view.CompileProfile.Version != 1 || view.CompileProfile.Name != "光源写法" {
		t.Fatalf("读回镜头表带档案版本：%v %+v", err, view.CompileProfile)
	}

	// 只保存不启用的 v2 不影响线上。
	v2, err := service.CreateDramaCompileProfile(admin, DramaCompileProfileRequest{Name: "草稿", Note: "先评测", Content: json.RawMessage(`{"seedance": {"sound_default": "安静"}}`)})
	if err != nil || v2.Version != 2 || v2.Enabled {
		t.Fatalf("草稿 v2：%v %+v", err, v2)
	}
	list, _ = service.AdminDramaCompileProfiles(admin)
	if list.ActiveVersion != 1 || len(list.Profiles) != 2 {
		t.Fatalf("生效仍是 v1：%+v", list.ActiveVersion)
	}
	if err := service.ActivateDramaCompileProfile(admin, v2.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ = service.AdminDramaCompileProfiles(admin); list.ActiveVersion != 2 {
		t.Fatalf("启用 v2 后只有它生效：%d", list.ActiveVersion)
	}

	// 回退到内置默认。
	if err := service.RevertDramaCompileProfile(admin); err != nil {
		t.Fatal(err)
	}
	compiled, err = service.CompileProjectShot("user-1", project.ID, again.Segments[0].ShotID, CompileProjectShotRequest{})
	if err != nil || compiled.CompileProfile.Version != 0 || !strings.Contains(compiled.Result.Prompt, "约束：") {
		t.Fatalf("回退后用内置默认：%v %+v", err, compiled.CompileProfile)
	}
	if err := service.DeleteDramaCompileProfile(admin, v1.ID); err != nil {
		t.Fatalf("停用后可以删历史版本：%v", err)
	}
}
