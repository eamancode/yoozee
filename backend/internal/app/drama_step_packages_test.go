package app

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"mime/multipart"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	builtinAssets "yingce/backend/builtin"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// chapterShotsPackageZip 按 build.py --package 的格式打包内置种子 yz-chapter-shots（与 yoozee-skills/packages 一致），edit 改文件。
func chapterShotsPackageZip(t *testing.T, edit func(map[string][]byte)) []byte {
	t.Helper()
	files := map[string][]byte{}
	err := fs.WalkDir(builtinAssets.FS, "yz-chapter-shots", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := fs.ReadFile(builtinAssets.FS, p)
		files[strings.TrimPrefix(p, "yz-chapter-shots/")] = content
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	edit(files)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	whole := sha256.New()
	digests := map[string]string{}
	for _, p := range paths {
		whole.Write([]byte(p))
		whole.Write([]byte{0})
		whole.Write(files[p])
		whole.Write([]byte{0})
		sum := sha256.Sum256(files[p])
		digests[p] = hex.EncodeToString(sum[:])
	}
	hashDoc, _ := json.Marshal(map[string]any{"contentHash": hex.EncodeToString(whole.Sum(nil)), "files": digests})
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, p := range append(paths, drama.StepPackageHashFile) {
		entry, err := w.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		content := files[p]
		if p == drama.StepPackageHashFile {
			content = hashDoc
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

func packageFileHeader(t *testing.T, data []byte) *multipart.FileHeader {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "yz-chapter-shots.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = w.Close()
	req := httptest.NewRequest("POST", "/api/admin/drama/packages", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if err := req.ParseMultipartForm(32 << 20); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = req.MultipartForm.RemoveAll() })
	return req.MultipartForm.File["file"][0]
}

func bumpManifest(t *testing.T, files map[string][]byte, edit func(*drama.StepPackageManifest)) {
	t.Helper()
	m, problems := drama.ParseStepPackageManifest(files[drama.StepPackageManifestFile])
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	edit(&m)
	files[drama.StepPackageManifestFile], _ = json.MarshalIndent(m, "", "  ")
}

// 第二期验收：改 yz-chapter-shots 一个字、上传，后台出现新版本（未启用，提示词不变）；手动启用后「只组装提示词」带上改动；
// 回退到原版本后与黄金文件逐字相同。缺卡、契约版本不符、超出预算的包被拒绝并逐条说明。
func TestFlowPackageUploadChangesAssembledPrompt(t *testing.T) {
	svc, db := newChapterShotsTestService(t)
	project, unit := seedQichuang(t, svc, db)
	admin := &model.User{ID: "admin-1", Username: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive}
	if err := db.Create(admin).Error; err != nil {
		t.Fatal(err)
	}
	golden := readTestdata(t, "server-prompt.txt")
	promptNow := func() DramaStepPrompt {
		t.Helper()
		got, err := svc.DramaStepPrompt("user-1", project.ID, unit.ID, drama.StepChapterShots)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := promptNow(); got.Prompt != golden {
		t.Fatalf("种子版本的提示词应与黄金文件相同：\n%s", firstDiff(golden, got.Prompt))
	}
	_, versions, err := svc.AdminFlowPackageVersions(admin, "yz-chapter-shots")
	if err != nil || len(versions) != 1 {
		t.Fatalf("版本：%+v %v", versions, err)
	}
	original := versions[0]

	data := chapterShotsPackageZip(t, func(files map[string][]byte) {
		files["SKILL.md"] = []byte(strings.Replace(string(files["SKILL.md"]), "你是这一章的导演。", "你是这一章的总导演。", 1))
		bumpManifest(t, files, func(m *drama.StepPackageManifest) { m.Version = "2.2.2" })
	})
	uploaded, err := svc.UploadFlowPackage(admin, packageFileHeader(t, data), "", DramaStepEvalModel{})
	if err != nil {
		t.Fatal(err)
	}
	if uploaded.Version.Active || uploaded.Version.VersionLabel != "2.2.2" || uploaded.Package.VersionCount != 2 || uploaded.Version.Eval != nil || uploaded.EvalError == "" {
		t.Fatalf("上传结果：%+v", uploaded)
	}
	if got := promptNow(); got.Prompt != golden {
		t.Fatal("未启用的版本不该影响提示词")
	}

	if _, err := svc.ActivateFlowPackageVersion(admin, "yz-chapter-shots", uploaded.Version.ID, FlowPackageActivateRequest{}); err != nil {
		t.Fatal(err)
	}
	changed := promptNow()
	if changed.Usage.Skill.Version != "2.2.2" || changed.Usage.Skill.VersionID != uploaded.Version.ID || changed.Prompt != strings.Replace(strings.Replace(golden, "你是这一章的导演。", "你是这一章的总导演。", 1), `version="2.2.1"`, `version="2.2.2"`, 1) {
		t.Fatalf("启用后提示词应只多这处改动和版本号：\n%s", firstDiff(golden, changed.Prompt))
	}

	if _, err := svc.ActivateFlowPackageVersion(admin, "yz-chapter-shots", original.ID, FlowPackageActivateRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := promptNow(); got.Prompt != golden || got.Usage.Skill.Version != "2.2.1" {
		t.Fatalf("回退后应恢复原样：\n%s", firstDiff(golden, got.Prompt))
	}

	for name, c := range map[string]struct {
		edit func(map[string][]byte)
		rule string
	}{
		"缺卡": {func(files map[string][]byte) {
			delete(files, "cards/情绪弧索引.md")
			bumpManifest(t, files, func(m *drama.StepPackageManifest) {
				m.Version = "2.2.3"
				m.Files = []string{"SKILL.md", "cards/相机四维编码.md", "cards/镜头表规则速查.md"}
			})
		}, drama.PackageRuleRequiredCard},
		"契约版本不符": {func(files map[string][]byte) {
			bumpManifest(t, files, func(m *drama.StepPackageManifest) { m.Version = "2.2.3"; m.Contract = "chapter.shots@3" })
		}, drama.PackageRuleContract},
		"超出预算": {func(files map[string][]byte) {
			files["cards/情绪弧索引.md"] = []byte(strings.Repeat("弧", 30000))
			bumpManifest(t, files, func(m *drama.StepPackageManifest) { m.Version = "2.2.3" })
		}, drama.PackageRuleBudget},
	} {
		_, err := svc.UploadFlowPackage(admin, packageFileHeader(t, chapterShotsPackageZip(t, c.edit)), "", DramaStepEvalModel{})
		var appErr *AppError
		if !errors.As(err, &appErr) || appErr.Reason != ReasonPackageCheckFailed {
			t.Fatalf("%s：应拒绝，得到 %v", name, err)
		}
		problems, _ := appErr.Details["problems"].([]drama.StepPackageProblem)
		if len(problems) != 1 || problems[0].Rule != c.rule {
			t.Fatalf("%s：原因 %+v", name, problems)
		}
	}
	if _, versions, _ = svc.AdminFlowPackageVersions(admin, "yz-chapter-shots"); len(versions) != 2 {
		t.Fatalf("坏包不落版本：%d", len(versions))
	}
}
