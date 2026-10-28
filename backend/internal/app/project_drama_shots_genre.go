// 生成分段按小节写（M8 卡 F）：建任务时按项目视频模型单段上限选戏类（文戏 15 / 文戏 30），按戏类把本单元切成小节（不调模型），
// 小节表和每一节的本轮指令（本场第一节带本场色卡）存进 metadata.dramaShots；worker 一轮一节（project_drama_run.go），
// 收尾逐节用戏类的转换器把技能原生输出转成分镜行（project_drama_finalize.go），原样输出按节存进 drama_shotlists.sections_json。
// M7.2 第 6 阶段开跑前的戏类判定调用和四张戏类卡已经去掉；整章 JSON Lines 的旧拼法（yz-chapter-shots）只留给技能包评测。

package app

import (
	"encoding/json"
	"slices"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// dramaShotsMeta 是任务 metadata.dramaShots。
type dramaShotsMeta struct {
	// Genre 是这次运行的戏类（wenxi15 / wenxi30）；ModelMax 是切节用的单段上限（秒）。
	Genre    string `json:"genre"`
	ModelMax int    `json:"modelMax"`
	// Sections 是切好的小节（一轮一节）；Turns 是每一节的本轮指令，第一节接在任务提示词后面。
	Sections []drama.Section `json:"sections"`
	Turns    []string        `json:"turns"`
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

// sectionShotsPlan 是按小节生成的一次运行：按戏类切小节、组装第一轮提示词和每一节的本轮指令（不读库，测试直接拿接口 JSON 喂进来）。
func sectionShotsPlan(project *model.Project, unit *model.ProjectUnit, units []model.ProjectUnit, assets []ProjectAssetSummary, usage DramaStepUsage, skill drama.StepSkill, genre drama.ShotGenre, m drama.Model) (*dramaStepPlan, error) {
	style := projectStyleSnapshot(project)
	anchor, _ := projectStyleBible(project.BibleJSON)
	stepAssets, assetRecords := chapterShotsAssets(assets)
	characters, characterRecords := chapterShotsCharacters(assets)
	plan := drama.PlanSections(chapterPlainText(unit.SourceText), unit.Title, genre, m.Duration.Max)
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
			Skill:          skill,
		},
		Genre: genre, Plan: plan, ModelLabel: m.Label,
		MasterPalette: projectMasterPalette(project.BibleJSON),
		Palettes:      unitScenePalettePrompts(project, units, unit.ID),
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
	shots := &dramaShotsMeta{Genre: genre.ID, ModelMax: m.Duration.Max, Sections: plan.Sections, Turns: prompt.Turns}
	return &dramaStepPlan{usage: usage, prompt: prompt.StepPrompt, skill: skill, scenes: turns, record: record, shots: shots}, nil
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
