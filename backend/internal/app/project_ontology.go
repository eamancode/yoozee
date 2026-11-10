// 本体论导演分镜（流水线一的分镜环节）：技能 yz-ontology-director 分环节分轮生成。
// 一次运行 = 一个任务：立项（项目还没有世界圣经卡时）→ 规划（S0/S2/S3）→ 讲戏（S4/S5）→ 每个单元一轮（S6/S7）→ 质检（S8 + S1 交接卡）。
// 每一轮只装这一轮的环节文件（不截断），前面各轮的产物按需作为输入；轮与轮之间不带对话历史。
// 收尾把单元卡写成段（粘贴提示词原文存 spec.verbatim），七部分、交接卡、圣经卡写进 project_ontology_documents。

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// ontologyMaxMaterials 是材料包素材清单最多列几项（一段实际挂的图另受视频模型上限约束）。
const ontologyMaxMaterials = 60

// dramaOntologyMeta 是任务 metadata.dramaOntology：建任务时算好的材料，运行时按它组每一轮的提示词。
type dramaOntologyMeta struct {
	Brief     drama.OntologyBrief      `json:"brief"`
	Materials []drama.OntologyMaterial `json:"materials"`
	Bible     string                   `json:"bible,omitempty"`
	Handoff   string                   `json:"handoff,omitempty"`
	UnitTitle string                   `json:"unitTitle"`
	Script    string                   `json:"script"`
	// Setup 表示项目还没有世界圣经卡，这次运行先跑立项轮。
	Setup     bool `json:"setup,omitempty"`
	MaxImages int  `json:"maxImages,omitempty"`
	// EstimatedTurns 是建任务时估的轮数（预授权、墙钟用）。
	EstimatedTurns int                 `json:"estimatedTurns"`
	Repair         *ontologyRepairMeta `json:"repair,omitempty"`
	// Discard 表示建任务时用户确认过整集替换会丢掉已有产物；Confirmed 是确认时各段（镜头 ID）已有产物的数量。
	// 收尾只允许删掉确认过、且产物没有增加的段。
	Discard   bool           `json:"discard,omitempty"`
	Confirmed map[string]int `json:"confirmed,omitempty"`
}

// ontologyMaxPromptRunes 是一轮提示词（技能上下文 + 材料包 + 剧本 + 前序产物）的上限；超过直接失败，不截断、不硬发给模型。
const ontologyMaxPromptRunes = 150000

// ontologyFingerprint 是一次运行的输入指纹：剧本、素材清单、圣经卡、交接卡、画风与时长约束。续跑时输入变了就不能接着旧轮次写。
func ontologyFingerprint(meta *dramaOntologyMeta) string {
	type material struct {
		Index           int
		Name, VersionID string
	}
	mats := make([]material, 0, len(meta.Materials))
	for _, m := range meta.Materials {
		mats = append(mats, material{m.Index, m.Name, m.VersionID})
	}
	encoded, _ := json.Marshal(map[string]any{"brief": meta.Brief, "materials": mats, "bible": meta.Bible, "handoff": meta.Handoff, "script": meta.Script, "unit": meta.UnitTitle, "maxImages": meta.MaxImages})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// ontologyRepairMeta 是返修回合：只重写一个段。
type ontologyRepairMeta struct {
	ShotID    string `json:"shotId"`
	SegmentID string `json:"segmentId"`
	Unit      string `json:"unit"`
	Symptom   string `json:"symptom"`
	Card      string `json:"card"`
	// OtherSeconds 是这一集其他单元的总时长合计（返修后的单元按每集上限扣掉它们之后的余量校验）。
	OtherSeconds float64 `json:"otherSeconds,omitempty"`
}

// ontologyOutputs 是任务 result.ontology：各轮的原样输出，收尾解析写库。
type ontologyOutputs struct {
	Setup  string   `json:"setup,omitempty"`
	Plan   string   `json:"plan,omitempty"`
	Direct string   `json:"direct,omitempty"`
	Units  []string `json:"units,omitempty"`
	Check  string   `json:"check,omitempty"`
	Repair string   `json:"repair,omitempty"`
}

func taskDramaOntology(inputJSON string) *dramaOntologyMeta {
	if !strings.Contains(inputJSON, `"dramaOntology"`) {
		return nil
	}
	var input struct {
		Metadata struct {
			DramaOntology *dramaOntologyMeta `json:"dramaOntology"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil {
		return nil
	}
	return input.Metadata.DramaOntology
}

func metadataDramaOntology(metadata map[string]any) *dramaOntologyMeta {
	raw, ok := metadata["dramaOntology"]
	if !ok {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var meta dramaOntologyMeta
	if json.Unmarshal(encoded, &meta) != nil {
		return nil
	}
	return &meta
}

// ontologyStepSkill 是流水线一分镜环节的技能（不走风格档案绑定表：流水线定了就是本体论导演）。
func (s *Service) ontologyStepSkill(project *model.Project, workflow drama.AssetWorkflow, step drama.Step) (DramaStepUsage, drama.StepSkill, error) {
	pkg, err := s.skillDomain().StepSkillPackage(workflow.StoryboardSkill)
	if err != nil {
		return DramaStepUsage{}, drama.StepSkill{}, err
	}
	skill := stepSkillOf(pkg)
	usage := DramaStepUsage{
		Workflow: workflow, Step: step.ID, StepLabel: step.Label, Contract: drama.ContractOntologyDirector,
		Profile: dramaProfileRef(projectDramaContext(project).profileID()),
		Skill:   DramaStepSkillRef{ID: skill.ID, Name: skill.Name, VersionID: skill.VersionID, Version: skill.Version, ContentHash: skill.ContentHash},
	}
	return usage, skill, nil
}

// planOntologyShots 组一次本体论分镜运行：材料包（素材清单按项目有图的资产编号）、圣经卡、上一集交接卡、本集剧本，以及第一轮的提示词。
func (s *Service) planOntologyShots(userID string, project *model.Project, unit *model.ProjectUnit, usage DramaStepUsage, skill drama.StepSkill, rule *DramaShotlistRule, repair *ontologyRepairMeta) (*dramaStepPlan, error) {
	m, err := dramaShotlistModel(project, briefSegmentRule(project.BriefJSON, rule))
	if err != nil {
		return nil, err
	}
	maxSeconds := min(drama.OntologyUnitMaxSeconds, m.Duration.Max)
	if maxSeconds < 5 {
		return nil, BadAuthRequest(fmt.Sprintf("视频模型 %s 一次最多 %d 秒，太短，本体论导演分镜至少需要 5 秒一个单元", m.Label, maxSeconds))
	}
	var materials []drama.OntologyMaterial
	if repair != nil {
		// 返修的原单元卡里 @图N 是按生成那一集时的素材清单编的号：用那一集存下的清单，不用现在重新编号的。
		materials, err = s.ontologyEpisodeMaterials(project.ID, unit.ID)
	} else {
		materials, err = s.ontologyMaterials(userID, project.ID)
	}
	if err != nil {
		return nil, err
	}
	meta := &dramaOntologyMeta{
		Brief:     ontologyProjectBrief(project, maxSeconds),
		Materials: materials, UnitTitle: unit.Title, Script: chapterPlainText(unit.SourceText), Repair: repair,
	}
	if m.Refs.Images != nil {
		meta.MaxImages = *m.Refs.Images
	}
	if bible, err := s.repo.LatestOntologyDocument(project.ID, "", model.OntologyDocBible); err == nil {
		meta.Bible = bible.Content
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	meta.Setup = strings.TrimSpace(meta.Bible) == "" && repair == nil
	if meta.Handoff, err = s.previousOntologyHandoff(project.ID, unit); err != nil {
		return nil, err
	}
	kind := drama.OntologyTurnPlan
	switch {
	case repair != nil:
		kind = drama.OntologyTurnRepair
		meta.EstimatedTurns = 1
	case meta.Setup:
		kind = drama.OntologyTurnProject
	}
	if repair == nil {
		meta.EstimatedTurns = ontologyEstimatedTurns(meta, maxSeconds)
	}
	skillCtx, uses, err := drama.OntologySkillContext(skill, kind)
	if err != nil {
		return nil, err
	}
	var priors []ontologyPrior
	unitID := ""
	if repair != nil {
		unitID = repair.Unit
		priors = []ontologyPrior{{"原单元卡", repair.Card}, {"翻车症状", repair.Symptom}}
	}
	prompt := ontologyTurnPrompt(skillCtx, meta, kind, unitID, priors)
	records := make([]map[string]any, 0, len(materials))
	for _, mat := range materials {
		records = append(records, map[string]any{"index": mat.Index, "name": mat.Name, "category": mat.Category, "versionId": mat.VersionID})
	}
	return &dramaStepPlan{usage: usage, prompt: drama.StepPrompt{Text: prompt, Files: uses}, skill: skill, ontology: meta, record: map[string]any{"canvasAssets": records}}, nil
}

// ontologyEstimatedTurns 估轮数：按每集时长和单元上限估单元数，再加规划、讲戏、质检（和立项）。
func ontologyEstimatedTurns(meta *dramaOntologyMeta, maxSeconds int) int {
	seconds := meta.Brief.EpisodeSeconds
	if seconds <= 0 {
		seconds = 120
	}
	units := int(math.Ceil(float64(seconds) / math.Max(1, float64(maxSeconds)*0.8)))
	turns := 3 + max(2, units)
	if meta.Setup {
		turns++
	}
	return turns
}

func ontologyProjectBrief(project *model.Project, maxSeconds int) drama.OntologyBrief {
	medium := map[string]string{"live_action": "写实", "cg_real": "写实（三维写实）", "anime_3d": "3D国漫", "anime_2d": "2D动漫"}[strings.TrimSpace(project.VisualType)]
	doc := map[string]any{}
	if raw := strings.TrimSpace(project.BriefJSON); raw != "" {
		_ = json.Unmarshal([]byte(raw), &doc)
	}
	genre, _ := doc["genre"].(string)
	anchor, palette, _, locked := projectLockedStyle(project.BibleJSON)
	if !locked {
		anchor, palette = nil, drama.MasterPalette{}
	}
	style := drama.OntologyStyleText(projectStyleSnapshot(project).Prompt, anchor, palette)
	return drama.OntologyBrief{Genre: genre, AspectRatio: project.AspectRatio, Medium: medium, EpisodeSeconds: int(briefEpisodeSeconds(project.BriefJSON)), MaxUnitSeconds: maxSeconds, Style: style}
}

// ontologyEpisodeMaterials 是这一集生成时存下的素材清单（返修用）。
func (s *Service) ontologyEpisodeMaterials(projectID string, unitID string) ([]drama.OntologyMaterial, error) {
	doc, err := s.repo.LatestOntologyDocument(projectID, unitID, model.OntologyDocMaterials)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, BadAuthRequest("这一集没有存下生成时的素材清单，请重新生成本集分镜后再返修")
	}
	if err != nil {
		return nil, err
	}
	var materials []drama.OntologyMaterial
	if err := json.Unmarshal([]byte(doc.Content), &materials); err != nil {
		return nil, fmt.Errorf("这一集的素材清单无效：%w", err)
	}
	return materials, nil
}

// prepareOntologyRun 补齐本体论运行的建任务参数：整集替换的确认范围；续跑时恢复上一次任务不可变的输入并按实际进度定轮数。
// 续跑要求技能版本和输入指纹都没变（剧本、素材、圣经卡、交接卡、画风改过就不能拿旧轮次接着写），轮数取上一次已知的总轮数，
// 只有各轮都写完时才算「只收尾」（不调模型、不建计费单）。
func (s *Service) prepareOntologyRun(userID string, projectID string, unitID string, plan *dramaStepPlan, run *dramaRunMeta, discard bool) error {
	meta := plan.ontology
	if discard {
		discards, err := s.dramaSegmentDiscards(projectID, unitID, nil)
		if err != nil {
			return err
		}
		meta.Discard, meta.Confirmed = true, map[string]int{}
		for _, d := range discards {
			meta.Confirmed[d.ShotID] = ontologyArtifactCount(d)
		}
	}
	if run.ResumeTaskID == "" {
		return nil
	}
	previous, err := s.repo.TaskForUser(userID, run.ResumeTaskID)
	if err != nil {
		return BadAuthRequest("要继续的分镜任务不存在")
	}
	prevMeta, prevStep, progress := taskDramaOntology(previous.InputJSON), taskDramaStep(previous.InputJSON), taskDramaRunProgress(previous.ResultJSON)
	switch {
	case prevMeta == nil || prevStep == nil || progress == nil:
		return BadAuthRequest("要继续的任务不是本体论导演分镜任务")
	case prevStep.VersionID != plan.skill.VersionID:
		return BadAuthRequest("本体论导演技能已更新，不能混用旧轮次，请重新生成本集分镜")
	case ontologyFingerprint(prevMeta) != ontologyFingerprint(meta):
		return BadAuthRequest("上一次任务之后剧本、素材、世界圣经卡、上一集交接卡或画风改过了，不能接着旧轮次写，请重新生成本集分镜")
	}
	prevMeta.Discard, prevMeta.Confirmed = meta.Discard, meta.Confirmed
	plan.ontology = prevMeta
	completed := min(run.ResumeFrom-1, len(progress.Turns))
	out, units := ontologyStateFrom(progress.Turns[:completed])
	if _, done := ontologyNextTurn(prevMeta, out, units); done {
		run.Turns = completed // 各轮都写完了：只重新收尾
		return nil
	}
	total := max(prevMeta.EstimatedTurns, progress.Total)
	if units != nil {
		total = ontologyTotalTurns(prevMeta, len(units))
	}
	run.Turns = max(total, completed+1)
	return nil
}

func ontologyArtifactCount(d DramaSegmentDiscard) int {
	n := 0
	for _, c := range d.Artifacts {
		n += c
	}
	return n
}

// ontologyMaterials 是素材清单：有图的人物（主卡在前、子卡跟在后）、场景、道具，各自用主版本当参考文件。没图的资产不进清单。
func (s *Service) ontologyMaterials(userID string, projectID string) ([]drama.OntologyMaterial, error) {
	assets, err := s.ProjectAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(assets))
	for _, a := range assets {
		if a.PrimaryVersionID != "" {
			versions = append(versions, a.PrimaryVersionID)
		}
	}
	imaged := map[string]bool{}
	if len(versions) > 0 {
		reps, err := s.repo.AssetRepresentationsByVersionIDs(versions)
		if err != nil {
			return nil, err
		}
		for _, r := range reps {
			if r.ResourceID != "" && strings.HasPrefix(r.MediaType, "image") {
				imaged[r.AssetVersionID] = true
			}
		}
	}
	rank := map[string]int{"character": 0, "environment": 1, "prop": 2}
	var picked []ProjectAssetSummary
	for _, a := range assets {
		category := string(a.Category)
		if _, ok := rank[category]; !ok || string(a.Status) == "archived" || !dramaAssetHasImage(a, imaged[a.PrimaryVersionID]) {
			continue
		}
		picked = append(picked, a)
	}
	sort.SliceStable(picked, func(i, j int) bool {
		ri, rj := rank[string(picked[i].Category)], rank[string(picked[j].Category)]
		if ri != rj {
			return ri < rj
		}
		return picked[i].Variant == nil && picked[j].Variant != nil
	})
	out := make([]drama.OntologyMaterial, 0, len(picked))
	for i, a := range picked {
		if i >= ontologyMaxMaterials {
			break
		}
		note := assetCategoryLabel(string(a.Category))
		if a.Variant != nil {
			note = fmt.Sprintf("（%s的子卡：%s）", firstNonEmpty(a.Variant.ParentTitle, "主卡"), a.Variant.Label)
		}
		out = append(out, drama.OntologyMaterial{Index: i + 1, Name: a.Title, Category: string(a.Category), Note: note, VersionID: a.PrimaryVersionID})
	}
	return out, nil
}

// previousOntologyHandoff 是同一项目里排在本集前面的那一集的最新交接卡；第一集或上一集还没生成时为空。
func (s *Service) previousOntologyHandoff(projectID string, unit *model.ProjectUnit) (string, error) {
	units, err := s.repo.ProjectUnits(projectID)
	if err != nil {
		return "", err
	}
	sort.SliceStable(units, func(i, j int) bool { return units[i].Position < units[j].Position })
	var previous *model.ProjectUnit
	for i := range units {
		if units[i].ID == unit.ID {
			break
		}
		if units[i].Kind == unit.Kind {
			previous = &units[i]
		}
	}
	if previous == nil {
		return "", nil
	}
	doc, err := s.repo.LatestOntologyDocument(projectID, previous.ID, model.OntologyDocHandoff)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return doc.Content, nil
}

type ontologyPrior struct{ Title, Text string }

// ontologyTurnPrompt 是一轮发给模型的完整提示词：技能上下文、材料包、本集剧本、前序产物、本轮任务。
func ontologyTurnPrompt(skillContext string, meta *dramaOntologyMeta, kind string, unit string, priors []ontologyPrior) string {
	parts := []string{skillContext, drama.OntologyMaterialPackage(meta.Brief, meta.Materials, meta.Bible, meta.Handoff)}
	if kind != drama.OntologyTurnRepair {
		parts = append(parts, fmt.Sprintf("【本集剧本】%s\n%s", meta.UnitTitle, strings.TrimSpace(meta.Script)))
	}
	for _, p := range priors {
		if strings.TrimSpace(p.Text) != "" {
			parts = append(parts, fmt.Sprintf("【前序产物：%s】\n%s", p.Title, strings.TrimSpace(p.Text)))
		}
	}
	parts = append(parts, drama.OntologyTurnInstruction(kind, unit))
	return strings.Join(parts, "\n\n")
}

// ---------------------------------------------------------------- 运行

type ontologyTurnSpec struct {
	kind, unit, label string
}

// ontologyNextTurn 按已写完的各轮决定下一轮；done 为 true 表示都写完了。
func ontologyNextTurn(meta *dramaOntologyMeta, out ontologyOutputs, units []string) (ontologyTurnSpec, bool) {
	switch {
	case meta.Repair != nil:
		if out.Repair == "" {
			return ontologyTurnSpec{drama.OntologyTurnRepair, meta.Repair.Unit, "返修 " + meta.Repair.Unit}, false
		}
		return ontologyTurnSpec{}, true
	case meta.Setup && out.Setup == "":
		return ontologyTurnSpec{drama.OntologyTurnProject, "", "立项：世界圣经卡"}, false
	case out.Plan == "":
		return ontologyTurnSpec{drama.OntologyTurnPlan, "", "本集定位、台词、节奏"}, false
	case out.Direct == "":
		return ontologyTurnSpec{drama.OntologyTurnDirect, "", "讲戏本与氛围"}, false
	case len(out.Units) < len(units):
		u := units[len(out.Units)]
		return ontologyTurnSpec{drama.OntologyTurnUnit, u, "单元卡 " + u}, false
	case out.Check == "":
		return ontologyTurnSpec{drama.OntologyTurnCheck, "", "交接卡与自检"}, false
	}
	return ontologyTurnSpec{}, true
}

// ontologyTurnPriors 是这一轮要带的前序产物。单元轮带规划、讲戏和上一张单元卡（接缝、结束状态）。
func ontologyTurnPriors(kind string, out ontologyOutputs) []ontologyPrior {
	switch kind {
	case drama.OntologyTurnDirect:
		return []ontologyPrior{{"本集定位、台词对照表、节奏总表", out.Plan}}
	case drama.OntologyTurnUnit:
		priors := []ontologyPrior{{"本集定位、台词对照表、节奏总表", out.Plan}, {"讲戏本与氛围设计", out.Direct}}
		if n := len(out.Units); n > 0 {
			priors = append(priors, ontologyPrior{"上一张单元卡", out.Units[n-1]})
		}
		return priors
	case drama.OntologyTurnCheck:
		return []ontologyPrior{{"本集定位、台词对照表、节奏总表", out.Plan}, {"全部单元卡", strings.Join(out.Units, "\n\n")}}
	}
	return nil
}

// ontologyLimits 是单元卡的校验参数；used 是本集其他单元已占的秒数（按每集合计上限扣）。
func ontologyLimits(meta *dramaOntologyMeta, used float64) drama.OntologyUnitLimits {
	return drama.OntologyUnitLimits{MaxSeconds: meta.Brief.MaxUnitSeconds, Materials: len(meta.Materials), MaxImages: meta.MaxImages,
		CapSeconds: drama.OntologyEpisodeCap(meta.Brief.EpisodeSeconds), UsedSeconds: used}
}

// ontologyCardsSeconds 是已写好的单元卡的总时长合计（每张在自己那一轮已经校验过，解析不出的不计）。
func ontologyCardsSeconds(texts []string) float64 {
	var total float64
	for _, text := range texts {
		cards, err := drama.ParseOntologyUnitCards(text)
		if err != nil {
			continue
		}
		for _, c := range cards {
			total += c.TotalSeconds
		}
	}
	return total
}

// ontologyTurnUsedSeconds 是这一轮单元之外本集已占的秒数：单元轮是前面写好的单元，返修轮是这一集其他单元。
func ontologyTurnUsedSeconds(kind string, meta *dramaOntologyMeta, out ontologyOutputs) float64 {
	switch {
	case kind == drama.OntologyTurnRepair && meta.Repair != nil:
		return meta.Repair.OtherSeconds
	case kind == drama.OntologyTurnUnit:
		return ontologyCardsSeconds(out.Units)
	}
	return 0
}

// ontologyTurnCheck 是这一轮输出的校验（不合格按临时失败重试，错误文字会回给模型）。
func ontologyTurnCheck(kind string, unit string, meta *dramaOntologyMeta, units []string, used float64) func(string, drama.TurnContext) (int, error) {
	limits := ontologyLimits(meta, used)
	return func(text string, _ drama.TurnContext) (int, error) {
		if drama.OntologyUnfinished(text) {
			return 0, errors.New("输出里出现了未完续写标记：本轮必须一次写完，不使用「回复继续」")
		}
		switch kind {
		case drama.OntologyTurnProject:
			return 1, drama.OntologyRequireSections(text, "世界圣经卡", "待确认清单")
		case drama.OntologyTurnPlan:
			if err := drama.OntologyRequireSections(text, "一、本集定位", "二、台词对照表", "三、节奏总表"); err != nil {
				return 0, err
			}
			units, err := drama.OntologyRhythmUnits(text)
			return len(units), err
		case drama.OntologyTurnDirect:
			return 1, drama.OntologyRequireSections(text, "四、讲戏本")
		case drama.OntologyTurnUnit:
			_, err := ontologySingleCard(text, unit, limits)
			return 1, err
		case drama.OntologyTurnCheck:
			return 1, drama.ValidateOntologyCheck(text, units)
		case drama.OntologyTurnRepair:
			if err := drama.OntologyRequireSections(text, "诊断", "改法", "重写后的单元卡"); err != nil {
				return 0, err
			}
			body, _ := drama.OntologySection(text, "重写后的单元卡")
			_, err := ontologySingleCard(body, unit, limits)
			return 1, err
		}
		return 0, fmt.Errorf("不认识的轮次 %s", kind)
	}
}

// ontologySingleCard 要求文本里恰好有这一个单元的一张卡，并通过单元卡校验。
func ontologySingleCard(text string, unit string, limits drama.OntologyUnitLimits) (drama.OntologyUnitCard, error) {
	cards, err := drama.ParseOntologyUnitCards(text)
	if err != nil {
		return drama.OntologyUnitCard{}, err
	}
	if len(cards) != 1 {
		return drama.OntologyUnitCard{}, fmt.Errorf("本轮只写单元 %s 的一张单元卡，输出里有 %d 张", unit, len(cards))
	}
	return cards[0], drama.ValidateOntologyUnit(cards[0], unit, limits)
}

// ontologyStateFrom 从已写完的轮次恢复各轮产物和单元表（续写、换 worker 时）。
func ontologyStateFrom(turns []dramaRunTurn) (ontologyOutputs, []string) {
	var out ontologyOutputs
	var units []string
	for _, t := range turns {
		switch t.Ontology {
		case drama.OntologyTurnProject:
			out.Setup = t.Text
		case drama.OntologyTurnPlan:
			out.Plan = t.Text
			units, _ = drama.OntologyRhythmUnits(t.Text)
		case drama.OntologyTurnDirect:
			out.Direct = t.Text
		case drama.OntologyTurnUnit:
			out.Units = append(out.Units, t.Text)
		case drama.OntologyTurnCheck:
			out.Check = t.Text
		case drama.OntologyTurnRepair:
			out.Repair = t.Text
		}
	}
	return out, units
}

// runOntologyRun 按轮循环调模型。轮次在运行中才定（规划轮写出节奏总表后才知道有几个单元）。
func (s *Service) runOntologyRun(ctx context.Context, input canvasGenerationInput, run dramaRunMeta, meta *dramaOntologyMeta) (map[string]any, error) {
	task, ok := ctx.Value(mediaExecutionTaskKey{}).(model.Task)
	if !ok || task.ID == "" {
		return nil, errors.New("本体论分镜缺少任务上下文")
	}
	step := taskDramaStep(task.InputJSON)
	if step == nil || step.SkillID == "" || step.VersionID == "" {
		return nil, errors.New("本体论分镜任务缺少技能版本")
	}
	pkg, err := s.skillDomain().StepSkillPackageVersion(step.SkillID, step.VersionID)
	if err != nil {
		return nil, err
	}
	skill := stepSkillOf(pkg)
	policy, err := s.RuntimePolicy()
	if err != nil {
		return nil, err
	}
	turnTimeout := storyboardTurnTimeout(policy.Task)
	turns, err := s.dramaRunStartTurns(task, run)
	if err != nil {
		return nil, err
	}
	out, units := ontologyStateFrom(turns)
	if out.Setup != "" {
		if bible, ok := drama.OntologySection(out.Setup, "世界圣经卡"); ok {
			meta.Bible = bible
		}
	}
	progress := dramaRunProgress{RunID: run.RunID, Total: max(run.Turns, len(turns)), Completed: len(turns), Turns: turns}
	stream := newDramaRunStream(s, task, input.OnTextDelta)
	defer stream.close()
	turnInput := input
	turnInput.OnTextDelta = stream.publish
	turnInput.TextHistory = nil
	for k := len(turns) + 1; ; k++ {
		spec, done := ontologyNextTurn(meta, out, units)
		if done {
			break
		}
		if latest, err := s.repo.Task(task.ID); err == nil && latest.Status == model.TaskStatusCancelled {
			return nil, context.Canceled
		}
		skillCtx, _, err := drama.OntologySkillContext(skill, spec.kind)
		if err != nil {
			return nil, err
		}
		priors := ontologyTurnPriors(spec.kind, out)
		if spec.kind == drama.OntologyTurnRepair {
			priors = []ontologyPrior{{"原单元卡", meta.Repair.Card}, {"翻车症状", meta.Repair.Symptom}}
		}
		turnInput.Prompt = ontologyTurnPrompt(skillCtx, meta, spec.kind, spec.unit, priors)
		used := ontologyTurnUsedSeconds(spec.kind, meta, out)
		if spec.kind == drama.OntologyTurnUnit || spec.kind == drama.OntologyTurnRepair {
			if line := drama.OntologyBudgetLine(ontologyLimits(meta, used)); line != "" {
				turnInput.Prompt += "\n\n" + line
			}
		}
		if n := len([]rune(turnInput.Prompt)); n > ontologyMaxPromptRunes {
			return nil, dramaSentenceError{fmt.Errorf("第 %d 轮（%s）的输入有 %d 字，超过上限 %d 字：本集剧本或单元太多，请把本集拆短后重新生成", k, spec.label, n, ontologyMaxPromptRunes)}
		}
		if units != nil {
			progress.Total = ontologyTotalTurns(meta, len(units))
		}
		marker := dramaTurnMarker{Index: k, Total: max(progress.Total, k), Scene: spec.unit, Heading: spec.label}
		plan := drama.TurnPlan{Unit: "轮", Count: ontologyTurnCheck(spec.kind, spec.unit, meta, units, used)}
		turn, err := s.runDramaTurn(ctx, task, turnInput, marker, stream, turnTimeout, plan, drama.TurnContext{Index: k, Contract: drama.ContractOntologyDirector})
		if err != nil {
			return nil, dramaRunTurnError("轮", marker, progress.Completed, turnTimeout, err)
		}
		// 提示词由技能版本和材料重新组出，不存全文（进度里每轮都存会让任务结果过大）；续写只用 Text。
		turn.Prompt = "ontology " + spec.kind + " " + spec.unit
		turn.TaskID, turn.Ontology, turn.Scene, turn.Heading = task.ID, spec.kind, spec.unit, spec.label
		progress.Turns = append(progress.Turns, turn)
		progress.Completed = k
		out, units = ontologyStateFrom(progress.Turns)
		if spec.kind == drama.OntologyTurnProject {
			if bible, ok := drama.OntologySection(turn.Text, "世界圣经卡"); ok {
				meta.Bible = bible
			}
		}
		if units != nil {
			progress.Total = ontologyTotalTurns(meta, len(units))
		}
		encoded, err := json.Marshal(map[string]any{"mode": "text", "dramaRun": progress})
		if err != nil {
			return nil, err
		}
		if err := s.repo.UpdateTaskResultForLease(task.ID, task.LeaseOwner, fmt.Sprintf("已写完第 %d/%d 轮：%s", k, max(progress.Total, k), spec.label), 35+60*k/max(progress.Total, k), string(encoded)); err != nil {
			return nil, fmt.Errorf("保存第 %d 轮进度失败：%w", k, err)
		}
	}
	texts := make([]string, 0, len(progress.Turns))
	for i := range progress.Turns {
		texts = append(texts, strings.TrimSpace(progress.Turns[i].Text))
	}
	return map[string]any{"mode": "text", "text": strings.Join(texts, "\n\n") + "\n", "dramaRun": progress}, nil
}

func ontologyTotalTurns(meta *dramaOntologyMeta, units int) int {
	if meta.Repair != nil {
		return 1
	}
	total := 3 + units
	if meta.Setup {
		total++
	}
	return total
}

// ---------------------------------------------------------------- 收尾

// ontologyResultOutputs 从任务结果的逐轮进度恢复各轮产物（结果里不再另存一份全文）。
func ontologyResultOutputs(result map[string]interface{}) (ontologyOutputs, []string, error) {
	encoded, err := json.Marshal(result["dramaRun"])
	if err != nil {
		return ontologyOutputs{}, nil, err
	}
	var progress dramaRunProgress
	if err := json.Unmarshal(encoded, &progress); err != nil || len(progress.Turns) == 0 {
		return ontologyOutputs{}, nil, errors.New("本体论分镜任务结果缺少逐轮输出")
	}
	out, units := ontologyStateFrom(progress.Turns)
	return out, units, nil
}

// ontologyDocWrite 是收尾要写的一份文档。
type ontologyDocWrite struct {
	unitID, kind, content string
}

// applyOntologyShotsTask 是本体论分镜的收尾。先把全部单元卡解析、校验、装成段并按视频模型检查，再复核要删的旧段（运行期间新增的产物
// 不会被删），然后写段，最后在一个事务里写圣经卡、立项清单、七部分、交接卡和素材清单。文档按任务幂等：重新收尾不重复加版本。
// 段写成而文档事务失败时任务失败，用「从中断处继续」只重新收尾（不调模型）即可补齐。
// 自检有硬失败时照常写段（便于查看和返修），Notes 写明哪些单元哪一项失败，流水线不接下一集。
func (s *Service) applyOntologyShotsTask(task model.Task, step *dramaStepTaskMeta, meta *dramaOntologyMeta, result map[string]interface{}) (DramaStepTaskOutcome, error) {
	target, err := taskDramaStepTarget(task)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	out, units, err := ontologyResultOutputs(result)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	outcome := DramaStepTaskOutcome{Step: drama.StepChapterShots, Status: DramaStepOutcomeWritten, UnitID: target.UnitID}
	project, err := s.activeProjectForUser(task.UserID, target.ProjectID)
	if err != nil {
		return DramaStepTaskOutcome{}, ontologyProjectError(err)
	}
	m, err := projectDramaModel(project)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if meta.Repair != nil {
		return s.applyOntologyRepair(task, target, meta, out, m, outcome)
	}
	var docs []ontologyDocWrite
	if meta.Setup {
		bible, ok := drama.OntologySection(out.Setup, "世界圣经卡")
		if !ok || strings.TrimSpace(bible) == "" {
			return DramaStepTaskOutcome{}, errors.New("立项轮没有写出世界圣经卡")
		}
		docs = append(docs, ontologyDocWrite{"", model.OntologyDocBible, bible}, ontologyDocWrite{"", model.OntologyDocSetup, ontologySetupNotes(out.Setup)})
	}
	if units == nil {
		return DramaStepTaskOutcome{}, errors.New("规划轮没有可用的节奏总表")
	}
	if len(out.Units) != len(units) || out.Check == "" {
		return DramaStepTaskOutcome{}, fmt.Errorf("节奏总表有 %d 个单元，只写出了 %d 张单元卡或缺少自检", len(units), len(out.Units))
	}
	if err := drama.ValidateOntologyCheck(out.Check, units); err != nil {
		return DramaStepTaskOutcome{}, err
	}
	cards := make([]drama.OntologyUnitCard, 0, len(units))
	segments := make([]json.RawMessage, 0, len(units))
	order := make([]string, 0, len(units))
	var used float64
	for i, text := range out.Units {
		// 逐张按「前面单元已用的秒数」校验每集合计上限，和生成时的单元轮一致。
		card, err := ontologySingleCard(text, units[i], ontologyLimits(meta, used))
		if err != nil {
			return DramaStepTaskOutcome{}, err
		}
		used += card.TotalSeconds
		seg, err := drama.OntologySegment(card, i+1, meta.Materials)
		if err != nil {
			return DramaStepTaskOutcome{}, err
		}
		if issues := drama.Issues(seg, m); drama.HasError(issues) {
			return DramaStepTaskOutcome{}, BadAuthRequest(fmt.Sprintf("单元 %s 装不成段：%s", card.ID, issues[0].Message))
		}
		raw, err := json.Marshal(seg)
		if err != nil {
			return DramaStepTaskOutcome{}, err
		}
		cards, segments, order = append(cards, card), append(segments, raw), append(order, seg.ID)
		outcome.Shots += len(seg.Shots)
	}
	materials, err := json.Marshal(meta.Materials)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	docs = append(docs, ontologyDocWrite{target.UnitID, model.OntologyDocEpisode, drama.OntologyEpisodeDocument(out.Plan, out.Direct, cards, out.Check)}, ontologyDocWrite{target.UnitID, model.OntologyDocMaterials, string(materials)})
	if handoff, ok := drama.OntologySection(out.Check, "六、本集交接卡"); ok && strings.TrimSpace(handoff) != "" {
		docs = append(docs, ontologyDocWrite{target.UnitID, model.OntologyDocHandoff, handoff})
	}
	docs = append(docs, ontologyDocWrite{target.UnitID, model.OntologyDocGate, ontologyGateContent(drama.OntologyHardFailures(out.Check))})
	// 同一集的整集替换、返修和手工写段串行做：要删的旧段在锁内读（锁外读的快照会漏掉别的收尾刚写的段），
	// 返修按写入时的其他段核对每集合计时长。
	unlock := lockOntologyEpisode(target.UnitID)
	defer unlock()
	removed, err := s.ontologyRemovedShots(task.UserID, target.ProjectID, target.UnitID, order)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	// 先删不再需要的旧段（事务内逐段复核产物后立即删除），再写段、按新顺序排；删除被拦下时什么都不写。
	if err := s.deleteOntologyRemovedShots(target.ProjectID, target.UnitID, removed, meta); err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if _, err := s.writeDramaSegments(task.UserID, target.ProjectID, DramaSegmentsWriteRequest{UnitID: target.UnitID, Segments: segments}); err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if err := s.reorderOntologySegments(task.UserID, target.ProjectID, target.UnitID, order); err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if err := s.writeOntologyDocs(task, target.ProjectID, docs); err != nil {
		return DramaStepTaskOutcome{}, fmt.Errorf("段已写入，文档写入失败（可从中断处继续，只重新收尾）：%w", err)
	}
	outcome.Segments, outcome.Scenes = len(segments), ontologySceneCount(cards)
	if failures := drama.OntologyHardFailures(out.Check); len(failures) > 0 {
		outcome.Errors = len(failures)
		outcome.Notes = append(outcome.Notes, "自检硬失败："+strings.Join(failures, "、")+"。请在导演文档里对这些段返修后再继续。")
	}
	return outcome, nil
}

// ontologyEpisodeLocks 让同一集的本体论收尾（整集替换、返修）和手工写段在本进程内串行，返修核对每集合计时长时其他段不会同时被改。
// 生产只跑一个 backend 进程；多进程部署时这里不跨进程。
var ontologyEpisodeLocks sync.Map

func lockOntologyEpisode(unitID string) func() {
	mu, _ := ontologyEpisodeLocks.LoadOrStore(unitID, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

// ontologyOtherSegmentsSeconds 是这一集除 segmentID 以外各段的时长合计（读写入前的实际段）。
func (s *Service) ontologyOtherSegmentsSeconds(userID string, projectID string, unitID string, segmentID string) (float64, error) {
	segments, err := s.DramaSegments(userID, projectID, unitID)
	if err != nil {
		return 0, err
	}
	var total float64
	for _, v := range segments {
		if v.Spec.ID != segmentID {
			total += v.Spec.Dur
		}
	}
	return total, nil
}

// ontologyGateContent / ontologyGateOpen 读写门禁文档：还没返修的自检硬失败项（JSON 字符串数组）。
func ontologyGateContent(open []string) string {
	if open == nil {
		open = []string{}
	}
	encoded, _ := json.Marshal(open)
	return string(encoded)
}

func ontologyGateOpen(content string) []string {
	var open []string
	_ = json.Unmarshal([]byte(content), &open)
	return open
}

// ontologyRemovedShots 是整集替换后不再需要的旧段（镜头 ID）。
func (s *Service) ontologyRemovedShots(userID string, projectID string, unitID string, order []string) ([]string, error) {
	existing, err := s.DramaSegments(userID, projectID, unitID)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, view := range existing {
		if !slices.Contains(order, view.Spec.ID) {
			removed = append(removed, view.ShotID)
		}
	}
	return removed, nil
}

// errOntologyRemovalBlocked 表示要删的旧段有未确认（或确认后又增加）的产物。
var errOntologyRemovalBlocked = errors.New("ontology removal blocked")

// deleteOntologyRemovedShots 在一个事务里逐段复核产物并立即删除：没有产物的段直接删；有产物的段必须是建任务时确认过（discard）
// 且产物没有增加。任一段不满足就整体回滚、一段都不删，返回需要确认的段。SQLite 写事务独占，计数与删除之间没有窗口；
// PostgreSQL 先锁镜头行，计数与删除是同一事务里紧挨着的两条语句。
func (s *Service) deleteOntologyRemovedShots(projectID string, unitID string, removed []string, meta *dramaOntologyMeta) error {
	if len(removed) == 0 {
		return nil
	}
	var blocked []string
	now := time.Now()
	err := s.repo.Transaction(func(r *repository.Repository) error {
		for _, shotID := range removed {
			n, err := r.CountActiveShotArtifacts(projectID, shotID)
			if err != nil {
				return err
			}
			if confirmed, ok := meta.Confirmed[shotID]; n > 0 && (!meta.Discard || !ok || int(n) > confirmed) {
				blocked = append(blocked, shotID)
				continue
			}
			if err := r.DeleteProjectShot(projectID, shotID, now); err != nil {
				return err
			}
		}
		if len(blocked) > 0 {
			return errOntologyRemovalBlocked
		}
		return nil
	})
	if errors.Is(err, errOntologyRemovalBlocked) {
		discards, derr := s.dramaSegmentDiscards(projectID, unitID, blocked)
		if derr != nil {
			return derr
		}
		return dramaSegmentsDiscardRequired(discards)
	}
	return err
}

// reorderOntologySegments 按单元顺序（段号 S01、S02……）重排本集的段。
func (s *Service) reorderOntologySegments(userID string, projectID string, unitID string, order []string) error {
	views, err := s.DramaSegments(userID, projectID, unitID)
	if err != nil {
		return err
	}
	shotOf := map[string]string{}
	for _, v := range views {
		shotOf[v.Spec.ID] = v.ShotID
	}
	ordered := make([]string, 0, len(order))
	for _, id := range order {
		if shotOf[id] == "" {
			return fmt.Errorf("写入后找不到段 %s", id)
		}
		ordered = append(ordered, shotOf[id])
	}
	return s.repo.ReorderUnitShots(projectID, unitID, ordered, time.Now())
}

// writeOntologyDocs 在一个事务里写收尾的全部文档；同一任务已写过的（同单元、同种类）跳过，重新收尾不重复加版本。
func (s *Service) writeOntologyDocs(task model.Task, projectID string, docs []ontologyDocWrite) error {
	return s.repo.Transaction(func(r *repository.Repository) error {
		for _, d := range docs {
			written, err := r.OntologyDocumentWrittenByTask(projectID, d.unitID, d.kind, task.ID)
			if err != nil {
				return err
			}
			if written {
				continue
			}
			if err := r.AppendOntologyDocument(&model.ProjectOntologyDocument{ID: newID(), ProjectID: projectID, UnitID: d.unitID, Kind: d.kind, Content: d.content, TaskID: task.ID, CreatedBy: task.UserID}); err != nil {
				return err
			}
		}
		return nil
	})
}

func ontologySceneCount(cards []drama.OntologyUnitCard) int {
	seen := map[string]bool{}
	for _, c := range cards {
		seen[c.Scene] = true
	}
	return len(seen)
}

// ontologySetupNotes 是立项轮的待确认清单和补图建议（原样）。
func ontologySetupNotes(setup string) string {
	var parts []string
	for _, title := range []string{"待确认清单", "补图建议"} {
		if body, ok := drama.OntologySection(setup, title); ok && strings.TrimSpace(body) != "" {
			parts = append(parts, "## "+title+"\n"+body)
		}
	}
	return strings.Join(parts, "\n\n")
}

// applyOntologyRepair 用重写后的单元卡替换那一个段，并把七部分里这张卡换成新卡（新版本）。先校验、装段、按模型检查，再写。
func (s *Service) applyOntologyRepair(task model.Task, target dramaStepTaskTarget, meta *dramaOntologyMeta, out ontologyOutputs, m drama.Model, outcome DramaStepTaskOutcome) (DramaStepTaskOutcome, error) {
	repair := meta.Repair
	unlock := lockOntologyEpisode(target.UnitID)
	defer unlock()
	// 每集合计时长按写入这一刻其他段的实际时长核对（建任务时的 OtherSeconds 只是提示模型用的估计，可能已过期或缺失）。
	others, err := s.ontologyOtherSegmentsSeconds(task.UserID, target.ProjectID, target.UnitID, repair.SegmentID)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	body, _ := drama.OntologySection(out.Repair, "重写后的单元卡")
	card, err := ontologySingleCard(body, repair.Unit, ontologyLimits(meta, others))
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	order, err := strconv.Atoi(strings.TrimPrefix(repair.SegmentID, "S"))
	if err != nil || order <= 0 {
		return DramaStepTaskOutcome{}, fmt.Errorf("返修的段号 %q 无效", repair.SegmentID)
	}
	seg, err := drama.OntologySegment(card, order, meta.Materials)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	seg.ID = repair.SegmentID
	if issues := drama.Issues(seg, m); drama.HasError(issues) {
		return DramaStepTaskOutcome{}, BadAuthRequest(fmt.Sprintf("返修后的单元 %s 装不成段：%s", card.ID, issues[0].Message))
	}
	raw, err := json.Marshal(seg)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	var docs []ontologyDocWrite
	doc, err := s.repo.LatestOntologyDocument(target.ProjectID, target.UnitID, model.OntologyDocEpisode)
	switch {
	case err == nil:
		updated := strings.Replace(doc.Content, strings.TrimSpace(repair.Card), card.Text, 1)
		updated += fmt.Sprintf("\n\n## 返修记录 %s\n症状：%s\n\n%s\n", repair.Unit, repair.Symptom, ontologyRepairNotes(out.Repair))
		docs = append(docs, ontologyDocWrite{target.UnitID, model.OntologyDocEpisode, updated})
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return DramaStepTaskOutcome{}, err
	}
	// 返修过的单元从门禁里去掉（用户看过自检结果、写了症状重写，视为已处理）；其他单元的硬失败仍挡着。
	gate, err := s.repo.LatestOntologyDocument(target.ProjectID, target.UnitID, model.OntologyDocGate)
	switch {
	case err == nil:
		open := slices.DeleteFunc(ontologyGateOpen(gate.Content), func(item string) bool { return strings.HasPrefix(item, repair.Unit+" ") })
		docs = append(docs, ontologyDocWrite{target.UnitID, model.OntologyDocGate, ontologyGateContent(open)})
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return DramaStepTaskOutcome{}, err
	}
	if _, err := s.writeDramaSegments(task.UserID, target.ProjectID, DramaSegmentsWriteRequest{UnitID: target.UnitID, Segments: []json.RawMessage{raw}}); err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if err := s.writeOntologyDocs(task, target.ProjectID, docs); err != nil {
		return DramaStepTaskOutcome{}, fmt.Errorf("段已返修，七部分更新失败（可从中断处继续，只重新收尾）：%w", err)
	}
	outcome.Segments, outcome.Shots, outcome.Scenes = 1, len(seg.Shots), 1
	return outcome, nil
}

func ontologyRepairNotes(text string) string {
	var parts []string
	for _, title := range []string{"诊断", "改法"} {
		if body, ok := drama.OntologySection(text, title); ok {
			parts = append(parts, "### "+title+"\n"+body)
		}
	}
	return strings.Join(parts, "\n\n")
}

// ontologyProjectError 把项目不存在或不属于当前用户的查询错误转成 404，其余错误原样返回。
func ontologyProjectError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NotFound("项目不存在")
	}
	return err
}

// ---------------------------------------------------------------- 接口：文档、编辑圣经卡、返修

// ProjectOntologyDocuments 是项目本体论分镜的文档（各文档最新版本）。
type ProjectOntologyDocuments struct {
	Bible    *model.ProjectOntologyDocument  `json:"bible,omitempty"`
	Setup    *model.ProjectOntologyDocument  `json:"setup,omitempty"`
	Episodes []model.ProjectOntologyDocument `json:"episodes"`
	Handoffs []model.ProjectOntologyDocument `json:"handoffs"`
}

func (s *Service) ProjectOntologyDocuments(userID string, projectID string) (ProjectOntologyDocuments, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return ProjectOntologyDocuments{}, ontologyProjectError(err)
	}
	docs, err := s.repo.OntologyDocumentsForProject(projectID)
	if err != nil {
		return ProjectOntologyDocuments{}, err
	}
	out := ProjectOntologyDocuments{Episodes: []model.ProjectOntologyDocument{}, Handoffs: []model.ProjectOntologyDocument{}}
	for i := range docs {
		doc := docs[i]
		switch doc.Kind {
		case model.OntologyDocBible:
			out.Bible = &doc
		case model.OntologyDocSetup:
			out.Setup = &doc
		case model.OntologyDocEpisode:
			out.Episodes = append(out.Episodes, doc)
		case model.OntologyDocHandoff:
			out.Handoffs = append(out.Handoffs, doc)
		}
	}
	return out, nil
}

// UpdateOntologyBibleRequest 是手改世界圣经卡：写一个新版本，只影响之后生成的集。
type UpdateOntologyBibleRequest struct {
	Content string `json:"content"`
}

func (s *Service) UpdateOntologyBible(userID string, projectID string, req UpdateOntologyBibleRequest) (*model.ProjectOntologyDocument, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return nil, ontologyProjectError(err)
	}
	if workflow, ok := drama.Workflow(project.WorkflowID); !ok || workflow.StoryboardMode != drama.StoryboardModeOntology {
		return nil, BadAuthRequest("只有本体论导演流水线的项目有世界圣经卡")
	}
	content := strings.TrimSpace(req.Content)
	if content == "" || len([]rune(content)) > 60000 {
		return nil, BadAuthRequest("世界圣经卡不能为空，且不超过 60000 字")
	}
	doc := &model.ProjectOntologyDocument{ID: newID(), ProjectID: projectID, Kind: model.OntologyDocBible, Content: content, CreatedBy: userID}
	if err := s.repo.AppendOntologyDocument(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// OntologyRepairRequest 是对一个段返修：写症状，服务端读这一段的原单元卡跑返修回合。
type OntologyRepairRequest struct {
	ShotID         string             `json:"shotId"`
	Symptom        string             `json:"symptom"`
	Model          string             `json:"model,omitempty"`
	LogicalModelID string             `json:"logicalModelId,omitempty"`
	Config         map[string]any     `json:"config,omitempty"`
	Rule           *DramaShotlistRule `json:"rule,omitempty"`
	TraceID        string             `json:"traceId,omitempty"`
	RequestID      string             `json:"requestId,omitempty"`
}

func (s *Service) RepairOntologySegment(userID string, projectID string, unitID string, req OntologyRepairRequest) (DramaStepRunResult, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return DramaStepRunResult{}, ontologyProjectError(err)
	}
	step, _ := drama.StepByID(drama.StepChapterShots)
	usage, skill, err := s.dramaStepSkill(project, step, req.Rule)
	if err != nil {
		return DramaStepRunResult{}, err
	}
	if usage.Workflow.StoryboardMode != drama.StoryboardModeOntology {
		return DramaStepRunResult{}, BadAuthRequest("只有本体论导演流水线的项目能用单元卡返修")
	}
	symptom := strings.TrimSpace(req.Symptom)
	if symptom == "" || len([]rune(symptom)) > 2000 {
		return DramaStepRunResult{}, BadAuthRequest("请写明翻车症状（不超过 2000 字）")
	}
	unit, err := s.repo.ProjectUnit(projectID, strings.TrimSpace(unitID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DramaStepRunResult{}, BadAuthRequest("章节不存在或不属于该项目")
	}
	if err != nil {
		return DramaStepRunResult{}, err
	}
	segments, err := s.DramaSegments(userID, projectID, unit.ID)
	if err != nil {
		return DramaStepRunResult{}, err
	}
	idx := slices.IndexFunc(segments, func(v ProjectDramaSegmentView) bool { return v.ShotID == strings.TrimSpace(req.ShotID) })
	if idx < 0 || strings.TrimSpace(segments[idx].Spec.Verbatim) == "" {
		return DramaStepRunResult{}, BadAuthRequest("这一段不是本体论导演生成的单元，不能按单元卡返修")
	}
	seg := segments[idx].Spec
	doc, err := s.repo.LatestOntologyDocument(projectID, unit.ID, model.OntologyDocEpisode)
	if err != nil {
		return DramaStepRunResult{}, BadAuthRequest("这一集还没有本体论分镜的七部分，不能返修")
	}
	cards, _ := drama.ParseOntologyUnitCards(doc.Content)
	cardIdx := slices.IndexFunc(cards, func(c drama.OntologyUnitCard) bool { return c.ID == seg.Title })
	if cardIdx < 0 {
		return DramaStepRunResult{}, BadAuthRequest(fmt.Sprintf("七部分里找不到单元卡 %s", seg.Title))
	}
	repair := &ontologyRepairMeta{ShotID: segments[idx].ShotID, SegmentID: seg.ID, Unit: seg.Title, Symptom: symptom, Card: cards[cardIdx].Text}
	for i, c := range cards {
		if i != cardIdx {
			repair.OtherSeconds += c.TotalSeconds
		}
	}
	plan, err := s.planOntologyShots(userID, project, unit, usage, skill, req.Rule, repair)
	if err != nil {
		return DramaStepRunResult{}, err
	}
	run := newDramaRunMeta(plan, unit.ID)
	taskReq := dramaStepTaskRequest(project.ID, unit.ID, plan, dramaStepTaskOptions{Model: req.Model, LogicalModelID: req.LogicalModelID, Config: req.Config, Rule: req.Rule, TraceID: req.TraceID, RequestID: req.RequestID})
	taskReq.dramaRun = &run
	task, err := s.CreateTask(userID, taskReq)
	if err != nil {
		return DramaStepRunResult{}, err
	}
	return DramaStepRunResult{Task: task, Usage: plan.usage}, nil
}

// compileOntologyShot 编译本体论单元：提示词就是粘贴提示词原文（只换参考图编号写法），不去掉无图参考、不加色卡和空间说明——
// 那样会让 @图N 和实际上传的图错位，或改动原文。参考的资产没图时报 error，提交前拦下。
func (s *Service) compileOntologyShot(userID string, projectID string, shot *model.Shot, seg drama.Segment, derived bool, m drama.Model) (DramaCompileResult, error) {
	result := drama.CompileVerbatim(seg, m)
	lookup, err := s.dramaRefLookup(userID, projectID, seg)
	if err != nil {
		return DramaCompileResult{}, err
	}
	for i, r := range seg.Refs {
		if info := lookup(r.File); !info.known || !info.hasImage {
			result.Issues = append(result.Issues, drama.Issue{Level: "error", Message: fmt.Sprintf("%s 的参考图 %d（%s）没有可用图片：请先给这个资产出图或重新生成本集分镜", seg.ID, i+1, firstNonEmpty(r.Note, r.File))})
		}
	}
	phrases, err := s.activeCompileProfile()
	if err != nil {
		return DramaCompileResult{}, err
	}
	return DramaCompileResult{ShotID: shot.ID, Spec: seg, Derived: derived, Result: result, CompileProfile: compileProfileRef(phrases)}, nil
}
