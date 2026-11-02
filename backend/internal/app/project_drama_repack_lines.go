package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"yingce/backend/internal/drama"
)

// Rebind from the saved script text rather than trusting an ordinal line ID after
// the source/parser changes. RowsToShotlist still requires the current script to match.
func repackDialogueText(rows []json.RawMessage, scriptJSON string) ([]json.RawMessage, error) {
	if strings.TrimSpace(scriptJSON) == "" {
		return rows, nil
	}
	var script drama.Script
	if err := json.Unmarshal([]byte(scriptJSON), &script); err != nil {
		return nil, fmt.Errorf("镜头表原台词表损坏: %w", err)
	}
	beats := map[string]drama.ScriptBeat{}
	for _, scene := range script.Scenes {
		for _, beat := range scene.Beats {
			if beat.Kind == "line" || beat.Kind == "vo" {
				beats[beat.ID] = beat
			}
		}
	}
	out := make([]json.RawMessage, len(rows))
	for i, raw := range rows {
		out[i] = raw
		var row map[string]json.RawMessage
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		var spec map[string]json.RawMessage
		if json.Unmarshal(row["spec"], &spec) != nil {
			continue
		}
		var lines []map[string]json.RawMessage
		if json.Unmarshal(spec["lines"], &lines) != nil {
			continue
		}
		changed := false
		for _, line := range lines {
			var id, text string
			_ = json.Unmarshal(line["line_id"], &id)
			_ = json.Unmarshal(line["text"], &text)
			if beat, ok := beats[id]; ok && strings.TrimSpace(text) == "" {
				line["text"], _ = json.Marshal(beat.Text)
				line["speaker"], _ = json.Marshal(beat.Speaker)
				delete(line, "line_id")
				changed = true
			}
		}
		if changed {
			spec["lines"], _ = json.Marshal(lines)
			row["spec"], _ = json.Marshal(spec)
			out[i], _ = json.Marshal(row)
		}
	}
	return out, nil
}
