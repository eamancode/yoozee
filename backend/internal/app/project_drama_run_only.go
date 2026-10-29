// 生成分段的「重新生成问题小节」（M8 卡 H 起头，卡 F 改成按小节）：镜头表检查有问题时只重写问题所在的几节。仍是一个运行一个任务、
// 一轮一节：不重写的节不调模型，这一节存下的原样输出照这一轮的提示词放进对话历史，镜头行用镜头表里已有的（保留分镜卡上的手改）；
// 重写的节照样按本轮指令接着写。收尾把各节按顺序拼成整章，和整章生成一样检查、装段（有 error 仍存草稿）。
// onlyScenes 给的是场：等于重写这几场的全部小节。

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// dramaKeptScene 是不重写的一节：对话历史里放的原样输出（text）、镜头表里原有的镜头行（rows，一行一个 JSON）和行数。
type dramaKeptScene struct {
	text  string
	rows  string
	count int
}

// onlyScenesRun 校验「重新生成问题小节」的参数（onlySections 是小节编号，onlyScenes 是场编号、展开成场里的全部小节），
// 记下要重写的节和作底的镜头表版本。镜头表里一镜都没有的节也要重写；作底的镜头表要是按小节生成的（每节存了原样输出）。
func (s *Service) onlyScenesRun(projectID string, unitID string, plan *dramaStepPlan, run dramaRunMeta, scenes []string, sections []string) (dramaRunMeta, error) {
	if plan.usage.Step != drama.StepChapterShots || plan.shots == nil {
		return dramaRunMeta{}, BadAuthRequest("只有生成分段能只重新生成几节")
	}
	if len(plan.scenes) < 2 {
		return dramaRunMeta{}, BadAuthRequest("本章只有一节，直接重新生成整章")
	}
	var ids, sceneIDs []string
	for _, sc := range plan.scenes {
		ids = append(ids, sc.Section)
		if !slices.Contains(sceneIDs, sc.ID) {
			sceneIDs = append(sceneIDs, sc.ID)
		}
	}
	want := map[string]bool{}
	for _, raw := range sections {
		id := strings.TrimSpace(raw)
		if !slices.Contains(ids, id) {
			return dramaRunMeta{}, BadAuthRequest(fmt.Sprintf("小节 %s 不在本章现在切出的小节里（%s）", drama.EchoSegmentID(id), strings.Join(ids, "、")))
		}
		want[id] = true
	}
	for _, raw := range scenes {
		id := strings.TrimSpace(raw)
		if !slices.Contains(sceneIDs, id) {
			return dramaRunMeta{}, BadAuthRequest(fmt.Sprintf("场 %s 不在本章正文的场次里（%s）", drama.EchoSegmentID(id), strings.Join(sceneIDs, "、")))
		}
		for _, sc := range plan.scenes {
			if sc.ID == id {
				want[sc.Section] = true
			}
		}
	}
	record, err := s.repo.DramaShotlistForUnit(projectID, unitID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return dramaRunMeta{}, BadAuthRequest("这一章还没有镜头表，先整章生成分段")
	}
	if err != nil {
		return dramaRunMeta{}, err
	}
	kept, err := keptShotlistSections(record, plan.shots)
	if err != nil {
		return dramaRunMeta{}, err
	}
	for _, id := range ids {
		if _, ok := kept[id]; !ok {
			want[id] = true
		}
	}
	run.OnlySections = run.OnlySections[:0]
	run.OnlyScenes = nil
	for _, id := range ids {
		if want[id] {
			run.OnlySections = append(run.OnlySections, id)
		}
	}
	run.BaseShotlistVersion = record.Version
	return run, nil
}

// keptShotlistSections 把镜头表记录里的分镜行按镜头的小节分好，配上每节存下的原样输出（按现在切出的小节编号）。存下的节按场、
// 正文行、台词和戏类对到现在切出的节：改了戏类以后同一场后面的节号会挪，镜头行跟着换到新编号。镜头表不是按小节生成的、
// 小节和现在切出的对不上（正文、模型或戏类改过）时拒绝；只有这次改戏类的那一场例外：那一场里切法变了的旧节换掉，不放进对话历史。
func keptShotlistSections(record *model.DramaShotlist, shots *dramaShotsMeta) (map[string]dramaKeptScene, error) {
	var shotlist drama.Shotlist
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(record.ShotlistJSON), &shotlist); err != nil {
		return nil, fmt.Errorf("镜头表数据损坏: %w", err)
	}
	var outputs []drama.SectionOutput
	if strings.TrimSpace(record.SectionsJSON) == "" || json.Unmarshal([]byte(record.SectionsJSON), &outputs) != nil || len(outputs) == 0 {
		return nil, BadAuthRequest("这一章的镜头表不是按小节生成的（场次规则或生成方式更新前写的），不能只重写几节；请整章重新生成")
	}
	if strings.TrimSpace(record.RowsJSON) == "" || json.Unmarshal([]byte(record.RowsJSON), &rows) != nil || len(rows) != len(shotlist.Shots) {
		return nil, BadAuthRequest("这一章的镜头表没有对得上的分镜行原文，不能只重写几节；请整章重新生成")
	}
	match := func(o drama.SectionOutput) (drama.Section, bool) {
		for _, sec := range shots.Sections {
			if sec.Scene == o.Scene && sec.Lines == o.Lines && sec.Genre == o.Genre && slices.Equal(nonNilIDs(o.LineIDs), nonNilIDs(sec.LineIDs)) {
				return sec, true
			}
		}
		return drama.Section{}, false
	}
	out := map[string]dramaKeptScene{}
	current := map[string]string{}
	replaced := map[string]bool{}
	for _, o := range outputs {
		sec, ok := match(o)
		switch {
		case ok:
			out[sec.ID] = dramaKeptScene{text: o.Raw}
			current[o.ID] = sec.ID
		case shots.Regenre != nil && o.Scene == shots.Regenre.Scene:
			replaced[o.ID] = true
		default:
			return nil, BadAuthRequest(fmt.Sprintf("镜头表里的小节 %s 和现在切出的小节对不上（正文、视频模型或戏类改过？），请整章重新生成", o.ID))
		}
	}
	for i, sh := range shotlist.Shots {
		if replaced[sh.Section] {
			continue
		}
		id, ok := current[sh.Section]
		kept := out[id]
		if !ok {
			return nil, BadAuthRequest(fmt.Sprintf("镜头 %s 不属于任何存下的小节，不能只重写几节；请整章重新生成", sh.ID))
		}
		var line bytes.Buffer
		if err := json.Compact(&line, rows[i]); err != nil {
			return nil, fmt.Errorf("分镜行原文损坏: %w", err)
		}
		kept.rows += line.String() + "\n"
		kept.count++
		out[id] = kept
	}
	for id, kept := range out {
		if kept.count == 0 {
			delete(out, id)
		}
	}
	return out, nil
}

func nonNilIDs(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// dramaRunKeptSections 是这次运行里不重写、还没放进对话历史的节（小节编号 → 原样输出与原有镜头行）。作底的镜头表在任务建好后
// 又改过（重新装段、分镜卡编辑、别的生成写了库）时拒绝，免得拿新内容和旧问题拼在一起。
func (s *Service) dramaRunKeptSections(task model.Task, run dramaRunMeta, done int) (map[string]dramaKeptScene, error) {
	if len(run.OnlySections) == 0 {
		return nil, nil
	}
	pending := false
	for k := done + 1; k <= len(run.Scenes); k++ {
		pending = pending || !slices.Contains(run.OnlySections, run.Scenes[k-1].Section)
	}
	if !pending {
		return nil, nil
	}
	shots := taskDramaShots(task.InputJSON)
	if shots == nil {
		return nil, errors.New("按小节重写的任务缺少小节表，请重新点「重新生成问题小节」")
	}
	target, err := taskDramaStepTarget(task)
	if err != nil {
		return nil, err
	}
	record, err := s.repo.DramaShotlistForUnit(target.ProjectID, target.UnitID)
	if err != nil {
		return nil, fmt.Errorf("读不到本章镜头表，不能只重写几节：%w", err)
	}
	if record.Version != run.BaseShotlistVersion {
		return nil, fmt.Errorf("本章镜头表在任务建好后又改过（第 %d 版 → 第 %d 版），请重新点「重新生成问题小节」", run.BaseShotlistVersion, record.Version)
	}
	kept, err := keptShotlistSections(record, shots)
	if err != nil {
		return nil, err
	}
	for _, id := range run.OnlySections {
		delete(kept, id)
	}
	return kept, nil
}
