package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const sceneCheckReason = "scene_reference_review_required"

type SceneReferenceReview struct {
	RevisionID string             `json:"revisionId"`
	Snapshot   string             `json:"snapshot,omitempty"`
	Current    drama.SceneSpace   `json:"current"`
	Expected   drama.SceneSpace   `json:"expected"`
	Choices    []drama.SceneSpace `json:"choices,omitempty"`
	Issues     []string           `json:"issues"`
	Sections   map[string]string  `json:"sections,omitempty"`
	Shots      []string           `json:"shots,omitempty"`
	TextOnly   bool               `json:"textOnly"`
	Required   bool               `json:"required"`
}

type SceneReferenceUpdate struct {
	RevisionID    string            `json:"revisionId"`
	SceneID       string            `json:"sceneId"`
	VersionID     string            `json:"versionId"`
	ResourceID    string            `json:"resourceId"`
	TextOnly      bool              `json:"textOnly"`
	Sections      map[string]string `json:"sections,omitempty"`
	Shots         map[int]string    `json:"shots,omitempty"`
	Facts         *drama.SceneFacts `json:"facts,omitempty"`
	FactsRevision string            `json:"factsRevision,omitempty"`
}

func sceneCheckError(shotID string, issues []string) *AppError {
	e := NewAppError(http.StatusConflict, strings.Join(issues, "；"))
	e.Reason = sceneCheckReason
	e.Details = map[string]any{"shotId": shotID, "issues": issues}
	return e
}

type sceneVersionFacts struct {
	ResourceID string           `json:"resourceId"`
	Facts      drama.SceneFacts `json:"facts"`
}

func (s *Service) sceneSpaceForVersion(userID, projectID, versionID string) (drama.SceneSpace, error) {
	if versionID == "" {
		return drama.SceneSpace{}, nil
	}
	v, err := s.repo.ProjectReferenceVersion(userID, projectID, versionID)
	if err != nil {
		return drama.SceneSpace{}, err
	}
	asset, err := s.repo.AssetForUser(userID, v.AssetID)
	if err != nil {
		return drama.SceneSpace{}, err
	}
	if asset.Category != model.AssetCategoryEnvironment {
		return drama.SceneSpace{}, BadAuthRequest("请选择本项目的场景资产")
	}
	space := drama.SceneSpace{AssetID: asset.ID, VersionID: v.ID, Label: asset.Title}
	space.ResourceID, err = s.videoChainVersionResourceID(userID, projectID, v, "image")
	if err != nil {
		return space, err
	}
	var definition struct {
		Space sceneVersionFacts `json:"sceneImageFacts"`
	}
	if json.Unmarshal([]byte(v.DefinitionJSON), &definition) == nil && space.ResourceID != "" &&
		definition.Space.ResourceID == space.ResourceID {
		space.Facts = &definition.Space.Facts
	}
	space.FactsRevision = videoChainHash([]any{space.ResourceID, space.Facts})
	return space, nil
}

func (s *Service) sceneSpaces(userID string, project *model.Project, unit *model.ProjectUnit) ([]drama.SceneSpace, error) {
	scriptScenes := drama.SplitScenes(chapterPlainText(unit.SourceText), unit.Title)
	if len(scriptScenes) == 0 {
		return nil, nil
	}
	units, err := s.repo.ProjectUnitSummaries(project.ID)
	if err != nil {
		return nil, err
	}
	_, numbers, _ := sceneUnits(units)
	episode := numbers[unit.ID]
	assigned := projectSceneAssets(project.BibleJSON)
	assets, err := s.repo.ProjectAssets(userID, project.ID)
	if err != nil {
		return nil, err
	}
	versions := map[string]string{}
	for _, a := range assets {
		if a.Category == model.AssetCategoryEnvironment && a.Status != model.AssetVersionStatusArchived {
			versions[a.ID] = a.PrimaryVersionID
		}
	}
	cache := map[string]drama.SceneSpace{}
	var spaces []drama.SceneSpace
	for _, sc := range scriptScenes {
		space := drama.SceneSpace{SceneID: sc.ID, Heading: sc.Heading, Location: sc.Location, InOut: sc.InOut, Defined: len(sc.Sources) > 0 && sc.Location != ""}
		entry := assigned[projectSceneCode(sc.ID, episode)]
		version := ""
		if episode > 0 && entry.UnitID == unit.ID && len(entry.Environments) == 1 {
			version = versions[entry.Environments[0]]
		} else if space.Defined {
			// Name-only matches can suggest a review, but cannot bind by accident.
			space.Ambiguous = true
		}
		if version != "" {
			picked, found := cache[version]
			if !found {
				picked, err = s.sceneSpaceForVersion(userID, project.ID, version)
				if err != nil {
					return nil, err
				}
				cache[version] = picked
			}
			space.AssetID, space.VersionID, space.ResourceID, space.Label, space.Facts =
				picked.AssetID, picked.VersionID, picked.ResourceID, picked.Label, picked.Facts
		}
		spaces = append(spaces, space)
	}
	return spaces, nil
}

func sceneRefIndex(seg drama.Segment) int {
	return slices.IndexFunc(seg.Refs, func(r drama.Ref) bool { return r.Role == "location" })
}

func addSceneBinding(seg *drama.Segment, spaces []drama.SceneSpace, sceneKey string) {
	space, found := drama.MatchSceneSpace(spaces, sceneKey)
	if !found {
		if len(spaces) > 0 && (len(spaces) != 1 || spaces[0].Ambiguous || spaces[0].VersionID != "") {
			seg.SceneBinding = &drama.SceneBinding{Pending: true}
		}
		return
	}
	if space.VersionID == "" && !space.Ambiguous {
		return
	}
	seg.Scene = space.SceneID
	seg.SceneBinding = &drama.SceneBinding{SceneID: space.SceneID, Location: space.Location, InOut: space.InOut, VersionID: space.VersionID, ResourceID: space.ResourceID, Pending: space.Ambiguous}
	if sceneRefIndex(*seg) < 0 && space.ResourceID != "" && seg.Mode != drama.ModeLockFirstFrame {
		seg.Refs = append(seg.Refs, drama.Ref{File: space.VersionID, Role: "location", Note: space.Label})
		seg.Mode = drama.ModeLockIdentity
	}
}

func (s *Service) sceneReview(userID string, project *model.Project, shot *model.Shot, seg drama.Segment, spaces []drama.SceneSpace) (SceneReferenceReview, error) {
	view := SceneReferenceReview{RevisionID: shot.CurrentRevisionID, Issues: []string{}, Choices: spaces}
	binding := seg.SceneBinding
	key := seg.Scene
	if binding != nil && binding.SceneID != "" {
		key = binding.SceneID
	}
	view.Expected, _ = drama.MatchSceneSpace(spaces, key)
	view.Sections = drama.ScenePromptSections(seg.Verbatim)
	for _, sub := range seg.Shots {
		view.Shots = append(view.Shots, sub.Text)
	}
	location := sceneRefIndex(seg)
	if location >= 0 {
		var err error
		view.Current, err = s.sceneSpaceForVersion(userID, project.ID, seg.Refs[location].File)
		if err != nil {
			view.Issues = append(view.Issues, "当前场景参考图不可读或已失效")
		}
		for _, ref := range seg.Refs[location+1:] {
			if ref.Role == "location" {
				view.Issues = append(view.Issues, "本单元绑定了多个主场景，请确认本场唯一的空间")
				break
			}
		}
	}
	view.Required = binding != nil || view.Expected.VersionID != "" || slices.ContainsFunc(spaces, func(scene drama.SceneSpace) bool { return scene.Defined })
	if !view.Required {
		return view, nil
	}
	view.TextOnly = binding != nil && binding.TextOnly
	if view.Expected.SceneID == "" {
		view.Issues = append(view.Issues, "无法唯一匹配剧本场次，请在分镜核对中选定本场")
	}
	if binding != nil && binding.Pending {
		view.Issues = append(view.Issues, "场景绑定已变化，请对照新图同步本段场景描述")
	}
	if binding != nil && binding.Location != "" && (drama.AssetNameKey(binding.Location) != drama.AssetNameKey(view.Expected.Location) || binding.InOut != view.Expected.InOut) {
		view.Issues = append(view.Issues, "剧本中的场次地点或内外景已变化，请重新核对")
	}
	if view.TextOnly {
		if location >= 0 {
			view.Issues = append(view.Issues, "文字场景仍关联图片，请重新保存场景设定")
		}
		return view, nil
	}
	if view.Expected.Ambiguous && (binding == nil || !binding.Override) {
		view.Issues = append(view.Issues, "本场主场景待确认")
	}
	if location < 0 || view.Current.ResourceID == "" {
		view.Issues = append(view.Issues, "本场缺少可用的场景参考图")
	} else {
		_, readyErr := s.videoChainReadyResource(userID, view.Current.ResourceID, "image/")
		if readyErr != nil {
			view.Issues = append(view.Issues, "本场场景图不可读或已失效")
		}
		if view.Expected.AssetID != "" && view.Expected.AssetID != view.Current.AssetID && (binding == nil || !binding.Override) {
			view.Issues = append(view.Issues, fmt.Sprintf("本场「%s」应使用「%s」，当前引用为「%s」", view.Expected.Heading, view.Expected.Label, view.Current.Label))
		}
		if binding != nil && (binding.VersionID != view.Current.VersionID || binding.ResourceID != view.Current.ResourceID) {
			view.Issues = append(view.Issues, "当前绑定版本或实际图片已变化，请刷新并核对空间描述")
		}
		if view.Current.Facts != nil && view.Expected.InOut != "" && view.Expected.InOut != "内外" &&
			view.Current.Facts.InOut != "" && view.Expected.InOut != view.Current.Facts.InOut {
			view.Issues = append(view.Issues, "剧本场次和场景图核对的内外景不同")
		}
		view.Issues = append(view.Issues, drama.SceneConflicts(seg, view.Current)...)
	}
	return view, nil
}

func (s *Service) shotSceneReview(userID string, project *model.Project, shot *model.Shot, seg drama.Segment) (SceneReferenceReview, error) {
	var spaces []drama.SceneSpace
	if shot.UnitID != "" {
		unit, err := s.repo.ProjectUnit(project.ID, shot.UnitID)
		if err != nil {
			return SceneReferenceReview{}, err
		}
		spaces, err = s.sceneSpaces(userID, project, unit)
		if err != nil {
			return SceneReferenceReview{}, err
		}
	}
	return s.sceneReview(userID, project, shot, seg, spaces)
}

func (s *Service) ProjectSceneReferenceReview(userID, projectID, shotID string) (SceneReferenceReview, error) {
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return SceneReferenceReview{}, err
	}
	shot, err := s.repo.ShotForProject(projectID, shotID)
	if err != nil {
		return SceneReferenceReview{}, err
	}
	seg, _, _, err := s.segmentForShot(userID, projectID, shot, "", s.dramaAssetIndex(userID, projectID))
	if err != nil {
		return SceneReferenceReview{}, err
	}
	return s.shotSceneReview(userID, project, shot, seg)
}

func (s *Service) UpdateProjectSceneReference(userID, projectID, shotID string, req SceneReferenceUpdate) (SceneReferenceReview, error) {
	var result SceneReferenceReview
	err := s.repo.Transaction(func(tx *repository.Repository) error {
		var err error
		result, err = (&Service{repo: tx}).updateProjectSceneReference(userID, projectID, shotID, req)
		return err
	})
	return result, err
}

func (s *Service) updateProjectSceneReference(userID, projectID, shotID string, req SceneReferenceUpdate) (SceneReferenceReview, error) {
	fail := func(msg string) (SceneReferenceReview, error) { return SceneReferenceReview{}, BadAuthRequest(msg) }
	project, err := requireActiveProject(s.repo.LockActiveStoryboardProject(userID, projectID))
	if err != nil {
		return SceneReferenceReview{}, err
	}
	shot, err := s.repo.LockShotForProject(projectID, shotID)
	if err != nil {
		return SceneReferenceReview{}, err
	}
	if req.RevisionID == "" || req.RevisionID != shot.CurrentRevisionID {
		return SceneReferenceReview{}, sceneCheckError(shotID, []string{"本段已更新，请重新打开场景核对"})
	}
	unit, err := s.repo.ProjectUnit(projectID, shot.UnitID)
	if err != nil {
		return SceneReferenceReview{}, err
	}
	seg, oldRev, _, err := s.segmentForShot(userID, projectID, shot, unit.Title, s.dramaAssetIndex(userID, projectID))
	if err != nil {
		return SceneReferenceReview{}, err
	}
	if oldRev == nil {
		return fail("本段缺少可修改的分镜版本")
	}
	spaces, err := s.sceneSpaces(userID, project, unit)
	if err != nil {
		return SceneReferenceReview{}, err
	}
	space, found := drama.MatchSceneSpace(spaces, req.SceneID)
	if !found || req.SceneID != space.SceneID {
		return fail("请选择当前集中的剧本场次")
	}
	current, err := s.sceneSpaceForVersion(userID, projectID, req.VersionID)
	if err != nil {
		return SceneReferenceReview{}, err
	}
	if req.TextOnly {
		if req.VersionID != "" || req.ResourceID != "" || req.Facts != nil {
			return fail("文字场景不能带场景图")
		}
	} else {
		if current.ResourceID == "" || current.ResourceID != req.ResourceID {
			return SceneReferenceReview{}, sceneCheckError(shotID, []string{"场景图片已变化，请刷新后核对新图"})
		}
		if seg.Mode == drama.ModeLockFirstFrame {
			return fail("首尾帧模式不能追加普通场景参考图")
		}
	}
	if req.Facts != nil {
		if req.FactsRevision == "" || req.FactsRevision != current.FactsRevision {
			return SceneReferenceReview{}, sceneCheckError(shotID, []string{"同一张图的空间事实已更新，请刷新后核对"})
		}
		if err := validateSceneFacts(req.Facts); err != nil {
			return SceneReferenceReview{}, err
		}
		if err := s.saveSceneFacts(current, *req.Facts); err != nil {
			return SceneReferenceReview{}, segmentReferenceWriteError(err)
		}
	}
	seg.Verbatim, err = drama.RewriteScenePrompt(seg.Verbatim, req.Sections)
	if err != nil {
		return fail(err.Error())
	}
	for _, i := range slices.Sorted(maps.Keys(req.Shots)) {
		text := req.Shots[i]
		if i < 0 || i >= len(seg.Shots) || strings.TrimSpace(text) == "" || len([]rune(text)) > 2500 || strings.ContainsAny(text, "\r\n") {
			return fail("镜头画面请用一段不换行的文字")
		}
		text = strings.TrimSpace(text)
		if seg.Verbatim != "" {
			for j, other := range seg.Shots {
				if i != j && other.Text == seg.Shots[i].Text {
					return fail(fmt.Sprintf("第 %d 镜与其他镜头共用同一句导演描述，请在导演文档返修本单元", i+1))
				}
			}
		}
		prompt, synced := drama.ReplaceSceneShotText(seg.Verbatim, seg.Shots[i].Text, text)
		if !synced {
			return fail(fmt.Sprintf("第 %d 镜在导演稿中找不到可安全替换的同一句，请在导演文档返修本单元", i+1))
		}
		seg.Verbatim, seg.Shots[i].Text = prompt, text
	}
	setReviewedSceneReference(&seg, space, current, req.TextOnly)
	result, err := s.sceneReview(userID, project, shot, seg, spaces)
	if err != nil {
		return SceneReferenceReview{}, err
	}
	if len(result.Issues) > 0 {
		return SceneReferenceReview{}, sceneCheckError(shotID, result.Issues)
	}
	now := time.Now()
	newRev := *oldRev
	newRev.ID, newRev.CreatedAt, newRev.CreatedBy = newID(), now, userID
	raw, err := json.Marshal(seg)
	if err != nil {
		return SceneReferenceReview{}, err
	}
	newRev.SpecJSON = string(raw)
	plans, err := s.segmentReferencePlan(userID, projectID, shotID, oldRev.ID, &newRev, now)
	if err != nil {
		return SceneReferenceReview{}, err
	}
	shot.UpdatedAt = now
	if err := s.repo.SaveShotWithRevisionSelective(shot, &newRev, false, staleDeciderFor(&newRev), plans...); err != nil {
		return SceneReferenceReview{}, segmentReferenceWriteError(err)
	}
	result.RevisionID = newRev.ID
	return result, nil
}

func (s *Service) ProjectSceneReferenceCandidate(userID, projectID, shotID, versionID string) (drama.SceneSpace, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return drama.SceneSpace{}, err
	}
	if _, err := s.repo.ShotForProject(projectID, shotID); err != nil {
		return drama.SceneSpace{}, err
	}
	space, err := s.sceneSpaceForVersion(userID, projectID, versionID)
	if err != nil {
		return space, err
	}
	if space.ResourceID != "" {
		if _, err := s.videoChainReadyResource(userID, space.ResourceID, "image/"); err != nil {
			return drama.SceneSpace{}, err
		}
	}
	return space, nil
}

func setReviewedSceneReference(seg *drama.Segment, expected, current drama.SceneSpace, textOnly bool) {
	first := sceneRefIndex(*seg)
	for i := len(seg.Refs) - 1; i >= 0; i-- {
		ref := seg.Refs[i]
		if ref.Role != "location" {
			continue
		}
		if seg.ContinuityBridge != nil && (textOnly || ref.File != current.VersionID) {
			seg.ContinuityBridge.SharedRefs = slices.DeleteFunc(seg.ContinuityBridge.SharedRefs, func(file string) bool { return file == ref.File })
		}
		seg.Verbatim = drama.StripSceneDeclaration(seg.Verbatim, fmt.Sprintf("@图%d", i+1))
		if i == first && !textOnly {
			seg.Refs[i].File, seg.Refs[i].Note = current.VersionID, current.Label
			remapSegmentFiles(seg, map[string]string{ref.File: current.VersionID})
			continue
		}
		seg.Verbatim = remapRemovedReference(seg.Verbatim, seg.Refs, i)
		seg.Refs = slices.Delete(seg.Refs, i, i+1)
		for j := range seg.Shots {
			seg.Shots[j].Refs = slices.DeleteFunc(seg.Shots[j].Refs, func(file string) bool { return file == ref.File })
		}
	}
	if first < 0 && !textOnly {
		seg.Refs = append(seg.Refs, drama.Ref{File: current.VersionID, Role: "location", Note: current.Label})
	}
	if seg.Mode == drama.ModeTextOnly && len(seg.Refs) > 0 {
		seg.Mode = drama.ModeLockIdentity
	} else if seg.Mode == drama.ModeLockIdentity && len(seg.Refs) == 0 {
		seg.Mode = drama.ModeTextOnly
	}
	seg.Scene = expected.SceneID
	seg.SceneBinding = &drama.SceneBinding{SceneID: expected.SceneID, Location: expected.Location, InOut: expected.InOut, VersionID: current.VersionID, ResourceID: current.ResourceID, TextOnly: textOnly, Override: true}
}

func (s *Service) preserveRepairedScene(userID, projectID string, original drama.Segment, repaired *drama.Segment) error {
	if original.SceneBinding == nil {
		return nil
	}
	binding := *original.SceneBinding
	current, err := s.sceneSpaceForVersion(userID, projectID, binding.VersionID)
	if err != nil {
		return err
	}
	generated := sceneRefIndex(*repaired)
	if (generated >= 0 && repaired.Refs[generated].File != binding.VersionID) || current.ResourceID != binding.ResourceID ||
		!strings.EqualFold(repaired.Scene, original.Scene) {
		binding.Pending = true
	}
	if binding.TextOnly || binding.VersionID != "" {
		setReviewedSceneReference(repaired, drama.SceneSpace{SceneID: original.Scene, Location: binding.Location, InOut: binding.InOut}, current, binding.TextOnly)
	}
	repaired.Scene, repaired.SceneBinding = original.Scene, &binding
	return nil
}

func validateSceneFacts(f *drama.SceneFacts) error {
	if f.InOut != "" && !slices.Contains([]string{"内", "外", "内外"}, f.InOut) || len([]rune(f.Layout)) > 1500 {
		return BadAuthRequest("空间事实的内外景或布局格式无效")
	}
	for _, list := range [][]string{f.Anchors, f.Absent, f.Unknown} {
		if len(list) > 30 {
			return BadAuthRequest("每类空间锚点最多 30 项")
		}
		for _, item := range list {
			if strings.TrimSpace(item) != item || item == "" || len([]rune(item)) > 60 || strings.ContainsAny(item, "\r\n") {
				return BadAuthRequest("空间锚点请填写 1 至 60 字的单行描述")
			}
		}
	}
	for _, anchor := range f.Anchors {
		if slices.Contains(f.Absent, anchor) || slices.Contains(f.Unknown, anchor) {
			return BadAuthRequest("同一锚点不能同时确认存在和标记为缺失或待核对")
		}
	}
	return nil
}

func (s *Service) saveSceneFacts(current drama.SceneSpace, facts drama.SceneFacts) error {
	version, err := s.repo.AssetVersion(current.VersionID)
	if err != nil {
		return err
	}
	definition := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(firstNonEmpty(version.DefinitionJSON, "{}")), &definition); err != nil || definition == nil {
		return BadAuthRequest("场景资产设定不可读")
	}
	definition["sceneImageFacts"], _ = json.Marshal(sceneVersionFacts{ResourceID: current.ResourceID, Facts: facts})
	raw, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	return s.repo.UpdateSceneAssetDefinition(current.AssetID, current.VersionID, version.DefinitionJSON, string(raw), time.Now())
}

func (s *Service) sceneBindingCurrent(userID, projectID string, shot *model.Shot, revision *model.ShotRevision) (string, error) {
	if shot.CurrentRevisionID != revision.ID {
		return "", sceneCheckError(shot.ID, []string{"本段版本已变化，请刷新后核对"})
	}
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		return "", err
	}
	seg, err := s.resolveSceneSegment(userID, projectID, shot)
	if err != nil {
		return "", err
	}
	view, err := s.shotSceneReview(userID, project, shot, seg)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	if !view.Required {
		return "", nil
	}
	if len(view.Issues) != 0 {
		return "", sceneCheckError(shot.ID, view.Issues)
	}
	return sceneReferenceHash(view), nil
}

func sceneReferenceHash(view SceneReferenceReview) string {
	return videoChainHash([]any{view.RevisionID, view.Expected.SceneID, view.Current.VersionID, view.Current.ResourceID, view.Current.Facts})
}

func (s *Service) verifySceneSnapshot(userID, projectID, shotID, revisionID, snapshot string) error {
	shot, err := s.repo.ShotForProject(projectID, shotID)
	if err != nil {
		return err
	}
	if shot.CurrentRevisionID != revisionID {
		return sceneCheckError(shotID, []string{"分镜在提交前已变化，请重新核对和预览"})
	}
	revision, err := s.repo.ShotRevisionForShot(shotID, revisionID)
	if err != nil {
		return err
	}
	current, err := s.sceneBindingCurrent(userID, projectID, shot, revision)
	if err != nil {
		return err
	}
	if current != snapshot {
		return sceneCheckError(shotID, []string{"场景图或空间事实已变化，请重新核对和预览"})
	}
	return nil
}

func (s *Service) verifySceneChainSnapshots(userID, projectID string, state videoChainState) error {
	for _, step := range state.Steps {
		if step.Status == "succeeded" || step.SceneSignature == "" {
			continue
		}
		if err := s.verifySceneSnapshot(userID, projectID, step.ShotID, step.RevisionID, step.SceneSignature); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) enforceSceneReferenceSubmission(userID string, project *model.Project, shot *model.Shot, revision *model.ShotRevision, input, metadata, marker map[string]any, prompt string, confirmed *videoChainStepState) error {
	seg, err := s.resolveSceneSegment(userID, project.ID, shot)
	if err != nil {
		return err
	}
	view, err := s.shotSceneReview(userID, project, shot, seg)
	if err != nil || !view.Required {
		return err
	}
	if len(view.Issues) != 0 {
		return sceneCheckError(shot.ID, view.Issues)
	}
	if shot.CurrentRevisionID != revision.ID || stringValue(metadata["sceneReferenceSnapshot"]) != sceneReferenceHash(view) {
		return sceneCheckError(shot.ID, []string{"预览依据的场景已变化，请核对场景并重新预览"})
	}
	images, _ := input["referenceImages"].([]any)
	expectedPrompt := ""
	var expectedImages []any
	if confirmed != nil {
		if confirmed.SceneSignature != sceneReferenceHash(view) {
			return sceneCheckError(shot.ID, []string{"连续生成参数中的场景已变化，请重新预览"})
		}
		expectedPrompt = stringValue(confirmed.Input["prompt"])
		expectedImages, _ = confirmed.Input["referenceImages"].([]any)
		if frame := stringValue(marker["frameResourceId"]); frame != "" {
			expectedImages = append(slices.Clone(expectedImages), map[string]any{"storageKey": "resource:" + frame})
		}
	} else {
		config, _ := input["config"].(map[string]any)
		m := firstNonEmpty(stringValue(config["channelModelKey"]), stringValue(config["model"]))
		compiled, err := s.CompileProjectShot(userID, project.ID, shot.ID, CompileProjectShotRequest{ChannelModel: m, Model: stringValue(metadata["dramaModel"])})
		if err != nil {
			return err
		}
		if drama.HasError(compiled.Result.Issues) {
			return sceneCheckError(shot.ID, []string{"本段编译有待处理的问题，请重新核对和报价"})
		}
		expectedPrompt = compiled.Result.Prompt
		expectedImages, _, _, err = s.videoChainCompiledReferences(userID, project.ID, compiled.Result.Files)
		if err != nil {
			return err
		}
	}
	if prompt != expectedPrompt || !slices.Equal(sceneImageStorageKeys(images), sceneImageStorageKeys(expectedImages)) {
		return sceneCheckError(shot.ID, []string{"本次提交的正文或参考图顺序与预览不同，请重新编译"})
	}
	if confirmed == nil {
		// The provider must read the verified resources, even if an older
		// client supplied inlined bytes with the same storage key.
		input["referenceImages"] = expectedImages
	}
	return nil
}

func (s *Service) resolveSceneSegment(userID, projectID string, shot *model.Shot) (drama.Segment, error) {
	title := ""
	if shot.UnitID != "" {
		unit, err := s.repo.ProjectUnit(projectID, shot.UnitID)
		if err != nil {
			return drama.Segment{}, err
		}
		title = unit.Title
	}
	seg, _, _, err := s.segmentForShot(userID, projectID, shot, title, s.dramaAssetIndex(userID, projectID))
	return seg, err
}

func sceneImageStorageKeys(images []any) []string {
	keys := make([]string, 0, len(images))
	for _, item := range images {
		image, _ := item.(map[string]any)
		keys = append(keys, stringValue(image["storageKey"]))
	}
	return keys
}

func (s *Service) withSceneReference(userID string, project *model.Project, shot *model.Shot, m drama.Model, compiled DramaCompileResult) (DramaCompileResult, error) {
	view, err := s.shotSceneReview(userID, project, shot, compiled.Spec)
	if err != nil {
		return DramaCompileResult{}, err
	}
	if !view.Required {
		return compiled, nil
	}
	compiled.SceneReference = &view
	view.Snapshot = sceneReferenceHash(view)
	for _, issue := range view.Issues {
		compiled.Result.Issues = append(compiled.Result.Issues, drama.Issue{Level: "error", Message: issue})
	}
	if len(view.Issues) > 0 || view.TextOnly {
		return compiled, nil
	}
	for _, file := range compiled.Result.Files {
		if file.File != view.Current.VersionID || file.Role != "location" {
			continue
		}
		line := fmt.Sprintf("【本场空间】%s 为「%s」的场景参考。保持参考图中的建筑空间、固定出入口和陈设位置，机位、构图、光照和演员表演按本段执行。", file.Tag, view.Expected.Location)
		if facts := view.Current.Facts; facts != nil {
			if facts.Layout != "" {
				line += " 核对的布局：" + facts.Layout
			}
			if len(facts.Anchors) > 0 {
				line += "。空间锚点：" + strings.Join(facts.Anchors, "、") + "。"
			}
		}
		compiled.Result.Prompt = line + "\n\n" + compiled.Result.Prompt
		compiled.Result.Paste = line + "\n\n" + compiled.Result.Paste
		compiled.Result.Chars = len([]rune(compiled.Result.Prompt))
		limit := m.PromptMaxChars
		if m.PromptMaxCharsMeasured != nil {
			limit = m.PromptMaxCharsMeasured
		}
		if limit != nil && compiled.Result.Chars > *limit {
			compiled.Result.Issues = append(compiled.Result.Issues, drama.Issue{Level: "error", Message: "加入场景依据后提示词超过模型长度限制，请精简本段场景描述"})
		}
		break
	}
	return compiled, nil
}
