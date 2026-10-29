// 生成分段按小节写（M8 卡 F / 卡 E）：建任务时按项目视频模型单段上限选默认戏类（文戏 15 / 文戏 30），用户改成武戏的剧本范围
// （本章镜头表 sections_json 里存的各节戏类，GenreOverridesFrom）按武戏，把本单元切成小节（不调模型）；小节表、每个戏类那条对话的
// 第一轮提示词和每一节的本轮指令（本场第一节带本场色卡）存进 metadata.dramaShots；worker 一轮一节、一个戏类一条对话
// （project_drama_run.go），收尾逐节用这一节戏类的转换器把技能原生输出转成分镜行（project_drama_finalize.go），原样输出按节存进
// drama_shotlists.sections_json（各节的戏类也就跟着存下）。
// M7.2 第 6 阶段开跑前的戏类判定调用和四张戏类卡已经去掉；整章 JSON Lines 的旧拼法（yz-chapter-shots）只留给技能包评测。

package app

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// dramaShotsMeta 是任务 metadata.dramaShots。
type dramaShotsMeta struct {
	// Genre 是这次运行的默认戏类（wenxi15 / wenxi30，按模型上限选）；各节的戏类在 Sections[i].Genre。ModelMax 是切节用的单段上限（秒）。
	Genre    string `json:"genre"`
	ModelMax int    `json:"modelMax"`
	// Sections 是切好的小节（一轮一节）；Turns 是每一节的本轮指令，第一节接在任务提示词后面。
	Sections []drama.Section `json:"sections"`
	Turns    []string        `json:"turns"`
	// Lanes 是第 1 节所在戏类以外各戏类那条对话的第一轮提示词（戏类 → 提示词）：写到这个戏类的第一节时接在本轮指令前面。
	Lanes map[string]string `json:"lanes,omitempty"`
	// Overrides 是这次切节用的改戏类范围（GenreOverride）；Regenre 是「改为武戏 / 改回文戏」的这一次改动（其余运行为空）。
	Overrides []drama.GenreOverride `json:"overrides,omitempty"`
	Regenre   *dramaRegenre         `json:"regenre,omitempty"`
}

// dramaRegenre 是分镜制作里把一节改成武戏（或改回文戏）：Scene 是这一节所在的场，From 是用户点的那一节（改之前的编号）和它原来的戏类，
// Genre 是改成的戏类，Sections 是按新戏类重切后要重写的节（新编号）。这一场里切法变了的旧节在收尾时换掉，不和新节混在一起。
type dramaRegenre struct {
	Scene     string   `json:"scene"`
	From      string   `json:"from"`
	FromGenre string   `json:"fromGenre"`
	Genre     string   `json:"genre"`
	Sections  []string `json:"sections"`
}

// genreOverridesOf 是本章镜头表存下的各节戏类里改成武戏的剧本范围（对不上当前正文的不算）；没有镜头表或不是按小节生成的返回 nil。
func genreOverridesOf(record *model.DramaShotlist, unit *model.ProjectUnit) []drama.GenreOverride {
	if record == nil || strings.TrimSpace(record.SectionsJSON) == "" {
		return nil
	}
	var outputs []drama.SectionOutput
	if json.Unmarshal([]byte(record.SectionsJSON), &outputs) != nil {
		return nil
	}
	return drama.GenreOverridesFrom(outputs, chapterPlainText(unit.SourceText), unit.Title)
}

// unitGenreOverrides 读本章镜头表，取改成武戏的剧本范围（刷新、整章重跑、只重跑几节都按它切，武戏不被默认文戏冲掉）。
func (s *Service) unitGenreOverrides(projectID string, unit *model.ProjectUnit) ([]drama.GenreOverride, error) {
	record, err := s.repo.DramaShotlistForUnit(projectID, unit.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return genreOverridesOf(record, unit), nil
}

// taskDramaShots 从任务输入读 metadata.dramaShots；不是按小节生成的任务返回 nil。metadata 不含密钥，不用解密。
func taskDramaShots(inputJSON string) *dramaShotsMeta {
	if !strings.Contains(inputJSON, `"dramaShots"`) {
		return nil
	}
	var input struct {
		Metadata struct {
			DramaShots *dramaShotsMeta `json:"dramaShots"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil || input.Metadata.DramaShots == nil || len(input.Metadata.DramaShots.Sections) == 0 {
		return nil
	}
	return input.Metadata.DramaShots
}

// projectShotGenre 是项目生成分段用的戏类和段长：项目生成模型的单段上限，叠上需求文档的单段秒数和渠道段长规则（只收紧）。
func projectShotGenre(project *model.Project, rule *DramaShotlistRule) (drama.ShotGenre, drama.Model, error) {
	m, err := dramaShotlistModel(project, briefSegmentRule(project.BriefJSON, rule))
	if err != nil {
		return drama.ShotGenre{}, drama.Model{}, err
	}
	return drama.DefaultShotGenre(m.Duration.Max), m, nil
}

// unitScenePalettePrompts 是本单元各场的【本场色卡】段（单元内场编号 → 文字）；导演风格没写到、对不上当前场次表的场没有。
func unitScenePalettePrompts(project *model.Project, units []model.ProjectUnit, unitID string) map[string]string {
	out := map[string]string{}
	for scene, e := range unitSceneColorScript(project.BibleJSON, units, unitID) {
		if text := drama.ScenePalettePrompt(&e); text != "" {
			out[scene] = text
		}
	}
	return out
}

// projectMasterPalette 是项目锁定风格时定的主基调色卡（bible_json.master_palette）；没有时为 nil。
func projectMasterPalette(bibleJSON string) *drama.MasterPalette {
	var doc struct {
		MasterPalette *drama.MasterPalette `json:"master_palette"`
	}
	if raw := strings.TrimSpace(bibleJSON); raw == "" || json.Unmarshal([]byte(raw), &doc) != nil || doc.MasterPalette.Empty() {
		return nil
	}
	return doc.MasterPalette
}

// sectionShotsPlan 是按小节生成的一次运行：按默认戏类和改戏类范围切小节、组装每个戏类那条对话的第一轮提示词和每一节的本轮指令
// （不读库，测试直接拿接口 JSON 喂进来）。skills 是各戏类绑定的技能（usage 是默认戏类那一格）。
func sectionShotsPlan(project *model.Project, unit *model.ProjectUnit, units []model.ProjectUnit, assets []ProjectAssetSummary, usage DramaStepUsage, skills map[string]drama.StepSkill, genre drama.ShotGenre, m drama.Model, overrides []drama.GenreOverride) (*dramaStepPlan, error) {
	style := projectStyleSnapshot(project)
	anchor, _ := projectStyleBible(project.BibleJSON)
	stepAssets, assetRecords := chapterShotsAssets(assets)
	characters, characterRecords := chapterShotsCharacters(assets)
	plan := drama.PlanSections(chapterPlainText(unit.SourceText), unit.Title, genre, m.Duration.Max, overrides)
	if len(plan.Sections) == 0 {
		return nil, BadAuthRequest("当前章节没有可用于分镜的正文")
	}
	prompt, err := drama.BuildSectionShotsPrompt(drama.SectionShotsInput{
		ChapterShotsInput: drama.ChapterShotsInput{
			ChapterTitle:   unit.Title,
			SourceText:     chapterEditorText(unit.SourceText),
			Style:          drama.StepStyle{Title: style.Title, Prompt: style.Prompt},
			Characters:     characters,
			Assets:         stepAssets,
			EpisodeSeconds: briefEpisodeSeconds(project.BriefJSON),
			Anchor:         anchor,
		},
		Skills: skills, Plan: plan, ModelLabel: m.Label, AspectRatio: strings.TrimSpace(project.AspectRatio),
		MasterPalette: projectMasterPalette(project.BibleJSON),
		Palettes:      unitScenePalettePrompts(project, units, unit.ID),
		Casts:         sceneCastTitles(sceneCastOf(project.BibleJSON, units, unit.ID, assets)),
	})
	if err != nil {
		return nil, err
	}
	turns := make([]drama.ShotsScene, 0, len(plan.Sections))
	for _, sec := range plan.Sections {
		turns = append(turns, drama.ShotsScene{ID: sec.Scene, Heading: sec.Heading, Section: sec.ID, Genre: sec.Genre, Est: sec.Est})
	}
	record := map[string]any{
		"canvasAssets":        assetRecords,
		"requirements":        "按小节输出技能原生格式的分镜，服务端转成镜头表写入分镜制作。",
		"projectStyle":        map[string]any{"presetId": style.PresetID, "title": style.Title, "prompt": style.Prompt},
		"characters":          characterRecords,
		"shotDurationSeconds": 0,
		"shotCount":           0,
	}
	shots := &dramaShotsMeta{Genre: genre.ID, ModelMax: m.Duration.Max, Sections: plan.Sections, Turns: prompt.Turns, Overrides: overrides}
	out := &dramaStepPlan{usage: usage, prompt: prompt.StepPrompt, skill: skills[genre.ID], scenes: turns, record: record, shots: shots}
	// 第 1 节不是默认戏类（第一场开头就改成了武戏）时，任务提示词是那个戏类的对话；默认戏类没有节时只记出现的戏类的技能。
	primary := plan.Sections[0].Genre
	if _, ok := skills[genre.ID]; !ok || !slices.ContainsFunc(plan.Sections, func(s drama.Section) bool { return s.Genre == genre.ID }) {
		out.skill = skills[primary]
	}
	for _, g := range plan.Genres() {
		if lane, ok := prompt.Lanes[g.ID]; ok {
			if shots.Lanes == nil {
				shots.Lanes = map[string]string{}
			}
			shots.Lanes[g.ID] = lane.Text
			out.prompt.Files = append(out.prompt.Files, lane.Files...)
		}
		if sk := skills[g.ID]; sk.ID != "" && sk.ID != out.skill.ID {
			out.laneSkills = append(out.laneSkills, sk)
		}
	}
	return out, nil
}

// sectionConvertContext 是收尾转换一节要的上下文：项目资产名（角色、场景、道具）、本场场景的锚点、是不是内景。
func sectionConvertContext(sec drama.Section, assets []ProjectAssetSummary, offset int) drama.SectionContext {
	ctx := drama.SectionContext{Section: sec, ShotOffset: offset, Indoor: strings.Contains(drama.ParseSceneHeading(sec.Heading).InOut, "内")}
	var allAnchors []string
	for _, a := range assets {
		if string(a.Status) == "archived" || strings.TrimSpace(a.Title) == "" {
			continue
		}
		category := string(a.Category)
		switch {
		case category == "character":
			ctx.Characters = append(ctx.Characters, a.Title)
		case slices.Contains(chapterShotsSceneCategories, category):
			ctx.Places = append(ctx.Places, a.Title)
			var anchors []string
			if a.Visual != nil {
				anchors = sceneAnchorList(a.Visual.Definition["anchors"])
			}
			allAnchors = append(allAnchors, anchors...)
			if drama.SectionPlaceMatches(sec.Heading, a.Title) {
				ctx.Anchors = append(ctx.Anchors, anchors...)
			}
		case category == "prop" || category == "wardrobe" || category == "weapon" || category == "accessory":
			ctx.Props = append(ctx.Props, a.Title)
		}
	}
	if len(ctx.Anchors) == 0 {
		ctx.Anchors = allAnchors
	}
	return ctx
}

// unitSceneColorScript 是这个单元各场的色彩脚本，按单元内的场编号（E01S02，与镜头 spec.scene 同一套）：
// 导演风格按项目统一编集号，这里取场次表里属于这个单元、还对得上当前场次表的条目。
func unitSceneColorScript(bibleJSON string, units []model.ProjectUnit, unitID string) map[string]drama.ColorScriptEntry {
	_, script := projectStyleBible(bibleJSON)
	if len(script) == 0 {
		return nil
	}
	table := styleSceneTable(units)
	out := map[string]drama.ColorScriptEntry{}
	for _, e := range script {
		if slot, ok := table[e.Code()]; ok && slot.UnitID == unitID && colorScriptCurrent(e, table) {
			out[drama.SceneCode(1, e.SceneNo)] = e
		}
	}
	return out
}

func joinTruthyStrings(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

// unitScenePalettes 是装段时绑到各场镜头的色卡：本单元每一场（单元内场编号 E01S02）对上的场次色卡（kind=scene）主版本。色卡按
// 项目级场编号（场次色卡的集号）认，对到场次表里属于本单元、色彩脚本还对得上当前场次表的场；旧色卡（M8 之前图片模型出的）、
// 场次已不存在（stale）、归档、没有主版本的跳过。
func (s *Service) unitScenePalettes(userID string, project *model.Project, unitID string) (map[string]string, error) {
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return nil, err
	}
	script := unitSceneColorScript(project.BibleJSON, units, unitID)
	if len(script) == 0 {
		return nil, nil
	}
	assets, err := s.projectPaletteAssets(userID, project.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for sceneID, e := range script {
		a, ok := assets[e.Code()]
		if !ok || !scenePaletteUsable(&a) {
			continue
		}
		out[sceneID] = a.PrimaryVersionID
	}
	return out, nil
}

// segmentPaletteVersions 是一段要绑的色卡版本：一段只属于一场（按小节装段时一节一段，同场各节绑同一张）。
func segmentPaletteVersions(seg drama.Segment, palettes map[string]string) []string {
	if v := palettes[strings.ToUpper(strings.TrimSpace(seg.Scene))]; v != "" {
		return []string{v}
	}
	return nil
}
