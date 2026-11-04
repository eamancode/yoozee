package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"time"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

type DirectorStyleSaveRequest struct {
	Regenerate       bool                    `json:"regenerate,omitempty"`
	ExpectedRevision *int64                  `json:"-"`
	ImageTask        *DramaImageTaskTemplate `json:"imageTask,omitempty"`
}

func (s *Service) SaveProjectDirectorStyle(userID, projectID string, req DirectorStyleSaveRequest) (model.StyleProfile, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return model.StyleProfile{}, err
	}
	anchor, palette, _, locked := projectLockedStyle(project.BibleJSON)
	if !locked {
		return model.StyleProfile{}, failedPrecondition("请先锁定项目导演风格")
	}
	id := videoChainHash([]string{"director-style", userID, projectID})[:32]
	var existing struct{ Title, Description string }
	if project.StyleProfileJSON != "" {
		if err := json.Unmarshal([]byte(project.StyleProfileJSON), &existing); err != nil {
			return model.StyleProfile{}, err
		}
	}
	raw, err := combinedDirectorProfile(project, anchor, palette, firstNonEmpty(existing.Title, project.Name), firstNonEmpty(existing.Description, "来自项目「"+project.Name+"」的导演风格"), id, nil)
	if err != nil {
		return model.StyleProfile{}, err
	}
	var document struct {
		Title, Description string
		Tags               []string
	}
	if err = json.Unmarshal([]byte(raw), &document); err != nil {
		return model.StyleProfile{}, err
	}
	tags, _ := json.Marshal(document.Tags)
	var fingerprint map[string]any
	if err := json.Unmarshal([]byte(raw), &fingerprint); err != nil {
		return model.StyleProfile{}, err
	}
	delete(fingerprint, "coverUrl")
	delete(fingerprint, "sourceProfileId")
	now := time.Now()
	row := &model.StyleProfile{ID: id, UserID: userID, Name: document.Title, Description: document.Description, TagsJSON: string(tags), ProfileJSON: raw, SourceProjectID: projectID, SourceFingerprint: videoChainHash(fingerprint), CoverStatus: "pending", CoverError: "请配置图片模型后生成风格封面", CreatedAt: now, UpdatedAt: now}
	row, err = s.repo.SaveDirectorStyleProfile(row, req.ExpectedRevision)
	if err != nil {
		return model.StyleProfile{}, err
	}
	if req.ImageTask.usable() {
		updated, coverErr := s.GenerateStyleProfileCover(userID, row.ID, req)
		if coverErr == nil {
			return updated, nil
		}
		_ = s.repo.SetStyleProfileCoverState(userID, row.ID, row.CoverTaskID, "failed", sanitizeUserVisibleError(coverErr.Error()))
		row, _ = s.repo.StyleProfileForUser(userID, row.ID)
	}
	return *row, nil
}

func (s *Service) migratePersonalDirectorProfiles(userID string) error {
	entries, err := s.repo.PersonalDirectorStyleEntries(userID)
	if err != nil {
		return err
	}
	projects, err := s.repo.Projects(userID)
	if err != nil {
		return err
	}
	origins := map[string]string{}
	for _, project := range projects {
		_, _, record, locked := projectLockedStyle(project.BibleJSON)
		if locked && record.Source != drama.StyleLockSourceLibrary && record.LibraryID != "" {
			origins[record.LibraryID] = project.ID
		}
	}
	for _, entry := range entries {
		id := videoChainHash([]string{"legacy-director-style", userID, entry.ID})[:32]
		projectID := origins[entry.ID]
		if projectID != "" {
			id = videoChainHash([]string{"director-style", userID, projectID})[:32]
		}
		if _, err := s.repo.StyleProfileForUser(userID, id); err == nil {
			continue
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		anchor, palette := styleLibraryEntryStyle(&entry)
		raw, err := combinedDirectorProfile(&model.Project{VisualType: entry.ProjectType, StyleProfileJSON: entry.StyleProfileJSON}, anchor, palette, entry.Name, entry.Description, id, nil)
		if err != nil {
			return fmt.Errorf("个人导演风格迁移失败：%w", err)
		}
		var document struct{ Tags []string }
		if err = json.Unmarshal([]byte(raw), &document); err != nil {
			return err
		}
		tags, _ := json.Marshal(document.Tags)
		_, err = s.repo.SaveDirectorStyleProfile(&model.StyleProfile{ID: id, UserID: userID, Name: entry.Name, Description: entry.Description, TagsJSON: string(tags), ProfileJSON: raw, SourceProjectID: projectID, SourceLibraryID: entry.ID, SourceFingerprint: videoChainHash(raw), CoverStatus: "pending", CoverError: "历史色卡保留在项目中，风格封面待生成", CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt}, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) GenerateStyleProfileCover(userID, id string, req DirectorStyleSaveRequest) (model.StyleProfile, error) {
	row, err := s.repo.StyleProfileForUser(userID, id)
	if err != nil {
		return model.StyleProfile{}, err
	}
	taskProjectID := ""
	if row.SourceProjectID != "" {
		if project, err := s.repo.ProjectForUser(userID, row.SourceProjectID); err == nil && project.Status == model.ProjectStatusActive {
			taskProjectID = project.ID
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return model.StyleProfile{}, err
		}
	}
	if err := s.refreshDirectorCoverState(row); err != nil {
		return model.StyleProfile{}, err
	}
	if row.CoverTaskID != "" && (row.CoverStatus == "queued" || row.CoverStatus == "running" || !req.Regenerate && row.CoverStatus == "ready" && row.CoverRevision == row.Revision) {
		return *row, nil
	}
	if !req.ImageTask.usable() {
		return model.StyleProfile{}, BadAuthRequest("请先配置可用的图片模型；导演风格文本已保存，无需重新分析")
	}
	var profile struct{ Prompt, NegativePrompt string }
	if err := json.Unmarshal([]byte(row.ProfileJSON), &profile); err != nil {
		return model.StyleProfile{}, err
	}
	taskID := videoChainHash([]any{"director-cover", row.ID, row.Revision, row.CoverAttempt + 1})[:32]
	reserved, err := s.repo.ReserveStyleProfileCover(row, taskID)
	if err != nil {
		return model.StyleProfile{}, err
	}
	if !reserved {
		current, err := s.repo.StyleProfileForUser(userID, id)
		if err != nil {
			return model.StyleProfile{}, err
		}
		return *current, nil
	}
	input, metadata := dramaImageTaskInput(*req.ImageTask)
	prompt := "为以下项目导演风格生成一张有代表性的影视画面，作为风格中心封面。通过符合项目题材的人物、场景、服饰材质、色彩和光影呈现整体风格。输出一张完整成品图；不要色卡、白模、拼版、文字、标题或水印。\n\n" + profile.Prompt
	if strings.TrimSpace(profile.NegativePrompt) != "" {
		prompt += "\n避免：" + profile.NegativePrompt
	}
	config, _ := input["config"].(map[string]any)
	if config != nil {
		config["count"] = "1"
	}
	metadata["source"], metadata["styleProfileId"], metadata["styleProfileRevision"] = "director-style-cover", row.ID, row.Revision
	taskReq := dramaImageTaskRequestOf(taskProjectID, *req.ImageTask, prompt, input)
	taskReq.admission = &taskAdmission{ID: taskID}
	if _, err = s.CreateTask(userID, taskReq); err != nil {
		if _, readErr := s.repo.TaskForUser(userID, taskID); readErr != nil {
			_ = s.repo.SetStyleProfileCoverState(userID, id, taskID, "failed", sanitizeUserVisibleError(err.Error()))
			return model.StyleProfile{}, err
		}
	}
	current, err := s.repo.StyleProfileForUser(userID, id)
	if err != nil {
		return model.StyleProfile{}, err
	}
	return *current, nil
}

func (s *Service) refreshDirectorCoverState(row *model.StyleProfile) error {
	if row.CoverTaskID == "" || row.CoverStatus == "ready" {
		return nil
	}
	task, err := s.repo.TaskForUser(row.UserID, row.CoverTaskID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if time.Since(row.UpdatedAt) < time.Minute {
			return nil
		}
		row.CoverStatus, row.CoverError = "failed", "封面任务创建未完成，可重试"
	} else if err != nil {
		return err
	} else {
		switch task.Status {
		case model.TaskStatusFailed, model.TaskStatusCancelled:
			row.CoverStatus, row.CoverError = "failed", firstNonEmpty(task.Error, "封面任务已停止，可重试")
		case model.TaskStatusRunning:
			row.CoverStatus, row.CoverError = "running", ""
		case model.TaskStatusSucceeded:
			var result map[string]interface{}
			if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
				return err
			}
			if err := s.finalizeDirectorStyleCover(*task, result); err != nil {
				return err
			}
			current, err := s.repo.StyleProfileForUser(row.UserID, row.ID)
			if err != nil {
				return err
			}
			*row = *current
			return nil
		default:
			row.CoverStatus = "queued"
		}
	}
	return s.repo.SetStyleProfileCoverState(row.UserID, row.ID, row.CoverTaskID, row.CoverStatus, row.CoverError)
}

func (s *Service) finalizeDirectorStyleCover(task model.Task, result map[string]interface{}) error {
	var input struct {
		Metadata struct {
			Source   string `json:"source"`
			ID       string `json:"styleProfileId"`
			Revision int64  `json:"styleProfileRevision"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return err
	}
	if input.Metadata.Source != "director-style-cover" {
		return nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	var output struct {
		Images []struct {
			ResourceID string `json:"resourceId"`
		} `json:"images"`
	}
	if err := json.Unmarshal(raw, &output); err != nil {
		return err
	}
	if len(output.Images) != 1 || output.Images[0].ResourceID == "" {
		return BadAuthRequest("风格封面必须返回一张已保存的图片")
	}
	resource, err := s.videoChainReadyResource(task.UserID, output.Images[0].ResourceID, "image/")
	if err != nil {
		return err
	}
	applied, err := s.repo.CompleteStyleProfileCover(task.UserID, input.Metadata.ID, task.ID, input.Metadata.Revision, resource.ID, resourceFileURL(resource.ID))
	if !applied && err == nil {
		result["styleCoverStatus"] = "stale"
	} else if err == nil {
		result["styleCoverStatus"] = "ready"
	}
	return err
}
