import assert from "node:assert/strict";
import test from "node:test";

import type { ProjectAsset, ProjectDetail, ShotAssetReference } from "@/services/api/projects";
import type { ReferenceImage } from "@/types/image";

import { buildShotAssetReferenceContext, projectAssetReferenceImage, projectAssetChoices, compileShotAssetReferencePrompt, referenceMediaForCompiledFiles, shotScenePalette, withShotScenePalette } from "./workflow-shot-references";

test("主卡、子卡、场景、道具固定版本的预览与提交引用一致，换图和取消不残留", () => {
    const assets = ["主卡", "子卡", "场景", "道具"].map(
        (title, index) => ({ id: `asset-${index}`, title, primaryVersionId: `current-${index}`, mediaType: "image", category: index < 2 ? "character" : index === 2 ? "environment" : "prop", storageKey: `resource:new-${index}` }) as ProjectAsset,
    );
    const references = assets.map(
        (asset, index) =>
            ({
                id: `ref-${index}`,
                shotId: "shot-1",
                assetVersionId: `fixed-${index}`,
                role: "reference",
                status: "linked",
                source: "segment",
                position: index,
                asset,
                referencedVersion: { id: `fixed-${index}`, assetId: asset.id, version: 1, representations: [{ id: `rep-${index}`, resourceId: `old-${index}`, mediaType: "image/png", role: "primary" }] },
            }) as ShotAssetReference,
    );
    const project = { assets, shotReferences: references.slice().reverse() } as ProjectDetail;
    const files = references.map((ref, index) => ({ file: ref.assetVersionId, tag: `@图片${index + 1}`, role: "identity" }));
    const media = referenceMediaForCompiledFiles(project, "shot-1", files);
    assert.deepEqual(media.missing, []);
    assert.deepEqual(
        media.images.map((img) => img.storageKey),
        ["resource:old-0", "resource:old-1", "resource:old-2", "resource:old-3"],
    );
    assert.deepEqual(
        buildShotAssetReferenceContext(project, "shot-1").referenceImages.map((img) => img.storageKey),
        media.images.map((img) => img.storageKey),
    );
    references.forEach((ref, index) => assert.equal(projectAssetReferenceImage(assets[index]!, ref)?.storageKey, `resource:old-${index}`));
    assert.equal(projectAssetReferenceImage(assets[0]!, { ...references[0]!, referencedVersion: { ...references[0]!.referencedVersion!, representations: [] } }), undefined);
    const changed = {
        ...project,
        shotReferences: references.map((ref) =>
            ref.id === "ref-1"
                ? { ...ref, assetVersionId: "replacement", referencedVersion: { ...ref.referencedVersion!, id: "replacement", representations: [{ id: "replacement-image", resourceId: "replacement-image", mediaType: "image/png", role: "primary" }] } }
                : ref,
        ),
    };
    const changedFiles = files.map((file) => (file.file === "fixed-1" ? { ...file, file: "replacement" } : file));
    assert.equal(referenceMediaForCompiledFiles(changed, "shot-1", changedFiles).images[1]?.storageKey, "resource:replacement-image");
    const cancelled = { ...changed, shotReferences: changed.shotReferences.filter((ref) => ref.id !== "ref-1") };
    assert.equal(referenceMediaForCompiledFiles(cancelled, "shot-1", changedFiles).missing.length, 1);
    const parent = { ...assets[0]!, variants: [assets[1]!] };
    assert.deepEqual(
        projectAssetChoices([parent, assets[1]!, assets[2]!]).map((asset) => asset.title),
        ["主卡", "子卡", "场景"],
    );
});

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

test("微缩场景使用实际图片编号并提示真实尺度，普通场景不受影响", () => {
    const spatial = { ...locationAsset, visual: { definition: { imageLayout: "spatial-master" } } } as unknown as ProjectAsset;
    const context = buildShotAssetReferenceContext(compiledDetail([spatial]), "shot-1");
    assert.equal(context.referenceImages[0].storageKey, "resource:res-loc");
    const prompt = compileShotAssetReferencePrompt(context.assetReferences);
    assert.match(prompt, /图片1/);
    assert.match(prompt, /真实尺度/);
    assert.match(prompt, /不继承微缩底座/);
    assert.doesNotMatch(compileShotAssetReferencePrompt(buildShotAssetReferenceContext(compiledDetail([locationAsset]), "shot-1").assetReferences), /微缩底座/);
});

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

test("手动按普通参考绑定的色卡不占内容图片编号，也不混入分镜图", () => {
    const project = compiledDetail([locationAsset, paletteAsset]);
    project.shotReferences![1].role = "reference";
    const context = buildShotAssetReferenceContext(project, "shot-1");
    assert.deepEqual(
        context.referenceImages.map((image) => image.storageKey),
        ["resource:res-loc"],
    );
    assert.deepEqual(
        context.assetReferences.map((asset) => [asset.title, asset.imageIndex]),
        [["电梯口", 1]],
    );
    assert.equal(shotScenePalette(project, "shot-1"), undefined);
    project.shotReferences![1].role = "style";
    const video = withShotScenePalette(context.referenceImages, shotScenePalette(project, "shot-1"), 3);
    assert.deepEqual(
        video.images.map((image) => image.storageKey),
        ["resource:res-loc", "resource:res-palette"],
    );
    assert.match(video.promptLine, /^图片2 是本场色卡/);
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

test("MiniMax-H3 锁首帧段的 first_frame / last_frame 作首尾帧提交，即梦锁首帧的 @图片1 仍是参考图", () => {
    const lastAsset = { ...locationAsset, id: "loc-2", title: "电梯口·尾", storageKey: "resource:res-last", primaryVersionId: "lv-2" } as ProjectAsset;
    const h3 = referenceMediaForCompiledFiles(compiledDetail([locationAsset, lastAsset]), "shot-1", [
        { file: "lv-1", tag: "first_frame", role: "first_frame" },
        { file: "lv-2", tag: "last_frame", role: "last_frame" },
    ]);
    assert.deepEqual([h3.missing, h3.unsupported, h3.frames], [[], [], true]);
    assert.deepEqual(
        h3.images.map((image) => [image.storageKey, image.role]),
        [
            ["resource:res-loc", "first_frame"],
            ["resource:res-last", "last_frame"],
        ],
    );

    const seedance = referenceMediaForCompiledFiles(compiledDetail([locationAsset]), "shot-1", [{ file: "lv-1", tag: "@图片1", role: "first_frame" }]);
    assert.equal(seedance.frames, false);
    assert.deepEqual(
        seedance.images.map((image) => [image.storageKey, image.role]),
        [["resource:res-loc", undefined]],
    );
});

test("首尾帧找不到、混了参考素材、参考视频和不认识的编号都给出准确提示", () => {
    const unbound = referenceMediaForCompiledFiles(compiledDetail([locationAsset]), "shot-1", [{ file: "gone", tag: "first_frame", role: "first_frame" }]);
    assert.deepEqual(unbound.missing, ["首帧（gone）"]);
    assert.equal(unbound.frames, false);

    const mixed = referenceMediaForCompiledFiles(compiledDetail([locationAsset, paletteAsset]), "shot-1", [
        { file: "lv-1", tag: "first_frame", role: "first_frame" },
        { file: "pv-1", tag: "<Picture 1>", role: "palette" },
    ]);
    assert.deepEqual(mixed.unsupported, ["首尾帧不能和参考图、参考音频一起提交，请去掉这一段的参考或改用锁人物模式"]);

    const other = referenceMediaForCompiledFiles(compiledDetail([locationAsset]), "shot-1", [
        { file: "lv-1", tag: "@视频1", role: "motion" },
        { file: "lv-1", tag: "<Mask 1>", role: "mask" },
    ]);
    assert.deepEqual(other.missing, []);
    assert.deepEqual(other.unsupported, ["@视频1 是参考视频，工作台暂不支持提交参考视频", "工作台不认识编译清单里的编号 <Mask 1>（lv-1）"]);
});
