import { describe, expect, test } from "bun:test";

import { storyboardRowsFromTask } from "@/lib/canvas/canvas-project-domain";
import { chapterSourceScenes, segmentDurationRule, sourceSceneOfRows, storyboardRowsToSegments } from "@/pages/projects/detail/chapter-segments";
import { bindStoryboardRowAssets, chapterStoryboardAssets } from "@/pages/projects/detail/chapter-storyboard-production";
import type { DramaSegment } from "@/services/api/drama";
import type { ProjectAsset, ProjectDetail } from "@/services/api/projects";
import type { GenerationTask } from "@/services/api/task-center";

// 线上验收夹具：一章三场（病房走廊、电梯口、电梯口打斗），模型输出 29 镜。
const fixture = (name: string) => Bun.file(new URL(`./fixtures/chapter-storyboard-online/${name}`, import.meta.url)).text();

describe("线上模型输出解析", () => {
    test("线上模型输出的 spec 解析后保留在每个分镜行上", async () => {
        const { rows } = storyboardRowsFromTask(task(await fixture("model-output.txt")));
        expect(rows).toHaveLength(29);
        expect(rows.filter((row) => (row as typeof row & { spec?: { scene?: string } }).spec?.scene).length).toBe(29);
    });
});

describe("没图的场景资产进清单，装段按场切", () => {
    test("已确认的场景、道具没出图也进资产清单，只有出了图的带参考", () => {
        const assets = chapterStoryboardAssets(onlineDetail({ sceneAssets: true }));
        expect(assets.map((asset) => [asset.title, asset.type])).toEqual([
            ["林小满", "character"],
            ["周岸", "character"],
            ["阿坤", "character"],
            ["病房走廊", "entity"],
            ["电梯口", "entity"],
            ["折叠刀", "entity"],
            ["病历夹", "image"],
        ]);
    });

    test("正文按场景标题切成三场，同名标题也是两场", async () => {
        const scenes = chapterSourceScenes(await fixture("chapter.md"));
        expect(scenes.map((scene) => scene.heading)).toEqual(["内·病房走廊·夜", "内·电梯口·夜", "内·电梯口·夜"]);
        expect(scenes.map((scene) => scene.dialogue.length)).toEqual([3, 10, 3]);
        // 编辑器取出的纯文本没有 #，标题单独成行也认。
        expect(chapterSourceScenes("内·病房·夜\n林小满：七床？\n\n外·停车场·日\n周岸：走。").map((scene) => scene.heading)).toEqual(["内·病房·夜", "外·停车场·日"]);
        expect(chapterSourceScenes("第三场比赛开始了。\n林小满：七床？")).toEqual([]);
    });

    test("分镜行都没绑场景时，按台词和文字的先后把行归到正文的场", async () => {
        const { rows } = storyboardRowsFromTask(task(await fixture("model-output.txt")));
        const scenes = chapterSourceScenes(await fixture("chapter.md"));
        const sceneOf = sourceSceneOfRows(rows, scenes);
        // 第 7 镜（电梯门合上）和第 19 镜（防火门弹开）没有台词，靠文字落到新的一场。
        expect(runs(sceneOf)).toEqual([
            [0, 6],
            [1, 12],
            [2, 11],
        ]);
    });

    test("按场装段：段不跨场，参考只在同场的段之间共享", async () => {
        const detail = onlineDetail();
        const { rows } = storyboardRowsFromTask(task(await fixture("model-output.txt")));
        const bound = bindStoryboardRowAssets(rows, chapterStoryboardAssets(detail));
        expect(bound.every((row) => row.assetBindings.every((binding) => binding.role === "character"))).toBe(true);

        const plan = storyboardRowsToSegments(bound, detail, { chapterTitle: "第一集 七床空了", sourceText: await fixture("chapter.md"), rule: segmentDurationRule(15) });
        const segments = plan.shots.map((shot) => shot.revision.spec as unknown as DramaSegment);
        const scenes = segments.map((segment) => segment.scene);
        expect(scenes).not.toContain("第一集 七床空了");
        expect(Array.from(new Set(scenes))).toEqual(["内·病房走廊·夜", "内·电梯口·夜"]);
        expect(plan.notes.filter((note) => note.includes("整章按一场"))).toEqual([]);

        // 段不跨场：第 1 场 6 镜、第 2 场 12 镜、第 3 场 11 镜，场界（第 6、18 镜之后）都落在段界上。
        const ends = segments.reduce<number[]>((result, segment) => [...result, (result[result.length - 1] || 0) + segment.shots.length], []);
        expect(ends[ends.length - 1]).toBe(29);
        expect(ends).toEqual(expect.arrayContaining([6, 18]));

        const first = segments.filter((segment) => segment.scene === "内·病房走廊·夜");
        expect(first.reduce((total, segment) => total + segment.shots.length, 0)).toBe(6);
        // 病房走廊只有林小满出场：周岸和阿坤的参考不带进这一场。
        expect(first.every((segment) => segment.refs?.map((ref) => ref.file).join() === "version-linxiaoman")).toBe(true);
        const fight = segments.slice(-2);
        expect(fight.every((segment) => segment.refs?.some((ref) => ref.file === "version-akun"))).toBe(true);
        expect(segments.filter((segment) => segment.refs?.some((ref) => ref.file === "version-akun")).every((segment) => segment.scene === "内·电梯口·夜")).toBe(true);
    });

    test("场景资产没出图时也能按绑定切场，参考和 @ 引用都不带没图的版本", async () => {
        const detail = onlineDetail({ sceneAssets: true });
        const { rows } = storyboardRowsFromTask(task(await fixture("model-output.txt")));
        const bound = bindStoryboardRowAssets(rows, chapterStoryboardAssets(detail));
        expect(bound[0].assetBindings.map((binding) => binding.nodeId)).toContain("corridor");
        expect(bound[6].assetBindings.map((binding) => binding.nodeId)).toContain("elevator");

        const plan = storyboardRowsToSegments(bound, detail, { chapterTitle: "第一集 七床空了", sourceText: await fixture("chapter.md"), rule: segmentDurationRule(15) });
        const segments = plan.shots.map((shot) => shot.revision.spec as unknown as DramaSegment);
        expect(Array.from(new Set(segments.map((segment) => segment.scene)))).toEqual(["病房走廊", "电梯口"]);
        const files = segments.flatMap((segment) => segment.refs?.map((ref) => ref.file) || []);
        expect(files.some((file) => ["version-corridor", "version-elevator", "version-knife"].includes(file))).toBe(false);
        expect(plan.shots.some((shot) => shot.revision.videoPrompt?.includes("@[asset:corridor]"))).toBe(false);
    });
});

function runs(values: number[]) {
    return values.reduce<Array<[number, number]>>((result, value) => {
        const last = result[result.length - 1];
        if (last && last[0] === value) last[1] += 1;
        else result.push([value, 1]);
        return result;
    }, []);
}

function task(text: string): GenerationTask {
    return { resultJson: JSON.stringify({ mode: "text", text }) } as GenerationTask;
}

function onlineDetail(options: { sceneAssets?: boolean } = {}): ProjectDetail {
    const character = (id: string, title: string): ProjectAsset => ({
        id,
        title,
        mediaType: "entity",
        category: "character",
        status: "confirmed",
        primaryVersionId: `version-${id}`,
        versionCount: 1,
        usages: [],
        position: 0,
        updatedAt: "",
        character: { versionId: `character-version-${id}`, version: 1, definition: {}, representations: [], visualStatus: "ready", voiceStatus: "missing" },
    });
    // 章节资产确认后的场景、道具：mediaType 为 entity，主版本只有文字设定；绑过图片后才变成 image。
    const entity = (id: string, title: string, category: ProjectAsset["category"]): ProjectAsset => ({
        id,
        title,
        mediaType: "entity",
        category,
        status: "confirmed",
        primaryVersionId: `version-${id}`,
        versionCount: 1,
        usages: [],
        position: 0,
        updatedAt: "",
        visual: { definition: { prompt: title }, status: "missing" },
    });
    const assets = [character("linxiaoman", "林小满"), character("zhouan", "周岸"), character("akun", "阿坤")];
    if (options.sceneAssets) {
        assets.push(entity("corridor", "病房走廊", "environment"), entity("elevator", "电梯口", "environment"), entity("knife", "折叠刀", "prop"));
        assets.push({ id: "chart", title: "病历夹", mediaType: "image", category: "prop", status: "confirmed", primaryVersionId: "version-chart", versionCount: 2, usages: [], position: 0, updatedAt: "" });
    }
    return {
        project: { id: "project-1", userId: "user-1", name: "七床", type: "short_drama", aspectRatio: "16:9", sourceType: "novel", description: "", stylePresetId: "urban-live-action", status: "active", revision: 1, createdAt: "", updatedAt: "" },
        units: [],
        canvases: [],
        canvasUnitLinks: [],
        assetFolders: [],
        workflows: [],
        assets,
        shots: [],
        shotRevisions: [],
        shotArtifacts: [],
        shotReferences: [],
        assetCandidates: [],
    } as ProjectDetail;
}
