// 小节改戏类（M8 卡 E）：分镜制作里把一节改成武戏（或改回文戏）。这一节的剧本范围按新戏类重切（武戏可能切成 1～N 节，每节不超过
// 视频模型单段上限；改回文戏时这一串相连的武戏节一起回到文戏，按文戏 15 / 30 的口径重切），走生成分段同一套运行 / 任务 / SSE：
// 只重写这一场里切法变了的节（其余节的原样输出放进对话历史、镜头行照旧），收尾按整章检查、装段。装段保留没变的节的段（分镜图、
// 预演、视频都留着），被改的那一节原来的段换成同一段剧本范围里的新内容（加版本，产物过期），多出来的节新建段，不再对应任何节的
// 旧段删掉。改成的戏类按节存在 sections_json（GenreOverridesFrom），之后刷新、整章重跑、只重跑几节、重装都按它切。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// DramaSectionGenreRequest 是「改为武戏 / 改回文戏」：genre 是 wuxi 或 wenxi（文戏按项目视频模型单段上限选 15 / 30）；
// 其余字段与生成分段一样（文本模型、渠道段长规则），新任务照生成分段跑。
type DramaSectionGenreRequest struct {
	DramaStepRunRequest
	Genre string `json:"genre"`
}

// DramaSectionGenreResult 是建好的任务和这次改动：Sections 是按新戏类重切后要重写的节（新编号），Genre 是改成的戏类，
// From 是用户点的那一节和它原来的戏类。
type DramaSectionGenreResult struct {
	DramaStepRunResult
	Sections  []string `json:"sections"`
	Genre     string   `json:"genre"`
	From      string   `json:"from"`
	FromGenre string   `json:"fromGenre"`
}

// ChangeDramaSectionGenre 把本章的一节改成武戏或改回文戏，建一个只重写这一场里受影响小节的生成分段任务。
func (s *Service) ChangeDramaSectionGenre(userID string, projectID string, unitID string, sectionID string, req DramaSectionGenreRequest) (DramaSectionGenreResult, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return DramaSectionGenreResult{}, err
	}
	unit, err := s.repo.ProjectUnit(project.ID, strings.TrimSpace(unitID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DramaSectionGenreResult{}, BadAuthRequest("章节不存在或不属于该项目")
	}
	if err != nil {
		return DramaSectionGenreResult{}, err
	}
	if strings.TrimSpace(req.ResumeTaskID) != "" || len(req.OnlyScenes) > 0 || len(req.OnlySections) > 0 {
		return DramaSectionGenreResult{}, BadAuthRequest("改戏类自己算要重写的节，不用给 resumeTaskId / onlyScenes / onlySections")
	}
	record, err := s.repo.DramaShotlistForUnit(project.ID, unit.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DramaSectionGenreResult{}, BadAuthRequest("这一章还没有镜头表，先生成分段再改戏类")
	}
	if err != nil {
		return DramaSectionGenreResult{}, err
	}
	if strings.TrimSpace(record.SectionsJSON) == "" {
		return DramaSectionGenreResult{}, BadAuthRequest("这一章的镜头表不是按小节生成的，先整章重新生成分段再改戏类")
	}
	change, err := sectionGenreChangeFor(project, unit, record, req.Rule, sectionID, req.Genre)
	if err != nil {
		return DramaSectionGenreResult{}, err
	}
	run, err := s.runDramaStepFor(userID, project, unit.ID, drama.StepChapterShots, req.DramaStepRunRequest, &change.overrides, change.regenre)
	if err != nil {
		return DramaSectionGenreResult{}, err
	}
	return DramaSectionGenreResult{DramaStepRunResult: run, Sections: change.regenre.Sections, Genre: change.regenre.Genre, From: change.regenre.From, FromGenre: change.regenre.FromGenre}, nil
}

// sectionGenreChangeFor 是这一章按当前镜头表存下的戏类、项目默认戏类和段长规则算出的一次改戏类。
func sectionGenreChangeFor(project *model.Project, unit *model.ProjectUnit, record *model.DramaShotlist, rule *DramaShotlistRule, sectionID string, target string) (sectionGenreChange, error) {
	genre, m, err := projectShotGenre(project, rule)
	if err != nil {
		return sectionGenreChange{}, err
	}
	return planSectionGenreChange(chapterPlainText(unit.SourceText), unit.Title, genre, m.Duration.Max, genreOverridesOf(record, unit), strings.TrimSpace(sectionID), target)
}

// sectionGenreChange 是算好的一次改戏类：新的改戏类范围（整章）和这次改动。
type sectionGenreChange struct {
	overrides []drama.GenreOverride
	regenre   *dramaRegenre
}

// planSectionGenreChange 算一次改戏类（不读库）：在当前切法（默认戏类 + current 范围）里找到 sectionID，
//   - 改武戏：这一节的正文行范围加进改戏类范围；
//   - 改回文戏：这一场里与它相连的一串武戏节的范围一起去掉（这一串是一起切出来的，单拿一节回文戏会把剩下的切法打乱）；
//
// 再按新范围重切，这一场里对不上原来任何一节（场、正文行、台词、戏类）的新节就是要重写的节。
func planSectionGenreChange(text string, title string, genre drama.ShotGenre, modelMax int, current []drama.GenreOverride, sectionID string, target string) (sectionGenreChange, error) {
	before := drama.PlanSections(text, title, genre, modelMax, current)
	idx := slices.IndexFunc(before.Sections, func(s drama.Section) bool { return s.ID == sectionID })
	if idx < 0 {
		ids := make([]string, 0, len(before.Sections))
		for _, s := range before.Sections {
			ids = append(ids, s.ID)
		}
		return sectionGenreChange{}, BadAuthRequest(fmt.Sprintf("小节 %s 不在本章现在切出的小节里（%s）", drama.EchoSegmentID(sectionID), strings.Join(ids, "、")))
	}
	sec := before.Sections[idx]
	from, _ := drama.ShotGenreByID(sec.Genre)
	var next []drama.GenreOverride
	var to drama.ShotGenre
	switch strings.TrimSpace(target) {
	case drama.ShotGenreWuxi:
		if sec.Genre == drama.ShotGenreWuxi {
			return sectionGenreChange{}, BadAuthRequest(fmt.Sprintf("小节 %s 已经是武戏", sec.ID))
		}
		to, _ = drama.ShotGenreByID(drama.ShotGenreWuxi)
		next = append(slices.Clone(current), drama.GenreOverride{Scene: sec.Scene, Lines: sec.Lines, Genre: drama.ShotGenreWuxi})
	case "wenxi", drama.ShotGenreWenxi15, drama.ShotGenreWenxi30:
		if from.IsWenxi() {
			return sectionGenreChange{}, BadAuthRequest(fmt.Sprintf("小节 %s 已经是文戏", sec.ID))
		}
		to = genre
		a, b := idx, idx
		for a > 0 && before.Sections[a-1].Scene == sec.Scene && before.Sections[a-1].Genre == sec.Genre {
			a--
		}
		for b+1 < len(before.Sections) && before.Sections[b+1].Scene == sec.Scene && before.Sections[b+1].Genre == sec.Genre {
			b++
		}
		lo, hi := before.Sections[a].Lines[0], before.Sections[b].Lines[1]
		for _, o := range current {
			if o.Scene == sec.Scene && o.Lines[0] <= hi && o.Lines[1] >= lo {
				continue
			}
			next = append(next, o)
		}
	default:
		return sectionGenreChange{}, BadAuthRequest("genre 只能是 wuxi（改为武戏）或 wenxi（改回文戏）")
	}
	after := drama.PlanSections(text, title, genre, modelMax, next)
	same := func(n drama.Section) bool {
		return slices.ContainsFunc(before.Sections, func(o drama.Section) bool {
			return o.Scene == n.Scene && o.Lines == n.Lines && o.Genre == n.Genre && slices.Equal(o.LineIDs, n.LineIDs)
		})
	}
	var changed []string
	for _, n := range after.Sections {
		if n.Scene == sec.Scene && !same(n) {
			changed = append(changed, n.ID)
		}
	}
	if len(changed) == 0 {
		return sectionGenreChange{}, BadAuthRequest(fmt.Sprintf("小节 %s 改成%s后切法没有变化", sec.ID, to.Label))
	}
	if next == nil {
		next = []drama.GenreOverride{}
	}
	return sectionGenreChange{overrides: next, regenre: &dramaRegenre{Scene: sec.Scene, From: sec.ID, FromGenre: sec.Genre, Genre: to.ID, Sections: changed}}, nil
}

// ---------------------------------------------------------------- 保留装段

// dramaSegmentSection 是一个现有段对应的小节（段 spec.section；不是按小节装的段为空）。
func dramaSegmentSection(specJSON string) string {
	var seg struct {
		Section string `json:"section"`
	}
	_ = json.Unmarshal([]byte(specJSON), &seg)
	return strings.TrimSpace(seg.Section)
}

// replaceProjectUnitShotsKeeping 是只重写几节（「重新生成问题小节」、改戏类）后的装段：按小节把新段对到本章现有的段——
//   - 节没变（场、正文行、台词、戏类都一样）的段沿用原镜头：内容一样时只改位置，分镜图、预演、视频都不动；内容变了加版本，
//     依赖输入变了的产物过期；
//   - 被改的节原来的段换成同一场里正文范围有重叠的第一段新内容（加版本，产物过期）；
//   - 多出来的新建，不再对应任何节的旧段删掉（连同候选视频和证据）。
//
// previous 是写之前的镜头表各节，sections 是这次的各节。guard 为 true 时要删的旧段有产物就拒绝（见 guardSegmentDiscards），什么都不写。
func (s *Service) replaceProjectUnitShotsKeeping(userID string, projectID string, unitID string, inputs []ReplaceProjectUnitShotInput, segments []drama.Segment, previous []drama.SectionOutput, sections []drama.SectionOutput, shotlist *model.DramaShotlist, guard bool) error {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return err
	}
	if len(inputs) == 0 || len(inputs) > dramaChapterMaxShots {
		return BadAuthRequest(fmt.Sprintf("本章 %d 段，章节分镜数量必须在 1 到 %d 之间", len(inputs), dramaChapterMaxShots))
	}
	all, err := s.repo.ProjectShots(projectID)
	if err != nil {
		return err
	}
	type oldShot struct {
		shot     model.Shot
		revision *model.ShotRevision
		section  drama.SectionOutput
		has      bool
	}
	prevByID := map[string]drama.SectionOutput{}
	for _, o := range previous {
		prevByID[o.ID] = o
	}
	var olds []oldShot
	for _, sh := range all {
		if sh.UnitID != unitID {
			continue
		}
		o := oldShot{shot: sh}
		if rev, err := s.repo.ShotRevisionForShot(sh.ID, sh.CurrentRevisionID); err == nil {
			o.revision = rev
			o.section, o.has = prevByID[dramaSegmentSection(rev.SpecJSON)]
		}
		olds = append(olds, o)
	}
	newByID := map[string]drama.SectionOutput{}
	for _, o := range sections {
		newByID[o.ID] = o
	}
	sameSection := func(a, b drama.SectionOutput) bool {
		return a.Scene == b.Scene && a.Lines == b.Lines && a.Genre == b.Genre && slices.Equal(a.LineIDs, b.LineIDs)
	}
	used := make([]bool, len(olds))
	pick := make([]int, len(segments))
	for i := range pick {
		pick[i] = -1
	}
	for i, seg := range segments {
		n, ok := newByID[seg.Section]
		if !ok {
			continue
		}
		for j, o := range olds {
			if !used[j] && o.has && sameSection(o.section, n) {
				used[j], pick[i] = true, j
				break
			}
		}
	}
	for i, seg := range segments {
		n, ok := newByID[seg.Section]
		if pick[i] >= 0 || !ok {
			continue
		}
		for j, o := range olds {
			if !used[j] && o.has && o.section.Scene == n.Scene && o.section.Lines[0] <= n.Lines[1] && o.section.Lines[1] >= n.Lines[0] {
				used[j], pick[i] = true, j
				break
			}
		}
	}
	now := time.Now()
	writes := make([]repository.UnitShotWrite, 0, len(inputs))
	for i, input := range inputs {
		title := strings.TrimSpace(input.Title)
		description := strings.TrimSpace(input.Description)
		if title == "" || description == "" || input.DurationMs < 0 {
			return BadAuthRequest("分镜标题、描述或时长无效")
		}
		shotID := newID()
		var old *oldShot
		if pick[i] >= 0 {
			old = &olds[pick[i]]
			shotID = old.shot.ID
		}
		revision, err := newShotRevision(userID, shotID, input.Revision, description, input.DurationMs, now)
		if err != nil {
			return err
		}
		if err := ensureSegmentWithinModel(project, title, revision.DurationMs); err != nil {
			return err
		}
		references, err := s.unitShotReferences(projectID, shotID, input, now)
		if err != nil {
			return err
		}
		shot := model.Shot{ID: shotID, ProjectID: projectID, UnitID: unitID, CurrentRevisionID: revision.ID, Title: title, Description: revision.PlotDescription, Position: i, DurationMs: revision.DurationMs, Status: "draft", CreatedAt: now, UpdatedAt: now}
		w := repository.UnitShotWrite{Shot: shot, Revision: &revision, References: references}
		if old != nil {
			w.Existing = true
			current, err := s.repo.ShotAssetReferencesForShot(old.shot.ID)
			if err != nil {
				return err
			}
			if old.revision != nil && sameShotRevision(old.revision, &revision) && sameShotReferences(current, references) {
				// 内容和引用都没变：只改位置和标题，产物原样留着。
				w.Revision, w.References = nil, nil
			} else {
				w.Decide = staleDeciderFor(&revision)
			}
		}
		writes = append(writes, w)
	}
	deletes := []string{}
	for j, o := range olds {
		if !used[j] {
			deletes = append(deletes, o.shot.ID)
		}
	}
	if guard {
		if err := s.guardSegmentDiscards(projectID, unitID, deletes); err != nil {
			return err
		}
	}
	if shotlist != nil {
		shotlist.ProjectID, shotlist.UnitID, shotlist.CreatedBy = projectID, unitID, userID
		if shotlist.ID == "" {
			shotlist.ID = newID()
		}
		shotlist.CreatedAt, shotlist.UpdatedAt = now, now
	}
	if err := s.repo.ReplaceProjectUnitShotsKeeping(projectID, unitID, writes, deletes, shotlist, now); err != nil {
		if errors.Is(err, repository.ErrProjectUnitShotsChanged) {
			return BadAuthRequest("本章分镜已发生变化，请刷新后重新确认")
		}
		return err
	}
	return nil
}

// unitShotReferences 是一段的资产引用（参考 6 个以内 + 本场色卡 role=style），与整章替换同一套校验。
func (s *Service) unitShotReferences(projectID string, shotID string, input ReplaceProjectUnitShotInput, now time.Time) ([]model.ShotAssetReference, error) {
	var references []model.ShotAssetReference
	seen := map[string]bool{}
	for _, raw := range input.AssetVersionIDs {
		versionID := strings.TrimSpace(raw)
		if versionID == "" || seen[versionID] {
			continue
		}
		if len(seen) >= 6 {
			return nil, BadAuthRequest("单个分镜最多引用 6 个资产版本")
		}
		if _, err := s.repo.AssetVersionForProject(projectID, versionID); err != nil {
			return nil, err
		}
		seen[versionID] = true
		references = append(references, model.ShotAssetReference{ID: newID(), ShotID: shotID, AssetVersionID: versionID, Role: "reference", Status: "linked", CreatedAt: now})
	}
	styles := map[string]bool{}
	for _, raw := range input.StyleVersionIDs {
		versionID := strings.TrimSpace(raw)
		if versionID == "" || styles[versionID] {
			continue
		}
		if _, err := s.repo.AssetVersionForProject(projectID, versionID); err != nil {
			return nil, err
		}
		styles[versionID] = true
		references = append(references, model.ShotAssetReference{ID: newID(), ShotID: shotID, AssetVersionID: versionID, Role: shotAssetRoleStyle, Status: "linked", CreatedAt: now})
	}
	return references, nil
}

// sameShotRevision 是两个版本的段内容一样（spec、画面、台词、提示词、时长）。
func sameShotRevision(a, b *model.ShotRevision) bool {
	return a.SpecJSON == b.SpecJSON && a.PlotDescription == b.PlotDescription && a.Dialogue == b.Dialogue && a.DurationMs == b.DurationMs &&
		a.VideoPrompt == b.VideoPrompt && a.ImagePrompt == b.ImagePrompt && a.NegativePrompt == b.NegativePrompt
}

func sameShotReferences(a, b []model.ShotAssetReference) bool {
	x, y := referenceIDs(a), referenceIDs(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
