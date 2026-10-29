package app

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yingce/backend/internal/database"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

var (
	updateChapterShots = flag.Bool("update-chapter-shots", false, "重写 testdata/qichuang-chapter-shots/server-prompt.txt")
	dumpChapterShots   = flag.String("dump-chapter-shots", "", "把七床复刻项目的接口数据写到这个目录（用前端旧实现对照提示词）")
)

const qichuangDir = "testdata/qichuang-chapter-shots"

type qichuangSeed struct {
	Project struct {
		Name             string `json:"name"`
		StylePresetID    string `json:"stylePresetId"`
		StyleProfileJSON string `json:"styleProfileJson"`
		BriefJSON        string `json:"briefJson"`
	} `json:"project"`
	Unit struct {
		Title string `json:"title"`
	} `json:"unit"`
	Characters []map[string]any `json:"characters"`
	Scenes     []map[string]any `json:"scenes"`
	Props      []map[string]any `json:"props"`
	Pending    []struct {
		Name     string         `json:"name"`
		Category string         `json:"category"`
		Details  map[string]any `json:"details"`
	} `json:"pending"`
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	return readTestdataIn(t, qichuangDir, name)
}

func readTestdataIn(t *testing.T, dir string, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

// plainTextToHTML 是章节页「添加章节」把粘贴的正文转成 HTML 的规则（chapters.tsx plainTextToHtml）。
func plainTextToHTML(value string) string {
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
	var out strings.Builder
	for _, paragraph := range regexp.MustCompile(`\n{2,}`).Split(escaped, -1) {
		out.WriteString("<p>" + strings.ReplaceAll(paragraph, "\n", "<br>") + "</p>")
	}
	return out.String()
}

// newChapterShotsTestService 是带完整表结构和内置技能的服务（内置技能同步会写技能包归档到数据目录）。
func newChapterShotsTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+newID()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(database.Models()...); err != nil {
		t.Fatal(err)
	}
	svc := &Service{repo: repository.New(db), dataDir: t.TempDir()}
	if err := svc.EnsureBuiltinSkills(); err != nil {
		t.Fatal(err)
	}
	return svc, db
}

// seedQichuang 按真实用户路径复刻测试项目「yoozee 实测·七床」第一章：建项目（悬疑犯罪 · 暗黑诡谲 · 真人实拍，每集 90 秒）、
// 粘贴正文建章节、提取出的 3 个角色、3 个场景（带锚点）、8 个道具确认成资产，另留一个待确认角色。
func seedQichuang(t *testing.T, svc *Service, db *gorm.DB) (model.Project, model.ProjectUnit) {
	t.Helper()
	return seedChapterShotsProject(t, svc, db, qichuangDir)
}

// seedChapterShotsProject 按 dir 里的 seed.json 与 chapter.txt 走真实用户路径建项目（七床与评测章节同一套）。
func seedChapterShotsProject(t *testing.T, svc *Service, db *gorm.DB, dir string) (model.Project, model.ProjectUnit) {
	t.Helper()
	var seed qichuangSeed
	if err := json.Unmarshal([]byte(readTestdataIn(t, dir, "seed.json")), &seed); err != nil {
		t.Fatal(err)
	}
	project, err := svc.CreateProject("user-1", CreateProjectRequest{Name: seed.Project.Name, SourceType: "text", StylePresetID: seed.Project.StylePresetID, StyleProfileJSON: seed.Project.StyleProfileJSON, GenerationModel: "seedance-2.5"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("brief_json", seed.Project.BriefJSON).Error; err != nil {
		t.Fatal(err)
	}
	project.BriefJSON = seed.Project.BriefJSON
	unit, err := svc.CreateProjectUnit("user-1", project.ID, CreateProjectUnitRequest{Kind: "chapter", Title: seed.Unit.Title, SourceText: plainTextToHTML(readTestdataIn(t, dir, "chapter.txt"))})
	if err != nil {
		t.Fatal(err)
	}
	var inputs []AssetCandidateInput
	for _, group := range []struct {
		category string
		items    []map[string]any
	}{{"character", seed.Characters}, {"environment", seed.Scenes}, {"prop", seed.Props}} {
		for _, item := range group.items {
			inputs = append(inputs, AssetCandidateInput{UnitID: unit.ID, Name: item["name"].(string), Category: group.category, Details: item})
		}
	}
	created, err := svc.CreateProjectAssetCandidates("user-1", project.ID, CreateAssetCandidatesRequest{Candidates: inputs, Source: assetCandidateSourceChapterCharacter})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range created {
		if _, err := svc.ConfirmProjectAssetCandidate("user-1", project.ID, candidate.ID, ConfirmProjectAssetCandidateRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	var pending []AssetCandidateInput
	for _, p := range seed.Pending {
		pending = append(pending, AssetCandidateInput{UnitID: unit.ID, Name: p.Name, Category: p.Category, Details: p.Details})
	}
	if _, err := svc.CreateProjectAssetCandidates("user-1", project.ID, CreateAssetCandidatesRequest{Candidates: pending, Source: assetCandidateSourceChapterCharacter}); err != nil {
		t.Fatal(err)
	}
	return project, unit
}

// 服务端组装的生成分段提示词：七床第一章逐字等于黄金文件；-dump-chapter-shots 另写出接口数据，交给前端旧实现对照。
func TestChapterShotsPromptQichuang(t *testing.T) {
	svc, db := newChapterShotsTestService(t)
	project, unit := seedQichuang(t, svc, db)
	if project.StyleProfile != "live-realism" {
		t.Fatalf("新建项目不选档案时按画风对照推荐：%q", project.StyleProfile)
	}
	sections, err := svc.DramaStepPrompt("user-1", project.ID, unit.ID, drama.StepChapterShots)
	if err != nil {
		t.Fatal(err)
	}
	if sections.Usage.Skill.ID != "yz-shots-wenxi30" || sections.Usage.Profile.ID != "live-realism" || sections.Usage.Bound || len(sections.Files) != 5 || !strings.Contains(sections.Prompt, "【分节生成说明】") {
		t.Fatalf("生成分段按戏类用文戏 30 的技能（SKILL.md + 4 张卡）：%+v %d", sections.Usage, len(sections.Files))
	}
	// 整章 JSON Lines 的旧拼法（yz-chapter-shots）只留给技能包评测：组装结果与黄金文件逐字相同。
	got := legacyChapterShotsPrompt(t, svc, project, unit)
	if got.Usage.Skill.ID != "yz-chapter-shots" || got.Usage.Profile.ID != "live-realism" || got.Usage.Bound || got.Usage.Skill.Version == "" || got.Usage.Skill.ContentHash == "" {
		t.Fatalf("没配绑定时用步骤默认技能：%+v", got.Usage)
	}
	if len(got.Files) != 4 || got.Files[0].Path != "SKILL.md" {
		t.Fatalf("注入 SKILL.md 和三张卡：%+v", got.Files)
	}
	if *dumpChapterShots != "" {
		dumpQichuang(t, svc, db, project, unit, got)
	}
	if *updateChapterShots {
		if err := os.WriteFile(filepath.Join(qichuangDir, "server-prompt.txt"), []byte(got.Prompt), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want := readTestdata(t, "server-prompt.txt"); got.Prompt != want {
		t.Fatalf("服务端提示词与黄金文件不一致（改了组装规则就用 -update-chapter-shots 重写并说明差异）：\n%s", firstDiff(want, got.Prompt))
	}
}

// legacyChapterShotsPrompt 是整章 JSON Lines 旧拼法（chapterShotsPlan）在七床上的提示词：按绑定表 chapter.shots 那一格取技能
// （评测重放旧包走的就是这条组装路径）。
func legacyChapterShotsPrompt(t *testing.T, svc *Service, project model.Project, unit model.ProjectUnit) DramaStepPrompt {
	t.Helper()
	step, _ := drama.StepByID(drama.StepChapterShots)
	fresh, err := svc.repo.ProjectForUser("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile := projectDramaContext(fresh).profileID()
	skillID, bound := step.DefaultSkill, false
	if row, err := svc.repo.DramaStepBinding(profile, step.ID); err == nil {
		skillID, bound = row.SkillID, true
	}
	pkg, err := svc.skillDomain().StepSkillPackage(skillID)
	if err != nil {
		t.Fatal(err)
	}
	skill := stepSkillOf(pkg)
	assets, err := svc.ProjectAssets("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := svc.repo.ProjectUnit(project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	usage := DramaStepUsage{Step: step.ID, StepLabel: step.Label, Contract: step.Contract, Bound: bound, Profile: dramaProfileRef(profile),
		Skill: DramaStepSkillRef{ID: skill.ID, Name: skill.Name, VersionID: skill.VersionID, Version: skill.Version, ContentHash: skill.ContentHash}}
	plan, err := chapterShotsPlan(fresh, stored, nil, assets, usage, skill)
	if err != nil {
		t.Fatal(err)
	}
	return DramaStepPrompt{Usage: plan.usage, Prompt: plan.prompt.Text, Files: plan.prompt.Files}
}

// normalizeStepPrompt 抹掉三处有意的差异：技能上下文的开场说明（技能由档案绑定，不是用户选的），
// 契约 @3 一镜一行的格式句（换回 @2 整段 JSON 的写法），契约的字段清单（由 schema 生成，类型写法与折行不同；字段名和顺序要一致）。
func normalizeStepPrompt(t *testing.T, prompt string) string {
	t.Helper()
	lines, err := drama.BuiltinContract(drama.ContractChapterShots)
	if err != nil {
		t.Fatal(err)
	}
	whole, err := drama.BuiltinContract(drama.ContractCanvasStoryboard)
	if err != nil {
		t.Fatal(err)
	}
	opts := drama.ContractTextOptions{SkillShaped: true}
	from, to := strings.Split(lines.Text(lines.DefaultRequirement(), opts), "\n"), strings.Split(whole.Text(whole.DefaultRequirement(), opts), "\n")
	if len(from) != len(to) {
		t.Fatalf("@3 与 @2 的契约文字行数不同")
	}
	for i := range from {
		if from[i] != to[i] {
			prompt = strings.Replace(prompt, from[i], to[i], 1)
		}
	}
	prompt = strings.Replace(prompt, lines.FieldInstruction("assets"), whole.FieldInstruction("assets"), 1)
	prompt = strings.Replace(prompt, "以下 skill-context 来自用户主动安装并在本轮明确选择的技能库。", "<skill-preamble>", 1)
	prompt = strings.Replace(prompt, "以下 skill-context 来自本步骤按项目风格档案绑定的流程技能。", "<skill-preamble>", 1)
	start := strings.Index(prompt, "不要省略字段：\n")
	end := strings.Index(prompt, "\n- rows 每项再加 spec")
	if start < 0 || end < start {
		t.Fatalf("找不到契约字段清单")
	}
	fields := regexp.MustCompile(`[A-Za-z]+(?:（[^）]*）)?`).FindAllString(prompt[start:end], -1)
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, strings.SplitN(f, "（", 2)[0])
	}
	return prompt[:start] + "<fields:" + strings.Join(names, ",") + ">" + prompt[end:]
}

// 与原先前端组装的提示词（frontend-prompt.txt：M6 之前的 chapterStoryboardTaskPrompt 在同一份接口数据上的输出）对照：
// 只有三处有意的差异——技能上下文的开场说明（技能由档案绑定，不是用户选的），契约 @3 改成一镜一行，契约的字段清单由 schema 生成（类型写法与折行）。
// 技能、画风、角色、资产与锚点、每集时长逐字相同。
func TestChapterShotsPromptMatchesFrontend(t *testing.T) {
	server := readTestdata(t, "server-prompt.txt")
	frontend := readTestdata(t, "frontend-prompt.txt")
	if a, b := normalizeStepPrompt(t, frontend), normalizeStepPrompt(t, server); a != b {
		t.Fatalf("除开场说明和字段清单外，服务端提示词应与前端旧实现逐字相同：\n%s", firstDiff(a, b))
	}
	if strings.Contains(server, "characters（本镜出现的角色名数组）") || !strings.Contains(server, "characters（字符串数组，本镜出现的角色名）") {
		t.Fatal("字段清单由 schema 生成")
	}
}

func firstDiff(want, got string) string {
	w, g := []rune(want), []rune(got)
	i := 0
	for i < len(w) && i < len(g) && w[i] == g[i] {
		i++
	}
	from := max(0, i-80)
	return "期望：…" + string(w[from:min(len(w), i+120)]) + "\n实际：…" + string(g[from:min(len(g), i+120)])
}

// dumpQichuang 写出前端组装要用的接口数据（与浏览器拿到的同一份 JSON）。
func dumpQichuang(t *testing.T, svc *Service, db *gorm.DB, project model.Project, unit model.ProjectUnit, got DramaStepPrompt) {
	t.Helper()
	dir := *dumpChapterShots
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	assets, err := svc.ProjectAssets("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := svc.repo.ProjectAssetCandidates(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := svc.repo.ProjectForUser("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := svc.ProjectBrief("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := svc.SkillPackageFiles("user-1", "yz-chapter-shots")
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string]any{}
	for _, f := range files {
		content, err := svc.SkillPackageFile("user-1", "yz-chapter-shots", f.Path)
		if err != nil {
			t.Fatal(err)
		}
		contents[f.Path] = content
	}
	var skill model.Skill
	if err := db.First(&skill, "id = ?", "yz-chapter-shots").Error; err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		raw, err := json.MarshalIndent(value, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("project.json", stored)
	write("unit.json", unit)
	write("assets.json", assets)
	write("candidates.json", candidates)
	write("brief.json", brief)
	write("skill.json", map[string]any{"skillId": skill.ID, "skillName": skill.Name, "versionId": skill.CurrentVersionID, "version": skill.VersionLabel, "files": files, "contents": contents})
	if err := os.WriteFile(filepath.Join(dir, "server-prompt.txt"), []byte(got.Prompt), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 绑定表：没配的格子用步骤默认技能；管理员改绑后项目自动用新技能的当前版本；空技能 ID 删掉这一格回到默认。
func TestDramaStepBindingsResolveAndAdminWrite(t *testing.T) {
	svc, db := newChapterShotsTestService(t)
	project, _ := seedQichuang(t, svc, db)
	admin := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	if _, err := svc.AdminDramaStepBindings(&model.User{ID: "user-1"}); err == nil {
		t.Fatal("非管理员不能读绑定表")
	}
	matrix, err := svc.AdminDramaStepBindings(admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(matrix.Profiles) != 5 || len(matrix.Steps) != 8 || matrix.Steps[0].DefaultSkill != "yz-style-lock" || matrix.Steps[1].DefaultSkill != "yz-script-adapt" || matrix.Steps[2].DefaultSkill != "yz-scene-palette" || matrix.Steps[3].DefaultSkill != "yz-assets-extract" || matrix.Steps[4].DefaultSkill != "yz-chapter-shots" ||
		matrix.Steps[5].DefaultSkill != "yz-shots-wenxi15" || matrix.Steps[6].DefaultSkill != "yz-shots-wenxi30" || matrix.Steps[7].DefaultSkill != "yz-shots-wuxi" {
		t.Fatalf("五个档案；风格锁定、剧本优化、场次色卡、资产设计、生成分段（整章旧版）和三行戏类（文戏 15、文戏 30、武戏）：%+v", matrix.Steps)
	}
	// 资产设计没配绑定时五个档案都用默认技能 yz-assets-extract（内置种子首次安装就是流程技能包）。
	if usage, err := svc.DramaStepUsage("user-1", project.ID, drama.StepAssetsExtract, nil); err != nil || usage.Skill.ID != "yz-assets-extract" || usage.Bound || usage.Contract != drama.ContractAssetsExtract {
		t.Fatalf("资产设计默认技能：%v %+v", err, usage)
	}
	// 场次色卡没配绑定时五个档案都用默认技能 yz-scene-palette（线上库由迁移 68 写成绑定），技能上下文里带色板设计卡。
	if usage, err := svc.DramaStepUsage("user-1", project.ID, drama.StepScenePalette, nil); err != nil || usage.Skill.ID != "yz-scene-palette" || usage.Bound || usage.Contract != drama.ContractScenePalette {
		t.Fatalf("场次色卡默认技能：%v %+v", err, usage)
	}
	// 剧本优化没配绑定时五个档案都用默认技能 yz-script-adapt（内置种子首次安装就是流程技能包）。
	if usage, err := svc.DramaStepUsage("user-1", project.ID, drama.StepScriptAdapt, nil); err != nil || usage.Skill.ID != "yz-script-adapt" || usage.Bound || usage.Contract != drama.ContractScriptAdapt {
		t.Fatalf("剧本优化默认技能：%v %+v", err, usage)
	}
	for _, bad := range []DramaStepBindingCell{
		{StyleProfile: "no-such", Step: drama.StepChapterShots, SkillID: "yz-chapter-shots"},
		{StyleProfile: "live-realism", Step: "episode.story", SkillID: "yz-chapter-shots"},
		{StyleProfile: "live-realism", Step: drama.StepChapterShots, SkillID: "no-such-skill"},
	} {
		if _, err := svc.UpdateDramaStepBindings(admin, DramaStepBindingsRequest{Bindings: []DramaStepBindingCell{bad}}); err == nil {
			t.Fatalf("应拒绝 %+v", bad)
		}
	}
	// 生成分段按戏类那一格查绑定（七床是 30 秒模型，文戏 30）。
	const wenxi30 = "chapter.shots#wenxi30"
	if _, err := svc.UpdateDramaStepBindings(admin, DramaStepBindingsRequest{Bindings: []DramaStepBindingCell{{StyleProfile: "live-realism", Step: wenxi30, SkillID: "yz-direct"}}}); err != nil {
		t.Fatal(err)
	}
	usage, err := svc.DramaStepUsage("user-1", project.ID, drama.StepChapterShots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Skill.ID != "yz-direct" || !usage.Bound || usage.Profile.ID != "live-realism" || usage.BindingStep != wenxi30 {
		t.Fatalf("改绑后用新技能：%+v", usage)
	}
	if _, err := svc.UpdateProject("user-1", project.ID, UpdateProjectRequest{StyleProfile: ptr("anime-3d")}); err != nil {
		t.Fatal(err)
	}
	if usage, _ = svc.DramaStepUsage("user-1", project.ID, drama.StepChapterShots, nil); usage.Skill.ID != "yz-shots-wenxi30" || usage.Bound || usage.Profile.ID != "anime-3d" {
		t.Fatalf("换档案后按新档案查绑定（没配用默认）：%+v", usage)
	}
	if _, err := svc.UpdateProject("user-1", project.ID, UpdateProjectRequest{StyleProfile: ptr("not-a-profile")}); err == nil {
		t.Fatal("项目只能选内置风格档案")
	}
	matrix, err = svc.UpdateDramaStepBindings(admin, DramaStepBindingsRequest{Bindings: []DramaStepBindingCell{{StyleProfile: "live-realism", Step: wenxi30}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(matrix.Cells) != 0 {
		t.Fatalf("空技能 ID 删掉这一格：%+v", matrix.Cells)
	}
}

// 运行这一步建的文本任务：与原先前端发的章节分镜任务同一形状，metadata 记下技能版本与内容哈希。
func TestDramaStepTaskRequestRecordsSkillVersion(t *testing.T) {
	svc, db := newChapterShotsTestService(t)
	project, unit := seedQichuang(t, svc, db)
	plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: " deepseek ", Config: map[string]any{"model": "deepseek"}})
	if req.Type != "canvas_text" || req.Operation != "storyboard" || req.Model != "deepseek" || req.Prompt != plan.prompt.Text || req.Input["mode"] != "text" {
		t.Fatalf("任务形状：%+v", req)
	}
	input, err := normalizeTaskInput(req.Input)
	if err != nil {
		t.Fatal(err)
	}
	metadata := input["metadata"].(map[string]any)
	step := metadata["dramaStep"].(map[string]any)
	if metadata["source"] != "short-drama-chapter-storyboard" || metadata["chapterId"] != unit.ID || step["step"] != drama.StepChapterShots || step["contract"] != "section.wenxi30@1" || step["contentHash"] != plan.skill.ContentHash || step["version"] != plan.skill.Version || step["styleProfile"] != "live-realism" {
		t.Fatalf("metadata：%+v", step)
	}
	if files := metadata["skillFiles"].([]any); len(files) != 5 {
		t.Fatalf("skillFiles：%+v", files)
	}
	if shots, ok := metadata["dramaShots"].(map[string]any); !ok || shots["genre"] != drama.ShotGenreWenxi30 {
		t.Fatalf("按小节生成的参数：%+v", metadata["dramaShots"])
	}
	if assets := input["canvasAssets"].([]any); len(assets) != 14 {
		t.Fatalf("资产清单 14 项（3 角色 + 3 场景 + 8 道具）：%d", len(assets))
	}
}

// 章节保存的 HTML 按编辑器 getText 的规则还原：段落之间空一行、<br> 换行、空白压缩、实体解码。
func TestChapterEditorText(t *testing.T) {
	cases := map[string]string{
		"<p>## 内·病房走廊·夜</p><p>（夜里）<br>林小满：七床？</p>":      "## 内·病房走廊·夜\n\n（夜里）\n林小满：七床？",
		"<p>  a   b  </p><p></p><p>c &amp; d<br> e</p>": "a b\n\n\n\nc & d\ne",
		"<h2>标题</h2><ul><li><p>一</p></li></ul>":         "标题\n\n\n\n\n\n一",
		"纯文本\n第二行":                                      "纯文本 第二行",
		"":                                              "",
	}
	for in, want := range cases {
		if got := chapterEditorText(in); got != want {
			t.Fatalf("%q → %q，期望 %q", in, got, want)
		}
	}
}

// 项目没有画风快照（只存预设 ID 的老项目）或章节没正文时，不组装、不建任务。
func TestDramaStepRequiresStyleAndText(t *testing.T) {
	svc, db := newChapterShotsTestService(t)
	project, unit := seedQichuang(t, svc, db)
	empty, err := svc.CreateProjectUnit("user-1", project.ID, CreateProjectUnitRequest{Kind: "chapter", Title: "空章"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DramaStepPrompt("user-1", project.ID, empty.ID, drama.StepChapterShots); err == nil || !strings.Contains(err.Error(), "正文") {
		t.Fatalf("没正文：%v", err)
	}
	if _, err := svc.DramaStepPrompt("user-1", project.ID, unit.ID, "episode.story"); err == nil {
		t.Fatal("步骤表里没有的步骤")
	}
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("style_profile_json", "").Error; err != nil {
		t.Fatal(err)
	}
	_, err = svc.DramaStepPrompt("user-1", project.ID, unit.ID, drama.StepChapterShots)
	var appErr *AppError
	if !errors.As(err, &appErr) || !strings.Contains(err.Error(), "画风") {
		t.Fatalf("没画风快照：%v", err)
	}
	if _, err := svc.DramaStepPrompt("user-2", project.ID, unit.ID, drama.StepChapterShots); err == nil {
		t.Fatal("别人的项目")
	}
}

// 边界数据（testdata/chapter-shots-edges，接口 JSON 形状）：画风按小节取舍与截断、角色排序与预算、别名、只有 prompt 的角色、
// 归档、没卡、没主版本、JSON 预览、锚点字符串与去重、分类名、技能卡的必读 / 相关度 / 预算截断 / 二进制与脚本、
// 编辑器 getText 的空白与块分隔、每集时长取整。前端旧实现在同一份数据上的输出是 frontend-prompt.txt。
func TestChapterShotsPromptEdgesMatchFrontend(t *testing.T) {
	const dir = "testdata/chapter-shots-edges"
	load := func(name string, into any) {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("%s：%v", name, err)
		}
	}
	var project model.Project
	var unit model.ProjectUnit
	var assets []ProjectAssetSummary
	var dump struct {
		SkillID   string `json:"skillId"`
		SkillName string `json:"skillName"`
		VersionID string `json:"versionId"`
		Version   string `json:"version"`
		Files     []struct {
			Path   string `json:"path"`
			Kind   string `json:"kind"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
		Contents map[string]struct {
			Content string `json:"content"`
			Binary  bool   `json:"binary"`
		} `json:"contents"`
	}
	load("project.json", &project)
	load("unit.json", &unit)
	load("assets.json", &assets)
	load("skill.json", &dump)
	skill := drama.StepSkill{ID: dump.SkillID, Name: dump.SkillName, VersionID: dump.VersionID, Version: dump.Version}
	for _, f := range dump.Files {
		c := dump.Contents[f.Path]
		skill.Files = append(skill.Files, drama.StepSkillFile{Path: f.Path, Kind: f.Kind, SHA256: f.SHA256, Binary: c.Binary, Content: c.Content})
	}
	plan, err := chapterShotsPlan(&project, &unit, []model.ProjectUnit{unit}, assets, DramaStepUsage{}, skill)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "frontend-prompt.txt"))
	if err != nil {
		t.Fatal(err)
	}
	frontend := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if a, b := normalizeStepPrompt(t, frontend), normalizeStepPrompt(t, plan.prompt.Text); a != b {
		t.Fatalf("边界数据上与前端旧实现不一致：\n%s", firstDiff(a, b))
	}
	if paths := []string{plan.prompt.Files[0].Path, plan.prompt.Files[1].Path, plan.prompt.Files[2].Path}; len(plan.prompt.Files) != 3 || paths[1] != "cards/long.md" || paths[2] != "cards/after.md" {
		t.Fatalf("必读卡在前、预算用完就停：%+v", plan.prompt.Files)
	}
}

// 运行这一步时按场次头切好场写进 metadata.dramaRun；「从第 N 场继续」只认本章没写完的失败任务，场次改过就不让接着写。
func TestDramaRunMetaAndResume(t *testing.T) {
	svc, db := newChapterShotsTestService(t)
	project, unit := seedQichuang(t, svc, db)
	plan, err := svc.planDramaStep("user-1", &project, unit.ID, drama.StepChapterShots, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := newDramaRunMeta(plan, unit.ID)
	if len(plan.scenes) < 2 || run.Turns != len(plan.scenes) || run.ResumeFrom != 1 || run.UnitID != unit.ID || run.RunID == "" {
		t.Fatalf("按场切好：%+v", run)
	}
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	progress := dramaRunProgress{RunID: run.RunID, Total: run.Turns, Completed: 1, Turns: []dramaRunTurn{{Index: 1, Rows: 2, Prompt: "p", Text: "t", TaskID: "prev"}}}
	input, _ := json.Marshal(map[string]any{"mode": "text", "metadata": map[string]any{"chapterId": unit.ID, "dramaStep": map[string]any{"step": drama.StepChapterShots}, "dramaRun": run}})
	result, _ := json.Marshal(map[string]any{"mode": "text", "dramaRun": progress})
	prev := model.Task{ID: "prev", UserID: "user-1", ProjectID: project.ID, Type: "canvas_text", Status: model.TaskStatusFailed, InputJSON: string(input), ResultJSON: string(result), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(&prev).Error; err != nil {
		t.Fatal(err)
	}
	resumed, err := svc.resumeDramaRun("user-1", project.ID, unit.ID, plan, "prev")
	if err != nil || resumed.RunID != run.RunID || resumed.ResumeFrom != 2 || resumed.ResumeTaskID != "prev" {
		t.Fatalf("从第 2 场继续：%v %+v", err, resumed)
	}
	if _, err := svc.resumeDramaRun("user-2", project.ID, unit.ID, plan, "prev"); err == nil {
		t.Fatal("别人的任务不能接着写")
	}
	if _, err := svc.resumeDramaRun("user-1", project.ID, "other-unit", plan, "prev"); err == nil {
		t.Fatal("别的章节不能接着写")
	}
	changed := *plan
	changed.scenes = plan.scenes[1:]
	if _, err := svc.resumeDramaRun("user-1", project.ID, unit.ID, &changed, "prev"); err == nil {
		t.Fatal("场次改过不能接着写")
	}
	db.Model(&model.Task{}).Where("id = ?", "prev").Update("status", model.TaskStatusRunning)
	if _, err := svc.resumeDramaRun("user-1", project.ID, unit.ID, plan, "prev"); err == nil {
		t.Fatal("还在跑的任务不能接着写")
	}
}
