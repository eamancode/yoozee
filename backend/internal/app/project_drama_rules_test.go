package app

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// 短剧 Agent 的工具清单：读工具在只读模式下也给，写工具只在允许写入时出现。
var (
	dramaReadTools  = []string{"drama_bible_read", "drama_brief_read", "drama_chapters_read", "drama_compile_prompt", "drama_progress_read", "drama_segments_read", "drama_shotlist_read"}
	dramaWriteTools = []string{
		"drama_artifact_register", "drama_bible_write", "drama_brief_write", "drama_delivery_package",
		"drama_evidence_write", "drama_gate_check", "drama_qa_frames", "drama_qa_transcribe",
		"drama_segments_write", "drama_shotlist_write", "drama_take_pick",
	}
)

func cloudAgentToolNames(req CloudAgentRequest) []string {
	names := make([]string, 0, 64)
	for _, tool := range cloudAgentTools(req) {
		function, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		if name, ok := function["name"].(string); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func dramaToolNames(req CloudAgentRequest) []string {
	names := make([]string, 0, len(dramaReadTools)+len(dramaWriteTools))
	for _, name := range cloudAgentToolNames(req) {
		if strings.HasPrefix(name, "drama_") {
			names = append(names, name)
		}
	}
	return names
}

func TestDramaAgentToolsFollowPermissionMode(t *testing.T) {
	// 不属于短剧项目的画布：一个短剧工具都不给。
	plain := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}}
	if got := dramaToolNames(plain); len(got) != 0 {
		t.Fatalf("普通画布不该看到短剧工具：%v", got)
	}

	// 短剧项目画布：只读模式只给读工具（人判的抽帧、人脸不替人做）。
	project := plain
	project.DramaProjectID = "project-1"
	project.PermissionMode = "read_only"
	readOnly := dramaToolNames(project)
	wantRead := append([]string{}, dramaReadTools...)
	sort.Strings(wantRead)
	if strings.Join(readOnly, ",") != strings.Join(wantRead, ",") {
		t.Fatalf("只读模式应只给读工具：%v", readOnly)
	}
	for _, name := range dramaWriteTools {
		if cloudAgentToolAllowed(project, name) {
			t.Fatalf("只读模式不该出现写工具 %s", name)
		}
	}

	// 允许写入的两种档位：读 + 写一共 16 个。
	for _, mode := range []string{"request_approval", "auto"} {
		withWrite := project
		withWrite.PermissionMode = mode
		got := dramaToolNames(withWrite)
		if len(got) != len(dramaReadTools)+len(dramaWriteTools) {
			t.Fatalf("%s 档位应给全部短剧工具，得到 %d 个：%v", mode, len(got), got)
		}
		for _, name := range append(append([]string{}, dramaReadTools...), dramaWriteTools...) {
			if !cloudAgentToolAllowed(withWrite, name) {
				t.Fatalf("%s 档位缺工具 %s", mode, name)
			}
		}
	}
}

func TestProjectVideoModelFamilyMustMatchGenerationModel(t *testing.T) {
	service, _, project, _ := newDramaTestService(t, "seedance-2.0")

	// 建项目：生成模型是 Seedance 2.0，默认视频模型却选了 H3 族的渠道模型 → 拒绝。
	if _, err := service.CreateProject("user-1", CreateProjectRequest{
		Name: "夜班", Type: "short-drama", GenerationModel: "seedance-2.0", DefaultVideoModel: "MiniMax-H3",
	}); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("跨族的默认视频模型应被拒绝，得到 %v", err)
	}
	// 同族放行。
	created, err := service.CreateProject("user-1", CreateProjectRequest{
		Name: "夜班（同族）", Type: "short-drama", GenerationModel: "seedance-2.0", DefaultVideoModel: "doubao-seedance-2-0-260128",
	})
	if err != nil {
		t.Fatalf("同族模型应放行：%v", err)
	}
	if created.GenerationModel != "seedance-2.0" || created.AuthMode != drama.AuthStep {
		t.Fatalf("生成模型与默认授权档位没落库：%+v", created)
	}
	// 不认识的渠道模型名不拦（不能因为命名规则挡住没见过的供应商）。
	if _, err := service.CreateProject("user-1", CreateProjectRequest{
		Name: "别的", Type: "short-drama", GenerationModel: "seedance-2.0", DefaultVideoModel: "kling-v2-master",
	}); err != nil {
		t.Fatalf("不认识的模型名应放行：%v", err)
	}
	// 不填默认视频模型也不拦。
	if _, err := service.CreateProject("user-1", CreateProjectRequest{Name: "只选生成模型", Type: "short-drama", GenerationModel: "seedance-2.5"}); err != nil {
		t.Fatalf("没填默认视频模型应放行：%v", err)
	}
	// 未知生成模型直接拒绝。
	if _, err := service.CreateProject("user-1", CreateProjectRequest{Name: "未知模型", Type: "short-drama", GenerationModel: "kling"}); err == nil {
		t.Fatal("未知生成模型应被拒绝")
	}

	// 改项目：把生成模型改小到 H3，而已有的默认视频模型属于 Seedance 族 → 拒绝。
	seedance := "doubao-seedance-2-0-260128"
	if _, err := service.UpdateProject("user-1", project.ID, UpdateProjectRequest{DefaultVideoModel: &seedance}); err != nil {
		t.Fatalf("同族默认视频模型应保存成功：%v", err)
	}
	h3 := "minimax-h3"
	if _, err := service.UpdateProject("user-1", project.ID, UpdateProjectRequest{GenerationModel: &h3}); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("改生成模型后与已有默认视频模型冲突应被拒绝，得到 %v", err)
	}
	// 两个一起改成同一族则放行。
	updated, err := service.UpdateProject("user-1", project.ID, UpdateProjectRequest{GenerationModel: &h3, DefaultVideoModel: &h3})
	if err != nil {
		t.Fatalf("同时改成同族应放行：%v", err)
	}
	if updated.GenerationModel != "minimax-h3" {
		t.Fatalf("生成模型没更新：%+v", updated)
	}
	// 清空默认视频模型永远允许。
	empty := ""
	if _, err := service.UpdateProject("user-1", project.ID, UpdateProjectRequest{DefaultVideoModel: &empty}); err != nil {
		t.Fatalf("清空默认视频模型应放行：%v", err)
	}
}

func TestProjectAuthModeOnlyAcceptsTwoTiers(t *testing.T) {
	service, _, project, _ := newDramaTestService(t, "seedance-2.0")

	// 空值按每步确认落库。
	created, err := service.CreateProject("user-1", CreateProjectRequest{Name: "默认档位", Type: "short-drama"})
	if err != nil {
		t.Fatal(err)
	}
	if created.AuthMode != drama.AuthStep {
		t.Fatalf("空授权档位应落成 step：%q", created.AuthMode)
	}
	// 两档都能存。
	full := "full"
	updated, err := service.UpdateProject("user-1", project.ID, UpdateProjectRequest{AuthMode: &full})
	if err != nil || updated.AuthMode != drama.AuthFull {
		t.Fatalf("full 档位应保存成功：%v %+v", err, updated)
	}
	step := "step"
	if updated, err = service.UpdateProject("user-1", project.ID, UpdateProjectRequest{AuthMode: &step}); err != nil || updated.AuthMode != drama.AuthStep {
		t.Fatalf("step 档位应保存成功：%v %+v", err, updated)
	}
	// 大小写和取值都要严格匹配，拼错的档位一律拒绝。
	for _, bad := range []string{"auto", "read_only", "全权", "STEP", "full/step", "full,step"} {
		value := bad
		if _, err := service.UpdateProject("user-1", project.ID, UpdateProjectRequest{AuthMode: &value}); err == nil {
			t.Fatalf("非法授权档位 %q 应被拒绝", bad)
		}
		if _, err := service.CreateProject("user-1", CreateProjectRequest{Name: "非法档位 " + bad, Type: "short-drama", AuthMode: bad}); err == nil {
			t.Fatalf("建项目时非法授权档位 %q 应被拒绝", bad)
		}
	}
	// 前后空白按归一化处理，不当成非法值（表单/接口传参常见）。
	for _, padded := range []string{" step", "step ", " full	", "	full "} {
		value := padded
		updated, err := service.UpdateProject("user-1", project.ID, UpdateProjectRequest{AuthMode: &value})
		if err != nil {
			t.Fatalf("带空白的档位 %q 应归一化后保存：%v", padded, err)
		}
		if updated.AuthMode != strings.TrimSpace(padded) {
			t.Fatalf("带空白的档位 %q 归一化结果 = %q", padded, updated.AuthMode)
		}
	}
}

func TestWriteDramaSegmentsRejectsOverLongBatchWithoutPartialWrites(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.0")

	// 一批三段，只有第三段超长（20 秒 > 15 秒）→ 整批拒绝，一段都不能落库。
	_, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{
		UnitID: unit.ID,
		Segments: []json.RawMessage{
			dramaSpec("S01", 8, "第一句"),
			dramaSpec("S02", 12, ""),
			dramaSpec("S03", 20, "第三句"),
		},
	})
	if appReason(err) != string(ReasonSegmentTooLong) {
		t.Fatalf("整批里有一段超长应整批拒绝，得到 %v", err)
	}
	var count int64
	if err := db.Model(&model.Shot{}).Where("project_id = ?", project.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("被拒的批次不能留下部分段，实际落了 %d 段", count)
	}

	// 把超长的那段改到上限内，整批就能落库。
	views, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{
		UnitID: unit.ID,
		Segments: []json.RawMessage{
			dramaSpec("S01", 8, "第一句"),
			dramaSpec("S02", 12, ""),
			dramaSpec("S03", 15, "第三句"),
		},
	})
	if err != nil {
		t.Fatalf("全部在限内应写入成功：%v", err)
	}
	if len(views) != 3 {
		t.Fatalf("应写入三段：%d", len(views))
	}

	// 段号不在契约里（不是 S01 这种）也拒绝。
	if _, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{
		UnitID:   unit.ID,
		Segments: []json.RawMessage{dramaSpec("shot-1", 8, "")},
	}); err == nil {
		t.Fatal("非法段号应被拒绝")
	}
}

func TestDramaProgressMarksOverLimitAfterShrinkingModel(t *testing.T) {
	// 项目先按 Seedance 2.5（30 秒）建，写一段 20 秒的段；再把生成模型改小到 2.0（15 秒）。
	service, _, project, unit := newDramaTestService(t, "seedance-2.5")
	if _, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{
		UnitID:   unit.ID,
		Segments: []json.RawMessage{dramaSpec("S01", 20, "第一句")},
	}); err != nil {
		t.Fatalf("Seedance 2.5 下 20 秒的段应能写入：%v", err)
	}
	before, err := service.DramaProgress("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Counts.OverLimit != 0 {
		t.Fatalf("还没改小模型时不该有超长段：%+v", before.Counts)
	}

	smaller := "seedance-2.0"
	if _, err := service.UpdateProject("user-1", project.ID, UpdateProjectRequest{GenerationModel: &smaller}); err != nil {
		t.Fatalf("把生成模型改小应成功（已有段不阻塞设置保存，只在概览标出）：%v", err)
	}
	after, err := service.DramaProgress("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Counts.OverLimit != 1 {
		t.Fatalf("改小模型后应标出 1 段超长：%+v", after.Counts)
	}
	if len(after.Segments) != 1 || !after.Segments[0].OverLimit {
		t.Fatalf("超长标记没落到段上：%+v", after.Segments)
	}
	if !strings.Contains(after.Line, "超过 Seedance 2.0 的 15 秒上限") {
		t.Fatalf("概览的一句话要点明超长：%q", after.Line)
	}
}

// 拆段、合段走同一个写入接口：给出本章段顺序，新段插在原段后面，被合并掉的段连同生成结果删除。
func TestWriteDramaSegmentsOrderSplitsAndMergesInPlace(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.5")
	views, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{
		UnitID:   unit.ID,
		Segments: []json.RawMessage{dramaSpec("S01", 8, "第一句"), dramaSpec("S02", 12, "第二句"), dramaSpec("S03", 6, "第三句")},
	})
	if err != nil {
		t.Fatal(err)
	}
	s02, s03 := views[1].ShotID, views[2].ShotID
	if err := db.Create(&model.ShotArtifact{ID: "artifact-s03", ProjectID: project.ID, UnitID: unit.ID, ShotID: s03, Type: "video", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}

	// 拆 S02：S02b 要排在 S02 后面、S03 前面，而不是追加到章末。
	views, err = service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{
		UnitID:   unit.ID,
		Segments: []json.RawMessage{dramaSpec("S02", 5, "第二句"), dramaSpec("S02b", 7, "")},
		Order:    []string{"S01", "S02", "S02b", "S03"},
	})
	if err != nil {
		t.Fatalf("拆段应成功：%v", err)
	}
	if got := segmentIDs(views); got != "S01,S02,S02b,S03" {
		t.Fatalf("拆出的段应插在原段后面，得到 %s", got)
	}
	if views[1].ShotID != s02 || views[1].Spec.Dur != 5 {
		t.Fatalf("原段应写新版本而不是新建：%+v", views[1])
	}

	// S02b 并回 S02：S02b 删除，其余段位置连续。
	views, err = service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{
		UnitID:   unit.ID,
		Segments: []json.RawMessage{dramaSpec("S02", 12, "第二句")},
		Order:    []string{"S01", "S02", "S03"},
	})
	if err != nil {
		t.Fatalf("合段应成功：%v", err)
	}
	if got := segmentIDs(views); got != "S01,S02,S03" {
		t.Fatalf("合段后应剩三段，得到 %s", got)
	}
	for index, view := range views {
		if view.Position != index {
			t.Fatalf("合段后位置应连续：%+v", views)
		}
	}

	// 顺序里漏掉的段会被删掉，连同它的生成结果。
	if _, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{
		UnitID:   unit.ID,
		Segments: []json.RawMessage{dramaSpec("S02", 18, "第二句")},
		Order:    []string{"S01", "S02"},
	}); err != nil {
		t.Fatalf("把 S03 并进 S02 应成功：%v", err)
	}
	var artifacts int64
	if err := db.Model(&model.ShotArtifact{}).Where("shot_id = ?", s03).Count(&artifacts).Error; err != nil {
		t.Fatal(err)
	}
	if artifacts != 0 {
		t.Fatalf("被合并掉的段的生成结果应一起删除，还剩 %d 个", artifacts)
	}

	// 顺序和写入对不上时整批拒绝，不落库。
	if _, err := service.WriteDramaSegments("user-1", project.ID, DramaSegmentsWriteRequest{
		UnitID:   unit.ID,
		Segments: []json.RawMessage{dramaSpec("S04", 5, "")},
		Order:    []string{"S01", "S02"},
	}); err == nil {
		t.Fatal("写入的段不在顺序里应被拒绝")
	}
}

func segmentIDs(views []DramaSegmentView) string {
	ids := make([]string, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.Spec.ID)
	}
	return strings.Join(ids, ",")
}

func dramaToolCall(name string, args any) cloudAgentCall {
	raw, _ := json.Marshal(args)
	call := cloudAgentCall{}
	call.Function.Name = name
	call.Function.Arguments = string(raw)
	return call
}

// 拆段、合段靠 order：工具要把它公开成可选的字符串数组，说明里讲清何时传、会删什么、拆段怎么编号。
func TestDramaSegmentsWriteToolAdvertisesOrder(t *testing.T) {
	req := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, DramaProjectID: "project-1"}
	var function map[string]any
	for _, tool := range cloudAgentTools(req) {
		if candidate := tool["function"].(map[string]any); candidate["name"] == "drama_segments_write" {
			function = candidate
		}
	}
	if function == nil {
		t.Fatal("项目画布应有 drama_segments_write")
	}
	parameters := function["parameters"].(map[string]any)
	order, ok := parameters["properties"].(map[string]any)["order"].(map[string]any)
	if !ok || order["type"] != "array" || order["items"].(map[string]any)["type"] != "string" {
		t.Fatalf("order 应是字符串数组：%v", parameters["properties"])
	}
	for _, name := range parameters["required"].([]string) {
		if name == "order" {
			t.Fatal("order 只在拆段、合段时给，不能是必填")
		}
	}
	description := function["description"].(string)
	for _, want := range []string{"不传 order", "S02b", "候选视频", "无法恢复"} {
		if !strings.Contains(description, want) {
			t.Fatalf("说明里缺「%s」：%s", want, description)
		}
	}
	if strings.Contains(description, "不删除段") {
		t.Fatalf("说明还是旧的「不删除段」：%s", description)
	}
}

// order 由通用的参数解码原样带进 WriteDramaSegments；写成字符串时 Agent 看到的是类型提示，不是笼统的工具失败。
func TestDramaSegmentsWriteToolDecodesOrderStraightThrough(t *testing.T) {
	var args DramaSegmentsWriteRequest
	if err := decodeCloudAgentJSONObject(`{"unitId":"unit-1","segments":[{"id":"S02"}],"order":["S01","S02","S02b"]}`, &args); err != nil {
		t.Fatalf("带 order 的参数应能解码：%v", err)
	}
	if args.UnitID != "unit-1" || len(args.Segments) != 1 || strings.Join(args.Order, ",") != "S01,S02,S02b" {
		t.Fatalf("order 没有原样带进来：%+v", args)
	}
	var bad DramaSegmentsWriteRequest
	err := decodeCloudAgentJSONObject(`{"unitId":"unit-1","segments":[],"order":"S01,S02"}`, &bad)
	if err == nil {
		t.Fatal("order 写成字符串应被拒绝")
	}
	if message := cloudAgentSafeToolError(cloudAgentJSONArgumentError(err)); !strings.Contains(message, "类型不匹配") {
		t.Fatalf("应给出类型提示，得到 %q", message)
	}
}

// PlanSegmentOrder 的每一句报错都要原样回给 Agent，不被净化成「工具执行失败」。
func TestSegmentOrderErrorsReachAgentVerbatim(t *testing.T) {
	existing := []drama.ExistingSegment{{ID: "S01"}, {ID: "S02"}, {ID: "S03", Derived: true}}
	cases := []struct {
		name    string
		order   []string
		written []string
	}{
		{"写入的段不在顺序里", []string{"S01", "S03"}, []string{"S02"}},
		{"段号重复", []string{"S01", "S02", "S02", "S03"}, []string{"S02"}},
		{"顺序里有不存在的段", []string{"S01", "S02", "S03", "S09"}, []string{"S02"}},
		{"合成段号的旧镜头不能靠省略删除", []string{"S01", "S02"}, []string{"S02"}},
		{"段号格式无效", []string{"S01", "第二段", "S03"}, nil},
		{"把镜头 ID 当段号", []string{"S01", strings.Repeat("0123456789abcdef", 4)}, nil},
		{"段号带引号和换行", []string{"S01", "S02\n\"b\"\\"}, nil},
	}
	for _, c := range cases {
		_, err := drama.PlanSegmentOrder(c.order, c.written, existing)
		if err == nil {
			t.Fatalf("%s：应被拒绝", c.name)
		}
		if got := cloudAgentSafeToolError(BadAuthRequest(err.Error())); got != err.Error() {
			t.Fatalf("%s：报错被净化成了 %q，应原样给 Agent：%q", c.name, got, err.Error())
		}
	}
}

// Agent 从工具入口拆段、合段：新段插在原段后面，合掉的段连候选一起删，order 有错时整批不落库且报错可读。
// 需要 CGO（SQLite），本机无 CGO 时不能运行。
func TestDramaSegmentsWriteToolSplitsAndMergesWithOrder(t *testing.T) {
	service, db, project, unit := newDramaTestService(t, "seedance-2.5")
	state := &cloudAgentRuntime{Request: CloudAgentRequest{DramaProjectID: project.ID}}
	write := func(segments []json.RawMessage, order []string) ([]DramaSegmentView, error) {
		args := map[string]any{"unitId": unit.ID, "segments": segments}
		if order != nil {
			args["order"] = order
		}
		result, err := service.cloudAgentDramaTool("user-1", state, dramaToolCall("drama_segments_write", args))
		if err != nil {
			return nil, err
		}
		return result.([]DramaSegmentView), nil
	}
	views, err := write([]json.RawMessage{dramaSpec("S01", 8, "第一句"), dramaSpec("S02", 12, "第二句"), dramaSpec("S03", 6, "第三句")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s03 := views[2].ShotID
	if err := db.Create(&model.ShotArtifact{ID: "artifact-tool-s03", ProjectID: project.ID, UnitID: unit.ID, ShotID: s03, Type: "video", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}

	// 不传 order：新段号追加到章末，不删段。
	if views, err = write([]json.RawMessage{dramaSpec("S02b", 5, "")}, nil); err != nil || segmentIDs(views) != "S01,S02,S03,S02b" {
		t.Fatalf("不传 order 应追加到章末：%s %v", segmentIDs(views), err)
	}
	// 传 order：S02b 摆回 S02 后面。
	if views, err = write([]json.RawMessage{dramaSpec("S02", 7, "第二句")}, []string{"S01", "S02", "S02b", "S03"}); err != nil || segmentIDs(views) != "S01,S02,S02b,S03" {
		t.Fatalf("拆出的段应在原段后面：%s %v", segmentIDs(views), err)
	}

	// 写错的 order 整批拒绝：报错可读，章里的段不变。
	for name, order := range map[string][]string{
		"顺序里有不存在的段": {"S01", "S02", "S02b", "S03", "S09"},
		"写入的段不在顺序里": {"S01", "S02b", "S03"},
		"段号格式无效":    {"S01", "第二段", "S03"},
	} {
		_, err := write([]json.RawMessage{dramaSpec("S02", 7, "第二句")}, order)
		if err == nil || cloudAgentSafeToolError(err) != err.Error() || !strings.Contains(err.Error(), "段顺序") {
			t.Fatalf("%s：应给出可读原因，得到 %v", name, err)
		}
		after, readErr := service.DramaSegments("user-1", project.ID, unit.ID)
		if readErr != nil || segmentIDs(after) != "S01,S02,S02b,S03" {
			t.Fatalf("%s：被拒绝的写入不该改动章节：%s %v", name, segmentIDs(after), readErr)
		}
	}

	// 合段：S03 并进 S02，order 里不写 S03，它的候选一起删除。
	if views, err = write([]json.RawMessage{dramaSpec("S02", 12, "第二句第三句")}, []string{"S01", "S02", "S02b"}); err != nil || segmentIDs(views) != "S01,S02,S02b" {
		t.Fatalf("合段后应剩三段：%s %v", segmentIDs(views), err)
	}
	var artifacts int64
	if err := db.Model(&model.ShotArtifact{}).Where("shot_id = ?", s03).Count(&artifacts).Error; err != nil || artifacts != 0 {
		t.Fatalf("被合并掉的段的候选应一起删除：%d %v", artifacts, err)
	}
}

// 旧镜头没有分段 JSON、段号按位置合成：order 里漏掉它不能当成删除，必须列出来。需要 CGO（SQLite）。
func TestDramaSegmentsWriteToolOrderMustListLegacySegments(t *testing.T) {
	service, _, project, unit := newDramaTestService(t, "seedance-2.5")
	state := &cloudAgentRuntime{Request: CloudAgentRequest{DramaProjectID: project.ID}}
	legacy, err := service.CreateProjectShot("user-1", project.ID, CreateProjectShotRequest{UnitID: unit.ID, Title: "旧镜头", Description: "护士推开七床的门", DurationMs: 5000})
	if err != nil {
		t.Fatal(err)
	}
	write := func(order []string) (any, error) {
		return service.cloudAgentDramaTool("user-1", state, dramaToolCall("drama_segments_write", map[string]any{
			"unitId": unit.ID, "segments": []json.RawMessage{dramaSpec("S02", 6, "")}, "order": order,
		}))
	}
	if _, err := write([]string{"S02"}); err == nil || cloudAgentSafeToolError(err) != err.Error() || !strings.Contains(err.Error(), "S01 还不是分段") {
		t.Fatalf("漏掉合成段号的旧镜头应被拒绝并说清原因，得到 %v", err)
	}
	result, err := write([]string{"S01", "S02"})
	if err != nil {
		t.Fatalf("列出旧镜头后应通过：%v", err)
	}
	views := result.([]DramaSegmentView)
	if len(views) != 2 || views[0].ShotID != legacy.ID || !views[0].Derived || views[1].Spec.ID != "S02" {
		t.Fatalf("旧镜头应原样保留、新段排在后面：%+v", views)
	}
}
