package repository

import (
	"yingce/backend/internal/model"
)

// DramaEvalRun 读一次评测。
func (r *Repository) DramaEvalRun(id string) (*model.DramaEvalRun, error) {
	var run model.DramaEvalRun
	if err := r.db.Where("id = ?", id).First(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

// DramaEvalRunsWithStatus 返回处于这些状态的评测，早的在前（后台推进步骤评测用）。
func (r *Repository) DramaEvalRunsWithStatus(statuses []string, limit int) ([]model.DramaEvalRun, error) {
	var runs []model.DramaEvalRun
	err := r.db.Where("status IN ?", statuses).Order("created_at asc").Limit(limit).Find(&runs).Error
	return runs, err
}

// DramaStepEvalRunsForSkill 返回一个技能包最近的步骤评测，新的在前。
func (r *Repository) DramaStepEvalRunsForSkill(skillID string, limit int) ([]model.DramaEvalRun, error) {
	var runs []model.DramaEvalRun
	err := r.db.Where("skill_id = ?", skillID).Order("created_at desc").Limit(limit).Find(&runs).Error
	return runs, err
}

// UpdateDramaEvalRunFrom 只在评测仍处于 from 里的某个状态时改它，返回是否改到（多个实例同时推进时只有一个落定）。
func (r *Repository) UpdateDramaEvalRunFrom(id string, from []string, values map[string]any) (bool, error) {
	result := r.db.Model(&model.DramaEvalRun{}).Where("id = ? AND status IN ?", id, from).Updates(values)
	return result.RowsAffected > 0, result.Error
}

// UpdateDramaEvalRun 改一次评测的列（启用记录等）。
func (r *Repository) UpdateDramaEvalRun(id string, values map[string]any) error {
	return r.db.Model(&model.DramaEvalRun{}).Where("id = ?", id).Updates(values).Error
}
