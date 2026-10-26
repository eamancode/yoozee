package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

// ErrDramaAssetChanged 是资产设计收尾时，要合并的已有资产在提取期间被改过（主版本变了）。
var ErrDramaAssetChanged = errors.New("资产在提取期间被改过")

// DramaAssetWrite 是资产设计收尾写的一项：
//   - Link 不为空是新建：候选（直接记成已确认，保留来自哪个单元）、资产、第 1 个版本、项目关联；
//   - 否则是合并到已有资产（ExpectedVersionID 是读到的主版本，变了整批不写）：DefinitionOnly 为 true 时只原地改主版本的设定
//     （只补别名，不影响出图），否则加一个版本换成主版本（Representations、Voice 是从原版本搬过来的角色形象和声音）。
type DramaAssetWrite struct {
	Candidate         *model.ProjectAssetCandidate
	Asset             *model.Asset
	Version           *model.AssetVersion
	Link              *model.ProjectAssetLink
	ExpectedVersionID string
	DefinitionOnly    bool
	Representations   []model.AssetRepresentation
	Voice             *model.CharacterVoiceBinding
}

// ApplyDramaAssetsExtract 在一个事务里写资产设计的结果，项目 revision 加一。
func (r *Repository) ApplyDramaAssetsExtract(userID string, projectID string, writes []DramaAssetWrite, now time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var project model.Project
		if err := tx.Select("id").Where("id = ? AND user_id = ?", projectID, userID).First(&project).Error; err != nil {
			return err
		}
		for _, w := range writes {
			if w.Link != nil {
				if err := tx.Create(w.Asset).Error; err != nil {
					return err
				}
				if err := tx.Create(w.Version).Error; err != nil {
					return err
				}
				if err := tx.Create(w.Link).Error; err != nil {
					return err
				}
				if err := tx.Create(w.Candidate).Error; err != nil {
					return err
				}
				continue
			}
			var current model.Asset
			err := tx.Select("id", "primary_version_id").
				Where("id = ? AND user_id = ?", w.Asset.ID, userID).
				Where("id IN (?)", tx.Model(&model.ProjectAssetLink{}).Select("asset_id").Where("project_id = ?", projectID)).
				First(&current).Error
			if err != nil {
				return err
			}
			if current.PrimaryVersionID != w.ExpectedVersionID {
				return ErrDramaAssetChanged
			}
			if w.DefinitionOnly {
				result := tx.Model(&model.AssetVersion{}).Where("id = ? AND asset_id = ?", w.Version.ID, w.Asset.ID).Updates(map[string]any{"definition_json": w.Version.DefinitionJSON, "updated_at": now})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return gorm.ErrRecordNotFound
				}
				if err := tx.Model(&model.Asset{}).Where("id = ?", w.Asset.ID).Updates(map[string]any{"payload_json": w.Asset.PayloadJSON, "updated_at": now}).Error; err != nil {
					return err
				}
				continue
			}
			if err := saveCharacterVersion(tx, w.Asset, w.Version, w.Representations, w.Voice); err != nil {
				return err
			}
		}
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": now}).Error
	})
}
