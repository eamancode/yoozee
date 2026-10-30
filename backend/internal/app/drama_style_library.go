// 平台风格库（M8 卡 A）：项目锁定的风格（风格锚 + 主基调色卡）可以存进来，按项目类型归档，团队共享——登录用户都能看、能新增，
// 改名、改色板、删除限管理员或创建者。别的项目在建项目时选用，就把风格锚和色板复制进自己的 bible_json（LockDramaStyle），
// 不跑分析；条目之后改了、删了都不影响已选的项目。封面是存入时合成的主色卡图（资源属于合成它的账号），所有登录用户经
// GET /drama/style-library/:id/cover 读。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"yingce/backend/internal/assets"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// 平台风格库条目的来源。
const (
	DramaStyleLibrarySourceAnalysis = "analysis"
	// DramaStyleLibrarySourceDirector 是建项时交给导演分析、锁定后自动存入的（说明里写项目名）。
	DramaStyleLibrarySourceDirector = "director"
	DramaStyleLibrarySourceManual   = "manual"
	DramaStyleLibrarySourceSeed     = "seed"
	dramaStyleLibraryNameRunes      = 60
	dramaStyleLibraryDescRunes      = 300
)

// DramaStyleLibraryItem 是风格库的一条（页面用）。
type DramaStyleLibraryItem struct {
	ID               string              `json:"id"`
	ProjectType      string              `json:"projectType"`
	ProjectTypeLabel string              `json:"projectTypeLabel"`
	Name             string              `json:"name"`
	Description      string              `json:"description"`
	StyleAnchor      *drama.StyleAnchor  `json:"styleAnchor"`
	MasterPalette    drama.MasterPalette `json:"masterPalette"`
	ColorCount       int                 `json:"colorCount"`
	// CoverURL 是主色卡图（相对 API 根）；没有封面时为空。
	CoverURL      string    `json:"coverUrl,omitempty"`
	Source        string    `json:"source"`
	CreatedBy     string    `json:"createdBy"`
	CreatedByName string    `json:"createdByName"`
	UpdatedBy     string    `json:"updatedBy"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	// CanEdit 是当前用户能不能改、删这一条（管理员或创建者）。
	CanEdit bool `json:"canEdit"`
}

// DramaStyleLibraryList 是风格库列表和项目类型（页面按类型分组）。
type DramaStyleLibraryList struct {
	Items        []DramaStyleLibraryItem `json:"items"`
	ProjectTypes []drama.ProjectTypeDef  `json:"projectTypes"`
}

// DramaStyleLibrarySaveRequest 是「存进风格库」：把项目锁定的风格存成一条。
type DramaStyleLibrarySaveRequest struct {
	ProjectID   string `json:"projectId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// ProjectType 只在项目的锁定记录里没有类型时用。
	ProjectType string `json:"projectType,omitempty"`
}

// DramaStyleLibraryUpdateRequest 是改一条：只改带了的字段；改了色板会重新合成封面。
type DramaStyleLibraryUpdateRequest struct {
	Name          *string              `json:"name,omitempty"`
	Description   *string              `json:"description,omitempty"`
	ProjectType   *string              `json:"projectType,omitempty"`
	StyleAnchor   *drama.StyleAnchor   `json:"styleAnchor,omitempty"`
	MasterPalette *drama.MasterPalette `json:"masterPalette,omitempty"`
}

func dramaStyleLibraryCoverURL(id string) string { return "/drama/style-library/" + id + "/cover" }

// styleLibraryEntryStyle 读条目的风格锚和色板（读不出的当作没有）。
func styleLibraryEntryStyle(entry *model.DramaStyleLibraryEntry) (*drama.StyleAnchor, drama.MasterPalette) {
	var anchor *drama.StyleAnchor
	var palette drama.MasterPalette
	_ = json.Unmarshal([]byte(entry.StyleAnchorJSON), &anchor)
	_ = json.Unmarshal([]byte(entry.MasterPaletteJSON), &palette)
	return anchor, drama.NormalizeMasterPalette(palette)
}

func canEditDramaStyleLibrary(actor *model.User, entry *model.DramaStyleLibraryEntry) bool {
	return actor != nil && (actor.Role == model.UserRoleAdmin || entry.CreatedBy == actor.ID)
}

func (s *Service) dramaStyleLibraryItems(actor *model.User, rows []model.DramaStyleLibraryEntry) ([]DramaStyleLibraryItem, error) {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.CreatedBy)
	}
	users, err := s.repo.UsersByIDs(ids)
	if err != nil {
		return nil, err
	}
	out := make([]DramaStyleLibraryItem, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		anchor, palette := styleLibraryEntryStyle(row)
		item := DramaStyleLibraryItem{
			ID: row.ID, ProjectType: row.ProjectType, ProjectTypeLabel: drama.ProjectTypeLabel(row.ProjectType), Name: row.Name, Description: row.Description,
			StyleAnchor: anchor, MasterPalette: palette, ColorCount: len(palette.Colors()), Source: row.Source,
			CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, CanEdit: canEditDramaStyleLibrary(actor, row),
		}
		if row.CoverResourceID != "" {
			item.CoverURL = dramaStyleLibraryCoverURL(row.ID)
		}
		if u, ok := users[row.CreatedBy]; ok {
			item.CreatedByName = firstNonEmpty(strings.TrimSpace(u.DisplayName), u.Username)
		}
		out = append(out, item)
	}
	return out, nil
}

// DramaStyleLibrary 列出风格库：projectType 为空列全部。登录用户都能看。
func (s *Service) DramaStyleLibrary(actor *model.User, projectType string) (DramaStyleLibraryList, error) {
	if actor == nil {
		return DramaStyleLibraryList{}, Unauthorized("请先登录")
	}
	projectType = strings.TrimSpace(projectType)
	if projectType != "" && drama.ProjectTypeLabel(projectType) == "" {
		return DramaStyleLibraryList{}, BadAuthRequest("未知的项目类型")
	}
	rows, err := s.repo.DramaStyleLibrary(projectType)
	if err != nil {
		return DramaStyleLibraryList{}, err
	}
	items, err := s.dramaStyleLibraryItems(actor, rows)
	if err != nil {
		return DramaStyleLibraryList{}, err
	}
	return DramaStyleLibraryList{Items: items, ProjectTypes: drama.ProjectTypes}, nil
}

func (s *Service) dramaStyleLibraryItem(actor *model.User, row *model.DramaStyleLibraryEntry) (DramaStyleLibraryItem, error) {
	items, err := s.dramaStyleLibraryItems(actor, []model.DramaStyleLibraryEntry{*row})
	if err != nil || len(items) == 0 {
		return DramaStyleLibraryItem{}, err
	}
	return items[0], nil
}

// validStyleLibraryName 校验名称（1～60 字）并查同类型重名。
func (s *Service) validStyleLibraryName(projectType string, name string, exceptID string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", BadAuthRequest("给这个风格起个名字")
	}
	if utf8.RuneCountInString(name) > dramaStyleLibraryNameRunes {
		return "", BadAuthRequest(fmt.Sprintf("风格名最多 %d 字", dramaStyleLibraryNameRunes))
	}
	taken, err := s.repo.DramaStyleLibraryNameTaken(projectType, name, exceptID)
	if err != nil {
		return "", err
	}
	if taken {
		return "", dramaStyleLibraryNameTaken(projectType, name)
	}
	return name, nil
}

func dramaStyleLibraryNameTaken(projectType string, name string) error {
	return BadAuthRequest(fmt.Sprintf("「%s」类型里已经有叫「%s」的风格，换个名字", drama.ProjectTypeLabel(projectType), name))
}

// dramaStyleLibraryWriteError 把写入撞上 (project_type, name) 唯一索引（重名检查和写入之间被别人抢先）换成和重名检查一样的提示。
func dramaStyleLibraryWriteError(err error, projectType string, name string) error {
	if isUniqueConstraintViolation(err) {
		return dramaStyleLibraryNameTaken(projectType, name)
	}
	return err
}

// isUniqueConstraintViolation 判断写入撞了唯一约束：gorm 开了 TranslateError 时是 ErrDuplicatedKey；没开时按驱动报错认——
// SQLite 是「UNIQUE constraint failed」，PostgreSQL 是「duplicate key value violates unique constraint」（SQLSTATE 23505）。
func isUniqueConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate key") || strings.Contains(message, "sqlstate 23505")
}

// discardDramaStyleLibraryCover 删掉风格库不再用的封面（资源属于合成它的账号，不一定是当前操作人）：还有条目拿它当封面、
// 或别的业务还引用着就保留。走孤儿资源清理同一条路径（按引用快照再查一遍，删资源记录并经 Outbox 删文件）。
// 清不掉只记日志，不影响已经成功的改、删；漏下的由定时孤儿清理兜底。
func (s *Service) discardDramaStyleLibraryCover(resourceID string) {
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return
	}
	if err := s.deleteDramaStyleLibraryCover(resourceID); err != nil {
		slog.Warn("drama style library cover cleanup failed", "resource", resourceID, "error", err)
	}
}

func (s *Service) deleteDramaStyleLibraryCover(resourceID string) error {
	inUse, err := s.repo.DramaStyleLibraryCoverInUse(resourceID)
	if err != nil || inUse {
		return err
	}
	resource, err := s.repo.Resource(resourceID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.cleanupDetachedUserResources(resource.UserID, []model.Resource{*resource})
}

func validStyleLibraryDescription(description string) (string, error) {
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > dramaStyleLibraryDescRunes {
		return "", BadAuthRequest(fmt.Sprintf("说明最多 %d 字", dramaStyleLibraryDescRunes))
	}
	return description, nil
}

// SaveDramaStyleToLibrary 是「存进风格库」：把项目锁定的风格（风格锚 + 主基调色卡）存成一条，封面另合成一张主色卡图（存入者的资源），
// 项目的锁定记录写上这一条。取自风格库的风格不用再存。
func (s *Service) SaveDramaStyleToLibrary(actor *model.User, req DramaStyleLibrarySaveRequest) (DramaStyleLibraryItem, error) {
	if actor == nil {
		return DramaStyleLibraryItem{}, Unauthorized("请先登录")
	}
	project, err := s.activeProjectForUser(actor.ID, strings.TrimSpace(req.ProjectID))
	if err != nil {
		return DramaStyleLibraryItem{}, err
	}
	anchor, palette, record, locked := projectLockedStyle(project.BibleJSON)
	if !locked {
		return DramaStyleLibraryItem{}, BadAuthRequest("项目还没有锁定风格，先锁定再存进风格库")
	}
	if record.Source == drama.StyleLockSourceLibrary && record.LibraryID != "" {
		if _, err := s.repo.DramaStyleLibraryEntry(record.LibraryID); err == nil {
			return DramaStyleLibraryItem{}, BadAuthRequest("这个风格取自风格库，已经在库里了")
		}
	}
	projectType := firstNonEmpty(record.ProjectType, strings.TrimSpace(req.ProjectType))
	if drama.ProjectTypeLabel(projectType) == "" {
		return DramaStyleLibraryItem{}, BadAuthRequest("请选这个风格归到哪个项目类型")
	}
	name, err := s.validStyleLibraryName(projectType, req.Name, "")
	if err != nil {
		return DramaStyleLibraryItem{}, err
	}
	description, err := validStyleLibraryDescription(req.Description)
	if err != nil {
		return DramaStyleLibraryItem{}, err
	}
	return s.createDramaStyleLibraryEntry(actor, project, anchor, palette, projectType, name, description, DramaStyleLibrarySourceAnalysis)
}

// saveDirectorStyleToLibrary 把导演定画风锁定的风格存进平台风格库：名字取项目名（同类型重名时加「（2）」「（3）」……），
// 说明写「来自导演分析 · 项目「X」」，来源 director。
func (s *Service) saveDirectorStyleToLibrary(userID string, project *model.Project) (DramaStyleLibraryItem, error) {
	actor, err := s.repo.User(userID)
	if err != nil {
		return DramaStyleLibraryItem{}, err
	}
	anchor, palette, record, locked := projectLockedStyle(project.BibleJSON)
	if !locked {
		return DramaStyleLibraryItem{}, BadAuthRequest("项目还没有锁定风格")
	}
	projectType := firstNonEmpty(record.ProjectType, project.VisualType)
	if drama.ProjectTypeLabel(projectType) == "" {
		return DramaStyleLibraryItem{}, BadAuthRequest("项目没有画面类型")
	}
	base := []rune(strings.Join(strings.Fields(project.Name), " "))
	if len(base) > dramaStyleLibraryNameRunes-4 {
		base = base[:dramaStyleLibraryNameRunes-4]
	}
	name := string(base)
	for n := 2; ; n++ {
		taken, err := s.repo.DramaStyleLibraryNameTaken(projectType, name, "")
		if err != nil {
			return DramaStyleLibraryItem{}, err
		}
		if !taken {
			break
		}
		if n > 99 {
			return DramaStyleLibraryItem{}, BadAuthRequest("风格库里同名的太多了")
		}
		name = fmt.Sprintf("%s（%d）", string(base), n)
	}
	description := fmt.Sprintf("来自导演分析 · 项目「%s」", string(base))
	if utf8.RuneCountInString(description) > dramaStyleLibraryDescRunes {
		description = string([]rune(description)[:dramaStyleLibraryDescRunes])
	}
	return s.createDramaStyleLibraryEntry(actor, project, anchor, palette, projectType, name, description, DramaStyleLibrarySourceDirector)
}

// createDramaStyleLibraryEntry 建一条风格库条目（封面另合成一张主色卡图，属于存入者），并把条目记进项目的锁定记录。
func (s *Service) createDramaStyleLibraryEntry(actor *model.User, project *model.Project, anchor *drama.StyleAnchor, palette drama.MasterPalette, projectType string, name string, description string, source string) (DramaStyleLibraryItem, error) {
	palette = drama.NormalizeMasterPalette(palette)
	if err := drama.ValidateMasterPalette(palette); err != nil {
		return DramaStyleLibraryItem{}, BadAuthRequest(err.Error())
	}
	cover, _, err := s.renderMasterPaletteResource(actor.ID, name, palette)
	if err != nil {
		return DramaStyleLibraryItem{}, err
	}
	anchorJSON, _ := json.Marshal(anchor)
	paletteJSON, _ := json.Marshal(palette)
	now := time.Now()
	row := model.DramaStyleLibraryEntry{
		ID: newID(), ProjectType: projectType, Name: name, Description: description, StyleAnchorJSON: string(anchorJSON), MasterPaletteJSON: string(paletteJSON),
		CoverResourceID: cover.ID, Source: source, CreatedBy: actor.ID, UpdatedBy: actor.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.CreateDramaStyleLibraryEntry(&row); err != nil {
		s.discardDramaStyleLibraryCover(cover.ID)
		return DramaStyleLibraryItem{}, dramaStyleLibraryWriteError(err, projectType, name)
	}
	// 项目的锁定记录记下存进的条目（锁定来源不变）。写不上不影响已经存好的条目。
	doc := map[string]any{}
	if json.Unmarshal([]byte(project.BibleJSON), &doc) == nil && doc != nil {
		if lock, ok := doc["style_lock"].(map[string]any); ok {
			lock["library_id"] = row.ID
			if lock["project_type"] == nil || lock["project_type"] == "" {
				lock["project_type"] = projectType
			}
			if encoded, err := json.Marshal(doc); err == nil {
				_ = s.repo.UpdateProjectDramaDocument(actor.ID, project.ID, "bible_json", string(encoded), now)
			}
		}
	}
	return s.dramaStyleLibraryItem(actor, &row)
}

func (s *Service) editableDramaStyleLibraryEntry(actor *model.User, id string) (*model.DramaStyleLibraryEntry, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	entry, err := s.repo.DramaStyleLibraryEntry(strings.TrimSpace(id))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("风格库里没有这一条")
	}
	if err != nil {
		return nil, err
	}
	if !canEditDramaStyleLibrary(actor, entry) {
		return nil, Forbidden("只有管理员或存入这个风格的人能改、删它")
	}
	return entry, nil
}

// UpdateDramaStyleLibraryEntry 改一条（管理员或创建者）：名称、说明、类型、风格锚、色板；改了色板或名称会重新合成封面。
func (s *Service) UpdateDramaStyleLibraryEntry(actor *model.User, id string, req DramaStyleLibraryUpdateRequest) (DramaStyleLibraryItem, error) {
	entry, err := s.editableDramaStyleLibraryEntry(actor, id)
	if err != nil {
		return DramaStyleLibraryItem{}, err
	}
	values := map[string]any{"updated_by": actor.ID}
	projectType := entry.ProjectType
	if req.ProjectType != nil {
		projectType = strings.TrimSpace(*req.ProjectType)
		if drama.ProjectTypeLabel(projectType) == "" {
			return DramaStyleLibraryItem{}, BadAuthRequest("未知的项目类型")
		}
		values["project_type"] = projectType
	}
	name := entry.Name
	if req.Name != nil || projectType != entry.ProjectType {
		candidate := entry.Name
		if req.Name != nil {
			candidate = *req.Name
		}
		if name, err = s.validStyleLibraryName(projectType, candidate, entry.ID); err != nil {
			return DramaStyleLibraryItem{}, err
		}
		values["name"] = name
	}
	if req.Description != nil {
		description, err := validStyleLibraryDescription(*req.Description)
		if err != nil {
			return DramaStyleLibraryItem{}, err
		}
		values["description"] = description
	}
	if req.StyleAnchor != nil {
		if req.StyleAnchor.Empty() {
			return DramaStyleLibraryItem{}, BadAuthRequest("风格锚不能是空的")
		}
		encoded, _ := json.Marshal(req.StyleAnchor)
		values["style_anchor_json"] = string(encoded)
	}
	_, palette := styleLibraryEntryStyle(entry)
	if req.MasterPalette != nil {
		palette = drama.NormalizeMasterPalette(*req.MasterPalette)
		if err := drama.ValidateMasterPalette(palette); err != nil {
			return DramaStyleLibraryItem{}, BadAuthRequest(err.Error())
		}
		encoded, _ := json.Marshal(palette)
		values["master_palette_json"] = string(encoded)
	}
	newCover := ""
	if req.MasterPalette != nil || name != entry.Name {
		cover, _, err := s.renderMasterPaletteResource(actor.ID, name, palette)
		if err != nil {
			return DramaStyleLibraryItem{}, err
		}
		newCover = cover.ID
		values["cover_resource_id"] = cover.ID
	}
	if err := s.repo.UpdateDramaStyleLibraryEntry(entry.ID, values, time.Now()); err != nil {
		s.discardDramaStyleLibraryCover(newCover)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DramaStyleLibraryItem{}, NotFound("风格库里没有这一条")
		}
		return DramaStyleLibraryItem{}, dramaStyleLibraryWriteError(err, projectType, name)
	}
	// 换了封面：旧封面在条目写成功之后才删（写失败时条目还指着它）。
	if newCover != "" && entry.CoverResourceID != newCover {
		s.discardDramaStyleLibraryCover(entry.CoverResourceID)
	}
	updated, err := s.repo.DramaStyleLibraryEntry(entry.ID)
	if err != nil {
		return DramaStyleLibraryItem{}, err
	}
	return s.dramaStyleLibraryItem(actor, updated)
}

// DeleteDramaStyleLibraryEntry 删一条（管理员或创建者），连同它的封面；已经选用它的项目保留各自复制的风格（主色卡图是项目自己合成的）。
func (s *Service) DeleteDramaStyleLibraryEntry(actor *model.User, id string) error {
	entry, err := s.editableDramaStyleLibraryEntry(actor, id)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteDramaStyleLibraryEntry(entry.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NotFound("风格库里没有这一条")
		}
		return err
	}
	s.discardDramaStyleLibraryCover(entry.CoverResourceID)
	return nil
}

// PrepareDramaStyleLibraryCoverDelivery 读一条的主色卡图：登录用户都能读（资源属于合成它的账号）。
func (s *Service) PrepareDramaStyleLibraryCoverDelivery(actor *model.User, id string, options ResourceAccessOptions, rangeHeader string) (*ResourceDelivery, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	entry, err := s.repo.DramaStyleLibraryEntry(strings.TrimSpace(id))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("风格库里没有这一条")
	}
	if err != nil {
		return nil, err
	}
	if entry.CoverResourceID == "" {
		return nil, NotFound("这个风格没有主色卡图")
	}
	resource, err := s.repo.Resource(entry.CoverResourceID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("主色卡图已不存在")
	}
	if err != nil {
		return nil, err
	}
	if resource.Kind != "image" || resource.Status != model.ResourceStatusReady {
		return nil, BadAuthRequest("主色卡图资源不可用")
	}
	if options.Purpose == "" {
		options.Purpose = assets.PurposeDisplay
	}
	return s.prepareResourceDelivery(resource.UserID, resource, options, rangeHeader)
}
