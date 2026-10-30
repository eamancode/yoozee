// 建项目（首页一页四段）：要拍什么、画面类型、画风从哪来、怎么做。校验失败按段返回 reason（project_source_invalid /
// project_visual_type_required / project_style_invalid / project_production_invalid），页面据此把错误落到对应的段。
// 小说、剧本的章 / 集随建项一起写进项目；从风格中心挑的风格建项时直接锁定（不跑风格分析）。建完由页面调流水线「继续」开始第一步。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const (
	// 创意的规模上限：集数、每集秒数。
	projectIdeaMaxEpisodes      = 30
	projectIdeaMinEpisodeSecond = 15
	projectIdeaMaxEpisodeSecond = 600
	// projectDefaultSegmentSeconds 是每段时长的默认值。
	projectDefaultSegmentSeconds = 15
)

// projectSectionError 是落在建项某一段上的 4xx。
func projectSectionError(reason ErrorReason, message string) *AppError {
	err := BadAuthRequest(message)
	err.Reason = reason
	return err
}

func (s *Service) CreateProject(userID string, req CreateProjectRequest) (model.Project, error) {
	if err := s.EnsureBuiltinProjectWorkflowTemplate(); err != nil {
		return model.Project{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return model.Project{}, projectSectionError(ReasonProjectSourceInvalid, "项目名称不能为空")
	}
	projectType := strings.TrimSpace(req.Type)
	if projectType == "" {
		projectType = "short-drama"
	}
	visualType := strings.TrimSpace(req.VisualType)
	if drama.ProjectTypeLabel(visualType) == "" {
		return model.Project{}, projectSectionError(ReasonProjectVisualTypeRequired, "先选画面类型（仿真人、写实 CG、3D 动漫、2D 动漫）")
	}
	sourceAs, units, description, err := validateProjectSource(req)
	if err != nil {
		return model.Project{}, err
	}
	style, library, err := s.validateProjectStyle(userID, visualType, req.Style)
	if err != nil {
		return model.Project{}, err
	}
	aspectRatio := strings.TrimSpace(req.AspectRatio)
	if aspectRatio == "" {
		aspectRatio = "9:16"
	}
	if err := validateProjectAspectRatio(aspectRatio); err != nil {
		return model.Project{}, projectSectionError(ReasonProjectProductionInvalid, err.Error())
	}
	generationModel, err := normalizeProjectGenerationModel(req.GenerationModel)
	if err != nil {
		return model.Project{}, projectSectionError(ReasonProjectProductionInvalid, err.Error())
	}
	defaultImageModel, err := normalizeProjectDefaultModel(req.DefaultImageModel)
	if err != nil {
		return model.Project{}, projectSectionError(ReasonProjectProductionInvalid, err.Error())
	}
	defaultVideoModel, err := normalizeProjectDefaultModel(req.DefaultVideoModel)
	if err != nil {
		return model.Project{}, projectSectionError(ReasonProjectProductionInvalid, err.Error())
	}
	if err := checkProjectVideoModelFamily(generationModel, defaultVideoModel); err != nil {
		return model.Project{}, projectSectionError(ReasonProjectProductionInvalid, err.Error())
	}
	runMode, err := normalizeProjectRunMode(req.RunMode)
	if err != nil {
		return model.Project{}, err
	}
	segmentSeconds := req.SegmentSeconds
	if segmentSeconds == 0 {
		segmentSeconds = projectDefaultSegmentSeconds
	}
	if err := checkSegmentSecondsForModel(segmentSeconds, generationModel); err != nil {
		return model.Project{}, projectSectionError(ReasonProjectProductionInvalid, err.Error())
	}
	briefJSON, err := projectCreateBrief(segmentSeconds, sourceAs, req.IdeaPlan)
	if err != nil {
		return model.Project{}, err
	}
	now := time.Now()
	project := model.Project{
		ID: newID(), UserID: userID, Name: name, Type: projectType, AspectRatio: aspectRatio, SourceType: sourceAs, Description: description,
		VisualType: visualType, RunMode: runMode, StyleSource: style, DefaultImageModel: defaultImageModel, DefaultVideoModel: defaultVideoModel,
		GenerationModel: generationModel, StyleProfile: drama.VisualTypeProfile(visualType), BriefJSON: briefJSON,
		Status: model.ProjectStatusActive, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.CreateProject(&project); err != nil {
		if errors.Is(err, repository.ErrProjectNameConflict) {
			// 项目名称冲突需要明确返回 409，页面才能按已有逻辑换一个名称重试。
			conflict := NewAppError(http.StatusConflict, "项目名称已存在")
			conflict.Reason = ReasonProjectNameConflict
			return model.Project{}, conflict
		}
		return model.Project{}, err
	}
	// 章 / 集和风格库锁定是建项的一部分：写不上就把刚建的项目删掉，表单留给用户重试。
	if len(units) > 0 {
		if _, err := s.ImportProjectUnits(userID, project.ID, ImportProjectUnitsRequest{Units: units}); err != nil {
			s.discardCreatedProject(userID, project.ID)
			return model.Project{}, err
		}
	}
	if library != nil {
		anchor, palette := styleLibraryEntryStyle(library)
		lock := styleLockInput{Anchor: anchor, Palette: palette, Title: library.Name, Record: drama.StyleLockRecord{Source: drama.StyleLockSourceLibrary, LibraryID: library.ID, ProjectType: library.ProjectType}}
		if _, err := s.applyDramaStyleLock(userID, &project, lock); err != nil {
			s.discardCreatedProject(userID, project.ID)
			return model.Project{}, fmt.Errorf("锁定所选风格失败：%w", err)
		}
	}
	created, err := s.repo.ProjectForUser(userID, project.ID)
	if err != nil {
		return model.Project{}, err
	}
	// 新项目不再建旧六步流程实例：进度按段、产物和证据推导。
	return *created, nil
}

// discardCreatedProject 删掉建到一半的项目（章节或锁定风格没写上）；删不掉只记日志。
func (s *Service) discardCreatedProject(userID string, projectID string) {
	if err := s.DeleteProject(userID, projectID); err != nil {
		_ = s.log(userID, "", "warn", "建项没成功，刚建的项目没删掉", err.Error())
	}
}

// validateProjectSource 校验要拍什么：返回来源类型、要写进项目的章 / 集和项目简介。
func validateProjectSource(req CreateProjectRequest) (string, []CreateProjectUnitRequest, string, error) {
	sourceAs := strings.TrimSpace(req.SourceAs)
	description := strings.TrimSpace(req.Description)
	text := strings.TrimSpace(req.Source.Text)
	switch sourceAs {
	case ProjectSourceBlank, "":
		sourceAs = ProjectSourceBlank
		return sourceAs, nil, description, nil
	case ProjectSourceIdea:
		if text == "" {
			return "", nil, "", projectSectionError(ReasonProjectSourceInvalid, "写一句创意再开始")
		}
		plan := req.IdeaPlan
		if plan == nil || plan.Episodes < 1 || plan.Episodes > projectIdeaMaxEpisodes {
			return "", nil, "", projectSectionError(ReasonProjectSourceInvalid, fmt.Sprintf("创意要生成 1 到 %d 集", projectIdeaMaxEpisodes))
		}
		if plan.EpisodeSeconds < projectIdeaMinEpisodeSecond || plan.EpisodeSeconds > projectIdeaMaxEpisodeSecond {
			return "", nil, "", projectSectionError(ReasonProjectSourceInvalid, fmt.Sprintf("每集时长要在 %d 到 %d 秒之间", projectIdeaMinEpisodeSecond, projectIdeaMaxEpisodeSecond))
		}
		return sourceAs, nil, firstNonEmpty(description, text), nil
	case ProjectSourceNovel, ProjectSourceScript:
		if len(req.Source.Units) == 0 {
			return "", nil, "", projectSectionError(ReasonProjectSourceInvalid, "正文是空的：粘贴剧本或小说，或导入文件")
		}
		kind := string(model.ProjectUnitKindChapter)
		if sourceAs == ProjectSourceScript {
			kind = string(model.ProjectUnitKindEpisode)
		}
		units := make([]CreateProjectUnitRequest, 0, len(req.Source.Units))
		for _, unit := range req.Source.Units {
			units = append(units, CreateProjectUnitRequest{Kind: kind, Title: unit.Title, SourceText: unit.SourceText})
		}
		return sourceAs, units, description, nil
	}
	return "", nil, "", projectSectionError(ReasonProjectSourceInvalid, "来源只能是创意、小说、剧本或空项目")
}

// validateProjectStyle 校验画风从哪来：导演分析的描述字数、参考图（本人已就绪的图片资源）；风格库条目要存在且属于所选画面类型。
func (s *Service) validateProjectStyle(userID string, visualType string, style model.ProjectStyleSource) (model.ProjectStyleSource, *model.DramaStyleLibraryEntry, error) {
	switch strings.TrimSpace(style.Mode) {
	case model.ProjectStyleDirector, "":
		brief := strings.TrimSpace(style.Brief)
		if drama.JSLen(brief) > drama.StyleLockTextChars {
			return style, nil, projectSectionError(ReasonProjectStyleInvalid, fmt.Sprintf("风格描述最多 %d 字", drama.StyleLockTextChars))
		}
		ids := make([]string, 0, len(style.RefImageIDs))
		seen := map[string]bool{}
		for _, id := range style.RefImageIDs {
			if id = strings.TrimSpace(id); id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) > drama.StyleLockMaxImages {
			return style, nil, projectSectionError(ReasonProjectStyleInvalid, fmt.Sprintf("参考图最多 %d 张", drama.StyleLockMaxImages))
		}
		for _, id := range ids {
			resource, err := s.repo.ResourceForUser(userID, id)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return style, nil, projectSectionError(ReasonProjectStyleInvalid, "参考图不存在或不属于当前账号，请重新上传")
			}
			if err != nil {
				return style, nil, err
			}
			if resource.Status != model.ResourceStatusReady || !strings.HasPrefix(strings.ToLower(resource.MimeType), "image/") {
				return style, nil, projectSectionError(ReasonProjectStyleInvalid, "参考图还没上传完，或不是图片")
			}
		}
		return model.ProjectStyleSource{Mode: model.ProjectStyleDirector, Brief: brief, RefImageIDs: ids}, nil, nil
	case model.ProjectStyleLibrary:
		id := strings.TrimSpace(style.StyleID)
		if id == "" {
			return style, nil, projectSectionError(ReasonProjectStyleInvalid, "从风格中心挑一个风格")
		}
		entry, err := s.repo.DramaStyleLibraryEntry(id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return style, nil, projectSectionError(ReasonProjectStyleInvalid, "风格中心里没有这一条，可能已被删除")
		}
		if err != nil {
			return style, nil, err
		}
		if entry.ProjectType != visualType {
			return style, nil, projectSectionError(ReasonProjectStyleInvalid, fmt.Sprintf("风格「%s」只适用于%s", entry.Name, drama.ProjectTypeLabel(entry.ProjectType)))
		}
		return model.ProjectStyleSource{Mode: model.ProjectStyleLibrary, StyleID: entry.ID}, entry, nil
	}
	return style, nil, projectSectionError(ReasonProjectStyleInvalid, "画风只能交给导演分析，或从风格中心挑")
}

// projectCreateBrief 是建项时的需求文档：每段时长；创意另写每集时长和集数（AI 生成章节、拆剧本按它定规模）。
func projectCreateBrief(segmentSeconds int, sourceAs string, plan *ProjectIdeaPlan) (string, error) {
	doc := map[string]any{"segment_seconds": segmentSeconds}
	if sourceAs == ProjectSourceIdea && plan != nil {
		doc["episode_seconds"] = plan.EpisodeSeconds
		episodes := make([]map[string]any, 0, plan.Episodes)
		for no := 1; no <= plan.Episodes; no++ {
			episodes = append(episodes, map[string]any{"no": no, "target_seconds": plan.EpisodeSeconds})
		}
		doc["episodes"] = episodes
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
