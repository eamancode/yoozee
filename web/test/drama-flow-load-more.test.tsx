import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
const originals = new Map<string, PropertyDescriptor | undefined>();

// 假的 IntersectionObserver：记下每个观察器盯着哪些节点，测试里手动触发「进入视野」。
class FakeObserver {
    static all: FakeObserver[] = [];
    nodes = new Set<Element>();
    constructor(public callback: (entries: Array<{ isIntersecting: boolean; target: Element }>) => void) {
        FakeObserver.all.push(this);
    }
    observe(node: Element) {
        this.nodes.add(node);
    }
    unobserve(node: Element) {
        this.nodes.delete(node);
    }
    disconnect() {
        this.nodes.clear();
    }
}
const observing = (node: Element | null) => Boolean(node) && FakeObserver.all.some((observer) => observer.nodes.has(node!));
const scrollIntoView = (node: Element) => FakeObserver.all.filter((observer) => observer.nodes.has(node)).forEach((observer) => observer.callback([{ isIntersecting: true, target: node }]));

for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    HTMLElement: dom.HTMLElement,
    Element: dom.Element,
    Node: dom.Node,
    SVGElement: dom.SVGElement,
    getComputedStyle: dom.getComputedStyle.bind(dom),
    IntersectionObserver: FakeObserver,
    IS_REACT_ACT_ENVIRONMENT: true,
})) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const { act, createElement } = await import("react");
const { createRoot } = await import("react-dom/client");
const { useLoadMoreSentinel } = await import("../src/pages/drama-flow/hooks/use-load-more-sentinel");

afterAll(() => {
    dom.happyDOM.abort();
    for (const [key, value] of originals) {
        if (value) Object.defineProperty(globalThis, key, value);
        else Reflect.deleteProperty(globalThis, key);
    }
});

/** 和项目列表一样的结构：筛选切换时整个结果区（含哨兵）先换成加载态，读完再渲染回来。 */
function List({ loading, enabled, onLoadMore }: { loading: boolean; enabled: boolean; onLoadMore: () => void }) {
    const ref = useLoadMoreSentinel({ enabled, onLoadMore });
    if (loading) return createElement("div", { "data-loading": true }, "正在读每个项目的进度");
    return createElement("div", null, createElement("p", null, "没有符合的，正在往后加载…"), createElement("div", { ref, className: "load-more" }));
}

test("结果区经历加载态后哨兵是新节点：新节点要被观察，进入视野就加载下一页", async () => {
    FakeObserver.all = [];
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    let calls = 0;
    const onLoadMore = () => void (calls += 1);
    try {
        await act(async () => root.render(createElement(List, { loading: false, enabled: true, onLoadMore })));
        const first = host.querySelector(".load-more");
        expect(observing(first)).toBe(true);

        // 切到「已完成」：摘要在读，结果区和哨兵都被卸掉。
        await act(async () => root.render(createElement(List, { loading: true, enabled: true, onLoadMore })));
        expect(host.querySelector(".load-more")).toBeNull();
        expect(observing(first)).toBe(false);

        // 摘要读完、首批没有匹配：哨兵重新挂上，是新节点，且分页条件没变。
        await act(async () => root.render(createElement(List, { loading: false, enabled: true, onLoadMore })));
        const second = host.querySelector(".load-more")!;
        expect(second).not.toBe(first);
        expect(observing(second)).toBe(true);

        await act(async () => scrollIntoView(second));
        expect(calls).toBe(1);
    } finally {
        await act(async () => root.unmount());
        host.remove();
    }
});

test("正在加载下一页、加载失败或没有下一页时不观察；条件恢复后重新观察", async () => {
    FakeObserver.all = [];
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    let calls = 0;
    const onLoadMore = () => void (calls += 1);
    try {
        await act(async () => root.render(createElement(List, { loading: false, enabled: false, onLoadMore })));
        const node = host.querySelector(".load-more")!;
        expect(observing(node)).toBe(false);

        await act(async () => root.render(createElement(List, { loading: false, enabled: true, onLoadMore })));
        expect(observing(node)).toBe(true);
        await act(async () => scrollIntoView(node));
        expect(calls).toBe(1);

        await act(async () => root.unmount());
        expect(observing(node)).toBe(false);
    } finally {
        host.remove();
    }
});
