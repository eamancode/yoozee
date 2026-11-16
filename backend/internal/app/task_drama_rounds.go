package app

import (
	"encoding/json"
	"strings"

	"yingce/backend/internal/model"
)

// TaskDramaRounds 是任务列表里导演分镜（SSS、本体论）一次运行的轮次摘要：已保存几轮、总轮数（规划 / 切分前是建任务时的估计）、
// 每一轮的种类和标题。只从任务已存的 metadata.dramaRun 和 result.dramaRun 读，不下发提示词、模型输出或其他 metadata。
type TaskDramaRounds struct {
	Flow int `json:"flow,omitempty"`
	// Mode 是 sss 或 ontology。
	Mode      string `json:"mode"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
	// Repair 是 SSS 单段返修（固定「返修 + 编译」两轮）。
	Repair bool             `json:"repair,omitempty"`
	Turns  []TaskDramaRound `json:"turns"`
}

// TaskDramaRound 是已保存的一轮：Kind 是 SSS 的 setup / plan / direct / cut / card / compile / review / repair（本体论同理用它自己的种类）。
type TaskDramaRound struct {
	Kind    string `json:"kind"`
	Heading string `json:"heading,omitempty"`
}

func taskDramaRounds(task model.Task) *TaskDramaRounds {
	if task.MediaStage != "" {
		return nil
	}
	mode := ""
	switch {
	case strings.Contains(task.InputJSON, `"dramaSetup"`):
		mode = "setup"
	case strings.Contains(task.InputJSON, `"dramaSSS"`):
		mode = "sss"
	case strings.Contains(task.InputJSON, `"dramaOntology"`):
		mode = "ontology"
	default:
		return nil
	}
	run := taskDramaRun(task.InputJSON)
	if run == nil {
		return nil
	}
	rounds := &TaskDramaRounds{Mode: mode, Total: run.Turns, Turns: []TaskDramaRound{}}
	if mode == "sss" {
		if meta := taskDramaSSS(task.InputJSON); meta != nil {
			rounds.Repair = meta.Repair != nil
			rounds.Flow = meta.Flow
		}
	}
	if !strings.Contains(task.ResultJSON, `"dramaRun"`) {
		return rounds
	}
	// 只取轮次的种类和标题；各轮的提示词、输出不进列表。
	var result struct {
		DramaRun *struct {
			Total     int `json:"total"`
			Completed int `json:"completed"`
			Turns     []struct {
				Heading  string `json:"heading"`
				SSS      string `json:"sss"`
				Ontology string `json:"ontology"`
			} `json:"turns"`
		} `json:"dramaRun"`
	}
	if json.Unmarshal([]byte(task.ResultJSON), &result) != nil || result.DramaRun == nil {
		return rounds
	}
	rounds.Completed = result.DramaRun.Completed
	rounds.Total = max(result.DramaRun.Total, result.DramaRun.Completed)
	for _, turn := range result.DramaRun.Turns {
		kind := turn.SSS
		if mode == "ontology" {
			kind = turn.Ontology
		}
		rounds.Turns = append(rounds.Turns, TaskDramaRound{Kind: kind, Heading: turn.Heading})
	}
	return rounds
}
