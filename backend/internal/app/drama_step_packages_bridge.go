package app

import (
	"mime/multipart"

	"yingce/backend/internal/model"
	"yingce/backend/internal/skills"
)

// 流程技能包（M6 第二、三期）：管理员上传 build.py --package 构建的 zip，静态校验通过存成新版本，随即在评测集上真跑评测
// （drama_step_eval.go），过门槛自动启用；也能手动启用、回退，评测没通过的要写明原因强制启用。

type (
	FlowPackageSummary      = skills.FlowPackageSummary
	FlowPackageVersionItem  = skills.FlowPackageVersionItem
	FlowPackageCheck        = skills.FlowPackageCheck
	FlowPackageUploadResult = skills.FlowPackageUploadResult
)

func (s *Service) AdminFlowPackages(actor *model.User) ([]FlowPackageSummary, error) {
	return s.skillDomain().AdminFlowPackages(actor)
}

// AdminFlowPackageVersions 是一个包的全部版本，每个版本带静态校验结论和最近一次评测。
func (s *Service) AdminFlowPackageVersions(actor *model.User, skillID string) (*FlowPackageSummary, []FlowPackageVersionView, error) {
	summary, items, err := s.skillDomain().AdminFlowPackageVersions(actor, skillID)
	if err != nil {
		return nil, nil, err
	}
	views, err := s.flowPackageVersionViews(summary.ID, items)
	if err != nil {
		return nil, nil, err
	}
	return summary, views, nil
}

// UploadFlowPackage 收包、静态校验、存成新版本（不启用），再用 sel 选的文本模型开始评测；评测开不起来（没选模型、余额不足等）
// 不影响版本保存，原因写在 EvalError 里，可以在版本列表里重跑。
func (s *Service) UploadFlowPackage(actor *model.User, header *multipart.FileHeader, note string, sel DramaStepEvalModel) (*FlowPackageUploadResponse, error) {
	uploaded, err := s.skillDomain().UploadFlowPackage(actor, header, note)
	if err != nil {
		return nil, err
	}
	out := &FlowPackageUploadResponse{Package: uploaded.Package, Version: FlowPackageVersionView{FlowPackageVersionItem: uploaded.Version}}
	if sel.empty() {
		out.EvalError = "没选评测用的文本模型，版本已保存但没有评测；在版本列表里点「评测」"
		return out, nil
	}
	eval, err := s.StartDramaStepEval(actor, uploaded.Package.ID, uploaded.Version.ID, sel)
	if err != nil {
		out.EvalError = "评测没能开始：" + err.Error()
		return out, nil
	}
	out.Version.Eval = eval
	return out, nil
}
