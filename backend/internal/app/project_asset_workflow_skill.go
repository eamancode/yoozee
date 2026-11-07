package app

import (
	"strings"
	"yingce/backend/internal/drama"
)

func (s *Service) assetWorkflowSkill(workflow drama.AssetWorkflow) (drama.StepSkill, error) {
	current, err := s.skillDomain().StepSkillPackage(workflow.AssetsSkill)
	if err != nil {
		return drama.StepSkill{}, err
	}
	if current.Version == workflow.AssetsVersion {
		return stepSkillOf(current), nil
	}
	versions, err := s.repo.SkillVersions(workflow.AssetsSkill)
	if err != nil {
		return drama.StepSkill{}, err
	}
	for _, version := range versions {
		if version.VersionLabel != workflow.AssetsVersion {
			continue
		}
		pkg, err := s.skillDomain().StepSkillPackageVersion(workflow.AssetsSkill, version.ID)
		if err != nil {
			return drama.StepSkill{}, err
		}
		return stepSkillOf(pkg), nil
	}
	return drama.StepSkill{}, BadAuthRequest("项目工作流指定的技能版本不可用，请检查流程技能包")
}

func (s *Service) assetWorkflowRecipe(workflow drama.AssetWorkflow, metadata map[string]any, name string) (string, error) {
	skill, err := s.assetWorkflowSkill(workflow)
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
