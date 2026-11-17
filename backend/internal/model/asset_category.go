package model

import "strings"

// NormalizeAssetCategory preserves recognized workflow categories on canvas sync.
func NormalizeAssetCategory(value AssetCategory, kind string) AssetCategory {
	switch strings.ToLower(strings.TrimSpace(string(value))) {
	case string(AssetCategoryCharacter):
		return AssetCategoryCharacter
	case string(AssetCategoryEnvironment):
		return AssetCategoryEnvironment
	case string(AssetCategoryProp), "wardrobe", "weapon", "accessory":
		return AssetCategoryProp
	case string(AssetCategoryMaterial), "style":
		return AssetCategoryMaterial
	case string(AssetCategoryOther):
		return AssetCategoryOther
	case string(AssetCategoryArk):
		return AssetCategoryArk
	case string(AssetCategoryPalette):
		return AssetCategoryPalette
	case string(AssetCategoryStoryboardReference):
		return AssetCategoryStoryboardReference
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "entity":
		return AssetCategoryCharacter
	case "image", "video", "audio", "model":
		return AssetCategoryMaterial
	default:
		return AssetCategoryOther
	}
}
