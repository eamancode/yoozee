// 流程技能包（M6 第二期）：短剧流程每一步用的技能从本地 yoozee-skills 构建成 zip，管理员上传；静态校验通过就存成新版本
// （origin=package，状态「已校验未启用」），手动启用，回退就是启用任一历史版本。线上不在线编辑包。
// 版本存在 skills / skill_versions 里，清单 package.json 另存 skill_versions.manifest；内置目录里的同名技能只作首次安装的种子。

package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

const (
	SkillOriginPackage = "package"

	// 包版本的静态校验结论：通过；按当前线上步骤表不兼容（契约升级后旧包会落到这里）；不是包版本（内置或在线编辑的历史版本）。
	FlowPackageCheckPassed     = "passed"
	FlowPackageCheckFailed     = "failed"
	FlowPackageCheckNotPackage = "not_package"
)

// FlowPackageCheck 是一个版本按当前线上规则重跑静态校验的结论。
type FlowPackageCheck struct {
	Status   string                     `json:"status"`
	Problems []drama.StepPackageProblem `json:"problems"`
}

// FlowPackageVersionItem 是后台版本列表的一行。
type FlowPackageVersionItem struct {
	ID            string           `json:"id"`
	VersionLabel  string           `json:"versionLabel"`
	Origin        string           `json:"origin"`
	ContentHash   string           `json:"contentHash"`
	FileCount     int              `json:"fileCount"`
	TotalBytes    int64            `json:"totalBytes"`
	Note          string           `json:"note"`
	Step          string           `json:"step,omitempty"`
	Contract      string           `json:"contract,omitempty"`
	Profiles      []string         `json:"profiles"`
	CreatedBy     string           `json:"createdBy"`
	CreatedByName string           `json:"createdByName"`
	CreatedAt     time.Time        `json:"createdAt"`
	Active        bool             `json:"active"`
	Check         FlowPackageCheck `json:"check"`
}

// FlowPackageSummary 是一个流程技能包（一个技能）。
type FlowPackageSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Step / Contract 取生效版本的清单；StepContract 是线上步骤表这一步现在的契约版本。
	Step           string                  `json:"step"`
	StepLabel      string                  `json:"stepLabel"`
	Contract       string                  `json:"contract"`
	StepContract   string                  `json:"stepContract"`
	VersionCount   int                     `json:"versionCount"`
	CurrentVersion *FlowPackageVersionItem `json:"currentVersion,omitempty"`
}

// FlowPackageUploadResult 是上传成功后的包和新版本（未启用）。
type FlowPackageUploadResult struct {
	Package FlowPackageSummary     `json:"package"`
	Version FlowPackageVersionItem `json:"version"`
}

// flowPackageHashDoc 是 build.py 写的 package.hash.json。
type flowPackageHashDoc struct {
	ContentHash string            `json:"contentHash"`
	Files       map[string]string `json:"files"`
}

// PackageCheckFailed 是上传的包没过静态校验：details.problems 逐条列出原因（规则编号、说明、涉及的文件）。
func PackageCheckFailed(problems []drama.StepPackageProblem) error {
	err := kernel.BadAuthRequest(fmt.Sprintf("技能包没通过静态校验（%d 条）：%s", len(problems), problems[0].Message))
	err.Reason = kernel.ReasonPackageCheckFailed
	err.Details = map[string]any{"problems": problems}
	return err
}

// UploadFlowPackage 收一个 build.py --package 构建的 zip：核对内容哈希，按线上步骤表做静态校验，版本号不能重复；
// 通过后存成新版本（不启用），不通过返回 package_check_failed 和逐条原因。
func (s *Service) UploadFlowPackage(actor *model.User, header *multipart.FileHeader, note string) (*FlowPackageUploadResult, error) {
	if err := s.requireActiveAdmin(actor); err != nil {
		return nil, err
	}
	if header == nil || header.Size <= 0 || header.Size > maxSkillPackageBytes {
		return nil, kernel.BadAuthRequest("技能包大小必须在 1B-20MB 之间")
	}
	file, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSkillPackageBytes+1))
	if err != nil {
		return nil, err
	}
	return s.uploadFlowPackage(actor.ID, data, note)
}

func (s *Service) uploadFlowPackage(actorID string, data []byte, note string) (*FlowPackageUploadResult, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > builtinSkillNoteMaxRunes {
		return nil, kernel.BadAuthRequest(fmt.Sprintf("版本说明不能超过 %d 字", builtinSkillNoteMaxRunes))
	}
	if len(data) > maxSkillPackageBytes {
		return nil, kernel.BadAuthRequest("技能包不能超过 20MB")
	}
	archive, err := archiveFromZip(data, "")
	if err != nil {
		var appErr *kernel.AppError
		if errors.As(err, &appErr) && appErr.Status == 400 {
			return nil, PackageCheckFailed([]drama.StepPackageProblem{{Rule: drama.PackageRuleArchive, Message: appErr.Message}})
		}
		return nil, err
	}
	files := archive.Files
	var problems []drama.StepPackageProblem
	if raw, ok := files[drama.StepPackageHashFile]; ok {
		delete(files, drama.StepPackageHashFile)
		problems = append(problems, checkFlowPackageHash(raw, files)...)
	} else {
		problems = append(problems, drama.StepPackageProblem{Rule: drama.PackageRuleHash, Path: drama.StepPackageHashFile, Message: "包里没有内容哈希 " + drama.StepPackageHashFile + "，请用 python yoozee-skills/build.py --package <id> 构建"})
	}
	manifestRaw := files[drama.StepPackageManifestFile]
	manifest, manifestProblems := drama.ParseStepPackageManifest(manifestRaw)
	problems = append(problems, manifestProblems...)
	var skill *model.Skill
	if len(manifestProblems) == 0 {
		problems = append(problems, drama.CheckStepPackage(manifest, files)...)
		skill, err = s.flowPackageSkill(manifest.ID)
		switch {
		case err == nil:
			duplicate, err := s.flowPackageVersionTaken(skill.ID, manifest.Version)
			if err != nil {
				return nil, err
			}
			if duplicate {
				problems = append(problems, drama.StepPackageProblem{Rule: drama.PackageRuleVersion, Message: fmt.Sprintf("版本号 %s 已经有了，改 package.json 的 version 再构建", manifest.Version)})
			}
		case isFlowPackageMissing(err):
			problems = append(problems, drama.StepPackageProblem{Rule: drama.PackageRuleManifest, Path: drama.StepPackageManifestFile, Message: err.Error()})
		default:
			return nil, err
		}
		if meta, err := parseBuiltinSkillMetadata(files["SKILL.md"]); err == nil && meta.SkillID != "" && meta.SkillID != manifest.ID {
			problems = append(problems, drama.StepPackageProblem{Rule: drama.PackageRuleEntry, Path: "SKILL.md", Message: fmt.Sprintf("SKILL.md 的 metadata.skillId 是 %s，清单 id 是 %s", meta.SkillID, manifest.ID)})
		}
	}
	finalized, err := finalizeSkillArchive(files, parseSkillPackageMetadata(files["SKILL.md"]))
	if err != nil {
		var appErr *kernel.AppError
		if !errors.As(err, &appErr) {
			return nil, err
		}
		problems = append(problems, drama.StepPackageProblem{Rule: drama.PackageRuleEntry, Path: "SKILL.md", Message: appErr.Message})
	}
	if len(problems) > 0 {
		return nil, PackageCheckFailed(problems)
	}
	versionID, err := s.addSkillVersionWith(skill.ID, finalized, model.SkillVersion{VersionLabel: manifest.Version, Origin: SkillOriginPackage, Note: note, CreatedBy: actorID, Manifest: string(manifestRaw)}, false)
	if err != nil {
		return nil, err
	}
	summary, items, err := s.flowPackageVersions(skill.ID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID == versionID {
			return &FlowPackageUploadResult{Package: *summary, Version: item}, nil
		}
	}
	return nil, fmt.Errorf("技能包 %s 新版本 %s 写入后没读到", skill.ID, versionID)
}

// checkFlowPackageHash 核对 build.py 写的哈希：每个文件的 sha256 与总的内容哈希（与 contentHashAndSize 同一算法）。
func checkFlowPackageHash(raw []byte, files map[string][]byte) []drama.StepPackageProblem {
	bad := func(format string, args ...any) drama.StepPackageProblem {
		return drama.StepPackageProblem{Rule: drama.PackageRuleHash, Path: drama.StepPackageHashFile, Message: fmt.Sprintf(format, args...)}
	}
	var doc flowPackageHashDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return []drama.StepPackageProblem{bad("%s 不是合法 JSON：%v", drama.StepPackageHashFile, err)}
	}
	var out []drama.StepPackageProblem
	for _, p := range sortedSkillPaths(files) {
		want, ok := doc.Files[p]
		if !ok {
			out = append(out, bad("%s 不在构建时的哈希清单里", p))
			continue
		}
		digest := sha256.Sum256(files[p])
		if hex.EncodeToString(digest[:]) != want {
			out = append(out, bad("%s 的内容与构建时不同（哈希不符）", p))
		}
	}
	for p := range doc.Files {
		if _, ok := files[p]; !ok {
			out = append(out, bad("构建时有 %s，包里没有", p))
		}
	}
	if hash, _ := contentHashAndSize(files); len(out) == 0 && hash != doc.ContentHash {
		out = append(out, bad("内容哈希 %s 与构建时的 %s 不符", hash, doc.ContentHash))
	}
	return out
}

type flowPackageMissingError struct{ message string }

func (e *flowPackageMissingError) Error() string { return e.message }

func isFlowPackageMissing(err error) bool {
	var target *flowPackageMissingError
	return errors.As(err, &target)
}

// flowPackageSkill 取一个已登记的流程技能包（有包版本的技能）。新包 ID 本期不自动建技能。
func (s *Service) flowPackageSkill(skillID string) (*model.Skill, error) {
	skill, err := s.repo.Skill(strings.TrimSpace(skillID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &flowPackageMissingError{fmt.Sprintf("流程技能包 %s 还没有登记：本期只能给已有的流程技能包上传新版本", skillID)}
	}
	if err != nil {
		return nil, err
	}
	ids, err := s.repo.PackageSkillIDs()
	if err != nil {
		return nil, err
	}
	if !ids[skill.ID] {
		return nil, &flowPackageMissingError{fmt.Sprintf("技能 %s 不是流程技能包", skillID)}
	}
	return skill, nil
}

func (s *Service) flowPackageVersionTaken(skillID string, label string) (bool, error) {
	versions, err := s.repo.SkillVersions(skillID)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(versions, func(v model.SkillVersion) bool { return v.VersionLabel == label }), nil
}

// AdminFlowPackages 列出流程技能包及其生效版本。
func (s *Service) AdminFlowPackages(actor *model.User) ([]FlowPackageSummary, error) {
	if err := s.requireActiveAdmin(actor); err != nil {
		return nil, err
	}
	ids, err := s.repo.PackageSkillIDs()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	slices.Sort(keys)
	out := make([]FlowPackageSummary, 0, len(keys))
	for _, id := range keys {
		summary, _, err := s.flowPackageVersions(id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *summary)
	}
	return out, nil
}

// AdminFlowPackageVersions 返回一个包的全部版本（新的在前），每个版本按当前线上规则重跑静态校验。
func (s *Service) AdminFlowPackageVersions(actor *model.User, skillID string) (*FlowPackageSummary, []FlowPackageVersionItem, error) {
	if err := s.requireActiveAdmin(actor); err != nil {
		return nil, nil, err
	}
	if _, err := s.adminFlowPackageSkill(skillID); err != nil {
		return nil, nil, err
	}
	return s.flowPackageVersions(strings.TrimSpace(skillID))
}

func (s *Service) adminFlowPackageSkill(skillID string) (*model.Skill, error) {
	skill, err := s.flowPackageSkill(skillID)
	if isFlowPackageMissing(err) {
		return nil, kernel.NotFound(err.Error())
	}
	return skill, err
}

// ActivateFlowPackageVersion 手动启用一个包版本；回退就是启用任一历史版本。按当前线上规则校验不过的版本不能启用。
func (s *Service) ActivateFlowPackageVersion(actor *model.User, skillID string, versionID string) (*FlowPackageSummary, error) {
	if err := s.requireActiveAdmin(actor); err != nil {
		return nil, err
	}
	skill, err := s.adminFlowPackageSkill(skillID)
	if err != nil {
		return nil, err
	}
	version, err := s.skillVersionOf(skill, versionID)
	if err != nil {
		return nil, err
	}
	if version.Origin != SkillOriginPackage {
		return nil, kernel.BadAuthRequest(fmt.Sprintf("版本 %s 不是包版本（内置或在线编辑的历史版本），不能在流程里启用", version.VersionLabel))
	}
	check, err := s.flowPackageVersionCheck(version)
	if err != nil {
		return nil, err
	}
	if check.Status != FlowPackageCheckPassed {
		return nil, PackageCheckFailed(check.Problems)
	}
	if version.ID != skill.CurrentVersionID {
		contents, err := s.versionContents(version)
		if err != nil {
			return nil, err
		}
		entry, ok := contents["SKILL.md"]
		if !ok {
			return nil, fmt.Errorf("技能版本 %s 缺少 SKILL.md", version.ID)
		}
		values := activeSkillVersionColumns(version, entry)
		// 包技能的名字和简介跟着生效版本的 SKILL.md 走（启动同步不再用内置目录覆盖）。
		if meta := parseSkillPackageMetadata(entry); meta.Name != "" && meta.Description != "" {
			values["name"], values["description"] = meta.Name, meta.Description
		}
		if err := s.repo.UpdateSkillColumns(skill.ID, values); err != nil {
			return nil, err
		}
	}
	summary, _, err := s.flowPackageVersions(skill.ID)
	return summary, err
}

// flowPackageVersions 读一个包的摘要与全部版本。
func (s *Service) flowPackageVersions(skillID string) (*FlowPackageSummary, []FlowPackageVersionItem, error) {
	skill, err := s.repo.Skill(skillID)
	if err != nil {
		return nil, nil, err
	}
	versions, err := s.repo.SkillVersions(skill.ID)
	if err != nil {
		return nil, nil, err
	}
	creators := []string{}
	for _, v := range versions {
		if v.CreatedBy != "" && !slices.Contains(creators, v.CreatedBy) {
			creators = append(creators, v.CreatedBy)
		}
	}
	users, err := s.repo.SkillOwners(creators)
	if err != nil {
		return nil, nil, err
	}
	items := make([]FlowPackageVersionItem, 0, len(versions))
	summary := &FlowPackageSummary{ID: skill.ID, Name: skill.Name, Description: skill.Description}
	for i := range versions {
		v := &versions[i]
		item := FlowPackageVersionItem{
			ID: v.ID, VersionLabel: v.VersionLabel, Origin: v.Origin, ContentHash: v.ContentHash, FileCount: v.FileCount, TotalBytes: v.TotalBytes,
			Note: v.Note, Profiles: []string{}, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt, Active: v.ID == skill.CurrentVersionID,
		}
		if user, ok := users[v.CreatedBy]; ok {
			item.CreatedByName = firstNonEmptyString(user.DisplayName, user.Username)
		}
		if m, problems := drama.ParseStepPackageManifest([]byte(v.Manifest)); v.Origin == SkillOriginPackage && len(problems) == 0 {
			item.Step, item.Contract, item.Profiles = m.Step, m.Contract, m.Profiles
		}
		item.Check, err = s.flowPackageVersionCheck(v)
		if err != nil {
			return nil, nil, err
		}
		if v.Origin == SkillOriginPackage {
			summary.VersionCount++
		}
		items = append(items, item)
		if item.Active {
			current := item
			summary.CurrentVersion = &current
			summary.Step, summary.Contract = item.Step, item.Contract
		}
	}
	if summary.Step == "" {
		for _, item := range items {
			if item.Step != "" {
				summary.Step = item.Step
				break
			}
		}
	}
	if step, ok := drama.StepByID(summary.Step); ok {
		summary.StepLabel, summary.StepContract = step.Label, step.Contract
	}
	return summary, items, nil
}

// flowPackageVersionCheck 按当前线上步骤表重跑一个版本的静态校验（读版本 zip，清单取 manifest 列）。
func (s *Service) flowPackageVersionCheck(version *model.SkillVersion) (FlowPackageCheck, error) {
	if version.Origin != SkillOriginPackage {
		return FlowPackageCheck{Status: FlowPackageCheckNotPackage, Problems: []drama.StepPackageProblem{}}, nil
	}
	manifest, problems := drama.ParseStepPackageManifest([]byte(version.Manifest))
	if len(problems) == 0 {
		contents, err := s.versionContents(version)
		if err != nil {
			return FlowPackageCheck{}, err
		}
		problems = drama.CheckStepPackage(manifest, contents)
	}
	if len(problems) > 0 {
		return FlowPackageCheck{Status: FlowPackageCheckFailed, Problems: problems}, nil
	}
	return FlowPackageCheck{Status: FlowPackageCheckPassed, Problems: []drama.StepPackageProblem{}}, nil
}

// seedFlowPackage 首次安装时把内置目录里的流程技能包落成第一个包版本（之后启动不再看内置目录）。
func (s *Service) seedFlowPackage(skill *model.Skill, archive skillPackageArchive) error {
	raw := archive.Files[drama.StepPackageManifestFile]
	manifest, problems := drama.ParseStepPackageManifest(raw)
	if len(problems) == 0 {
		problems = drama.CheckStepPackage(manifest, archive.Files)
	}
	if len(problems) > 0 {
		return fmt.Errorf("内置流程技能包 %s 没过静态校验：%s", skill.ID, problems[0].Message)
	}
	if manifest.ID != skill.ID {
		return fmt.Errorf("内置流程技能包目录 %s 的清单 id 是 %s", skill.ID, manifest.ID)
	}
	activate := true
	if current := strings.TrimSpace(skill.CurrentVersionID); current != "" {
		if version, err := s.repo.SkillVersion(current); err == nil && version.Origin == SkillOriginOnline {
			activate = false
		}
	}
	_, err := s.addSkillVersionWith(skill.ID, archive, model.SkillVersion{VersionLabel: manifest.Version, Origin: SkillOriginPackage, Note: "首次安装的内置种子", Manifest: string(raw)}, activate)
	return err
}

func isFlowPackageSeed(archive skillPackageArchive) bool {
	_, ok := archive.Files[drama.StepPackageManifestFile]
	return ok
}
