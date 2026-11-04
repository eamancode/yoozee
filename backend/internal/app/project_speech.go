package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func speechSourceHash(source string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(source))) }

func unitSpeechLedger(unit *model.ProjectUnit) *drama.SpeechLedger {
	var ledger drama.SpeechLedger
	if json.Unmarshal([]byte(unit.SpeechLedgerJSON), &ledger) != nil || ledger.SourceHash != speechSourceHash(unit.SourceText) {
		return nil
	}
	return &ledger
}

func (s *Service) GetUnitSpeechLedger(userID, projectID, unitID string) (map[string]any, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	unit, err := s.repo.ProjectUnit(projectID, unitID)
	if err != nil {
		return nil, err
	}
	ledger := unitSpeechLedger(unit)
	return map[string]any{"ready": ledger != nil, "ledger": ledger}, nil
}

func (s *Service) planSpeechExtract(project *model.Project, unit *model.ProjectUnit, step drama.Step) (*dramaStepPlan, error) {
	source := chapterPlainText(unit.SourceText)
	if strings.TrimSpace(source) == "" {
		return nil, BadAuthRequest("当前单元没有剧本正文")
	}
	usage, skill, err := s.dramaStepSkill(project, step, nil)
	if err != nil {
		return nil, err
	}
	return speechExtractPlan(project, unit, usage, skill)
}

func speechExtractPlan(project *model.Project, unit *model.ProjectUnit, usage DramaStepUsage, skill drama.StepSkill) (*dramaStepPlan, error) {
	step, _ := drama.StepByID(drama.StepSpeechExtract)
	source := chapterPlainText(unit.SourceText)
	prompt, files, ok := drama.StepSkillPrompt(skill, drama.SpeechSourcePrompt(source, unit.Title), step.SkillBudgetChars, step.SkillLinkedFiles)
	if !ok {
		return nil, BadAuthRequest("声音台账技能缺少 SKILL.md")
	}
	contract, err := drama.BuiltinContract(drama.ContractSpeechExtract)
	if err != nil {
		return nil, err
	}
	return &dramaStepPlan{usage: usage, skill: skill, prompt: drama.StepPrompt{Text: prompt + "\n\n" + contract.Text(contract.DefaultRequirement(), drama.ContractTextOptions{SkillShaped: true}), Files: files}, record: map[string]any{"speechSourceHash": speechSourceHash(unit.SourceText)}, scenes: []drama.ShotsScene{{Heading: unit.Title, AssetSourceText: source}}}, nil
}

func (s *Service) applySpeechExtractTask(task model.Task, result map[string]interface{}) (map[string]any, error) {
	target, err := taskDramaStepTarget(task)
	if err != nil {
		return nil, err
	}
	unit, err := s.repo.ProjectUnit(target.ProjectID, target.UnitID)
	if err != nil {
		return nil, err
	}
	var input struct {
		Hash string `json:"speechSourceHash"`
	}
	if json.Unmarshal([]byte(task.InputJSON), &input) != nil || input.Hash != speechSourceHash(unit.SourceText) {
		return nil, failedPrecondition("剧本已变化，请重新提取声音台账")
	}
	text, _ := result["text"].(string)
	ledger, err := drama.ParseSpeechLedger(text, chapterPlainText(unit.SourceText), unit.Title)
	if err != nil {
		return nil, err
	}
	ledger.SourceHash = input.Hash
	if meta := taskDramaStep(task.InputJSON); meta != nil {
		ledger.SkillVersion = meta.VersionID
	}
	encoded, err := json.Marshal(ledger)
	if err != nil {
		return nil, err
	}
	if err = s.repo.SaveUnitSpeechLedger(task.UserID, target.ProjectID, unit.ID, unit.SourceText, string(encoded)); err != nil {
		return nil, err
	}
	out := map[string]any{"step": drama.StepSpeechExtract, "status": "written", "unitId": unit.ID, "lines": len(ledger.Entries)}
	if pipeline := taskDramaPipeline(task.InputJSON); pipeline != nil && len(pipeline.Queue) > 0 {
		latest, err := s.repo.TaskForUser(task.UserID, task.ID)
		if err != nil || latest.Status == model.TaskStatusCancelled {
			return out, nil
		}
		if exists, err := s.repo.TaskChainedFromExists(task.UserID, task.ID); err != nil || exists {
			return out, nil
		}
		text, _, err := s.dramaTaskText(task)
		if err != nil {
			out["chained"] = &DramaChainedTask{Step: drama.StepChapterShots, Error: err.Error()}
		} else {
			out["chained"] = s.startShotsQueue(task.UserID, target.ProjectID, text, task.ID, pipeline.Queue)
		}
	}
	return out, nil
}
