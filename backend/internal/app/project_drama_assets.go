// 资产设计（assets.extract，M7.2 第 5 阶段）：单元级步骤。用户在剧集或章节上点「提取资产」，服务端按项目记忆（本单元剧本、
// 已有资产名录、风格锚、本集色彩脚本、风格档案、项目画风）组装提示词、建一个任务，worker 在任务里一轮写一场（逐轮续写与生成分段共用
// project_drama_run.go）；收尾时解析、检查，按名字和别名与项目已有资产合并——已有的只补别名和缺的外观提示词，不改名字、不动已绑的图；
// 其余先写候选（记下来自哪个单元）、同一事务里直接确认成资产和版本（不走资产页的人工确认）。带了图片任务模板时接着给这次新建、
// 还没有图的资产排出图任务：角色走三视图（character_turnaround 模板，收尾 finalizeCharacterTurnaroundTask 绑到新版本），
// 场景、道具走资产概念图（project_asset_image，收尾 finalizeProjectAssetImageTask 绑图）；排队与色卡共用 project_drama_image_queue.go。
// 外观提示词存在资产版本的设定里：角色是 appearance（外观）和 multiViewPrompt（三视图提示词），场景、道具是 description 和 prompt。
// M8 卡 D：要先有这一集的场次色卡才能提取（palette_required）；提示词带主基调色卡（角色、道具）和本集场次色卡（场景），外观写色名和
// HEX；角色行的 variants 建成子卡（project_drama_variants.go）；写库时把场景资产回填到场次（bible_json.scene_assets，
// project_drama_scene_cast.go）；出图带色卡图（角色、子卡、道具带主基调色卡图，场景带本场色卡图和主基调色卡图，子卡另带主卡三视图）。
// 场次色卡收尾自动按集依次接着提取（metadata.dramaAssets.chain，一集跑完再起下一集）。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const (
	// dramaAssetsCandidateSource 是资产设计写的候选的 source（候选直接是已确认，资产页候选区不显示）。
	dramaAssetsCandidateSource = "drama_assets_extract"
	// assetImageKey 是角色、场景、道具出图状态在 payload.metadata 里的键（只有资产设计建的资产有）。
	assetImageKey = "assetImage"
	// dramaAssetImageTaskSource 是资产设计排的出图任务的 metadata.source。
	dramaAssetImageTaskSource = "short-drama-assets"
	dramaAssetsOutcomeItems   = 40
)

// dramaAssetCategories 是资产设计写的三类资产。
var dramaAssetCategories = []model.AssetCategory{model.AssetCategoryCharacter, model.AssetCategoryEnvironment, model.AssetCategoryProp}

// dramaAssetsMeta 是任务 metadata.dramaAssets：本单元、它在导演风格编号里的集号（取本集色彩脚本，0 表示不在编号里）、场数、要不要出图；
// Chain 是场次色卡收尾自动接着起的提取（还没提取的后面几集）。
type dramaAssetsMeta struct {
	UnitID  string           `json:"unitId"`
	Kind    string           `json:"kind"`
	Episode int              `json:"episode,omitempty"`
	Scenes  int              `json:"scenes"`
	Images  bool             `json:"images"`
	Chain   *dramaAssetChain `json:"chain,omitempty"`
}

// dramaAssetChain 是自动接着提取的一串：From 是起这一串的场次色卡任务，Queue 是这一集之后还要依次提取的单元。
type dramaAssetChain struct {
	From  string   `json:"from"`
	Queue []string `json:"queue"`
}

func taskDramaAssets(inputJSON string) *dramaAssetsMeta {
	var input struct {
		Metadata struct {
			DramaAssets *dramaAssetsMeta `json:"dramaAssets"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil {
		return nil
	}
	return input.Metadata.DramaAssets
}

// taskAssetImageTemplate 从（解密后的）任务输入读顶层 assetImageTask。
func taskAssetImageTemplate(inputJSON string) *DramaImageTaskTemplate {
	var input struct {
		Template *DramaImageTaskTemplate `json:"assetImageTask"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil || !input.Template.usable() {
		return nil
	}
	return input.Template
}

// ---------------------------------------------------------------- 组装提示词

// errPaletteRequiredMessage 是还没有场次色卡时提取资产的提示（reason palette_required）。
const errPaletteRequiredMessage = "先生成场次色卡：资产的颜色按主基调色卡和本场色卡约束，这一%s还没有场次色卡，到章节页点「生成场次色卡」"

// planAssetsExtract 读单元列表、资产清单和 bible，要求这一集已有场次色卡，组装提示词。
func (s *Service) planAssetsExtract(userID string, project *model.Project, unit *model.ProjectUnit, usage DramaStepUsage, skill drama.StepSkill) (*dramaStepPlan, error) {
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return nil, err
	}
	ready, err := s.unitScenePaletteReady(userID, project, unit.ID)
	if err != nil {
		return nil, err
	}
	if !ready {
		noun := "章"
		if unit.Kind == model.ProjectUnitKindEpisode {
			noun = "集"
		}
		appErr := NewAppError(http.StatusBadRequest, fmt.Sprintf(errPaletteRequiredMessage, noun))
		appErr.Reason = ReasonPaletteRequired
		return nil, appErr
	}
	assets, err := s.ProjectAssets(userID, project.ID)
	if err != nil {
		return nil, err
	}
	return assetsExtractPlan(project, unit, units, assets, usage, skill)
}

// unitScenePaletteReady 是这个单元已经有场次色卡：色彩脚本里有这一集对得上当前场次表的场，且至少一场有能用的场次色卡（kind=scene）。
func (s *Service) unitScenePaletteReady(userID string, project *model.Project, unitID string) (bool, error) {
	palettes, err := s.unitScenePalettes(userID, project, unitID)
	return len(palettes) > 0, err
}

// assetsUnitNumber 是单元在导演风格编号里的集号（有剧集看剧集，没有剧集看原文章节；不在编号里为 0），和它的叫法（第 N 集 / 第 N 章）。
func assetsUnitNumber(unit *model.ProjectUnit, units []model.ProjectUnit) (int, string, string) {
	sorted := slices.Clone(units)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Position < sorted[j].Position })
	episode := 0
	if _, numbers, _, err := styleBibleUnits(sorted, nil); err == nil {
		episode = numbers[unit.ID]
	}
	noun := "章"
	if unit.Kind == model.ProjectUnitKindEpisode {
		noun = "集"
	}
	index := 0
	for _, u := range sorted {
		if u.Kind == unit.Kind {
			index++
		}
		if u.ID == unit.ID {
			break
		}
	}
	return episode, fmt.Sprintf("第 %d %s", index, noun), noun
}

// assetsEpisodeColorScript 是本集的色彩脚本：集号对得上、还对得上当前场次表的各场。
func assetsEpisodeColorScript(bibleJSON string, units []model.ProjectUnit, episode int) []drama.ColorScriptEntry {
	if episode <= 0 {
		return nil
	}
	_, script := projectStyleBible(bibleJSON)
	table := styleSceneTable(units)
	var out []drama.ColorScriptEntry
	for _, e := range script {
		if e.Episode == episode && colorScriptCurrent(e, table) {
			out = append(out, e)
		}
	}
	sortColorScript(out)
	return out
}

// assetsExtractPlan 把单元、项目记忆和技能版本组装成提示词与运行参数（不读库，评测直接拿评测集的单元和资产清单喂进来）。
func assetsExtractPlan(project *model.Project, unit *model.ProjectUnit, units []model.ProjectUnit, assets []ProjectAssetSummary, usage DramaStepUsage, skill drama.StepSkill) (*dramaStepPlan, error) {
	text := chapterEditorText(unit.SourceText)
	if text == "" {
		return nil, BadAuthRequest("当前单元没有正文，不能提取资产")
	}
	episode, label, noun := assetsUnitNumber(unit, units)
	anchor, _ := projectStyleBible(project.BibleJSON)
	_, master, _, _ := projectLockedStyle(project.BibleJSON)
	style := projectStyleSnapshot(project)
	known, records := assetsKnownFromSummaries(assets)
	in := drama.AssetsExtractInput{
		UnitTitle: unit.Title, UnitLabel: label, UnitNoun: noun, SourceText: text, Known: known, Anchor: anchor, Master: master,
		ColorScript: assetsEpisodeColorScript(project.BibleJSON, units, episode),
		Style:       drama.StepStyle{Title: style.Title, Prompt: style.Prompt}, Skill: skill,
	}
	if p, err := drama.BuiltinProfile(projectDramaContext(project).profileID()); err == nil {
		in.Profile = drama.StyleBibleProfile{Label: p.Label, StyleBlock: p.StyleBlock.Zh, LightPolicy: p.LightPolicy}
	}
	prompt, err := drama.BuildAssetsExtractPrompt(in)
	if err != nil {
		return nil, err
	}
	scenes := drama.ChapterShotsScenes(chapterPlainText(unit.SourceText), unit.Title)
	return &dramaStepPlan{
		usage: usage, prompt: prompt, skill: skill, scenes: scenes,
		record: map[string]any{
			"canvasAssets": records,
			"requirements": "提取本" + noun + "的角色、场景和道具，每个资产写中文外观和英文出图提示词，直接写进项目资产并出图。",
			"dramaSources": []map[string]any{{"unitId": unit.ID, "title": unit.Title, "kind": string(unit.Kind), "episode": episode}},
		},
		assets: &dramaAssetsMeta{UnitID: unit.ID, Kind: string(unit.Kind), Episode: episode, Scenes: max(1, len(scenes))},
	}, nil
}

// assetsKnownFromSummaries 是【项目已有资产】：资产清单里的角色、场景、道具（不含归档的），名字、别名、是否缺外观。
// 角色子卡不单列，记在主卡后面（「子卡：护士服、淋湿」）。
func assetsKnownFromSummaries(assets []ProjectAssetSummary) ([]drama.AssetsKnown, []dramaStepAssetRecord) {
	var known []drama.AssetsKnown
	var records []dramaStepAssetRecord
	variants := map[string][]string{}
	for _, a := range assets {
		if a.Variant != nil && string(a.Status) != string(model.AssetVersionStatusArchived) {
			variants[a.Variant.ParentID] = append(variants[a.Variant.ParentID], a.Variant.Label)
		}
	}
	for _, a := range assets {
		if !slices.Contains(dramaAssetCategories, a.Category) || string(a.Status) == string(model.AssetVersionStatusArchived) || a.Variant != nil {
			continue
		}
		var definition map[string]any
		imaged := a.MediaType == "image"
		switch {
		case a.Character != nil:
			definition, imaged = a.Character.Definition, a.Character.VisualStatus == "ready"
		case a.Visual != nil:
			definition = a.Visual.Definition
		}
		known = append(known, drama.AssetsKnown{Category: string(a.Category), Name: a.Title, Aliases: definitionAliases(definition), Incomplete: !imaged && !assetDefinitionHasLook(string(a.Category), definition), Variants: variants[a.ID]})
		records = append(records, dramaStepAssetRecord{ID: a.ID, Title: a.Title, Category: string(a.Category)})
	}
	return known, records
}

// definitionAliases 读资产设定里的 aliases（字符串数组）。
func definitionAliases(definition map[string]any) []string {
	var out []string
	for _, text := range aliasStrings(definition["aliases"]) {
		if text = strings.TrimSpace(text); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// assetDefinitionHasLook 是设定里已有外观或出图提示词（角色看 appearance / multiViewPrompt，场景、道具看 description / prompt）。
func assetDefinitionHasLook(category string, definition map[string]any) bool {
	keys := []string{"description", "prompt"}
	if category == string(model.AssetCategoryCharacter) {
		keys = []string{"appearance", "multiViewPrompt"}
	}
	for _, k := range keys {
		if text, _ := definition[k].(string); strings.TrimSpace(text) != "" {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- 收尾

// DramaAssetsCategoryCount 是一类资产的新增、合并（已有资产，含补了别名或外观的）、跳过数。
type DramaAssetsCategoryCount struct {
	Created int `json:"created"`
	Merged  int `json:"merged"`
	Skipped int `json:"skipped"`
}

// DramaAssetsOutcomeItem 是写出的一个资产：Action 是 created（新建）、updated（已有资产补了别名或外观）、merged（已有，没有可补的）。
type DramaAssetsOutcomeItem struct {
	AssetID  string `json:"assetId"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Action   string `json:"action"`
	// ParentID 是子卡的主卡。
	ParentID string `json:"parentId,omitempty"`
}

// DramaAssetsExtractOutcome 是资产设计收尾写进任务结果 result.dramaStep 的摘要。Variants 是新建的角色子卡数（也算在 Created 里），
// Scenes 是回填了场景资产的场数，Chained 是自动接着起的下一集提取。
type DramaAssetsExtractOutcome struct {
	Step       string                              `json:"step"`
	Status     string                              `json:"status"`
	UnitID     string                              `json:"unitId"`
	Parse      drama.AssetsExtractParseStats       `json:"parse"`
	Created    int                                 `json:"created"`
	Merged     int                                 `json:"merged"`
	Skipped    int                                 `json:"skipped"`
	ByCategory map[string]DramaAssetsCategoryCount `json:"byCategory"`
	Errors     int                                 `json:"errors"`
	Warnings   int                                 `json:"warnings"`
	Items      []drama.CheckItem                   `json:"items,omitempty"`
	Assets     []DramaAssetsOutcomeItem            `json:"assets"`
	// Images 是出图：排上的、等空位的（同时任务数有上限，前面的出完图自动接着排）、没排上的。
	Images   DramaImageQueueResult `json:"images"`
	Variants int                   `json:"variants"`
	Scenes   int                   `json:"scenes"`
	Chained  *DramaChainedTask     `json:"chained,omitempty"`
}

// dramaAssetEntry 是项目里的一个角色、场景或道具（合并用）：主版本和它的设定、有没有图。
type dramaAssetEntry struct {
	asset      model.Asset
	version    *model.AssetVersion
	definition map[string]any
	imaged     bool
	// changed / content 是这次合并改了设定（content：改了别名以外的字段，要加版本）；rows 是合并进来的行数。
	changed bool
	content bool
}

// dramaAssetIndex 是按「分类:名字键」找已有资产的索引（名字和别名都算）。角色子卡不进名字索引，按主卡 ID 和变体名另记在 children。
type dramaAssetIndex struct {
	entries  []*dramaAssetEntry
	byKey    map[string]*dramaAssetEntry
	children map[string][]*dramaAssetEntry
}

func (s *Service) dramaAssetIndexOf(userID string, projectID string) (*dramaAssetIndex, error) {
	assets, err := s.repo.ProjectAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	index := &dramaAssetIndex{byKey: map[string]*dramaAssetEntry{}, children: map[string][]*dramaAssetEntry{}}
	for _, a := range assets {
		if !slices.Contains(dramaAssetCategories, a.Category) || a.Status == model.AssetVersionStatusArchived {
			continue
		}
		entry := &dramaAssetEntry{asset: a, definition: map[string]any{}, imaged: a.Kind == "image"}
		if a.PrimaryVersionID != "" {
			version, err := s.repo.AssetVersion(a.PrimaryVersionID)
			if err != nil {
				return nil, err
			}
			entry.version = version
			_ = json.Unmarshal([]byte(firstNonEmpty(version.DefinitionJSON, "{}")), &entry.definition)
			if entry.definition == nil {
				entry.definition = map[string]any{}
			}
			if a.Category == model.AssetCategoryCharacter {
				if entry.imaged, err = s.characterVersionImaged(version.ID); err != nil {
					return nil, err
				}
			}
		}
		if a.ParentAssetID != "" {
			index.children[a.ParentAssetID] = append(index.children[a.ParentAssetID], entry)
			continue
		}
		index.add(entry)
	}
	return index, nil
}

func (x *dramaAssetIndex) add(entry *dramaAssetEntry) {
	x.entries = append(x.entries, entry)
	for _, name := range append([]string{entry.asset.Title}, definitionAliases(entry.definition)...) {
		key := string(entry.asset.Category) + ":" + model.AssetCandidateNameKey(name)
		if _, ok := x.byKey[key]; !ok && model.AssetCandidateNameKey(name) != "" {
			x.byKey[key] = entry
		}
	}
}

func (x *dramaAssetIndex) lookup(row drama.AssetsExtractRow) *dramaAssetEntry {
	for _, name := range append([]string{row.Name}, row.Aliases...) {
		if entry, ok := x.byKey[row.Category+":"+model.AssetCandidateNameKey(name)]; ok {
			return entry
		}
	}
	return nil
}

func (x *dramaAssetIndex) known() []drama.AssetsKnown {
	out := make([]drama.AssetsKnown, 0, len(x.entries))
	for _, e := range x.entries {
		category := string(e.asset.Category)
		var variants []string
		for _, c := range x.children[e.asset.ID] {
			variants = append(variants, c.asset.VariantLabel)
		}
		out = append(out, drama.AssetsKnown{Category: category, Name: e.asset.Title, Aliases: definitionAliases(e.definition), Incomplete: !e.imaged && !assetDefinitionHasLook(category, e.definition), Variants: variants})
	}
	return out
}

// child 是主卡下变体名相同的子卡。
func (x *dramaAssetIndex) child(parentID string, label string) *dramaAssetEntry {
	for _, c := range x.children[parentID] {
		if drama.AssetVariantKey(c.asset.VariantLabel) == drama.AssetVariantKey(label) {
			return c
		}
	}
	return nil
}

// characterVersionImaged 是这一版角色有三视图（或旧版的正、侧、背三张）。
func (s *Service) characterVersionImaged(versionID string) (bool, error) {
	reps, err := s.repo.AssetRepresentations(versionID)
	if err != nil {
		return false, err
	}
	roles := map[string]bool{}
	for _, r := range reps {
		roles[r.Role] = true
	}
	return roles["turnaround_sheet"] || (roles["front"] && roles["side"] && roles["back"]), nil
}

// assetsRowDefinition 是一行资产写进设定的字段：角色 appearance / multiViewPrompt，场景、道具 description / prompt；
// palette_hint 引用到的色卡颜色（本集场次色卡、主基调色卡）接在英文提示词后面（Color accents），firstScene 写场编号（不在导演风格编号里时写场序）。
func assetsRowDefinition(row drama.AssetsExtractRow, episode int, colors []drama.ColorScriptEntry, master drama.MasterPalette) map[string]any {
	prompt := drama.AssetsPromptEn(row)
	if accents := drama.AssetsPaletteColors(row.PaletteHint, colors, master); prompt != "" && len(accents) > 0 {
		parts := make([]string, 0, len(accents))
		for _, c := range accents {
			parts = append(parts, strings.TrimSpace(firstNonEmpty(c.NameEn, c.NameZh)+" "+c.Hex))
		}
		if !strings.HasSuffix(prompt, ".") {
			prompt += "."
		}
		prompt += " Color accents: " + strings.Join(parts, ", ") + "."
	}
	definition := map[string]any{"aliases": slices.Clone(row.Aliases)}
	if row.Category == drama.AssetCharacter {
		definition["appearance"], definition["multiViewPrompt"] = drama.AssetsAppearance(row), prompt
	} else {
		definition["description"], definition["prompt"] = drama.AssetsAppearance(row), prompt
	}
	if len(row.Anchors) > 0 && row.Category == drama.AssetEnvironment {
		definition["anchors"] = slices.Clone(row.Anchors)
	}
	if row.FirstScene > 0 {
		definition["firstScene"] = fmt.Sprintf("第 %d 场", row.FirstScene)
		if episode > 0 {
			definition["firstScene"] = drama.SceneCode(episode, row.FirstScene)
		}
	}
	if row.PaletteHint != "" {
		definition["paletteHint"] = row.PaletteHint
	}
	return definition
}

// assetLookFields 是合并到已有资产时可以补的外观字段（空着才补）。
var assetLookFields = []string{"appearance", "multiViewPrompt", "description", "prompt", "anchors"}

// mergeIntoEntry 把一行并进已有资产的设定（只在内存里）：补别名（行的名字和已有名字不同也算），补空着的外观字段——
// 场景、道具已经有图的不补（补了出图提示词也和图对不上），角色的图在形象里，补设定不影响它。不改名字，不动已绑的图。
func mergeIntoEntry(entry *dramaAssetEntry, row drama.AssetsExtractRow, details map[string]any) {
	before, _ := json.Marshal(entry.definition)
	mergeAssetAliases(entry.definition, entry.asset.Title, entry.definition["aliases"], details["aliases"], row.Name)
	if !entry.imaged || entry.asset.Category == model.AssetCategoryCharacter {
		for _, key := range assetLookFields {
			value, ok := details[key]
			if !ok || !emptyCharacterDefinitionValue(entry.definition[key]) || emptyCharacterDefinitionValue(value) {
				continue
			}
			entry.definition[key] = value
			entry.content = true
		}
	}
	if after, _ := json.Marshal(entry.definition); string(after) != string(before) {
		entry.changed = true
	}
}

// applyAssetsExtractTask 是资产设计的 Apply：模型输出 → 资产行 → 检查 → 与已有资产合并 / 新建、建子卡、场景资产回填到场次（同一事务）。
// 一行能用的都没有时任务失败（资产都不动）。出图不在这里排（queueAssetsExtractImages）：收尾先接着建下一集的提取任务占一个空位，
// 再按剩下的空位排出图，免得出图把同时任务数占满、下一集接不上。
func (s *Service) applyAssetsExtractTask(task model.Task, meta *dramaStepTaskMeta, result map[string]interface{}) (DramaAssetsExtractOutcome, error) {
	target, err := taskDramaStepTarget(task)
	if err != nil {
		return DramaAssetsExtractOutcome{}, err
	}
	am := taskDramaAssets(task.InputJSON)
	if am == nil {
		return DramaAssetsExtractOutcome{}, errors.New("资产设计任务缺少运行参数")
	}
	project, err := s.activeProjectForUser(task.UserID, target.ProjectID)
	if err != nil {
		return DramaAssetsExtractOutcome{}, err
	}
	unit, err := s.repo.ProjectUnit(project.ID, target.UnitID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DramaAssetsExtractOutcome{}, errors.New("单元在写入前被删除，没有写入资产")
	}
	if err != nil {
		return DramaAssetsExtractOutcome{}, err
	}
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return DramaAssetsExtractOutcome{}, err
	}
	text, _ := result["text"].(string)
	rows, stats := drama.AssetsExtractFromText(text)
	index, err := s.dramaAssetIndexOf(task.UserID, project.ID)
	if err != nil {
		return DramaAssetsExtractOutcome{}, err
	}
	colors := assetsEpisodeColorScript(project.BibleJSON, units, am.Episode)
	_, master, _, _ := projectLockedStyle(project.BibleJSON)
	var locations []string
	for _, sc := range drama.SplitScenes(chapterPlainText(unit.SourceText), unit.Title) {
		locations = append(locations, sc.Location)
	}
	checked := drama.CheckAssetsExtract(rows, drama.AssetsExtractCheckContext{Known: index.known(), Scenes: am.Scenes, ColorScript: colors, Master: master, SceneLocations: locations})
	if len(checked.Rows) == 0 {
		return DramaAssetsExtractOutcome{}, dramaSentenceError{fmt.Errorf("资产设计没有能用的资产行（读出 %d 行，%d 行读不出，%d 行不合格），没有写入", stats.Rows, stats.Invalid, checked.Skipped)}
	}
	decrypted, decryptErr := s.decryptTaskInputJSON(task.InputJSON)
	template := (*DramaImageTaskTemplate)(nil)
	if am.Images && decryptErr == nil {
		template = taskAssetImageTemplate(decrypted)
	}
	writes, outcome, err := s.dramaAssetWrites(task.UserID, project, unit, am, index, checked, colors, master, template != nil)
	if err != nil {
		return DramaAssetsExtractOutcome{}, err
	}
	outcome.Step, outcome.Status, outcome.UnitID, outcome.Parse = meta.Step, DramaStepOutcomeWritten, unit.ID, stats
	// 场景资产回填到这一集的场次：已有的和这次新建的场景都算，地点对得上名字或别名的绑上。
	bible := ""
	if _, numbers, _ := sceneUnits(units); numbers[unit.ID] > 0 {
		bindings := unitSceneEnvironments(*unit, numbers[unit.ID], index.environments())
		if bible, err = mergeSceneAssetsDocument(project.BibleJSON, unit.ID, numbers[unit.ID], bindings); err != nil {
			return DramaAssetsExtractOutcome{}, err
		}
		outcome.Scenes = len(bindings)
	}
	if err := s.repo.ApplyDramaAssetsExtract(task.UserID, project.ID, writes, bible, time.Now()); err != nil {
		if errors.Is(err, repository.ErrDramaAssetChanged) {
			return DramaAssetsExtractOutcome{}, errors.New("要合并的资产在提取期间被改过，没有写入，请重新提取")
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DramaAssetsExtractOutcome{}, errors.New("项目或资产在写入前被删除，没有写入")
		}
		return DramaAssetsExtractOutcome{}, err
	}
	if am.Images && template == nil {
		_ = s.log(task.UserID, task.ID, "warn", "资产出图没有排上", "任务里读不出图片任务模板")
	}
	return outcome, nil
}

// queueAssetsExtractImages 给这次提取新建的资产排出图（带了图片任务模板、建了资产时）；排不上不影响已写的资产。
func (s *Service) queueAssetsExtractImages(task model.Task, outcome *DramaAssetsExtractOutcome) {
	am := taskDramaAssets(task.InputJSON)
	if am == nil || !am.Images || outcome.Created == 0 {
		return
	}
	decrypted, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		return
	}
	template := taskAssetImageTemplate(decrypted)
	if template == nil {
		return
	}
	if outcome.Images, err = s.queueDramaAssetImages(task.UserID, taskProjectID(task), *template, task.ID, nil); err != nil {
		_ = s.log(task.UserID, task.ID, "warn", "资产出图没有排上", err.Error())
		outcome.Images = emptyDramaImageQueueResult()
	}
}

// environments 是能绑到场次的场景资产（已有的在前，这次新建的按写的先后接在后面）。
func (x *dramaAssetIndex) environments() []sceneEnvironmentCandidate {
	var out []sceneEnvironmentCandidate
	for _, e := range x.entries {
		if e.asset.Category != model.AssetCategoryEnvironment {
			continue
		}
		first, _ := e.definition["firstScene"].(string)
		out = append(out, sceneEnvironmentCandidate{ID: e.asset.ID, Title: e.asset.Title, Aliases: definitionAliases(e.definition), FirstScene: first})
	}
	return out
}

// dramaAssetWrites 把检查后的行变成写库的项：对上已有资产的并进去（每个资产最多一项写），其余新建（候选直接是已确认）；
// 角色行的 variants 建成子卡（同主卡同变体名的只补场序）。
func (s *Service) dramaAssetWrites(userID string, project *model.Project, unit *model.ProjectUnit, am *dramaAssetsMeta, index *dramaAssetIndex, checked drama.AssetsExtractResult, colors []drama.ColorScriptEntry, master drama.MasterPalette, images bool) ([]repository.DramaAssetWrite, DramaAssetsExtractOutcome, error) {
	outcome := DramaAssetsExtractOutcome{Skipped: checked.Skipped, ByCategory: map[string]DramaAssetsCategoryCount{}, Errors: checked.Report.Errors, Warnings: checked.Report.Warnings, Assets: []DramaAssetsOutcomeItem{}, Images: emptyDramaImageQueueResult()}
	for _, item := range checked.Report.Items {
		if len(outcome.Items) < dramaAssetsOutcomeItems {
			outcome.Items = append(outcome.Items, item)
		}
	}
	count := func(category string, f func(*DramaAssetsCategoryCount)) {
		c := outcome.ByCategory[category]
		f(&c)
		outcome.ByCategory[category] = c
	}
	for category, n := range checked.SkippedBy {
		count(category, func(c *DramaAssetsCategoryCount) { c.Skipped += n })
	}
	folderID, err := s.resolveProjectAssetFolderID(project.ID, nil)
	if err != nil {
		return nil, outcome, err
	}
	position, err := s.repo.NextProjectAssetPosition(project.ID, folderID)
	if err != nil {
		return nil, outcome, err
	}
	now := time.Now()
	initial := dramaImageNone
	if images {
		initial = dramaImageWaiting
	}
	var writes []repository.DramaAssetWrite
	var merged []*dramaAssetEntry
	for _, row := range checked.Rows {
		details := assetsRowDefinition(row, am.Episode, colors, master)
		if entry := index.lookup(row); entry != nil {
			mergeIntoEntry(entry, row, details)
			if !slices.Contains(merged, entry) {
				merged = append(merged, entry)
			}
			continue
		}
		details["name"] = row.Name
		detailsJSON, err := json.Marshal(details)
		if err != nil {
			return nil, outcome, err
		}
		category := model.AssetCategory(row.Category)
		candidate := model.ProjectAssetCandidate{ID: newID(), ProjectID: project.ID, UnitID: unit.ID, Name: row.Name, NameKey: model.AssetCandidateNameKey(row.Name), Category: category, Status: "confirmed", Source: dramaAssetsCandidateSource, DetailsJSON: string(detailsJSON), CreatedAt: now, UpdatedAt: now}
		asset, version, err := assetFromCandidate(userID, &candidate, now)
		if err != nil {
			return nil, outcome, err
		}
		payload := assetPayload(&asset)
		payloadMetadata(payload)[assetImageKey] = dramaImageState{Status: initial, UpdatedAt: now.Format(time.RFC3339Nano)}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, outcome, err
		}
		asset.PayloadJSON = string(raw)
		candidate.ResolvedAssetID = asset.ID
		link := model.ProjectAssetLink{ID: newID(), ProjectID: project.ID, AssetID: asset.ID, FolderID: folderID, Position: position, CreatedAt: now}
		position++
		writes = append(writes, repository.DramaAssetWrite{Candidate: &candidate, Asset: &asset, Version: &version, Link: &link})
		// 后面的行（别名不同但实际是同一个）合并到它，不再新建。
		index.add(&dramaAssetEntry{asset: asset, version: &version, definition: details, imaged: false})
		outcome.Created++
		count(row.Category, func(c *DramaAssetsCategoryCount) { c.Created++ })
		outcome.Assets = append(outcome.Assets, DramaAssetsOutcomeItem{AssetID: asset.ID, Name: row.Name, Category: row.Category, Action: "created"})
	}
	created := map[string]bool{}
	for _, w := range writes {
		created[w.Asset.ID] = true
	}
	for _, entry := range merged {
		category := string(entry.asset.Category)
		if created[entry.asset.ID] {
			// 这次新建的资产又被后面的行补了别名：直接改在新建的版本上。
			for i := range writes {
				if writes[i].Asset.ID == entry.asset.ID {
					if err := refreshCreatedAssetDefinition(&writes[i], entry.definition); err != nil {
						return nil, outcome, err
					}
				}
			}
			continue
		}
		action := "merged"
		if entry.changed && entry.version != nil {
			w, err := s.mergedAssetWrite(entry, now)
			if err != nil {
				return nil, outcome, err
			}
			writes = append(writes, w)
			action = "updated"
		}
		outcome.Merged++
		count(category, func(c *DramaAssetsCategoryCount) { c.Merged++ })
		outcome.Assets = append(outcome.Assets, DramaAssetsOutcomeItem{AssetID: entry.asset.ID, Name: entry.asset.Title, Category: category, Action: action})
	}
	// 子卡：主卡这次新建的用合并后的设定，已有的用当前主版本的设定（主卡的名字和图不动）。
	for _, row := range checked.Rows {
		if row.Category != drama.AssetCharacter || len(row.Variants) == 0 {
			continue
		}
		parent := index.lookup(row)
		if parent == nil {
			continue
		}
		for _, v := range row.Variants {
			codes := variantSceneCodes(v.Scenes, am.Episode)
			if child := index.child(parent.asset.ID, v.Label); child != nil {
				w, changed, err := variantScenesWrite(child, codes, now)
				if err != nil {
					return nil, outcome, err
				}
				action := "merged"
				if changed && !created[child.asset.ID] {
					writes = append(writes, w)
					action = "updated"
				}
				outcome.Merged++
				count(drama.AssetCharacter, func(c *DramaAssetsCategoryCount) { c.Merged++ })
				outcome.Assets = append(outcome.Assets, DramaAssetsOutcomeItem{AssetID: child.asset.ID, Name: child.asset.Title, Category: drama.AssetCharacter, Action: action, ParentID: parent.asset.ID})
				continue
			}
			asset, version, err := newVariantAsset(userID, parent.asset, parent.definition, v.Label, v.Kind, v.Appearance, v.PromptEn, assetVariantMeta{Scenes: codes, Source: "extract"}, initial, now)
			if err != nil {
				return nil, outcome, err
			}
			link := model.ProjectAssetLink{ID: newID(), ProjectID: project.ID, AssetID: asset.ID, FolderID: folderID, Position: position, CreatedAt: now}
			position++
			writes = append(writes, repository.DramaAssetWrite{Asset: &asset, Version: &version, Link: &link})
			created[asset.ID] = true
			index.children[parent.asset.ID] = append(index.children[parent.asset.ID], &dramaAssetEntry{asset: asset, version: &version, definition: map[string]any{}})
			outcome.Created++
			outcome.Variants++
			count(drama.AssetCharacter, func(c *DramaAssetsCategoryCount) { c.Created++ })
			outcome.Assets = append(outcome.Assets, DramaAssetsOutcomeItem{AssetID: asset.ID, Name: asset.Title, Category: drama.AssetCharacter, Action: "created", ParentID: parent.asset.ID})
		}
	}
	return writes, outcome, nil
}

// variantScenesWrite 是已有子卡只补场：场都有了时 changed 为 false（不写）；否则只改 payload（不加版本、不动图）。
func variantScenesWrite(child *dramaAssetEntry, codes []string, now time.Time) (repository.DramaAssetWrite, bool, error) {
	meta := assetVariantMetaOf(&child.asset)
	if !mergeVariantScenes(&meta, codes) {
		return repository.DramaAssetWrite{}, false, nil
	}
	asset := child.asset
	payload := assetPayload(&asset)
	payloadMetadata(payload)[assetVariantKey] = meta
	payload["updatedAt"] = now.Format(time.RFC3339Nano)
	raw, err := json.Marshal(payload)
	if err != nil {
		return repository.DramaAssetWrite{}, false, err
	}
	asset.PayloadJSON = string(raw)
	child.asset = asset
	return repository.DramaAssetWrite{Asset: &asset, ExpectedVersionID: asset.PrimaryVersionID, PayloadOnly: true}, true, nil
}

// refreshCreatedAssetDefinition 把新建资产的设定换成合并后的（版本设定、候选详情、payload 里的设定一起换）。
func refreshCreatedAssetDefinition(w *repository.DramaAssetWrite, definition map[string]any) error {
	encoded, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	w.Version.DefinitionJSON, w.Candidate.DetailsJSON = string(encoded), string(encoded)
	payload := assetPayload(w.Asset)
	if data, ok := payload["data"].(map[string]any); ok {
		data["definition"] = definition
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	w.Asset.PayloadJSON = string(raw)
	return nil
}

// mergedAssetWrite 是已有资产合并后的写：角色加一个版本（形象和声音搬过去，与资产页「归并到角色」同一条路径）；
// 场景、道具补了外观的加一个版本，只补别名的原地改主版本设定（不影响出图）。名字、已绑的图都不动。
func (s *Service) mergedAssetWrite(entry *dramaAssetEntry, now time.Time) (repository.DramaAssetWrite, error) {
	encoded, err := json.Marshal(entry.definition)
	if err != nil {
		return repository.DramaAssetWrite{}, err
	}
	asset := entry.asset
	if asset.Category == model.AssetCategoryCharacter {
		// 角色加版本会重建 payload：资产层面的 metadata（出图排队状态）由 prepareNextCharacterVersion 带过去，还在等空位的角色照样排三视图。
		next, version, reps, voice, err := s.prepareNextCharacterVersion(&asset, asset.Title, string(encoded), nil, nil, false)
		if err != nil {
			return repository.DramaAssetWrite{}, err
		}
		version.Note = firstNonEmpty(version.Note, "资产设计补充")
		return repository.DramaAssetWrite{Asset: &next, Version: &version, ExpectedVersionID: entry.version.ID, Representations: reps, Voice: voice}, nil
	}
	payload := assetPayload(&asset)
	if data, ok := payload["data"].(map[string]any); ok && data["definition"] != nil {
		data["definition"] = entry.definition
	}
	if !entry.content {
		version := *entry.version
		version.DefinitionJSON = string(encoded)
		raw, err := json.Marshal(payload)
		if err != nil {
			return repository.DramaAssetWrite{}, err
		}
		asset.PayloadJSON = string(raw)
		return repository.DramaAssetWrite{Asset: &asset, Version: &version, ExpectedVersionID: entry.version.ID, DefinitionOnly: true}, nil
	}
	versions, err := s.repo.AssetVersions(asset.ID)
	if err != nil {
		return repository.DramaAssetWrite{}, err
	}
	number := entry.version.Version + 1
	if len(versions) > 0 {
		number = versions[0].Version + 1
	}
	version := model.AssetVersion{ID: newID(), AssetID: asset.ID, Version: number, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: string(encoded), Prompt: entry.version.Prompt, Note: "资产设计补充", CreatedAt: now, UpdatedAt: now}
	payload["primaryVersionId"], payload["updatedAt"] = version.ID, now.Format(time.RFC3339Nano)
	raw, err := json.Marshal(payload)
	if err != nil {
		return repository.DramaAssetWrite{}, err
	}
	asset.PrimaryVersionID, asset.PayloadJSON, asset.UpdatedAt = version.ID, string(raw), now
	return repository.DramaAssetWrite{Asset: &asset, Version: &version, ExpectedVersionID: entry.version.ID}, nil
}

// ---------------------------------------------------------------- 出图

// DramaAssetImagesRequest 是资产页「继续出图」：AssetIDs 为空时排全部资产设计建的、还没有图、没在出图的角色、场景和道具。
type DramaAssetImagesRequest struct {
	AssetIDs  []string                `json:"assetIds,omitempty"`
	ImageTask *DramaImageTaskTemplate `json:"imageTask"`
}

// dramaImageAsset 是一个能出图的角色、场景或道具，和它现在有没有图。
type dramaImageAsset struct {
	asset  model.Asset
	imaged bool
}

// projectDramaImageAssets 是项目里资产设计建的（payload 记了出图状态）角色、场景、道具，按建的先后。
func (s *Service) projectDramaImageAssets(userID string, projectID string) ([]dramaImageAsset, error) {
	assets, err := s.repo.ProjectAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	var out []dramaImageAsset
	for _, a := range assets {
		if !slices.Contains(dramaAssetCategories, a.Category) || a.Status == model.AssetVersionStatusArchived {
			continue
		}
		if _, ok := payloadMetadata(assetPayload(&a))[assetImageKey]; !ok {
			continue
		}
		imaged := a.Kind == "image"
		if a.Category == model.AssetCategoryCharacter && a.PrimaryVersionID != "" {
			if imaged, err = s.characterVersionImaged(a.PrimaryVersionID); err != nil {
				return nil, err
			}
		}
		out = append(out, dramaImageAsset{asset: a, imaged: imaged})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].asset.CreatedAt.Before(out[j].asset.CreatedAt) })
	return out, nil
}

// queueDramaAssetImages 给资产设计建的资产排出图任务：only 为空时排全部「等空位」的，否则先把 only 里的改成「等空位」再排。
// 按建的先后，空位用完就停，剩下的保持「等空位」，前面的出完图后接着排。
func (s *Service) queueDramaAssetImages(userID string, projectID string, template DramaImageTaskTemplate, origin string, only []string) (DramaImageQueueResult, error) {
	result := emptyDramaImageQueueResult()
	if !template.usable() {
		return result, BadAuthRequest("资产出图要先选图片模型")
	}
	dramaImageQueueMu.Lock()
	defer dramaImageQueueMu.Unlock()
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return result, err
	}
	assets, err := s.projectDramaImageAssets(userID, projectID)
	if err != nil {
		return result, err
	}
	now := time.Now().Format(time.RFC3339Nano)
	if len(only) > 0 {
		byID := map[string]model.Asset{}
		for _, a := range assets {
			byID[a.asset.ID] = a.asset
		}
		for _, id := range only {
			asset, ok := byID[strings.TrimSpace(id)]
			if !ok {
				return result, BadAuthRequest("所选资产不是这个项目里资产设计建的角色、场景或道具")
			}
			if state := dramaImageStateOf(&asset, assetImageKey); state.Status == dramaImageQueued && s.dramaImageTaskActive(userID, state.TaskID) {
				return result, BadAuthRequest(fmt.Sprintf("「%s」正在出图，等它完成再重出", asset.Title))
			}
			if err := s.putDramaImageState(userID, asset.ID, assetImageKey, dramaImageState{Status: dramaImageWaiting, UpdatedAt: now}); err != nil {
				return result, err
			}
		}
		if assets, err = s.projectDramaImageAssets(userID, projectID); err != nil {
			return result, err
		}
	}
	style := projectStyleSnapshot(project)
	byID := map[string]dramaImageAsset{}
	for _, item := range assets {
		byID[item.asset.ID] = item
	}
	var jobs []dramaImageJob
	for _, item := range assets {
		asset := item.asset
		if dramaImageStateOf(&asset, assetImageKey).Status != dramaImageWaiting {
			continue
		}
		// 子卡要带主卡的三视图：主卡还在等出图或正在出图时先不排，主卡出完图接着排时再排它。
		if parent, ok := byID[asset.ParentAssetID]; ok && !parent.imaged {
			if status := dramaImageStateOf(&parent.asset, assetImageKey).Status; status == dramaImageWaiting || (status == dramaImageQueued && s.dramaImageTaskActive(userID, dramaImageStateOf(&parent.asset, assetImageKey).TaskID)) {
				continue
			}
		}
		jobs = append(jobs, dramaImageJob{Asset: asset, Build: func(level int) (CreateTaskRequest, error) {
			version, err := s.repo.AssetVersion(asset.PrimaryVersionID)
			if err != nil {
				return CreateTaskRequest{}, err
			}
			definition := map[string]any{}
			_ = json.Unmarshal([]byte(firstNonEmpty(version.DefinitionJSON, "{}")), &definition)
			refs, err := s.dramaAssetImageRefs(userID, project, asset, definition)
			if err != nil {
				return CreateTaskRequest{}, err
			}
			if asset.Category == model.AssetCategoryCharacter {
				return s.dramaCharacterImageTaskRequest(userID, project, style, asset, version, template, origin, refs, level)
			}
			return s.dramaVisualImageTaskRequest(userID, project, style, asset, version, template, origin, refs, level)
		}})
	}
	return s.dispatchDramaImages(userID, assetImageKey, jobs)
}

// QueueDramaAssetImages 是资产页「继续出图」：不点名时排资产设计建的、还没有图也没在出图的资产。
func (s *Service) QueueDramaAssetImages(userID string, projectID string, req DramaAssetImagesRequest) (DramaImageQueueResult, error) {
	if req.ImageTask == nil {
		return DramaImageQueueResult{}, BadAuthRequest("资产出图要先选图片模型")
	}
	only := req.AssetIDs
	if len(only) == 0 {
		assets, err := s.projectDramaImageAssets(userID, projectID)
		if err != nil {
			return DramaImageQueueResult{}, err
		}
		for _, a := range assets {
			state := s.dramaImageView(userID, &a.asset, assetImageKey, a.imaged)
			if !a.imaged && state.Status != dramaImageQueued {
				only = append(only, a.asset.ID)
			}
		}
		if len(only) == 0 {
			return emptyDramaImageQueueResult(), nil
		}
	}
	return s.queueDramaAssetImages(userID, projectID, *req.ImageTask, "assets", only)
}

// characterStyleSections 是三视图从项目画风里取的小节：只继承角色资产相关规范（与前端 characterStyleConstraint 一致）。
var characterStyleSections = []string{"风格组合", "项目定位", "题材世界观", "视觉媒介", "角色设计系统", "项目色彩与光影", "服饰、材质与场景", "资产一致性", "全局禁用"}

// characterStyleConstraint 是三视图模板的【项目画风】：项目画风里角色相关的小节。
func characterStyleConstraint(style styleProfileDocument) string {
	if style.Prompt == "" {
		return "项目尚未指定画风，保持角色媒介中性。"
	}
	lines := []string{fmt.Sprintf("项目画风：%s。角色造型、配色、服装材质与最终渲染媒介必须遵循以下规范：", style.Title)}
	for _, line := range strings.Split(style.Prompt, "\n") {
		if strings.HasPrefix(line, "【") {
			if end := strings.Index(line, "】"); end > 0 && slices.Contains(characterStyleSections, line[len("【"):end]) {
				lines = append(lines, line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// characterTurnaroundSetting 是三视图模板的【角色设定】：设定里画面相关的字段（与前端 characterTurnaroundVariables 一致）。
func characterTurnaroundSetting(definition map[string]any) string {
	var parts []string
	for _, key := range []string{"role", "appearance", "physique", "clothing", "personality", "props", "consistencyPrompt", "multiViewPrompt"} {
		if text, _ := definition[key].(string); strings.TrimSpace(text) != "" {
			parts = append(parts, strings.TrimSpace(text))
		}
	}
	return strings.Join(parts, "；")
}

// dramaCharacterImageTaskRequest 是一个角色（或子卡）的三视图任务：与资产页「生成三视图」同一个提示词模板（character_turnaround）和 metadata，
// 收尾由 finalizeCharacterTurnaroundTask 绑到新角色版本；模板本身放进顶层 assetImageTask，出完图接着排下一个时用。
// 参考图（子卡带主卡三视图，都带主基调色卡）按 level 降级挑，说明接在【角色设定】后面。
func (s *Service) dramaCharacterImageTaskRequest(userID string, project *model.Project, style styleProfileDocument, asset model.Asset, version *model.AssetVersion, template DramaImageTaskTemplate, origin string, refs []dramaImageRef, level int) (CreateTaskRequest, error) {
	definition := map[string]any{}
	_ = json.Unmarshal([]byte(firstNonEmpty(version.DefinitionJSON, "{}")), &definition)
	setting := characterTurnaroundSetting(definition)
	if setting == "" {
		return CreateTaskRequest{}, BadAuthRequest("角色设定里没有外貌、体型或服装，出不了三视图")
	}
	input, metadata := dramaImageTaskInput(template)
	note, err := s.withDramaImageRefs(userID, input, refs, template, level)
	if err != nil {
		return CreateTaskRequest{}, err
	}
	if note != "" {
		setting += "\n" + note
	}
	maps.Copy(metadata, map[string]any{
		"operation": "character_turnaround", "promptTemplateOperation": "character_turnaround",
		"promptTemplateVariables": map[string]string{"角色名称": asset.Title, "项目画风": characterStyleConstraint(style), "角色设定": setting},
		"characterAssetId":        asset.ID, "domainProjectId": project.ID, "nodeId": "character-turnaround:" + asset.ID, "source": dramaAssetImageTaskSource,
		"resolvedCharacterVersions": []map[string]string{{"assetId": asset.ID, "versionId": version.ID}},
		"dramaAsset":                map[string]any{"origin": origin},
	})
	if style.PresetID != "" {
		metadata["stylePresetId"] = style.PresetID
	}
	input["assetImageTask"] = template
	return dramaImageTaskRequestOf(project.ID, template, "使用当前启用的角色三视图模板。", input), nil
}

// dramaVisualImageTaskRequest 是一个场景或道具的概念图任务：提示词写法与资产页「生成图片」一致（前端 projectVisualImageRequest），
// 收尾由 finalizeProjectAssetImageTask 绑到资产版本。参考图（场景带本场色卡和主基调色卡，道具带主基调色卡）按 level 降级挑。
func (s *Service) dramaVisualImageTaskRequest(userID string, project *model.Project, style styleProfileDocument, asset model.Asset, version *model.AssetVersion, template DramaImageTaskTemplate, origin string, refs []dramaImageRef, level int) (CreateTaskRequest, error) {
	definition := map[string]any{}
	_ = json.Unmarshal([]byte(firstNonEmpty(version.DefinitionJSON, "{}")), &definition)
	prompt, _ := definition["prompt"].(string)
	description, _ := definition["description"].(string)
	if strings.TrimSpace(prompt) == "" {
		return CreateTaskRequest{}, BadAuthRequest("资产缺少出图提示词")
	}
	label, tail := "道具", "完整展示单个道具主体的形状、材质与纹理，背景简洁，不出现人物，不添加无关物品。"
	if asset.Category == model.AssetCategoryEnvironment {
		label, tail = "场景", "展示场景的空间布局、建筑、材质与光照，不出现人物，便于后续分镜保持场景一致。"
	}
	input, metadata := dramaImageTaskInput(template)
	note, err := s.withDramaImageRefs(userID, input, refs, template, level)
	if err != nil {
		return CreateTaskRequest{}, err
	}
	parts := []string{label + "参考图：" + asset.Title, strings.TrimSpace(description), strings.TrimSpace(prompt), tail, note}
	if style.Prompt != "" {
		parts = append(parts, "项目画风（必须遵循）：\n"+style.Prompt)
	}
	text := strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), "\n\n")
	maps.Copy(metadata, map[string]any{
		"operation": "project_asset_image", "domainProjectId": project.ID, "projectAssetId": asset.ID, "projectAssetVersionId": version.ID,
		"nodeId": "project-asset-image:" + asset.ID, "source": dramaAssetImageTaskSource, "dramaAsset": map[string]any{"origin": origin},
	})
	if style.PresetID != "" {
		metadata["stylePresetId"] = style.PresetID
	}
	input["assetImageTask"] = template
	return dramaImageTaskRequestOf(project.ID, template, text, input), nil
}

// afterDramaAssetImageTask 在资产设计排的出图任务成功落库之后跑（inputJSON 是落库前的任务输入，带着加密的模板）：记下出图状态，
// 接着排「等空位」的资产。这里的失败只记日志，不影响任务结果。
func (s *Service) afterDramaAssetImageTask(task model.Task, inputJSON string) {
	if !strings.Contains(inputJSON, `"dramaAsset"`) {
		return
	}
	decrypted, err := s.decryptTaskInputJSON(inputJSON)
	if err != nil {
		_ = s.log(task.UserID, task.ID, "warn", "资产出图收尾读不出任务输入", err.Error())
		return
	}
	var input struct {
		Metadata struct {
			Operation      string `json:"operation"`
			CharacterID    string `json:"characterAssetId"`
			ProjectAssetID string `json:"projectAssetId"`
			ProjectID      string `json:"domainProjectId"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal([]byte(decrypted), &input)
	assetID := firstNonEmpty(input.Metadata.CharacterID, input.Metadata.ProjectAssetID)
	projectID := firstNonEmpty(task.ProjectID, input.Metadata.ProjectID)
	if assetID == "" || projectID == "" {
		return
	}
	state := dramaImageState{Status: dramaImageBound, TaskID: task.ID, UpdatedAt: time.Now().Format(time.RFC3339Nano)}
	var result struct {
		Status string `json:"projectAssetImageStatus"`
	}
	_ = json.Unmarshal([]byte(task.ResultJSON), &result)
	if input.Metadata.Operation == "project_asset_image" && result.Status != "bound" {
		state.Status, state.Error = dramaImageStale, "出图期间资产换了版本，图留在任务结果里，没有绑到当前版本"
	}
	if err := s.putDramaImageState(task.UserID, assetID, assetImageKey, state); err != nil {
		_ = s.log(task.UserID, task.ID, "warn", "资产出图状态没有写上", err.Error())
	}
	s.continueDramaImages(task, projectID, decrypted)
}

// ProjectAssetImageSummary 是资产设计建的资产在资产清单里的出图状态（none / waiting / queued / bound / stale / failed）。
type ProjectAssetImageSummary struct {
	Status string `json:"status"`
	TaskID string `json:"taskId,omitempty"`
	Error  string `json:"error,omitempty"`
}

// assetImageSummary 是资产设计建的资产的出图状态；不是资产设计建的返回 nil。
func (s *Service) assetImageSummary(userID string, asset *model.Asset, imaged bool) *ProjectAssetImageSummary {
	if _, ok := payloadMetadata(assetPayload(asset))[assetImageKey]; !ok {
		return nil
	}
	state := s.dramaImageView(userID, asset, assetImageKey, imaged)
	if imaged {
		state.Status, state.Error = dramaImageBound, ""
	}
	return &ProjectAssetImageSummary{Status: state.Status, TaskID: state.TaskID, Error: state.Error}
}

// projectAssetsProgress 是「参考齐了」说明用的资产概况：角色、场景、道具一共几个，几个还没出图。
func projectAssetsProgress(assets []ProjectAssetSummary) drama.AssetsInput {
	var in drama.AssetsInput
	for _, a := range assets {
		if !slices.Contains(dramaAssetCategories, a.Category) || string(a.Status) == string(model.AssetVersionStatusArchived) {
			continue
		}
		in.Total++
		imaged := a.MediaType == "image"
		if a.Category == model.AssetCategoryCharacter {
			imaged = a.Character != nil && a.Character.VisualStatus == "ready"
		}
		if !imaged {
			in.Unimaged++
		}
	}
	return in
}
