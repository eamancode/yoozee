import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    localStorage: dom.localStorage,
    Node: dom.Node,
    Element: dom.Element,
    HTMLElement: dom.HTMLElement,
    HTMLButtonElement: dom.HTMLButtonElement,
    SVGElement: dom.SVGElement,
    ShadowRoot: dom.ShadowRoot,
    CustomEvent: dom.CustomEvent,
    MutationObserver: dom.MutationObserver,
    ResizeObserver: dom.ResizeObserver,
    getComputedStyle: dom.getComputedStyle.bind(dom),
    requestAnimationFrame: dom.requestAnimationFrame.bind(dom),
    cancelAnimationFrame: dom.cancelAnimationFrame.bind(dom),
    IS_REACT_ACT_ENVIRONMENT: true,
}))
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { ModelPicker } = await import("../../src/components/model-picker");
const { createModelChannel, defaultConfig } = await import("../../src/stores/use-config-store");
afterAll(() => dom.happyDOM.abort());

test.each([
    ["已保存模型", "new::saved-model", "saved-model"],
    ["没有已选模型", "", "first-model"],
])("Space 打开后异步到达的可选模型获得焦点，Escape 将焦点归还模型卡（%s）", async (_case, value, expectedModel) => {
    const host = document.body.appendChild(document.createElement("div"));
    const root = createRoot(host);
    const blocked = createModelChannel({ id: "old", name: "原品牌", models: ["old-model"] });
    const available = createModelChannel({ id: "new", name: "新品牌", models: ["first-model", "saved-model"] });
    const choices: string[] = [];
    const render = (ready: boolean) =>
        act(async () =>
            root.render(
                <ModelPicker
                    config={{ ...defaultConfig, channels: ready ? [blocked, available] : [blocked] }}
                    value={value}
                    modelDisabledReason={(model) => (model === "old::old-model" ? "已下架" : "")}
                    onChange={(model) => choices.push(model)}
                    showSelectedPrice={false}
                    triggerContent="项目模型"
                />,
            ),
        );
    const frame = () =>
        act(async () => {
            await new Promise<void>((resolve) => dom.requestAnimationFrame(() => resolve()));
        });
    try {
        await render(false);
        const trigger = host.querySelector<HTMLButtonElement>('button[aria-haspopup="listbox"]')!;
        trigger.focus();
        await act(async () => {
            const space = { key: " ", code: "Space", bubbles: true, cancelable: true };
            const down = trigger.dispatchEvent(new dom.KeyboardEvent("keydown", space));
            const up = trigger.dispatchEvent(new dom.KeyboardEvent("keyup", space));
            // happy-dom 不执行原生按钮的 Space 默认动作。浏览器在 keyup 后派发 detail=0 的 click。
            if (down && up) trigger.dispatchEvent(new dom.MouseEvent("click", { bubbles: true, detail: 0 }));
        });
        await frame();
        expect(trigger.getAttribute("aria-expanded")).toBe("true");
        expect(document.querySelector('[role="listbox"]')?.textContent).toContain("原品牌");
        expect(document.querySelector('[role="option"]:not(:disabled)')).toBeNull();

        await render(true);
        await frame();
        const candidate = document.activeElement;
        expect(candidate?.matches('[role="option"]:not(:disabled)')).toBe(true);
        expect(candidate!.textContent).toContain(expectedModel);
        expect(candidate!.getAttribute("aria-selected")).toBe(String(Boolean(value)));
        await act(async () => candidate!.dispatchEvent(new dom.KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
        await frame();
        expect(trigger.getAttribute("aria-expanded")).toBe("false");
        expect(document.activeElement).toBe(trigger);
        expect(choices).toEqual([]);
    } finally {
        await act(async () => root.unmount());
        host.remove();
    }
});
