package app

import (
	"strings"
	"yingce/backend/internal/drama"
)

func (s *Service) assetWorkflowSkill(workflow drama.AssetWorkflow, versionID string) (drama.StepSkill, error) {
	var selected drama.StepSkill
	if versionID == "" {
		current, err := s.skillDomain().StepSkillPackage(workflow.AssetsSkill)
		if err != nil {
			return selected, err
		}
		selected = stepSkillOf(current)
	} else {
		frozen, err := s.skillDomain().StepSkillPackageVersion(workflow.AssetsSkill, versionID)
		if err != nil {
			return selected, err
		}
		selected = stepSkillOf(frozen)
	}
	for _, file := range selected.Files {
		if file.Path != drama.StepPackageManifestFile {
			continue
		}
		manifest, problems := drama.ParseStepPackageManifest([]byte(file.Content))
		if len(problems) == 0 && manifest.Step == drama.StepAssetsExtract && manifest.Contract == workflow.AssetsContract {
			return selected, nil
		}
		break
	}
	return drama.StepSkill{}, BadAuthRequest("已启用的资产技能包与项目工作流契约不符")
}

func (s *Service) assetWorkflowRecipe(workflow drama.AssetWorkflow, metadata map[string]any, name string) (string, error) {
	versionID, _ := metadata["assetWorkflowSkillVersionId"].(string)
	skill, err := s.assetWorkflowSkill(workflow, versionID)
	if err != nil {
		return "", err
	}
	path := "recipes/" + name + ".txt"
	for _, file := range skill.Files {
		if file.Path != path || file.Binary || strings.TrimSpace(file.Content) == "" {
			continue
		}
		metadata["assetWorkflowSkill"] = map[string]string{"skillId": skill.ID, "versionId": skill.VersionID, "version": skill.Version, "contentHash": skill.ContentHash, "recipe": path, "recipeHash": file.SHA256}
		return strings.TrimSpace(file.Content), nil
	}
	return "", BadAuthRequest("工作流技能包缺少图片规格：" + path)
}
