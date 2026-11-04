package app

import "yingce/backend/internal/model"

func lowPriorityProp(asset *model.Asset) bool {
	if asset.Category != model.AssetCategoryProp {
		return false
	}
	data, _ := assetPayload(asset)["data"].(map[string]any)
	definition, _ := data["definition"].(map[string]any)
	return definition["productionPriority"] == "low"
}

func lowPriorityPropSummary(asset ProjectAssetSummary) bool {
	return asset.Category == model.AssetCategoryProp && asset.Visual != nil && asset.Visual.Definition["productionPriority"] == "low"
}
