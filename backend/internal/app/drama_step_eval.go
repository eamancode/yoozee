// 流程技能包的真跑评测与自动启用门槛（M6 第三期）：包上传、静态校验通过后，对评测集里包覆盖的风格档案的章节
// （再加清单 eval 点名的章节），用管理员选的默认文本模型真跑这一步——提示词与 /drama/steps/:step/prompt 同一条组装路径，
// 结果交给与章节页相同的解析器、资产绑定、镜头表检查和装段；再跟当前启用版本在同一组章节上的结果比，按步骤表里的门槛判定。
// 全部达标就自动启用并记审计日志；不达标标「未通过」，保留指标对比与逐章结果，管理员可以写明原因强制启用。
//
// 评测任务在请求里同步建好（任务输入按任务表的规则加密，评测记录里不存渠道配置），之后由后台循环按任务状态推进、落定。
// 记录在 drama_eval_runs（stage 为步骤 ID）。

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/skills"
)

// 步骤评测的状态。
const (
	DramaStepEvalQueued  = "queued"
	DramaStepEvalRunning = "running"
	DramaStepEvalPassed  = "passed"
	DramaStepEvalFailed  = "failed"
	// DramaStepEvalError 是评测没跑完（任务上游失败、超时、建不出任务），不算包的问题，可以重跑。
	DramaStepEvalError = "error"
)

const (
	// dramaStepEvalTaskSource 是评测任务的 metadata.source（章节页按 short-drama-chapter-storyboard 认任务，评测任务不混进去）。
	dramaStepEvalTaskSource    = "drama-step-eval"
	dramaStepEvalRoleCandidate = "candidate"
	dramaStepEvalRoleBaseline  = "baseline"
	dramaStepEvalTaskMissing   = "missing"
	dramaStepEvalTick          = 10 * time.Second
	dramaStepEvalTimeout       = 2 * time.Hour
	dramaStepEvalLeaseScope    = "drama-step-eval"
	// dramaStepEvalLookback 是找对照结果、版本最近评测时往回看的记录数。
	dramaStepEvalLookback       = 60
	dramaStepEvalReasonMaxRunes = 500
)

var dramaStepEvalInFlight = []string{DramaStepEvalQueued, DramaStepEvalRunning}

// DramaStepEvalModel 是评测用的文本模型（后台页面带上管理员设置里的默认文本模型，与章节页「生成分段」同一套字段）。
type DramaStepEvalModel struct {
	Model          string         `json:"model"`
	LogicalModelID string         `json:"logicalModelId"`
	Config         map[string]any `json:"config"`
}

func (m DramaStepEvalModel) empty() bool {
	return strings.TrimSpace(m.Model) == "" && strings.TrimSpace(m.LogicalModelID) == ""
}

// dramaStepEvalTask 是一章一个角色（候选 / 对照）的文本任务；Status 是后台循环最近一次看到的任务状态。
type dramaStepEvalTask struct {
	Chapter string `json:"chapter"`
	Role    string `json:"role"`
	TaskID  string `json:"taskId"`
	Status  string `json:"status"`
}

// DramaStepEvalResult 是步骤评测的逐章结果（drama_eval_runs.result_json）。
type DramaStepEvalResult struct {
	Candidate []drama.StepEvalChapterResult `json:"candidate"`
	Baseline  []drama.StepEvalChapterResult `json:"baseline"`
	// BaselineRunID 是对照结果复用了哪次评测；为空表示这次一起跑了对照（或候选就是启用版本，自己对照自己）。
	BaselineRunID string `json:"baselineRunId,omitempty"`
}

// DramaStepEvalView 是一次步骤评测（后台版本列表里每个版本带最近一次）。
type DramaStepEvalView struct {
	ID                     string                  `json:"id"`
	Step                   string                  `json:"step"`
	StepLabel              string                  `json:"stepLabel"`
	SkillID                string                  `json:"skillId"`
	SkillVersionID         string                  `json:"skillVersionId"`
	BaselineSkillVersionID string                  `json:"baselineSkillVersionId,omitempty"`
	Status                 string                  `json:"status"`
	Model                  string                  `json:"model"`
	Chapters               []drama.StepEvalChapter `json:"chapters"`
	TasksDone              int                     `json:"tasksDone"`
	TasksTotal             int                     `json:"tasksTotal"`
	Gate                   *drama.StepGateVerdict  `json:"gate,omitempty"`
	Result                 *DramaStepEvalResult    `json:"result,omitempty"`
	Error                  string                  `json:"error,omitempty"`
	Activation             string                  `json:"activation,omitempty"`
	ActivationReason       string                  `json:"activationReason,omitempty"`
	ActivatedBy            string                  `json:"activatedBy,omitempty"`
	ActivatedAt            *time.Time              `json:"activatedAt,omitempty"`
	CreatedBy              string                  `json:"createdBy"`
	CreatedAt              time.Time               `json:"createdAt"`
	FinishedAt             *time.Time              `json:"finishedAt,omitempty"`
}

// StartDramaStepEval 对技能包的一个版本开始评测（上传后自动开始，也可以在版本列表里重跑）。
func (s *Service) StartDramaStepEval(actor *model.User, skillID string, versionID string, sel DramaStepEvalModel) (*DramaStepEvalView, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if sel.empty() {
		return nil, BadAuthRequest("请选择评测用的文本模型（后台页面用你设置里的默认文本模型）")
	}
	skill, err := s.repo.Skill(strings.TrimSpace(skillID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("技能包不存在")
	}
	if err != nil {
		return nil, err
	}
	version, err := s.repo.SkillVersion(strings.TrimSpace(versionID))
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && version.SkillID != skill.ID) {
		return nil, NotFound("这个技能包没有该版本")
	}
	if err != nil {
		return nil, err
	}
	if version.Origin != skills.SkillOriginPackage {
		return nil, BadAuthRequest(fmt.Sprintf("版本 %s 不是包版本，不能评测", version.VersionLabel))
	}
	manifest, problems := drama.ParseStepPackageManifest([]byte(version.Manifest))
	if len(problems) > 0 {
		return nil, BadAuthRequest("版本清单无效：" + problems[0].Message)
	}
	step, ok := drama.StepByID(manifest.Step)
	if !ok {
		return nil, BadAuthRequest(fmt.Sprintf("步骤 %s 不在线上步骤表里", manifest.Step))
	}
	chapters := step.EvalChapters(manifest.Profiles, manifest.Eval)
	if len(chapters) == 0 {
		return nil, BadAuthRequest("这个包覆盖的风格档案在评测集里没有章节")
	}
	recent, err := s.repo.DramaStepEvalRunsForSkill(skill.ID, dramaStepEvalLookback)
	if err != nil {
		return nil, err
	}
	for _, r := range recent {
		if r.SkillVersionID == version.ID && slices.Contains(dramaStepEvalInFlight, r.Status) {
			return nil, BadAuthRequest(fmt.Sprintf("版本 %s 正在评测，等这次跑完再重跑", version.VersionLabel))
		}
	}
	candidate, err := s.skillDomain().StepSkillPackageVersion(skill.ID, version.ID)
	if err != nil {
		return nil, err
	}
	modelLabel := firstNonEmpty(strings.TrimSpace(sel.Model), strings.TrimSpace(sel.LogicalModelID))
	run := &model.DramaEvalRun{
		ID: newID(), Stage: step.ID, Model: truncateRunes(modelLabel, 64), SkillID: skill.ID, SkillVersionID: version.ID,
		BaselineSkillVersionID: skill.CurrentVersionID, Status: DramaStepEvalQueued, CreatedBy: actor.ID, CreatedAt: time.Now(),
	}
	ids := make([]string, 0, len(chapters))
	for _, c := range chapters {
		ids = append(ids, c.ID)
	}
	unitIDs, _ := json.Marshal(ids)
	run.UnitIDsJSON = string(unitIDs)
	result := DramaStepEvalResult{Candidate: []drama.StepEvalChapterResult{}, Baseline: []drama.StepEvalChapterResult{}}
	var baseline *skills.StepPackage
	if run.BaselineSkillVersionID != "" && run.BaselineSkillVersionID != version.ID {
		if reused, runID := reusableDramaStepBaseline(recent, run.BaselineSkillVersionID, run.Model, chapters); reused != nil {
			result.Baseline, result.BaselineRunID = reused, runID
		} else if baseline, err = s.skillDomain().StepSkillPackageVersion(skill.ID, run.BaselineSkillVersionID); err != nil {
			return nil, err
		}
	}
	// 先把每章的提示词组装好（数据或技能有问题就在建任务前报错），再记评测、建任务。
	type plannedTask struct {
		chapter, role string
		req           CreateTaskRequest
	}
	var planned []plannedTask
	for _, c := range chapters {
		in, err := loadDramaEvalChapter(c)
		if err != nil {
			return nil, err
		}
		for _, role := range []string{dramaStepEvalRoleCandidate, dramaStepEvalRoleBaseline} {
			pkg := candidate
			if role == dramaStepEvalRoleBaseline {
				if baseline == nil {
					continue
				}
				pkg = baseline
			}
			skillRef := stepSkillOf(pkg)
			usage := DramaStepUsage{
				Step: step.ID, StepLabel: step.Label, Contract: step.Contract, Bound: true, Profile: dramaProfileRef(c.Profile),
				Skill: DramaStepSkillRef{ID: skillRef.ID, Name: skillRef.Name, VersionID: skillRef.VersionID, Version: skillRef.Version, ContentHash: skillRef.ContentHash},
			}
			plan, err := chapterShotsPlan(in.project, in.unit, in.assets, usage, skillRef)
			if err != nil {
				return nil, fmt.Errorf("评测章节 %s 组装提示词：%w", c.ID, err)
			}
			req := dramaStepTaskRequest("", "", plan, DramaStepRunRequest{Model: sel.Model, LogicalModelID: sel.LogicalModelID, Config: sel.Config})
			metadata := req.Input["metadata"].(map[string]any)
			delete(metadata, "domainProjectId")
			delete(metadata, "chapterId")
			metadata["source"] = dramaStepEvalTaskSource
			metadata["dramaStepEval"] = map[string]any{"runId": run.ID, "chapter": c.ID, "role": role}
			planned = append(planned, plannedTask{chapter: c.ID, role: role, req: req})
		}
	}
	raw, _ := json.Marshal(result)
	run.ResultJSON, run.TasksJSON = string(raw), "[]"
	if err := s.repo.CreateDramaEvalRun(run); err != nil {
		return nil, err
	}
	tasks := make([]dramaStepEvalTask, 0, len(planned))
	for _, p := range planned {
		task, err := s.CreateTask(actor.ID, p.req)
		if err != nil {
			encoded, _ := json.Marshal(tasks)
			message := fmt.Sprintf("建评测任务失败（%s）：%s", p.chapter, err.Error())
			now := time.Now()
			if _, updateErr := s.repo.UpdateDramaEvalRunFrom(run.ID, dramaStepEvalInFlight, map[string]any{"status": DramaStepEvalError, "tasks_json": string(encoded), "error": truncateRunes(message, 1000), "finished_at": &now}); updateErr != nil {
				return nil, errors.Join(err, updateErr)
			}
			return nil, err
		}
		tasks = append(tasks, dramaStepEvalTask{Chapter: p.chapter, Role: p.role, TaskID: task.ID, Status: string(task.Status)})
	}
	encoded, _ := json.Marshal(tasks)
	if err := s.repo.UpdateDramaEvalRun(run.ID, map[string]any{"tasks_json": string(encoded)}); err != nil {
		return nil, err
	}
	run.TasksJSON = string(encoded)
	view := dramaStepEvalViewOf(*run)
	return &view, nil
}

// reusableDramaStepBaseline 找启用版本在同一模型、同一组章节上最近一次全部解析成功的结果（作为候选跑的，或一起跑的对照）。
func reusableDramaStepBaseline(recent []model.DramaEvalRun, versionID string, modelLabel string, chapters []drama.StepEvalChapter) ([]drama.StepEvalChapterResult, string) {
	for _, r := range recent {
		if r.Model != modelLabel || (r.Status != DramaStepEvalPassed && r.Status != DramaStepEvalFailed) {
			continue
		}
		var result DramaStepEvalResult
		if json.Unmarshal([]byte(r.ResultJSON), &result) != nil {
			continue
		}
		var source []drama.StepEvalChapterResult
		switch {
		case r.SkillVersionID == versionID:
			source = result.Candidate
		case r.BaselineSkillVersionID == versionID && result.BaselineRunID == "":
			source = result.Baseline
		default:
			continue
		}
		byChapter := map[string]drama.StepEvalChapterResult{}
		for _, c := range source {
			byChapter[c.Chapter] = c
		}
		out := make([]drama.StepEvalChapterResult, 0, len(chapters))
		for _, c := range chapters {
			if got, ok := byChapter[c.ID]; ok && got.Parsed {
				out = append(out, got)
			}
		}
		if len(out) == len(chapters) {
			return out, r.ID
		}
	}
	return nil, ""
}

// dramaEvalChapterInput 是一章评测数据：项目、章节、资产清单（与接口 JSON 同形），项目的风格档案按评测集改成这一章的档案。
type dramaEvalChapterInput struct {
	project *model.Project
	unit    *model.ProjectUnit
	assets  []ProjectAssetSummary
}

func loadDramaEvalChapter(c drama.StepEvalChapter) (dramaEvalChapterInput, error) {
	files, err := drama.ReadEvalChapter(c.ID)
	if err != nil {
		return dramaEvalChapterInput{}, err
	}
	in := dramaEvalChapterInput{project: &model.Project{}, unit: &model.ProjectUnit{}}
	for name, pair := range map[string]struct {
		raw  []byte
		into any
	}{"project.json": {files.Project, in.project}, "unit.json": {files.Unit, in.unit}, "assets.json": {files.Assets, &in.assets}} {
		if err := json.Unmarshal(pair.raw, pair.into); err != nil {
			return dramaEvalChapterInput{}, fmt.Errorf("评测章节 %s 的 %s 无效：%w", c.ID, name, err)
		}
	}
	in.project.StyleProfile = c.Profile
	return in, nil
}

// stepSkillOf 把技能域读出的版本文件换成提示词组装用的形状。
func stepSkillOf(pkg *skills.StepPackage) drama.StepSkill {
	skill := drama.StepSkill{ID: pkg.SkillID, Name: pkg.Name, VersionID: pkg.VersionID, Version: pkg.Version, ContentHash: pkg.ContentHash}
	for _, f := range pkg.Files {
		skill.Files = append(skill.Files, drama.StepSkillFile{Path: f.Path, Kind: f.Kind, Binary: f.Binary, Content: f.Content, SHA256: f.SHA256})
	}
	return skill
}

// ---------------------------------------------------------------- 后台推进

func (s *Service) startDramaStepEvalLoop() {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(dramaStepEvalTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil || s.IsDraining() {
					return
				}
				s.advanceDramaStepEvals(ctx)
			}
		}
	})
}

// advanceDramaStepEvals 推进排队、运行中的步骤评测：多个实例时只有拿到租约的一个推进，落定用条件更新防重复。
func (s *Service) advanceDramaStepEvals(ctx context.Context) {
	if s.coordinator != nil {
		lease, acquired, err := s.coordinator.AcquireLease(ctx, dramaStepEvalLeaseScope, 1, time.Minute)
		if err != nil || !acquired {
			return
		}
		defer lease.Release()
	}
	runs, err := s.repo.DramaEvalRunsWithStatus(dramaStepEvalInFlight, 20)
	if err != nil {
		log.Printf("drama step eval: %v", err)
		return
	}
	for i := range runs {
		if err := s.advanceDramaStepEval(&runs[i]); err != nil {
			log.Printf("drama step eval run=%s: %v", runs[i].ID, err)
		}
	}
}

func dramaStepEvalTaskDone(status string) bool {
	switch status {
	case string(model.TaskStatusSucceeded), string(model.TaskStatusFailed), string(model.TaskStatusCancelled), dramaStepEvalTaskMissing:
		return true
	}
	return false
}

func (s *Service) advanceDramaStepEval(run *model.DramaEvalRun) error {
	var tasks []dramaStepEvalTask
	_ = json.Unmarshal([]byte(run.TasksJSON), &tasks)
	if len(tasks) == 0 {
		// 建任务的请求还没写回任务清单（或中途退出）：等一会儿，太久就判出错。
		if time.Since(run.CreatedAt) > 10*time.Minute {
			return s.failDramaStepEval(run, tasks, "评测任务没有建出来，请重跑评测")
		}
		return nil
	}
	changed, started, done := false, false, true
	for i := range tasks {
		if !dramaStepEvalTaskDone(tasks[i].Status) {
			status := dramaStepEvalTaskMissing
			task, err := s.repo.Task(tasks[i].TaskID)
			switch {
			case err == nil:
				status = string(task.Status)
			case !errors.Is(err, gorm.ErrRecordNotFound):
				return err
			}
			if status != tasks[i].Status {
				tasks[i].Status, changed = status, true
			}
		}
		if !dramaStepEvalTaskDone(tasks[i].Status) {
			done = false
		}
		if tasks[i].Status != string(model.TaskStatusQueued) {
			started = true
		}
	}
	if done {
		return s.finishDramaStepEval(run, tasks)
	}
	if time.Since(run.CreatedAt) > dramaStepEvalTimeout {
		return s.failDramaStepEval(run, tasks, fmt.Sprintf("评测超过 %d 小时没跑完", int(dramaStepEvalTimeout.Hours())))
	}
	status := DramaStepEvalQueued
	if started {
		status = DramaStepEvalRunning
	}
	if !changed && status == run.Status {
		return nil
	}
	encoded, _ := json.Marshal(tasks)
	_, err := s.repo.UpdateDramaEvalRunFrom(run.ID, dramaStepEvalInFlight, map[string]any{"status": status, "tasks_json": string(encoded)})
	return err
}

func (s *Service) failDramaStepEval(run *model.DramaEvalRun, tasks []dramaStepEvalTask, message string) error {
	encoded, _ := json.Marshal(tasks)
	now := time.Now()
	_, err := s.repo.UpdateDramaEvalRunFrom(run.ID, dramaStepEvalInFlight, map[string]any{"status": DramaStepEvalError, "tasks_json": string(encoded), "error": truncateRunes(message, 1000), "finished_at": &now})
	return err
}

// finishDramaStepEval 在全部任务结束后逐章算指标、判门槛、落定；通过就自动启用。
func (s *Service) finishDramaStepEval(run *model.DramaEvalRun, tasks []dramaStepEvalTask) error {
	step, ok := drama.StepByID(run.Stage)
	if !ok {
		return s.failDramaStepEval(run, tasks, fmt.Sprintf("步骤 %s 不在线上步骤表里", run.Stage))
	}
	var result DramaStepEvalResult
	_ = json.Unmarshal([]byte(run.ResultJSON), &result)
	result.Candidate = []drama.StepEvalChapterResult{}
	if result.BaselineRunID == "" {
		result.Baseline = []drama.StepEvalChapterResult{}
	}
	phrases, err := s.activeCompileProfile()
	if err != nil {
		return err
	}
	var infra []string
	for _, t := range tasks {
		c, ok := step.EvalChapter(t.Chapter)
		if !ok {
			return s.failDramaStepEval(run, tasks, fmt.Sprintf("评测章节 %s 不在评测集里", t.Chapter))
		}
		var task *model.Task
		if t.Status != dramaStepEvalTaskMissing {
			if task, err = s.repo.Task(t.TaskID); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		res, infraErr := dramaStepEvalChapterResult(c, t.TaskID, task, phrases)
		if infraErr != "" {
			infra = append(infra, fmt.Sprintf("%s（%s）：%s", c.Label, map[string]string{dramaStepEvalRoleCandidate: "候选", dramaStepEvalRoleBaseline: "对照"}[t.Role], infraErr))
		}
		if t.Role == dramaStepEvalRoleBaseline {
			result.Baseline = append(result.Baseline, res)
		} else {
			result.Candidate = append(result.Candidate, res)
		}
	}
	if run.BaselineSkillVersionID == "" || run.BaselineSkillVersionID == run.SkillVersionID {
		result.Baseline = result.Candidate
	}
	verdict := step.Gate.Evaluate(result.Candidate, result.Baseline)
	status, message := DramaStepEvalFailed, ""
	switch {
	case len(infra) > 0:
		status, message = DramaStepEvalError, "评测任务没跑成，不算包的问题，可以重跑："+strings.Join(infra, "；")
	case verdict.Passed:
		status = DramaStepEvalPassed
	}
	resultJSON, _ := json.Marshal(result)
	gateJSON, _ := json.Marshal(verdict)
	tasksJSON, _ := json.Marshal(tasks)
	now := time.Now()
	updated, err := s.repo.UpdateDramaEvalRunFrom(run.ID, dramaStepEvalInFlight, map[string]any{
		"status": status, "result_json": string(resultJSON), "gate_json": string(gateJSON), "tasks_json": string(tasksJSON),
		"error": truncateRunes(message, 1000), "finished_at": &now,
	})
	if err != nil || !updated {
		return err
	}
	if status == DramaStepEvalPassed {
		return s.autoActivateDramaStepPackage(run, verdict)
	}
	return nil
}

// dramaStepEvalChapterResult 把一章的任务结果交给解析器、资产绑定、镜头表检查和装段，得出门槛指标。
// 第二个返回值非空表示任务没跑成（上游失败、任务不见了、数据坏了），不是包的问题。
func dramaStepEvalChapterResult(c drama.StepEvalChapter, taskID string, task *model.Task, phrases *drama.CompileProfile) (drama.StepEvalChapterResult, string) {
	r := drama.StepEvalChapterResult{Chapter: c.ID, Label: c.Label, Profile: c.Profile, TaskID: taskID}
	if task == nil {
		r.Failure = "评测任务不见了"
		return r, r.Failure
	}
	in, err := loadDramaEvalChapter(c)
	if err != nil {
		r.Failure = err.Error()
		return r, r.Failure
	}
	pc := projectDramaContext(in.project)
	pc.Episode = 1
	r.TargetSeconds = pc.targetSeconds()
	switch task.Status {
	case model.TaskStatusSucceeded:
	case model.TaskStatusFailed, model.TaskStatusCancelled:
		r.Failure = firstNonEmpty(strings.TrimSpace(task.Error), "任务失败")
		if diagnostic := taskExecutionDiagnostic(task); diagnostic != nil && diagnostic.Code == string(ReasonTextOutputTruncated) {
			r.Truncated = true
			return r, ""
		}
		return r, r.Failure
	default:
		r.Failure = "任务没有结束"
		return r, r.Failure
	}
	rows, err := drama.StoryboardRowsFromResult(task.ResultJSON)
	if err != nil {
		r.Failure = err.Error()
		return r, ""
	}
	bindDramaRowAssets(rows, in.assets)
	raws := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			r.Failure = err.Error()
			return r, ""
		}
		raws = append(raws, encoded)
	}
	parsed, err := parseShotlistRows(raws)
	if err != nil {
		r.Failure = err.Error()
		return r, ""
	}
	r.Parsed = true
	m, err := projectDramaModel(in.project)
	if err != nil {
		r.Parsed, r.Failure = false, err.Error()
		return r, r.Failure
	}
	var script *drama.Script
	if s := drama.ScriptFromText(chapterPlainText(in.unit.SourceText), in.unit.Title); len(s.Scenes) > 0 {
		script = &s
	}
	plan, err := planDramaShotlist(parsed, script, in.assets, map[string]bool{}, m, phrases, pc)
	var appErr *AppError
	if err != nil && !(errors.As(err, &appErr) && appErr.Reason == ReasonShotlistCheckFailed) {
		r.Parsed, r.Failure = false, err.Error()
		return r, r.Failure
	}
	r.Rows, r.SpecRows = plan.Stats.Rows, plan.Stats.Rows-plan.Stats.NoSpec
	r.LinesTotal, r.LinesExact = plan.Lines.Total, plan.Lines.Exact
	r.Errors, r.Warnings, r.Shots = plan.Report.Errors, plan.Report.Warnings, len(plan.Shotlist.Shots)
	for _, item := range plan.Report.Items {
		if r.Rules == nil {
			r.Rules = map[string]int{}
		}
		r.Rules[firstNonEmpty(item.Rule, "契约")]++
	}
	for _, sh := range plan.Shotlist.Shots {
		r.EstSeconds += float64(sh.Est)
	}
	r.Segments, r.PackProblems = len(plan.Segments), len(plan.Problems)
	for _, seg := range plan.Segments {
		r.SegmentSeconds += seg.Dur
	}
	return r, ""
}

// autoActivateDramaStepPackage 在评测通过后启用候选版本：评测期间启用版本换过、上传的人已不是管理员时不启用，写进评测的说明。
func (s *Service) autoActivateDramaStepPackage(run *model.DramaEvalRun, verdict drama.StepGateVerdict) error {
	note := func(message string) error {
		return s.repo.UpdateDramaEvalRun(run.ID, map[string]any{"error": message})
	}
	skill, err := s.repo.Skill(run.SkillID)
	if err != nil {
		return err
	}
	if skill.CurrentVersionID == run.SkillVersionID {
		return nil
	}
	if skill.CurrentVersionID != run.BaselineSkillVersionID {
		return note("评测期间启用版本已经换了，没有自动启用；需要的话请手动启用")
	}
	actor, err := s.repo.User(run.CreatedBy)
	if err != nil || actor.Role != model.UserRoleAdmin {
		return note("上传的账号已不是管理员，没有自动启用；需要的话请手动启用")
	}
	if _, err := s.skillDomain().ActivateFlowPackageVersion(actor, run.SkillID, run.SkillVersionID); err != nil {
		return note(truncateRunes("自动启用失败："+err.Error(), 1000))
	}
	now := time.Now()
	if err := s.repo.UpdateDramaEvalRun(run.ID, map[string]any{"activation": "auto", "activation_reason": "评测通过门槛，自动启用", "activated_by": actor.ID, "activated_at": &now}); err != nil {
		return err
	}
	version, _ := s.repo.SkillVersion(run.SkillVersionID)
	label := run.SkillVersionID
	if version != nil {
		label = version.VersionLabel
	}
	log.Printf("drama step eval run=%s: %s %s 过门槛，自动启用", run.ID, run.SkillID, label)
	return s.appendAdminAudit(actor, "drama_package.auto_activate", "skill_version", run.SkillVersionID,
		fmt.Sprintf("流程技能包 %s 版本 %s 评测通过门槛，自动启用", run.SkillID, label), map[string]any{"evalRunId": run.ID, "baselineVersionId": run.BaselineSkillVersionID, "gate": verdict})
}

// ---------------------------------------------------------------- 启用、视图

// FlowPackageActivateRequest 是启用请求：评测没通过（或没跑完、出错）的版本要 force 并写明原因；没评测过的历史版本（回退）直接启用。
type FlowPackageActivateRequest struct {
	Force  bool   `json:"force"`
	Reason string `json:"reason"`
}

// ActivateFlowPackageVersion 手动启用一个包版本（回退就是启用历史版本）；评测没通过的要强制并写明原因，记审计日志。
func (s *Service) ActivateFlowPackageVersion(actor *model.User, skillID string, versionID string, req FlowPackageActivateRequest) (*FlowPackageSummary, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	latest, err := s.latestDramaStepEvals(strings.TrimSpace(skillID))
	if err != nil {
		return nil, err
	}
	eval, evaluated := latest[strings.TrimSpace(versionID)]
	forced := evaluated && eval.Status != DramaStepEvalPassed
	reason := strings.TrimSpace(req.Reason)
	if forced {
		if !req.Force || reason == "" {
			err := NewAppError(http.StatusBadRequest, fmt.Sprintf("这个版本的评测%s，要强制启用请写明原因", dramaStepEvalStatusText(eval.Status)))
			err.Reason = ReasonPackageEvalNotPassed
			err.Details = map[string]any{"evalId": eval.ID, "status": eval.Status}
			return nil, err
		}
		if utf8.RuneCountInString(reason) > dramaStepEvalReasonMaxRunes {
			return nil, BadAuthRequest(fmt.Sprintf("强制启用的原因不能超过 %d 字", dramaStepEvalReasonMaxRunes))
		}
	}
	summary, err := s.skillDomain().ActivateFlowPackageVersion(actor, skillID, versionID)
	if err != nil || !forced {
		return summary, err
	}
	now := time.Now()
	if err := s.repo.UpdateDramaEvalRun(eval.ID, map[string]any{"activation": "forced", "activation_reason": reason, "activated_by": actor.ID, "activated_at": &now}); err != nil {
		return nil, err
	}
	if err := s.appendAdminAudit(actor, "drama_package.force_activate", "skill_version", versionID,
		fmt.Sprintf("流程技能包 %s 强制启用评测%s的版本：%s", skillID, dramaStepEvalStatusText(eval.Status), reason), map[string]any{"evalRunId": eval.ID, "status": eval.Status}); err != nil {
		return nil, err
	}
	return summary, nil
}

func dramaStepEvalStatusText(status string) string {
	switch status {
	case DramaStepEvalQueued:
		return "还在排队"
	case DramaStepEvalRunning:
		return "还在运行"
	case DramaStepEvalFailed:
		return "未通过"
	case DramaStepEvalError:
		return "出错没跑完"
	case DramaStepEvalPassed:
		return "已通过"
	}
	return "状态未知"
}

// latestDramaStepEvals 是一个技能包每个版本最近一次评测。
func (s *Service) latestDramaStepEvals(skillID string) (map[string]model.DramaEvalRun, error) {
	runs, err := s.repo.DramaStepEvalRunsForSkill(skillID, dramaStepEvalLookback)
	if err != nil {
		return nil, err
	}
	out := map[string]model.DramaEvalRun{}
	for _, r := range runs {
		if _, ok := out[r.SkillVersionID]; !ok {
			out[r.SkillVersionID] = r
		}
	}
	return out, nil
}

func dramaStepEvalViewOf(r model.DramaEvalRun) DramaStepEvalView {
	view := DramaStepEvalView{
		ID: r.ID, Step: r.Stage, SkillID: r.SkillID, SkillVersionID: r.SkillVersionID, BaselineSkillVersionID: r.BaselineSkillVersionID,
		Status: r.Status, Model: r.Model, Chapters: []drama.StepEvalChapter{}, Error: r.Error, Activation: r.Activation, ActivationReason: r.ActivationReason,
		ActivatedBy: r.ActivatedBy, ActivatedAt: r.ActivatedAt, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt,
	}
	step, _ := drama.StepByID(r.Stage)
	view.StepLabel = step.Label
	var ids []string
	_ = json.Unmarshal([]byte(r.UnitIDsJSON), &ids)
	for _, id := range ids {
		if c, ok := step.EvalChapter(id); ok {
			view.Chapters = append(view.Chapters, c)
		} else {
			view.Chapters = append(view.Chapters, drama.StepEvalChapter{ID: id, Label: id})
		}
	}
	var tasks []dramaStepEvalTask
	_ = json.Unmarshal([]byte(r.TasksJSON), &tasks)
	view.TasksTotal = len(tasks)
	for _, t := range tasks {
		if dramaStepEvalTaskDone(t.Status) {
			view.TasksDone++
		}
	}
	if r.GateJSON != "" {
		var verdict drama.StepGateVerdict
		if json.Unmarshal([]byte(r.GateJSON), &verdict) == nil {
			view.Gate = &verdict
		}
	}
	if !slices.Contains(dramaStepEvalInFlight, r.Status) && r.ResultJSON != "" {
		var result DramaStepEvalResult
		if json.Unmarshal([]byte(r.ResultJSON), &result) == nil {
			view.Result = &result
		}
	}
	return view
}

// FlowPackageVersionView 是后台版本列表的一行：静态校验之外再带最近一次评测。
type FlowPackageVersionView struct {
	FlowPackageVersionItem
	Eval *DramaStepEvalView `json:"eval,omitempty"`
}

// FlowPackageUploadResponse 是上传结果：版本已保存（未启用）；评测开始了 version.eval 是排队中的评测，没开始 EvalError 写原因。
type FlowPackageUploadResponse struct {
	Package   FlowPackageSummary     `json:"package"`
	Version   FlowPackageVersionView `json:"version"`
	EvalError string                 `json:"evalError,omitempty"`
}

func (s *Service) flowPackageVersionViews(skillID string, items []FlowPackageVersionItem) ([]FlowPackageVersionView, error) {
	latest, err := s.latestDramaStepEvals(skillID)
	if err != nil {
		return nil, err
	}
	out := make([]FlowPackageVersionView, 0, len(items))
	for _, item := range items {
		view := FlowPackageVersionView{FlowPackageVersionItem: item}
		if r, ok := latest[item.ID]; ok {
			eval := dramaStepEvalViewOf(r)
			view.Eval = &eval
		}
		out = append(out, view)
	}
	return out, nil
}
