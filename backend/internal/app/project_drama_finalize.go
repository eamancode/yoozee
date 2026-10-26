// 短剧流程步骤的收尾（M7.2）：带 metadata.dramaStep 的文本任务转成功之前，服务端解析模型输出、检查、写库，
// 与资产出图的 finalizeProjectAssetImageTask 同一位置。前端不再解析任务结果、不再写镜头表。
// 生成分段写镜头表并装段；剧本优化写剧集单元（project_drama_script_adapt.go）。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// DramaStepTaskOutcome 是收尾写进任务结果 result.dramaStep 的摘要（解析率、检查、写库结果）。
type DramaStepTaskOutcome struct {
	Step string `json:"step"`
	// Status 是 written（镜头表与段已写）或 draft（检查有 error，只存了草稿，没有装段）。
	Status   string               `json:"status"`
	UnitID   string               `json:"unitId"`
	Parse    drama.ShotLinesStats `json:"parse"`
	Errors   int                  `json:"errors"`
	Warnings int                  `json:"warnings"`
	Shots    int                  `json:"shots"`
	Segments int                  `json:"segments"`
	Scenes   int                  `json:"scenes"`
	Version  int                  `json:"version"`
	Notes    []string             `json:"notes,omitempty"`
	Profile  string               `json:"profile,omitempty"`
}

const (
	DramaStepOutcomeWritten = "written"
	DramaStepOutcomeDraft   = "draft"
)

// dramaStepTaskTarget 是步骤任务作用的项目和章节（metadata.domainProjectId / chapterId）。
type dramaStepTaskTarget struct {
	ProjectID string
	UnitID    string
}

func taskDramaStepTarget(task model.Task) (dramaStepTaskTarget, error) {
	var input struct {
		Metadata struct {
			ProjectID string `json:"domainProjectId"`
			ChapterID string `json:"chapterId"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return dramaStepTaskTarget{}, fmt.Errorf("流程步骤任务输入无效：%w", err)
	}
	target := dramaStepTaskTarget{ProjectID: firstNonEmpty(strings.TrimSpace(input.Metadata.ProjectID), strings.TrimSpace(task.ProjectID)), UnitID: strings.TrimSpace(input.Metadata.ChapterID)}
	if target.ProjectID == "" || target.UnitID == "" {
		return dramaStepTaskTarget{}, errors.New("流程步骤任务缺少项目或章节标识")
	}
	return target, nil
}

// finalizeDramaStepTask 在任务转成功之前跑这一步的解析 → 检查 → 写库，摘要写进 result.dramaStep。
// 返回 false 表示不是流程步骤任务。写库失败（数据库、权限、解析不出镜头行）时任务按失败收尾。
func (s *Service) finalizeDramaStepTask(task model.Task, result map[string]interface{}) (bool, error) {
	meta := taskDramaStep(task.InputJSON)
	// 流程技能包评测的任务没有项目章节，指标由评测自己算，不写库。
	if meta == nil || strings.Contains(task.InputJSON, `"dramaStepEval"`) {
		return false, nil
	}
	switch meta.Step {
	case drama.StepChapterShots:
		outcome, err := s.applyChapterShotsTask(task, meta, result)
		if err != nil {
			return false, err
		}
		result["dramaStep"] = outcome
		return true, nil
	case drama.StepScriptAdapt:
		outcome, err := s.applyScriptAdaptTask(task, meta, result)
		if err != nil {
			return false, err
		}
		result["dramaStep"] = outcome
		return true, nil
	default:
		return false, fmt.Errorf("流程步骤 %s 没有服务端收尾", meta.Step)
	}
}

// applyChapterShotsTask 是生成分段的 Apply：模型输出 → 分镜行 → 按名字绑资产 → 镜头表 → 检查 → 装段写库。
// 检查有 error 时不拒绝：行和检查结果存成草稿（check_json），不装段；检查结果不写成证据（kind=gate 的证据是成片门禁）。
func (s *Service) applyChapterShotsTask(task model.Task, meta *dramaStepTaskMeta, result map[string]interface{}) (DramaStepTaskOutcome, error) {
	target, err := taskDramaStepTarget(task)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	text, _ := result["text"].(string)
	rows, stats, err := drama.StoryboardRowsFromText(text)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	assets, err := s.ProjectAssets(task.UserID, target.ProjectID)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	bindDramaRowAssets(rows, assets)
	raws := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			return DramaStepTaskOutcome{}, err
		}
		raws = append(raws, encoded)
	}
	written, err := s.WriteDramaShotlist(task.UserID, target.ProjectID, target.UnitID, DramaShotlistWriteRequest{Rows: raws, Rule: meta.Rule, KeepDraft: true, Parse: &stats})
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	outcome := DramaStepTaskOutcome{
		Step: meta.Step, Status: DramaStepOutcomeWritten, UnitID: target.UnitID, Parse: stats,
		Errors: written.Check.Errors, Warnings: written.Check.Warnings, Shots: len(written.Shotlist.Shotlist.Shots),
		Version: written.Shotlist.Version, Notes: written.Notes, Profile: written.Profile,
	}
	if written.Draft {
		outcome.Status = DramaStepOutcomeDraft
		return outcome, nil
	}
	scenes := map[string]bool{}
	for _, seg := range written.Segments {
		scenes[seg.Spec.Scene] = true
	}
	outcome.Segments, outcome.Scenes = len(written.Segments), len(scenes)
	return outcome, nil
}
