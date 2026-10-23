package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func TestAdminDramaEvalComparesCompileProfilesOnStoredShotlist(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.5")
	seedOnlineQichuangAssets(t, db, project, unit)
	admin := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	if _, err := service.AdminDramaEval(admin, DramaEvalRequest{ProjectID: project.ID}); err == nil {
		t.Fatal("没有镜头表的项目不能评测视频提示词阶段")
	}
	written, err := service.WriteDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: loadOnlineRowsRaw(t)})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := service.CreateDramaCompileProfile(admin, DramaCompileProfileRequest{Name: "灯光写法", Note: "灯光七项前加「灯光：」", Content: json.RawMessage(`{"shot": {"light_full": "灯光：{text}。"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdminDramaEval(&model.User{ID: "user-1", Role: model.UserRoleUser}, DramaEvalRequest{ProjectID: project.ID, CandidateVersion: candidate.Version}); err == nil {
		t.Fatal("非管理员不能评测")
	}

	run, err := service.AdminDramaEval(admin, DramaEvalRequest{ProjectID: project.ID, CandidateVersion: candidate.Version})
	if err != nil {
		t.Fatal(err)
	}
	if run.Baseline.Version != 0 || run.Candidate.Version != candidate.Version || len(run.Rows) != 2 {
		t.Fatalf("基线默认取生效档案（v0），候选 v%d：%+v", candidate.Version, run.Rows)
	}
	base, cand := run.Rows[0], run.Rows[1]
	if base.Source != "rows" || base.Shots != 29 || base.Errors != 0 || base.SpecRate != 1 || base.Segments != len(written.Segments) || base.TotalSeconds == 0 || base.Chars == 0 {
		t.Fatalf("基线指标：%+v", base)
	}
	if cand.Segments != base.Segments || cand.Chars <= base.Chars {
		t.Fatalf("候选只多了「灯光：」前缀，段数不变、字数变多：%+v vs %+v", cand, base)
	}
	if len(run.Segments) != base.Segments || !strings.Contains(run.Segments[0].Candidate, "灯光：") || strings.Contains(run.Segments[0].Baseline, "灯光：") {
		t.Fatalf("逐段提示词对比：%+v", run.Segments[0].Segment)
	}
	if !strings.Contains(run.Markdown, "| 第一章 | v0 内置默认 | 分镜行原文 | 0 / ") || !strings.Contains(run.Markdown, "候选与基线提示词不同的段") {
		t.Fatalf("Markdown 指标表：\n%s", run.Markdown)
	}
	// 评测只读：镜头表版本、段都不动。
	view, _ := service.DramaShotlist("user-1", project.ID, unit.ID)
	if view.Version != written.Shotlist.Version || view.CompileProfile.Version != 0 {
		t.Fatalf("评测不应改镜头表：%+v", view)
	}
	runs, err := service.AdminDramaEvalRuns(admin)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID || len(runs[0].Rows) != 2 {
		t.Fatalf("评测记录：%v %+v", err, runs)
	}

	// 老镜头表（没有分镜行原文）：用镜头表 + 段参考反推，段数与参考与原文重放一致。
	if err := db.Model(&model.DramaShotlist{}).Where("unit_id = ?", unit.ID).Update("rows_json", "").Error; err != nil {
		t.Fatal(err)
	}
	legacy, err := service.AdminDramaEval(admin, DramaEvalRequest{ProjectID: project.ID, UnitIDs: []string{unit.ID}, CandidateVersion: candidate.Version})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Rows[0].Source != "shotlist" || legacy.Rows[0].SpecRate != -1 || legacy.Rows[0].Segments != base.Segments {
		t.Fatalf("镜头表反推：%+v", legacy.Rows[0])
	}
	for i, seg := range legacy.Segments {
		if seg.Baseline != run.Segments[i].Baseline {
			t.Fatalf("%s 反推的提示词与原文重放不一致", seg.Segment)
		}
	}
	if _, err := service.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{}); err == nil {
		t.Fatal("没有分镜行原文的镜头表不能直接重装")
	}
}

func TestRepackDramaShotlistUsesActiveCompileProfile(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.5")
	seedOnlineQichuangAssets(t, db, project, unit)
	admin := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	written, err := service.WriteDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistWriteRequest{Rows: loadOnlineRowsRaw(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateDramaCompileProfile(admin, DramaCompileProfileRequest{Name: "主光源", Note: "改灯光标签", Content: json.RawMessage(`{"light": {"key": "主光源"}}`), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	repacked, err := service.RepackDramaShotlist("user-1", project.ID, unit.ID, DramaShotlistRepackRequest{Rule: &DramaShotlistRule{MaxSeconds: 15}})
	if err != nil {
		t.Fatal(err)
	}
	if repacked.Shotlist.Version != written.Shotlist.Version+1 || repacked.Shotlist.CompileProfile.Version != 1 || !strings.Contains(segmentsText(t, repacked.Segments), "主光源：") {
		t.Fatalf("重装段用生效档案：%+v", repacked.Shotlist.CompileProfile)
	}
	for _, seg := range repacked.Segments {
		if seg.Spec.Dur > 15 {
			t.Fatalf("渠道段长规则只收紧：%s %v 秒", seg.Spec.ID, seg.Spec.Dur)
		}
	}
	if _, err := service.RepackDramaShotlist("someone-else", project.ID, unit.ID, DramaShotlistRepackRequest{}); err == nil {
		t.Fatal("别人的项目不能重装")
	}
}

// 离线 CLI 用的同一个函数：线上实测 on3（七床第一章）在 2.5 上 6 段、2.0 上 10 段，与 pack.py 一致。
func TestEvalDramaCompileOnOnlineFixture(t *testing.T) {
	const dir = "../drama/testdata/online-qichuang-on3"
	var doc struct {
		Rows []json.RawMessage `json:"rows"`
	}
	raw, err := os.ReadFile(dir + "/rows.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var stub []struct {
		ID       string              `json:"id"`
		Title    string              `json:"title"`
		Category model.AssetCategory `json:"category"`
	}
	raw, _ = os.ReadFile(dir + "/assets.json")
	if err := json.Unmarshal(raw, &stub); err != nil {
		t.Fatal(err)
	}
	assets := make([]ProjectAssetSummary, 0, len(stub))
	for _, a := range stub {
		assets = append(assets, ProjectAssetSummary{ID: a.ID, Title: a.Title, Category: a.Category, PrimaryVersionID: a.ID + "-v1"})
	}
	chapter, _ := os.ReadFile(onlineQichuangDir + "/chapter.md")
	def, _ := drama.DefaultCompileProfile()
	for model, want := range map[string]int{"seedance-2.5": 6, "seedance-2.0": 10} {
		m, _ := drama.ModelByID(model)
		run, err := EvalDramaCompile([]DramaEvalInput{{Label: "七床·第一章", Rows: doc.Rows, ChapterText: string(chapter), Title: "七床", Assets: assets}}, m, def, def)
		if err != nil {
			t.Fatal(err)
		}
		if r := run.Rows[0]; r.Shots != 41 || r.Errors != 0 || r.Warnings != 15 || r.Segments != want || r.OverLimit != 0 {
			t.Fatalf("%s：%+v", model, r)
		}
		if strings.Contains(run.Markdown, "候选与基线提示词不同的段：0 / ") == false {
			t.Fatalf("同一档案对比应没有差异：\n%s", run.Markdown)
		}
	}
}

// 老镜头表（没有分镜行原文）的反推与原文重放逐段一致，包括 #11 的情形：没图的人物写外貌声明、没图的场景写进镜头描述、只有部分资产有图。
func TestEvalStoredShotlistMatchesRowsReplayWithImagelessAssets(t *testing.T) {
	rows, assets, script := loadOn3(t)
	raw, err := os.ReadFile("../drama/testdata/online-qichuang-on3/rows.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rows []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	all := map[string]bool{}
	for _, a := range assets {
		all[a.ID] = true
	}
	def, _ := drama.DefaultCompileProfile()
	for name, imaged := range map[string]map[string]bool{"none": {}, "lin": {"char-1": true}, "all": all} {
		for _, model := range []string{"seedance-2.5", "seedance-2.0", "minimax-h3"} {
			m, _ := drama.ModelByID(model)
			stored, err := planDramaShotlist(rows, &script, assets, imaged, m, def)
			if err != nil {
				t.Fatal(err)
			}
			chapter, _ := os.ReadFile(onlineQichuangDir + "/chapter.md")
			replay, err := EvalDramaCompile([]DramaEvalInput{{Label: "on3", Rows: doc.Rows, ChapterText: string(chapter), Title: "七床", Assets: assets, Imaged: imaged}}, m, def, def)
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := EvalDramaCompile([]DramaEvalInput{{Label: "on3", Shotlist: &stored.Shotlist, Segments: stored.Segments, ChapterText: string(chapter), Title: "七床", Assets: assets, Imaged: imaged}}, m, def, def)
			if err != nil {
				t.Fatal(err)
			}
			if legacy.Rows[0].Source != "shotlist" || len(legacy.Segments) != len(replay.Segments) {
				t.Fatalf("%s/%s：段数 %d vs %d", name, model, len(legacy.Segments), len(replay.Segments))
			}
			for i, seg := range legacy.Segments {
				if seg.Baseline != replay.Segments[i].Baseline {
					t.Fatalf("%s/%s %s 反推的提示词与原文重放不一致：\n%s\n----\n%s", name, model, seg.Segment, seg.Baseline, replay.Segments[i].Baseline)
				}
				if name == "none" && strings.Contains(seg.Baseline, "@图片") {
					t.Fatalf("%s/%s %s 没图的资产不该编译成 @图片", name, model, seg.Segment)
				}
			}
		}
	}
}
