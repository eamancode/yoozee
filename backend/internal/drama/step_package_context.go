package drama

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// StagedPackageContext is an allowlist for T3's stage loader, not an execution plan.
// Each stage lists its complete context, including SKILL.md and params.md.
type StagedPackageContext struct {
	SchemaVersion int                 `json:"schemaVersion"`
	ExcludedPaths []string            `json:"excludedPaths"`
	Stages        map[string][]string `json:"stages"`
}

func checkStagedPackageContext(step Step, files map[string][]byte) []StepPackageProblem {
	var out []StepPackageProblem
	add := func(rule, message string) {
		out = append(out, StepPackageProblem{Rule: rule, Path: StepPackageContextFile, Message: message})
	}
	var context StagedPackageContext
	if err := json.Unmarshal(files[StepPackageContextFile], &context); err != nil || context.SchemaVersion != 1 {
		add(PackageRuleManifest, "runtime-context.json 必须是 schemaVersion=1 的分环节装载清单")
		return out
	}
	if context.ExcludedPaths == nil || context.Stages == nil {
		add(PackageRuleManifest, "runtime-context.json 的 excludedPaths / stages 类型无效")
		return out
	}
	for _, prefix := range []string{"archive/", "dist/", "tests/"} {
		if !slices.Contains(context.ExcludedPaths, prefix) {
			add(PackageRuleFiles, "分环节装载清单必须排除 "+prefix)
		}
	}
	if len(context.Stages) != 9 {
		add(PackageRuleFiles, "分环节装载清单必须且只能包含 S0–S8")
	}
	for i := 0; i <= 8; i++ {
		id := fmt.Sprintf("S%d", i)
		paths := context.Stages[id]
		if !slices.Contains(paths, "SKILL.md") || !slices.Contains(paths, "params.md") {
			add(PackageRuleFiles, id+" 必须装载 SKILL.md 和 params.md")
		}
		if firstDuplicate(paths) != "" {
			add(PackageRuleFiles, id+" 装载清单包含重复文件")
		}
		total, stageCount := 0, 0
		for _, p := range paths {
			if strings.HasPrefix(p, "stages/"+id+"_") && strings.HasSuffix(p, ".md") {
				stageCount++
			}
			content, exists := files[p]
			if !cleanPackagePath(p) || !exists || !utf8.Valid(content) || !linkedSkillTextFile(p, "") {
				add(PackageRuleFiles, id+" 装载文件不存在或不是有效文本："+p)
				continue
			}
			for _, prefix := range context.ExcludedPaths {
				if strings.HasPrefix(p, prefix) {
					add(PackageRuleFiles, id+" 不得装载排除路径："+p)
				}
			}
			total += jsLen(string(content))
		}
		if stageCount != 1 {
			add(PackageRuleFiles, id+" 必须装载对应的 stages/"+id+" 文件")
		}
		if len(paths)-1 > step.SkillLinkedFiles || total > step.SkillBudgetChars {
			add(PackageRuleBudget, fmt.Sprintf("%s 装载 %d 字、%d 张关联卡；上限 %d 字、%d 张，不得截断", id, total, len(paths)-1, step.SkillBudgetChars, step.SkillLinkedFiles))
		}
	}
	return out
}

// sssMaxHeuristicsPerUnit 是一个单元最多整份装几个启发库文件（库号）。
const sssMaxHeuristicsPerUnit = 4

// checkSSSPackageContext 校验 SSS 导演分镜包的分轮装载清单：八种轮次齐全、每轮都装 SKILL.md 和 参数.md、文件存在且是文本、
// 不装排除目录（范例/），每轮字数和文件数不超过上限（不截断）；启发库目录和总索引存在，每单元最多装几个库有上限。
func checkSSSPackageContext(files map[string][]byte) []StepPackageProblem {
	var out []StepPackageProblem
	add := func(rule, message string) {
		out = append(out, StepPackageProblem{Rule: rule, Path: StepPackageContextFile, Message: message})
	}
	var rc SSSRuntimeContext
	if err := json.Unmarshal(files[StepPackageContextFile], &rc); err != nil || rc.SchemaVersion != 1 || rc.Stages == nil || rc.ExcludedPaths == nil {
		add(PackageRuleManifest, "runtime-context.json 必须是 schemaVersion=1 的分轮装载清单（excludedPaths、heuristics、stages）")
		return out
	}
	if !slices.Contains(rc.ExcludedPaths, "范例/") {
		add(PackageRuleFiles, "分轮装载清单必须排除 范例/")
	}
	h := rc.Heuristics
	if h.Dir == "" || h.MaxPerUnit <= 0 || h.MaxPerUnit > sssMaxHeuristicsPerUnit || !strings.HasPrefix(h.Index, h.Dir) {
		add(PackageRuleManifest, "runtime-context.json 的 heuristics 需要 dir、dir 下的 index，maxPerUnit 在 1–4 之间")
	} else if _, ok := files[h.Index]; !ok {
		add(PackageRuleFiles, "启发库总索引不存在："+h.Index)
	}
	if len(rc.Stages) != len(SSSTurnKinds) {
		add(PackageRuleFiles, "分轮装载清单必须且只能包含 "+strings.Join(SSSTurnKinds, "、"))
	}
	for _, kind := range SSSTurnKinds {
		paths, ok := rc.Stages[kind]
		if !ok {
			add(PackageRuleFiles, "分轮装载清单缺少轮次 "+kind)
			continue
		}
		if !slices.Contains(paths, "SKILL.md") || !slices.Contains(paths, "参数.md") {
			add(PackageRuleFiles, kind+" 必须装载 SKILL.md 和 参数.md")
		}
		if firstDuplicate(paths) != "" {
			add(PackageRuleFiles, kind+" 装载清单包含重复文件")
		}
		total := 0
		for _, p := range paths {
			content, exists := files[p]
			if !cleanPackagePath(p) || !exists || !utf8.Valid(content) || !linkedSkillTextFile(p, "") {
				add(PackageRuleFiles, kind+" 装载文件不存在或不是有效文本："+p)
				continue
			}
			for _, prefix := range rc.ExcludedPaths {
				if strings.HasPrefix(p, prefix) {
					add(PackageRuleFiles, kind+" 不得装载排除路径："+p)
				}
			}
			total += jsLen(string(content))
		}
		if len(paths)-1 > SSSStageMaxFiles || total > SSSStageBudgetChars {
			add(PackageRuleBudget, fmt.Sprintf("%s 装载 %d 字、%d 张关联卡；上限 %d 字、%d 张，不得截断", kind, total, len(paths)-1, SSSStageBudgetChars, SSSStageMaxFiles))
		}
	}
	return out
}
