import { describe, expect, test } from "bun:test";

import { canvasResourceMentionToken } from "@/lib/canvas/canvas-resource-references";
import type { ProjectAsset, ProjectDetail } from "@/services/api/projects";
import { buildShotAssetReferenceContext, changeShotAssetMentionPrompt, ensureShotAssetMentionPrompt, resolveShotAssetMentionPrompt } from "@/pages/projects/workbench/detail/workflow-shot-references";

describe("workflow shot asset references", () => {
    test("builds ordered image references for bound media and characters", () => {
        const context = buildShotAssetReferenceContext(detail(), "shot-1");

        expect(context.mentionReferences.map((reference) => reference.label)).toEqual(["雨夜街道", "林默"]);
        expect(context.referenceImages.map((image) => image.id)).toEqual(["asset-scene", "asset-character"]);
        expect(context.referenceImages[1]?.storageKey).toBe("resource:character-cover");
    });

    test("resolves asset tokens to the submitted reference image order", () => {
        const context = buildShotAssetReferenceContext(detail(), "shot-1");
        const [scene, character] = context.mentionReferences;
        const prompt = `让 ${canvasResourceMentionToken(character)} 从 ${canvasResourceMentionToken(scene)} 走近镜头`;

        expect(resolveShotAssetMentionPrompt(prompt, context)).toContain("让 图片2 从 图片1 走近镜头");
    });

    test("rejects stale mentions after an asset is unbound", () => {
        const context = buildShotAssetReferenceContext(detail(), "shot-1");

        expect(() => resolveShotAssetMentionPrompt("跟随 @[asset:missing]", context)).toThrow("未绑定到当前镜头");
    });

    test("adds bound assets as editable mention tokens without duplicating existing mentions", () => {
        const context = buildShotAssetReferenceContext(detail(), "shot-1");
        const prompt = ensureShotAssetMentionPrompt("让 @[asset:asset-character] 走近镜头", context.mentionReferences);

        expect(prompt).toContain("@[asset:asset-character]");
        expect(prompt.match(/@\[asset:asset-character\]/g)).toHaveLength(1);
        expect(prompt).toContain("【场景与道具参考】\n雨夜街道：@[asset:asset-scene]");
    });

    test("replaces and unlinks main, child, scene and prop mentions without stale submit references", () => {
        for (const title of ["人物主卡", "人物·雨衣", "雨夜街道", "旧钥匙"]) {
            const previous = { id: "previous", title };
            const next = { id: "asset-scene", title: "雨夜街道" };
            const prompt = `跟随 @[asset:previous]，再看 @[asset:previous]。\n${title}：@[asset:previous]\n保留对白`;
            const replaced = changeShotAssetMentionPrompt(prompt, previous, next);
            expect(replaced).not.toContain("@[asset:previous]");
            expect(replaced).toContain("雨夜街道：@[asset:asset-scene]");
            expect(resolveShotAssetMentionPrompt(replaced, buildShotAssetReferenceContext(detail(), "shot-1"))).toContain("跟随 图片1，再看 图片1。");
            const unlinked = changeShotAssetMentionPrompt(prompt, previous);
            expect(unlinked).toContain(`跟随 ${title}，再看 ${title}。`);
            expect(unlinked).not.toContain("@[asset:previous]");
            expect(unlinked).toContain("保留对白");
            expect(resolveShotAssetMentionPrompt(unlinked, buildShotAssetReferenceContext(detail(), "shot-1"))).toContain("保留对白");
            expect(changeShotAssetMentionPrompt(unlinked, previous)).toBe(unlinked);
        }
    });

    test("binding edits preserve unrelated tokens and do not hide invalid input", () => {
        const value = "跟随 @[asset:other] 和 @[asset:old-extra]，@图片2 不变";
        expect(changeShotAssetMentionPrompt(value, { id: "old", title: "旧人物" })).toBe(value);
        expect(() => resolveShotAssetMentionPrompt(value, buildShotAssetReferenceContext(detail(), "shot-1"))).toThrow("未绑定到当前镜头");
    });
});

function detail(): ProjectDetail {
    const scene = {
        id: "asset-scene",
        title: "雨夜街道",
        mediaType: "image",
        category: "environment",
        status: "confirmed",
        primaryVersionId: "version-scene",
        versionCount: 1,
        usages: [],
        position: 0,
        storageKey: "resource:scene-image",
        updatedAt: "2026-08-29T00:00:00Z",
    } satisfies ProjectAsset;
    const character = {
        id: "asset-character",
        title: "林默",
        mediaType: "entity",
        category: "character",
        status: "confirmed",
        primaryVersionId: "version-character",
        versionCount: 1,
        usages: [],
        position: 1,
        updatedAt: "2026-08-29T00:00:00Z",
        character: {
            versionId: "version-character",
            version: 1,
            definition: {},
            representations: [{ id: "representation-1", resourceId: "character-cover", mediaType: "image", role: "primary" }],
            visualStatus: "ready",
            voiceStatus: "missing",
        },
    } satisfies ProjectAsset;
    const audio = {
        id: "asset-audio",
        title: "雨声",
        mediaType: "audio",
        category: "other",
        status: "confirmed",
        primaryVersionId: "version-audio",
        versionCount: 1,
        usages: [],
        position: 2,
        storageKey: "resource:rain-audio",
        updatedAt: "2026-08-29T00:00:00Z",
    } satisfies ProjectAsset;
    return {
        assets: [scene, character, audio],
        shotReferences: [
            { id: "reference-scene", shotId: "shot-1", assetVersionId: "version-scene", role: "reference", status: "linked", createdAt: "2026-08-29T00:00:00Z" },
            { id: "reference-character", shotId: "shot-1", assetVersionId: "version-character", role: "reference", status: "linked", createdAt: "2026-08-29T00:00:01Z" },
            { id: "reference-audio", shotId: "shot-1", assetVersionId: "version-audio", role: "reference", status: "linked", createdAt: "2026-08-29T00:00:02Z" },
            { id: "reference-other-shot", shotId: "shot-2", assetVersionId: "version-scene", role: "reference", status: "linked", createdAt: "2026-08-29T00:00:03Z" },
        ],
    } as ProjectDetail;
}
