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

// qichuangSceneRows 是七床线上输出按场分好的镜头行（一镜一行）。
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
		scene, _ := row["spec"].(map[string]any)["scene"].(string)
		encoded, _ := json.Marshal(row)
		out[scene] = append(out[scene], string(encoded))
	}
	return out
}

// streamingShotsUpstream 是假的流式 Chat Completions 上游：按对话里已有几轮判断在写第几场，一行一个 SSE 增量吐出这一场的镜头；
// fail 返回非 0 时这次调用直接返回该状态码。
type streamingShotsUpstream struct {
	mu        sync.Mutex
	scenes    map[string][]string
	calls     int
	histories []int
	fail      func(call int, scene int) int
}

func (u *streamingShotsUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []json.RawMessage `json:"messages"`
		Stream   bool              `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	u.mu.Lock()
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
	lines := u.scenes[fmt.Sprintf("E01S%02d", scene)]
	if !body.Stream {
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
	req := dramaStepTaskRequest(project.ID, unit.ID, plan, DramaStepRunRequest{Model: "test", Config: map[string]any{"baseUrl": upstream, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	req.Input["metadata"].(map[string]any)["dramaRun"] = run
	input, err := normalizeTaskInput(req.Input)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.protectTaskSecrets(input); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(input)
	task := &model.Task{ID: newID(), UserID: "user-1", ProjectID: project.ID, Type: req.Type, Operation: req.Operation, Prompt: req.Prompt, Status: model.TaskStatusQueued, InputJSON: string(encoded), RouteRun: 1}
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

// 七床第一集走真实 worker 路径：一个任务内三场三轮、流式输出进同一条文本流、收尾写镜头表和段；
// 第三场一直 503 时任务失败但保留前两场，「从第 3 场继续」的新任务只写第三场就写完整集。
func TestDramaStepTaskRunsScenesAndFinalizesThroughWorker(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	failScene3 := true
	upstream := &streamingShotsUpstream{scenes: qichuangSceneRows(t), fail: func(call int, scene int) int {
		if failScene3 && scene == 3 {
			return http.StatusServiceUnavailable
		}
		return 0
	}}
	server := httptest.NewServer(upstream)
	defer server.Close()

	first := queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if timeout := taskExecutionTimeout(first, RuntimeTaskPolicy{TextTimeoutMinutes: 8, StoryboardTimeoutMinutes: 20}); timeout != 3*3*20*time.Minute+3*2*dramaTurnBackoffCap {
		t.Fatalf("三场的任务总时限：%s", timeout)
	}
	failed, err := runQueuedTask(t, svc)
	if err == nil || failed.Status != model.TaskStatusFailed || !strings.Contains(failed.Error, "第 3 场") {
		t.Fatalf("第三场失败：%v %s %q", err, failed.Status, failed.Error)
	}
	progress := taskDramaRunProgress(failed.ResultJSON)
	if progress == nil || progress.Completed != 2 || progress.Total != 3 || progress.Turns[1].Text == "" {
		t.Fatalf("失败的任务保留前两场：%+v", progress)
	}
	if upstream.calls != 2+dramaTurnMaxAttempts {
		t.Fatalf("第三场重试用尽：%d 次调用", upstream.calls)
	}
	if segments, _ := svc.DramaSegments("user-1", project.ID, unit.ID); len(segments) != 0 {
		t.Fatalf("没写完不写库：%d 段", len(segments))
	}

	failScene3, upstream.calls, upstream.histories = false, 0, nil
	queueDramaStepTask(t, svc, db, project, unit, server.URL, failed.ID)
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("从第 3 场继续：%v %s %q", err, done.Status, done.Error)
	}
	if upstream.calls != 1 || upstream.histories[0] != 4 {
		t.Fatalf("只写第三场、带前两场对话：%d %v", upstream.calls, upstream.histories)
	}
	var result struct {
		Text      string               `json:"text"`
		DramaRun  dramaRunProgress     `json:"dramaRun"`
		DramaStep DramaStepTaskOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(done.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result.DramaRun.Completed != 3 || result.DramaRun.Turns[0].TaskID != failed.ID || result.DramaRun.Turns[0].Text != "" {
		t.Fatalf("成功后的进度只留摘要：%+v", result.DramaRun)
	}
	if result.DramaStep.Status != DramaStepOutcomeWritten || result.DramaStep.Parse.Rows != 41 || result.DramaStep.Segments == 0 || result.DramaStep.Scenes != 3 {
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
	deltas, err := svc.repo.TaskTextDeltas("user-1", done.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var stream strings.Builder
	for _, d := range deltas {
		stream.WriteString(d.Content)
	}
	if !strings.HasPrefix(stream.String(), `{"@turn":{"index":3,"total":3,"scene":"E01S03"`) {
		t.Fatalf("续写任务的文本流从第三场的标记开始：%.120s", stream.String())
	}
	if rows, _, _ := drama.StoryboardRowsFromText(stream.String()); len(rows) != 14 {
		t.Fatalf("文本流里是第三场的 14 镜：%d", len(rows))
	}
}
