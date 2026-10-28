package repository

import (
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

// DramaPaletteWrite 是场次色卡收尾（或风格锁定）写的一张色卡：Link 不为空是新建资产（资产、第 1 个版本、项目关联），
// 为空是给已有色卡加一个版本并换成主版本；Version 也为空是只改 payload（标成场次已不存在）。
type DramaPaletteWrite struct {
	Asset   *model.Asset
	Version *model.AssetVersion
	Link    *model.ProjectAssetLink
}

// ApplyDramaScenePalettes 在一个事务里写场次色卡的结果：项目的 bible_json，每场一张色卡（新建或加版本），和标成场次已不存在的旧色卡。
func (r *Repository) ApplyDramaScenePalettes(userID string, projectID string, bibleJSON string, palettes []DramaPaletteWrite, now time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Project{}).Where("id = ? AND user_id = ?", projectID, userID).Updates(map[string]any{"bible_json": bibleJSON, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		for _, p := range palettes {
			if p.Link != nil {
				if err := tx.Create(p.Asset).Error; err != nil {
					return err
				}
				if err := tx.Create(p.Version).Error; err != nil {
					return err
				}
				if err := tx.Create(p.Link).Error; err != nil {
					return err
				}
				continue
			}
			if p.Version != nil {
				if err := tx.Create(p.Version).Error; err != nil {
					return err
				}
			}
			updated := tx.Model(&model.Asset{}).
				Where("id = ? AND user_id = ? AND category = ?", p.Asset.ID, userID, model.AssetCategoryPalette).
				Where("id IN (?)", tx.Model(&model.ProjectAssetLink{}).Select("asset_id").Where("project_id = ?", projectID)).
				Updates(map[string]any{"kind": p.Asset.Kind, "title": p.Asset.Title, "status": p.Asset.Status, "primary_version_id": p.Asset.PrimaryVersionID, "payload_json": p.Asset.PayloadJSON, "updated_at": p.Asset.UpdatedAt})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
			// 同一场重跑场次色卡加了版本：绑着这张色卡（role=style）的镜头换到新版本，产物过期。
			if p.Version != nil {
				if _, err := followAssetVersion(tx, p.Asset.ID, p.Version.ID, now); err != nil {
					return err
				}
			}
		}
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Update("revision", gorm.Expr("revision + 1")).Error
	})
}

// UpdateAssetPayloadIfUnchanged 只在 payload 还是 previous 时改成 next（资产出图排队时防并发覆盖）；返回是否改了。
func (r *Repository) UpdateAssetPayloadIfUnchanged(userID string, assetID string, previous string, next string, now time.Time) (bool, error) {
	result := r.db.Model(&model.Asset{}).Where("id = ? AND user_id = ? AND payload_json = ?", assetID, userID, previous).Updates(map[string]any{"payload_json": next, "updated_at": now})
	return result.RowsAffected == 1, result.Error
}
