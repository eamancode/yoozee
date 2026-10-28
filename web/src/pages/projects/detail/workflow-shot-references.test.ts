import assert from "node:assert/strict";
import test from "node:test";

import type { ProjectAsset, ProjectDetail, ShotAssetReference } from "@/services/api/projects";
import type { ReferenceImage } from "@/types/image";

import { buildShotAssetReferenceContext, referenceMediaForCompiledFiles, shotScenePalette, withShotScenePalette } from "./workflow-shot-references";

const paletteAsset = {
    id: "palette-1",
    title: "E01S02 色卡 · 内·电梯口·夜",
    category: "palette",
    mediaType: "image",
    storageKey: "resource:res-palette",
    primaryVersionId: "pv-1",
    palette: {
        kind: "scene",
        code: "E01S02",
        design: "",
        imageStatus: "bound",
        entry: {
            episode: 1,
            scene_no: 2,
            scene: "内·电梯口·夜",
            inherits_from: "",
            shared_colors: [{ name_zh: "水泥灰", name_en: "Concrete Grey", hex: "#6e6e6e", usage: "墙" }],
            new_colors: [{ name_zh: "护士蓝", name_en: "Nurse Blue", hex: "#5B7FA3", usage: "制服" }],
        },
    },
} as unknown as ProjectAsset;

const detail = (palette: ProjectAsset) =>
    ({
        assets: [palette],
        shotReferences: [{ id: "ref-1", shotId: "shot-1", assetVersionId: "pv-1", role: "style", status: "linked", asset: palette } as unknown as ShotAssetReference],
    }) as unknown as ProjectDetail;

const image = (id: string): ReferenceImage => ({ id, name: id, type: "image/*", dataUrl: "" });

test("本场色卡不进角色 / 场景 / 道具的参考，单独给出图和色值", () => {
    const project = detail(paletteAsset);
    assert.equal(buildShotAssetReferenceContext(project, "shot-1").referenceImages.length, 0);
    const palette = shotScenePalette(project, "shot-1");
    assert.equal(palette?.code, "E01S02");
    assert.equal(palette?.image?.storageKey, "resource:res-palette");
    assert.equal(palette?.colorsLine, "本场色值：水泥灰 #6E6E6E、护士蓝 #5B7FA3。");
    assert.equal(shotScenePalette(project, "shot-2"), undefined);
    assert.equal(shotScenePalette(detail({ ...paletteAsset, palette: { ...paletteAsset.palette!, kind: undefined } } as ProjectAsset), "shot-1"), undefined);
});

test("视频参考图有空位时色卡排最后，满了只写色值", () => {
    const palette = shotScenePalette(detail(paletteAsset), "shot-1");
    const three = [image("a"), image("b"), image("c")];
    const withRoom = withShotScenePalette(three, palette, 4);
    assert.equal(withRoom.images.length, 4);
    assert.equal(withRoom.images[3].storageKey, "resource:res-palette");
    assert.match(withRoom.promptLine, /^图片4 是本场色卡（E01S02）/);
    const full = withShotScenePalette([...three, image("d")], palette, 4);
    assert.equal(full.images.length, 4);
    assert.equal(full.promptLine, "本场色值：水泥灰 #6E6E6E、护士蓝 #5B7FA3。");
    assert.equal(withShotScenePalette(three, palette, 0).images.length, 3);
    assert.deepEqual(withShotScenePalette(three, undefined, 4), { images: three, promptLine: "" });
});

const locationAsset = {
    id: "loc-1",
    title: "电梯口",
    category: "scene",
    mediaType: "image",
    storageKey: "resource:res-loc",
    primaryVersionId: "lv-1",
} as unknown as ProjectAsset;

const compiledDetail = (assets: ProjectAsset[]) =>
    ({
        assets,
        shotReferences: assets.map((asset, index) => ({ id: `ref-${index}`, shotId: "shot-1", assetVersionId: asset.primaryVersionId, role: asset.palette ? "style" : "reference", status: "linked", asset }) as unknown as ShotAssetReference),
    }) as unknown as ProjectDetail;

test("编译清单按方言编号换参考图：MiniMax-H3 的 <Picture N> 与即梦的 @图片N 一样认", () => {
    const cases: Array<{ name: string; assets: ProjectAsset[]; files: Array<{ file: string; tag: string; role: string }>; keys: string[] }> = [
        { name: "H3 只有色卡", assets: [paletteAsset], files: [{ file: "pv-1", tag: "<Picture 1>", role: "palette" }], keys: ["resource:res-palette"] },
        {
            name: "H3 资产 + 色卡",
            assets: [locationAsset, paletteAsset],
            files: [
                { file: "lv-1", tag: "<Picture 1>", role: "location" },
                { file: "pv-1", tag: "<Picture 2>", role: "palette" },
            ],
            keys: ["resource:res-loc", "resource:res-palette"],
        },
        { name: "H3 只有资产", assets: [locationAsset], files: [{ file: "lv-1", tag: "<Picture 1>", role: "location" }], keys: ["resource:res-loc"] },
        {
            name: "即梦 资产 + 色卡",
            assets: [locationAsset, paletteAsset],
            files: [
                { file: "lv-1", tag: "@图片1", role: "location" },
                { file: "pv-1", tag: "@图片2", role: "palette" },
            ],
            keys: ["resource:res-loc", "resource:res-palette"],
        },
    ];
    for (const item of cases) {
        const media = referenceMediaForCompiledFiles(compiledDetail(item.assets), "shot-1", item.files);
        assert.deepEqual(media.missing, [], item.name);
        assert.deepEqual(
            media.images.map((image) => image.storageKey),
            item.keys,
            item.name,
        );
    }
    const unbound = referenceMediaForCompiledFiles(compiledDetail([locationAsset]), "shot-1", [
        { file: "pv-1", tag: "<Picture 1>", role: "palette" },
        { file: "lv-1", tag: "<Audio 1>", role: "voice" },
    ]);
    assert.deepEqual(unbound.missing, ["<Picture 1>（pv-1）", "<Audio 1>（lv-1）"]);
});
