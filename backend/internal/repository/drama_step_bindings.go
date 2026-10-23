package repository

import (
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 短剧流程绑定（M6）：风格档案 × 步骤 → 技能，每格一行。

func (r *Repository) DramaStepBindings() ([]model.DramaStepBinding, error) {
	var rows []model.DramaStepBinding
	err := r.db.Order("step asc, style_profile asc").Find(&rows).Error
	return rows, err
}

// DramaStepBinding 取一格；没配时返回 gorm.ErrRecordNotFound。
func (r *Repository) DramaStepBinding(styleProfile string, step string) (*model.DramaStepBinding, error) {
	var row model.DramaStepBinding
	if err := r.db.First(&row, "style_profile = ? AND step = ?", styleProfile, step).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// SaveDramaStepBindings 在一个事务里写入多格：skillID 为空的格子删掉（回到步骤默认技能）。
func (r *Repository) SaveDramaStepBindings(rows []model.DramaStepBinding) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		for _, row := range rows {
			if row.SkillID == "" {
				if err := tx.Where("style_profile = ? AND step = ?", row.StyleProfile, row.Step).Delete(&model.DramaStepBinding{}).Error; err != nil {
					return err
				}
				continue
			}
			row.CreatedAt, row.UpdatedAt = now, now
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "style_profile"}, {Name: "step"}},
				DoUpdates: clause.AssignmentColumns([]string{"skill_id", "updated_by", "updated_at"}),
			}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
