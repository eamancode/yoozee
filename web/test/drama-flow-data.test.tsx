import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { projectTasksKey, unitWorkspaceKey, useFlowData, type FlowData } from "../src/pages/drama-flow/hooks/use-flow-data";
import { dramaBriefKey } from "../src/pages/projects/workbench/detail/drama-brief";
import { dramaBibleKey, dramaProgressKey } from "../src/pages/projects/workbench/detail/drama-panels";
import { dramaEpisodesKey } from "../src/services/api/project-drama";

// 只在服务端渲染一次读缓存，不触发任何请求：缓存里先放好各个接口的数据。
function render(client: QueryClient, unitId: string) {
    let data: FlowData | undefined;
    function Probe() {
        data = useFlowData("p1", unitId);
        return null;
    }
    renderToStaticMarkup(createElement(QueryClientProvider, { client }, createElement(Probe)));
    return data!;
}

function seeded() {
    const client = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false, retryOnMount: false } } });
    const summary = { id: "episode-1", projectId: "p1", kind: "episode", title: "第一集", sourceText: "", wordCount: 28, status: "draft", position: 0, createdAt: "", updatedAt: "" };
    client.setQueryData(["project", "p1", "core"], { project: { id: "p1", name: "测试", generationModel: "" } });
    client.setQueryData(["project", "p1", "units"], { units: [summary], canvasCounts: {} });
    client.setQueryData(["project", "p1", "assets"], { assets: [] });
    client.setQueryData(projectTasksKey("p1"), []);
    client.setQueryData(dramaBibleKey("p1"), {});
    client.setQueryData(dramaBriefKey("p1"), {});
    client.setQueryData(dramaEpisodesKey("p1"), { episodes: [], adaptedChapterIds: [] });
    client.setQueryData(dramaProgressKey("p1"), { segments: [], counts: { segments: 0, generated: 0, inCut: 0, overLimit: 0 } });
    return { client, summary };
}

test("剧本正文只从 workspace 的完整单元读，摘要列表里的空正文不外露成正文", () => {
    const { client, summary } = seeded();
    const before = render(client, "episode-1");
    expect(before.unit?.id).toBe("episode-1");
    expect(before.fullUnit).toBeUndefined();

    const full = { ...summary, sourceText: "<!-- sources: c1:1-3 --><p>第一场：阿婆把热橙汁递给林深。</p>" };
    client.setQueryData(unitWorkspaceKey("p1", "episode-1"), { unit: full, assets: [], tasks: [], workflows: [], shots: [], shotRevisions: [], shotArtifacts: [], shotReferences: [], assetCandidates: [] });
    const after = render(client, "episode-1");
    expect(after.fullUnit?.sourceText).toBe(full.sourceText);
    expect(after.detail?.units[0].sourceText).toBe(full.sourceText);
    client.clear();
});

test("资产读失败时保留失败状态，页面据此显示「没读到」而不是空列表", () => {
    const { client } = seeded();
    client
        .getQueryCache()
        .find({ queryKey: ["project", "p1", "assets"] })!
        .setState({ data: undefined, error: new Error("模拟资产接口 500"), status: "error" });
    const data = render(client, "episode-1");
    expect(data.assetsQuery.isError).toBe(true);
    expect(data.assetsQuery.data).toBeUndefined();
    client.clear();
});
