package app

import (
	"regexp"

	"yingce/backend/internal/model"
)

// taskListErrorLimit 是任务列表里失败原因的长度上限（字）：够写清「第几轮、为什么」，长篇上游正文不进列表。
const taskListErrorLimit = 300

// 不带标记名、直接出现在上游错误正文里的凭据：OpenAI 风格 sk- 密钥、Google API Key、Bearer 令牌、JWT。
var taskErrorBareSecret = regexp.MustCompile(`(?i)\bbearer\s+[^\s"',;]+|\bsk-[a-z0-9_\-]{6,}|\bAIza[0-9a-z_\-]{10,}|\beyJ[a-z0-9_\-]{8,}\.[a-z0-9_\-]{8,}\.[a-z0-9_\-]{4,}`)

// taskListError 是任务列表（TaskSummary.error）里失败任务的原因：先按画布 / Agent 同一套分类转成用户能看懂的话
// （userFacingTaskError：审核、存储、网络、鉴权、5xx 归类，供应商可读原因保留，上游地址、任务标识隐藏），
// 再去掉带标记的凭据（authorization、cookie、x-goog-api-key、api_key…）、URL 和裸露的密钥 / 令牌，截到 300 字。
// 不是失败状态、没有原因时为空；原始错误只在任务详情的执行诊断里给有权限的人看。
func taskListError(task model.Task) string {
	if task.Status != model.TaskStatusFailed || task.Error == "" {
		return ""
	}
	return truncateRunes(taskErrorBareSecret.ReplaceAllString(redactDiagnosticText(userFacingTaskError(task.Error), 4*taskListErrorLimit), "[REDACTED]"), taskListErrorLimit)
}
