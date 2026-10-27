package skills

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

const chapterShotsSkillID = "yz-chapter-shots"

// seedPackageFiles 是内置种子 backend/builtin/yz-chapter-shots（与 yoozee-skills/packages/yz-chapter-shots 一致）。
func seedPackageFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := fs.WalkDir(builtinSkillFiles, chapterShotsSkillID, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := fs.ReadFile(builtinSkillFiles, p)
		files[strings.TrimPrefix(p, chapterShotsSkillID+"/")] = content
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// buildPackageZip 与 build.py --package 同样打包：files 列出的文件、package.json、package.hash.json。
func buildPackageZip(t *testing.T, files map[string][]byte, edit func(map[string][]byte)) []byte {
	t.Helper()
	payload := map[string][]byte{}
	for p, c := range files {
		payload[p] = c
	}
	if edit != nil {
		edit(payload)
	}
	hash, _ := contentHashAndSize(payload)
	digest := flowPackageHashDoc{ContentHash: hash, Files: map[string]string{}}
	for p, c := range payload {
		sum := sha256.Sum256(c)
		digest.Files[p] = hex.EncodeToString(sum[:])
	}
	raw, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, p := range append(sortedSkillPaths(payload), drama.StepPackageHashFile) {
		entry, err := w.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		content := payload[p]
		if p == drama.StepPackageHashFile {
			content = raw
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func setManifest(t *testing.T, files map[string][]byte, edit func(*drama.StepPackageManifest)) {
	t.Helper()
	m, problems := drama.ParseStepPackageManifest(files[drama.StepPackageManifestFile])
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	edit(&m)
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	files[drama.StepPackageManifestFile] = raw
}

func packageProblems(t *testing.T, err error) []drama.StepPackageProblem {
	t.Helper()
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) || appErr.Reason != kernel.ReasonPackageCheckFailed || appErr.Status != 400 {
		t.Fatalf("want package_check_failed, got %v", err)
	}
	problems, _ := appErr.Details["problems"].([]drama.StepPackageProblem)
	if len(problems) == 0 {
		t.Fatalf("details.problems 为空：%#v", appErr.Details)
	}
	return problems
}

func problemRules(problems []drama.StepPackageProblem) []string {
	var rules []string
	for _, p := range problems {
		rules = append(rules, p.Rule)
	}
	slices.Sort(rules)
	return slices.Compact(rules)
}

// 首次安装：内置种子落成第一个包版本并生效；启动同步再跑不动它，「内置技能」页不再列出它。
func TestFlowPackageSeededOnFirstInstall(t *testing.T) {
	svc, db, admin := newBuiltinVersionTestService(t)
	skill := loadSkillRow(t, db, chapterShotsSkillID)
	summary, versions, err := svc.AdminFlowPackageVersions(admin, chapterShotsSkillID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Origin != SkillOriginPackage || !versions[0].Active || versions[0].VersionLabel != "2.3.0" || versions[0].Check.Status != FlowPackageCheckPassed {
		t.Fatalf("versions = %+v", versions)
	}
	if summary.Step != drama.StepChapterShots || summary.Contract != drama.ContractChapterShots || summary.StepContract != drama.ContractChapterShots || skill.CurrentVersionID != versions[0].ID {
		t.Fatalf("summary = %+v", summary)
	}
	if err := svc.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&model.SkillVersion{}).Where("skill_id = ?", chapterShotsSkillID).Count(&count)
	if count != 1 {
		t.Fatalf("restart sync added versions: %d", count)
	}
	builtin, err := svc.AdminBuiltinSkills(admin)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(builtin, func(s BuiltinSkillSummary) bool { return s.SkillID == chapterShotsSkillID }) {
		t.Fatal("流程技能包不该出现在「内置技能」页")
	}
	if _, _, err := svc.CreateBuiltinSkillVersion(admin, chapterShotsSkillID, BuiltinSkillVersionRequest{Files: []BuiltinSkillFileEdit{{Path: "SKILL.md", Content: "x"}}, Note: "改"}); err == nil {
		t.Fatal("流程技能包不能在线编辑")
	}
}

// 上传 → 已校验未启用 → 手动启用 → 运行时读到改动 → 回退到历史版本恢复原样；内置种子再同步也不覆盖。
func TestFlowPackageUploadActivateRollback(t *testing.T) {
	svc, db, admin := newBuiltinVersionTestService(t)
	original := loadSkillRow(t, db, chapterShotsSkillID).CurrentVersionID
	seed := seedPackageFiles(t)
	data := buildPackageZip(t, seed, func(files map[string][]byte) {
		files["SKILL.md"] = []byte(strings.Replace(string(files["SKILL.md"]), "你是这一章的导演。", "你是这一章的总导演。", 1))
		setManifest(t, files, func(m *drama.StepPackageManifest) { m.Version = "2.2.2" })
	})
	result, err := svc.uploadFlowPackage(admin.ID, data, "导演改成总导演")
	if err != nil {
		t.Fatal(err)
	}
	if result.Version.Active || result.Version.Origin != SkillOriginPackage || result.Version.Check.Status != FlowPackageCheckPassed || result.Version.CreatedByName != "admin" || result.Version.Note != "导演改成总导演" {
		t.Fatalf("uploaded = %+v", result.Version)
	}
	if got := loadSkillRow(t, db, chapterShotsSkillID).CurrentVersionID; got != original {
		t.Fatal("上传不该直接启用")
	}
	pkg, err := svc.StepSkillPackage(chapterShotsSkillID)
	if err != nil || pkg.Version != "2.3.0" {
		t.Fatalf("未启用时运行时仍读旧版本：%v %v", pkg, err)
	}

	if _, err := svc.ActivateFlowPackageVersion(admin, chapterShotsSkillID, result.Version.ID); err != nil {
		t.Fatal(err)
	}
	pkg, err = svc.StepSkillPackage(chapterShotsSkillID)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Version != "2.2.2" || pkg.VersionID != result.Version.ID || !strings.Contains(stepEntry(pkg), "你是这一章的总导演。") {
		t.Fatalf("启用后运行时版本 = %s", pkg.Version)
	}
	if err := svc.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
	if got := loadSkillRow(t, db, chapterShotsSkillID).CurrentVersionID; got != result.Version.ID {
		t.Fatal("启动同步覆盖了上传的包版本")
	}

	summary, err := svc.ActivateFlowPackageVersion(admin, chapterShotsSkillID, original)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err = svc.StepSkillPackage(chapterShotsSkillID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CurrentVersion == nil || summary.CurrentVersion.ID != original || pkg.Version != "2.3.0" || strings.Contains(stepEntry(pkg), "总导演") || stepEntry(pkg) != string(seed["SKILL.md"]) {
		t.Fatalf("回退后 = %s", pkg.Version)
	}

	// 同一个版本号不能再传。
	again := buildPackageZip(t, seed, func(files map[string][]byte) {
		files["SKILL.md"] = append(files["SKILL.md"], '\n')
		setManifest(t, files, func(m *drama.StepPackageManifest) { m.Version = "2.2.2" })
	})
	if rules := problemRules(packageProblems(t, mustFail(svc.uploadFlowPackage(admin.ID, again, "")))); !slices.Equal(rules, []string{drama.PackageRuleVersion}) {
		t.Fatalf("重复版本号：%v", rules)
	}
}

func stepEntry(pkg *StepPackage) string {
	for _, f := range pkg.Files {
		if f.Path == "SKILL.md" {
			return f.Content
		}
	}
	return ""
}

func mustFail[T any](_ T, err error) error { return err }

// 坏包被拒绝并逐条说明原因：缺卡、契约版本不符、超出预算、哈希不符、未登记的包；拒绝的包不落版本。
func TestFlowPackageUploadRejectsBrokenPackages(t *testing.T) {
	svc, db, admin := newBuiltinVersionTestService(t)
	seed := seedPackageFiles(t)
	bump := func(m *drama.StepPackageManifest) { m.Version = "9.9.9" }
	cases := map[string]struct {
		data []byte
		want []string
	}{
		"缺卡": {buildPackageZip(t, seed, func(files map[string][]byte) {
			delete(files, "cards/情绪弧索引.md")
			setManifest(t, files, func(m *drama.StepPackageManifest) {
				bump(m)
				m.Files = slices.DeleteFunc(m.Files, func(f string) bool { return f == "cards/情绪弧索引.md" })
			})
		}), []string{drama.PackageRuleRequiredCard}},
		"契约版本不符": {buildPackageZip(t, seed, func(files map[string][]byte) {
			setManifest(t, files, func(m *drama.StepPackageManifest) { bump(m); m.Contract = drama.ContractCanvasStoryboard })
		}), []string{drama.PackageRuleContract}},
		"超出预算": {buildPackageZip(t, seed, func(files map[string][]byte) {
			files["cards/镜头表规则速查.md"] = []byte(strings.Repeat("规则", 16000))
			setManifest(t, files, bump)
		}), []string{drama.PackageRuleBudget}},
		"未知风格档案": {buildPackageZip(t, seed, func(files map[string][]byte) {
			setManifest(t, files, func(m *drama.StepPackageManifest) { bump(m); m.Profiles = append(m.Profiles, "noir") })
		}), []string{drama.PackageRuleProfiles}},
		"未登记的包": {buildPackageZip(t, seed, func(files map[string][]byte) {
			setManifest(t, files, func(m *drama.StepPackageManifest) { bump(m); m.ID = "yz-chapter-shots-fight" })
		}), []string{drama.PackageRuleEntry, drama.PackageRuleManifest}},
		"不是 zip": {[]byte("not a zip"), []string{drama.PackageRuleArchive}},
	}
	for name, c := range cases {
		problems := packageProblems(t, mustFail(svc.uploadFlowPackage(admin.ID, c.data, "")))
		if rules := problemRules(problems); !slices.Equal(rules, c.want) {
			t.Errorf("%s：规则 %v，期望 %v（%+v）", name, rules, c.want, problems)
		}
	}
	// 构建后改过内容：哈希对不上。
	files := map[string][]byte{}
	for p, content := range seed {
		files[p] = content
	}
	setManifest(t, files, bump)
	raw := buildPackageZip(t, files, nil)
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range reader.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var content bytes.Buffer
		_, _ = content.ReadFrom(r)
		r.Close()
		if path.Base(f.Name) == "SKILL.md" {
			content.WriteString("\n偷偷改了\n")
		}
		entry, _ := w.Create(f.Name)
		_, _ = entry.Write(content.Bytes())
	}
	_ = w.Close()
	if rules := problemRules(packageProblems(t, mustFail(svc.uploadFlowPackage(admin.ID, buf.Bytes(), "")))); !slices.Equal(rules, []string{drama.PackageRuleHash}) {
		t.Errorf("哈希不符：%v", rules)
	}
	var count int64
	db.Model(&model.SkillVersion{}).Where("skill_id = ?", chapterShotsSkillID).Count(&count)
	if count != 1 {
		t.Fatalf("拒绝的包落了版本：%d", count)
	}
	member := &model.User{ID: "member-1", Username: "member", Role: model.UserRoleUser, Status: model.UserStatusActive}
	if err := db.Create(member).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdminFlowPackages(member); err == nil {
		t.Fatal("非管理员不能看流程技能包")
	}
	if _, err := svc.ActivateFlowPackageVersion(member, chapterShotsSkillID, loadSkillRow(t, db, chapterShotsSkillID).CurrentVersionID); err == nil {
		t.Fatal("非管理员不能启用")
	}
}

// 已有库：v64 把当前版本标成包版本、清单取种子；契约升级后旧版本在列表里标成不通过，不能启用；内置或在线版本不能在流程里启用。
func TestFlowPackageVersionCheckAgainstCurrentSteps(t *testing.T) {
	svc, db, admin := newBuiltinVersionTestService(t)
	current := loadSkillRow(t, db, chapterShotsSkillID).CurrentVersionID
	broken := map[string]any{"id": chapterShotsSkillID, "step": drama.StepChapterShots, "contract": "chapter.shots@1", "profiles": []string{"live-realism"}, "version": "2.2.1", "files": []string{"SKILL.md", "cards/情绪弧索引.md", "cards/相机四维编码.md", "cards/镜头表规则速查.md"}}
	raw, _ := json.Marshal(broken)
	if err := db.Model(&model.SkillVersion{}).Where("id = ?", current).Update("manifest", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	_, versions, err := svc.AdminFlowPackageVersions(admin, chapterShotsSkillID)
	if err != nil {
		t.Fatal(err)
	}
	if versions[0].Check.Status != FlowPackageCheckFailed || problemRules(versions[0].Check.Problems)[0] != drama.PackageRuleContract {
		t.Fatalf("check = %+v", versions[0].Check)
	}
	// 一个历史内置版本（不是包版本）。
	legacy := buildPackageZip(t, seedPackageFiles(t), nil)
	archive, err := archiveFromZip(legacy, "")
	if err != nil {
		t.Fatal(err)
	}
	delete(archive.Files, drama.StepPackageHashFile)
	legacyID, err := svc.addSkillVersionWith(chapterShotsSkillID, archive, model.SkillVersion{VersionLabel: "2.2.0", Origin: SkillOriginBuiltin}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ActivateFlowPackageVersion(admin, chapterShotsSkillID, legacyID); err == nil || !strings.Contains(err.Error(), "不是包版本") {
		t.Fatalf("内置历史版本不能在流程里启用：%v", err)
	}
}
