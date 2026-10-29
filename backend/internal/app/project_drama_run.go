// 短剧流程步骤的逐轮续写（M7.2）：一个运行 = 一个任务。worker 领到任务后在进程内按轮循环调模型（生成分段一轮一场，
// 剧本优化、导演风格一轮一集；每一轮怎么说、怎么验由步骤表的 TurnPlan 定），每一轮带上前面各轮的 textHistory、各用一次分镜超时，
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
	// OnlyScenes 是「重新生成问题场」：只重写这几场，其余各场用镜头表（第 BaseShotlistVersion 版）里已有的镜头行，
	// 照原来的轮次放进对话历史（不调模型），收尾按整章合并、检查、装段。为空时每一场都写。见 project_drama_run_only.go。
	OnlyScenes          []string `json:"onlyScenes,omitempty"`
	BaseShotlistVersion int      `json:"baseShotlistVersion,omitempty"`
	// OnlySections 是按小节生成时的「重新生成问题小节」：只重写这几节（OnlyScenes 给了场时展开成这几场的全部小节）；
	// 其余各节的原样输出放进对话历史，镜头行用镜头表里已有的。
	OnlySections []string `json:"onlySections,omitempty"`
}

// dramaRunTurn 是写完的一轮（一场或一集）。Prompt / Text 是续写和「从第 N 场继续」的对话历史，任务成功后从结果里去掉。
type dramaRunTurn struct {
	Index   int    `json:"index"`
	Scene   string `json:"scene,omitempty"`
	Heading string `json:"heading,omitempty"`
	// Section / Genre 是按小节生成时这一轮写的小节和戏类（wenxi15 / wenxi30 / wuxi）；一个戏类一条对话，续写历史按戏类分开。
	Section string `json:"section,omitempty"`
	Genre   string `json:"genre,omitempty"`
	// Rows 是这一轮写了几条：生成分段是镜头行数，剧本优化是集数（1）。
	Rows     int            `json:"rows"`
	Attempts int            `json:"attempts"`
	Prompt   string         `json:"prompt,omitempty"`
	Text     string         `json:"text,omitempty"`
	Usage    map[string]any `json:"usage,omitempty"`
	// TaskID 是写出这一轮的任务（从上一次任务接过来的轮次指向原任务）。
	TaskID string `json:"taskId"`
	// Kept 表示这一轮没调模型：「重新生成问题场」时不重写的场，Text 是镜头表里原有的镜头行；按小节生成时 Text 是这一节存下的
	// 原样输出（进对话历史），KeptRows 是镜头表里这一节原有的镜头行（一行一个 JSON，收尾直接用，保留分镜卡上的手改）。
	Kept     bool   `json:"kept,omitempty"`
	KeptRows string `json:"keptRows,omitempty"`
}

// dramaRunProgress 是任务 result.dramaRun：运行进度。前端按 Completed / Total 显示「从第 N 场继续」。
type dramaRunProgress struct {
	RunID     string             `json:"runId"`
	Total     int                `json:"total"`
	Completed int                `json:"completed"`
	Scenes    []drama.ShotsScene `json:"scenes,omitempty"`
	Turns     []dramaRunTurn     `json:"turns"`
	// Genre 是按小节生成时这次运行的默认戏类（各轮的戏类在 Turns[i].Genre）。
	Genre string `json:"genre,omitempty"`
}

// dramaTurnMarker 是写进文本流的回合标记行：前端见到它就把这一场之前收到的半截输出清掉（重试时同一场会重写）。
type dramaTurnMarker struct {
	Index   int    `json:"index"`
	Total   int    `json:"total"`
	Scene   string `json:"scene,omitempty"`
	Heading string `json:"heading,omitempty"`
	Attempt int    `json:"attempt"`
	// Section / Genre / Est 是按小节生成时这一轮的小节、戏类名（文戏 15）和估时秒数；侧栏按「场 → 小节」分组。
	Section string  `json:"section,omitempty"`
	Genre   string  `json:"genre,omitempty"`
	Est     float64 `json:"est,omitempty"`
	// Only 是「重新生成问题场 / 小节」要重写的场或小节（只有这几轮有回合标记和输出）。
	Only []string `json:"only,omitempty"`
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

// dramaRunCallsNoUpstream 表示这次运行一轮都不用写：从第 Turns+1 轮接着（上一次各轮都写完、只是收尾失败），只拿存下的输出重新收尾。
func dramaRunCallsNoUpstream(run *dramaRunMeta) bool {
	return run != nil && run.Turns > 0 && run.ResumeFrom > run.Turns
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
// 轮次（章节的场次、要写的集）没变过。生成分段各节都写完、只是收尾失败时也可以接着：ResumeFrom 是总轮数加一，新任务不调模型只重新收尾。
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
	case progress.Completed >= progress.Total && plan.usage.Step != drama.StepChapterShots:
		return dramaRunMeta{}, BadAuthRequest(fmt.Sprintf("上一次各%s都已写完，请重新%s", unit, label))
	case !sameShotsScenes(run.Scenes, plan.scenes):
		return dramaRunMeta{}, BadAuthRequest(fmt.Sprintf("正文的%s次改过了（或场次规则已更新：连续同地点的场次头并成一场），不能从中间继续，请重新%s", unit, label))
	}
	// 生成分段各节都写完了、收尾（写镜头表、装段）没成：重新收尾，不再调模型，各节输出原样取上一次任务存下的。
	// 只重写几节的运行，不重写的节用的是镜头表第 BaseShotlistVersion 版的镜头行：镜头表之后改过就不能拿旧行收尾。
	if progress.Completed >= progress.Total && len(run.OnlySections) > 0 {
		record, err := s.repo.DramaShotlistForUnit(projectID, unitID)
		if err != nil || record.Version != run.BaseShotlistVersion {
			return dramaRunMeta{}, BadAuthRequest("本章镜头表在上一次任务之后又改过，不能拿上一次的输出重新收尾，请重新" + label)
		}
	}
	return dramaRunMeta{RunID: run.RunID, Step: run.Step, UnitID: unitID, Scenes: run.Scenes, Sources: run.Sources, Turns: run.Turns, ResumeFrom: progress.Completed + 1, ResumeTaskID: previous.ID,
		OnlyScenes: run.OnlyScenes, OnlySections: run.OnlySections, BaseShotlistVersion: run.BaseShotlistVersion}, nil
}

func sameShotsScenes(a []drama.ShotsScene, b []drama.ShotsScene) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ID != y.ID || x.Heading != y.Heading || x.Opening != y.Opening || x.Section != y.Section || x.Genre != y.Genre || x.Est != y.Est || !slices.Equal(x.SourceHeadings, y.SourceHeadings) {
			return false
		}
	}
	return true
}

// dramaRunStartTurns 是这次执行前已经写完的轮次：同一任务上次执行存下的（换路由重跑、租约被别的 worker 接管）优先，
// 否则取「从第 N 场 / 节继续」所指任务的前 N-1 轮。
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
	// 按小节生成：一轮一节，每一节的本轮指令建任务时已经组好；一个戏类一条对话（续写历史只带同一戏类的轮次），当轮按这一节戏类的
	// 转换器校验，转出的镜头行另写一行进文本流给侧栏显示。
	shots := taskDramaShots(task.InputJSON)
	if shots != nil {
		if len(shots.Turns) != len(shots.Sections) || len(shots.Sections) != total {
			return nil, errors.New("按小节生成的任务参数不完整，请重新生成")
		}
		for _, sec := range shots.Sections {
			if _, ok := drama.ShotGenreByID(sec.Genre); !ok {
				return nil, fmt.Errorf("小节 %s 的戏类 %q 不认识，请重新生成", sec.ID, sec.Genre)
			}
		}
		progress.Genre = shots.Genre
		// 按小节生成的轮次单位是「节」（各轮的解析按这一节的戏类，见下面的 turnPlan）。
		if g, ok := drama.ShotGenreByID(shots.Genre); ok {
			plan = dramaTurnPlan(g.Step)
		}
	}
	stream := newDramaRunStream(s, task, input.OnTextDelta)
	defer stream.close()
	turnInput := input
	turnInput.OnTextDelta = stream.publish
	written := 0
	for _, t := range turns {
		written += t.Rows
	}
	kept, err := s.dramaRunKeptSections(task, run, len(turns))
	if err != nil {
		return nil, err
	}
	if dramaRunCallsNoUpstream(&run) && len(turns) < total {
		// 只收尾的任务没建计费单，绝不能在这里补调模型。
		return nil, errors.New("上一次任务存下的各轮不完整，不能只重新收尾，请重新生成")
	}
	if len(turns) >= total {
		// 各轮都在上一次任务里写完了（上一次收尾失败）：不调模型，直接拿存下的输出收尾。先把各轮存进本任务的结果，
		// 这次收尾再失败时本任务也能「用已写好的 N 节重新写入」（续写同样只认上一次任务存下的轮次）。
		encoded, err := json.Marshal(map[string]any{"mode": "text", "dramaRun": progress})
		if err != nil {
			return nil, err
		}
		if err := s.repo.UpdateTaskResultForLease(task.ID, task.LeaseOwner, fmt.Sprintf("%d %s已写完，重新收尾", total, plan.Unit), 95, string(encoded)); err != nil {
			return nil, fmt.Errorf("保存各%s进度失败：%w", plan.Unit, err)
		}
	}
	for k := len(turns) + 1; k <= total; k++ {
		// 用户可以在轮与轮之间取消（task_lifecycle 对逐轮续写放行）：取消落库后，别的实例上的 worker 在下一轮开始前停下。
		if latest, err := s.repo.Task(task.ID); err == nil && latest.Status == model.TaskStatusCancelled {
			return nil, context.Canceled
		}
		prompt := input.Prompt
		turnPlan, history := plan, dramaRunHistory(input.TextHistory, progress.Turns)
		genreLabel := ""
		switch {
		case shots != nil:
			sec := shots.Sections[k-1]
			g, _ := drama.ShotGenreByID(sec.Genre)
			turnPlan, genreLabel = dramaTurnPlan(g.Step), g.Label
			if prompt, err = sectionTurnPrompt(input.Prompt, shots, k, progress.Turns); err != nil {
				return nil, err
			}
			history = sectionRunHistory(input.TextHistory, shots, sec.Genre, progress.Turns)
		case k == 1 && len(run.Scenes) > 1:
			prompt += "\n\n" + plan.Intro(run.Scenes)
		case k > 1:
			prompt = plan.Next(run.Scenes, k, written)
		}
		turnInput.Prompt = prompt
		turnInput.TextHistory = history
		marker := dramaTurnMarker{Index: k, Total: total}
		key := ""
		if k <= len(run.Scenes) {
			sc := run.Scenes[k-1]
			marker.Scene, marker.Heading, key = sc.ID, sc.Heading, sc.ID
			if sc.Section != "" {
				marker.Section, marker.Genre, marker.Est, key = sc.Section, genreLabel, sc.Est, sc.Section
			}
		}
		check := drama.TurnContext{Index: k, Sources: run.Sources, Prior: dramaRunPriorText(progress.Turns)}
		if k <= len(run.Scenes) {
			check.Turn = run.Scenes[k-1]
		}
		var turn dramaRunTurn
		if rows, ok := kept[key]; ok {
			// 不重写的场 / 节：原有内容照这一轮的提示词放进对话历史，后面重写的照样接着它往下写。
			turn = dramaRunTurn{Index: k, Scene: marker.Scene, Heading: marker.Heading, Rows: rows.count, Prompt: turnInput.Prompt, Text: rows.text, Kept: true, KeptRows: rows.rows}
		} else {
			marker.Only = run.OnlyScenes
			if shots != nil {
				marker.Only = run.OnlySections
			}
			if turn, err = s.runDramaTurn(ctx, task, turnInput, marker, stream, turnTimeout, turnPlan, check); err != nil {
				return nil, dramaRunTurnError(plan.Unit, marker, progress.Completed, turnTimeout, err)
			}
			if shots != nil {
				stream.sectionRows(shots, progress.Turns, k, turn.Text)
			}
		}
		turn.TaskID, turn.Section = task.ID, marker.Section
		if shots != nil {
			turn.Genre = shots.Sections[k-1].Genre
		}
		progress.Turns = append(progress.Turns, turn)
		progress.Completed = k
		written += turn.Rows
		encoded, err := json.Marshal(map[string]any{"mode": "text", "dramaRun": progress})
		if err != nil {
			return nil, err
		}
		stage := fmt.Sprintf("已写完第 %d/%d %s", k, total, plan.Unit)
		if err := s.repo.UpdateTaskResultForLease(task.ID, task.LeaseOwner, stage, 35+60*k/total, string(encoded)); err != nil {
			return nil, fmt.Errorf("保存第 %d %s进度失败：%w", k, plan.Unit, err)
		}
	}
	texts := make([]string, 0, len(progress.Turns))
	for i := range progress.Turns {
		texts = append(texts, strings.TrimSpace(progress.Turns[i].Text))
		progress.Turns[i].Prompt = ""
		// 按小节生成的原样输出留到收尾：逐节转换、存进镜头表记录后再从任务结果里去掉。
		if shots == nil {
			progress.Turns[i].Text = ""
		}
	}
	return map[string]any{"mode": "text", "text": strings.Join(texts, "\n") + "\n", "dramaRun": progress}, nil
}

// sectionTurnPrompt 是按小节生成第 k 节这一轮发给模型的话：这一节是它那个戏类的第一节时，接在这条对话开头的提示词后面（第 1 节所在
// 戏类是任务提示词，其余戏类是 Lanes 里的那条）；同一场上一节是另一个戏类写的时，附上它原样输出的结尾，用来接站位、朝向、道具和灯光。
func sectionTurnPrompt(taskPrompt string, shots *dramaShotsMeta, k int, prior []dramaRunTurn) (string, error) {
	sec := shots.Sections[k-1]
	turn := shots.Turns[k-1]
	if !slices.ContainsFunc(shots.Sections[:k-1], func(s drama.Section) bool { return s.Genre == sec.Genre }) {
		head := taskPrompt
		if sec.Genre != shots.Sections[0].Genre {
			if head = shots.Lanes[sec.Genre]; strings.TrimSpace(head) == "" {
				return "", fmt.Errorf("小节 %s 的戏类没有对话开头的提示词，请重新生成", sec.ID)
			}
		}
		turn = joinTruthyStrings(head, turn)
	}
	if k > 1 && len(prior) >= k-1 {
		if prev := shots.Sections[k-2]; prev.Scene == sec.Scene && prev.Genre != sec.Genre {
			turn = joinTruthyStrings(turn, sectionHandoffPrompt(prev, prior[k-2].Text))
		}
	}
	return turn, nil
}

// sectionHandoffRunes 是交接给另一个戏类时，上一节原样输出取结尾多少字。
const sectionHandoffRunes = 1500

// sectionHandoffPrompt 是【上一节结尾】：同一场上一节是另一套分镜技能写的（文戏接武戏、武戏接文戏），把它原样输出的结尾带上。
func sectionHandoffPrompt(prev drama.Section, text string) string {
	g, _ := drama.ShotGenreByID(prev.Genre)
	tail := []rune(strings.TrimSpace(text))
	if len(tail) > sectionHandoffRunes {
		tail = append([]rune("……"), tail[len(tail)-sectionHandoffRunes:]...)
	}
	return fmt.Sprintf("【上一节结尾】同一场上一节 %s（%s）是另一套分镜技能写的，它原样输出的结尾如下。只用来接最后一镜的站位、朝向、距离、手里的道具、伤和灯光，不照它的格式写：\n%s", prev.ID, g.Label, string(tail))
}

// sectionRunHistory 是按小节生成这一轮的续写历史：只带同一戏类的前面各轮（一个戏类一条对话）；任务原有的 textHistory 只接在第 1 节
// 所在戏类那条对话前面。
func sectionRunHistory(base []providerTextMessage, shots *dramaShotsMeta, genre string, turns []dramaRunTurn) []providerTextMessage {
	var lane []dramaRunTurn
	for i, t := range turns {
		g := t.Genre
		if g == "" && i < len(shots.Sections) {
			g = shots.Sections[i].Genre
		}
		if g == genre {
			lane = append(lane, t)
		}
	}
	if genre != shots.Sections[0].Genre {
		base = nil
	}
	return dramaRunHistory(base, lane)
}

// dramaRunPriorText 是前面各轮的输出按顺序拼接（导演风格当轮校验承接色用）。
func dramaRunPriorText(turns []dramaRunTurn) string {
	texts := make([]string, 0, len(turns))
	for _, t := range turns {
		texts = append(texts, strings.TrimSpace(t.Text))
	}
	return strings.Join(texts, "\n")
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
func (s *Service) runDramaTurn(ctx context.Context, task model.Task, input canvasGenerationInput, marker dramaTurnMarker, stream *dramaRunStream, turnTimeout time.Duration, plan drama.TurnPlan, check drama.TurnContext) (dramaRunTurn, error) {
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
			count, parseErr := plan.Count(text, check)
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
		return fmt.Errorf("%s生成超时（每%s %d 分钟）%s。", scene, unit, int(turnTimeout.Minutes()), kept)
	}
	return dramaSentenceError{fmt.Errorf("%s生成失败%s：%w", scene, kept, err)}
}

// dramaSentenceError 让失败原因以句号收尾：页面在原因后面还会接一句（「原文和已有剧集都没有改动。」），
// 解析错误这类不带标点的原因直接连上去读不通。
type dramaSentenceError struct{ err error }

func (e dramaSentenceError) Error() string {
	msg := e.err.Error()
	if strings.HasSuffix(msg, "。") || strings.HasSuffix(msg, "！") || strings.HasSuffix(msg, "？") || strings.HasSuffix(msg, ".") {
		return msg
	}
	return msg + "。"
}

func (e dramaSentenceError) Unwrap() error { return e.err }

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

// sectionRows 在一节写完后往文本流里写一行转出的镜头行（{"rows": [...]}，侧栏按镜头显示）：镜头 id 接着本场前面各节往下编，
// 与收尾写库的编号一致；资产名、锚点要到收尾才对，这里只为显示。
func (d *dramaRunStream) sectionRows(shots *dramaShotsMeta, prior []dramaRunTurn, k int, text string) {
	sec := shots.Sections[k-1]
	offset := 0
	for _, t := range prior {
		if t.Scene == sec.Scene {
			offset += t.Rows
		}
	}
	conv, err := drama.ConvertSection(sec.Genre, text, drama.SectionContext{Section: sec, ShotOffset: offset})
	if err != nil {
		return
	}
	encoded, err := json.Marshal(map[string]any{"rows": conv.Rows})
	if err != nil {
		return
	}
	if d.lastByte != 0 && d.lastByte != '\n' {
		d.write("\n")
	}
	d.write(string(encoded) + "\n")
}

func (d *dramaRunStream) close() {
	if d.own != nil {
		d.own.Close()
	}
}
