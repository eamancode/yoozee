import { expect, test } from "bun:test";
import { fileURLToPath } from "node:url";
import { App } from "antd";
import { renderToStaticMarkup } from "react-dom/server";

import { SLLCatalogResults } from "../src/pages/admin/channels/sll-catalog-dialog";
import { ChannelModelManager } from "../src/pages/admin/components/channel-model-manager";
import type { SLLCatalogRow, SLLCatalogView } from "../src/services/api/admin-sll-catalog";
import type { ModelChannel } from "../src/stores/use-config-store";

const row: SLLCatalogRow = {
    modelId: "model.a",
    displayName: "Seedance 2.5（MJ）",
    group: "default",
    groupRatio: 1,
    available: true,
    enabled: false,
    capabilities: { durationMin: 30, durationMax: 30, maxImages: 7, maxAudios: 0 },
    tiers: [{ resolution: "720p", costMicroCny: 6_000_000 }],
    metric: { state: "value", source: "SLL 官方", windowHours: 24, rate: 0, sampleCount: null, stale: false },
};
const view: SLLCatalogView = {
    group: "default",
    source: "https://api.lecapi.net/api/pricing",
    collectedAt: "2026-11-17T10:00:00Z",
    added: 2,
    updated: 1,
    excluded: [{ modelId: "model.a", resolution: "1080p", reason: "超出单条预算" }],
    rows: [row, { ...row, modelId: "model.b", metric: { ...row.metric, state: "unavailable", rate: null } }, { ...row, modelId: "model.c", metric: { ...row.metric, state: "fetch_failed", rate: 89.5, stale: true } }],
};

test("catalog shows exact upstream IDs, costs, exclusions, unknown capabilities, real zero and collection failures", () => {
    const html = renderToStaticMarkup(<SLLCatalogResults view={view} keyword="" />);
    for (const text of ["model.a", "model.b", "model.c", "倍率 1", "720p ¥6/条", "1080p", "超出单条预算", "已停用", "画幅 待核实", "音频 不支持", "0%", "供应商无统计", "样本数 未公开", "采集失败，上次 89.5%", "已过期"]) {
        expect(html).toContain(text);
    }
    const filtered = renderToStaticMarkup(<SLLCatalogResults view={view} keyword="MODEL.B" />);
    expect(filtered).toContain("model.b");
    expect(filtered).not.toContain("model.a");
    expect(filtered).not.toContain("model.c");
    const byName = renderToStaticMarkup(<SLLCatalogResults view={view} keyword="（MJ）" />);
    expect(byName).toContain("model.a");
    expect(byName).toContain("model.b");
});

test("SLL catalog action follows the server's channel projection", () => {
    const channel: ModelChannel = { id: "system", name: "同名渠道", baseUrl: "", apiKey: "", apiFormat: "openai", models: [] };
    const render = (available: boolean) =>
        renderToStaticMarkup(
            <App>
                <ChannelModelManager channel={{ ...channel, sllCatalogAvailable: available }} onChanged={() => {}} />
            </App>,
        );
    expect(render(true)).toContain("SLL 视频线路目录");
    expect(render(false)).not.toContain("SLL 视频线路目录");
});

test("admin catalog dialog drives independent fixture HTTP actions", async () => {
    const harness = new URL("./fixtures/sll-catalog-actions.harness.tsx", import.meta.url);
    const run = Bun.spawn([process.execPath, "test", fileURLToPath(harness)], { stdout: "pipe", stderr: "pipe" });
    const [code, stdout, stderr] = await Promise.all([run.exited, new Response(run.stdout).text(), new Response(run.stderr).text()]);
    if (code !== 0) throw new Error(stdout + stderr);
}, 45_000);
