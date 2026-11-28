package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

var testDramaAdmin = &model.User{ID: "admin-drama", Role: model.UserRoleAdmin, Username: "admin-drama", Email: "admin-drama@example.com"}

// seedSkill 建一个技能（legacy 单文件 SKILL.md）；owner 是管理员时是管理员私有的，普通用户看不到、也不能直接读。
func seedSkill(t *testing.T, fake *fakeDramaBackend, id string, owner string, name string, content string) {
	t.Helper()
	if err := fake.db.Create(&model.Skill{ID: id, OwnerID: owner, Name: name, IsPrivate: true, Instruction: content, Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
}

func configureDramaStepSkills(t *testing.T, svc *Service, options ...DramaSkillChoiceOption) DramaStepSkillConfig {
	t.Helper()
	config, err := svc.UpdateAdminDramaStepSkills(testDramaAdmin, DramaStepSkillConfig{Options: options})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	return config
}

func appStatus(err error) int {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Status
	}
	return 0
}

// 后台配置：只有管理员能改；步骤、技能、文件都要有效，同一步不重复；名字缺省取技能名；读回只返回保存的内容。
func TestAdminDramaStepSkillsValidation(t *testing.T) {
	svc, fake := newDramaTestService(t)
	seedSkill(t, fake, "skill-plan", testDramaAdmin.ID, "分集方法论", "按情绪起伏分集，每集结尾留钩子。")
	user := &model.User{ID: "usr-drama", Role: model.UserRoleUser}
	if _, err := svc.UpdateAdminDramaStepSkills(user, DramaStepSkillConfig{}); appStatus(err) != 403 {
		t.Fatalf("non-admin must be refused: %v", err)
	}
	if _, err := svc.AdminDramaStepSkills(user); appStatus(err) != 403 {
		t.Fatalf("non-admin read must be refused: %v", err)
	}
	bad := []struct {
		name   string
		option DramaSkillChoiceOption
		want   string
	}{
		{"unknown step", DramaSkillChoiceOption{Step: "video", SkillID: "skill-plan", Enabled: true}, "步骤无效"},
		{"no skill", DramaSkillChoiceOption{Step: DramaStepPlan, Enabled: true}, "没有选技能"},
		{"missing skill", DramaSkillChoiceOption{Step: DramaStepPlan, SkillID: "nope", Enabled: true}, "不可用"},
		{"missing file", DramaSkillChoiceOption{Step: DramaStepPlan, SkillID: "skill-plan", Path: "other.md", Enabled: true}, "不可用"},
	}
	for _, c := range bad {
		if _, err := svc.UpdateAdminDramaStepSkills(testDramaAdmin, DramaStepSkillConfig{Options: []DramaSkillChoiceOption{c.option}}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
	dup := DramaSkillChoiceOption{Step: DramaStepPlan, SkillID: "skill-plan", Enabled: true}
	if _, err := svc.UpdateAdminDramaStepSkills(testDramaAdmin, DramaStepSkillConfig{Options: []DramaSkillChoiceOption{dup, dup}}); err == nil || !strings.Contains(err.Error(), "配置了两次") {
		t.Fatalf("duplicate: %v", err)
	}
	config := configureDramaStepSkills(t, svc, dup, DramaSkillChoiceOption{Step: DramaStepBible, SkillID: "skill-plan", Label: "提取法", Enabled: false})
	if len(config.Options) != 2 || config.Options[0].Path != "SKILL.md" || config.Options[0].Label != "分集方法论" || config.Options[1].Label != "提取法" || config.UpdatedAt == nil {
		t.Fatalf("saved config: %+v", config)
	}
	read, err := svc.AdminDramaStepSkills(testDramaAdmin)
	if err != nil || len(read.Options) != 2 {
		t.Fatalf("read back: %+v %v", read, err)
	}
	// 用户目录只列启用的。
	catalog, err := svc.DramaStepSkillCatalog("usr-drama")
	if err != nil || len(catalog.Options) != 1 || catalog.Options[0].Step != DramaStepPlan {
		t.Fatalf("catalog: %+v %v", catalog, err)
	}
}

// 作品设置：四步都能选后台技能（含分集规划），名字按后台写回；不在清单里的、新选的自选技能拒绝；
// 没改动的早先自选技能可以保留；保存后记成这个用户的「上次选择」。
func TestDramaSettingsStepSkillChoices(t *testing.T) {
	svc, fake := newDramaTestService(t)
	seedSkill(t, fake, "skill-plan", testDramaAdmin.ID, "分集方法论", "按情绪起伏分集。")
	seedSkill(t, fake, "skill-script", testDramaAdmin.ID, "拆镜法", "一镜一事。")
	configureDramaStepSkills(t, svc,
		DramaSkillChoiceOption{Step: DramaStepPlan, SkillID: "skill-plan", Label: "后台分集", Enabled: true},
		DramaSkillChoiceOption{Step: DramaStepScript, SkillID: "skill-script", Enabled: true},
	)
	summary := createTestDrama(t, svc, DramaReviewAuto)
	settings := summary.Production.Settings
	settings.PlanSkill = &DramaSkillRef{SkillID: "skill-plan", SkillName: "客户端改的名字", Source: DramaSkillSourceSystem}
	settings.ScriptSkill = &DramaSkillRef{SkillID: "skill-script", Source: DramaSkillSourceSystem}
	updated, err := svc.UpdateDramaSettings("usr-drama", summary.Project.ID, UpdateDramaSettingsRequest{Settings: &settings})
	if err != nil {
		t.Fatalf("admin skills must be selectable even though the user cannot see them: %v", err)
	}
	saved := updated.Production.Settings
	if saved.PlanSkill == nil || saved.PlanSkill.SkillName != "后台分集" || saved.PlanSkill.Path != "SKILL.md" || saved.ScriptSkill == nil || saved.ScriptSkill.SkillName != "拆镜法" {
		t.Fatalf("saved skills: %+v %+v", saved.PlanSkill, saved.ScriptSkill)
	}
	// 上次选择：新建作品默认按它。
	catalog, err := svc.DramaStepSkillCatalog("usr-drama")
	if err != nil || catalog.Last.Plan == nil || catalog.Last.Plan.SkillID != "skill-plan" || catalog.Last.Script == nil || catalog.Last.Bible != nil || catalog.Last.Prompt != nil || len(catalog.Unavailable) != 0 {
		t.Fatalf("last choice: %+v %v", catalog, err)
	}
	// 不在清单里（这一步没配这个技能）的后台技能拒绝。
	wrongStep := saved
	wrongStep.BibleSkill = &DramaSkillRef{SkillID: "skill-plan", Source: DramaSkillSourceSystem}
	if _, err := svc.UpdateDramaSettings("usr-drama", summary.Project.ID, UpdateDramaSettingsRequest{Settings: &wrongStep}); err == nil || !strings.Contains(err.Error(), "不在后台可选清单里") {
		t.Fatalf("option of another step: %v", err)
	}
	// 新选用户自己技能库里的技能：拒绝。
	ownSkill := saved
	ownSkill.PromptSkill = &DramaSkillRef{SkillID: "skill-script"}
	if _, err := svc.UpdateDramaSettings("usr-drama", summary.Project.ID, UpdateDramaSettingsRequest{Settings: &ownSkill}); err == nil || !strings.Contains(err.Error(), "只能选择系统默认或后台配置的技能") {
		t.Fatalf("new library skill: %v", err)
	}
	// 早先自选、这次没改动的技能可以保留（老作品改别的设置不被卡住）。
	seedSkill(t, fake, "own-skill", "usr-drama", "我的提示词法", "逐镜时间码。")
	legacy := &DramaSkillRef{SkillID: "own-skill", SkillName: "我的提示词法", Path: "SKILL.md"}
	raw := saved
	raw.PromptSkill = legacy
	if err := svc.repo.UpdateDramaProduction(summary.Project.ID, map[string]any{"settings_json": encodeDramaSettings(raw)}); err != nil {
		t.Fatal(err)
	}
	keep := raw
	keep.Concurrency = 3
	if _, err := svc.UpdateDramaSettings("usr-drama", summary.Project.ID, UpdateDramaSettingsRequest{Settings: &keep}); err != nil {
		t.Fatalf("unchanged legacy skill must be kept: %v", err)
	}
	// 「上次选择」只记后台技能，早先自选的不作为新作品默认。
	if catalog, _ := svc.DramaStepSkillCatalog("usr-drama"); catalog.Last.Prompt != nil {
		t.Fatalf("legacy skill recorded as default: %+v", catalog.Last.Prompt)
	}
	// 新建作品只能选系统默认或后台技能。
	create := DramaSettings{ReviewMode: DramaReviewAuto, Models: fakeDramaModels, PromptSkill: legacy}
	if _, err := svc.CreateDrama("usr-drama", CreateDramaRequest{Name: "新作", Settings: create, Chapters: []DramaChapterInput{{Title: "第一章", Text: "正文"}}}); err == nil || !strings.Contains(err.Error(), "只能选择系统默认或后台配置的技能") {
		t.Fatalf("create with library skill: %v", err)
	}
}

// 执行：分集规划把后台技能正文交给文本模型（技能是管理员私有的，用户看不到也照样读到）；管理员停用后，
// 下一次执行到这一步明确报「已下架」，不悄悄按内置规则跑；目录里这个用户的上次选择变成不可用。
func TestDramaPlanUsesAdminSkillAndDelistFailsLoudly(t *testing.T) {
	svc, fake := newDramaTestService(t)
	seedSkill(t, fake, "skill-plan", testDramaAdmin.ID, "分集方法论", "分集技能正文：每集结尾必须停在一个没有回答的问题上。")
	option := DramaSkillChoiceOption{Step: DramaStepPlan, SkillID: "skill-plan", Enabled: true}
	configureDramaStepSkills(t, svc, option)
	settings := DramaSettings{ReviewMode: DramaReviewAuto, MediaMode: DramaMediaManual, Concurrency: 16, Models: fakeDramaModels}
	settings.EpisodeSeconds = 60
	settings.PlanSkill = &DramaSkillRef{SkillID: "skill-plan", Source: DramaSkillSourceSystem}
	summary, err := svc.CreateDrama("usr-drama", CreateDramaRequest{Name: "分集技能", Start: true, Settings: settings, Chapters: []DramaChapterInput{{Title: "第一章", Text: "萧炎……"}, {Title: "第二章", Text: "纳兰……"}}})
	if err != nil {
		t.Fatal(err)
	}
	runDramaTicks(t, svc, summary.Project.ID, 6)
	var plan *CreateTaskRequest
	for i := range fake.requests {
		if strings.Contains(fake.requests[i].Prompt, "下一集编号") {
			plan = &fake.requests[i]
		}
	}
	if plan == nil {
		t.Fatal("plan step did not run")
	}
	if !strings.Contains(plan.Prompt, "【写作技能：分集方法论】") || !strings.Contains(plan.Prompt, "分集技能正文：每集结尾必须停在一个没有回答的问题上。") || !strings.Contains(plan.Prompt, `{"episodes":`) {
		t.Fatalf("plan prompt lacks the admin skill: %s", plan.Prompt)
	}
	input, err := json.Marshal(plan.Input)
	if err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "test-protected-skill", UserID: "usr-drama", Type: "canvas_text", Status: model.TaskStatusFailed, Prompt: plan.Prompt, InputJSON: string(input)}
	for _, taskInput := range []string{task.InputJSON, publicTaskInputJSON(task.InputJSON)} {
		task.InputJSON = taskInput
		for _, output := range []any{taskSummaryForOutput(task), taskForOutput(task), dramaJobTaskView(&task, nil)} {
			encoded, err := json.Marshal(output)
			if err != nil || strings.Contains(string(encoded), "分集技能正文") || !strings.Contains(string(encoded), "已应用后台配置的写作技能") {
				t.Fatalf("protected prompt output is invalid: %s, %v", encoded, err)
			}
		}
	}
	if err := fake.db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	// 下架（停用）后：读取这一步技能明确报错。
	option.Enabled = false
	configureDramaStepSkills(t, svc, option)
	if _, err := svc.taskLifecycle().retryTask("usr-drama", task.ID); err == nil || !strings.Contains(err.Error(), "回到短剧流水线") {
		t.Fatalf("generic retry must not replay a protected prompt after delisting: %v", err)
	}
	if _, err := svc.dramaWritingSkill("usr-drama", DramaStepPlan, settings.PlanSkill); err == nil || !strings.Contains(err.Error(), "已被管理员下架或停用") {
		t.Fatalf("delisted skill must fail loudly: %v", err)
	}
	catalog, err := svc.DramaStepSkillCatalog("usr-drama")
	if err != nil || catalog.Last.Plan != nil || len(catalog.Unavailable) != 1 || catalog.Unavailable[0] != "分集规划" {
		t.Fatalf("delisted last choice: %+v %v", catalog, err)
	}
}
