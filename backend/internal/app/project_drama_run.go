// 短剧流程步骤的逐轮续写（M7.2）：一个运行 = 一个任务。worker 领到任务后在进程内按轮循环调模型（生成分段一轮一场，
// 剧本优化一轮一集；每一轮怎么说、怎么验由步骤表的 TurnPlan 定），每一轮带上前面各轮的 textHistory、各用一次分镜超时，
// 失败的那一轮自动重试（最多 2 次，指数退避）；所有轮次的输出追加到同一个任务的 text-events 流，前端只订阅一次。
// 每写完一轮把它存进任务 result，任务失败也保留，「从第 N 场 / 集继续」用它接着跑。
// 不建运行表：运行标识在 metadata.dramaRun，进度在 result.dramaRun。租约由 worker 的续租协程在整轮循环期间续着。

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

const (
	// dramaTurnMaxAttempts 是一轮最多调几次模型（首次 + 2 次重试）。
	dramaTurnMaxAttempts = 3
	// dramaTurnBackoffCap 是一次退避的上限，也计进任务总时限。
	dramaTurnBackoffCap = 30 * time.Second
)

// dramaTurnBackoffBase 是第一次重试前等多久，之后每次翻倍（测试里调小）。
var dramaTurnBackoffBase = 5 * time.Second

// errDramaTurnUnparsed 是一轮的输出解析不出来（模型写成了解释文字、剧集没有场次头等）；按临时失败重试。
var errDramaTurnUnparsed = errors.New("这一轮的输出解析不出来")

// dramaRunMeta 是任务 metadata.dramaRun：运行标识、作用的章节、切好的轮次（场次或集次），以及从哪一轮接着写。
type dramaRunMeta struct {
	RunID  string `json:"runId"`
	Step   string `json:"step"`
	UnitID string `json:"unitId,omitempty"`
	// Scenes 是各轮：生成分段是场（ID 是剧本场编号），剧本优化是集（ID 是集号）。
	Scenes []drama.ShotsScene `json:"scenes,omitempty"`
	// Sources 是项目级步骤点名的来源单元（剧本优化改编时校验每一集的 sources；调节奏、生成分段为空）。
	Sources []string `json:"sources,omitempty"`
	// Turns 是一共几轮（场数、集数；不到两场时为 1）。
	Turns int `json:"turns"`
	// ResumeFrom 是从第几轮开始写（从 1 数）；ResumeTaskID 是前面各轮所在的任务。
	ResumeFrom   int    `json:"resumeFrom"`
	ResumeTaskID string `json:"resumeTaskId,omitempty"`
}

// dramaRunTurn 是写完的一轮（一场或一集）。Prompt / Text 是续写和「从第 N 场继续」的对话历史，任务成功后从结果里去掉。
type dramaRunTurn struct {
	Index   int    `json:"index"`
	Scene   string `json:"scene,omitempty"`
	Heading string `json:"heading,omitempty"`
	// Rows 是这一轮写了几条：生成分段是镜头行数，剧本优化是集数（1）。
	Rows     int            `json:"rows"`
	Attempts int            `json:"attempts"`
	Prompt   string         `json:"prompt,omitempty"`
	Text     string         `json:"text,omitempty"`
	Usage    map[string]any `json:"usage,omitempty"`
	// TaskID 是写出这一轮的任务（从上一次任务接过来的轮次指向原任务）。
	TaskID string `json:"taskId"`
}

// dramaRunProgress 是任务 result.dramaRun：运行进度。前端按 Completed / Total 显示「从第 N 场继续」。
type dramaRunProgress struct {
	RunID     string             `json:"runId"`
	Total     int                `json:"total"`
	Completed int                `json:"completed"`
	Scenes    []drama.ShotsScene `json:"scenes,omitempty"`
	Turns     []dramaRunTurn     `json:"turns"`
}

// dramaTurnMarker 是写进文本流的回合标记行：前端见到它就把这一场之前收到的半截输出清掉（重试时同一场会重写）。
type dramaTurnMarker struct {
	Index   int    `json:"index"`
	Total   int    `json:"total"`
	Scene   string `json:"scene,omitempty"`
	Heading string `json:"heading,omitempty"`
	Attempt int    `json:"attempt"`
}

func newDramaRunMeta(plan *dramaStepPlan, unitID string) dramaRunMeta {
	run := dramaRunMeta{RunID: newID(), Step: plan.usage.Step, UnitID: unitID, Scenes: plan.scenes, Turns: max(1, len(plan.scenes)), ResumeFrom: 1}
	if plan.adapt != nil && plan.adapt.Mode == drama.ScriptAdaptModeAdapt {
		run.Sources = plan.adapt.SourceUnitIDs
	}
	return run
}

// dramaTurnPlan 是这次运行所属步骤的轮次切法（步骤表里没有时按生成分段）。
func dramaTurnPlan(stepID string) drama.TurnPlan {
	if step, ok := drama.StepByID(stepID); ok && step.Turns.Count != nil {
		return step.Turns
	}
	step, _ := drama.StepByID(drama.StepChapterShots)
	return step.Turns
}

// taskDramaRun 从任务输入读 metadata.dramaRun；没有返回 nil。
func taskDramaRun(inputJSON string) *dramaRunMeta {
	if !strings.Contains(inputJSON, `"dramaRun"`) {
		return nil
	}
	var input struct {
		Metadata struct {
			DramaRun *dramaRunMeta `json:"dramaRun"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil || input.Metadata.DramaRun == nil || input.Metadata.DramaRun.RunID == "" {
		return nil
	}
	return input.Metadata.DramaRun
}

func metadataDramaRun(metadata map[string]any) *dramaRunMeta {
	raw, ok := metadata["dramaRun"]
	if !ok {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var run dramaRunMeta
	if json.Unmarshal(encoded, &run) != nil || run.RunID == "" {
		return nil
	}
	return &run
}

// taskDramaRunProgress 读任务结果里的运行进度；没有返回 nil。
func taskDramaRunProgress(resultJSON string) *dramaRunProgress {
	if !strings.Contains(resultJSON, `"dramaRun"`) {
		return nil
	}
	var result struct {
		DramaRun *dramaRunProgress `json:"dramaRun"`
	}
	if json.Unmarshal([]byte(resultJSON), &result) != nil || result.DramaRun == nil {
		return nil
	}
	return result.DramaRun
}

// storyboardTaskTimeout 是分镜任务的执行墙钟：逐场续写按轮数放宽（每轮最多 dramaTurnMaxAttempts 次调用加退避），
// 真正掐断卡住调用的是每一轮自己的分镜超时。
func storyboardTaskTimeout(task *model.Task, policy RuntimeTaskPolicy) time.Duration {
	turn := storyboardTurnTimeout(policy)
	run := taskDramaRun(task.InputJSON)
	if run == nil {
		return turn
	}
	turns := max(1, run.Turns-max(0, run.ResumeFrom-1))
	return time.Duration(turns*dramaTurnMaxAttempts)*turn + time.Duration(turns*(dramaTurnMaxAttempts-1))*dramaTurnBackoffCap
}

// resumeDramaRun 是「从第 N 场 / 集继续」：上一次任务要是本人、本章（项目级步骤是同一批来源）、同一步，已经结束且没写完；
// 轮次（章节的场次、要写的集）没变过。
func (s *Service) resumeDramaRun(userID string, projectID string, unitID string, plan *dramaStepPlan, resumeTaskID string) (dramaRunMeta, error) {
	label := plan.usage.StepLabel
	unit := dramaTurnPlan(plan.usage.Step).Unit
	previous, err := s.repo.TaskForUser(userID, resumeTaskID)
	if err != nil {
		return dramaRunMeta{}, BadAuthRequest("要继续的" + label + "任务不存在")
	}
	run, progress := taskDramaRun(previous.InputJSON), taskDramaRunProgress(previous.ResultJSON)
	meta := taskDramaStep(previous.InputJSON)
	fresh := newDramaRunMeta(plan, unitID)
	switch {
	case previous.ProjectID != projectID || run == nil || meta == nil || meta.Step != plan.usage.Step || run.UnitID != unitID || !slices.Equal(run.Sources, fresh.Sources):
		return dramaRunMeta{}, BadAuthRequest("要继续的任务不是这一次的" + label)
	case previous.Status != model.TaskStatusFailed && previous.Status != model.TaskStatusCancelled:
		return dramaRunMeta{}, BadAuthRequest("只有失败或取消的" + label + "任务才能从中间继续")
	case progress == nil || progress.Completed == 0 || len(progress.Turns) < progress.Completed:
		return dramaRunMeta{}, BadAuthRequest(fmt.Sprintf("上一次一%s都没写完，请重新%s", unit, label))
	case progress.Completed >= progress.Total:
		return dramaRunMeta{}, BadAuthRequest(fmt.Sprintf("上一次各%s都已写完，请重新%s", unit, label))
	case !sameShotsScenes(run.Scenes, plan.scenes):
		return dramaRunMeta{}, BadAuthRequest(fmt.Sprintf("正文的%s次改过了，不能从中间继续，请重新%s", unit, label))
	}
	return dramaRunMeta{RunID: run.RunID, Step: run.Step, UnitID: unitID, Scenes: run.Scenes, Sources: run.Sources, Turns: run.Turns, ResumeFrom: progress.Completed + 1, ResumeTaskID: previous.ID}, nil
}

func sameShotsScenes(a []drama.ShotsScene, b []drama.ShotsScene) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// dramaRunStartTurns 是这次执行前已经写完的轮次：同一任务上次执行存下的（换路由重跑、租约被别的 worker 接管）优先，
// 否则取「从第 N 场继续」所指任务的前 N-1 轮。
func (s *Service) dramaRunStartTurns(task model.Task, run dramaRunMeta) ([]dramaRunTurn, error) {
	if latest, err := s.repo.Task(task.ID); err == nil {
		if progress := taskDramaRunProgress(latest.ResultJSON); progress != nil && progress.RunID == run.RunID && len(progress.Turns) > 0 {
			return progress.Turns, nil
		}
	}
	if run.ResumeTaskID == "" || run.ResumeFrom <= 1 {
		return nil, nil
	}
	previous, err := s.repo.TaskForUser(task.UserID, run.ResumeTaskID)
	if err != nil {
		return nil, fmt.Errorf("读不到要继续的任务：%w", err)
	}
	progress := taskDramaRunProgress(previous.ResultJSON)
	if progress == nil || progress.RunID != run.RunID || len(progress.Turns) < run.ResumeFrom-1 {
		return nil, errors.New("要继续的任务没有存下已写完的轮次，请重新生成")
	}
	turns := progress.Turns[:run.ResumeFrom-1]
	for _, t := range turns {
		if strings.TrimSpace(t.Prompt) == "" || strings.TrimSpace(t.Text) == "" {
			return nil, errors.New("要继续的任务缺少已写完各轮的对话，请重新生成")
		}
	}
	return turns, nil
}

// runDramaRun 按轮循环调模型（input 是第一轮的输入：提示词是服务端组装的整章 / 整批提示词）。返回的结果 text 是各轮输出按顺序拼接，
// dramaRun 是进度（去掉了对话正文）；收尾（finalizeDramaStepTask）从 text 解析镜头行或剧集。
func (s *Service) runDramaRun(ctx context.Context, input canvasGenerationInput, run dramaRunMeta) (map[string]any, error) {
	task, ok := ctx.Value(mediaExecutionTaskKey{}).(model.Task)
	if !ok || task.ID == "" {
		return nil, errors.New("逐场续写缺少任务上下文")
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return nil, err
	}
	turnTimeout := storyboardTurnTimeout(policy.Task)
	turns, err := s.dramaRunStartTurns(task, run)
	if err != nil {
		return nil, err
	}
	total := max(1, run.Turns)
	plan := dramaTurnPlan(run.Step)
	progress := dramaRunProgress{RunID: run.RunID, Total: total, Completed: len(turns), Scenes: run.Scenes, Turns: turns}
	stream := newDramaRunStream(s, task, input.OnTextDelta)
	defer stream.close()
	turnInput := input
	turnInput.OnTextDelta = stream.publish
	written := 0
	for _, t := range turns {
		written += t.Rows
	}
	for k := len(turns) + 1; k <= total; k++ {
		prompt := input.Prompt
		if k == 1 && len(run.Scenes) > 1 {
			prompt += "\n\n" + plan.Intro(run.Scenes)
		} else if k > 1 {
			prompt = plan.Next(run.Scenes, k, written)
		}
		turnInput.Prompt = prompt
		turnInput.TextHistory = dramaRunHistory(input.TextHistory, progress.Turns)
		marker := dramaTurnMarker{Index: k, Total: total}
		if k <= len(run.Scenes) {
			marker.Scene, marker.Heading = run.Scenes[k-1].ID, run.Scenes[k-1].Heading
		}
		turn, err := s.runDramaTurn(ctx, task, turnInput, marker, stream, turnTimeout, plan, run.Sources)
		if err != nil {
			return nil, dramaRunTurnError(plan.Unit, marker, progress.Completed, turnTimeout, err)
		}
		turn.TaskID = task.ID
		progress.Turns = append(progress.Turns, turn)
		progress.Completed = k
		written += turn.Rows
		encoded, err := json.Marshal(map[string]any{"mode": "text", "dramaRun": progress})
		if err != nil {
			return nil, err
		}
		stage := fmt.Sprintf("已写完第 %d/%d %s", k, total, plan.Unit)
		if err := s.repo.UpdateTaskResultForLease(task.ID, task.LeaseOwner, stage, 35+60*k/total, string(encoded)); err != nil {
			return nil, fmt.Errorf("保存第 %d 场进度失败：%w", k, err)
		}
	}
	texts := make([]string, 0, len(progress.Turns))
	for i := range progress.Turns {
		texts = append(texts, strings.TrimSpace(progress.Turns[i].Text))
		progress.Turns[i].Prompt, progress.Turns[i].Text = "", ""
	}
	return map[string]any{"mode": "text", "text": strings.Join(texts, "\n") + "\n", "dramaRun": progress}, nil
}

// dramaRunHistory 是续写的对话历史：前面各轮的提示词和模型输出依次排好（接在任务原有的 textHistory 后面）。
func dramaRunHistory(base []providerTextMessage, turns []dramaRunTurn) []providerTextMessage {
	history := append([]providerTextMessage{}, base...)
	for _, t := range turns {
		history = append(history, providerTextMessage{Role: "user", Content: t.Prompt}, providerTextMessage{Role: "assistant", Content: t.Text})
	}
	return history
}

// runDramaTurn 写一轮：每次调用各用一次分镜超时；临时失败（网络、上游 5xx / 408 / 429、超时、输出解析不出来）
// 按 5 秒、10 秒退避重试，最多 dramaTurnMaxAttempts 次。每次调用前往流里写回合标记。
func (s *Service) runDramaTurn(ctx context.Context, task model.Task, input canvasGenerationInput, marker dramaTurnMarker, stream *dramaRunStream, turnTimeout time.Duration, plan drama.TurnPlan, sources []string) (dramaRunTurn, error) {
	var lastErr error
	for attempt := 1; attempt <= dramaTurnMaxAttempts; attempt++ {
		if attempt > 1 {
			wait := min(dramaTurnBackoffBase<<(attempt-2), dramaTurnBackoffCap)
			_ = s.log(task.UserID, task.ID, "warn", fmt.Sprintf("第 %d %s第 %d 次调用失败，%s 后重试", marker.Index, plan.Unit, attempt-1, wait), lastErr.Error())
			select {
			case <-ctx.Done():
				return dramaRunTurn{}, ctx.Err()
			case <-time.After(wait):
			}
		}
		marker.Attempt = attempt
		stream.marker(marker)
		turnCtx, cancel := context.WithTimeout(ctx, turnTimeout)
		result, err := runTextTask(turnCtx, input)
		cancel()
		if err == nil {
			text, _ := result["text"].(string)
			count, parseErr := plan.Count(text, sources)
			if parseErr != nil {
				lastErr = fmt.Errorf("%w：%v", errDramaTurnUnparsed, parseErr)
				continue
			}
			stream.finishTurn(text)
			turn := dramaRunTurn{Index: marker.Index, Scene: marker.Scene, Heading: marker.Heading, Rows: count, Attempts: attempt, Prompt: input.Prompt, Text: text}
			if usage, ok := result["usage"].(map[string]any); ok {
				turn.Usage = usage
			}
			return turn, nil
		}
		lastErr = err
		if !dramaTurnRetryable(ctx, err) {
			break
		}
	}
	return dramaRunTurn{}, lastErr
}

// dramaTurnRetryable 判断一轮失败能不能重试：参数、鉴权、内容审核、输出被截断不重试；任务本身被取消或到总时限也不重试。
func dramaTurnRetryable(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, errDramaTurnUnparsed) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) || isTransientResponseDecodeError(err) {
		return true
	}
	if code, _ := ChannelSlotFailureDetails(err); code != "" {
		return true
	}
	var httpErr providerHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode >= http.StatusInternalServerError || httpErr.StatusCode == http.StatusRequestTimeout || httpErr.StatusCode == http.StatusTooManyRequests
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// dramaRunTurnError 是一轮最终失败的错误：说明哪一场（集）、已写完几场（集）；超时单独写明（不带 DeadlineExceeded，
// 免得 worker 把它改写成笼统的「任务执行超时」）。其余错误保留原错误供诊断和换路由判断。
func dramaRunTurnError(unit string, marker dramaTurnMarker, completed int, turnTimeout time.Duration, err error) error {
	scene := fmt.Sprintf("第 %d %s", marker.Index, unit)
	if marker.Heading != "" && marker.Heading != scene {
		scene += "「" + marker.Heading + "」"
	}
	kept := ""
	if completed > 0 {
		kept = fmt.Sprintf("，已写完的 %d %s已保存，可从第 %d %s继续", completed, unit, completed+1, unit)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s生成超时（每%s %d 分钟）%s", scene, unit, int(turnTimeout.Minutes()), kept)
	}
	return fmt.Errorf("%s生成失败%s：%w", scene, kept, err)
}

// dramaRunStream 把各轮输出和回合标记写进同一个任务的 text-events。模型不支持流式时，一轮结束后把整轮输出补写进去。
type dramaRunStream struct {
	upstream  func(string)
	own       *taskTextStreamPublisher
	streamed  bool
	lastByte  byte
	turnBytes int
}

func newDramaRunStream(s *Service, task model.Task, upstream func(string)) *dramaRunStream {
	stream := &dramaRunStream{upstream: upstream, streamed: upstream != nil}
	if upstream == nil {
		stream.own = newTaskTextStreamPublisher(s, task.UserID, task.ID)
		stream.upstream = stream.own.Publish
	}
	return stream
}

func (d *dramaRunStream) write(text string) {
	if text == "" {
		return
	}
	d.upstream(text)
	d.lastByte = text[len(text)-1]
}

// publish 是模型的流式输出。
func (d *dramaRunStream) publish(delta string) {
	d.turnBytes += len(delta)
	d.write(delta)
}

// marker 写一行回合标记（前面的输出没以换行结尾时先补换行）。
func (d *dramaRunStream) marker(m dramaTurnMarker) {
	encoded, _ := json.Marshal(map[string]any{drama.ShotLinesMarkerKey: m})
	if d.lastByte != 0 && d.lastByte != '\n' {
		d.write("\n")
	}
	d.write(string(encoded) + "\n")
	d.turnBytes = 0
}

// finishTurn 在一轮成功后补齐：没有流式输出时写整轮文本，最后保证以换行结尾。
func (d *dramaRunStream) finishTurn(text string) {
	if !d.streamed || d.turnBytes == 0 {
		d.write(text)
	}
	if d.lastByte != '\n' {
		d.write("\n")
	}
}

func (d *dramaRunStream) close() {
	if d.own != nil {
		d.own.Close()
	}
}
