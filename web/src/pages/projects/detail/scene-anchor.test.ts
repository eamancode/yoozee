import assert from "node:assert/strict";
import test from "node:test";
import type { DramaScene } from "@/services/api/drama";
import { sceneAnchor, sceneAnchorBlock, sceneLabel } from "./scene-anchor";

function scene(id: string, heading: string, location: string, times: string[], sources: Array<[string, number]>): DramaScene {
    return {
        id,
        shotScene: id,
        no: Number(id.slice(-2)),
        heading,
        location,
        times,
        sourceHeadings: sources.map(([text, line]) => ({ text, line })),
        lines: [sources[0]?.[1] || 1, sources[0]?.[1] || 1],
        lineCount: 1,
        legacyNos: [],
    };
}

// 七床第 1 集之后又回到走廊：走廊 → 电梯口（两个同名场次头并成一场）→ 走廊。
const scenes = [
    scene("E01S01", "内·病房走廊·夜", "病房走廊", ["夜"], [["内·病房走廊·夜", 1]]),
    scene(
        "E01S02",
        "内·电梯口·夜",
        "电梯口",
        ["夜"],
        [
            ["内·电梯口·夜", 12],
            ["内·电梯口·夜", 30],
        ],
    ),
    scene("E01S03", "内·病房走廊·夜", "病房走廊", ["夜"], [["内·病房走廊·夜", 43]]),
];

test("场次定位：同样文字的场次头按出现次序区分", () => {
    assert.deepEqual(sceneAnchor(scenes, 0), { text: "内·病房走廊·夜", occurrence: 0 });
    assert.deepEqual(sceneAnchor(scenes, 1), { text: "内·电梯口·夜", occurrence: 0 });
    assert.deepEqual(sceneAnchor(scenes, 2), { text: "内·病房走廊·夜", occurrence: 1 });
});

test("场次定位：在编辑器文本块里找到对应的场次头，「## 」和空白不影响", () => {
    const blocks = ["## 内·病房走廊·夜", "（走廊。）", "内·电梯口 ·夜", "（门开。）", "内·电梯口·夜", "（打。）", "内·病房走廊·夜", "（回来。）"];
    assert.equal(sceneAnchorBlock(blocks, sceneAnchor(scenes, 1)!), 2);
    assert.equal(sceneAnchorBlock(blocks, sceneAnchor(scenes, 2)!), 6);
    assert.equal(sceneAnchorBlock(["别的"], sceneAnchor(scenes, 0)!), -1);
});

test("场次定位：没有场次头的场（未命名场）没有定位", () => {
    assert.equal(sceneAnchor([scene("E01S01", "未命名场", "", [], [])], 0), undefined);
});

test("场次标签写地点和时间变化", () => {
    assert.equal(sceneLabel(scene("E01S02", "内·电梯口·夜→晨", "电梯口", ["夜", "晨"], [["内·电梯口·夜", 1]])), "电梯口 · 夜→晨");
    assert.equal(sceneLabel(scene("E01S03", "第3场", "", [], [["第3场", 9]])), "第3场");
    assert.equal(sceneLabel({ ...scene("E01S04", "外·医院门口·夜", "医院门口", ["夜"], [["外·医院门口·夜", 12]]), inOut: "外" }), "外·医院门口 · 夜");
});
