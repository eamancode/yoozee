// 编译档案（M5）：装段和编译的文字带版本在线编辑。生效档案 = 启用的那一版，没有就用内置默认（版本 0）。
// 装段把档案版本记在镜头表上，编译结果返回档案版本；领域逻辑（模板、校验、合并）在 internal/drama。

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const (
	dramaCompileProfileMaxBytes  = 256 << 10
	dramaCompileProfileNameRunes = 120
	dramaCompileProfileNoteRunes = 500
)

// DramaCompileProfileRef 标出一次装段或编译用的是哪个编译档案。
type DramaCompileProfileRef struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
}

// DramaCompileProfileView 是后台看到的一个档案版本。
type DramaCompileProfileView struct {
	model.DramaCompileProfile
	Content json.RawMessage `json:"content"`
}

// DramaCompileProfileList 是后台档案页的数据：版本列表、生效版本号和内置默认全文（风格块按来源文件填好）。
type DramaCompileProfileList struct {
	Profiles      []DramaCompileProfileView `json:"profiles"`
	ActiveVersion int                       `json:"activeVersion"`
	Default       json.RawMessage           `json:"default"`
}

type DramaCompileProfileRequest struct {
	Name    string          `json:"name"`
	Content json.RawMessage `json:"content"`
	Note    string          `json:"note"`
	Enabled bool            `json:"enabled"`
}

func compileProfileRef(p *drama.CompileProfile) DramaCompileProfileRef {
	return DramaCompileProfileRef{Version: p.Version(), Name: p.Name()}
}

func loadCompileProfileRecord(record *model.DramaCompileProfile) (*drama.CompileProfile, error) {
	p, err := drama.ParseCompileProfile([]byte(record.ContentJSON), false)
	if err != nil {
		return nil, fmt.Errorf("编译档案 v%d 无效: %w", record.Version, err)
	}
	return p.WithMeta(record.Version, record.Name), nil
}

// activeCompileProfile 是装段、编译当前用的档案。
func (s *Service) activeCompileProfile() (*drama.CompileProfile, error) {
	record, err := s.repo.ActiveDramaCompileProfile()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return drama.DefaultCompileProfile()
	}
	if err != nil {
		return nil, err
	}
	return loadCompileProfileRecord(record)
}

// compileProfileVersion 取某个版本的档案，0 为内置默认（评测用）。
func (s *Service) compileProfileVersion(version int) (*drama.CompileProfile, error) {
	if version == 0 {
		return drama.DefaultCompileProfile()
	}
	record, err := s.repo.DramaCompileProfileByVersion(version)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound(fmt.Sprintf("编译档案 v%d 不存在", version))
	}
	if err != nil {
		return nil, err
	}
	return loadCompileProfileRecord(record)
}

func (s *Service) AdminDramaCompileProfiles(actor *model.User) (*DramaCompileProfileList, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	records, err := s.repo.DramaCompileProfiles()
	if err != nil {
		return nil, err
	}
	resolved, err := drama.ResolvedDefaultCompileProfile()
	if err != nil {
		return nil, err
	}
	def, err := json.Marshal(resolved)
	if err != nil {
		return nil, err
	}
	out := &DramaCompileProfileList{Profiles: make([]DramaCompileProfileView, 0, len(records)), Default: def}
	for _, r := range records {
		content := json.RawMessage(r.ContentJSON)
		if !json.Valid(content) {
			content = json.RawMessage("{}")
		}
		out.Profiles = append(out.Profiles, DramaCompileProfileView{DramaCompileProfile: r, Content: content})
		if r.Enabled {
			out.ActiveVersion = r.Version
		}
	}
	return out, nil
}

// CreateDramaCompileProfile 新建一个版本（校验未知字段、未知占位符、缺了必须的占位符）；enabled 时立即生效。
func (s *Service) CreateDramaCompileProfile(actor *model.User, req DramaCompileProfileRequest) (*DramaCompileProfileView, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	note := strings.TrimSpace(req.Note)
	if name == "" || utf8.RuneCountInString(name) > dramaCompileProfileNameRunes {
		return nil, BadAuthRequest(fmt.Sprintf("版本名称必须为 1-%d 个字", dramaCompileProfileNameRunes))
	}
	if note == "" || utf8.RuneCountInString(note) > dramaCompileProfileNoteRunes {
		return nil, BadAuthRequest(fmt.Sprintf("请写明这一版改了什么（不超过 %d 字）", dramaCompileProfileNoteRunes))
	}
	if len(req.Content) == 0 || len(req.Content) > dramaCompileProfileMaxBytes {
		return nil, BadAuthRequest("编译档案内容为空或超过 256 KB")
	}
	if _, err := drama.ParseCompileProfile(req.Content, true); err != nil {
		return nil, BadAuthRequest(err.Error())
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, req.Content); err != nil {
		return nil, BadAuthRequest("编译档案不是合法 JSON")
	}
	now := time.Now()
	record := &model.DramaCompileProfile{ID: newID(), Name: name, ContentJSON: compact.String(), Note: note, Enabled: req.Enabled, CreatedBy: actor.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreateDramaCompileProfile(record); err != nil {
		return nil, err
	}
	return &DramaCompileProfileView{DramaCompileProfile: *record, Content: json.RawMessage(record.ContentJSON)}, nil
}

func (s *Service) ActivateDramaCompileProfile(actor *model.User, id string) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	record, err := s.repo.DramaCompileProfile(strings.TrimSpace(id))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NotFound("编译档案版本不存在")
	}
	if err != nil {
		return err
	}
	// 启用前再校验一次：代码升级后旧版本里的模板可能已不合法。
	if _, err := drama.ParseCompileProfile([]byte(record.ContentJSON), false); err != nil {
		return BadAuthRequest(fmt.Sprintf("v%d 在当前版本的程序里无效：%s", record.Version, err.Error()))
	}
	return s.repo.ActivateDramaCompileProfile(record.ID)
}

// RevertDramaCompileProfile 回退到内置默认：停用全部版本。
func (s *Service) RevertDramaCompileProfile(actor *model.User) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	return s.repo.ActivateDramaCompileProfile("")
}

func (s *Service) DeleteDramaCompileProfile(actor *model.User, id string) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	deleted, err := s.repo.DeleteDramaCompileProfile(strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if !deleted {
		return BadAuthRequest("版本不存在或正在启用，启用中的版本不能删除")
	}
	return nil
}
