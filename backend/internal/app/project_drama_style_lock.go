// 风格锁定（style.lock，M8 卡 A）：项目级步骤。建项目时用户写了风格文字或传了参考图（或只选了画风），前端建完项目立刻跑这一步：
// 服务端按项目类型、风格文字、画风、风格档案和需求组装提示词，参考图以图片引用（input.referenceImages = resource:ID，执行前按
// 现有媒体水合换成供应商能读的地址）随任务发给文本模型，一个任务一轮。收尾只解析、检查，把风格锚和主基调色卡写进任务结果
// result.dramaStep（候选），不动项目；用户在侧栏点「锁定此风格」（或建项目时直接选了平台风格库的条目）才由 LockDramaStyle
// 写进 bible_json（style_anchor 整体替换、master_palette、style_lock），并用代码合成主色卡图、建 / 加版本主色卡资产
// （分类 palette，payload.metadata.masterPalette.kind=master，与场次色卡区分）。

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// masterPaletteKey 是主色卡资产 payload.metadata 里的键。
const masterPaletteKey = "masterPalette"

// dramaStyleLockMeta 是任务 metadata.dramaStyleLock：这次分析的输入（「重新分析」按它回填对话框）。
type dramaStyleLockMeta struct {
	ProjectType string   `json:"projectType"`
	Text        string   `json:"text,omitempty"`
	Images      []string `json:"images,omitempty"`
	// Source 是 analysis（有文字或参考图）或 preset（只按画风生成）。
	Source string `json:"source"`
	// AutoLock 是收尾直接锁定（建项目时只选了画风预设：按画风当场生成主色卡存在项目上，不用再点锁定）。
	AutoLock bool `json:"autoLock,omitempty"`
}

// DramaStyleLockOutcome 是风格锁定收尾写进 result.dramaStep 的候选：用户点「锁定此风格」时取它写进项目。
type DramaStyleLockOutcome struct {
	Step          string                    `json:"step"`
	Status        string                    `json:"status"`
	Source        string                    `json:"source"`
	ProjectType   string                    `json:"projectType"`
	StyleAnchor   *drama.StyleAnchor        `json:"styleAnchor"`
	MasterPalette drama.MasterPalette       `json:"masterPalette"`
	Parse         drama.StyleLockParseStats `json:"parse"`
	Errors        int                       `json:"errors"`
	Warnings      int                       `json:"warnings"`
	Items         []drama.CheckItem         `json:"items,omitempty"`
	// Locked 是收尾直接锁定（AutoLock）写进项目的锁定记录。
	Locked *drama.StyleLockRecord `json:"locked,omitempty"`
}

// 风格锁定任务结果的状态：candidate 是候选，等用户锁定；locked 是收尾已直接锁定。
const (
	DramaStyleLockOutcomeCandidate = "candidate"
	DramaStyleLockOutcomeLocked    = "locked"
)

// DramaStyleLockRequest 是「锁定此风格」：TaskID（风格分析任务的候选）和 LibraryID（平台风格库的条目）二选一。
type DramaStyleLockRequest struct {
	TaskID    string `json:"taskId,omitempty"`
	LibraryID string `json:"libraryId,omitempty"`
}

// DramaStyleLockResult 是锁定后的项目风格：风格锚、主基调色卡、来源，主色卡资产和合成图。
type DramaStyleLockResult struct {
	StyleAnchor   *drama.StyleAnchor     `json:"styleAnchor"`
	MasterPalette drama.MasterPalette    `json:"masterPalette"`
	StyleLock     drama.StyleLockRecord  `json:"styleLock"`
	Asset         *ProjectAssetSummary   `json:"asset,omitempty"`
	ImageError    string                 `json:"imageError,omitempty"`
	FontNote      string                 `json:"fontNote,omitempty"`
	Library       *DramaStyleLibraryItem `json:"library,omitempty"`
}

// projectLockedStyle 读项目锁定的风格：风格锚、主基调色卡、来源；locked 是风格锚非空且色板至少一个颜色。读不出的字段当作没有。
func projectLockedStyle(bibleJSON string) (anchor *drama.StyleAnchor, palette drama.MasterPalette, record drama.StyleLockRecord, locked bool) {
	var doc struct {
		StyleAnchor   *drama.StyleAnchor     `json:"style_anchor"`
		MasterPalette *drama.MasterPalette   `json:"master_palette"`
		StyleLock     *drama.StyleLockRecord `json:"style_lock"`
	}
	if raw := strings.TrimSpace(bibleJSON); raw != "" {
		var loose map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &loose)
		_ = json.Unmarshal(loose["style_anchor"], &doc.StyleAnchor)
		_ = json.Unmarshal(loose["master_palette"], &doc.MasterPalette)
		_ = json.Unmarshal(loose["style_lock"], &doc.StyleLock)
	}
	if doc.MasterPalette != nil {
		palette = *doc.MasterPalette
	}
	if doc.StyleLock != nil {
		record = *doc.StyleLock
	}
	return doc.StyleAnchor, palette, record, !doc.StyleAnchor.Empty() && !palette.Empty()
}

// projectLockedAnchor 是项目锁定的风格锚；没有锁定（没有主基调色卡）时返回 nil。
func projectLockedAnchor(bibleJSON string) *drama.StyleAnchor {
	anchor, _, _, locked := projectLockedStyle(bibleJSON)
	if !locked {
		return nil
	}
	return anchor
}

// planStyleLock 校验项目类型、风格文字和参考图（要是本人已就绪的图片资源），组装提示词。
func (s *Service) planStyleLock(userID string, project *model.Project, step drama.Step, req DramaProjectStepRunRequest) (*dramaStepPlan, error) {
	projectType := strings.TrimSpace(req.ProjectType)
	if projectType == "" {
		_, _, record, _ := projectLockedStyle(project.BibleJSON)
		projectType = record.ProjectType
	}
	if drama.ProjectTypeLabel(projectType) == "" {
		return nil, BadAuthRequest("请先选项目类型（仿真人、3D 动漫、写实 CG、2D 动漫）")
	}
	text := strings.TrimSpace(req.StyleText)
	if drama.JSLen(text) > drama.StyleLockTextChars {
		return nil, BadAuthRequest(fmt.Sprintf("风格描述最多 %d 字", drama.StyleLockTextChars))
	}
	var ids []string
	seen := map[string]bool{}
	for _, id := range req.ReferenceImages {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) > drama.StyleLockMaxImages {
		return nil, BadAuthRequest(fmt.Sprintf("参考图最多 %d 张", drama.StyleLockMaxImages))
	}
	refs := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		resource, err := s.repo.ResourceForUser(userID, id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, BadAuthRequest("参考图不存在或不属于当前账号，请重新上传")
		}
		if err != nil {
			return nil, err
		}
		if resource.Status != model.ResourceStatusReady || !strings.HasPrefix(strings.ToLower(resource.MimeType), "image/") {
			return nil, BadAuthRequest("参考图还没上传完，或不是图片")
		}
		refs = append(refs, map[string]any{"id": resource.ID, "type": "image", "storageKey": "resource:" + resource.ID, "mimeType": resource.MimeType, "width": resource.Width, "height": resource.Height})
	}
	source := drama.StyleLockSourceAnalysis
	if text == "" && len(ids) == 0 {
		source = drama.StyleLockSourcePreset
		if style := projectStyleSnapshot(project); style.Prompt == "" && len(style.Selection) == 0 {
			return nil, BadAuthRequest("写一段风格描述、传参考图，或先选项目画风，再分析项目风格")
		}
	}
	usage, skill, err := s.dramaStepSkill(project, step)
	if err != nil {
		return nil, err
	}
	plan, err := styleLockPlan(project, &dramaStyleLockMeta{ProjectType: projectType, Text: text, Images: ids, Source: source, AutoLock: req.LockOnFinish}, usage, skill)
	if err != nil {
		return nil, err
	}
	plan.referenceImages = refs
	return plan, nil
}

// styleLockPlan 组装风格锁定的提示词与运行参数（不读库，评测直接拿评测集的项目喂进来）。一轮，不切轮次。
func styleLockPlan(project *model.Project, lock *dramaStyleLockMeta, usage DramaStepUsage, skill drama.StepSkill) (*dramaStepPlan, error) {
	style := projectStyleSnapshot(project)
	in := drama.StyleLockInput{
		ProjectType: lock.ProjectType, Text: lock.Text, Images: len(lock.Images), BriefJSON: project.BriefJSON,
		Style: drama.StepStyle{Title: style.Title, Prompt: style.Prompt, Selection: style.Selection}, Skill: skill,
	}
	if p, err := drama.BuiltinProfile(projectDramaContext(project).profileID()); err == nil {
		in.Profile = drama.StyleBibleProfile{Label: p.Label, StyleBlock: p.StyleBlock.Zh, LightPolicy: p.LightPolicy}
	}
	prompt, err := drama.BuildStyleLockPrompt(in)
	if err != nil {
		return nil, err
	}
	return &dramaStepPlan{
		usage: usage, prompt: prompt, skill: skill,
		record: map[string]any{"requirements": "锁定全片风格：输出全局风格锚和按主角组、配角组、道具组、环境组分组的主基调色卡。"},
		lock:   lock,
	}, nil
}

// taskDramaStyleLock 从任务输入读 metadata.dramaStyleLock。
func taskDramaStyleLock(inputJSON string) *dramaStyleLockMeta {
	var input struct {
		Metadata struct {
			DramaStyleLock *dramaStyleLockMeta `json:"dramaStyleLock"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil {
		return nil
	}
	return input.Metadata.DramaStyleLock
}

// applyStyleLockTask 是风格锁定的收尾：解析、检查，有 error 时任务失败；否则把候选写进任务结果，项目不动。
func (s *Service) applyStyleLockTask(task model.Task, meta *dramaStepTaskMeta, result map[string]interface{}) (DramaStyleLockOutcome, error) {
	lock := taskDramaStyleLock(task.InputJSON)
	projectID := taskProjectID(task)
	if lock == nil || projectID == "" {
		return DramaStyleLockOutcome{}, errors.New("风格锁定任务缺少项目或运行参数")
	}
	project, err := s.activeProjectForUser(task.UserID, projectID)
	if err != nil {
		return DramaStyleLockOutcome{}, err
	}
	text, _ := result["text"].(string)
	out, stats := drama.StyleLockFromText(text)
	report := drama.CheckStyleLock(out)
	if report.Errors > 0 {
		for _, item := range report.Items {
			if item.Level == "error" {
				return DramaStyleLockOutcome{}, fmt.Errorf("风格锁定检查有 %d 个问题：%s", report.Errors, item.Message)
			}
		}
	}
	outcome := DramaStyleLockOutcome{
		Step: meta.Step, Status: DramaStyleLockOutcomeCandidate, Source: lock.Source, ProjectType: lock.ProjectType,
		StyleAnchor: out.Anchor, MasterPalette: out.Palette, Parse: stats, Errors: report.Errors, Warnings: report.Warnings,
	}
	for _, item := range report.Items {
		if len(outcome.Items) < dramaAdaptOutcomeItems {
			outcome.Items = append(outcome.Items, item)
		}
	}
	if lock.AutoLock {
		locked, err := s.applyDramaStyleLock(task.UserID, project, styleLockInput{Anchor: out.Anchor, Palette: out.Palette, Title: project.Name, Record: drama.StyleLockRecord{Source: lock.Source, ProjectType: lock.ProjectType, TaskID: task.ID}})
		if err != nil {
			return DramaStyleLockOutcome{}, fmt.Errorf("风格已分析出来，锁定写入失败：%w", err)
		}
		if locked.ImageError != "" {
			_ = s.log(task.UserID, task.ID, "warn", "主色卡图没有绑上", locked.ImageError)
		}
		outcome.Status, outcome.Locked = DramaStyleLockOutcomeLocked, &locked.StyleLock
	}
	return outcome, nil
}

// LockDramaStyle 是「锁定此风格」：取风格分析任务的候选，或平台风格库的条目，写进项目。
func (s *Service) LockDramaStyle(userID string, projectID string, req DramaStyleLockRequest) (DramaStyleLockResult, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return DramaStyleLockResult{}, err
	}
	taskID, libraryID := strings.TrimSpace(req.TaskID), strings.TrimSpace(req.LibraryID)
	if (taskID == "") == (libraryID == "") {
		return DramaStyleLockResult{}, BadAuthRequest("锁定风格要点名风格分析任务或风格库条目（二选一）")
	}
	var lock styleLockInput
	if libraryID != "" {
		entry, err := s.repo.DramaStyleLibraryEntry(libraryID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DramaStyleLockResult{}, NotFound("风格库里没有这一条，可能已被删除")
		}
		if err != nil {
			return DramaStyleLockResult{}, err
		}
		anchor, palette := styleLibraryEntryStyle(entry)
		lock = styleLockInput{Anchor: anchor, Palette: palette, Title: entry.Name, Record: drama.StyleLockRecord{Source: drama.StyleLockSourceLibrary, LibraryID: entry.ID, ProjectType: entry.ProjectType}}
	} else {
		task, err := s.repo.TaskForUser(userID, taskID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DramaStyleLockResult{}, BadAuthRequest("风格分析任务不存在")
		}
		if err != nil {
			return DramaStyleLockResult{}, err
		}
		meta := taskDramaStep(task.InputJSON)
		if meta == nil || meta.Step != drama.StepStyleLock || taskProjectID(*task) != project.ID {
			return DramaStyleLockResult{}, BadAuthRequest("这个任务不是本项目的风格分析")
		}
		if task.Status != model.TaskStatusSucceeded {
			return DramaStyleLockResult{}, BadAuthRequest("风格分析还没有成功完成，不能锁定")
		}
		var result struct {
			DramaStep *DramaStyleLockOutcome `json:"dramaStep"`
		}
		if json.Unmarshal([]byte(task.ResultJSON), &result) != nil || result.DramaStep == nil || result.DramaStep.StyleAnchor.Empty() {
			return DramaStyleLockResult{}, BadAuthRequest("风格分析任务的结果里读不出风格锚和主基调色卡")
		}
		outcome := result.DramaStep
		lock = styleLockInput{Anchor: outcome.StyleAnchor, Palette: outcome.MasterPalette, Title: project.Name, Record: drama.StyleLockRecord{Source: firstNonEmpty(outcome.Source, drama.StyleLockSourceAnalysis), ProjectType: outcome.ProjectType, TaskID: task.ID}}
	}
	return s.applyDramaStyleLock(userID, project, lock)
}

// styleLockInput 是要写进项目的锁定风格。
type styleLockInput struct {
	Anchor  *drama.StyleAnchor
	Palette drama.MasterPalette
	// Title 是合成图上的标题（风格库条目名或项目名）。
	Title  string
	Record drama.StyleLockRecord
}

// masterPaletteMeta 是主色卡资产 payload.metadata.masterPalette。
type masterPaletteMeta struct {
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	LibraryID string `json:"libraryId,omitempty"`
}

// masterPaletteDefinition 是主色卡版本的设定（asset_versions.definition_json）。
type masterPaletteDefinition struct {
	Kind        string              `json:"kind"`
	Title       string              `json:"title"`
	Palette     drama.MasterPalette `json:"palette"`
	StyleAnchor *drama.StyleAnchor  `json:"styleAnchor,omitempty"`
	Source      string              `json:"source"`
	LibraryID   string              `json:"libraryId,omitempty"`
}

// masterPaletteMetaOf 是主色卡资产的标记；不是主色卡时 ok 为 false。
func masterPaletteMetaOf(asset *model.Asset) (masterPaletteMeta, bool) {
	if asset.Category != model.AssetCategoryPalette {
		return masterPaletteMeta{}, false
	}
	meta, ok := decodeInto[masterPaletteMeta](payloadMetadata(assetPayload(asset))[masterPaletteKey])
	return meta, ok && meta.Kind == "master"
}

// projectMasterPaletteAsset 是项目的主色卡资产（最早建的那张）；没有返回 nil。
func (s *Service) projectMasterPaletteAsset(userID string, projectID string) (*model.Asset, error) {
	assets, err := s.repo.ProjectAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	var found *model.Asset
	for i := range assets {
		if _, ok := masterPaletteMetaOf(&assets[i]); ok && (found == nil || assets[i].CreatedAt.Before(found.CreatedAt)) {
			found = &assets[i]
		}
	}
	return found, nil
}

// applyDramaStyleLock 写锁定结果：合成主色卡图存成资源 → bible_json（style_anchor 整体替换、master_palette、style_lock）和主色卡资产
// （新建或加版本）同一事务 → 图绑到主色卡这一版。图绑不上时锁定已经生效，结果里写明原因。
func (s *Service) applyDramaStyleLock(userID string, project *model.Project, in styleLockInput) (DramaStyleLockResult, error) {
	palette := drama.NormalizeMasterPalette(in.Palette)
	if in.Anchor.Empty() {
		return DramaStyleLockResult{}, BadAuthRequest("风格锚是空的，不能锁定")
	}
	if err := drama.ValidateMasterPalette(palette); err != nil {
		return DramaStyleLockResult{}, BadAuthRequest(err.Error())
	}
	resource, fontNote, err := s.renderMasterPaletteResource(userID, firstNonEmpty(strings.TrimSpace(in.Title), project.Name), palette)
	if err != nil {
		return DramaStyleLockResult{}, err
	}
	now := time.Now()
	record := in.Record
	record.ResourceID, record.LockedAt, record.LockedBy = resource.ID, now.Format(time.RFC3339), userID
	write, versionID, err := s.masterPaletteWrite(userID, project, in, palette, now)
	if err != nil {
		return DramaStyleLockResult{}, err
	}
	record.AssetID = write.Version.AssetID
	doc := map[string]any{}
	if raw := strings.TrimSpace(project.BibleJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil || doc == nil {
			return DramaStyleLockResult{}, BadAuthRequest("项目的连戏表不是合法 JSON，没有写入锁定风格")
		}
	}
	for key, value := range map[string]any{"style_anchor": in.Anchor, "master_palette": palette, "style_lock": record} {
		encoded, _ := json.Marshal(value)
		var plain any
		_ = json.Unmarshal(encoded, &plain)
		doc[key] = plain
	}
	if err := validateDramaBibleDocument(doc); err != nil {
		return DramaStyleLockResult{}, err
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return DramaStyleLockResult{}, err
	}
	if err := s.repo.ApplyDramaStyleLock(userID, project.ID, string(encoded), write, now); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DramaStyleLockResult{}, errors.New("项目或主色卡在写入前被删除，没有锁定")
		}
		return DramaStyleLockResult{}, err
	}
	out := DramaStyleLockResult{StyleAnchor: in.Anchor, MasterPalette: palette, StyleLock: record, FontNote: fontNote}
	if _, err := s.bindProjectAssetImage(userID, project.ID, record.AssetID, BindProjectAssetImageRequest{ResourceID: resource.ID, ExpectedVersionID: versionID}, newID()); err != nil {
		out.ImageError = "风格已锁定，主色卡图没有绑到资产上：" + err.Error()
	}
	if asset, err := s.repo.AssetForUser(userID, record.AssetID); err == nil {
		if summary, err := s.projectAssetSummary(userID, project.ID, asset); err == nil {
			out.Asset = &summary
		}
	}
	return out, nil
}

// renderMasterPaletteResource 合成主色卡图并存成 userID 的图片资源；fontNote 是没有中文字体时的说明（只画了 HEX 和英文名）。
func (s *Service) renderMasterPaletteResource(userID string, title string, palette drama.MasterPalette) (*model.Resource, string, error) {
	png, err := drama.RenderMasterPalettePNG(title, palette)
	if err != nil {
		return nil, "", BadAuthRequest("主色卡图合成失败：" + err.Error())
	}
	_, problem := drama.MasterPaletteFontNote()
	resource, err := s.UploadResourceFile(userID, "master-palette.png", int64(len(png)), "image", drama.MasterPaletteImageWidth, drama.MasterPaletteImageHeight, 0, bytes.NewReader(png))
	if err != nil {
		return nil, "", err
	}
	return resource, problem, nil
}

// masterPaletteWrite 准备主色卡资产：项目已有主色卡时加一个版本并换成主版本（还没有图，随后绑合成图），没有时新建资产、版本 1 和项目关联。
func (s *Service) masterPaletteWrite(userID string, project *model.Project, in styleLockInput, palette drama.MasterPalette, now time.Time) (repository.DramaPaletteWrite, string, error) {
	definition := masterPaletteDefinition{Kind: "master", Title: strings.TrimSpace(in.Title), Palette: palette, StyleAnchor: in.Anchor, Source: in.Record.Source, LibraryID: in.Record.LibraryID}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return repository.DramaPaletteWrite{}, "", err
	}
	title := "主基调色卡"
	if definition.Title != "" {
		title += " · " + definition.Title
	}
	title = drama.JSTruncate(title, 200)
	meta := masterPaletteMeta{Kind: "master", Source: in.Record.Source, LibraryID: in.Record.LibraryID}
	versionID := newID()
	version := model.AssetVersion{ID: versionID, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: string(encoded), Note: "风格锁定", CreatedAt: now, UpdatedAt: now}
	current, err := s.projectMasterPaletteAsset(userID, project.ID)
	if err != nil {
		return repository.DramaPaletteWrite{}, "", err
	}
	if current != nil {
		versions, err := s.repo.AssetVersions(current.ID)
		if err != nil {
			return repository.DramaPaletteWrite{}, "", err
		}
		if len(versions) > 0 {
			version.Version = versions[0].Version + 1
		}
		version.AssetID = current.ID
		payload := assetPayload(current)
		payloadMetadata(payload)[masterPaletteKey] = meta
		payload["kind"], payload["status"], payload["primaryVersionId"], payload["title"] = "entity", model.AssetVersionStatusConfirmed, versionID, title
		payload["coverUrl"], payload["updatedAt"] = "", now.Format(time.RFC3339Nano)
		payload["data"] = map[string]any{"definition": definition}
		raw, err := json.Marshal(payload)
		if err != nil {
			return repository.DramaPaletteWrite{}, "", err
		}
		asset := *current
		asset.Kind, asset.Title, asset.Status, asset.PrimaryVersionID, asset.PayloadJSON, asset.UpdatedAt = "entity", title, model.AssetVersionStatusConfirmed, versionID, string(raw), now
		return repository.DramaPaletteWrite{Asset: &asset, Version: &version}, versionID, nil
	}
	folderID, err := s.resolveProjectAssetFolderID(project.ID, nil)
	if err != nil {
		return repository.DramaPaletteWrite{}, "", err
	}
	position, err := s.repo.NextProjectAssetPosition(project.ID, folderID)
	if err != nil {
		return repository.DramaPaletteWrite{}, "", err
	}
	assetID := newID()
	version.AssetID = assetID
	payload := map[string]any{
		"id": assetID, "kind": "entity", "category": model.AssetCategoryPalette, "status": model.AssetVersionStatusConfirmed,
		"primaryVersionId": versionID, "title": title, "coverUrl": "", "tags": []string{"色卡", "主基调色卡"},
		"data":      map[string]any{"definition": definition},
		"metadata":  map[string]any{masterPaletteKey: meta},
		"createdAt": now.Format(time.RFC3339Nano), "updatedAt": now.Format(time.RFC3339Nano),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return repository.DramaPaletteWrite{}, "", err
	}
	asset := model.Asset{ID: assetID, UserID: userID, Kind: "entity", Category: model.AssetCategoryPalette, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: versionID, Title: title, PayloadJSON: string(raw), CreatedAt: now, UpdatedAt: now}
	link := model.ProjectAssetLink{ID: newID(), ProjectID: project.ID, AssetID: assetID, FolderID: folderID, Position: position, CreatedAt: now}
	return repository.DramaPaletteWrite{Asset: &asset, Version: &version, Link: &link}, versionID, nil
}

// ProjectAssetMasterPaletteSummary 是主色卡资产在资产清单里的摘要（资产页主色卡卡片用）。
type ProjectAssetMasterPaletteSummary struct {
	Title       string              `json:"title"`
	Palette     drama.MasterPalette `json:"palette"`
	StyleAnchor *drama.StyleAnchor  `json:"styleAnchor,omitempty"`
	Source      string              `json:"source"`
	LibraryID   string              `json:"libraryId,omitempty"`
}

// masterPaletteSummary 是主色卡的摘要：设定取主版本。
func masterPaletteSummary(asset *model.Asset, versions []model.AssetVersion) *ProjectAssetMasterPaletteSummary {
	meta, _ := masterPaletteMetaOf(asset)
	out := &ProjectAssetMasterPaletteSummary{Source: meta.Source, LibraryID: meta.LibraryID, Palette: drama.MasterPalette{Groups: []drama.MasterPaletteGroup{}}}
	for _, v := range versions {
		if v.ID != asset.PrimaryVersionID {
			continue
		}
		var definition masterPaletteDefinition
		if json.Unmarshal([]byte(v.DefinitionJSON), &definition) == nil {
			out.Title, out.Palette, out.StyleAnchor = definition.Title, drama.NormalizeMasterPalette(definition.Palette), definition.StyleAnchor
		}
	}
	return out
}
