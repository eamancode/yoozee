package app

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

var (
	wuxiParamSeconds = regexp.MustCompile(`【参数表（平台已填）】时长：(\d+) 秒`)
	wuxiParamPeople  = regexp.MustCompile(`A：([^；｜。]+)；B：([^；｜。（]+)`)
)

// fakeWuxiSection 是 v7.0 4.29 每镜标准格式的一个武戏小节：N 秒 N 镜（N 取本轮参数表），台词依次落在第 2 镜起，偶数镜甩镜擦伤带闪帧，
// 特效里写一句配乐（转换器应剥掉）。
func fakeWuxiSection(prompt string, lines [][]string) string {
	n := 10
	if m := wuxiParamSeconds.FindStringSubmatch(prompt); m != nil {
		fmt.Sscan(m[1], &n)
	}
	a, b := "周岸", "阿坤"
	if m := wuxiParamPeople.FindStringSubmatch(prompt); m != nil {
		a = strings.TrimSpace(m[1])
		if !strings.HasPrefix(m[2], "无") {
			b = strings.TrimSpace(m[2])
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "【核心观看体验】%s护住身后 + 窄走廊近身刀战 + 夺刀反制\n\n", a)
	for k := 1; k <= n; k++ {
		fmt.Fprintf(&out, "镜 %d｜%d-%ds\n", k, k-1, k)
		if k%2 == 0 {
			out.WriteString("景别 / 焦段 / 运镜：MS / 35mm / WHIP 甩镜\n")
			fmt.Fprintf(&out, "摄影机目标：%s\n站位：%s 画左中景·面向右·同高；%s 画右中景·面向左·同高；相距 1m\n", b, a, b)
			fmt.Fprintf(&out, "动作链：%s反手横划→刀尖擦过%s左袖口→%s侧身卸力→袖口裂开\n力度：P2 中·擦伤\n受击：R1 上身一晃\n战损：L1→L2\n闪帧：F2 黑闪 2 帧\n", b, a, a)
		} else {
			out.WriteString("景别 / 焦段 / 运镜：FS / 24mm / PUSH 急推\n")
			fmt.Fprintf(&out, "摄影机目标：%s\n站位：%s 画左中景·面向右·同高；%s 画右中景·面向左·同高；相距 3m\n", a, a, b)
			fmt.Fprintf(&out, "动作链：%s举刀扑来→%s侧身抢中线→无接触→双方贴近\n力度：P1 轻·试探\n受击：无\n战损：L1\n闪帧：无\n", b, a)
		}
		out.WriteString("特效：Layer1 衣摆气流；配乐压低\n环境反馈：头顶灯管晃动\n")
		fmt.Fprintf(&out, "表情：%s 眉微蹙；%s 冷笑\n", a, b)
		if i := k - 2; i >= 0 && i < len(lines) {
			fmt.Fprintf(&out, "台词：%s：“%s”\n", lines[i][2], lines[i][3])
		} else {
			out.WriteString("台词：无\n")
		}
		out.WriteString("光影：头顶日光灯，冷白 6500K\n下一镜连接：MOTION MATCH\n\n")
	}
	return out.String()
}

// queueRegenreTask 按 ChangeDramaSectionGenre 的形状建「改为武戏 / 改回文戏」的生成分段任务（自定义渠道指向假上游，挂计费单）。
func queueRegenreTask(t *testing.T, svc *Service, db *gorm.DB, project model.Project, unit model.ProjectUnit, upstream string, sectionID string, target string) (*model.Task, *dramaRegenre) {
	t.Helper()
	record, err := svc.repo.DramaShotlistForUnit(project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	change, err := sectionGenreChangeFor(&project, &unit, record, nil, sectionID, target)
	if err != nil {
		t.Fatal(err)
	}
	plan, run, err := svc.prepareDramaStepRun("user-1", &project, unit.ID, drama.StepChapterShots, DramaStepRunRequest{}, &change.overrides, change.regenre)
	if err != nil {
		t.Fatal(err)
	}
	req := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": upstream, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	req.Input["metadata"].(map[string]any)["dramaRun"] = run
	return insertDramaTestTask(t, svc, db, project.ID, req), change.regenre
}

// addStoryboardArtifact 给一段挂一张按当前版本算好指纹的分镜图（ready），看装段后它过不过期。
func addStoryboardArtifact(t *testing.T, svc *Service, db *gorm.DB, projectID string, view DramaSegmentView) string {
	t.Helper()
	id := newID()
	artifact := model.ShotArtifact{ID: id, ProjectID: projectID, UnitID: view.UnitID, ShotID: view.ShotID, RevisionID: view.RevisionID, Type: drama.ArtifactStoryboard, Version: 1, Status: "ready", Selected: true,
		InputHash: svc.artifactInputHash(view.ShotID, view.RevisionID, drama.ArtifactStoryboard), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func artifactStatus(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()
	var a model.ShotArtifact
	if err := db.First(&a, "id = ?", id).Error; err != nil {
		return "deleted"
	}
	return a.Status
}

func segmentSections(segments []DramaSegmentView) []string {
	var out []string
	for _, s := range segments {
		out = append(out, s.Spec.Section+"/"+s.Spec.Genre)
	}
	return out
}

func sectionGenresOf(t *testing.T, svc *Service, project model.Project, unit model.ProjectUnit) []string {
	t.Helper()
	view, err := svc.DramaShotlist("user-1", project.ID, unit.ID)
	if err != nil || view == nil {
		t.Fatalf("镜头表：%v", err)
	}
	var out []string
	for _, s := range view.Sections {
		out = append(out, fmt.Sprintf("%s/%s/%v", s.ID, s.Genre, s.Lines))
	}
	return out
}

// 卡 E 主路径（七床第一集，Seedance 2.5 = 30 秒，默认文戏 30）：E01S02 第 2 节改武戏 → 这一节的剧本范围按武戏重切成 2 节、请求用武戏技能
// （七床是真人剧档案，默认精简切片 yz-shots-wuxi-live）另起一条对话、带上一节结尾，只调这 2 节；镜头 extras 有力度标签、原样输出存进
// sections_json；装段：其余节的段和分镜图原样留着，原第 2 节的段换成武戏内容、分镜图过期，多出的一节新建段，不残留旧段；编译稿是
// 武戏段落结构。刷新（场次表）、整章重跑、只重跑别的节之后仍是武戏；改回文戏得到原来的 1 节文戏 30。
func TestSectionGenreChangeToWuxiAndBack(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	upstream := &sectionShotsUpstream{}
	server := httptest.NewServer(upstream)
	defer server.Close()
	queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("整章文戏 30：%v %q", err, done.Error)
	}
	before, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	if !slices.Equal(segmentSections(before), []string{"E01S01-P1/wenxi30", "E01S02-P1/wenxi30", "E01S02-P2/wenxi30"}) {
		t.Fatalf("默认三节文戏 30：%v", segmentSections(before))
	}
	keepArt := addStoryboardArtifact(t, svc, db, project.ID, before[1])
	staleArt := addStoryboardArtifact(t, svc, db, project.ID, before[2])

	// 参数校验：不认识的戏类、已经是文戏、不在本章的小节。
	for _, tc := range []struct{ section, genre, want string }{{"E01S02-P2", "wenxi60", "genre 只能是"}, {"E01S02-P2", "wenxi", "已经是文戏"}, {"E01S09-P1", "wuxi", "不在本章现在切出的小节里"}} {
		if _, err := svc.ChangeDramaSectionGenre("user-1", project.ID, unit.ID, tc.section, DramaSectionGenreRequest{Genre: tc.genre}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s → %s 应报「%s」：%v", tc.section, tc.genre, tc.want, err)
		}
	}

	upstream.calls, upstream.prompts, upstream.sections, upstream.histories = 0, nil, nil, nil
	task, regenre := queueRegenreTask(t, svc, db, project, unit, server.URL, "E01S02-P2", "wuxi")
	if regenre.From != "E01S02-P2" || regenre.FromGenre != drama.ShotGenreWenxi30 || regenre.Genre != drama.ShotGenreWuxi || !slices.Equal(regenre.Sections, []string{"E01S02-P2", "E01S02-P3"}) {
		t.Fatalf("改武戏重切成 2 节：%+v", regenre)
	}
	shots := taskDramaShots(task.InputJSON)
	if shots.Lanes[drama.ShotGenreWuxi] == "" || !strings.Contains(shots.Lanes[drama.ShotGenreWuxi], `skill-id="yz-shots-wuxi-live"`) || strings.Contains(task.Prompt, "yz-shots-wuxi") {
		t.Fatal("武戏另起一条对话，用仿真人档案的武戏技能")
	}
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("改武戏：%v %q", err, done.Error)
	}
	if upstream.calls != 2 || !slices.Equal(upstream.sections, []string{"E01S02-P2", "E01S02-P3"}) {
		t.Fatalf("只写两个武戏节：%d %v", upstream.calls, upstream.sections)
	}
	if upstream.histories[0] != 0 || upstream.histories[1] != 2 {
		t.Fatalf("武戏对话不带文戏节的历史、第二节接着武戏对话：%v", upstream.histories)
	}
	first := upstream.prompts[0]
	for _, want := range []string{`skill-id="yz-shots-wuxi-live"`, "【分节生成说明】本章共 4 个小节", "其中第 3、4 节是武戏", "戏类：武戏", "【参数表（平台已填）】时长：", "【本节节拍】", "【上一节结尾】同一场上一节 E01S02-P1（文戏 30）", "v7.0 每镜标准格式"} {
		if !strings.Contains(first, want) {
			t.Fatalf("武戏第一轮缺「%s」", want)
		}
	}
	if !strings.HasPrefix(upstream.prompts[1], "继续写下一节。【第 4/4 节：E01S02-P3】") {
		t.Fatalf("第二个武戏节续写：%.60s", upstream.prompts[1])
	}
	var result struct {
		DramaStep DramaStepTaskOutcome `json:"dramaStep"`
	}
	_ = json.Unmarshal([]byte(done.ResultJSON), &result)
	if result.DramaStep.Status != DramaStepOutcomeWritten || result.DramaStep.Sections != 4 || result.DramaStep.WuxiSections != 2 || result.DramaStep.Regenre == nil || result.DramaStep.Segments != 4 {
		t.Fatalf("收尾：%+v", result.DramaStep)
	}
	genres := sectionGenresOf(t, svc, project, unit)
	if !slices.Equal(genres, []string{"E01S01-P1/wenxi30/[2 11]", "E01S02-P1/wenxi30/[13 27]", "E01S02-P2/wuxi/[28 36]", "E01S02-P3/wuxi/[37 42]"}) {
		t.Fatalf("戏类按节存：%v", genres)
	}
	view, _ := svc.DramaShotlist("user-1", project.ID, unit.ID)
	var wuxiSeconds float64
	for _, sh := range view.Shotlist.Shots {
		if sh.Genre != drama.ShotGenreWuxi {
			continue
		}
		wuxiSeconds += float64(sh.Est)
		if sh.Extras["力度"] == "" || sh.Extras["站位"] == "" || sh.Fight == nil || containsAnyWord(sh.Extras["特效"], "配乐") {
			t.Fatalf("武戏镜头 extras：%+v", sh)
		}
	}
	if wuxiSeconds != float64(view.Sections[2].Est+view.Sections[3].Est) || !strings.Contains(view.Sections[2].Raw, "【核心观看体验】") {
		t.Fatalf("N 秒 N 镜、原样输出存下：%.0f", wuxiSeconds)
	}
	after, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	if !slices.Equal(segmentSections(after), []string{"E01S01-P1/wenxi30", "E01S02-P1/wenxi30", "E01S02-P2/wuxi", "E01S02-P3/wuxi"}) {
		t.Fatalf("装成 4 段：%v", segmentSections(after))
	}
	if after[0].ShotID != before[0].ShotID || after[1].ShotID != before[1].ShotID || after[2].ShotID != before[2].ShotID || after[3].ShotID == before[2].ShotID {
		t.Fatal("没变的节沿用原段，原第 2 节的段换成第一个武戏节，多出的一节新建")
	}
	if artifactStatus(t, db, keepArt) != "ready" || artifactStatus(t, db, staleArt) != "stale" {
		t.Fatalf("没变的段分镜图不动、被改的段分镜图过期：%s %s", artifactStatus(t, db, keepArt), artifactStatus(t, db, staleArt))
	}
	for _, seg := range after[2:] {
		if seg.Spec.Dur > 30 {
			t.Fatalf("武戏段不超过 30 秒：%.0f", seg.Spec.Dur)
		}
	}
	compiled, err := svc.CompileProjectShot("user-1", project.ID, after[2].ShotID, CompileProjectShotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"摄影机对准", "站位：", "（P", "接下一镜", "本节核心观看体验", "打斗约束"} {
		if !strings.Contains(compiled.Result.Prompt, want) {
			t.Fatalf("武戏编译稿缺「%s」：%s", want, compiled.Result.Prompt)
		}
	}
	if plain, _ := svc.CompileProjectShot("user-1", project.ID, after[1].ShotID, CompileProjectShotRequest{}); strings.Contains(plain.Result.Prompt, "摄影机对准") {
		t.Fatal("文戏段保持原结构")
	}

	// 刷新：场次表按存下的戏类切；整章重跑：武戏节仍走武戏对话；只重跑别的节：武戏节原样保留。
	scenes, err := svc.DramaScenes("user-1", project.ID, unit.ID, nil)
	if err != nil || len(scenes.Scenes) != 2 || len(scenes.Scenes[1].Sections) != 3 || scenes.Scenes[1].Sections[1].Genre != drama.ShotGenreWuxi {
		t.Fatalf("场次表按节显示武戏：%v %+v", err, scenes.Scenes)
	}
	upstream.calls, upstream.prompts, upstream.sections, upstream.histories = 0, nil, nil, nil
	whole := queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if ws := taskDramaShots(whole.InputJSON); len(ws.Sections) != 4 || ws.Sections[2].Genre != drama.ShotGenreWuxi || ws.Lanes[drama.ShotGenreWuxi] == "" {
		t.Fatalf("整章重跑按存下的戏类切：%+v", ws.Sections)
	}
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("整章重跑：%v %q", err, done.Error)
	}
	if upstream.calls != 4 || !strings.Contains(upstream.prompts[2], `skill-id="yz-shots-wuxi-live"`) || upstream.histories[2] != 0 || upstream.histories[1] != 2 {
		t.Fatalf("整章重跑两条对话：%d %v", upstream.calls, upstream.histories)
	}
	if got := sectionGenresOf(t, svc, project, unit); !slices.Equal(got, genres) {
		t.Fatalf("整章重跑后仍是武戏：%v", got)
	}
	plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run, err := svc.onlyScenesRun(project.ID, unit.ID, plan, newDramaRunMeta(plan, unit.ID), nil, []string{"E01S01-P1"}); err != nil || !slices.Equal(run.OnlySections, []string{"E01S01-P1"}) {
		t.Fatalf("只重跑第 1 节，武戏节原样保留：%v %+v", err, run.OnlySections)
	}

	// 改回文戏：点哪一个武戏节都把这一串一起改回，得到原来的 1 节文戏 30，第二个武戏节的段删掉。
	wuxiSegments, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	upstream.calls, upstream.prompts, upstream.sections, upstream.histories = 0, nil, nil, nil
	_, back := queueRegenreTask(t, svc, db, project, unit, server.URL, "E01S02-P3", "wenxi")
	if back.Genre != drama.ShotGenreWenxi30 || !slices.Equal(back.Sections, []string{"E01S02-P2"}) {
		t.Fatalf("改回文戏得 1 节文戏 30：%+v", back)
	}
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("改回文戏：%v %q", err, done.Error)
	}
	if upstream.calls != 1 || strings.Contains(upstream.prompts[0], "yz-shots-wuxi") || upstream.histories[0] != 4 {
		t.Fatalf("只写回 1 节、接着文戏对话：%d %v", upstream.calls, upstream.histories)
	}
	if got := sectionGenresOf(t, svc, project, unit); !slices.Equal(got, []string{"E01S01-P1/wenxi30/[2 11]", "E01S02-P1/wenxi30/[13 27]", "E01S02-P2/wenxi30/[28 42]"}) {
		t.Fatalf("改回文戏后：%v", got)
	}
	final, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	if len(final) != 3 || final[2].ShotID != wuxiSegments[2].ShotID {
		t.Fatalf("改回文戏装成 3 段，原第一个武戏段换回文戏：%v", segmentSections(final))
	}
	var leftover int64
	db.Model(&model.Shot{}).Where("id = ?", wuxiSegments[3].ShotID).Count(&leftover)
	if leftover != 0 {
		t.Fatal("第二个武戏节的段不残留")
	}
}

// Seedance 2.0（15 秒）项目：默认文戏 15；改武戏后每个武戏节不超过 15 秒、没有文戏 30。
func TestSectionGenreChangeOn15Seconds(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	project = qichuangOnModel(t, svc, db, project, "seedance-2.0")
	upstream := &sectionShotsUpstream{}
	server := httptest.NewServer(upstream)
	defer server.Close()
	queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("整章文戏 15：%v %q", err, done.Error)
	}
	_, regenre := queueRegenreTask(t, svc, db, project, unit, server.URL, "E01S02-P5", "wuxi")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("改武戏：%v %q", err, done.Error)
	}
	view, _ := svc.DramaShotlist("user-1", project.ID, unit.ID)
	wuxi := 0
	for _, s := range view.Sections {
		if s.Genre == drama.ShotGenreWenxi30 {
			t.Fatalf("15 秒项目不用文戏 30：%s", s.ID)
		}
		if s.Genre == drama.ShotGenreWuxi {
			wuxi++
			if s.Est > 15 {
				t.Fatalf("武戏节 %s %.0f 秒超过 15", s.ID, s.Est)
			}
		}
	}
	if wuxi != len(regenre.Sections) || wuxi == 0 {
		t.Fatalf("武戏节：%d %v", wuxi, regenre.Sections)
	}
	segments, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	for _, seg := range segments {
		if seg.Spec.Dur > 15 {
			t.Fatalf("段 %s %.0f 秒超过 15", seg.Spec.ID, seg.Spec.Dur)
		}
	}
}

// 场次色卡收尾写 bible_json 只换 color_script：提取资产在色卡收尾读快照之后回填了 scene_assets，色卡写库时两边都保留。
func TestScenePaletteBiblePatchKeepsConcurrentKeys(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	entries := seedQichuangStyle(t, svc, project, unit)
	snapshot, err := svc.repo.ProjectForUser("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 色卡收尾开始之后，提取资产按条件写回了场景回填。
	if err := svc.repo.ApplyDramaAssetsExtract("user-1", project.ID, nil, func(bible string) (string, error) {
		doc := map[string]any{}
		_ = json.Unmarshal([]byte(bible), &doc)
		doc["scene_assets"] = map[string]any{"E01S01": map[string]any{"unit_id": unit.ID, "environments": []string{"env-1"}}}
		encoded, err := json.Marshal(doc)
		return string(encoded), err
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rewritten := slices.Clone(entries)
	rewritten[0].Purpose = "重写后的色彩用途"
	patch := func(bible string) (string, error) { return mergeColorScriptDocument(bible, nil, rewritten) }
	if _, err := patch(snapshot.BibleJSON); err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.ApplyDramaScenePalettes("user-1", project.ID, patch, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	fresh, _ := svc.repo.ProjectForUser("user-1", project.ID)
	var doc struct {
		SceneAssets map[string]any           `json:"scene_assets"`
		ColorScript []drama.ColorScriptEntry `json:"color_script"`
		StyleAnchor map[string]any           `json:"style_anchor"`
	}
	if err := json.Unmarshal([]byte(fresh.BibleJSON), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SceneAssets["E01S01"] == nil || len(doc.ColorScript) != len(entries) || doc.ColorScript[0].Purpose != "重写后的色彩用途" || doc.StyleAnchor["medium"] == nil {
		t.Fatalf("两边的写都要保留：%s", fresh.BibleJSON)
	}
}
