package skills

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
)

// 两个 SSS 导演技能逐字节导入：清单里每个文件的 SHA-256 与嵌入的一致，包里只多出柚子接入用的五个文件；
// 本机有 E: 盘源目录时再逐个比对原文件（Go embed 不收全角括号的文件名，sources.json 记了改名）。
func TestSSSImportChecksums(t *testing.T) {
	sources := map[string]string{
		"yz-sss-film-director":  `E:\skills研究\分镜制作skills\sss电影导演分镜`,
		"yz-sss-drama-director": `E:\skills研究\分镜制作skills\sss短剧导演分镜`,
	}
	for id, source := range sources {
		raw, err := fs.ReadFile(builtinSkillFiles, id+".SHA256SUMS")
		if err != nil {
			t.Fatal(err)
		}
		checksums := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			sum, p, ok := strings.Cut(line, "  ")
			if !ok || len(sum) != 64 || checksums[p] != "" {
				t.Fatalf("invalid or duplicate checksum: %q", line)
			}
			checksums[p] = sum
			content, err := fs.ReadFile(builtinSkillFiles, id+"/"+p)
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(content)) != sum {
				t.Fatalf("%s embedded source mismatch: %s (%v)", id, p, err)
			}
		}
		if len(checksums) != 53 {
			t.Fatalf("%s source count = %d, want 53", id, len(checksums))
		}
		added := map[string]bool{"agents/catalog.yaml": true, "agents/interface.yaml": true, "sources.json": true, "runtime-context.json": true, "package.json": true}
		count := 0
		if err := fs.WalkDir(builtinSkillFiles, id, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel := strings.TrimPrefix(p, id+"/")
			if checksums[rel] == "" && !added[rel] {
				t.Errorf("%s unexpected imported file: %s", id, rel)
			}
			count++
			return nil
		}); err != nil || count != len(checksums)+len(added) {
			t.Fatalf("%s import file count = %d: %v", id, count, err)
		}
		var meta struct {
			PathRenames map[string]string `json:"pathRenames"`
		}
		rawMeta, _ := fs.ReadFile(builtinSkillFiles, id+"/sources.json")
		if err := json.Unmarshal(rawMeta, &meta); err != nil || len(meta.PathRenames) != 2 {
			t.Fatalf("%s sources.json renames = %v %v", id, meta.PathRenames, err)
		}
		t.Run(id+"/external-source", func(t *testing.T) {
			if _, err := os.Stat(source); os.IsNotExist(err) {
				t.Skip("external source directory absent (CI has no E: drive); embedded SHA-256 verification still runs")
			} else if err != nil {
				t.Fatal(err)
			}
			seen := 0
			err := filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, err := filepath.Rel(source, p)
				if err != nil {
					return err
				}
				rel = filepath.ToSlash(rel)
				if renamed, ok := meta.PathRenames[rel]; ok {
					rel = renamed
				}
				content, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				if fmt.Sprintf("%x", sha256.Sum256(content)) != checksums[rel] {
					t.Errorf("external source mismatch: %s", rel)
				}
				seen++
				return nil
			})
			if err != nil || seen != len(checksums) {
				t.Fatalf("external source count = %d, imported = %d: %v", seen, len(checksums), err)
			}
		})
	}
}

func TestSSSBuiltinSeedAndCatalog(t *testing.T) {
	s, _, admin := newBuiltinVersionTestService(t)
	if err := s.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]struct {
		name     string
		contract string
		files    int
	}{
		"yz-sss-film-director":  {"SSS 电影导演分镜", drama.ContractSSSDirector, 58},
		"yz-sss-drama-director": {"SSS 短剧导演分镜", drama.ContractSSSDirector, 58},
		"yz-sss-film-script":    {"yoozee · SSS 电影拆剧本（立意、全集结构与分集剧本）", drama.ContractSSSScriptAdapt, 2},
		"yz-sss-drama-script":   {"yoozee · SSS 短剧拆剧本（立意、全集结构与分集剧本）", drama.ContractSSSScriptAdapt, 2},
		"yz-sss-drama-style":    {"yoozee · SSS 短剧导演定画风（世界基准段与视听总谱）", drama.ContractStyleLock, 2},
	} {
		skill, err := s.repo.Skill(id)
		if err != nil {
			t.Fatal(id, err)
		}
		version := "1.0.0"
		if want.contract == drama.ContractSSSScriptAdapt {
			version = "1.1.0"
		}
		if skill.Name != want.name || skill.VersionLabel != version || skill.FileCount != want.files {
			t.Fatalf("%s catalog = %q %q %d", id, skill.Name, skill.VersionLabel, skill.FileCount)
		}
		summary, versions, err := s.AdminFlowPackageVersions(admin, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(versions) != 1 || versions[0].Check.Status != FlowPackageCheckPassed || summary.Contract != want.contract || summary.StepContract != want.contract {
			t.Fatalf("%s seed = %+v %+v", id, summary, versions)
		}
	}
}
