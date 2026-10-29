// 短剧流程步骤的收尾（M7.2）：带 metadata.dramaStep 的文本任务转成功之前，服务端解析模型输出、检查、写库，
// 与资产出图的 finalizeProjectAssetImageTask 同一位置。前端不再解析任务结果、不再写镜头表。
// 生成分段写镜头表并装段；剧本优化写剧集单元（project_drama_script_adapt.go），写完自动接着建场次色卡任务；风格锁定只把候选写进
// 任务结果，用户点锁定才写项目（project_drama_style_lock.go）；场次色卡写色彩脚本、场次色卡并合成色卡图（project_drama_scene_palette.go）；
// 资产设计写已确认资产并排出图（project_drama_assets.go）。

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
	// Genre / Sections 是按小节生成时的默认戏类和写了几节；WuxiSections 是其中几节是武戏；Regenre 是这次是「改为武戏 / 改回文戏」。
	Genre        string        `json:"genre,omitempty"`
	Sections     int           `json:"sections,omitempty"`
	WuxiSections int           `json:"wuxiSections,omitempty"`
	Regenre      *dramaRegenre `json:"regenre,omitempty"`
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

// taskProjectID 是项目级步骤任务的项目（metadata.domainProjectId，没有时取任务的项目）。
func taskProjectID(task model.Task) string {
	var input struct {
		Metadata struct {
			ProjectID string `json:"domainProjectId"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal([]byte(task.InputJSON), &input)
	return firstNonEmpty(strings.TrimSpace(input.Metadata.ProjectID), strings.TrimSpace(task.ProjectID))
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
		// 剧集写好之后自动接着跑场次色卡；没建成只记原因，已写的剧集不动。
		if target := taskProjectID(task); target != "" {
			outcome.Chained = s.chainScenePaletteAfterAdapt(task, target)
		}
		result["dramaStep"] = outcome
		return true, nil
	case drama.StepStyleLock:
		outcome, err := s.applyStyleLockTask(task, meta, result)
		if err != nil {
			return false, err
		}
		result["dramaStep"] = outcome
		return true, nil
	case drama.StepScenePalette:
		outcome, err := s.applyScenePaletteTask(task, meta, result)
		if err != nil {
			return false, err
		}
		// 色卡写好之后按集依次接着提取资产（M8 卡 D）；没建成只记原因，已写的色卡不动。
		if target := taskProjectID(task); target != "" {
			outcome.Chained = s.chainAssetsAfterScenePalette(task, target)
		}
		result["dramaStep"] = outcome
		return true, nil
	case drama.StepAssetsExtract:
		outcome, err := s.applyAssetsExtractTask(task, meta, result)
		if err != nil {
			return false, err
		}
		// 先接着建下一集的提取（占一个空位），再按剩下的空位给这一集的新资产排出图。
		if target := taskProjectID(task); target != "" {
			outcome.Chained = s.chainAfterAssetsExtract(task, target, taskDramaAssets(task.InputJSON))
		}
		s.queueAssetsExtractImages(task, &outcome)
		result["dramaStep"] = outcome
		return true, nil
	default:
		return false, fmt.Errorf("流程步骤 %s 没有服务端收尾", meta.Step)
	}
}

// applyChapterShotsTask 是生成分段的 Apply：模型输出 → 分镜行 → 按名字绑资产 → 镜头表 → 检查 → 装段写库。
// 按小节生成的任务逐节用戏类的转换器把原样输出转成分镜行，原样输出存进镜头表记录（sections_json），任务结果的 text 换成转出的镜头行。
// 检查有 error 时不拒绝：行和检查结果存成草稿（check_json），不装段；检查结果不写成证据（kind=gate 的证据是成片门禁）。
func (s *Service) applyChapterShotsTask(task model.Task, meta *dramaStepTaskMeta, result map[string]interface{}) (DramaStepTaskOutcome, error) {
	target, err := taskDramaStepTarget(task)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	assets, err := s.ProjectAssets(task.UserID, target.ProjectID)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	shots := taskDramaShots(task.InputJSON)
	var rows []map[string]any
	var stats drama.ShotLinesStats
	var sections []drama.SectionOutput
	if shots != nil {
		rows, stats, sections, err = sectionShotsRows(shots, result, assets)
	} else {
		text, _ := result["text"].(string)
		rows, stats, err = drama.StoryboardRowsFromText(text)
	}
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
	// 只重写了几节（重新生成问题小节、改戏类）时装段沿用没变的节的段；整章生成照旧整章替换。
	keep := false
	if run := taskDramaRun(task.InputJSON); shots != nil && run != nil && (len(run.OnlySections) > 0 || shots.Regenre != nil) {
		keep = true
	}
	written, err := s.WriteDramaShotlist(task.UserID, target.ProjectID, target.UnitID, DramaShotlistWriteRequest{Rows: raws, Rule: meta.Rule, KeepDraft: true, Parse: &stats, Sections: sections, KeepSegments: keep})
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	outcome := DramaStepTaskOutcome{
		Step: meta.Step, Status: DramaStepOutcomeWritten, UnitID: target.UnitID, Parse: stats,
		Errors: written.Check.Errors, Warnings: written.Check.Warnings, Shots: len(written.Shotlist.Shotlist.Shots),
		Version: written.Shotlist.Version, Notes: written.Notes, Profile: written.Profile,
	}
	if shots != nil {
		outcome.Genre, outcome.Sections = shots.Genre, len(sections)
		for _, sec := range sections {
			if sec.Genre == drama.ShotGenreWuxi {
				outcome.WuxiSections++
			}
		}
		if shots.Regenre != nil {
			outcome.Regenre = shots.Regenre
		}
		// 切节时的提醒（文戏 15 连着接话超过两句台词等）只是 warn：记进摘要说明，不拦写库和装段。
		for _, sec := range sections {
			for _, w := range sec.Warnings {
				outcome.Notes = append(outcome.Notes, "小节 "+sec.ID+"："+w)
			}
		}
		// 原样输出已存进镜头表记录：任务结果里换成转出的镜头行（一行一镜，侧栏刷新后按镜头还原），去掉各节原文。
		lines := make([]string, 0, len(raws))
		for _, raw := range raws {
			lines = append(lines, string(raw))
		}
		result["text"] = strings.Join(lines, "\n") + "\n"
		if progress, ok := resultDramaRunProgress(result); ok {
			for i := range progress.Turns {
				progress.Turns[i].Text, progress.Turns[i].KeptRows = "", ""
			}
			result["dramaRun"] = progress
		}
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

// resultDramaRunProgress 读任务结果里的运行进度（runDramaRun 放进去的是结构体，从库里读回来是 map）。
func resultDramaRunProgress(result map[string]interface{}) (dramaRunProgress, bool) {
	raw, ok := result["dramaRun"]
	if !ok {
		return dramaRunProgress{}, false
	}
	if progress, ok := raw.(dramaRunProgress); ok {
		return progress, true
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return dramaRunProgress{}, false
	}
	var progress dramaRunProgress
	if json.Unmarshal(encoded, &progress) != nil {
		return dramaRunProgress{}, false
	}
	return progress, true
}

// sectionShotsRows 把按小节生成的各节输出转成整章的分镜行：重写的节按戏类转换器转（项目资产名、本场锚点、内景补光源），
// 不重写的节（「重新生成问题小节」）直接用镜头表里原有的镜头行；镜头 id 按场接着往下编。返回分镜行、解析统计和每一节的原样输出。
func sectionShotsRows(shots *dramaShotsMeta, result map[string]interface{}, assets []ProjectAssetSummary) ([]map[string]any, drama.ShotLinesStats, []drama.SectionOutput, error) {
	progress, ok := resultDramaRunProgress(result)
	if !ok || len(progress.Turns) != len(shots.Sections) {
		return nil, drama.ShotLinesStats{}, nil, fmt.Errorf("按小节生成的结果不完整：%d 节只写了 %d 节", len(shots.Sections), len(progress.Turns))
	}
	var rows []map[string]any
	var stats drama.ShotLinesStats
	outputs := make([]drama.SectionOutput, 0, len(shots.Sections))
	offset := map[string]int{}
	for i, turn := range progress.Turns {
		sec := shots.Sections[i]
		ctx := sectionConvertContext(sec, assets, offset[sec.Scene])
		conv, err := drama.ConvertSection(sec.Genre, turn.Text, ctx)
		var part []map[string]any
		switch {
		case turn.Kept:
			kept, _, keptErr := drama.StoryboardRowsFromText(turn.KeptRows)
			if keptErr != nil {
				return nil, drama.ShotLinesStats{}, nil, fmt.Errorf("小节 %s 原有的镜头行读不出来：%w", sec.ID, keptErr)
			}
			part = renumberSectionRows(kept, sec, offset[sec.Scene])
		case err != nil:
			return nil, drama.ShotLinesStats{}, nil, fmt.Errorf("小节 %s 的输出转不成镜头：%w", sec.ID, err)
		default:
			part = conv.Rows
		}
		out := drama.NewSectionOutput(sec, turn.Text, conv)
		out.TaskID = turn.TaskID
		outputs = append(outputs, out)
		offset[sec.Scene] += len(part)
		rows = append(rows, part...)
		stats.Rows += len(part)
		stats.Rejected += conv.Stats.Rejected
	}
	stats.Lines = stats.Rows + stats.Rejected
	if stats.Lines > 0 {
		stats.Rate = float64(stats.Rows) / float64(stats.Lines)
	}
	if len(rows) == 0 {
		return nil, stats, nil, drama.ErrNoStoryboardRows
	}
	return drama.NormalizeStoryboardRows(rows), stats, outputs, nil
}

// renumberSectionRows 给不重写的节原有的镜头行按本场接着编镜头 id（前面重写的节镜头数变了，id 跟着挪）。
func renumberSectionRows(rows []map[string]any, sec drama.Section, offset int) []map[string]any {
	for i, row := range rows {
		spec, _ := row["spec"].(map[string]any)
		if spec == nil {
			continue
		}
		spec["id"] = fmt.Sprintf("%s-%02d", sec.Scene, offset+i+1)
		spec["scene"], spec["section"] = sec.Scene, sec.ID
	}
	return rows
}
