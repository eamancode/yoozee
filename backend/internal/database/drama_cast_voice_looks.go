package database

import (
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

func migrateDramaCastVoiceLooks(tx *gorm.DB) error {
	for _, field := range []string{"VoiceProfileID", "VoiceSampleResourceID", "ParentID", "LookName", "LookFromChapter", "LookToChapter"} {
		if tx.Migrator().HasColumn(&model.DramaCast{}, field) {
			continue
		}
		if err := tx.Migrator().AddColumn(&model.DramaCast{}, field); err != nil {
			return err
		}
	}
	for column, value := range map[string]any{"voice_profile_id": "", "voice_sample_resource_id": "", "parent_id": "", "look_name": "", "look_from_chapter": 0, "look_to_chapter": 0} {
		if err := tx.Model(&model.DramaCast{}).Where(column+" IS NULL").UpdateColumn(column, value).Error; err != nil {
			return err
		}
	}
	if !tx.Migrator().HasColumn(&model.DramaShot{}, "LookCastIDsJSON") {
		if err := tx.Migrator().AddColumn(&model.DramaShot{}, "LookCastIDsJSON"); err != nil {
			return err
		}
	}
	if err := tx.Model(&model.DramaShot{}).Where("look_cast_ids_json IS NULL").UpdateColumn("look_cast_ids_json", "").Error; err != nil {
		return err
	}
	if tx.Migrator().HasIndex(&model.DramaCast{}, "ParentID") {
		return nil
	}
	return tx.Migrator().CreateIndex(&model.DramaCast{}, "ParentID")
}
