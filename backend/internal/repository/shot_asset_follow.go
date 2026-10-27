package repository

import (
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

// followAssetVersion 是资产换了主版本（色卡出图或重跑导演风格加版本、角色换定妆或改设定、资产设计合并加版本）之后，
// 让镜头跟上：引用这个资产旧版本的镜头引用换到新版本（同一镜头已经以同一用途引用新版本的，删掉旧引用），这些镜头的产物过期
// （与 UpsertShotAssetReferenceAndInvalidate 同一条规则：不动失败和已过期的），所在项目 revision 加一。镜头产出登记成的
// 资产引用（role=output）不跟。在调用方写新版本的事务里跑，返回换了引用的镜头数。
func followAssetVersion(tx *gorm.DB, assetID string, newVersionID string, now time.Time) (int, error) {
	var refs []model.ShotAssetReference
	err := tx.Table("shot_asset_references").Select("shot_asset_references.*").
		Joins("JOIN asset_versions ON asset_versions.id = shot_asset_references.asset_version_id").
		Where("asset_versions.asset_id = ? AND shot_asset_references.asset_version_id <> ? AND shot_asset_references.role <> ?", assetID, newVersionID, "output").
		Scan(&refs).Error
	if err != nil || len(refs) == 0 {
		return 0, err
	}
	shotIDs := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		var duplicate int64
		if err := tx.Model(&model.ShotAssetReference{}).Where("shot_id = ? AND asset_version_id = ? AND role = ?", ref.ShotID, newVersionID, ref.Role).Count(&duplicate).Error; err != nil {
			return 0, err
		}
		if duplicate > 0 {
			err = tx.Where("id = ?", ref.ID).Delete(&model.ShotAssetReference{}).Error
		} else {
			err = tx.Model(&model.ShotAssetReference{}).Where("id = ?", ref.ID).Update("asset_version_id", newVersionID).Error
		}
		if err != nil {
			return 0, err
		}
		if !seen[ref.ShotID] {
			seen[ref.ShotID] = true
			shotIDs = append(shotIDs, ref.ShotID)
		}
	}
	if err := tx.Model(&model.ShotArtifact{}).Where("shot_id IN ? AND status NOT IN ?", shotIDs, []string{"failed", "stale"}).
		Updates(map[string]any{"status": "stale", "selected": false, "updated_at": now}).Error; err != nil {
		return 0, err
	}
	err = tx.Model(&model.Project{}).Where("id IN (?)", tx.Model(&model.Shot{}).Select("project_id").Where("id IN ?", shotIDs)).
		Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": now}).Error
	return len(shotIDs), err
}

// SelectedShotArtifactReady 是镜头有没有选中、没过期也没失败的某类产物（预演闸门查 action_board）。
func (r *Repository) SelectedShotArtifactReady(projectID string, shotID string, artifactType string) (bool, error) {
	var count int64
	err := r.db.Model(&model.ShotArtifact{}).
		Where("project_id = ? AND shot_id = ? AND type = ? AND selected = ? AND status NOT IN ?", projectID, shotID, artifactType, true, []string{"stale", "failed"}).
		Count(&count).Error
	return count > 0, err
}
