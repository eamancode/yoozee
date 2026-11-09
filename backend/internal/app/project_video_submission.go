package app

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

const dramaSceneEnding = "【本镜最后阶段】视频最后1秒，硬切至斜向下45度俯视的大全景机位，画面同时切换为无贴图的哑光白色三维白模。完整呈现场景空间布局、主要道具及本小节最后一个镜头结束时仍在场景内人物的全身。保持人物原有站位、朝向、姿态及彼此距离，保持场景结构和道具位置不变。锁定机位与构图，持续1秒直至视频结束；人物仅有轻微呼吸起伏和身体自然晃动，双脚保持原位。保留柔和阴影，使人物轮廓、遮挡关系和空间层次清晰可辨。"

const dramaSubmissionKey = "_dramaVideoSubmission"

func projectVideoDurationSlots(duration VideoDurationConfig) []int {
	values := duration.Values
	switch duration.Selection {
	case "enum":
	case "range":
		if duration.Min != duration.Max {
			return nil
		}
		values = []int{duration.Min}
	default:
		return nil
	}
	seen := map[int]bool{}
	for _, value := range values {
		if value != 15 && value != 30 {
			return nil
		}
		seen[value] = true
	}
	var slots []int
	for _, value := range []int{15, 30} {
		if seen[value] {
			slots = append(slots, value)
		}
	}
	return slots
}

func projectVideoSubmissionSeconds(slots []int, content float64) (int, error) {
	if !isPositiveVideoSeconds(content) {
		return 0, BadAuthRequest("镜头组时长必须大于 0")
	}
	if len(slots) == 0 {
		return 0, nil
	}
	max := slots[len(slots)-1]
	if content > float64(max) {
		return 0, BadAuthRequest(fmt.Sprintf("镜头组超过 %d 秒，不能使用该时长档位模型", max))
	}
	if len(slots) == 2 && content >= 15 {
		return 30, nil
	}
	return slots[0], nil
}

// System channel capabilities come from the current catalog, never from client metadata.
func (s *Service) projectTaskVideoDurationSlots(input map[string]any) ([]int, error) {
	profile, err := s.projectTaskVideoProfile(input)
	if err != nil || profile == nil {
		return nil, err
	}
	return projectVideoDurationSlots(profile.Duration), nil
}

func (s *Service) projectTaskVideoProfile(input map[string]any) (*VideoCapabilityConfig, error) {
	config, _ := input["config"].(map[string]any)
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	var provider providerConfig
	if err := json.Unmarshal(raw, &provider); err != nil {
		return nil, err
	}
	channelID := firstNonEmpty(provider.ChannelID, systemChannelIDFromBaseURL(provider.BaseURL))
	profile := provider.CapabilityConfig
	if channelID != "" {
		item, err := s.repo.ChannelModelByKey(channelID, providerChannelModelKey(provider))
		if err != nil {
			return nil, BadAuthRequest("当前系统渠道模型未配置或已停用")
		}
		profile, err = normalizedChannelModelCapability(item, s.protocolRegistry())
		if err != nil {
			return nil, err
		}
	}
	if profile == nil || profile.Video == nil {
		return nil, nil
	}
	return profile.Video, nil
}

type submissionSegment struct {
	Scene string  `json:"scene"`
	Dur   float64 `json:"dur"`
	// Verbatim 非空是本体论导演的单元：粘贴提示词自带 1 秒站位交接镜头，不再追加白模收尾。
	Verbatim string `json:"verbatim"`
}

func segmentSubmissionContent(shot model.Shot, revision *model.ShotRevision) submissionSegment {
	var segment submissionSegment
	if revision != nil {
		_ = json.Unmarshal([]byte(revision.SpecJSON), &segment)
	}
	segment.Scene = strings.TrimSpace(segment.Scene)
	if segment.Dur == 0 {
		segment.Dur = float64(shot.DurationMs) / 1000
	}
	return segment
}

// Admission and explicit retry both read current narrative order. Only task snapshots
// receive the suffix; editable shot prompts and compile previews stay unchanged.
func (s *Service) prepareDramaVideoSubmission(userID string, input map[string]any, prompt string, retry bool) (string, error) {
	projectID, shotID := dramaTaskTarget(input)
	if projectID == "" || shotID == "" {
		return prompt, nil
	}
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return "", err
	}
	shot, err := s.repo.ShotForProject(projectID, shotID)
	if err != nil {
		return "", err
	}
	revision, err := s.repo.ShotRevisionForShot(shot.ID, shot.CurrentRevisionID)
	if err != nil {
		return "", err
	}
	metadata, _ := input["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		input["metadata"] = metadata
	}
	if revisionID, _ := metadata["shotRevisionId"].(string); revisionID != "" && revisionID != shot.CurrentRevisionID {
		return "", BadAuthRequest("镜头内容已更新，请刷新镜头组后重新提交")
	}
	basePrompt := prompt
	enabled := project.WhiteModelStoryboardEnabled
	if retry {
		if previous, ok := metadata[dramaSubmissionKey].(map[string]any); ok {
			enabled = true
			if snapshot, ok := previous["whiteModelStoryboardEnabled"].(bool); ok {
				enabled = snapshot
			}
			if base, ok := previous["basePrompt"].(string); ok {
				basePrompt = base
			}
		}
	}
	marker, _ := input[videoChainMetadataKey].(map[string]any)
	if controllerID := stringValue(marker["controllerId"]); controllerID != "" {
		_, state, err := s.videoChainTaskForUser(userID, projectID, controllerID)
		if err != nil {
			return "", err
		}
		enabled = state.WhiteModelStoryboardEnabled == nil || *state.WhiteModelStoryboardEnabled
	}
	if !enabled && stringValue(marker["frameResourceId"]) != "" {
		return "", BadAuthRequest("白模分镜已关闭，请重新准备生成请求")
	}
	segment := segmentSubmissionContent(*shot, revision)
	if !isPositiveVideoSeconds(segment.Dur) {
		return "", BadAuthRequest("镜头组时长必须大于 0")
	}
	slots, err := s.projectTaskVideoDurationSlots(input)
	if err != nil {
		return "", err
	}
	seconds, err := projectVideoSubmissionSeconds(slots, segment.Dur)
	if err != nil {
		return "", err
	}
	if seconds > 0 {
		config, _ := input["config"].(map[string]any)
		// A stale client may have quoted the 15-second tier at the new boundary.
		// Reject that request before billing rather than silently charging a new tier.
		if len(slots) == 2 {
			intentOptions, _ := input["capabilityOptions"].(map[string]any)
			for _, options := range []map[string]any{config, intentOptions} {
				if value, ok := options["videoSeconds"]; ok {
					requested, parseErr := strconv.ParseFloat(fmt.Sprint(value), 64)
					if parseErr != nil || requested != float64(seconds) {
						return "", BadAuthRequest(fmt.Sprintf("该镜头内容应使用 %d 秒档，请刷新报价后重新提交", seconds))
					}
				}
			}
		}
		config["videoSeconds"] = strconv.Itoa(seconds)
		if options, ok := input["capabilityOptions"].(map[string]any); ok {
			options["videoSeconds"] = seconds
		}
	}
	shots, err := s.repo.ProjectUnitShots(projectID, shot.UnitID)
	if err != nil {
		return "", err
	}
	sameScene, nextID := false, ""
	previousID := ""
	previousRevisionID := ""
	for i := range shots {
		if shots[i].ID != shotID {
			continue
		}
		if i > 0 && segment.Scene != "" {
			previous := shots[i-1]
			previousRevision, err := s.repo.ShotRevisionForShot(previous.ID, previous.CurrentRevisionID)
			if err != nil {
				return "", err
			}
			if segment.Scene == segmentSubmissionContent(previous, previousRevision).Scene {
				previousID = previous.ID
				previousRevisionID = previous.CurrentRevisionID
			}
		}
		if i+1 == len(shots) {
			break
		}
		next := shots[i+1]
		nextRevision, err := s.repo.ShotRevisionForShot(next.ID, next.CurrentRevisionID)
		if err != nil {
			return "", err
		}
		nextID = next.ID
		sameScene = segment.Scene != "" && segment.Scene == segmentSubmissionContent(next, nextRevision).Scene
		break
	}
	prompt = basePrompt
	if stringValue(marker["frameResourceId"]) != "" && (stringValue(marker["sourceShotId"]) != previousID || stringValue(marker["sourceRevisionId"]) != previousRevisionID || stringValue(marker["sourceVideoResourceId"]) == "") {
		return "", failedPrecondition("站位参考的来源与当前相邻镜头不一致，请重新准备")
	}
	if previousID == "" && stringValue(marker["frameResourceId"]) != "" {
		return "", failedPrecondition("场景归属或叙事顺序已变化，请重新准备生成；不跨场景继承末帧")
	}
	if marker, ok := input[videoChainMetadataKey].(map[string]any); ok {
		frameID := stringValue(marker["frameResourceId"])
		if frameID != "" {
			images, _ := input["referenceImages"].([]any)
			index := -1
			for i, value := range images {
				image, _ := value.(map[string]any)
				if stringValue(image["storageKey"]) == "resource:"+frameID {
					if index >= 0 {
						return "", BadAuthRequest("站位参考图重复，请重新准备镜头")
					}
					index = i
				}
			}
			if index < 0 {
				return "", BadAuthRequest("站位参考提醒缺少实际发送的截帧图片")
			}
			project, err := s.repo.ProjectForUser(userID, projectID)
			if err != nil {
				return "", err
			}
			m, err := projectDramaModel(project)
			if err != nil {
				return "", err
			}
			tag := drama.ReferenceImageTag(m, index+1)
			if stringValue(marker["referenceTag"]) != tag {
				return "", BadAuthRequest("站位参考图编号与实际图片顺序不一致")
			}
			prompt = videoChainPositionPrompt(tag) + "\n\n" + prompt
		}
	}
	verbatim := strings.TrimSpace(segment.Verbatim) != ""
	if enabled && sameScene && !verbatim && !strings.Contains(prompt, dramaSceneEnding) {
		prompt += "\n\n" + dramaSceneEnding
	}
	metadata[dramaSubmissionKey] = map[string]any{"basePrompt": basePrompt, "endingApplied": enabled && sameScene && !verbatim, "whiteModelStoryboardEnabled": enabled, "nextShotId": nextID, "contentSeconds": segment.Dur, "submissionSeconds": seconds}
	metadata["shotRevisionId"] = shot.CurrentRevisionID
	if artifact, ok := metadata["artifactMetadata"].(map[string]any); ok {
		artifact["contentDurationSeconds"] = segment.Dur
		if seconds > 0 {
			artifact["durationSeconds"] = seconds
		}
	}
	input["prompt"] = prompt
	return prompt, nil
}

func isPositiveVideoSeconds(seconds float64) bool {
	return seconds > 0 && !math.IsNaN(seconds) && !math.IsInf(seconds, 0)
}
