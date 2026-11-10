package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// fakeOntologyUpstream 按每轮【本轮任务】回一段符合技能输出契约的文字；s01-u2 第一次故意漏写「本段不使用背景音乐」，验证校验意见回给模型后重试。
type fakeOntologyUpstream struct {
	mu      sync.Mutex
	prompts []string
	u2Calls int
	// failU2 让 s01-u2 一直返回 400（不可重试），用来造一个写到一半失败的任务。
	failU2 bool
}

func ontologyFakeCard(unit string, seconds int) string {
	return fmt.Sprintf("### 单元卡 %s｜总时长 %d 秒（叙事 %d 秒 + 交接 1 秒）｜2 镜｜节奏短剧档\n\n【导演分镜卡】\n事件链：递表 → 认出\n镜头表：\n| 镜头 | 时间 | 戏剧职责 | 第一注意力 | 景别 | 视场角 | 机位 | 运镜 | 构图 | 动作与表情 | 台词与声音 | 光 | 切出 |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|\n| 1 | 0–%d 秒 | 建立 | 柜台 | 中景 | 47° | 平视 | 固定 | 居中 | 林夏把怀表推过柜台 | 雨声 | 台灯 | 硬切 |\n| 2 | %d–%d 秒 | 站位交接 | 全体站位 | 全景 | 84° | 高位 | 固定 | 完整入画 | 保持原位 | 环境声 | 不变 | 结束 |\n结束状态：林夏在画左。\n\n【粘贴提示词】\n```\n%d秒，竖屏9:16，真人写实短剧质感，2个镜头。\n镜头1：林夏把怀表推过柜台。\n声音：雨声。本段不使用背景音乐。\n```\n",
		unit, seconds, seconds-1, seconds-1, seconds-1, seconds, seconds)
}

func ontologyFakeCheckTable(units ...string) string {
	var b strings.Builder
	b.WriteString("| 单元 | H1 | H2 | H3 | H4 | H5 | H6 | H7 | H8 | H9 | H10 | H11 |\n|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, u := range units {
		b.WriteString("| " + u + strings.Repeat(" | ✓", 11) + " |\n")
	}
	return b.String()
}

func (f *fakeOntologyUpstream) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		prompt := body.Messages[len(body.Messages)-1].Content
		if f.failU2 && strings.Contains(prompt, "只写单元 s01-u2") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
			return
		}
		f.mu.Lock()
		f.prompts = append(f.prompts, prompt)
		f.mu.Unlock()
		var reply string
		switch {
		case strings.Contains(prompt, "这是立项回合"):
			reply = "## 世界圣经卡\n林夏：28岁女性，齐肩黑发。\n\n## 待确认清单\n| 编号 | 位置 |\n|---|---|\n| Q1 | 立项 |\n\n## 补图建议\n缺老秦的参考图。"
		case strings.Contains(prompt, "这是每集回合的第一轮"):
			reply = "## 一、本集定位\n本集一句话：当表。\n\n## 二、台词对照表\n本集无\n\n## 三、节奏总表\n| 单元 | 所属场 |\n|---|---|\n| s01-u1 | s01 |\n| s01-u2 | s01 |\n"
		case strings.Contains(prompt, "按 S4、S5 执行"):
			reply = "## 四、讲戏本\n### s01 当铺·夜\n场阐述：林夏想要真相。\n\n## 氛围设计\n台灯暖光 3200K。"
		case strings.Contains(prompt, "只写单元 s01-u1"):
			reply = ontologyFakeCard("s01-u1", 12)
		case strings.Contains(prompt, "只写单元 s01-u2"):
			f.mu.Lock()
			f.u2Calls++
			first := f.u2Calls == 1
			f.mu.Unlock()
			reply = ontologyFakeCard("s01-u2", 10)
			if first {
				reply = strings.Replace(reply, "本段不使用背景音乐。", "", 1)
			}
		case strings.Contains(prompt, "按 S8 自检全部单元卡"):
			reply = "## 六、本集交接卡\n【集交接卡】第1集 → 第2集\n林夏：在柜台外。\n\n## 七、自检结果与待确认清单\n" + ontologyFakeCheckTable("s01-u1", "s01-u2")
		default:
			t.Errorf("unexpected prompt: %s", prompt[len(prompt)-200:])
		}
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}, "finish_reason": "stop"}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}
}

func TestOntologyRunWritesSegmentsAndDocuments(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	previous := dramaTurnBackoffBase
	dramaTurnBackoffBase = time.Millisecond
	t.Cleanup(func() { dramaTurnBackoffBase = previous })
	svc, db := newChapterShotsTestService(t)
	now := time.Now()
	project := model.Project{ID: "project-onto", UserID: "user-1", Name: "当铺", Status: model.ProjectStatusActive, GenerationModel: "seedance-2.5", WorkflowID: drama.WorkflowOntology, WorkflowVersion: drama.WorkflowVersion, AspectRatio: "9:16", Revision: 1, CreatedAt: now, UpdatedAt: now}
	units := []model.ProjectUnit{
		{ID: "unit-1", ProjectID: project.ID, Kind: model.ProjectUnitKindChapter, Title: "第一集", SourceText: "<p>林夏走进当铺。</p>", Position: 0, Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now},
		{ID: "unit-2", ProjectID: project.ID, Kind: model.ProjectUnitKindChapter, Title: "第二集", SourceText: "<p>老秦开口。</p>", Position: 1, Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&units).Error; err != nil {
		t.Fatal(err)
	}
	pkg, err := svc.skillDomain().StepSkillPackage(drama.OntologySkillID)
	if err != nil {
		t.Fatal(err)
	}
	usage, skill, err := svc.ontologyStepSkill(&project, mustWorkflow(t, project.WorkflowID), mustStep(t, drama.StepChapterShots))
	if err != nil || skill.VersionID != pkg.VersionID || usage.Contract != drama.ContractOntologyDirector {
		t.Fatalf("skill = %+v %+v %v", usage, skill.VersionID, err)
	}
	plan, err := svc.planOntologyShots("user-1", &project, &units[0], usage, skill, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	meta := plan.ontology
	if !meta.Setup || meta.Brief.MaxUnitSeconds <= 0 || meta.EstimatedTurns < 6 || !strings.Contains(plan.prompt.Text, "这是立项回合") {
		t.Fatalf("plan = %+v", meta)
	}
	for _, f := range plan.prompt.Files {
		if strings.HasPrefix(f.Path, "archive/") || strings.HasPrefix(f.Path, "dist/") {
			t.Fatalf("first turn loads excluded file %s", f.Path)
		}
	}

	upstream := &fakeOntologyUpstream{}
	server := httptest.NewServer(upstream.handler(t))
	defer server.Close()
	run := newDramaRunMeta(plan, units[0].ID)
	taskReq := dramaStepTaskRequest(project.ID, units[0].ID, plan, dramaStepTaskOptions{})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = run
	input, _ := json.Marshal(taskReq.Input)
	task := model.Task{ID: "task-onto", UserID: "user-1", ProjectID: project.ID, Type: "canvas_text", Operation: "storyboard", Status: model.TaskStatusRunning, LeaseOwner: "worker-onto", LeaseExpiresAt: ptr(now.Add(time.Hour)), InputJSON: string(input), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	result, err := svc.runOntologyRun(dramaRunCtx(task), dramaRunInput(server.URL), run, taskDramaOntology(task.InputJSON))
	if err != nil {
		t.Fatal(err)
	}
	// 立项、规划、讲戏、两个单元（u2 重试一次）、质检。
	if len(upstream.prompts) != 7 || upstream.u2Calls != 2 {
		t.Fatalf("calls = %d, u2 = %d", len(upstream.prompts), upstream.u2Calls)
	}
	retry := upstream.prompts[5]
	if !strings.Contains(retry, "【本轮输出修正】") || !strings.Contains(retry, drama.OntologyNoMusicLine) {
		t.Fatalf("retry must feed the validation problem back: %s", retry[len(retry)-300:])
	}
	if !strings.Contains(upstream.prompts[4], "【前序产物：上一张单元卡】") || !strings.Contains(upstream.prompts[4], "### 单元卡 s01-u1") {
		t.Fatal("unit turn must carry the previous card")
	}
	if !strings.Contains(upstream.prompts[1], "林夏：28岁女性") {
		t.Fatal("turns after setup must use the new world bible")
	}
	progress := result["dramaRun"].(dramaRunProgress)
	if progress.Completed != 6 || progress.Total != 6 {
		t.Fatalf("progress = %d/%d", progress.Completed, progress.Total)
	}

	applied, err := svc.finalizeDramaStepTask(task, result)
	if err != nil || !applied {
		t.Fatalf("finalize = %v %v", applied, err)
	}
	outcome := result["dramaStep"].(DramaStepTaskOutcome)
	if outcome.Segments != 2 || outcome.Scenes != 1 || len(outcome.Notes) != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	segments, err := svc.DramaSegments("user-1", project.ID, units[0].ID)
	if err != nil || len(segments) != 2 {
		t.Fatalf("segments = %d %v", len(segments), err)
	}
	if segments[0].Spec.ID != "S01" || segments[0].Spec.Title != "s01-u1" || segments[0].Spec.Dur != 12 || !strings.Contains(segments[0].Spec.Verbatim, drama.OntologyNoMusicLine) {
		t.Fatalf("segment 1 = %+v", segments[0].Spec)
	}
	compiled, err := svc.CompileProjectShot("user-1", project.ID, segments[1].ShotID, CompileProjectShotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Result.Prompt != segments[1].Spec.Verbatim || compiled.Result.Duration < 10 {
		t.Fatalf("compiled prompt must be the paste prompt verbatim: %q", compiled.Result.Prompt)
	}
	docs, err := svc.ProjectOntologyDocuments("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if docs.Bible == nil || !strings.Contains(docs.Bible.Content, "林夏") || docs.Setup == nil || !strings.Contains(docs.Setup.Content, "补图建议") {
		t.Fatalf("project docs = %+v %+v", docs.Bible, docs.Setup)
	}
	if len(docs.Episodes) != 1 || !strings.Contains(docs.Episodes[0].Content, "## 五、单元卡\n### 单元卡 s01-u1") || !strings.Contains(docs.Episodes[0].Content, "## 七、自检结果与待确认清单") {
		t.Fatalf("episode doc = %+v", docs.Episodes)
	}
	// 流水线把写过七部分的集算作分镜做完（否则「继续」会重跑这一集）。
	st, err := svc.loadPipelineState("user-1", &project)
	if err != nil {
		t.Fatal(err)
	}
	if !st.shotlist[units[0].ID] || st.shotlist[units[1].ID] {
		t.Fatalf("shotlist = %+v", st.shotlist)
	}
	// 门禁：自检全过时写空门禁，流水线不挡。
	if gate, err := svc.repo.LatestOntologyDocument(project.ID, units[0].ID, model.OntologyDocGate); err != nil || gate.Content != "[]" || len(st.ontologyOpen) != 0 {
		t.Fatalf("gate = %+v %v open=%v", gate, err, st.ontologyOpen)
	}
	// 假设自检有两个硬失败：流水线读到门禁；返修 s01-u2 后只剩 s01-u1 的。
	if err := svc.repo.AppendOntologyDocument(&model.ProjectOntologyDocument{ID: newID(), ProjectID: project.ID, UnitID: units[0].ID, Kind: model.OntologyDocGate, Content: `["s01-u1 H3","s01-u2 H1"]`, TaskID: "task-check", CreatedBy: "user-1"}); err != nil {
		t.Fatal(err)
	}
	if st, err = svc.loadPipelineState("user-1", &project); err != nil || len(st.ontologyOpen[units[0].ID]) != 2 {
		t.Fatalf("open failures = %v %v", st.ontologyOpen, err)
	}
	// 返修按写入时其他段的实际时长核对每集上限：每集 20 秒 → 上限 22 秒，S01 是 12 秒，S02 改成 11 秒超限、10 秒正好。
	repairMeta := *taskDramaOntology(task.InputJSON)
	repairMeta.Brief.EpisodeSeconds = 20
	cards, _ := drama.ParseOntologyUnitCards(docs.Episodes[0].Content)
	repairMeta.Repair = &ontologyRepairMeta{ShotID: segments[1].ShotID, SegmentID: "S02", Unit: "s01-u2", Symptom: "衔接断开", Card: cards[1].Text, OtherSeconds: 0}
	model0, _ := projectDramaModel(&project)
	target := dramaStepTaskTarget{ProjectID: project.ID, UnitID: units[0].ID}
	repairOut := func(seconds int) ontologyOutputs {
		return ontologyOutputs{Repair: "## 诊断\n衔接断开。\n\n## 改法\n补一镜。\n\n## 重写后的单元卡\n" + ontologyFakeCard("s01-u2", seconds)}
	}
	if _, err := svc.applyOntologyRepair(model.Task{ID: "task-repair-1", UserID: "user-1"}, target, &repairMeta, repairOut(11), model0, DramaStepTaskOutcome{}); err == nil || !strings.Contains(err.Error(), "最多 10 秒") {
		t.Fatalf("repair over the episode budget must be rejected even with a stale OtherSeconds: %v", err)
	}
	if _, err := svc.applyOntologyRepair(model.Task{ID: "task-repair-2", UserID: "user-1"}, target, &repairMeta, repairOut(10), model0, DramaStepTaskOutcome{}); err != nil {
		t.Fatal(err)
	}
	if st, err = svc.loadPipelineState("user-1", &project); err != nil || len(st.ontologyOpen[units[0].ID]) != 1 || st.ontologyOpen[units[0].ID][0] != "s01-u1 H3" {
		t.Fatalf("repair must clear only its own unit from the gate: %v %v", st.ontologyOpen, err)
	}
	// 第二集：读到第一集的交接卡，有圣经卡就不再立项。
	next, err := svc.planOntologyShots("user-1", &project, &units[1], usage, skill, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.ontology.Setup || !strings.Contains(next.ontology.Handoff, "第1集 → 第2集") || !strings.Contains(next.prompt.Text, "这是每集回合的第一轮") {
		t.Fatalf("next episode plan = %+v", next.ontology)
	}
	// 编辑圣经卡写新版本。
	if _, err := svc.UpdateOntologyBible("user-1", project.ID, UpdateOntologyBibleRequest{Content: "新圣经"}); err != nil {
		t.Fatal(err)
	}
	if doc, err := svc.repo.LatestOntologyDocument(project.ID, "", model.OntologyDocBible); err != nil || doc.Version != 2 || doc.Content != "新圣经" {
		t.Fatalf("bible v2 = %+v %v", doc, err)
	}
}

func TestOntologyNextTurnSequence(t *testing.T) {
	meta := &dramaOntologyMeta{Setup: true}
	var out ontologyOutputs
	var kinds []string
	units := []string(nil)
	for i := 0; i < 10; i++ {
		spec, done := ontologyNextTurn(meta, out, units)
		if done {
			break
		}
		kinds = append(kinds, spec.kind+":"+spec.unit)
		switch spec.kind {
		case drama.OntologyTurnProject:
			out.Setup = "x"
		case drama.OntologyTurnPlan:
			out.Plan, units = "x", []string{"s01-u1", "s02-u1"}
		case drama.OntologyTurnDirect:
			out.Direct = "x"
		case drama.OntologyTurnUnit:
			out.Units = append(out.Units, "x")
		case drama.OntologyTurnCheck:
			out.Check = "x"
		}
	}
	if got := strings.Join(kinds, ","); got != "project:,plan:,direct:,unit:s01-u1,unit:s02-u1,check:" {
		t.Fatalf("turns = %s", got)
	}
	if ontologyTotalTurns(meta, 2) != 6 || ontologyTotalTurns(&dramaOntologyMeta{Repair: &ontologyRepairMeta{}}, 9) != 1 {
		t.Fatal("total turns")
	}
	repair := &dramaOntologyMeta{Repair: &ontologyRepairMeta{Unit: "s01-u2"}}
	if spec, done := ontologyNextTurn(repair, ontologyOutputs{}, nil); done || spec.kind != drama.OntologyTurnRepair || spec.unit != "s01-u2" {
		t.Fatalf("repair = %+v", spec)
	}
}

func mustWorkflow(t *testing.T, id string) drama.AssetWorkflow {
	t.Helper()
	wf, ok := drama.Workflow(id)
	if !ok {
		t.Fatalf("workflow %s", id)
	}
	return wf
}

func mustStep(t *testing.T, id string) drama.Step {
	t.Helper()
	step, ok := drama.StepByID(id)
	if !ok {
		t.Fatalf("step %s", id)
	}
	return step
}

func TestOntologyUnitTurnEnforcesEpisodeBudget(t *testing.T) {
	// 每集 18 秒 → 合计上限 19.8 秒；第一个单元 12 秒，第二个单元 10 秒就超了。
	meta := &dramaOntologyMeta{Brief: drama.OntologyBrief{EpisodeSeconds: 18, MaxUnitSeconds: 15}, Materials: make([]drama.OntologyMaterial, 4)}
	out := ontologyOutputs{Units: []string{ontologyFakeCard("s01-u1", 12)}}
	used := ontologyTurnUsedSeconds(drama.OntologyTurnUnit, meta, out)
	if used != 12 {
		t.Fatalf("used = %v", used)
	}
	check := ontologyTurnCheck(drama.OntologyTurnUnit, "s01-u2", meta, []string{"s01-u1", "s01-u2"}, used)
	if _, err := check(ontologyFakeCard("s01-u2", 10), drama.TurnContext{}); err == nil || !strings.Contains(err.Error(), "最多 7.8 秒") {
		t.Fatalf("over-budget unit must be rejected with the remaining seconds: %v", err)
	}
	if _, err := check(ontologyFakeCard("s01-u2", 7), drama.TurnContext{}); err != nil {
		t.Fatalf("unit within budget should pass: %v", err)
	}
	if line := drama.OntologyBudgetLine(ontologyLimits(meta, used)); !strings.Contains(line, "合计不超过 19.8 秒") || !strings.Contains(line, "最多 7.8 秒") {
		t.Fatalf("budget line = %q", line)
	}
	// 返修轮按这一集其他单元的合计扣。
	meta.Repair = &ontologyRepairMeta{Unit: "s01-u2", OtherSeconds: 12}
	if got := ontologyTurnUsedSeconds(drama.OntologyTurnRepair, meta, ontologyOutputs{}); got != 12 {
		t.Fatalf("repair used = %v", got)
	}
}
