package app

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// The storyboard card submits the snapshot shown for confirmation. Older workbench
// callers may omit it and still receive task, ownership and source-document checks.
type DeleteProjectShotRequest struct {
	ExpectedRevisionID  *string  `json:"expectedRevisionId"`
	ExpectedArtifactIDs []string `json:"expectedArtifactIds"`
}

func (s *Service) DeleteProjectShotChecked(userID, projectID, shotID string, req DeleteProjectShotRequest) error {
	shotID = strings.TrimSpace(shotID)
	if shotID == "" {
		return BadAuthRequest("镜头不能为空")
	}
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NotFound("项目不存在")
		}
		return err
	}
	before, err := s.repo.ShotForProject(projectID, shotID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NotFound("分镜单元已删除或不在本项目，请刷新列表")
	}
	if err != nil {
		return err
	}
	unlock := lockOntologyEpisode(before.UnitID)
	defer unlock()
	return s.repo.Transaction(func(r *repository.Repository) error {
		project, err := r.LockActiveStoryboardProject(userID, projectID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NotFound("项目不存在")
		}
		if err != nil {
			return err
		}
		if project.Status == model.ProjectStatusArchived {
			return BadAuthRequest("项目已归档，不能修改短剧生产数据")
		}
		shot, err := r.LockShotForProject(projectID, shotID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NotFound("分镜单元已删除或不在本项目，请刷新列表")
		}
		if err != nil {
			return err
		}
		if shot.UnitID != before.UnitID || (req.ExpectedRevisionID != nil && shot.CurrentRevisionID != *req.ExpectedRevisionID) {
			return NewAppError(http.StatusConflict, "分镜单元已更新，请刷新后重新确认")
		}
		if err := guardStoryboardTasks(r, userID, shot, "本单元"); err != nil {
			return err
		}
		if req.ExpectedArtifactIDs != nil {
			ids, err := r.ShotArtifactIDs(projectID, shotID)
			if err != nil {
				return err
			}
			expected := slices.Clone(req.ExpectedArtifactIDs)
			slices.Sort(expected)
			if !slices.Equal(ids, expected) {
				return NewAppError(http.StatusConflict, "本单元的关联产物已变化，请刷新后重新确认")
			}
		}
		var seg drama.Segment
		if shot.CurrentRevisionID != "" {
			rev, err := r.ShotRevisionForShot(shotID, shot.CurrentRevisionID)
			if err != nil {
				return err
			}
			if strings.TrimSpace(rev.SpecJSON) != "" {
				if seg, err = drama.ParseSegment([]byte(rev.SpecJSON)); err != nil {
					return NewAppError(http.StatusConflict, "分镜版本无法解析，无法核对要删除的子镜头")
				}
			}
		}
		if err := removeDirectorUnitDocuments(r, userID, shot, seg); err != nil {
			return err
		}
		if seg.ID != "" {
			others, err := r.OtherUnitShotSpecs(shot)
			if err != nil {
				return err
			}
			remaining, err := reconnectAfterShotDelete(r, shot, seg, others, userID)
			if err != nil {
				return err
			}
			if err := removeShotlistUnit(r, project, shot, seg, remaining); err != nil {
				return err
			}
		}
		if err := r.DetachDeletedShotContinuity(shot); err != nil {
			return err
		}
		return r.DeleteProjectShot(projectID, shotID, time.Now())
	})
}

func guardStoryboardTasks(r *repository.Repository, userID string, shot *model.Shot, target string) error {
	tasks, err := r.ActiveTasksForStoryboardShot(userID, shot.ProjectID, shot.UnitID, shot.ID)
	if err != nil || len(tasks) == 0 {
		return err
	}
	labels := make([]string, 0, len(tasks))
	for _, task := range tasks {
		status := string(task.Status)
		if task.Status == model.TaskStatusFailed && task.Stage != "" {
			status = task.Stage
		}
		labels = append(labels, fmt.Sprintf("%s（%s，%s）", task.ID, task.Type, status))
	}
	return NewAppError(http.StatusConflict, "以下任务正在使用"+target+"，结束后可删除："+strings.Join(labels, "、"))
}
