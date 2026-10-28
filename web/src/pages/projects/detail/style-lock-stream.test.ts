import assert from "node:assert/strict";
import test from "node:test";

import { masterPaletteGroupKey, parseStyleLockStream } from "./style-lock-stream";

const marker = (attempt: number) => JSON.stringify({ "@turn": { index: 1, total: 1, attempt } });
const anchor = JSON.stringify({ style_anchor: { medium: "真人实拍", forbidden: ["霓虹"] } });
const group = (name: string, ...hexes: string[]) => JSON.stringify({ group: name, colors: hexes.map((hex, index) => ({ name_zh: `色${index + 1}`, name_en: `Color ${index + 1}`, hex, usage: "用途" })) });

test("parseStyleLockStream 记下风格锚，按固定分组顺序合并色板", () => {
    const text = [marker(1), anchor, group("环境组", "#e8ecef"), group("主角", "5b7fa3", "#E3D3C4"), group("主角组", "#1B1B1F"), group("道具组", "#7A3B2E"), '{"group": "配角组", "colors": [{"hex": "#8DA'].join("\n");
    const state = parseStyleLockStream(text);
    assert.equal(state.anchor?.medium, "真人实拍");
    assert.deepEqual(
        state.palette.groups.map((item) => [item.key, item.name, item.colors.length]),
        [
            ["protagonist", "主角组", 3],
            ["props", "道具组", 1],
            ["environment", "环境组", 1],
        ],
    );
    assert.equal(state.palette.groups[0].colors[0].hex, "#5B7FA3");
    assert.equal(state.palette.groups[2].colors[0].hex, "#E8ECEF");
    assert.equal(state.colorCount, 5);
});

test("parseStyleLockStream 重试时清掉上一次的半截输出，整张色板一行也认", () => {
    const whole = JSON.stringify({ master_palette: { groups: [{ group: "配角组", colors: [{ name_zh: "黑衣", name_en: "Night Coat", hex: "#26262B", usage: "黑衣人" }] }] } });
    const state = parseStyleLockStream([marker(1), anchor, group("主角组", "#111111"), marker(2), whole].join("\n"));
    assert.equal(state.attempt, 2);
    assert.equal(state.anchor, undefined);
    assert.deepEqual(
        state.palette.groups.map((item) => item.key),
        ["supporting"],
    );
});

test("masterPaletteGroupKey 认中英文组名", () => {
    assert.equal(masterPaletteGroupKey("Protagonists"), "protagonist");
    assert.equal(masterPaletteGroupKey("场景"), "environment");
    assert.equal(masterPaletteGroupKey("服装"), "other");
});
