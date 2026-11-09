package skills

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
)

const ontologySkillID = "yz-ontology-director"

func TestOntologyImportChecksums(t *testing.T) {
	raw, err := fs.ReadFile(builtinSkillFiles, ontologySkillID+".SHA256SUMS")
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
		content, err := fs.ReadFile(builtinSkillFiles, ontologySkillID+"/"+p)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(content)) != sum {
			t.Fatalf("embedded source mismatch: %s (%v)", p, err)
		}
	}
	if len(checksums) != 37 {
		t.Fatalf("source count = %d, want 37", len(checksums))
	}
	// Only these integration files may be added to the byte-preserved import.
	added := map[string]bool{"agents/catalog.yaml": true, "agents/interface.yaml": true, "sources.json": true, "runtime-context.json": true, "package.json": true}
	count := 0
	err = fs.WalkDir(builtinSkillFiles, ontologySkillID, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, ontologySkillID+"/")
		if checksums[rel] == "" && !added[rel] {
			t.Errorf("unexpected imported file: %s", rel)
		}
		count++
		return nil
	})
	if err != nil || count != len(checksums)+len(added) {
		t.Fatalf("import file count = %d: %v", count, err)
	}
	t.Run("external-source", func(t *testing.T) {
		source := os.Getenv("ONTOLOGY_DIRECTOR_SOURCE_DIR")
		if source == "" {
			source = `E:\skills研究\分镜制作skills\本体论导演方案v3_20261108\重构\seedance25-ontology-director`
		}
		if _, err := os.Stat(source); os.IsNotExist(err) {
			t.Skip("external source directory absent (CI has no E: drive); embedded SHA-256 verification still runs")
		} else if err != nil {
			t.Fatal(err)
		}
		count := 0
		err := filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(source, p)
			if err != nil {
				return err
			}
			content, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if fmt.Sprintf("%x", sha256.Sum256(content)) != checksums[filepath.ToSlash(rel)] {
				t.Errorf("external source mismatch: %s", rel)
			}
			count++
			return nil
		})
		if err != nil || count != len(checksums) {
			t.Fatalf("external source count = %d, imported source count = %d: %v", count, len(checksums), err)
		}
	})
}

func TestOntologyBuiltinSeedAndCatalog(t *testing.T) {
	s, _, admin := newBuiltinVersionTestService(t)
	for i := 0; i < 2; i++ {
		if err := s.EnsureBuiltinSkills(); err != nil {
			t.Fatal(err)
		}
	}
	skill, err := s.repo.Skill(ontologySkillID)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "本体论导演分镜" || skill.VersionLabel != "2.2" || skill.FileCount != 42 {
		t.Fatalf("catalog = %+v", skill)
	}
	summary, versions, err := s.AdminFlowPackageVersions(admin, ontologySkillID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Check.Status != FlowPackageCheckPassed || summary.Contract != drama.ContractOntologyDirector || summary.StepContract != drama.ContractOntologyDirector {
		t.Fatalf("seed not installed exactly once: %+v %+v", summary, versions)
	}
	pkg, err := s.StepSkillPackage(ontologySkillID)
	if err != nil || pkg.Version != "2.2" || len(pkg.Files) != 42 {
		t.Fatalf("version snapshot = %+v: %v", pkg, err)
	}
	version, err := s.repo.SkillVersion(skill.CurrentVersionID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.versionContents(version)
	if err != nil {
		t.Fatal(err)
	}
	setManifest(t, files, func(m *drama.StepPackageManifest) { m.Version = "2.2.1" })
	uploaded, err := s.uploadFlowPackage(admin.ID, buildPackageZip(t, files, nil), "catalog sidecar round trip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivateFlowPackageVersion(admin, ontologySkillID, uploaded.Version.ID); err != nil {
		t.Fatal(err)
	}
	skill, err = s.repo.Skill(ontologySkillID)
	if err != nil || skill.Name != "本体论导演分镜" || skill.VersionLabel != "2.2.1" {
		t.Fatalf("catalog after activation = %+v: %v", skill, err)
	}
}
