package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

type directorSetupMeta struct {
	Batches [][]string        `json:"batches"`
	Basis   []string          `json:"basis"`
	Queue   []string          `json:"queue,omitempty"`
	Context string            `json:"context"`
	Scripts map[string]string `json:"scripts"`
}

func taskDirectorSetup(input string) *directorSetupMeta {
	if !strings.Contains(input, "\"dramaSetup\"") {
		return nil
	}
	var envelope struct {
		Metadata struct {
			Setup *directorSetupMeta `json:"dramaSetup"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(input), &envelope) != nil {
		return nil
	}
	return envelope.Metadata.Setup
}

const setupFirstInstruction = "【本轮任务】按本批全部定稿剧本、立意与全集结构、世界与总谱、素材清单写全剧圣经。逐字搬入世界基准段，按剧本登记人物、场景、关键道具和造型变化。输出 ## 全剧圣经 和 ## 待确认。"
const setupExtendInstruction = "【本轮任务】这是增补回合：在当前全剧圣经基础上，按本批全部剧集补充新出场的人物、场景、关键道具和造型变化；已有条目只在剧本明确改变时修订。输出完整的 ## 全剧圣经 和 ## 待确认。"

func (m *directorSetupMeta) prompt(k int, prior []dramaRunTurn) (string, error) {
	if k < 1 || k > len(m.Batches) {
		return "", errors.New("立项批次超出范围")
	}
	parts := []string{m.Context}
	instruction := setupFirstInstruction
	if k > 1 {
		if len(prior) < k-1 {
			return "", errors.New("前一批全剧圣经缺失")
		}
		bible, ok := drama.SSSBibleText(prior[k-2].Text)
		if !ok {
			return "", errors.New("前一批全剧圣经缺失")
		}
		parts = append(parts, "【当前全剧圣经】\n"+bible)
		instruction = setupExtendInstruction
	}
	for _, id := range m.Batches[k-1] {
		parts = append(parts, m.Scripts[id])
	}
	result := strings.Join(append(parts, instruction), "\n\n")
	if utf8.RuneCountInString(result) > sssMaxPromptRunes {
		return "", fmt.Errorf("全剧立项第 %d 批与前一批圣经合计超过 %d 字", k, sssMaxPromptRunes)
	}
	return result, nil
}

func (s *Service) planDirectorSetup(userID string, p *model.Project, step drama.Step, queue []string) (*dramaStepPlan, error) {
	w, _ := drama.Workflow(p.WorkflowID)
	if w.StoryboardMode != drama.StoryboardModeSSS {
		return nil, BadAuthRequest("全剧立项只适用 SSS 流水线")
	}
	units, err := s.repo.ProjectUnits(p.ID)
	if err != nil {
		return nil, err
	}
	m := &directorSetupMeta{Queue: slices.Clone(queue), Scripts: map[string]string{}}
	var missing []string
	for _, u := range units {
		if u.Kind != model.ProjectUnitKindEpisode {
			continue
		}
		body := chapterPlainText(u.SourceText)
		m.Basis = append(m.Basis, u.ID)
		if strings.TrimSpace(body) == "" {
			missing = append(missing, u.Title+"缺少剧本")
		}
		m.Scripts[u.ID] = "【定稿剧本】" + u.Title + "\n" + body
	}
	if len(m.Basis) == 0 {
		missing = append(missing, "尚无剧集")
	}
	var docs drama.SSSDocs
	for _, d := range []struct {
		kind, label string
		dst         *string
	}{{model.OntologyDocWorld, "尚未完成导演定画风", &docs.World}, {model.OntologyDocConcept, "缺少立意与全集结构", &docs.Concept}} {
		record, err := s.repo.LatestOntologyDocument(p.ID, "", d.kind)
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && strings.TrimSpace(record.Content) == "") {
			missing = append(missing, d.label)
			continue
		}
		if err != nil {
			return nil, err
		}
		*d.dst = record.Content
	}
	if len(missing) > 0 {
		return nil, sssPrecondition(ReasonDirectorSetupNotReady, strings.Join(missing, "；"), map[string]any{"missing": missing})
	}
	usage, skill, err := s.sssStepSkill(p, w, step)
	if err != nil {
		return nil, err
	}
	context, files, err := drama.SSSSkillContext(skill, drama.SSSTurnSetup, nil)
	if err != nil {
		return nil, err
	}
	mats, err := s.sssMaterials(userID, p.ID)
	if err != nil {
		return nil, err
	}
	brief := sssProjectBrief(p, drama.SSSWorkflowKind(p.WorkflowID), drama.SSSUnitMaxSeconds)
	m.Context = context + "\n\n" + drama.SSSMaterialPackage(brief, mats, docs)
	m.Batches, err = m.remainingBatches(0, "")
	if err != nil {
		return nil, err
	}
	var scenes []drama.ShotsScene
	for i, batch := range m.Batches {
		inputs := []string{m.Context}
		for _, id := range batch {
			inputs = append(inputs, id, m.Scripts[id])
		}
		scenes = append(scenes, drama.ShotsScene{ID: videoChainHash(inputs), Heading: fmt.Sprintf("全剧立项 第 %d/%d 批", i+1, len(m.Batches))})
	}
	first, err := m.prompt(1, nil)
	if err != nil {
		return nil, err
	}
	return &dramaStepPlan{usage: usage, skill: skill, scenes: scenes, setup: m, prompt: drama.StepPrompt{Text: first, Files: files}}, nil
}

type directorBibleBasis struct {
	Episodes int  `json:"episodes"`
	Total    int  `json:"total"`
	Legacy   bool `json:"legacy"`
	Outdated bool `json:"outdated"`
}

// 每次增补都以刚写出的圣经重新核算剩余剧集；已经写完的批次保留在任务检查点。
func (m *directorSetupMeta) remainingBatches(done int, previous string) ([][]string, error) {
	if done < 0 || done > len(m.Basis) {
		return nil, errors.New("全剧立项检查点超出剧集范围")
	}
	if done == len(m.Basis) {
		return nil, nil
	}
	prefix := m.Context + "\n\n" + setupFirstInstruction
	if done > 0 {
		bible, ok := drama.SSSBibleText(previous)
		if !ok {
			return nil, errors.New("前一批全剧圣经缺失")
		}
		prefix = m.Context + "\n\n【当前全剧圣经】\n" + bible + "\n\n" + setupExtendInstruction
	}
	budget := (sssMaxPromptRunes - utf8.RuneCountInString(prefix)) * 9 / 10
	var batches [][]string
	size := 0
	for _, id := range m.Basis[done:] {
		n := utf8.RuneCountInString(m.Scripts[id]) + 2
		if n > budget {
			return nil, BadAuthRequest(strings.SplitN(m.Scripts[id], "\n", 2)[0] + "太长，超过单批立项预算")
		}
		if len(batches) == 0 || size+n > budget {
			batches = append(batches, nil)
			size = 0
		}
		i := len(batches) - 1
		batches[i] = append(batches[i], id)
		size += n
	}
	return batches, nil
}

func (s *Service) directorBibleBasis(userID, projectID string, bible *model.ProjectOntologyDocument) (directorBibleBasis, error) {
	var basis directorBibleBasis
	units, err := s.repo.ProjectUnits(projectID)
	if err != nil {
		return basis, err
	}
	var ids []string
	for _, u := range units {
		if u.Kind == model.ProjectUnitKindEpisode {
			ids = append(ids, u.ID)
		}
	}
	basis.Total = len(ids)
	if bible == nil {
		return basis, nil
	}
	task, err := s.repo.TaskForUser(userID, bible.TaskID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		basis.Outdated = true
		return basis, nil
	}
	if err != nil {
		return basis, err
	}
	setup := taskDirectorSetup(task.InputJSON)
	if setup == nil {
		step := taskDramaStep(task.InputJSON)
		basis.Legacy = step != nil && step.Step == drama.StepChapterShots
		if basis.Legacy {
			basis.Episodes = 1
		}
		return basis, nil
	}
	basis.Episodes = len(setup.Basis)
	basis.Outdated = !slices.Equal(ids, setup.Basis)
	for _, u := range units {
		if u.Kind == model.ProjectUnitKindEpisode && (u.UpdatedAt.After(task.CreatedAt) || setup.Scripts[u.ID] != "【定稿剧本】"+u.Title+"\n"+chapterPlainText(u.SourceText)) {
			basis.Outdated = true
		}
	}
	return basis, nil
}
