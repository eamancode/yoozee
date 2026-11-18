package drama

import (
	"encoding/json"
	"slices"
	"strings"
)

// AssetVisualDelta describes visible, persistent changes to the default look.
// Evidence is retained separately from the normalized comparison key.
type AssetVisualDelta struct {
	AgeStage     string `json:"age_stage,omitempty"`
	Clothing     string `json:"clothing,omitempty"`
	Hair         string `json:"hair,omitempty"`
	VisibleState string `json:"visible_state,omitempty"`
	Evidence     string `json:"evidence,omitempty"`
}

func (d *AssetVisualDelta) Key() string {
	if d == nil {
		return ""
	}
	fields := []string{AssetNameKey(d.AgeStage), AssetNameKey(d.Clothing), AssetNameKey(d.Hair), AssetNameKey(d.VisibleState)}
	if !slices.ContainsFunc(fields, func(field string) bool { return field != "" }) {
		return ""
	}
	return strings.Join(fields, "|")
}

// Matches compares the attributes actually specified in the new observation.
// An omitted field carries no new information about the existing look.
func (d *AssetVisualDelta) Matches(existing *AssetVisualDelta) bool {
	if d == nil || existing == nil || d.Key() == "" {
		return false
	}
	pairs := [][2]string{{d.AgeStage, existing.AgeStage}, {d.Clothing, existing.Clothing}, {d.Hair, existing.Hair}, {d.VisibleState, existing.VisibleState}}
	for _, pair := range pairs {
		if proposed := AssetNameKey(pair[0]); proposed != "" && proposed != AssetNameKey(pair[1]) {
			return false
		}
	}
	return true
}

type KnownAssetLook struct {
	ID    string            `json:"variant_id"`
	Label string            `json:"label"`
	Delta *AssetVisualDelta `json:"visual_delta,omitempty"`
	Look  string            `json:"look,omitempty"`
}

// ContinuityAssetContext prioritizes references supported by this unit before
// including compact project-wide context. The server still resolves all IDs.
func ContinuityAssetContext(source string, known []AssetsKnown) []AssetsKnown {
	script := AssetNameKey(source)
	type ranked struct {
		asset AssetsKnown
		score int
	}
	rows := make([]ranked, 0, len(known))
	for _, asset := range known {
		score := 0
		for _, name := range append([]string{asset.Name}, asset.Aliases...) {
			key := AssetNameKey(name)
			if n := len([]rune(key)); n >= 2 && strings.Contains(script, key) {
				score = max(score, 100+min(n, 40))
			}
		}
		if asset.Category == AssetCharacter {
			score++
		}
		asset.Relevant = score >= 100
		rows = append(rows, ranked{asset: asset, score: score})
	}
	slices.SortStableFunc(rows, func(a, b ranked) int { return b.score - a.score })
	out := make([]AssetsKnown, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.asset)
	}
	return out
}

func compactKnownLook(look KnownAssetLook) string {
	if look.Delta != nil {
		delta := *look.Delta
		delta.Evidence = ""
		if b, err := json.Marshal(delta); err == nil {
			return look.Label + " " + look.ID + " " + jsTruncate(string(b), 160)
		}
	}
	return look.Label + " " + look.ID + " " + jsTruncate(jsCompact(look.Look), 82)
}
