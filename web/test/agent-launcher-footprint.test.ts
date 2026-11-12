import { expect, test } from "bun:test";

import { agentLauncherRect, launcherInsetRight, publishAgentLauncherFootprint, readAgentLauncherFootprint } from "../src/lib/agent-launcher-footprint";
import { PANE_MIN_HEIGHT, PANE_NARROW_BOTTOM, paneLauncherLayout } from "../src/pages/drama-flow/hooks/use-launcher-inset";

// 线上第四轮遮挡复现的几何：1424×905 视口，柚智 244×220 离右下各 20px，底栏主按钮 x=1298.625 y=859 105×34。
const viewport = { width: 1424, height: 905 };
const expanded = { right: 20, bottom: 20, width: 244, height: 220, overhang: 8 };
const collapsed = { right: 20, bottom: 20, width: 48, height: 48, overhang: 8 };

test("浮层矩形按右下角锚定换算，并算上收起按钮伸出的部分", () => {
    expect(agentLauncherRect(expanded, viewport)).toEqual({ left: 1152, top: 657, right: 1404, bottom: 885 });
});

test("底栏和浮层上下左右都重叠时，右侧留白让按钮整体落在浮层左边", () => {
    const launcher = agentLauncherRect(expanded, viewport);
    const bar = { left: 200, top: 847, right: 1424, bottom: 905 };
    const inset = launcherInsetRight(bar, launcher);
    expect(inset).toBe(1424 - 1152 + 12);
    // 留白后按钮右边缘（bar.right - inset - 原有内边距）落在浮层左边。
    expect(bar.right - inset).toBeLessThan(launcher.left);
    // 收起后只让一个小圆球的位置。
    expect(launcherInsetRight(bar, agentLauncherRect(collapsed, viewport))).toBe(1424 - (1424 - 20 - 48 - 8) + 12);
});

test("上下或左右不重叠时不留白；浮层隐藏时不留白", () => {
    const launcher = agentLauncherRect(expanded, viewport);
    expect(launcherInsetRight({ left: 200, top: 100, right: 1424, bottom: 160 }, launcher)).toBe(0);
    expect(launcherInsetRight({ left: 200, top: 847, right: 1000, bottom: 905 }, launcher)).toBe(0);
    expect(launcherInsetRight({ left: 200, top: 847, right: 1424, bottom: 905 }, null)).toBe(0);
});

test("发布占位：相同值不重复通知，隐藏时清空", () => {
    publishAgentLauncherFootprint(expanded);
    expect(readAgentLauncherFootprint()).toEqual(expanded);
    publishAgentLauncherFootprint({ ...expanded });
    expect(readAgentLauncherFootprint()).toEqual(expanded);
    publishAgentLauncherFootprint(null);
    expect(readAgentLauncherFootprint()).toBeNull();
});

// 第六轮 R5-01：浮层被拖到别处后，详情栏放进浮层上方或下方更高的空档；不够高时保留最小高度，不被压没。
/** 窄屏浮层式详情栏的实际位置：top = 72 + shift，bottom = max(72, clear)。 */
const narrow = (layout: { shift: number; clear: number }, viewportHeight: number) => {
    const top = 72 + layout.shift;
    const bottom = viewportHeight - Math.max(PANE_NARROW_BOTTOM, layout.clear);
    return { top, bottom, height: bottom - top };
};
const narrowLimit = (viewportHeight: number) => viewportHeight - PANE_NARROW_BOTTOM;

test("默认右下角：放在浮层上方，和第五轮一致（1024×768 下高 436px）", () => {
    const launcher = { top: 768 - 20 - 220 - 8, bottom: 768 - 20 };
    const layout = paneLauncherLayout(72, narrowLimit(768), 768, launcher);
    expect(layout).toEqual({ minHeight: PANE_MIN_HEIGHT, shift: 0, clear: 260, paneMax: 436 });
    expect(narrow(layout, 768)).toEqual({ top: 72, bottom: 508, height: 436 });
});

test("复现：1024×768 头像被拖到顶部（y 20–248）后，详情栏挪到浮层下方，标题和关闭按钮不被盖住", () => {
    const launcher = { top: 12, bottom: 248 };
    const layout = paneLauncherLayout(72, narrowLimit(768), 768, launcher);
    const box = narrow(layout, 768);
    expect(box.top).toBe(248 + 12);
    expect(box.bottom).toBe(768 - 72);
    expect(box.height).toBeGreaterThanOrEqual(PANE_MIN_HEIGHT);
    expect(layout.paneMax).toBe(box.height);
});

test("浮层停在中间、上下两段都不够 240px：保留 240px，允许重叠，不压没", () => {
    const launcher = { top: 250, bottom: 500 };
    const layout = paneLauncherLayout(72, narrowLimit(768), 768, launcher);
    const box = narrow(layout, 768);
    expect(box.height).toBe(PANE_MIN_HEIGHT);
    expect(box.top).toBeGreaterThanOrEqual(72);
    expect(box.bottom).toBeLessThanOrEqual(768 - 72);
});

test("宽屏并排：头像拖高后详情栏顶边下移到浮层下方，高度仍按空档算", () => {
    const launcher = { top: 20, bottom: 260 };
    const layout = paneLauncherLayout(239, 900, 900, launcher);
    expect(layout.shift).toBe(260 + 12 - 239);
    expect(layout.paneMax).toBe(900 - 272);
    // 浮层在详情栏顶边上方、没有重叠的部分时不挪。
    expect(paneLauncherLayout(239, 900, 900, { top: 20, bottom: 200 }).shift).toBe(0);
});

test("视口矮到放不下 240px：降为实际可用高度，不出现负值或零高度", () => {
    for (const viewportHeight of [400, 360, 300, 200]) {
        const limit = narrowLimit(viewportHeight);
        for (const launcher of [null, { top: viewportHeight - 248, bottom: viewportHeight - 20 }, { top: 12, bottom: 248 }, { top: 80, bottom: viewportHeight - 80 }]) {
            const layout = paneLauncherLayout(72, limit, viewportHeight, launcher);
            const box = launcher ? narrow(layout, viewportHeight) : { top: 72, bottom: limit, height: limit - 72 };
            expect(layout.minHeight).toBe(Math.min(PANE_MIN_HEIGHT, Math.max(0, limit - 72)));
            expect(box.height).toBeGreaterThanOrEqual(layout.minHeight);
            expect(box.top).toBeGreaterThanOrEqual(72);
            expect(layout.shift).toBeGreaterThanOrEqual(0);
            expect(layout.paneMax).toBeGreaterThanOrEqual(layout.minHeight);
        }
    }
    // 顶边已经在可用范围外：最小高度为 0，不出负数。
    expect(paneLauncherLayout(900, 696, 768, { top: 12, bottom: 248 })).toEqual({ minHeight: 0, shift: 0, clear: 0, paneMax: 0 });
});

test("结果只取决于没让位时的顶边、可用范围和浮层，同样输入同样输出", () => {
    const launcher = { top: 12, bottom: 248 };
    expect(paneLauncherLayout(72, 696, 768, launcher)).toEqual(paneLauncherLayout(72, 696, 768, launcher));
});
