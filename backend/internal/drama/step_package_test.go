package drama

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const stepPackageRulesPath = "contracts/step-packages.json"

// build.py 读这份导出在本地跑同一套静态校验；步骤表或规则改了而没重新生成时这里失败（go test ./internal/drama -run TestStepPackageRulesExported -update）。
func TestStepPackageRulesExported(t *testing.T) {
	raw, err := json.MarshalIndent(StepPackageRules(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want := string(raw) + "\n"
	if *updateGenerated {
		if err := os.WriteFile(stepPackageRulesPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(stepPackageRulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s 与步骤表不一致，带 -update 重新生成", stepPackageRulesPath)
	}
}

type stepPackageCase struct {
	Name     string                     `json:"name"`
	Manifest json.RawMessage            `json:"manifest"`
	Files    map[string]json.RawMessage `json:"files"`
	Expect   []string                   `json:"expect"`
}

// checkStepPackageForTest 是上传时的顺序：清单不合格就不往下查。
func checkStepPackageForTest(manifest []byte, files map[string][]byte) []StepPackageProblem {
	m, problems := ParseStepPackageManifest(manifest)
	if len(problems) > 0 {
		return problems
	}
	return CheckStepPackage(m, files)
}

// 与 yoozee-skills/tests/test_package.py 共用样例：两边对同一个包给出同样的规则编号。
func TestStepPackageCases(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(skillsTestDir, "packages", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []stepPackageCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			files := map[string][]byte{}
			for p, v := range c.Files {
				var text string
				if err := json.Unmarshal(v, &text); err != nil {
					var rep struct {
						Repeat string `json:"repeat"`
						Times  int    `json:"times"`
					}
					if err := json.Unmarshal(v, &rep); err != nil {
						t.Fatal(err)
					}
					text = strings.Repeat(rep.Repeat, rep.Times)
				}
				files[p] = []byte(text)
			}
			problems := checkStepPackageForTest(c.Manifest, files)
			var rules []string
			for _, p := range problems {
				rules = append(rules, p.Rule)
			}
			slices.Sort(rules)
			want := slices.Clone(c.Expect)
			slices.Sort(want)
			if !slices.Equal(rules, want) {
				t.Fatalf("规则 %v，期望 %v：%+v", rules, want, problems)
			}
		})
	}
}

// 本地仓库的 yz-chapter-shots 包要能过校验，且和内置种子 backend/builtin/yz-chapter-shots 逐字一致（以本地仓库为准，build.py 同步）。
func TestChapterShotsPackageMatchesBuiltinSeed(t *testing.T) {
	local := readPackageDir(t, "../../../yoozee-skills/packages/yz-chapter-shots")
	seed := readPackageDir(t, "../../builtin/yz-chapter-shots")
	if len(local) != len(seed) {
		t.Fatalf("文件数不一致：本地 %d，内置 %d", len(local), len(seed))
	}
	for p, content := range local {
		if string(seed[p]) != string(content) {
			t.Fatalf("%s 与内置种子不一致，跑 python yoozee-skills/build.py 同步", p)
		}
	}
	if problems := checkStepPackageForTest(local[StepPackageManifestFile], local); len(problems) > 0 {
		t.Fatalf("yz-chapter-shots 包没过校验：%+v", problems)
	}
	m, _ := ParseStepPackageManifest(local[StepPackageManifestFile])
	if m.ID != "yz-chapter-shots" || m.Step != StepChapterShots || m.Contract != ContractChapterShots || !slices.Equal(m.Profiles, BuiltinProfileIDs()) {
		t.Fatalf("清单：%+v", m)
	}
}

func readPackageDir(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = content
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
