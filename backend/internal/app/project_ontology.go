// 本体论导演分镜（流水线一的分镜环节）：技能 yz-ontology-director 分环节分轮生成。
// 一次运行 = 一个任务：立项（项目还没有世界圣经卡时）→ 规划（S0/S2/S3）→ 讲戏（S4/S5）→ 每个单元一轮（S6/S7）→ 质检（S8 + S1 交接卡）。
// 每一轮只装这一轮的环节文件（不截断），前面各轮的产物按需作为输入；轮与轮之间不带对话历史。
// 收尾把单元卡写成段（粘贴提示词原文存 spec.verbatim），七部分、交接卡、圣经卡写进 project_ontology_documents。

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
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
}

// ontologyRepairMeta 是返修回合：只重写一个段。
type ontologyRepairMeta struct {
	ShotID    string `json:"shotId"`
	SegmentID string `json:"segmentId"`
	Unit      string `json:"unit"`
	Symptom   string `json:"symptom"`
	Card      string `json:"card"`
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
	materials, err := s.ontologyMaterials(userID, project.ID)
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
	return drama.OntologyBrief{Genre: genre, AspectRatio: project.AspectRatio, Medium: medium, EpisodeSeconds: int(briefEpisodeSeconds(project.BriefJSON)), MaxUnitSeconds: maxSeconds}
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

// ontologyTurnCheck 是这一轮输出的校验（不合格按临时失败重试，错误文字会回给模型）。
func ontologyTurnCheck(kind string, unit string, meta *dramaOntologyMeta) func(string, drama.TurnContext) (int, error) {
	limits := drama.OntologyUnitLimits{MaxSeconds: meta.Brief.MaxUnitSeconds, Materials: len(meta.Materials), MaxImages: meta.MaxImages}
	return func(text string, _ drama.TurnContext) (int, error) {
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
			return 1, drama.OntologyRequireSections(text, "六、本集交接卡", "七、自检结果与待确认清单")
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
		if units != nil {
			progress.Total = ontologyTotalTurns(meta, len(units))
		}
		marker := dramaTurnMarker{Index: k, Total: max(progress.Total, k), Scene: spec.unit, Heading: spec.label}
		plan := drama.TurnPlan{Unit: "轮", Count: ontologyTurnCheck(spec.kind, spec.unit, meta)}
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
	return map[string]any{"mode": "text", "text": strings.Join(texts, "\n\n") + "\n", "dramaRun": progress, "ontology": out}, nil
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

// applyOntologyShotsTask 是本体论分镜的收尾：圣经卡（立项）、七部分、交接卡写文档，单元卡写成段。返修只替换一个段。
// 自检有硬失败时照常写段（便于查看和返修），Notes 写明哪些单元哪一项失败，流水线不接下一集。
func (s *Service) applyOntologyShotsTask(task model.Task, step *dramaStepTaskMeta, meta *dramaOntologyMeta, result map[string]interface{}) (DramaStepTaskOutcome, error) {
	target, err := taskDramaStepTarget(task)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	var out ontologyOutputs
	encoded, err := json.Marshal(result["ontology"])
	if err == nil {
		err = json.Unmarshal(encoded, &out)
	}
	if err != nil {
		return DramaStepTaskOutcome{}, fmt.Errorf("本体论分镜任务结果无效：%w", err)
	}
	outcome := DramaStepTaskOutcome{Step: drama.StepChapterShots, Status: DramaStepOutcomeWritten, UnitID: target.UnitID}
	limits := drama.OntologyUnitLimits{MaxSeconds: meta.Brief.MaxUnitSeconds, Materials: len(meta.Materials), MaxImages: meta.MaxImages}
	if meta.Repair != nil {
		return s.applyOntologyRepair(task, target, meta, out, limits, outcome)
	}
	if meta.Setup {
		bible, ok := drama.OntologySection(out.Setup, "世界圣经卡")
		if !ok || strings.TrimSpace(bible) == "" {
			return DramaStepTaskOutcome{}, errors.New("立项轮没有写出世界圣经卡")
		}
		if err := s.appendOntologyDoc(task, target.ProjectID, "", model.OntologyDocBible, bible); err != nil {
			return DramaStepTaskOutcome{}, err
		}
		if err := s.appendOntologyDoc(task, target.ProjectID, "", model.OntologyDocSetup, ontologySetupNotes(out.Setup)); err != nil {
			return DramaStepTaskOutcome{}, err
		}
	}
	units, err := drama.OntologyRhythmUnits(out.Plan)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if len(out.Units) != len(units) {
		return DramaStepTaskOutcome{}, fmt.Errorf("节奏总表有 %d 个单元，只写出了 %d 张单元卡", len(units), len(out.Units))
	}
	cards := make([]drama.OntologyUnitCard, 0, len(units))
	segments := make([]json.RawMessage, 0, len(units))
	order := make([]string, 0, len(units))
	for i, text := range out.Units {
		card, err := ontologySingleCard(text, units[i], limits)
		if err != nil {
			return DramaStepTaskOutcome{}, err
		}
		seg, err := drama.OntologySegment(card, i+1, meta.Materials)
		if err != nil {
			return DramaStepTaskOutcome{}, err
		}
		raw, err := json.Marshal(seg)
		if err != nil {
			return DramaStepTaskOutcome{}, err
		}
		cards, segments, order = append(cards, card), append(segments, raw), append(order, seg.ID)
		outcome.Shots += len(seg.Shots)
	}
	if _, err := s.writeDramaSegments(task.UserID, target.ProjectID, DramaSegmentsWriteRequest{UnitID: target.UnitID, Segments: segments, Order: order}); err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if err := s.appendOntologyDoc(task, target.ProjectID, target.UnitID, model.OntologyDocEpisode, drama.OntologyEpisodeDocument(out.Plan, out.Direct, cards, out.Check)); err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if handoff, ok := drama.OntologySection(out.Check, "六、本集交接卡"); ok && strings.TrimSpace(handoff) != "" {
		if err := s.appendOntologyDoc(task, target.ProjectID, target.UnitID, model.OntologyDocHandoff, handoff); err != nil {
			return DramaStepTaskOutcome{}, err
		}
	}
	outcome.Segments, outcome.Scenes = len(segments), ontologySceneCount(cards)
	if failures := drama.OntologyHardFailures(out.Check); len(failures) > 0 {
		outcome.Errors = len(failures)
		outcome.Notes = append(outcome.Notes, "自检硬失败："+strings.Join(failures, "、")+"。请在制作台对这些段返修后再继续。")
	}
	return outcome, nil
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

func (s *Service) appendOntologyDoc(task model.Task, projectID string, unitID string, kind string, content string) error {
	return s.repo.AppendOntologyDocument(&model.ProjectOntologyDocument{ID: newID(), ProjectID: projectID, UnitID: unitID, Kind: kind, Content: content, TaskID: task.ID, CreatedBy: task.UserID})
}

// applyOntologyRepair 用重写后的单元卡替换那一个段，并把七部分里这张卡换成新卡（新版本）。
func (s *Service) applyOntologyRepair(task model.Task, target dramaStepTaskTarget, meta *dramaOntologyMeta, out ontologyOutputs, limits drama.OntologyUnitLimits, outcome DramaStepTaskOutcome) (DramaStepTaskOutcome, error) {
	repair := meta.Repair
	body, _ := drama.OntologySection(out.Repair, "重写后的单元卡")
	card, err := ontologySingleCard(body, repair.Unit, limits)
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
	raw, err := json.Marshal(seg)
	if err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if _, err := s.writeDramaSegments(task.UserID, target.ProjectID, DramaSegmentsWriteRequest{UnitID: target.UnitID, Segments: []json.RawMessage{raw}}); err != nil {
		return DramaStepTaskOutcome{}, err
	}
	if doc, err := s.repo.LatestOntologyDocument(target.ProjectID, target.UnitID, model.OntologyDocEpisode); err == nil {
		updated := strings.Replace(doc.Content, strings.TrimSpace(repair.Card), card.Text, 1)
		updated += fmt.Sprintf("\n\n## 返修记录 %s\n症状：%s\n\n%s\n", repair.Unit, repair.Symptom, ontologyRepairNotes(out.Repair))
		if err := s.appendOntologyDoc(task, target.ProjectID, target.UnitID, model.OntologyDocEpisode, updated); err != nil {
			return DramaStepTaskOutcome{}, err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return DramaStepTaskOutcome{}, err
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
		return ProjectOntologyDocuments{}, err
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
		return nil, err
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
		return DramaStepRunResult{}, err
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
