import { describe, expect, test } from "bun:test";

import { buildCanvasOperations, repairPendingCanvasMutationRequest } from "@/services/user-data-sync";
import type { CanvasProject } from "@/stores/canvas/use-canvas-store";

function canvas(patch: Partial<CanvasProject> = {}): CanvasProject {
    return {
        id: "canvas",
        title: "自由画布 1",
        createdAt: "",
        updatedAt: "",
        nodes: [],
        connections: [],
        chatSessions: [],
        activeChatId: null,
        backgroundMode: "dots" as CanvasProject["backgroundMode"],
        showImageInfo: false,
        viewport: { x: 0, y: 0, k: 1 },
        previsScenes: [],
        ...patch,
    };
}

describe("画布 operations 生成", () => {
    test("新画布只发 create_canvas，不追加会与创建字段冲突的 set_field", () => {
        const operations = buildCanvasOperations(undefined, canvas());
        expect(operations[0]?.type).toBe("create_canvas");
        expect(operations.some((operation) => operation.type === "set_field")).toBe(false);
    });

    test("已有画布改标题时带上 beforeValue", () => {
        const operations = buildCanvasOperations(canvas({ title: "旧" }), canvas({ title: "新" }));
        expect(operations).toEqual([{ type: "set_field", field: "title", value: "新", beforeValue: "旧" }]);
    });

    test("修复旧 journal：移除新画布中重复的 set_field，保留 mutationId", () => {
        const legacy = JSON.parse(
            JSON.stringify({
                mutationId: "m-1",
                operations: [
                    { type: "create_canvas", id: "canvas", patch: { title: "自由画布 1" } },
                    { type: "set_field", field: "title", value: "自由画布 1", beforeValue: undefined },
                ],
            }),
        );
        const repaired = repairPendingCanvasMutationRequest(legacy, undefined, canvas());
        expect(repaired.mutationId).toBe("m-1");
        expect(repaired.operations).toEqual([{ type: "create_canvas", id: "canvas", patch: { title: "自由画布 1" } }]);
    });

    test("修复旧 journal：移除空 projectId 到缺省值的语义 no-op", () => {
        const legacy = {
            mutationId: "m-3",
            operations: [
                { type: "create_node", node: { id: "node-1" } },
                { type: "set_field", field: "projectId", value: null, beforeValue: "" },
            ],
        } as never;
        const repaired = repairPendingCanvasMutationRequest(legacy, canvas({ projectId: "" }), canvas({ projectId: undefined }));
        expect(repaired.mutationId).toBe("m-3");
        expect(repaired.operations).toEqual([{ type: "create_node", node: { id: "node-1" } }]);
    });
    test("修复旧 journal：缺失的可选字段基线编码为 null", () => {
        const legacy = JSON.parse(JSON.stringify({ mutationId: "m-2", operations: [{ type: "set_field", field: "timeline", value: { tracks: [] }, beforeValue: undefined }] }));
        const repaired = repairPendingCanvasMutationRequest(legacy, canvas(), canvas());
        expect(repaired.operations).toEqual([{ type: "set_field", field: "timeline", value: { tracks: [] }, beforeValue: null }]);
    });

    test("patch_node 的 before 不会因为旧值是 undefined 被 JSON 丢成空对象", () => {
        const base = canvas({ nodes: [{ id: "n1", metadata: { prompt: undefined, x: 1 } }] as never });
        const next = canvas({ nodes: [{ id: "n1", metadata: { prompt: "hello", x: 1 } }] as never });
        const wire = JSON.parse(JSON.stringify(buildCanvasOperations(base, next)));
        const patch = wire.find((operation: { type: string }) => operation.type === "patch_node");
        // 服务端要求 patch_node 必须带非空 before，否则整笔按「节点补丁缺少字段基线」拒绝。
        expect(patch.before).toEqual({ "metadata.prompt": null });
    });

    test("修复旧 journal：没有本地投影时，before 为空用已确认基线补回字段值", () => {
        const legacy = JSON.parse(
            JSON.stringify({
                mutationId: "m-9",
                operations: [{ type: "patch_node", id: "n1", patch: { "metadata.prompt": "hello" }, before: {} }],
            }),
        );
        const baseline = canvas({ nodes: [{ id: "n1", metadata: { prompt: "旧值" } }] as never });
        const repaired = repairPendingCanvasMutationRequest(legacy, baseline, undefined);
        expect(repaired.mutationId).toBe("m-9");
        expect(repaired.operations).toEqual([{ type: "patch_node", id: "n1", patch: { "metadata.prompt": "hello" }, before: { "metadata.prompt": "旧值" } }]);
    });

    test("修复旧 journal：基线里也没有该字段时补 null", () => {
        const legacy = JSON.parse(
            JSON.stringify({
                mutationId: "m-10",
                operations: [{ type: "patch_node", id: "n1", patch: { "metadata.prompt": "hello" }, before: {} }],
            }),
        );
        const repaired = repairPendingCanvasMutationRequest(legacy, canvas({ nodes: [{ id: "n1" }] as never }), undefined);
        expect(repaired.operations).toEqual([{ type: "patch_node", id: "n1", patch: { "metadata.prompt": "hello" }, before: { "metadata.prompt": null } }]);
    });

    test("修复旧 journal：patch 被序列化成空对象的 patch_node 按基线+投影整批重建", () => {
        // 旧包把 undefined 值序列化掉，journal 里存下来的就是空 patch；重放是原样发送，
        // 所以只有整批重建才能恢复这次编辑、并且让画布重新同步上。
        const legacy = JSON.parse(
            JSON.stringify({
                mutationId: "m-11",
                operations: [{ type: "patch_node", id: "n1", patch: {}, before: { "metadata.prompt": "旧值" } }],
            }),
        );
        const baseline = canvas({ nodes: [{ id: "n1", metadata: { prompt: "旧值" } }] as never });
        const prepared = canvas({ nodes: [{ id: "n1", metadata: { prompt: "新值" } }] as never });
        const repaired = repairPendingCanvasMutationRequest(legacy, baseline, prepared);
        expect(repaired.mutationId).toBe("m-11");
        expect(repaired.operations).toEqual([{ type: "patch_node", id: "n1", patch: { "metadata.prompt": "新值" }, before: { "metadata.prompt": "旧值" } }]);
    });

    test("修复旧 journal：before 为空的 patch_node 也走整批重建", () => {
        const legacy = JSON.parse(
            JSON.stringify({
                mutationId: "m-12",
                operations: [{ type: "patch_node", id: "n1", patch: { "metadata.prompt": "新值" }, before: {} }],
            }),
        );
        const baseline = canvas({ nodes: [{ id: "n1", metadata: { prompt: "旧值" } }] as never });
        const prepared = canvas({ nodes: [{ id: "n1", metadata: { prompt: "新值" } }] as never });
        const repaired = repairPendingCanvasMutationRequest(legacy, baseline, prepared);
        expect(repaired.mutationId).toBe("m-12");
        expect(repaired.operations).toEqual([{ type: "patch_node", id: "n1", patch: { "metadata.prompt": "新值" }, before: { "metadata.prompt": "旧值" } }]);
    });

    test("修复旧 journal：健康的请求原样返回", () => {
        const healthy = {
            mutationId: "m-13",
            operations: [{ type: "patch_node", id: "n1", patch: { "metadata.prompt": "新值" }, before: { "metadata.prompt": "旧值" } }],
        } as never;
        const baseline = canvas({ nodes: [{ id: "n1", metadata: { prompt: "旧值" } }] as never });
        const prepared = canvas({ nodes: [{ id: "n1", metadata: { prompt: "新值" } }] as never });
        expect(repairPendingCanvasMutationRequest(healthy, baseline, prepared)).toBe(healthy);
    });

    test("修复旧 journal：没有基线可重建时，空 patch 的 patch_node 丢弃而不是继续卡 400", () => {
        const legacy = JSON.parse(
            JSON.stringify({
                mutationId: "m-14",
                operations: [
                    { type: "patch_node", id: "n1", patch: {}, before: { "metadata.prompt": "旧值" } },
                    { type: "patch_node", id: "n2", patch: { "metadata.prompt": "新值" }, before: { "metadata.prompt": "旧值" } },
                ],
            }),
        );
        const repaired = repairPendingCanvasMutationRequest(legacy, undefined, undefined);
        expect(repaired.operations).toEqual([{ type: "patch_node", id: "n2", patch: { "metadata.prompt": "新值" }, before: { "metadata.prompt": "旧值" } }]);
    });
});
