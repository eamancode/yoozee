import assert from "node:assert/strict";
import test from "node:test";

import type { DramaSegmentView, DramaShot } from "@/services/api/project-drama";

import { segmentDurationRule } from "./chapter-segments";
import { editShotCard, editSubShot, isDirectorSegment, mergeWithNext, moveEdgeShot, shotFixEdit, splitSegment } from "./segment-edit";

const rule = segmentDurationRule(15);

const view = (id: string, verbatim?: string): DramaSegmentView => ({
    shotId: `shot-${id}`,
    unitId: "u1",
    title: id,
    position: 0,
    durationMs: 10000,
    derived: false,
    spec: {
        id,
        scene: "E01S01",
        dur: 10,
        mode: "text_only",
        shots: [
            { at: 0, size: "中景", text: "阿婆把温橙汁递给林深", shot_id: `${id}-1` },
            { at: 5, size: "近景", text: "林深接过橙汁", shot_id: `${id}-2` },
        ],
        ...(verbatim ? { verbatim } : {}),
    },
});

const shot = (id: string): DramaShot => ({ id, scene: "E01S01", size: "中景", text: "阿婆把温橙汁递给林深", est: 5 }) as unknown as DramaShot;

// 第四轮复验 R4-P1-02：导演分镜段（SSS、本体论）的 verbatim 才是实际提交的提示词；普通改镜头、拆合、挪镜头、分镜卡编辑、
// 快捷修复都拒绝（报错说明要走返修），不产生只改了 shots 的段。
test("导演分镜段不能普通编辑或拆合，报错指向返修", () => {
    const director = view("S01", "10秒，雨夜。@图1把温橙汁递给@图2。");
    const plain = view("S02");
    const list = [director, plain];
    assert.equal(isDirectorSegment(director.spec), true);
    assert.equal(isDirectorSegment(plain.spec), false);
    assert.equal(isDirectorSegment({ verbatim: "   " }), false);
    const attempts: Array<[string, () => unknown]> = [
        ["edit shot text", () => editSubShot(list, 0, 0, { text: "手改画面", lines: [], seconds: 5 }, rule)],
        ["retime shot", () => editSubShot(list, 0, 0, { text: "阿婆把温橙汁递给林深", lines: [], seconds: 6 }, rule)],
        ["split", () => splitSegment(list, 0, 1, rule)],
        ["merge into next", () => mergeWithNext(list, 0, rule)],
        ["plain absorbs director (merge from previous)", () => mergeWithNext([plain, director], 0, rule)],
        ["move edge shot into director", () => moveEdgeShot(list, 1, "prev", rule)],
        ["move edge shot out of director", () => moveEdgeShot(list, 0, "next", rule)],
        ["shot card edit", () => editShotCard(list, 0, 0, shot("S01-1"), { ...shot("S01-1"), text: "改了" } as DramaShot, rule)],
        ["issue quick fix", () => shotFixEdit(list, 0, [shot("S01-1")])],
    ];
    for (const [name, run] of attempts) {
        assert.throws(run, /导演分镜单元.*症状返修/, name);
    }
    // 原流程段照常编辑，导演段原文不受影响。
    const edited = editSubShot([plain], 0, 0, { text: "手改画面", lines: [], seconds: 5 }, rule);
    assert.equal(edited.segments[0].shots[0].text, "手改画面");
    assert.equal(edited.segments[0].verbatim, undefined);
    assert.equal(splitSegment([plain], 0, 1, rule).segments.length, 2);
    assert.equal(director.spec.verbatim, "10秒，雨夜。@图1把温橙汁递给@图2。");
});
