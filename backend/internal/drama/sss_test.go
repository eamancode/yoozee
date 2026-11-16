package drama

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// loadBuiltinSkill 读 backend/builtin/<id> 的清单和全部文件（与上传校验同一份数据）。
func loadBuiltinSkill(t *testing.T, id string) (StepPackageManifest, map[string][]byte, StepSkill) {
	t.Helper()
	base := filepath.Join("..", "..", "builtin", id)
	raw, err := os.ReadFile(filepath.Join(base, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, problems := ParseStepPackageManifest(raw)
	if len(problems) != 0 {
		t.Fatal(id, problems)
	}
	files := map[string][]byte{}
	skill := StepSkill{ID: id}
	for _, p := range manifest.Files {
		content, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		files[p] = content
		skill.Files = append(skill.Files, StepSkillFile{Path: p, Content: string(content)})
	}
	return manifest, files, skill
}

func TestSSSBuiltinPackagesPassStaticChecks(t *testing.T) {
	for _, kind := range []string{SSSKindFilm, SSSKindDrama} {
		for _, id := range []string{SSSDirectorSkill(kind), SSSScriptSkill(kind), SSSStyleSkill(kind)} {
			manifest, files, skill := loadBuiltinSkill(t, id)
			if problems := CheckStepPackage(manifest, files); len(problems) != 0 {
				t.Fatal(id, problems)
			}
			if id != SSSDirectorSkill(kind) {
				continue
			}
			if manifest.Contract != ContractSSSDirector || manifest.Step != StepChapterShots {
				t.Fatal(id, manifest)
			}
			rc, err := ReadSSSRuntimeContext(skill)
			if err != nil {
				t.Fatal(err)
			}
			codes := SSSHeuristicCodes(skill, rc)
			if len(codes) != 19 || codes[0] != "A01" || !slices.Contains(codes, "E01") || slices.Contains(codes, "00") {
				t.Fatalf("%s heuristic codes = %v", id, codes)
			}
			// E01 分上下两份，选 E01 时两份都装；单元卡轮再装 4 个库也不超过一轮的提示词上限。
			e01, err := SSSHeuristicFiles(skill, rc, []string{"E01"})
			if err != nil || len(e01) != 2 {
				t.Fatal(e01, err)
			}
			for _, turn := range SSSTurnKinds {
				extra := []string(nil)
				if turn == SSSTurnCard {
					extra, _ = SSSHeuristicFiles(skill, rc, []string{"C01", "C02", "E01", "A04"})
				}
				ctx, uses, err := SSSSkillContext(skill, turn, extra)
				if err != nil {
					t.Fatal(id, turn, err)
				}
				if !strings.Contains(ctx, `<skill-file path="SKILL.md">`) || !strings.Contains(ctx, `<skill-file path="参数.md">`) || slices.ContainsFunc(uses, func(u StepSkillFileUse) bool { return strings.HasPrefix(u.Path, "范例/") }) {
					t.Fatal(id, turn, "required files missing or 范例 loaded")
				}
				if n := len([]rune(ctx)); n > 120000 {
					t.Fatalf("%s %s skill context %d chars", id, turn, n)
				}
			}
		}
	}
}

// sssExample 读技能自带范例里的一份文件。
func sssExample(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "builtin", SSSDirectorSkill(SSSKindDrama), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSSSParsesTheSkillsOwnEpisodeExample(t *testing.T) {
	doc := sssExample(t, "范例/08_一整集全流程示范.md")
	for _, part := range []string{"第一部分", "第二部分", "第三部分", "第四部分", "第五部分", "第六部分", "第七部分", "第八部分"} {
		if body, ok := SSSSection(doc, part); !ok || body == "" {
			t.Fatal("example part missing", part)
		}
	}
	units, err := SSSUnits(doc)
	if err != nil || !slices.Equal(units, []string{"E2-U1", "E2-U2"}) {
		t.Fatal(units, err)
	}
	cards, err := ParseSSSEpisodeCards(doc)
	if err != nil || len(cards) != 2 {
		t.Fatal(len(cards), err)
	}
	for _, c := range cards {
		if c.TotalSeconds != 23 || c.Shots != 10 || !c.Freeze || c.Scene != "周家餐厅" {
			t.Fatalf("header = %+v", c)
		}
		rows, err := sssCheckShotTable(c)
		if err != nil || len(rows) != 11 || rows[10].Duty != "全景定格" {
			t.Fatalf("%s shot table: %d rows, %v", c.ID, len(rows), err)
		}
		if err := ValidateSSSCardBody(c, c.ID, SSSUnitLimits{MaxSeconds: 30, CapSeconds: 55}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateSSSCardBody(cards[1], "E2-U2", SSSUnitLimits{MaxSeconds: 15}); err == nil || !strings.Contains(err.Error(), "超过平台上限 15 秒") {
		t.Fatal("15-second model must reject a 23-second unit", err)
	}
	if err := ValidateSSSCardBody(cards[1], "E2-U2", SSSUnitLimits{MaxSeconds: 30, CapSeconds: 44, UsedSeconds: 23}); err == nil || !strings.Contains(err.Error(), "最多 21 秒") {
		t.Fatal("episode cap must be enforced", err)
	}
	if handoff, ok := SSSHandoffText(doc); !ok || !strings.Contains(handoff, "第 2 集交接卡") {
		t.Fatal("handoff not found")
	}
}

var sssExampleMaterials = []SSSMaterial{
	{Index: 1, Name: "林夏", Category: "character", VersionID: "v-linxia"},
	{Index: 2, Name: "周母", Category: "character", VersionID: "v-zhoumu"},
	{Index: 3, Name: "周家餐厅", Category: "environment", VersionID: "v-room"},
	{Index: 4, Name: "铜钥匙", Category: "prop", VersionID: "v-key"},
}

// sssExamplePrompt 是模板/05 的完整示例提示词（E2-U2）。
func sssExamplePrompt(t *testing.T) string {
	t.Helper()
	tpl := sssExample(t, "模板/05_提示词骨架.md")
	body, ok := SSSSection(tpl, "一条完整的示例")
	m := sssFence.FindStringSubmatch(body)
	if !ok || m == nil {
		t.Fatal("example prompt missing")
	}
	return strings.TrimSpace(m[1])
}

func TestSSSPasteValidatesTheSkillsOwnPromptExample(t *testing.T) {
	paste := sssExamplePrompt(t)
	if err := sssCheckSections(paste); err != nil {
		t.Fatal("the skill's own example must have all fifteen sections", err)
	}
	card := SSSUnitCard{ID: "E2-U2", Scene: "周家餐厅", TotalSeconds: 23, Shots: 10, Paste: paste, Fenced: true, Risk: "第 7 镜桌下低机位可能被拍成平视。"}
	lim := SSSUnitLimits{MaxSeconds: 30, MaxImages: 9, Materials: sssExampleMaterials}
	err := ValidateSSSPaste(card, lim)
	if err == nil || !strings.Contains(err.Error(), "@站位参考") {
		t.Fatal("@站位参考 has no screenshot on this platform and must be rejected", err)
	}
	withFrame := lim
	withFrame.PositionReference = true
	if err := ValidateSSSPaste(card, withFrame); err != nil {
		t.Fatal("有相邻定格时应支持技能示例", err)
	}
	mixed := card
	mixed.Paste = strings.Replace(paste, "@站位参考", "角色动作与 @站位参考", 1)
	if err := ValidateSSSPaste(mixed, withFrame); err == nil || !strings.Contains(err.Error(), "单独起一行") {
		t.Fatal("共用一行会随站位图一起误删的动作指令应退回", err)
	}
	withFrame.MaxImages = 4
	if err := ValidateSSSPaste(card, withFrame); err == nil || !strings.Contains(err.Error(), "留一个名额") {
		t.Fatal("站位图占用名额", err)
	}
	lines := strings.Split(paste, "\n")
	lines = slices.DeleteFunc(lines, func(l string) bool { return strings.HasPrefix(l, "@站位参考") })
	card.Paste = strings.Join(lines, "\n")
	withFrame.MaxImages = 9
	if err := ValidateSSSPaste(card, withFrame); err == nil || !strings.Contains(err.Error(), "必须写一次") {
		t.Fatal("缺必需站位图", err)
	}
	if err := ValidateSSSPaste(card, lim); err != nil {
		t.Fatal(err)
	}
	used, problems := SSSPasteTags(card.Paste, sssExampleMaterials)
	if len(problems) != 0 || !slices.Equal(used, []int{0, 1, 2, 3}) {
		t.Fatal(used, problems)
	}
	lim.MaxImages = 3
	if err := ValidateSSSPaste(card, lim); err == nil || !strings.Contains(err.Error(), "超过视频模型上限 3 张") {
		t.Fatal("model reference cap must be enforced", err)
	}
	// 四个标签按第一次出现的顺序换成 @图1..4，参考文件同序；原文其余字节不变。
	card.Body = "### E2-U2｜长档｜叙事 22 秒＋定格 1 秒＝23 秒｜1 镜｜周家餐厅（本场第 2 段／共 2 段）｜卡点\n\n#### 镜头表\n\n| 镜 | 时间 | 时长 | 景别·视场角 | 机位 | 运动 | 焦点 | 画面与表演 | 台词/声音 | 职责 |\n|---|---|---|---|---|---|---|---|---|---|\n| 1 | 0–23 | 23 | 近景 | 平视 | 固定 | 眼睛 | 林夏看着周母。 | —— | 说 |\n"
	card.Shots = 1
	seg, err := SSSSegment(card, 2, sssExampleMaterials)
	if err != nil {
		t.Fatal(err)
	}
	if seg.ID != "S02" || seg.Title != "E2-U2" || seg.Dur != 23 || seg.Mode != ModeLockIdentity || len(seg.Refs) != 4 || seg.Refs[0].File != "v-linxia" || seg.Refs[2].Role != "location" || seg.Refs[3].Role != "prop" {
		t.Fatalf("segment = %+v", seg)
	}
	if strings.Contains(seg.Verbatim, "@林夏") || !strings.Contains(seg.Verbatim, "@图1：二十六岁左右的女性") || !strings.Contains(seg.Verbatim, "@图3：老式住宅的餐厅") {
		t.Fatal(seg.Verbatim[:400])
	}
	if strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(seg.Verbatim, "@图1", "@林夏"), "@图2", "@周母"), "@图3", "@周家餐厅"), "@图4", "@铜钥匙") != card.Paste {
		t.Fatal("verbatim changed bytes other than the tags")
	}
}

func TestSSSTagsLongestNameAndForbiddenReferences(t *testing.T) {
	mats := []SSSMaterial{{Index: 1, Name: "林夏", Category: "character", VersionID: "a"}, {Index: 2, Name: "林夏婚礼造型", Category: "character", VersionID: "b"}}
	used, problems := SSSPasteTags("@林夏婚礼造型站着，@林夏坐着。", mats)
	if len(problems) != 0 || !slices.Equal(used, []int{1, 0}) {
		t.Fatal(used, problems)
	}
	_, problems = SSSPasteTags("@林夏声音：年轻女声。@王五走进来。@视频1 @音频1", mats)
	joined := strings.Join(problems, "；")
	for _, want := range []string{"@林夏声音 不可用", "@王五走进来 不在素材清单里", "@视频1 不可用", "@音频1 不可用"} {
		if !strings.Contains(joined, want) {
			t.Fatal(want, joined)
		}
	}
}

func TestSSSCutTurnUnitsAndHeuristics(t *testing.T) {
	cut := "## 整场镜头表\n\n镜头……\n\n## 第四部分　单元总表\n\n| 单元 | 档 |\n|---|---|\n| E3-U1 | 长 |\n| E3-U2 | 短 |\n\n## 启发库选用\n\n| 单元 | 库号 |\n|---|---|\n| E3-U1 | A01、F01 |\n| E3-U2 | E01、C01、D01 |\n"
	units, err := SSSUnits(cut)
	if err != nil || !slices.Equal(units, []string{"E3-U1", "E3-U2"}) {
		t.Fatal(units, err)
	}
	known := []string{"A01", "C01", "D01", "E01", "F01"}
	choices, err := SSSHeuristicChoices(cut, units, known, 4)
	if err != nil || !slices.Equal(choices["E3-U2"], []string{"E01", "C01", "D01"}) {
		t.Fatal(choices, err)
	}
	if _, err := SSSHeuristicChoices(cut, units, known, 2); err == nil || !strings.Contains(err.Error(), "最多 2 个") {
		t.Fatal(err)
	}
	if _, err := SSSHeuristicChoices(strings.Replace(cut, "| E3-U1 | A01、F01 |", "| E3-U1 | Z99 |", 1), units, known, 4); err == nil || !strings.Contains(err.Error(), "没有选启发库") {
		t.Fatal(err)
	}
}

func TestSSSEpisodeDocumentKeepsEightPartsInOrder(t *testing.T) {
	doc := sssExample(t, "范例/08_一整集全流程示范.md")
	cards, _ := ParseSSSEpisodeCards(doc)
	cut := "## 整场镜头表\n\n整场\n\n" + "## 第四部分　单元总表\n\n| 单元 |\n|---|\n| E2-U1 |\n"
	out := SSSEpisodeDocument(doc, doc, cut, cards, doc)
	at := -1
	for _, part := range SSSEpisodeParts {
		i := strings.Index(out, "## "+part)
		if i <= at {
			t.Fatal("part out of order or missing", part)
		}
		at = i
	}
	back, err := ParseSSSEpisodeCards(out)
	if err != nil || len(back) != 2 || back[1].ID != "E2-U2" {
		t.Fatal(len(back), err)
	}
}

func TestSSSMaterialPackageStatesPlatformFacts(t *testing.T) {
	pkg := SSSMaterialPackage(SSSBrief{Kind: SSSKindFilm, AspectRatio: "16:9", MaxUnitSeconds: 15, MaxImages: 9, EpisodeSeconds: 90}, sssExampleMaterials[:2], SSSDocs{Concept: "## 立意页\n立意", World: "## 世界基准段\n基准"})
	for _, want := range []string{"电影模式", "15 秒", "全剧只用短档", "横屏16:9", "3840×2160", "@林夏", "@周母", "@站位参考", "最多带 9 张参考图", "## 立意页", "## 世界基准段", "【全剧圣经】\n无"} {
		if !strings.Contains(pkg, want) {
			t.Fatal(want, pkg)
		}
	}
}

func TestScriptAdaptConceptTurn(t *testing.T) {
	turns := ScriptAdaptWithConcept([]ShotsScene{{ID: "1", Heading: "第 1 集"}, {ID: "2", Heading: "第 2 集"}})
	if intro := ScriptAdaptTurnPlan(turns); !strings.Contains(intro, "分 3 个回合") || !strings.Contains(intro, "## 立意页") {
		t.Fatal(intro)
	}
	if p := ScriptAdaptNextPrompt(turns, 2); !strings.Contains(p, "现在写第 1 集") || !strings.Contains(p, "no 写 1") {
		t.Fatal(p)
	}
	if p := ScriptAdaptNextPrompt(turns, 3); !strings.Contains(p, "继续写第 2 集") {
		t.Fatal(p)
	}
	concept := "## 立意页\n\n### 一句话故事\n她回到抱错的家。\n\n## 全集结构表\n\n| 集 | 任务 |\n|---|---|\n| 1 | 进门 |\n"
	if n, err := CountScriptAdaptRun(concept, TurnContext{Turn: turns[0]}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := CountScriptAdaptRun("## 立意页\n\n立意\n", TurnContext{Turn: turns[0]}); err == nil {
		t.Fatal("missing 全集结构表 must fail")
	}
	episode := `{"no":1,"title":"进门","sources":[]}` + "\n## 内·周家餐厅·夜\n林夏：回去可以。\n"
	if _, err := CountScriptAdaptRun(concept+episode, TurnContext{Turn: turns[0]}); err == nil {
		t.Fatal("concept turn must not write an episode")
	}
	full := concept + "\n" + episode
	if got := ScriptAdaptConceptText(full); strings.Contains(got, "周家餐厅") || !strings.Contains(got, "## 全集结构表") || !strings.Contains(got, "| 1 | 进门 |") {
		t.Fatal(got)
	}
	episodes, _ := ScriptAdaptEpisodesFromText(full)
	if len(episodes) != 1 || len(episodes[0].Scenes) != 1 || strings.Contains(episodes[0].Text, "立意") {
		t.Fatalf("concept text leaked into the episode: %+v", episodes)
	}
	if n, err := CountScriptAdaptRun(episode, TurnContext{Turn: turns[1]}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestStyleLockWorldTurn(t *testing.T) {
	world := "\n## 世界基准段\n\n真人实拍质感的竖屏短剧。\n\n## 视觉总谱\n\n色彩……\n\n## 声音总谱\n\n挂钟秒针。\n"
	if _, err := CountStyleLockRun(styleLockOutput, TurnContext{Turn: ShotsScene{ID: StyleLockWorldTurn}}); err == nil || !strings.Contains(err.Error(), "世界基准段") {
		t.Fatal("SSS style turn must require the three sections", err)
	}
	if n, err := CountStyleLockRun(styleLockOutput+world, TurnContext{Turn: ShotsScene{ID: StyleLockWorldTurn}}); err != nil || n != 6 {
		t.Fatal(n, err)
	}
	if n, err := CountStyleLockRun(styleLockOutput, TurnContext{}); err != nil || n != 6 {
		t.Fatal("legacy style lock unchanged", n, err)
	}
	out, _ := StyleLockFromText(styleLockOutput + world)
	if out.Anchor == nil || len(out.Palette.Colors()) != 6 {
		t.Fatal("markdown after the JSON lines must not disturb the contract parse")
	}
	if got := StyleLockWorldText(styleLockOutput + world); !strings.HasPrefix(got, "## 世界基准段") || !strings.Contains(got, "## 声音总谱\n\n挂钟秒针。") {
		t.Fatal(got)
	}
}

func TestSplitSkillsQuoteTheirSourceChaptersVerbatim(t *testing.T) {
	for _, kind := range []string{SSSKindFilm, SSSKindDrama} {
		src := filepath.Join("..", "..", "builtin", SSSDirectorSkill(kind))
		read := func(rel string) string {
			matches, _ := filepath.Glob(filepath.Join(src, filepath.FromSlash(rel)))
			if len(matches) != 1 {
				t.Fatal(rel, matches)
			}
			raw, err := os.ReadFile(matches[0])
			if err != nil {
				t.Fatal(err)
			}
			return strings.TrimRight(string(raw), "\n")
		}
		_, _, script := loadBuiltinSkill(t, SSSScriptSkill(kind))
		for _, rel := range []string{"手册/01_*", "手册/02_*", "模板/01_*"} {
			if !strings.Contains(script.Files[0].Content, read(rel)) {
				t.Fatal(kind, "script skill must quote", rel)
			}
		}
		_, _, style := loadBuiltinSkill(t, SSSStyleSkill(kind))
		bible := read("手册/03_*")
		for _, title := range []string{"一、世界基准段", "五、视觉总谱", "六、声音总谱", "九、圣经的纪律"} {
			body, _ := SSSSection(bible, title)
			if body == "" || !strings.Contains(style.Files[0].Content, body) {
				t.Fatal(kind, "style skill must quote", title)
			}
		}
		for _, file := range []StepSkill{script, style} {
			if !strings.Contains(file.Files[0].Content, "## 柚子流程接入说明") {
				t.Fatal(file.ID, "integration note missing")
			}
		}
	}
}

// SSS-02：材料包写的分辨率与项目画幅一致；参数.md 没给分辨率的画幅不写像素，也不写成竖屏。
func TestSSSMaterialPackageResolutionFollowsAspect(t *testing.T) {
	for _, kind := range []string{SSSKindFilm, SSSKindDrama} {
		for ratio, want := range map[string]string{"16:9": "横屏16:9，分辨率 3840×2160", "9:16": "竖屏9:16，分辨率 2160×3840", "": "竖屏9:16，分辨率 2160×3840", "1:1": "画幅：1:1（项目画幅", "4:3": "画幅：4:3（项目画幅"} {
			pkg := SSSMaterialPackage(SSSBrief{Kind: kind, AspectRatio: ratio, MaxUnitSeconds: 30}, nil, SSSDocs{})
			if !strings.Contains(pkg, want) {
				t.Fatalf("%s %q: want %q in\n%s", kind, ratio, want, pkg[:400])
			}
			if ratio == "1:1" || ratio == "4:3" {
				if strings.Contains(pkg, "3840") || strings.Contains(pkg, "2160") {
					t.Fatalf("%s %q must not carry a portrait or landscape resolution", kind, ratio)
				}
			}
		}
	}
}

// fifteenSections 拼一条十五段提示词；body 给每一段的正文（空串表示这一段只有段名）。
func fifteenSections(body func(name string) string) string {
	var b strings.Builder
	for _, name := range SSSPromptSections {
		b.WriteString(name + "\n" + body(name) + "\n\n")
	}
	return b.String()
}

// SSS-03：十五段每段都要有正文，只有段名、空白正文都不算编译完成；不适用的段明写「无」可以通过。
func TestSSSPasteRejectsEmptySections(t *testing.T) {
	check := func(paste string) error {
		return ValidateSSSPaste(SSSUnitCard{ID: "E1-U1", Paste: paste, Fenced: true, Risk: "无"}, SSSUnitLimits{})
	}
	bare := fifteenSections(func(string) string { return "" }) + SSSNoMusicLine
	if err := check(bare); err == nil || !strings.Contains(err.Error(), "没有正文：场景背景、生效参考素材") || !strings.Contains(err.Error(), "、输出设置") {
		t.Fatal("bare headings must be rejected, naming the empty sections", err)
	}
	full := func(name string) string {
		if name == "声音" {
			return "雨声。" + SSSNoMusicLine + "。"
		}
		return name + "的正文。"
	}
	if err := check(fifteenSections(full)); err != nil {
		t.Fatal(err)
	}
	partly := fifteenSections(func(name string) string {
		if name == "物理" {
			return ""
		}
		return full(name)
	})
	if err := check(partly); err == nil || !strings.Contains(err.Error(), "没有正文：物理") {
		t.Fatal("one empty section must be named", err)
	}
	blank := fifteenSections(func(name string) string {
		if name == "表演" {
			return "   \n\t  "
		}
		return full(name)
	})
	if err := check(blank); err == nil || !strings.Contains(err.Error(), "没有正文：表演") {
		t.Fatal("whitespace is not a body", err)
	}
	explicit := fifteenSections(func(name string) string {
		if name == "物理" || name == "布光" {
			return "无"
		}
		return full(name)
	})
	if err := check(explicit); err != nil {
		t.Fatal("sections explicitly written as 无 must pass", err)
	}
}

// SSS-04：保留写法先于素材名判断：素材恰好叫「视频」「音频」「站位参考」「林夏声音」也不能当参考图用。
func TestSSSReservedTagsBeatMaterialNames(t *testing.T) {
	mats := []SSSMaterial{
		{Index: 1, Name: "视频", Category: "prop", VersionID: "a"},
		{Index: 2, Name: "音频", Category: "prop", VersionID: "b"},
		{Index: 3, Name: "站位参考", Category: "environment", VersionID: "c"},
		{Index: 4, Name: "林夏声音", Category: "character", VersionID: "d"},
		{Index: 5, Name: "林夏", Category: "character", VersionID: "e"},
		{Index: 6, Name: "视频图", Category: "prop", VersionID: "f"},
	}
	for _, tag := range []string{"@视频", "@音频1", "@站位参考", "@林夏声音"} {
		used, problems := SSSPasteTags(tag+"：说明。", mats)
		if len(used) != 0 || len(problems) != 1 || !strings.Contains(problems[0], tag+" 不可用") {
			t.Fatalf("%s must be rejected even though a material has that name: %v %v", tag, used, problems)
		}
	}
	for _, tag := range []string{"@视频2", "@音频", "@站位参考", "@周母声音"} {
		if _, problems := SSSPasteTags(tag+"，", nil); len(problems) != 1 || !strings.Contains(problems[0], "不可用") {
			t.Fatalf("%s with no materials: %v", tag, problems)
		}
	}
	// 正常名称、前缀重名、中文标点照旧：林夏 / 视频图（改过名的资产）能用，@图k 与 refs 同序去重。
	paste := "@林夏站着。@视频图：一台旧录像机，@林夏回头（看它）。"
	used, problems := SSSPasteTags(paste, mats)
	if len(problems) != 0 || !slices.Equal(used, []int{4, 5}) {
		t.Fatal(used, problems)
	}
	card := SSSUnitCard{ID: "E1-U1", Scene: "客厅", TotalSeconds: 5, Shots: 1, Paste: paste,
		Body: "### E1-U1｜短档｜5 秒｜1 镜｜客厅｜钩子\n\n#### 镜头表\n\n| 镜 | 时间 | 画面与表演 |\n|---|---|---|\n| 1 | 0–5 | 林夏回头。 |\n"}
	seg, err := SSSSegment(card, 1, mats)
	if err != nil || seg.Verbatim != "@图1站着。@图2：一台旧录像机，@图1回头（看它）。" || len(seg.Refs) != 2 || seg.Refs[0].File != "e" || seg.Refs[1].File != "f" {
		t.Fatalf("segment = %+v %v", seg, err)
	}
	card.Paste = "@站位参考 @林夏"
	if _, err := SSSSegment(card, 1, mats); err == nil {
		t.Fatal("segment assembly must refuse reserved tags too")
	}
	for name, want := range map[string]string{"视频": "media", "音频3": "media", "站位参考": "media", "林夏声音": "voice", "视频图": "", "站位参考图": "", "林夏": ""} {
		if got := SSSReservedTag(name); got != want {
			t.Fatalf("SSSReservedTag(%q) = %q, want %q", name, got, want)
		}
	}
}

// SSS-R2-01：「声音」按素材名的边界判。人物引用后面接「压低声音」「听到声音」这类动作正文，照常引用人物、原文不动；
// 只有 @林夏声音（名字后紧跟「声音」且标签到此为止）或素材本身叫「林夏声音」才是参考音频写法。
func TestSSSVoiceRuleUsesMaterialBoundary(t *testing.T) {
	linxia := []SSSMaterial{{Index: 1, Name: "林夏", Category: "character", VersionID: "v-linxia"}}
	full := func(body string) string {
		return fifteenSections(func(name string) string {
			switch name {
			case "生效参考素材":
				return body
			case "声音":
				return "雨声。" + SSSNoMusicLine + "。"
			}
			return name + "的正文。"
		})
	}
	card := func(paste string) SSSUnitCard {
		return SSSUnitCard{ID: "E1-U1", Scene: "门口", TotalSeconds: 5, Shots: 1, Paste: paste, Fenced: true, Risk: "无",
			Body: "### E1-U1｜短档｜5 秒｜1 镜｜门口｜钩子\n\n#### 镜头表\n\n| 镜 | 时间 | 画面与表演 |\n|---|---|---|\n| 1 | 0–5 | 林夏抬头。 |\n"}
	}
	for _, body := range []string{"@林夏站着。", "@林夏压低声音，抬头看门。", "@林夏听到声音。", "@林夏声音很轻地说话。"} {
		used, problems := SSSPasteTags(body, linxia)
		if len(problems) != 0 || len(used) != 1 || used[0] != 0 {
			t.Fatalf("%s: character reference with action prose must be accepted: %v %v", body, used, problems)
		}
		paste := full(body)
		if err := ValidateSSSPaste(card(paste), SSSUnitLimits{Materials: linxia}); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		seg, err := SSSSegment(card(paste), 1, linxia)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		want := strings.Replace(body, "@林夏", "@图1", 1)
		if !strings.Contains(seg.Verbatim, want) || len(seg.Refs) != 1 || seg.Refs[0].File != "v-linxia" {
			t.Fatalf("%s: action prose must be kept verbatim after @图1: %+v", body, seg.Refs)
		}
	}
	for _, body := range []string{"@林夏声音：年轻女声。", "@林夏声音", "@林夏声音，现场对白。"} {
		if _, problems := SSSPasteTags(body, linxia); len(problems) != 1 || !strings.Contains(problems[0], "@林夏声音 不可用") {
			t.Fatalf("%s must be rejected as a voice reference: %v", body, problems)
		}
		if err := ValidateSSSPaste(card(full(body)), SSSUnitLimits{Materials: linxia}); err == nil {
			t.Fatalf("%s must fail paste validation", body)
		}
		if _, err := SSSSegment(card(full(body)), 1, linxia); err == nil {
			t.Fatalf("%s must not be assembled", body)
		}
	}
	// 前缀重名：林夏 / 林夏声音（未改名的同名素材）/ 林夏声音图（改过名的合法资产）。
	mats := []SSSMaterial{
		{Index: 1, Name: "林夏", Category: "character", VersionID: "a"},
		{Index: 2, Name: "林夏声音", Category: "character", VersionID: "b"},
		{Index: 3, Name: "林夏声音图", Category: "prop", VersionID: "c"},
	}
	if _, problems := SSSPasteTags("@林夏声音：女声。", mats); len(problems) != 1 || !strings.Contains(problems[0], "不可用") {
		t.Fatalf("material named 林夏声音 must not bypass the voice rule: %v", problems)
	}
	paste := "@林夏压低声音，看着@林夏声音图站着。@林夏听到声音。"
	used, problems := SSSPasteTags(paste, mats)
	if len(problems) != 0 || !slices.Equal(used, []int{0, 2}) {
		t.Fatalf("legal references with prefix-overlapping names: %v %v", used, problems)
	}
	seg, err := SSSSegment(card(full(paste)), 1, mats)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seg.Verbatim, "@图1压低声音，看着@图2站着。@图1听到声音。") || len(seg.Refs) != 2 || seg.Refs[0].File != "a" || seg.Refs[1].File != "c" {
		t.Fatalf("@图k order and refs must not regress: %+v", seg.Refs)
	}
	// 没有素材时的 @某某声音、@视频N 仍按保留写法拒绝。
	for _, body := range []string{"@周母声音：", "@视频3，"} {
		if _, problems := SSSPasteTags(body, nil); len(problems) != 1 || !strings.Contains(problems[0], "不可用") {
			t.Fatalf("%s: %v", body, problems)
		}
	}
}
