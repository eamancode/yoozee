package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

var dramaRunTestScenes = []drama.ShotsScene{
	{ID: "E01S01", Heading: "内·病房走廊·夜", Opening: "夜里的病房走廊，日光灯一盏接一盏。"},
	{ID: "E01S02", Heading: "内·电梯口·夜", Opening: "电梯门正在合上，一只手从里面把门挡住。"},
	{ID: "E01S03", Heading: "内·电梯口·夜", Opening: "防火门弹开，阿坤从楼梯间冲出来。"},
}

// fakeShotsUpstream 是假的 Chat Completions 上游：按对话里已有几轮判断在写第几场，每场回两镜（一镜一行）；
// fail 决定第 n 次调用（从 1 数）返回什么错误状态（0 为正常）。
type fakeShotsUpstream struct {
	mu        sync.Mutex
	calls     int
	histories []int
	prompts   []string
	fail      func(call int, scene int) int
}

func (f *fakeShotsUpstream) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		f.mu.Lock()
		f.calls++
		call := f.calls
		history := len(body.Messages) - 1
		f.histories = append(f.histories, history)
		f.prompts = append(f.prompts, body.Messages[len(body.Messages)-1].Content)
		f.mu.Unlock()
		scene := history/2 + 1
		var lines []string
		garbage := false
		if f.fail != nil {
			switch status := f.fail(call, scene); {
			case status < 0:
				// 模型没按契约写：一段解释文字，解析不出镜头行。
				lines, garbage = []string{"抱歉，我先说明一下这一场的拆法。"}, true
			case status != 0:
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"message":"upstream unavailable"}}`)
				return
			}
		}
		for i := 1; i <= 2 && !garbage; i++ {
			row, _ := json.Marshal(map[string]any{"plotDescription": fmt.Sprintf("第%d场第%d镜", scene, i), "spec": map[string]any{"scene": fmt.Sprintf("E01S%02d", scene)}})
			lines = append(lines, string(row))
		}
		reply, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": strings.Join(lines, "\n")}, "finish_reason": "stop"}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
	}
}

func newDramaRunTestService(t *testing.T) (*Service, *gorm.DB, func(id string, run dramaRunMeta) model.Task) {
	t.Helper()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	previous := dramaTurnBackoffBase
	dramaTurnBackoffBase = time.Millisecond
	t.Cleanup(func() { dramaTurnBackoffBase = previous })
	service, db, project, unit := newDramaTestService(t, "seedance-2.5")
	if err := db.AutoMigrate(&model.SystemSetting{}, &model.TaskTextDelta{}, &model.TaskLog{}); err != nil {
		t.Fatal(err)
	}
	create := func(id string, run dramaRunMeta) model.Task {
		input, _ := json.Marshal(map[string]any{"mode": "text", "metadata": map[string]any{"domainProjectId": project.ID, "chapterId": unit.ID, "dramaStep": map[string]any{"step": drama.StepChapterShots}, "dramaRun": run}})
		task := model.Task{ID: id, UserID: "user-1", ProjectID: project.ID, Type: "canvas_text", Operation: "storyboard", Status: model.TaskStatusRunning, LeaseOwner: "worker-" + id, LeaseExpiresAt: ptr(time.Now().Add(time.Hour)), InputJSON: string(input), CreatedAt: time.Now(), UpdatedAt: time.Now()}
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
		return task
	}
	return service, db, create
}

func dramaRunInput(url string) canvasGenerationInput {
	return canvasGenerationInput{Mode: "text", Prompt: "整章提示词", Config: providerConfig{BaseURL: url, APIKey: "k", Model: "m", InterfaceType: "chat-completion"}}
}

func dramaRunCtx(task model.Task) context.Context {
	return withTaskExecutionID(context.WithValue(context.Background(), mediaExecutionTaskKey{}, task), task.ID)
}

// 一个任务内按场循环：每场一轮、带上前面各轮的对话；某一轮上游 502 自动重试；每写完一场存进任务结果；
// 所有轮次的输出和回合标记都进同一个任务的文本流。
func TestDramaRunLoopsScenesInOneTask(t *testing.T) {
	service, _, create := newDramaRunTestService(t)
	upstream := &fakeShotsUpstream{fail: func(call int, scene int) int {
		switch call {
		case 1:
			return -1
		case 3:
			return http.StatusBadGateway
		}
		return 0
	}}
	server := httptest.NewServer(upstream.handler(t))
	defer server.Close()
	run := dramaRunMeta{RunID: "run-1", Step: drama.StepChapterShots, Scenes: dramaRunTestScenes, Turns: 3, ResumeFrom: 1}
	task := create("task-1", run)
	result, err := service.runDramaRun(dramaRunCtx(task), dramaRunInput(server.URL), run)
	if err != nil {
		t.Fatal(err)
	}
	if upstream.calls != 5 || fmt.Sprint(upstream.histories) != "[0 0 2 2 4]" {
		t.Fatalf("三场五次调用（第一场解析不出镜头行、第二场 502 各重试一次），历史条数 %v", upstream.histories)
	}
	if !strings.Contains(upstream.prompts[0], "整章提示词") || !strings.Contains(upstream.prompts[0], "【分回合输出】本章按正文的场景标题共 3 场") {
		t.Fatalf("第一轮是整章提示词加分回合说明：%s", upstream.prompts[0])
	}
	if !strings.HasPrefix(upstream.prompts[4], "继续写第 3 场：「内·电梯口·夜」（从「防火门弹开") || !strings.Contains(upstream.prompts[4], "shotNumber 从 5 起") {
		t.Fatalf("续写指令：%s", upstream.prompts[4])
	}
	rows, stats, err := drama.StoryboardRowsFromText(result["text"].(string))
	if err != nil || len(rows) != 6 || stats.Rate != 1 || rows[4]["plotDescription"] != "第3场第1镜" {
		t.Fatalf("结果文本是各场按顺序拼起来的镜头行：%v %d %+v", err, len(rows), stats)
	}
	progress := result["dramaRun"].(dramaRunProgress)
	if progress.Completed != 3 || progress.Total != 3 || progress.Turns[0].Attempts != 2 || progress.Turns[1].Attempts != 2 || progress.Turns[0].Text != "" || progress.Turns[2].Scene != "E01S03" {
		t.Fatalf("进度：%+v", progress)
	}
	saved, _ := service.repo.Task(task.ID)
	if p := taskDramaRunProgress(saved.ResultJSON); p == nil || p.Completed != 3 || p.Turns[2].Text == "" || saved.Stage != "已写完第 3/3 场" {
		t.Fatalf("每写完一场存进任务结果（带对话，供续写）：%+v %s", p, saved.Stage)
	}
	replay, err := service.TaskTextReplay("user-1", task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var stream strings.Builder
	for _, d := range replay.Deltas {
		stream.WriteString(d.Content)
	}
	if got := strings.Count(stream.String(), `{"@turn":`); got != 5 {
		t.Fatalf("每次调用前一行回合标记（含重试）：%d\n%s", got, stream.String())
	}
	streamRows, streamStats, _ := drama.StoryboardRowsFromText(stream.String())
	if len(streamRows) != 6 || streamStats.Rejected != 0 {
		t.Fatalf("文本流里能逐行解析出全部镜头：%d %+v", len(streamRows), streamStats)
	}
}

// 某一场重试用完仍失败：任务失败，已写完的场留在结果里；「从第 N 场继续」的新任务只写剩下的场，对话接着上一次的。
func TestDramaRunResumesFromFailedScene(t *testing.T) {
	service, db, create := newDramaRunTestService(t)
	broken := true
	upstream := &fakeShotsUpstream{fail: func(call int, scene int) int {
		if broken && scene == 3 {
			return http.StatusServiceUnavailable
		}
		return 0
	}}
	server := httptest.NewServer(upstream.handler(t))
	defer server.Close()
	run := dramaRunMeta{RunID: "run-2", Step: drama.StepChapterShots, UnitID: "unit-1", Scenes: dramaRunTestScenes, Turns: 3, ResumeFrom: 1}
	first := create("task-a", run)
	_, err := service.runDramaRun(dramaRunCtx(first), dramaRunInput(server.URL), run)
	if err == nil || !strings.Contains(err.Error(), "第 3 场「内·电梯口·夜」生成失败，已写完的 2 场已保存，可从第 3 场继续") {
		t.Fatalf("第三场失败：%v", err)
	}
	if upstream.calls != 2+dramaTurnMaxAttempts {
		t.Fatalf("失败的一场最多调 %d 次：%d", dramaTurnMaxAttempts, upstream.calls)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", first.ID).Updates(map[string]any{"status": model.TaskStatusFailed, "lease_owner": ""}).Error; err != nil {
		t.Fatal(err)
	}

	broken, upstream.calls, upstream.histories = false, 0, nil
	resumed := create("task-b", dramaRunMeta{RunID: "run-2", Step: drama.StepChapterShots, UnitID: "unit-1", Scenes: dramaRunTestScenes, Turns: 3, ResumeFrom: 3, ResumeTaskID: first.ID})
	if timeout := storyboardTaskTimeout(&resumed, RuntimeTaskPolicy{StoryboardTimeoutMinutes: 20}); timeout != 60*time.Minute+2*dramaTurnBackoffCap {
		t.Fatalf("续写任务的总时限只算剩下的一场：%s", timeout)
	}
	result, err := service.runDramaRun(dramaRunCtx(resumed), dramaRunInput(server.URL), taskDramaRunOrFail(t, resumed))
	if err != nil {
		t.Fatal(err)
	}
	if upstream.calls != 1 || upstream.histories[0] != 4 {
		t.Fatalf("只写第三场，带前两场的对话：%d %v", upstream.calls, upstream.histories)
	}
	progress := result["dramaRun"].(dramaRunProgress)
	if progress.Completed != 3 || progress.Turns[0].TaskID != first.ID || progress.Turns[2].TaskID != resumed.ID {
		t.Fatalf("前两场来自上一次任务：%+v", progress.Turns)
	}
	if rows, _, _ := drama.StoryboardRowsFromText(result["text"].(string)); len(rows) != 6 {
		t.Fatalf("结果带齐三场：%d", len(rows))
	}
}

func taskDramaRunOrFail(t *testing.T, task model.Task) dramaRunMeta {
	t.Helper()
	run := taskDramaRun(task.InputJSON)
	if run == nil {
		t.Fatal("任务没有 dramaRun")
	}
	return *run
}

// 按场次头切场：两场以上才续写；同名的场用开场原句区分。
func TestChapterShotsScenesFromHeadings(t *testing.T) {
	scenes := drama.ChapterShotsScenes(chapterPlainText(chapterHTML(t)), "第一集")
	if len(scenes) < 2 || scenes[0].ID != "E01S01" || scenes[0].Heading == "" || scenes[0].Opening == "" {
		t.Fatalf("七床按场次头切场：%+v", scenes)
	}
	if got := drama.ChapterShotsScenes("林小满：七床？\n周岸：别喊。", "t"); got != nil {
		t.Fatalf("没有场次头整章一轮：%+v", got)
	}
}

func TestDramaTurnRetryable(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		err  error
		want bool
	}{
		{providerHTTPError{StatusCode: 502}, true},
		{providerHTTPError{StatusCode: 429}, true},
		{providerHTTPError{StatusCode: 400}, false},
		{providerHTTPError{StatusCode: 401}, false},
		{context.DeadlineExceeded, true},
		{errDramaTurnUnparsed, true},
		{context.Canceled, false},
		{fmt.Errorf("wrapped: %w", providerHTTPError{StatusCode: 503}), true},
	} {
		if got := dramaTurnRetryable(ctx, c.err); got != c.want {
			t.Fatalf("%v：%v", c.err, got)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if dramaTurnRetryable(cancelled, providerHTTPError{StatusCode: 502}) {
		t.Fatal("任务取消后不重试")
	}
}

// 失败原因以句号收尾：页面在后面接「原文和已有剧集都没有改动。」，解析错误不带标点时会连成一句。
func TestDramaRunTurnErrorEndsWithFullStop(t *testing.T) {
	marker := dramaTurnMarker{Index: 1, Total: 2}
	err := dramaRunTurnError("集", marker, 0, time.Minute, fmt.Errorf("%w：%v", errDramaTurnUnparsed, errors.New("这一集的 JSON 头没写 sources")))
	if got := err.Error(); got != "第 1 集生成失败：这一轮的输出解析不出来：这一集的 JSON 头没写 sources。" || !errors.Is(err, errDramaTurnUnparsed) {
		t.Fatalf("解析失败的原因补句号、仍能认出原错误：%q", got)
	}
	if got := dramaRunTurnError("集", marker, 0, time.Minute, errors.New("上游返回 503。")).Error(); strings.HasSuffix(got, "。。") {
		t.Fatalf("已有句号不重复：%q", got)
	}
	if got := dramaRunTurnError("场", dramaTurnMarker{Index: 3, Total: 3}, 2, 8*time.Minute, context.DeadlineExceeded).Error(); got != "第 3 场生成超时（每场 8 分钟），已写完的 2 场已保存，可从第 3 场继续。" {
		t.Fatalf("超时原因：%q", got)
	}
}
