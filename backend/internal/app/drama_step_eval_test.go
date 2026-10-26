package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

var updateDramaEvalSet = flag.Bool("update-drama-evalset", false, "按种子重建 internal/drama/evalset/ 的评测章节数据")

const dramaEvalSeedsDir = "testdata/drama-eval-seeds"

// dramaEvalSeedDir 是评测章节的种子目录：七床与生成分段的黄金对照共用一份。
func dramaEvalSeedDir(id string) string {
	if id == "qichuang-e01" {
		return qichuangDir
	}
	return filepath.Join(dramaEvalSeedsDir, id)
}

// 评测集的数据由种子按真实用户路径建项目后导出（-update-drama-evalset 重建）。每章都要能读、档案对得上评测集；
// 七床第一章用评测数据组装的提示词与生成分段的黄金文件逐字相同（评测与用户点「生成分段」同一条路径）。
func TestDramaEvalSetFixtures(t *testing.T) {
	step, _ := drama.StepByID(drama.StepChapterShots)
	if *updateDramaEvalSet {
		for _, c := range step.EvalSet {
			svc, db := newChapterShotsTestService(t)
			project, unit := seedChapterShotsProject(t, svc, db, dramaEvalSeedDir(c.ID))
			if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("style_profile", c.Profile).Error; err != nil {
				t.Fatal(err)
			}
			writeDramaEvalChapter(t, svc, c.ID, project.ID, unit)
		}
	}
	svc, _ := newChapterShotsTestService(t)
	pkg, err := svc.skillDomain().StepSkillPackage(step.DefaultSkill)
	if err != nil {
		t.Fatal(err)
	}
	profiles := map[string]bool{}
	for _, c := range step.EvalSet {
		profiles[c.Profile] = true
		in, err := loadDramaEvalChapter(c)
		if err != nil {
			t.Fatal(err)
		}
		if in.project.StyleProfile != c.Profile || in.project.GenerationModel == "" || briefEpisodeSeconds(in.project.BriefJSON) != 90 {
			t.Fatalf("%s 项目：%+v", c.ID, in.project)
		}
		plan, err := chapterShotsPlan(in.project, in.unit, in.assets, DramaStepUsage{}, stepSkillOf(pkg))
		if err != nil {
			t.Fatalf("%s：%v", c.ID, err)
		}
		if script := drama.ScriptFromText(chapterPlainText(in.unit.SourceText), in.unit.Title); len(script.Scenes) == 0 {
			t.Fatalf("%s 正文读不出场", c.ID)
		}
		if c.ID == "qichuang-e01" {
			if want := readTestdata(t, "server-prompt.txt"); plan.prompt.Text != want {
				t.Fatalf("七床评测数据组装的提示词应与黄金文件相同：\n%s", firstDiff(want, plan.prompt.Text))
			}
		}
	}
	for _, id := range drama.BuiltinProfileIDs() {
		if !profiles[id] {
			t.Fatalf("风格档案 %s 没有评测章节", id)
		}
	}
}

func writeDramaEvalChapter(t *testing.T, svc *Service, id string, projectID string, unit model.ProjectUnit) {
	t.Helper()
	dir := filepath.Join("..", "drama", "evalset", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.repo.ProjectForUser("user-1", projectID)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := svc.ProjectAssets("user-1", projectID)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"project.json": stored, "unit.json": unit, "assets.json": assets} {
		raw, err := json.MarshalIndent(value, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// 解析器、资产绑定、检查和装段：七床第一章的一份线上真实输出（model-output.txt）按与章节页相同的路径处理。
func TestDramaStepEvalChapterResultOnRealOutput(t *testing.T) {
	step, _ := drama.StepByID(drama.StepChapterShots)
	c, _ := step.EvalChapter("qichuang-e01")
	phrases, err := drama.DefaultCompileProfile()
	if err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(map[string]any{"mode": "text", "text": "```json\n" + readTestdata(t, "model-output.txt") + "\n```"})
	task := &model.Task{ID: "task-1", Status: model.TaskStatusSucceeded, ResultJSON: string(result)}
	got, infra := dramaStepEvalChapterResult(step, c, task.ID, task, phrases)
	if infra != "" || !got.Parsed || got.Failure != "" {
		t.Fatalf("应解析成功：%+v %s", got, infra)
	}
	if got.Rows == 0 || got.SpecRows != got.Rows || got.LinesTotal == 0 || got.LinesExact != got.LinesTotal || got.Errors != 0 || got.TargetSeconds != 90 {
		t.Fatalf("七床线上输出：%+v", got)
	}
	if got.Segments == 0 || got.EstSeconds <= 0 || got.Shots != got.Rows {
		t.Fatalf("应装出段：%+v", got)
	}
	t.Logf("七床线上输出：%d 行，台词 %d/%d，error %d warn %d，估时 %.1f 秒，%d 段 %.1f 秒，规则 %v", got.Rows, got.LinesExact, got.LinesTotal, got.Errors, got.Warnings, got.EstSeconds, got.Segments, got.SegmentSeconds, got.Rules)

	// 删掉 spec 说明的坏包写不出 spec：每行只剩平铺字段。
	var payload map[string]any
	if err := json.Unmarshal([]byte(readTestdata(t, "model-output.txt")), &payload); err != nil {
		t.Fatal(err)
	}
	for _, row := range payload["rows"].([]any) {
		delete(row.(map[string]any), "spec")
	}
	noSpec, _ := json.Marshal(payload)
	result, _ = json.Marshal(map[string]any{"mode": "text", "text": string(noSpec)})
	bad, infra := dramaStepEvalChapterResult(step, c, "task-2", &model.Task{Status: model.TaskStatusSucceeded, ResultJSON: string(result)}, phrases)
	if infra != "" || !bad.Parsed || bad.SpecRows != 0 || bad.Rows != got.Rows {
		t.Fatalf("没有 spec：%+v", bad)
	}
	verdict := step.Gate.Evaluate([]drama.StepEvalChapterResult{bad}, []drama.StepEvalChapterResult{got})
	if verdict.Passed || gateCheck(verdict, drama.GateSpecRate).Passed {
		t.Fatalf("缺 spec 的包要被门槛挡下：%+v", verdict)
	}

	// 截断：任务失败、诊断原因是 text_output_truncated，算包的问题（截断 1、解析失败），不算评测出错。
	truncated := &model.Task{Status: model.TaskStatusFailed, Error: "模型输出达到上限被截断", ExecutionDiagnosticJSON: `{"code":"text_output_truncated"}`}
	cut, infra := dramaStepEvalChapterResult(step, c, "task-3", truncated, phrases)
	if infra != "" || !cut.Truncated || cut.Parsed {
		t.Fatalf("截断：%+v %s", cut, infra)
	}
	upstream := &model.Task{Status: model.TaskStatusFailed, Error: "上游 502"}
	if _, infra := dramaStepEvalChapterResult(step, c, "task-4", upstream, phrases); infra == "" {
		t.Fatal("上游失败算评测出错，可以重跑")
	}
	garbage, _ := json.Marshal(map[string]any{"mode": "text", "text": "抱歉，我无法完成"})
	if r, infra := dramaStepEvalChapterResult(step, c, "task-5", &model.Task{Status: model.TaskStatusSucceeded, ResultJSON: string(garbage)}, phrases); infra != "" || r.Parsed {
		t.Fatalf("解析不出镜头行算包的问题：%+v %s", r, infra)
	}
}

func gateCheck(v drama.StepGateVerdict, metric string) drama.StepGateCheck {
	for _, c := range v.Checks {
		if c.Metric == metric {
			return c
		}
	}
	return drama.StepGateCheck{}
}

// newDramaStepEvalTestService 是带系统文本渠道和积分的服务：评测任务能真的建出来（不执行）。
func newDramaStepEvalTestService(t *testing.T) (*Service, *gorm.DB, *model.User) {
	t.Helper()
	svc, db := newChapterShotsTestService(t)
	admin := &model.User{ID: "admin-1", Username: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive}
	capability := mustEncodeModelCapabilityConfig(t, DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test"))
	for _, row := range []any{
		admin,
		&model.ModelChannel{ID: "channel", Scope: model.ChannelScopeSystem, Enabled: true, Name: "受控测试"},
		&model.ChannelModel{ID: "cm", ChannelID: "channel", ModelKey: "text-test", Capability: "text", Protocol: model.ChannelInterfaceChatCompletion, CapabilityConfigJSON: capability, BillingMode: "fixed_request", UnitPriceMicrocredits: 100, PriceConfigured: true, Enabled: true},
		&model.ChannelModelPriceTier{ID: "tier", ChannelModelID: "cm", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 100, PriceConfigured: true, Enabled: true},
		&model.CreditAccount{UserID: admin.ID, AvailableMicrocredits: 1_000_000},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return svc, db, admin
}

var dramaStepEvalTestModel = DramaStepEvalModel{Model: "text-test", Config: map[string]any{"channelId": "channel", "model": "text-test"}}

// dramaStepEvalWorker 模拟任务执行：给一个已投出、还没结束的任务写结局。
type dramaStepEvalWorker func(t *testing.T, task *model.Task, slot dramaStepEvalTask)

// succeedWith 把任务写成成功，输出取 output(章节, 角色)。
func succeedWith(db *gorm.DB, output func(chapter, role string) string) dramaStepEvalWorker {
	return func(t *testing.T, task *model.Task, slot dramaStepEvalTask) {
		t.Helper()
		result, _ := json.Marshal(map[string]any{"mode": "text", "text": output(slot.Chapter, slot.Role)})
		if err := db.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": string(result)}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// failUnsubmitted 把任务写成线上那种失败：上游 502、诊断说没提交上游（任务执行失败路径记下的同一套字段）。
func failUnsubmitted(t *testing.T, db *gorm.DB, task *model.Task, submitted bool) {
	t.Helper()
	cause := providerHTTPError{StatusCode: 502, Status: "502 Bad Gateway"}
	task.Error = providerUserFacingErrorMessage(cause)
	outcome := "unknown"
	if submitted {
		outcome = "accepted"
	}
	recordTaskDiagnostic(task, "execution", "provider_execution_failed", outcome, "reconcile_submission", cause)
	if err := db.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": task.Error, "execution_diagnostic_json": task.ExecutionDiagnosticJSON}).Error; err != nil {
		t.Fatal(err)
	}
}

// driveDramaStepEvals 一轮一轮地推进：每轮先让 worker 处理所有已投出、没结束的评测任务，再推进一次，直到 evalIDs 都落定。
// 返回同一时刻在跑的评测任务最多几个（跨所有评测）。
func driveDramaStepEvals(t *testing.T, svc *Service, worker dramaStepEvalWorker, evalIDs ...string) int {
	t.Helper()
	peak := 0
	for round := 0; round < 50; round++ {
		runs, err := svc.repo.DramaEvalRunsWithStatus(dramaStepEvalInFlight, 50)
		if err != nil {
			t.Fatal(err)
		}
		active := 0
		for _, run := range runs {
			var slots []dramaStepEvalTask
			_ = json.Unmarshal([]byte(run.TasksJSON), &slots)
			for _, slot := range slots {
				if slot.TaskID == "" {
					continue
				}
				task, err := svc.repo.Task(slot.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				if task.Status != model.TaskStatusQueued && task.Status != model.TaskStatusRunning {
					continue
				}
				active++
				if !strings.Contains(task.InputJSON, dramaStepEvalTaskSource) {
					t.Fatalf("评测任务要标 source：%s", task.InputJSON)
				}
				worker(t, task, slot)
			}
		}
		peak = max(peak, active)
		svc.advanceDramaStepEvals(context.Background())
		pending := false
		for _, id := range evalIDs {
			run, err := svc.repo.DramaEvalRun(id)
			if err != nil {
				t.Fatal(err)
			}
			if run.Status == DramaStepEvalQueued || run.Status == DramaStepEvalRunning {
				pending = true
			}
		}
		if !pending {
			return peak
		}
	}
	t.Fatal("评测推进 50 轮还没落定")
	return peak
}

func flowPackageEval(t *testing.T, svc *Service, admin *model.User, versionID string) *DramaStepEvalView {
	t.Helper()
	_, versions, err := svc.AdminFlowPackageVersions(admin, "yz-chapter-shots")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range versions {
		if v.ID == versionID {
			return v.Eval
		}
	}
	t.Fatalf("没有版本 %s", versionID)
	return nil
}

// 第三期验收（本机模拟模型输出）：只覆盖真人剧的小改动包，上传即在《七床》第一集上评测（启用版本没评测过，一起跑对照）；
// 候选与对照输出相同，过门槛就自动启用并记审计。坏包（删掉 spec 说明，输出不带 spec）被门槛挡下保持未启用；
// 强制启用要写原因；再上传一个包时复用启用版本上次的结果作对照，不再跑对照任务。
func TestDramaStepEvalGateAutoActivates(t *testing.T) {
	svc, db, admin := newDramaStepEvalTestService(t)
	noDramaStepEvalRetryDelay(t)
	output := readTestdata(t, "model-output.txt")
	// 线上真实输出估时偏长：按每集时长压到门槛内，其余不动（门槛判定本身另有单元测试）。
	fitted := fitDramaEvalOutput(t, output, 95)
	upload := func(version string, edit func(map[string][]byte)) *FlowPackageUploadResponse {
		t.Helper()
		data := chapterShotsPackageZip(t, func(files map[string][]byte) {
			edit(files)
			bumpManifest(t, files, func(m *drama.StepPackageManifest) {
				m.Version, m.Profiles, m.Eval = version, []string{"live-realism"}, []string{}
			})
		})
		res, err := svc.UploadFlowPackage(admin, packageFileHeader(t, data), "", dramaStepEvalTestModel)
		if err != nil {
			t.Fatal(err)
		}
		if res.EvalError != "" || res.Version.Eval == nil || res.Version.Eval.Status != DramaStepEvalQueued {
			t.Fatalf("上传后应开始评测：%+v", res)
		}
		return res
	}
	activeVersion := func() string {
		t.Helper()
		var skill model.Skill
		if err := db.First(&skill, "id = ?", "yz-chapter-shots").Error; err != nil {
			t.Fatal(err)
		}
		return skill.CurrentVersionID
	}
	seed := activeVersion()

	good := upload("2.2.2", func(files map[string][]byte) {
		files["SKILL.md"] = []byte(strings.Replace(string(files["SKILL.md"]), "你是这一章的导演。", "你是这一章的总导演。", 1))
	})
	if eval := good.Version.Eval; eval.TasksTotal != 2 || eval.TasksRunning != 1 || eval.TasksWaiting != 1 || len(eval.Chapters) != 1 || eval.Chapters[0].ID != "qichuang-e01" || eval.BaselineSkillVersionID != seed {
		t.Fatalf("真人剧一章，候选加对照两个任务位，先投一个：%+v", eval)
	}
	if _, err := svc.StartDramaStepEval(admin, "yz-chapter-shots", good.Version.ID, dramaStepEvalTestModel); err == nil {
		t.Fatal("同一版本正在评测时不能再开一次")
	}
	svc.advanceDramaStepEvals(context.Background())
	if eval := flowPackageEval(t, svc, admin, good.Version.ID); eval.Status != DramaStepEvalQueued || eval.TasksDone != 0 || eval.TasksRunning != 1 {
		t.Fatalf("任务没结束时还在排队、不多投：%+v", eval)
	}
	checked := 0
	peak := driveDramaStepEvals(t, svc, func(t *testing.T, task *model.Task, slot dramaStepEvalTask) {
		if strings.Contains(task.Prompt, "你是这一章的总导演。") != (slot.Role == dramaStepEvalRoleCandidate) || !strings.Contains(task.Prompt, "林小满") || task.UserID != admin.ID || task.ProjectID != "" {
			t.Fatalf("%s 任务的提示词用对应版本组装：%s…", slot.Role, task.Prompt[:min(200, len(task.Prompt))])
		}
		checked++
		succeedWith(db, func(string, string) string { return fitted })(t, task, slot)
	}, good.Version.Eval.ID)
	if checked != 2 || peak != 1 {
		t.Fatalf("候选、对照各跑一次，同时只跑一个：%d 个，最多 %d 个并发", checked, peak)
	}
	eval := flowPackageEval(t, svc, admin, good.Version.ID)
	if eval.Status != DramaStepEvalPassed || eval.Activation != "auto" || eval.Gate == nil || !eval.Gate.Passed || eval.Result == nil || len(eval.Result.Baseline) != 1 {
		t.Fatalf("过门槛应自动启用：%+v", eval)
	}
	if activeVersion() != good.Version.ID {
		t.Fatal("自动启用后生效版本应是新包")
	}
	var audits int64
	db.Model(&model.AdminAuditEvent{}).Where("action = ?", "drama_package.auto_activate").Count(&audits)
	if audits != 1 {
		t.Fatalf("自动启用要记审计：%d", audits)
	}

	// 坏包：删掉 SKILL.md 里 spec 的说明，模型照此不写 spec。对照复用 2.2.2 刚才的结果，只建候选任务。
	bad := upload("2.2.3", func(files map[string][]byte) {
		files["SKILL.md"] = []byte(strings.Replace(string(files["SKILL.md"]), "spec", "备注", -1))
	})
	if bad.Version.Eval.TasksTotal != 1 {
		t.Fatalf("对照应复用上次的结果：%+v", bad.Version.Eval)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(fitted), &payload)
	for _, row := range payload["rows"].([]any) {
		delete(row.(map[string]any), "spec")
	}
	noSpec, _ := json.Marshal(payload)
	driveDramaStepEvals(t, svc, succeedWith(db, func(string, string) string { return string(noSpec) }), bad.Version.Eval.ID)
	eval = flowPackageEval(t, svc, admin, bad.Version.ID)
	if eval.Status != DramaStepEvalFailed || eval.Activation != "" || gateCheck(*eval.Gate, drama.GateSpecRate).Passed || eval.Result.BaselineRunID != good.Version.Eval.ID {
		t.Fatalf("缺 spec 的包未通过、不启用：%+v", eval)
	}
	if activeVersion() != good.Version.ID {
		t.Fatal("未通过的包不能被启用")
	}
	_, err := svc.ActivateFlowPackageVersion(admin, "yz-chapter-shots", bad.Version.ID, FlowPackageActivateRequest{Force: true})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Reason != ReasonPackageEvalNotPassed {
		t.Fatalf("没写原因不能强制启用：%v", err)
	}
	if _, err := svc.ActivateFlowPackageVersion(admin, "yz-chapter-shots", bad.Version.ID, FlowPackageActivateRequest{Force: true, Reason: "线上急修，先用"}); err != nil {
		t.Fatal(err)
	}
	if eval = flowPackageEval(t, svc, admin, bad.Version.ID); eval.Activation != "forced" || eval.ActivationReason != "线上急修，先用" || activeVersion() != bad.Version.ID {
		t.Fatalf("强制启用记原因：%+v", eval)
	}
	// 回退到没评测过的种子版本不用写原因。
	if _, err := svc.ActivateFlowPackageVersion(admin, "yz-chapter-shots", seed, FlowPackageActivateRequest{}); err != nil {
		t.Fatal(err)
	}
	// 上游失败算评测出错（不是未通过），可以重跑。
	retry, err := svc.StartDramaStepEval(admin, "yz-chapter-shots", bad.Version.ID, dramaStepEvalTestModel)
	if err != nil {
		t.Fatal(err)
	}
	// 已经提交上游的失败不重投（免得重复计费），这一章失败、评测记出错。
	driveDramaStepEvals(t, svc, func(t *testing.T, task *model.Task, _ dramaStepEvalTask) { failUnsubmitted(t, db, task, true) }, retry.ID)
	if eval = flowPackageEval(t, svc, admin, bad.Version.ID); eval.Status != DramaStepEvalError || eval.Error == "" || eval.Retries != 0 {
		t.Fatalf("提交后的上游失败算出错、不重投：%+v", eval)
	}
}

func noDramaStepEvalRetryDelay(t *testing.T) {
	t.Helper()
	saved := dramaStepEvalRetryDelays
	dramaStepEvalRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { dramaStepEvalRetryDelays = saved })
}

// 线上现象（2.2.1+bad.1 连跑两次）：十个评测任务一起投，渠道只扛得住一个，其余在提交前 502，评测判出错。
// 模拟渠道只允许 1 个并发、前几次返回 502：连续上传两个包同时评测，全局同时只跑一个任务；提交前失败的任务重投，
// 两个评测最终都跑完并给出门槛结论。重投次数用尽才算这一章失败。
func TestDramaStepEvalThrottlesAndRetries(t *testing.T) {
	svc, db, admin := newDramaStepEvalTestService(t)
	noDramaStepEvalRetryDelay(t)
	fitted := fitDramaEvalOutput(t, readTestdata(t, "model-output.txt"), 95)
	upload := func(version string) *FlowPackageUploadResponse {
		t.Helper()
		data := chapterShotsPackageZip(t, func(files map[string][]byte) {
			files["SKILL.md"] = append(files["SKILL.md"], []byte("\n<!-- "+version+" -->\n")...)
			bumpManifest(t, files, func(m *drama.StepPackageManifest) {
				m.Version, m.Profiles, m.Eval = version, []string{"live-realism"}, []string{}
			})
		})
		res, err := svc.UploadFlowPackage(admin, packageFileHeader(t, data), "", dramaStepEvalTestModel)
		if err != nil || res.EvalError != "" {
			t.Fatalf("上传：%+v %v", res, err)
		}
		return res
	}
	first, second := upload("2.2.2"), upload("2.2.3")
	if first.Version.Eval.TasksRunning != 1 || second.Version.Eval.TasksRunning != 0 || second.Version.Eval.TasksWaiting != 2 {
		t.Fatalf("两个评测共用一个并发位：%+v / %+v", first.Version.Eval, second.Version.Eval)
	}
	// 渠道：第 1、2、4 次执行 502（没提交上游）——同一个任务位连着失败两次后第三次成功，另一个失败一次。
	calls := 0
	peak := driveDramaStepEvals(t, svc, func(t *testing.T, task *model.Task, slot dramaStepEvalTask) {
		calls++
		if calls == 1 || calls == 2 || calls == 4 {
			failUnsubmitted(t, db, task, false)
			return
		}
		succeedWith(db, func(string, string) string { return fitted })(t, task, slot)
	}, first.Version.Eval.ID, second.Version.Eval.ID)
	if peak != 1 {
		t.Fatalf("全局同时最多跑一个评测任务，实际 %d", peak)
	}
	a, b := flowPackageEval(t, svc, admin, first.Version.ID), flowPackageEval(t, svc, admin, second.Version.ID)
	if a.Status != DramaStepEvalPassed || a.Gate == nil || !a.Gate.Passed || a.Activation != "auto" || a.TasksDone != 2 {
		t.Fatalf("第一个评测跑完、过门槛、自动启用：%+v", a)
	}
	if b.Status != DramaStepEvalPassed || b.Gate == nil || b.Activation != "" || !strings.Contains(b.Error, "启用版本已经换了") {
		t.Fatalf("第二个评测也有门槛结论，启用版本换过就不自动启用：%+v", b)
	}
	if a.Retries+b.Retries != 3 || calls != 7 {
		t.Fatalf("三次 502 各重投一次（都在第一个评测）：重投 %d+%d，执行 %d 次", a.Retries, b.Retries, calls)
	}

	// 一直 502：重投两次用尽，这一章失败，评测记出错（不是未通过）。
	again, err := svc.StartDramaStepEval(admin, "yz-chapter-shots", second.Version.ID, dramaStepEvalTestModel)
	if err != nil {
		t.Fatal(err)
	}
	driveDramaStepEvals(t, svc, func(t *testing.T, task *model.Task, _ dramaStepEvalTask) { failUnsubmitted(t, db, task, false) }, again.ID)
	if eval := flowPackageEval(t, svc, admin, second.Version.ID); eval.Status != DramaStepEvalError || eval.Retries != len(dramaStepEvalRetryDelays) || !strings.Contains(eval.Error, "HTTP 502") {
		t.Fatalf("重投用尽记出错：%+v", eval)
	}
}

// 重投只给没提交上游的 5xx、连不上、超时；提交过的、524、截断、内容问题都不重投。
func TestDramaStepEvalRetryable(t *testing.T) {
	diag := func(submitted bool, code string) string {
		raw, _ := json.Marshal(model.TaskExecutionDiagnostic{Code: code, TaskSubmitted: submitted})
		return string(raw)
	}
	for _, c := range []struct {
		err       string
		submitted bool
		code      string
		want      bool
	}{
		{"模型服务暂时不可用（HTTP 502）", false, "provider_execution_failed", true},
		{"模型服务暂时不可用（HTTP 503）：overloaded", false, "provider_execution_failed", true},
		{"连接模型服务失败，请检查渠道地址和网络", false, "provider_execution_failed", true},
		{"模型服务响应超时，请稍后重试", false, "provider_execution_failed", true},
		{"模型服务暂时不可用（HTTP 502）", true, "provider_execution_failed", false},
		{"上游网关超时（524）：模型请求可能仍在服务端执行并产生费用", false, "provider_execution_failed", false},
		{"模型输出达到上限被截断", false, string(ReasonTextOutputTruncated), false},
		{"模型服务拒绝了请求，请检查模型和参数", false, "provider_execution_failed", false},
	} {
		task := &model.Task{Status: model.TaskStatusFailed, Error: c.err, ExecutionDiagnosticJSON: diag(c.submitted, c.code)}
		if got := dramaStepEvalRetryable(task); got != c.want {
			t.Fatalf("%s（提交 %v）：重投 %v", c.err, c.submitted, got)
		}
	}
	if dramaStepEvalRetryable(&model.Task{Status: model.TaskStatusFailed, Error: "模型服务暂时不可用（HTTP 502）"}) {
		t.Fatal("没有诊断就不知道提交没提交，不重投")
	}
}

// fitDramaEvalOutput 把输出里每镜的估时按比例压到合计不超过 total 秒（spec.est 与 durationSeconds 一起改）。
func fitDramaEvalOutput(t *testing.T, output string, total float64) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatal(err)
	}
	rows := payload["rows"].([]any)
	sum := 0.0
	for _, row := range rows {
		spec, _ := row.(map[string]any)["spec"].(map[string]any)
		est, _ := spec["est"].(float64)
		sum += est
	}
	if sum > total {
		for _, row := range rows {
			spec, _ := row.(map[string]any)["spec"].(map[string]any)
			est, _ := spec["est"].(float64)
			spec["est"] = float64(int(est*total/sum*10)) / 10
		}
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}
