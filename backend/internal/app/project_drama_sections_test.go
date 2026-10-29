package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// sectionShotsUpstream 是按小节生成的假上游：从本轮提示词读出节号、戏类和本节台词表，按戏类的技能原生格式回这一节
// （先一个建立镜，之后每句台词一镜）。drop 是要漏写的台词 id（制造 R-SHOT-001）；fail 返回非 0 时这次调用直接返回该状态码。
type sectionShotsUpstream struct {
	mu        sync.Mutex
	calls     int
	histories []int
	prompts   []string
	sections  []string
	drop      map[string]bool
	fail      func(call int, section string) int
	// rewrite 改写这一节的回复（模拟模型按提示把人物名写成子卡「名·变体」）。
	rewrite func(section string, out string) string
}

var (
	sectionTurnHead  = regexp.MustCompile(`【第 (\d+)/(\d+) 节：(E\d{2}S\d{2}-P\d+)】[^\n]*戏类：(文戏 15|文戏 30)`)
	sectionTableLine = regexp.MustCompile(`(?m)^- (E\d{2}S\d{2}L\d+) ([^：\s]+)：(.+?)(（画外）)?$`)
)

func (u *sectionShotsUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	head := sectionTurnHead.FindStringSubmatch(last.Content)
	u.mu.Lock()
	u.calls++
	call := u.calls
	u.prompts = append(u.prompts, last.Content)
	u.histories = append(u.histories, len(body.Messages)-1)
	section := ""
	if head != nil {
		section = head[3]
	}
	u.sections = append(u.sections, section)
	u.mu.Unlock()
	if u.fail != nil {
		if status := u.fail(call, section); status != 0 {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"message":"upstream unavailable"}}`)
			return
		}
	}
	if head == nil {
		(&streamingShotsUpstream{}).reply(w, body.Stream, []string{"没读到节号，先解释一下。"})
		return
	}
	var lines [][]string
	for _, m := range sectionTableLine.FindAllStringSubmatch(last.Content[strings.Index(last.Content, "【本节台词表】"):], -1) {
		if !u.drop[m[1]] {
			lines = append(lines, m)
		}
	}
	var out string
	if head[4] == "文戏 15" {
		out = fakeWenxi15Section(head[1], lines)
	} else {
		out = fakeWenxi30Section(head[1], lines)
	}
	if u.rewrite != nil {
		out = u.rewrite(section, out)
	}
	(&streamingShotsUpstream{}).reply(w, body.Stream, strings.Split(out, "\n"))
}

func fakeSpeakers(lines [][]string) []string {
	var out []string
	for _, m := range lines {
		if !slices.Contains(out, m[2]) {
			out = append(out, m[2])
		}
	}
	if len(out) == 0 {
		out = []string{"林小满"}
	}
	return out
}

// fakeWenxi15Section 是 7.3 原生格式的一个片段：建立镜 + 每句台词一镜，带 BGM（编译时应去掉）。
func fakeWenxi15Section(k string, lines [][]string) string {
	who := fakeSpeakers(lines)
	var b strings.Builder
	fmt.Fprintf(&b, "## 片段 %s（时长：12秒）\n\n", k)
	fmt.Fprintf(&b, "**要素提取** [角色：%s；场景：病房走廊；道具：病历夹；BGM背景音乐：低音钢琴长音，慢速；摄影基调：冷白日光灯]；Audio：SFX：日光灯镇流器低鸣、脚步声。\n\n", strings.Join(who, "、"))
	fmt.Fprintf(&b, "**当前空间站位** [%s站在走廊中央；主光源为头顶日光灯从上方直射]。\n\n", who[0])
	step := 11.0 / float64(len(lines)+1)
	fmt.Fprintf(&b, "S1，0s-%.1fs，全景、平视。%s转身看向门口。风格：冷峻悬疑。核心情绪：不安。\n", step, who[0])
	for i, m := range lines {
		fmt.Fprintf(&b, "S%d，%.1fs-%.1fs，中近景、过肩。%s抬头，握紧病历夹。风格：冷峻悬疑。核心情绪：紧张。%s（台词）：“%s”\n", i+2, step*float64(i+1), step*float64(i+2), m[2], m[2], m[3])
	}
	fmt.Fprintf(&b, "\n**最后一镜结束站位** [11s-12s，%s停在门口]。\n\n**下一片段继承点** [%s停在门口、日光灯]。\n", who[0], who[0])
	return b.String()
}

// fakeWenxi30Section 是即梦导演分镜字段的一个小节。
func fakeWenxi30Section(k string, lines [][]string) string {
	who := fakeSpeakers(lines)
	step := 24.0 / float64(len(lines)+1)
	var b strings.Builder
	fmt.Fprintf(&b, "小节%s：第%s节\n\n生成时长：0-24秒\n剧情功能：推进。\n场景：夜里的医院。\n人物：%s。\n持续光源：头顶日光灯。\n持续声音：日光灯低鸣；禁止BGM/配乐。\n\n", k, k, strings.Join(who, "，"))
	shot := func(n int, speaker, text string) {
		fmt.Fprintf(&b, "镜头%d（%.1f-%.1f秒）\n镜头目的：推进剧情。\n镜头人物站位：%s站在画面左侧。\n画面：中景，正面，%s站在走廊里。\n镜头：50mm，浅景深。\n运镜：固定机位，起始景别=中景 -> 结束景别=中景。\n动作：%s转身，抬手按住病历夹。\n", n, step*float64(n-1), step*float64(n), speaker, speaker, speaker)
		if text == "" {
			b.WriteString("台词：无。\n")
		} else {
			fmt.Fprintf(&b, "台词：%s：%s【音色（清亮＋画内近场）+语速（中速）+语调（平）+情感（紧张）】；画内。\n", speaker, text)
		}
		b.WriteString("声音：脚步声，禁止BGM/配乐。\n灯光与光影：主光为头顶日光灯从上方直射，色温冷白。\n衔接：硬切，衔接点为视线。\n\n")
	}
	shot(1, who[0], "")
	for i, m := range lines {
		shot(i+2, m[2], m[3])
	}
	b.WriteString("小节约束：人物形象一致，无字幕。\n即梦粘贴提示词：\n0-4秒：建立。\n声音：脚步声；禁止BGM/配乐。\n")
	return b.String()
}

func qichuangOnModel(t *testing.T, svc *Service, db *gorm.DB, project model.Project, generationModel string) model.Project {
	t.Helper()
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("generation_model", generationModel).Error; err != nil {
		t.Fatal(err)
	}
	fresh, err := svc.repo.ProjectForUser("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return *fresh
}

func textStream(t *testing.T, svc *Service, taskID string) string {
	t.Helper()
	deltas, err := svc.repo.TaskTextDeltas("user-1", taskID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var stream strings.Builder
	for _, d := range deltas {
		stream.WriteString(d.Content)
	}
	return stream.String()
}

// 七床第一集、Seedance 2.5（30 秒）项目：默认文戏 30、绑定 yz-shots-wenxi30；按小节一轮一节、每节一段，段记小节和戏类；
// 第 2 节一直 503 时任务失败、保留第 1 节，「从第 2 节继续」只调剩下的节；原样输出存进镜头表记录，任务结果换成转出的镜头行。
func TestSectionShotsRunThroughWorkerWenxi30(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	failSecond := true
	upstream := &sectionShotsUpstream{fail: func(call int, section string) int {
		if failSecond && section == "E01S02-P1" {
			return http.StatusServiceUnavailable
		}
		return 0
	}}
	server := httptest.NewServer(upstream)
	defer server.Close()

	usage, err := svc.DramaStepUsage("user-1", project.ID, drama.StepChapterShots, nil)
	if err != nil || usage.Genre == nil || usage.Genre.ID != drama.ShotGenreWenxi30 || usage.Skill.ID != "yz-shots-wenxi30" || usage.BindingStep != "chapter.shots#wenxi30" || usage.Contract != "section.wenxi30@1" {
		t.Fatalf("30 秒模型用文戏 30：%v %+v", err, usage)
	}
	first := queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	shots := taskDramaShots(first.InputJSON)
	if shots == nil || shots.Genre != drama.ShotGenreWenxi30 || len(shots.Sections) != 3 || len(shots.Turns) != 3 {
		t.Fatalf("任务带小节表：%+v", shots)
	}
	if strings.Contains(first.InputJSON, "先给每一场判戏类") || strings.Contains(first.Prompt, "戏类卡") {
		t.Fatal("不再判戏类、不再注入戏类卡")
	}
	failed, err := runQueuedTask(t, svc)
	if err == nil || failed.Status != model.TaskStatusFailed || !strings.Contains(failed.Error, "第 2 节") || !strings.Contains(failed.Error, "可从第 2 节继续") {
		t.Fatalf("第 2 节失败：%v %s %q", err, failed.Status, failed.Error)
	}
	prompt := upstream.prompts[0]
	for _, want := range []string{`skill-id="yz-shots-wenxi30"`, "跳过「即梦粘贴提示词」", "不写即梦粘贴提示词", "cards/04-对话戏.md", "【分节生成说明】本章共 3 个小节", "【第 1/3 节：E01S01-P1】", "【本节台词表】", "E01S01L02 林小满：七床的人呢？", "【本节输出要求】", "小节1："} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("第一轮提示词缺「%s」", want)
		}
	}
	if strings.Contains(prompt, "E01S02L01") || strings.Contains(prompt, "受保护输出契约") {
		t.Fatal("第一轮只带第 1 节的台词，不再拼 JSON Lines 契约")
	}
	if !strings.HasPrefix(upstream.prompts[1], "继续写下一节。【第 2/3 节：E01S02-P1】") {
		t.Fatalf("续写一节：%.80s", upstream.prompts[1])
	}

	failSecond = false
	upstream.calls, upstream.histories, upstream.sections = 0, nil, nil
	queueDramaStepTask(t, svc, db, project, unit, server.URL, failed.ID)
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("从第 2 节继续：%v %s %q", err, done.Status, done.Error)
	}
	if upstream.calls != 2 || !slices.Equal(upstream.sections, []string{"E01S02-P1", "E01S02-P2"}) || upstream.histories[0] != 2 {
		t.Fatalf("只写剩下两节、带第 1 节对话：%d %v %v", upstream.calls, upstream.sections, upstream.histories)
	}
	var result struct {
		Text      string               `json:"text"`
		DramaRun  dramaRunProgress     `json:"dramaRun"`
		DramaStep DramaStepTaskOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(done.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result.DramaStep.Status != DramaStepOutcomeWritten || result.DramaStep.Genre != drama.ShotGenreWenxi30 || result.DramaStep.Sections != 3 || result.DramaStep.Segments != 3 || result.DramaStep.Errors != 0 {
		t.Fatalf("收尾一节一段：%+v", result.DramaStep)
	}
	if rows, _, err := drama.StoryboardRowsFromText(result.Text); err != nil || len(rows) != result.DramaStep.Shots {
		t.Fatalf("任务结果 text 换成转出的镜头行：%v %d", err, len(rows))
	}
	for _, turn := range result.DramaRun.Turns {
		if turn.Text != "" || turn.Section == "" || turn.Genre != drama.ShotGenreWenxi30 {
			t.Fatalf("进度只留摘要、每轮记小节：%+v", turn)
		}
	}
	segments, err := svc.DramaSegments("user-1", project.ID, unit.ID)
	if err != nil || len(segments) != 3 {
		t.Fatalf("三节三段：%v %d", err, len(segments))
	}
	for i, seg := range segments {
		want := shots.Sections[i].ID
		if seg.Spec.Section != want || seg.Spec.Genre != drama.ShotGenreWenxi30 || seg.Spec.Dur > 30 {
			t.Fatalf("第 %d 段：小节 %s 戏类 %s 时长 %.0f", i+1, seg.Spec.Section, seg.Spec.Genre, seg.Spec.Dur)
		}
	}
	view, err := svc.DramaShotlist("user-1", project.ID, unit.ID)
	if err != nil || view == nil || len(view.Sections) != 3 || !strings.HasPrefix(view.Sections[0].Raw, "小节1：") || len(view.Sections[2].Shots) == 0 || view.Sections[1].TaskID != done.ID || view.Sections[0].TaskID != failed.ID {
		t.Fatalf("每节原样输出存进镜头表记录：%v %+v", err, view)
	}
	for _, sh := range view.Shotlist.Shots {
		if sh.Section == "" || sh.Genre != drama.ShotGenreWenxi30 || len(sh.Lines) > 1 {
			t.Fatalf("镜头记小节和戏类：%+v", sh)
		}
		if containsAnyWord(sh.Sfx, "BGM", "配乐") {
			t.Fatalf("配乐字样不进镜头：%q", sh.Sfx)
		}
	}
	stream := textStream(t, svc, done.ID)
	if !strings.HasPrefix(stream, `{"@turn":{"index":2,"total":3,"scene":"E01S02"`) || !strings.Contains(stream, `"section":"E01S02-P1","genre":"文戏 30"`) || !strings.Contains(stream, `{"rows":[`) {
		t.Fatalf("文本流按节写标记、写完一节补一行转出的镜头：%.300s", stream)
	}
}

func containsAnyWord(text string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// Seedance 2.0（15 秒）项目同一集：文戏 15、yz-shots-wenxi15，每节不超过 15 秒、最多两句台词（极短三句）；
// 原样输出里留着 BGM，编译出的视频提示词里没有。
func TestSectionShotsWenxi15Project(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	project = qichuangOnModel(t, svc, db, project, "seedance-2.0")
	upstream := &sectionShotsUpstream{}
	server := httptest.NewServer(upstream)
	defer server.Close()
	task := queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	shots := taskDramaShots(task.InputJSON)
	if shots == nil || shots.Genre != drama.ShotGenreWenxi15 || len(shots.Sections) < 5 {
		t.Fatalf("15 秒模型用文戏 15：%+v", shots)
	}
	for _, sec := range shots.Sections {
		if sec.Est > 15 || len(sec.LineIDs) > 3 {
			t.Fatalf("%s 估时 %.1f、%d 句台词", sec.ID, sec.Est, len(sec.LineIDs))
		}
	}
	if !strings.Contains(task.Prompt, `skill-id="yz-shots-wenxi15"`) || strings.Contains(task.Prompt, "cards/04-对话戏.md") || !strings.Contains(shots.Turns[0], "## 片段 1（时长：XX秒）") {
		t.Fatal("文戏 15 项目用 7.3 技能")
	}
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("生成：%v %q", err, done.Error)
	}
	if upstream.calls != len(shots.Sections) {
		t.Fatalf("一节一轮：%d 次调用，%d 节", upstream.calls, len(shots.Sections))
	}
	view, _ := svc.DramaShotlist("user-1", project.ID, unit.ID)
	if view.Draft || !strings.Contains(view.Sections[0].Raw, "BGM背景音乐") {
		t.Fatalf("原样输出保留 BGM：%+v", view.Check)
	}
	segments, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	if len(segments) != len(shots.Sections) {
		t.Fatalf("一节一段：%d 段 %d 节", len(segments), len(shots.Sections))
	}
	compiled, err := svc.CompileProjectShot("user-1", project.ID, segments[0].ShotID, CompileProjectShotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(compiled.Result.Prompt, "钢琴") || strings.Contains(compiled.Result.Prompt, "BGM") || compiled.Spec.Genre != drama.ShotGenreWenxi15 {
		t.Fatalf("编译稿不带 BGM：%s", compiled.Result.Prompt)
	}
}

// 「重新生成问题小节」：第 2 节漏了一句台词存成草稿，问题挂在第 2 节；onlySections 只重写这一节、只调一次模型，
// 其余节的原样输出进对话历史、镜头行原样留下；onlyScenes 等于重写这一场的全部小节。
func TestOnlySectionsRerunsProblemSection(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	upstream := &sectionShotsUpstream{drop: map[string]bool{"E01S02L02": true}}
	server := httptest.NewServer(upstream)
	defer server.Close()
	queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("整章生成：%v %q", err, done.Error)
	}
	view, _ := svc.DramaShotlist("user-1", project.ID, unit.ID)
	if !view.Draft || len(view.Issues) != 1 || view.Issues[0].Section != "E01S02-P1" {
		t.Fatalf("第 2 节有问题、存成草稿：%+v", view.Issues)
	}
	// 没重写的节按节记下镜头（镜头 id 会跟着前面重写的节挪，按节比内容）。
	keptShots := func(shots []drama.Shot) map[string][]string {
		out := map[string][]string{}
		for _, sh := range shots {
			if sh.Section != "E01S02-P1" {
				out[sh.Section] = append(out[sh.Section], sh.Action+"|"+fmt.Sprint(sh.Lines))
			}
		}
		return out
	}
	before := keptShots(view.Shotlist.Shots)

	plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.onlyScenesRun(project.ID, unit.ID, plan, newDramaRunMeta(plan, unit.ID), nil, []string{"E01S09-P1"}); err == nil {
		t.Fatal("不在本章的小节应拒绝")
	}
	if run, err := svc.onlyScenesRun(project.ID, unit.ID, plan, newDramaRunMeta(plan, unit.ID), []string{"E01S02"}, nil); err != nil || !slices.Equal(run.OnlySections, []string{"E01S02-P1", "E01S02-P2"}) {
		t.Fatalf("onlyScenes 展开成这一场的全部小节：%v %+v", err, run.OnlySections)
	}
	run, err := svc.onlyScenesRun(project.ID, unit.ID, plan, newDramaRunMeta(plan, unit.ID), nil, []string{"E01S02-P1"})
	if err != nil || !slices.Equal(run.OnlySections, []string{"E01S02-P1"}) || run.BaseShotlistVersion != view.Version {
		t.Fatalf("只重写第 2 节：%v %+v", err, run)
	}
	req := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": server.URL, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	req.Input["metadata"].(map[string]any)["dramaRun"] = run
	insertDramaTestTask(t, svc, db, project.ID, req)
	upstream.drop, upstream.calls, upstream.histories, upstream.sections = nil, 0, nil, nil
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("重新生成问题小节：%v %q", err, done.Error)
	}
	if upstream.calls != 1 || upstream.sections[0] != "E01S02-P1" || upstream.histories[0] != 2 {
		t.Fatalf("只调一次模型、带第 1 节的对话：%d %v %v", upstream.calls, upstream.sections, upstream.histories)
	}
	after, _ := svc.DramaShotlist("user-1", project.ID, unit.ID)
	if after.Draft || after.Check.Errors != 0 || len(after.Sections) != 3 {
		t.Fatalf("合并整章、检查通过：%+v", after.Check)
	}
	if got := keptShots(after.Shotlist.Shots); fmt.Sprint(got) != fmt.Sprint(before) || len(got) != 2 {
		t.Fatalf("没重写的两节镜头不变：\n%v\n%v", before, got)
	}
	stream := textStream(t, svc, done.ID)
	if strings.Count(stream, `"@turn"`) != 1 || !strings.Contains(stream, `"only":["E01S02-P1"]`) {
		t.Fatalf("文本流只有第 2 节：%.200s", stream)
	}
	// 按场生成的旧镜头表（没有存小节原样输出）不能只重写几节。
	if err := db.Model(&model.DramaShotlist{}).Where("unit_id = ?", unit.ID).Update("sections_json", "").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.onlyScenesRun(project.ID, unit.ID, plan, newDramaRunMeta(plan, unit.ID), nil, []string{"E01S02-P1"}); err == nil || !strings.Contains(err.Error(), "不是按小节生成的") {
		t.Fatalf("旧镜头表应拒绝只重写几节：%v", err)
	}
}

// 本场色卡只放在本场第一节的本轮指令里；装段后每段以 role=style 绑本场色卡，同场各节绑同一张。
func TestSectionShotsCarryScenePalette(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	seedQichuangStyle(t, svc, project, unit)
	if fresh, err := svc.repo.ProjectForUser("user-1", project.ID); err == nil {
		project = *fresh
	}
	upstream := &sectionShotsUpstream{}
	server := httptest.NewServer(upstream)
	defer server.Close()
	queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("生成：%v %q", err, done.Error)
	}
	if len(upstream.prompts) != 3 || !strings.Contains(upstream.prompts[0], qichuangPaletteHex[0]) || !strings.Contains(upstream.prompts[1], qichuangPaletteHex[1]) || strings.Contains(upstream.prompts[2], "【本场色卡】") {
		t.Fatalf("本场第一节带色卡，同场后续节不再带：%d", len(upstream.prompts))
	}
	if strings.Contains(upstream.prompts[0], qichuangPaletteHex[1]) {
		t.Fatal("第 1 节不带第 2 场的色卡")
	}
	palettes, _ := svc.projectPaletteAssets("user-1", project.ID)
	segments, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	for _, seg := range segments {
		refs := shotReferencesByRole(t, db, seg.ShotID, shotAssetRoleStyle)
		code := drama.SceneCode(1, sceneNoOf(t, seg.Spec.Scene))
		if len(refs) != 1 || refs[0].AssetVersionID != palettes[code].PrimaryVersionID {
			t.Fatalf("段 %s（%s）绑本场色卡：%+v", seg.Spec.ID, seg.Spec.Section, refs)
		}
	}
}

// 场次表接口按戏类带出每场的小节；后台流程技能页有文戏 15 / 文戏 30 / 武戏三行戏类绑定。
func TestDramaScenesSectionsAndGenreBindingRows(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	view, err := svc.DramaScenes("user-1", project.ID, unit.ID, nil)
	if err != nil || view.Genre == nil || view.Genre.ID != drama.ShotGenreWenxi30 || view.Genre.MaxSeconds != 30 {
		t.Fatalf("场次表带戏类：%v %+v", err, view)
	}
	total := 0
	for _, sc := range view.Scenes {
		total += len(sc.Sections)
		for _, sec := range sc.Sections {
			if !strings.HasPrefix(sec.ID, sc.ShotScene+"-P") || sec.Est > 30 {
				t.Fatalf("小节：%+v", sec)
			}
		}
	}
	if total != 3 {
		t.Fatalf("七床第一集文戏 30 切三节：%d", total)
	}
	project = qichuangOnModel(t, svc, db, project, "seedance-2.0")
	if view, _ := svc.DramaScenes("user-1", project.ID, unit.ID, nil); view.Genre.ID != drama.ShotGenreWenxi15 || view.Genre.MaxSeconds != 15 {
		t.Fatalf("15 秒模型的场次表按文戏 15 切：%+v", view.Genre)
	}
	matrix, err := svc.AdminDramaStepBindings(&model.User{ID: "admin", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	var genres []string
	for _, step := range matrix.Steps {
		if step.Genre != "" {
			genres = append(genres, step.ID)
			if step.Genre == drama.ShotGenreWuxi && !step.Reserved {
				t.Fatal("武戏行是预留的")
			}
		}
	}
	if !slices.Equal(genres, []string{"chapter.shots#wenxi15", "chapter.shots#wenxi30", "chapter.shots#wuxi"}) {
		t.Fatalf("三行戏类绑定：%v", genres)
	}
	project = qichuangOnModel(t, svc, db, project, "seedance-2.5")
	before, _ := svc.DramaStepUsage("user-1", project.ID, drama.StepChapterShots, nil)
	if _, err := svc.UpdateDramaStepBindings(&model.User{ID: "admin", Role: "admin"}, DramaStepBindingsRequest{Bindings: []DramaStepBindingCell{{StyleProfile: before.Profile.ID, Step: "chapter.shots#wenxi30", SkillID: "yz-shots-wenxi15"}}}); err != nil {
		t.Fatalf("戏类行能绑技能：%v", err)
	}
	if usage, _ := svc.DramaStepUsage("user-1", project.ID, drama.StepChapterShots, nil); usage.Skill.ID != "yz-shots-wenxi15" || !usage.Bound {
		t.Fatalf("按戏类行的绑定取技能：%+v", usage)
	}
}

// 同一个 Seedance 2.5 项目在不同视频渠道段长下：步骤用法（弹窗「戏类 / 本步使用」）、场次表（场次条节数）和生成分段任务
// 用同一个 rule 选戏类、切小节，三处一致。渠道只能 15 秒时三处都是文戏 15、9 节；30 秒或不带 rule 时都是文戏 30、3 节。
func TestSectionGenreSameAcrossUsageScenesAndRun(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	for _, tc := range []struct {
		name     string
		rule     *DramaShotlistRule
		genre    string
		skill    string
		sections int
	}{
		{"不带渠道段长", nil, drama.ShotGenreWenxi30, "yz-shots-wenxi30", 3},
		{"渠道上限 15 秒", &DramaShotlistRule{MaxSeconds: 15, MinSeconds: 4}, drama.ShotGenreWenxi15, "yz-shots-wenxi15", 9},
		{"渠道上限 30 秒", &DramaShotlistRule{MaxSeconds: 30, MinSeconds: 4}, drama.ShotGenreWenxi30, "yz-shots-wenxi30", 3},
	} {
		usage, err := svc.DramaStepUsage("user-1", project.ID, drama.StepChapterShots, tc.rule)
		if err != nil || usage.Genre == nil || usage.Genre.ID != tc.genre || usage.Skill.ID != tc.skill {
			t.Fatalf("%s：步骤用法 %v %+v", tc.name, err, usage)
		}
		view, err := svc.DramaScenes("user-1", project.ID, unit.ID, tc.rule)
		if err != nil || view.Genre == nil || view.Genre.ID != tc.genre || view.Genre.MaxSeconds != usage.Genre.MaxSeconds {
			t.Fatalf("%s：场次表戏类 %v %+v", tc.name, err, view)
		}
		var scenesIDs []string
		for _, sc := range view.Scenes {
			for _, sec := range sc.Sections {
				scenesIDs = append(scenesIDs, sec.ID)
			}
		}
		units, err := svc.ProjectDramaScenes("user-1", project.ID, tc.rule)
		if err != nil || len(units) != 1 || units[0].Genre.ID != tc.genre {
			t.Fatalf("%s：项目场次表戏类 %v", tc.name, err)
		}
		plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, tc.rule)
		if err != nil {
			t.Fatal(err)
		}
		req := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"model": "test"}, Rule: tc.rule})
		encoded, _ := json.Marshal(req.Input)
		shots := taskDramaShots(string(encoded))
		if shots == nil || shots.Genre != tc.genre || len(shots.Sections) != tc.sections {
			t.Fatalf("%s：生成分段任务 %+v", tc.name, shots)
		}
		var runIDs []string
		for _, sec := range shots.Sections {
			runIDs = append(runIDs, sec.ID)
		}
		if !slices.Equal(scenesIDs, runIDs) {
			t.Fatalf("%s：场次条的小节与生成分段的小节不一致：%v / %v", tc.name, scenesIDs, runIDs)
		}
	}
}

// 文戏 15 连着接话超过两句台词的一节：不拆、照常写库装段（0 error），提醒记进摘要说明。
func TestSectionShotsWenxi15LongExchangeIsWarnOnly(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	project = qichuangOnModel(t, svc, db, project, "seedance-2.0")
	html := "<p>## 内·病房走廊·夜</p><p>（林小满抬头看向门口，周岸转身。）</p><p>林小满：你是谁？</p><p>周岸：你不认识我了？</p><p>林小满：我该认识你吗？</p><p>周岸：你真的忘了？</p>"
	if err := db.Model(&model.ProjectUnit{}).Where("id = ?", unit.ID).Update("source_text", html).Error; err != nil {
		t.Fatal(err)
	}
	upstream := &sectionShotsUpstream{}
	server := httptest.NewServer(upstream)
	defer server.Close()
	task := queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if shots := taskDramaShots(task.InputJSON); shots == nil || len(shots.Sections) != 1 || len(shots.Sections[0].Warnings) != 1 || !strings.Contains(shots.Turns[0], "本节有 4 句台词") {
		t.Fatalf("四句接话落在一节、记提醒：%+v", shots)
	}
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("生成：%v %q", err, done.Error)
	}
	var result struct {
		DramaStep DramaStepTaskOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(done.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result.DramaStep.Status != DramaStepOutcomeWritten || result.DramaStep.Errors != 0 || !slices.ContainsFunc(result.DramaStep.Notes, func(n string) bool { return strings.Contains(n, "E01S01-P1") && strings.Contains(n, "4 句台词") }) {
		t.Fatalf("只是提醒，照常装段：%+v", result.DramaStep)
	}
	view, _ := svc.DramaShotlist("user-1", project.ID, unit.ID)
	if len(view.Sections) != 1 || len(view.Sections[0].Warnings) != 1 {
		t.Fatalf("原样输出记录带提醒：%+v", view.Sections)
	}
}
