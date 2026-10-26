import { describe, expect, test } from "bun:test";

import { ASSET_CATEGORY_OPTIONS, assetCategoryLabel, defaultAssetCategoryForKind, normalizeAssetCategory } from "@/lib/asset-category";

describe("asset category contract", () => {
    test("旧服饰、武器和配饰统一迁移为道具", () => {
        expect(normalizeAssetCategory("wardrobe")).toBe("prop");
        expect(normalizeAssetCategory("weapon")).toBe("prop");
        expect(normalizeAssetCategory("accessory")).toBe("prop");
    });

    test("旧画风迁移为素材，未知值归入其他", () => {
        expect(normalizeAssetCategory("style")).toBe("material");
        expect(normalizeAssetCategory("unknown")).toBe("other");
        expect(assetCategoryLabel("material")).toBe("素材");
    });

    test("未分类媒体默认归入素材", () => {
        expect(defaultAssetCategoryForKind("image")).toBe("material");
        expect(defaultAssetCategoryForKind("video")).toBe("material");
        expect(defaultAssetCategoryForKind("audio")).toBe("material");
        expect(defaultAssetCategoryForKind("text")).toBe("other");
    });

    test("色卡是导演风格生成的分类：认得出、有中文名，但不出现在手选分类里", () => {
        expect(normalizeAssetCategory("palette")).toBe("palette");
        expect(assetCategoryLabel("palette")).toBe("色卡");
        expect(ASSET_CATEGORY_OPTIONS.some((option) => option.value === "palette")).toBe(false);
    });
});
