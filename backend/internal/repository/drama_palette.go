package repository

import (
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

// DramaPaletteWrite 是导演风格收尾写的一张色卡：Link 不为空是新建资产（资产、第 1 个版本、项目关联），
// 为空是给已有色卡加一个版本并换成主版本。
type DramaPaletteWrite struct {
	Asset   *model.Asset
	Version *model.AssetVersion
	Link    *model.ProjectAssetLink
}

// ApplyDramaStyleBible 在一个事务里写导演风格的结果：项目的 bible_json，和每场一张色卡（新建或加版本）。
func (r *Repository) ApplyDramaStyleBible(userID string, projectID string, bibleJSON string, palettes []DramaPaletteWrite, now time.Time) error {
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
			if err := tx.Create(p.Version).Error; err != nil {
				return err
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
		}
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Update("revision", gorm.Expr("revision + 1")).Error
	})
}

// UpdateAssetPayloadIfUnchanged 只在 payload 还是 previous 时改成 next（色卡出图排队、记取色结果时防并发覆盖）；返回是否改了。
func (r *Repository) UpdateAssetPayloadIfUnchanged(userID string, assetID string, previous string, next string, now time.Time) (bool, error) {
	result := r.db.Model(&model.Asset{}).Where("id = ? AND user_id = ? AND payload_json = ?", assetID, userID, previous).Updates(map[string]any{"payload_json": next, "updated_at": now})
	return result.RowsAffected == 1, result.Error
}

// UpdateAssetRepresentationMetadata 改一条资产表示的 metadata_json（色卡主图的取色校验）。
func (r *Repository) UpdateAssetRepresentationMetadata(id string, metadataJSON string) error {
	return r.db.Model(&model.AssetRepresentation{}).Where("id = ?", id).Update("metadata_json", metadataJSON).Error
}
