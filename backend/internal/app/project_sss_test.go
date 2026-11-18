package app

import (
	"encoding/json"
	"errors"
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

// sssFakeCardBody 是单元卡轮的一张卡（写到接缝为止）：叙事 seconds-1 秒＋1 秒全景定格。
func sssFakeCardBody(unit string, seconds int) string {
	n := seconds - 1
	return fmt.Sprintf("### %s｜短档｜叙事 %d 秒＋定格 1 秒＝%d 秒｜1 镜＋定格｜当铺（本场第 1 段／共 2 段）｜钩子\n\n"+
		"**难事：** 无。　**复杂度：** 低。\n\n#### 导演阐述\n\n因为林夏要当掉怀表，所以从她的手开始拍。\n\n"+
		"#### 镜头表\n\n| 镜 | 时间 | 时长 | 景别·视场角 | 机位 | 运动 | 焦点 | 画面与表演 | 台词/声音 | 职责 |\n|---|---|---|---|---|---|---|---|---|---|\n"+
		"| 1 | 0–%d | %d | 中景·47° | 平视 | 固定 | 林夏 | 林夏把怀表推过柜台。 | 林夏（平静）：“当了。” | 说 |\n"+
		"| 2 | %d–%d | 1 | 全景·84° | 高位 45° 斜俯 | 固定 | 全景深 | 全景定格：林夏、老秦完整入画。 | —— | 全景定格 |\n\n"+
		"#### 接缝\n\n**承上：** 开场。\n**启下：** 最后 1 秒全景定格。\n", unit, n, seconds, n, n, n, seconds)
}

// sssFakeCompile 是编译轮输出：十五段粘贴提示词（noMusic=false 时漏掉背景音乐那一句）和风险与备选。
func sssFakeCompile(noMusic bool) string {
	var b strings.Builder
	b.WriteString("#### 粘贴提示词\n\n```\n")
	for _, name := range drama.SSSPromptSections {
		b.WriteString(name + "\n")
		switch name {
		case "声音":
			b.WriteString("雨声，台灯电流声。林夏现场说：“当了。”")
			if noMusic {
				b.WriteString("本段不使用背景音乐。")
			}
			b.WriteString("\n\n")
		default:
			b.WriteString("当铺柜台，夜，台灯暖光。\n\n")
		}
	}
	b.WriteString("```\n\n#### 风险与备选\n\n**最可能翻车的地方（估计）：** 怀表太小看不清；降级为近景。\n")
	return b.String()
}

type fakeSSSUpstream struct {
	mu          sync.Mutex
	prompts     []string
	compileU2   int
	failCompile bool
}

func (f *fakeSSSUpstream) handler(t *testing.T) http.HandlerFunc {
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
		task := prompt[strings.LastIndex(prompt, "【本轮任务】"):]
		f.mu.Lock()
		f.prompts = append(f.prompts, prompt)
		f.mu.Unlock()
		var reply string
		switch {
		case strings.Contains(task, "这是立项回合的第三步"):
			reply = "## 全剧圣经\n\n### 世界基准段\n> 真人实拍质感的竖屏短剧，画面清晰。\n\n### 人物：林夏\n> 林夏：二十八岁女性，齐肩黑发。\n\n## 待确认\n\n1. 老秦没有参考图。\n"
		case strings.Contains(task, "这是每集回合的第一轮"):
			reply = "## 第一部分　本集设计\n\n钩子：怀表。\n\n## 第二部分　台词定稿表\n\n| 序号 | 原台词 | 定稿 |\n|---|---|---|\n| 1 | 当了 | 当了 |\n"
		case strings.Contains(task, "按手册/06 讲戏"):
			reply = "## 第三部分　讲戏本\n\n### 第 1 场　当铺　夜\n在场人物账：林夏、老秦。\n"
		case strings.Contains(task, "按手册/07 预切单元"):
			reply = "## 整场镜头表\n\n第 1 场 20 秒，分两段。\n\n## 第四部分　单元总表\n\n| 单元 | 档 | 总时长 |\n|---|---|---|\n| E1-U1 | 短 | 12 秒 |\n| E1-U2 | 短 | 10 秒 |\n\n## 启发库选用\n\n| 单元 | 库号 |\n|---|---|\n| E1-U1 | A01、F01 |\n| E1-U2 | E01 |\n"
		case strings.Contains(task, "只写单元 E1-U1 的单元卡"):
			reply = sssFakeCardBody("E1-U1", 12)
		case strings.Contains(task, "只写单元 E1-U2 的单元卡"):
			reply = sssFakeCardBody("E1-U2", 10)
		case strings.Contains(task, "把单元 E1-U1 的单元卡"):
			reply = sssFakeCompile(true)
		case strings.Contains(task, "把单元 E1-U2 的单元卡"):
			if f.failCompile {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
				return
			}
			f.mu.Lock()
			f.compileU2++
			first := f.compileU2 == 1
			f.mu.Unlock()
			reply = sssFakeCompile(!first)
			if strings.Contains(task, "本单元要写 @站位参考") {
				reply = strings.Replace(sssFakeCompile(true), "生效参考素材\n", "生效参考素材\n@站位参考：承接上段。\n", 1)
			}
		case strings.Contains(task, "做导演复看与质检交接"):
			reply = "## 第六部分　后期备忘\n\n剪掉两个定格秒。\n\n## 第七部分　集交接卡\n\n### 第 1 集交接卡\n林夏在柜台外，怀表在老秦手里。\n\n## 第八部分　复看记录与待确认\n\n### 待确认\n1. 无。\n"
		default:
			t.Errorf("unexpected prompt: %s", task)
		}
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}, "finish_reason": "stop"}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}
}

func TestSSSRunWritesSegmentsAndDocuments(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	previous := dramaTurnBackoffBase
	dramaTurnBackoffBase = time.Millisecond
	t.Cleanup(func() { dramaTurnBackoffBase = previous })
	svc, db := newChapterShotsTestService(t)
	now := time.Now()
	project := model.Project{ID: "project-sss", UserID: "user-1", Name: "当铺", Status: model.ProjectStatusActive, GenerationModel: "seedance-2.5", WorkflowID: drama.WorkflowSSSDrama, WorkflowVersion: drama.WorkflowVersion, AspectRatio: "9:16", Revision: 1, CreatedAt: now, UpdatedAt: now}
	units := []model.ProjectUnit{
		{ID: "sss-unit-1", ProjectID: project.ID, Kind: model.ProjectUnitKindEpisode, Title: "第一集", SourceText: "<p>林夏走进当铺。</p>", Position: 0, Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now},
		{ID: "sss-unit-2", ProjectID: project.ID, Kind: model.ProjectUnitKindEpisode, Title: "第二集", SourceText: "<p>老秦开口。</p>", Position: 1, Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&units).Error; err != nil {
		t.Fatal(err)
	}
	// 拆剧本、导演定画风写好的文档：分镜立项要读到它们。
	for kind, content := range map[string]string{model.OntologyDocConcept: "## 立意页\n\n一句话：她当掉了父亲的怀表。", model.OntologyDocWorld: "## 世界基准段\n\n真人实拍质感的竖屏短剧，画面清晰。"} {
		if err := svc.repo.AppendOntologyDocument(&model.ProjectOntologyDocument{ID: newID(), ProjectID: project.ID, Kind: kind, Content: content, TaskID: "task-" + kind, CreatedBy: "user-1"}); err != nil {
			t.Fatal(err)
		}
	}
	usage, skill, err := svc.sssStepSkill(&project, mustWorkflow(t, project.WorkflowID), mustStep(t, drama.StepChapterShots))
	if err != nil || skill.ID != "yz-sss-drama-director" || usage.Contract != drama.ContractSSSDirector {
		t.Fatalf("skill = %+v %+v %v", usage, skill.ID, err)
	}
	plan, err := svc.planSSSShots("user-1", &project, &units[0], usage, skill, nil, nil, sssShotOptions{Flow: 1})
	if err != nil {
		t.Fatal(err)
	}
	meta := plan.sss
	if !meta.Setup || meta.Kind != drama.SSSKindDrama || meta.Brief.MaxUnitSeconds <= 0 || meta.EstimatedTurns < 9 || !strings.Contains(plan.prompt.Text, "这是立项回合的第三步") {
		t.Fatalf("plan = %+v", meta)
	}
	if !strings.Contains(plan.prompt.Text, "她当掉了父亲的怀表") || !strings.Contains(plan.prompt.Text, "真人实拍质感的竖屏短剧") {
		t.Fatal("setup turn must read the concept and world documents")
	}
	for _, f := range plan.prompt.Files {
		if strings.HasPrefix(f.Path, "范例/") || strings.HasPrefix(f.Path, "启发库/A") {
			t.Fatalf("first turn loads %s", f.Path)
		}
	}

	upstream := &fakeSSSUpstream{}
	server := httptest.NewServer(upstream.handler(t))
	defer server.Close()
	run := newDramaRunMeta(plan, units[0].ID)
	taskReq := dramaStepTaskRequest(project.ID, units[0].ID, plan, dramaStepTaskOptions{})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = run
	input, _ := json.Marshal(taskReq.Input)
	task := model.Task{ID: "task-sss", UserID: "user-1", ProjectID: project.ID, Type: "canvas_text", Operation: "storyboard", Status: model.TaskStatusRunning, LeaseOwner: "worker-sss", LeaseExpiresAt: ptr(now.Add(time.Hour)), InputJSON: string(input), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	result, err := svc.runSSSRun(dramaRunCtx(task), dramaRunInput(server.URL), run, taskDramaSSS(task.InputJSON))
	if err != nil {
		t.Fatal(err)
	}
	// 立项、规划、讲戏、切分、U1 单元卡与编译、U2 单元卡与编译（编译漏了背景音乐，重试一次）、复看。
	if len(upstream.prompts) != 10 || upstream.compileU2 != 2 {
		t.Fatalf("calls = %d, compile u2 = %d", len(upstream.prompts), upstream.compileU2)
	}
	if retry := upstream.prompts[8]; !strings.Contains(retry, "【本轮输出修正】") || !strings.Contains(retry, drama.SSSNoMusicLine) {
		t.Fatal("retry must feed the validation problem back")
	}
	cardU2 := upstream.prompts[6]
	if !strings.Contains(cardU2, `<skill-file path="启发库/E01_情绪表演过程(上).md">`) || !strings.Contains(cardU2, `<skill-file path="启发库/E01_情绪表演过程(下).md">`) || strings.Contains(cardU2, "启发库/A01") {
		t.Fatal("unit card turn must load exactly the libraries chosen for that unit")
	}
	if strings.Contains(cardU2, "【本集剧本】第一集") || !strings.Contains(upstream.prompts[1], "【本集剧本】第一集") || !strings.Contains(cardU2, "【前序产物：上一个单元的单元卡（接缝用）】") || !strings.Contains(cardU2, "### E1-U1") {
		t.Fatal("unit card turn carries the shot table and the previous card, not the script")
	}
	// 第五轮：单元卡轮带上单元总表里本单元的原行做时长核算，并写明定格 / 技术尾巴进镜头表、最后一行等于总时长。
	if !strings.Contains(cardU2, "【单元总表核算】") || !strings.Contains(cardU2, "| E1-U2 | 短 | 10 秒 |") || strings.Contains(cardU2, "| E1-U1 | 短 | 12 秒 |\n镜头表最后一行") || !strings.Contains(cardU2, "最后一行的结束秒必须等于标题的总时长") {
		t.Fatal("unit card turn must carry its own unit-table row and the timing contract")
	}
	if !strings.Contains(upstream.prompts[5], "【前序产物：本单元的单元卡】") || !strings.Contains(upstream.prompts[5], "<skill-file path=\"模板/05_提示词骨架.md\">") {
		t.Fatal("compile turn must carry the unit card and the prompt skeleton")
	}
	if !strings.Contains(upstream.prompts[1], "林夏：二十八岁女性") {
		t.Fatal("turns after setup must use the new bible")
	}
	progress := result["dramaRun"].(dramaRunProgress)
	if progress.Completed != 9 || progress.Total != 9 {
		t.Fatalf("progress = %d/%d", progress.Completed, progress.Total)
	}

	applied, err := svc.finalizeDramaStepTask(task, result)
	if err != nil || !applied {
		t.Fatalf("finalize = %v %v", applied, err)
	}
	outcome := result["dramaStep"].(DramaStepTaskOutcome)
	if outcome.Segments != 2 || outcome.Scenes != 1 || outcome.Shots != 4 {
		t.Fatalf("outcome = %+v", outcome)
	}
	segments, err := svc.DramaSegments("user-1", project.ID, units[0].ID)
	if err != nil || len(segments) != 2 {
		t.Fatalf("segments = %d %v", len(segments), err)
	}
	if s := segments[0].Spec; s.ID != "S01" || s.Title != "E1-U1" || s.Dur != 12 || s.Scene != "当铺" || !strings.HasPrefix(s.Verbatim, "场景背景") || !strings.Contains(s.Verbatim, drama.SSSNoMusicLine) {
		t.Fatalf("segment 1 = %+v", s)
	}
	compiled, err := svc.CompileProjectShot("user-1", project.ID, segments[1].ShotID, CompileProjectShotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Result.Prompt != segments[1].Spec.Verbatim {
		t.Fatalf("compiled prompt must be the paste prompt verbatim: %q", compiled.Result.Prompt)
	}
	docs, err := svc.ProjectOntologyDocuments("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if docs.Mode != drama.StoryboardModeSSS || docs.Bible == nil || !strings.Contains(docs.Bible.Content, "林夏") || docs.Setup == nil || !strings.Contains(docs.Setup.Content, "老秦没有参考图") || docs.Concept == nil || docs.World == nil {
		t.Fatalf("project docs = %+v", docs)
	}
	if len(docs.Episodes) != 1 || len(docs.Handoffs) != 1 {
		t.Fatalf("episode docs = %d handoffs = %d", len(docs.Episodes), len(docs.Handoffs))
	}
	at := -1
	for _, part := range drama.SSSEpisodeParts {
		i := strings.Index(docs.Episodes[0].Content, "## "+part)
		if i <= at {
			t.Fatal("eight parts out of order", part)
		}
		at = i
	}
	if materials, err := svc.repo.LatestOntologyDocument(project.ID, units[0].ID, model.OntologyDocMaterials); err != nil || materials.Content != "[]" {
		t.Fatalf("materials doc = %+v %v", materials, err)
	}
	// 流水线把写过八部分的集算作分镜做完；SSS 不写自检门禁。
	st, err := svc.loadPipelineState("user-1", &project)
	if err != nil {
		t.Fatal(err)
	}
	if !st.shotlist[units[0].ID] || st.shotlist[units[1].ID] || len(st.ontologyOpen) != 0 {
		t.Fatalf("pipeline state = %+v %v", st.shotlist, st.ontologyOpen)
	}
	// 返修：重写 E1-U2，只替换这一段，八部分里这张卡换成新卡并追加返修记录。
	repairMeta := *taskDramaSSS(task.InputJSON)
	cards, err := drama.ParseSSSEpisodeCards(docs.Episodes[0].Content)
	if err != nil || len(cards) != 2 {
		t.Fatal(len(cards), err)
	}
	repairMeta.Repair = &sssRepairMeta{ShotID: segments[1].ShotID, SegmentID: "S02", Unit: "E1-U2", Symptom: "怀表看不清", Card: cards[1].Text()}
	model0, _ := projectDramaModel(&project)
	target := dramaStepTaskTarget{ProjectID: project.ID, UnitID: units[0].ID}
	repaired := sssOutputs{Repair: "## 诊断\n\n怀表太小。\n\n## 改法\n\n改成近景。\n\n## 重写后的单元卡\n\n" + strings.Replace(sssFakeCardBody("E1-U2", 8), "中景·47°", "近景·35°", 1), RepairCompile: sssFakeCompile(true)}
	if _, err := svc.applySSSRepair(model.Task{ID: "task-sss-repair", UserID: "user-1"}, target, &repairMeta, repaired, model0, DramaStepTaskOutcome{}); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.DramaSegments("user-1", project.ID, units[0].ID)
	if len(after) != 2 || after[1].Spec.Dur != 8 || after[0].Spec.Dur != 12 || after[1].ShotID != segments[1].ShotID {
		t.Fatalf("repair must replace only S02: %+v", after)
	}
	if doc, _ := svc.repo.LatestOntologyDocument(project.ID, units[0].ID, model.OntologyDocEpisode); !strings.Contains(doc.Content, "近景·35°") || !strings.Contains(doc.Content, "## 返修记录 E1-U2") {
		t.Fatal("episode document not updated by the repair")
	}
	// 第二集：读到第一集的交接卡，有圣经就不再立项。
	next, err := svc.planSSSShots("user-1", &project, &units[1], usage, skill, nil, nil, sssShotOptions{Flow: 1})
	if err != nil {
		t.Fatal(err)
	}
	if next.sss.Setup || !strings.Contains(next.sss.Docs.Handoff, "第 1 集交接卡") || !strings.Contains(next.prompt.Text, "这是每集回合的第一轮") {
		t.Fatalf("next episode plan = %+v", next.sss)
	}
	// SSS 项目也能手改圣经。
	if _, err := svc.UpdateOntologyBible("user-1", project.ID, UpdateOntologyBibleRequest{Content: "新圣经"}); err != nil {
		t.Fatal(err)
	}
}

func TestSSSFlow2DirectorEpisode(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	svc, db := newChapterShotsTestService(t)
	p := model.Project{ID: "p-flow2", UserID: "user-1", Name: "当铺", Status: model.ProjectStatusActive, WorkflowID: drama.WorkflowSSSDrama, WorkflowVersion: drama.WorkflowVersion, GenerationModel: "seedance-2.5", AspectRatio: "9:16", StyleProfileJSON: `{"prompt":"纪实夜景"}`}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	units := []model.ProjectUnit{
		{ID: "ep-a", ProjectID: p.ID, Kind: model.ProjectUnitKindEpisode, Title: "第一集", Position: 0, SourceText: "上一集正文"},
		{ID: "cut", ProjectID: p.ID, Kind: model.ProjectUnitKindEpisode, Title: "删减集", Position: 1},
		{ID: "source", ProjectID: p.ID, Kind: model.ProjectUnitKindChapter, Title: "原文章节", Position: 2},
		{ID: "ep-b", ProjectID: p.ID, Kind: model.ProjectUnitKindEpisode, Title: "第二集", Position: 3, SourceText: "林夏：当了。"},
	}
	if err := db.Create(&units).Error; err != nil {
		t.Fatal(err)
	}
	u := &units[3]
	usage, skill, err := svc.sssStepSkill(&p, mustWorkflow(t, p.WorkflowID), mustStep(t, drama.StepChapterShots))
	if err != nil {
		t.Fatal(err)
	}
	planEpisode := func(independent bool) (*dramaStepPlan, error) {
		return svc.planSSSShots("user-1", &p, u, usage, skill, nil, nil, sssShotOptions{Flow: 2, AllowMissingHandoff: independent})
	}
	write := func(kind, id, content string) {
		t.Helper()
		if err := svc.repo.AppendOntologyDocument(&model.ProjectOntologyDocument{ID: newID(), ProjectID: p.ID, UnitID: id, Kind: kind, Content: content, TaskID: "source-" + kind, CreatedBy: "user-1"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = planEpisode(false); err == nil || !strings.Contains(err.Error(), "本集设计") {
		t.Fatalf("缺设计应拦截: %v", err)
	}
	design := "## 第一部分　本集设计\n原剧本阶段独有的设计\n\n## 第二部分　台词定稿表\n原剧本阶段独有的定稿表"
	write(model.OntologyDocDesign, u.ID, design)
	if _, err = planEpisode(false); err == nil || !strings.Contains(err.Error(), "全剧立项") {
		t.Fatalf("缺圣经应拦截: %v", err)
	}
	write(model.OntologyDocBible, "", "项目圣经")
	concept := "## 立意页\n当铺\n## 全集结构表\n| 集号 | 任务 |\n|---|---|\n"
	for i := 1; i <= 30; i++ {
		concept += fmt.Sprintf("| %d | 结构行[%02d] |\n", i, i)
	}
	write(model.OntologyDocConcept, "", concept)
	write(model.OntologyDocHandoff, "cut", "删减集的交接")
	beforeDelete, err := planEpisode(false)
	if err != nil || beforeDelete.sss.Docs.Handoff != "删减集的交接" {
		t.Fatalf("分镜材料必须读取紧邻的剧集交接卡：%v", err)
	}
	if err := svc.DeleteProjectUnit(p.UserID, p.ID, "cut"); err != nil {
		t.Fatal(err)
	}
	_, blocked := svc.RunDramaStep(p.UserID, p.ID, u.ID, drama.StepChapterShots, DramaStepRunRequest{})
	var problem *AppError
	if !errors.As(blocked, &problem) || problem.Status != 409 || problem.Reason != ReasonPreviousHandoffRequired || problem.Details["previousUnitId"] != units[0].ID {
		t.Fatalf("入口错误合同: %+v", blocked)
	}
	queue := svc.startShotsQueue(p.UserID, p.ID, &DramaPipelineText{Model: "local-fixture"}, "", []string{u.ID, "later"})
	if queue.UnitID != u.ID || queue.TaskID != "" || queue.Remaining != 2 || !strings.Contains(queue.Error, "交接卡") {
		t.Fatalf("前置失败却继续排队: %+v", queue)
	}
	if _, err = planEpisode(false); err == nil || !strings.Contains(err.Error(), "交接卡") {
		t.Fatalf("缺交接卡应拦截: %v", err)
	}
	plan, err := planEpisode(true)
	if err != nil {
		t.Fatal(err)
	}
	up := &fakeSSSUpstream{}
	server := httptest.NewServer(up.handler(t))
	defer server.Close()
	run := newDramaRunMeta(plan, u.ID)
	req := dramaStepTaskRequest(p.ID, u.ID, plan, dramaStepTaskOptions{})
	req.Input["metadata"].(map[string]any)["dramaRun"] = run
	input, _ := json.Marshal(req.Input)
	now := time.Now()
	task := model.Task{ID: "task-flow2", UserID: "user-1", ProjectID: p.ID, Type: "canvas_text", Operation: "storyboard", Status: model.TaskStatusRunning, LeaseOwner: "worker", LeaseExpiresAt: ptr(now.Add(time.Hour)), InputJSON: string(input), CreatedAt: now, UpdatedAt: now}
	if err = db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	result, err := svc.runSSSRun(dramaRunCtx(task), dramaRunInput(server.URL), run, taskDramaSSS(task.InputJSON))
	if err != nil {
		t.Fatal(err)
	}
	progress := result["dramaRun"].(dramaRunProgress)
	if progress.Completed != 7 || progress.Total != 7 || len(up.prompts) != 7 {
		t.Fatalf("实际轮次 %d/%d 上游 %d", progress.Completed, progress.Total, len(up.prompts))
	}
	for i, prompt := range up.prompts {
		if strings.Contains(prompt, "结构行[15]") || !strings.Contains(prompt, "结构行[02]") {
			t.Fatalf("第 %d 轮带错结构表", i+1)
		}
		taskText := prompt[strings.LastIndex(prompt, "【本轮任务】"):]
		if strings.Contains(taskText, "本单元要写 @站位参考") != (i == 5) {
			t.Fatalf("第 %d 轮的站位参考要求错误", i+1)
		}
		if progress.Turns[i].InputRunes != len([]rune(prompt)) {
			t.Fatal("轮次输入字数", i)
		}
	}
	legacy := *plan.sss
	legacy.Flow = 0
	legacy.Docs.Concept = concept
	before := sssTurnPrompt("", &legacy, drama.SSSTurnCard, "E1-U1", nil, nil)
	after := sssTurnPrompt("", plan.sss, drama.SSSTurnCard, "E1-U1", nil, nil)
	if len([]rune(before)) <= len([]rune(after)) {
		t.Fatal("材料包没有缩短")
	}
	t.Logf("单元卡共用输入材料字数：裁剪前 %d，裁剪后 %d", len([]rune(before)), len([]rune(after)))
	if !strings.Contains(up.prompts[0], "按手册/06 讲戏") || !strings.Contains(up.prompts[0], "原剧本阶段独有的设计") {
		t.Fatal("讲戏未读取剧本设计")
	}
	if ok, err := svc.finalizeDramaStepTask(task, result); err != nil || !ok {
		t.Fatal(ok, err)
	}
	doc, err := svc.repo.LatestOntologyDocument(p.ID, u.ID, model.OntologyDocEpisode)
	if err != nil || !strings.Contains(doc.Content, "原剧本阶段独有的定稿表") || !strings.Contains(doc.Content, "### 待确认\n本集独立开局") {
		t.Fatal("八部分来源或独立开局标记错误", err)
	}
	segs, err := svc.DramaSegments("user-1", p.ID, u.ID)
	if err != nil || len(segs) != 2 || strings.Count(segs[1].Spec.Verbatim, "@站位参考") != 1 {
		t.Fatal("站位参考未保留", err)
	}
}

func TestSSSNextTurnSequence(t *testing.T) {
	meta := &dramaSSSMeta{Setup: true}
	var turns []dramaRunTurn
	var kinds []string
	for i := 0; i < 20; i++ {
		spec, done := sssNextTurn(meta, sssStateFrom(turns))
		if done {
			break
		}
		kinds = append(kinds, spec.kind+":"+spec.unit)
		text := "x"
		if spec.kind == drama.SSSTurnCut {
			text = "## 第四部分　单元总表\n\n| 单元 |\n|---|\n| E1-U1 |\n| E1-U2 |\n"
		}
		turns = append(turns, dramaRunTurn{SSS: spec.kind, Scene: spec.unit, Text: text})
	}
	if got := strings.Join(kinds, ","); got != "setup:,plan:,direct:,cut:,card:E1-U1,compile:E1-U1,card:E1-U2,compile:E1-U2,review:" {
		t.Fatalf("turns = %s", got)
	}
	if sssTotalTurns(meta, 2) != 9 || sssTotalTurns(&dramaSSSMeta{Repair: &sssRepairMeta{}}, 9) != 2 {
		t.Fatal("total turns")
	}
	repair := &dramaSSSMeta{Repair: &sssRepairMeta{Unit: "E1-U2"}}
	spec, _ := sssNextTurn(repair, sssOutputs{})
	spec2, _ := sssNextTurn(repair, sssStateFrom([]dramaRunTurn{{SSS: drama.SSSTurnRepair, Text: "x"}}))
	_, done := sssNextTurn(repair, sssStateFrom([]dramaRunTurn{{SSS: drama.SSSTurnRepair, Text: "x"}, {SSS: drama.SSSTurnCompile, Text: "y"}}))
	if spec.kind != drama.SSSTurnRepair || spec2.kind != drama.SSSTurnCompile || !done {
		t.Fatal("repair turns", spec, spec2, done)
	}
}

func TestSSSRunResumesFromAFailedCompileTurn(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	svc, db := newChapterShotsTestService(t)
	now := time.Now()
	project := model.Project{ID: "project-sss-resume", UserID: "user-1", Name: "当铺", Status: model.ProjectStatusActive, GenerationModel: "seedance-2.5", WorkflowID: drama.WorkflowSSSFilm, WorkflowVersion: drama.WorkflowVersion, AspectRatio: "16:9", Revision: 1, CreatedAt: now, UpdatedAt: now}
	unit := model.ProjectUnit{ID: "sss-resume-1", ProjectID: project.ID, Kind: model.ProjectUnitKindEpisode, Title: "第一集", SourceText: "<p>林夏走进当铺。</p>", Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	usage, skill, err := svc.sssStepSkill(&project, mustWorkflow(t, project.WorkflowID), mustStep(t, drama.StepChapterShots))
	if err != nil || skill.ID != "yz-sss-film-director" {
		t.Fatal(skill.ID, err)
	}
	plan, err := svc.planSSSShots("user-1", &project, &unit, usage, skill, nil, nil, sssShotOptions{Flow: 1})
	if err != nil || plan.sss.Kind != drama.SSSKindFilm || !strings.Contains(plan.prompt.Text, "电影模式") || !strings.Contains(plan.prompt.Text, "横屏16:9") {
		t.Fatal("film plan", err)
	}
	upstream := &fakeSSSUpstream{failCompile: true}
	server := httptest.NewServer(upstream.handler(t))
	defer server.Close()
	run := newDramaRunMeta(plan, unit.ID)
	taskReq := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = run
	input, _ := json.Marshal(taskReq.Input)
	task := model.Task{ID: "task-sss-1", UserID: "user-1", ProjectID: project.ID, Type: "canvas_text", Operation: "storyboard", Status: model.TaskStatusRunning, LeaseOwner: "w", LeaseExpiresAt: ptr(now.Add(time.Hour)), InputJSON: string(input), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.runSSSRun(dramaRunCtx(task), dramaRunInput(server.URL), run, taskDramaSSS(task.InputJSON)); err == nil {
		t.Fatal("compile of E1-U2 must fail")
	}
	if err := db.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	failed, _ := svc.repo.Task(task.ID)
	progress := taskDramaRunProgress(failed.ResultJSON)
	if progress == nil || progress.Completed != 7 {
		t.Fatalf("progress before failure = %+v", progress)
	}
	// 从中断处继续：指纹相同，只再调 E1-U2 编译和复看。
	again, err := svc.planSSSShots("user-1", &project, &unit, usage, skill, nil, nil, sssShotOptions{Flow: 1})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := svc.resumeDramaRun("user-1", project.ID, unit.ID, again, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.prepareSSSRun("user-1", project.ID, unit.ID, again, &resumed, false); err != nil {
		t.Fatal(err)
	}
	if resumed.ResumeFrom != 8 || resumed.Turns != 9 {
		t.Fatalf("resume = %+v", resumed)
	}
	resumedReq := dramaStepTaskRequest(project.ID, unit.ID, again, dramaStepTaskOptions{})
	resumedReq.Input["metadata"].(map[string]any)["dramaRun"] = resumed
	resumedInput, _ := json.Marshal(resumedReq.Input)
	resumedTask := task
	resumedTask.ID, resumedTask.InputJSON = "task-sss-2", string(resumedInput)
	if err := db.Create(&resumedTask).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.runSSSRun(dramaRunCtx(resumedTask), dramaRunInput(server.URL), resumed, taskDramaSSS(resumedTask.InputJSON)); err == nil {
		t.Fatal("the first new round of the resumed run must fail again")
	}
	if err := db.Model(&model.Task{}).Where("id = ?", resumedTask.ID).Update("status", model.TaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	resumed, err = svc.resumeDramaRun("user-1", project.ID, unit.ID, again, resumedTask.ID)
	if err != nil || resumed.ResumeFrom != 8 {
		t.Fatalf("repeated failure lost checkpoints: resume = %+v, err = %v", resumed, err)
	}
	if err := svc.prepareSSSRun("user-1", project.ID, unit.ID, again, &resumed, false); err != nil {
		t.Fatal(err)
	}
	resumedReq.Input["metadata"].(map[string]any)["dramaRun"] = resumed
	resumedInput, _ = json.Marshal(resumedReq.Input)
	resumedTask.ID, resumedTask.InputJSON = "task-sss-3", string(resumedInput)
	if err := db.Create(&resumedTask).Error; err != nil {
		t.Fatal(err)
	}
	resumedUpstream := &fakeSSSUpstream{compileU2: 1}
	resumedServer := httptest.NewServer(resumedUpstream.handler(t))
	defer resumedServer.Close()
	result, err := svc.runSSSRun(dramaRunCtx(resumedTask), dramaRunInput(resumedServer.URL), resumed, taskDramaSSS(resumedTask.InputJSON))
	if err != nil {
		t.Fatal(err)
	}
	finished := result["dramaRun"].(dramaRunProgress)
	if finished.Completed != 9 || finished.Turns[6].TaskID != task.ID || finished.Turns[7].SSS != drama.SSSTurnCompile || finished.Turns[8].SSS != drama.SSSTurnReview || len(resumedUpstream.prompts) != 2 {
		t.Fatalf("resumed run completed %d rounds with %d new requests", finished.Completed, len(resumedUpstream.prompts))
	}
	// 剧本改过就不能接着旧轮次写。
	if err := db.Model(&model.ProjectUnit{}).Where("id = ?", unit.ID).Update("source_text", "<p>改过的剧本。</p>").Error; err != nil {
		t.Fatal(err)
	}
	changedUnit := unit
	changedUnit.SourceText = "<p>改过的剧本。</p>"
	changed, err := svc.planSSSShots("user-1", &project, &changedUnit, usage, skill, nil, nil, sssShotOptions{Flow: 1})
	if err != nil {
		t.Fatal(err)
	}
	rerun, _ := svc.resumeDramaRun("user-1", project.ID, unit.ID, changed, task.ID)
	if err := svc.prepareSSSRun("user-1", project.ID, unit.ID, changed, &rerun, false); err == nil || !strings.Contains(err.Error(), "改过了") {
		t.Fatalf("changed script must block resume: %v", err)
	}
}

// SSS 项目的拆剧本、导演定画风用流水线自带的单文件技能，分镜用原技能；其余流水线照旧走绑定表。
func TestSSSWorkflowStepSkills(t *testing.T) {
	s, db := newDramaWorkerTestService(t)
	project, _ := seedQichuang(t, s, db)
	for wf, kind := range map[string]string{drama.WorkflowSSSFilm: drama.SSSKindFilm, drama.WorkflowSSSDrama: drama.SSSKindDrama} {
		if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("workflow_id", wf).Error; err != nil {
			t.Fatal(err)
		}
		for step, want := range map[string]string{drama.StepScriptAdapt: drama.SSSScriptSkill(kind), drama.StepStyleLock: drama.SSSStyleSkill(kind), drama.StepChapterShots: drama.SSSDirectorSkill(kind), drama.StepAssetsExtract: drama.IntegratedAssetsSkill} {
			usage, err := s.DramaStepUsage("user-1", project.ID, step, nil)
			if err != nil {
				t.Fatal(wf, step, err)
			}
			if usage.Skill.ID != want || usage.Bound {
				t.Fatalf("%s %s skill = %s", wf, step, usage.Skill.ID)
			}
		}
	}
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("workflow_id", drama.WorkflowIntegrated).Error; err != nil {
		t.Fatal(err)
	}
	for step, want := range map[string]string{drama.StepScriptAdapt: "yz-script-adapt", drama.StepStyleLock: "yz-style-lock"} {
		if usage, err := s.DramaStepUsage("user-1", project.ID, step, nil); err != nil || usage.Skill.ID != want {
			t.Fatalf("legacy %s = %+v %v", step, usage.Skill, err)
		}
	}
}

// SSS 拆剧本：第一轮写立意页与全集结构表（不写剧本），之后一轮一集；收尾照常建剧集，立意与结构存成项目文档。
func TestSSSScriptAdaptWritesConceptThenEpisodes(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, chapters := seedNovelProject(t, svc, db)
	setProjectRunMode(t, db, project.ID, drama.RunEveryStep)
	if _, err := svc.UpdateFeatureAvailability(&model.User{ID: "admin", Role: model.UserRoleAdmin}, FeatureAvailability{CustomChannelsEnabled: true, TaskCenterEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("workflow_id", drama.WorkflowSSSDrama).Error; err != nil {
		t.Fatal(err)
	}
	project.WorkflowID = drama.WorkflowSSSDrama
	c1, c2, c3 := chapters[0].ID, chapters[1].ID, chapters[2].ID
	upstream := &streamingShotsUpstream{scenes: map[string][]string{
		"E01S01": {"## 立意页", "", "### 一句话故事", "夜班护士发现七床空了。", "", "## 全集结构表", "", "| 集 | 任务 | 卡点 |", "|---|---|---|", "| 1 | 七床空了 | 电梯门开 |", "| 2 | 刀 | 现在信了 |"},
		"E01S02": episodeLines(1, "七床空了", fmt.Sprintf(`[{"unitId": %q, "from": 1, "to": 3}, {"unitId": %q, "from": 1, "to": 1}]`, c1, c2),
			"## 内·病房走廊·夜", "林小满：七床？", "## 第一部分　本集设计", "空床", "## 第二部分　台词定稿表", "| 定稿台词 | 处理 |", "|---|---|", "| 七床？ | 保留 |"),
		"E01S03": episodeLines(2, "现在信了", fmt.Sprintf(`[{"unitId": %q, "from": 2, "to": 3}, {"unitId": %q, "from": 1, "to": 4}]`, c2, c3),
			"## 内·电梯口·夜", "周岸：现在信了？", "## 第一部分　本集设计", "揭晓", "## 第二部分　台词定稿表", "| 定稿台词 | 处理 |", "|---|---|", "| 现在信了？ | 保留 |"),
	}}
	server := httptest.NewServer(upstream)
	defer server.Close()
	config := map[string]any{"baseUrl": server.URL, "apiKey": "test-key", "interfaceType": "chat-completion", "model": "test"}
	started, err := svc.RunDramaProjectStep(project.UserID, project.ID, drama.StepScriptAdapt, DramaProjectStepRunRequest{TargetEpisodes: 2, Model: "test", Config: config})
	if err != nil || taskDramaStep(started.Task.InputJSON).Contract != drama.ContractSSSScriptAdapt {
		t.Fatalf("剧本入口契约错误: %v", err)
	}
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("拆剧本：%v %s %q", err, done.Status, done.Error)
	}
	if upstream.calls != 3 || !strings.Contains(upstream.prompts[0], "第 1 回合只写立意与全集结构") || !strings.Contains(upstream.prompts[0], "SSS 短剧拆剧本") || !strings.Contains(upstream.prompts[1], "现在写第 1 集") {
		t.Fatalf("calls = %d", upstream.calls)
	}
	var result struct {
		DramaStep DramaScriptAdaptOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(done.ResultJSON), &result); err != nil || len(result.DramaStep.Episodes) != 2 || result.DramaStep.Errors != 0 {
		t.Fatalf("outcome = %+v %v", result.DramaStep, err)
	}
	doc, err := svc.repo.LatestOntologyDocument(project.ID, "", model.OntologyDocConcept)
	if err != nil || !strings.HasPrefix(doc.Content, "## 立意页") || !strings.Contains(doc.Content, "| 2 | 刀 | 现在信了 |") || strings.Contains(doc.Content, "七床？") {
		t.Fatalf("concept doc = %+v %v", doc, err)
	}
	units, _ := svc.repo.ProjectUnits(project.ID)
	for _, u := range units {
		if u.Kind == model.ProjectUnitKindEpisode {
			design, err := svc.repo.LatestOntologyDocument(project.ID, u.ID, model.OntologyDocDesign)
			if err != nil || !strings.Contains(design.Content, "第二部分　台词定稿表") || strings.Contains(u.SourceText, "本集设计") {
				t.Fatalf("集设计写入错误 %v %+v", err, design)
			}
		}
		if u.Kind == model.ProjectUnitKindEpisode && strings.Contains(u.SourceText, "立意") {
			t.Fatal("concept leaked into an episode")
		}
	}
	// 导演定画风：带上立意与结构，另要三部分。
	step, _ := drama.StepByID(drama.StepStyleLock)
	plan, err := svc.planStyleLock("user-1", &project, step, DramaProjectStepRunRequest{StyleText: "冷色夜班医院"})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.lock.World || len(plan.scenes) != 1 || plan.scenes[0].ID != drama.StyleLockWorldTurn || !strings.Contains(plan.prompt.Text, "【附加输出】") || !strings.Contains(plan.prompt.Text, "夜班护士发现七床空了") || plan.usage.Skill.ID != "yz-sss-drama-style" {
		t.Fatalf("style plan = %+v %s", plan.lock, plan.usage.Skill.ID)
	}
	var first string
	for _, unit := range units {
		if unit.Kind == model.ProjectUnitKindEpisode && unit.Title == "七床空了" {
			first = unit.ID
			break
		}
	}
	if first == "" {
		t.Fatal("第一集缺失")
	}
	upstream.calls = 0
	upstream.scenes["E01S02"] = episodeLines(1, "七床空了", "[]", "## 内·病房走廊·夜", "林小满：人呢？", "## 第一部分　本集设计", "追问", "## 第二部分　台词定稿表", "| 定稿台词 | 处理 |", "|---|---|", "| 人呢？ | 改 |")
	if _, err = svc.RunDramaProjectStep(project.UserID, project.ID, drama.StepScriptAdapt, DramaProjectStepRunRequest{Mode: drama.ScriptAdaptModeRhythm, SourceUnitIDs: []string{first}, Model: "test", Config: config}); err != nil {
		t.Fatal(err)
	}
	updated, err := runQueuedTask(t, svc)
	if err != nil || updated.Status != model.TaskStatusSucceeded {
		t.Fatalf("调节奏任务失败: %v %s", err, updated.Error)
	}
	episode, err := svc.repo.ProjectUnit(project.ID, first)
	if err != nil || !strings.Contains(episode.SourceText, "人呢？") {
		t.Fatal("调节奏后剧集没有原地更新", err)
	}
	version, err := svc.repo.LatestOntologyDocument(project.ID, first, model.OntologyDocDesign)
	if err != nil || version.Version != 2 || !strings.Contains(version.Content, "人呢？") {
		t.Fatal("同集设计没有新增版本", err)
	}
}

// seedSSSEpisode 不调模型，直接用一组合格的各轮输出收尾，得到一集两段（E1-U1 12 秒、E1-U2 10 秒）和八部分。
func seedSSSEpisode(t *testing.T) (*Service, model.Project, model.ProjectUnit, *dramaSSSMeta, drama.Model) {
	t.Helper()
	svc, db := newChapterShotsTestService(t)
	now := time.Now()
	project := model.Project{ID: "project-sss-repair", UserID: "user-1", Name: "当铺", Status: model.ProjectStatusActive, GenerationModel: "seedance-2.5", WorkflowID: drama.WorkflowSSSDrama, WorkflowVersion: drama.WorkflowVersion, AspectRatio: "9:16", Revision: 1, CreatedAt: now, UpdatedAt: now}
	unit := model.ProjectUnit{ID: "sss-repair-1", ProjectID: project.ID, Kind: model.ProjectUnitKindEpisode, Title: "第一集", SourceText: "<p>林夏走进当铺。</p>", Status: model.ProjectUnitStatusDraft, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	usage, skill, err := svc.sssStepSkill(&project, mustWorkflow(t, project.WorkflowID), mustStep(t, drama.StepChapterShots))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := svc.planSSSShots("user-1", &project, &unit, usage, skill, nil, nil, sssShotOptions{Flow: 1})
	if err != nil {
		t.Fatal(err)
	}
	plan.sss.Setup = false
	turn := func(kind, unit, text string) dramaRunTurn { return dramaRunTurn{SSS: kind, Scene: unit, Text: text} }
	cut := "## 整场镜头表\n\n分两段。\n\n## 第四部分　单元总表\n\n| 单元 |\n|---|\n| E1-U1 |\n| E1-U2 |\n\n## 启发库选用\n\n| 单元 | 库号 |\n|---|---|\n| E1-U1 | A01 |\n| E1-U2 | E01 |\n"
	progress := dramaRunProgress{RunID: "run", Turns: []dramaRunTurn{
		turn(drama.SSSTurnPlan, "", "## 第一部分　本集设计\n\n钩子。\n\n## 第二部分　台词定稿表\n\n无\n"),
		turn(drama.SSSTurnDirect, "", "## 第三部分　讲戏本\n\n在场：林夏。\n"),
		turn(drama.SSSTurnCut, "", cut),
		turn(drama.SSSTurnCard, "E1-U1", sssFakeCardBody("E1-U1", 12)), turn(drama.SSSTurnCompile, "E1-U1", sssFakeCompile(true)),
		turn(drama.SSSTurnCard, "E1-U2", sssFakeCardBody("E1-U2", 10)), turn(drama.SSSTurnCompile, "E1-U2", sssFakeCompile(true)),
		turn(drama.SSSTurnReview, "", "## 第六部分　后期备忘\n\n剪定格。\n\n## 第七部分　集交接卡\n\n林夏在柜台外。\n\n## 第八部分　复看记录与待确认\n\n无\n"),
	}}
	taskReq := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{})
	input, _ := json.Marshal(taskReq.Input)
	task := model.Task{ID: "task-sss-seed", UserID: "user-1", ProjectID: project.ID, InputJSON: string(input)}
	if _, err := svc.applySSSShotsTask(task, plan.sss, map[string]interface{}{"dramaRun": progress}); err != nil {
		t.Fatal(err)
	}
	m, _ := projectDramaModel(&project)
	return svc, project, unit, plan.sss, m
}

// sssRepairFor 是返修任务建时的快照：读八部分里这一刻的原卡。
func sssRepairFor(t *testing.T, svc *Service, project model.Project, unit model.ProjectUnit, base *dramaSSSMeta, segment string, unitID string) *dramaSSSMeta {
	t.Helper()
	doc, err := svc.repo.LatestOntologyDocument(project.ID, unit.ID, model.OntologyDocEpisode)
	if err != nil {
		t.Fatal(err)
	}
	cards, _ := drama.ParseSSSEpisodeCards(doc.Content)
	segments, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	meta := *base
	for _, c := range cards {
		if c.ID != unitID {
			continue
		}
		for _, s := range segments {
			if s.Spec.ID == segment {
				meta.Repair = &sssRepairMeta{ShotID: s.ShotID, SegmentID: segment, Unit: unitID, Symptom: "看不清", Card: c.Text()}
			}
		}
	}
	if meta.Repair == nil {
		t.Fatal("repair target not found", segment, unitID)
	}
	return &meta
}

func sssRepaired(unit string, seconds int) sssOutputs {
	return sssOutputs{Repair: "## 诊断\n\n看不清。\n\n## 改法\n\n改近景。\n\n## 重写后的单元卡\n\n" + sssFakeCardBody(unit, seconds), RepairCompile: sssFakeCompile(true)}
}

// sssEpisodeState 是本集各段时长和八部分里各单元卡的时长（段与文档必须一致）。
func sssEpisodeState(t *testing.T, svc *Service, project model.Project, unit model.ProjectUnit) (map[string]float64, map[string]float64, string) {
	t.Helper()
	segments, err := svc.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	segs := map[string]float64{}
	for _, s := range segments {
		segs[s.Spec.Title] = s.Spec.Dur
	}
	doc, err := svc.repo.LatestOntologyDocument(project.ID, unit.ID, model.OntologyDocEpisode)
	if err != nil {
		t.Fatal(err)
	}
	cards, err := drama.ParseSSSEpisodeCards(doc.Content)
	if err != nil {
		t.Fatal(err)
	}
	docs := map[string]float64{}
	for _, c := range cards {
		docs[c.ID] = c.TotalSeconds
	}
	return segs, docs, doc.Content
}

// SSS-05：返修快照过期（另一次返修先收尾、或目标段已不在）时，在写任何东西之前拒绝；段和八部分始终是同一个版本。
func TestSSSRepairRejectsStaleSnapshots(t *testing.T) {
	svc, project, unit, base, m := seedSSSEpisode(t)
	target := dramaStepTaskTarget{ProjectID: project.ID, UnitID: unit.ID}
	repairTask := func(id string) model.Task { return model.Task{ID: id, UserID: "user-1"} }
	// 两次返修都在 S02 是 10 秒时建；B 先收尾成 8 秒，A 再拿同一份旧快照收尾成 6 秒：A 必须被拒，段和文档都留在 B。
	a := sssRepairFor(t, svc, project, unit, base, "S02", "E1-U2")
	b := sssRepairFor(t, svc, project, unit, base, "S02", "E1-U2")
	if _, err := svc.applySSSRepair(repairTask("task-b"), target, b, sssRepaired("E1-U2", 8), m, DramaStepTaskOutcome{}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.applySSSRepair(repairTask("task-a"), target, a, sssRepaired("E1-U2", 6), m, DramaStepTaskOutcome{})
	if err == nil || !strings.Contains(err.Error(), "已经变了") {
		t.Fatalf("stale snapshot must be rejected: %v", err)
	}
	segs, docs, content := sssEpisodeState(t, svc, project, unit)
	if segs["E1-U2"] != 8 || docs["E1-U2"] != 8 || segs["E1-U1"] != 12 || docs["E1-U1"] != 12 || strings.Count(content, "## 返修记录 E1-U2") != 1 {
		t.Fatalf("after stale reject: segs=%v docs=%v records=%d", segs, docs, strings.Count(content, "## 返修记录"))
	}
	// 同一任务重复收尾：不再写第二遍（八部分不加版本、返修记录不重复）。
	before, _ := svc.repo.LatestOntologyDocument(project.ID, unit.ID, model.OntologyDocEpisode)
	if _, err := svc.applySSSRepair(repairTask("task-b"), target, b, sssRepaired("E1-U2", 8), m, DramaStepTaskOutcome{}); err != nil {
		t.Fatal("re-finalizing the same task must succeed idempotently", err)
	}
	if after, _ := svc.repo.LatestOntologyDocument(project.ID, unit.ID, model.OntologyDocEpisode); after.Version != before.Version {
		t.Fatalf("re-finalize added a version: %d → %d", before.Version, after.Version)
	}
	// 普通连续返修：按现在的卡再建快照，8 → 7 秒通过。
	c := sssRepairFor(t, svc, project, unit, base, "S02", "E1-U2")
	if _, err := svc.applySSSRepair(repairTask("task-c"), target, c, sssRepaired("E1-U2", 7), m, DramaStepTaskOutcome{}); err != nil {
		t.Fatal(err)
	}
	// 不同目标：S01 的快照不受 S02 返修影响。
	d := sssRepairFor(t, svc, project, unit, base, "S01", "E1-U1")
	if _, err := svc.applySSSRepair(repairTask("task-d"), target, d, sssRepaired("E1-U1", 11), m, DramaStepTaskOutcome{}); err != nil {
		t.Fatal(err)
	}
	segs, docs, content = sssEpisodeState(t, svc, project, unit)
	if segs["E1-U1"] != 11 || docs["E1-U1"] != 11 || segs["E1-U2"] != 7 || docs["E1-U2"] != 7 || strings.Count(content, "## 返修记录") != 3 {
		t.Fatalf("consecutive repairs: segs=%v docs=%v records=%d", segs, docs, strings.Count(content, "## 返修记录"))
	}
	// 收尾时段已写、文档没写成（模拟上次写文档失败）：从中断处继续重新收尾，文档补上，段与文档一致。
	e := sssRepairFor(t, svc, project, unit, base, "S02", "E1-U2")
	card := drama.ParseSSSCompile(mustSSSCard(t, sssRepairCardText(sssRepaired("E1-U2", 9).Repair), "E1-U2"), sssFakeCompile(true))
	seg, err := drama.SSSSegment(card, 2, e.Materials)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(seg)
	if _, err := svc.writeDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{UnitID: unit.ID, Segments: []json.RawMessage{raw}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.applySSSRepair(repairTask("task-e"), target, e, sssRepaired("E1-U2", 9), m, DramaStepTaskOutcome{}); err != nil {
		t.Fatal("resuming after the segment was written must finish the document", err)
	}
	if segs, docs, _ = sssEpisodeState(t, svc, project, unit); segs["E1-U2"] != 9 || docs["E1-U2"] != 9 {
		t.Fatalf("resume: segs=%v docs=%v", segs, docs)
	}
	// 目标段已不在（镜头 ID 对不上）：拒绝，什么都不写。
	f := sssRepairFor(t, svc, project, unit, base, "S02", "E1-U2")
	f.Repair.ShotID = "shot-gone"
	if _, err := svc.applySSSRepair(repairTask("task-f"), target, f, sssRepaired("E1-U2", 5), m, DramaStepTaskOutcome{}); err == nil || !strings.Contains(err.Error(), "已经不在本集里") {
		t.Fatalf("missing target segment must be rejected: %v", err)
	}
	if segs, docs, _ = sssEpisodeState(t, svc, project, unit); segs["E1-U2"] != 9 || docs["E1-U2"] != 9 {
		t.Fatalf("rejected repair must not write: segs=%v docs=%v", segs, docs)
	}
}

func mustSSSCard(t *testing.T, text string, unit string) drama.SSSUnitCard {
	t.Helper()
	card, err := sssCardOf(text, unit)
	if err != nil {
		t.Fatal(err)
	}
	return card
}

// SSS-04：资产名撞上保留写法时加「图」字，去空白、去 @，同名加序号。
func TestSSSMaterialNamesAvoidReservedTags(t *testing.T) {
	got := sssMaterialsFrom([]drama.OntologyMaterial{
		{Index: 1, Name: "林夏", Category: "character", VersionID: "a"},
		{Index: 2, Name: "林 夏", Category: "character", VersionID: "b"},
		{Index: 3, Name: "视频", Category: "prop", VersionID: "c"},
		{Index: 4, Name: "站位参考", Category: "environment", VersionID: "d"},
		{Index: 5, Name: "林夏声音", Category: "character", VersionID: "e"},
		{Index: 6, Name: "@音频1", Category: "prop", VersionID: "f"},
		{Index: 7, Name: " ", Category: "prop", VersionID: "g"},
	})
	var names []string
	for _, m := range got {
		names = append(names, m.Name)
		if drama.SSSReservedTag(m.Name) != "" {
			t.Fatalf("material name %q collides with a reserved tag", m.Name)
		}
	}
	if strings.Join(names, ",") != "林夏,林夏2,视频图,站位参考图,林夏声音图,音频1图,道具" {
		t.Fatal(names)
	}
}
