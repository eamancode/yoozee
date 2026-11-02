package app

import (
	"encoding/json"
	"testing"

	"yingce/backend/internal/drama"
)

func TestRepackRebindsSavedDialogueAfterOrdinalChanges(t *testing.T) {
	previous := drama.Script{Scenes: []drama.ScriptScene{{ID: "E01S01", Beats: []drama.ScriptBeat{{ID: "E01S01L01", Kind: "line", Speaker: "△杯内微距", Text: "橙色漩涡卷起来。"}, {ID: "E01S01L02", Kind: "line", Speaker: "阿婆", Text: "这么快？"}}}}}
	saved, _ := json.Marshal(previous)
	rows := []json.RawMessage{json.RawMessage(`{"plotDescription":"阿婆看杯子","spec":{"id":"E01S01-01","scene":"E01S01","est":3,"lines":[{"line_id":"E01S01L02","tone":"惊讶"}],"extras":{"custom":"preserved"}}}`)}
	rebound, err := repackDialogueText(rows, string(saved))
	if err != nil {
		t.Fatal(err)
	}
	current := drama.ScriptFromText("第一场｜果汁摊-日-内\n△杯内微距：橙色漩涡卷起来。\n阿婆：这么快？", "t")
	var row drama.StoryboardRow
	if err := json.Unmarshal(rebound[0], &row); err != nil {
		t.Fatal(err)
	}
	result := drama.RowsToShotlist([]drama.StoryboardRow{row}, &current, "")
	line := result.Shotlist.Shots[0].Lines[0]
	if line.LineID != "E01S01L01" || line.Tone != "惊讶" {
		t.Fatalf("应按原话重绑定，不信旧序号：%+v", line)
	}
	if string(rows[0]) == string(rebound[0]) || result.Shotlist.Shots[0].Extras["custom"] != "preserved" {
		t.Fatal("只替换引用，保留用户其他字段")
	}
	changed := drama.ScriptFromText("第一场｜果汁摊-日-内\n阿婆：一点也不快。", "t")
	result = drama.RowsToShotlist([]drama.StoryboardRow{row}, &changed, "")
	if result.Shotlist.Shots[0].Lines[0].LineID != "" {
		t.Fatal("原文已改时不能挪用同序号新对白")
	}
}
