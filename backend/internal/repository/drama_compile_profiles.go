package repository

import (
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// 编译档案（M5）：版本号全局递增，同一时刻最多一个启用。

func (r *Repository) DramaCompileProfiles() ([]model.DramaCompileProfile, error) {
	var profiles []model.DramaCompileProfile
	err := r.db.Order("version desc").Find(&profiles).Error
	return profiles, err
}

func (r *Repository) DramaCompileProfile(id string) (*model.DramaCompileProfile, error) {
	var profile model.DramaCompileProfile
	if err := r.db.First(&profile, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

func (r *Repository) DramaCompileProfileByVersion(version int) (*model.DramaCompileProfile, error) {
	var profile model.DramaCompileProfile
	if err := r.db.First(&profile, "version = ?", version).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

// ActiveDramaCompileProfile 返回启用中的档案；没有返回 gorm.ErrRecordNotFound（调用方用内置默认）。
func (r *Repository) ActiveDramaCompileProfile() (*model.DramaCompileProfile, error) {
	var profile model.DramaCompileProfile
	if err := r.db.Where("enabled = ?", true).Order("version desc").First(&profile).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

// CreateDramaCompileProfile 取下一个版本号新建；启用时同一事务停用其他版本。
func (r *Repository) CreateDramaCompileProfile(profile *model.DramaCompileProfile) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var latest struct{ Version int }
		if err := tx.Model(&model.DramaCompileProfile{}).Select("COALESCE(MAX(version), 0) AS version").Scan(&latest).Error; err != nil {
			return err
		}
		profile.Version = latest.Version + 1
		if profile.Enabled {
			if err := tx.Model(&model.DramaCompileProfile{}).Where("enabled = ?", true).Updates(map[string]any{"enabled": false, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
		}
		return tx.Create(profile).Error
	})
}

// ActivateDramaCompileProfile 启用一个版本并停用其他版本；id 为空时全部停用（回退到内置默认）。
func (r *Repository) ActivateDramaCompileProfile(id string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Model(&model.DramaCompileProfile{}).Where("enabled = ? AND id <> ?", true, id).Updates(map[string]any{"enabled": false, "updated_at": now}).Error; err != nil {
			return err
		}
		if id == "" {
			return nil
		}
		result := tx.Model(&model.DramaCompileProfile{}).Where("id = ?", id).Updates(map[string]any{"enabled": true, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// DeleteDramaCompileProfile 只删停用中的版本。
func (r *Repository) DeleteDramaCompileProfile(id string) (bool, error) {
	result := r.db.Where("id = ? AND enabled = ?", id, false).Delete(&model.DramaCompileProfile{})
	return result.RowsAffected > 0, result.Error
}

// ProjectByID 不按用户取项目（只给管理员评测用，调用方先校验管理员）。
func (r *Repository) ProjectByID(id string) (*model.Project, error) {
	var project model.Project
	if err := r.db.First(&project, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &project, nil
}

// DramaShotlistsForProject 列出一个项目的全部镜头表。
func (r *Repository) DramaShotlistsForProject(projectID string) ([]model.DramaShotlist, error) {
	var shotlists []model.DramaShotlist
	err := r.db.Where("project_id = ?", projectID).Find(&shotlists).Error
	return shotlists, err
}

func (r *Repository) CreateDramaEvalRun(run *model.DramaEvalRun) error {
	return r.db.Create(run).Error
}

// DramaEvalRuns 返回最近的评测记录，新的在前。
func (r *Repository) DramaEvalRuns(limit int) ([]model.DramaEvalRun, error) {
	var runs []model.DramaEvalRun
	err := r.db.Order("created_at desc").Limit(limit).Find(&runs).Error
	return runs, err
}
