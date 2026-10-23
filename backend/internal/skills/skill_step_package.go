// 短剧流程步骤读绑定技能的当前版本（M6）：绑定由管理员设置，运行时不看用户有没有添加这个技能。

package skills

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

// StepPackageFile 是技能当前版本的一个文件；Binary 的文件（含超过预览上限的文本）不带内容。
type StepPackageFile struct {
	Path    string
	Kind    string
	SHA256  string
	Binary  bool
	Content string
}

// StepPackage 是技能当前生效版本的全部文件（按路径排序）。
type StepPackage struct {
	SkillID     string
	Name        string
	VersionID   string
	Version     string
	ContentHash string
	Files       []StepPackageFile
}

// StepSkillPackage 读一个技能当前生效版本的全部文件。技能不存在或已停用时返回错误，不回退到别的技能。
func (s *Service) StepSkillPackage(skillID string) (*StepPackage, error) {
	skill, err := s.repo.Skill(strings.TrimSpace(skillID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.BadAuthRequest(fmt.Sprintf("流程技能 %s 不存在，请管理员在「流程技能」里重新绑定", skillID))
	}
	if err != nil {
		return nil, err
	}
	if skill.Status != skillStatusEnabled {
		return nil, kernel.BadAuthRequest(fmt.Sprintf("流程技能「%s」已停用，请管理员在「流程技能」里重新绑定", skill.Name))
	}
	snapshot, err := s.currentSkillSnapshot(skill)
	if err != nil {
		return nil, err
	}
	out := &StepPackage{SkillID: skill.ID, Name: skill.Name, VersionID: snapshot.version.ID, Version: snapshot.version.VersionLabel, ContentHash: snapshot.version.ContentHash, Files: make([]StepPackageFile, 0, len(snapshot.files))}
	for _, file := range snapshot.files {
		item := StepPackageFile{Path: file.Path, Kind: file.Kind, SHA256: file.SHA256}
		if !isPreviewText(file.MimeType, file.Path) || file.Size > maxSkillPreviewBytes {
			item.Binary = true
		} else {
			content, err := s.readSkillPackageSnapshotEntry(snapshot, file.Path)
			if err != nil {
				return nil, err
			}
			item.Content = string(content)
		}
		out.Files = append(out.Files, item)
	}
	return out, nil
}

// currentSkillSnapshot 是技能当前版本的文件清单（与 loadSkillPackageSnapshot 同一套校验，不做可见性判断）。
func (s *Service) currentSkillSnapshot(skill *model.Skill) (*skillPackageSnapshot, error) {
	version, err := s.repo.SkillVersion(skill.CurrentVersionID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return legacySkillPackageSnapshot(skill)
	}
	if err != nil {
		return nil, err
	}
	if version.SkillID != skill.ID {
		return nil, fmt.Errorf("技能 %s 当前版本归属不一致", skill.ID)
	}
	files, err := s.repo.SkillFiles(version.ID)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return legacySkillPackageSnapshot(skill)
	}
	if err := validateSkillPackageSnapshot(s, skill, version, files); err != nil {
		return nil, err
	}
	return &skillPackageSnapshot{skill: skill, version: version, files: files}, nil
}
