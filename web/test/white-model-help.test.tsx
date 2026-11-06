import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
const originals = new Map<string, PropertyDescriptor | undefined>();
for (const [key, value] of Object.entries({
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
})) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { WhiteModelStoryboardOption } = await import("../src/pages/projects/workbench/white-model-storyboard-option");

afterAll(async () => {
    await dom.happyDOM.abort();
    for (const [key, value] of originals) {
        if (value) Object.defineProperty(globalThis, key, value);
        else Reflect.deleteProperty(globalThis, key);
    }
});

test("说明按钮首次聚焦后点击仍打开，Esc 关闭，不切换选项或提交表单", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const changes: boolean[] = [];
    let submissions = 0;
    try {
        await act(async () =>
            root.render(
                <form
                    onSubmit={(event) => {
                        event.preventDefault();
                        submissions++;
                    }}
                >
                    <WhiteModelStoryboardOption checked={false} onChange={(checked) => changes.push(checked)} settings />
                </form>,
            ),
        );
        const button = host.querySelector("button")!;
        expect(button.type).toBe("button");
        expect(button.getAttribute("aria-describedby")).toBeNull();
        await act(async () => button.focus());
        expect(button.getAttribute("aria-describedby")).toBeTruthy();
        await act(async () => button.click());
        const tooltipId = button.getAttribute("aria-describedby")!;
        expect(tooltipId).toBeTruthy();
        expect(document.getElementById(tooltipId)?.textContent).toContain("已开始的生成链沿用提交时设置");
        expect(host.querySelector("input")?.checked).toBe(false);
        expect(changes).toEqual([]);
        expect(submissions).toBe(0);
        await act(async () => {
            button.dispatchEvent(new dom.MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body }));
            await new Promise((resolve) => setTimeout(resolve, 150));
        });
        expect(button.getAttribute("aria-describedby")).toBeTruthy();
        await act(async () => button.dispatchEvent(new dom.KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
        expect(button.getAttribute("aria-describedby")).toBeNull();
        await act(async () => button.click());
        expect(button.getAttribute("aria-describedby")).toBeTruthy();
        await act(async () => button.blur());
        expect(button.getAttribute("aria-describedby")).toBeNull();
    } finally {
        await act(async () => root.unmount());
        host.remove();
    }
});
