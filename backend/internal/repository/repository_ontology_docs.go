package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// LatestOntologyDocument 读某个文档的最新版本；没有返回 gorm.ErrRecordNotFound。unitID 为空是项目级文档。
func (r *Repository) LatestOntologyDocument(projectID string, unitID string, kind string) (*model.ProjectOntologyDocument, error) {
	var doc model.ProjectOntologyDocument
	err := r.db.Where("project_id = ? AND unit_id = ? AND kind = ?", projectID, unitID, kind).Order("version DESC").First(&doc).Error
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// OntologyDocumentsForProject 是项目各文档的最新版本（项目级和每集），按单元、种类排好。
func (r *Repository) OntologyDocumentsForProject(projectID string) ([]model.ProjectOntologyDocument, error) {
	var docs []model.ProjectOntologyDocument
	latest := r.db.Model(&model.ProjectOntologyDocument{}).Select("unit_id, kind, MAX(version) AS version").Where("project_id = ?", projectID).Group("unit_id, kind")
	err := r.db.Table("project_ontology_documents AS d").
		Joins("JOIN (?) AS l ON d.unit_id = l.unit_id AND d.kind = l.kind AND d.version = l.version", latest).
		Where("d.project_id = ?", projectID).Order("d.unit_id, d.kind").Select("d.*").Find(&docs).Error
	return docs, err
}

// AppendOntologyDocument 追加一个新版本（版本号在事务里取最新加一；并发写同一文档时唯一索引兜住，返回错误由调用方重试或报错）。
func (r *Repository) AppendOntologyDocument(doc *model.ProjectOntologyDocument) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var last model.ProjectOntologyDocument
		err := tx.Where("project_id = ? AND unit_id = ? AND kind = ?", doc.ProjectID, doc.UnitID, doc.Kind).Order("version DESC").First(&last).Error
		switch {
		case err == nil:
			doc.Version = last.Version + 1
		case errors.Is(err, gorm.ErrRecordNotFound):
			doc.Version = 1
		default:
			return err
		}
		if doc.CreatedAt.IsZero() {
			doc.CreatedAt = time.Now()
		}
		return tx.Create(doc).Error
	})
}

// OntologyDocumentWrittenByTask 表示这个任务是否已经写过这份文档（重新收尾时跳过，不重复加版本）。
func (r *Repository) OntologyDocumentWrittenByTask(projectID string, unitID string, kind string, taskID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.ProjectOntologyDocument{}).Where("project_id = ? AND unit_id = ? AND kind = ? AND task_id = ?", projectID, unitID, kind, taskID).Count(&count).Error
	return count > 0, err
}

// CountActiveShotArtifacts 先锁住镜头行（与产物登记共用 lockShotRow），再数这个镜头未失败的产物（与整集替换确认时的计数口径一致）。
// 必须在删段的同一事务里调用：锁一直持有到提交，期间不会有新产物登记进来。
func (r *Repository) CountActiveShotArtifacts(projectID string, shotID string) (int64, error) {
	if err := lockShotRow(r.db.Where("project_id = ?", projectID), shotID); err != nil {
		return 0, err
	}
	var count int64
	err := r.db.Model(&model.ShotArtifact{}).Where("shot_id = ? AND status <> ?", shotID, "failed").Count(&count).Error
	return count, err
}
