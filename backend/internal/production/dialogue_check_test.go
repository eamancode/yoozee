package production

import (
	"reflect"
	"testing"
)

func TestScriptCoverageChecksSpokenLinesAndSkipsScreenText(t *testing.T) {
	source := "第五场｜市场\n△杯内微距：橙色漩涡卷起来。\n△画面掠过标着「源头工厂直发」的包装。\n落版字幕：三十九块九，全国包邮。\n阿婆（画外）：又跳了。\n旁白：她始终记得那一晚。\n林深：我记住了。"
	want := []string{"又跳了。", "她始终记得那一晚。", "我记住了。"}
	if got := SourceDialogueLines(source, SourceScript); !reflect.DeepEqual(got, want) {
		t.Fatalf("spoken lines = %#v; want %#v", got, want)
	}
	if got := SourceDialogueLines("他说：「我还记得那一晚。」", SourceNovel); len(got) != 1 || got[0] != "我还记得那一晚。" {
		t.Fatalf("novel quotation did not survive: %#v", got)
	}
}
