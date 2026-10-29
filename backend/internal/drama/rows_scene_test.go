package drama

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// 一章 120 场：第 100 场以后的场号是三位（E01S100），台词编号 E01S100L01。镜头的场跟台词走时按真实场号取，
// 不能截成前 6 个字符（E01S10）把第 100 场以后的镜头装进第 10 场。
func TestRowsSceneFromLineIDBeyondScene99(t *testing.T) {
	var b strings.Builder
	places := []string{"内·病房走廊·夜", "内·电梯口·夜"}
	for i := 1; i <= 120; i++ {
		fmt.Fprintf(&b, "## %s\n\n林小满：第%d场的台词。\n\n", places[(i-1)%2], i)
	}
	script := ScriptFromText(b.String(), "长章")
	if len(script.Scenes) != 120 || script.Scenes[99].ID != "E01S100" {
		t.Fatalf("120 场、第 100 场编号 E01S100：%d %s", len(script.Scenes), script.Scenes[99].ID)
	}
	var rows []StoryboardRow
	for _, n := range []int{10, 100, 120} {
		// 模型把场号写错成第 1 场，台词对得上第 n 场：场跟台词走。
		spec, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("E01S01-%02d", n), "scene": "E01S01", "est": 3, "size": "MS", "action": "林小满抬头。", "focus": "林小满",
			"lines": []map[string]any{{"speaker": "林小满", "text": fmt.Sprintf("第%d场的台词。", n)}}})
		rows = append(rows, StoryboardRow{Spec: spec})
	}
	result := RowsToShotlist(rows, &script, "")
	for i, want := range []string{"E01S10", "E01S100", "E01S120"} {
		sh := result.Shotlist.Shots[i]
		if sh.Scene != want || len(sh.Lines) != 1 || sh.Lines[0].LineID != want+"L01" {
			t.Fatalf("第 %d 镜应在 %s：scene=%s lines=%+v", i+1, want, sh.Scene, sh.Lines)
		}
	}
}

// 第 100 集以后的段按真实集号查目标片长（不能读成第 10 集）。
func TestSegmentsEpisodeThreeDigits(t *testing.T) {
	if got := SegmentsEpisode([]Segment{{Scene: "E100S01"}}); got != 100 {
		t.Fatalf("E100S01 是第 100 集：%d", got)
	}
	if got := SegmentsEpisode([]Segment{{Scene: "E07S12"}}); got != 7 {
		t.Fatalf("E07S12 是第 7 集：%d", got)
	}
}
