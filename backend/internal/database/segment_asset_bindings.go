package database

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

// Add association rows for existing current specs, without changing revisions,
// documents, images, artifacts or tasks. Running twice is harmless.
func migrateSegmentAssetBindings(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&model.Shot{}) || !tx.Migrator().HasTable(&model.ShotRevision{}) {
		return nil
	}
	if err := tx.AutoMigrate(&model.ShotAssetReference{}); err != nil {
		return err
	}
	// The selected timestamp comes from the existing revision, not migration time.
	var shots []model.Shot
	var skipped int
	err := tx.Model(&model.Shot{}).FindInBatches(&shots, 100, func(_ *gorm.DB, _ int) error {
		for _, shot := range shots {
			var rev model.ShotRevision
			if err := tx.First(&rev, "id = ? AND shot_id = ?", shot.CurrentRevisionID, shot.ID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			} else if err != nil {
				return err
			}
			var seg drama.Segment
			if json.Unmarshal([]byte(rev.SpecJSON), &seg) != nil || seg.ID == "" {
				continue
			}
			seen := map[string]bool{}
			for i, ref := range seg.Refs {
				var version model.AssetVersion
				err := tx.Table("asset_versions av").Select("av.*").Joins("JOIN assets a ON a.id = av.asset_id").
					Joins("JOIN project_asset_links pal ON pal.asset_id = a.id").Joins("JOIN projects p ON p.id = pal.project_id AND p.user_id = a.user_id").
					Where("p.id = ? AND (av.id = ? OR (a.id = ? AND av.id = a.primary_version_id))", shot.ProjectID, ref.File, ref.File).Order("av.id").First(&version).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					skipped++
					continue
				}
				if err != nil {
					return err
				}
				if seen[version.ID] {
					continue
				}
				seen[version.ID] = true
				binding := model.ShotAssetReference{ID: kernel.NewID(), ShotID: shot.ID, AssetVersionID: version.ID, Role: "reference", Status: "linked", Source: "segment", Position: i, CreatedAt: rev.CreatedAt}
				if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "shot_id"}, {Name: "asset_version_id"}, {Name: "role"}}, DoUpdates: clause.AssignmentColumns([]string{"source", "position"})}).Create(&binding).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}).Error
	if skipped > 0 {
		tx.Logger.Warn(tx.Statement.Context, "分段绑定补齐：%d 项原引用已失效或不属于项目，未建立关联，需人工处理", skipped)
	}
	return err
}
