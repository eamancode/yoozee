import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { projectTasksKey, unitWorkspaceKey, useFlowData } from "../src/pages/drama-flow/hooks/use-flow-data";
import { videoChainsKey } from "../src/services/api/project-video-chains";
import { StageVideo } from "../src/pages/drama-flow/parts/stage-video";
import { dramaBriefKey } from "../src/pages/projects/workbench/detail/drama-brief";
import { dramaBibleKey, dramaProgressKey } from "../src/pages/projects/workbench/detail/drama-panels";
import { dramaEpisodesKey } from "../src/services/api/project-drama";

// 真实的 useFlowData + 真实的出片页，只在服务端渲染一次读缓存，不发任何请求。
// 缓存里没放的查询就是「还在读」，用 setState 放进去的错误就是「没读到」。

const unit = (id: string, position: number) => ({ id, projectId: "p1", kind: "episode", title: `第${position + 1}集`, sourceText: "", wordCount: 10, status: "draft", position, createdAt: "", updatedAt: "" });
const segment = (shotId: string, unitId: string) => ({
    shotId,
    unitId,
    segmentId: shotId,
    title: "",
    durationMs: 8000,
    overLimit: false,
    hasDialogue: false,
    script: true,
    refs: true,
    generated: false,
    reviewed: false,
    inCut: false,
    accepted: false,
    takes: 0,
});
const chainStep = (shotId: string, status: string) => ({ shotId, revisionId: "r", title: shotId, scene: "", contentSeconds: 8, generationSeconds: 8, model: "m", status, phase: "" });
const chain = (unitId: string, steps: ReturnType<typeof chainStep>[]) => ({ id: `chain-${unitId}`, projectId: "p1", unitId, status: "running", stage: "", previewHash: "", totalMicrocredits: 0, whiteModelStoryboardEnabled: false, steps });

function seeded({ progress = true }: { progress?: boolean } = {}) {
    const client = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false, retryOnMount: false } } });
    client.setQueryData(["project", "p1", "core"], { project: { id: "p1", name: "测试", generationModel: "" } });
    client.setQueryData(["project", "p1", "units"], { units: [unit("e1", 0), unit("e2", 1)], canvasCounts: {} });
    client.setQueryData(["project", "p1", "assets"], { assets: [] });
    client.setQueryData(projectTasksKey("p1"), []);
    client.setQueryData(dramaBibleKey("p1"), {});
    client.setQueryData(dramaBriefKey("p1"), {});
    client.setQueryData(dramaEpisodesKey("p1"), { episodes: [], adaptedChapterIds: [] });
    client.setQueryData(unitWorkspaceKey("p1", "e1"), { unit: unit("e1", 0), assets: [], tasks: [], workflows: [], shots: [], shotRevisions: [], shotArtifacts: [], shotReferences: [], assetCandidates: [] });
    client.setQueryData(["wallet", "summary"], { account: { availableMicrocredits: 0 } });
    if (progress)
        client.setQueryData(dramaProgressKey("p1"), {
            segments: [segment("s1", "e1"), segment("s2", "e1"), segment("s3", "e2")],
            counts: { segments: 3, generated: 0, inCut: 0, overLimit: 0 },
        });
    return client;
}

function fail(client: QueryClient, queryKey: readonly unknown[]) {
    client
        .getQueryCache()
        .build(client, { queryKey })
        .setState({ data: undefined, error: new Error("模拟接口 500"), status: "error" });
}

/** 渲染出片页，返回摘要行「标签 → 值」。 */
function summaryOf(client: QueryClient) {
    function Page() {
        const data = useFlowData("p1", "e1");
        return createElement(StageVideo, { data, canGenerate: true, onSelect() {}, onGenerate() {}, onStopChain() {}, onRetryAll() {}, onStopAll() {} });
    }
    const html = renderToStaticMarkup(createElement(QueryClientProvider, { client }, createElement(Page)));
    const values = Object.fromEntries([...html.matchAll(/<dt>(.*?)<\/dt><dd>(.*?)<\/dd>/g)].map((match) => [match[1], match[2]]));
    client.clear();
    return { values, html };
}

test("进度首次就没读到：段、已出、生成中、排队、失败、还没出都写「没读到」", () => {
    const client = seeded({ progress: false });
    fail(client, dramaProgressKey("p1"));
    const { values } = summaryOf(client);
    for (const label of ["段", "已出", "生成中", "排队", "失败", "还没出"]) expect(values[label]).toBe("没读到");
});

test("进度刷新失败但还有旧缓存：同样不拿旧数字顶替", () => {
    const client = seeded();
    client
        .getQueryCache()
        .find({ queryKey: dramaProgressKey("p1") })!
        .setState({ error: new Error("刷新失败"), status: "error" });
    client.setQueryData(videoChainsKey("p1", "e1"), { chains: [] });
    const { values } = summaryOf(client);
    for (const label of ["段", "生成中", "失败"]) expect(values[label]).toBe("没读到");
});

test("进度读到、这一集的出片链还在读：生成中、排队、失败写「…」，段卡不写「还没出」", () => {
    const { values, html } = summaryOf(seeded());
    expect(values["段"]).toBe("2");
    for (const label of ["生成中", "排队", "失败", "还没出"]) expect(values[label]).toBe("…");
    expect(html).toContain("正在读出片状态…");
    expect(html).not.toContain('drama-flow-chip">还没出<');
    // 不知道这一段是不是正在出，提交按钮先禁用。
    expect(html).toMatch(/<button[^>]*disabled=""[^>]*><span>出这一段<\/span>/);
});

test("这一集的出片链没读到：三项写「没读到」并给出重试", () => {
    const client = seeded();
    fail(client, videoChainsKey("p1", "e1"));
    const { values, html } = summaryOf(client);
    for (const label of ["生成中", "排队", "失败", "还没出"]) expect(values[label]).toBe("没读到");
    expect(html).toContain("这一集的出片状态（生成中、排队、失败）没读到");
});

test("别的集的出片链没读到，不影响这一集的数字", () => {
    const client = seeded();
    client.setQueryData(videoChainsKey("p1", "e1"), { chains: [chain("e1", [chainStep("s1", "running"), chainStep("s2", "failed")])] });
    fail(client, videoChainsKey("p1", "e2"));
    const { values } = summaryOf(client);
    expect(values["生成中"]).toBe("1");
    expect(values["失败"]).toBe("1");
    expect(values["还没出"]).toBe("0");
});
