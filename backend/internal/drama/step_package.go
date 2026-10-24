package drama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// 流程技能包（M6 第二期）：技能目录加清单 package.json，本地 build.py --package 构建成 zip，管理员上传后先过静态校验。
// 校验规则只有这一份；yoozee-skills/build.py 读 contracts/step-packages.json（由 StepPackageRules 生成）在本地跑同一套。
// 版本号是否重复要查库，在 skills 域里做。

const (
	// StepPackageManifestFile 是包的清单文件。
	StepPackageManifestFile = "package.json"
	// StepPackageHashFile 是 build.py 写进 zip 的内容哈希；上传时核对后丢弃，不进版本文件。
	StepPackageHashFile = "package.hash.json"
)

// 静态校验的规则编号（前端按它分组显示，build.py 用同一套）。
const (
	PackageRuleManifest     = "manifest"
	PackageRuleStep         = "step"
	PackageRuleContract     = "contract"
	PackageRuleProfiles     = "profiles"
	PackageRuleFiles        = "files"
	PackageRuleRequiredCard = "required_card"
	PackageRuleBudget       = "budget"
	PackageRuleVersion      = "version"
	PackageRuleHash         = "hash"
	PackageRuleEntry        = "entry"
	// PackageRuleEval 是清单 eval 点名了这一步评测集里没有的章节。
	PackageRuleEval = "eval"
	// PackageRuleArchive 是 zip 本身读不出来（不是 zip、路径越界、超过大小、缺 SKILL.md）。
	PackageRuleArchive = "archive"
)

// StepPackageManifest 是 package.json：一个包只服务一个步骤，可以覆盖多个风格档案；契约由步骤提供，包只声明满足哪个版本。
type StepPackageManifest struct {
	ID       string   `json:"id"`
	Step     string   `json:"step"`
	Contract string   `json:"contract"`
	Profiles []string `json:"profiles"`
	Version  string   `json:"version"`
	// Files 是包里的全部文件（不含 package.json 自己）；SKILL.md 必须在内。
	Files []string `json:"files"`
	// Eval 是除包覆盖的档案自带的评测章节外，另外要跑的章节（都要在步骤的评测集里）。
	Eval []string `json:"eval"`
}

// StepPackageProblem 是一条校验不过的原因。
type StepPackageProblem struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

// StepPackageRuleSet 是静态校验用到的线上数据，导出给 build.py。
type StepPackageRuleSet struct {
	ManifestFile    string                  `json:"manifestFile"`
	HashFile        string                  `json:"hashFile"`
	Steps           []StepPackageStepLimits `json:"steps"`
	Profiles        []string                `json:"profiles"`
	RequiredReading string                  `json:"requiredReading"`
	CardReference   string                  `json:"cardReference"`
	TextExtensions  []string                `json:"textExtensions"`
	IDPattern       string                  `json:"idPattern"`
	VersionPattern  string                  `json:"versionPattern"`
	ContractPattern string                  `json:"contractPattern"`
}

type StepPackageStepLimits struct {
	ID               string            `json:"id"`
	Contract         string            `json:"contract"`
	SkillBudgetChars int               `json:"skillBudgetChars"`
	SkillLinkedFiles int               `json:"skillLinkedFiles"`
	EvalSet          []StepEvalChapter `json:"evalSet"`
}

var (
	packageIDPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,35}$`)
	packageVersionPattern  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
	packageContractPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*@[0-9]+$`)
	// skillCardReference 是 SKILL.md 里用反引号写出的文本文件路径（点名的卡）。
	skillCardReference = regexp.MustCompile("`([^`\\s]+\\.(?:md|mdx|txt|json|yaml|yml|toml|csv))`")
)

// StepPackageRules 是导出给 build.py 的规则数据（contracts/step-packages.json）。
func StepPackageRules() StepPackageRuleSet {
	out := StepPackageRuleSet{
		ManifestFile: StepPackageManifestFile, HashFile: StepPackageHashFile,
		Profiles: BuiltinProfileIDs(), RequiredReading: skillRequiredReading.String(), CardReference: skillCardReference.String(),
		TextExtensions: slices.Clone(skillTextExtensions), IDPattern: packageIDPattern.String(), VersionPattern: packageVersionPattern.String(),
		ContractPattern: packageContractPattern.String(),
	}
	for _, s := range steps {
		out.Steps = append(out.Steps, StepPackageStepLimits{ID: s.ID, Contract: s.Contract, SkillBudgetChars: s.SkillBudgetChars, SkillLinkedFiles: s.SkillLinkedFiles, EvalSet: slices.Clone(s.EvalSet)})
	}
	return out
}

// ParseStepPackageManifest 读 package.json：字段齐全、类型对、没有多余字段。
func ParseStepPackageManifest(raw []byte) (StepPackageManifest, []StepPackageProblem) {
	var m StepPackageManifest
	problem := func(format string, args ...any) []StepPackageProblem {
		return []StepPackageProblem{{Rule: PackageRuleManifest, Path: StepPackageManifestFile, Message: fmt.Sprintf(format, args...)}}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return m, problem("包里没有 %s", StepPackageManifestFile)
	}
	if !utf8.Valid(raw) {
		return m, problem("%s 不是 UTF-8", StepPackageManifestFile)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, problem("%s 不是合法的清单：%v", StepPackageManifestFile, err)
	}
	if decoder.More() {
		return m, problem("%s 只能有一个 JSON 对象", StepPackageManifestFile)
	}
	var out []StepPackageProblem
	add := func(format string, args ...any) { out = append(out, problem(format, args...)...) }
	if !packageIDPattern.MatchString(m.ID) {
		add("id「%s」只能是小写字母、数字和连字符，1～36 个字符", m.ID)
	}
	if strings.TrimSpace(m.Step) == "" {
		add("缺少 step")
	}
	if !packageContractPattern.MatchString(m.Contract) {
		add("contract「%s」要写成「步骤@版本号」，如 %s", m.Contract, ContractChapterShots)
	}
	if !packageVersionPattern.MatchString(m.Version) {
		add("version「%s」只能是字母、数字和 . + -，最长 64 个字符", m.Version)
	}
	if len(m.Profiles) == 0 {
		add("profiles 至少写一个风格档案")
	}
	if dup := firstDuplicate(m.Profiles); dup != "" {
		add("profiles 里「%s」重复", dup)
	}
	if len(m.Files) == 0 {
		add("files 不能为空")
	}
	for _, f := range m.Files {
		if !cleanPackagePath(f) {
			add("files 里的「%s」不是包内的相对路径", f)
		} else if f == StepPackageManifestFile || f == StepPackageHashFile {
			add("files 不用列 %s", f)
		}
	}
	if dup := firstDuplicate(m.Files); dup != "" {
		add("files 里「%s」重复", dup)
	}
	if len(m.Files) > 0 && !slices.Contains(m.Files, "SKILL.md") {
		add("files 里没有 SKILL.md")
	}
	for _, e := range m.Eval {
		if strings.TrimSpace(e) == "" {
			add("eval 里有空的章节 ID")
			break
		}
	}
	return m, out
}

// CheckStepPackage 对照线上步骤表查一个包：step 存在、契约版本一致、风格档案已知、files 与包内文件一一对应、
// eval 点名的章节在这一步的评测集里、SKILL.md 点名必读的卡都在 files 里、注入总字数不超过这一步的上限。files 是包里的文件（package.json 可有可无，不算进 files）。
func CheckStepPackage(m StepPackageManifest, files map[string][]byte) []StepPackageProblem {
	var out []StepPackageProblem
	add := func(rule, p, format string, args ...any) {
		out = append(out, StepPackageProblem{Rule: rule, Path: p, Message: fmt.Sprintf(format, args...)})
	}
	step, stepOK := StepByID(m.Step)
	if !stepOK {
		known := make([]string, 0, len(steps))
		for _, s := range steps {
			known = append(known, s.ID)
		}
		add(PackageRuleStep, "", "step「%s」不在线上步骤表里（现有：%s）", m.Step, strings.Join(known, "、"))
	} else if m.Contract != step.Contract {
		add(PackageRuleContract, "", "清单声明满足契约 %s，线上「%s」这一步的契约是 %s", m.Contract, step.Label, step.Contract)
	}
	for _, id := range m.Profiles {
		if !ValidProfileID(id) {
			add(PackageRuleProfiles, "", "风格档案「%s」不存在（已知：%s）", id, strings.Join(BuiltinProfileIDs(), "、"))
		}
	}
	if stepOK {
		for _, id := range m.Eval {
			if _, ok := step.EvalChapter(id); !ok {
				known := make([]string, 0, len(step.EvalSet))
				for _, c := range step.EvalSet {
					known = append(known, c.ID)
				}
				add(PackageRuleEval, "", "eval 里的章节「%s」不在「%s」这一步的评测集里（现有：%s）", id, step.Label, strings.Join(known, "、"))
			}
		}
	}
	listed := map[string]bool{}
	for _, f := range m.Files {
		listed[f] = true
		if _, ok := files[f]; !ok {
			add(PackageRuleFiles, f, "files 列了 %s，包里没有这个文件", f)
		}
	}
	for _, p := range sortedKeys(files) {
		if p != StepPackageManifestFile && p != StepPackageHashFile && !listed[p] {
			add(PackageRuleFiles, p, "包里的 %s 没写进 files", p)
		}
	}
	entryRaw, ok := files["SKILL.md"]
	if !ok || !listed["SKILL.md"] {
		return out
	}
	if !utf8.Valid(entryRaw) {
		add(PackageRuleEntry, "SKILL.md", "SKILL.md 不是 UTF-8 文本")
		return out
	}
	entry := string(entryRaw)
	for _, ref := range missingRequiredCards(entry, listed) {
		add(PackageRuleRequiredCard, ref, "SKILL.md 点名必读 %s，但 files 里没有", ref)
	}
	if stepOK {
		out = append(out, checkPackageBudget(step, entry, m.Files, files)...)
	}
	return out
}

// missingRequiredCards 是 SKILL.md 用「必须先读取」之类的话点名、却不在 files 里的卡。
func missingRequiredCards(entry string, listed map[string]bool) []string {
	var out []string
	for _, line := range strings.Split(entry, "\n") {
		for _, match := range skillCardReference.FindAllStringSubmatch(line, -1) {
			ref := strings.TrimPrefix(match[1], "./")
			if listed[ref] || slices.Contains(out, ref) || !skillRequiredReading.MatchString(ref+" "+line) {
				continue
			}
			out = append(out, ref)
		}
	}
	return out
}

type packageCard struct {
	path     string
	chars    int
	index    int
	required bool
}

// checkPackageBudget 估这一步最多注入多少字：SKILL.md 全文，加必读的卡，再按最坏情况用其余点名的卡里最长的补满可读张数。
// 与运行时 StepSkillPrompt 同一套点名规则；超出时运行时会截断，所以在上传时就拒绝。
func checkPackageBudget(step Step, entry string, listedFiles []string, files map[string][]byte) []StepPackageProblem {
	var cards []packageCard
	for _, p := range listedFiles {
		content, ok := files[p]
		if !ok || p == "SKILL.md" || !linkedSkillTextFile(p, "") || !utf8.Valid(content) {
			continue
		}
		index := strings.Index(entry, p)
		if index < 0 {
			continue
		}
		lineStart := strings.LastIndex(entry[:index], "\n") + 1
		line := entry[lineStart:]
		if lineEnd := strings.Index(entry[index:], "\n"); lineEnd >= 0 {
			line = entry[lineStart : index+lineEnd]
		}
		cards = append(cards, packageCard{path: p, chars: jsLen(string(content)), index: index, required: skillRequiredReading.MatchString(p + " " + line)})
	}
	slices.SortStableFunc(cards, func(a, b packageCard) int {
		if a.required != b.required {
			if a.required {
				return -1
			}
			return 1
		}
		if a.required {
			return a.index - b.index
		}
		return b.chars - a.chars
	})
	var out []StepPackageProblem
	required := 0
	for _, c := range cards {
		if c.required {
			required++
		}
	}
	if required > step.SkillLinkedFiles {
		out = append(out, StepPackageProblem{Rule: PackageRuleBudget, Message: fmt.Sprintf("SKILL.md 点名必读 %d 张卡，「%s」这一步最多注入 %d 张，多出的读不到", required, step.Label, step.SkillLinkedFiles)})
	}
	if len(cards) > step.SkillLinkedFiles {
		cards = cards[:step.SkillLinkedFiles]
	}
	total := jsLen(entry)
	parts := []string{fmt.Sprintf("SKILL.md %d 字", total)}
	for _, c := range cards {
		total += c.chars
		parts = append(parts, fmt.Sprintf("%s %d 字", c.path, c.chars))
	}
	if total > step.SkillBudgetChars {
		out = append(out, StepPackageProblem{Rule: PackageRuleBudget, Message: fmt.Sprintf("最多注入 %d 字（%s），超过「%s」这一步的上限 %d 字，超出部分会被截断", total, strings.Join(parts, " + "), step.Label, step.SkillBudgetChars)})
	}
	return out
}

func cleanPackagePath(p string) bool {
	if p == "" || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") {
		return false
	}
	clean := path.Clean(p)
	return clean == p && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func firstDuplicate(values []string) string {
	seen := map[string]bool{}
	for _, v := range values {
		if seen[v] {
			return v
		}
		seen[v] = true
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
