import assert from "node:assert/strict";
import test from "node:test";
import { parseShotStream, shotStreamFromResult } from "./chapter-shot-stream";

const marker = (index: number, attempt = 1) => JSON.stringify({ "@turn": { index, total: 2, scene: `E01S0${index}`, heading: index === 1 ? "内·病房走廊·夜" : "内·电梯口·夜", attempt } });
const shot = (n: number, plot: string) => JSON.stringify({ shotNumber: n, plotDescription: plot, dialogue: "", shotSize: "中景", spec: { id: `E01S01-0${n}` } });

test("parseShotStream 按场分组，只认已经写完的行", () => {
    const text = [marker(1), shot(1, "推门"), shot(2, "空床"), marker(2), shot(3, "电梯门")].join("\n") + "\n" + `{"shotNumber":4,"plotDe`;
    const state = parseShotStream(text);
    assert.equal(state.total, 2);
    assert.equal(state.current, 2);
    assert.equal(state.shotCount, 3);
    assert.deepEqual(state.scenes.map((scene) => [scene.scene, scene.heading, scene.shots.length]), [["E01S01", "内·病房走廊·夜", 2], ["E01S02", "内·电梯口·夜", 1]]);
    assert.equal(state.scenes[0].shots[1].plot, "空床");
    assert.equal(state.scenes[0].shots[0].size, "中景");
});

test("parseShotStream 同一场重试时清掉这一场的半截输出", () => {
    const text = [marker(1), shot(1, "推门"), marker(2), shot(2, "写到一半就断了"), marker(2, 2), shot(2, "电梯门"), shot(3, "周岸")].join("\n") + "\n";
    const state = parseShotStream(text);
    assert.equal(state.scenes.length, 2);
    assert.equal(state.scenes[1].attempt, 2);
    assert.deepEqual(state.scenes[1].shots.map((item) => item.plot), ["电梯门", "周岸"]);
});

test("shotStreamFromResult 从结束的任务还原：失败任务按各场原文，成功任务按 spec.scene 分场", () => {
    const failed = shotStreamFromResult(JSON.stringify({ dramaRun: { total: 3, turns: [{ index: 1, scene: "E01S01", heading: "内·病房走廊·夜", text: shot(1, "推门") }, { index: 2, scene: "E01S02", heading: "内·电梯口·夜", text: `${shot(2, "电梯门")}\n${shot(3, "周岸")}` }] } }));
    assert.equal(failed.total, 3);
    assert.deepEqual(failed.scenes.map((scene) => scene.shots.length), [1, 2]);
    const row = (n: number, scene: string) => JSON.stringify({ shotNumber: n, plotDescription: `镜${n}`, spec: { scene } });
    const succeeded = shotStreamFromResult(JSON.stringify({ text: [row(1, "E01S01"), row(2, "E01S01"), row(3, "E01S02")].join("\n") + "\n", dramaRun: { total: 2, scenes: [{ scene: "E01S01", heading: "内·病房走廊·夜" }, { scene: "E01S02", heading: "内·电梯口·夜" }], turns: [] } }));
    assert.deepEqual(succeeded.scenes.map((scene) => [scene.heading, scene.shots.length]), [["内·病房走廊·夜", 2], ["内·电梯口·夜", 1]]);
});

test("parseShotStream 没有回合标记、坏行和代码块围栏也不报错", () => {
    const state = parseShotStream(["```json", shot(1, "推门"), "{坏行", "", "```"].join("\n") + "\n");
    assert.equal(state.scenes.length, 1);
    assert.equal(state.shotCount, 1);
});
