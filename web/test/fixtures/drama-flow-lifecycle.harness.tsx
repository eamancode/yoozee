/**
 * 短剧新工作流真实组件生命周期回归（独立进程跑，DOM 全局量不污染其他用例）。
 *
 * 挂真实路由页 DramaFlowDetailPage（分镜、定妆、出片、项目信息、运行记录都是真实组件），
 * 接口走 drama-flow-api-fixture（无模型、只读 + 分镜保存）；不 mock 任何被测组件。
 * scrollIntoView 分别按「返回 Promise」（Chrome 157 实测）和「返回 undefined」两种条件跑，
 * 用例结束后恢复原实现。
 */
import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";
import { createEpisodeProject } from "./drama-flow-episode-project";
import { createDramaJourneyProject } from "./drama-flow-journey-project";
import type { DramaModelCatalog } from "@/services/api/drama";
import type { DramaCompileResult, DramaProgress } from "@/services/api/project-drama";
import type { ProjectUnitWorkspace } from "@/services/api/projects";
import { projectVideoRoutingKey } from "@/services/api/project-video-routing";

const dom = new Window({ url: "http://localhost/", width: 1440, height: 1000 });
const originals = new Map<string, PropertyDescriptor | undefined>();
const globals: Record<string, unknown> = {
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    location: dom.location,
    history: dom.history,
    localStorage: dom.localStorage,
    sessionStorage: dom.sessionStorage,
    HTMLElement: dom.HTMLElement,
    HTMLInputElement: dom.HTMLInputElement,
    HTMLTextAreaElement: dom.HTMLTextAreaElement,
    HTMLButtonElement: dom.HTMLButtonElement,
    HTMLBodyElement: dom.HTMLBodyElement,
    HTMLHtmlElement: dom.HTMLHtmlElement,
    Element: dom.Element,
    Node: dom.Node,
    Text: dom.Text,
    DocumentFragment: dom.DocumentFragment,
    SVGElement: dom.SVGElement,
    ShadowRoot: dom.ShadowRoot,
    Event: dom.Event,
    CustomEvent: dom.CustomEvent,
    KeyboardEvent: dom.KeyboardEvent,
    MouseEvent: dom.MouseEvent,
    MutationObserver: dom.MutationObserver,
    ResizeObserver: dom.ResizeObserver,
    IntersectionObserver: dom.IntersectionObserver,
    getComputedStyle: dom.getComputedStyle.bind(dom),
    matchMedia: dom.matchMedia.bind(dom),
    requestAnimationFrame: dom.requestAnimationFrame.bind(dom),
    cancelAnimationFrame: dom.cancelAnimationFrame.bind(dom),
    IS_REACT_ACT_ENVIRONMENT: true,
};
for (const [key, value] of Object.entries(globals)) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}

// 浏览器 API 的返回值按用例切换：Chrome 157 的原生 scrollIntoView 返回 Promise。
const nativeScroll = dom.Element.prototype.scrollIntoView;
let scrollReturns: "promise" | "undefined" = "promise";
const scrolled: string[] = [];
dom.Element.prototype.scrollIntoView = function (this: Element) {
    scrolled.push(this.className || this.tagName);
    return (scrollReturns === "promise" ? Promise.resolve() : undefined) as unknown as void;
};
// happy-dom 的 Web Animations 取消时会抛未处理的 AbortError；去掉 animate，让 motion 走 JS 动画。
for (const proto of [dom.Element.prototype, dom.HTMLElement.prototype] as unknown as Array<Record<string, unknown>>) delete proto.animate;
// 资源图片等原始 fetch 一律拦下：回归不出网。
const blockedFetches: string[] = [];
const originalFetch = globalThis.fetch;
globalThis.fetch = (async (input: unknown) => {
    blockedFetches.push(String(input));
    throw new Error("fetch blocked in drama-flow lifecycle harness");
}) as typeof fetch;

const { act, createElement } = await import("react");
const { createRoot } = await import("react-dom/client");
const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
const { MemoryRouter, Route, Routes, useLocation, useNavigate } = await import("react-router");
const { App } = await import("antd");
const { apiClient } = await import("@/services/api/request");
const { useConfigStore } = await import("@/stores/use-config-store");
const { systemChannelModelChannels } = await import("@/lib/user-session");
const { projectLockedStyle } = await import("@/pages/projects/workbench/detail/chapter-style-lock");
const { dramaBibleKey, dramaSegmentsKey } = await import("@/pages/projects/workbench/detail/drama-panels");
const { default: DramaFlowDetailPage } = await import("@/pages/drama-flow/detail");
const { referenceMediaForCompiledFiles } = await import("@/pages/projects/workbench/detail/workflow-shot-references");
const { resourceIdFromStorageKey } = await import("@/services/api/resources");
const fx = await import("./drama-flow-api-fixture");

afterAll(() => {
    dom.Element.prototype.scrollIntoView = nativeScroll;
    globalThis.fetch = originalFetch;
    dom.happyDOM.abort();
    for (const [key, value] of originals) {
        if (value) Object.defineProperty(globalThis, key, value);
        else Reflect.deleteProperty(globalThis, key);
    }
});

let fixture = fx.createDramaFlowFixture();
apiClient.defaults.adapter = async (config) => {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries((config.params as Record<string, unknown>) || {})) if (value !== undefined && value !== null) params.set(key, String(value));
    const query = params.toString();
    const body = typeof config.data === "string" && config.data ? JSON.parse(config.data) : config.data;
    const result = fixture.handle(config.method || "get", `${config.url}${query ? `?${query}` : ""}`, body);
    if (!result) return new Promise(() => {});
    // 和真实网络一样每次给新解析的 JSON，不把夹具内部对象的引用交出去。
    return { status: result.status, statusText: "", headers: {}, config, data: JSON.parse(JSON.stringify(result.body)) };
};
// 模型目录：系统渠道照登录后的方式并进配置，项目默认视频模型才认得出来。
useConfigStore.getState().mergeSystemChannels(systemChannelModelChannels(fixture.catalog.channels as never));

const flush = async (rounds = 6) => {
    for (let index = 0; index < rounds; index += 1) await act(async () => await new Promise((resolve) => setTimeout(resolve, 0)));
};

let navigateTo: (path: string) => void = () => {};
let currentPath = "";
function RouterProbe() {
    const navigate = useNavigate();
    const location = useLocation();
    navigateTo = (path) => void navigate(path);
    currentPath = `${location.pathname}${location.search}`;
    return null;
}

type MountOptions = Parameters<typeof fx.createDramaFlowFixture>[0] & { prepare?: (fixture: ReturnType<typeof fx.createDramaFlowFixture>) => void };

async function mount(path: string, { prepare, ...options }: MountOptions = {}) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
    fixture = fx.createDramaFlowFixture(options);
    prepare?.(fixture);
    const errors: string[] = [];
    const host = document.createElement("div");
    document.body.append(host);
    const record = (error: unknown) => errors.push(error instanceof Error ? `${error.name}: ${error.message}` : String(error));
    const root = createRoot(host, { onUncaughtError: record, onCaughtError: record, onRecoverableError: record });
    const consoleError = console.error;
    console.error = (...args: unknown[]) => {
        const text = args.map(String).join(" ");
        if (/is not a function|destroy is not a function|Cannot update an unmounted/.test(text)) errors.push(text);
    };
    const routes = createElement(
        Routes,
        null,
        createElement(Route, { path: "/drama-flow/:projectId", element: createElement(DramaFlowDetailPage) }),
        createElement(Route, { path: "/drama-flow/:projectId/:stage", element: createElement(DramaFlowDetailPage) }),
        createElement(Route, { path: "*", element: createElement("p", null, "离开了新工作流") }),
    );
    await act(async () => root.render(createElement(QueryClientProvider, { client }, createElement(App, null, createElement(MemoryRouter, { initialEntries: [path] }, routes, createElement(RouterProbe))))));
    await flush();
    return {
        host,
        client,
        errors,
        text: () => document.body.textContent || "",
        click: async (element: Element | null | undefined) => {
            if (!element) throw new Error("要点的元素没找到");
            await act(async () => void (element as HTMLElement).click());
            await flush();
        },
        go: async (next: string) => {
            await act(async () => navigateTo(next));
            await flush();
        },
        dispose: async () => {
            await act(async () => root.unmount());
            host.remove();
            console.error = consoleError;
            client.clear();
        },
    };
}

const byText = (selector: string, text: string) => [...document.querySelectorAll(selector)].find((node) => (node.textContent || "").includes(text));
const summaryValue = (label: string) => {
    for (const row of document.querySelectorAll("dl.drama-flow-summary > div")) {
        if (row.querySelector("dt")?.textContent === label) return (row.querySelector(".drama-flow-model-name") ?? row.querySelector("dd"))?.textContent || "";
    }
    return undefined;
};
const infoValue = (label: string) => {
    const terms = [...document.querySelectorAll("dl.drama-flow-info-facts dt")];
    return terms.find((node) => node.textContent === label)?.nextElementSibling?.textContent || undefined;
};
const P = fx.FIXTURE_PROJECT_ID;

test("单元资产绑定：固定版本、静默角色、试听、同类更换、取消、刷新和解除", async () => {
    const shot = fx.FIXTURE_SHOT_ID;
    const path = `/projects/${P}/shots/${shot}/assets`;
    const compileModels: string[] = [];
    const writes: Array<[string, unknown]> = [];
    const actor = (id: string, title: string, sample?: string) => ({
        id,
        title,
        category: "character",
        primaryVersionId: id + "-current",
        versionCount: 2,
        mediaType: "image",
        usages: [],
        storageKey: "resource:" + fx.FIXTURE_IMAGE_PORTRAIT,
        character: { version: 2, versionId: id + "-current", definition: {}, representations: [], voice: { profile: { sampleResourceId: sample } } },
    });
    const main = actor("a", "顾音", "new-sample");
    const variant = { ...actor("dress", "顾音·夜班"), variant: { parentId: main.id } };
    const library = [{ ...main, variants: [variant] }, actor("b", "顾宁", "shared"), { ...actor("p", "口琴"), category: "prop" }];
    const versionImages: Record<string, string> = { pinned: fx.FIXTURE_IMAGE_SQUARE, "b-current": fx.FIXTURE_IMAGE_PORTRAIT, "dress-current": "res-night-costume", "p-current": "res-harmonica" };
    const row = (asset: typeof main, version: string, sample?: string) => ({
        id: "r-" + version,
        shotId: shot,
        assetVersionId: version,
        role: "reference",
        source: "segment",
        status: "linked",
        asset,
        referencedVersion: { id: version, assetId: asset.id, version: 1, sampleResourceId: sample, representations: [{ id: version, resourceId: versionImages[version], role: "primary", mediaType: "image/png" }] },
    });
    const refs = [row(main, "pinned", "shared"), row(library[1], "b-current", "shared")];
    const ok = (data: unknown) => ({ status: 200, body: { code: 0, data, msg: "ok" } });
    const page = await mount(`/drama-flow/${P}/shots`, {
        assetImages: true,
        prepare(f) {
            const channels = structuredClone(f.catalog.channels);
            const model = channels[0].models.find((item) => item.modelKey === "lec-vp-seedance-2-5-f");
            if (!model || !("capabilityConfig" in model) || !model.capabilityConfig?.video) throw new Error("缺少测试视频线路");
            model.capabilityConfig.video.references.maxAudios = 0;
            useConfigStore.getState().mergeSystemChannels(systemChannelModelChannels(channels as never));
            const handle = f.handle;
            f.handle = (method, url, input) => {
                const verb = method.toUpperCase();
                if (verb === "GET" && url === `/projects/${P}/assets`) return ok({ assets: library });
                if (verb === "POST" && url.endsWith(`/shots/${shot}/compile`)) {
                    compileModels.push((input as { channelModel: string }).channelModel);
                    const speaker = refs.find((item) => item.assetVersionId === "pinned");
                    const files = refs.map((item, i) => ({ file: item.assetVersionId, tag: "@图片" + (i + 1), role: "identity" }));
                    if (speaker) files.push({ file: "shared", tag: "@音频1", role: "voice" });
                    return ok({
                        compiled: {
                            voiceBindings: speaker ? [{ versionId: "pinned", sampleResourceId: "shared", tag: "@音频1" }] : [],
                            result: { prompt: "当前引用", files, issues: [] },
                        },
                    });
                }
                if (verb === "POST" && url === path) {
                    const body = input as { replaceReferenceId?: string; assetVersionId: string };
                    writes.push([verb, body]);
                    const target = body.replaceReferenceId ? refs.findIndex((item) => item.id === body.replaceReferenceId) : refs.length;
                    const chosen = [...library, variant].find((asset) => asset.primaryVersionId === body.assetVersionId)!;
                    const reference = row(chosen, body.assetVersionId, chosen.character.voice.profile.sampleResourceId);
                    refs.splice(target, body.replaceReferenceId ? 1 : 0, reference);
                    return ok({ reference });
                }
                if (verb === "DELETE" && url.startsWith(path + "/")) {
                    writes.push([verb, url]);
                    refs.splice(
                        refs.findIndex((item) => url.endsWith("/" + item.id)),
                        1,
                    );
                    return ok({});
                }
                const result = handle(method, url, input);
                if (url.endsWith("/workspace") && result?.body?.data) Object.assign(result.body.data, { assets: library, shotReferences: refs });
                return result;
            };
        },
    });
    const openEditor = () => page.click(document.querySelector('button[aria-label="编辑绑定 S01"]'));
    const openChoices = async () => {
        const field = document.querySelector<HTMLInputElement>("#flow-unit-binding-choice")!;
        await act(async () => field.dispatchEvent(new dom.KeyboardEvent("keydown", { bubbles: true, key: "ArrowDown", keyCode: 40 })));
        await flush();
    };
    const button = (text: string) => buttonByText(document.querySelector('[role="dialog"]')!, text);
    const compareWithOldWorkbench = async (images: string[], audios: string[]) => {
        await page.go(`/drama-flow/${P}/video?item=${shot}`);
        const workspace = page.client.getQueryData<ProjectUnitWorkspace>(["project", P, "unit-workspace", fx.FIXTURE_UNIT_ID])!;
        const compiled = page.client.getQueryData<DramaCompileResult>(["project", P, "drama", "compile", shot, "flow", fx.FIXTURE_VIDEO_MODEL])!;
        expect(workspace.unit.projectId).toBe(P);
        const files = compiled.result.files;
        const old = referenceMediaForCompiledFiles(workspace, shot, files);
        expect([old.missing, old.unsupported]).toEqual([[], []]);
        const oldImages = old.images.map((item) => resourceIdFromStorageKey(item.storageKey));
        const oldAudios = old.audios.map((item) => resourceIdFromStorageKey(item.storageKey));
        expect([oldImages, oldAudios]).toEqual([images, audios]);
        const pane = document.querySelector("aside.drama-flow-pane")!;
        const displayed = Array.from(pane.querySelectorAll(".drama-flow-seg-assets > span")).map((row) => {
            const src = row.querySelector("img,audio")?.getAttribute("src") || "";
            return { tag: row.textContent?.split(" · ")[0], id: src.match(/\/resources\/([^/]+)\/file$/)?.[1] };
        });
        expect(displayed.map((item) => item.tag)).toEqual(files.map((item) => item.tag));
        expect(displayed.map((item) => item.id)).toEqual([...oldImages, ...oldAudios]);
        const text = pane.textContent;
        await page.go(`/drama-flow/${P}/shots`);
        return text;
    };
    try {
        await openEditor();
        expect(document.querySelector('img[alt="顾音固定版本预览"]')?.getAttribute("src")).toContain(fx.FIXTURE_IMAGE_SQUARE);
        expect(document.querySelector('audio[aria-label="试听顾音的声音样本"]')?.getAttribute("src")).toContain("/shared/file");
        expect(byText("li", "顾音")?.textContent).toContain("@音频1");
        expect(byText("li", "顾宁")?.textContent).toContain("本段未引用");
        expect(document.querySelector('[role="dialog"] [role="alert"]')?.textContent).toContain("当前线路最多支持 0 个");
        await page.click(document.querySelector('[aria-label="更换顾音"]'));
        await openChoices();
        expect(byText(".ant-select-item-option", "口琴")).toBeUndefined();
        await page.click(byText(".ant-select-item-option", "顾音·夜班"));
        expect(page.text()).toContain("此版本没有声音样本");
        await page.click(button("取消编辑"));
        expect(writes).toEqual([]);
        await compareWithOldWorkbench([fx.FIXTURE_IMAGE_SQUARE, fx.FIXTURE_IMAGE_PORTRAIT], ["shared"]);
        await openEditor();
        await page.click(document.querySelector('[aria-label="更换顾音"]'));
        await openChoices();
        await page.click(byText(".ant-select-item-option", "顾音·夜班"));
        await page.click(button("保存绑定"));
        expect(writes).toEqual([["POST", { role: "reference", assetVersionId: "dress-current", replaceReferenceId: "r-pinned" }]]);
        const card = () => document.querySelector("article.drama-flow-seg .drama-flow-seg-assets")!;
        expect(card().textContent).toContain("顾音·夜班");
        expect(await compareWithOldWorkbench(["res-night-costume", fx.FIXTURE_IMAGE_PORTRAIT], [])).toContain("@图片1 · 顾音·夜班");
        await openEditor();
        await page.click(document.querySelector('[aria-label="解除顾音·夜班绑定"]'));
        await page.click(button("保存绑定"));
        expect(writes[1]).toEqual(["DELETE", path + "/r-dress-current"]);
        expect(card().textContent).not.toContain("顾音·夜班");
        expect(card().textContent).toContain("顾宁");
        await compareWithOldWorkbench([fx.FIXTURE_IMAGE_PORTRAIT], []);
        await openEditor();
        await page.click(button("绑定资产"));
        await openChoices();
        await page.click(byText(".ant-select-item-option", "口琴"));
        await page.click(button("保存绑定"));
        expect(writes[2]).toEqual(["POST", { role: "reference", assetVersionId: "p-current" }]);
        expect(card().textContent).toContain("口琴");
        await compareWithOldWorkbench([fx.FIXTURE_IMAGE_PORTRAIT, "res-harmonica"], []);
        expect(compileModels.length).toBeGreaterThan(1);
        expect(new Set(compileModels)).toEqual(new Set([fx.FIXTURE_VIDEO_MODEL]));
        expect(fixture.forbidden).toEqual([]);
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
        useConfigStore.getState().mergeSystemChannels(systemChannelModelChannels(fixture.catalog.channels as never));
    }
}, 30_000);

test("分镜单元删除：取消恢复焦点、冲突后重审、运行保护及选中段缓存", async () => {
    const target = fx.FIXTURE_SHOT_ID;
    const neighbor = fx.FIXTURE_SHOT_ID_2;
    const writes: Array<{ shotId: string; body: unknown }> = [];
    const artifacts = [
        { id: "board", type: "storyboard", shotId: target },
        { id: "film", type: "video", shotId: target },
    ];
    let taskRunning = false;
    const page = await mount(`/drama-flow/${P}/shots?item=${neighbor}`, {
        prepare(f) {
            const spec = f.state.segments[0].spec;
            spec.shots.push({ ...spec.shots[0], at: 5 }, { ...spec.shots[0], at: 10 });
            const handle = f.handle;
            f.handle = (method, url, body) => {
                if (method.toUpperCase() === "DELETE" && url.startsWith(`/projects/${P}/shots/`)) {
                    const shotId = url.split("/").at(-1)!;
                    writes.push({ shotId, body });
                    if (writes.length === 1) {
                        artifacts.push({ id: "frame", type: "end_frame", shotId });
                        taskRunning = true;
                        return { status: 409, body: { code: 409, msg: "关联产物已变化，请重新核对" } };
                    }
                    f.state.segments = f.state.segments.filter((row) => row.shotId !== shotId);
                    return { status: 200, body: { code: 0, msg: "", data: { deleted: true } } };
                }
                const result = handle(method, url, body);
                if (url.endsWith("/workspace") && result?.body?.data) {
                    Object.assign(result.body.data, {
                        shotArtifacts: artifacts,
                        tasks: taskRunning ? [{ id: "render-local", status: "running", clientContext: { shotId: target } }] : [],
                    });
                }
                return result;
            };
        },
    });
    const button = (id: string) => document.querySelector<HTMLButtonElement>(`button[aria-label="删除分镜单元 ${id}"]`);
    const dialog = () => document.querySelector('[role="dialog"]');
    const confirm = () => byText('[role="dialog"] button', "确认删除") as HTMLButtonElement;
    try {
        button("S01")!.focus();
        await page.click(button("S01"));
        expect(currentPath).toContain(`item=${neighbor}`);
        expect(dialog()?.textContent).toContain("第 1 集 · 场 E01S01 · S01 随行鲜，共 3 个子镜头");
        expect(dialog()?.textContent).toContain("分镜图 × 1、视频 × 1");
        await page.click(buttonByText(dialog()!, "取消"));
        expect(document.activeElement).toBe(button("S01")!);
        expect(writes).toHaveLength(0);
        expect(document.querySelectorAll("article.drama-flow-seg")).toHaveLength(2);

        await page.click(button("S01"));
        await act(async () => {
            confirm().click();
            confirm().click();
        });
        await flush();
        expect(writes).toHaveLength(1);
        expect(writes[0]).toEqual({ shotId: target, body: { expectedRevisionId: "rev-1", expectedArtifactIds: ["board", "film"] } });
        expect(dialog()?.textContent).toContain("关联产物已变化");
        expect(button("S01")).not.toBeNull();
        expect(confirm().disabled).toBe(true);
        await page.click(byText('[role="dialog"] button', "重新核对"));
        expect(dialog()?.textContent).toContain("尾帧 × 1");
        expect(dialog()?.textContent).toContain("生成任务 render-local");
        expect(confirm().disabled).toBe(true);
        taskRunning = false;
        await page.click(byText('[role="dialog"] button', "重新核对"));
        await page.click(confirm());
        expect(writes[1]).toEqual({ shotId: target, body: { expectedRevisionId: "rev-1", expectedArtifactIds: ["board", "film", "frame"] } });
        expect(button("S01")).toBeNull();
        expect(button("S02")).not.toBeNull();
        expect(currentPath).toContain(`item=${neighbor}`);
        expect(summaryValue("段")).toBe("1");
        expect(summaryValue("镜头")).toBe("10");
        expect(summaryValue("总时长")).toBe("15 秒");
        await page.go(`/drama-flow/${P}/video`);
        expect(page.host.querySelector(".drama-flow-work")?.textContent || page.host.textContent).not.toContain("S01 随行鲜");
        await page.go(`/drama-flow/${P}/shots?item=${neighbor}`);
        expect(button("S01")).toBeNull();
        await page.click(button("S02"));
        await page.click(confirm());
        expect(writes[2]).toEqual({ shotId: neighbor, body: { expectedRevisionId: "rev-2", expectedArtifactIds: [] } });
        expect(currentPath).not.toContain("item=");
        expect(page.host.querySelector("aside.drama-flow-pane")).toBeNull();
        expect(summaryValue("段")).toBe("0");
        expect(fixture.forbidden).toEqual([]);
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
    }
}, 20000);

test("另一位编辑先删除时，重新核对后关闭已删单元详情并更新进度", async () => {
    const target = fx.FIXTURE_SHOT_ID;
    let requests = 0;
    const page = await mount(`/drama-flow/${P}/shots?item=${target}`, {
        prepare(f) {
            const handle = f.handle;
            f.handle = (method, url, body) => {
                if (method.toUpperCase() !== "DELETE" || url !== `/projects/${P}/shots/${target}`) return handle(method, url, body);
                f.state.segments = f.state.segments.filter((row) => row.shotId !== target);
                requests++;
                return { status: 404, body: { code: 404, msg: "分镜单元已删除或不在本项目，请刷新列表" } };
            };
        },
    });
    try {
        await page.click(document.querySelector('button[aria-label="删除分镜单元 S01"]'));
        await page.click(byText('[role="dialog"] button', "确认删除"));
        expect(document.querySelector('[role="dialog"]')?.textContent).toContain("请刷新列表");
        expect(currentPath).toContain(`item=${target}`);
        await page.click(byText('[role="dialog"] button', "重新核对"));
        expect(currentPath).not.toContain("item=");
        expect(summaryValue("段")).toBe("1");
        expect(page.host.querySelector("aside.drama-flow-pane")).toBeNull();
        expect(document.querySelector('button[aria-label="删除分镜单元 S02"]')).not.toBeNull();
        expect(requests).toBe(1);
        expect(fixture.forbidden).toEqual([]);
    } finally {
        await page.dispose();
    }
});

test("MC-TEXT 文本卡的可用渠道贯通剧本优化、立项、画风、色卡和资产提取", async () => {
    const { useDramaModelPreferencesStore: prefs } = await import("@/stores/use-drama-model-preferences-store");
    const before = useConfigStore.getState().config;
    const previous = prefs.getState().models;
    const blocked = "CHANNEL_000001::text-paused";
    const effective = "CHANNEL_000001::text-ready";
    const requests: Array<{ url: string; model?: string; logicalModelId?: string; replace?: boolean }> = [];
    let requireReplaceConfirmation = false;
    const page = await mount(`/drama-flow/${P}/script`, {
        workflowId: "pipeline-2",
        style: "locked",
        prepare: (f) => {
            for (const key of ["text-paused", "text-ready", "text-chosen"]) {
                f.catalog.channels[0].models.push({ modelKey: key, displayName: key, available: true, capability: "text", priceTiers: [] });
            }
            const config = useConfigStore.getState();
            config.mergeSystemChannels(systemChannelModelChannels(f.catalog.channels as never));
            const current = useConfigStore.getState().config;
            useConfigStore.setState({
                config: {
                    ...current,
                    channels: current.channels.map((channel) => ({
                        ...channel,
                        modelCosts: channel.modelCosts?.map((item) => (item.model.startsWith("text-") ? { ...item, logicalModelId: `logical-${item.model}` } : item)),
                    })),
                },
            });
            prefs.setState({ models: { text: blocked } });
            const handle = f.handle;
            f.handle = (method, url, body) => {
                if (/\/drama\/steps\/(script\.adapt|style\.lock|scene\.palette|assets\.extract)$/.test(url) && method.toUpperCase() === "GET") {
                    return { status: 200, body: { code: 0, msg: "", data: { usage: { skill: { name: "测试技能", version: "1" }, profile: { label: "测试风格" } } } } };
                }
                if (url.endsWith("/run") && method.toUpperCase() === "POST") {
                    requests.push({ url, ...(body as { model?: string; logicalModelId?: string }) });
                    if (requireReplaceConfirmation && url.endsWith("/script.adapt/run") && !requests.at(-1)?.replace) {
                        return { status: 409, body: { code: 409, reason: "drama_episodes_replace_required", msg: "请确认补写", details: { mode: "design", episodes: [{ unitId: fx.FIXTURE_UNIT_ID, title: "第 1 集", shots: 1, hasShotlist: true }] } } };
                    }
                    return { status: 400, body: { code: 400, msg: "模拟请求已收集" } };
                }
                const result = handle(method, url, body);
                if (url.split("?")[0].endsWith("/drama/scenes") && result?.body?.data) {
                    Object.assign(result.body.data, { paletteReady: true });
                }
                if (url === "/dramas/models" && result?.body?.data) {
                    const catalog = result.body.data as DramaModelCatalog;
                    catalog.options.text = catalog.options.text.filter((option) => option.choice.modelKey !== "text-paused");
                    catalog.unavailable = { ...catalog.unavailable, text: [{ choice: { channelId: "CHANNEL_000001", modelKey: "text-paused" }, reason: "测试渠道已停用" }] };
                }
                return result;
            };
        },
    });
    try {
        expect(summaryValue("文本模型")).toContain("现在改用 text-ready");
        await page.click(byText("button", "补写本集设计"));
        expect(document.querySelector('[aria-label="选择用于剧本优化的文本模型"]')?.textContent).toContain("text-ready");
        await page.click(byText('[role="dialog"] button', "补写所选集设计"));
        expect(requests[0]).toMatchObject({ url: `/projects/${P}/drama/steps/script.adapt/run`, model: effective, logicalModelId: "logical-text-ready" });
        await page.go(`/drama-flow/${P}/shots`);
        expect(summaryValue("文本模型")).toContain("text-ready");
        await page.click(document.querySelector('section[aria-label="全剧立项"] button'));
        expect(requests[1]).toMatchObject({ url: `/projects/${P}/drama/steps/director.setup/run`, model: effective, logicalModelId: "logical-text-ready" });
        for (const [openLabel, pickerLabel, imageSwitch, target] of [
            ["分析项目风格", "选择用于风格分析的文本模型", "", `/projects/${P}/drama/steps/style.lock/run`],
            ["生成场次色卡", "选择用于场次色卡的文本模型", "提取后给新资产出图", `/projects/${P}/drama/steps/scene.palette/run`],
            ["提取角色、场景与道具", "选择用于提取资产的文本模型", "给新资产出图", `/projects/${P}/units/${fx.FIXTURE_UNIT_ID}/drama/steps/assets.extract/run`],
        ]) {
            await page.go(`/drama-flow/${P}/look`);
            await page.click(document.querySelector(`button[aria-label="${openLabel}"]`));
            const picker = document.querySelector(`[aria-label="${pickerLabel}"]`);
            if (openLabel === "分析项目风格") {
                expect(picker?.textContent).toContain("text-ready");
                await page.click(picker);
                expect((byText('[role="option"]', "text-paused") as HTMLButtonElement).disabled).toBe(true);
                await page.click(byText('[role="option"]', "text-chosen"));
            }
            expect(picker?.textContent).toContain("text-chosen");
            expect(summaryValue("文本模型")).toContain("text-chosen");
            if (imageSwitch) await page.click(document.querySelector(`[role="switch"][aria-label="${imageSwitch}"]`));
            await page.click(document.querySelector('[role="dialog"] .ant-modal-footer button.ant-btn-primary'));
            expect(requests.at(-1)).toMatchObject({ url: target, model: "CHANNEL_000001::text-chosen", logicalModelId: "logical-text-chosen" });
            await page.go(`/drama-flow/${P}/script`);
        }
        requireReplaceConfirmation = true;
        for (const disabledKey of ["text-chosen", "text-ready"]) {
            await page.click(byText("button", "补写本集设计"));
            await page.click(byText('[role="dialog"] button', "补写所选集设计"));
            const beforeConfirmation = requests.length;
            expect(requests.at(-1)).toMatchObject({ model: `CHANNEL_000001::${disabledKey}`, logicalModelId: `logical-${disabledKey}` });
            const catalog = page.client.getQueryData<DramaModelCatalog>(["drama-models"])!;
            const removed = catalog.options.text.find((option) => option.choice.modelKey === disabledKey)!;
            await act(async () => {
                page.client.setQueryData(["drama-models"], {
                    ...catalog,
                    options: { ...catalog.options, text: catalog.options.text.filter((option) => option !== removed) },
                    unavailable: { ...catalog.unavailable, text: [...(catalog.unavailable?.text ?? []), { choice: removed.choice, reason: "文本渠道已暂停" }] },
                });
            });
            await flush();
            const picker = document.querySelector('[aria-label="选择补写或替换使用的文本模型"]');
            const confirm = buttonByText(picker!.closest('[role="dialog"]')!, "补写设计")!;
            if (disabledKey === "text-chosen") {
                expect(summaryValue("文本模型")).toContain("text-ready");
                expect(picker?.textContent).toContain("text-ready");
                await page.click(confirm);
                expect(requests).toHaveLength(beforeConfirmation + 1);
                expect(requests.at(-1)).toMatchObject({ replace: true, model: effective, logicalModelId: "logical-text-ready" });
                await page.go(`/drama-flow/${P}/shots`);
                await page.go(`/drama-flow/${P}/script`);
            } else {
                expect(confirm.disabled).toBe(true);
                await page.click(confirm);
                expect(requests).toHaveLength(beforeConfirmation);
            }
        }
    } finally {
        await page.dispose();
        useConfigStore.setState({ config: before });
        prefs.setState({ models: previous });
    }
}, 20000);

for (const mode of ["promise", "undefined"] as const) {
    test(`D-00 分镜：选中 → 保存后段数据更新 → 换选 → 关详情 → 离开（scrollIntoView 返回 ${mode}）`, async () => {
        scrollReturns = mode;
        scrolled.length = 0;
        const page = await mount(`/drama-flow/${P}/shots`);
        try {
            await page.click(byText("button.drama-flow-seg-head", "S01"));
            expect(currentPath).toContain(`item=${fx.FIXTURE_SHOT_ID}`);
            expect(document.querySelector("aside.drama-flow-pane")).not.toBeNull();
            expect(scrolled.length).toBeGreaterThan(0);

            // 保存走真实的分镜保存接口，再按分镜卡保存成功后的做法写回段缓存——线上崩溃就发生在这一步之后的 effect 清理。
            const { writeDramaSegments } = await import("@/services/api/project-drama");
            const spec = { ...fixture.state.segments[0].spec, shots: [{ ...fixture.state.segments[0].spec.shots[0], text: "阿婆把热橙汁推到林深手边，雨声更大。" }] };
            const saved = await writeDramaSegments(P, { unitId: fx.FIXTURE_UNIT_ID, segments: [spec] });
            await act(async () => void page.client.setQueryData(dramaSegmentsKey(P, fx.FIXTURE_UNIT_ID), saved.segments));
            await flush();
            expect(fixture.state.saves).toBe(1);
            expect(page.text()).toContain("雨声更大");

            // 「刷新」：重新挂一次页面，从接口读回来仍是改后的内容。
            const before = scrolled.length;
            await page.click(byText("button.drama-flow-seg-head", "S02"));
            expect(currentPath).toContain(`item=${fx.FIXTURE_SHOT_ID_2}`);
            expect(scrolled.length).toBeGreaterThan(before);
            await page.click(document.querySelector('button[aria-label="收起详情"]'));
            expect(document.querySelector("aside.drama-flow-pane")).toBeNull();
            await page.go(`/drama-flow/${P}/look`);
            await page.go("/projects");
            expect(page.text()).toContain("离开了新工作流");
            expect(page.errors).toEqual([]);
            expect(fixture.forbidden).toEqual([]);
        } finally {
            await page.dispose();
        }
        const refreshed = await mount(`/drama-flow/${P}/shots?item=${fx.FIXTURE_SHOT_ID}`);
        try {
            // 新夹具重新开始，所以这里只验证「带 item 直达」的挂载和卸载同样不崩。
            expect(refreshed.errors).toEqual([]);
            expect(document.querySelector("aside.drama-flow-pane")).not.toBeNull();
        } finally {
            await refreshed.dispose();
        }
    });

    test(`D-00 定妆资产：选中 → 换选 → 关详情 → 卸载（scrollIntoView 返回 ${mode}）`, async () => {
        scrollReturns = mode;
        scrolled.length = 0;
        const page = await mount(`/drama-flow/${P}/look?tab=character`);
        try {
            await page.click(byText("button.drama-flow-asset", "林深"));
            expect(currentPath).toContain(`item=${fx.FIXTURE_ASSET_ID}`);
            expect(document.querySelector('button.drama-flow-asset[aria-pressed="true"]')?.textContent).toContain("林深");
            await page.click(byText("button.drama-flow-asset", "阿婆"));
            expect(currentPath).toContain(`item=${fx.FIXTURE_ASSET_ID_2}`);
            expect(scrolled.length).toBeGreaterThan(0);
            await page.click(document.querySelector('button[aria-label="收起详情"]'));
            expect(currentPath).not.toContain("item=");
            expect(page.errors).toEqual([]);
            expect(fixture.forbidden).toEqual([]);
        } finally {
            await page.dispose();
        }
    });

    test(`D-00 出片详情：选中 → 换选 → 关详情 → 卸载，不提交视频（scrollIntoView 返回 ${mode}）`, async () => {
        scrollReturns = mode;
        scrolled.length = 0;
        const page = await mount(`/drama-flow/${P}/video`);
        try {
            await page.click(byText("button.drama-flow-vcard-title", "S01"));
            expect(currentPath).toContain(`item=${fx.FIXTURE_SHOT_ID}`);
            expect(page.text()).toContain("当前编译的提示词");
            await page.click(byText("button.drama-flow-vcard-title", "S02"));
            expect(currentPath).toContain(`item=${fx.FIXTURE_SHOT_ID_2}`);
            expect(scrolled.length).toBeGreaterThan(0);
            await page.click(document.querySelector('button[aria-label="收起详情"]'));
            expect(document.querySelector("aside.drama-flow-pane")).toBeNull();
            expect(page.errors).toEqual([]);
            expect(fixture.forbidden).toEqual([]);
            expect(fixture.calls.some((call) => call.method !== "GET" && call.path.includes("video-chains"))).toBe(false);
        } finally {
            await page.dispose();
        }
    });
}

for (const style of ["none", "anchor_only", "locked"] as const) {
    test(`D-01 画风「${style}」：阶段条、定妆页和旧工作台口径一致`, async () => {
        const page = await mount(`/drama-flow/${P}/look?tab=style`, { style });
        try {
            const oldLocked = projectLockedStyle(fixture.project.bibleJson).locked;
            const expected = { none: ["画风未定", "还没锁定"], anchor_only: ["画风待锁定", "有风格锚，还没锁定"], locked: ["画风已定", "已锁定"] }[style];
            expect(oldLocked).toBe(style === "locked");
            expect(page.text()).toContain(expected[0]);
            expect(summaryValue("状态")).toBe(expected[1]);
            if (style !== "locked") expect(page.text()).not.toContain("画风已定");
            if (style === "anchor_only") expect(page.text()).toContain("当前风格锚（还没锁定）");
            expect(page.errors).toEqual([]);
            expect(fixture.forbidden).toEqual([]);
        } finally {
            await page.dispose();
        }
    });
}

test("D-01 画风档案在读、首次没读到、带缓存刷新失败：不写已锁定也不写未锁定", async () => {
    const bible = `/projects/${P}/drama/bible`;
    const unsure = (label: string) => {
        expect(page.text(), label).not.toContain("画风已定");
        expect(page.text(), label).not.toContain("画风未定");
        expect(page.text(), label).not.toContain("画风待锁定");
    };
    let page = await mount(`/drama-flow/${P}/look?tab=style`, { style: "locked", prepare: (next) => next.overrides.set(bible, "pending") });
    try {
        expect(summaryValue("状态")).toBe("…");
        unsure("在读");
    } finally {
        await page.dispose();
    }

    page = await mount(`/drama-flow/${P}/look?tab=style`, { style: "locked", prepare: (next) => next.overrides.set(bible, "error") });
    try {
        expect(page.text()).toContain("风格档案没读到");
        unsure("首次没读到");
    } finally {
        await page.dispose();
    }

    page = await mount(`/drama-flow/${P}/look?tab=style`, { style: "locked" });
    try {
        expect(summaryValue("状态")).toBe("已锁定");
        expect(page.text()).toContain("画风已定");
        // 有缓存，这次刷新失败：不能继续写「已锁定」，也不能改写成「还没锁定」。
        fixture.overrides.set(bible, "error");
        await act(async () => void page.client.invalidateQueries({ queryKey: dramaBibleKey(P) }));
        await flush();
        expect(page.text()).toContain("风格档案没读到");
        unsure("带缓存刷新失败");
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
    }
});

const tasksPath = "/tasks";
const settled = fx.fixtureTask("t1", { amountMicrocredits: 5_000_000, actualAmountMicrocredits: 4_000_000, status: "settled" });
const refunded = fx.fixtureTask("t2", { amountMicrocredits: 3_000_000, status: "refunded" });
const reserved = fx.fixtureTask("t3", { amountMicrocredits: 2_000_000, status: "reserved" });
const unbilled = fx.fixtureTask("t4");

const runsHead = () => document.querySelector(".drama-flow-runs-head > span")?.textContent || "";

for (const [label, tasks, expected] of [
    ["读到空列表", [], "0 积分 · 暂无消费"],
    ["全部有账单（含退款、预授权）", [settled, refunded, reserved], "6 积分"],
    ["全部没有账单", [unbilled, fx.fixtureTask("t5")], "2 次运行都没有计费记录"],
    ["混合缺账单和退款", [settled, refunded, unbilled], "已知 4 积分，另有 1 次没有计费记录（合计不完整）"],
] as const) {
    test(`D-02 花费：${label}`, async () => {
        const page = await mount(`/drama-flow/${P}?panel=info`, { tasks: [...tasks] });
        try {
            expect(infoValue("最近 100 次运行")).toBe(expected);
            await page.go(`/drama-flow/${P}?panel=runs`);
            expect(runsHead()).toBe(`最近 100 次 · 共花 ${expected}`);
            if (!tasks.length) expect(page.text()).toContain("还没有运行记录");
            else {
                const runs = document.querySelector(".drama-flow-runs")?.textContent || "";
                expect(runs).toContain(fx.FIXTURE_VIDEO_MODEL_NAME);
                expect(runs).not.toContain("CHANNEL_000001");
            }
            expect(page.errors).toEqual([]);
        } finally {
            await page.dispose();
        }
    });
}

test("D-02 花费：在读、首次没读到、带缓存刷新失败都不算 0", async () => {
    let page = await mount(`/drama-flow/${P}?panel=info`, { prepare: (next) => next.overrides.set(tasksPath, "pending") });
    try {
        expect(infoValue("最近 100 次运行")).toBe("…");
        await page.go(`/drama-flow/${P}?panel=runs`);
        expect(runsHead()).toBe("最近 100 次 · 共花 …");
        expect(page.text()).toContain("正在读运行记录");
    } finally {
        await page.dispose();
    }

    page = await mount(`/drama-flow/${P}?panel=runs`, { prepare: (next) => next.overrides.set(tasksPath, "error") });
    try {
        expect(runsHead()).toBe("最近 100 次 · 共花 没读到");
        expect(page.text()).toContain("运行记录没读到");
        expect(page.text()).not.toContain("暂无消费");
    } finally {
        await page.dispose();
    }

    page = await mount(`/drama-flow/${P}?panel=info`, { tasks: [] });
    try {
        expect(infoValue("最近 100 次运行")).toBe("0 积分 · 暂无消费");
        fixture.overrides.set(tasksPath, "error");
        await act(async () => void page.client.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", P] }));
        await flush();
        expect(infoValue("最近 100 次运行")).toBe("没读到");
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
    }
});

test("D-03 模型名：目录读到时显示名字，不出现内部渠道 ID；不可用、没配分开写", async () => {
    const page = await mount(`/drama-flow/${P}?panel=info`, { imageModel: "CHANNEL_000001::gone-image-model" });
    try {
        expect(infoValue("视频模型")).toBe(fx.FIXTURE_VIDEO_MODEL_NAME);
        expect(infoValue("图片模型")).toBe("原来选的模型已不可用，到设置里重选");
        const drawer = document.querySelector(".drama-flow-info")?.textContent || "";
        expect(drawer).not.toContain("CHANNEL_000001");
        expect(drawer).not.toContain("lec-vp-seedance");
        // 只改显示：项目里存的模型 ID 原样不动，也没有写项目、提交生成。
        expect(fixture.project.defaultVideoModel).toBe(fx.FIXTURE_VIDEO_MODEL);
        expect(fixture.forbidden).toEqual([]);
        await page.go(`/drama-flow/${P}/video`);
        expect(summaryValue("视频模型")).toBe(fx.FIXTURE_VIDEO_MODEL_NAME);
    } finally {
        await page.dispose();
    }

    const unset = await mount(`/drama-flow/${P}?panel=info`, { prepare: (next) => next.overrides.set("/drama/models", "error") });
    try {
        expect(infoValue("视频模型")).toBe("还没配好");
        expect(infoValue("图片模型")).toBe("用全局默认");
    } finally {
        await unset.dispose();
    }
});

test("短剧能力目录刷新失败时，缓存不能放行报价；恢复后重新校验模型", async () => {
    const page = await mount(`/drama-flow/${P}/video`);
    try {
        expect(summaryValue("视频模型")).toBe(fx.FIXTURE_VIDEO_MODEL_NAME);
        fixture.overrides.set("/dramas/models", "error");
        await act(async () => {
            await page.client.invalidateQueries({ queryKey: ["drama-models"] });
        });
        await flush();
        expect(summaryValue("视频模型")).toContain("没法确认");
        const generate = byText("button", "出这一段") as HTMLButtonElement | undefined;
        if (generate && !generate.disabled) await page.click(generate);
        expect(fixture.state.previews).toBe(0);
        expect(document.querySelector('[role="dialog"]')).toBeNull();
        fixture.overrides.delete("/dramas/models");
        await act(async () => {
            await page.client.invalidateQueries({ queryKey: ["drama-models"] });
        });
        await flush();
        expect(summaryValue("视频模型")).toBe(fx.FIXTURE_VIDEO_MODEL_NAME);
        type QuoteRequest = { model: { model: string; logicalModelId?: string }; requestId: string; previewHash?: string };
        const quotes: QuoteRequest[] = [];
        const submissions: QuoteRequest[] = [];
        const handle = fixture.handle;
        fixture.handle = (method, url, body) => {
            if (method.toUpperCase() === "POST") {
                if (url === `/projects/${P}/video-chains/preview`) quotes.push(body as QuoteRequest);
                if (url === `/projects/${P}/video-chains`) {
                    submissions.push(body as QuoteRequest);
                    return { status: 400, body: { code: 400, msg: "验收夹具已记录请求" } };
                }
            }
            return handle(method, url, body);
        };
        await page.click(buttonByText(cardOf("S02")!, "出这一段"));
        await page.click(buttonByText(modal()!, "预览当前场景与费用"));
        expect(quotes[0]?.model.model).toBe(fx.FIXTURE_VIDEO_MODEL);
        expect(buttonByText(modal()!, "开始连续生成")).toBeTruthy();
        fixture.overrides.set("/dramas/models", "error");
        await act(async () => {
            await page.client.invalidateQueries({ queryKey: ["drama-models"] });
        });
        await flush();
        expect(buttonByText(modal()!, "开始连续生成")).toBeFalsy();
        expect(submissions).toHaveLength(0);
        fixture.overrides.delete("/dramas/models");
        await act(async () => {
            await page.client.invalidateQueries({ queryKey: ["drama-models"] });
        });
        await flush();
        await page.click(buttonByText(modal()!, "预览当前场景与费用"));
        await page.click(buttonByText(modal()!, "开始连续生成"));
        expect(quotes).toHaveLength(2);
        expect(submissions).toEqual([{ ...quotes[1], previewHash: "preview-2" }]);
        expect(fixture.forbidden).toEqual([]);
    } finally {
        await page.dispose();
    }
});

test("D-03 模型名：目录在读、没读到时不报「已不可用」", async () => {
    let page = await mount(`/drama-flow/${P}?panel=info`, { imageModel: "CHANNEL_000001::gone-image-model", prepare: (next) => next.overrides.set("/model-catalog", "pending") });
    try {
        expect(infoValue("视频模型")).toBe(`${fx.FIXTURE_VIDEO_MODEL_NAME}（正在核对是否可用…）`);
        expect(infoValue("图片模型")).toBe("正在读模型目录…");
    } finally {
        await page.dispose();
    }
    page = await mount(`/drama-flow/${P}?panel=info`, { imageModel: "CHANNEL_000001::gone-image-model", prepare: (next) => next.overrides.set("/model-catalog", "error") });
    try {
        expect(infoValue("视频模型")).toBe(`${fx.FIXTURE_VIDEO_MODEL_NAME}（模型目录没读到，没法确认现在是否可用）再读一次`);
        expect(infoValue("图片模型")).toBe("模型目录没读到再读一次");
        expect(page.text()).not.toContain("已不可用");
        // 目录恢复后点「再读一次」，按目录给出结论。
        fixture.overrides.delete("/model-catalog");
        await page.click(byText(".drama-flow-info button", "再读一次"));
        expect(infoValue("视频模型")).toBe(fx.FIXTURE_VIDEO_MODEL_NAME);
        expect(infoValue("图片模型")).toBe("原来选的模型已不可用，到设置里重选");
    } finally {
        await page.dispose();
    }
});

// 第七轮 R6-D04：新工作流和连续生成面板共用出片链缓存（同一个 key、同一种形状 { chains }）。
const { QueryObserver } = await import("@tanstack/react-query");
const { videoChainsKey, videoChainsQuery } = await import("@/services/api/project-video-chains");
const { VideoChainPanel } = await import("@/pages/projects/workbench/detail/video-chain-panel");
const U = fx.FIXTURE_UNIT_ID;
/** antd 会在两个汉字的按钮文字中间加空格，按去掉空白后的文字找按钮。 */
const buttonByText = (scope: ParentNode, text: string) => [...scope.querySelectorAll("button")].find((node) => (node.textContent || "").replace(/\s/g, "") === text);
const cardOf = (segmentId: string) => [...document.querySelectorAll("article.drama-flow-vcard")].find((node) => node.querySelector(".drama-flow-seg-no")?.textContent === segmentId);
const chainCache = (client: InstanceType<typeof QueryClient>) => client.getQueryData(videoChainsKey(P, U)) as { chains?: unknown[] } | undefined;
const modal = () => document.querySelector(".ant-modal");

test("项目自动切线清除旧报价，手选覆盖只在本项目生效", async () => {
    const { useDramaModelPreferencesStore: prefs } = await import("@/stores/use-drama-model-preferences-store");
    const previous = prefs.getState().models;
    const config = useConfigStore.getState().config;
    const otherId = "project-other-route";
    const other = fx.createDramaFlowFixture();
    const automatic = "lec-seedance-2-5-auto";
    const manual = "lec-seedance-2-5-manual";
    const qualified = (key: string) => `CHANNEL_000001::${key}`;
    const routeKey = projectVideoRoutingKey(P);
    type Quote = { model: { model: string }; requestId: string; previewHash?: string };
    const quotes: Quote[] = [];
    const submissions: Quote[] = [];
    const page = await mount(`/drama-flow/${P}/video`, {
        prepare(f) {
            for (const [key, name] of [
                [automatic, "Seedance 自动线路"],
                [manual, "Seedance 手选线路"],
            ]) {
                f.catalog.channels[0].models.push({ ...f.catalog.channels[0].models[0], modelKey: key, displayName: name });
            }
            prefs.setState({ models: { video: fx.FIXTURE_VIDEO_MODEL } });
            useConfigStore.getState().mergeSystemChannels(systemChannelModelChannels(f.catalog.channels as never));
            const handle = f.handle;
            f.handle = (method, path, body) => {
                if (path.startsWith(`/projects/${otherId}/`)) {
                    if (path.endsWith("/core")) return { status: 200, body: { code: 0, msg: "ok", data: { project: { ...other.project, id: otherId } } } };
                    return other.handle(method, path.replace(`/projects/${otherId}/`, `/projects/${P}/`), body);
                }
                if (method.toUpperCase() === "POST") {
                    if (path === `/projects/${P}/video-chains/preview`) quotes.push(body as Quote);
                    if (path === `/projects/${P}/video-chains`) {
                        submissions.push(body as Quote);
                        return { status: 200, body: { code: 0, msg: "ok", data: { chain: { id: "mock-confirmed" } } } };
                    }
                }
                return handle(method, path, body);
            };
        },
    });
    const reloadRouting = async () => {
        await act(async () => {
            await page.client.invalidateQueries({ queryKey: routeKey });
        });
        await flush();
    };
    try {
        await page.click(buttonByText(cardOf("S02")!, "出这一段"));
        await page.click(buttonByText(modal()!, "预览当前场景与费用"));
        expect(quotes[0].model.model).toBe(fx.FIXTURE_VIDEO_MODEL);
        fixture.state.videoRoute = { status: "auto", fromModel: fx.FIXTURE_VIDEO_MODEL, model: qualified(automatic), reason: "同线路连续三次渠道故障", successRate: 91.5, sampledAt: "2026-11-12T08:00:00Z", changedAt: "2026-11-12T08:20:00Z", revision: 1 };
        await reloadRouting();
        expect(summaryValue("视频模型")).toBe("Seedance 自动线路");
        for (const scope of [page.host, modal()!]) {
            expect(scope.textContent).toContain("原线路 Seedance 2.5 Fast");
            expect(scope.textContent).toContain("同线路连续三次渠道故障");
            expect(scope.textContent).toContain("91.5%");
            expect(scope.textContent).toContain(new Date(fixture.state.videoRoute.sampledAt!).toLocaleString());
        }
        expect(buttonByText(modal()!, "开始连续生成")).toBeUndefined();
        expect(submissions).toEqual([]);
        await page.click(buttonByText(modal()!, "预览当前场景与费用"));
        expect(quotes[1].model.model).toBe(qualified(automatic));
        await page.click(modal()!.querySelector('[aria-label="更换本次报价的视频模型"]'));
        let option = byText('[role="option"]', manual);
        if (!option) {
            await page.click(byText("button.canvas-model-picker-brand", "手选线路"));
            option = byText('[role="option"]', manual);
        }
        await page.click(option);
        expect(summaryValue("视频模型")).toBe("Seedance 手选线路");
        expect(fixture.state.videoRoute).toMatchObject({ revision: 2, status: "manual", fromModel: qualified(automatic), model: qualified(manual) });
        expect(buttonByText(modal()!, "开始连续生成")).toBeUndefined();
        await page.click(buttonByText(modal()!, "预览当前场景与费用"));
        expect(quotes[2].model.model).toBe(qualified(manual));
        expect(quotes[2].requestId).not.toBe(quotes[1].requestId);
        await page.click(buttonByText(modal()!, "开始连续生成"));
        expect(submissions).toEqual([{ ...quotes[2], previewHash: "preview-3" }]);
        expect(prefs.getState().models.video).toBe(fx.FIXTURE_VIDEO_MODEL);
        await page.go(`/drama-flow/${otherId}/video`);
        expect(summaryValue("视频模型")).toBe(fx.FIXTURE_VIDEO_MODEL_NAME);
        expect(modal()).toBeNull();
        await page.go(`/drama-flow/${P}/video`);
        expect(summaryValue("视频模型")).toBe("Seedance 手选线路");
        fixture.state.videoRoute = { ...fixture.state.videoRoute, status: "unavailable", reason: "合格线路缺少音频参考能力", successRate: null, sampledAt: null, revision: 3 };
        await reloadRouting();
        expect(page.text()).toContain("合格线路缺少音频参考能力");
        expect(page.text()).toContain("成功率 未知");
        expect(submissions).toHaveLength(1);
    } finally {
        await page.dispose();
        useConfigStore.setState({ config });
        prefs.setState({ models: previous });
    }
});
const submitted = () => fixture.calls.filter((call) => call.method !== "GET" && /\/video-chains(\/[^/]+\/(retry|cancel))?$/.test(call.path));

test("出片恢复：503 受理未知时换线只读估价，核实后重新报价只确认一次", async () => {
    const before = useConfigStore.getState().config;
    const { useDramaModelPreferencesStore: prefs } = await import("@/stores/use-drama-model-preferences-store");
    const oldPreferences = prefs.getState().models;
    const chainPath = `/projects/${P}/video-chains`;
    const backup = "lec-vp-seedance-2-5-backup";
    const first = { shotId: fx.FIXTURE_SHOT_ID, title: "S01", status: "succeeded", phase: "completed", contentSeconds: 8, generationSeconds: 15, model: fx.FIXTURE_VIDEO_MODEL, referenceCount: 2, videoResourceId: "completed-video" };
    const failed = {
        ...first,
        shotId: fx.FIXTURE_SHOT_ID_2,
        title: "S02",
        status: "failed",
        phase: "generating",
        contentSeconds: 21,
        generationSeconds: 30,
        referenceCount: 7,
        videoResourceId: "",
        taskId: "original-child",
        error: "HTTP 503",
        diagnostic: { category: "route_unavailable", submissionOutcome: "unknown", billingStatus: "uncertain", terminalStatus: "failed", safeToResubmit: false, canQueryProvider: false, upstreamCode: "HTTP 503" },
    };
    const chain = { id: "chain-failed", scope: "scene", anchorShotId: first.shotId, projectId: P, unitId: U, status: "failed", previewHash: "old", totalMicrocredits: 0, steps: [first, failed] };
    type Quote = { shotId: string; model: { model: string; input: { config: { videoSeconds: string } } }; resumeChainId: string; requestId: string; previewHash?: string };
    const quotes: Quote[] = [];
    const confirmations: Quote[] = [];
    let lookups = 0;
    let releaseOldQuote: (() => void) | undefined;
    let delayOldQuote = false;
    const interceptor = apiClient.interceptors.response.use(async (reply) => {
        if (delayOldQuote && reply.config.url === chainPath + "/preview") {
            delayOldQuote = false;
            await new Promise<void>((resolve) => {
                releaseOldQuote = resolve;
            });
        }
        return reply;
    });
    let route: { status: string; model: string; fromModel: string; revision: number } | null = null;
    const page = await mount(`/drama-flow/${P}/video`, {
        prepare(f) {
            f.state.segments[1].durationMs = 21000;
            f.catalog.channels[0].models.push({ ...f.catalog.channels[0].models[0], modelKey: backup, displayName: "Seedance 2.5 备用" });
            useConfigStore.getState().mergeSystemChannels(systemChannelModelChannels(f.catalog.channels as never));
            const fallback = f.handle;
            const ok = (data: unknown) => ({ status: 200, body: { code: 0, msg: "ok", data } });
            f.handle = (method, path, body) => {
                if (path === `/projects/${P}/drama/video-routing`) return ok({ route });
                if (path === `/projects/${P}/drama/video-routing/override`) {
                    const input = body as { model: string; fromModel: string; revision: number };
                    route = { status: "manual", model: input.model, fromModel: input.fromModel, revision: input.revision + 1 };
                    return ok({ route });
                }
                if (method.toLowerCase() === "get" && path.split("?")[0] === chainPath) return ok({ chains: [chain] });
                if (path === "/tasks/original-child" && method.toLowerCase() === "get") {
                    lookups++;
                    // A later reconciliation records a protocol rejection for this attempt.
                    if (lookups === 2) Object.assign(failed.diagnostic, { submissionOutcome: "not_submitted", billingStatus: "refunded", safeToResubmit: true });
                    return ok({ id: "original-child", status: "failed", billing: { status: failed.diagnostic.billingStatus } });
                }
                if (path === chainPath + "/preview") {
                    const input = body as Quote;
                    quotes.push(input);
                    return ok({
                        chain: {
                            ...chain,
                            id: "",
                            status: "preview",
                            previewHash: `quote-${quotes.length}`,
                            confirmationBlockedReason: failed.diagnostic.safeToResubmit ? "" : "请核对原任务是否已产生费用。",
                            totalMicrocredits: 2000000,
                            steps: [first, { ...failed, model: input.model.model, status: "waiting", error: "", quote: { billingMode: "fixed_request", amountMicrocredits: 2000000 } }],
                        },
                    });
                }
                if (path === chainPath + "/chain-failed/retry") {
                    confirmations.push(body as Quote);
                    chain.status = "running";
                    return ok({ chain });
                }
                return fallback(method, path, body);
            };
        },
    });
    try {
        await page.click(buttonByText(cardOf("S01")!, "出这一段"));
        expect(buttonByText(modal()!, "预览当前场景与费用")).toBeUndefined();
        expect(buttonByText(modal()!, "从失败步骤重新报价")).toBeTruthy();
        await page.click(buttonByText(modal()!, "关闭"));
        await page.click(buttonByText(cardOf("S02")!, "重新出"));
        expect(modal()?.textContent).toContain("线路不可用");
        await page.click(buttonByText(modal()!, "查询原请求与结算"));
        expect(lookups).toBe(1);
        expect(modal()?.textContent).toContain("待核对");
        expect((buttonByText(modal()!, "从失败步骤重新报价") as HTMLButtonElement).disabled).toBe(false);
        expect(quotes).toHaveLength(0);
        expect(confirmations).toHaveLength(0);
        await page.click(buttonByText(modal()!, "从失败步骤重新报价"));
        expect(modal()?.textContent).toContain("复用已完成视频");
        expect(modal()?.textContent).toContain("内容 21 秒 → 生成 30 秒");
        expect(modal()?.textContent).toContain("7 张参考图");
        expect((buttonByText(modal()!, "确认报价并从失败段继续") as HTMLButtonElement).disabled).toBe(true);
        delayOldQuote = true;
        await page.click(buttonByText(modal()!, "从失败步骤重新报价"));
        expect(releaseOldQuote).toBeDefined();
        await page.click(modal()!.querySelector('[aria-label="更换本次报价的视频模型"]'));
        let option = byText('[role="option"]', backup);
        if (!option) {
            for (const group of document.querySelectorAll(".canvas-model-picker-brand")) {
                await page.click(group);
                option = byText('[role="option"]', backup);
                if (option) break;
            }
        }
        await page.click(option);
        expect(buttonByText(modal()!, "确认报价并从失败段继续")).toBeUndefined();
        await page.click(buttonByText(modal()!, "从失败步骤重新报价"));
        expect(quotes[2]).toMatchObject({ resumeChainId: chain.id, shotId: first.shotId, model: { model: `CHANNEL_000001::${backup}`, input: { config: { videoSeconds: "30" } } } });
        expect(quotes[2].requestId).not.toBe(quotes[1].requestId);
        await act(async () => releaseOldQuote?.());
        await flush();
        expect(modal()?.textContent).toContain("报价标识 quote-3");
        expect(modal()?.textContent).not.toContain("报价标识 quote-2");
        expect(modal()?.textContent).toContain("本次仅供估价。请核对原任务是否已产生费用。");
        expect(modal()?.textContent).toContain("原任务 original-child");
        expect(modal()?.textContent).toContain("受理状态 未知");
        expect(modal()?.textContent).toContain("结算 待核对");
        expect(modal()?.textContent).toContain("剩余步骤预估 2 平台积分");
        expect(modal()?.querySelector('a[href*="completed-video"]')).toBeTruthy();
        const held = buttonByText(modal()!, "确认报价并从失败段继续") as HTMLButtonElement;
        expect(held.disabled).toBe(true);
        await act(async () => held.click());
        expect(confirmations).toEqual([]);
        expect(submitted()).toEqual([]);
        await page.click(buttonByText(modal()!, "查询原请求与结算"));
        expect(lookups).toBe(2);
        expect(modal()?.textContent).toContain("已退款");
        expect(buttonByText(modal()!, "确认报价并从失败段继续")).toBeUndefined();
        await page.click(buttonByText(modal()!, "从失败步骤重新报价"));
        expect(quotes[3].model).toEqual(quotes[2].model);
        expect(quotes[3].requestId).not.toBe(quotes[2].requestId);
        const confirm = buttonByText(modal()!, "确认报价并从失败段继续") as HTMLButtonElement;
        expect(confirm.disabled).toBe(false);
        await act(async () => {
            confirm.click();
            confirm.click();
        });
        await flush();
        expect(confirmations).toEqual([{ ...quotes[3], previewHash: "quote-4" }]);
        expect(prefs.getState().models.video).toBe(oldPreferences.video);
        expect(fixture.forbidden).toEqual([]);
        expect(page.errors).toEqual([]);
    } finally {
        releaseOldQuote?.();
        apiClient.interceptors.response.eject(interceptor);
        await page.dispose();
        useConfigStore.setState({ config: before });
        prefs.setState({ models: oldPreferences });
    }
});

test("出片恢复：原单轮询核清后仍需全新有效报价，缺少报价标识不能收费", async () => {
    let reconciled = false;
    let returnHash = true;
    const chainPath = `/projects/${P}/video-chains`;
    const page = await mount(`/drama-flow/${P}/video`, {
        chains: "failed",
        prepare(f) {
            const fallback = f.handle;
            f.handle = (method, path, body) => {
                const reply = fallback(method, path, body);
                const diagnostic = { submissionOutcome: reconciled ? "not_submitted" : "unknown", billingStatus: reconciled ? "refunded" : "uncertain", safeToResubmit: reconciled, terminalStatus: "failed" };
                if (method.toLowerCase() === "get" && path.split("?")[0] === chainPath) {
                    const data = reply?.body?.data as { chains: { steps: Record<string, unknown>[] }[] };
                    Object.assign(data.chains[0].steps[0], { taskId: "original", diagnostic });
                }
                if (path === chainPath + "/preview") {
                    const data = reply?.body?.data as { chain: { confirmationBlockedReason?: string; previewHash: string; steps: Record<string, unknown>[] } };
                    if (!reconciled) data.chain.confirmationBlockedReason = "原单尚待核对。";
                    if (!returnHash) data.chain.previewHash = "";
                    Object.assign(data.chain.steps[0], { taskId: "original", diagnostic });
                }
                return reply;
            };
        },
    });
    try {
        await page.click(buttonByText(cardOf("S01")!, "重新出"));
        const estimate = () => page.click(buttonByText(modal()!, "从失败步骤重新报价"));
        const confirm = () => buttonByText(modal()!, "确认报价并从失败段继续") as HTMLButtonElement;
        await estimate();
        expect(confirm().disabled).toBe(true);
        reconciled = true;
        await act(async () => void (await page.client.invalidateQueries({ queryKey: ["project-video-chains", P, U] })));
        await flush();
        expect(modal()?.textContent).toContain("结算 已退款");
        expect(confirm().disabled).toBe(true);
        returnHash = false;
        await estimate();
        expect(confirm().disabled).toBe(true);
        expect(modal()?.textContent).toContain("缺少有效报价标识");
        returnHash = true;
        await estimate();
        expect(confirm().disabled).toBe(false);
        reconciled = false;
        await act(async () => void (await page.client.invalidateQueries({ queryKey: ["project-video-chains", P, U] })));
        await flush();
        expect(confirm().disabled).toBe(true);
        await act(async () => confirm().click());
        expect(submitted()).toEqual([]);
        expect(fixture.forbidden).toEqual([]);
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
    }
});

test("出片恢复：已受理的超时先查原上游任务，取回视频后可在没有可用模型时确认复用", async () => {
    const chainPath = `/projects/${P}/video-chains`;
    let recovered = false;
    let closed = false;
    const requests: string[] = [];
    const original = { id: "accepted-child", status: "failed", providerRequestId: "accepted-job", billing: { status: "uncertain" } };
    const page = await mount(`/drama-flow/${P}/video`, {
        prepare(f) {
            const fallback = f.handle;
            const ok = (data: unknown) => ({ status: 200, body: { code: 0, msg: "ok", data } });
            f.handle = (method, path, body) => {
                const step = {
                    shotId: fx.FIXTURE_SHOT_ID,
                    title: "S01",
                    model: fx.FIXTURE_VIDEO_MODEL,
                    contentSeconds: 21,
                    generationSeconds: 30,
                    referenceCount: 7,
                    taskId: original.id,
                    phase: "generating",
                    status: "failed",
                    diagnostic: {
                        category: "timeout",
                        quoteId: "initial-quote",
                        submissionOutcome: "accepted",
                        terminalStatus: recovered ? "succeeded" : "failed",
                        safeToResubmit: false,
                        canQueryProvider: !recovered,
                        billingStatus: recovered ? "settled" : "uncertain",
                    },
                };
                const chain = { id: "timed-out", projectId: P, unitId: U, anchorShotId: step.shotId, status: closed ? "succeeded" : "failed", steps: [step], previewHash: "initial-quote", totalMicrocredits: 0 };
                if (method.toLowerCase() === "get" && path.split("?")[0] === chainPath) return ok({ chains: [chain] });
                if (path === `/tasks/${original.id}`) {
                    requests.push(path);
                    return ok(original);
                }
                if (path === `/tasks/${original.id}/query-provider`) {
                    requests.push(path);
                    recovered = true;
                    return ok({ task: { ...original, status: "succeeded" }, providerStatus: "completed", recovered, billingSettled: true });
                }
                if (path === chainPath + "/preview") {
                    requests.push(path);
                    return ok({ chain: { ...chain, id: "", status: "preview", previewHash: "reuse-result", steps: [{ ...step, status: "succeeded", videoResourceId: "original-output" }] } });
                }
                if (path === chainPath + "/timed-out/retry") {
                    requests.push(path);
                    expect(body).toMatchObject({ previewHash: "reuse-result", resumeChainId: chain.id });
                    closed = true;
                    return ok({ chain: { ...chain, status: "succeeded" } });
                }
                return fallback(method, path, body);
            };
        },
    });
    try {
        await page.click(buttonByText(cardOf("S01")!, "重新出"));
        expect(modal()?.textContent).toContain("原报价 initial-quote");
        expect((buttonByText(modal()!, "从失败步骤重新报价") as HTMLButtonElement).disabled).toBe(false);
        await page.click(buttonByText(modal()!, "查询原请求与结算"));
        expect(modal()?.textContent).toContain("已取回原视频");
        fixture.overrides.set("/dramas/models", "error");
        await act(async () => void (await page.client.invalidateQueries({ queryKey: ["drama-models"] })));
        await flush();
        await page.click(buttonByText(modal()!, "预览可复用的原视频"));
        expect(modal()?.textContent).toContain("剩余步骤预估 0 平台积分");
        expect(modal()?.querySelector('a[href*="original-output"]')).toBeTruthy();
        await page.click(buttonByText(modal()!, "确认复用原视频"));
        expect(requests).toEqual(["/tasks/accepted-child", "/tasks/accepted-child/query-provider", chainPath + "/preview", chainPath + "/timed-out/retry"]);
        expect(fixture.forbidden).toEqual([]);
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
    }
});

test("出片预览：26 秒正文的预检错误独立显示，历史视频仍可查看，旧报价不能确认", async () => {
    const chainPath = `/projects/${P}/video-chains`;
    const page = await mount(`/drama-flow/${P}/video`, {
        chains: "history",
        prepare(f) {
            f.state.segments[0].durationMs = 26000;
            const handle = f.handle;
            f.handle = (method, path, body) => {
                const reply = handle(method, path, body);
                if (method.toLowerCase() === "get" && path.split("?")[0] === chainPath) {
                    const list = reply?.body?.data as { chains: { steps: Record<string, unknown>[] }[] };
                    Object.assign(list.chains[0].steps[0], { contentSeconds: 8, generationSeconds: 15, videoResourceId: "old-take" });
                }
                if (path === chainPath + "/preview" && f.state.previews === 2) {
                    return {
                        status: 400,
                        body: {
                            code: 400,
                            msg: "参考数量超限",
                            reason: "model_capability_not_supported",
                            details: { shotId: fx.FIXTURE_SHOT_ID, model: fx.FIXTURE_VIDEO_MODEL, contentSeconds: 26, generationSeconds: 30, allowedSeconds: [15, 30], referenceCount: 7, message: "最多支持 5 张参考图" },
                        },
                    };
                }
                return reply;
            };
        },
    });
    try {
        await page.click(buttonByText(cardOf("S01")!, "出这一段"));
        await page.click(buttonByText(modal()!, "预览当前场景与费用"));
        expect(buttonByText(modal()!, "开始连续生成")).toBeTruthy();
        await page.click(buttonByText(modal()!, "预览当前场景与费用"));
        const alert = modal()?.querySelector('[role="alert"]')?.textContent;
        for (const text of ["本次费用预览", "正文 26 秒", "请求 30 秒", "15 / 30 秒", "参考图 7 张", "最多支持 5 张", fx.FIXTURE_VIDEO_MODEL]) expect(alert).toContain(text);
        expect(alert).not.toContain("内容 8 秒");
        expect(modal()?.querySelector('a[href*="old-take"]')).toBeTruthy();
        expect(buttonByText(modal()!, "开始连续生成")).toBeUndefined();
        expect(fixture.state.previewConfigs.map((cfg) => cfg.videoSeconds)).toEqual(["30", "30"]);
        await page.click(buttonByText(modal()!, "调整本段时长或参考素材"));
        expect(currentPath).toContain("/shots?");
        expect(currentPath).toContain(fx.FIXTURE_SHOT_ID);
        expect(submitted()).toEqual([]);
    } finally {
        await page.dispose();
    }
});

for (const chains of ["none", "history", "failed", "running"] as const) {
    test(`R6-D04 真实出片入口「出这一段」打开连续生成面板不崩溃（出片链：${chains}）`, async () => {
        const page = await mount(`/drama-flow/${P}/video`, { chains });
        try {
            // 新工作流先把这一集的出片链写进缓存，形状是接口原样的 { chains }。
            expect(Array.isArray(chainCache(page.client)?.chains)).toBe(true);
            const go = buttonByText(cardOf("S02")!, "出这一段");
            expect(go, "S02 卡上应有可点的「出这一段」").toBeTruthy();
            expect((go as HTMLButtonElement).disabled).toBe(false);
            await page.click(go);
            expect(page.text()).toContain("同场景连续生成");
            expect(page.errors).toEqual([]);

            // 面板这个 observer 挂上后刷新一次：两个 observer 共存，缓存形状不变，新工作流的段卡状态照样读得到。
            await act(async () => void (await page.client.invalidateQueries({ queryKey: ["project-video-chains", P] })));
            await flush();
            expect(Array.isArray(chainCache(page.client)?.chains)).toBe(true);
            if (chains === "failed") expect(cardOf("S01")?.textContent).toContain("失败");
            if (chains === "running") expect(cardOf("S01")?.textContent).toContain("生成中");

            if (chains === "running") {
                // 有链正在出时，面板只读显示这条链，不给预览新任务（不能同时再起一条）。
                expect(modal()?.textContent).toContain("S01");
                expect(buttonByText(modal()!, "预览当前场景与费用")).toBeUndefined();
            } else {
                await page.click(buttonByText(modal()!, "预览当前场景与费用"));
                expect(fixture.state.previews).toBe(1);
                expect(modal()?.textContent).toContain("平台积分（整链预估");
            }
            await page.click(buttonByText(modal()!, "关闭"));
            expect(page.text()).not.toContain("同场景连续生成");
            expect(page.errors).toEqual([]);
            expect(submitted()).toEqual([]);
            expect(fixture.forbidden).toEqual([]);
        } finally {
            await page.dispose();
        }
    });
}

test("R6-D04 出片失败的段点「重新出」：确认前只能重新报价", async () => {
    const page = await mount(`/drama-flow/${P}/video`, { chains: "failed" });
    try {
        await page.click(buttonByText(cardOf("S01")!, "重新出"));
        expect(modal()?.textContent).toContain("上游超时");
        expect(buttonByText(modal()!, "确认报价并从失败段继续")).toBeUndefined();
        await page.click(buttonByText(modal()!, "从失败步骤重新报价"));
        expect(fixture.state.previews).toBe(1);
        expect(modal()?.textContent).toContain("剩余步骤预估 1.5 平台积分");
        expect(buttonByText(modal()!, "确认报价并从失败段继续")).toBeTruthy();
        await page.click(buttonByText(modal()!, "关闭"));
        expect(page.errors).toEqual([]);
        expect(submitted()).toEqual([]);
        expect(fixture.forbidden).toEqual([]);
    } finally {
        await page.dispose();
    }
});

test("R6-D04 同一个 QueryClient：两个 observer 先后挂载、刷新、切集，缓存形状始终是 { chains }", async () => {
    fixture = fx.createDramaFlowFixture({ chains: "failed" });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
    // 新工作流那一侧：同一个查询，自己读 .chains。
    const flow = new QueryObserver(client, { ...videoChainsQuery(P, U) });
    const seenFlow: unknown[] = [];
    const stopFlow = flow.subscribe((result) => result.data && seenFlow.push(result.data));
    await flush();
    // 面板那一侧后挂载，同一个 key。
    const panel = new QueryObserver(client, { ...videoChainsQuery(P, U) });
    const seenPanel: unknown[] = [];
    const stopPanel = panel.subscribe((result) => result.data && seenPanel.push(result.data));
    await act(async () => void (await panel.refetch()));
    await act(async () => void (await flow.refetch()));
    // 面板切到另一集，再切回来。
    panel.setOptions({ ...videoChainsQuery(P, "u-other") });
    await act(async () => void (await panel.refetch()));
    panel.setOptions({ ...videoChainsQuery(P, U) });
    await flush();
    for (const data of [...seenFlow, ...seenPanel, client.getQueryData(videoChainsKey(P, U)), client.getQueryData(videoChainsKey(P, "u-other"))]) {
        expect(Array.isArray((data as { chains?: unknown[] }).chains)).toBe(true);
    }
    expect((client.getQueryData(videoChainsKey(P, U)) as { chains: Array<{ status: string }> }).chains[0].status).toBe("failed");
    stopFlow();
    stopPanel();
    client.clear();
});

test("R6-D04 出片链读失败：新工作流写「没读到」并禁用提交；连续生成面板显示错误，不伪装成没有链", async () => {
    const chainsPath = `/projects/${P}/video-chains`;
    const page = await mount(`/drama-flow/${P}/video`, { chains: "history", prepare: (next) => next.overrides.set(chainsPath, "error") });
    try {
        expect(summaryValue("失败")).toBe("没读到");
        expect((buttonByText(cardOf("S02")!, "出这一段") as HTMLButtonElement).disabled).toBe(true);
    } finally {
        await page.dispose();
    }

    // 旧工作台的用法：单独挂连续生成面板。
    fixture = fx.createDramaFlowFixture({ chains: "history" });
    fixture.overrides.set(chainsPath, "error");
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    try {
        await act(async () =>
            root.render(
                createElement(
                    QueryClientProvider,
                    { client },
                    createElement(
                        App,
                        null,
                        createElement(VideoChainPanel, {
                            open: true,
                            projectId: P,
                            whiteModelStoryboardEnabled: false,
                            unitId: U,
                            shotId: fx.FIXTURE_SHOT_ID,
                            buildModel: async () => ({ model: fx.FIXTURE_VIDEO_MODEL, input: {} }),
                            onClose() {},
                            onRefresh: async () => undefined,
                        }),
                    ),
                ),
            ),
        );
        await flush();
        const alert = document.querySelector(".ant-modal [role='alert']");
        expect(alert?.textContent || "").not.toBe("");
        expect(submitted()).toEqual([]);
    } finally {
        await act(async () => root.unmount());
        host.remove();
        client.clear();
    }
});

// 第九轮 R8-01 / R8-02：出片请求按所选模型能力对齐；能力错误写出是哪个参数。
test("R8-01 全局同步音频开、6 秒，所选模型不支持音频且只有 15/30 档：预览成功，请求按模型对齐，全局偏好不变", async () => {
    const before = useConfigStore.getState().config;
    useConfigStore.setState((state) => ({ config: { ...state.config, videoGenerateAudio: "true", videoWatermark: "true", videoSeconds: "6", vquality: "1080" } }));
    const page = await mount(`/drama-flow/${P}/video`);
    try {
        await page.click(buttonByText(cardOf("S02")!, "出这一段"));
        await page.click(buttonByText(modal()!, "预览当前场景与费用"));
        expect(fixture.state.previewConfigs.length).toBe(1);
        const sent = fixture.state.previewConfigs[0];
        expect(sent.videoGenerateAudio).toBe("false");
        expect(sent.videoWatermark).toBe("false");
        // S02 正文 15 秒，按既有 15/30 秒适配规则用 30 秒档；不是全局的 6 秒。
        expect(sent.videoSeconds).toBe("30");
        expect(fixture.state.previews).toBe(1);
        expect(modal()?.textContent).toContain("平台积分（整链预估");
        // 只影响这次请求：全局偏好原样。
        const after = useConfigStore.getState().config;
        expect([after.videoGenerateAudio, after.videoWatermark, after.videoSeconds, after.vquality]).toEqual(["true", "true", "6", "1080"]);
        await page.click(buttonByText(modal()!, "关闭"));
        expect(submitted()).toEqual([]);
        expect(fixture.forbidden).toEqual([]);
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
        useConfigStore.setState({ config: before });
    }
});

for (const scenario of ["切换模型", "弃稿确认时停用", "独立开局时停用", "弃稿确认时换集", "尚未锁定画风"] as const) {
    test(`MC-SHOTS 跨确认${scenario}，只按当前卡片与当前集提交`, async () => {
        const { useDramaModelPreferencesStore: prefs } = await import("@/stores/use-drama-model-preferences-store");
        const before = useConfigStore.getState().config;
        const previous = prefs.getState().models;
        const textA = "CHANNEL_000001::fixture-text";
        const textB = "CHANNEL_000001::text-b";
        const requests: Array<{ model: string; logicalModelId?: string; allowMissingHandoff?: boolean }> = [];
        const path = `/drama-flow/${P}/shots?ep=${fx.FIXTURE_UNIT_ID}`;
        const page = await mount(path, {
            workflowId: "pipeline-2",
            style: scenario === "尚未锁定画风" ? "none" : "locked",
            handoffGate: true,
            prepare: (f) => {
                f.catalog.channels[0].models.push({ modelKey: "text-b", displayName: "分镜备选文本", available: true, capability: "text", priceTiers: [] });
                useConfigStore.getState().mergeSystemChannels(systemChannelModelChannels(f.catalog.channels as never));
                prefs.setState({ models: { text: textA } });
                const handle = f.handle;
                f.handle = (method, url, body) => {
                    const result = handle(method, url, body);
                    if (url.endsWith("/chapter.shots/run") && method.toUpperCase() === "POST") requests.push(body as (typeof requests)[number]);
                    if (url.endsWith(`/${fx.FIXTURE_UNIT_ID}/workspace`) && result?.body?.data) {
                        Object.assign(result.body.data, {
                            shots: [{ id: fx.FIXTURE_SHOT_ID, unitId: fx.FIXTURE_UNIT_ID, position: 0, title: "S01 随行鲜" }],
                            shotArtifacts: [{ id: "existing-board", shotId: fx.FIXTURE_SHOT_ID, type: "storyboard", status: "ready" }],
                        });
                    }
                    return result;
                };
            },
        });
        const disableText = async () => {
            const key = ["drama-models"];
            const catalog = page.client.getQueryData<DramaModelCatalog>(key)!;
            await act(async () => {
                page.client.setQueryData(key, { ...catalog, options: { ...catalog.options, text: [] }, unavailable: { ...catalog.unavailable, text: catalog.options.text.map(({ choice }) => ({ choice, reason: "文本渠道已暂停" })) } });
            });
            await flush();
        };
        const changeModel = async (model: string, logicalId: string) => {
            await act(async () => {
                useConfigStore.setState(({ config }) => ({
                    config: {
                        ...config,
                        channels: config.channels.map((channel) => ({
                            ...channel,
                            modelCosts: channel.modelCosts?.map((item) => (`CHANNEL_000001::${item.model}` === model ? { ...item, logicalModelId: logicalId } : item)),
                        })),
                    },
                }));
                prefs.getState().remember("text", model);
            });
            await flush();
        };
        try {
            expect(summaryValue("文本模型")).toContain("验收文本模型");
            await page.click(byText("button", "整集重新生成"));
            const discard = byText('[role="dialog"] button', "删掉并整集重新生成");
            if (scenario === "尚未锁定画风") {
                expect(page.text()).toContain("项目还没锁定画风：先到「定妆 · 画风」让导演定画风，再生成分镜");
                expect(discard).toBeUndefined();
                expect(requests).toHaveLength(0);
                return;
            }
            expect(discard).toBeTruthy();
            expect(requests).toHaveLength(0);
            if (scenario === "弃稿确认时换集") await page.go(`/drama-flow/${P}/shots?ep=previous-episode`);
            if (scenario === "弃稿确认时停用") await disableText();
            if (scenario === "切换模型") {
                await changeModel(textB, "latest-b");
                expect(summaryValue("文本模型")).toContain("分镜备选文本");
            }
            await page.click(discard);
            if (scenario === "弃稿确认时停用" || scenario === "弃稿确认时换集") {
                expect(requests).toHaveLength(0);
                return;
            }
            expect(requests).toHaveLength(1);
            expect(requests[0].model).toBe(scenario === "切换模型" ? textB : textA);
            const independent = byText('[role="dialog"] button', "独立开局");
            expect(independent).toBeTruthy();
            if (scenario === "独立开局时停用") {
                await disableText();
                await page.click(independent);
                expect(requests).toHaveLength(1);
            } else {
                expect(requests[0].logicalModelId).toBe("latest-b");
                await changeModel(textA, "latest-a");
                expect(summaryValue("文本模型")).toContain("验收文本模型");
                await page.click(independent);
                expect(requests).toHaveLength(2);
                expect(requests[1]).toMatchObject({ model: textA, logicalModelId: "latest-a", allowMissingHandoff: true });
            }
        } finally {
            await page.dispose();
            useConfigStore.setState({ config: before });
            prefs.setState({ models: previous });
        }
    });
}

// 模拟已打开页面时平台停用图片渠道，只刷新服务端短剧目录，保留浏览器保存的模型。
async function disableFixtureImage(page: Awaited<ReturnType<typeof mount>>, key: string) {
    const catalog = page.client.getQueryData<DramaModelCatalog>(["drama-models"])!;
    const removed = catalog.options.image.find((option) => option.choice.modelKey === key)!;
    await act(async () => {
        page.client.setQueryData(["drama-models"], {
            ...catalog,
            options: { ...catalog.options, image: catalog.options.image.filter((option) => option !== removed) },
            unavailable: { ...catalog.unavailable, image: [{ choice: removed.choice, reason: "图片渠道已暂停" }] },
        });
    });
    await flush();
}

test("MC-IMG 确认面板打开后渠道停用：阻止旧选择，切换后卡片与排队请求一致", async () => {
    const { useDramaModelPreferencesStore } = await import("@/stores/use-drama-model-preferences-store");
    const before = useConfigStore.getState().config;
    const prefs = useDramaModelPreferencesStore.getState().models;
    const page = await mount(`/drama-flow/${P}/look`, {
        stopAtAssets: true,
        style: "locked",
        imageModel: "CHANNEL_000001::image-a",
        prepare: (f) => {
            f.catalog.channels[0].models.push({ modelKey: "image-b", displayName: "备选图片渠道", available: true, capability: "image", priceTiers: [] });
            useDramaModelPreferencesStore.setState({ models: {} });
            useConfigStore.getState().mergeSystemChannels(systemChannelModelChannels(f.catalog.channels as never));
        },
    });
    try {
        await page.click(byText("button", "继续：角色场景资产"));
        expect(modal()?.querySelector("button[aria-haspopup]")?.textContent).toContain("验收图片模型");
        await act(async () => {
            useDramaModelPreferencesStore.getState().remember("image", "CHANNEL_000001::image-b");
        });
        expect(modal()?.querySelector("button[aria-haspopup]")?.textContent).toContain("备选图片渠道");
        await act(async () => {
            useDramaModelPreferencesStore.getState().remember("image", "CHANNEL_000001::image-a");
        });
        await disableFixtureImage(page, "image-a");
        const submit = () => buttonByText(modal()!, "为2个资产出图")!;
        expect(submit().disabled).toBe(true);
        await page.click(submit());
        expect(fixture.state.imageQueues).toHaveLength(0);
        await page.click(modal()!.querySelector("button[aria-haspopup]"));
        const paused = byText('[role="option"]', "image-a") as HTMLButtonElement;
        expect(paused.disabled).toBe(true);
        expect(paused.textContent).toContain("图片渠道已暂停");
        await page.click(byText("button.canvas-model-picker-brand", "备选图片渠道"));
        await page.click(byText('[role="option"]', "image-b"));
        expect(summaryValue("图片模型")).toContain("备选图片渠道");
        expect(submit().disabled).toBe(false);
        await page.click(submit());
        expect(fixture.state.imageQueues.map((q) => q.imageTask?.model)).toEqual(["CHANNEL_000001::image-b"]);
        expect(fixture.forbidden).toEqual([]);
    } finally {
        await page.dispose();
        useConfigStore.setState({ config: before });
        useDramaModelPreferencesStore.setState({ models: prefs });
    }
});

test("MC-IMG 继续纯文本步骤：最后一个图片渠道停用时请求中没有出图模板", async () => {
    const before = useConfigStore.getState().config;
    const posts: unknown[] = [];
    const page = await mount(`/drama-flow/${P}/look`, {
        stopAtAssets: true,
        style: "locked",
        imageModel: "CHANNEL_000001::image-a",
        prepare: (f) => {
            f.catalog.channels[0].models.push({ modelKey: "gpt-6.1-sol", displayName: "文本渠道", available: true, capability: "text", priceTiers: [] });
            useConfigStore.getState().mergeSystemChannels(systemChannelModelChannels(f.catalog.channels as never));
            const handle = f.handle;
            f.handle = (method, path, body) => {
                if (method.toUpperCase() === "POST" && path.endsWith("/drama/pipeline/next")) {
                    posts.push(body);
                    return { status: 200, body: { code: 0, data: {}, msg: "" } };
                }
                return handle(method, path, body);
            };
        },
    });
    try {
        await disableFixtureImage(page, "image-a");
        const key = ["project", P, "drama", "progress"];
        const progress = page.client.getQueryData<DramaProgress>(key)!;
        await act(async () => {
            page.client.setQueryData(key, { ...progress, pipeline: { ...progress.pipeline, current: "palette", stop: { stage: "palette", continuable: true, label: "场次色卡", action: "继续：场次色卡" } } });
        });
        await flush();
        expect(summaryValue("文本模型")).toContain("文本渠道");
        await page.click(byText("button", "继续：场次色卡"));
        expect(posts).toHaveLength(1);
        expect(posts[0]).toHaveProperty("model", "CHANNEL_000001::gpt-6.1-sol");
        expect(posts[0]).not.toHaveProperty("imageTask");
        expect(fixture.forbidden).toEqual([]);
    } finally {
        await page.dispose();
        useConfigStore.setState({ config: before });
    }
});

test("MC-IMG 原偏好恢复后可重新选择并提交，即使它与弹窗打开时保存的偏好相同", async () => {
    const { useDramaModelPreferencesStore: prefs } = await import("@/stores/use-drama-model-preferences-store");
    const config = useConfigStore.getState().config;
    const previous = prefs.getState().models;
    const modelB = "CHANNEL_000001::image-b";
    const page = await mount(`/drama-flow/${P}/look`, {
        stopAtAssets: true,
        style: "locked",
        imageModel: "CHANNEL_000001::image-a",
        prepare: (f) => {
            f.catalog.channels[0].models.push({ modelKey: "image-b", displayName: "恢复图片渠道", available: true, capability: "image", priceTiers: [] });
            useConfigStore.getState().mergeSystemChannels(systemChannelModelChannels(f.catalog.channels as never));
            prefs.setState({ models: { image: modelB } });
        },
    });
    try {
        const key = ["drama-models"];
        const all = page.client.getQueryData<DramaModelCatalog>(key)!;
        await disableFixtureImage(page, "image-b");
        await page.click(byText("button", "继续：角色场景资产"));
        const picker = () => modal()!.querySelector("button[aria-haspopup]")!;
        expect(picker().textContent).toContain("验收图片模型");
        await act(async () => {
            page.client.setQueryData(key, all);
        });
        await disableFixtureImage(page, "image-a");
        const submit = () => buttonByText(modal()!, "为2个资产出图")!;
        expect(submit().disabled).toBe(true);
        await page.click(picker());
        await page.click(byText("button.canvas-model-picker-brand", "恢复图片渠道"));
        await page.click(byText('[role="option"]', "image-b"));
        expect(picker().textContent).toContain("恢复图片渠道");
        expect(summaryValue("图片模型")).toContain("恢复图片渠道");
        expect(submit().disabled).toBe(false);
        await page.click(submit());
        expect(fixture.state.imageQueues.map((q) => q.imageTask?.model)).toEqual([modelB]);
    } finally {
        await page.dispose();
        useConfigStore.setState({ config });
        prefs.setState({ models: previous });
    }
});

for (const [override, expected, notExpected] of [
    ["capability", "同步音频超出支持范围", undefined],
    ["capability-bare", "所选模型不支持当前请求", "同步音频"],
] as const) {
    test(`R8-02 费用预览被能力校验拒绝（${override === "capability" ? "带详情" : "没有详情"}）：面板写清楚，不提交`, async () => {
        const page = await mount(`/drama-flow/${P}/video`, { prepare: (next) => next.overrides.set(`/projects/${P}/video-chains/preview`, override) });
        try {
            await page.click(buttonByText(cardOf("S02")!, "出这一段"));
            await page.click(buttonByText(modal()!, "预览当前场景与费用"));
            const alert = [...modal()!.querySelectorAll("[role='alert']")].map((node) => node.textContent || "").join(" ");
            expect(alert).toContain(expected);
            if (notExpected) expect(alert).not.toContain(notExpected);
            expect(modal()?.textContent).not.toContain("平台积分（整链预估");
            expect(submitted()).toEqual([]);
            expect(page.errors).toEqual([]);
        } finally {
            await page.dispose();
        }
    });
}

for (const [workflowId, title] of [
    ["pipeline-1", "SSS 导演文档"],
    ["pipeline-2", "SSS 导演文档"],
    ["pipeline-3", "本体论导演文档"],
] as const) {
    test(`SSS-01 导演流水线（${workflowId}）：新工作流有「导演文档」，分镜状态按段说，不提镜头表`, async () => {
        const page = await mount(`/drama-flow/${P}/shots`, { workflowId });
        try {
            expect(page.text()).toContain("导演分镜已装成 2 段");
            expect(page.text()).not.toContain("还没有镜头表");
            expect(buttonByText(document.body, "整集重新生成")).toBeTruthy();
            await page.click(buttonByText(document.body, "导演文档"));
            const drawer = document.querySelector(".ant-drawer");
            expect(drawer?.textContent).toContain(title);
            if (workflowId !== "pipeline-3") {
                for (const tab of ["全剧圣经", "立意与结构", "世界与总谱", "E01 随行鲜"]) expect(drawer?.textContent).toContain(tab);
            } else {
                expect(drawer?.textContent).toContain("世界圣经卡");
                expect(drawer?.textContent).not.toContain("立意与结构");
            }
            expect(fixture.calls.some((call) => call.path === `/projects/${P}/drama/ontology`)).toBe(true);
            expect(fixture.unknown).toEqual([]);
            expect(page.errors).toEqual([]);
        } finally {
            await page.dispose();
        }
    });
}

test("SSS-01 原流程项目：没有「导演文档」，分镜状态仍按镜头表说", async () => {
    const page = await mount(`/drama-flow/${P}/shots`);
    try {
        expect(page.text()).toContain("还没有镜头表");
        expect(buttonByText(document.body, "导演文档")).toBeFalsy();
        expect(fixture.calls.some((call) => call.path === `/projects/${P}/drama/ontology`)).toBe(false);
    } finally {
        await page.dispose();
    }
});

const episodeRail = () => document.querySelector<HTMLElement>("aside.drama-flow-rail")!;
const episodeButton = (name: string) => [...episodeRail().querySelectorAll("button")].find((button) => (button.getAttribute("aria-label") || button.textContent)?.trim() === name);
const selectEpisode = (title: string) => [...episodeRail().querySelectorAll("button[aria-pressed]")].find((button) => (button.querySelector("strong")?.textContent || button.getAttribute("aria-label"))?.startsWith(title));
const episodeDialog = () => [...document.querySelectorAll<HTMLElement>('[role="dialog"]')].at(-1)!;
const routeParam = (key: string) => new URL(currentPath, "http://fixture.local").searchParams.get(key);
type EpisodePage = Awaited<ReturnType<typeof mount>>;

async function fillEpisodeField(field: HTMLInputElement | HTMLTextAreaElement, text: string) {
    await act(async () => {
        const prototype = field.tagName === "INPUT" ? dom.HTMLInputElement.prototype : dom.HTMLTextAreaElement.prototype;
        Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(field, text);
        field.dispatchEvent(new Event("input", { bubbles: true }));
    });
}

function checkEpisodeSelection(title: string, id: string) {
    expect(selectEpisode(title)?.getAttribute("aria-pressed")).toBe("true");
    expect(routeParam("ep")).toBe(id);
}

async function deleteEpisode(page: EpisodePage, title: string) {
    await page.click(episodeButton(`删除此剧集：${title}`));
    const dialog = episodeDialog();
    expect(dialog.textContent).toContain(title);
    for (const impact of ["正文", "镜头表", "段", "画布"]) expect(dialog.textContent).toContain(impact);
    await page.click(buttonByText(dialog, "删除剧集"));
}

test("FLOW-01 同一三集项目增减剧集、删除单元、更换演员并按新线路完成失败段", async () => {
    const server = createDramaJourneyProject();
    const writes: Array<{ method: string; path: string; body: unknown }> = [];
    const { useDramaModelPreferencesStore: prefs } = await import("@/stores/use-drama-model-preferences-store");
    const previous = prefs.getState().models;
    const config = useConfigStore.getState().config;
    const page = await mount(`/drama-flow/${P}/script?ep=ep-a`, {
        prepare(f) {
            server.attach(f);
            const handle = f.handle;
            f.handle = (method, path, body) => {
                if (method.toUpperCase() !== "GET" && path.startsWith("/projects/")) writes.push({ method: method.toUpperCase(), path, body });
                return handle(method, path, body);
            };
            prefs.setState({ models: { video: fx.FIXTURE_VIDEO_MODEL } });
            useConfigStore.getState().mergeSystemChannels(systemChannelModelChannels(f.catalog.channels as never));
        },
    });
    const shot = "shot-ep-a";
    const identity = { projectId: P, unitId: "ep-a", shotId: shot };
    const media = { imageIds: ["image-new"], audioIds: ["voice-new"] };
    const stage = (label: string) => page.click(byText("nav.drama-flow-strip button", label));
    const editor = () => page.click(document.querySelector('[aria-label="编辑绑定 S01"]'));
    const checkCompiledMedia = () => {
        const rows = [...document.querySelectorAll("aside.drama-flow-pane .drama-flow-seg-assets > span")];
        expect(
            rows.map((row) => ({
                tag: row.textContent?.split(" · ")[0],
                resource: row
                    .querySelector("img,audio")
                    ?.getAttribute("src")
                    ?.match(/\/resources\/([^/]+)\/file$/)?.[1],
            })),
        ).toEqual([
            { tag: "@图片1", resource: "image-new" },
            { tag: "@音频1", resource: "voice-new" },
        ]);
    };
    try {
        checkEpisodeSelection("雨棚", "ep-a");
        expect(episodeRail().querySelector(".drama-flow-rail-head > span")?.textContent).toBe("剧集 · 3");
        await page.click(episodeButton("添加剧集"));
        await fillEpisodeField(episodeDialog().querySelector("input")!, "清晨");
        await page.click(buttonByText(episodeDialog(), "创建剧集"));
        checkEpisodeSelection("清晨", "new-ep-4");
        await page.click(selectEpisode("渡口"));
        await deleteEpisode(page, "渡口");
        checkEpisodeSelection("归家", "ep-c");
        await page.click(selectEpisode("雨棚"));
        await stage("分镜");
        checkEpisodeSelection("雨棚", "ep-a");
        expect(episodeRail().querySelector(".drama-flow-rail-head > span")?.textContent).toBe("剧集 · 3");
        expect(selectEpisode("清晨")).toBeDefined();
        expect(selectEpisode("渡口")).toBeUndefined();
        await page.click(document.querySelector('[aria-label="删除分镜单元 S02"]'));
        await page.click(byText('[role="dialog"] button', "确认删除"));
        expect(document.querySelector('[aria-label="删除分镜单元 S02"]')).toBeNull();
        await editor();
        expect(document.querySelector(`audio[aria-label="试听${server.oldActor}的声音样本"]`)?.getAttribute("src")).toContain("/voice-old/file");
        await page.click(document.querySelector(`[aria-label="更换${server.oldActor}"]`));
        const options = document.querySelector<HTMLInputElement>("#flow-unit-binding-choice")!;
        await act(async () => options.dispatchEvent(new dom.KeyboardEvent("keydown", { bubbles: true, key: "ArrowDown", keyCode: 40 })));
        await flush();
        await page.click(byText(".ant-select-item-option", server.newActor));
        await page.click(buttonByText(episodeDialog(), "保存绑定"));
        expect(document.querySelector('article.drama-flow-seg [aria-label="绑定的资产"]')?.textContent).toContain(server.newActor);
        expect(document.querySelector('article.drama-flow-seg [aria-label="绑定的资产"]')?.textContent).not.toContain(server.oldActor);
        await stage("出片");
        checkEpisodeSelection("雨棚", "ep-a");
        expect(cardOf("S02")).toBeUndefined();
        await page.click(buttonByText(cardOf("S01")!, "详情"));
        checkCompiledMedia();
        expect(server.compiles.at(-1)).toMatchObject({ ...identity, ...media, model: fx.FIXTURE_VIDEO_MODEL });

        for (let index = 0; index < 4; index++) {
            const model = index === 3 ? server.automaticModel : fx.FIXTURE_VIDEO_MODEL;
            const expected = { ...identity, ...media, model };
            await page.click(buttonByText(cardOf("S01")!, index === 0 ? "出这一段" : "重新出"));
            if (index === 3) {
                expect(summaryValue("视频模型")).toBe(server.automaticName);
                expect(modal()?.textContent).toContain("已自动切换项目视频线路");
                expect(buttonByText(modal()!, "确认报价并从失败段继续")).toBeUndefined();
            } else {
                expect(summaryValue("视频模型")).toBe(fx.FIXTURE_VIDEO_MODEL_NAME);
            }
            await page.click(buttonByText(modal()!, index === 0 ? "预览当前场景与费用" : "从失败步骤重新报价"));
            expect(server.attempts).toHaveLength(index);
            expect(server.quotes).toHaveLength(index + 1);
            const quote = server.quotes[index];
            expect(quote).toMatchObject(expected);
            if (index > 0) expect(quote.resumeChainId).toBe("journey-chain");
            expect(modal()?.textContent).toContain(quote.previewHash);
            await page.click(buttonByText(modal()!, index === 0 ? "开始连续生成" : "确认报价并从失败段继续"));
            expect(server.attempts).toHaveLength(index + 1);
            expect(server.attempts[index]).toMatchObject({ ...expected, previewHash: quote.previewHash, requestId: quote.requestId, status: index === 3 ? "succeeded" : "failed" });
            await page.click(buttonByText(modal()!, "关闭"));
        }
        expect(new Set(server.quotes.map((quote) => quote.previewHash)).size).toBe(4);
        expect(new Set(server.attempts.map((attempt) => attempt.taskId)).size).toBe(4);
        expect(server.compiles.at(-1)).toMatchObject({ ...identity, ...media, model: server.automaticModel });
        checkCompiledMedia();
        expect(cardOf("S01")?.textContent).toContain("已出片");
        expect(cardOf("S02")).toBeUndefined();
        await stage("分镜");
        expect(selectEpisode("渡口")).toBeUndefined();
        expect(document.querySelector('[aria-label="删除分镜单元 S02"]')).toBeNull();
        await editor();
        expect(document.querySelector(`audio[aria-label="试听${server.newActor}的声音样本"]`)?.getAttribute("src")).toContain("/voice-new/file");
        expect(server.episodes.units.filter((unit) => unit.kind === "episode").map((unit) => unit.id)).toEqual(["ep-a", "ep-c", "new-ep-4"]);
        expect(writes.every(({ path }) => path.startsWith(`/projects/${P}/`))).toBe(true);
        expect(writes.filter(({ method }) => method === "DELETE").map(({ path }) => path)).toEqual([`/projects/${P}/units/ep-b`, `/projects/${P}/shots/discard-unit`]);
        expect(writes.filter(({ path }) => path === `/projects/${P}/shots/${shot}/assets`).map(({ body }) => body)).toEqual([expect.objectContaining({ assetVersionId: "actor-new-v1", replaceReferenceId: expect.any(String) })]);
        expect(fixture.forbidden).toEqual([]);
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
        useConfigStore.setState({ config });
        prefs.setState({ models: previous });
        localStorage.removeItem("drama-flow:rail-view");
    }
}, 30000);

test("EP-01 三集项目经真实导航增集、编辑正文、删中间集和末集，再从缓存为空的旧链接打开", async () => {
    const server = createEpisodeProject();
    let page = await mount(`/drama-flow/${P}/script?ep=ep-b`, { prepare: server.attach });
    try {
        checkEpisodeSelection("渡口", "ep-b");
        expect(episodeRail().textContent).not.toContain("原文章节");
        await page.click(episodeButton("添加剧集"));
        const create = episodeDialog();
        await fillEpisodeField(create.querySelector("input")!, "  新的清晨  ");
        await fillEpisodeField(create.querySelector("textarea")!, "早安 <小舟>。");
        await page.click(buttonByText(create, "创建剧集"));
        expect(server.writes).toEqual([{ method: "POST", path: `/projects/${P}/units`, body: { kind: "episode", title: "新的清晨", sourceText: "<p>早安 &lt;小舟&gt;。</p>" } }]);
        checkEpisodeSelection("新的清晨", "new-ep-4");
        expect(document.querySelector(".drama-flow-script-text")?.textContent).toBe("早安 <小舟>。");
        await page.click(buttonByText(document.body, "改剧本"));
        await fillEpisodeField(document.querySelector('textarea[aria-label="剧本正文"]')!, "第二稿");
        await page.click(buttonByText(document.body, "保存"));
        expect(server.writes[1]).toMatchObject({ method: "PATCH", path: `/projects/${P}/units/new-ep-4`, body: { sourceText: "<p>第二稿</p>" } });

        for (const stage of ["定妆", "分镜", "出片", "剧本"]) {
            await page.click(byText("nav.drama-flow-strip button", stage));
            checkEpisodeSelection("新的清晨", "new-ep-4");
            expect(byText("nav.drama-flow-strip button", "剧本")?.textContent).toContain("4 集");
        }
        await page.click(selectEpisode("渡口"));
        await deleteEpisode(page, "渡口");
        checkEpisodeSelection("归家", "ep-c");
        expect(document.querySelector(".drama-flow-script-text")?.textContent).toBe("归家的独立正文");
        expect(selectEpisode("渡口")).toBeUndefined();
        expect(byText("nav.drama-flow-strip button", "分镜")?.textContent).toContain("2 段");
        await page.click(byText("nav.drama-flow-strip button", "分镜"));
        expect(document.querySelector("main")?.textContent).toContain("归家长镜头");
        expect(document.querySelector("main")?.textContent).not.toContain("渡口长镜头");
        await page.click(selectEpisode("新的清晨"));
        await deleteEpisode(page, "新的清晨");
        checkEpisodeSelection("归家", "ep-c");
        expect(server.writes.filter((call) => call.method === "DELETE").map((call) => call.path)).toEqual([`/projects/${P}/units/ep-b`, `/projects/${P}/units/new-ep-4`]);
        expect(page.errors).toEqual([]);

        await page.dispose();
        page = await mount(`/drama-flow/${P}/video?ep=ep-b&item=shot-ep-b`, { prepare: server.attach });
        checkEpisodeSelection("雨棚", "ep-a");
        expect(routeParam("item")).toBeNull();
        expect(document.querySelector("main")?.textContent).toContain("雨棚长镜头");
        await page.click(selectEpisode("归家"));
        await page.click(episodeButton("矩阵"));
        await deleteEpisode(page, "雨棚");
        checkEpisodeSelection("归家", "ep-c");
        await deleteEpisode(page, "归家");
        expect(routeParam("ep")).toBeNull();
        expect(episodeButton("添加首集")).toBeTruthy();
        expect(episodeRail().textContent).not.toContain("原文章节");
        expect(byText("nav.drama-flow-strip button", "剧本")?.textContent).toContain("还没有剧集");
        await page.click(episodeButton("添加首集"));
        await fillEpisodeField(episodeDialog().querySelector("input")!, "重开");
        await page.click(buttonByText(episodeDialog(), "创建剧集"));
        checkEpisodeSelection("重开", "new-ep-5");
        expect(server.units.map((unit) => unit.id)).toEqual(["source-only", "new-ep-5"]);
        expect(server.writes.map((call) => call.method)).toEqual(["POST", "PATCH", "DELETE", "DELETE", "DELETE", "DELETE", "POST"]);
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
        localStorage.removeItem("drama-flow:rail-view");
    }
}, 30000);

test("删除当前中间集后，索引先刷新也会选中下一集并清除旧段链接", async () => {
    const server = createEpisodeProject();
    const page = await mount(`/drama-flow/${P}/script?ep=ep-b&item=shot-ep-b`, { prepare: server.attach });
    const adapter = apiClient.defaults.adapter;
    const confirmation = Promise.withResolvers<void>();
    try {
        if (typeof adapter !== "function") throw new Error("缺少夹具 HTTP 适配器");
        apiClient.defaults.adapter = async (config) => {
            const response = await adapter(config);
            if (config.method === "delete" && config.url === `/projects/${P}/units/ep-b`) await confirmation.promise;
            return response;
        };
        checkEpisodeSelection("渡口", "ep-b");
        await deleteEpisode(page, "渡口");
        expect(server.writes).toMatchObject([{ method: "DELETE", path: `/projects/${P}/units/ep-b` }]);
        // 服务端已经完成删除，列表查询可以先于本页的删除成功回调拿到新索引。
        await act(async () => await page.client.invalidateQueries({ queryKey: ["project", P, "units"], exact: true }));
        await flush();
        checkEpisodeSelection("归家", "ep-c");
        expect(routeParam("item")).toBeNull();
        expect(selectEpisode("渡口")).toBeUndefined();
        expect(document.querySelector(".drama-flow-script-text")?.textContent).toBe("归家的独立正文");
        confirmation.resolve();
        await flush();
        checkEpisodeSelection("归家", "ep-c");
    } finally {
        confirmation.resolve();
        apiClient.defaults.adapter = adapter;
        await page.dispose();
    }
});

test("EP-02 必填、取消和写入失败保留导航、未保存的剧本以及创建表单", async () => {
    const server = createEpisodeProject();
    const page = await mount(`/drama-flow/${P}/script?ep=ep-a`, { prepare: server.attach });
    try {
        await page.click(buttonByText(document.body, "改剧本"));
        const draft = () => document.querySelector<HTMLTextAreaElement>('textarea[aria-label="剧本正文"]')!;
        await fillEpisodeField(draft(), "还没保存的独白");
        await page.click(episodeButton("添加剧集"));
        await fillEpisodeField(episodeDialog().querySelector("input")!, "   ");
        await page.click(buttonByText(episodeDialog(), "创建剧集"));
        expect(server.writes).toHaveLength(0);
        await fillEpisodeField(episodeDialog().querySelector("input")!, "保留标题");
        await fillEpisodeField(episodeDialog().querySelector("textarea")!, "保留正文");
        server.rejectNext = "POST";
        await page.click(buttonByText(episodeDialog(), "创建剧集"));
        expect(server.writes[0]?.method).toBe("POST");
        expect(episodeDialog().querySelector('[role="alert"]')?.textContent).toContain("测试请求失败");
        expect(episodeDialog().querySelector("input")?.value).toBe("保留标题");
        expect(episodeDialog().querySelector("textarea")?.value).toBe("保留正文");
        checkEpisodeSelection("雨棚", "ep-a");
        await page.click(buttonByText(episodeDialog(), "取消"));
        await page.click(episodeButton("删除此剧集：雨棚"));
        await page.click(buttonByText(episodeDialog(), "取消"));
        expect(server.writes).toHaveLength(1);
        server.rejectNext = "DELETE";
        await deleteEpisode(page, "雨棚");
        expect(episodeDialog().querySelector('[role="alert"]')?.textContent).toContain("测试请求失败");
        checkEpisodeSelection("雨棚", "ep-a");
        expect(draft().value).toBe("还没保存的独白");
        expect(server.units.map((unit) => unit.id)).toEqual(["source-only", "ep-a", "ep-b", "ep-c"]);
        expect(server.writes.map((call) => call.method)).toEqual(["POST", "DELETE"]);
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
    }
}, 30000);

test("EP-03 旧原文章节带分镜时，剧集进度、视频链请求与待办只包含成片剧集", async () => {
    const server = createEpisodeProject();
    const page = await mount(`/drama-flow/${P}/video?ep=ep-b&item=shot-ep-b`, { prepare: server.attach });
    try {
        expect(fixture.state.segments.some((shot) => shot.unitId === "source-only")).toBe(true);
        expect(byText("nav.drama-flow-strip button", "分镜")?.textContent).toContain("3 段");
        expect(byText("nav.drama-flow-strip button", "出片")?.textContent).toContain("已出 0/3");
        await deleteEpisode(page, "雨棚");
        checkEpisodeSelection("渡口", "ep-b");
        expect(routeParam("item")).toBe("shot-ep-b");
        expect(byText("nav.drama-flow-strip button", "出片")?.textContent).toContain("已出 0/2");
        expect([...document.querySelectorAll(".drama-flow-vcard-title strong")].map((node) => node.textContent)).toEqual(["渡口长镜头"]);
        const chains = fixture.calls.filter((call) => call.path.includes("/video-chains?"));
        expect(chains.length).toBeGreaterThan(0);
        expect(chains.some((call) => call.path.includes("source-only"))).toBe(false);
        await page.click(byText(".drama-flow-top-actions button", "待办"));
        const inbox = document.querySelector(".drama-flow-inbox")!;
        expect(inbox.textContent).toContain("渡口长镜头");
        expect(inbox.textContent).toContain("归家长镜头");
        expect(inbox.textContent).not.toContain("雨棚长镜头");
        expect(inbox.textContent).not.toContain("原文章节长镜头");
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
    }
}, 30000);

test("EP-04 导演文档的实际交接页在删中间集后显示新的前序剧集与交接正文", async () => {
    const server = createEpisodeProject();
    const page = await mount(`/drama-flow/${P}/shots?ep=ep-c`, { prepare: server.attach, workflowId: "pipeline-2" });
    const openHandoff = async () => {
        await page.click(buttonByText(document.body, "导演文档"));
        await page.click(byText('.ant-drawer [role="tab"]', "归家"));
        return document.querySelector<HTMLElement>('.ant-drawer [aria-label="前序剧集"]')!;
    };
    try {
        expect((await openHandoff()).textContent).toContain("第 2 集《渡口》");
        expect(document.querySelector(".ant-drawer blockquote")?.textContent).toBe("交接：渡口中留下的纸条");
        await page.click(document.querySelector(".ant-drawer-close"));
        await deleteEpisode(page, "渡口");
        checkEpisodeSelection("归家", "ep-c");
        expect((await openHandoff()).textContent).toContain("第 1 集《雨棚》");
        expect(document.querySelector(".ant-drawer blockquote")?.textContent).toBe("交接：雨棚中留下的纸条");
        expect(byText('.ant-drawer [role="tab"]', "渡口")).toBeUndefined();
        expect(server.writes.map(({ method, path }) => [method, path])).toEqual([["DELETE", `/projects/${P}/units/ep-b`]]);
        expect(page.errors).toEqual([]);
    } finally {
        await page.dispose();
    }
}, 30000);

for (const empty of [false, true]) {
    test(`EP-05 归档项目${empty ? "空态" : "有三集时"}：列表和矩阵都说明只读原因，点击增删不发写请求`, async () => {
        const server = createEpisodeProject({ archived: true, empty });
        const page = await mount(`/drama-flow/${P}/script`, { prepare: server.attach });
        try {
            for (const view of ["列表", "矩阵"]) {
                await page.click(episodeButton(view));
                expect(episodeButton(view)?.getAttribute("aria-pressed")).toBe("true");
                expect(episodeRail().textContent).toMatch(/项目已归档.*恢复/);
                const add = episodeButton(empty ? "添加首集" : "添加剧集");
                expect(add?.disabled).toBe(true);
                await page.click(add);
                const deletes = [...episodeRail().querySelectorAll<HTMLButtonElement>('button[aria-label^="删除此剧集"]')];
                expect(deletes.map((button) => button.getAttribute("aria-label"))).toEqual(empty ? [] : ["删除此剧集：雨棚", "删除此剧集：渡口", "删除此剧集：归家"]);
                for (const button of deletes) {
                    expect(button.disabled).toBe(true);
                    await page.click(button);
                }
                expect(episodeDialog()).toBeUndefined();
            }
            if (!empty) {
                await page.click(selectEpisode("渡口"));
                checkEpisodeSelection("渡口", "ep-b");
                expect(document.querySelector(".drama-flow-script-text")?.textContent).toBe("渡口的独立正文");
                expect(buttonByText(document.body, "改剧本")?.disabled).toBe(true);
            }
            expect(server.writes).toEqual([]);
            expect(page.errors).toEqual([]);
        } finally {
            await page.dispose();
            localStorage.removeItem("drama-flow:rail-view");
        }
    });
}

for (const creating of [true, false]) {
    test(`EP-06 ${creating ? "创建" : "删除"}确认打开后项目归档，提交禁用，保留草稿且允许取消`, async () => {
        const server = createEpisodeProject();
        const page = await mount(`/drama-flow/${P}/script?ep=ep-a`, { prepare: server.attach });
        try {
            await page.click(buttonByText(document.body, "改剧本"));
            const draft = document.querySelector<HTMLTextAreaElement>('textarea[aria-label="剧本正文"]')!;
            await fillEpisodeField(draft, "归档之前的草稿");
            await page.click(episodeButton(creating ? "添加剧集" : "删除此剧集：雨棚"));
            if (creating) await fillEpisodeField(episodeDialog().querySelector("input")!, "归档之前的标题");
            const confirm = () => buttonByText(episodeDialog(), creating ? "创建剧集" : "删除剧集");
            expect(confirm()?.disabled).toBe(false);
            server.archived = true;
            await act(async () => void (await page.client.invalidateQueries({ queryKey: ["project", P, "core"], exact: true })));
            await flush();
            expect(episodeDialog().querySelector('[role="status"]')?.textContent).toMatch(/项目已归档.*恢复/);
            expect(confirm()?.disabled).toBe(true);
            await page.click(confirm());
            if (creating) {
                expect(episodeDialog().querySelector("input")?.value).toBe("归档之前的标题");
                const form = episodeDialog().querySelector("form")!;
                await act(async () => void form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
                await flush();
            }
            await page.click(buttonByText(episodeDialog(), "取消"));
            expect(episodeDialog()).toBeUndefined();
            checkEpisodeSelection("雨棚", "ep-a");
            expect(draft.readOnly).toBe(true);
            expect(draft.value).toBe("归档之前的草稿");
            expect(buttonByText(document.body, "保存")?.disabled).toBe(true);
            await page.click(buttonByText(document.body, "保存"));
            expect(server.writes).toEqual([]);
            expect(page.errors).toEqual([]);
        } finally {
            await page.dispose();
        }
    });
}
