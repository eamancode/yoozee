import { describe, expect, test } from "bun:test";
import { createStyleProfileSnapshot, parseStyleProfile, serializeStyleProfile } from "../src/lib/canvas/style-profile";
import { assetCategoryLabel, parseAssetCategory } from "../src/lib/asset-category";

describe("director style snapshots and storyboard references", () => {
    test("personal styles retain director fields, execution assets and their frozen snapshots", () => {
        const source = createStyleProfileSnapshot({
            presetId: "personal-director",
            sourceProfileId: "personal-director",
            title: "随行鲜 · 日常写实",
            description: "冷雨与暖灯中的人物关系",
            tags: ["真人", "生活"],
            prompt: "【视觉媒介】真人影视\n【使用边界】具体动作由分镜决定",
            negativePrompt: "白模、色卡",
            source: "user",
            revision: 3,
            selection: { world: "urban", tone: "healing", medium: "live-action", character: "realistic" },
            director: { visualType: "live_action", styleAnchor: { medium: "真人影视", lighting: "实景暖灯", camera: "克制观察" }, masterPalette: { groups: [] } },
            assets: [{ id: "real-reference", kind: "reference", provider: "project", title: "已授权风格参考", referenceResourceIds: ["owned-image"], status: "validated", license: { commercial: true, note: "已授权" } }],
        });
        const frozen = serializeStyleProfile(source);
        const copy = parseStyleProfile(frozen)!;
        expect(copy.director).toEqual(source.director);
        expect(copy.selection).toEqual(source.selection);
        expect(copy.assets[0].license?.commercial).toBe(true);
        copy.director!.styleAnchor.camera = "新镜头语言";
        copy.assets[0].referenceResourceIds!.push("second-image");
        expect(source.director!.styleAnchor.camera).toBe("克制观察");
        expect(parseStyleProfile(frozen)!.assets[0].referenceResourceIds).toEqual(["owned-image"]);
    });

    test("storyboard reference has its own reusable business category", () => {
        expect(parseAssetCategory("storyboard_reference")).toBe("storyboard_reference");
        expect(assetCategoryLabel("storyboard_reference")).toBe("分镜参考");
    });
});
