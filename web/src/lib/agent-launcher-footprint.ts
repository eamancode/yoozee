import { useSyncExternalStore } from "react";

/**
 * 柚智浮层（右下角 Agent 入口）当前占的位置，只读通知：浮层自己发布，页面用来给按钮让位。
 * 用离视口右边、下边的距离表示（浮层按右下角锚定），视口尺寸变化时不用重算。
 * 收起/展开、头像媒体加载后变大、拖动结束、隐藏都会更新；隐藏或没挂载时是 null。
 */
export type AgentLauncherFootprint = {
    right: number;
    bottom: number;
    width: number;
    height: number;
    /** 收起/展开小按钮从浮层左上角伸出去的距离，算占位时一起算上。 */
    overhang: number;
};

let current: AgentLauncherFootprint | null = null;
const listeners = new Set<() => void>();

const same = (left: AgentLauncherFootprint | null, right: AgentLauncherFootprint | null) =>
    left === right || Boolean(left && right && left.right === right.right && left.bottom === right.bottom && left.width === right.width && left.height === right.height && left.overhang === right.overhang);

export function publishAgentLauncherFootprint(next: AgentLauncherFootprint | null) {
    if (same(current, next)) return;
    current = next;
    for (const listener of listeners) listener();
}

export function readAgentLauncherFootprint() {
    return current;
}

function subscribe(listener: () => void) {
    listeners.add(listener);
    return () => void listeners.delete(listener);
}

export function useAgentLauncherFootprint() {
    return useSyncExternalStore(subscribe, readAgentLauncherFootprint, () => null);
}

/** 浮层在视口里的矩形（含收起按钮伸出的部分）。 */
export function agentLauncherRect(footprint: AgentLauncherFootprint, viewport: { width: number; height: number }) {
    const right = viewport.width - footprint.right;
    const bottom = viewport.height - footprint.bottom;
    return { left: right - footprint.width - footprint.overhang, top: bottom - footprint.height - footprint.overhang, right, bottom };
}

type Box = { left: number; top: number; right: number; bottom: number };
const GAP = 12;

/**
 * 横向让位：元素和浮层上下有重叠、左右也有重叠时，元素右侧要留多宽才能让内容整体落在浮层左边。
 * 只用元素的边框盒（不含它自己因此加的内边距），结果不随留白反复变化。
 */
export function launcherInsetRight(element: Box, launcher: Box | null) {
    if (!launcher) return 0;
    const overlaps = element.top < launcher.bottom && element.bottom > launcher.top && element.left < launcher.right && element.right > launcher.left;
    return overlaps ? Math.max(0, Math.ceil(element.right - launcher.left + GAP)) : 0;
}
