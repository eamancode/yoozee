package database

import (
	"slices"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// v64：yz-chapter-shots 的当前版本（可能是在线编辑版）标成第一个包版本，清单取内置种子、版本号和文件取这一版自己的；
// 其他版本不动；已有包版本时重跑不改。
func TestDramaStepPackagesMigrationMarksCurrentVersion(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:drama-step-packages?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	skill := model.Skill{ID: "yz-chapter-shots", Name: "章节分镜", SourceType: "builtin", Status: 1, CurrentVersionID: "v-online"}
	versions := []model.SkillVersion{
		{ID: "v-builtin", SkillID: skill.ID, VersionLabel: "2.2.1", Origin: "builtin"},
		{ID: "v-online", SkillID: skill.ID, VersionLabel: "2.2.1+online.1", Origin: "online", Note: "动作起止"},
	}
	files := []model.SkillFile{
		{ID: "f1", SkillVersionID: "v-online", Path: "SKILL.md"},
		{ID: "f2", SkillVersionID: "v-online", Path: "cards/相机四维编码.md"},
		{ID: "f3", SkillVersionID: "v-online", Path: "cards/镜头表规则速查.md"},
	}
	if err := db.Create(&skill).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&versions).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&files).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateDramaStepPackages(db); err != nil {
		t.Fatal(err)
	}
	var got []model.SkillVersion
	if err := db.Order("id").Find(&got, "skill_id = ?", skill.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got[0].Origin != "builtin" || got[0].Manifest != "" {
		t.Fatalf("历史内置版本被改了：%+v", got[0])
	}
	current := got[1]
	m, problems := drama.ParseStepPackageManifest([]byte(current.Manifest))
	if current.Origin != "package" || len(problems) > 0 || current.Note != "动作起止" {
		t.Fatalf("当前版本：origin=%q note=%q %v", current.Origin, current.Note, problems)
	}
	if m.ID != skill.ID || m.Step != drama.StepChapterShots || m.Contract != drama.ContractChapterShots || m.Version != "2.2.1+online.1" ||
		!slices.Equal(m.Files, []string{"SKILL.md", "cards/相机四维编码.md", "cards/镜头表规则速查.md"}) {
		t.Fatalf("清单：%+v", m)
	}
	if err := db.Model(&model.SkillVersion{}).Where("id = ?", "v-online").Update("manifest", `{"keep":true}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateDramaStepPackages(db); err != nil {
		t.Fatal(err)
	}
	var again model.SkillVersion
	if err := db.First(&again, "id = ?", "v-online").Error; err != nil || again.Manifest != `{"keep":true}` {
		t.Fatalf("已有包版本时重跑不该改：%q %v", again.Manifest, err)
	}
}
