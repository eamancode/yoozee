package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// The caller holds the episode lock and passes the repository for the shot deletion transaction.
func removeDirectorUnitDocuments(r *repository.Repository, userID string, shot *model.Shot, seg drama.Segment) error {
	if strings.TrimSpace(seg.Verbatim) == "" {
		return nil
	}
	doc, err := r.LatestOntologyDocument(shot.ProjectID, shot.UnitID, model.OntologyDocEpisode)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return directorDocumentConflict("找不到本集导演文档，无法同步删除单元卡")
	}
	if err != nil {
		return err
	}
	content, err := drama.RemoveDirectorUnit(doc.Content, seg.Title)
	if err != nil {
		return directorDocumentConflict(err.Error())
	}
	appendVersion := func(kind, body string) error {
		return r.AppendOntologyDocument(&model.ProjectOntologyDocument{
			ID: newID(), ProjectID: shot.ProjectID, UnitID: shot.UnitID, Kind: kind,
			Content: body, CreatedBy: userID,
		})
	}
	if err := appendVersion(model.OntologyDocEpisode, content); err != nil {
		return err
	}
	gate, err := r.LatestOntologyDocument(shot.ProjectID, shot.UnitID, model.OntologyDocGate)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var open []string
	if err := json.Unmarshal([]byte(gate.Content), &open); err != nil {
		return directorDocumentConflict("本集导演自检记录无法解析，未删除单元卡")
	}
	before := len(open)
	open = slices.DeleteFunc(open, func(item string) bool {
		id, _, _ := strings.Cut(item, " ")
		return id == seg.Title
	})
	if len(open) != before {
		return appendVersion(model.OntologyDocGate, ontologyGateContent(open))
	}
	return nil
}

func directorDocumentConflict(message string) *AppError {
	err := NewAppError(http.StatusConflict, message)
	err.Reason = ReasonConflict
	return err
}

// A new whole-episode run may intentionally generate different units. A run
// prepared before a manual deletion cannot use its old completed turns.
func (s *Service) checkDirectorUnitEditVersion(projectID, unitID string, expected int) error {
	version, err := s.repo.DirectorUnitEditVersion(projectID, unitID)
	if err != nil {
		return err
	}
	if version != expected {
		return directorDocumentConflict("本集分镜单元已删除，旧导演任务的分段结果已失效，请刷新后重新生成")
	}
	return nil
}
