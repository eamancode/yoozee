package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// streamingShotsUpstream 是假的流式 Chat Completions 上游：按对话里已有几轮判断在写第几场（集），一行一个 SSE 增量吐出这一轮的内容；
// fail 返回非 0 时这次调用直接返回该状态码；prompts 记每次调用的本轮提示词。
type streamingShotsUpstream struct {
	mu        sync.Mutex
	scenes    map[string][]string
	calls     int
	histories []int
	prompts   []string
	fail      func(call int, scene int) int
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
	plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, nil)
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
