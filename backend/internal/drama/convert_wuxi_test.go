package drama

import (
	"strings"
	"testing"
)

// qichuangWuxiPlan 是七床第一集在 maxSeconds 上限的模型上、把 lines 这段剧本范围改成武戏后的小节计划。
func qichuangWuxiPlan(t *testing.T, maxSeconds int, lines [2]int) SectionPlan {
	t.Helper()
	return PlanSections(qichuangChapter(t), "第一集", DefaultShotGenre(maxSeconds), maxSeconds, []GenreOverride{{Scene: "E01S02", Lines: lines, Genre: ShotGenreWuxi}})
}

// 改武戏重切：Seedance 2.5（30 秒）上 E01S02 第 2 节（文戏 30，26.3 秒，正文第 28～42 行的打斗）按武戏口径重切成 2 个武戏小节，
// 每节整秒 N 秒、不超过 30 秒，台词一句不丢；其余节不动。Seedance 2.0（15 秒）上同一段每节不超过 15 秒。改回文戏（没有 override）
// 得到原来那 1 节文戏 30。
func TestPlanSectionsWuxiOverride(t *testing.T) {
	g30 := DefaultShotGenre(30)
	before := PlanSections(qichuangChapter(t), "第一集", g30, 30, nil)
	orig, ok := before.Section("E01S02-P2")
	if !ok || orig.Genre != ShotGenreWenxi30 || orig.Lines != [2]int{28, 42} {
		t.Fatalf("原第 2 节：%+v", orig)
	}
	plan := qichuangWuxiPlan(t, 30, orig.Lines)
	var wuxi []Section
	for _, s := range plan.Sections {
		if s.Genre == ShotGenreWuxi {
			wuxi = append(wuxi, s)
		}
	}
	if len(wuxi) != 2 || wuxi[0].ID != "E01S02-P2" || wuxi[1].ID != "E01S02-P3" {
		t.Fatalf("应重切成 2 个武戏小节：%+v", wuxi)
	}
	var lines []string
	for _, s := range wuxi {
		if s.Est != float64(int(s.Est)) || s.Est > 30 || s.Est < 4 || s.Max != 30 {
			t.Fatalf("%s 应是不超过 30 秒的整秒：%.1f（上限 %.0f）", s.ID, s.Est, s.Max)
		}
		lines = append(lines, s.LineIDs...)
	}
	if strings.Join(lines, ",") != strings.Join(orig.LineIDs, ",") || wuxi[0].Lines[0] != 28 || wuxi[1].Lines[1] != 42 {
		t.Fatalf("武戏节应正好覆盖原节的范围和台词：%v vs %v", lines, orig.LineIDs)
	}
	if p1, _ := plan.Section("E01S02-P1"); p1.Genre != ShotGenreWenxi30 || p1.Lines != [2]int{13, 27} {
		t.Fatalf("其余节不动：%+v", p1)
	}
	if got := plan.Genres(); len(got) != 2 || got[0].ID != ShotGenreWenxi30 || got[1].ID != ShotGenreWuxi {
		t.Fatalf("计划里的戏类：%+v", got)
	}
	// Seedance 2.0：每个武戏小节不超过 15 秒。
	for _, s := range qichuangWuxiPlan(t, 15, [2]int{27, 42}).Sections {
		if s.Genre == ShotGenreWuxi && (s.Est > 15 || s.Max != 15) {
			t.Fatalf("15 秒模型的武戏节 %s 有 %.0f 秒", s.ID, s.Est)
		}
		if s.Genre == ShotGenreWenxi30 {
			t.Fatalf("15 秒模型不该出现文戏 30：%s", s.ID)
		}
	}
	// 改回文戏：去掉 override，同一段又是原来的 1 节文戏 30。
	again := PlanSections(qichuangChapter(t), "第一集", g30, 30, nil)
	if back, _ := again.Section("E01S02-P2"); back.Lines != orig.Lines || back.Genre != ShotGenreWenxi30 || len(again.Sections) != len(before.Sections) {
		t.Fatalf("改回文戏应回到原来的 1 节：%+v", back)
	}
}

func TestWuxiBeatsPrompt(t *testing.T) {
	if got := WuxiBeatsPrompt(30); !strings.Contains(got, "0–3s 建立威胁；3–8s 第一次真正交锋；8–14s 攻防升级；14–20s 战局反转；20–26s 极限爆发；26–30s 终结+余波") {
		t.Fatalf("30 秒照原图：%s", got)
	}
	if got := WuxiBeatsPrompt(22); !strings.Contains(got, "按 22/30 缩放") || !strings.Contains(got, "0–2s 建立威胁") || !strings.Contains(got, "19–22s 终结+余波") {
		t.Fatalf("22 秒按比例缩放：%s", got)
	}
	if got := WuxiBeatsPrompt(4); !strings.Contains(got, "–4s") || strings.Count(got, "s ") > 6 {
		t.Fatalf("4 秒挤在一起的段并掉：%s", got)
	}
}

func wuxiSectionContext(t *testing.T) SectionContext {
	t.Helper()
	sec, ok := qichuangWuxiPlan(t, 30, [2]int{28, 42}).Section("E01S02-P2")
	if !ok {
		t.Fatal("没有武戏节 E01S02-P2")
	}
	sec.Est = 5
	return SectionContext{Section: sec, Characters: []string{"林小满", "周岸", "阿坤"}, Places: []string{"电梯口"}, Props: []string{"折叠刀", "病历夹"}, Anchors: []string{"电梯门", "防火门", "日光灯"}, Indoor: true}
}

// 武戏转换器（v7.0 4.29 每镜标准格式）：一镜 1 秒；P/R/L/F、特效、摄影机目标、站位原文、环境反馈、下一镜连接、核心观看体验进 extras；
// WHIP 是 whip（不并成 pan）；台词逐字落 line_id，「（接上句）」不另起一句；配乐字样剥掉；每镜有 fight（接触判定）。
func TestConvertWuxiBlockFormat(t *testing.T) {
	ctx := wuxiSectionContext(t)
	conv, err := ConvertSection(ShotGenreWuxi, readSectionSample(t, "qichuang-e01s02-p2.wuxi.txt"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Rows) != 5 || conv.Stats.Shots != 5 || conv.Stats.Lines != 2 || conv.Stats.LineMissing != 0 || conv.Stats.MusicStripped == 0 {
		t.Fatalf("应 5 镜、2 句台词全落、剥掉配乐：%+v", conv.Stats)
	}
	if len(conv.Fields) != 1 || conv.Fields[0].Name != "核心观看体验" {
		t.Fatalf("原样字段：%+v", conv.Fields)
	}
	var shots []Shot
	for _, row := range conv.Rows {
		sh := rowSpec(t, row)
		shots = append(shots, sh)
		if sh.Est != 1 || sh.Genre != ShotGenreWuxi || sh.Section != "E01S02-P2" || sh.Fight == nil || sh.Fight.Contact == "" {
			t.Fatalf("%s：est=%v genre=%s section=%s fight=%+v", sh.ID, sh.Est, sh.Genre, sh.Section, sh.Fight)
		}
		for _, field := range []string{sh.Action, sh.Extras["特效"], sh.Extras["环境反馈"]} {
			if containsAny(field, bgmWords...) {
				t.Fatalf("%s 留着配乐字样：%q", sh.ID, field)
			}
		}
		if sh.Light == nil || sh.Light.SourceAnchor == "" {
			t.Fatalf("%s 室内要有光源锚点：%+v", sh.ID, sh.Light)
		}
	}
	first, second, third := shots[0], shots[1], shots[2]
	if first.ID != "E01S02-01" || first.Size != "全景" || !slicesContains(first.Move, "push") || first.Cam.F == nil || first.Cam.F.LensMM == nil || *first.Cam.F.LensMM != 24 {
		t.Fatalf("第 1 镜景别 / 焦段 / 运镜：%s %v %+v", first.Size, first.Move, first.Cam)
	}
	if first.Extras["核心观看体验"] == "" || first.Extras["摄影机目标"] != "阿坤" || first.Extras["力度"] != "P1 轻·试探" || first.Focus != "阿坤" || !strings.Contains(first.Extras["站位"], "相距 4m") {
		t.Fatalf("第 1 镜 extras：%+v focus=%s", first.Extras, first.Focus)
	}
	if len(first.Blocking) != 3 || first.Blocking[0].Who != "阿坤" || first.Blocking[0].Facing != "面向左" || first.Fight.Initiator != "阿坤" || first.Fight.Contact != "none" {
		t.Fatalf("第 1 镜站位与动作：%+v %+v", first.Blocking, first.Fight)
	}
	if len(first.Lines) != 1 || first.Lines[0].LineID == "" {
		t.Fatalf("第 1 镜台词落 line_id：%+v", first.Lines)
	}
	if !slicesContains(second.Move, "whip") || slicesContains(second.Move, "pan") || second.Fight.Contact != "graze" || second.Extras["战损"] != "L1→L2" {
		t.Fatalf("第 2 镜甩镜、擦伤：%v %+v %+v", second.Move, second.Fight, second.Extras)
	}
	if third.Size != "中近景" || !slicesContains(third.Move, "handheld_violent") || third.Extras["闪帧"] != "F2 黑闪 2 帧" || third.Fight.Contact != "blocked" || third.Transition == nil || third.Transition.CutPoint != "动作" {
		t.Fatalf("第 3 镜重击格挡：%s %v %+v %+v %+v", third.Size, third.Move, third.Extras, third.Fight, third.Transition)
	}
	if len(third.Lines) != 1 || len(shots[3].Lines) != 0 {
		t.Fatalf("「（接上句）」不另起台词：%+v %+v", third.Lines, shots[3].Lines)
	}
	if !slicesContains(shots[4].Move, "orbit") || shots[4].Transition.CutPoint != "道具" {
		t.Fatalf("第 5 镜环绕、遮挡转场：%v %+v", shots[4].Move, shots[4].Transition)
	}
}

// v6.0 成品示例的单行写法（镜 N｜t..｜景别+运镜｜站位｜动作｜A 微表情｜B 微表情｜力度｜伤痕｜光影）也收：力度、伤痕、闪帧从栏里认。
func TestConvertWuxiCard0LineFormat(t *testing.T) {
	ctx := SectionContext{Section: Section{ID: "E01S01-P1", Scene: "E01S01", Genre: ShotGenreWuxi, Est: 30}}
	conv, err := ConvertSection(ShotGenreWuxi, readSectionSample(t, "card0-wuxi-example.txt"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Rows) != 6 || conv.Stats.Rejected != 0 {
		t.Fatalf("应 6 镜：%+v", conv.Stats)
	}
	byNo := map[int]Shot{}
	for i, row := range conv.Rows {
		byNo[i] = rowSpec(t, row)
	}
	if byNo[0].Size != "中近景" || !slicesContains(byNo[0].Move, "push") || byNo[0].Light == nil || byNo[0].Light.Kelvin != "7500K" {
		t.Fatalf("镜 1：%s %v %+v", byNo[0].Size, byNo[0].Move, byNo[0].Light)
	}
	if byNo[2].Extras["力度"] != "P2 中·格挡" || byNo[2].Extras["受击"] != "R1" || !slicesContains(byNo[2].Move, "crane_up") || byNo[2].Cam.Y != "Y2" {
		t.Fatalf("镜 3：%+v %v %+v", byNo[2].Extras, byNo[2].Move, byNo[2].Cam)
	}
	if !slicesContains(byNo[3].Move, "whip") || byNo[3].Extras["闪帧"] != "F2" || byNo[3].Extras["战损"] != "L2" || byNo[3].Fight.Contact != "hit" {
		t.Fatalf("镜 6：%v %+v %+v", byNo[3].Move, byNo[3].Extras, byNo[3].Fight)
	}
	if byNo[5].Extras["力度"] != "P4 暴·绝杀贯穿" || byNo[5].Extras["闪帧"] != "F4" {
		t.Fatalf("镜 27：%+v", byNo[5].Extras)
	}
}

// 武戏节编译用武戏段落结构（摄影机目标 → 站位 → 动作链与 P/R/L/F → 表情 → 特效 → 环境反馈 → 光影 → 接下一镜，段头核心观看体验，
// 约束前加打斗约束），配乐不进编译稿；同样的镜头当文戏节编译时保持原来的结构。
func TestCompileWuxiStructure(t *testing.T) {
	ctx := wuxiSectionContext(t)
	conv, err := ConvertSection(ShotGenreWuxi, readSectionSample(t, "qichuang-e01s02-p2.wuxi.txt"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var shots []Shot
	for _, row := range conv.Rows {
		shots = append(shots, rowSpec(t, row))
	}
	script := ScriptFromText(qichuangChapter(t), "第一集")
	model, ok := ModelByID("seedance-2.5")
	if !ok {
		t.Fatal("没有 seedance-2.5")
	}
	compile := func(shots []Shot) string {
		segs, problems := Pack(PackInput{Shotlist: Shotlist{Profile: "live-realism", Shots: shots}, Script: &script, Model: model, FileExists: func(string) bool { return true }})
		if len(segs) != 1 || len(problems) != 0 {
			t.Fatalf("应装成 1 段：%d %v", len(segs), problems)
		}
		res, err := Compile(segs[0], model, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return res.Prompt
	}
	wuxi := compile(shots)
	for _, want := range []string{"本节核心观看体验：护人者以伤换位", "全景 / ", "摄影机对准阿坤。", "站位：阿坤 画右前景·面向左·同高", "（P3 重·格挡，R2 踉跄撞门，L2，F2 黑闪 2 帧）", "特效：Layer2 金属火星。", "环境反馈：电梯门凹进一块", "接下一镜：IMPACT CUT 撞门后果。", "打斗约束：动作先于特效", "周岸：“退后！”"} {
		if !strings.Contains(wuxi, want) {
			t.Fatalf("武戏编译稿缺「%s」：\n%s", want, wuxi)
		}
	}
	if strings.Contains(wuxi, "配乐压低") || strings.Contains(wuxi, "闪帧：无") || strings.Contains(wuxi, "特效：无") {
		t.Fatalf("武戏编译稿不写配乐和空标签：\n%s", wuxi)
	}
	var wenxi []Shot
	for _, sh := range shots {
		sh.Genre = ShotGenreWenxi30
		wenxi = append(wenxi, sh)
	}
	plain := compile(wenxi)
	if strings.Contains(plain, "摄影机对准") || strings.Contains(plain, "打斗约束") || strings.Contains(plain, "本节核心观看体验") {
		t.Fatalf("文戏节保持原结构：\n%s", plain)
	}
}

// 武戏节按戏类放宽：R-SHOT-006/022/024/026 记成 warn，不拦流程（武戏技能不写 camera_emotion、表演节拍和情绪弧）。
func TestWuxiRelaxedRules(t *testing.T) {
	rules := GenreRelaxedRules(ShotGenreWuxi)
	for _, r := range []string{"R-SHOT-006", "R-SHOT-022", "R-SHOT-024", "R-SHOT-026"} {
		if !slicesContains(rules, r) {
			t.Fatalf("武戏没放宽 %s：%v", r, rules)
		}
	}
	if GenreRelaxedRules("") != nil {
		t.Fatal("旧镜头表不放宽")
	}
	ctx := wuxiSectionContext(t)
	conv, err := ConvertSection(ShotGenreWuxi, readSectionSample(t, "qichuang-e01s02-p2.wuxi.txt"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	var shots []Shot
	for _, row := range conv.Rows {
		shots = append(shots, rowSpec(t, row))
	}
	script := ScriptFromText(qichuangChapter(t), "第一集")
	report := CheckShots(Shotlist{Profile: "live-realism", Shots: shots}, CheckContext{Script: &script, Profile: testProfile(t, "live-realism"), Vocab: testVocab(t), Rules: testShotRules(t), WarnOnly: GenreRelaxedRules(ShotlistGenre(shots))})
	relaxedSeen := 0
	for _, is := range report.Items {
		if slicesContains(rules, is.Rule) {
			relaxedSeen++
			if is.Level != "warn" {
				t.Fatalf("%s 应放宽成 warn：%+v", is.Rule, is)
			}
		}
	}
	if relaxedSeen == 0 {
		t.Fatal("这组武戏镜头应触发被放宽的规则（没写 camera_emotion / 情绪弧）")
	}
}

// 运镜词表：whip 是单独一项（甩镜），机位轴算旋转，编译词「甩镜」；中文「甩镜」和英文 WHIP 都认成 whip。
func TestWuxiWhipMove(t *testing.T) {
	if got := wuxiMoves("MS / 35mm / WHIP 甩镜"); len(got) != 1 || got[0] != "whip" {
		t.Fatalf("WHIP：%v", got)
	}
	if got := wuxiMoves("MCU WHIP 急推"); len(got) != 2 || got[0] != "whip" || got[1] != "push" {
		t.Fatalf("WHIP 急推：%v", got)
	}
	if moveAxis["whip"] != "rot" {
		t.Fatal("whip 是旋转轴")
	}
	cp, err := DefaultCompileProfile()
	if err != nil || cp.Camera.Move["whip"] != "甩镜" {
		t.Fatalf("编译词：%v %q", err, cp.Camera.Move["whip"])
	}
}

// 一章有文戏节和武戏节：一个戏类一条对话。第一条是第 1 节的戏类（文戏 30），武戏另起一条，用武戏技能开头；武戏节的本轮指令带
// 平台填好的参数表、按 N/30 缩放的节拍和武戏输出要求，续写提示只看同一戏类。
func TestBuildSectionShotsPromptLanes(t *testing.T) {
	plan := qichuangWuxiPlan(t, 30, [2]int{28, 42})
	skill := func(id string) StepSkill {
		return StepSkill{ID: id, Files: []StepSkillFile{{Path: "SKILL.md", Kind: "markdown", Content: "# " + id}}}
	}
	prompt, err := BuildSectionShotsPrompt(SectionShotsInput{
		ChapterShotsInput: ChapterShotsInput{ChapterTitle: "第一集", SourceText: qichuangChapter(t)},
		Skills:            map[string]StepSkill{ShotGenreWenxi30: skill("yz-shots-wenxi30"), ShotGenreWuxi: skill("yz-shots-wuxi-live")},
		Plan:              plan, ModelLabel: "Seedance 2.5", AspectRatio: "9:16",
	})
	if err != nil {
		t.Fatal(err)
	}
	lane, ok := prompt.Lanes[ShotGenreWuxi]
	if !ok || len(prompt.Lanes) != 1 || !strings.Contains(prompt.Text, "yz-shots-wenxi30") || strings.Contains(prompt.Text, "yz-shots-wuxi") || !strings.Contains(lane.Text, "yz-shots-wuxi-live") {
		t.Fatalf("两条对话各用自己的技能：lanes=%d", len(prompt.Lanes))
	}
	if !strings.Contains(lane.Text, "其中第 3、4 节是武戏，由这个对话按本技能写") || !strings.Contains(lane.Text, "（文戏 30，另一套技能写）") {
		t.Fatalf("武戏对话的分节说明：%s", lane.Text[strings.Index(lane.Text, "【分节生成说明】"):])
	}
	if len(prompt.Turns) != len(plan.Sections) {
		t.Fatalf("每节一条本轮指令：%d", len(prompt.Turns))
	}
	first, second := prompt.Turns[2], prompt.Turns[3]
	for _, want := range []string{"戏类：武戏", "【参数表（平台已填）】时长：23 秒（23 镜，1 秒 1 镜）", "A：周岸；B：林小满｜", "模式：D·短剧分镜", "画幅：9:16", "【本节节拍】", "按 23/30 缩放", "v7.0 每镜标准格式", "「镜 23｜22-23s」", "一句可以跨相邻几个 1 秒镜头说完"} {
		if !strings.Contains(first, want) {
			t.Fatalf("武戏第 1 节缺「%s」：\n%s", want, first)
		}
	}
	if strings.HasPrefix(first, "继续写下一节") || !strings.HasPrefix(second, "继续写下一节") || !strings.HasPrefix(prompt.Turns[1], "继续写下一节") {
		t.Fatal("续写提示只看同一戏类的前一节")
	}
	if !strings.Contains(second, "镜 1 从上一节最后一镜的站位") {
		t.Fatal("同场后续武戏节接着上一节打")
	}
	if _, err := BuildSectionShotsPrompt(SectionShotsInput{Plan: plan, Skills: map[string]StepSkill{ShotGenreWenxi30: skill("yz-shots-wenxi30")}}); err == nil {
		t.Fatal("武戏没给技能应报错")
	}
}
