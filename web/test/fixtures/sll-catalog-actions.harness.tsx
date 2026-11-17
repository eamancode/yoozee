import { expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
for (const [name, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    HTMLElement: dom.HTMLElement,
    Element: dom.Element,
    Node: dom.Node,
    SVGElement: dom.SVGElement,
    ShadowRoot: dom.ShadowRoot,
    ResizeObserver: dom.ResizeObserver,
    getComputedStyle: dom.getComputedStyle.bind(dom),
    IS_REACT_ACT_ENVIRONMENT: true,
}))
    Object.defineProperty(globalThis, name, { configurable: true, value });

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
const { SLLCatalogDialog } = await import("../../src/pages/admin/channels/sll-catalog-dialog");
const { apiClient } = await import("../../src/services/api/request");

test("confirm the group and drive independent catalog and metric actions through the real dialog", async () => {
    const path = "/admin/channels/fixture/sll-catalog";
    const calls: string[] = [];
    const imported = {
        group: "contracted",
        needsGroupConfirmation: false,
        added: 1,
        updated: 0,
        collectedAt: "2026-11-17T10:00:00Z",
        excluded: [],
        rows: [{ modelId: "abc", displayName: "Fixture", group: "contracted", groupRatio: 1.5, available: true, enabled: true, capabilities: {}, tiers: [], metric: { state: "never", windowHours: 24, rate: null, stale: false } }],
    };
    let view = { ...imported, group: "old-group", needsGroupConfirmation: true };
    let reported = false;
    apiClient.defaults.adapter = async (config) => {
        const route = `${config.method} ${config.url}`;
        calls.push(route);
        if (route === `post ${path}/sync`) {
            expect(JSON.parse(config.data)).toEqual({ group: "contracted" });
            view = imported;
        } else if (route === `post ${path}/metrics`) {
            view.rows[0]!.metric.state = "fetch_failed";
            return { status: 502, statusText: "", config, headers: {}, data: { code: 502, msg: "官方成功率采集失败" } };
        } else expect(route).toBe(`get ${path}`);
        return { status: 200, statusText: "", config, headers: {}, data: { code: 0, data: structuredClone(view) } };
    };
    const root = createRoot(document.body.appendChild(document.createElement("div")));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    const button = (label: string) => Array.from(document.querySelectorAll("button")).find((node) => node.textContent?.trim() === label)!;
    const settle = () =>
        act(async () => {
            await new Promise((resolve) => setTimeout(resolve, 40));
        });
    try {
        await act(async () =>
            root.render(
                <QueryClientProvider client={client}>
                    <SLLCatalogDialog
                        channelId="fixture"
                        onClose={() => {}}
                        onImported={async () => {
                            reported = true;
                        }}
                    />
                </QueryClientProvider>,
            ),
        );
        await settle();
        const input = document.querySelector("#sll-call-group") as HTMLInputElement;
        expect(document.body.textContent).toContain("渠道凭据已变更");
        expect(input.value).toBe("");
        expect(button("同步公开价目").disabled).toBe(true);
        await act(async () => {
            Object.getOwnPropertyDescriptor(dom.HTMLInputElement.prototype, "value")!.set!.call(input, "contracted");
            input.dispatchEvent(new dom.Event("input", { bubbles: true }));
        });
        expect(button("同步公开价目").disabled).toBe(false);
        await act(async () => button("同步公开价目").click());
        await settle();
        expect(reported).toBe(true);
        expect(document.body.textContent).toContain("目录同步完成");
        expect(calls.filter((call) => call.startsWith("post"))).toEqual([`post ${path}/sync`]);
        await act(async () => button("刷新官方成功率").click());
        await settle();
        expect(calls.filter((call) => call.startsWith("post"))).toEqual([`post ${path}/sync`, `post ${path}/metrics`]);
        expect(document.querySelector('[role="alert"]')?.textContent).toContain("官方成功率采集失败");
        expect(document.querySelector("summary")?.textContent).toContain("采集失败");
    } finally {
        await act(async () => root.unmount());
        client.clear();
        await dom.happyDOM.abort();
    }
}, 25_000);
