package model

import "testing"

func TestNormalizeCanvasAndDramaAssetCategories(t *testing.T) {
	for _, category := range []AssetCategory{AssetCategoryArk, AssetCategoryPalette, AssetCategoryStoryboardReference} {
		if got := NormalizeAssetCategory(category, "image"); got != category {
			t.Errorf("%q image became %q", category, got)
		}
	}
}
