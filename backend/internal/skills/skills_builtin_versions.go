// 内置技能在线覆盖：后台在线编辑出的版本（origin=online）优先于内置包同步出的版本（origin=builtin）。
// 运行时仍只读 skills.current_version_id；启动同步只比较内置包与 builtin_version_id，在线版本生效时不切走。

package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const (
	SkillOriginBuiltin = "builtin"
	SkillOriginOnline  = "online"

	builtinSkillNoteMaxRunes   = 500
	builtinSkillEditMaxFiles   = 64
	builtinSkillEditFileMaxLen = maxSkillPreviewBytes
)

// BuiltinSkillVersionItem 是后台看到的一个版本。
type BuiltinSkillVersionItem struct {
	ID           string    `json:"id"`
	VersionLabel string    `json:"versionLabel"`
	Origin       string    `json:"origin"`
	ContentHash  string    `json:"contentHash"`
	FileCount    int       `json:"fileCount"`
	TotalBytes   int64     `json:"totalBytes"`
	Note         string    `json:"note"`
	CreatedBy    string    `json:"createdBy"`
	CreatedAt    time.Time `json:"createdAt"`
	Active       bool      `json:"active"`
	Builtin      bool      `json:"builtin"`
}

// BuiltinSkillSummary 是后台列表里的一个内置技能。
type BuiltinSkillSummary struct {
	SkillID     string `json:"skillId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Tag         string `json:"tag"`
	// Overridden 表示生效的是在线版本。
	Overridden bool `json:"overridden"`
	// BuiltinUpdated 表示在线版本生效期间内置包又更新过（内置版本比生效版本新）。
	BuiltinUpdated     bool                     `json:"builtinUpdated"`
	OnlineVersionCount int64                    `json:"onlineVersionCount"`
	CurrentVersion     *BuiltinSkillVersionItem `json:"currentVersion,omitempty"`
	BuiltinVersion     *BuiltinSkillVersionItem `json:"builtinVersion,omitempty"`
}

// BuiltinSkillFileEdit 是在线编辑的一个文本文件。
type BuiltinSkillFileEdit struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// BuiltinSkillVersionRequest 以某个版本为底（默认生效版本）覆盖、删除文件，新建一个在线版本。
type BuiltinSkillVersionRequest struct {
	BaseVersionID string                 `json:"baseVersionId"`
	Files         []BuiltinSkillFileEdit `json:"files"`
	DeletePaths   []string               `json:"deletePaths"`
	Note          string                 `json:"note"`
	Activate      bool                   `json:"activate"`
}

// syncBuiltinPackage 把内置包同步成版本：与 builtin_version_id 那一版哈希相同就不动；
// 不同就新建内置版本，生效的不是在线版本时同时切过去。
func (s *Service) syncBuiltinPackage(skill *model.Skill, archive skillPackageArchive) error {
	builtinID := strings.TrimSpace(skill.BuiltinVersionID)
	if builtinID != "" {
		version, err := s.repo.SkillVersion(builtinID)
		switch {
		case err == nil && version.SkillID == skill.ID:
			if version.ContentHash == archive.ContentHash {
				return nil
			}
		case err == nil || errors.Is(err, gorm.ErrRecordNotFound):
			builtinID = ""
		default:
			return err
		}
	}
	if builtinID == "" && skill.CurrentVersionID != "" && skill.ContentHash == archive.ContentHash {
		// v60 之前同步的库：当前版本就是这份内置包，补上来源和指针即可。
		if err := s.repo.SetSkillVersionOrigin(skill.CurrentVersionID, SkillOriginBuiltin); err != nil {
			return err
		}
		return s.repo.UpdateSkillColumns(skill.ID, map[string]any{"builtin_version_id": skill.CurrentVersionID})
	}
	activate := true
	if current := strings.TrimSpace(skill.CurrentVersionID); current != "" {
		if version, err := s.repo.SkillVersion(current); err == nil && version.Origin == SkillOriginOnline {
			activate = false
		}
	}
	label := archive.Metadata.Version
	if label == "" {
		label = "1"
		if skill.CurrentVersionID != "" {
			label = time.Now().UTC().Format("20060102-150405")
		}
	}
	if err := s.addBuiltinSkillVersion(skill.ID, archive, label, SkillOriginBuiltin, "", "", activate); err != nil {
		return err
	}
	if !activate {
		log.Printf("内置技能 %s 的内置包已更新到 %s，在线版本 %s 仍生效", skill.ID, label, skill.VersionLabel)
	}
	return nil
}

// addBuiltinSkillVersion 落一个版本；内置版本同时更新 builtin_version_id，activate 时切为生效版本。
func (s *Service) addBuiltinSkillVersion(skillID string, archive skillPackageArchive, label string, origin string, note string, createdBy string, activate bool) error {
	versionID := kernel.NewID()
	packageKey, version, files, err := s.persistSkillArchive(skillID, versionID, archive, "")
	if err != nil {
		return err
	}
	version.VersionLabel = label
	version.Origin = origin
	version.Note = note
	version.CreatedBy = createdBy
	values := map[string]any{}
	if origin == SkillOriginBuiltin {
		values["builtin_version_id"] = versionID
	}
	if activate {
		for key, value := range activeSkillVersionColumns(version, archive.Files["SKILL.md"]) {
			values[key] = value
		}
	}
	if err := s.repo.AddSkillVersionWithUpdates(version, files, skillID, values); err != nil {
		_ = os.Remove(filepath.Join(s.dataDir, "skill-packages", filepath.FromSlash(packageKey)))
		return err
	}
	return nil
}

// activeSkillVersionColumns 是切换生效版本时要改的技能列：版本指针、摘要和 instruction（生效版本的 SKILL.md）。
func activeSkillVersionColumns(version *model.SkillVersion, entry []byte) map[string]any {
	now := time.Now()
	return map[string]any{
		"current_version_id": version.ID, "version_label": version.VersionLabel, "content_hash": version.ContentHash,
		"file_count": version.FileCount, "total_bytes": version.TotalBytes, "instruction": string(entry),
		"sync_status": "synced", "sync_error": "", "last_checked_at": now, "last_synced_at": now,
	}
}

func (s *Service) requireActiveAdmin(actor *model.User) error {
	return s.curationActor(actor, true)
}

// adminBuiltinSkill 取一个内置技能（后台在线覆盖只对内置技能开放）。
func (s *Service) adminBuiltinSkill(actor *model.User, skillID string) (*model.Skill, error) {
	if err := s.requireActiveAdmin(actor); err != nil {
		return nil, err
	}
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return nil, kernel.BadAuthRequest("技能 ID 不能为空")
	}
	skill, err := s.repo.Skill(skillID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.NotFound("技能不存在")
	}
	if err != nil {
		return nil, err
	}
	if skill.SourceType != "builtin" {
		return nil, kernel.BadAuthRequest("只有内置技能可以在线覆盖")
	}
	return skill, nil
}

func (s *Service) skillVersionOf(skill *model.Skill, versionID string) (*model.SkillVersion, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" {
		return nil, kernel.BadAuthRequest("版本 ID 不能为空")
	}
	version, err := s.repo.SkillVersion(versionID)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && version.SkillID != skill.ID) {
		return nil, kernel.NotFound("这个技能没有该版本")
	}
	return version, err
}

func builtinVersionItem(skill *model.Skill, v model.SkillVersion) *BuiltinSkillVersionItem {
	return &BuiltinSkillVersionItem{
		ID: v.ID, VersionLabel: v.VersionLabel, Origin: v.Origin, ContentHash: v.ContentHash, FileCount: v.FileCount, TotalBytes: v.TotalBytes,
		Note: v.Note, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt, Active: v.ID == skill.CurrentVersionID, Builtin: v.ID == skill.BuiltinVersionID,
	}
}

func (s *Service) builtinSkillSummaries(skills []model.Skill) ([]BuiltinSkillSummary, error) {
	ids := make([]string, 0, len(skills))
	versionIDs := make([]string, 0, len(skills)*2)
	for _, skill := range skills {
		ids = append(ids, skill.ID)
		for _, id := range []string{skill.CurrentVersionID, skill.BuiltinVersionID} {
			if id != "" && !slices.Contains(versionIDs, id) {
				versionIDs = append(versionIDs, id)
			}
		}
	}
	versions, err := s.repo.SkillVersionsByIDs(versionIDs)
	if err != nil {
		return nil, err
	}
	counts, err := s.repo.SkillVersionCounts(ids, SkillOriginOnline)
	if err != nil {
		return nil, err
	}
	out := make([]BuiltinSkillSummary, 0, len(skills))
	for i := range skills {
		skill := &skills[i]
		item := BuiltinSkillSummary{SkillID: skill.ID, Name: skill.Name, Description: skill.Description, Tag: skill.Tag, OnlineVersionCount: counts[skill.ID]}
		current, hasCurrent := versions[skill.CurrentVersionID]
		if hasCurrent {
			item.CurrentVersion = builtinVersionItem(skill, current)
			item.Overridden = current.Origin == SkillOriginOnline
		}
		if builtin, ok := versions[skill.BuiltinVersionID]; ok {
			item.BuiltinVersion = builtinVersionItem(skill, builtin)
			item.BuiltinUpdated = item.Overridden && hasCurrent && builtin.CreatedAt.After(current.CreatedAt)
		}
		out = append(out, item)
	}
	return out, nil
}

// AdminBuiltinSkills 列出内置技能及其生效版本、内置版本。
func (s *Service) AdminBuiltinSkills(actor *model.User) ([]BuiltinSkillSummary, error) {
	if err := s.requireActiveAdmin(actor); err != nil {
		return nil, err
	}
	skills, err := s.repo.BuiltinSkillsForAdmin()
	if err != nil {
		return nil, err
	}
	return s.builtinSkillSummaries(skills)
}

func (s *Service) builtinSkillSummary(skillID string) (*BuiltinSkillSummary, error) {
	skill, err := s.repo.Skill(skillID)
	if err != nil {
		return nil, err
	}
	items, err := s.builtinSkillSummaries([]model.Skill{*skill})
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

// AdminBuiltinSkillVersions 返回一个内置技能的摘要和全部版本（新的在前）。
func (s *Service) AdminBuiltinSkillVersions(actor *model.User, skillID string) (*BuiltinSkillSummary, []BuiltinSkillVersionItem, error) {
	skill, err := s.adminBuiltinSkill(actor, skillID)
	if err != nil {
		return nil, nil, err
	}
	versions, err := s.repo.SkillVersions(skill.ID)
	if err != nil {
		return nil, nil, err
	}
	items := make([]BuiltinSkillVersionItem, 0, len(versions))
	for _, v := range versions {
		items = append(items, *builtinVersionItem(skill, v))
	}
	summary, err := s.builtinSkillSummary(skill.ID)
	if err != nil {
		return nil, nil, err
	}
	return summary, items, nil
}

func (s *Service) versionContents(version *model.SkillVersion) (map[string][]byte, error) {
	if version.PackageKey == "" {
		return nil, fmt.Errorf("技能版本 %s 缺少 ZIP 包", version.ID)
	}
	return readSkillArchiveEntries(s.dataDir, version.PackageKey)
}

// AdminBuiltinSkillVersionFiles 列出某个版本的文件。
func (s *Service) AdminBuiltinSkillVersionFiles(actor *model.User, skillID string, versionID string) ([]SkillPackageFileItem, error) {
	skill, err := s.adminBuiltinSkill(actor, skillID)
	if err != nil {
		return nil, err
	}
	version, err := s.skillVersionOf(skill, versionID)
	if err != nil {
		return nil, err
	}
	files, err := s.repo.SkillFiles(version.ID)
	if err != nil {
		return nil, err
	}
	items := make([]SkillPackageFileItem, 0, len(files))
	for _, file := range files {
		items = append(items, skillFileItem(file))
	}
	return items, nil
}

// AdminBuiltinSkillVersionFile 读某个版本里的一个文件（文本文件返回内容）。
func (s *Service) AdminBuiltinSkillVersionFile(actor *model.User, skillID string, versionID string, filePath string) (*SkillPackageFileContent, error) {
	skill, err := s.adminBuiltinSkill(actor, skillID)
	if err != nil {
		return nil, err
	}
	version, err := s.skillVersionOf(skill, versionID)
	if err != nil {
		return nil, err
	}
	filePath, err = normalizeSkillPath(filePath)
	if err != nil {
		return nil, err
	}
	files, err := s.repo.SkillFiles(version.ID)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.Path != filePath {
			continue
		}
		out := &SkillPackageFileContent{File: skillFileItem(file), VersionID: version.ID, Version: version.VersionLabel}
		if !isPreviewText(file.MimeType, file.Path) {
			out.Binary = true
			return out, nil
		}
		if file.Size > maxSkillPreviewBytes {
			return nil, kernel.BadAuthRequest("文件超过 512KB，不能在线编辑")
		}
		content, err := s.readSkillArchiveEntry(version, filePath)
		if err != nil {
			return nil, err
		}
		out.Content = string(content)
		return out, nil
	}
	return nil, kernel.BadAuthRequest("技能文件不存在")
}

// CreateBuiltinSkillVersion 以某个版本为底覆盖、删除文本文件，新建在线版本；activate 时立即生效。
func (s *Service) CreateBuiltinSkillVersion(actor *model.User, skillID string, req BuiltinSkillVersionRequest) (*BuiltinSkillSummary, *BuiltinSkillVersionItem, error) {
	skill, err := s.adminBuiltinSkill(actor, skillID)
	if err != nil {
		return nil, nil, err
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		return nil, nil, kernel.BadAuthRequest("请写明这一版改了什么")
	}
	if utf8.RuneCountInString(note) > builtinSkillNoteMaxRunes {
		return nil, nil, kernel.BadAuthRequest(fmt.Sprintf("版本说明不能超过 %d 字", builtinSkillNoteMaxRunes))
	}
	if len(req.Files)+len(req.DeletePaths) == 0 {
		return nil, nil, kernel.BadAuthRequest("没有改动")
	}
	if len(req.Files) > builtinSkillEditMaxFiles || len(req.DeletePaths) > builtinSkillEditMaxFiles {
		return nil, nil, kernel.BadAuthRequest(fmt.Sprintf("一次最多改 %d 个文件", builtinSkillEditMaxFiles))
	}
	baseID := firstNonEmptyString(req.BaseVersionID, skill.CurrentVersionID)
	base, err := s.skillVersionOf(skill, baseID)
	if err != nil {
		return nil, nil, err
	}
	contents, err := s.versionContents(base)
	if err != nil {
		return nil, nil, err
	}
	files := make(map[string][]byte, len(contents)+len(req.Files))
	for filePath, content := range contents {
		files[filePath] = content
	}
	for _, raw := range req.DeletePaths {
		filePath, err := normalizeSkillPath(raw)
		if err != nil {
			return nil, nil, err
		}
		if filePath == "SKILL.md" {
			return nil, nil, kernel.BadAuthRequest("SKILL.md 不能删除")
		}
		if _, ok := files[filePath]; !ok {
			return nil, nil, kernel.BadAuthRequest("要删除的文件不存在：" + filePath)
		}
		delete(files, filePath)
	}
	for _, edit := range req.Files {
		filePath, err := normalizeSkillPath(edit.Path)
		if err != nil {
			return nil, nil, err
		}
		if !isPreviewText(skillFileMime(filePath, []byte(edit.Content)), filePath) {
			return nil, nil, kernel.BadAuthRequest("只能在线编辑文本文件：" + filePath)
		}
		if !utf8.ValidString(edit.Content) || len(edit.Content) > builtinSkillEditFileMaxLen {
			return nil, nil, kernel.BadAuthRequest(fmt.Sprintf("%s 不是 UTF-8 文本或超过 512KB", filePath))
		}
		files[filePath] = []byte(edit.Content)
	}
	if len(files) > maxSkillPackageFiles {
		return nil, nil, kernel.BadAuthRequest("技能包文件数量超过上限")
	}
	if err := checkBuiltinEntryEdit(contents["SKILL.md"], files["SKILL.md"]); err != nil {
		return nil, nil, err
	}
	archive, err := finalizeSkillArchive(files, parseSkillPackageMetadata(files["SKILL.md"]))
	if err != nil {
		return nil, nil, err
	}
	if archive.TotalBytes > maxSkillPackageBytes {
		return nil, nil, kernel.BadAuthRequest("技能包超过大小上限")
	}
	if archive.ContentHash == base.ContentHash {
		return nil, nil, kernel.BadAuthRequest("内容与底版本相同，没有改动")
	}
	label, err := s.onlineVersionLabel(skill)
	if err != nil {
		return nil, nil, err
	}
	if err := s.addBuiltinSkillVersion(skill.ID, archive, label, SkillOriginOnline, note, actor.ID, req.Activate); err != nil {
		return nil, nil, err
	}
	versions, err := s.repo.SkillVersions(skill.ID)
	if err != nil {
		return nil, nil, err
	}
	summary, err := s.builtinSkillSummary(skill.ID)
	if err != nil {
		return nil, nil, err
	}
	fresh, err := s.repo.Skill(skill.ID)
	if err != nil {
		return nil, nil, err
	}
	for _, v := range versions {
		if v.Origin == SkillOriginOnline && v.VersionLabel == label {
			return summary, builtinVersionItem(fresh, v), nil
		}
	}
	return summary, nil, nil
}

// onlineVersionLabel 是「内置版本号+online.n」，n 是这个技能在线版本的序号。
func (s *Service) onlineVersionLabel(skill *model.Skill) (string, error) {
	counts, err := s.repo.SkillVersionCounts([]string{skill.ID}, SkillOriginOnline)
	if err != nil {
		return "", err
	}
	base := skill.VersionLabel
	if skill.BuiltinVersionID != "" {
		if v, err := s.repo.SkillVersion(skill.BuiltinVersionID); err == nil {
			base = v.VersionLabel
		}
	}
	if base == "" {
		base = "1"
	}
	return fmt.Sprintf("%s+online.%d", base, counts[skill.ID]+1), nil
}

// checkBuiltinEntryEdit 校验在线改过的 SKILL.md：frontmatter 能解析，skillId 不变，
// outputFields 声明过的字段名一个不少（前端解析器靠它拿到 spec 等额外字段）。
func checkBuiltinEntryEdit(before []byte, after []byte) error {
	if len(after) == 0 {
		return kernel.BadAuthRequest("技能包入口必须是 SKILL.md")
	}
	next, err := parseBuiltinSkillMetadata(after)
	if err != nil {
		return kernel.BadAuthRequest("SKILL.md 的 frontmatter 无效：" + err.Error())
	}
	if next.Name == "" || next.Description == "" {
		return kernel.BadAuthRequest("SKILL.md 缺少 name 或 description")
	}
	prev, err := parseBuiltinSkillMetadata(before)
	if err != nil {
		return nil
	}
	if prev.SkillID != next.SkillID {
		return kernel.BadAuthRequest("SKILL.md 的 metadata.skillId 不能修改")
	}
	nextFields, err := skillOutputFieldNames(after)
	if err != nil {
		return kernel.BadAuthRequest("SKILL.md 的 metadata.outputFields 不是合法的单行 JSON 数组：" + err.Error())
	}
	prevFields, _ := skillOutputFieldNames(before)
	for _, name := range prevFields {
		if !slices.Contains(nextFields, name) {
			return kernel.BadAuthRequest("SKILL.md 的 metadata.outputFields 少了字段 " + name + "：前端解析器靠它读取额外输出")
		}
	}
	return nil
}

// skillOutputFieldNames 按前端 skillOutputFields 同一规则读 frontmatter metadata.outputFields 的字段名。
func skillOutputFieldNames(markdown []byte) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(string(markdown), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, nil
	}
	inMetadata := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		indented := len(key) > len(strings.TrimLeft(key, " \t"))
		key = strings.TrimSpace(key)
		if !indented {
			inMetadata = key == "metadata"
			continue
		}
		if !inMetadata || key != "outputFields" {
			continue
		}
		var fields []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &fields); err != nil {
			return nil, err
		}
		names := make([]string, 0, len(fields))
		for _, f := range fields {
			if name := strings.TrimSpace(f.Name); name != "" {
				names = append(names, name)
			}
		}
		return names, nil
	}
	return nil, nil
}

// ActivateBuiltinSkillVersion 把某个版本（在线或历史内置）设为生效。
func (s *Service) ActivateBuiltinSkillVersion(actor *model.User, skillID string, versionID string) (*BuiltinSkillSummary, error) {
	skill, err := s.adminBuiltinSkill(actor, skillID)
	if err != nil {
		return nil, err
	}
	version, err := s.skillVersionOf(skill, versionID)
	if err != nil {
		return nil, err
	}
	if err := s.activateSkillVersion(skill, version); err != nil {
		return nil, err
	}
	return s.builtinSkillSummary(skill.ID)
}

// RevertBuiltinSkill 回退到内置版本。
func (s *Service) RevertBuiltinSkill(actor *model.User, skillID string) (*BuiltinSkillSummary, error) {
	skill, err := s.adminBuiltinSkill(actor, skillID)
	if err != nil {
		return nil, err
	}
	if skill.BuiltinVersionID == "" {
		return nil, kernel.BadAuthRequest("这个技能还没有内置版本记录，重启服务同步后再试")
	}
	version, err := s.skillVersionOf(skill, skill.BuiltinVersionID)
	if err != nil {
		return nil, err
	}
	if err := s.activateSkillVersion(skill, version); err != nil {
		return nil, err
	}
	return s.builtinSkillSummary(skill.ID)
}

func (s *Service) activateSkillVersion(skill *model.Skill, version *model.SkillVersion) error {
	if version.ID == skill.CurrentVersionID {
		return nil
	}
	contents, err := s.versionContents(version)
	if err != nil {
		return err
	}
	entry, ok := contents["SKILL.md"]
	if !ok {
		return fmt.Errorf("技能版本 %s 缺少 SKILL.md", version.ID)
	}
	return s.repo.UpdateSkillColumns(skill.ID, activeSkillVersionColumns(version, entry))
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
