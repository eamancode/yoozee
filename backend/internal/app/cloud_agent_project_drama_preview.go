package app

import (
	"fmt"
	"sort"
	"strings"

	"yingce/backend/internal/repository"
)

func cloudAgentPreviewProjectDrama(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (cloudAgentApprovalPreview, error) {
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return cloudAgentApprovalPreview{}, err
	}
	project, err := repo.ProjectForUser(userID, canvas.ProjectID)
	if err != nil {
		return cloudAgentApprovalPreview{}, err
	}
	var args map[string]any
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return cloudAgentApprovalPreview{}, cloudAgentJSONArgumentError(err)
	}
	if missing := cloudAgentMissingRequiredArguments(cloudAgentProjectDramaSchemas(), call); len(missing) > 0 {
		return cloudAgentApprovalPreview{}, &cloudAgentFieldArgumentError{error: &cloudAgentArgumentError{BadAuthRequest("缺少必填参数")}, Field: strings.Join(missing, ","), Issue: "required"}
	}
	actions := map[string]string{
		"drama_brief_write": "更新需求文档", "drama_bible_write": "更新连戏表", "drama_segments_write": "保存分段", "drama_shotlist_write": "写入镜头表",
		"drama_artifact_register": "登记产物", "drama_qa_transcribe": "转录检查", "drama_qa_frames": "提取画面", "drama_evidence_write": "登记质检证据",
		"drama_take_pick": "选择候选", "drama_gate_check": "检查交付门禁", "drama_delivery_package": "生成交付清单",
	}
	action := actions[call.Function.Name]
	if action == "" {
		return cloudAgentApprovalPreview{}, BadAuthRequest("工具不属于短剧项目写入")
	}
	fields := make([]string, 0, len(args))
	for key := range args {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	return cloudAgentApprovalPreview{
		Kind: "project_drama", Title: action,
		Description: fmt.Sprintf("将在短剧项目「%s」%s，确认后按本次参数执行。", project.Name, action),
		Items:       []cloudAgentApprovalPreviewItem{{Operation: call.Function.Name, Fields: fields, Summary: action}},
	}, nil
}
