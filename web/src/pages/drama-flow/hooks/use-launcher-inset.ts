import { useLayoutEffect, useState, type CSSProperties } from "react";

import { agentLauncherRect, launcherInsetRight, useAgentLauncherFootprint } from "@/lib/agent-launcher-footprint";

/** 详情栏至少保留的可用高度：浮层挡得太多时宁可重叠，也不把详情栏压没。 */
export const PANE_MIN_HEIGHT = 240;
/** 窄屏浮层式详情栏默认离视口底的距离，和 drama-flow.css 里 `bottom: max(72px, …)` 一致。 */
export const PANE_NARROW_BOTTOM = 72;
/** 详情栏和浮层之间留的间距。 */
const GAP = 12;

export type PaneLayout = { minHeight: number; shift: number; clear: number; paneMax: number };

/**
 * 详情栏怎样给柚智浮层让位（纯函数，便于测边界）。
 * top：详情栏没让位时的顶边；bottomLimit：详情栏下边缘最低能到哪（窄屏是视口高 − 72，宽屏是视口高）；
 * launcher：和详情栏左右重叠的浮层的上下边（不重叠传 null）。
 * 浮层把详情栏所在的竖条分成上、下两段，详情栏放进更高的那一段：
 * - 放上段：顶边不动，下边缘停在浮层上方（clear = 下边缘到视口底的距离）。
 * - 放下段：顶边下移到浮层下方（shift），下边缘不动。浮层被拖到顶部时，标题和关闭按钮因此不会被盖住。
 * 那一段不够 minHeight（一般 240px，视口太矮时降为实际可用高度）时仍保留 minHeight，允许和浮层重叠。
 * 只用「没让位时」的顶边和视口算，不用详情栏自己的高度，所以按结果改样式后不会反复变化。
 */
export function paneLauncherLayout(top: number, bottomLimit: number, viewportHeight: number, launcher: { top: number; bottom: number } | null): PaneLayout {
    const room = Math.max(0, bottomLimit - top);
    const minHeight = Math.min(PANE_MIN_HEIGHT, room);
    if (!launcher) return { minHeight, shift: 0, clear: 0, paneMax: room };
    const above = Math.max(0, Math.min(bottomLimit, launcher.top - GAP) - top);
    const belowStart = Math.max(top, launcher.bottom + GAP);
    const below = Math.max(0, bottomLimit - belowStart);
    if (above >= below) {
        const height = Math.min(room, Math.max(above, minHeight));
        return { minHeight, shift: 0, clear: Math.max(0, Math.ceil(viewportHeight - (top + height))), paneMax: Math.floor(height) };
    }
    const height = Math.min(room, Math.max(below, minHeight));
    // 下段不够高时往上挪到正好留出 minHeight（允许和浮层重叠），不越过原来的顶边。
    const start = Math.max(top, Math.min(belowStart, bottomLimit - height));
    return { minHeight, shift: Math.ceil(start - top), clear: Math.max(0, Math.ceil(viewportHeight - (start + height))), paneMax: Math.floor(height) };
}

/**
 * 给右下角柚智浮层让位：按浮层实际占位（收起/展开、头像加载后变大、拖动后的位置都算）算出的值，
 * 写成 CSS 变量，由样式用到内边距、位置或高度上。不隐藏、不挪动浮层。
 * side="right"：底栏这类横条。和浮层上下重叠时右侧留白（--drama-flow-launcher-inset），按钮整体落在浮层左边。
 * side="pane"：详情栏。和浮层左右重叠时按 paneLauncherLayout 放进浮层上方或下方的空档：
 *   --drama-flow-pane-shift（顶边下移）、--drama-flow-launcher-clear（下边缘离视口底）、--drama-flow-pane-max（高度上限）。
 */
export function useLauncherInset(side: "right" | "pane") {
    const footprint = useAgentLauncherFootprint();
    const [node, setNode] = useState<HTMLElement | null>(null);
    const [vars, setVars] = useState<Record<string, string>>({});

    useLayoutEffect(() => {
        if (!node) return;
        const measure = () => {
            const viewport = { width: window.innerWidth, height: window.innerHeight };
            const launcher = footprint ? agentLauncherRect(footprint, viewport) : null;
            const box = node.getBoundingClientRect();
            let next: Record<string, string> = {};
            if (side === "right") next = { "--drama-flow-launcher-inset": `${launcherInsetRight(box, launcher)}px` };
            else if (launcher && box.left < launcher.right && box.right > launcher.left) {
                const style = getComputedStyle(node);
                const fixed = style.position === "fixed";
                // 减掉此刻真正加在元素上的下移量，拿到「没让位时」的顶边（不用自己记的值，避免样式还没生效时算错）。
                const top = box.top - (Number.parseFloat(style.getPropertyValue("--drama-flow-pane-shift")) || 0);
                const layout = paneLauncherLayout(top, viewport.height - (fixed ? PANE_NARROW_BOTTOM : 0), viewport.height, launcher);
                next = { "--drama-flow-pane-shift": `${layout.shift}px`, "--drama-flow-launcher-clear": `${layout.clear}px`, "--drama-flow-pane-max": `${layout.paneMax}px` };
            }
            setVars((current) => (JSON.stringify(current) === JSON.stringify(next) ? current : next));
        };
        measure();
        // 元素换行变高、窗口变窄、页面滚动（详情栏是 sticky，顶边会动）都会改变结果。
        const observer = new ResizeObserver(measure);
        observer.observe(node);
        window.addEventListener("resize", measure);
        window.addEventListener("scroll", measure, true);
        return () => {
            observer.disconnect();
            window.removeEventListener("resize", measure);
            window.removeEventListener("scroll", measure, true);
        };
    }, [node, footprint, side]);

    return { ref: setNode, vars, style: vars as CSSProperties };
}
