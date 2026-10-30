// 镜头、镜头产物、素材引用与项目工作流。
//
// 镜头与素材引用的变更只让依赖它的产物过期（按输入指纹）；旧六步流程已只读，不再回退步骤状态。

package repository

import (
	"errors"
	"slices"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

func (r *Repository) ProjectShots(projectID string) ([]model.Shot, error) {
	var shots []model.Shot
	err := r.db.Where("project_id = ?", projectID).Order("unit_id asc, position asc").Find(&shots).Error
	return shots, err
}

func (r *Repository) SaveShot(shot *model.Shot, create bool) error {
	if create {
		return r.db.Create(shot).Error
	}
	return r.db.Model(&model.Shot{}).Where("id = ? AND project_id = ?", shot.ID, shot.ProjectID).Updates(map[string]any{
		"unit_id": shot.UnitID, "title": shot.Title, "description": shot.Description, "position": shot.Position,
		"duration_ms": shot.DurationMs, "status": shot.Status, "updated_at": shot.UpdatedAt,
	}).Error
}

// SaveShotWithRevision 原子保存镜头当前值、新版本和下游失效状态（已有产物全部过期）。
func (r *Repository) SaveShotWithRevision(shot *model.Shot, revision *model.ShotRevision, create bool) error {
	return r.SaveShotWithRevisionSelective(shot, revision, create, nil)
}

// ShotArtifactStaleDecider 在保存新版本的事务里决定哪些产物过期：入参是镜头当前未过期的产物和素材引用。
// nil 表示全部过期（旧行为）。
type ShotArtifactStaleDecider func(artifacts []model.ShotArtifact, references []model.ShotAssetReference) []string

// SaveShotWithRevisionSelective 原子保存镜头和新版本，只让依赖输入真的变了的产物过期；
// 有产物过期时才把章节工作流退回分镜步。
func (r *Repository) SaveShotWithRevisionSelective(shot *model.Shot, revision *model.ShotRevision, create bool, decide ShotArtifactStaleDecider) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if create {
			if err := tx.Create(shot).Error; err != nil {
				return err
			}
		} else {
			result := tx.Model(&model.Shot{}).Where("id = ? AND project_id = ?", shot.ID, shot.ProjectID).Updates(map[string]any{
				"unit_id": shot.UnitID, "title": shot.Title, "description": shot.Description, "position": shot.Position,
				"duration_ms": shot.DurationMs, "status": shot.Status, "updated_at": shot.UpdatedAt,
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
		}
		var currentVersion int
		if err := tx.Model(&model.ShotRevision{}).Where("shot_id = ?", shot.ID).Select("COALESCE(MAX(version), 0)").Scan(&currentVersion).Error; err != nil {
			return err
		}
		revision.Version = currentVersion + 1
		if err := tx.Create(revision).Error; err != nil {
			return err
		}
		shot.CurrentRevisionID = revision.ID
		if err := tx.Model(&model.Shot{}).Where("id = ? AND project_id = ?", shot.ID, shot.ProjectID).Updates(map[string]any{
			"current_revision_id": revision.ID, "description": shot.Description, "duration_ms": shot.DurationMs,
			"status": shot.Status, "updated_at": shot.UpdatedAt,
		}).Error; err != nil {
			return err
		}
		invalidated := create
		if !create {
			var live []model.ShotArtifact
			if err := tx.Where("shot_id = ? AND status NOT IN ?", shot.ID, []string{"failed", "stale"}).Find(&live).Error; err != nil {
				return err
			}
			var staleIDs []string
			if decide == nil {
				for _, artifact := range live {
					staleIDs = append(staleIDs, artifact.ID)
				}
			} else {
				var references []model.ShotAssetReference
				if err := tx.Where("shot_id = ?", shot.ID).Find(&references).Error; err != nil {
					return err
				}
				staleIDs = decide(live, references)
			}
			if len(staleIDs) > 0 {
				if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND id IN ?", shot.ID, staleIDs).Updates(map[string]any{"status": "stale", "selected": false, "updated_at": shot.UpdatedAt}).Error; err != nil {
					return err
				}
				invalidated = true
			}
		}
		if invalidated {
		}
		return tx.Model(&model.Project{}).Where("id = ?", shot.ProjectID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": shot.UpdatedAt}).Error
	})
}

func (r *Repository) ReplaceProjectUnitShots(projectID string, unitID string, shots []model.Shot, revisions []model.ShotRevision, references []model.ShotAssetReference, expectedShotIDs []string) error {
	return r.ReplaceProjectUnitShotsWithShotlist(projectID, unitID, shots, revisions, references, expectedShotIDs, nil)
}

// ReplaceProjectUnitShotsWithShotlist 同 ReplaceProjectUnitShots；shotlist 不为空时在同一事务里写入本章镜头表
// （已有就版本号加一并覆盖），段和它的来源一起成功或一起失败。
func (r *Repository) ReplaceProjectUnitShotsWithShotlist(projectID string, unitID string, shots []model.Shot, revisions []model.ShotRevision, references []model.ShotAssetReference, expectedShotIDs []string, shotlist *model.DramaShotlist) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if shotlist != nil {
			if err := upsertDramaShotlist(tx, shotlist); err != nil {
				return err
			}
		}
		if expectedShotIDs != nil {
			var currentShotIDs []string
			if err := tx.Model(&model.Shot{}).Where("project_id = ? AND unit_id = ?", projectID, unitID).Order("id asc").Pluck("id", &currentShotIDs).Error; err != nil {
				return err
			}
			expected := append([]string(nil), expectedShotIDs...)
			sort.Strings(expected)
			if !slices.Equal(currentShotIDs, expected) {
				return ErrProjectUnitShotsChanged
			}
		}
		shotIDs := tx.Model(&model.Shot{}).Select("id").Where("project_id = ? AND unit_id = ?", projectID, unitID)
		if err := tx.Where("project_id = ? AND unit_id = ?", projectID, unitID).Delete(&model.ShotArtifact{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ? AND shot_id IN (?)", projectID, shotIDs).Delete(&model.ShotEvidence{}).Error; err != nil {
			return err
		}
		if err := tx.Where("shot_id IN (?)", shotIDs).Delete(&model.ShotRevision{}).Error; err != nil {
			return err
		}
		if err := tx.Where("shot_id IN (?)", shotIDs).Delete(&model.ShotAssetReference{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ? AND shot_id IN (?)", projectID, shotIDs).Delete(&model.ProjectAssetCandidate{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ? AND unit_id = ?", projectID, unitID).Delete(&model.Shot{}).Error; err != nil {
			return err
		}
		// 分批插入：一章最多 2000 段，一条 INSERT 带全部段的版本和参考会超过 SQLite / PostgreSQL 的绑定参数上限。
		if err := tx.CreateInBatches(&shots, 200).Error; err != nil {
			return err
		}
		if len(revisions) > 0 {
			if err := tx.CreateInBatches(&revisions, 200).Error; err != nil {
				return err
			}
		}
		if len(references) > 0 {
			if err := tx.CreateInBatches(&references, 200).Error; err != nil {
				return err
			}
		}
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": time.Now()}).Error
	})
}

// SaveDramaShotlist 只写一章的镜头表记录（检查没过的草稿），不动本章镜头。
func (r *Repository) SaveDramaShotlist(shotlist *model.DramaShotlist) error {
	return r.db.Transaction(func(tx *gorm.DB) error { return upsertDramaShotlist(tx, shotlist) })
}

func upsertDramaShotlist(tx *gorm.DB, shotlist *model.DramaShotlist) error {
	var existing model.DramaShotlist
	err := tx.Where("project_id = ? AND unit_id = ?", shotlist.ProjectID, shotlist.UnitID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		shotlist.Version = 1
		return tx.Create(shotlist).Error
	}
	if err != nil {
		return err
	}
	shotlist.ID = existing.ID
	shotlist.Version = existing.Version + 1
	shotlist.CreatedAt = existing.CreatedAt
	return tx.Model(&model.DramaShotlist{}).Where("id = ?", existing.ID).Updates(map[string]any{
		"version": shotlist.Version, "shotlist_json": shotlist.ShotlistJSON, "script_json": shotlist.ScriptJSON,
		"check_json": shotlist.CheckJSON, "rows_json": shotlist.RowsJSON, "sections_json": shotlist.SectionsJSON, "packed_sections_json": shotlist.PackedSectionsJSON, "compile_profile_version": shotlist.CompileProfileVersion,
		"created_by": shotlist.CreatedBy, "updated_at": shotlist.UpdatedAt,
	}).Error
}

// DramaShotlistForUnit 读一章的镜头表；没有返回 gorm.ErrRecordNotFound。
func (r *Repository) DramaShotlistForUnit(projectID string, unitID string) (*model.DramaShotlist, error) {
	var shotlist model.DramaShotlist
	if err := r.db.First(&shotlist, "project_id = ? AND unit_id = ?", projectID, unitID).Error; err != nil {
		return nil, err
	}
	return &shotlist, nil
}

func (r *Repository) ShotForProject(projectID string, shotID string) (*model.Shot, error) {
	var shot model.Shot
	if err := r.db.First(&shot, "id = ? AND project_id = ?", shotID, projectID).Error; err != nil {
		return nil, err
	}
	return &shot, nil
}

func (r *Repository) ShotRevisionForShot(shotID string, revisionID string) (*model.ShotRevision, error) {
	var revision model.ShotRevision
	if err := r.db.First(&revision, "id = ? AND shot_id = ?", revisionID, shotID).Error; err != nil {
		return nil, err
	}
	return &revision, nil
}

// DeleteProjectShot 原子删除单个镜头的领域关联，并重新压紧同章节镜头顺序。
func (r *Repository) DeleteProjectShot(projectID string, shotID string, updatedAt time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var shot model.Shot
		if err := tx.First(&shot, "id = ? AND project_id = ?", shotID, projectID).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ? AND shot_id = ?", projectID, shotID).Delete(&model.ProductionTaskLink{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ? AND shot_id = ?", projectID, shotID).Delete(&model.ShotArtifact{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ? AND shot_id = ?", projectID, shotID).Delete(&model.ShotEvidence{}).Error; err != nil {
			return err
		}
		if err := tx.Where("shot_id = ?", shotID).Delete(&model.ShotRevision{}).Error; err != nil {
			return err
		}
		if err := tx.Where("shot_id = ?", shotID).Delete(&model.ShotAssetReference{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ? AND shot_id = ?", projectID, shotID).Delete(&model.ProjectAssetCandidate{}).Error; err != nil {
			return err
		}
		result := tx.Where("id = ? AND project_id = ?", shotID, projectID).Delete(&model.Shot{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		var remaining []model.Shot
		if err := tx.Select("id", "position").Where("project_id = ? AND unit_id = ?", projectID, shot.UnitID).Order("position asc, created_at asc, id asc").Find(&remaining).Error; err != nil {
			return err
		}
		for position, item := range remaining {
			if item.Position == position {
				continue
			}
			if err := tx.Model(&model.Shot{}).Where("id = ? AND project_id = ?", item.ID, projectID).Update("position", position).Error; err != nil {
				return err
			}
		}
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": updatedAt}).Error
	})
}

// ReorderUnitShots 按给定顺序重写一章镜头的位置（拆段、合段后用）。
func (r *Repository) ReorderUnitShots(projectID string, unitID string, shotIDs []string, updatedAt time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for position, shotID := range shotIDs {
			result := tx.Model(&model.Shot{}).Where("id = ? AND project_id = ? AND unit_id = ?", shotID, projectID, unitID).Updates(map[string]any{"position": position, "updated_at": updatedAt})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
		}
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": updatedAt}).Error
	})
}

func (r *Repository) ProjectShotRevisions(projectID string) ([]model.ShotRevision, error) {
	var revisions []model.ShotRevision
	err := r.db.Table("shot_revisions").Select("shot_revisions.*").
		Joins("JOIN shots ON shots.id = shot_revisions.shot_id").
		Where("shots.project_id = ?", projectID).
		Order("shots.unit_id asc, shots.position asc, shot_revisions.version asc").Scan(&revisions).Error
	return revisions, err
}

func (r *Repository) ProjectShotArtifacts(projectID string) ([]model.ShotArtifact, error) {
	var artifacts []model.ShotArtifact
	err := r.db.Where("project_id = ?", projectID).Order("unit_id asc, shot_id asc, type asc, version asc").Find(&artifacts).Error
	return artifacts, err
}

func (r *Repository) CreateShotArtifact(artifact *model.ShotArtifact) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var currentVersion int
		if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND type = ?", artifact.ShotID, artifact.Type).Select("COALESCE(MAX(version), 0)").Scan(&currentVersion).Error; err != nil {
			return err
		}
		artifact.Version = currentVersion + 1
		if artifact.Selected {
			if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND type = ?", artifact.ShotID, artifact.Type).Updates(map[string]any{"selected": false, "updated_at": artifact.UpdatedAt}).Error; err != nil {
				return err
			}
		}
		return tx.Create(artifact).Error
	})
}

func (r *Repository) MarkShotArtifactsStale(shotID string, updatedAt time.Time) error {
	return r.db.Model(&model.ShotArtifact{}).Where("shot_id = ? AND status NOT IN ?", shotID, []string{"failed", "stale"}).Updates(map[string]any{"status": "stale", "selected": false, "updated_at": updatedAt}).Error
}

func (r *Repository) UpsertProductionTaskLink(link *model.ProductionTaskLink) error {
	return r.db.Where("task_id = ? AND shot_id = ? AND artifact_type = ?", link.TaskID, link.ShotID, link.ArtifactType).Assign(map[string]any{
		"project_id": link.ProjectID, "canvas_id": link.CanvasID, "unit_id": link.UnitID, "shot_id": link.ShotID,
		"workflow_step_id": link.WorkflowStepID, "artifact_type": link.ArtifactType, "updated_at": link.UpdatedAt,
	}).FirstOrCreate(link).Error
}

func (r *Repository) UpsertShotAssetReference(reference *model.ShotAssetReference) error {
	result := r.db.Model(&model.ShotAssetReference{}).Where("shot_id = ? AND asset_version_id = ? AND role = ?", reference.ShotID, reference.AssetVersionID, reference.Role).Updates(map[string]any{"status": reference.Status})
	if result.Error != nil || result.RowsAffected > 0 {
		return result.Error
	}
	return r.db.Create(reference).Error
}

func (r *Repository) UpsertShotAssetReferenceAndInvalidate(projectID string, reference *model.ShotAssetReference, updatedAt time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ShotAssetReference{}).Where("shot_id = ? AND asset_version_id = ? AND role = ?", reference.ShotID, reference.AssetVersionID, reference.Role).Updates(map[string]any{"status": reference.Status})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Create(reference).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND status NOT IN ?", reference.ShotID, []string{"failed", "stale"}).Updates(map[string]any{"status": "stale", "selected": false, "updated_at": updatedAt}).Error; err != nil {
			return err
		}
		var shot model.Shot
		if err := tx.First(&shot, "id = ? AND project_id = ?", reference.ShotID, projectID).Error; err != nil {
			return err
		}
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": updatedAt}).Error
	})
}

func (r *Repository) DeleteShotAssetReferenceAndInvalidate(projectID string, shotID string, referenceID string, updatedAt time.Time) (bool, error) {
	deleted := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND shot_id = ?", referenceID, shotID).Delete(&model.ShotAssetReference{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		deleted = true
		if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND status NOT IN ?", shotID, []string{"failed", "stale"}).Updates(map[string]any{"status": "stale", "selected": false, "updated_at": updatedAt}).Error; err != nil {
			return err
		}
		var shot model.Shot
		if err := tx.First(&shot, "id = ? AND project_id = ?", shotID, projectID).Error; err != nil {
			return err
		}
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": updatedAt}).Error
	})
	return deleted, err
}

func (r *Repository) ProjectShotAssetReferences(projectID string) ([]model.ShotAssetReference, error) {
	var references []model.ShotAssetReference
	err := r.db.Table("shot_asset_references").Select("shot_asset_references.*").
		Joins("JOIN shots ON shots.id = shot_asset_references.shot_id").
		Where("shots.project_id = ?", projectID).
		Order("shot_asset_references.created_at asc").Scan(&references).Error
	return references, err
}

func (r *Repository) ProjectAssetCandidates(projectID string) ([]model.ProjectAssetCandidate, error) {
	var candidates []model.ProjectAssetCandidate
	err := r.db.Where("project_id = ?", projectID).Order("created_at asc").Find(&candidates).Error
	return candidates, err
}

func (r *Repository) ProjectAssetCandidate(projectID string, candidateID string) (*model.ProjectAssetCandidate, error) {
	var candidate model.ProjectAssetCandidate
	if err := r.db.First(&candidate, "id = ? AND project_id = ?", candidateID, projectID).Error; err != nil {
		return nil, err
	}
	return &candidate, nil
}

func (r *Repository) CreateProjectAssetCandidates(candidates []model.ProjectAssetCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	return r.db.Create(&candidates).Error
}

func (r *Repository) CreateProjectAssetCandidate(candidate *model.ProjectAssetCandidate) (bool, error) {
	result := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(candidate)
	return result.RowsAffected == 1, result.Error
}

// ConfirmProjectAssetCandidate 将正式资产身份、首版本、项目引用和候选状态放在同一事务中，避免出现半确认数据。
func (r *Repository) ConfirmProjectAssetCandidate(candidate *model.ProjectAssetCandidate, asset *model.Asset, version *model.AssetVersion, link *model.ProjectAssetLink, createAsset bool) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if createAsset {
			if err := tx.Create(asset).Error; err != nil {
				return err
			}
			if err := tx.Create(version).Error; err != nil {
				return err
			}
		} else if err := tx.First(&model.Asset{}, "id = ? AND user_id = ?", asset.ID, asset.UserID).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ? AND asset_id = ?", link.ProjectID, link.AssetID).FirstOrCreate(link).Error; err != nil {
			return err
		}
		result := tx.Model(&model.ProjectAssetCandidate{}).
			Where("id = ? AND project_id = ? AND status = ?", candidate.ID, candidate.ProjectID, "pending_confirmation").
			Updates(map[string]any{"status": candidate.Status, "resolved_asset_id": candidate.ResolvedAssetID, "updated_at": candidate.UpdatedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrInvalidData
		}
		return tx.Model(&model.Project{}).Where("id = ?", candidate.ProjectID).
			Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": candidate.UpdatedAt}).Error
	})
}

func (r *Repository) WorkflowTemplateVersion(templateKey string, version int) (*model.WorkflowTemplateVersion, error) {
	var template model.WorkflowTemplateVersion
	if err := r.db.First(&template, "template_key = ? AND version = ?", templateKey, version).Error; err != nil {
		return nil, err
	}
	return &template, nil
}

func (r *Repository) CreateWorkflowTemplateVersion(template *model.WorkflowTemplateVersion) error {
	return r.db.Create(template).Error
}

func (r *Repository) ProjectWorkflowInstances(projectID string) ([]model.WorkflowInstance, error) {
	var instances []model.WorkflowInstance
	err := r.db.Where("project_id = ?", projectID).Order("created_at asc").Find(&instances).Error
	return instances, err
}

func (r *Repository) WorkflowInstanceForScope(projectID string, unitID string, templateVersionID string) (*model.WorkflowInstance, error) {
	var instance model.WorkflowInstance
	if err := r.db.First(&instance, "project_id = ? AND unit_id = ? AND template_version_id = ?", projectID, unitID, templateVersionID).Error; err != nil {
		return nil, err
	}
	return &instance, nil
}

func (r *Repository) WorkflowInstance(id string) (*model.WorkflowInstance, error) {
	var instance model.WorkflowInstance
	if err := r.db.First(&instance, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &instance, nil
}

func (r *Repository) WorkflowSteps(instanceID string) ([]model.WorkflowStepInstance, error) {
	var steps []model.WorkflowStepInstance
	err := r.db.Where("workflow_instance_id = ?", instanceID).Order("position asc").Find(&steps).Error
	return steps, err
}

func (r *Repository) NextWorkflowStep(instanceID string, position int) (*model.WorkflowStepInstance, error) {
	var step model.WorkflowStepInstance
	if err := r.db.Where("workflow_instance_id = ? AND position > ?", instanceID, position).Order("position asc").First(&step).Error; err != nil {
		return nil, err
	}
	return &step, nil
}

func (r *Repository) CreateWorkflowInstance(instance *model.WorkflowInstance, steps []model.WorkflowStepInstance) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(instance).Error; err != nil {
			return err
		}
		if len(steps) == 0 {
			return nil
		}
		return tx.Create(&steps).Error
	})
}

func (r *Repository) WorkflowStepForProject(projectID string, stepID string) (*model.WorkflowStepInstance, error) {
	var step model.WorkflowStepInstance
	err := r.db.Table("workflow_step_instances").Select("workflow_step_instances.*").Joins("JOIN workflow_instances ON workflow_instances.id = workflow_step_instances.workflow_instance_id").Where("workflow_instances.project_id = ? AND workflow_step_instances.id = ?", projectID, stepID).First(&step).Error
	if err != nil {
		return nil, err
	}
	return &step, nil
}

func (r *Repository) UpdateWorkflowStep(step *model.WorkflowStepInstance) error {
	return r.db.Model(&model.WorkflowStepInstance{}).Where("id = ? AND workflow_instance_id = ?", step.ID, step.WorkflowInstanceID).Updates(map[string]any{"status": step.Status, "output_json": step.OutputJSON, "error": step.Error, "started_at": step.StartedAt, "completed_at": step.CompletedAt, "updated_at": step.UpdatedAt}).Error
}

// RegisterTaskOutputRecords 把成功任务的产物表示、生产关联和段产物写进同一事务；重复回填按任务与用途幂等。
// 旧六步流程已只读，这里不再推进任何步骤或流程实例。
func (r *Repository) RegisterTaskOutputRecords(projectID string, representation *model.AssetRepresentation, productionLink *model.ProductionTaskLink, artifact *model.ShotArtifact) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if representation != nil {
			var existingRepresentation model.AssetRepresentation
			if err := tx.Where("task_id = ? AND role = ?", representation.TaskID, representation.Role).First(&existingRepresentation).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(representation).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
		}
		if productionLink != nil {
			var existingProductionLink model.ProductionTaskLink
			err := tx.Where("task_id = ? AND shot_id = ? AND artifact_type = ?", productionLink.TaskID, productionLink.ShotID, productionLink.ArtifactType).First(&existingProductionLink).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(productionLink).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else if err := tx.Model(&existingProductionLink).Updates(map[string]any{
				"project_id": productionLink.ProjectID, "canvas_id": productionLink.CanvasID, "unit_id": productionLink.UnitID,
				"workflow_step_id": productionLink.WorkflowStepID, "updated_at": productionLink.UpdatedAt,
			}).Error; err != nil {
				return err
			}
		}
		if artifact != nil {
			var existing model.ShotArtifact
			if err := tx.Where("task_id = ? AND shot_id = ? AND type = ?", artifact.TaskID, artifact.ShotID, artifact.Type).First(&existing).Error; err == nil {
				artifact = nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if artifact != nil {
			var currentVersion int
			if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND type = ?", artifact.ShotID, artifact.Type).Select("COALESCE(MAX(version), 0)").Scan(&currentVersion).Error; err != nil {
				return err
			}
			artifact.Version = currentVersion + 1
			if artifact.Selected {
				if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND type = ?", artifact.ShotID, artifact.Type).Updates(map[string]any{"selected": false, "updated_at": artifact.UpdatedAt}).Error; err != nil {
					return err
				}
			}
			if err := tx.Create(artifact).Error; err != nil {
				return err
			}
		}
		projectResult := tx.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": time.Now()})
		if projectResult.Error != nil {
			return projectResult.Error
		}
		if projectResult.RowsAffected != 1 {
			return gorm.ErrInvalidData
		}
		return nil
	})
}

// CreateShotEvidence 记一条证据。
func (r *Repository) CreateShotEvidence(evidence *model.ShotEvidence) error {
	return r.db.Create(evidence).Error
}

// ProjectShotEvidence 返回项目的全部证据（按时间顺序）。
func (r *Repository) ProjectShotEvidence(projectID string) ([]model.ShotEvidence, error) {
	var items []model.ShotEvidence
	err := r.db.Where("project_id = ?", projectID).Order("created_at asc, id asc").Find(&items).Error
	return items, err
}

// ShotArtifactForProject 读取项目里的一条产物。
func (r *Repository) ShotArtifactForProject(projectID string, artifactID string) (*model.ShotArtifact, error) {
	var artifact model.ShotArtifact
	if err := r.db.First(&artifact, "id = ? AND project_id = ?", artifactID, projectID).Error; err != nil {
		return nil, err
	}
	return &artifact, nil
}

// SelectShotArtifact 把一条就绪产物设为该镜头同类型的唯一选中项。
func (r *Repository) SelectShotArtifact(projectID string, artifactID string, updatedAt time.Time) (*model.ShotArtifact, error) {
	var selected model.ShotArtifact
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&selected, "id = ? AND project_id = ?", artifactID, projectID).Error; err != nil {
			return err
		}
		if selected.Status != "ready" {
			return ErrShotArtifactNotReady
		}
		if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND type = ? AND id <> ?", selected.ShotID, selected.Type, selected.ID).Updates(map[string]any{"selected": false, "updated_at": updatedAt}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ShotArtifact{}).Where("id = ?", selected.ID).Updates(map[string]any{"selected": true, "updated_at": updatedAt}).Error; err != nil {
			return err
		}
		selected.Selected = true
		selected.UpdatedAt = updatedAt
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": updatedAt}).Error
	})
	if err != nil {
		return nil, err
	}
	return &selected, nil
}

// ShotAssetReferencesForShot 返回一个镜头当前绑定的素材引用。
func (r *Repository) ShotAssetReferencesForShot(shotID string) ([]model.ShotAssetReference, error) {
	var references []model.ShotAssetReference
	err := r.db.Where("shot_id = ?", shotID).Order("created_at asc, id asc").Find(&references).Error
	return references, err
}

// UnitShotWrite 是保留装段（ReplaceProjectUnitShotsKeeping）的一段：Existing 为 false 是新建（镜头、第 1 版、引用）；
// 为 true 是沿用本章已有的镜头（同一 ID）：Revision 为 nil 表示内容没变，只改位置和标题，产物不动；否则加这个版本并换成当前版本，
// 引用换成 References，依赖输入变了的产物（按 Decide 判定）过期。
type UnitShotWrite struct {
	Shot       model.Shot
	Revision   *model.ShotRevision
	References []model.ShotAssetReference
	Existing   bool
	Decide     ShotArtifactStaleDecider
}

// ReplaceProjectUnitShotsKeeping 是只重写几节后的整章装段：在一个事务里写本章镜头表，按 writes 沿用 / 新建段，删掉 deletes 里不再
// 对应任何小节的旧段（连同产物、证据、版本、引用、候选和制作任务关联）。沿用的段 ID 不变，没变的段分镜图、预演、视频都留着。
func (r *Repository) ReplaceProjectUnitShotsKeeping(projectID string, unitID string, writes []UnitShotWrite, deletes []string, shotlist *model.DramaShotlist, now time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if shotlist != nil {
			if err := upsertDramaShotlist(tx, shotlist); err != nil {
				return err
			}
		}
		for _, id := range deletes {
			if err := deleteShotTx(tx, projectID, unitID, id); err != nil {
				return err
			}
		}
		for _, w := range writes {
			shot := w.Shot
			switch {
			case !w.Existing:
				if err := tx.Create(&shot).Error; err != nil {
					return err
				}
				if w.Revision != nil {
					rev := *w.Revision
					rev.Version = 1
					if err := tx.Create(&rev).Error; err != nil {
						return err
					}
				}
				if len(w.References) > 0 {
					if err := tx.Create(&w.References).Error; err != nil {
						return err
					}
				}
			case w.Revision == nil:
				result := tx.Model(&model.Shot{}).Where("id = ? AND project_id = ? AND unit_id = ?", shot.ID, projectID, unitID).Updates(map[string]any{"title": shot.Title, "position": shot.Position, "updated_at": now})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrProjectUnitShotsChanged
				}
			default:
				var currentVersion int
				if err := tx.Model(&model.ShotRevision{}).Where("shot_id = ?", shot.ID).Select("COALESCE(MAX(version), 0)").Scan(&currentVersion).Error; err != nil {
					return err
				}
				rev := *w.Revision
				rev.Version = currentVersion + 1
				if err := tx.Create(&rev).Error; err != nil {
					return err
				}
				result := tx.Model(&model.Shot{}).Where("id = ? AND project_id = ? AND unit_id = ?", shot.ID, projectID, unitID).Updates(map[string]any{
					"current_revision_id": rev.ID, "title": shot.Title, "description": shot.Description, "duration_ms": shot.DurationMs,
					"position": shot.Position, "status": shot.Status, "updated_at": now,
				})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrProjectUnitShotsChanged
				}
				if err := tx.Where("shot_id = ?", shot.ID).Delete(&model.ShotAssetReference{}).Error; err != nil {
					return err
				}
				if len(w.References) > 0 {
					if err := tx.Create(&w.References).Error; err != nil {
						return err
					}
				}
				var live []model.ShotArtifact
				if err := tx.Where("shot_id = ? AND status NOT IN ?", shot.ID, []string{"failed", "stale"}).Find(&live).Error; err != nil {
					return err
				}
				var staleIDs []string
				if w.Decide == nil {
					for _, a := range live {
						staleIDs = append(staleIDs, a.ID)
					}
				} else {
					staleIDs = w.Decide(live, w.References)
				}
				if len(staleIDs) > 0 {
					if err := tx.Model(&model.ShotArtifact{}).Where("shot_id = ? AND id IN ?", shot.ID, staleIDs).Updates(map[string]any{"status": "stale", "selected": false, "updated_at": now}).Error; err != nil {
						return err
					}
				}
			}
		}
		return tx.Model(&model.Project{}).Where("id = ?", projectID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": now}).Error
	})
}

// deleteShotTx 在事务里删掉一个段和它的全部关联（同 DeleteProjectShot，不重排位置：保留装段随后按新顺序写位置）。
func deleteShotTx(tx *gorm.DB, projectID string, unitID string, shotID string) error {
	for _, m := range []any{&model.ProductionTaskLink{}, &model.ShotArtifact{}, &model.ShotEvidence{}, &model.ProjectAssetCandidate{}} {
		if err := tx.Where("project_id = ? AND shot_id = ?", projectID, shotID).Delete(m).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("shot_id = ?", shotID).Delete(&model.ShotRevision{}).Error; err != nil {
		return err
	}
	if err := tx.Where("shot_id = ?", shotID).Delete(&model.ShotAssetReference{}).Error; err != nil {
		return err
	}
	result := tx.Where("id = ? AND project_id = ? AND unit_id = ?", shotID, projectID, unitID).Delete(&model.Shot{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrProjectUnitShotsChanged
	}
	return nil
}
