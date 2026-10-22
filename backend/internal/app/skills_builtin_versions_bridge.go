package app

import (
	"yingce/backend/internal/model"
	"yingce/backend/internal/skills"
)

// 内置技能在线覆盖（M5）：后台编辑内置技能生成在线版本，优先于内置包。

type (
	BuiltinSkillSummary        = skills.BuiltinSkillSummary
	BuiltinSkillVersionItem    = skills.BuiltinSkillVersionItem
	BuiltinSkillVersionRequest = skills.BuiltinSkillVersionRequest
	BuiltinSkillFileEdit       = skills.BuiltinSkillFileEdit
)

func (s *Service) AdminBuiltinSkills(actor *model.User) ([]BuiltinSkillSummary, error) {
	return s.skillDomain().AdminBuiltinSkills(actor)
}

func (s *Service) AdminBuiltinSkillVersions(actor *model.User, skillID string) (*BuiltinSkillSummary, []BuiltinSkillVersionItem, error) {
	return s.skillDomain().AdminBuiltinSkillVersions(actor, skillID)
}

func (s *Service) AdminBuiltinSkillVersionFiles(actor *model.User, skillID string, versionID string) ([]SkillPackageFileItem, error) {
	return s.skillDomain().AdminBuiltinSkillVersionFiles(actor, skillID, versionID)
}

func (s *Service) AdminBuiltinSkillVersionFile(actor *model.User, skillID string, versionID string, filePath string) (*SkillPackageFileContent, error) {
	return s.skillDomain().AdminBuiltinSkillVersionFile(actor, skillID, versionID, filePath)
}

func (s *Service) CreateBuiltinSkillVersion(actor *model.User, skillID string, req BuiltinSkillVersionRequest) (*BuiltinSkillSummary, *BuiltinSkillVersionItem, error) {
	return s.skillDomain().CreateBuiltinSkillVersion(actor, skillID, req)
}

func (s *Service) ActivateBuiltinSkillVersion(actor *model.User, skillID string, versionID string) (*BuiltinSkillSummary, error) {
	return s.skillDomain().ActivateBuiltinSkillVersion(actor, skillID, versionID)
}

func (s *Service) RevertBuiltinSkill(actor *model.User, skillID string) (*BuiltinSkillSummary, error) {
	return s.skillDomain().RevertBuiltinSkill(actor, skillID)
}
