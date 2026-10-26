// 流程技能包的真跑评测与自动启用门槛（M6 第三期）：包上传、静态校验通过后，对评测集里包覆盖的风格档案的章节
// （再加清单 eval 点名的章节），用管理员选的默认文本模型真跑这一步——提示词与 /drama/steps/:step/prompt 同一条组装路径，
// 结果交给与章节页相同的解析器、资产绑定、镜头表检查和装段；再跟当前启用版本在同一组章节上的结果比，按步骤表里的门槛判定。
// 全部达标就自动启用并记审计日志；不达标标「未通过」，保留指标对比与逐章结果，管理员可以写明原因强制启用。
//
// 评测任务不一次投完：后台循环每 10 秒推进一次，全局同时在跑的评测任务不超过步骤表的 EvalConcurrency（多个评测排队共用），
// 前一个结束再投下一个；还没提交上游就失败（5xx、连不上）的任务隔一段时间重投，最多两次。评测记录只存模型选择，
// 其中的密钥按任务表同一规则加密。记录在 drama_eval_runs（stage 为步骤 ID）。

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
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
	// dramaStepEvalSlotPending 是还没投出（或等着重投）的任务位。
	dramaStepEvalSlotPending = "pending"
	dramaStepEvalTick        = 10 * time.Second
	// dramaStepEvalTimeout 是整次评测的上限：任务一个一个跑，十个任务加重投要留够时间。
	dramaStepEvalTimeout    = 6 * time.Hour
	dramaStepEvalLeaseScope = "drama-step-eval"
	// dramaStepEvalLookback 是找对照结果、版本最近评测时往回看的记录数。
	dramaStepEvalLookback       = 60
	dramaStepEvalReasonMaxRunes = 500
)

var dramaStepEvalInFlight = []string{DramaStepEvalQueued, DramaStepEvalRunning}

// dramaStepEvalRetryDelays 是没提交上游就失败的任务第几次重投前等多久（次数即上限）；测试里改成 0。
var dramaStepEvalRetryDelays = []time.Duration{30 * time.Second, 90 * time.Second}

// DramaStepEvalModel 是评测用的文本模型（后台页面带上管理员设置里的默认文本模型，与章节页「生成分段」同一套字段）。
type DramaStepEvalModel struct {
	Model          string         `json:"model"`
	LogicalModelID string         `json:"logicalModelId"`
	Config         map[string]any `json:"config"`
}

func (m DramaStepEvalModel) empty() bool {
	return strings.TrimSpace(m.Model) == "" && strings.TrimSpace(m.LogicalModelID) == ""
}

// dramaStepEvalTask 是一章一个角色（候选 / 对照）的任务位：TaskID 为空、Status 为 pending 表示还没投出或在等重投；
// Status 否则是后台循环最近一次看到的任务状态。Retries 是已经重投的次数，PreviousTaskIDs 是重投前失败的任务。
type dramaStepEvalTask struct {
	Chapter         string     `json:"chapter"`
	Role            string     `json:"role"`
	TaskID          string     `json:"taskId"`
	Status          string     `json:"status"`
	Retries         int        `json:"retries,omitempty"`
	RetryAt         *time.Time `json:"retryAt,omitempty"`
	LastError       string     `json:"lastError,omitempty"`
	PreviousTaskIDs []string   `json:"previousTaskIds,omitempty"`
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
	// TasksRunning 是已经投出、还没结束的任务；TasksWaiting 是排队等投（或等重投）的任务位；Retries 是累计重投次数。
	TasksRunning     int                    `json:"tasksRunning"`
	TasksWaiting     int                    `json:"tasksWaiting"`
	Retries          int                    `json:"retries"`
	Gate             *drama.StepGateVerdict `json:"gate,omitempty"`
	Result           *DramaStepEvalResult   `json:"result,omitempty"`
	Error            string                 `json:"error,omitempty"`
	Activation       string                 `json:"activation,omitempty"`
	ActivationReason string                 `json:"activationReason,omitempty"`
	ActivatedBy      string                 `json:"activatedBy,omitempty"`
	ActivatedAt      *time.Time             `json:"activatedAt,omitempty"`
	CreatedBy        string                 `json:"createdBy"`
	CreatedAt        time.Time              `json:"createdAt"`
	FinishedAt       *time.Time             `json:"finishedAt,omitempty"`
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
	// 先把每章的提示词组装一遍（数据或技能有问题就在记评测前报错）；任务由后台循环按并发上限逐个投出，投时再组装。
	var slots []dramaStepEvalTask
	for _, c := range chapters {
		for _, role := range []string{dramaStepEvalRoleCandidate, dramaStepEvalRoleBaseline} {
			pkg := candidate
			if role == dramaStepEvalRoleBaseline {
				if baseline == nil {
					continue
				}
				pkg = baseline
			}
			if _, err := dramaStepEvalTaskRequest(run.ID, step, c, role, pkg, sel); err != nil {
				return nil, err
			}
			slots = append(slots, dramaStepEvalTask{Chapter: c.ID, Role: role, Status: dramaStepEvalSlotPending})
		}
	}
	modelJSON, err := s.protectedDramaStepEvalModel(sel)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(result)
	encoded, _ := json.Marshal(slots)
	run.ResultJSON, run.TasksJSON, run.ModelJSON = string(raw), string(encoded), modelJSON
	if err := s.repo.CreateDramaEvalRun(run); err != nil {
		return nil, err
	}
	// 立刻推进一次：有空位就投出第一个任务，建任务失败（余额不足等）当场记进评测。
	s.advanceDramaStepEvals(context.Background())
	if latest, err := s.repo.DramaEvalRun(run.ID); err == nil {
		run = latest
	}
	view := dramaStepEvalViewOf(*run)
	return &view, nil
}

// dramaStepEvalTaskRequest 组装一个任务位的文本任务：与「生成分段」同一条提示词组装路径，metadata 标成评测任务。
func dramaStepEvalTaskRequest(runID string, step drama.Step, c drama.StepEvalChapter, role string, pkg *skills.StepPackage, sel DramaStepEvalModel) (CreateTaskRequest, error) {
	in, err := loadDramaEvalChapter(c)
	if err != nil {
		return CreateTaskRequest{}, err
	}
	skillRef := stepSkillOf(pkg)
	usage := DramaStepUsage{
		Step: step.ID, StepLabel: step.Label, Contract: step.Contract, Bound: true, Profile: dramaProfileRef(c.Profile),
		Skill: DramaStepSkillRef{ID: skillRef.ID, Name: skillRef.Name, VersionID: skillRef.VersionID, Version: skillRef.Version, ContentHash: skillRef.ContentHash},
	}
	var plan *dramaStepPlan
	if step.ID == drama.StepScriptAdapt {
		// 剧本优化的评测：这一章改编成一集。
		plan, err = scriptAdaptPlan(in.project, []model.ProjectUnit{*in.unit}, drama.ScriptAdaptModeAdapt, 1, 0, nil, usage, skillRef)
	} else {
		plan, err = chapterShotsPlan(in.project, in.unit, in.assets, usage, skillRef)
	}
	if err != nil {
		return CreateTaskRequest{}, fmt.Errorf("评测章节 %s 组装提示词：%w", c.ID, err)
	}
	req := dramaStepTaskRequest("", "", plan, dramaStepTaskOptions{Model: sel.Model, LogicalModelID: sel.LogicalModelID, Config: sel.Config})
	metadata := req.Input["metadata"].(map[string]any)
	delete(metadata, "domainProjectId")
	delete(metadata, "chapterId")
	metadata["source"] = dramaStepEvalTaskSource
	metadata["dramaStepEval"] = map[string]any{"runId": runID, "chapter": c.ID, "role": role}
	// 评测与章节页一样按场续写，门槛量的是用户实际跑出来的结果。
	metadata["dramaRun"] = newDramaRunMeta(plan, "")
	return req, nil
}

// protectedDramaStepEvalModel 把模型选择存进评测记录：config 里的密钥按任务表同一规则加密。
func (s *Service) protectedDramaStepEvalModel(sel DramaStepEvalModel) (string, error) {
	raw, err := json.Marshal(sel)
	if err != nil {
		return "", err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	if err := s.protectTaskSecrets(doc); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(doc)
	return string(encoded), err
}

func (s *Service) dramaStepEvalModelOf(run *model.DramaEvalRun) (DramaStepEvalModel, error) {
	var doc any
	if err := json.Unmarshal([]byte(run.ModelJSON), &doc); err != nil {
		return DramaStepEvalModel{}, fmt.Errorf("评测记录没有可用的模型选择：%w", err)
	}
	if err := s.decryptTaskSecrets(doc); err != nil {
		return DramaStepEvalModel{}, err
	}
	raw, _ := json.Marshal(doc)
	var sel DramaStepEvalModel
	if err := json.Unmarshal(raw, &sel); err != nil || sel.empty() {
		return DramaStepEvalModel{}, fmt.Errorf("评测记录没有可用的模型选择")
	}
	return sel, nil
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

// advanceDramaStepEvals 推进排队、运行中的步骤评测：先按任务现状更新每个任务位（没提交上游就失败的排上重投），
// 再按评测开始的先后在全局并发上限内投出等着的任务位，最后落定全部结束的评测。多个实例时只有拿到租约的一个推进，
// 落定用条件更新防重复。
func (s *Service) advanceDramaStepEvals(ctx context.Context) {
	if s.coordinator != nil {
		lease, acquired, err := s.coordinator.AcquireLease(ctx, dramaStepEvalLeaseScope, 1, time.Minute)
		if err != nil || !acquired {
			return
		}
		defer lease.Release()
	}
	runs, err := s.repo.DramaEvalRunsWithStatus(dramaStepEvalInFlight, 50)
	if err != nil {
		log.Printf("drama step eval: %v", err)
		return
	}
	now := time.Now()
	type pass struct {
		run     *model.DramaEvalRun
		tasks   []dramaStepEvalTask
		changed bool
		failed  string
	}
	passes := make([]*pass, 0, len(runs))
	active := 0
	for i := range runs {
		p := &pass{run: &runs[i]}
		_ = json.Unmarshal([]byte(p.run.TasksJSON), &p.tasks)
		p.changed, err = s.refreshDramaStepEvalTasks(p.tasks, now)
		if err != nil {
			log.Printf("drama step eval run=%s: %v", p.run.ID, err)
			continue
		}
		for _, t := range p.tasks {
			if t.TaskID != "" && !dramaStepEvalTaskDone(t.Status) {
				active++
			}
		}
		passes = append(passes, p)
	}
	for _, p := range passes {
		step, ok := drama.StepByID(p.run.Stage)
		if !ok {
			p.failed = fmt.Sprintf("步骤 %s 不在线上步骤表里", p.run.Stage)
			continue
		}
		limit := dramaStepEvalConcurrency(step)
		for i := range p.tasks {
			t := &p.tasks[i]
			if t.Status != dramaStepEvalSlotPending || (t.RetryAt != nil && t.RetryAt.After(now)) || active >= limit {
				continue
			}
			taskID, err := s.dispatchDramaStepEvalTask(p.run, step, *t)
			if err != nil {
				p.failed = fmt.Sprintf("建评测任务失败（%s）：%s", t.Chapter, err.Error())
				break
			}
			t.TaskID, t.Status, t.RetryAt, p.changed = taskID, string(model.TaskStatusQueued), nil, true
			active++
		}
	}
	for _, p := range passes {
		if err := s.settleDramaStepEval(p.run, p.tasks, p.changed, p.failed, now); err != nil {
			log.Printf("drama step eval run=%s: %v", p.run.ID, err)
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

// dramaStepEvalConcurrency 是这一步评测全局同时在跑的任务数：步骤表的 EvalConcurrency，夹在 1～StepEvalMaxConcurrency。
func dramaStepEvalConcurrency(step drama.Step) int {
	return min(max(step.EvalConcurrency, 1), drama.StepEvalMaxConcurrency)
}

// refreshDramaStepEvalTasks 按任务现状更新任务位；没提交上游就因 5xx、连不上失败的任务排上重投（重投次数用尽才算这一章失败）。
func (s *Service) refreshDramaStepEvalTasks(tasks []dramaStepEvalTask, now time.Time) (bool, error) {
	changed := false
	for i := range tasks {
		t := &tasks[i]
		if t.TaskID == "" || dramaStepEvalTaskDone(t.Status) {
			continue
		}
		status := dramaStepEvalTaskMissing
		task, err := s.repo.Task(t.TaskID)
		switch {
		case err == nil:
			status = string(task.Status)
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return changed, err
		}
		if status == string(model.TaskStatusFailed) && t.Retries < len(dramaStepEvalRetryDelays) && dramaStepEvalRetryable(task) {
			at := now.Add(dramaStepEvalRetryDelays[t.Retries])
			t.PreviousTaskIDs = append(t.PreviousTaskIDs, t.TaskID)
			t.LastError = truncateRunes(task.Error, 300)
			t.TaskID, t.Status, t.RetryAt = "", dramaStepEvalSlotPending, &at
			t.Retries++
			changed = true
			continue
		}
		if status != t.Status {
			t.Status, changed = status, true
		}
	}
	return changed, nil
}

var dramaStepEvalRetryableError = regexp.MustCompile(`HTTP 5\d\d|连接模型服务失败|模型服务响应超时`)

// dramaStepEvalRetryable 是失败的任务能不能重投：诊断说没提交上游，且原因是上游 5xx、连不上或超时（524 的说明里写着
// 可能已在执行，不重投）。已经提交上游的不重投，免得重复计费。
func dramaStepEvalRetryable(task *model.Task) bool {
	diagnostic := taskExecutionDiagnostic(task)
	if diagnostic == nil || diagnostic.TaskSubmitted || diagnostic.Code == string(ReasonTextOutputTruncated) {
		return false
	}
	message := firstNonEmpty(task.Error, diagnostic.SafeMessage)
	return !strings.Contains(message, "524") && dramaStepEvalRetryableError.MatchString(message)
}

// dispatchDramaStepEvalTask 投出一个任务位：按评测记录里的模型选择和版本重新组装提示词（与开始评测时校验过的一样）。
func (s *Service) dispatchDramaStepEvalTask(run *model.DramaEvalRun, step drama.Step, slot dramaStepEvalTask) (string, error) {
	c, ok := step.EvalChapter(slot.Chapter)
	if !ok {
		return "", fmt.Errorf("评测章节 %s 不在评测集里", slot.Chapter)
	}
	versionID := run.SkillVersionID
	if slot.Role == dramaStepEvalRoleBaseline {
		versionID = run.BaselineSkillVersionID
	}
	pkg, err := s.skillDomain().StepSkillPackageVersion(run.SkillID, versionID)
	if err != nil {
		return "", err
	}
	sel, err := s.dramaStepEvalModelOf(run)
	if err != nil {
		return "", err
	}
	req, err := dramaStepEvalTaskRequest(run.ID, step, c, slot.Role, pkg, sel)
	if err != nil {
		return "", err
	}
	task, err := s.CreateTask(run.CreatedBy, req)
	if err != nil {
		return "", err
	}
	return task.ID, nil
}

// settleDramaStepEval 写回任务位和状态；投任务失败就判出错，全部结束就落定。
func (s *Service) settleDramaStepEval(run *model.DramaEvalRun, tasks []dramaStepEvalTask, changed bool, failed string, now time.Time) error {
	if failed != "" {
		return s.failDramaStepEval(run, tasks, failed)
	}
	done, started := true, false
	for _, t := range tasks {
		if !dramaStepEvalTaskDone(t.Status) {
			done = false
		}
		if t.TaskID != "" && t.Status != string(model.TaskStatusQueued) || len(t.PreviousTaskIDs) > 0 {
			started = true
		}
	}
	if len(tasks) == 0 {
		return s.failDramaStepEval(run, tasks, "评测没有任务，请重跑评测")
	}
	if done {
		return s.finishDramaStepEval(run, tasks)
	}
	if now.Sub(run.CreatedAt) > dramaStepEvalTimeout {
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
		res, infraErr := dramaStepEvalChapterResult(step, c, t.TaskID, task, phrases)
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

// dramaStepEvalChapterResult 把一章的任务结果交给解析器、资产绑定、镜头表检查和装段，得出门槛指标（剧本优化是剧集解析和检查）。
// 第二个返回值非空表示任务没跑成（上游失败、任务不见了、数据坏了），不是包的问题。
func dramaStepEvalChapterResult(step drama.Step, c drama.StepEvalChapter, taskID string, task *model.Task, phrases *drama.CompileProfile) (drama.StepEvalChapterResult, string) {
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
	var output struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(firstNonEmpty(task.ResultJSON, "{}")), &output)
	if step.ID == drama.StepScriptAdapt {
		return scriptAdaptEvalResult(r, in, output.Text), ""
	}
	rows, _, err := drama.StoryboardRowsFromText(output.Text)
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

// scriptAdaptEvalResult 是剧本优化一章的指标：改编出的一集带场次头、检查没有 error 才算解析成功；Rows 是集数，Shots 是场数。
func scriptAdaptEvalResult(r drama.StepEvalChapterResult, in dramaEvalChapterInput, text string) drama.StepEvalChapterResult {
	episodes, stats := drama.ScriptAdaptEpisodesFromText(text)
	paragraphs := map[string]int{in.unit.ID: len(drama.ScriptAdaptParagraphs(chapterEditorText(in.unit.SourceText)))}
	report := drama.CheckScriptAdapt(episodes, drama.ScriptAdaptCheckContext{Mode: drama.ScriptAdaptModeAdapt, Paragraphs: paragraphs, Episodes: 1})
	r.Rows, r.Errors, r.Warnings = len(episodes), report.Errors, report.Warnings
	for _, ep := range episodes {
		r.Shots += len(ep.Scenes)
	}
	for _, item := range report.Items {
		if r.Rules == nil {
			r.Rules = map[string]int{}
		}
		r.Rules[item.Rule]++
	}
	r.Parsed = stats.Episodes > 0 && stats.Episodes == stats.Blocks && report.Errors == 0
	if !r.Parsed {
		r.Failure = fmt.Sprintf("解析出 %d 集，%d 集带场次头，检查 %d 个 error", stats.Blocks, stats.Episodes, report.Errors)
	}
	return r
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
		view.Retries += t.Retries
		switch {
		case dramaStepEvalTaskDone(t.Status):
			view.TasksDone++
		case t.TaskID == "":
			view.TasksWaiting++
		default:
			view.TasksRunning++
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
