export const ASSET_CATEGORIES = ["character", "environment", "prop", "material", "other", "palette"] as const;

export type AssetCategory = (typeof ASSET_CATEGORIES)[number];

/** 用户能手选的分类：色卡（palette）只由导演风格按色彩脚本生成，不出现在分类选择里。 */
export const EDITABLE_ASSET_CATEGORIES = ASSET_CATEGORIES.filter((category) => category !== "palette");

export const ASSET_CATEGORY_LABELS: Record<AssetCategory, string> = {
    character: "角色",
    environment: "场景",
    prop: "道具",
    material: "素材",
    other: "其他",
    palette: "色卡",
};

export const ASSET_CATEGORY_OPTIONS = EDITABLE_ASSET_CATEGORIES.map((value) => ({
    value,
    label: ASSET_CATEGORY_LABELS[value],
}));

const LEGACY_ASSET_CATEGORY_MAP: Record<string, AssetCategory> = {
    wardrobe: "prop",
    weapon: "prop",
    accessory: "prop",
    style: "material",
};

export function normalizeAssetCategory(value: unknown, fallback: AssetCategory = "other"): AssetCategory {
    if (typeof value !== "string") return fallback;
    const normalized = value.trim().toLowerCase();
    if ((ASSET_CATEGORIES as readonly string[]).includes(normalized)) return normalized as AssetCategory;
    return LEGACY_ASSET_CATEGORY_MAP[normalized] || fallback;
}

export function parseAssetCategory(value: unknown): AssetCategory {
    if (typeof value !== "string") throw new Error("素材 category 必须是字符串");
    const normalized = value.trim().toLowerCase();
    if ((ASSET_CATEGORIES as readonly string[]).includes(normalized)) return normalized as AssetCategory;
    const mapped = LEGACY_ASSET_CATEGORY_MAP[normalized];
    if (!mapped) throw new Error("素材 category 无效");
    return mapped;
}

export function defaultAssetCategoryForKind(kind: string): AssetCategory {
    if (kind === "entity") return "character";
    if (kind === "image" || kind === "video" || kind === "audio" || kind === "model") return "material";
    return "other";
}

export function assetCategoryLabel(value: unknown) {
    return ASSET_CATEGORY_LABELS[normalizeAssetCategory(value)];
}
