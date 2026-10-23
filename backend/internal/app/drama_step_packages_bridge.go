package app

import (
	"mime/multipart"

	"yingce/backend/internal/model"
	"yingce/backend/internal/skills"
)

// 流程技能包（M6 第二期）：管理员上传 build.py --package 构建的 zip，静态校验通过存成新版本，手动启用、回退。

type (
	FlowPackageSummary      = skills.FlowPackageSummary
	FlowPackageVersionItem  = skills.FlowPackageVersionItem
	FlowPackageCheck        = skills.FlowPackageCheck
	FlowPackageUploadResult = skills.FlowPackageUploadResult
)

func (s *Service) AdminFlowPackages(actor *model.User) ([]FlowPackageSummary, error) {
	return s.skillDomain().AdminFlowPackages(actor)
}

func (s *Service) AdminFlowPackageVersions(actor *model.User, skillID string) (*FlowPackageSummary, []FlowPackageVersionItem, error) {
	return s.skillDomain().AdminFlowPackageVersions(actor, skillID)
}

func (s *Service) UploadFlowPackage(actor *model.User, header *multipart.FileHeader, note string) (*FlowPackageUploadResult, error) {
	return s.skillDomain().UploadFlowPackage(actor, header, note)
}

func (s *Service) ActivateFlowPackageVersion(actor *model.User, skillID string, versionID string) (*FlowPackageSummary, error) {
	return s.skillDomain().ActivateFlowPackageVersion(actor, skillID, versionID)
}
