import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { storyboardRowsFromTask } from "@/lib/canvas/canvas-project-domain";
import { bindStoryboardRowAssets } from "@/pages/projects/detail/chapter-storyboard-production";
import type { GenerationTask } from "@/services/api/task-center";

// 线上实测（七床 E01，deepseek-v4.1-flash）的模型原文经柚子真实解析链得到分镜行；后端 Go 的端到端夹具 rows.json 就是这一步的输出。
const dir = new URL("../../backend/internal/drama/testdata/online-qichuang/", import.meta.url);

describe("线上实测输出 → 分镜行", () => {
    test("29 行都带镜头表 spec，与后端端到端夹具一致", () => {
        const text = readFileSync(new URL("model-output.txt", dir), "utf8");
        const task = { resultJson: JSON.stringify({ mode: "text", reasoning: "", text }) } as GenerationTask;
        const { rows } = storyboardRowsFromTask(task);
        expect(rows).toHaveLength(29);
        expect(rows.every((row) => row.spec && typeof row.spec === "object")).toBe(true);
        const fixture = JSON.parse(readFileSync(new URL("rows.json", dir), "utf8")) as { rows: Array<{ spec: Record<string, unknown> }> };
        expect(rows.map((row) => row.spec)).toEqual(fixture.rows.map((row) => row.spec));
    });

    test("第一期补丁上线后的实测（on3）：41 行带 spec，按资产清单绑定后与后端夹具一致（对话场的行不绑阿坤）", () => {
        const on3 = new URL("../online-qichuang-on3/", dir);
        const text = readFileSync(new URL("model-output.txt", on3), "utf8");
        const assets = JSON.parse(readFileSync(new URL("assets.json", on3), "utf8"));
        const task = { resultJson: JSON.stringify({ mode: "text", reasoning: "", text }) } as GenerationTask;
        const rows = bindStoryboardRowAssets(storyboardRowsFromTask(task).rows, assets);
        expect(rows).toHaveLength(41);
        const fixture = JSON.parse(readFileSync(new URL("rows.json", on3), "utf8")) as { rows: Array<{ spec: Record<string, unknown>; assetBindings: unknown[] }> };
        expect(rows.map((row) => row.spec)).toEqual(fixture.rows.map((row) => row.spec));
        expect(rows.map((row) => row.assetBindings)).toEqual(fixture.rows.map((row) => row.assetBindings));
        const dialogue = rows.filter((row) => row.spec?.scene === "E01S02");
        expect(dialogue.length).toBeGreaterThan(0);
        expect(dialogue.some((row) => row.assetBindings.some((binding) => binding.nodeId === "char-3"))).toBe(false);
    });
});
