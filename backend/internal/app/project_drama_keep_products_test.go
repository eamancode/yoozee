package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// discardDetails 读「会删掉已有产物、要确认」的拒绝里列出的段。
func discardDetails(t *testing.T, err error) []DramaSegmentDiscard {
	t.Helper()
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Reason != ReasonDramaSegmentsDiscard || appErr.Status != http.StatusConflict {
		t.Fatalf("应拒绝并要求确认（%s）：%v", ReasonDramaSegmentsDiscard, err)
	}
	encoded, _ := json.Marshal(appErr.Details["discards"])
	var out []DramaSegmentDiscard
	_ = json.Unmarshal(encoded, &out)
	return out
}

func shotlistOutcome(t *testing.T, task *model.Task) DramaStepTaskOutcome {
	t.Helper()
	var result struct {
		DramaStep DramaStepTaskOutcome `json:"dramaStep"`
	}
	_ = json.Unmarshal([]byte(task.ResultJSON), &result)
	return result.DramaStep
}

// M8 验收 A 的路径（七床第一集，第 1 节漏写一句台词 → R-SHOT-001）：
//   - 带问题装段后改武戏：上次已接受的老问题（第 1 节没重写）沿用已接受，章节不回草稿、照常保留装段，S01 分镜图仍是 ready；
//   - 改回文戏时新写的节又漏台词：新问题照常拦成草稿，段不动；
//   - 草稿上「先装段」走保留装段：没变的段原 ID、产物都在；多出来的武戏段要删、它有分镜图 → 先拒绝并列出，确认后才删；
//   - 装出来的段是这次被改的节换上新内容，不整章换新 ID。
func TestAcceptedIssuesSurvivePartialRerunAndRepackKeepsProducts(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	upstream := &sectionShotsUpstream{drop: map[string]bool{"E01S01L02": true}}
	server := httptest.NewServer(upstream)
	defer server.Close()
	queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded || shotlistOutcome(t, done).Status != DramaStepOutcomeDraft {
		t.Fatalf("整章生成第 1 节漏台词、存草稿：%v", err)
	}
	accepted, err := svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{AcceptIssues: true})
	if err != nil || !accepted.Accepted || accepted.Check.Errors != 1 || len(accepted.Segments) != 3 {
		t.Fatalf("带问题装段：%v %+v", err, accepted.Check)
	}
	before, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	art0 := addStoryboardArtifact(t, svc, db, project.ID, before[0])
	art1 := addStoryboardArtifact(t, svc, db, project.ID, before[1])

	// 改武戏：只重写 E01S02 的节；E01S01-P1 的老问题是已接受过的 → 不回草稿。
	queueRegenreTask(t, svc, db, project, unit, server.URL, "E01S02-P2", "wuxi")
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("改武戏：%v %q", err, done.Error)
	}
	if outcome := shotlistOutcome(t, done); outcome.Status != DramaStepOutcomeWritten || outcome.Errors != 1 || outcome.Segments != 4 {
		t.Fatalf("老问题沿用已接受，改武戏照常写入：%+v", outcome)
	}
	view, _ := svc.DramaShotlist("user-1", project.ID, unit.ID)
	if view.Draft || !view.Accepted || len(view.Issues) != 1 || view.Issues[0].Section != "E01S01-P1" {
		t.Fatalf("镜头表仍是已接受、问题还挂在第 1 节：draft=%v accepted=%v %+v", view.Draft, view.Accepted, view.Issues)
	}
	wuxi, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	if len(wuxi) != 4 || wuxi[0].ShotID != before[0].ShotID || wuxi[1].ShotID != before[1].ShotID || wuxi[2].ShotID != before[2].ShotID {
		t.Fatalf("保留装段：%v", segmentSections(wuxi))
	}
	if artifactStatus(t, db, art0) != "ready" || artifactStatus(t, db, art1) != "ready" {
		t.Fatalf("没变的段分镜图不动：%s %s", artifactStatus(t, db, art0), artifactStatus(t, db, art1))
	}
	art3 := addStoryboardArtifact(t, svc, db, project.ID, wuxi[3])

	// 改回文戏时新写的这一节漏了一句台词：新问题照常拦成草稿，段一个不动。
	var newLine string
	for _, sec := range view.Sections {
		if sec.ID == "E01S02-P2" && len(sec.LineIDs) > 0 {
			newLine = sec.LineIDs[0]
		}
	}
	if newLine == "" {
		t.Fatal("E01S02-P2 要有台词")
	}
	upstream.drop = map[string]bool{"E01S01L02": true, newLine: true}
	queueRegenreTask(t, svc, db, project, unit, server.URL, "E01S02-P3", "wenxi")
	done, err = runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded || shotlistOutcome(t, done).Status != DramaStepOutcomeDraft || shotlistOutcome(t, done).Errors != 2 {
		t.Fatalf("新问题照常拦成草稿：%v %+v", err, shotlistOutcome(t, done))
	}
	still, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	if len(still) != 4 || still[3].ShotID != wuxi[3].ShotID {
		t.Fatalf("草稿不动段：%v", segmentSections(still))
	}

	// 草稿上「先装段」：保留装段，多出来的武戏段（有分镜图）要删 → 先拒绝、列出会丢的产物。
	_, err = svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{AcceptIssues: true})
	discards := discardDetails(t, err)
	if len(discards) != 1 || discards[0].ShotID != wuxi[3].ShotID || discards[0].Section != "E01S02-P3" || discards[0].Artifacts[drama.ArtifactStoryboard] != 1 {
		t.Fatalf("只列多出来的那一段和它的分镜图：%+v", discards)
	}
	if artifactStatus(t, db, art3) != "ready" {
		t.Fatal("没确认时什么都不删")
	}
	repacked, err := svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{AcceptIssues: true, Discard: true})
	if err != nil || !repacked.Accepted || repacked.Check.Errors != 2 {
		t.Fatalf("确认后带问题装段：%v %+v", err, repacked.Check)
	}
	after, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	if !slices.Equal(segmentSections(after), []string{"E01S01-P1/wenxi30", "E01S02-P1/wenxi30", "E01S02-P2/wenxi30"}) {
		t.Fatalf("装成三节文戏 30：%v", segmentSections(after))
	}
	if after[0].ShotID != before[0].ShotID || after[1].ShotID != before[1].ShotID || after[2].ShotID != wuxi[2].ShotID {
		t.Fatal("先装段不整章换新 ID：没变的段、被改的节原来的段都沿用")
	}
	if artifactStatus(t, db, art0) != "ready" || artifactStatus(t, db, art1) != "ready" || artifactStatus(t, db, art3) != "deleted" {
		t.Fatalf("没变的段产物都在，确认过的那一段删掉：%s %s %s", artifactStatus(t, db, art0), artifactStatus(t, db, art1), artifactStatus(t, db, art3))
	}
}

// 整章替换（不是按小节生成的旧镜头表重新装段、整章重新生成）会删掉本章全部产物：没带确认就拒绝并按段列出；
// 带了才做。没有产物时不用确认。
func TestWholeChapterReplaceRequiresDiscardConfirm(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	upstream := &sectionShotsUpstream{}
	server := httptest.NewServer(upstream)
	defer server.Close()
	queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("整章生成：%v", err)
	}
	if _, err := svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{}); err != nil {
		t.Fatalf("没有产物时重新装段不用确认：%v", err)
	}
	segments, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	art := addStoryboardArtifact(t, svc, db, project.ID, segments[1])
	// 按小节的镜头表重新装段走保留装段：内容没变，不删产物、不用确认。
	if _, err := svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{}); err != nil || artifactStatus(t, db, art) != "ready" {
		t.Fatalf("按小节重新装段保留产物：%v %s", err, artifactStatus(t, db, art))
	}
	kept, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	if kept[1].ShotID != segments[1].ShotID {
		t.Fatal("重新装段不换段 ID")
	}
	// 整章重新生成：建任务前就要确认。
	runReq := DramaStepRunRequest{Model: "fake-shots", Config: map[string]any{"channelId": "ch-text", "model": "fake-shots", "textModel": "fake-shots"}}
	_, err := svc.RunDramaStep("user-1", project.ID, unit.ID, drama.StepChapterShots, runReq)
	if discards := discardDetails(t, err); len(discards) != 1 || discards[0].ShotID != segments[1].ShotID {
		t.Fatalf("整章重新生成列出有产物的段：%+v", discards)
	}
	runReq.Discard = true
	if _, err := svc.RunDramaStep("user-1", project.ID, unit.ID, drama.StepChapterShots, runReq); err != nil && strings.Contains(err.Error(), "确认") {
		t.Fatalf("确认后不再拦：%v", err)
	}
	// 旧镜头表（没有小节原样输出）只能整章替换。
	if err := db.Model(&model.DramaShotlist{}).Where("unit_id = ?", unit.ID).Update("sections_json", "").Error; err != nil {
		t.Fatal(err)
	}
	_, err = svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{})
	if discards := discardDetails(t, err); len(discards) != 1 || discards[0].Artifacts[drama.ArtifactStoryboard] != 1 || discards[0].Label == "" {
		t.Fatalf("整章替换列出会丢的产物：%+v", discards)
	}
	if artifactStatus(t, db, art) != "ready" {
		t.Fatal("没确认时不删")
	}
	if _, err := svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{Discard: true}); err != nil || artifactStatus(t, db, art) != "deleted" {
		t.Fatalf("确认后整章替换：%v %s", err, artifactStatus(t, db, art))
	}
}

// 部分重跑中途失败（M8 验收 B 改回文戏第 3 节失败）：「从第 N 节继续」只调没写完的节，结果与一次跑完一致、照常保留装段；
// 作底的镜头表在失败之后又改过就拒绝续写（免得拿旧的不重写节覆盖新内容）。
func TestOnlySectionsRunResumesFromFailedSection(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	failing := ""
	upstream := &sectionShotsUpstream{fail: func(call int, section string) int {
		if section == failing {
			return http.StatusServiceUnavailable
		}
		return 0
	}}
	server := httptest.NewServer(upstream)
	defer server.Close()
	queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("整章：%v", err)
	}
	before, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	art0 := addStoryboardArtifact(t, svc, db, project.ID, before[0])

	plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.onlyScenesRun(project.ID, unit.ID, plan, newDramaRunMeta(plan, unit.ID), []string{"E01S02"}, nil)
	if err != nil || !slices.Equal(run.OnlySections, []string{"E01S02-P1", "E01S02-P2"}) {
		t.Fatalf("重写 E01S02：%v %+v", err, run.OnlySections)
	}
	req := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": server.URL, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	req.Input["metadata"].(map[string]any)["dramaRun"] = run
	insertDramaTestTask(t, svc, db, project.ID, req)
	failing = "E01S02-P2"
	failed, err := runQueuedTask(t, svc)
	if err == nil || failed.Status != model.TaskStatusFailed || !strings.Contains(failed.Error, "可从第 3 节继续") {
		t.Fatalf("第 3 节失败：%v %q", err, failed.Error)
	}

	failing = ""
	upstream.calls, upstream.sections = 0, nil
	queueDramaStepTask(t, svc, db, project, unit, server.URL, failed.ID)
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("从第 3 节继续：%v %q", err, done.Error)
	}
	if upstream.calls != 1 || !slices.Equal(upstream.sections, []string{"E01S02-P2"}) {
		t.Fatalf("续写只调没写完的那一节：%d %v", upstream.calls, upstream.sections)
	}
	after, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	if len(after) != 3 || after[0].ShotID != before[0].ShotID || artifactStatus(t, db, art0) != "ready" {
		t.Fatalf("续写收尾照常保留装段：%v %s", segmentSections(after), artifactStatus(t, db, art0))
	}

	// 再来一次失败，之后镜头表又改过（重新装段加了一版）：不能拿旧的不重写节接着写。
	insertDramaTestTask(t, svc, db, project.ID, func() CreateTaskRequest {
		plan, _ := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, nil)
		run, _ := svc.onlyScenesRun(project.ID, unit.ID, plan, newDramaRunMeta(plan, unit.ID), []string{"E01S02"}, nil)
		req := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": server.URL, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
		req.Input["metadata"].(map[string]any)["dramaRun"] = run
		return req
	}())
	failing = "E01S02-P2"
	failed, _ = runQueuedTask(t, svc)
	if failed.Status != model.TaskStatusFailed {
		t.Fatalf("第二次失败：%s", failed.Status)
	}
	if _, err := svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{Discard: true}); err != nil {
		t.Fatal(err)
	}
	plan, _ = svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, nil)
	if _, err := svc.resumeDramaRun("user-1", project.ID, unit.ID, plan, failed.ID); err == nil || !strings.Contains(err.Error(), "又改过") {
		t.Fatalf("镜头表改过就不能续写：%v", err)
	}
}

// 改戏类改在场中间（M8 验收本机复现：E01S02-P1 改武戏切成两节，后面的节号往后挪）、之后改回文戏又出新问题落草稿：草稿的各节和现有的段
// 节号对不上。「先装段」按现有段装出时的那一版各节（packed_sections_json）对：内容没变的后一节原段、产物都在，只有多出来的武戏段要删。
func TestRepackFromDraftMatchesPackedSections(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedQichuang(t, svc, db)
	upstream := &sectionShotsUpstream{}
	server := httptest.NewServer(upstream)
	defer server.Close()
	queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("整章：%v", err)
	}
	_, regenre := queueRegenreTask(t, svc, db, project, unit, server.URL, "E01S02-P1", "wuxi")
	if done, err := runQueuedTask(t, svc); err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("改武戏：%v", err)
	}
	if len(regenre.Sections) < 2 {
		t.Fatalf("夹具前提：E01S02-P1 改武戏要切成至少两节：%v", regenre.Sections)
	}
	wuxi, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	last := wuxi[len(wuxi)-1]
	if last.Spec.Genre != drama.ShotGenreWenxi30 {
		t.Fatalf("夹具前提：最后一节还是原来的文戏：%v", segmentSections(wuxi))
	}
	keepArt := addStoryboardArtifact(t, svc, db, project.ID, last)
	extra := wuxi[len(wuxi)-2]
	dropArt := addStoryboardArtifact(t, svc, db, project.ID, extra)

	// 改回文戏时新写的节漏了一句台词：落草稿，草稿的各节比现有的段少一节、后面的节号往前挪。
	view, _ := svc.DramaShotlist("user-1", project.ID, unit.ID)
	upstream.drop = map[string]bool{view.Sections[1].LineIDs[0]: true}
	queueRegenreTask(t, svc, db, project, unit, server.URL, extra.Spec.Section, "wenxi")
	if done, err := runQueuedTask(t, svc); err != nil || shotlistOutcome(t, done).Status != DramaStepOutcomeDraft {
		t.Fatalf("新问题落草稿：%v %+v", err, shotlistOutcome(t, done))
	}
	_, err := svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{AcceptIssues: true})
	discards := discardDetails(t, err)
	if len(discards) != 1 || discards[0].ShotID != extra.ShotID {
		t.Fatalf("只删多出来的武戏段（%s），不删内容没变的最后一节：%+v", extra.ShotID, discards)
	}
	if _, err := svc.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{AcceptIssues: true, Discard: true}); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.DramaSegments("user-1", project.ID, unit.ID)
	if after[len(after)-1].ShotID != last.ShotID || artifactStatus(t, db, keepArt) != "ready" || artifactStatus(t, db, dropArt) != "deleted" {
		t.Fatalf("最后一节原段、分镜图都在：%v %s %s", segmentSections(after), artifactStatus(t, db, keepArt), artifactStatus(t, db, dropArt))
	}
}
