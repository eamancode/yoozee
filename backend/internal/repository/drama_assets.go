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
//   - Link 不为空是新建：候选（直接记成已确认，保留来自哪个单元；角色子卡没有候选）、资产、第 1 个版本、项目关联；
//   - 否则是合并到已有资产（ExpectedVersionID 是读到的主版本，变了整批不写）：PayloadOnly 为 true 时只改资产 payload（子卡补场序），
//     DefinitionOnly 为 true 时只原地改主版本的设定（只补别名，不影响出图），否则加一个版本换成主版本（Representations、Voice
//     是从原版本搬过来的角色形象和声音）。
type DramaAssetWrite struct {
	Candidate         *model.ProjectAssetCandidate
	Asset             *model.Asset
	Version           *model.AssetVersion
	Link              *model.ProjectAssetLink
	ExpectedVersionID string
	DefinitionOnly    bool
	PayloadOnly       bool
	Representations   []model.AssetRepresentation
	Voice             *model.CharacterVoiceBinding
}

// ApplyDramaAssetsExtract 在一个事务里写资产设计的结果，项目 revision 加一。patchBible 不为空时在事务里读当前的 bible_json、
// 交给它只改自己负责的键（场景资产回填到场次），按读到的原值条件写回：同时有别的写（场次色卡写 color_script）时重读再改，
// 不拿提取开始时的旧快照整块覆盖。
func (r *Repository) ApplyDramaAssetsExtract(userID string, projectID string, writes []DramaAssetWrite, patchBible func(bibleJSON string) (string, error), now time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var project model.Project
		if err := tx.Select("id").Where("id = ? AND user_id = ?", projectID, userID).First(&project).Error; err != nil {
			return err
		}
		if patchBible != nil {
			if err := patchProjectBible(tx, projectID, patchBible); err != nil {
				return err
			}
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
				if w.Candidate != nil {
					if err := tx.Create(w.Candidate).Error; err != nil {
						return err
					}
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
			if w.PayloadOnly {
				if err := tx.Model(&model.Asset{}).Where("id = ?", w.Asset.ID).Updates(map[string]any{"payload_json": w.Asset.PayloadJSON, "updated_at": now}).Error; err != nil {
					return err
				}
				continue
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

// UpdateAssetVariantLabel 改角色子卡的变体名和标题（资产页改子卡）。
func (r *Repository) UpdateAssetVariantLabel(userID string, assetID string, title string, label string, now time.Time) error {
	result := r.db.Model(&model.Asset{}).Where("id = ? AND user_id = ? AND parent_asset_id <> ''", assetID, userID).
		Updates(map[string]any{"title": title, "variant_label": label, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ProjectAssetShotReferenceTitles 是项目里引用这个资产（任一版本）的镜头标题，按标题排（删子卡前列出引用用）。
func (r *Repository) ProjectAssetShotReferenceTitles(projectID string, assetID string) ([]string, error) {
	var titles []string
	err := r.db.Table("shot_asset_references").Distinct("shots.title").
		Joins("JOIN shots ON shots.id = shot_asset_references.shot_id").
		Joins("JOIN asset_versions ON asset_versions.id = shot_asset_references.asset_version_id").
		Where("shots.project_id = ? AND asset_versions.asset_id = ?", projectID, assetID).
		Order("shots.title").Pluck("shots.title", &titles).Error
	return titles, err
}

// CreateProjectAssetVariant 在一个事务里建一张角色子卡：资产、第 1 个版本、项目关联，项目 revision 加一。
func (r *Repository) CreateProjectAssetVariant(projectID string, asset *model.Asset, version *model.AssetVersion, link *model.ProjectAssetLink, now time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(asset).Error; err != nil {
			return err
		}
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		if err := tx.Create(link).Error; err != nil {
			return err
		}
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": now}).Error
	})
}

// ErrDramaBibleBusy 是连戏表一直被别处同时改（重读三次还对不上），这次没写。
var ErrDramaBibleBusy = errors.New("连戏表正被同时修改")

// patchProjectBible 读项目的 bible_json，用 patch 改出新值，按原值条件写回（原值变了就重读再改，最多三次）。
func patchProjectBible(tx *gorm.DB, projectID string, patch func(string) (string, error)) error {
	for attempt := 0; attempt < 3; attempt++ {
		var current struct{ BibleJSON string }
		if err := tx.Model(&model.Project{}).Select("bible_json").Where("id = ?", projectID).Take(&current).Error; err != nil {
			return err
		}
		next, err := patch(current.BibleJSON)
		if err != nil {
			return err
		}
		if next == current.BibleJSON {
			return nil
		}
		result := tx.Model(&model.Project{}).Where("id = ? AND bible_json = ?", projectID, current.BibleJSON).Update("bible_json", next)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			return nil
		}
	}
	return ErrDramaBibleBusy
}
