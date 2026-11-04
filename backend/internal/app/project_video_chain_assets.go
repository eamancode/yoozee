package app

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"time"
	"yingce/backend/internal/model"
)

func videoChainFrameAssetID(projectID string, step *videoChainStepState) string {
	return videoChainHash([]string{projectID, step.SourceShotID, step.SourceRevisionID, step.SourceVideoResourceID})[:32]
}

func (s *Service) reuseVideoChainFrame(task *model.Task, step *videoChainStepState) (bool, error) {
	asset, err := s.repo.AssetForUser(task.UserID, videoChainFrameAssetID(task.ProjectID, step))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	version, err := s.repo.AssetVersion(asset.PrimaryVersionID)
	if err != nil {
		return false, err
	}
	var source StoryboardReferenceSource
	if err := json.Unmarshal([]byte(version.DefinitionJSON), &source); err != nil {
		return false, err
	}
	if asset.Category != model.AssetCategoryStoryboardReference || source.SourceShotID != step.SourceShotID || source.SourceRevisionID != step.SourceRevisionID || source.SourceVideoResourceID != step.SourceVideoResourceID {
		return false, failedPrecondition("已有分镜参考的来源不匹配，请检查资产后重试")
	}
	if _, err := s.videoChainReadyResource(task.UserID, source.FrameResourceID, "image/"); err != nil {
		return false, err
	}
	step.FrameResourceID, step.FrameTimeSeconds, step.FrameSelection = source.FrameResourceID, source.FrameTimeSeconds, source.FrameSelection
	return true, nil
}

type StoryboardReferenceSource struct {
	SourceShotTitle       string                              `json:"sourceShotTitle,omitempty"`
	Historical            bool                                `json:"historical"`
	Consumers             []model.StoryboardReferenceConsumer `json:"consumers,omitempty"`
	FrameTimeSeconds      *float64                            `json:"frameTimeSeconds,omitempty"`
	SourceShotID          string                              `json:"sourceShotId"`
	SourceRevisionID      string                              `json:"sourceRevisionId"`
	SourceVideoResourceID string                              `json:"sourceVideoResourceId"`
	SourceVideoTaskID     string                              `json:"sourceVideoTaskId"`
	FrameResourceID       string                              `json:"frameResourceId"`
	ControllerID          string                              `json:"controllerId"`
	FrameSelection        string                              `json:"frameSelection"`
}

func (s *Service) registerVideoChainFrame(task *model.Task, step *videoChainStepState) error {
	source, err := s.repo.ShotForProject(task.ProjectID, step.SourceShotID)
	if err != nil {
		return err
	}
	if source.CurrentRevisionID != step.SourceRevisionID {
		return failedPrecondition("上一组镜头版本已变化，请按最新内容重试")
	}
	id := videoChainFrameAssetID(task.ProjectID, step)
	versionID := videoChainHash([]string{"frame-version", id})[:32]
	now := time.Now()
	lineage := StoryboardReferenceSource{SourceShotTitle: source.Title, FrameTimeSeconds: step.FrameTimeSeconds, SourceShotID: source.ID, SourceRevisionID: step.SourceRevisionID, SourceVideoResourceID: step.SourceVideoResourceID, SourceVideoTaskID: step.SourceVideoTaskID, FrameResourceID: step.FrameResourceID, ControllerID: task.ID, FrameSelection: step.FrameSelection}
	definition, err := json.Marshal(lineage)
	if err != nil {
		return err
	}
	title := source.Title + " · 片末站位参考"
	payload, err := json.Marshal(map[string]any{"id": id, "kind": "image", "category": model.AssetCategoryStoryboardReference, "title": title, "status": "confirmed", "primaryVersionId": versionID, "data": map[string]any{"storageKey": "resource:" + step.FrameResourceID, "source": "video-tail-frame"}, "metadata": map[string]any{"storyboardReference": lineage}})
	if err != nil {
		return err
	}
	asset := model.Asset{ID: id, UserID: task.UserID, Kind: "image", Category: model.AssetCategoryStoryboardReference, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: versionID, Title: title, PayloadJSON: string(payload), CreatedAt: now, UpdatedAt: now}
	version := model.AssetVersion{ID: versionID, AssetID: id, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: string(definition), Note: "上一镜头组片末截帧，供下一组继承空间站位", CreatedAt: now, UpdatedAt: now}
	representation := model.AssetRepresentation{ID: videoChainHash([]string{"frame-representation", id})[:32], TaskID: id, AssetVersionID: versionID, ResourceID: step.FrameResourceID, MediaType: "image", Role: "continuity_frame", MetadataJSON: string(definition), CreatedAt: now}
	link := model.ProjectAssetLink{ID: videoChainHash([]string{"frame-project", id})[:32], ProjectID: task.ProjectID, AssetID: id, CreatedAt: now}
	ref := model.ShotAssetReference{ID: videoChainHash([]string{"frame-binding", step.ShotID, versionID})[:32], ShotID: step.ShotID, AssetVersionID: versionID, Role: "continuity", Status: "linked", CreatedAt: now}
	if err := s.repo.RegisterStoryboardReference(task, step.RevisionID, &asset, &version, &representation, &link, &ref, step.FrameAssetID != ""); err != nil {
		return err
	}
	step.FrameAssetID = id
	return nil
}
