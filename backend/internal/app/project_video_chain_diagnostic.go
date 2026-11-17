package app

import (
	"encoding/json"
	"strconv"
	"strings"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// A failed local task is not proof that its provider did not accept a job.
// Keep the incident classification separate from permission to submit again.
type videoChainAttemptDiagnostic struct {
	AttemptID         string `json:"attemptId,omitempty"`
	QuoteID           string `json:"quoteId,omitempty"`
	ChannelID         string `json:"channelId,omitempty"`
	UpstreamModel     string `json:"upstreamModel,omitempty"`
	Phase             string `json:"phase"`
	TerminalStatus    string `json:"terminalStatus"`
	SubmissionOutcome string `json:"submissionOutcome"`
	BillingStatus     string `json:"billingStatus"`
	Category          string `json:"category"`
	UpstreamCode      string `json:"upstreamCode,omitempty"`
	CountDecision     string `json:"countDecision"`
	SafeToResubmit    bool   `json:"safeToResubmit"`
	CanQueryProvider  bool   `json:"canQueryProvider"`
	CanRecoverMedia   bool   `json:"canRecoverMedia"`
	originalRequestID string
}

func (s *Service) videoChainAttemptDiagnostic(child *model.Task) (videoChainAttemptDiagnostic, error) {
	return videoChainDiagnosticFromRepo(s.repo, child)
}

func videoChainDiagnosticFromRepo(repo *repository.Repository, child *model.Task) (videoChainAttemptDiagnostic, error) {
	d := videoChainAttemptDiagnostic{Phase: "execution", TerminalStatus: string(child.Status), Category: "unknown",
		CountDecision: "ignore", SubmissionOutcome: "unknown", BillingStatus: "none"}
	// The actual model can differ from both the public name and channel model
	// key (e.g. a price tier overrides it). Read only identity fields.
	var input struct {
		Chain struct {
			PreviewHash string `json:"previewHash"`
		} `json:"_projectVideoChain"`
		Config struct {
			ChannelID        string `json:"channelId"`
			ProviderModelKey string `json:"providerModelKey"`
			Model            string `json:"model"`
		} `json:"config"`
	}
	if json.Unmarshal([]byte(child.InputJSON), &input) == nil {
		d.QuoteID = input.Chain.PreviewHash
		d.ChannelID = input.Config.ChannelID
		d.UpstreamModel = firstNonEmpty(input.Config.ProviderModelKey, input.Config.Model)
	}
	attempts, err := repo.RouteAttempts(child.ID, child.RouteRun)
	if err != nil {
		return d, err
	}
	var attempt model.RouteAttempt
	if len(attempts) > 0 {
		attempt = attempts[len(attempts)-1]
		d.AttemptID = attempt.ID
		d.ChannelID = firstNonEmpty(attempt.ChannelID, d.ChannelID)
	}
	providerID := firstNonEmpty(child.ProviderRequestID, attempt.ProviderRequestID)
	if child.BillingOrderID != "" {
		order, err := repo.BillingOrder(child.BillingOrderID)
		if err != nil {
			return d, err
		}
		if order.UserID != child.UserID || order.TaskID != child.ID {
			return d, BadAuthRequest("原请求与计费订单归属不一致")
		}
		d.BillingStatus = string(order.Status)
		d.ChannelID = firstNonEmpty(d.ChannelID, order.ChannelID)
		providerID = firstNonEmpty(providerID, order.ProviderRequestID)
	}
	// Earlier retries reused the same local child ID. A receipt from an earlier
	// generation is not evidence about this attempt.
	if providerID == "" && child.Status == model.TaskStatusFailed && (child.RouteRun <= 1 || !attempt.StartedAt.IsZero() || child.BillingOrderID != "") {
		id, err := repo.VideoChainProviderRequestID(child.UserID, child.ID, child.BillingOrderID, attempt.StartedAt)
		if err != nil {
			return d, err
		}
		providerID = id
	}
	d.originalRequestID = providerID
	d.classify(child, attempt, providerID)
	return d, nil
}

func (d *videoChainAttemptDiagnostic) classify(child *model.Task, attempt model.RouteAttempt, providerID string) {
	facts := taskExecutionDiagnostic(child)
	if facts != nil {
		d.Phase = facts.Phase
		switch facts.SubmissionOutcome {
		case "accepted", "not_submitted":
			d.SubmissionOutcome = facts.SubmissionOutcome
		}
	}
	switch attempt.DispatchState {
	case "accepted":
		d.SubmissionOutcome = "accepted"
	case "rejected_no_job", "not_sent":
		if d.SubmissionOutcome != "accepted" {
			d.SubmissionOutcome = "not_submitted"
		}
	case "submission_unknown":
		// An older receipt can have an unknown dispatch state even when
		// the execution diagnostic records a completed provider call.
		if d.SubmissionOutcome != "accepted" {
			d.SubmissionOutcome = "unknown"
		}
	}
	if providerID != "" || child.MediaRecoveryJSON != "" || child.Status == model.TaskStatusSucceeded {
		d.SubmissionOutcome = "accepted"
	}
	d.CanRecoverMedia = child.Status == model.TaskStatusFailed && child.MediaRecoveryJSON != "" && d.BillingStatus != "refunded"
	d.CanQueryProvider = child.Status == model.TaskStatusFailed && child.MediaRecoveryJSON == "" && providerID != ""
	code := 0
	if value, ok := strings.CutPrefix(attempt.FailureCode, "upstream_"); ok {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 400 && parsed <= 599 {
			code = parsed
			d.UpstreamCode = "HTTP " + strconv.Itoa(code)
		}
	}
	message := strings.ToLower(attempt.FailureMessage + " " + child.Error)
	has := func(markers ...string) bool {
		for _, marker := range markers {
			if strings.Contains(message, marker) {
				return true
			}
		}
		return false
	}
	terminalQuery := providerID != "" && (child.PollStage == "provider_failed" || child.PollStage == "provider_cancelled")
	switch {
	case child.Status == model.TaskStatusSucceeded:
		d.Category, d.CountDecision = "succeeded", "reset"
	case child.Status == model.TaskStatusCancelled || child.PollStage == "provider_cancelled":
		d.Category = "cancelled"
	case child.Status != model.TaskStatusFailed:
		return
	case child.MediaRecoveryJSON != "" || d.Phase == "result_persistence":
		d.Category = "result_persistence"
	case isContentModerationFailure(message) || has("内容审核未通过", "安全审核", "违反安全策略", "输入素材疑似包含真人形象", "sensitivecontentdetected", "content policy"):
		d.Category = "content_rejected"
	case code == 402 || has("余额不足", "模型服务额度不足", "insufficient balance"):
		d.Category = "insufficient_balance"
	case code == 400 || code == 422 || (facts != nil && facts.Code == "invalid_argument") || has("模型服务拒绝了请求，请检查模型和参数"):
		d.Category = "invalid_input"
	case d.Phase == "preparation":
		d.Category = "preflight"
	case code == 503 && videoRouteUnavailableReceipt(attempt.FailureMessage):
		d.Category = "route_unavailable"
		if d.SubmissionOutcome != "accepted" || terminalQuery {
			d.CountDecision = "increment"
		}
	case terminalQuery:
		d.Category, d.CountDecision = "provider_failed", "increment"
	case code == 504 || code == 524 || code == 408 || (facts != nil && facts.Code == "timeout") || has("任务执行超时", "视频生成等待超时", "响应超时", "context deadline exceeded"):
		d.Category = "timeout"
	case code >= 500 && code <= 599:
		d.Category = "upstream_service"
		if d.SubmissionOutcome == "not_submitted" {
			d.CountDecision = "increment"
		}
	}
	reconciledBilling := d.BillingStatus == "none" || d.BillingStatus == "refunded" || d.BillingStatus == "settled"
	// Only the original create's explicit no-route receipt is evidence of
	// rejection. Local refunds alone and HTTP 503 alone prove no such thing.
	rejected := d.SubmissionOutcome == "not_submitted" || (d.Category == "route_unavailable" && d.SubmissionOutcome == "unknown")
	d.SafeToResubmit = child.Status == model.TaskStatusFailed && reconciledBilling &&
		d.Category != "result_persistence" && (rejected || terminalQuery)
}

func videoRouteUnavailableReceipt(message string) bool {
	message = strings.ToLower(message)
	for _, marker := range []string{"无可用渠道", "没有可用渠道", "没有可用路由", "no available channel", "no available route"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
