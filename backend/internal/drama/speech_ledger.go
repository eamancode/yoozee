package drama

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const StepSpeechExtract = "speech.extract"
const ContractSpeechExtract = "speech.extract@1"

func CountSpeechExtractTurn(text string, turn TurnContext) (int, error) {
	if turn.Turn.AssetSourceText == "" {
		return 0, fmt.Errorf("声音提取缺少完整原文")
	}
	ledger, err := ParseSpeechLedger(text, turn.Turn.AssetSourceText, turn.Turn.Heading)
	if err != nil {
		return 0, err
	}
	return max(1, len(ledger.Entries)), nil
}

type SpeechEntry struct {
	Scene        string `json:"scene"`
	Paragraph    int    `json:"paragraph"`
	ParagraphEnd int    `json:"paragraph_end,omitempty"`
	Speaker      string `json:"speaker"`
	Kind         string `json:"kind"`
	Text         string `json:"text"`
}

type SpeechLedger struct {
	SourceHash   string        `json:"sourceHash"`
	SkillVersion string        `json:"skillVersion"`
	Entries      []SpeechEntry `json:"entries"`
	Script       Script        `json:"script"`
}

func SpeechSourcePrompt(text, title string) string {
	var b strings.Builder
	b.WriteString("从下面完整剧本独立提取全部台词、画外音、旁白。逐字保留，标注场和段号；不要依照已有镜头筛选。\n")
	for _, scene := range SplitScenes(text, title) {
		fmt.Fprintf(&b, "\n【%s %s】\n", scene.ID, scene.Heading)
		for i, line := range scene.Body {
			fmt.Fprintf(&b, "[%d] %s\n", i+1, line)
		}
	}
	return b.String()
}

func ParseSpeechLedger(output, source, title string) (SpeechLedger, error) {
	ledger := SpeechLedger{Entries: []SpeechEntry{}, Script: Script{Title: title, SceneRule: SceneRuleMerged}}
	scenes := SplitScenes(source, title)
	sceneIndex := map[string]Scene{}
	for _, scene := range scenes {
		sceneIndex[scene.ID] = scene
	}
	declaredEmpty := false
	for _, raw := range strings.Split(output, "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var empty struct {
			Empty bool `json:"empty"`
		}
		if json.Unmarshal([]byte(raw), &empty) == nil && empty.Empty {
			if declaredEmpty {
				return ledger, fmt.Errorf("声音台账重复空结果标记")
			}
			declaredEmpty = true
			continue
		}
		var entry SpeechEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return ledger, fmt.Errorf("声音台账不是逐行 JSON: %w", err)
		}
		scene, ok := sceneIndex[entry.Scene]
		end := entry.ParagraphEnd
		if end == 0 {
			end = entry.Paragraph
		}
		if !ok || entry.Paragraph < 1 || end < entry.Paragraph || end > len(scene.Body) {
			return ledger, fmt.Errorf("声音台账出处无效: %s 段%d", entry.Scene, entry.Paragraph)
		}
		if entry.Kind != "dialogue" && entry.Kind != "voiceover" && entry.Kind != "narration" {
			return ledger, fmt.Errorf("声音类型无效: %s", entry.Kind)
		}
		if strings.TrimSpace(entry.Text) == "" || !strings.Contains(strings.Join(scene.Body[entry.Paragraph-1:end], "\n"), entry.Text) {
			return ledger, fmt.Errorf("声音台账改写了原文: %s 段%d", entry.Scene, entry.Paragraph)
		}
		entry.ParagraphEnd = end
		if entry.Speaker == "" && entry.Kind == "narration" {
			entry.Speaker = "旁白"
		}
		if entry.Speaker == "" {
			return ledger, fmt.Errorf("声音台账缺说话人: %s 段%d", entry.Scene, entry.Paragraph)
		}
		for _, raw := range scene.Body[entry.Paragraph:end] {
			beats, _ := sceneBeats([]string{raw})
			for _, beat := range beats {
				if beat.isLine() {
					return ledger, fmt.Errorf("声音台账跨越另一条台词: %s 段%d", entry.Scene, entry.Paragraph)
				}
			}
		}
		for _, prior := range ledger.Entries {
			if prior.Scene == entry.Scene && prior.Paragraph == entry.Paragraph && prior.Text == entry.Text {
				return ledger, fmt.Errorf("声音台账重复记录同一出处")
			}
		}
		ledger.Entries = append(ledger.Entries, entry)
	}
	if len(ledger.Entries) == 0 && !declaredEmpty {
		return ledger, fmt.Errorf("声音台账为空，未声明无发言")
	}
	if len(ledger.Entries) > 0 && declaredEmpty {
		return ledger, fmt.Errorf("声音台账同时声明有发言和无发言")
	}
	for _, scene := range scenes {
		entries := []SpeechEntry{}
		for _, entry := range ledger.Entries {
			if entry.Scene == scene.ID {
				entries = append(entries, entry)
			}
		}
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].Paragraph < entries[j].Paragraph })
		beats := []ScriptBeat{}
		characters := []string{}
		for i, line := range scene.Body {
			covered := false
			for _, entry := range entries {
				if i+1 >= entry.Paragraph && i+1 <= entry.ParagraphEnd {
					covered = true
				}
				if entry.Paragraph != i+1 {
					continue
				}
				kind := "line"
				if entry.Kind != "dialogue" {
					kind = "vo"
				}
				beats = append(beats, ScriptBeat{Kind: kind, Speaker: entry.Speaker, Text: entry.Text, Tag: "原文事实", SourceLine: i + 1})
				if entry.Kind != "narration" && !containsString(characters, entry.Speaker) {
					characters = append(characters, entry.Speaker)
				}
			}
			if !covered {
				beats = append(beats, ScriptBeat{Kind: "action", Text: line, Tag: "原文事实", SourceLine: i + 1})
			}
		}
		// Explicit dialogue is a completeness floor; a model cannot erase source speech.
		for paragraph, raw := range scene.Body {
			baseline, _ := sceneBeats([]string{raw})
			for _, beat := range baseline {
				if !beat.isLine() {
					continue
				}
				found := false
				for _, entry := range entries {
					if entry.Paragraph <= paragraph+1 && entry.ParagraphEnd >= paragraph+1 && entry.Speaker == beat.Speaker && strings.Contains(normLine(entry.Text), normLine(beat.Text)) && (beat.Kind != "vo" || entry.Kind != "dialogue") {
						found = true
						break
					}
				}
				if !found {
					return ledger, fmt.Errorf("声音台账遗漏原文台词: %s %s", scene.ID, beat.Speaker)
				}
			}
		}
		ledger.Script.Scenes = append(ledger.Script.Scenes, ScriptScene{ID: scene.ID, Heading: scene.Heading, Characters: characters, Beats: beats})
	}
	AssignBeatIDs(&ledger.Script)
	return ledger, nil
}
