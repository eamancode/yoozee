// 生成分段的「重新生成问题场」（M8 卡 H）：镜头表检查有问题时只重写问题所在的几场。仍是一个运行一个任务、一轮一场：
// 不重写的场不调模型，镜头表里它原有的镜头行照这一轮的提示词放进对话历史，重写的场照样「继续写第 k 场」接着写；
// 收尾把各轮输出按场序拼成整章，和整章生成一样检查、装段（有 error 仍存草稿）。按小节生成以后改成按小节（onlySections）。

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

// dramaKeptScene 是不重写的一场：镜头表里原有的镜头行（一行一个 JSON 对象）和行数。
type dramaKeptScene struct {
	text  string
	count int
}

// onlyScenesRun 校验「重新生成问题场」的参数，记下要重写的场和作底的镜头表版本。
// 镜头表里一行都没有的场也要重写（没有可留的镜头）；镜头表里有认不出场的镜头时拒绝（留不住它们）。
func (s *Service) onlyScenesRun(projectID string, unitID string, plan *dramaStepPlan, run dramaRunMeta, only []string, sections []string) (dramaRunMeta, error) {
	if len(sections) > 0 {
		return dramaRunMeta{}, BadAuthRequest("现在按场生成，还不能按小节重新生成；请用 onlyScenes 给场编号")
	}
	if plan.usage.Step != drama.StepChapterShots {
		return dramaRunMeta{}, BadAuthRequest("只有生成分段能只重新生成几场")
	}
	if len(plan.scenes) < 2 {
		return dramaRunMeta{}, BadAuthRequest("本章正文只有一场，直接重新生成整章")
	}
	ids := make([]string, 0, len(plan.scenes))
	for _, sc := range plan.scenes {
		ids = append(ids, sc.ID)
	}
	want := map[string]bool{}
	for _, raw := range only {
		id := strings.TrimSpace(raw)
		if !slices.Contains(ids, id) {
			return dramaRunMeta{}, BadAuthRequest(fmt.Sprintf("场 %s 不在本章正文的场次里（%s）", drama.EchoSegmentID(id), strings.Join(ids, "、")))
		}
		want[id] = true
	}
	record, err := s.repo.DramaShotlistForUnit(projectID, unitID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return dramaRunMeta{}, BadAuthRequest("这一章还没有镜头表，先整章生成分段")
	}
	if err != nil {
		return dramaRunMeta{}, err
	}
	// 切场规则改成「连续同地点并成一场」之前写的镜头表：镜头还是旧场编号，和现在的场次（plan.scenes）对不上，留不住。
	if storedScriptLegacy(record.ScriptJSON) {
		unit, err := s.repo.ProjectUnit(projectID, unitID)
		if err != nil {
			return dramaRunMeta{}, err
		}
		if drama.ScenesRenumbered(drama.SplitScenes(chapterPlainText(unit.SourceText), unit.Title)) {
			return dramaRunMeta{}, BadAuthRequest("这一章的镜头表是场次规则更新前写的，场编号和现在的场次对不上，不能只重写几场；请整章重新生成")
		}
	}
	scenes, err := keptShotlistScenes(record)
	if err != nil {
		return dramaRunMeta{}, err
	}
	for scene := range scenes {
		if !slices.Contains(ids, scene) {
			return dramaRunMeta{}, BadAuthRequest(fmt.Sprintf("镜头表里有镜头的场 %s 对不上现在正文的场次（正文改过？），请整章重新生成", scene))
		}
	}
	for _, id := range ids {
		if _, ok := scenes[id]; !ok {
			want[id] = true
		}
	}
	run.OnlyScenes = run.OnlyScenes[:0]
	for _, id := range ids {
		if want[id] {
			run.OnlyScenes = append(run.OnlyScenes, id)
		}
	}
	run.BaseShotlistVersion = record.Version
	return run, nil
}

// keptShotlistScenes 把镜头表记录里的分镜行原文按镜头的场分好（分镜行与镜头表的镜头一一对应）。
func keptShotlistScenes(record *model.DramaShotlist) (map[string]dramaKeptScene, error) {
	var shotlist drama.Shotlist
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(record.ShotlistJSON), &shotlist); err != nil {
		return nil, fmt.Errorf("镜头表数据损坏: %w", err)
	}
	if strings.TrimSpace(record.RowsJSON) == "" || json.Unmarshal([]byte(record.RowsJSON), &rows) != nil || len(rows) != len(shotlist.Shots) {
		return nil, BadAuthRequest("这一章的镜头表没有对得上的分镜行原文，不能只重写几场；请整章重新生成")
	}
	out := map[string]dramaKeptScene{}
	for i, sh := range shotlist.Shots {
		var line bytes.Buffer
		if err := json.Compact(&line, rows[i]); err != nil {
			return nil, fmt.Errorf("分镜行原文损坏: %w", err)
		}
		kept := out[sh.Scene]
		kept.text += line.String() + "\n"
		kept.count++
		out[sh.Scene] = kept
	}
	return out, nil
}

// dramaRunKeptScenes 是这次运行里不重写、还没放进对话历史的场（场编号 → 原有镜头行）。作底的镜头表在任务建好后又改过
// （重新装段、分镜卡编辑、别的生成写了库）时拒绝，免得拿新内容和旧问题拼在一起。
func (s *Service) dramaRunKeptScenes(task model.Task, run dramaRunMeta, done int) (map[string]dramaKeptScene, error) {
	if len(run.OnlyScenes) == 0 {
		return nil, nil
	}
	pending := false
	for k := done + 1; k <= len(run.Scenes); k++ {
		pending = pending || !slices.Contains(run.OnlyScenes, run.Scenes[k-1].ID)
	}
	if !pending {
		return nil, nil
	}
	target, err := taskDramaStepTarget(task)
	if err != nil {
		return nil, err
	}
	record, err := s.repo.DramaShotlistForUnit(target.ProjectID, target.UnitID)
	if err != nil {
		return nil, fmt.Errorf("读不到本章镜头表，不能只重写几场：%w", err)
	}
	if record.Version != run.BaseShotlistVersion {
		return nil, fmt.Errorf("本章镜头表在任务建好后又改过（第 %d 版 → 第 %d 版），请重新点「重新生成问题场」", run.BaseShotlistVersion, record.Version)
	}
	scenes, err := keptShotlistScenes(record)
	if err != nil {
		return nil, err
	}
	for _, id := range run.OnlyScenes {
		delete(scenes, id)
	}
	return scenes, nil
}
