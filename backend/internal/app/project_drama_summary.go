package app

import (
	"errors"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// dramaSummaryLimit 是一次批量摘要最多读几个项目：列表页按进入视野的卡片分批请求。
const dramaSummaryLimit = 50

// DramaProjectSummary 是项目列表卡片要的进度摘要：流水线、段计数、一句话，以及出图、出片的失败和进行中数。
type DramaProjectSummary struct {
	Pipeline *drama.Pipeline      `json:"pipeline,omitempty"`
	Counts   drama.ProgressCounts `json:"counts"`
	Line     string               `json:"line"`
	// Episodes 是剧集数（kind=episode），Chapters 是原文章节数：卡片上没改编时写「N 章」，改编后写「N 集」，两者不混算。
	Episodes      int `json:"episodes"`
	Chapters      int `json:"chapters"`
	FailedImages  int `json:"failedImages"`
	FailedVideos  int `json:"failedVideos"`
	RunningVideos int `json:"runningVideos"`
}

// DramaSummariesResult 按项目 id 给摘要；不存在或不属于当前用户的 id 放进 Missing，不让一张卡片拖垮整批。
type DramaSummariesResult struct {
	Summaries map[string]DramaProjectSummary `json:"summaries"`
	Missing   []string                       `json:"missing"`
}

func (s *Service) DramaSummaries(userID string, ids []string) (DramaSummariesResult, error) {
	result := DramaSummariesResult{Summaries: map[string]DramaProjectSummary{}, Missing: []string{}}
	seen := map[string]bool{}
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) > dramaSummaryLimit {
		return result, BadAuthRequest("一次最多读 50 个项目的进度")
	}
	for _, id := range unique {
		summary, err := s.dramaProjectSummary(userID, id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result.Missing = append(result.Missing, id)
			continue
		}
		if err != nil {
			return result, err
		}
		result.Summaries[id] = summary
	}
	return result, nil
}

func (s *Service) dramaProjectSummary(userID, projectID string) (DramaProjectSummary, error) {
	progress, err := s.DramaProgress(userID, projectID)
	if err != nil {
		return DramaProjectSummary{}, err
	}
	summary := DramaProjectSummary{Pipeline: progress.Pipeline, Counts: progress.Counts, Line: progress.Line}
	units, err := s.repo.ProjectUnits(projectID)
	if err != nil {
		return summary, err
	}
	for _, unit := range units {
		if unit.Kind == model.ProjectUnitKindEpisode {
			summary.Episodes++
		} else {
			summary.Chapters++
		}
	}

	assets, err := s.projectDramaImageAssets(userID, projectID)
	if err != nil {
		return summary, err
	}
	for i := range assets {
		if s.dramaImageView(userID, &assets[i].asset, assetImageKey, assets[i].imaged).Status == dramaImageFailed {
			summary.FailedImages++
		}
	}

	unitIDs := []string{}
	seenUnit := map[string]bool{}
	for _, segment := range progress.Segments {
		if segment.UnitID != "" && !seenUnit[segment.UnitID] {
			seenUnit[segment.UnitID] = true
			unitIDs = append(unitIDs, segment.UnitID)
		}
	}
	for _, unitID := range unitIDs {
		chains, err := s.ProjectVideoChains(userID, projectID, unitID)
		if err != nil {
			return summary, err
		}
		failed, running := latestVideoStepCounts(chains)
		summary.FailedVideos += failed
		summary.RunningVideos += running
	}
	return summary, nil
}

// latestVideoStepCounts 每段只看最近一次出片（chains 按创建时间倒序）：旧的失败在重试成功后不再算失败；
// 预览、只准备参考的链不算出片。与前端 drama-flow/model/video-steps.ts 的 latestVideoSteps 同一口径。
func latestVideoStepCounts(chains []ProjectVideoChainView) (failed, running int) {
	seen := map[string]bool{}
	for _, chain := range chains {
		if chain.ID == "" || chain.Status == "preview" || chain.PrepareOnly {
			continue
		}
		active := chain.Status == "queued" || chain.Status == "running"
		for _, step := range chain.Steps {
			if seen[step.ShotID] {
				continue
			}
			seen[step.ShotID] = true
			switch {
			case step.Status == "succeeded":
			case step.Status == "failed":
				failed++
			case active:
				running++
			case chain.Status == "failed":
				failed++
			}
		}
	}
	return failed, running
}
