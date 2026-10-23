package repository

import (
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// 内置技能的版本管理：内置包同步出的版本与后台在线编辑出的版本共用 skill_versions，
// skills.current_version_id 指向生效版本，skills.builtin_version_id 指向最近一次内置包版本。

// BuiltinSkillsForAdmin 列出启用中的内置技能（后台「内置技能」页）。
func (r *Repository) BuiltinSkillsForAdmin() ([]model.Skill, error) {
	var skills []model.Skill
	err := r.db.Where("status = ? AND source_type = ?", 1, "builtin").Order("sort_weight desc").Order("id asc").Find(&skills).Error
	return skills, err
}

// SkillVersions 返回一个技能的全部版本，新的在前。
func (r *Repository) SkillVersions(skillID string) ([]model.SkillVersion, error) {
	var versions []model.SkillVersion
	err := r.db.Where("skill_id = ?", skillID).Order("created_at desc").Order("id desc").Find(&versions).Error
	return versions, err
}

// SkillVersionCounts 按技能统计某个来源的版本数。
func (r *Repository) SkillVersionCounts(skillIDs []string, origin string) (map[string]int64, error) {
	out := make(map[string]int64, len(skillIDs))
	if len(skillIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SkillID string
		Count   int64
	}
	if err := r.db.Model(&model.SkillVersion{}).Select("skill_id, count(*) as count").Where("skill_id IN ? AND origin = ?", skillIDs, origin).Group("skill_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.SkillID] = row.Count
	}
	return out, nil
}

// SkillVersionsByIDs 按 ID 取版本（列表页一次取生效版本和内置版本）。
func (r *Repository) SkillVersionsByIDs(ids []string) (map[string]model.SkillVersion, error) {
	out := make(map[string]model.SkillVersion, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var versions []model.SkillVersion
	if err := r.db.Where("id IN ?", ids).Find(&versions).Error; err != nil {
		return nil, err
	}
	for _, v := range versions {
		out[v.ID] = v
	}
	return out, nil
}

// AddSkillVersionWithUpdates 新建一个版本及其文件清单，并在同一事务里只改技能的指定列（不整行保存，免得覆盖启动同步写的目录信息）。
func (r *Repository) AddSkillVersionWithUpdates(version *model.SkillVersion, files []model.SkillFile, skillID string, values map[string]any) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		if len(files) > 0 {
			if err := tx.Create(&files).Error; err != nil {
				return err
			}
		}
		if len(values) == 0 {
			return nil
		}
		return tx.Model(&model.Skill{}).Where("id = ?", skillID).Updates(values).Error
	})
}

// UpdateSkillColumns 只改技能的指定列。
func (r *Repository) UpdateSkillColumns(skillID string, values map[string]any) error {
	return r.db.Model(&model.Skill{}).Where("id = ?", skillID).Updates(values).Error
}

// SetSkillVersionOrigin 回填版本来源（v60 之前同步出的内置版本）。
func (r *Repository) SetSkillVersionOrigin(versionID string, origin string) error {
	return r.db.Model(&model.SkillVersion{}).Where("id = ?", versionID).Update("origin", origin).Error
}
