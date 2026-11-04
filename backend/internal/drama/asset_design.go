package drama

import "slices"

// AssetDesign preserves the attachment's evidence, cross references and chronology
// separately from the single paragraph used to describe the default appearance.
type AssetDesign struct {
	ProductionPriority string          `json:"production_priority,omitempty"`
	BodyDescription    string          `json:"body_description,omitempty"`
	Role               string          `json:"role,omitempty"`
	IdentityAppearance string          `json:"identity_appearance,omitempty"`
	Clothing           string          `json:"clothing,omitempty"`
	PropDesign         string          `json:"prop_design,omitempty"`
	SpatialLayout      string          `json:"spatial_layout,omitempty"`
	Characters         []string        `json:"characters,omitempty"`
	Props              []string        `json:"props,omitempty"`
	Environments       []string        `json:"environments,omitempty"`
	Evidence           []AssetEvidence `json:"evidence,omitempty"`
	States             []AssetState    `json:"states,omitempty"`
}

type AssetEvidence struct {
	Kind     string `json:"kind"`
	Location string `json:"location"`
	Text     string `json:"text"`
}

type AssetState struct {
	Location string `json:"location"`
	Trigger  string `json:"trigger"`
	State    string `json:"state"`
}

// MergeAssetDesign adds later-scene evidence and state changes without replacing
// the initial design or dropping earlier source references.
func MergeAssetDesign(first, next *AssetDesign) *AssetDesign {
	if first == nil {
		return next
	}
	if next == nil {
		return first
	}
	out := *first
	if out.BodyDescription == "" {
		out.BodyDescription = next.BodyDescription
	}
	priority := map[string]int{"": 0, "low": 1, "medium": 2, "high": 3}
	if priority[next.ProductionPriority] > priority[out.ProductionPriority] {
		out.ProductionPriority = next.ProductionPriority
	}
	for _, field := range [][2]*string{{&out.Role, &next.Role}, {&out.IdentityAppearance, &next.IdentityAppearance}, {&out.Clothing, &next.Clothing}, {&out.PropDesign, &next.PropDesign}, {&out.SpatialLayout, &next.SpatialLayout}} {
		if *field[0] == "" {
			*field[0] = *field[1]
		}
	}
	out.Characters = mergeDesignValues(first.Characters, next.Characters)
	out.Props = mergeDesignValues(first.Props, next.Props)
	out.Environments = mergeDesignValues(first.Environments, next.Environments)
	out.Evidence = mergeDesignValues(first.Evidence, next.Evidence)
	out.States = mergeDesignValues(first.States, next.States)
	return &out
}

func mergeDesignValues[T comparable](first, next []T) []T {
	out := slices.Clone(first)
	for _, value := range next {
		if !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}
