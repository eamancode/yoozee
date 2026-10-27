package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/database"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/platform"
	"yingce/backend/internal/repository"
)

// qichuangSceneRows 是七床线上输出按场分好的镜头行（一镜一行）。线上那次是旧规则（一个场次头一场，三场）写的；
// 现在第 2、3 个场次头（都是「内·电梯口·夜」）并成一场，按新规则模型会把第三个场次头下的镜头接着写在 E01S02 里：
// 这里把 E01S03 的镜头改成 E01S02、spec.id 接着第 2 场往下编。
func qichuangSceneRows(t *testing.T) map[string][]string {
	t.Helper()
	var payload struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal([]byte(readTestdata(t, "model-output.txt")), &payload); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, row := range payload.Rows {
		spec, _ := row["spec"].(map[string]any)
		scene, _ := spec["scene"].(string)
		if scene == "E01S03" {
			scene = "E01S02"
			spec["scene"], spec["id"] = scene, fmt.Sprintf("E01S02-%02d", len(out[scene])+1)
		}
		encoded, _ := json.Marshal(row)
		out[scene] = append(out[scene], string(encoded))
	}
	return out
}

// streamingShotsUpstream 是假的流式 Chat Completions 上游：按对话里已有几轮判断在写第几场，一行一个 SSE 增量吐出这一场的镜头；
// fail 返回非 0 时这次调用直接返回该状态码。生成分段第一轮之前的戏类判定单独应答（genres 是回复正文，genreStatus 非 0 时
// 判定调用返回该状态码），不计进 calls / histories；prompts 记每次续写调用的本轮提示词。
type streamingShotsUpstream struct {
	mu          sync.Mutex
	scenes      map[string][]string
	calls       int
	histories   []int
	prompts     []string
	fail        func(call int, scene int) int
	genres      string
	genreStatus int
	genreCalls  int
}

func (u *streamingShotsUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []json.RawMessage `json:"messages"`
		Stream   bool              `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var last struct {
		Content string `json:"content"`
	}
	if len(body.Messages) > 0 {
		_ = json.Unmarshal(body.Messages[len(body.Messages)-1], &last)
	}
	if strings.Contains(last.Content, "先给每一场判戏类") {
		u.mu.Lock()
		u.genreCalls++
		u.mu.Unlock()
		if u.genreStatus != 0 {
			w.WriteHeader(u.genreStatus)
			fmt.Fprint(w, `{"error":{"message":"upstream unavailable"}}`)
			return
		}
		u.reply(w, body.Stream, []string{u.genres})
		return
	}
	u.mu.Lock()
	u.prompts = append(u.prompts, last.Content)
	u.calls++
	call := u.calls
	history := len(body.Messages) - 1
	u.histories = append(u.histories, history)
	u.mu.Unlock()
	scene := history/2 + 1
	if u.fail != nil {
		if status := u.fail(call, scene); status != 0 {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"message":"upstream unavailable"}}`)
			return
		}
	}
	u.reply(w, body.Stream, u.scenes[fmt.Sprintf("E01S%02d", scene)])
}

// reply 按请求是否流式回一段正文（流式时一行一个增量）。
func (u *streamingShotsUpstream) reply(w http.ResponseWriter, stream bool, lines []string) {
	if !stream {
		reply, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": strings.Join(lines, "\n")}, "finish_reason": "stop"}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for i, line := range lines {
		if i < len(lines)-1 {
			line += "\n"
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": line}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		w.(http.Flusher).Flush()
	}
	done, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 50}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", done)
}

func newDramaWorkerTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	previous := dramaTurnBackoffBase
	dramaTurnBackoffBase = time.Millisecond
	t.Cleanup(func() { dramaTurnBackoffBase = previous })
	db := newSQLiteTestDB(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, ".settings-key"), bytes.Repeat([]byte{7}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &Service{repo: repository.New(db), dataDir: dataDir, activeCancels: make(map[string]context.CancelFunc), coordinator: platform.NewCoordinatorWithRedis(nil, "drama-run-test")}
	if err := svc.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
	actor := &model.User{ID: "admin", Role: "admin"}
	if _, err := svc.UpdateFeatureAvailability(actor, FeatureAvailability{CustomChannelsEnabled: true, TaskCenterEnabled: true, CreditsEnabled: true}); err != nil {
		t.Fatal(err)
	}
	return svc, db
}

// queueDramaStepTask 按 RunDramaStep 的形状建一个生成分段任务（自定义渠道指向假上游），并挂上计费单，交给 worker 领取。
func queueDramaStepTask(t *testing.T, svc *Service, db *gorm.DB, project model.Project, unit model.ProjectUnit, upstream string, resumeTaskID string) *model.Task {
	t.Helper()
	plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots)
	if err != nil {
		t.Fatal(err)
	}
	run := newDramaRunMeta(plan, unit.ID)
	if resumeTaskID != "" {
		if run, err = svc.resumeDramaRun("user-1", project.ID, unit.ID, plan, resumeTaskID); err != nil {
			t.Fatal(err)
		}
	}
	req := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": upstream, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	req.Input["metadata"].(map[string]any)["dramaRun"] = run
	return insertDramaTestTask(t, svc, db, project.ID, req)
}

// insertDramaTestTask 把一个步骤任务请求直接写成排队中的任务（带计费单），交给 worker 领取。
func insertDramaTestTask(t *testing.T, svc *Service, db *gorm.DB, projectID string, req CreateTaskRequest) *model.Task {
	t.Helper()
	input, err := normalizeTaskInput(req.Input)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.protectTaskSecrets(input); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(input)
	task := &model.Task{ID: newID(), UserID: "user-1", ProjectID: projectID, Type: req.Type, Operation: req.Operation, Prompt: req.Prompt, Status: model.TaskStatusQueued, InputJSON: string(encoded), RouteRun: 1}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	order := model.BillingOrder{ID: newID(), UserID: task.UserID, TaskID: task.ID, IdempotencyKey: newID(), Status: model.BillingStatusReserved, AmountMicrocredits: 100, ReservedAmountMicrocredits: 100}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.FirstOrCreate(&model.CreditAccount{UserID: task.UserID}, model.CreditAccount{UserID: task.UserID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CreditAccount{}).Where("user_id = ?", task.UserID).Updates(map[string]any{"available_microcredits": 10_000, "reserved_microcredits": 100}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(task).Update("billing_order_id", order.ID).Error; err != nil {
		t.Fatal(err)
	}
	return task
}

func runQueuedTask(t *testing.T, svc *Service) (*model.Task, error) {
	t.Helper()
	claimed, err := svc.repo.ClaimNextTask("worker", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	runErr := svc.taskWorker().processClaimedTask(claimed, nil)
	stored, err := svc.repo.Task(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored, runErr
}

// 七床第一集走真实 worker 路径：三个场次头并成两场（走廊、电梯口），一个任务内两场两轮、流式输出进同一条文本流、收尾写镜头表和段；
// 第二场一直 503 时任务失败但保留第一场，「从第 2 场继续」的新任务只写第二场就写完整集。
func TestDramaStepTaskRunsScenesAndFinalizesThroughWorker(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	failScene2 := true
	upstream := &streamingShotsUpstream{scenes: qichuangSceneRows(t), fail: func(call int, scene int) int {
		if failScene2 && scene == 2 {
			return http.StatusServiceUnavailable
		}
		return 0
	}}
	server := httptest.NewServer(upstream)
	defer server.Close()

	first := queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if timeout := taskExecutionTimeout(first, RuntimeTaskPolicy{TextTimeoutMinutes: 8, StoryboardTimeoutMinutes: 20}); timeout != 2*3*20*time.Minute+2*2*dramaTurnBackoffCap {
		t.Fatalf("两场的任务总时限：%s", timeout)
	}
	failed, err := runQueuedTask(t, svc)
	if err == nil || failed.Status != model.TaskStatusFailed || !strings.Contains(failed.Error, "第 2 场") {
		t.Fatalf("第二场失败：%v %s %q", err, failed.Status, failed.Error)
	}
	progress := taskDramaRunProgress(failed.ResultJSON)
	if progress == nil || progress.Completed != 1 || progress.Total != 2 || progress.Turns[0].Text == "" || len(progress.Scenes[1].SourceHeadings) != 2 {
		t.Fatalf("失败的任务保留第一场：%+v", progress)
	}
	if upstream.calls != 1+dramaTurnMaxAttempts {
		t.Fatalf("第二场重试用尽：%d 次调用", upstream.calls)
	}
	if segments, _ := svc.DramaSegments("user-1", project.ID, unit.ID); len(segments) != 0 {
		t.Fatalf("没写完不写库：%d 段", len(segments))
	}

	failScene2, upstream.calls, upstream.histories = false, 0, nil
	queueDramaStepTask(t, svc, db, project, unit, server.URL, failed.ID)
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("从第 2 场继续：%v %s %q", err, done.Status, done.Error)
	}
	if upstream.calls != 1 || upstream.histories[0] != 2 {
		t.Fatalf("只写第二场、带第一场对话：%d %v", upstream.calls, upstream.histories)
	}
	var result struct {
		Text      string               `json:"text"`
		DramaRun  dramaRunProgress     `json:"dramaRun"`
		DramaStep DramaStepTaskOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(done.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result.DramaRun.Completed != 2 || result.DramaRun.Turns[0].TaskID != failed.ID || result.DramaRun.Turns[0].Text != "" {
		t.Fatalf("成功后的进度只留摘要：%+v", result.DramaRun)
	}
	if result.DramaStep.Status != DramaStepOutcomeWritten || result.DramaStep.Parse.Rows != 41 || result.DramaStep.Segments == 0 || result.DramaStep.Scenes != 2 {
		t.Fatalf("收尾写镜头表并装段：%+v", result.DramaStep)
	}
	segments, err := svc.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil || len(segments) != result.DramaStep.Segments {
		t.Fatalf("段落库：%v %d", err, len(segments))
	}
	view, err := svc.DramaShotlist("user-1", project.ID, unit.ID)
	if err != nil || view == nil || view.Draft || len(view.Shotlist.Shots) != 41 || view.Check.Parse == nil {
		t.Fatalf("镜头表落库：%v %+v", err, view)
	}
	for _, sh := range view.Shotlist.Shots {
		if sh.Scene != "E01S01" && sh.Scene != "E01S02" {
			t.Fatalf("镜头只有 E01S01、E01S02 两场：%s %s", sh.ID, sh.Scene)
		}
	}
	if view.Script == nil || len(view.Script.Scenes) != 2 || view.Script.SceneRule != drama.SceneRuleMerged {
		t.Fatalf("台词表按新规则两场：%+v", view.Script)
	}
	deltas, err := svc.repo.TaskTextDeltas("user-1", done.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var stream strings.Builder
	for _, d := range deltas {
		stream.WriteString(d.Content)
	}
	if !strings.HasPrefix(stream.String(), `{"@turn":{"index":2,"total":2,"scene":"E01S02"`) {
		t.Fatalf("续写任务的文本流从第二场的标记开始：%.120s", stream.String())
	}
	if rows, _, _ := drama.StoryboardRowsFromText(stream.String()); len(rows) != 30 {
		t.Fatalf("文本流里是第二场（并场后）的 30 镜：%d", len(rows))
	}
}
