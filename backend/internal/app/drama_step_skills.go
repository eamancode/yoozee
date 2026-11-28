package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/production"

	"gorm.io/gorm"
)

// 短剧文字步骤的技能：管理员在后台为每个文字步骤（分集规划、资产提取、分镜拆分、视频提示词）配置可选技能，
// 用户在制作设置里每步选「系统默认」（内置规则）或其中一个。新建作品时每步默认取这个用户上次的选择。
//
// 配置存在 system_settings（drama_step_skills）；每个用户上次的选择存在 drama_step_skills_last:<userId>。
// 执行时后台配置的技能按服务端授权读取（不要求用户自己能看到这个技能），并且只在它仍在清单里、启用时读取，
// 下架后明确报错让用户改选，不悄悄退回内置规则。技能内容随管理员修改，下一次执行读最新版本。

const (
	dramaStepSkillsSettingKey    = "drama_step_skills"
	dramaStepSkillsLastKeyPrefix = "drama_step_skills_last:"
	dramaStepSkillsPerStepLimit  = 20
	dramaProtectedSkillKey       = "dramaProtectedSkill"
)

// 保留给 worker 的真实提示词，但任务列表、详情和制作台不展示内含后台技能的原始请求。
func protectDramaSkillInput(input map[string]any, ref *DramaSkillRef) map[string]any {
	if ref != nil && ref.Source == DramaSkillSourceSystem {
		metadata, _ := input["metadata"].(map[string]any)
		if metadata == nil {
			metadata = map[string]any{}
			input["metadata"] = metadata
		}
		metadata[dramaProtectedSkillKey] = true
	}
	return input
}

func taskHasProtectedDramaSkill(raw string) bool {
	var input struct {
		Metadata struct {
			Source         string `json:"source"`
			ProtectedSkill bool   `json:"dramaProtectedSkill"`
		} `json:"metadata"`
	}
	return json.Unmarshal([]byte(raw), &input) == nil &&
		input.Metadata.Source == "drama_production" && input.Metadata.ProtectedSkill
}

func taskDisplayPrompt(task model.Task) string {
	if taskHasProtectedDramaSkill(task.InputJSON) {
		return "已应用后台配置的写作技能，完整请求仅在后台可见"
	}
	return task.Prompt
}

// dramaSkillSteps 是可以挂技能的文字步骤，顺序即界面顺序。
var dramaSkillSteps = []struct{ Step, Label string }{
	{DramaStepPlan, "分集规划"},
	{DramaStepBible, "资产提取"},
	{DramaStepScript, "分镜拆分"},
	{DramaStepPrompt, "视频提示词"},
}

func dramaSkillStepLabel(step string) string {
	for _, item := range dramaSkillSteps {
		if item.Step == step {
			return item.Label
		}
	}
	return ""
}

// DramaSkillChoiceOption 是后台给某个步骤配置的一个可选技能：哪个技能、用其中哪个文本文件、给用户看的名字。
type DramaSkillChoiceOption struct {
	Step        string `json:"step"`
	SkillID     string `json:"skillId"`
	Path        string `json:"path"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// DramaStepSkillConfig 是后台的「短剧步骤技能」配置。
type DramaStepSkillConfig struct {
	Options   []DramaSkillChoiceOption `json:"options"`
	UpdatedAt *time.Time               `json:"updatedAt,omitempty"`
}

// DramaStepSkillChoices 是四个文字步骤各自的技能（nil = 系统默认）。
type DramaStepSkillChoices struct {
	Plan   *DramaSkillRef `json:"plan,omitempty"`
	Bible  *DramaSkillRef `json:"bible,omitempty"`
	Script *DramaSkillRef `json:"script,omitempty"`
	Prompt *DramaSkillRef `json:"prompt,omitempty"`
}

// DramaStepSkillCatalog 是用户在制作设置里能选的技能：每步启用的后台技能，加上这个用户上次的选择。
// Last 里只留仍然可选的；上次选的技能已经下架的，步骤名写进 Unavailable，前端提示后按系统默认处理。
type DramaStepSkillCatalog struct {
	Options     []DramaSkillChoiceOption `json:"options"`
	Last        DramaStepSkillChoices    `json:"last"`
	Unavailable []string                 `json:"unavailable,omitempty"`
}

func (c DramaStepSkillChoices) get(step string) *DramaSkillRef {
	switch step {
	case DramaStepPlan:
		return c.Plan
	case DramaStepBible:
		return c.Bible
	case DramaStepScript:
		return c.Script
	case DramaStepPrompt:
		return c.Prompt
	}
	return nil
}

func (c *DramaStepSkillChoices) set(step string, ref *DramaSkillRef) {
	switch step {
	case DramaStepPlan:
		c.Plan = ref
	case DramaStepBible:
		c.Bible = ref
	case DramaStepScript:
		c.Script = ref
	case DramaStepPrompt:
		c.Prompt = ref
	}
}

// skillChoices / withSkillChoices 在作品设置和四步技能之间互转。
func (s DramaSettings) skillChoices() DramaStepSkillChoices {
	return DramaStepSkillChoices{Plan: s.PlanSkill, Bible: s.BibleSkill, Script: s.ScriptSkill, Prompt: s.PromptSkill}
}

func (s DramaSettings) withSkillChoices(c DramaStepSkillChoices) DramaSettings {
	s.PlanSkill, s.BibleSkill, s.ScriptSkill, s.PromptSkill = c.Plan, c.Bible, c.Script, c.Prompt
	return s
}

func sameDramaSkillRef(a, b *DramaSkillRef) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.SkillID == b.SkillID && a.Path == b.Path && a.Source == b.Source
}

// matchDramaStepSkill 找配置里这一步、这个技能文件、启用的那一项。
func (c DramaStepSkillConfig) match(step string, ref *DramaSkillRef) *DramaSkillChoiceOption {
	if ref == nil {
		return nil
	}
	for i := range c.Options {
		option := &c.Options[i]
		if option.Enabled && option.Step == step && option.SkillID == ref.SkillID && option.Path == ref.Path {
			return option
		}
	}
	return nil
}

func (s *Service) dramaStepSkillConfig() (DramaStepSkillConfig, error) {
	setting, err := s.repo.SystemSetting(dramaStepSkillsSettingKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DramaStepSkillConfig{Options: []DramaSkillChoiceOption{}}, nil
	}
	if err != nil {
		return DramaStepSkillConfig{}, err
	}
	var value DramaStepSkillConfig
	if json.Unmarshal([]byte(setting.ValueJSON), &value) != nil {
		return DramaStepSkillConfig{}, errors.New("短剧步骤技能配置格式无效")
	}
	if value.Options == nil {
		value.Options = []DramaSkillChoiceOption{}
	}
	updatedAt := setting.UpdatedAt
	value.UpdatedAt = &updatedAt
	return value, nil
}

// AdminDramaStepSkills 是后台「短剧步骤技能」页读取的配置。
func (s *Service) AdminDramaStepSkills(actor *model.User) (DramaStepSkillConfig, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return DramaStepSkillConfig{}, err
	}
	return s.dramaStepSkillConfig()
}

// UpdateAdminDramaStepSkills 整体替换配置：每项的技能和文件都要存在、是文本，步骤内不重复。
func (s *Service) UpdateAdminDramaStepSkills(actor *model.User, req DramaStepSkillConfig) (DramaStepSkillConfig, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return DramaStepSkillConfig{}, err
	}
	options := make([]DramaSkillChoiceOption, 0, len(req.Options))
	perStep := map[string]int{}
	seen := map[string]bool{}
	for i, raw := range req.Options {
		option := DramaSkillChoiceOption{
			Step:        strings.TrimSpace(raw.Step),
			SkillID:     truncateRunes(strings.TrimSpace(raw.SkillID), 64),
			Path:        truncateRunes(strings.TrimSpace(raw.Path), 300),
			Label:       truncateRunes(strings.TrimSpace(raw.Label), 60),
			Description: truncateRunes(strings.TrimSpace(raw.Description), 200),
			Enabled:     raw.Enabled,
		}
		label := dramaSkillStepLabel(option.Step)
		if label == "" {
			return DramaStepSkillConfig{}, BadAuthRequest(fmt.Sprintf("第 %d 项的步骤无效", i+1))
		}
		if option.SkillID == "" {
			return DramaStepSkillConfig{}, BadAuthRequest(fmt.Sprintf("第 %d 项（%s）没有选技能", i+1, label))
		}
		if option.Path == "" {
			option.Path = "SKILL.md"
		}
		key := option.Step + "\x00" + option.SkillID + "\x00" + option.Path
		if seen[key] {
			return DramaStepSkillConfig{}, BadAuthRequest(fmt.Sprintf("%s里同一个技能文件配置了两次", label))
		}
		seen[key] = true
		if perStep[option.Step]++; perStep[option.Step] > dramaStepSkillsPerStepLimit {
			return DramaStepSkillConfig{}, BadAuthRequest(fmt.Sprintf("%s最多配置 %d 个技能", label, dramaStepSkillsPerStepLimit))
		}
		file, err := s.trustedSkillPackageFile(option.SkillID, option.Path)
		if err != nil {
			return DramaStepSkillConfig{}, BadAuthRequest(fmt.Sprintf("%s的技能文件 %s 不可用：%s", label, option.Path, err.Error()))
		}
		if file.Binary || strings.TrimSpace(file.Content) == "" {
			return DramaStepSkillConfig{}, BadAuthRequest(fmt.Sprintf("%s的技能文件 %s 不是文本或为空", label, option.Path))
		}
		if option.Label == "" {
			if skill, err := s.repo.Skill(option.SkillID); err == nil {
				option.Label = truncateRunes(strings.TrimSpace(skill.Name), 60)
			}
			option.Label = firstNonEmpty(option.Label, option.SkillID)
		}
		options = append(options, option)
	}
	encoded, err := json.Marshal(DramaStepSkillConfig{Options: options})
	if err != nil {
		return DramaStepSkillConfig{}, err
	}
	setting := model.SystemSetting{Key: dramaStepSkillsSettingKey, ValueJSON: string(encoded), UpdatedBy: actor.ID}
	current, err := s.repo.SystemSetting(dramaStepSkillsSettingKey)
	if err == nil {
		setting.CreatedAt = current.CreatedAt
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return DramaStepSkillConfig{}, err
	}
	if err := s.repo.SaveSystemSetting(&setting); err != nil {
		return DramaStepSkillConfig{}, err
	}
	enabled := 0
	for _, option := range options {
		if option.Enabled {
			enabled++
		}
	}
	summary := fmt.Sprintf("更新短剧步骤技能：%d 项，启用 %d 项", len(options), enabled)
	if err := s.appendAdminAudit(actor, "drama_step_skills.update", "system_setting", dramaStepSkillsSettingKey, summary, map[string]any{"options": len(options), "enabled": enabled}); err != nil {
		return DramaStepSkillConfig{}, err
	}
	return s.dramaStepSkillConfig()
}

// DramaStepSkillCatalog 是制作设置里能选的技能和这个用户上次的选择。
func (s *Service) DramaStepSkillCatalog(userID string) (DramaStepSkillCatalog, error) {
	config, err := s.dramaStepSkillConfig()
	if err != nil {
		return DramaStepSkillCatalog{}, err
	}
	catalog := DramaStepSkillCatalog{Options: []DramaSkillChoiceOption{}}
	for _, option := range config.Options {
		if option.Enabled {
			catalog.Options = append(catalog.Options, option)
		}
	}
	last := s.lastDramaStepSkills(userID)
	for _, item := range dramaSkillSteps {
		ref := last.get(item.Step)
		if ref == nil {
			continue
		}
		option := config.match(item.Step, ref)
		if option == nil {
			catalog.Unavailable = append(catalog.Unavailable, item.Label)
			continue
		}
		catalog.Last.set(item.Step, &DramaSkillRef{SkillID: option.SkillID, SkillName: option.Label, Path: option.Path, Source: DramaSkillSourceSystem})
	}
	return catalog, nil
}

func (s *Service) lastDramaStepSkills(userID string) DramaStepSkillChoices {
	setting, err := s.repo.SystemSetting(dramaStepSkillsLastKeyPrefix + userID)
	if err != nil {
		return DramaStepSkillChoices{}
	}
	var value DramaStepSkillChoices
	if json.Unmarshal([]byte(setting.ValueJSON), &value) != nil {
		return DramaStepSkillChoices{}
	}
	return value
}

// recordDramaStepSkills 记下这个用户这次保存的每步技能，新建作品时作为默认。只记后台技能，系统默认记为空；
// 早先从自己技能库选的技能不作为新作品的默认（新作品只能选后台技能）。记录失败不影响保存本身。
func (s *Service) recordDramaStepSkills(userID string, settings DramaSettings) {
	choices := settings.skillChoices()
	var last DramaStepSkillChoices
	for _, item := range dramaSkillSteps {
		if ref := choices.get(item.Step); ref != nil && ref.Source == DramaSkillSourceSystem {
			copied := *ref
			last.set(item.Step, &copied)
		}
	}
	encoded, err := json.Marshal(last)
	if err == nil {
		key := dramaStepSkillsLastKeyPrefix + userID
		setting := model.SystemSetting{Key: key, ValueJSON: string(encoded), UpdatedBy: userID}
		if current, readErr := s.repo.SystemSetting(key); readErr == nil {
			setting.CreatedAt = current.CreatedAt
		}
		err = s.repo.SaveSystemSetting(&setting)
	}
	if err != nil {
		slog.Warn("drama step skills: record last choice failed", "user", userID, "error", err)
	}
}

// validateDramaSkills 校验作品设置里四个文字步骤的技能：只能是系统默认、后台清单里启用的技能，或者没改动的
// 早先自选技能（current 是作品保存前的设置；新建作品为 nil）。后台技能的名字按后台配置写回，并确认能读到正文。
func (s *Service) validateDramaSkills(userID string, next DramaSettings, current *DramaSettings) (DramaSettings, error) {
	config, err := s.dramaStepSkillConfig()
	if err != nil {
		return next, err
	}
	choices := next.skillChoices()
	var previous DramaStepSkillChoices
	if current != nil {
		previous = current.skillChoices()
	}
	for _, item := range dramaSkillSteps {
		ref := choices.get(item.Step)
		if ref == nil {
			continue
		}
		if ref.Source == DramaSkillSourceSystem {
			option := config.match(item.Step, ref)
			if option == nil {
				return next, BadAuthRequest(fmt.Sprintf("%s技能「%s」不在后台可选清单里（可能已下架），请改选", item.Label, firstNonEmpty(ref.SkillName, ref.SkillID)))
			}
			ref.SkillName = option.Label
		} else if !sameDramaSkillRef(ref, previous.get(item.Step)) {
			return next, BadAuthRequest(item.Label + "只能选择系统默认或后台配置的技能")
		}
		if _, err := s.dramaWritingSkill(userID, item.Step, ref); err != nil {
			return next, err
		}
		choices.set(item.Step, ref)
	}
	return next.withSkillChoices(choices), nil
}

// dramaWritingSkill 读取作品为某个文字步骤选定的写作技能；技能不可读或已下架时报错，不悄悄退回内置写法。
func (s *Service) dramaWritingSkill(userID string, step string, ref *DramaSkillRef) (*production.WritingSkill, error) {
	if ref == nil || strings.TrimSpace(ref.SkillID) == "" {
		return nil, nil
	}
	label := dramaSkillStepLabel(step)
	name := firstNonEmpty(ref.SkillName, ref.SkillID)
	var file *SkillPackageFileContent
	if ref.Source == DramaSkillSourceSystem {
		config, err := s.dramaStepSkillConfig()
		if err != nil {
			return nil, err
		}
		option := config.match(step, ref)
		if option == nil {
			return nil, BadAuthRequest(fmt.Sprintf("%s技能「%s」已被管理员下架或停用，请到制作设置里改选", label, name))
		}
		name = option.Label
		if file, err = s.trustedSkillPackageFile(ref.SkillID, ref.Path); err != nil {
			return nil, BadAuthRequest(label + "技能「" + name + "」不可用：" + err.Error())
		}
	} else {
		// 早先从用户自己技能库选的技能：按技能的归属及发布权限读取。
		var err error
		if file, err = s.SkillPackageFile(userID, ref.SkillID, ref.Path); err != nil {
			var appErr *AppError
			if errors.As(err, &appErr) && (appErr.Status == 401 || appErr.Status == 403) {
				return nil, err
			}
			return nil, BadAuthRequest(label + "技能「" + name + "」不可用：" + err.Error())
		}
	}
	if file.Binary || strings.TrimSpace(file.Content) == "" {
		return nil, BadAuthRequest(label + "技能文件 " + ref.Path + " 不是文本或为空")
	}
	return &production.WritingSkill{Name: name, Path: ref.Path, Content: file.Content}, nil
}
