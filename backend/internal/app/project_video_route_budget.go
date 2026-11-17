package app

import (
	"context"
	"maps"
	"slices"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const projectVideoApprovedCostKey = "projectVideoApprovedCostMicroCNY"

// The persisted step omits the pending predecessor frame. Restore the same
// priced slot while validating the quote inside the controller's write lock.
func (s *Service) checkVideoChainQuoteRevision(tx *repository.Repository, userID, projectID string, state videoChainState) error {
	if err := tx.ValidateProjectVideoRouteRevision(projectID, state.RouteRevision); err != nil {
		return err
	}
	for _, step := range state.Steps {
		meta, _ := step.Input["metadata"].(map[string]any)
		if step.Status == "succeeded" || meta[projectVideoApprovedCostKey] == nil {
			continue
		}
		input := maps.Clone(step.Input)
		if step.ReferenceTag != "" && !step.ReferenceSkipped {
			images, _ := input["referenceImages"].([]any)
			input["referenceImages"] = append(slices.Clone(images), videoChainPendingPosition())
		}
		if err := s.checkConfirmedVideoRoute(context.Background(), tx, userID, projectID, input); err != nil {
			return err
		}
	}
	return nil
}

func videoChainPendingPosition() map[string]any {
	return map[string]any{"id": "pending-position", "role": "reference", "type": "image/png", "mimeType": "image/png"}
}

// The auto-route budget applies to the SKU this shot will actually submit,
// not to every SKU exposed under the selected catalog model.
func (s *Service) projectVideoAutoCost(ctx context.Context, repo *repository.Repository, userID string, input map[string]any, route *model.ProjectVideoRoute) (*VideoUpstreamCost, error) {
	c, _ := input["config"].(map[string]any)
	channel, key := stringValue(c["channelId"]), stringValue(c["channelModelKey"])
	if channel+"::"+key != route.Model {
		return nil, failedPrecondition("项目自动线路已变化，请重新报价")
	}
	tier, err := repo.SLLCatalog().EligibleTier(ctx, channel, stringValue(c["priceTierId"]))
	if err != nil {
		return nil, err
	}
	if tier == nil || tier.ModelKey != key || tier.ProviderModelKey != stringValue(c["providerModelKey"]) {
		return nil, failedPrecondition("当前自动线路价档已失效，或无法核实上游每条成本不超过 6 元，请重新选择并报价")
	}
	// Fresh visibility is important across service replicas. Pricing the
	// resolved request below can reuse this one operation's group snapshot.
	checker := &Service{repo: repo, pluginRuntime: s.pluginRuntime, dataDir: s.dataDir}
	access, err := checker.userGroupAccess(userID)
	if err != nil {
		return nil, err
	}
	meta, _ := input["metadata"].(map[string]any)
	artifact, _ := meta["artifactMetadata"].(map[string]any)
	reason, err := checker.projectVideoOfferMismatch(userID, access, input, numberValue(artifact["contentDurationSeconds"], 0), *tier, true)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, failedPrecondition("自动线路无法承载本段规格，请重新报价：" + reason)
	}
	return &VideoUpstreamCost{Currency: "CNY", Unit: "request", AmountMicros: tier.CostMicroCNY}, nil
}

// Recheck against the current group (without the catalog's visibility cache).
// tx is the same transaction that reserves the child's credits at admission.
func (s *Service) checkConfirmedVideoRoute(ctx context.Context, tx *repository.Repository, userID, projectID string, input map[string]any) error {
	route, err := tx.ProjectVideoRoute(projectID)
	if err != nil {
		return err
	}
	meta, _ := input["metadata"].(map[string]any)
	if projectVideoRoutingRevision(route) != int64(numberValue(meta["projectVideoRouteRevision"], 0)) {
		return failedPrecondition("本项目的线路已变化，请重新报价")
	}
	if route == nil || route.Status != "auto" {
		return nil
	}
	if err := tx.LockVideoQuoteGroup(userID); err != nil {
		return err
	}
	cost, err := s.projectVideoAutoCost(ctx, tx, userID, input, route)
	if err != nil {
		return err
	}
	if numberValue(meta[projectVideoApprovedCostKey], -1) != float64(cost.AmountMicros) {
		return failedPrecondition("已确认的自动线路上游人民币成本发生变化，请重新报价")
	}
	return nil
}
