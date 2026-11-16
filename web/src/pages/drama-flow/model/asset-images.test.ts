import assert from "node:assert/strict";
import { describe, test } from "node:test";

import type { ProjectAsset } from "@/services/api/projects";

import { evidenceText, assetFields } from "./asset-fields";
import { assetImageQueueMessage, pendingAssetImages } from "./asset-images";

const asset = (id: string, category: string, status?: string, extra: Partial<ProjectAsset> = {}) => ({ id, title: id, category, ...(status ? { image: { status } } : {}), ...extra }) as unknown as ProjectAsset;

describe("「继续：角色场景资产」确认面板列哪些资产", () => {
    test("没出图、失败、等待的列出来；已出图、出图中、色卡、没有出图状态的不列", () => {
        const assets = [
            asset("none", "character", "none"),
            asset("failed", "environment", "failed"),
            asset("waiting", "prop", "waiting"),
            asset("stale", "character", "stale"),
            asset("bound", "character", "bound"),
            asset("queued", "prop", "queued"),
            asset("palette", "palette", "none"),
            asset("no-image", "character"),
        ];
        assert.deepEqual(
            pendingAssetImages(assets).map((item) => item.id),
            ["none", "failed", "waiting", "stale"],
        );
    });
});

describe("出图排队结果的提示", () => {
    test("有没排上的先警告", () => {
        assert.deepEqual(assetImageQueueMessage({ queued: ["a"], waiting: [], failed: ["b"] }), { level: "warning", text: "有 1 个资产没排上出图，原因写在卡片上" });
    });
    test("有等前面出完的写明等待", () => {
        assert.deepEqual(assetImageQueueMessage({ queued: ["a"], waiting: ["b", "c"], failed: [] }), { level: "success", text: "已排 1 个资产出图，2 个等前面的（如场景要用的道具）出完再接着排" });
    });
    test("全排上、什么都没排", () => {
        assert.deepEqual(assetImageQueueMessage({ queued: ["a", "b"], waiting: [], failed: [] }), { level: "success", text: "已排 2 个资产出图" });
        assert.deepEqual(assetImageQueueMessage({ queued: [], waiting: [], failed: [] }), { level: "info", text: "没有要出图的资产" });
    });
});

describe("资产的原文依据", () => {
    const structured = [{ kind: "台词", location: "第一场", text: "阿婆：喝口热的。" }, { kind: "动作", location: "", text: "林深接过橙汁。" }, { text: "" }];
    test("资产一体化：extractionDesign.evidence 的对象一条一行", () => {
        assert.equal(evidenceText({ extractionDesign: { evidence: structured } }), "台词 · 第一场：阿婆：喝口热的。\n动作：林深接过橙汁。");
    });
    test("旧资产：根上的 evidence / sourceQuote 字符串或字符串数组", () => {
        assert.equal(evidenceText({ evidence: "  雨夜，阿婆递橙汁。 " }), "雨夜，阿婆递橙汁。");
        assert.equal(evidenceText({ sourceQuote: ["第一句", " ", "第二句"] }), "第一句\n第二句");
        // 根上有就用根上的；根上为空才看提取设计。
        assert.equal(evidenceText({ evidence: "", extractionDesign: { evidence: structured.slice(1, 2) } }), "动作：林深接过橙汁。");
    });
    test("没有依据时为空（显示「没填」）", () => {
        assert.equal(evidenceText({}), "");
        assert.equal(evidenceText({ evidence: [], extractionDesign: { evidence: [{ kind: "台词" }] } }), "");
    });
    test("角色、场景、道具的「原文依据」行都读结构化依据", () => {
        const design = { extractionDesign: { evidence: structured.slice(0, 1) } };
        const character = assetFields(asset("c", "character", "none", { character: { definition: { name: "阿婆", ...design } } } as unknown as Partial<ProjectAsset>));
        const scene = assetFields(asset("s", "environment", "none", { visual: { definition: { description: "雨棚", ...design } } } as unknown as Partial<ProjectAsset>));
        const prop = assetFields(asset("p", "prop", "none", { visual: { definition: { description: "橙汁", ...design } } } as unknown as Partial<ProjectAsset>));
        for (const fields of [character, scene, prop]) assert.equal(fields.find((field) => field.label === "原文依据")?.value, "台词 · 第一场：阿婆：喝口热的。");
    });
});
