package drama

import (
	"encoding/json"
	"strings"
	"testing"
)

const speechScript = "第一场｜小摊-夜-外\n△暴雨。\n阿婆：（画外，声音很轻）又跳了。\n阿婆：嗯。\n阿婆：嗯。\n旁白：五年前。\n林深：谢谢您。\n△字幕：随行鲜。"

func speechEntriesText() string {
	rows := []SpeechEntry{
		{Scene: "E01S01", Paragraph: 2, Speaker: "阿婆", Kind: "voiceover", Text: "又跳了。"},
		{Scene: "E01S01", Paragraph: 3, Speaker: "阿婆", Kind: "dialogue", Text: "嗯。"},
		{Scene: "E01S01", Paragraph: 4, Speaker: "阿婆", Kind: "dialogue", Text: "嗯。"},
		{Scene: "E01S01", Paragraph: 5, Speaker: "旁白", Kind: "narration", Text: "五年前。"},
		{Scene: "E01S01", Paragraph: 6, Speaker: "林深", Kind: "dialogue", Text: "谢谢您。"},
	}
	var lines []string
	for _, row := range rows {
		encoded, _ := json.Marshal(row)
		lines = append(lines, string(encoded))
	}
	return strings.Join(lines, "\n")
}

func TestSpeechLedgerIndependentSourceAndRepeatedSpeech(t *testing.T) {
	ledger, err := ParseSpeechLedger(speechEntriesText(), speechScript, "随行鲜")
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Entries) != 5 || ledger.Script.LineCount() != 5 {
		t.Fatalf("lost source speech: %+v", ledger)
	}
	beats := ledger.Script.Scenes[0].Beats
	if beats[1].Kind != "vo" || beats[1].Text != "又跳了。" || beats[4].Kind != "vo" {
		t.Fatalf("wrong speech classification: %+v", beats)
	}
	if beats[2].ID == beats[3].ID {
		t.Fatal("repeated replies must have distinct source IDs")
	}
	report := CheckShots(Shotlist{Shots: []Shot{}}, CheckContext{Script: &ledger.Script})
	missing := 0
	for _, item := range report.Items {
		if item.Rule == "R-SHOT-001" {
			missing++
		}
	}
	if missing != 5 {
		t.Fatalf("empty shots must still report all five missing lines: %+v", report)
	}
}

func TestSpeechLedgerRejectsRewriteAndMissingRepeatedOccurrence(t *testing.T) {
	for _, output := range []string{strings.Replace(speechEntriesText(), "又跳了。", "不跳了。", 1), strings.Replace(speechEntriesText(), "\"paragraph\":4", "\"paragraph\":3", 1), `{"empty":true}`} {
		if _, err := ParseSpeechLedger(output, speechScript, "随行鲜"); err == nil {
			t.Fatal("invalid extraction accepted")
		}
	}
}

func TestSpeechSourcePromptIncludesLongScriptTail(t *testing.T) {
	source := strings.Repeat("△雨一直下。\n", 25000) + "旁白：最后一句必须保留。"
	prompt := SpeechSourcePrompt(source, "长剧本")
	if !strings.Contains(prompt, "最后一句必须保留。") {
		t.Fatal("full source was truncated")
	}
}

func TestSpeechLedgerCrossLineAndQuotedCue(t *testing.T) {
	source := "第一场｜小摊-夜-外\n林深：（画外）“谢谢。”\n旁白：我记得那晚，\n雨一直下。"
	output := `{"scene":"E01S01","paragraph":1,"speaker":"林深","kind":"voiceover","text":"谢谢。"}` + "\n" + `{"scene":"E01S01","paragraph":2,"paragraph_end":3,"speaker":"旁白","kind":"narration","text":"我记得那晚，\n雨一直下。"}`
	ledger, err := ParseSpeechLedger(output, source, "随行鲜")
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Script.Scenes[0].Characters) != 1 || ledger.Script.Scenes[0].Characters[0] != "林深" || ledger.Script.LineCount() != 2 {
		t.Fatalf("cross line script: %+v", ledger)
	}
	if _, err := CountSpeechExtractTurn(`not json`, TurnContext{Turn: ShotsScene{AssetSourceText: source}}); err == nil {
		t.Fatal("invalid turn accepted")
	}
}

func TestSpeechLedgerEmptyAndOffscreenClassification(t *testing.T) {
	if _, err := ParseSpeechLedger("", "△纯动作。", "无对白"); err == nil {
		t.Fatal("silent blank accepted")
	}
	if _, err := ParseSpeechLedger(`{"empty":true}`, "△纯动作。", "无对白"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSpeechLedger(strings.Replace(speechEntriesText(), `"kind":"voiceover"`, `"kind":"dialogue"`, 1), speechScript, "随行鲜"); err == nil {
		t.Fatal("explicit offscreen cue lost")
	}
}
