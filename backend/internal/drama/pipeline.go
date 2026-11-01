package drama

import "fmt"

// 流水线一格的状态。
const (
	PipelineDone    = "done"
	PipelineRunning = "running"
	PipelineStopped = "stopped"
	PipelinePending = "pending"
)

// PipelineFacts 是一个环节的事实（由 app 按产物、任务和资产状态填）：Done 做完了；Running 有任务在跑或在排队；
// Detail 是这一格下面的小字；Problem 是停在这里的具体原因（上次失败、检查有问题、出图失败、没接上的原因），没有为空。
type PipelineFacts struct {
	Done    bool
	Running bool
	Detail  string
	Problem string
	// Action 是停在这里时要用户做的事（按钮文字）；空则按环节给默认的「继续：xx」。
	Action string
	// Manual 表示停在这里要用户自己去处理（导入正文、改分镜问题），点「继续」做不了。
	Manual bool
}

// PipelineStageView 是流水线的一格。
type PipelineStageView struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// PipelineStop 是流水线停在哪、等你做什么。Continuable 表示可以点「继续」由服务端接着开始这一环（出视频、剪辑不行）。
type PipelineStop struct {
	Stage       string `json:"stage"`
	Label       string `json:"label"`
	Reason      string `json:"reason"`
	Action      string `json:"action"`
	Continuable bool   `json:"continuable"`
}

// Pipeline 是概览首屏的流水线：生成模式、各格状态、当前环节和停下的原因（有任务在跑时 Stop 为空）。
type Pipeline struct {
	RunMode      string              `json:"runMode"`
	RunModeLabel string              `json:"runModeLabel"`
	Stages       []PipelineStageView `json:"stages"`
	Current      string              `json:"current,omitempty"`
	Stop         *PipelineStop       `json:"stop,omitempty"`
}

// DerivePipeline 按各环节的事实推出流水线：第一个没做完的是当前环节；有任务在跑就不算停；否则停在当前环节，
// 原因依次取：出视频（要花钱，等人确认）、这一格的具体问题、生成模式在这里停、等你点继续。
func DerivePipeline(runMode string, facts map[string]PipelineFacts) Pipeline {
	out := Pipeline{RunMode: runMode, RunModeLabel: RunModeLabel(runMode), Stages: make([]PipelineStageView, 0, len(PipelineStages))}
	running := false
	for _, def := range PipelineStages {
		f := facts[def.Key]
		view := PipelineStageView{Key: def.Key, Label: def.Label, State: PipelinePending, Detail: f.Detail}
		switch {
		case f.Running:
			view.State = PipelineRunning
			running = true
			if out.Current == "" {
				out.Current = def.Key
			}
		case f.Done:
			view.State = PipelineDone
		case out.Current == "":
			out.Current = def.Key
		}
		out.Stages = append(out.Stages, view)
	}
	if running || out.Current == "" {
		return out
	}
	for i := range out.Stages {
		if out.Stages[i].Key == out.Current {
			out.Stages[i].State = PipelineStopped
		}
	}
	f := facts[out.Current]
	label := PipelineStageLabel(out.Current)
	stop := &PipelineStop{Stage: out.Current, Label: label, Continuable: !f.Manual, Action: firstNonEmptyString(f.Action, "继续："+label)}
	switch {
	case out.Current == StageVideo:
		stop.Reason, stop.Action, stop.Continuable = "等你确认花费", "去制作台出视频", false
	case out.Current == StageCut:
		stop.Reason, stop.Action, stop.Continuable = "视频都出好了，等你剪辑、过门禁后交付", "去交付", false
	case f.Problem != "":
		stop.Reason = f.Problem
	case !ShouldAutoContinue(runMode, out.Current):
		stop.Reason = fmt.Sprintf("生成模式「%s」在这一步前停下，等你点继续", RunModeLabel(runMode))
	default:
		stop.Reason = "等你点继续"
	}
	out.Stop = stop
	return out
}
