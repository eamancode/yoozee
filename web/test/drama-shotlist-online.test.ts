import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { storyboardRowsFromTask } from "@/lib/canvas/canvas-project-domain";
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
});
