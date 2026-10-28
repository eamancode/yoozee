package repository

import (
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

// DramaStyleLibrary 列出平台风格库（团队共享，不按用户过滤）：projectType 为空时列全部，按类型、更新时间倒序。
func (r *Repository) DramaStyleLibrary(projectType string) ([]model.DramaStyleLibraryEntry, error) {
	var rows []model.DramaStyleLibraryEntry
	query := r.db.Model(&model.DramaStyleLibraryEntry{})
	if projectType != "" {
		query = query.Where("project_type = ?", projectType)
	}
	err := query.Order("project_type ASC").Order("updated_at DESC").Order("id ASC").Find(&rows).Error
	return rows, err
}

// DramaStyleLibraryEntry 取一条；不存在返回 gorm.ErrRecordNotFound。
func (r *Repository) DramaStyleLibraryEntry(id string) (*model.DramaStyleLibraryEntry, error) {
	var row model.DramaStyleLibraryEntry
	if err := r.db.Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// DramaStyleLibraryNameTaken 是同一类型里有没有同名条目（exceptID 是正在改名的那一条）。
func (r *Repository) DramaStyleLibraryNameTaken(projectType string, name string, exceptID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.DramaStyleLibraryEntry{}).Where("project_type = ? AND name = ? AND id <> ?", projectType, name, exceptID).Count(&count).Error
	return count > 0, err
}

func (r *Repository) CreateDramaStyleLibraryEntry(row *model.DramaStyleLibraryEntry) error {
	return r.db.Create(row).Error
}

// UpdateDramaStyleLibraryEntry 改一条的字段；不存在返回 gorm.ErrRecordNotFound。
func (r *Repository) UpdateDramaStyleLibraryEntry(id string, values map[string]any, now time.Time) error {
	values["updated_at"] = now
	result := r.db.Model(&model.DramaStyleLibraryEntry{}).Where("id = ?", id).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteDramaStyleLibraryEntry 删一条；不存在返回 gorm.ErrRecordNotFound。
func (r *Repository) DeleteDramaStyleLibraryEntry(id string) error {
	result := r.db.Where("id = ?", id).Delete(&model.DramaStyleLibraryEntry{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ApplyDramaStyleLock 在一个事务里写项目的锁定风格：bible_json，和主基调色卡资产（新建资产、第 1 个版本、项目关联，
// 或给已有的主色卡加一个版本并换成主版本）。
func (r *Repository) ApplyDramaStyleLock(userID string, projectID string, bibleJSON string, palette DramaPaletteWrite, now time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Project{}).Where("id = ? AND user_id = ?", projectID, userID).Updates(map[string]any{"bible_json": bibleJSON, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if palette.Link != nil {
			if err := tx.Create(palette.Asset).Error; err != nil {
				return err
			}
			if err := tx.Create(palette.Version).Error; err != nil {
				return err
			}
			if err := tx.Create(palette.Link).Error; err != nil {
				return err
			}
		} else if palette.Asset != nil {
			if err := tx.Create(palette.Version).Error; err != nil {
				return err
			}
			updated := tx.Model(&model.Asset{}).
				Where("id = ? AND user_id = ? AND category = ?", palette.Asset.ID, userID, model.AssetCategoryPalette).
				Where("id IN (?)", tx.Model(&model.ProjectAssetLink{}).Select("asset_id").Where("project_id = ?", projectID)).
				Updates(map[string]any{"kind": palette.Asset.Kind, "title": palette.Asset.Title, "status": palette.Asset.Status, "primary_version_id": palette.Asset.PrimaryVersionID, "payload_json": palette.Asset.PayloadJSON, "updated_at": palette.Asset.UpdatedAt})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
			if _, err := followAssetVersion(tx, palette.Asset.ID, palette.Version.ID, now); err != nil {
				return err
			}
		}
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Update("revision", gorm.Expr("revision + 1")).Error
	})
}
