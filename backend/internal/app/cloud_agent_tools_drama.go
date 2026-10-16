// 短剧 Agent 的项目工具（drama_*）：只在当前画布属于短剧项目时出现。
// 数据都写在项目里（需求、分段、连戏表、证据），不改画布、不花钱；生成媒体仍走 generate_media 的审批。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func cloudAgentDramaTool(name string) bool {
	return strings.HasPrefix(name, "drama_")
}

func (s *Service) cloudAgentDramaTool(userID string, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	projectID := strings.TrimSpace(state.Request.DramaProjectID)
	if projectID == "" {
		return nil, BadAuthRequest("当前画布不属于短剧项目，不能使用短剧工具")
	}
	decode := func(target any) error {
		if err := decodeCloudAgentJSONObject(call.Function.Arguments, target); err != nil {
			return cloudAgentJSONArgumentError(err)
		}
		return nil
	}
	switch call.Function.Name {
	case "drama_progress_read":
		if err := decode(&struct{}{}); err != nil {
			return nil, err
		}
		progress, err := s.DramaProgress(userID, projectID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"projectId": projectID, "model": map[string]any{"id": progress.Model.ID, "label": progress.Model.Label, "maxSegmentSeconds": progress.Model.Duration.Max, "calibrated": progress.Model.Calibrated}, "authMode": progress.AuthMode, "line": progress.Line, "next": progress.Next, "endStates": progress.EndStates, "segments": progress.Segments}, nil
	case "drama_brief_read":
		if err := decode(&struct{}{}); err != nil {
			return nil, err
		}
		return s.ProjectBrief(userID, projectID)
	case "drama_bible_read":
		if err := decode(&struct{}{}); err != nil {
			return nil, err
		}
		return s.ProjectBible(userID, projectID)
	case "drama_segments_read":
		var args struct {
			UnitID string `json:"unitId"`
		}
		if err := decode(&args); err != nil {
			return nil, err
		}
		return s.DramaSegments(userID, projectID, strings.TrimSpace(args.UnitID))
	case "drama_compile_prompt":
		var args struct {
			ShotID string `json:"shotId"`
			Model  string `json:"model"`
		}
		if err := decode(&args); err != nil {
			return nil, err
		}
		return s.CompileProjectShot(userID, projectID, args.ShotID, CompileProjectShotRequest{Model: args.Model})
	case "drama_brief_write", "drama_bible_write":
		var args struct {
			Document json.RawMessage `json:"document"`
		}
		if err := decode(&args); err != nil {
			return nil, err
		}
		if call.Function.Name == "drama_brief_write" {
			return s.UpdateProjectBrief(userID, projectID, args.Document)
		}
		return s.UpdateProjectBible(userID, projectID, args.Document)
	case "drama_segments_write":
		var args DramaSegmentsWriteRequest
		if err := decode(&args); err != nil {
			return nil, err
		}
		return s.WriteDramaSegments(userID, projectID, args)
	case "drama_artifact_register":
		var args struct {
			ShotID       string `json:"shotId"`
			TaskID       string `json:"taskId"`
			ArtifactType string `json:"artifactType"`
		}
		if err := decode(&args); err != nil {
			return nil, err
		}
		return s.DramaRegisterArtifact(userID, projectID, args.ShotID, args.TaskID, args.ArtifactType)
	case "drama_qa_transcribe":
		var args struct {
			ArtifactID string `json:"artifactId"`
		}
		if err := decode(&args); err != nil {
			return nil, err
		}
		task, err := s.DramaTranscribe(userID, projectID, args.ArtifactID)
		if err != nil {
			return nil, err
		}
		state.TaskIDs = appendUniqueString(state.TaskIDs, task.ID)
		return map[string]any{"taskId": task.ID, "status": task.Status, "next": "用 task_get 等它成功，再 drama_evidence_write（kind=transcript，taskId）"}, nil
	case "drama_qa_frames":
		var args struct {
			ArtifactID string `json:"artifactId"`
		}
		if err := decode(&args); err != nil {
			return nil, err
		}
		return s.DramaFrames(userID, projectID, args.ArtifactID)
	case "drama_evidence_write":
		var args CreateShotEvidenceRequest
		if err := decode(&args); err != nil {
			return nil, err
		}
		args.ResourceID = ""
		return s.CreateShotEvidence(userID, projectID, args)
	case "drama_take_pick":
		var args struct {
			ArtifactID string `json:"artifactId"`
			Reason     string `json:"reason"`
		}
		if err := decode(&args); err != nil {
			return nil, err
		}
		return s.PickShotArtifact(userID, projectID, args.ArtifactID, PickShotArtifactRequest{Reason: args.Reason})
	case "drama_gate_check":
		var args DramaGateRequest
		if err := decode(&args); err != nil {
			return nil, err
		}
		return s.DramaGate(userID, projectID, args)
	case "drama_delivery_package":
		if err := decode(&struct{}{}); err != nil {
			return nil, err
		}
		if _, err := s.activeProjectForUser(userID, projectID); err != nil {
			return nil, err
		}
		return map[string]any{"downloadPath": fmt.Sprintf("/api/projects/%s/drama/delivery", projectID), "note": "把这个下载地址交给用户；打开时生成最新的交付包并记一条「已交付」证据。"}, nil
	}
	return nil, BadAuthRequest("未知的短剧工具：" + call.Function.Name)
}

func appendUniqueString(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

// DramaRegisterArtifact 把一个成功的生成任务登记为某段的一条候选，返回新产物。
func (s *Service) DramaRegisterArtifact(userID string, projectID string, shotID string, taskID string, artifactType string) (model.ShotArtifact, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return model.ShotArtifact{}, err
	}
	stepKey := map[string]string{drama.ArtifactStoryboard: "storyboard", drama.ArtifactActionBoard: "previz", drama.ArtifactVideo: "video"}[artifactType]
	if stepKey == "" {
		return model.ShotArtifact{}, BadAuthRequest("产物类型只能是 storyboard、action_board 或 video")
	}
	shot, err := s.repo.ShotForProject(projectID, strings.TrimSpace(shotID))
	if err != nil {
		return model.ShotArtifact{}, err
	}
	task, err := s.repo.TaskForUser(userID, strings.TrimSpace(taskID))
	if err != nil {
		return model.ShotArtifact{}, err
	}
	if task.Status != model.TaskStatusSucceeded {
		return model.ShotArtifact{}, BadAuthRequest("任务还没有成功完成")
	}
	resourceID, mediaType := taskOutputResource(task.ResultJSON, task.Type)
	if resourceID == "" {
		return model.ShotArtifact{}, BadAuthRequest("任务结果里没有可登记的媒体")
	}
	if (artifactType == drama.ArtifactVideo) != (mediaType == "video") {
		return model.ShotArtifact{}, BadAuthRequest("任务产出的媒体类型与产物类型不一致")
	}
	stepID := ""
	workflows, err := s.ProjectWorkflows(projectID)
	if err != nil {
		return model.ShotArtifact{}, err
	}
	for _, wf := range workflows {
		if wf.Instance.UnitID != shot.UnitID {
			continue
		}
		for _, step := range wf.Steps {
			if step.StepKey == stepKey {
				stepID = step.ID
			}
		}
	}
	if stepID == "" {
		created, err := s.CreateUnitWorkflow(userID, projectID, shot.UnitID)
		if err != nil {
			return model.ShotArtifact{}, err
		}
		for _, step := range created.Steps {
			if step.StepKey == stepKey {
				stepID = step.ID
			}
		}
	}
	if stepID == "" {
		return model.ShotArtifact{}, errors.New("章节工作流缺少对应步骤")
	}
	assetVersionID, err := s.ensureGeneratedProjectAsset(*task, projectID, shot.ID, resourceID, mediaType)
	if err != nil {
		return model.ShotArtifact{}, err
	}
	metadata, _ := json.Marshal(map[string]any{"source": "short-drama-agent", "taskId": task.ID, "model": task.Model})
	if _, err := s.RegisterTaskOutput(userID, projectID, stepID, RegisterTaskOutputRequest{TaskID: task.ID, ShotID: shot.ID, ArtifactType: artifactType, AssetVersionID: assetVersionID, ResourceID: resourceID, MediaType: mediaType, Role: "output", MetadataJSON: string(metadata)}); err != nil {
		return model.ShotArtifact{}, err
	}
	artifacts, err := s.repo.ProjectShotArtifacts(projectID)
	if err != nil {
		return model.ShotArtifact{}, err
	}
	for i := len(artifacts) - 1; i >= 0; i-- {
		if artifacts[i].TaskID == task.ID && artifacts[i].ShotID == shot.ID && artifacts[i].Type == artifactType {
			return artifacts[i], nil
		}
	}
	return model.ShotArtifact{}, errors.New("产物登记后没有找到记录")
}
