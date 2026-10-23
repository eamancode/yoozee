package skills

import (
	"path/filepath"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// onlineEditSkillID 是在线编辑测试用的内置技能（yz-chapter-shots 已是流程技能包，不能在线编辑）。
const onlineEditSkillID = "yz-producer"

func newBuiltinVersionTestService(t *testing.T) (*Service, *gorm.DB, *model.User) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "skills.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.Skill{}, &model.UserSkillState{}, &model.SkillVersion{}, &model.SkillFile{}, &model.BuiltinSkillTombstone{}, &model.User{}, &model.UserIdentity{}); err != nil {
		t.Fatal(err)
	}
	admin := &model.User{ID: "admin-1", Username: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive}
	if err := db.Create(admin).Error; err != nil {
		t.Fatal(err)
	}
	svc := New(repository.New(db), t.TempDir(), nil)
	if err := svc.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
	return svc, db, admin
}

func loadSkillRow(t *testing.T, db *gorm.DB, id string) model.Skill {
	t.Helper()
	var skill model.Skill
	if err := db.First(&skill, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return skill
}

func editedEntry(t *testing.T, svc *Service, admin *model.User, versionID string, edit func(string) string) string {
	t.Helper()
	file, err := svc.AdminBuiltinSkillVersionFile(admin, onlineEditSkillID, versionID, "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	return edit(file.Content)
}

func TestBuiltinSkillOnlineVersionWinsOverStartupSync(t *testing.T) {
	svc, db, admin := newBuiltinVersionTestService(t)
	before := loadSkillRow(t, db, onlineEditSkillID)
	if before.BuiltinVersionID == "" || before.BuiltinVersionID != before.CurrentVersionID {
		t.Fatalf("builtin pointer not set on first sync: builtin=%q current=%q", before.BuiltinVersionID, before.CurrentVersionID)
	}
	builtinLabel := before.VersionLabel

	content := editedEntry(t, svc, admin, before.CurrentVersionID, func(s string) string { return s + "\n\n在线迭代：每镜动作写清起止状态。\n" })
	summary, version, err := svc.CreateBuiltinSkillVersion(admin, onlineEditSkillID, BuiltinSkillVersionRequest{Files: []BuiltinSkillFileEdit{{Path: "SKILL.md", Content: content}}, Note: "动作起止", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	if version == nil || version.Origin != SkillOriginOnline || version.VersionLabel != builtinLabel+"+online.1" || !version.Active {
		t.Fatalf("online version = %#v", version)
	}
	if !summary.Overridden || summary.OnlineVersionCount != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	after := loadSkillRow(t, db, onlineEditSkillID)
	if after.CurrentVersionID != version.ID || after.BuiltinVersionID != before.BuiltinVersionID || !strings.Contains(after.Instruction, "在线迭代") {
		t.Fatalf("skill after online edit: current=%q builtin=%q", after.CurrentVersionID, after.BuiltinVersionID)
	}
	// 运行时读到的是在线版本，并报出版本号。
	file, err := svc.SkillPackageFile("someone", onlineEditSkillID, "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if file.VersionID != version.ID || file.Version != version.VersionLabel || !strings.Contains(file.Content, "在线迭代") {
		t.Fatalf("runtime file version = %q/%q", file.VersionID, file.Version)
	}

	// 内置包没变时重启同步什么都不动。
	if err := svc.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
	if got := loadSkillRow(t, db, onlineEditSkillID); got.CurrentVersionID != version.ID || !strings.Contains(got.Instruction, "在线迭代") {
		t.Fatalf("startup sync replaced the online version: current=%q", got.CurrentVersionID)
	}
	var versionCount int64
	db.Model(&model.SkillVersion{}).Where("skill_id = ?", onlineEditSkillID).Count(&versionCount)
	if versionCount != 2 {
		t.Fatalf("version count = %d, want 2 (builtin + online)", versionCount)
	}

	// 内置包更新：记一个新的内置版本，在线版本仍生效，列表标出内置包有更新。
	newer := map[string][]byte{"SKILL.md": []byte(strings.Replace(content, "在线迭代", "内置包新写法", 1))}
	archive, err := finalizeSkillArchive(newer, parseSkillPackageMetadata(newer["SKILL.md"]))
	if err != nil {
		t.Fatal(err)
	}
	current := loadSkillRow(t, db, onlineEditSkillID)
	if err := svc.syncBuiltinPackage(&current, archive); err != nil {
		t.Fatal(err)
	}
	updated := loadSkillRow(t, db, onlineEditSkillID)
	if updated.CurrentVersionID != version.ID || updated.BuiltinVersionID == before.BuiltinVersionID {
		t.Fatalf("builtin update: current=%q builtin=%q", updated.CurrentVersionID, updated.BuiltinVersionID)
	}
	summary, versions, err := svc.AdminBuiltinSkillVersions(admin, onlineEditSkillID)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.BuiltinUpdated || len(versions) != 3 {
		t.Fatalf("summary after builtin update = %#v, %d versions", summary, len(versions))
	}

	// 回退到内置版本：生效版本、instruction 都换成最新的内置版本。
	summary, err = svc.RevertBuiltinSkill(admin, onlineEditSkillID)
	if err != nil {
		t.Fatal(err)
	}
	reverted := loadSkillRow(t, db, onlineEditSkillID)
	if summary.Overridden || reverted.CurrentVersionID != updated.BuiltinVersionID || !strings.Contains(reverted.Instruction, "内置包新写法") {
		t.Fatalf("revert: current=%q builtin=%q overridden=%v", reverted.CurrentVersionID, reverted.BuiltinVersionID, summary.Overridden)
	}

	// 再启用在线版本：任意历史版本都能设为生效。
	if _, err := svc.ActivateBuiltinSkillVersion(admin, onlineEditSkillID, version.ID); err != nil {
		t.Fatal(err)
	}
	if got := loadSkillRow(t, db, onlineEditSkillID); got.CurrentVersionID != version.ID {
		t.Fatalf("activate online version: current=%q", got.CurrentVersionID)
	}
}

func TestBuiltinSkillOnlineVersionSavedWithoutActivation(t *testing.T) {
	svc, db, admin := newBuiltinVersionTestService(t)
	before := loadSkillRow(t, db, onlineEditSkillID)
	content := editedEntry(t, svc, admin, before.CurrentVersionID, func(s string) string { return s + "\n草稿\n" })
	_, version, err := svc.CreateBuiltinSkillVersion(admin, onlineEditSkillID, BuiltinSkillVersionRequest{Files: []BuiltinSkillFileEdit{{Path: "SKILL.md", Content: content}}, Note: "草稿，先评测"})
	if err != nil {
		t.Fatal(err)
	}
	if version.Active {
		t.Fatal("draft version must not be active")
	}
	if got := loadSkillRow(t, db, onlineEditSkillID); got.CurrentVersionID != before.CurrentVersionID {
		t.Fatalf("draft changed the active version: %q", got.CurrentVersionID)
	}
	// 草稿能按版本读回。
	file, err := svc.AdminBuiltinSkillVersionFile(admin, onlineEditSkillID, version.ID, "SKILL.md")
	if err != nil || !strings.Contains(file.Content, "草稿") {
		t.Fatalf("read draft: %v", err)
	}
}

func TestBuiltinSkillOnlineVersionRejectsBrokenContract(t *testing.T) {
	svc, db, admin := newBuiltinVersionTestService(t)
	current := loadSkillRow(t, db, onlineEditSkillID).CurrentVersionID
	cases := map[string]func(string) string{
		"changes skillId": func(s string) string {
			return strings.Replace(s, `skillId: "yz-producer"`, `skillId: "yz-producer-2"`, 1)
		},
		"breaks frontmatter": func(s string) string { return strings.TrimPrefix(s, "---") },
	}
	for name, edit := range cases {
		content := editedEntry(t, svc, admin, current, edit)
		if _, _, err := svc.CreateBuiltinSkillVersion(admin, onlineEditSkillID, BuiltinSkillVersionRequest{Files: []BuiltinSkillFileEdit{{Path: "SKILL.md", Content: content}}, Note: name, Activate: true}); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
	unchanged := editedEntry(t, svc, admin, current, func(s string) string { return s })
	if _, _, err := svc.CreateBuiltinSkillVersion(admin, onlineEditSkillID, BuiltinSkillVersionRequest{Files: []BuiltinSkillFileEdit{{Path: "SKILL.md", Content: unchanged}}, Note: "没改"}); err == nil {
		t.Error("identical content must be rejected")
	}
	if _, _, err := svc.CreateBuiltinSkillVersion(admin, onlineEditSkillID, BuiltinSkillVersionRequest{DeletePaths: []string{"SKILL.md"}, Note: "删入口"}); err == nil {
		t.Error("deleting SKILL.md must be rejected")
	}
	member := &model.User{ID: "member-1", Username: "member", Role: model.UserRoleUser, Status: model.UserStatusActive}
	if err := db.Create(member).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdminBuiltinSkills(member); err == nil {
		t.Error("non-admin must not list builtin skill versions")
	}
	if _, err := svc.RevertBuiltinSkill(member, onlineEditSkillID); err == nil {
		t.Error("non-admin must not revert")
	}
	if got := loadSkillRow(t, db, onlineEditSkillID).CurrentVersionID; got != current {
		t.Fatalf("rejected edits changed the active version: %q", got)
	}
}

// v60 之前同步的库没有 builtin_version_id：内置包没变时只回填指针和来源，不新建版本。
func TestBuiltinSkillSyncBackfillsLegacyPointer(t *testing.T) {
	svc, db, _ := newBuiltinVersionTestService(t)
	skill := loadSkillRow(t, db, onlineEditSkillID)
	if err := db.Model(&model.Skill{}).Where("id = ?", skill.ID).Update("builtin_version_id", "").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.SkillVersion{}).Where("id = ?", skill.CurrentVersionID).Update("origin", "").Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
	after := loadSkillRow(t, db, onlineEditSkillID)
	if after.BuiltinVersionID != skill.CurrentVersionID || after.CurrentVersionID != skill.CurrentVersionID {
		t.Fatalf("backfill: builtin=%q current=%q want %q", after.BuiltinVersionID, after.CurrentVersionID, skill.CurrentVersionID)
	}
	var version model.SkillVersion
	if err := db.First(&version, "id = ?", skill.CurrentVersionID).Error; err != nil || version.Origin != SkillOriginBuiltin {
		t.Fatalf("origin backfill = %q, %v", version.Origin, err)
	}
}
