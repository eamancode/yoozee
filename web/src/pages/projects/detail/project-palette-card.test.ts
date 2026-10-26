import assert from "node:assert/strict";
import test from "node:test";

import type { ProjectAssetPalette } from "@/services/api/projects";

import { paletteImageStatusLabel } from "./project-palette-card";

const palette = (patch: Partial<ProjectAssetPalette>): ProjectAssetPalette => ({
    code: "E03S01",
    entry: { episode: 3, scene_no: 1, scene: "第三章", inherits_from: "", shared_colors: [], new_colors: [] },
    design: "",
    prompt: "",
    imageStatus: "bound",
    ...patch,
});

test("色卡出图状态照常显示", () => {
    assert.equal(paletteImageStatusLabel(palette({ imageStatus: "bound" })), "已出图");
    assert.equal(paletteImageStatusLabel(palette({ imageStatus: "waiting" })), "等空位出图");
});

test("场次已不存在的色卡不管出图状态都标出来", () => {
    assert.equal(paletteImageStatusLabel(palette({ sceneGone: true, imageStatus: "bound" })), "场次已不存在");
    assert.equal(paletteImageStatusLabel(palette({ sceneGone: true, imageStatus: "failed" })), "场次已不存在");
});
