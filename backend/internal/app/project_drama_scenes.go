// 场次表（M8 卡 C）：按剧集（没有剧集时按原文章节）正文实时切场（drama.SplitScenes，连续同地点并成一场），不建表——
// 场编号只由正文和集序决定，正文不改编号就不变；规则改变前写下的色彩脚本、色卡、镜头表保持原编号，
// 编号错位时场次表带上提醒，RenumberDramaScenes 可以一次性把色彩脚本和色卡按新场次重排（不自动跑）。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
)

// DramaScenesView 是一个单元（剧集或原文章节）的场次表。
type DramaScenesView struct {
	UnitID string `json:"unitId"`
	Title  string `json:"title"`
	// Episode 是导演风格的集号（有剧集按剧集序号，没有剧集按原文章节序号）；0 是这个单元不在项目场次表里（已有剧集时的原文章节）。
	Episode      int              `json:"episode"`
	FromChapters bool             `json:"fromChapters"`
	Rule         int              `json:"rule"`
	Scenes       []DramaSceneView `json:"scenes"`
	// Genre 是生成分段按哪个戏类切小节（按项目视频模型单段上限选：文戏 15 / 文戏 30）；各场的 sections 按它切。
	Genre *DramaStepGenre `json:"genre,omitempty"`
	// Notice 是切场规则改变后，这个单元已有的数据还是旧场次编号、编号错位了；没有错位时为空。
	Notice *DramaSceneRuleNotice `json:"notice,omitempty"`
	// PaletteReady 是这个单元已有场次色卡（至少一场有能用的 kind=scene 色卡），可以提取资产（M8 卡 D）。
	PaletteReady bool `json:"paletteReady"`
}

// DramaSceneView 是场次表里的一场。
type DramaSceneView struct {
	// ID 是场编号：E 后是集号（导演风格、色卡用这套）；Episode 为 0 时同 ShotScene。
	ID string `json:"id"`
	// ShotScene 是单元内场编号 E01S02（镜头 spec.scene、台词 id 用这套）。
	ShotScene string   `json:"shotScene"`
	No        int      `json:"no"`
	Heading   string   `json:"heading"`
	InOut     string   `json:"inOut,omitempty"`
	Location  string   `json:"location,omitempty"`
	Times     []string `json:"times"`
	// SourceHeadings 是并成这一场的正文场次头（行号是正文第几个非空行）；未命名场、整章一场时为空。
	SourceHeadings []drama.SceneHeading `json:"sourceHeadings"`
	// Lines 是这一场在正文里的行范围 [首行, 末行]（只数非空行，从 1 数，含场次头）；LineCount 是行数。
	Lines     [2]int `json:"lines"`
	LineCount int    `json:"lineCount"`
	// LegacyNos 是这一场在规则改变前（一个场次头一场）的场序。
	LegacyNos []int `json:"legacyNos"`
	// Sections 是这一场按戏类切出的小节（一节 = 一次生成 = 一段视频），与生成分段的轮次同一套（请求带上渠道段长 rule 时按同一口径选戏类、切节）。
	Sections []DramaSectionView `json:"sections"`
	// PaletteAssetID 是这一场能用的场次色卡；Environments 是提取资产时回填绑到这一场的场景资产（bible_json.scene_assets）。
	PaletteAssetID string               `json:"paletteAssetId,omitempty"`
	Environments   []DramaSceneAssetRef `json:"environments"`
}

// DramaSceneAssetRef 是绑到一场的资产。
type DramaSceneAssetRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// DramaSectionView 是场次表里的一个小节。
type DramaSectionView struct {
	// ID 是小节编号（单元内场编号加 -P 序号：E01S02-P1），镜头 spec.section、段 spec.section 用这套。
	ID    string `json:"id"`
	No    int    `json:"no"`
	Genre string `json:"genre"`
	// Lines 是覆盖的正文行范围（与场次的 lines 同一套）；LineIDs 是这一节的台词 id；Est 是估时秒数。
	Lines    [2]int   `json:"lines"`
	LineIDs  []string `json:"lineIds"`
	Speakers []string `json:"speakers"`
	Est      float64  `json:"est"`
	// Warnings 是切节没能满足戏类口径的地方（一句台词就超上限、接话拆不开导致台词超额）。
	Warnings []string `json:"warnings,omitempty"`
}

// DramaSceneRuleNotice 是切场规则改变后编号错位的已有数据。
type DramaSceneRuleNotice struct {
	// LegacyScenes 是按旧规则（一个场次头一场）这个单元的场数。
	LegacyScenes int `json:"legacyScenes"`
	// ColorScript、Palettes 是旧规则写的、编号已经错位的色彩脚本条目和色卡（场编号）；Shotlist 是镜头表还是旧规则切的场。
	ColorScript []string `json:"colorScript"`
	Palettes    []string `json:"palettes"`
	Shotlist    bool     `json:"shotlist"`
	Message     string   `json:"message"`
}

// sceneUnits 是项目的单元（按位置排好）和导演风格的集号表。
func sceneUnits(units []model.ProjectUnit) ([]model.ProjectUnit, map[string]int, bool) {
	sorted := slices.Clone(units)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Position < sorted[j].Position })
	_, numbers, fromChapters, err := styleBibleUnits(sorted, nil)
	if err != nil {
		return sorted, map[string]int{}, false
	}
	return sorted, numbers, fromChapters
}

func dramaScenesView(unit model.ProjectUnit, episode int, fromChapters bool, scenes []drama.Scene, sections map[string][]drama.Section) DramaScenesView {
	view := DramaScenesView{UnitID: unit.ID, Title: unit.Title, Episode: episode, FromChapters: fromChapters, Rule: drama.SceneRuleMerged, Scenes: make([]DramaSceneView, 0, len(scenes))}
	for _, sc := range scenes {
		id := sc.ID
		if episode > 0 {
			id = drama.SceneCode(episode, sc.No)
		}
		sources := sc.Sources
		if sources == nil {
			sources = []drama.SceneHeading{}
		}
		times := sc.Times
		if times == nil {
			times = []string{}
		}
		parts := make([]DramaSectionView, 0, len(sections[sc.ID]))
		for _, sec := range sections[sc.ID] {
			parts = append(parts, DramaSectionView{ID: sec.ID, No: sec.No, Genre: sec.Genre, Lines: sec.Lines, LineIDs: sec.LineIDs, Speakers: sec.Speakers, Est: sec.Est, Warnings: sec.Warnings})
		}
		view.Scenes = append(view.Scenes, DramaSceneView{
			ID: id, ShotScene: sc.ID, No: sc.No, Heading: sc.Heading, InOut: sc.InOut, Location: sc.Location, Times: times,
			SourceHeadings: sources, Lines: sc.Lines, LineCount: sc.Lines[1] - sc.Lines[0] + 1, LegacyNos: sc.LegacyNos, Sections: parts, Environments: []DramaSceneAssetRef{},
		})
	}
	return view
}

// sceneRuleNotice 是一个单元编号错位的已有数据：只有按新规则切出的场和旧规则不一样（有并起来的场）时才看。
// script 是项目的色彩脚本，palettes 是项目的色卡（按场编号），shotlist 是这个单元存下的镜头表（没有为 nil）。
func sceneRuleNotice(unit model.ProjectUnit, episode int, scenes []drama.Scene, table map[string]styleSceneSlot, script []drama.ColorScriptEntry, palettes map[string]model.Asset, shotlist *model.DramaShotlist) *DramaSceneRuleNotice {
	if !drama.ScenesRenumbered(scenes) {
		return nil
	}
	notice := &DramaSceneRuleNotice{ColorScript: []string{}, Palettes: []string{}}
	for _, sc := range scenes {
		notice.LegacyScenes += len(sc.LegacyNos)
	}
	if episode > 0 {
		for _, e := range script {
			if e.Episode == episode && e.SceneRule == 0 && (e.UnitID == "" || e.UnitID == unit.ID) && !colorScriptCurrent(e, table) {
				notice.ColorScript = append(notice.ColorScript, e.Code())
			}
		}
		for _, code := range slices.Sorted(maps.Keys(palettes)) {
			a := palettes[code]
			meta := paletteMetaOf(&a)
			if meta.Episode != episode || meta.SceneRule != 0 || meta.Stale {
				continue
			}
			if slot, ok := table[code]; !ok || slot.LegacyShifted {
				notice.Palettes = append(notice.Palettes, code)
			}
		}
	}
	if shotlist != nil && strings.TrimSpace(shotlist.ScriptJSON) != "" {
		var stored drama.Script
		if json.Unmarshal([]byte(shotlist.ScriptJSON), &stored) == nil && stored.SceneRule == 0 && len(stored.Scenes) != len(scenes) {
			notice.Shotlist = true
		}
	}
	var stale []string
	if len(notice.ColorScript) > 0 {
		stale = append(stale, fmt.Sprintf("色彩脚本 %d 场", len(notice.ColorScript)))
	}
	if len(notice.Palettes) > 0 {
		stale = append(stale, fmt.Sprintf("色卡 %d 张", len(notice.Palettes)))
	}
	if notice.Shotlist {
		stale = append(stale, "镜头表")
	}
	if len(stale) == 0 {
		return nil
	}
	// 色彩脚本和色卡都由场次色卡写：引导到章节页「生成场次色卡」重新生成。
	var redo []string
	if notice.Shotlist {
		redo = append(redo, "镜头表请重新生成分段")
	}
	if len(notice.ColorScript) > 0 || len(notice.Palettes) > 0 {
		redo = append(redo, "色彩脚本与色卡请到章节页点「生成场次色卡」重新生成")
	}
	notice.Message = fmt.Sprintf("场次规则已更新：同一集里连续同地点、内外相同的场次头并成一场，这一集从 %d 场变成 %d 场。已有的%s还是旧场次编号，并场之后的编号对不上：%s。",
		notice.LegacyScenes, len(scenes), strings.Join(stale, "、"), strings.Join(redo, "；"))
	return notice
}

// DramaScenes 读一个单元的场次表（按正文实时切）。rule 是视频渠道段长（与生成分段 run 请求的 rule 同口径），
// 每场的小节按它选的戏类切，场次条的节数才与实际运行一致。
func (s *Service) DramaScenes(userID string, projectID string, unitID string, rule *DramaShotlistRule) (*DramaScenesView, error) {
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("项目不存在")
		}
		return nil, err
	}
	unitID = strings.TrimSpace(unitID)
	units, err := s.repo.ProjectUnits(projectID)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(units, func(u model.ProjectUnit) bool { return u.ID == unitID })
	if i < 0 {
		return nil, NotFound("章节或剧集不存在")
	}
	views, err := s.dramaScenesViews(userID, project, units, []model.ProjectUnit{units[i]}, rule)
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

// ProjectDramaScenes 读项目场次表里每个单元（全部剧集，没有剧集时全部原文章节）的场次表；rule 同 DramaScenes。
func (s *Service) ProjectDramaScenes(userID string, projectID string, rule *DramaShotlistRule) ([]DramaScenesView, error) {
	project, err := s.repo.ProjectForUser(userID, projectID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("项目不存在")
		}
		return nil, err
	}
	units, err := s.repo.ProjectUnits(projectID)
	if err != nil {
		return nil, err
	}
	sorted, numbers, _ := sceneUnits(units)
	var picked []model.ProjectUnit
	for _, u := range sorted {
		if numbers[u.ID] > 0 {
			picked = append(picked, u)
		}
	}
	return s.dramaScenesViews(userID, project, units, picked, rule)
}

func (s *Service) dramaScenesViews(userID string, project *model.Project, units []model.ProjectUnit, picked []model.ProjectUnit, rule *DramaShotlistRule) ([]DramaScenesView, error) {
	_, numbers, fromChapters := sceneUnits(units)
	table := styleSceneTable(units)
	_, script := projectStyleBible(project.BibleJSON)
	palettes, err := s.projectPaletteAssets(userID, project.ID)
	if err != nil {
		return nil, err
	}
	assets, err := s.repo.ProjectAssets(userID, project.ID)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	for _, a := range assets {
		if a.Status != model.AssetVersionStatusArchived {
			titles[a.ID] = a.Title
		}
	}
	bound := projectSceneAssets(project.BibleJSON)
	genre, m, err := projectShotGenre(project, rule)
	if err != nil {
		return nil, err
	}
	_, hi := genre.SectionLimits(m.Duration.Max)
	genreView := &DramaStepGenre{ID: genre.ID, Label: genre.Label, MaxSeconds: hi, Model: m.Label}
	views := make([]DramaScenesView, 0, len(picked))
	for _, u := range picked {
		text := chapterPlainText(u.SourceText)
		scenes := drama.SplitScenes(text, u.Title)
		view := dramaScenesView(u, numbers[u.ID], fromChapters, scenes, drama.PlanSections(text, u.Title, genre, m.Duration.Max).ScenesOf())
		view.Genre = genreView
		ready := unitSceneColorScript(project.BibleJSON, units, u.ID)
		for i := range view.Scenes {
			sc := &view.Scenes[i]
			if e, ok := ready[sc.ShotScene]; ok && numbers[u.ID] > 0 {
				if p, ok := palettes[e.Code()]; ok && scenePaletteUsable(&p) {
					sc.PaletteAssetID = p.ID
					view.PaletteReady = true
				}
			}
			if entry, ok := bound[sc.ID]; ok && (entry.UnitID == "" || entry.UnitID == u.ID) {
				for _, id := range entry.Environments {
					if title := titles[id]; title != "" {
						sc.Environments = append(sc.Environments, DramaSceneAssetRef{ID: id, Title: title})
					}
				}
			}
		}
		if drama.ScenesRenumbered(scenes) {
			shotlist, err := s.repo.DramaShotlistForUnit(project.ID, u.ID)
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			view.Notice = sceneRuleNotice(u, numbers[u.ID], scenes, table, script, palettes, shotlist)
		}
		views = append(views, view)
	}
	return views, nil
}

// ---------------------------------------------------------------- 按新场次重排

// DramaSceneRenumberRequest 是按新场次重排的请求：Apply 为 false 时只预览，不写库。
type DramaSceneRenumberRequest struct {
	Apply bool `json:"apply"`
}

// DramaSceneRenumberResult 是重排结果：Moves 是每个旧规则色彩脚本条目的去向；Palettes 是色卡的改动。
type DramaSceneRenumberResult struct {
	Applied  bool                   `json:"applied"`
	Moves    []drama.SceneMove      `json:"moves"`
	Palettes []DramaPaletteRenumber `json:"palettes"`
}

// DramaPaletteRenumber 是一张旧规则色卡的改动：挪了编号（To）、并进了别的场（Retired，标成场次已不存在），或原样保留（Note）。
type DramaPaletteRenumber struct {
	AssetID string `json:"assetId"`
	From    string `json:"from"`
	To      string `json:"to,omitempty"`
	Retired bool   `json:"retired,omitempty"`
	Note    string `json:"note,omitempty"`
}

// RenumberDramaScenes 一次性把规则改变前写的色彩脚本和色卡按新场次重排（drama.RenumberColorScript：按旧场序找到现在的场、
// 地点对得上才换编号；并进已有色彩脚本的场去掉、它的色卡标成场次已不存在并记 note）。色卡只改 metadata 和标题，不加版本、
// 不动图和已绑的镜头；镜头表不动（重新生成分段才换成新编号）。不自动跑，Apply 为 false 时只返回预览。
func (s *Service) RenumberDramaScenes(userID string, projectID string, req DramaSceneRenumberRequest) (DramaSceneRenumberResult, error) {
	result := DramaSceneRenumberResult{Moves: []drama.SceneMove{}, Palettes: []DramaPaletteRenumber{}}
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, NotFound("项目不存在")
		}
		return result, err
	}
	units, err := s.repo.ProjectUnits(projectID)
	if err != nil {
		return result, err
	}
	sorted, numbers, _ := sceneUnits(units)
	episodes := map[int]drama.SceneRenumberEpisode{}
	for _, u := range sorted {
		if no := numbers[u.ID]; no > 0 {
			episodes[no] = drama.SceneRenumberEpisode{UnitID: u.ID, Scenes: drama.SplitScenes(chapterPlainText(u.SourceText), u.Title)}
		}
	}
	_, script := projectStyleBible(project.BibleJSON)
	renumbered, moves := drama.RenumberColorScript(script, episodes)
	if moves != nil {
		result.Moves = moves
	}
	paletteWrites, paletteChanges, err := s.renumberPaletteWrites(userID, project, renumbered, moves, episodes)
	if err != nil {
		return result, err
	}
	result.Palettes = paletteChanges
	if !req.Apply || (len(moves) == 0 && len(paletteWrites) == 0) {
		return result, nil
	}
	doc := map[string]any{}
	if raw := strings.TrimSpace(project.BibleJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return result, fmt.Errorf("项目的连戏表不是合法 JSON，没有重排：%w", err)
		}
	}
	doc["color_script"] = renumbered
	encoded, err := json.Marshal(doc)
	if err != nil {
		return result, err
	}
	if err := s.repo.ApplyDramaScenePalettes(userID, project.ID, string(encoded), paletteWrites, time.Now()); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, NotFound("项目或色卡在写入前被删除，没有重排")
		}
		return result, err
	}
	result.Applied = true
	return result, nil
}

// renumberPaletteWrites 是旧规则色卡的改动：色彩脚本条目挪了编号的，色卡跟着改 metadata（场编号、规则标记、原编号）和标题；
// 条目被去掉的（并进已有的场），色卡标成场次已不存在、记 note；条目原样保留的色卡不动。
func (s *Service) renumberPaletteWrites(userID string, project *model.Project, renumbered []drama.ColorScriptEntry, moves []drama.SceneMove, episodes map[int]drama.SceneRenumberEpisode) ([]repository.DramaPaletteWrite, []DramaPaletteRenumber, error) {
	assets, err := s.repo.ProjectAssets(userID, project.ID)
	if err != nil {
		return nil, nil, err
	}
	byFrom := map[string]drama.SceneMove{}
	for _, m := range moves {
		byFrom[m.From] = m
	}
	entries := map[string]drama.ColorScriptEntry{}
	for _, e := range renumbered {
		entries[e.Code()] = e
	}
	now := time.Now()
	var writes []repository.DramaPaletteWrite
	changes := []DramaPaletteRenumber{}
	for _, a := range assets {
		meta := paletteMetaOf(&a)
		if meta.Code == "" || meta.SceneRule != 0 || meta.Stale {
			continue
		}
		m, ok := byFrom[meta.Code]
		if !ok || (!m.Moved && !m.Dropped) {
			continue
		}
		change := DramaPaletteRenumber{AssetID: a.ID, From: meta.Code}
		asset := a
		meta.RenumberedFrom = meta.Code
		switch {
		case m.Dropped:
			change.Retired, change.Note = true, "场次规则更新："+m.Note+"，这张色卡不再用"
			meta.Stale, meta.StaleAt, meta.Note = true, now.Format(time.RFC3339Nano), change.Note
		default:
			e, ok := entries[m.To]
			if !ok {
				continue
			}
			change.To = m.To
			meta.Code, meta.Episode, meta.SceneNo, meta.SceneRule = e.Code(), e.Episode, e.SceneNo, drama.SceneRuleMerged
			if m.To != m.From {
				meta.Note = fmt.Sprintf("场次规则更新：由 %s 改成 %s（%s）", m.From, m.To, episodeSceneHeading(episodes, e))
				asset.Title = drama.PaletteTitle(e)
			}
		}
		payload := assetPayload(&a)
		metadata := payloadMetadata(payload)
		metadata["palette"] = meta
		payload["title"], payload["updatedAt"] = asset.Title, now.Format(time.RFC3339Nano)
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, err
		}
		asset.PayloadJSON, asset.UpdatedAt = string(raw), now
		writes = append(writes, repository.DramaPaletteWrite{Asset: &asset})
		changes = append(changes, change)
	}
	return writes, changes, nil
}

func episodeSceneHeading(episodes map[int]drama.SceneRenumberEpisode, e drama.ColorScriptEntry) string {
	if ep, ok := episodes[e.Episode]; ok && e.SceneNo >= 1 && e.SceneNo <= len(ep.Scenes) {
		return ep.Scenes[e.SceneNo-1].Heading
	}
	return e.Scene
}

// legacyShiftedColorScript 是规则改变前写的、编号已经错位的色彩脚本场数：所在集按新规则有并起来的场，且这一场对不上当前场次表。
// 总览「色卡齐了」的说明里提示场次规则已更新。
func legacyShiftedColorScript(bibleJSON string, units []model.ProjectUnit) int {
	_, script := projectStyleBible(bibleJSON)
	if !slices.ContainsFunc(script, func(e drama.ColorScriptEntry) bool { return e.SceneRule == 0 }) {
		return 0
	}
	sorted, numbers, _ := sceneUnits(units)
	renumbered := map[int]bool{}
	for _, u := range sorted {
		if no := numbers[u.ID]; no > 0 && drama.ScenesRenumbered(drama.SplitScenes(chapterPlainText(u.SourceText), u.Title)) {
			renumbered[no] = true
		}
	}
	table := styleSceneTable(units)
	n := 0
	for _, e := range script {
		if e.SceneRule == 0 && renumbered[e.Episode] && !colorScriptCurrent(e, table) {
			n++
		}
	}
	return n
}

// legacyShiftedKept 是这次不重写的集里、切场规则改变前写的、编号已经错位的色彩脚本条目（旧场在现在的场次表里还在，只是并进了
// 前面的场）：重跑别的集时原样留着、它们的色卡也不标成场次已不存在，等重跑那一集或按新场次重排；不当承接用（不进提示词和检查）。
func legacyShiftedKept(script []drama.ColorScriptEntry, runEpisodes map[int]bool, units []model.ProjectUnit, table map[string]styleSceneSlot) []drama.ColorScriptEntry {
	sorted, numbers, _ := sceneUnits(units)
	scenesOf := map[int][]drama.Scene{}
	unitOf := map[int]string{}
	for _, u := range sorted {
		if no := numbers[u.ID]; no > 0 {
			scenesOf[no], unitOf[no] = drama.SplitScenes(chapterPlainText(u.SourceText), u.Title), u.ID
		}
	}
	var out []drama.ColorScriptEntry
	for _, e := range script {
		if e.SceneRule != 0 || runEpisodes[e.Episode] || colorScriptCurrent(e, table) {
			continue
		}
		scenes, ok := scenesOf[e.Episode]
		if !ok || (e.UnitID != "" && e.UnitID != unitOf[e.Episode]) || !drama.ScenesRenumbered(scenes) || drama.SceneOfLegacy(scenes, e.SceneNo) == nil {
			continue
		}
		out = append(out, e)
	}
	return out
}
