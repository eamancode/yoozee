package app

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// longQichuangChapter 是七床人物演的长章：scenes 场轮流在病房走廊、电梯口，每场 lines 句两人对白（文戏 15 两句一节）。
func longQichuangChapter(scenes int, lines int) string {
	var b strings.Builder
	places := []string{"内·病房走廊·夜", "内·电梯口·夜"}
	for i := 1; i <= scenes; i++ {
		fmt.Fprintf(&b, "## %s\n\n（日光灯一盏接一盏，林小满抱着病历夹站在原地。）\n", places[(i-1)%2])
		for j := 1; j <= lines; j++ {
			speaker := "林小满"
			if j%2 == 0 {
				speaker = "周岸"
			}
			fmt.Fprintf(&b, "%s：第%d场第%d句，我们得快点走。\n", speaker, i, j)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func seedLongQichuang(t *testing.T, svc *Service, db *gorm.DB, scenes int, lines int) (model.Project, model.ProjectUnit) {
	t.Helper()
	project, unit := seedQichuang(t, svc, db)
	project = qichuangOnModel(t, svc, db, project, "seedance-2.0")
	source := plainTextToHTML(longQichuangChapter(scenes, lines))
	if err := db.Model(&model.ProjectUnit{}).Where("id = ?", unit.ID).Update("source_text", source).Error; err != nil {
		t.Fatal(err)
	}
	unit.SourceText = source
	return project, unit
}

// 一章 100 节、300 镜的文戏 15：收尾一次写入 300 镜、按节装成 100 段（原先整章 200 行的上限会在模型写完后拒绝）。
// 收尾失败（这里把上限临时调到 100 镜）时任务失败、原因写明本章多少镜和上限；各节输出留在任务里，
// 接着这次任务「重新收尾」不再调模型，直接用存下的输出写进镜头表。
func TestSectionShotsLargeChapterFinalizesAndRefinalizes(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedLongQichuang(t, svc, db, 20, 10)
	upstream := &sectionShotsUpstream{}
	server := httptest.NewServer(upstream)
	defer server.Close()

	saved := dramaChapterMaxShots
	dramaChapterMaxShots = 100
	defer func() { dramaChapterMaxShots = saved }()
	first := queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	shots := taskDramaShots(first.InputJSON)
	if shots == nil || shots.Genre != drama.ShotGenreWenxi15 || len(shots.Sections) != 100 {
		t.Fatalf("20 场每场 10 句切成 100 节：%+v", shots)
	}
	failed, err := runQueuedTask(t, svc)
	if err == nil || failed.Status != model.TaskStatusFailed || !strings.Contains(failed.Error, "本章 300 个镜头超过一章 100 个镜头的上限") || !strings.Contains(failed.Error, "拆成两章") {
		t.Fatalf("收尾超上限失败、说清多少镜和上限：%v %s %q", err, failed.Status, failed.Error)
	}
	if upstream.calls != 100 {
		t.Fatalf("100 节各调一次：%d", upstream.calls)
	}
	if progress := taskDramaRunProgress(failed.ResultJSON); progress == nil || progress.Completed != 100 || len(progress.Turns) != 100 || progress.Turns[99].Text == "" {
		t.Fatal("收尾失败时各节输出仍留在任务结果里")
	}

	dramaChapterMaxShots = saved
	retry := queueDramaStepTask(t, svc, db, project, unit, server.URL, failed.ID)
	if run := taskDramaRun(retry.InputJSON); run == nil || run.ResumeFrom != 101 || run.ResumeTaskID != failed.ID {
		t.Fatalf("各节都写完了：从第 101 节接着＝只收尾：%+v", run)
	}
	// 重新写入不建计费单（见 TestRefinalizeRunIsNotBilled）：去掉测试辅助函数挂上的单，worker 照常跑完、余额不动。
	if err := db.Model(&model.Task{}).Where("id = ?", retry.ID).Update("billing_order_id", "").Error; err != nil {
		t.Fatal(err)
	}
	var wallet model.CreditAccount
	db.First(&wallet, "user_id = ?", "user-1")
	start := time.Now()
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("重新收尾：%v %s %q", err, done.Status, done.Error)
	}
	t.Logf("300 镜收尾（转换、检查、装 100 段、写库）用时 %s", time.Since(start))
	if upstream.calls != 100 {
		t.Fatalf("重新收尾不再调模型：%d 次调用", upstream.calls)
	}
	var after model.CreditAccount
	db.First(&after, "user_id = ?", "user-1")
	if after.AvailableMicrocredits != wallet.AvailableMicrocredits || after.ReservedMicrocredits != wallet.ReservedMicrocredits {
		t.Fatalf("没有计费单的重新写入不动余额：%+v → %+v", wallet, after)
	}
	var result struct {
		DramaStep DramaStepTaskOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(done.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result.DramaStep.Status != DramaStepOutcomeWritten || result.DramaStep.Shots != 300 || result.DramaStep.Sections != 100 || result.DramaStep.Segments != 100 {
		t.Fatalf("写入 300 镜、100 节装 100 段：%+v", result.DramaStep)
	}
	view, err := svc.DramaShotlist("user-1", project.ID, unit.ID)
	if err != nil || view == nil || len(view.Shotlist.Shots) != 300 || len(view.Sections) != 100 || view.Sections[0].TaskID != failed.ID {
		t.Fatalf("镜头表 300 镜、各节原样输出指向写出它的任务：%v", err)
	}
	// 300 镜的镜头表重新装段（读存下的分镜行再走一遍写入）也不受原先 200 行的上限挡。
	if _, err := svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{}); err != nil {
		t.Fatalf("300 镜重新装段：%v", err)
	}
}

// 各节都写完的生成分段任务能重新收尾，其余步骤（剧本优化等）写完了仍要求重新跑；没写完的照旧从第 N 节继续。
func TestResumeAfterAllTurnsOnlyForChapterShots(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedLongQichuang(t, svc, db, 2, 4)
	plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := newDramaRunMeta(plan, unit.ID)
	turns := make([]dramaRunTurn, 0, run.Turns)
	for k := 1; k <= run.Turns; k++ {
		turns = append(turns, dramaRunTurn{Index: k, Prompt: "p", Text: "t"})
	}
	progress := dramaRunProgress{RunID: run.RunID, Total: run.Turns, Completed: run.Turns, Turns: turns}
	req := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: "test"})
	req.Input["metadata"].(map[string]any)["dramaRun"] = run
	task := insertDramaTestTask(t, svc, db, project.ID, req)
	result, _ := json.Marshal(map[string]any{"mode": "text", "dramaRun": progress})
	if err := db.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{"status": model.TaskStatusFailed, "result_json": string(result)}).Error; err != nil {
		t.Fatal(err)
	}
	resumed, err := svc.resumeDramaRun("user-1", project.ID, unit.ID, plan, task.ID)
	if err != nil || resumed.ResumeFrom != run.Turns+1 {
		t.Fatalf("生成分段写完了可以只收尾：%v %+v", err, resumed)
	}
	adapt := *plan
	adapt.usage.Step = drama.StepScriptAdapt
	if _, err := svc.resumeDramaRun("user-1", project.ID, unit.ID, &adapt, task.ID); err == nil {
		t.Fatal("别的步骤写完了不走重新收尾")
	}
}

// 写镜头表的数量、体积上限：超过一章上限的说清镜头数和上限；Agent 一次塞进超大的分镜行按总字节拒绝。
func TestShotlistRowsWithinCap(t *testing.T) {
	if err := shotlistRowsWithinCap(nil); err == nil {
		t.Fatal("没有分镜行要拒绝")
	}
	rows := make([]json.RawMessage, dramaChapterMaxShots+1)
	for i := range rows {
		rows[i] = json.RawMessage(`{}`)
	}
	if err := shotlistRowsWithinCap(rows); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("本章 %d 个镜头超过一章 %d 个镜头的上限", dramaChapterMaxShots+1, dramaChapterMaxShots)) {
		t.Fatalf("超上限：%v", err)
	}
	if err := shotlistRowsWithinCap(rows[:dramaChapterMaxShots]); err != nil {
		t.Fatalf("上限内放行：%v", err)
	}
	big := json.RawMessage(`"` + strings.Repeat("x", dramaShotlistMaxBytes/4) + `"`)
	if err := shotlistRowsWithinCap([]json.RawMessage{big, big, big, big, big}); err == nil || !strings.Contains(err.Error(), "MB") {
		t.Fatalf("总字节超上限：%v", err)
	}
}

// 建任务前按各节估时（一秒一镜）预估：超过一章镜头上限的不建任务，免得写完才被拒。
func TestChapterShotsOverCapBeforeRun(t *testing.T) {
	sections := make([]drama.Section, 0, 150)
	for i := 0; i < 150; i++ {
		sections = append(sections, drama.Section{ID: fmt.Sprintf("E01S01-P%d", i+1), Est: 12})
	}
	if err := chapterShotsOverCap(&dramaShotsMeta{Sections: sections}); err != nil {
		t.Fatalf("150 节 × 12 秒 = 30 分钟以内不拦：%v", err)
	}
	more := append(sections, sections...)
	if err := chapterShotsOverCap(&dramaShotsMeta{Sections: more}); err == nil || !strings.Contains(err.Error(), "本章切成 300 节") {
		t.Fatalf("300 节 × 12 秒 = 60 分钟拦下：%v", err)
	}
	if err := chapterShotsOverCap(nil); err != nil {
		t.Fatal("不是按小节生成不拦")
	}
}

// 「用已写好的 N 节重新写入」一次上游都不调：不建计费单、不预授权，余额和流水都不动；
// 没写完的「从第 N 节继续」要接着调模型，照常预授权。
func TestRefinalizeRunIsNotBilled(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedLongQichuang(t, svc, db, 2, 4)
	for _, row := range []any{
		&model.ModelChannel{ID: "ch-text", Scope: "system", Enabled: true, Name: "text", BaseURL: "https://upstream.example.com/v1", APIFormat: "openai"},
		&model.ChannelModel{ID: "cm-text", ChannelID: "ch-text", ModelKey: "fake-shots", Capability: "text", Protocol: "chat-completion", CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, DefaultModelCapabilityConfigForModel("chat-completion", "fake-shots")), BillingMode: "fixed_request", UnitPriceMicrocredits: 300_000, PriceConfigured: true, Enabled: true},
		&model.ChannelModelPriceTier{ID: "tier-text", ChannelModelID: "cm-text", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 300_000, PriceConfigured: true, Enabled: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, nil)
	if err != nil {
		t.Fatal(err)
	}
	failedRun := func(completed int) string {
		run := newDramaRunMeta(plan, unit.ID)
		turns := make([]dramaRunTurn, 0, completed)
		for k := 1; k <= completed; k++ {
			turns = append(turns, dramaRunTurn{Index: k, Prompt: "p", Text: "t"})
		}
		req := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: "test"})
		req.Input["metadata"].(map[string]any)["dramaRun"] = run
		task := insertDramaTestTask(t, svc, db, project.ID, req)
		result, _ := json.Marshal(map[string]any{"mode": "text", "dramaRun": dramaRunProgress{RunID: run.RunID, Total: run.Turns, Completed: completed, Turns: turns}})
		if err := db.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{"status": model.TaskStatusFailed, "result_json": string(result)}).Error; err != nil {
			t.Fatal(err)
		}
		return task.ID
	}
	account := func() model.CreditAccount {
		var a model.CreditAccount
		if err := db.First(&a, "user_id = ?", "user-1").Error; err != nil {
			t.Fatal(err)
		}
		return a
	}
	ledger := func() int64 {
		var n int64
		db.Model(&model.CreditLedgerEntry{}).Where("user_id = ?", "user-1").Count(&n)
		return n
	}
	runReq := func(resume string) DramaStepRunRequest {
		return DramaStepRunRequest{Model: "fake-shots", Config: map[string]any{"channelId": "ch-text", "model": "fake-shots", "textModel": "fake-shots"}, ResumeTaskID: resume}
	}
	// 两次失败的前任务先建好（测试辅助函数会改写余额），再给账户充值、记下余额和流水。
	allWritten, oneWritten := failedRun(len(plan.scenes)), failedRun(1)
	if err := db.Model(&model.CreditAccount{}).Where("user_id = ?", "user-1").Updates(map[string]any{"available_microcredits": 10_000_000, "reserved_microcredits": 0}).Error; err != nil {
		t.Fatal(err)
	}
	before, entries := account(), ledger()

	refinalize, err := svc.RunDramaStep("user-1", project.ID, unit.ID, drama.StepChapterShots, runReq(allWritten))
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := svc.repo.Task(refinalize.Task.ID)
	if stored.BillingOrderID != "" || account() != before || ledger() != entries {
		t.Fatalf("重新写入不建计费单、余额和流水不动：order=%q %+v → %+v，流水 %d → %d", stored.BillingOrderID, before, account(), entries, ledger())
	}

	resume, err := svc.RunDramaStep("user-1", project.ID, unit.ID, drama.StepChapterShots, runReq(oneWritten))
	if err != nil {
		t.Fatal(err)
	}
	stored, _ = svc.repo.Task(resume.Task.ID)
	if after := account(); stored.BillingOrderID == "" || after.ReservedMicrocredits <= before.ReservedMicrocredits {
		t.Fatalf("从第 2 节继续照常预授权：order=%q %+v", stored.BillingOrderID, after)
	}
}
