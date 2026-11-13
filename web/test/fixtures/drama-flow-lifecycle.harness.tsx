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
    Element: dom.Element,
    Node: dom.Node,
    Text: dom.Text,
    DocumentFragment: dom.DocumentFragment,
    SVGElement: dom.SVGElement,
    ShadowRoot: dom.ShadowRoot,
    Event: dom.Event,
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
    for (const row of document.querySelectorAll("dl.drama-flow-summary > div")) if (row.querySelector("dt")?.textContent === label) return row.querySelector("dd")?.textContent || "";
    return undefined;
};
const infoValue = (label: string) => {
    const terms = [...document.querySelectorAll("dl.drama-flow-info-facts dt")];
    return terms.find((node) => node.textContent === label)?.nextElementSibling?.textContent || undefined;
};
const P = fx.FIXTURE_PROJECT_ID;

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
const submitted = () => fixture.calls.filter((call) => call.method !== "GET" && /\/video-chains(\/[^/]+\/(retry|cancel))?$/.test(call.path));

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

test("R6-D04 出片失败的段点「重新出」：面板显示失败原因，能预览新任务与费用，不点续跑", async () => {
    const page = await mount(`/drama-flow/${P}/video`, { chains: "failed" });
    try {
        await page.click(buttonByText(cardOf("S01")!, "重新出"));
        expect(modal()?.textContent).toContain("上游超时");
        expect(buttonByText(modal()!, "从失败步骤继续")).toBeTruthy();
        await page.click(buttonByText(modal()!, "预览新任务与费用"));
        expect(fixture.state.previews).toBe(1);
        expect(modal()?.textContent).toContain("平台积分（整链预估");
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
