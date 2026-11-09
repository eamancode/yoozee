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
