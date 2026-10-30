// 分镜卡逐张编辑与问题标记（M8 卡 H）：带问题装段以后，镜头表检查的 error 按镜头挂到段上；分镜制作里改段内一个镜头，
// 改的是它背后的镜头表镜头（动作、构图、台词、景别、时长、机位运镜、情绪弧、站位、灯光）。写段仍走 WriteDramaSegments：
// 服务端先把改过的镜头写回镜头表（分镜行原文里的 spec 一起改，之后重装段不丢），按当前编译档案重编这些镜头在段里的文字，
// 写段，返回整章重跑检查后的结果；问题清掉的镜头不再标。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// DramaSegmentsCheck 是写段时一起改了镜头表后整章的检查结果；各段挂的问题在 ProjectDramaSegmentView.Issues。
type DramaSegmentsCheck struct {
	Version  int  `json:"version"`
	Errors   int  `json:"errors"`
	Warnings int  `json:"warnings"`
	Accepted bool `json:"accepted"`
}

type DramaSegmentsWriteResult struct {
	Segments []ProjectDramaSegmentView `json:"segments"`
	// Check 只在带 shots（分镜卡编辑）时有。
	Check *DramaSegmentsCheck `json:"check,omitempty"`
}

// WriteDramaSegmentsChecked 是 WriteDramaSegments 加分镜卡编辑：带 Shots 时先改镜头表、重编镜头、重检，再写段。
// 段没写成时镜头表退回编辑前的内容（版本号照常往上走），免得检查结果和段对不上。
func (s *Service) WriteDramaSegmentsChecked(userID string, projectID string, req DramaSegmentsWriteRequest) (DramaSegmentsWriteResult, error) {
	if len(req.Shots) == 0 {
		views, err := s.writeDramaSegments(userID, projectID, req)
		return DramaSegmentsWriteResult{Segments: views}, err
	}
	edit, err := s.planDramaShotEdits(userID, projectID, req)
	if err != nil {
		return DramaSegmentsWriteResult{}, err
	}
	if err := s.repo.SaveDramaShotlist(edit.record); err != nil {
		return DramaSegmentsWriteResult{}, err
	}
	req.Segments, req.Shots = edit.segments, nil
	views, err := s.writeDramaSegments(userID, projectID, req)
	if err != nil {
		restore := *edit.previous
		restore.UpdatedAt = time.Now()
		if restoreErr := s.repo.SaveDramaShotlist(&restore); restoreErr != nil {
			return DramaSegmentsWriteResult{}, fmt.Errorf("%w（镜头表也没能退回编辑前：%v）", err, restoreErr)
		}
		return DramaSegmentsWriteResult{}, err
	}
	report := edit.report
	return DramaSegmentsWriteResult{Segments: views, Check: &DramaSegmentsCheck{Version: edit.record.Version, Errors: report.Errors, Warnings: report.Warnings, Accepted: report.Accepted}}, nil
}

// dramaShotEditPlan 是一次分镜卡编辑算好的内容：要存的镜头表记录、要写的段、整章新的检查结果。
type dramaShotEditPlan struct {
	previous *model.DramaShotlist
	record   *model.DramaShotlist
	segments []json.RawMessage
	report   drama.CheckReport
}

func (s *Service) planDramaShotEdits(userID string, projectID string, req DramaSegmentsWriteRequest) (dramaShotEditPlan, error) {
	if len(req.Order) > 0 {
		return dramaShotEditPlan{}, BadAuthRequest("改分镜卡和拆段、合段要分两次保存")
	}
	if len(req.Shots) > dramaShotEditMaxShots {
		return dramaShotEditPlan{}, BadAuthRequest(fmt.Sprintf("一次最多改 %d 个镜头", dramaShotEditMaxShots))
	}
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return dramaShotEditPlan{}, err
	}
	unitID := strings.TrimSpace(req.UnitID)
	unit, err := s.repo.ProjectUnit(projectID, unitID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dramaShotEditPlan{}, BadAuthRequest(fmt.Sprintf("章节 %q 不在这个项目里", unitID))
		}
		return dramaShotEditPlan{}, err
	}
	previous, err := s.repo.DramaShotlistForUnit(projectID, unitID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return dramaShotEditPlan{}, BadAuthRequest("这一章还没有镜头表，只能改段内文字，不能按镜头表编辑分镜卡")
	}
	if err != nil {
		return dramaShotEditPlan{}, err
	}
	if req.ShotlistVersion > 0 && req.ShotlistVersion != previous.Version {
		conflict := NewAppError(http.StatusConflict, fmt.Sprintf("镜头表在你编辑期间更新过（第 %d 版 → 第 %d 版），刷新后再改", req.ShotlistVersion, previous.Version))
		conflict.Reason = ReasonConflict
		return dramaShotEditPlan{}, conflict
	}
	var stored drama.Shotlist
	var check drama.CheckReport
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(previous.ShotlistJSON), &stored); err != nil {
		return dramaShotEditPlan{}, fmt.Errorf("镜头表数据损坏: %w", err)
	}
	_ = json.Unmarshal([]byte(firstNonEmpty(previous.CheckJSON, "{}")), &check)
	if check.HasErrors() && !check.Accepted {
		return dramaShotEditPlan{}, BadAuthRequest("这一章的镜头表检查没过、还没装段，分镜制作里的段还是上一次装出的：先在生成分段侧栏选「先装段」或重新生成问题场")
	}
	if strings.TrimSpace(previous.RowsJSON) == "" || json.Unmarshal([]byte(previous.RowsJSON), &rows) != nil || len(rows) != len(stored.Shots) {
		return dramaShotEditPlan{}, BadAuthRequest("这一章的镜头表没有对得上的分镜行原文，不能按镜头编辑；请重新生成分段")
	}
	var script *drama.Script
	if strings.TrimSpace(previous.ScriptJSON) != "" {
		var parsed drama.Script
		if err := json.Unmarshal([]byte(previous.ScriptJSON), &parsed); err == nil {
			script = &parsed
		}
	}
	vocab, err := drama.BuiltinVocab()
	if err != nil {
		return dramaShotEditPlan{}, err
	}
	rowOf := make(map[string]int, len(stored.Shots))
	for i, sh := range stored.Shots {
		if _, ok := rowOf[sh.ID]; !ok {
			rowOf[sh.ID] = i
		}
	}
	edited := map[string]bool{}
	for n, raw := range req.Shots {
		shot, err := parseEditedShot(raw, n+1, vocab)
		if err != nil {
			return dramaShotEditPlan{}, err
		}
		i, ok := rowOf[shot.ID]
		if !ok {
			return dramaShotEditPlan{}, BadAuthRequest(fmt.Sprintf("镜头 %s 不在这一章的镜头表里", drama.EchoSegmentID(shot.ID)))
		}
		if edited[shot.ID] {
			return dramaShotEditPlan{}, BadAuthRequest(fmt.Sprintf("一次保存里镜头 %s 出现了两次", shot.ID))
		}
		edited[shot.ID] = true
		if rows[i], err = withRowSpec(rows[i], shot); err != nil {
			return dramaShotEditPlan{}, err
		}
	}
	parsedRows, err := parseShotlistRows(rows)
	if err != nil {
		return dramaShotEditPlan{}, err
	}
	m, err := dramaShotlistModel(project, briefSegmentRule(project.BriefJSON, nil))
	if err != nil {
		return dramaShotEditPlan{}, err
	}
	assets, err := s.ProjectAssets(userID, projectID)
	if err != nil {
		return dramaShotEditPlan{}, err
	}
	imaged, err := s.dramaImagedAssets(assets)
	if err != nil {
		return dramaShotEditPlan{}, err
	}
	phrases, err := s.activeCompileProfile()
	if err != nil {
		return dramaShotEditPlan{}, err
	}
	pc := projectDramaContext(project)
	pc.Episode = s.dramaChapterEpisode(projectID, unit)
	if pc.SceneCast, err = s.unitSceneCast(project, unit.ID, assets); err != nil {
		return dramaShotEditPlan{}, err
	}
	// 整章按改过的分镜行重转、重检、重装一遍（不落库）：检查要整章看台词覆盖，镜头的段内文字也要按前后镜（情绪弧承接）编。
	plan, err := planDramaShotlistWith(parsedRows, script, assets, imaged, m, phrases, pc, true)
	if err != nil {
		return dramaShotEditPlan{}, err
	}
	compiled := map[string]drama.SubShot{}
	for _, seg := range plan.Segments {
		for _, sub := range seg.Shots {
			compiled[sub.ShotID] = sub
		}
	}
	for id := range edited {
		if _, ok := compiled[id]; !ok {
			return dramaShotEditPlan{}, BadAuthRequest(fmt.Sprintf("镜头 %s 改完以后对不上镜头表（改了台词所在的场？），没有保存", id))
		}
	}
	segments, err := s.dramaShotEditSegments(userID, projectID, unitID, req.Segments, edited, compiled)
	if err != nil {
		return dramaShotEditPlan{}, err
	}
	report := plan.Report
	report.Parse = check.Parse
	report.Accepted = report.HasErrors()
	shotlistJSON, _ := json.Marshal(plan.Shotlist)
	checkJSON, _ := json.Marshal(report)
	rowsJSON, _ := json.Marshal(rows)
	record := *previous
	record.ShotlistJSON, record.CheckJSON, record.RowsJSON = string(shotlistJSON), string(checkJSON), string(rowsJSON)
	record.CreatedBy, record.UpdatedAt = userID, time.Now()
	return dramaShotEditPlan{previous: previous, record: &record, segments: segments, report: report}, nil
}

// parseEditedShot 读一张分镜卡改出的镜头：字段只能是契约里的；景别、情绪弧只能从镜头表的景别和情绪弧索引里选，时长要大于 0。
func parseEditedShot(raw json.RawMessage, n int, vocab *drama.Vocab) (drama.Shot, error) {
	if len(raw) > dramaShotlistRowMaxBytes {
		return drama.Shot{}, BadAuthRequest(fmt.Sprintf("第 %d 个镜头超过 %d KB", n, dramaShotlistRowMaxBytes>>10))
	}
	shot, unknown, err := drama.ParseShot(raw)
	if err != nil {
		return drama.Shot{}, BadAuthRequest(fmt.Sprintf("第 %d 个镜头格式无效：%s", n, err.Error()))
	}
	if len(unknown) > 0 {
		return drama.Shot{}, BadAuthRequest(fmt.Sprintf("镜头 %s 有镜头表契约外的字段：%s", drama.EchoSegmentID(shot.ID), strings.Join(unknown, "、")))
	}
	if strings.TrimSpace(shot.ID) == "" {
		return drama.Shot{}, BadAuthRequest(fmt.Sprintf("第 %d 个镜头缺少 id", n))
	}
	if shot.Size != "" && !slices.Contains(drama.ShotSizes(), shot.Size) {
		return drama.Shot{}, BadAuthRequest(fmt.Sprintf("镜头 %s 的景别「%s」不在可选的景别里（%s）", shot.ID, shot.Size, strings.Join(drama.ShotSizes(), "、")))
	}
	if !(shot.Est > 0) {
		return drama.Shot{}, BadAuthRequest(fmt.Sprintf("镜头 %s 的时长要大于 0 秒", shot.ID))
	}
	if arc := shot.EmotionArc; arc != nil && (arc.ID != "" || arc.Continues != "" || arc.PhaseFrom != 0 || arc.PhaseTo != 0) && vocab.Arc(arc.ID) == nil {
		return drama.Shot{}, BadAuthRequest(fmt.Sprintf("镜头 %s 的情绪弧「%s」不在情绪弧索引里，只能从索引里选", shot.ID, arc.ID))
	}
	return shot, nil
}

// withRowSpec 把分镜行原文里的 spec 换成改过的镜头，其余字段（分镜行的提示词备注、资产绑定）原样留着。
func withRowSpec(raw json.RawMessage, shot drama.Shot) (json.RawMessage, error) {
	row := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, fmt.Errorf("分镜行原文损坏: %w", err)
	}
	spec, err := json.Marshal(shot)
	if err != nil {
		return nil, err
	}
	row["spec"] = spec
	return json.Marshal(row)
}

// dramaShotEditSegments 是这次要写的段：请求里的段，加上改过的镜头所在、请求里没带的段（如「只保留在这个镜头」去掉别段里的台词）。
// 改过的镜头在段里的内容换成重编的（段内开始秒 at 用请求里的，没带的段用原来的）。
func (s *Service) dramaShotEditSegments(userID string, projectID string, unitID string, requested []json.RawMessage, edited map[string]bool, compiled map[string]drama.SubShot) ([]json.RawMessage, error) {
	if len(requested) > 60 {
		return nil, BadAuthRequest("一次写入 1 到 60 段")
	}
	views, err := s.DramaSegments(userID, projectID, unitID)
	if err != nil {
		return nil, err
	}
	var segments []drama.Segment
	for _, raw := range requested {
		seg, err := drama.ParseSegment(raw)
		if err != nil {
			return nil, BadAuthRequest(err.Error())
		}
		segments = append(segments, seg)
	}
	holds := func(seg drama.Segment, id string) bool {
		return slices.ContainsFunc(seg.Shots, func(sub drama.SubShot) bool { return sub.ShotID == id })
	}
	for id := range edited {
		if slices.ContainsFunc(segments, func(seg drama.Segment) bool { return holds(seg, id) }) {
			continue
		}
		at := slices.IndexFunc(views, func(v ProjectDramaSegmentView) bool { return !v.Derived && holds(v.Spec, id) })
		if at < 0 {
			return nil, BadAuthRequest(fmt.Sprintf("镜头 %s 不在本章的段里（还没装段，或拆合时去掉了），不能按分镜卡编辑", id))
		}
		if !slices.ContainsFunc(segments, func(seg drama.Segment) bool { return seg.ID == views[at].Spec.ID }) {
			segments = append(segments, views[at].Spec)
		}
	}
	out := make([]json.RawMessage, 0, len(segments))
	for _, seg := range segments {
		shots := slices.Clone(seg.Shots)
		for k, sub := range shots {
			if fresh, ok := compiled[sub.ShotID]; ok && edited[sub.ShotID] {
				fresh.At = sub.At
				shots[k] = fresh
			}
		}
		seg.Shots = shots
		encoded, err := json.Marshal(seg)
		if err != nil {
			return nil, err
		}
		out = append(out, encoded)
	}
	return out, nil
}

// attachDramaSegmentIssues 把各章镜头表检查的 error 挂到段上：只看带问题装了段（或检查没问题）的镜头表，
// 还没装段的草稿不挂（段是上一次装出的，镜头对不上）。
func (s *Service) attachDramaSegmentIssues(projectID string, views []ProjectDramaSegmentView) error {
	byUnit := map[string][]drama.ShotIssue{}
	for i := range views {
		unitID := views[i].UnitID
		issues, ok := byUnit[unitID]
		if !ok {
			var err error
			if issues, err = s.dramaUnitIssues(projectID, unitID); err != nil {
				return err
			}
			byUnit[unitID] = issues
		}
		views[i].Issues = segmentIssues(issues, views[i].Spec)
	}
	return nil
}

// dramaUnitIssues 是一章镜头表最近一次检查的 error（已挂到镜头上）；没有镜头表、检查没问题、还是没装段的草稿时为空。
func (s *Service) dramaUnitIssues(projectID string, unitID string) ([]drama.ShotIssue, error) {
	record, err := s.repo.DramaShotlistForUnit(projectID, unitID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var check drama.CheckReport
	if json.Unmarshal([]byte(firstNonEmpty(record.CheckJSON, "{}")), &check) != nil || !check.HasErrors() || !check.Accepted {
		return nil, nil
	}
	var shotlist drama.Shotlist
	if err := json.Unmarshal([]byte(record.ShotlistJSON), &shotlist); err != nil {
		return nil, nil
	}
	var script *drama.Script
	if strings.TrimSpace(record.ScriptJSON) != "" {
		var parsed drama.Script
		if json.Unmarshal([]byte(record.ScriptJSON), &parsed) == nil {
			script = &parsed
		}
	}
	return drama.ShotIssues(check, shotlist, script, "error"), nil
}

// segmentIssues 是挂在这一段镜头上的问题。
func segmentIssues(issues []drama.ShotIssue, seg drama.Segment) []drama.ShotIssue {
	var out []drama.ShotIssue
	for _, issue := range issues {
		if slices.ContainsFunc(issue.Shots, func(id string) bool {
			return slices.ContainsFunc(seg.Shots, func(sub drama.SubShot) bool { return sub.ShotID == id })
		}) {
			out = append(out, issue)
		}
	}
	return out
}

// countDramaProgressIssues 给进度表的每段填带问题装段后挂着的 error 条数，并在进度行后面写「带问题的段 N」。
func (s *Service) countDramaProgressIssues(projectID string, progress *drama.Progress, shots []model.Shot, revisions map[string]*model.ShotRevision) error {
	shotByID := make(map[string]model.Shot, len(shots))
	for _, shot := range shots {
		shotByID[shot.ID] = shot
	}
	byUnit := map[string][]drama.ShotIssue{}
	for i := range progress.Segments {
		row := &progress.Segments[i]
		issues, ok := byUnit[row.UnitID]
		if !ok {
			var err error
			if issues, err = s.dramaUnitIssues(projectID, row.UnitID); err != nil {
				return err
			}
			byUnit[row.UnitID] = issues
		}
		rev := revisions[shotByID[row.ShotID].CurrentRevisionID]
		if len(issues) == 0 || rev == nil {
			continue
		}
		if seg, err := drama.ParseSegment([]byte(rev.SpecJSON)); err == nil {
			row.Issues = len(segmentIssues(issues, seg))
		}
		if row.Issues > 0 {
			progress.Counts.Issues++
		}
	}
	if progress.Counts.Issues > 0 {
		progress.Line += fmt.Sprintf(" · 带问题的段 %d", progress.Counts.Issues)
	}
	return nil
}
