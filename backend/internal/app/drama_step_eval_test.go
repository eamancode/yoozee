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
	got, infra := dramaStepEvalChapterResult(c, task.ID, task, phrases)
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
	bad, infra := dramaStepEvalChapterResult(c, "task-2", &model.Task{Status: model.TaskStatusSucceeded, ResultJSON: string(result)}, phrases)
	if infra != "" || !bad.Parsed || bad.SpecRows != 0 || bad.Rows != got.Rows {
		t.Fatalf("没有 spec：%+v", bad)
	}
	verdict := step.Gate.Evaluate([]drama.StepEvalChapterResult{bad}, []drama.StepEvalChapterResult{got})
	if verdict.Passed || gateCheck(verdict, drama.GateSpecRate).Passed {
		t.Fatalf("缺 spec 的包要被门槛挡下：%+v", verdict)
	}

	// 截断：任务失败、诊断原因是 text_output_truncated，算包的问题（截断 1、解析失败），不算评测出错。
	truncated := &model.Task{Status: model.TaskStatusFailed, Error: "模型输出达到上限被截断", ExecutionDiagnosticJSON: `{"code":"text_output_truncated"}`}
	cut, infra := dramaStepEvalChapterResult(c, "task-3", truncated, phrases)
	if infra != "" || !cut.Truncated || cut.Parsed {
		t.Fatalf("截断：%+v %s", cut, infra)
	}
	upstream := &model.Task{Status: model.TaskStatusFailed, Error: "上游 502"}
	if _, infra := dramaStepEvalChapterResult(c, "task-4", upstream, phrases); infra == "" {
		t.Fatal("上游失败算评测出错，可以重跑")
	}
	garbage, _ := json.Marshal(map[string]any{"mode": "text", "text": "抱歉，我无法完成"})
	if r, infra := dramaStepEvalChapterResult(c, "task-5", &model.Task{Status: model.TaskStatusSucceeded, ResultJSON: string(garbage)}, phrases); infra != "" || r.Parsed {
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

// completeDramaStepEvalTasks 把评测的任务按 output(章节, 角色) 写成结束状态（不调模型）。
func completeDramaStepEvalTasks(t *testing.T, svc *Service, db *gorm.DB, evalID string, output func(chapter, role string) string) {
	t.Helper()
	run, err := svc.repo.DramaEvalRun(evalID)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []dramaStepEvalTask
	if err := json.Unmarshal([]byte(run.TasksJSON), &tasks); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		stored, err := svc.repo.Task(task.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stored.InputJSON, dramaStepEvalTaskSource) && !strings.HasPrefix(stored.InputJSON, "enc:") {
			t.Fatalf("评测任务要标 source：%s", stored.InputJSON)
		}
		result, _ := json.Marshal(map[string]any{"mode": "text", "text": output(task.Chapter, task.Role)})
		if err := db.Model(&model.Task{}).Where("id = ?", task.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": string(result)}).Error; err != nil {
			t.Fatal(err)
		}
	}
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
	if eval := good.Version.Eval; eval.TasksTotal != 2 || len(eval.Chapters) != 1 || eval.Chapters[0].ID != "qichuang-e01" || eval.BaselineSkillVersionID != seed {
		t.Fatalf("真人剧一章，候选加对照两个任务：%+v", eval)
	}
	var evalTasks []dramaStepEvalTask
	goodRun, _ := svc.repo.DramaEvalRun(good.Version.Eval.ID)
	_ = json.Unmarshal([]byte(goodRun.TasksJSON), &evalTasks)
	for _, et := range evalTasks {
		task, err := svc.repo.Task(et.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(task.Prompt, "你是这一章的总导演。") != (et.Role == dramaStepEvalRoleCandidate) || !strings.Contains(task.Prompt, "林小满") || task.UserID != admin.ID || task.ProjectID != "" {
			t.Fatalf("%s 任务的提示词用对应版本组装：%s…", et.Role, task.Prompt[:min(200, len(task.Prompt))])
		}
	}
	if _, err := svc.StartDramaStepEval(admin, "yz-chapter-shots", good.Version.ID, dramaStepEvalTestModel); err == nil {
		t.Fatal("同一版本正在评测时不能再开一次")
	}
	svc.advanceDramaStepEvals(context.Background())
	if eval := flowPackageEval(t, svc, admin, good.Version.ID); eval.Status != DramaStepEvalQueued || eval.TasksDone != 0 {
		t.Fatalf("任务没结束时还在排队：%+v", eval)
	}
	completeDramaStepEvalTasks(t, svc, db, good.Version.Eval.ID, func(string, string) string { return fitted })
	svc.advanceDramaStepEvals(context.Background())
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
	completeDramaStepEvalTasks(t, svc, db, bad.Version.Eval.ID, func(string, string) string { return string(noSpec) })
	svc.advanceDramaStepEvals(context.Background())
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
	var tasks []dramaStepEvalTask
	run, _ := svc.repo.DramaEvalRun(retry.ID)
	_ = json.Unmarshal([]byte(run.TasksJSON), &tasks)
	for _, task := range tasks {
		db.Model(&model.Task{}).Where("id = ?", task.TaskID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "上游 502"})
	}
	svc.advanceDramaStepEvals(context.Background())
	if eval = flowPackageEval(t, svc, admin, bad.Version.ID); eval.Status != DramaStepEvalError || eval.Error == "" {
		t.Fatalf("上游失败算出错：%+v", eval)
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
