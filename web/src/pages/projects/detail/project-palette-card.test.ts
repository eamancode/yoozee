import assert from "node:assert/strict";
import test from "node:test";

import type { ProjectAssetPalette } from "@/services/api/projects";

import { groupedPaletteColors, paletteImageStatusLabel } from "./project-palette-card";

const palette = (patch: Partial<ProjectAssetPalette>): ProjectAssetPalette => ({
    code: "E03S01",
    entry: { episode: 3, scene_no: 1, scene: "第三章", inherits_from: "", shared_colors: [], new_colors: [] },
    design: "",
    kind: "scene",
    imageStatus: "bound",
    ...patch,
});

test("场次色卡的图由代码合成；旧版色卡单独标出", () => {
    assert.equal(paletteImageStatusLabel(palette({ imageStatus: "bound" })), "代码合成");
    assert.equal(paletteImageStatusLabel(palette({ imageStatus: "none" })), "图没合成上");
    assert.equal(paletteImageStatusLabel(palette({ kind: undefined, imageStatus: "bound" })), "旧版色卡");
});

test("颜色按主色卡的分组排，没写分组的归其他", () => {
    const groups = groupedPaletteColors([
        { name_zh: "水泥灰", name_en: "", hex: "#6E6E6E", usage: "", group: "environment" },
        { name_zh: "护士蓝", name_en: "", hex: "#5B7FA3", usage: "", group: "protagonist" },
        { name_zh: "无组", name_en: "", hex: "#111111", usage: "" },
    ]);
    assert.deepEqual(
        groups.map((group) => [group.label, group.colors.map((color) => color.name_zh)]),
        [
            ["主角组", ["护士蓝"]],
            ["环境组", ["水泥灰"]],
            ["其他", ["无组"]],
        ],
    );
});

test("场次已不存在的色卡不管出图状态都标出来", () => {
    assert.equal(paletteImageStatusLabel(palette({ sceneGone: true, imageStatus: "bound" })), "场次已不存在");
    assert.equal(paletteImageStatusLabel(palette({ sceneGone: true, imageStatus: "failed" })), "场次已不存在");
});
