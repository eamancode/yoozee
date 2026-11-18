package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (s *Service) ProjectVideoRouting(userID, projectID string) (*model.ProjectVideoRoute, error) {
	if _, err := s.repo.WorkbenchProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	return s.repo.ProjectVideoRoute(projectID)
}

func projectVideoRoutingRevision(route *model.ProjectVideoRoute) int64 {
	if route == nil {
		return 0
	}
	return route.Revision
}

func (s *Service) OverrideProjectVideoRouting(userID, projectID, selected, from string, revision int64) (*model.ProjectVideoRoute, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	channel, key, ok := strings.Cut(selected, "::")
	if !ok || channel == "" || key == "" || len(selected) > 500 || len(from) > 500 || revision < 0 {
		return nil, BadAuthRequest("请选择有效的视频线路")
	}
	access, err := s.userGroupAccess(userID)
	if err != nil {
		return nil, err
	}
	_, item, err := s.selectableSystemChannelModel(access, map[string]any{"channelId": channel, "model": key})
	if err != nil {
		return nil, err
	}
	if normalizeCapability(item.Capability) != "video" {
		return nil, BadAuthRequest("所选模型不支持视频")
	}
	state := &model.ProjectVideoRoute{ProjectID: projectID, Model: selected, FromModel: from, Status: "manual", Reason: "用户为本项目选择了视频线路", Revision: revision + 1, ChangedAt: time.Now().UTC()}
	err = s.repo.Transaction(func(tx *repository.Repository) error {
		project, err := tx.LockWorkbenchStoryboardProject(userID, projectID)
		if err != nil {
			return err
		}
		if project.Status == model.ProjectStatusArchived {
			return failedPrecondition("项目已归档，无法更改视频线路")
		}
		old, err := tx.ProjectVideoRoute(projectID)
		if err != nil {
			return err
		}
		if projectVideoRoutingRevision(old) != revision {
			return NewAppError(http.StatusConflict, "项目线路已经变化，请刷新后重新选择")
		}
		if old != nil {
			if old.Model == selected && old.Status == "manual" {
				state = old
				return nil
			}
			state.FromModel = old.Model
		}
		return tx.SaveProjectVideoRoute(state)
	})
	return state, err
}

var errNeedVideoCandidates = errors.New("video route ranking required")

// The receipt, consecutive count and selection commit together. Ranking only runs
// at the threshold and outside the write lock. The second transaction rechecks
// all preconditions, including the route revision accepted with the original job.
func (s *Service) recordProjectVideoTerminal(child *model.Task) error {
	if child.Type != "canvas_video" || child.ProjectID == "" || child.Status != model.TaskStatusSucceeded && child.Status != model.TaskStatusFailed {
		return nil
	}
	raw, err := s.decryptTaskInputJSON(child.InputJSON)
	if err != nil {
		return err
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return err
	}
	if target, shot := dramaTaskTarget(input); target != child.ProjectID || shot == "" {
		return nil
	}
	meta, _ := input["metadata"].(map[string]any)
	cfg, _ := input["config"].(map[string]any)
	art, _ := meta["artifactMetadata"].(map[string]any)
	project, err := s.repo.WorkbenchProjectForUser(child.UserID, child.ProjectID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if project.Type != "short-drama" {
		return nil
	}
	d, err := s.videoChainAttemptDiagnostic(child)
	if err != nil {
		return err
	}
	if d.CountDecision != "increment" && d.CountDecision != "reset" {
		return nil
	}
	if d.ChannelID == "" {
		d.ChannelID = stringValue(cfg["channelId"])
	}
	key := firstNonEmpty(stringValue(cfg["channelModelKey"]), stringValue(cfg["model"]))
	if d.UpstreamModel == "" {
		d.UpstreamModel = firstNonEmpty(stringValue(cfg["providerModelKey"]), key)
	}
	if d.ChannelID == "" || key == "" || d.UpstreamModel == "" {
		return nil
	}
	active := d.ChannelID + "::" + key
	var candidates []projectVideoRouteCandidate
	var reason string
	ranked := false
	now := time.Now().UTC()
	event := model.ProjectVideoRouteEvent{
		TaskID: child.ID, RouteRun: child.RouteRun, ProjectID: project.ID,
		ChannelID: d.ChannelID, ProviderModelKey: d.UpstreamModel, Decision: d.CountDecision,
		RouteRevision: int64(numberValue(meta["projectVideoRouteRevision"], 0)), CreatedAt: now,
	}
	apply := func(tx *repository.Repository) error {
		if _, err := tx.LockActiveStoryboardProject(child.UserID, project.ID); err != nil {
			return err
		}
		count, fresh, err := tx.ApplyProjectVideoRouteEvent(&event)
		if err != nil || !fresh || count != 3 {
			return err
		}
		old, err := tx.ProjectVideoRoute(project.ID)
		if err != nil {
			return err
		}
		revision := projectVideoRoutingRevision(old)
		if old != nil && old.Model != active {
			return nil
		}
		if captured, ok := meta["projectVideoRouteRevision"]; ok && int64(numberValue(captured, -1)) != revision {
			return nil
		}
		if !ranked {
			return errNeedVideoCandidates
		}
		next := &model.ProjectVideoRoute{ProjectID: project.ID, FromModel: active, Model: active, ChangedAt: now, Revision: revision + 1, Status: "unavailable", Reason: "本项目该线路连续三次渠道故障。" + reason}
		if len(candidates) > 0 && candidates[0].Rate != nil {
			best := candidates[0]
			next.Model, next.SuccessRate, next.SampledAt, next.Status = best.ChannelID+"::"+best.ModelKey, best.Rate, &best.CollectedAt, "auto"
			next.Reason = "本项目原线路连续三次渠道故障，已按本段规格选择成功率最高的可用线路"
		}
		return tx.SaveProjectVideoRoute(next)
	}
	err = s.repo.Transaction(apply)
	if !errors.Is(err, errNeedVideoCandidates) {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	candidates, reason, err = s.projectVideoRouteCandidates(ctx, child.UserID, project.ID, d.ChannelID, d.UpstreamModel, input, numberValue(art["contentDurationSeconds"], 0), now)
	if err != nil {
		return err
	}
	ranked = true
	return s.repo.Transaction(apply)
}
