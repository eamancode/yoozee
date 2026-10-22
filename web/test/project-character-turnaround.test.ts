import { describe, expect, test } from "bun:test";

import type { GenerationTask } from "../src/services/api/task-center";
import { createModelChannel, defaultConfig, encodeChannelModel } from "../src/stores/use-config-store";
import { pendingTurnaroundAssetIds, turnaroundImageModel } from "../src/pages/projects/detail/project-character-media";

function task(patch: Partial<GenerationTask>): GenerationTask {
    return { id: "task-1", type: "canvas_image", status: "running", prompt: "", progress: 0, attempts: 1, createdAt: "2026-10-19T00:00:00Z", updatedAt: "2026-10-19T00:00:00Z", ...patch } as GenerationTask;
}

function imageConfig() {
    const channel = createModelChannel({ id: "image-channel", name: "图片", baseUrl: "https://example.test", apiKey: "test-key", interfaceType: "openai", models: ["global-image", "project-image"] });
    const globalModel = encodeChannelModel(channel.id, "global-image");
    const projectModel = encodeChannelModel(channel.id, "project-image");
    return { config: { ...defaultConfig, channels: [channel], model: globalModel, imageModel: globalModel }, globalModel, projectModel };
}

describe("角色三视图", () => {
    test("刷新后按任务 nodeId 找回仍在排队或生成的角色", () => {
        const ids = pendingTurnaroundAssetIds([
            task({ id: "a", status: "queued", clientContext: { nodeId: "character-turnaround:hero" } }),
            task({ id: "b", status: "running", clientContext: { nodeId: "character-turnaround:villain" } }),
            task({ id: "c", status: "succeeded", clientContext: { nodeId: "character-turnaround:done" } }),
            task({ id: "d", status: "running", clientContext: { nodeId: "storyboard-node" } }),
        ]);
        expect([...ids].sort()).toEqual(["hero", "villain"]);
    });

    test("三视图优先用项目默认图片模型，渠道里没有时退回全局图片模型", () => {
        const { config, globalModel, projectModel } = imageConfig();
        expect(turnaroundImageModel(config, projectModel)).toBe(projectModel);
        expect(turnaroundImageModel(config, "")).toBe(globalModel);
        expect(turnaroundImageModel(config, "missing-channel::missing-model")).toBe(globalModel);
    });

    test("资产页把后端仍在跑的三视图并入生成中状态", async () => {
        const source = await Bun.file(new URL("../src/pages/projects/detail/assets.tsx", import.meta.url)).text();
        expect(source).toContain("listGenerationTasks(100, { projectId: detail.project.id, activeOnly: true })");
        expect(source).toContain("...serverGeneratingIds]");
        expect(source).toContain("turnaroundImageModel(effectiveConfig, detail.project.defaultImageModel)");
    });
});
