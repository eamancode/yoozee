import { describe, expect, test } from "bun:test";

import { storyboardRowsFromTask } from "@/lib/canvas/canvas-project-domain";
import { compileCanvasStylePreset } from "@/lib/canvas/canvas-style-system";
import { segmentDurationRule, storyboardRowsToSegments } from "@/pages/projects/detail/chapter-segments";
import {
    bindStoryboardRowAssets,
    chapterStoryboardAssetPrompt,
    chapterStoryboardAssets,
    chapterStoryboardCharacters,
    chapterStoryboardReplaceImpact,
    chapterStoryboardTaskPrompt,
    storyboardRowsToProjectShots,
} from "@/pages/projects/detail/chapter-storyboard-production";
import type { DramaSegment } from "@/services/api/drama";
import type { ProjectAsset, ProjectDetail } from "@/services/api/projects";
import type { GenerationTask } from "@/services/api/task-center";
import type { StoryboardRow } from "@/types/canvas";

describe("章节分镜写入分镜制作", () => {
    test("将分镜任务结果完整映射为镜头脚本", () => {
        const shots = storyboardRowsToProjectShots([storyboardRow()], projectDetail());

        expect(shots).toHaveLength(1);
        expect(shots[0]).toMatchObject({
            title: "镜头01",
            description: "林默推门进入雨夜的旧屋。",
            durationMs: 5000,
            revision: {
                plotDescription: "林默推门进入雨夜的旧屋。",
                dialogue: "林默：有人吗？",
                shotSize: "中景",
                cameraAngle: "平视，35mm",
                cameraMovement: "缓慢推进",
                imagePrompt: "雨夜旧屋中的林默",
                videoPrompt: "林默推门，镜头缓慢推进\n\n【角色参考】\n林默：@[asset:character-1]\n\n【场景与道具参考】\n旧信封：@[asset:prop-1]",
                continuityNotes: "林默停在门内，右手仍扶着门把",
                actionBeats: [{ description: "0-2 秒推门；2-5 秒环顾" }],
            },
        });
        expect(shots[0].assetVersionIds).toEqual(expect.arrayContaining(["asset-version-1", "asset-version-2"]));
        expect(shots[0].revision.action).toContain("表演调度：林默谨慎推门并环顾室内");
        expect(shots[0].revision.action).toContain("声音：雨声与木门摩擦声");
    });

    test("把已确认角色和本章待确认角色交给分镜任务", () => {
        const detail = projectDetail();

        expect(chapterStoryboardCharacters(detail, "chapter-1")).toEqual([
            { assetId: "character-1", versionId: "character-version-1", name: "林默", definition: { appearance: "黑色风衣" } },
            { name: "张天昊", definition: { role: "主角" } },
        ]);
        expect(chapterStoryboardAssets(detail).map((asset) => ({ id: asset.id, type: asset.type, characterVersionId: asset.characterVersionId }))).toEqual([
            { id: "character-1", type: "character", characterVersionId: "character-version-1" },
            { id: "prop-1", type: "image", characterVersionId: undefined },
        ]);
    });

    test("资产清单写进分镜提示词，并要求每个镜头写出场资产", () => {
        const prompt = chapterStoryboardAssetPrompt(chapterStoryboardAssets(projectDetail()));
        expect(prompt).toContain("【项目已确认资产】");
        expect(prompt).toContain("- 林默（角色）");
        expect(prompt).toContain("- 旧信封（道具）：泛黄的旧信封");
        expect(prompt).toContain("rows 每项再加 assets");
        expect(chapterStoryboardAssetPrompt([])).toBe("");
    });

    test("分镜任务提示词带上项目画风和已确认角色外观，待确认角色不写外观", () => {
        const detail = projectDetail();
        detail.assets[0].character!.definition = { appearance: "瘦高，眉骨有一道旧疤", clothing: "黑色风衣", consistencyPrompt: "始终戴银色怀表" };
        const style = compileCanvasStylePreset({ world: "suspense", tone: "dark", medium: "ink", character: "stylized" });
        const prompt = chapterStoryboardTaskPrompt("【用户任务】\n章节：雨夜\n\n林默推门进入旧屋。", {
            sourceText: "林默推门进入旧屋。张天昊在屋里等他。",
            projectStyle: { title: style.title, prompt: style.prompt },
            characters: chapterStoryboardCharacters(detail, "chapter-1"),
            assets: chapterStoryboardAssets(detail),
        });

        expect(prompt).toStartWith("【用户任务】\n章节：雨夜");
        expect(prompt).toContain(`【项目画风：${style.title}】`);
        expect(prompt).toContain("【视觉媒介】");
        expect(prompt).toContain(style.prompt.split("\n").find((line) => line.startsWith("【视觉媒介】")));
        expect(prompt).toContain("- 林默｜外貌：瘦高，眉骨有一道旧疤；服装：黑色风衣；一致性：始终戴银色怀表");
        expect(prompt).not.toContain("张天昊｜");
        expect(prompt.indexOf("【项目画风")).toBeLessThan(prompt.indexOf("【已确认角色外观】"));
        expect(prompt.indexOf("【已确认角色外观】")).toBeLessThan(prompt.indexOf("【受保护输出契约】"));
        expect(prompt.indexOf("【受保护输出契约】")).toBeLessThan(prompt.indexOf("【项目已确认资产】"));
    });

    test("模型按名称给的资产落成分镜行绑定，按场景装成两段（走廊 4 镜 + 病房 2 镜）", () => {
        const detail = wardDetail();
        const rows = storyboardRowsFromTask(storyboardTask([
            { plotDescription: "林小满抱着病历夹走进夜里的走廊", assets: ["林小满", "病房走廊"], characters: ["林小满"], durationSeconds: 4 },
            { plotDescription: "她边走边翻病历夹", assets: ["林小满", "病房走廊", "蓝色七床病历夹"], durationSeconds: 4 },
            { plotDescription: "她看向七床门牌", assets: ["林小满", "病房走廊（夜）"], durationSeconds: 4 },
            { plotDescription: "她推开七床的门", assets: ["林小满", "病房走廊"], durationSeconds: 4 },
            { plotDescription: "病床空着，被子叠得整整齐齐", assets: ["七床病房"], durationSeconds: 4 },
            { plotDescription: "林小满站在床边", dialogue: "林小满：七床的人呢？", assets: ["林小满", "七床病房"], durationSeconds: 4 },
        ])).rows;
        const bound = bindStoryboardRowAssets(rows, chapterStoryboardAssets(detail));

        expect(bound[0].assetBindings).toEqual([
            { nodeId: "linxiaoman", role: "character", priority: 100 },
            { nodeId: "corridor", role: "environment", priority: 99 },
        ]);
        expect(bound[0].characters).toEqual([{ characterName: "林小满", characterAssetId: "linxiaoman", characterVersionId: "character-version-linxiaoman" }]);
        expect(bound[2].assetBindings.map((binding) => binding.nodeId)).toEqual(["linxiaoman", "corridor"]);
        const plan = storyboardRowsToSegments(bound, detail, { chapterTitle: "第二章", rule: segmentDurationRule(30) });
        const segments = plan.shots.map((shot) => shot.revision.spec as unknown as DramaSegment);
        expect(segments.map((segment) => [segment.scene, segment.shots.length])).toEqual([["病房走廊", 4], ["七床病房", 2]]);
        expect(segments[0].refs?.map((ref) => ref.file)).toEqual(["version-linxiaoman", "version-corridor", "version-chart"]);
        expect(segments[1].refs?.map((ref) => ref.file)).toEqual(["version-linxiaoman", "version-ward"]);
        expect(plan.notes).toEqual([]);
    });

    test("模型没给某一类资产时，按镜头文字里出现的资产名补；场景只补最先出现的，长名不被短名截走", () => {
        const assets = chapterStoryboardAssets(wardDetail());
        const [corridor, ward] = bindStoryboardRowAssets([
            storyboardRowWith({ imageGenerationPrompt: "病房走廊，夜，日光灯", plotDescription: "林小满走向七床病房，手里拿着蓝色七床病历夹" }),
            storyboardRowWith({ plotDescription: "病床空着", characters: ["林小满"] as unknown as StoryboardRow["characters"], continuityOut: "林小满停在七床病房的床边" }),
        ], assets);

        expect(corridor.assetBindings.map((binding) => [binding.nodeId, binding.role])).toEqual([["corridor", "environment"], ["linxiaoman", "character"], ["chart", "prop"]]);
        // 模型给了角色：角色以模型为准，场景按文字补。
        expect(ward.assetBindings.map((binding) => [binding.nodeId, binding.priority > 50 ? "model" : "text"])).toEqual([["linxiaoman", "model"], ["ward", "text"]]);
    });

    test("名称对不上、有歧义或画外音时不乱绑", () => {
        const assets = chapterStoryboardAssets(wardDetail());
        const [row] = bindStoryboardRowAssets([storyboardRowWith({ plotDescription: "病房里很安静", dialogue: "林小满（画外）：走了……", assets: ["病房", "不存在的道具"] } as Partial<StoryboardRow>)], assets);
        expect(row.assetBindings).toEqual([]);
    });

    test("替换确认统计该章会被删除的关联数据", () => {
        expect(chapterStoryboardReplaceImpact(projectDetail(), "chapter-1")).toEqual({
            shotCount: 1,
            revisionCount: 1,
            referenceCount: 1,
            artifactCount: 1,
            candidateCount: 1,
        });
    });
});

function storyboardTask(rows: Array<Record<string, unknown>>): GenerationTask {
    return { resultJson: JSON.stringify({ mode: "text", text: JSON.stringify({ title: "第二章", rows }) }) } as GenerationTask;
}

function storyboardRowWith(patch: Partial<StoryboardRow>): StoryboardRow {
    return { ...storyboardRow(), plotDescription: "", dialogue: "", characters: [], performanceBlocking: "", imageGenerationPrompt: "", videoMotionPrompt: "", continuityOut: "", assetBindings: [], ...patch };
}

function wardDetail(): ProjectDetail {
    const asset = (id: string, title: string, category: ProjectAsset["category"]): ProjectAsset => ({
        id, title, mediaType: "image", category, status: "ready", primaryVersionId: `version-${id}`, versionCount: 1, usages: [], position: 0, updatedAt: "",
        ...(category === "character" ? { character: { versionId: `character-version-${id}`, version: 1, definition: {}, representations: [], visualStatus: "ready", voiceStatus: "missing" } } : {}),
    });
    return {
        ...projectDetail(),
        assets: [asset("linxiaoman", "林小满", "character"), asset("corridor", "病房走廊", "environment"), asset("ward", "七床病房", "environment"), asset("chart", "蓝色七床病历夹", "prop")],
    };
}

function storyboardRow(): StoryboardRow {
    return {
        id: "row-1", shotNumber: 1, durationSeconds: 5,
        plotDescription: "林默推门进入雨夜的旧屋。", dialogue: "林默：有人吗？", characters: [{ characterName: "林默", characterAssetId: "character-1", characterVersionId: "character-version-1" }],
        narrativeIntent: "建立悬念", viewerPOV: "跟随林默", performanceBlocking: "林默谨慎推门并环顾室内",
        shotSize: "中景", emotion: "戒备", lightingAndAtmosphere: "冷色月光与室内暖光交界", audioEffects: "雨声与木门摩擦声",
        camera: "平视，35mm", motion: "缓慢推进", timeBeats: "0-2 秒推门；2-5 秒环顾",
        imageGenerationPrompt: "雨夜旧屋中的林默", videoMotionPrompt: "林默推门，镜头缓慢推进",
        mustHave: [], optionalDetails: [], continuityOut: "林默停在门内，右手仍扶着门把", negativePrompt: "画面文字", assetBindings: [{ nodeId: "prop-1", role: "prop", priority: 80 }],
    };
}

function projectDetail(): ProjectDetail {
    return {
        project: { id: "project-1", userId: "user-1", name: "雨夜", type: "short_drama", aspectRatio: "16:9", sourceType: "novel", description: "", stylePresetId: "urban-live-action", status: "active", revision: 1, createdAt: "", updatedAt: "" },
        units: [], canvases: [], canvasUnitLinks: [], assetFolders: [], workflows: [],
        assets: [
            { id: "character-1", title: "林默", mediaType: "image", category: "character", status: "ready", primaryVersionId: "asset-version-1", versionCount: 1, usages: [], position: 0, updatedAt: "", character: { versionId: "character-version-1", version: 1, definition: { appearance: "黑色风衣" }, representations: [], visualStatus: "ready", voiceStatus: "missing" } },
            { id: "prop-1", title: "旧信封", mediaType: "image", category: "prop", status: "ready", primaryVersionId: "asset-version-2", versionCount: 1, usages: [], position: 1, previewText: "泛黄的旧信封", updatedAt: "" },
            { id: "draft-prop", title: "未完成道具", mediaType: "image", category: "prop", status: "draft", versionCount: 0, usages: [], position: 2, updatedAt: "" },
        ],
        shots: [{ id: "shot-1", projectId: "project-1", unitId: "chapter-1", currentRevisionId: "revision-1", title: "SC.01", description: "旧分镜", position: 0, durationMs: 3000, status: "draft", createdAt: "", updatedAt: "" }],
        shotRevisions: [{ id: "revision-1", shotId: "shot-1", version: 1, plotDescription: "旧分镜", action: "", dialogue: "", shotSize: "", cameraAngle: "", cameraMovement: "", durationMs: 3000, imagePrompt: "", videoPrompt: "", negativePrompt: "", continuityNotes: "", actionBeatsJson: "[]", createdAt: "" }],
        shotArtifacts: [{ id: "artifact-1", projectId: "project-1", unitId: "chapter-1", shotId: "shot-1", type: "storyboard", version: 1, status: "ready", selected: true, metadataJson: "{}", createdAt: "", updatedAt: "" }],
        shotReferences: [{ id: "reference-1", shotId: "shot-1", assetVersionId: "asset-version-1", role: "reference", status: "linked", createdAt: "" }],
        assetCandidates: [
            { id: "candidate-1", projectId: "project-1", unitId: "chapter-1", shotId: "shot-1", name: "雨伞", category: "prop", status: "pending_confirmation", detailsJson: "{}", createdAt: "", updatedAt: "" },
            { id: "candidate-character-1", projectId: "project-1", unitId: "chapter-1", name: "张天昊", category: "character", status: "pending_confirmation", detailsJson: JSON.stringify({ role: "主角" }), createdAt: "", updatedAt: "" },
            { id: "candidate-character-other", projectId: "project-1", unitId: "chapter-2", name: "另一章角色", category: "character", status: "pending_confirmation", detailsJson: "{}", createdAt: "", updatedAt: "" },
        ],
    };
}
