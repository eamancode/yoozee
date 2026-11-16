package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

// 任务列表的导演分镜轮次摘要：只带已保存几轮、总轮数和每轮的种类、标题；各轮提示词、模型输出不进列表。
func TestTaskDramaRoundsSummary(t *testing.T) {
	input := `{"metadata":{"dramaRun":{"runId":"run-1","step":"chapter.shots","turns":10,"resumeFrom":1},"dramaSSS":{"estimatedTurns":10}}}`
	result := `{"mode":"text","dramaRun":{"runId":"run-1","total":8,"completed":3,"turns":[` +
		`{"index":1,"heading":"本集设计与台词定稿","sss":"plan","rows":1,"attempts":1,"prompt":"SECRET-PROMPT","text":"SECRET-OUTPUT","taskId":"t1"},` +
		`{"index":2,"heading":"讲戏本","sss":"direct","rows":1,"attempts":1,"text":"SECRET-OUTPUT","taskId":"t1"},` +
		`{"index":3,"heading":"整场镜头表与单元总表","sss":"cut","rows":1,"attempts":1,"text":"SECRET-OUTPUT","taskId":"t1"}]}}`
	summary := taskSummaryForOutput(model.Task{ID: "t1", Type: "canvas_text", Status: model.TaskStatusRunning, InputJSON: input, ResultJSON: result})
	rounds := summary.DramaRounds
	if rounds == nil || rounds.Mode != "sss" || rounds.Completed != 3 || rounds.Total != 8 || rounds.Repair || len(rounds.Turns) != 3 {
		t.Fatalf("rounds = %+v", rounds)
	}
	if rounds.Turns[2] != (TaskDramaRound{Kind: "cut", Heading: "整场镜头表与单元总表"}) {
		t.Fatalf("turn = %+v", rounds.Turns[2])
	}
	encoded, _ := json.Marshal(summary)
	if strings.Contains(string(encoded), "SECRET") || strings.Contains(string(encoded), `"inputJson"`) || strings.Contains(string(encoded), `"resultJson"`) {
		t.Fatalf("task list leaked run content: %s", encoded)
	}

	// 第一轮写完前：结果里还没有进度，总轮数是建任务时的估计。
	early := taskDramaRounds(model.Task{InputJSON: input})
	if early == nil || early.Total != 10 || early.Completed != 0 || len(early.Turns) != 0 {
		t.Fatalf("early = %+v", early)
	}
	// 单段返修。
	repair := taskDramaRounds(model.Task{InputJSON: `{"metadata":{"dramaRun":{"runId":"r","turns":2},"dramaSSS":{"repair":{"unit":"E1-U2"}}}}`})
	if repair == nil || !repair.Repair || repair.Total != 2 {
		t.Fatalf("repair = %+v", repair)
	}
	// 本体论用它自己的轮次种类。
	ontology := taskDramaRounds(model.Task{InputJSON: `{"metadata":{"dramaRun":{"runId":"r","turns":12},"dramaOntology":{"estimatedTurns":12}}}`, ResultJSON: `{"dramaRun":{"total":9,"completed":2,"turns":[{"heading":"项目设定","ontology":"project"},{"heading":"节奏总表","ontology":"plan"}]}}`})
	if ontology == nil || ontology.Mode != "ontology" || ontology.Total != 9 || ontology.Turns[1].Kind != "plan" {
		t.Fatalf("ontology = %+v", ontology)
	}
	// 普通逐场续写、出图任务、媒体阶段的任务没有摘要。
	for _, task := range []model.Task{
		{InputJSON: `{"metadata":{"dramaRun":{"runId":"r","turns":3}}}`},
		{InputJSON: `{"mode":"image"}`},
		{InputJSON: input, MediaStage: "download"},
	} {
		if got := taskDramaRounds(task); got != nil {
			t.Fatalf("%s: unexpected rounds %+v", task.InputJSON, got)
		}
	}
}
