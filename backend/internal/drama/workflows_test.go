package drama

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegratedBuiltinPackageAndFullScriptContext(t *testing.T) {
	base := filepath.Join("..", "..", "builtin", IntegratedAssetsSkill)
	raw, err := os.ReadFile(filepath.Join(base, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, problems := ParseStepPackageManifest(raw)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	files := map[string][]byte{}
	skill := StepSkill{ID: IntegratedAssetsSkill}
	for _, path := range manifest.Files {
		content, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		files[path] = content
		skill.Files = append(skill.Files, StepSkillFile{Path: path, Content: string(content)})
	}
	if problems := CheckStepPackage(manifest, files); len(problems) != 0 {
		t.Fatal(problems)
	}
	script, err := os.ReadFile("testdata/suixingxian.txt")
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildAssetsExtractPrompt(AssetsExtractInput{Contract: ContractIntegratedAssets, SourceText: string(script), Skill: skill})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"references/extraction-core.md", "references/character-design.md", "references/scene-spatial.md"} {
		found := false
		for _, used := range prompt.Files {
			if used.Path == path {
				found = true
			}
		}
		if !found {
			t.Fatalf("required skill module omitted: %s", path)
		}
	}
	if !strings.Contains(prompt.Text, "39.9") || !strings.Contains(prompt.Text, "generation_prompt") {
		t.Fatal("script or protected contract truncated")
	}
}

func TestAssetWorkflowsSeparateLayoutsAndContracts(t *testing.T) {
	for _, id := range []string{WorkflowSSSFilm, WorkflowSSSDrama, WorkflowOntology, WorkflowIntegrated, WorkflowOriginal} {
		workflow, ok := Workflow(id)
		if !ok {
			t.Fatal(id)
		}
		contract, err := BuiltinContract(workflow.AssetsContract)
		if err != nil {
			t.Fatal(err)
		}
		text := contract.Text(contract.DefaultRequirement(), ContractTextOptions{SkillShaped: true})
		if IntegratedAssets(id) {
			if workflow.CharacterLayout != "four-panel" || workflow.SceneLayout != "spatial-master" || !strings.Contains(text, "generation_prompt") {
				t.Fatal(workflow, text)
			}
		} else if workflow.CharacterLayout != "turnaround" || workflow.AssetsContract != ContractAssetsExtract {
			t.Fatal(workflow)
		}
	}
	if _, ok := Workflow("unknown"); ok {
		t.Fatal("invalid workflow accepted")
	}
}

// v100 起 SSS 电影、SSS 短剧排在最前，本体论、资产一体化、原流程顺延为三、四、五：顺延后的定义（技能、契约、版式）不能变。
func TestWorkflowRenumberPreservesLegacyDefinitions(t *testing.T) {
	integrated, ok := Workflow("pipeline-4")
	if !ok || integrated != (AssetWorkflow{ID: "pipeline-4", Version: "1", Label: "流水线四 资产一体化", AssetsSkill: "yz-assets-integrated", AssetsContract: "assets.extract@2", CharacterLayout: "four-panel", SceneLayout: "spatial-master"}) {
		t.Fatalf("old integrated definition changed: %+v", integrated)
	}
	original, ok := Workflow("pipeline-5")
	if !ok || original != (AssetWorkflow{ID: "pipeline-5", Version: "1", Label: "流水线五 原流程", AssetsContract: "assets.extract@1", CharacterLayout: "turnaround", SceneLayout: "concept"}) {
		t.Fatalf("old original definition changed: %+v", original)
	}
	if empty, ok := Workflow(""); !ok || empty != original {
		t.Fatal("empty workflow must use original", empty)
	}
	ontology, ok := Workflow("pipeline-3")
	if !ok || ontology.StoryboardMode != "ontology" || ontology.StoryboardSkill != "yz-ontology-director" || ontology.Label != "流水线三 本体论导演" {
		t.Fatal("ontology storyboard route missing", ontology)
	}
	ontology.ID, ontology.Label, ontology.StoryboardMode, ontology.StoryboardSkill = integrated.ID, integrated.Label, "", ""
	if ontology != integrated {
		t.Fatal("ontology changed the integrated asset protocol", ontology, integrated)
	}
	if _, ok := Workflow("pipeline-6"); ok {
		t.Fatal("unknown pipeline accepted")
	}
}

// SSS 两条流水线：资产与资产一体化完全一致（四宫格、微缩场景），只换分镜、拆剧本、导演定画风三个技能。
func TestSSSWorkflowsUseIntegratedAssetsAndOwnSkills(t *testing.T) {
	integrated, _ := Workflow(WorkflowIntegrated)
	for id, kind := range map[string]string{"pipeline-1": SSSKindFilm, "pipeline-2": SSSKindDrama} {
		w, ok := Workflow(id)
		if !ok || SSSWorkflowKind(id) != kind {
			t.Fatal(id, w)
		}
		if w.StoryboardMode != StoryboardModeSSS || w.StoryboardSkill != "yz-sss-"+kind+"-director" || w.ScriptSkill != "yz-sss-"+kind+"-script" || w.StyleSkill != "yz-sss-"+kind+"-style" {
			t.Fatalf("%s skills = %+v", id, w)
		}
		w.ID, w.Label, w.StoryboardMode, w.StoryboardSkill, w.ScriptSkill, w.StyleSkill = integrated.ID, integrated.Label, "", "", "", ""
		if w != integrated {
			t.Fatal("SSS workflow changed the integrated asset protocol", id, w)
		}
	}
	if film, _ := Workflow("pipeline-1"); film.Label != "流水线一 SSS 电影导演" {
		t.Fatal(film.Label)
	}
	if drama, _ := Workflow("pipeline-2"); drama.Label != "流水线二 SSS 短剧导演" || DefaultWorkflow != "pipeline-2" {
		t.Fatal(drama.Label, DefaultWorkflow)
	}
	for _, id := range []string{"pipeline-3", "pipeline-4", "pipeline-5", ""} {
		if w, _ := Workflow(id); w.ScriptSkill != "" || w.StyleSkill != "" || SSSWorkflowKind(id) != "" {
			t.Fatal("legacy workflow got SSS skills", id, w)
		}
	}
}

func TestIntegratedAssetsPreserveFullPromptBeyondLegacySummary(t *testing.T) {
	full := strings.Repeat("保持空间几何关系。", 400)
	line, _ := json.Marshal(AssetsExtractRow{Category: AssetEnvironment, Name: "便民市场", Appearance: "铁皮棚半室外", PromptEn: "market", GenerationPrompt: full, FirstScene: 1})
	rows, stats := AssetsExtractFromText(string(line))
	if stats.Invalid != 0 || len(rows) != 1 || rows[0].GenerationPrompt != full {
		t.Fatalf("full prompt lost: %v", stats)
	}
	if len(AssetsPromptEn(rows[0])) > 1200 {
		t.Fatal("legacy summary changed")
	}
}

func TestIntegratedTaskDoesNotInheritThreeViewInstruction(t *testing.T) {
	prompt, err := BuildAssetsExtractPrompt(AssetsExtractInput{Contract: ContractIntegratedAssets, SourceText: "林深站在市场。", Skill: StepSkill{ID: IntegratedAssetsSkill, Files: []StepSkillFile{{Path: "SKILL.md", Content: "# 资产四宫格"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt.Text, "角色是三视图设定图") || !strings.Contains(prompt.Text, "微缩母版") {
		t.Fatal(prompt.Text)
	}
}
