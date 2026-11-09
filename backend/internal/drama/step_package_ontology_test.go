package drama

import "testing"

func TestOntologyPackageAndLegacyBudget(t *testing.T) {
	files := readPackageDir(t, "../../builtin/yz-ontology-director")
	if problems := checkStepPackageForTest(files[StepPackageManifestFile], files); len(problems) > 0 {
		t.Fatalf("ontology package: %+v", problems)
	}
	step, _ := StepByID(StepChapterShots)
	m, _ := ParseStepPackageManifest(files[StepPackageManifestFile])
	legacy := checkPackageBudget(step, string(files["SKILL.md"]), m.Files, files)
	if len(legacy) == 0 {
		t.Fatal("expected legacy single-turn budget to reject full entry references")
	}
	for _, problem := range legacy {
		t.Logf("legacy limit (not used for staged contract): [%s] %s", problem.Rule, problem.Message)
	}
	// Importing the package does not switch existing steps or their default binding.
	if step.Contract != ContractChapterShots || step.DefaultSkill != "yz-chapter-shots" || step.SkillBudgetChars != 32000 || step.SkillLinkedFiles != 3 {
		t.Fatalf("legacy step changed: %+v", step)
	}
}
