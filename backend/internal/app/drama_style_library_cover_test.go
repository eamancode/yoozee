package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func testStyleLibraryPalette(hex string) drama.MasterPalette {
	return drama.MasterPalette{Groups: []drama.MasterPaletteGroup{{
		Key: drama.PaletteGroupProtagonist, Name: "主角组",
		Colors: []drama.PaletteColor{{NameZh: "护士蓝", NameEn: "Nurse Blue", Hex: hex, Usage: "制服"}},
	}}}
}

// seedStyleLibraryEntry 按「存进风格库」的形状直接建一条：封面是 ownerID 合成的主色卡图。
func seedStyleLibraryEntry(t *testing.T, svc *Service, ownerID string, name string) model.DramaStyleLibraryEntry {
	t.Helper()
	palette := testStyleLibraryPalette("#5B7FA3")
	cover, _, err := svc.renderMasterPaletteResource(ownerID, name, palette)
	if err != nil {
		t.Fatal(err)
	}
	paletteJSON, _ := json.Marshal(palette)
	now := time.Now()
	row := model.DramaStyleLibraryEntry{
		ID: newID(), ProjectType: drama.VisualLiveAction, Name: name, StyleAnchorJSON: `{"medium":"真人实拍"}`, MasterPaletteJSON: string(paletteJSON),
		CoverResourceID: cover.ID, Source: DramaStyleLibrarySourceManual, CreatedBy: ownerID, UpdatedBy: ownerID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.repo.CreateDramaStyleLibraryEntry(&row); err != nil {
		t.Fatal(err)
	}
	return row
}

// assertStyleLibraryCoverGone 是封面资源记录删了、文件经 Outbox 删了。
func assertStyleLibraryCoverGone(t *testing.T, svc *Service, resource *model.Resource) {
	t.Helper()
	if _, err := svc.repo.Resource(resource.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("旧封面资源记录应删掉：%v", err)
	}
	svc.drainResourceDeletionJobs(8)
	path := filepath.Join(svc.dataDir, "resources", filepath.FromSlash(resource.ObjectKey))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("旧封面文件应删掉：%s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func mustStyleLibraryCover(t *testing.T, svc *Service, id string) *model.Resource {
	t.Helper()
	entry, err := svc.repo.DramaStyleLibraryEntry(id)
	if err != nil || entry.CoverResourceID == "" {
		t.Fatalf("条目要有封面：%v %+v", err, entry)
	}
	resource, err := svc.repo.Resource(entry.CoverResourceID)
	if err != nil {
		t.Fatalf("封面资源要在：%v", err)
	}
	return resource
}

// 改名、改色板换了封面后删旧封面；只改说明不动封面；删条目连封面一起删。封面属于存入者，管理员操作也能删。
func TestStyleLibraryCoverCleanup(t *testing.T) {
	svc, _ := newDramaWorkerTestService(t)
	owner := &model.User{ID: "user-1", Role: "user"}
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	entry := seedStyleLibraryEntry(t, svc, owner.ID, "都市冷调悬疑")

	first := mustStyleLibraryCover(t, svc, entry.ID)
	description := "只改说明"
	if _, err := svc.UpdateDramaStyleLibraryEntry(owner, entry.ID, DramaStyleLibraryUpdateRequest{Description: &description}); err != nil {
		t.Fatal(err)
	}
	if again := mustStyleLibraryCover(t, svc, entry.ID); again.ID != first.ID {
		t.Fatalf("只改说明不换封面：%s → %s", first.ID, again.ID)
	}

	newName := "都市冷调"
	if _, err := svc.UpdateDramaStyleLibraryEntry(owner, entry.ID, DramaStyleLibraryUpdateRequest{Name: &newName}); err != nil {
		t.Fatal(err)
	}
	second := mustStyleLibraryCover(t, svc, entry.ID)
	if second.ID == first.ID {
		t.Fatal("改名要重新合成封面")
	}
	assertStyleLibraryCoverGone(t, svc, first)

	palette := testStyleLibraryPalette("#7A3B2E")
	if _, err := svc.UpdateDramaStyleLibraryEntry(admin, entry.ID, DramaStyleLibraryUpdateRequest{MasterPalette: &palette}); err != nil {
		t.Fatal(err)
	}
	third := mustStyleLibraryCover(t, svc, entry.ID)
	if third.ID == second.ID || third.UserID != admin.ID || second.UserID != owner.ID {
		t.Fatalf("改色板换封面（新封面属于改的人）：%s/%s → %s/%s", second.ID, second.UserID, third.ID, third.UserID)
	}
	assertStyleLibraryCoverGone(t, svc, second)

	if err := svc.DeleteDramaStyleLibraryEntry(owner, entry.ID); err != nil {
		t.Fatal(err)
	}
	assertStyleLibraryCoverGone(t, svc, third)
}

// 封面还被别的条目用着（或别的业务引用着）就不删。
func TestStyleLibraryCoverKeptWhileReferenced(t *testing.T) {
	svc, _ := newDramaWorkerTestService(t)
	owner := &model.User{ID: "user-1", Role: "user"}
	a := seedStyleLibraryEntry(t, svc, owner.ID, "甲")
	b := seedStyleLibraryEntry(t, svc, owner.ID, "乙")
	shared := mustStyleLibraryCover(t, svc, a.ID)
	if err := svc.repo.UpdateDramaStyleLibraryEntry(b.ID, map[string]any{"cover_resource_id": shared.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDramaStyleLibraryEntry(owner, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.repo.Resource(shared.ID); err != nil {
		t.Fatalf("另一条还拿它当封面，要保留：%v", err)
	}
	// 资源找不到也不让删除失败。
	if err := svc.repo.UpdateDramaStyleLibraryEntry(b.ID, map[string]any{"cover_resource_id": "missing-resource"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDramaStyleLibraryEntry(owner, b.ID); err != nil {
		t.Fatalf("封面资源已不在也照样删条目：%v", err)
	}
}

// 重名检查和写入之间被别人抢先：唯一索引冲突换成和重名检查一样的提示。
func TestStyleLibraryUniqueViolationMapsToNameTaken(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	// 迁移里有 (project_type, name) 唯一索引；这里补一个同义索引，保证测试不依赖迁移版本。
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_test_drama_style_library_unique_name ON drama_style_library (project_type, name)").Error; err != nil {
		t.Fatal(err)
	}
	entry := seedStyleLibraryEntry(t, svc, "user-1", "都市冷调悬疑")
	dup := entry
	dup.ID = newID()
	err := svc.repo.CreateDramaStyleLibraryEntry(&dup)
	if !isUniqueConstraintViolation(err) {
		t.Fatalf("应识别为唯一约束冲突：%v", err)
	}
	mapped := dramaStyleLibraryWriteError(err, entry.ProjectType, entry.Name)
	if mapped == nil || !strings.Contains(mapped.Error(), "已经有叫「都市冷调悬疑」的风格") {
		t.Fatalf("要换成重名提示：%v", mapped)
	}
	if isUniqueConstraintViolation(errors.New("connection refused")) || isUniqueConstraintViolation(nil) {
		t.Fatal("别的错误不算唯一约束冲突")
	}
	if other := errors.New("boom"); dramaStyleLibraryWriteError(other, "live-action", "x") != other {
		t.Fatal("别的错误原样返回")
	}
}
