package app

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"yingce/backend/internal/model"
)

const dramaSceneEnding = "【本镜最后阶段】镜头平稳后拉并适度升高，最终停在能够完整交代场景空间布局的广角全景机位，将本场景所有人物的全身及主要场景结构纳入画面。保持人物在剧情结束时的位置、朝向、姿态及彼此距离，保持场景几何结构和道具位置不变。人物、服装、道具及环境统一转为无贴图的哑光白色三维模型，保留柔和阴影，使轮廓、前后层次和空间关系清晰可辨。上述运镜与白模转换须在视频最后1秒开始前完成；最后1秒锁定机位，将完成转换的白模全景画面定格至视频结束。"

const dramaSubmissionKey = "_dramaVideoSubmission"

func fixedProjectVideoSeconds(duration VideoDurationConfig) int {
	value := 0
	switch duration.Selection {
	case "enum":
		if len(duration.Values) == 0 {
			return 0
		}
		value = duration.Values[0]
		for _, candidate := range duration.Values {
			if candidate != value {
				return 0
			}
		}
	case "range":
		if duration.Min == duration.Max {
			value = duration.Min
		}
	}
	if value == 15 || value == 30 {
		return value
	}
	return 0
}

// System channel capabilities come from the current catalog, never from client metadata.
func (s *Service) projectVideoFixedDuration(input map[string]any) (int, error) {
	config, _ := input["config"].(map[string]any)
	raw, err := json.Marshal(config)
	if err != nil {
		return 0, err
	}
	var provider providerConfig
	if err := json.Unmarshal(raw, &provider); err != nil {
		return 0, err
	}
	channelID := firstNonEmpty(provider.ChannelID, systemChannelIDFromBaseURL(provider.BaseURL))
	profile := provider.CapabilityConfig
	if channelID != "" {
		item, err := s.repo.ChannelModelByKey(channelID, providerChannelModelKey(provider))
		if err != nil {
			return 0, BadAuthRequest("当前系统渠道模型未配置或已停用")
		}
		profile, err = normalizedChannelModelCapability(item, s.protocolRegistry())
		if err != nil {
			return 0, err
		}
	}
	if profile == nil || profile.Video == nil {
		return 0, nil
	}
	return fixedProjectVideoSeconds(profile.Video.Duration), nil
}

type submissionSegment struct {
	Scene string  `json:"scene"`
	Dur   float64 `json:"dur"`
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
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
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
	if retry {
		if previous, ok := metadata[dramaSubmissionKey].(map[string]any); ok {
			if base, ok := previous["basePrompt"].(string); ok {
				basePrompt = base
			}
		}
	}
	segment := segmentSubmissionContent(*shot, revision)
	if !isPositiveVideoSeconds(segment.Dur) {
		return "", BadAuthRequest("镜头组时长必须大于 0")
	}
	fixed, err := s.projectVideoFixedDuration(input)
	if err != nil {
		return "", err
	}
	if fixed > 0 {
		if segment.Dur > float64(fixed) {
			return "", BadAuthRequest(fmt.Sprintf("镜头组超过 %d 秒，不能使用仅生成 %d 秒的模型", fixed, fixed))
		}
		config, _ := input["config"].(map[string]any)
		config["videoSeconds"] = strconv.Itoa(fixed)
	}
	shots, err := s.repo.ProjectUnitShots(projectID, shot.UnitID)
	if err != nil {
		return "", err
	}
	sameScene, nextID := false, ""
	for i := range shots {
		if shots[i].ID != shotID || i+1 == len(shots) {
			continue
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
	if sameScene && !strings.Contains(prompt, dramaSceneEnding) {
		prompt += "\n\n" + dramaSceneEnding
	}
	metadata[dramaSubmissionKey] = map[string]any{"basePrompt": basePrompt, "endingApplied": sameScene, "nextShotId": nextID, "contentSeconds": segment.Dur, "fixedSeconds": fixed}
	metadata["shotRevisionId"] = shot.CurrentRevisionID
	if artifact, ok := metadata["artifactMetadata"].(map[string]any); ok {
		artifact["contentDurationSeconds"] = segment.Dur
		if fixed > 0 {
			artifact["durationSeconds"] = fixed
		}
	}
	input["prompt"] = prompt
	return prompt, nil
}

func isPositiveVideoSeconds(seconds float64) bool {
	return seconds > 0 && !math.IsNaN(seconds) && !math.IsInf(seconds, 0)
}
