// SSS 电影 / 短剧导演分镜（流水线一、二的分镜环节）：技能 yz-sss-film-director / yz-sss-drama-director 分轮生成。
// 一次运行 = 一个任务：立项（项目还没有全剧圣经时）→ 规划（集设计、台词手术）→ 讲戏 → 切分（整场镜头表、单元总表、启发库选用）
// → 每个单元两轮（单元卡、编译）→ 复看（后期备忘、集交接卡、复看与待确认）。返修是 返修 → 编译 两轮。
// 每一轮只装这一轮的技能文件（不截断），前面各轮的产物按需作为输入；轮与轮之间不带对话历史。
// 收尾把单元卡写成段（粘贴提示词原文存 spec.verbatim，@名称 换成 @图k），八部分、交接卡、全剧圣经写进 project_ontology_documents。
// 并发、删旧段、排序、文档幂等写与本体论导演共用（project_ontology.go）。

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
	"strconv"
	"strings"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// sssMaxPromptRunes 是一轮提示词（技能上下文 + 材料包 + 剧本 + 前序产物）的上限；超过直接失败，不截断。
const sssMaxPromptRunes = 160000

// dramaSSSMeta 是任务 metadata.dramaSSS：建任务时算好的材料，运行时按它组每一轮的提示词。
type dramaSSSMeta struct {
	Flow             int                 `json:"flow,omitempty"`
	IndependentStart bool                `json:"independentStart,omitempty"`
	PreviousTitle    string              `json:"previousTitle,omitempty"`
	Kind             string              `json:"kind"`
	Brief            drama.SSSBrief      `json:"brief"`
	Materials        []drama.SSSMaterial `json:"materials"`
	Docs             drama.SSSDocs       `json:"docs"`
	UnitTitle        string              `json:"unitTitle"`
	Script           string              `json:"script"`
	// Setup 表示项目还没有全剧圣经，这次运行先跑立项轮。
	Setup bool `json:"setup,omitempty"`
	// EstimatedTurns 是建任务时估的轮数（预授权、墙钟用）。
	EstimatedTurns int            `json:"estimatedTurns"`
	Repair         *sssRepairMeta `json:"repair,omitempty"`
	// Discard / Confirmed 同本体论：整集替换时用户确认过会丢掉的产物。
	Discard   bool           `json:"discard,omitempty"`
	Confirmed map[string]int `json:"confirmed,omitempty"`
}

// sssRepairMeta 是返修回合：只重写一个段。
type sssRepairMeta struct {
	PositionReference bool    `json:"positionReference,omitempty"`
	ShotID            string  `json:"shotId"`
	SegmentID         string  `json:"segmentId"`
	Unit              string  `json:"unit"`
	Symptom           string  `json:"symptom"`
	Card              string  `json:"card"`
	OtherSeconds      float64 `json:"otherSeconds,omitempty"`
}

// sssFingerprint 是一次运行的输入指纹：续跑时剧本、素材、各文档、画风与时长约束改过就不能接着旧轮次写。
func sssFingerprint(meta *dramaSSSMeta) string {
	type material struct {
		Index           int
		Name, VersionID string
	}
	mats := make([]material, 0, len(meta.Materials))
	for _, m := range meta.Materials {
		mats = append(mats, material{m.Index, m.Name, m.VersionID})
	}
	encoded, _ := json.Marshal(map[string]any{"kind": meta.Kind, "brief": meta.Brief, "materials": mats, "docs": meta.Docs, "script": meta.Script, "unit": meta.UnitTitle})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func taskDramaSSS(inputJSON string) *dramaSSSMeta {
	if !strings.Contains(inputJSON, `"dramaSSS"`) {
		return nil
	}
	var input struct {
		Metadata struct {
			DramaSSS *dramaSSSMeta `json:"dramaSSS"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil {
		return nil
	}
	return input.Metadata.DramaSSS
}

func metadataDramaSSS(metadata map[string]any) *dramaSSSMeta {
	raw, ok := metadata["dramaSSS"]
	if !ok {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var meta dramaSSSMeta
	if json.Unmarshal(encoded, &meta) != nil {
		return nil
	}
	return &meta
}

// sssStepSkill 是 SSS 流水线分镜环节的技能（不走风格档案绑定表：流水线定了就是这一个技能）。
func (s *Service) sssStepSkill(project *model.Project, workflow drama.AssetWorkflow, step drama.Step) (DramaStepUsage, drama.StepSkill, error) {
	pkg, err := s.skillDomain().StepSkillPackage(workflow.StoryboardSkill)
	if err != nil {
		return DramaStepUsage{}, drama.StepSkill{}, err
	}
	skill := stepSkillOf(pkg)
	usage := DramaStepUsage{
		Workflow: workflow, Step: step.ID, StepLabel: step.Label, Contract: drama.ContractSSSDirector,
		Profile: dramaProfileRef(projectDramaContext(project).profileID()),
		Skill:   DramaStepSkillRef{ID: skill.ID, Name: skill.Name, VersionID: skill.VersionID, Version: skill.Version, ContentHash: skill.ContentHash},
	}
	return usage, skill, nil
}

// planSSSShots 组一次 SSS 分镜运行：材料包（素材清单按项目有图的资产）、立意与结构、世界与总谱、全剧圣经、上一集交接卡、本集剧本。
type sssShotOptions struct {
	Flow                int
	AllowMissingHandoff bool
}

func (s *Service) planSSSShots(userID string, project *model.Project, unit *model.ProjectUnit, usage DramaStepUsage, skill drama.StepSkill, rule *DramaShotlistRule, repair *sssRepairMeta, options sssShotOptions) (*dramaStepPlan, error) {
	m, err := dramaShotlistModel(project, briefSegmentRule(project.BriefJSON, rule))
	if err != nil {
		return nil, err
	}
	maxSeconds := min(drama.SSSUnitMaxSeconds, m.Duration.Max)
	if maxSeconds < 5 {
		return nil, BadAuthRequest(fmt.Sprintf("视频模型 %s 一次最多 %d 秒，太短，SSS 导演分镜至少需要 5 秒一个单元", m.Label, maxSeconds))
	}
	var materials []drama.SSSMaterial
	if repair != nil {
		// 返修的原单元卡里的 @名称 是按生成那一集时的素材清单写的：用那一集存下的清单。
		materials, err = s.sssEpisodeMaterials(project.ID, unit.ID)
	} else {
		materials, err = s.sssMaterials(userID, project.ID)
	}
	if err != nil {
		return nil, err
	}
	kind := drama.SSSWorkflowKind(project.WorkflowID)
	meta := &dramaSSSMeta{Kind: kind, Materials: materials, UnitTitle: unit.Title, Script: chapterPlainText(unit.SourceText), Repair: repair}
	meta.Brief = sssProjectBrief(project, kind, maxSeconds)
	if m.Refs.Images != nil {
		meta.Brief.MaxImages = *m.Refs.Images
	}
	for _, d := range []struct {
		kind string
		dst  *string
	}{{model.OntologyDocConcept, &meta.Docs.Concept}, {model.OntologyDocWorld, &meta.Docs.World}, {model.OntologyDocBible, &meta.Docs.Bible}} {
		doc, err := s.repo.LatestOntologyDocument(project.ID, "", d.kind)
		switch {
		case err == nil:
			*d.dst = doc.Content
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return nil, err
		}
	}
	meta.Setup = strings.TrimSpace(meta.Docs.Bible) == "" && repair == nil
	meta.Flow = options.Flow
	if meta.Flow >= 2 && repair == nil {
		design, err := s.repo.LatestOntologyDocument(project.ID, unit.ID, model.OntologyDocDesign)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, sssPrecondition(ReasonEpisodeDesignRequired, "这一集还没有本集设计：先在剧本阶段补写本集设计", map[string]any{"unitId": unit.ID, "title": unit.Title})
		}
		if err != nil {
			return nil, err
		}
		meta.Docs.Design = design.Content
		units, err := s.repo.ProjectUnits(project.ID)
		if err != nil {
			return nil, err
		}
		number := 0
		for _, ep := range units {
			if ep.Kind == unit.Kind {
				number++
			}
			if ep.ID == unit.ID {
				break
			}
		}
		meta.Docs.Concept = drama.SSSConceptForEpisode(meta.Docs.Concept, number)
		if meta.Setup {
			return nil, sssPrecondition(ReasonDirectorBibleRequired, "还没有全剧立项：先跑全剧立项", nil)
		}
	}
	prev, handoff, err := s.previousEpisodeHandoff(project.ID, unit, meta.Flow >= 2)
	if err != nil {
		return nil, err
	}
	meta.Docs.Handoff = handoff
	if meta.Flow >= 2 && prev != nil && handoff == "" && repair == nil {
		if !options.AllowMissingHandoff {
			return nil, sssPrecondition(ReasonPreviousHandoffRequired, prev.Title+"还没有出分镜（没有交接卡）：先完成它，或确认独立开局", map[string]any{"previousUnitId": prev.ID, "previousTitle": prev.Title})
		}
		meta.IndependentStart = true
		meta.PreviousTitle = prev.Title
	}
	first := drama.SSSTurnPlan
	priors := []ontologyPrior(nil)
	switch {
	case repair != nil:
		first, meta.EstimatedTurns = drama.SSSTurnRepair, 2
		priors = []ontologyPrior{{"原单元卡", repair.Card}, {"翻车症状", repair.Symptom}}
	case meta.Setup:
		first = drama.SSSTurnSetup
	case meta.Flow >= 2:
		first = drama.SSSTurnDirect
		priors = sssTurnPriors(first, meta, sssOutputs{})
	}
	if repair == nil {
		meta.EstimatedTurns = sssEstimatedTurns(meta, maxSeconds)
	}
	skillCtx, uses, err := drama.SSSSkillContext(skill, first, nil)
	if err != nil {
		return nil, err
	}
	unitID := ""
	if repair != nil {
		unitID = repair.Unit
	}
	prompt := sssTurnPrompt(skillCtx, meta, first, unitID, nil, priors)
	records := make([]map[string]any, 0, len(materials))
	for _, mat := range materials {
		records = append(records, map[string]any{"index": mat.Index, "name": mat.Name, "category": mat.Category, "versionId": mat.VersionID})
	}
	return &dramaStepPlan{usage: usage, prompt: drama.StepPrompt{Text: prompt, Files: uses}, skill: skill, sss: meta, record: map[string]any{"canvasAssets": records}}, nil
}

// sssEstimatedTurns 估轮数：按每集时长和单元上限估单元数，每个单元两轮，再加规划、讲戏、切分、复看（和立项）。
func sssEstimatedTurns(meta *dramaSSSMeta, maxSeconds int) int {
	seconds := meta.Brief.EpisodeSeconds
	if seconds <= 0 {
		seconds = 120
	}
	units := max(2, int(math.Ceil(float64(seconds)/math.Max(1, float64(maxSeconds)*0.8))))
	return sssTotalTurns(meta, units)
}

func sssProjectBrief(project *model.Project, kind string, maxSeconds int) drama.SSSBrief {
	medium := map[string]string{"live_action": "真人写实", "cg_real": "三维动画（写实）", "anime_3d": "三维动画", "anime_2d": "二维动画"}[strings.TrimSpace(project.VisualType)]
	anchor, palette, _, locked := projectLockedStyle(project.BibleJSON)
	if !locked {
		anchor, palette = nil, drama.MasterPalette{}
	}
	style := drama.SSSStyleText(projectStyleSnapshot(project).Prompt, anchor, palette)
	return drama.SSSBrief{Kind: kind, AspectRatio: project.AspectRatio, Medium: medium, EpisodeSeconds: int(briefEpisodeSeconds(project.BriefJSON)), MaxUnitSeconds: maxSeconds, Style: style}
}

// sssEpisodeMaterials 是这一集生成时存下的素材清单（返修用）。
func (s *Service) sssEpisodeMaterials(projectID string, unitID string) ([]drama.SSSMaterial, error) {
	doc, err := s.repo.LatestOntologyDocument(projectID, unitID, model.OntologyDocMaterials)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, BadAuthRequest("这一集没有存下生成时的素材清单，请重新生成本集分镜后再返修")
	}
	if err != nil {
		return nil, err
	}
	var materials []drama.SSSMaterial
	if err := json.Unmarshal([]byte(doc.Content), &materials); err != nil {
		return nil, fmt.Errorf("这一集的素材清单无效：%w", err)
	}
	return materials, nil
}

// sssMaterials 是素材清单：与本体论同一套挑法（有图的人物、场景、道具，主卡在前），名称就是资产名；同名的后面加序号，
// 名称里的 @ 和空白去掉，撞上保留写法的加「图」字（技能按 @名称 引用，名称要能原样写进提示词）。
func (s *Service) sssMaterials(userID string, projectID string) ([]drama.SSSMaterial, error) {
	picked, err := s.ontologyMaterials(userID, projectID)
	if err != nil {
		return nil, err
	}
	return sssMaterialsFrom(picked), nil
}

// sssMaterialsFrom 把挑好的资产写成素材清单名称（纯函数，测试直接调）。
func sssMaterialsFrom(picked []drama.OntologyMaterial) []drama.SSSMaterial {
	out := make([]drama.SSSMaterial, 0, len(picked))
	used := map[string]bool{}
	for _, m := range picked {
		base := strings.Join(strings.Fields(strings.ReplaceAll(m.Name, "@", "")), "")
		if base == "" {
			base = firstNonEmpty(assetCategoryLabel(m.Category), "素材")
		}
		// 资产名撞上平台的保留写法（@视频、@音频、@站位参考、@某某声音）时加「图」字，技能才能用 @名称 引用它。
		if drama.SSSReservedTag(base) != "" {
			base += "图"
		}
		name := base
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s%d", base, n)
		}
		used[name] = true
		out = append(out, drama.SSSMaterial{Index: m.Index, Name: name, Category: m.Category, Note: m.Note, VersionID: m.VersionID})
	}
	return out
}

// sssTurnPrompt 是一轮发给模型的完整提示词：技能上下文、材料包、本集剧本（单元卡、编译、返修轮不带）、前序产物、本轮任务。
func sssTurnPrompt(skillContext string, meta *dramaSSSMeta, kind string, unit string, codes []string, priors []ontologyPrior) string {
	parts := []string{skillContext, drama.SSSMaterialPackage(meta.Brief, meta.Materials, meta.Docs)}
	switch kind {
	case drama.SSSTurnCard, drama.SSSTurnCompile, drama.SSSTurnRepair:
	default:
		parts = append(parts, fmt.Sprintf("【本集剧本】%s\n%s", meta.UnitTitle, strings.TrimSpace(meta.Script)))
	}
	for _, p := range priors {
		if strings.TrimSpace(p.Text) != "" {
			parts = append(parts, fmt.Sprintf("【前序产物：%s】\n%s", p.Title, strings.TrimSpace(p.Text)))
		}
	}
	parts = append(parts, drama.SSSTurnInstruction(kind, unit, codes))
	if meta.Flow >= 2 && kind == drama.SSSTurnDirect {
		parts = append(parts, "本集设计与台词定稿表已在剧本阶段写好，台词以本集剧本为准，不再改台词。")
	}
	if meta.IndependentStart && kind == drama.SSSTurnReview {
		parts = append(parts, "本集是独立开局，没有上一集交接卡。")
	}
	return strings.Join(parts, "\n\n")
}

// prepareSSSRun 同 prepareOntologyRun：整集替换的确认范围；续跑时恢复上一次任务不可变的输入并按实际进度定轮数。
func (s *Service) prepareSSSRun(userID string, projectID string, unitID string, plan *dramaStepPlan, run *dramaRunMeta, discard bool) error {
	meta := plan.sss
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
	prevMeta, prevStep, progress := taskDramaSSS(previous.InputJSON), taskDramaStep(previous.InputJSON), taskDramaRunProgress(previous.ResultJSON)
	switch {
	case prevMeta == nil || prevStep == nil || progress == nil:
		return BadAuthRequest("要继续的任务不是 SSS 导演分镜任务")
	case prevStep.VersionID != plan.skill.VersionID:
		return BadAuthRequest("SSS 导演技能已更新，不能混用旧轮次，请重新生成本集分镜")
	case sssFingerprint(prevMeta) != sssFingerprint(meta):
		return BadAuthRequest("上一次任务之后剧本、素材、立意与结构、世界与总谱、全剧圣经、上一集交接卡或画风改过了，不能接着旧轮次写，请重新生成本集分镜")
	}
	prevMeta.Discard, prevMeta.Confirmed = meta.Discard, meta.Confirmed
	plan.sss = prevMeta
	completed := min(run.ResumeFrom-1, len(progress.Turns))
	out := sssStateFrom(progress.Turns[:completed])
	if _, done := sssNextTurn(prevMeta, out); done {
		run.Turns = completed
		return nil
	}
	total := max(prevMeta.EstimatedTurns, progress.Total)
	if out.Units != nil {
		total = sssTotalTurns(prevMeta, len(out.Units))
	}
	run.Turns = max(total, completed+1)
	return nil
}

// ---------------------------------------------------------------- 运行

// sssOutputs 是各轮的原样输出（续写、收尾从逐轮进度恢复）。Cards / Compiles 按单元顺序。
type sssOutputs struct {
	Setup, Plan, Direct, Cut, Review string
	Cards, Compiles                  []string
	Repair, RepairCompile            string
	// Units 是切分轮单元总表里的单元；Codes 是各单元选用的启发库库号。
	Units []string
	Codes map[string][]string
}

type sssTurnSpec struct {
	kind, unit, label string
}

func sssStateFrom(turns []dramaRunTurn) sssOutputs {
	var out sssOutputs
	for _, t := range turns {
		switch t.SSS {
		case drama.SSSTurnSetup:
			out.Setup = t.Text
		case drama.SSSTurnPlan:
			out.Plan = t.Text
		case drama.SSSTurnDirect:
			out.Direct = t.Text
		case drama.SSSTurnCut:
			out.Cut = t.Text
			out.Units, _ = drama.SSSUnits(t.Text)
			out.Codes = t.Codes
		case drama.SSSTurnCard:
			out.Cards = append(out.Cards, t.Text)
		case drama.SSSTurnCompile:
			if out.Repair != "" {
				out.RepairCompile = t.Text
			} else {
				out.Compiles = append(out.Compiles, t.Text)
			}
		case drama.SSSTurnReview:
			out.Review = t.Text
		case drama.SSSTurnRepair:
			out.Repair = t.Text
		}
	}
	return out
}

// sssNextTurn 按已写完的各轮决定下一轮；done 为 true 表示都写完了。
func sssNextTurn(meta *dramaSSSMeta, out sssOutputs) (sssTurnSpec, bool) {
	if meta.Repair != nil {
		switch {
		case out.Repair == "":
			return sssTurnSpec{drama.SSSTurnRepair, meta.Repair.Unit, "返修 " + meta.Repair.Unit}, false
		case out.RepairCompile == "":
			return sssTurnSpec{drama.SSSTurnCompile, meta.Repair.Unit, "编译 " + meta.Repair.Unit}, false
		}
		return sssTurnSpec{}, true
	}
	switch {
	case meta.Flow < 2 && meta.Setup && out.Setup == "":
		return sssTurnSpec{drama.SSSTurnSetup, "", "立项：全剧圣经"}, false
	case meta.Flow < 2 && out.Plan == "":
		return sssTurnSpec{drama.SSSTurnPlan, "", "本集设计与台词定稿"}, false
	case out.Direct == "":
		return sssTurnSpec{drama.SSSTurnDirect, "", "讲戏本"}, false
	case out.Cut == "":
		return sssTurnSpec{drama.SSSTurnCut, "", "整场镜头表与单元总表"}, false
	case len(out.Compiles) < len(out.Units):
		u := out.Units[len(out.Compiles)]
		if len(out.Cards) == len(out.Compiles) {
			return sssTurnSpec{drama.SSSTurnCard, u, "单元卡 " + u}, false
		}
		return sssTurnSpec{drama.SSSTurnCompile, u, "编译 " + u}, false
	case out.Review == "":
		return sssTurnSpec{drama.SSSTurnReview, "", "后期备忘、交接卡与复看"}, false
	}
	return sssTurnSpec{}, true
}

func sssTotalTurns(meta *dramaSSSMeta, units int) int {
	if meta.Repair != nil {
		return 2
	}
	if meta.Flow >= 2 {
		return 3 + 2*units
	}
	total := 4 + 2*units
	if meta.Setup {
		total++
	}
	return total
}

// sssCardOf 解析一轮单元卡（或返修重写的单元卡）的正文：恰好这一个单元的一张卡。
func sssCardOf(text string, unit string) (drama.SSSUnitCard, error) {
	cards, err := drama.ParseSSSUnitCards(text)
	if err != nil {
		return drama.SSSUnitCard{}, err
	}
	if len(cards) != 1 {
		return drama.SSSUnitCard{}, fmt.Errorf("本轮只写单元 %s 的一张单元卡，输出里有 %d 张", unit, len(cards))
	}
	card := cards[0]
	card.Paste, card.Risk, card.Fenced = "", "", false
	return card, nil
}

// sssRepairCardText 是返修轮「重写后的单元卡」那一部分。
func sssRepairCardText(text string) string {
	body, _ := drama.SSSSection(text, "重写后的单元卡")
	return body
}

// sssTurnPriors 是这一轮要带的前序产物。
func sssTurnPriors(kind string, meta *dramaSSSMeta, out sssOutputs) []ontologyPrior {
	if meta.Flow >= 2 {
		out.Plan = meta.Docs.Design
		if (kind == drama.SSSTurnCard || kind == drama.SSSTurnCompile) && len(out.Compiles) < len(out.Units) {
			out.Direct = drama.SSSDirectForScenes(out.Direct, drama.SSSUnitScenes(out.Cut, out.Units[len(out.Compiles)]))
		}
	}
	switch kind {
	case drama.SSSTurnDirect:
		return []ontologyPrior{{"本集设计与台词定稿表", out.Plan}}
	case drama.SSSTurnCut:
		return []ontologyPrior{{"本集设计与台词定稿表", out.Plan}, {"讲戏本", out.Direct}}
	case drama.SSSTurnCard:
		priors := []ontologyPrior{{"本集设计与台词定稿表", out.Plan}, {"讲戏本", out.Direct}, {"整场镜头表、单元总表与启发库选用", out.Cut}}
		if n := len(out.Cards); n > 0 {
			priors = append(priors, ontologyPrior{"上一个单元的单元卡（接缝用）", out.Cards[n-1]})
		}
		return priors
	case drama.SSSTurnCompile:
		if meta.Repair != nil {
			return []ontologyPrior{{"本单元的单元卡", sssRepairCardText(out.Repair)}}
		}
		if meta.Flow >= 2 {
			priors := []ontologyPrior{{"本集设计与台词定稿表", out.Plan}, {"本场讲戏本", out.Direct}, {"切分结果", out.Cut}, {"本单元的单元卡", out.Cards[len(out.Cards)-1]}}
			if len(out.Cards) > 1 {
				priors = append(priors, ontologyPrior{"上一个单元的单元卡", out.Cards[len(out.Cards)-2]})
			}
			return priors
		}
		return []ontologyPrior{{"本单元的单元卡", out.Cards[len(out.Cards)-1]}}
	case drama.SSSTurnReview:
		cards := make([]string, 0, len(out.Cards))
		for i := range out.Cards {
			cards = append(cards, strings.TrimSpace(out.Cards[i])+"\n\n"+strings.TrimSpace(out.Compiles[i]))
		}
		return []ontologyPrior{{"本集设计与台词定稿表", out.Plan}, {"整场镜头表与单元总表", out.Cut}, {"全部单元卡", strings.Join(cards, "\n\n")}}
	case drama.SSSTurnRepair:
		return []ontologyPrior{{"原单元卡", meta.Repair.Card}, {"翻车症状", meta.Repair.Symptom}}
	}
	return nil
}

// sssReviewPriorsSlim 是复看轮提示词超长时的退路：单元卡只带正文（粘贴提示词已逐条过了系统校验）。
func sssReviewPriorsSlim(out sssOutputs) []ontologyPrior {
	return []ontologyPrior{{"本集设计与台词定稿表", out.Plan}, {"整场镜头表与单元总表", out.Cut},
		{"全部单元卡（只附正文；粘贴提示词已逐条通过系统校验：十五段、长度、标签、背景音乐）", strings.Join(out.Cards, "\n\n")}}
}

func sssLimits(meta *dramaSSSMeta, used float64) drama.SSSUnitLimits {
	return drama.SSSUnitLimits{MaxSeconds: meta.Brief.MaxUnitSeconds, MaxImages: meta.Brief.MaxImages, Materials: meta.Materials,
		CapSeconds: drama.SSSEpisodeCap(meta.Brief.EpisodeSeconds), UsedSeconds: used}
}

// sssCardsSeconds 是前面已写好的单元卡总时长。
func sssCardsSeconds(texts []string, units []string) float64 {
	var total float64
	for i, text := range texts {
		if i < len(units) {
			if card, err := sssCardOf(text, units[i]); err == nil {
				total += card.TotalSeconds
			}
		}
	}
	return total
}

// sssUsedSeconds 是这一轮单元之外本集已占的秒数：单元卡、编译轮是前面写好的单元，返修是这一集其他单元。
func sssUsedSeconds(kind string, meta *dramaSSSMeta, out sssOutputs) float64 {
	switch {
	case meta.Repair != nil:
		return meta.Repair.OtherSeconds
	case kind == drama.SSSTurnCard:
		return sssCardsSeconds(out.Cards, out.Units)
	case kind == drama.SSSTurnCompile:
		return sssCardsSeconds(out.Cards[:len(out.Cards)-1], out.Units)
	}
	return 0
}

// sssTurnCheck 是这一轮输出的校验（不合格按临时失败重试，错误文字会回给模型）。
func sssTurnCheck(kind string, unit string, meta *dramaSSSMeta, skill drama.StepSkill, out sssOutputs, used float64) func(string, drama.TurnContext) (int, error) {
	limits := sssLimits(meta, used)
	limits.PositionReference = kind == drama.SSSTurnCompile && sssPositionNeeded(meta, out, unit)
	return func(text string, _ drama.TurnContext) (int, error) {
		if drama.OntologyUnfinished(text) {
			return 0, errors.New("输出里出现了未完续写标记：本轮必须一次写完，不使用「回复继续」")
		}
		switch kind {
		case drama.SSSTurnSetup:
			return 1, drama.SSSRequireSections(text, "全剧圣经", "待确认")
		case drama.SSSTurnPlan:
			return 1, drama.SSSRequireSections(text, "第一部分", "第二部分")
		case drama.SSSTurnDirect:
			return 1, drama.SSSRequireSections(text, "第三部分")
		case drama.SSSTurnCut:
			if err := drama.SSSRequireSections(text, "整场镜头表", "第四部分", "启发库选用"); err != nil {
				return 0, err
			}
			units, err := drama.SSSUnits(text)
			if err != nil {
				return 0, err
			}
			_, err = sssCutChoices(text, units, skill)
			return len(units), err
		case drama.SSSTurnCard:
			card, err := sssCardOf(text, unit)
			if err != nil {
				return 0, err
			}
			return 1, drama.ValidateSSSCardBody(card, unit, limits)
		case drama.SSSTurnCompile:
			body := sssCompileBody(meta, out)
			card, err := sssCardOf(body, unit)
			if err != nil {
				return 0, fmt.Errorf("编译用的单元卡读不出：%w", err)
			}
			return 1, drama.ValidateSSSPaste(drama.ParseSSSCompile(card, text), limits)
		case drama.SSSTurnReview:
			if err := drama.SSSRequireSections(text, "第六部分", "第七部分", "第八部分"); err != nil {
				return 0, err
			}
			if _, ok := drama.SSSHandoffText(text); !ok {
				return 0, errors.New("第七部分集交接卡为空")
			}
			return 1, nil
		case drama.SSSTurnRepair:
			if err := drama.SSSRequireSections(text, "诊断", "改法", "重写后的单元卡"); err != nil {
				return 0, err
			}
			card, err := sssCardOf(sssRepairCardText(text), unit)
			if err != nil {
				return 0, err
			}
			return 1, drama.ValidateSSSCardBody(card, unit, limits)
		}
		return 0, fmt.Errorf("不认识的轮次 %s", kind)
	}
}

// sssCompileBody 是编译轮要编译的单元卡正文：返修时是重写后的卡，否则是最近一张单元卡。
func sssCompileBody(meta *dramaSSSMeta, out sssOutputs) string {
	if meta.Repair != nil {
		return sssRepairCardText(out.Repair)
	}
	return out.Cards[len(out.Cards)-1]
}

func sssPositionNeeded(meta *dramaSSSMeta, out sssOutputs, unit string) bool {
	if meta.Repair != nil {
		return meta.Repair.Unit == unit && meta.Repair.PositionReference
	}
	i := slices.Index(out.Units, unit)
	if meta.Flow < 2 || i < 1 || i >= len(out.Cards) {
		return false
	}
	prev, err := sssCardOf(out.Cards[i-1], out.Units[i-1])
	if err != nil {
		return false
	}
	rows, err := drama.SSSShotTable(prev)
	if err != nil || len(rows) == 0 || !strings.Contains(rows[len(rows)-1].Duty, "定格") {
		return false
	}
	cur, err := sssCardOf(out.Cards[i], unit)
	return err == nil && cur.Scene != "" && cur.Scene == prev.Scene
}

// sssCutChoices 读切分轮的启发库选用并对照技能包里的库号。
func sssCutChoices(cut string, units []string, skill drama.StepSkill) (map[string][]string, error) {
	rc, err := drama.ReadSSSRuntimeContext(skill)
	if err != nil {
		return nil, err
	}
	return drama.SSSHeuristicChoices(cut, units, drama.SSSHeuristicCodes(skill, rc), rc.Heuristics.MaxPerUnit)
}

// runSSSRun 按轮循环调模型。轮次在运行中才定（切分轮写出单元总表后才知道有几个单元）。
func (s *Service) runSSSRun(ctx context.Context, input canvasGenerationInput, run dramaRunMeta, meta *dramaSSSMeta) (map[string]any, error) {
	task, ok := ctx.Value(mediaExecutionTaskKey{}).(model.Task)
	if !ok || task.ID == "" {
		return nil, errors.New("SSS 分镜缺少任务上下文")
	}
	step := taskDramaStep(task.InputJSON)
	if step == nil || step.SkillID == "" || step.VersionID == "" {
		return nil, errors.New("SSS 分镜任务缺少技能版本")
	}
	pkg, err := s.skillDomain().StepSkillPackageVersion(step.SkillID, step.VersionID)
	if err != nil {
		return nil, err
	}
	skill := stepSkillOf(pkg)
	rc, err := drama.ReadSSSRuntimeContext(skill)
	if err != nil {
		return nil, err
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return nil, err
	}
	turnTimeout := storyboardTurnTimeout(policy.Task)
	turns, err := s.dramaRunStartTurns(task, run)
	if err != nil {
		return nil, err
	}
	out := sssStateFrom(turns)
	if bible, ok := drama.SSSBibleText(out.Setup); ok {
		meta.Docs.Bible = bible
	}
	progress := dramaRunProgress{RunID: run.RunID, Total: max(run.Turns, len(turns)), Completed: len(turns), Turns: turns}
	stream := newDramaRunStream(s, task, input.OnTextDelta)
	defer stream.close()
	turnInput := input
	turnInput.OnTextDelta = stream.publish
	turnInput.TextHistory = nil
	for k := len(turns) + 1; ; k++ {
		spec, done := sssNextTurn(meta, out)
		if done {
			break
		}
		if latest, err := s.repo.Task(task.ID); err == nil && latest.Status == model.TaskStatusCancelled {
			return nil, context.Canceled
		}
		var codes, extra []string
		if spec.kind == drama.SSSTurnCard {
			codes = out.Codes[spec.unit]
			if extra, err = drama.SSSHeuristicFiles(skill, rc, codes); err != nil {
				return nil, err
			}
		}
		skillCtx, _, err := drama.SSSSkillContext(skill, spec.kind, extra)
		if err != nil {
			return nil, err
		}
		turnInput.Prompt = sssTurnPrompt(skillCtx, meta, spec.kind, spec.unit, codes, sssTurnPriors(spec.kind, meta, out))
		if spec.kind == drama.SSSTurnCompile && sssPositionNeeded(meta, out, spec.unit) {
			turnInput.Prompt += "\n本单元要写 @站位参考，给这张图留一个名额。"
		}
		if spec.kind == drama.SSSTurnReview && len([]rune(turnInput.Prompt)) > sssMaxPromptRunes {
			if meta.Flow >= 2 {
				out.Plan = meta.Docs.Design
			}
			turnInput.Prompt = sssTurnPrompt(skillCtx, meta, spec.kind, spec.unit, codes, sssReviewPriorsSlim(out))
		}
		used := sssUsedSeconds(spec.kind, meta, out)
		if spec.kind == drama.SSSTurnCard || spec.kind == drama.SSSTurnRepair {
			// 单元卡、返修重写单元卡：带上单元总表里这一单元的原行，镜头表照它核算时长（定格、技术尾巴都写进镜头表）。
			if line := drama.SSSUnitTimingLine(out.Cut, spec.unit); line != "" {
				turnInput.Prompt += "\n\n" + line
			}
		}
		if spec.kind == drama.SSSTurnCard || spec.kind == drama.SSSTurnCompile || spec.kind == drama.SSSTurnRepair {
			if line := drama.SSSBudgetLine(sssLimits(meta, used)); line != "" {
				turnInput.Prompt += "\n\n" + line
			}
		}
		if n := len([]rune(turnInput.Prompt)); n > sssMaxPromptRunes {
			return nil, dramaSentenceError{fmt.Errorf("第 %d 轮（%s）的输入有 %d 字，超过上限 %d 字：本集剧本或单元太多，请把本集拆短后重新生成", k, spec.label, n, sssMaxPromptRunes)}
		}
		if out.Units != nil {
			progress.Total = sssTotalTurns(meta, len(out.Units))
		}
		marker := dramaTurnMarker{Index: k, Total: max(progress.Total, k), Scene: spec.unit, Heading: spec.label}
		plan := drama.TurnPlan{Unit: "轮", Count: sssTurnCheck(spec.kind, spec.unit, meta, skill, out, used)}
		turn, err := s.runDramaTurn(ctx, task, turnInput, marker, stream, turnTimeout, plan, drama.TurnContext{Index: k, Contract: drama.ContractSSSDirector})
		if err != nil {
			return nil, dramaRunTurnError("轮", marker, progress.Completed, turnTimeout, err)
		}
		// 提示词由技能版本和材料重新组出，不存全文；续写只用 Text。
		turn.Prompt = "sss " + spec.kind + " " + spec.unit
		turn.TaskID, turn.SSS, turn.Scene, turn.Heading = task.ID, spec.kind, spec.unit, spec.label
		if spec.kind == drama.SSSTurnCut {
			units, _ := drama.SSSUnits(turn.Text)
			if turn.Codes, err = sssCutChoices(turn.Text, units, skill); err != nil {
				return nil, err
			}
		}
		progress.Turns = append(progress.Turns, turn)
		progress.Completed = k
		out = sssStateFrom(progress.Turns)
		if spec.kind == drama.SSSTurnSetup {
			if bible, ok := drama.SSSBibleText(turn.Text); ok {
				meta.Docs.Bible = bible
			}
		}
		if out.Units != nil {
			progress.Total = sssTotalTurns(meta, len(out.Units))
		}
		encoded, err := json.Marshal(map[string]any{"mode": "text", "dramaRun": progress})
		if err != nil {
			return nil, err
		}
		if err := s.repo.UpdateTaskResultForLease(task.ID, task.LeaseOwner, fmt.Sprintf("已写完第 %d/%d 轮：%s，输入 %d 字", k, max(progress.Total, k), spec.label, len([]rune(turnInput.Prompt))), 35+60*k/max(progress.Total, k), string(encoded)); err != nil {
			return nil, fmt.Errorf("保存第 %d 轮进度失败：%w", k, err)
		}
	}
	texts := make([]string, 0, len(progress.Turns))
	for i := range progress.Turns {
		texts = append(texts, strings.TrimSpace(progress.Turns[i].Text))
	}
	return map[string]any{"mode": "text", "text": strings.Join(texts, "\n\n") + "\n", "dramaRun": progress}, nil
}

// ---------------------------------------------------------------- 收尾

func sssResultOutputs(result map[string]interface{}) (sssOutputs, error) {
	encoded, err := json.Marshal(result["dramaRun"])
	if err != nil {
		return sssOutputs{}, err
	}
	var progress dramaRunProgress
	if err := json.Unmarshal(encoded, &progress); err != nil || len(progress.Turns) == 0 {
		return sssOutputs{}, errors.New("SSS 分镜任务结果缺少逐轮输出")
	}
	return sssStateFrom(progress.Turns), nil
}

// sssEpisodeCards 把各单元的单元卡和编译输出合成完整单元卡，逐张按「前面单元已用的秒数」校验（与生成时一致）。
func sssEpisodeCards(meta *dramaSSSMeta, out sssOutputs) ([]drama.SSSUnitCard, error) {
	if out.Units == nil {
		return nil, errors.New("切分轮没有可用的单元总表")
	}
	if len(out.Cards) != len(out.Units) || len(out.Compiles) != len(out.Units) || out.Review == "" {
		return nil, fmt.Errorf("单元总表有 %d 个单元，只写出了 %d 张单元卡、%d 条粘贴提示词，或缺少复看", len(out.Units), len(out.Cards), len(out.Compiles))
	}
	cards := make([]drama.SSSUnitCard, 0, len(out.Units))
	var used float64
	for i, unit := range out.Units {
		body, err := sssCardOf(out.Cards[i], unit)
		if err != nil {
			return nil, err
		}
		lim := sssLimits(meta, used)
		lim.PositionReference = sssPositionNeeded(meta, out, unit)
		if err := drama.ValidateSSSCardBody(body, unit, lim); err != nil {
			return nil, err
		}
		card := drama.ParseSSSCompile(body, out.Compiles[i])
		if err := drama.ValidateSSSPaste(card, lim); err != nil {
			return nil, err
		}
		used += card.TotalSeconds
		cards = append(cards, card)
	}
	return cards, nil
}

// applySSSShotsTask 是 SSS 分镜的收尾：先把全部单元卡校验、装成段并按视频模型检查，再复核要删的旧段，然后写段，最后写
// 全剧圣经、立项待确认、八部分、交接卡和素材清单。文档按任务幂等，段写成而文档失败时「从中断处继续」只重新收尾。
func (s *Service) applySSSShotsTask(task model.Task, meta *dramaSSSMeta, result map[string]interface{}) (DramaStepTaskOutcome, error) {
	target, err := taskDramaStepTarget(task)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	out, err := sssResultOutputs(result)
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
		return s.applySSSRepair(task, target, meta, out, m, outcome)
	}
	var docs []ontologyDocWrite
	if meta.Setup {
		bible, ok := drama.SSSBibleText(out.Setup)
		if !ok {
			return DramaStepTaskOutcome{}, errors.New("立项轮没有写出全剧圣经")
		}
		pending, _ := drama.SSSSection(out.Setup, "待确认")
		docs = append(docs, ontologyDocWrite{"", model.OntologyDocBible, bible}, ontologyDocWrite{"", model.OntologyDocSetup, "## 待确认\n\n" + pending})
	}
	cards, err := sssEpisodeCards(meta, out)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	segments := make([]json.RawMessage, 0, len(cards))
	order := make([]string, 0, len(cards))
	scenes := map[string]bool{}
	for i, card := range cards {
		seg, err := drama.SSSSegment(card, i+1, meta.Materials, sssPositionNeeded(meta, out, card.ID))
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
		segments, order = append(segments, raw), append(order, seg.ID)
		scenes[card.Scene] = true
		outcome.Shots += len(seg.Shots)
	}
	materials, err := json.Marshal(meta.Materials)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if meta.Flow >= 2 {
		out.Plan = meta.Docs.Design
	}
	episode := drama.SSSEpisodeDocument(out.Plan, out.Direct, out.Cut, cards, out.Review)
	if meta.IndependentStart {
		header := "## " + drama.SSSEpisodeParts[7] + "\n"
		before, eighth, ok := strings.Cut(episode, header)
		if ok {
			note := "本集独立开局：生成时" + meta.PreviousTitle + "还没有交接卡。\n"
			if strings.Contains(eighth, "### 待确认\n") {
				eighth = strings.Replace(eighth, "### 待确认\n", "### 待确认\n"+note, 1)
			} else {
				eighth = strings.TrimRight(eighth, "\n") + "\n\n### 待确认\n" + note
			}
			episode = before + header + eighth
		}
	}
	docs = append(docs, ontologyDocWrite{target.UnitID, model.OntologyDocEpisode, episode},
		ontologyDocWrite{target.UnitID, model.OntologyDocMaterials, string(materials)})
	if handoff, ok := drama.SSSHandoffText(out.Review); ok {
		docs = append(docs, ontologyDocWrite{target.UnitID, model.OntologyDocHandoff, handoff})
	}
	unlock := lockOntologyEpisode(target.UnitID)
	defer unlock()
	removed, err := s.ontologyRemovedShots(task.UserID, target.ProjectID, target.UnitID, order)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if err := s.deleteDirectorRemovedShots(target.ProjectID, target.UnitID, removed, meta.Discard, meta.Confirmed); err != nil {
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
	outcome.Segments, outcome.Scenes = len(segments), len(scenes)
	return outcome, nil
}

// applySSSRepair 用重写后的单元卡和新的粘贴提示词替换那一个段，并把八部分里这张卡换成新卡（新版本）。
func (s *Service) applySSSRepair(task model.Task, target dramaStepTaskTarget, meta *dramaSSSMeta, out sssOutputs, m drama.Model, outcome DramaStepTaskOutcome) (DramaStepTaskOutcome, error) {
	repair := meta.Repair
	unlock := lockOntologyEpisode(target.UnitID)
	defer unlock()
	others, err := s.ontologyOtherSegmentsSeconds(task.UserID, target.ProjectID, target.UnitID, repair.SegmentID)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	body, err := sssCardOf(sssRepairCardText(out.Repair), repair.Unit)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	lim := sssLimits(meta, others)
	if err := drama.ValidateSSSCardBody(body, repair.Unit, lim); err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if out.RepairCompile == "" {
		return DramaStepTaskOutcome{}, errors.New("返修后的单元还没有编译出粘贴提示词")
	}
	card := drama.ParseSSSCompile(body, out.RepairCompile)
	lim.PositionReference = repair.PositionReference
	if err := drama.ValidateSSSPaste(card, lim); err != nil {
		return DramaStepTaskOutcome{}, err
	}
	order, err := strconv.Atoi(strings.TrimPrefix(repair.SegmentID, "S"))
	if err != nil || order <= 0 {
		return DramaStepTaskOutcome{}, fmt.Errorf("返修的段号 %q 无效", repair.SegmentID)
	}
	seg, err := drama.SSSSegment(card, order, meta.Materials, repair.PositionReference)
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
	// 写任何东西之前（仍在单集锁内）核对返修快照还有效：返修任务建时读的原卡，必须还是八部分里现在这张、目标段也还在。
	// 两次返修乱序收尾、返修期间整集重新生成时，后到的旧快照在这里被拒，不会出现「段是新的、八部分还是别的版本」。
	outcome.Segments, outcome.Shots, outcome.Scenes = 1, len(seg.Shots), 1
	written, err := s.repo.OntologyDocumentWrittenByTask(target.ProjectID, target.UnitID, model.OntologyDocEpisode, task.ID)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if written {
		// 同一任务重复收尾：段和八部分都已由这个任务写过（先写段、后写文档），不再写第二遍。
		return outcome, nil
	}
	doc, err := s.repo.LatestOntologyDocument(target.ProjectID, target.UnitID, model.OntologyDocEpisode)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DramaStepTaskOutcome{}, BadAuthRequest("这一集没有八部分，不能收尾返修，请重新生成本集分镜")
	}
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	original := strings.TrimSpace(repair.Card)
	if original == "" || !strings.Contains(doc.Content, original) {
		return DramaStepTaskOutcome{}, BadAuthRequest(fmt.Sprintf("单元 %s 在这次返修开始后已经变了（另一次返修先完成，或本集重新生成过），这次返修没有写入；请按现在的单元卡重新返修", repair.Unit))
	}
	views, err := s.DramaSegments(task.UserID, target.ProjectID, target.UnitID)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if !slices.ContainsFunc(views, func(v ProjectDramaSegmentView) bool {
		return v.ShotID == repair.ShotID && v.Spec.ID == repair.SegmentID
	}) {
		return DramaStepTaskOutcome{}, BadAuthRequest(fmt.Sprintf("要返修的段 %s 已经不在本集里，这次返修没有写入", repair.SegmentID))
	}
	updated := strings.Replace(doc.Content, original, card.Text(), 1)
	updated += fmt.Sprintf("\n\n## 返修记录 %s\n\n症状：%s\n\n%s\n", repair.Unit, repair.Symptom, sssRepairNotes(out.Repair))
	docs := []ontologyDocWrite{{target.UnitID, model.OntologyDocEpisode, updated}}
	if _, err := s.writeDramaSegments(task.UserID, target.ProjectID, DramaSegmentsWriteRequest{UnitID: target.UnitID, Segments: []json.RawMessage{raw}}); err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if err := s.writeOntologyDocs(task, target.ProjectID, docs); err != nil {
		return DramaStepTaskOutcome{}, fmt.Errorf("段已返修，八部分更新失败（可从中断处继续，只重新收尾）：%w", err)
	}
	return outcome, nil
}

func sssRepairNotes(text string) string {
	var parts []string
	for _, title := range []string{"诊断", "改法"} {
		if body, ok := drama.SSSSection(text, title); ok {
			parts = append(parts, "### "+title+"\n\n"+body)
		}
	}
	return strings.Join(parts, "\n\n")
}

// RepairSSSSegment 是对 SSS 流水线的一个段返修：读这一段在八部分里的原单元卡，跑返修 + 编译两轮。
func (s *Service) RepairSSSSegment(userID string, project *model.Project, unitID string, req OntologyRepairRequest, usage DramaStepUsage, skill drama.StepSkill) (DramaStepRunResult, error) {
	symptom := strings.TrimSpace(req.Symptom)
	if symptom == "" || len([]rune(symptom)) > 2000 {
		return DramaStepRunResult{}, BadAuthRequest("请写明翻车症状（不超过 2000 字）")
	}
	unit, err := s.repo.ProjectUnit(project.ID, strings.TrimSpace(unitID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DramaStepRunResult{}, BadAuthRequest("章节不存在或不属于该项目")
	}
	if err != nil {
		return DramaStepRunResult{}, err
	}
	segments, err := s.DramaSegments(userID, project.ID, unit.ID)
	if err != nil {
		return DramaStepRunResult{}, err
	}
	idx := slices.IndexFunc(segments, func(v ProjectDramaSegmentView) bool { return v.ShotID == strings.TrimSpace(req.ShotID) })
	if idx < 0 || strings.TrimSpace(segments[idx].Spec.Verbatim) == "" {
		return DramaStepRunResult{}, BadAuthRequest("这一段不是 SSS 导演生成的单元，不能按单元卡返修")
	}
	seg := segments[idx].Spec
	doc, err := s.repo.LatestOntologyDocument(project.ID, unit.ID, model.OntologyDocEpisode)
	if err != nil {
		return DramaStepRunResult{}, BadAuthRequest("这一集还没有 SSS 分镜的八部分，不能返修")
	}
	cards, _ := drama.ParseSSSEpisodeCards(doc.Content)
	cardIdx := slices.IndexFunc(cards, func(c drama.SSSUnitCard) bool { return c.ID == seg.Title })
	if cardIdx < 0 {
		return DramaStepRunResult{}, BadAuthRequest(fmt.Sprintf("八部分里找不到单元卡 %s", seg.Title))
	}
	repair := &sssRepairMeta{ShotID: segments[idx].ShotID, SegmentID: seg.ID, Unit: seg.Title, Symptom: symptom, Card: cards[cardIdx].Text()}
	if idx > 0 && seg.Scene != "" && segments[idx-1].Spec.Scene == seg.Scene {
		repair.PositionReference = drama.SegmentEndsWithFreeze(segments[idx-1].Spec)
	}
	for i, c := range cards {
		if i != cardIdx {
			repair.OtherSeconds += c.TotalSeconds
		}
	}
	plan, err := s.planSSSShots(userID, project, unit, usage, skill, req.Rule, repair, sssShotOptions{Flow: 2})
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
