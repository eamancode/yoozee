package drama

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// 三个戏类的分镜技能包（yoozee-skills/tools/slice_section_skills.py 生成）：本地包与内置种子逐字一致、能过静态校验、
// 必读卡全部注入且不超过 4 万字预算；每段「原文」标记里的文字与资产库源文件对应行逐字相同。武戏两个包按风格档案分：
// 仿真人档案用精简切片，其余用完整切片（合起来覆盖五个档案）。
var sectionSkillPackages = []struct {
	id, step, contract, source, sha string
	profiles                        []string
	cards                           int
}{
	{"yz-shots-wenxi15", StepChapterShots + "#" + ShotGenreWenxi15, "section.wenxi15@1", "分镜-文戏15/7.3.json", "72564e7c041b69d82d7c4425ca61faaf05697681d5be13dca522ce7af07b7fa2", BuiltinProfileIDs(), 4},
	{"yz-shots-wenxi30", StepChapterShots + "#" + ShotGenreWenxi30, "section.wenxi30@1", "分镜-文戏30/即梦剧本-分镜_优化版.txt", "4dfad8e3029150eb14397ca000358d60025d49b7354769154425c4d1ddada707", BuiltinProfileIDs(), 4},
	{"yz-shots-wuxi", StepChapterShots + "#" + ShotGenreWuxi, "section.wuxi@1", "分镜-武戏/skill-complete-v7.md", "0bb7fe6f626a2ae83084b51b5ef12857411e61a5f606a3f116e73f0b0b0c9fb1", []string{"action-epic", "anime-3d", "game-ip"}, 4},
	{"yz-shots-wuxi-live", StepChapterShots + "#" + ShotGenreWuxi, "section.wuxi@1", "分镜-武戏/skill-complete-v7.md", "0bb7fe6f626a2ae83084b51b5ef12857411e61a5f606a3f116e73f0b0b0c9fb1", []string{"duanju-shiga", "live-realism"}, 3},
}

var sectionSkillExcerpt = regexp.MustCompile(`(?s)<!-- 原文：(\S+) 第 (\d+)(?:～(\d+))? 行[^\n]*-->\n(.*?)\n<!-- 原文结束 -->`)

func TestSectionSkillPackages(t *testing.T) {
	for _, pkg := range sectionSkillPackages {
		local := readPackageDir(t, "../../../yoozee-skills/packages/"+pkg.id)
		seed := readPackageDir(t, "../../builtin/"+pkg.id)
		if len(local) != len(seed) {
			t.Fatalf("%s 文件数不一致：本地 %d，内置 %d", pkg.id, len(local), len(seed))
		}
		for p, content := range local {
			if string(seed[p]) != string(content) {
				t.Fatalf("%s/%s 与内置种子不一致", pkg.id, p)
			}
		}
		if problems := checkStepPackageForTest(local[StepPackageManifestFile], local); len(problems) > 0 {
			t.Fatalf("%s 没过校验：%+v", pkg.id, problems)
		}
		m, _ := ParseStepPackageManifest(local[StepPackageManifestFile])
		if m.ID != pkg.id || m.Step != pkg.step || m.Contract != pkg.contract || !slices.Equal(m.Profiles, pkg.profiles) {
			t.Fatalf("清单：%+v", m)
		}
		skill := StepSkill{ID: pkg.id}
		for _, p := range m.Files {
			skill.Files = append(skill.Files, StepSkillFile{Path: p, Kind: "markdown", Content: string(local[p])})
		}
		prompt, files, ok := StepSkillPrompt(skill, "章节：第一集", SectionSkillBudgetChars, SectionSkillLinkedFiles)
		if !ok || len(files) != pkg.cards+1 || strings.Contains(prompt, "已在此处截断") || jsLen(prompt) > SectionSkillBudgetChars {
			t.Fatalf("%s 应整份注入 SKILL.md 与 %d 张卡：%d 个文件，%d 字，截断=%v", pkg.id, pkg.cards, len(files), jsLen(prompt), strings.Contains(prompt, "已在此处截断"))
		}
		if !strings.Contains(string(local["SKILL.md"]), "## 柚子流程接入说明") {
			t.Fatalf("%s 的 SKILL.md 没有接入说明", pkg.id)
		}
		for p, content := range local {
			if p == StepPackageManifestFile {
				continue
			}
			if len(sectionSkillExcerpt.FindAllSubmatch(content, -1)) == 0 {
				t.Fatalf("%s/%s 没有原文片段", pkg.id, p)
			}
		}
	}
}

// 原文逐字对照：资产库不在本机（CI）时跳过；本机设 YOOZEE_ASSETS_DIR 或用默认的 E:/AI影视/资产。
func TestSectionSkillExcerptsVerbatim(t *testing.T) {
	dir := os.Getenv("YOOZEE_ASSETS_DIR")
	if dir == "" {
		dir = "E:/AI影视/资产"
	}
	for _, pkg := range sectionSkillPackages {
		raw, err := os.ReadFile(filepath.Join(dir, pkg.source))
		if err != nil {
			t.Skipf("资产库原文不在本机（%v），跳过逐字对照", err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != pkg.sha {
			t.Fatalf("%s 的 SHA-256 与卡 0 记录的不一致，原文变了", pkg.source)
		}
		lines := strings.Split(string(raw), "\n")
		name := filepath.Base(pkg.source)
		local := readPackageDir(t, "../../../yoozee-skills/packages/"+pkg.id)
		n := 0
		for p, content := range local {
			for _, m := range sectionSkillExcerpt.FindAllStringSubmatch(string(content), -1) {
				a, _ := strconv.Atoi(m[2])
				b := a
				if m[3] != "" {
					b, _ = strconv.Atoi(m[3])
				}
				if m[1] != name || a < 1 || b > len(lines) || a > b {
					t.Fatalf("%s/%s 的片段标记不对：%s 第 %d～%d 行", pkg.id, p, m[1], a, b)
				}
				if want := strings.Join(lines[a-1:b], "\n"); m[4] != want {
					t.Fatalf("%s/%s 第 %d～%d 行与原文不一致", pkg.id, p, a, b)
				}
				n++
			}
		}
		if n == 0 {
			t.Fatalf("%s 一段原文都没有", pkg.id)
		}
	}
}
