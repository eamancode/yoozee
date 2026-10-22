import { describe, expect, test } from "bun:test";
import { createRef } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import { CanvasViewport } from "../src/components/canvas/canvas-viewport";
import { canvasAppearanceForTheme } from "../src/lib/canvas/canvas-appearance";
import { applyCanvasLiveViewport, CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, CANVAS_VIEWPORT_PREVIEW_EVENT, canvasGridMetrics } from "../src/lib/canvas/canvas-live-viewport";
import type { CanvasBackgroundMode } from "../src/lib/canvas-theme";
import type { ViewportTransform } from "../src/types/canvas";

const viewports: ViewportTransform[] = [
    { x: 0, y: 0, k: 1 },
    { x: 389.5, y: -207.25, k: 0.05 },
    { x: -17.125, y: 20.875, k: 0.119 },
    { x: -17.125, y: 20.875, k: 0.12 },
    { x: 150.2, y: -40.1, k: 0.666 },
    { x: 150.2, y: -40.1, k: 0.667 },
    { x: -1800.5, y: 3100.75, k: 2 },
];

// 与 canvas-viewport 的 CANVAS_GRID_BASE 一致：网格随视口平移缩放，间距按 k 在 [base*2/3, base*4/3) 内折叠。
const gridBase: Record<"lines" | "dots", number> = { dots: 24, lines: 32 };

function renderGrid(mode: CanvasBackgroundMode, viewport: ViewportTransform, theme: "light" | "dark" = "dark") {
    const markup = renderToStaticMarkup(
        <CanvasViewport containerRef={createRef<HTMLDivElement>()} viewport={viewport} onViewportChange={() => {}} backgroundMode={mode} appearance={canvasAppearanceForTheme(theme)}>
            <div data-test-node />
        </CanvasViewport>,
    );
    return markup.match(/<div data-canvas-grid-layer[^>]*>/)?.[0] ?? null;
}

describe("canvas screen-space background", () => {
    for (const mode of ["lines", "dots"] as const) {
        test(`${mode} derives grid geometry from the viewport`, () => {
            for (const theme of ["light", "dark"] as const) {
                for (const viewport of viewports) {
                    const markup = renderGrid(mode, viewport, theme);
                    expect(markup).not.toBeNull();
                    const { spacing, offsetX, offsetY } = canvasGridMetrics(viewport, gridBase[mode]);
                    expect(markup).toContain(`background-size:${spacing}px ${spacing}px`);
                    expect(markup).toContain(`transform:translate3d(${offsetX}px, ${offsetY}px, 0)`);
                    expect(markup).not.toContain("var(--canvas-");
                    if (mode === "dots") expect(markup).toContain("1px, transparent 1.4px");
                }
            }
        });
    }

    test("blank mode renders no grid at any viewport", () => {
        for (const viewport of viewports) expect(renderGrid("blank", viewport)).toBeNull();
    });

    test("live updates transform the world and emit events without mutating the background", () => {
        const properties = new Map<string, string>([["--canvas-committed-scale", "0.5"]]);
        const world = { style: { transform: "", transformOrigin: "", willChange: "" } };
        const grid = { dataset: { canvasGridBase: String(gridBase.dots) }, style: { backgroundSize: "", transform: "" } };
        const container = Object.assign(new EventTarget(), {
            style: {
                getPropertyValue: (name: string) => properties.get(name) ?? "",
                setProperty: (name: string, value: string) => properties.set(name, value),
            },
            dataset: { canvasViewportInteracting: "true" },
            querySelector: (selector: string) => (selector === "[data-canvas-world-layer]" ? world : grid),
        });
        const graphics: ViewportTransform[] = [];
        const previews: ViewportTransform[] = [];
        let scrolls = 0;
        container.addEventListener(CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, (event) => graphics.push((event as CustomEvent<ViewportTransform>).detail));
        container.addEventListener(CANVAS_VIEWPORT_PREVIEW_EVENT, (event) => previews.push((event as CustomEvent<ViewportTransform>).detail));
        container.addEventListener("scroll", () => scrolls++);

        for (const viewport of viewports) {
            applyCanvasLiveViewport(container as unknown as HTMLDivElement, viewport, false);
            expect(world.style.transform).toBe(`translate3d(${viewport.x}px, ${viewport.y}px, 0) scale(${viewport.k / 0.5})`);
            // 交互期不改继承变量，避免整棵节点子树在每一帧强制重算样式。
            expect(properties.has("--canvas-live-scale")).toBe(false);
            expect(properties.has("--canvas-live-inverse-scale")).toBe(false);
            // 网格层直接改合成属性：间距随缩放、偏移随平移取模。
            const { spacing, offsetX, offsetY } = canvasGridMetrics(viewport, gridBase.dots);
            expect(grid.style.backgroundSize).toBe(`${spacing}px ${spacing}px`);
            expect(grid.style.transform).toBe(`translate3d(${offsetX}px, ${offsetY}px, 0)`);
        }
        expect(graphics).toEqual(viewports);
        expect(previews).toEqual([]);
        expect(scrolls).toBe(0);
        expect(world.style.willChange).toBe("transform");

        container.dataset.canvasViewportInteracting = "false";
        applyCanvasLiveViewport(container as unknown as HTMLDivElement, viewports[0]);
        expect(properties.get("--canvas-live-scale")).toBe("1");
        expect(properties.get("--canvas-live-inverse-scale")).toBe("1");
        expect(world.style.willChange).toBe("");
        expect(previews).toEqual([viewports[0]]);
        expect(scrolls).toBe(1);
    });
});
