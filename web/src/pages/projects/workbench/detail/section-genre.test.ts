import assert from "node:assert/strict";
import test from "node:test";
import type { DramaSegmentView, DramaShotGenreId } from "@/services/api/project-drama";
import { sectionGenreConfirm, sectionGenreSpan, sectionGenreTarget } from "./section-genre";

const view = (id: string, scene: string, section: string, genre: DramaShotGenreId): DramaSegmentView => ({
    shotId: `shot-${id}`,
    unitId: "u1",
    title: id,
    position: 0,
    durationMs: 20000,
    derived: false,
    spec: { id, scene, dur: 20, mode: "text_only", shots: [{ at: 0, text: "画面" }], section, genre },
});

const list = [view("S01", "E01S01", "E01S01-P1", "wenxi30"), view("S02", "E01S02", "E01S02-P1", "wenxi30"), view("S03", "E01S02", "E01S02-P2", "wuxi"), view("S04", "E01S02", "E01S02-P3", "wuxi"), view("S05", "E01S03", "E01S03-P1", "wuxi")];

test("文戏节改为武戏，武戏节改回文戏", () => {
    assert.equal(sectionGenreTarget("wenxi30"), "wuxi");
    assert.equal(sectionGenreTarget("wenxi15"), "wuxi");
    assert.equal(sectionGenreTarget("wuxi"), "wenxi");
});

test("改为武戏只动这一节；改回文戏带上同一场相连的武戏节，不跨场", () => {
    assert.deepEqual(
        sectionGenreSpan(list, 1).map((v) => v.spec.id),
        ["S02"],
    );
    assert.deepEqual(
        sectionGenreSpan(list, 3).map((v) => v.spec.id),
        ["S03", "S04"],
    );
    assert.deepEqual(
        sectionGenreSpan(list, 2).map((v) => v.spec.id),
        ["S03", "S04"],
    );
    assert.deepEqual(
        sectionGenreSpan(list, 4).map((v) => v.spec.id),
        ["S05"],
    );
    assert.deepEqual(sectionGenreSpan([{ ...list[0], spec: { ...list[0].spec, section: undefined } }], 0), []);
});

test("确认框写明覆盖、重切口径和产物过期", () => {
    const toWuxi = sectionGenreConfirm(list, [list[1]], "wuxi", 30, () => 2);
    assert.equal(toWuxi.title, "把 E01S02-P1 改为武戏？");
    assert.match(toWuxi.lines[0], /按武戏口径重新切成 1～N 个武戏小节（N 秒 N 镜，每节不超过 30 秒）/);
    assert.match(toWuxi.lines[0], /直接覆盖现在的文戏版/);
    assert.match(toWuxi.lines[1], /段 S02 会换成武戏内容，它的分镜图、动作预演和视频会标成过期/);
    assert.ok(toWuxi.lines.some((line) => line.includes("已有 2 个生成结果（分镜图、预演或视频）")));
    const toWenxi = sectionGenreConfirm(list, sectionGenreSpan(list, 2), "wenxi", 15, () => 0);
    assert.equal(toWenxi.title, "把 E01S02-P2、E01S02-P3 改回文戏？");
    assert.match(toWenxi.lines[0], /一起改回文戏/);
    assert.ok(!toWenxi.lines.some((line) => line.includes("生成结果")));
    assert.equal(toWenxi.okText, "改回文戏并重写");
});

test("确认框按实际影响写同场其他段：可能因场景绑定变化而过期，不再说其余小节都不动", () => {
    const takes: Record<string, number> = { "shot-S03": 1 };
    const confirm = sectionGenreConfirm(list, [list[1]], "wuxi", 30, (id) => takes[id] || 0);
    const sameScene = confirm.lines.find((line) => line.includes("同一场"));
    assert.ok(sameScene, "同场有别的段时要列出来");
    assert.match(sameScene, /同一场 E01S02 的 S03、S04 不重写，但可能因场景绑定变化而过期/);
    assert.match(sameScene, /其中 S03 已有生成结果/);
    assert.match(sameScene, /其他场的段和产物不动/);
    assert.ok(!confirm.lines.some((line) => line.includes("其余小节的段和产物不动")));
    const alone = sectionGenreConfirm(list, [list[0]], "wuxi", 30, () => 0);
    assert.ok(alone.lines.includes("这一场没有别的段；其他场的段和产物不动。"));
});

test("确认框的小节、场按项目编号显示", () => {
    const confirm = sectionGenreConfirm(
        list,
        [list[1]],
        "wuxi",
        30,
        () => 0,
        (text) => text.replace("E01S", "E02S"),
    );
    assert.equal(confirm.title, "把 E02S02-P1 改为武戏？");
    assert.ok(confirm.lines.some((line) => line.includes("同一场 E02S02 的")));
});
