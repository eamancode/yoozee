package app

import (
	"encoding/json"
	"strings"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

func combinedDirectorProfile(project *model.Project, anchor *drama.StyleAnchor, palette drama.MasterPalette, name, description, id string, fields map[string]any) (string, error) {
	profile := map[string]any{}
	if strings.TrimSpace(project.StyleProfileJSON) != "" {
		if err := json.Unmarshal([]byte(project.StyleProfileJSON), &profile); err != nil {
			return "", err
		}
	}
	profile["schemaVersion"], profile["presetId"], profile["title"], profile["source"], profile["revision"] = 1, "director-"+id, name, "user", 1
	if description != "" || profile["description"] == nil {
		profile["description"] = description
	}
	if profile["tags"] == nil {
		profile["tags"] = []string{"导演风格"}
	}
	if profile["assets"] == nil {
		profile["assets"] = []any{}
	}
	for _, key := range []string{"title", "description", "tags", "prompt", "negativePrompt"} {
		if value, ok := fields[key]; ok {
			profile[key] = value
		}
	}
	if fields != nil {
		selection, _ := profile["selection"].(map[string]any)
		if selection == nil {
			selection = map[string]any{}
		}
		medium, _ := selection["medium"].(string)
		switch project.VisualType {
		case drama.VisualLiveAction:
			selection["medium"], selection["character"] = "live-action", "realistic"
		case drama.VisualCGReal:
			selection["medium"], selection["character"] = "cg-real", "realistic"
		case drama.VisualAnime3D:
			if medium != "3d-anime" && medium != "3d-cartoon" {
				selection["medium"] = "3d-anime"
			}
		case drama.VisualAnime2D:
			if medium != "2d-guoman" && medium != "ink" {
				selection["medium"] = "2d-guoman"
			}
		}
		profile["selection"] = selection
	}
	base, _ := profile["prompt"].(string)
	base = strings.SplitN(base, "\n\n【项目导演风格】", 2)[0]
	director := drama.StyleLockAnchorPrompt(anchor, "在基础风格字段之上采用以下导演规格。")
	colors, err := json.Marshal(palette)
	if err != nil {
		return "", err
	}
	profile["prompt"] = strings.TrimSpace(base) + "\n\n【项目导演风格】\n" + director + "\n【导演主基调色卡】\n" + string(colors)
	profile["director"] = map[string]any{"styleAnchor": anchor, "masterPalette": palette, "visualType": project.VisualType}
	encoded, err := json.Marshal(profile)
	if err != nil {
		return "", err
	}
	return validateStyleProfileJSON(string(encoded))
}
