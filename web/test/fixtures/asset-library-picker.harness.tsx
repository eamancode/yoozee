import { Window } from "happy-dom";
import { expect, mock } from "bun:test";

const dom = new Window({ url: "http://localhost/" });
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    HTMLElement: dom.HTMLElement,
    Element: dom.Element,
    Node: dom.Node,
    SVGElement: dom.SVGElement,
    ShadowRoot: dom.ShadowRoot,
    getComputedStyle: dom.getComputedStyle.bind(dom),
    IS_REACT_ACT_ENVIRONMENT: true,
})) {
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
const { App } = await import("antd");
const base = {
    title: "历史上传图",
    coverUrl: "",
    tags: [],
    createdAt: "2026-10-01",
    updatedAt: "2026-10-01",
    kind: "image",
    category: "other",
    data: { storageKey: "resource:upload", dataUrl: "", width: 800, height: 600, bytes: 100, mimeType: "image/png" },
};
mock.module("@/stores/use-user-store", () => ({ useUserStore: (select: (state: unknown) => unknown) => select({ user: { id: "owner" }, hydrated: true }) }));
mock.module("@/components/cached-resource-image", () => ({ CachedResourceImage: () => <span>图片</span> }));
mock.module("@/services/user-data-sync", () => ({
    loadAssetLibraryPage: async ({ page }: { page: number }) => ({
        assets: [{ ...base, id: `remote-${page}`, title: page === 1 ? "历史上传图" : "历史生成图" }],
        total: 81,
        matchingCategoryCounts: { other: 81 },
        categoryCounts: { other: 81 },
        page,
        pageSize: 40,
    }),
    saveRemoteUserDataNow: async () => {},
    deleteAssetsWithRemoteSync: async () => {},
    localSavedRemotePendingMessage: () => "同步失败",
}));
const { AssetLibraryPickerModal, AssetLibrarySelectionError } = await import("@/components/assets/asset-library-picker-modal");
const host = document.createElement("div");
document.body.append(host);
const root = createRoot(host);
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
let finish: (() => void) | undefined;
let calls = 0;
let selection: string[] = [];
let selectedRecords: string[] = [];
let partialFailure = false;
const render = async () =>
    act(async () => {
        root.render(
            <App>
                <QueryClientProvider client={queryClient}>
                    <AssetLibraryPickerModal
                        open
                        remoteLibrary
                        acceptRemoteItems
                        items={[]}
                        categoryLabels={{ all: "全部素材", other: "其他" }}
                        pagination={undefined}
                        onClose={() => {}}
                        onConfirm={async (ids, items) => {
                            calls++;
                            selection = ids;
                            selectedRecords = (items || []).map((item) => item.id);
                            if (partialFailure) throw new AssetLibrarySelectionError([ids[0]], "上传图：资源不存在");
                            await new Promise<void>((resolve) => {
                                finish = resolve;
                            });
                        }}
                    />
                </QueryClientProvider>
            </App>,
        );
    });
async function settle() {
    await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 35));
    });
}
const button = (label: string) => Array.from(document.querySelectorAll("button")).find((entry) => entry.getAttribute("aria-label") === label || entry.textContent?.trim() === label) as HTMLButtonElement;
try {
    await render();
    await settle();
    const first = document.querySelector<HTMLButtonElement>(".asset-picker-card-action")!;
    expect(first.disabled).toBe(false);
    expect(document.querySelector(".asset-picker-count")?.textContent).toContain("共 81 个匹配素材");
    await act(async () => first.click());
    await act(async () => button("下一页").click());
    await settle();
    await act(async () => document.querySelector<HTMLButtonElement>(".asset-picker-card-action")!.click());
    const confirm = button("使用已选素材（2）");
    await act(async () => {
        confirm.click();
        confirm.click();
    });
    expect(calls).toBe(1);
    expect(selection).toEqual(["remote-1", "remote-2"]);
    expect(selectedRecords).toEqual(expect.arrayContaining(["remote-1", "remote-2"]));
    finish!();
    await settle();
    partialFailure = true;
    await act(async () => button("使用已选素材（2）").click());
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("资源不存在");
    expect(document.querySelector(".asset-picker-count")?.textContent).toContain("已选 1 个");
    console.log("PASS: remote selection, retained records, counts, duplicate submit, partial failure");
} finally {
    await act(async () => root.unmount());
    queryClient.clear();
    dom.happyDOM.abort();
}
