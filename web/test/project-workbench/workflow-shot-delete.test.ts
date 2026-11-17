import { expect, test } from "bun:test";
import { deleteProjectShot } from "@/services/api/projects";
import { apiClient } from "@/services/api/request";

test("镜头编辑器在保存按钮左侧提供带确认的删除操作", async () => {
    const source = await Bun.file(new URL("../../src/pages/projects/workbench/detail/workflow-production-workbench.tsx", import.meta.url)).text();
    const deleteButton = source.indexOf(">删除镜头</Button>");
    const saveButton = source.indexOf(">保存脚本</Button>");

    expect(source).toContain("deleteProjectShot(projectId, shotId)");
    expect(source).toContain("脚本版本、资产引用和生成产物都会被删除");
    expect(source).toContain("okButtonProps: { danger: true }");
    expect(deleteButton).toBeGreaterThan(-1);
    expect(saveButton).toBeGreaterThan(deleteButton);
});

test("镜头删除 API 使用项目和镜头双重路径作用域", async () => {
    const previousAdapter = apiClient.defaults.adapter;
    const calls: Array<{ method?: string; url?: string; body: unknown }> = [];
    apiClient.defaults.adapter = async (config) => {
        calls.push({ method: config.method, url: config.url, body: config.data === undefined ? undefined : JSON.parse(config.data) });
        return { config, headers: {}, status: 200, statusText: "OK", data: { code: 0, data: { deleted: true } } };
    };

    try {
        const confirmed = { expectedRevisionId: "rev-7", expectedArtifactIds: ["take-1", "take-2"] };
        expect(await deleteProjectShot("project/a", "shot?b", confirmed)).toEqual({ deleted: true });
        await deleteProjectShot("project/c", "shot#d");

        expect(calls).toEqual([
            { method: "delete", url: "/projects/project%2Fa/shots/shot%3Fb", body: confirmed },
            { method: "delete", url: "/projects/project%2Fc/shots/shot%23d", body: undefined },
        ]);
    } finally {
        apiClient.defaults.adapter = previousAdapter;
    }
});
