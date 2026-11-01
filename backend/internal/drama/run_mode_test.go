package drama

import (
	"strings"
	"testing"
)

// 出视频、剪辑在任何生成模式下都不自动开始：视频要花钱，必须由人带着预估花费点。
func TestShouldAutoContinueNeverStartsVideo(t *testing.T) {
	for _, mode := range append(append([]string{}, RunModes...), "", "full", "step") {
		for _, stage := range []string{StageVideo, StageCut} {
			if ShouldAutoContinue(mode, stage) {
				t.Fatalf("生成模式 %q 自动开始了 %s", mode, stage)
			}
		}
	}
}

func TestShouldAutoContinueByMode(t *testing.T) {
	cases := map[string]map[string]bool{
		RunUntilVideo:       {StageScript: true, StageStyle: true, StagePalette: true, StageAssets: true, StageShots: true, StagePack: true},
		RunReviewFromAssets: {StageScript: true, StageStyle: true, StagePalette: true, StageAssets: false, StageShots: false, StagePack: false},
		RunEveryStep:        {StageScript: false, StageStyle: false, StagePalette: false, StageAssets: false, StageShots: false, StagePack: false},
	}
	for mode, stages := range cases {
		for stage, want := range stages {
			if got := ShouldAutoContinue(mode, stage); got != want {
				t.Fatalf("%s 接 %s = %v，应为 %v", mode, stage, got, want)
			}
		}
	}
	// 不认识的生成模式什么都不接。
	if ShouldAutoContinue("", StageScript) || ShouldAutoContinue("full", StagePalette) {
		t.Fatal("不认识的生成模式不该自动接")
	}
}

func TestDerivePipelineStopsBeforeVideo(t *testing.T) {
	done := PipelineFacts{Done: true}
	facts := map[string]PipelineFacts{StageScript: done, StageStyle: done, StagePalette: done, StageAssets: done, StageShots: done, StagePack: done}
	p := DerivePipeline(RunUntilVideo, facts)
	if p.Current != StageVideo || p.Stop == nil || p.Stop.Stage != StageVideo || p.Stop.Continuable {
		t.Fatalf("装段完成后应停在出视频、不能点继续：%+v %+v", p, p.Stop)
	}
	if p.Stop.Label+"："+p.Stop.Reason != "出视频：等你确认花费" {
		t.Fatalf("停下的原因：%q", p.Stop.Label+"："+p.Stop.Reason)
	}
	for _, stage := range p.Stages[:6] {
		if stage.State != PipelineDone {
			t.Fatalf("%s 应是做完了：%s", stage.Key, stage.State)
		}
	}
	if p.Stages[6].State != PipelineStopped || p.Stages[7].State != PipelinePending {
		t.Fatalf("出视频停住、剪辑待做：%+v", p.Stages[6:])
	}
}

func TestDerivePipelineRunningHasNoStop(t *testing.T) {
	facts := map[string]PipelineFacts{StageScript: {Done: true}, StageStyle: {Running: true}}
	p := DerivePipeline(RunUntilVideo, facts)
	if p.Stop != nil || p.Current != StageStyle || p.Stages[1].State != PipelineRunning {
		t.Fatalf("有任务在跑时不算停：%+v", p)
	}
}

func TestDerivePipelineStopReasons(t *testing.T) {
	done := PipelineFacts{Done: true}
	// 资产我要过目：场次色卡做完后停在资产，写明是生成模式停的。
	p := DerivePipeline(RunReviewFromAssets, map[string]PipelineFacts{StageScript: done, StageStyle: done, StagePalette: done})
	if p.Stop == nil || p.Stop.Stage != StageAssets || !p.Stop.Continuable || !strings.Contains(p.Stop.Reason, "资产我要过目") {
		t.Fatalf("资产我要过目应停在资产：%+v", p.Stop)
	}
	// 具体问题优先于生成模式。
	p = DerivePipeline(RunUntilVideo, map[string]PipelineFacts{StageScript: done, StageStyle: {Problem: "上次导演定画风失败：超时"}})
	if p.Stop == nil || p.Stop.Stage != StageStyle || p.Stop.Reason != "上次导演定画风失败：超时" {
		t.Fatalf("应写明失败原因：%+v", p.Stop)
	}
	// 要用户自己处理的不能点继续。
	p = DerivePipeline(RunUntilVideo, map[string]PipelineFacts{StageScript: {Problem: "还没有正文", Manual: true, Action: "去章节页"}})
	if p.Stop == nil || p.Stop.Continuable || p.Stop.Action != "去章节页" {
		t.Fatalf("没有正文时要去章节页：%+v", p.Stop)
	}
	// 跑到出视频前、什么都没开始：等你点继续。
	p = DerivePipeline(RunUntilVideo, nil)
	if p.Stop == nil || p.Stop.Stage != StageScript || p.Stop.Reason != "等你点继续" {
		t.Fatalf("没开始时停在拆剧本：%+v", p.Stop)
	}
}
