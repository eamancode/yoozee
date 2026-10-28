import assert from "node:assert/strict";
import test from "node:test";

import { parseScenePaletteStream, resumedScenePaletteStream, scenePaletteStreamFromResult } from "./scene-palette-stream";

const marker = (index: number, episode: number, attempt = 1) => JSON.stringify({ "@turn": { index, total: 2, scene: String(episode), heading: `第 ${episode} 集`, attempt } });
const anchor = JSON.stringify({ style_anchor: { medium: "真人实拍", forbidden: ["霓虹"] } });
const scene = (episode: number, no: number, inherits = "") =>
    JSON.stringify({
        scene: `E${String(episode).padStart(2, "0")}S${String(no).padStart(2, "0")}`,
        inherits_from: inherits,
        shared_colors: [{ name_zh: "水泥灰", name_en: "Concrete Grey", hex: "#6e6e6e", usage: "墙", group: "environment" }],
        new_colors: [{ name_zh: "护士蓝", name_en: "Nurse Blue", hex: "#5B7FA3", usage: "制服", group: "protagonist" }],
    });

test("parseScenePaletteStream 按集分组每场的颜色，场编号读出集号场序，风格锚行跳过", () => {
    const text = [marker(1, 1), anchor, scene(1, 1), scene(1, 2, "e01s01"), marker(2, 3), scene(3, 1), '{"scene": "E03S02", "shared_colors": [半截'].join("\n");
    const state = parseScenePaletteStream(text);
    assert.equal(state.total, 2);
    assert.equal(state.current, 2);
    assert.equal(state.episodes.length, 2);
    assert.equal(state.episodes[0].title, "第 1 集");
    assert.equal(state.episodes[1].episode, 3);
    assert.equal(state.sceneCount, 3);
    const second = state.episodes[0].scenes[1];
    assert.equal(second.episode, 1);
    assert.equal(second.scene_no, 2);
    assert.equal(second.inherits_from, "E01S01");
    assert.equal(second.shared_colors[0].hex, "#6E6E6E");
    assert.equal(second.new_colors[0].group, "protagonist");
});

test("同一集重试时清掉之前的半截输出；结果正文没有回合标记时按场编号的集号分集", () => {
    const retried = parseScenePaletteStream([marker(1, 1), scene(1, 1), marker(1, 1, 2), scene(1, 1), scene(1, 2)].join("\n"));
    assert.equal(retried.episodes.length, 1);
    assert.equal(retried.episodes[0].attempt, 2);
    assert.equal(retried.episodes[0].scenes.length, 2);
    const done = scenePaletteStreamFromResult(JSON.stringify({ text: [scene(1, 1), scene(1, 2), scene(2, 1)].join("\n"), dramaRun: { total: 2, turns: [] } }));
    assert.equal(done.episodes.length, 2);
    assert.equal(done.total, 2);
    assert.equal(done.episodes[1].scenes[0].episode, 2);
    // 旧写法：JSON 头 + scene_no。
    const legacy = parseScenePaletteStream([JSON.stringify({ episode: 2 }), JSON.stringify({ episode: 2, scene_no: 1, scene: "## 内·电梯里·夜", shared_colors: [], new_colors: [] })].join("\n"));
    assert.equal(legacy.episodes[0].scenes[0].scene, "内·电梯里·夜");
    assert.equal(legacy.episodes[0].episode, 2);
});

test("续写时前面已写完的集接在新输出前面", () => {
    const previous = scenePaletteStreamFromResult(JSON.stringify({ dramaRun: { total: 2, turns: [{ index: 1, scene: "1", heading: "第 1 集", text: scene(1, 1) }] } }));
    const live = parseScenePaletteStream("");
    const resumed = resumedScenePaletteStream(live, { from: 2, total: 2, previous });
    assert.equal(resumed.current, 2);
    assert.equal(resumed.episodes.length, 1);
    assert.equal(resumed.sceneCount, 1);
});
