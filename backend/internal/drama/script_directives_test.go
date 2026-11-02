package drama

import "testing"

func TestScriptStageDirectionsAndEndCardAreNotDialogue(t *testing.T) {
	script := ScriptFromText("第一场｜果汁摊-日-内\n△杯内微距：橙色漩涡卷起来。\n阿婆：这么快？\n落版字幕：随行鲜，39.9 元。\n林深：（很轻）我一直记着。", "测试")
	if script.LineCount() != 2 {
		t.Fatalf("动作冒号和落版不能变成台词：%+v", script.Scenes)
	}
	beats := script.Scenes[0].Beats
	if beats[0].Kind != "action" || beats[2].Kind != "action" || beats[3].Speaker != "林深" {
		t.Fatalf("动作、文字和人物对白必须保留：%+v", beats)
	}
}

func TestSectionLineMatchesNestedStraightAndCurlyQuotes(t *testing.T) {
	text := `（站起来，很轻）阿婆，那天您说"一杯果汁而已"。我那天，是靠着这杯果汁，走回学校的。`
	beats := map[string]SectionBeat{"E01S01L01": {ID: "E01S01L01", Kind: "line", Speaker: "林深", Text: text}}
	if got := matchSectionLine(sectionLine{speaker: "林深", text: "阿婆，那天您说“一杯果汁而已"}, []string{"E01S01L01"}, beats); got != "E01S01L01" {
		t.Fatalf("只改变引号样式的对白应恢复原文：%q", got)
	}
	if got := matchSectionLine(sectionLine{speaker: "林深", text: "我已经忘了"}, []string{"E01S01L01"}, beats); got != "" {
		t.Fatal("不能把改写的对白当成原文")
	}
}
