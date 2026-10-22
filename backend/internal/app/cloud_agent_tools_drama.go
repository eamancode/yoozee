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

// dramaSegmentsWriteDescription 是 drama_segments_write 给 Agent 的用法说明；order 的语义由 drama.PlanSegmentOrder 把关。
const dramaSegmentsWriteDescription = "按段号写一章的分段：已有段号写新版本（只让依赖输入真的变了的产物过期），新段号新建一段并排到本章末尾。每段必须符合契约 segment，时长不超过项目生成模型的上限（Seedance 2.0 / MiniMax-H3 15 秒，Seedance 2.5 30 秒），否则整批拒绝。" +
	"只改段内容时不传 order，不会删段。拆段或合段要传 order：先 drama_segments_read 取本章全部段号，order 是本章全部段号的新顺序（没改动的段和 derived 的旧镜头也要列），段按它排位置，segments 里至少写一段，写入的段号都要在 order 里。" +
	"拆段：S02 拆成两段时，S02 写成前半，后半用 S02b（原段号加小写字母，已占用就用 S02c），order 写 [\"S01\",\"S02\",\"S02b\",\"S03\"]，新段紧跟在原段后面而不是排到章末。" +
	"合段：S03 并进 S02 时，写合并后的 S02，order 里不写 S03。没出现在 order 里的已有段会连同它的候选视频、分镜图和证据一起删除，无法恢复，所以只在合段时省略，其余段一个不落。"

// dramaShotlistWriteDescription 是 drama_shotlist_write 给 Agent 的用法说明；校验与装段规则在 internal/drama。
const dramaShotlistWriteDescription = "写一章的镜头表并整章重装段：服务端把章节正文转成台词表，分镜行的 spec 转成镜头表（台词按原文对回台词 id），按 R-SHOT 规则检查，有 error 就整批拒绝并返回规则项与镜头（reason=shotlist_check_failed），没有 error 才按场装段、替换本章全部段（旧段连同候选、证据一起删除）。" +
	"每行一镜，spec 写 id（E01S01-01）、scene（E01S01）、purpose、subject、size、cam{z,y,x,f?}、move、action、start_state、end_state、lines[{text 原文一字不改, speaker, delivery?, pace?, tone?, emotion?}]，可选 emotion_arc、performance、light（灯光七项）、blocking、transition、refs_hint 等；绑定资产写在 assetBindings。warn 不挡保存，随结果返回。"

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
	case "drama_chapters_read":
		var args struct {
			UnitID string `json:"unitId"`
			Offset int    `json:"offset"`
		}
		if err := decode(&args); err != nil {
			return nil, err
		}
		return s.dramaChaptersRead(userID, projectID, strings.TrimSpace(args.UnitID), args.Offset)
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
	case "drama_shotlist_read":
		var args struct {
			UnitID string `json:"unitId"`
		}
		if err := decode(&args); err != nil {
			return nil, err
		}
		view, err := s.DramaShotlist(userID, projectID, args.UnitID)
		if err != nil {
			return nil, err
		}
		if view == nil {
			return map[string]any{"unitId": args.UnitID, "shotlist": nil, "note": "这一章还没有镜头表：用 drama_shotlist_write 写入分镜行，或用户在章节页生成分镜"}, nil
		}
		return view, nil
	case "drama_shotlist_write":
		var args struct {
			UnitID string `json:"unitId"`
			DramaShotlistWriteRequest
		}
		if err := decode(&args); err != nil {
			return nil, err
		}
		return s.WriteDramaShotlist(userID, projectID, args.UnitID, args.DramaShotlistWriteRequest)
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

const dramaChapterPageRunes = 8000

// dramaChaptersRead 列章节，或分页读一章原文（按字符计，不切断多字节字符）。
func (s *Service) dramaChaptersRead(userID string, projectID string, unitID string, offset int) (any, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, BadAuthRequest("offset 不能为负数")
	}
	if unitID == "" {
		units, err := s.repo.ProjectUnitSummaries(projectID)
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, len(units))
		for _, unit := range units {
			items = append(items, map[string]any{"unitId": unit.ID, "title": unit.Title, "position": unit.Position, "wordCount": unit.WordCount, "kind": unit.Kind})
		}
		return map[string]any{"chapters": items, "next": "用 unitId 读某一章正文"}, nil
	}
	unit, err := s.repo.ProjectUnit(projectID, unitID)
	if err != nil {
		return nil, err
	}
	text := []rune(unit.SourceText)
	if offset > len(text) {
		offset = len(text)
	}
	end := offset + dramaChapterPageRunes
	if end > len(text) {
		end = len(text)
	}
	out := map[string]any{"unitId": unit.ID, "title": unit.Title, "offset": offset, "total": len(text), "text": string(text[offset:end])}
	if end < len(text) {
		out["nextOffset"] = end
	}
	if len(text) == 0 {
		out["note"] = "这一章还没有正文"
	}
	return out, nil
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
	if artifactType != drama.ArtifactStoryboard && artifactType != drama.ArtifactActionBoard && artifactType != drama.ArtifactVideo {
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
	assetVersionID, err := s.ensureGeneratedProjectAsset(*task, projectID, shot.ID, resourceID, mediaType)
	if err != nil {
		return model.ShotArtifact{}, err
	}
	metadata, _ := json.Marshal(map[string]any{"source": "short-drama-agent", "taskId": task.ID, "model": task.Model})
	if err := s.RegisterShotOutput(userID, projectID, RegisterTaskOutputRequest{TaskID: task.ID, ShotID: shot.ID, ArtifactType: artifactType, AssetVersionID: assetVersionID, ResourceID: resourceID, MediaType: mediaType, Role: "output", MetadataJSON: string(metadata)}); err != nil {
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
