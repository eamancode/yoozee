import assert from "node:assert/strict";
import test from "node:test";

import { paletteCheckSummary } from "./project-palette-card";
import { parseStyleBibleStream, resumedStyleBibleStream, styleBibleStreamFromResult } from "./style-bible-stream";

const marker = (index: number, episode: number, attempt = 1) => JSON.stringify({ "@turn": { index, total: 2, scene: String(episode), heading: `第 ${episode} 集`, attempt } });
const anchor = JSON.stringify({ style_anchor: { medium: "真人实拍", forbidden: ["霓虹"] } });
const scene = (episode: number, no: number, inherits = "") =>
    JSON.stringify({
        episode,
        scene_no: no,
        scene: `## 内·场${no}·夜`,
        inherits_from: inherits,
        shared_colors: [{ name_zh: "水泥灰", name_en: "Concrete Grey", hex: "#6e6e6e", usage: "墙" }],
        new_colors: [{ name_zh: "护士蓝", name_en: "Nurse Blue", hex: "#5B7FA3", usage: "制服" }],
    });

test("parseStyleBibleStream 记下风格锚，按集分组每场的颜色", () => {
    const text = [marker(1, 1), anchor, JSON.stringify({ episode: 1, title: "七床空了" }), scene(1, 1), scene(1, 2, "E01S01"), marker(2, 3), JSON.stringify({ episode: 3 }), scene(3, 1), '{"episode": 3, "scene_no": 2, "scene": "半截'].join("\n");
    const state = parseStyleBibleStream(text);
    assert.equal(state.anchor?.medium, "真人实拍");
    assert.equal(state.total, 2);
    assert.equal(state.current, 2);
    assert.equal(state.episodes.length, 2);
    assert.equal(state.episodes[0].title, "七床空了");
    assert.equal(state.episodes[1].episode, 3);
    assert.equal(state.sceneCount, 3);
    const second = state.episodes[0].scenes[1];
    assert.equal(second.scene, "内·场2·夜");
    assert.equal(second.inherits_from, "E01S01");
    assert.equal(second.shared_colors[0].hex, "#6E6E6E");
});

test("同一集重试时清掉之前的半截输出；结果正文没有回合标记时按 JSON 头分集", () => {
    const retried = parseStyleBibleStream([marker(1, 1), anchor, scene(1, 1), marker(1, 1, 2), anchor, scene(1, 1), scene(1, 2)].join("\n"));
    assert.equal(retried.episodes.length, 1);
    assert.equal(retried.episodes[0].attempt, 2);
    assert.equal(retried.episodes[0].scenes.length, 2);
    const done = styleBibleStreamFromResult(JSON.stringify({ text: [anchor, JSON.stringify({ episode: 1 }), scene(1, 1), JSON.stringify({ episode: 2 }), scene(2, 1)].join("\n"), dramaRun: { total: 2, turns: [] } }));
    assert.equal(done.episodes.length, 2);
    assert.equal(done.total, 2);
    assert.equal(done.episodes[1].scenes[0].episode, 2);
});

test("续写时前面已写完的集接在新输出前面", () => {
    const previous = styleBibleStreamFromResult(JSON.stringify({ dramaRun: { total: 2, turns: [{ index: 1, scene: "1", heading: "第 1 集", text: [anchor, JSON.stringify({ episode: 1 }), scene(1, 1)].join("\n") }] } }));
    const live = parseStyleBibleStream("");
    const resumed = resumedStyleBibleStream(live, { from: 2, total: 2, previous });
    assert.equal(resumed.current, 2);
    assert.equal(resumed.episodes.length, 1);
    assert.equal(resumed.anchor?.medium, "真人实拍");
});

test("paletteCheckSummary 说清哪几块偏了", () => {
    assert.equal(paletteCheckSummary({ blocks: [{ hex: "#111111", sampled: "#111111", deltaE: 0 }], passed: true, threshold: 12 }), "取色校验通过（每块 ΔE ≤ 12）");
    assert.match(
        paletteCheckSummary({
            blocks: [
                { hex: "#111111", sampled: "#111111", deltaE: 0 },
                { hex: "#6E6E6E", sampled: "#2E7D32", deltaE: 30.12 },
            ],
            passed: false,
            threshold: 12,
        }),
        /第 2 块 #6E6E6E 取到 #2E7D32（ΔE 30\.1）/,
    );
    assert.equal(paletteCheckSummary({ blocks: [], passed: false, threshold: 12, note: "图片太小，无法取色" }), "取色校验没做：图片太小，无法取色");
});
