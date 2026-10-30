import assert from "node:assert/strict";
import test from "node:test";

import { projectSceneCode, unitEpisodeNumber, unitKindOf, unitsOfKind } from "./unit-numbering";

test("章和集各自按 position 编号，不混排", () => {
    const units = [
        { id: "e2", kind: "episode", position: 4 },
        { id: "c1", kind: "chapter", position: 0 },
        { id: "e1", kind: "episode", position: 3 },
        { id: "c2", kind: "", position: 1 },
    ];
    const episodes = unitsOfKind(units, "episode");
    assert.deepEqual(
        episodes.units.map((unit) => unit.id),
        ["e1", "e2"],
    );
    assert.equal(episodes.noun, "集");
    const chapters = unitsOfKind(units, "chapter");
    assert.deepEqual(
        chapters.units.map((unit) => unit.id),
        ["c1", "c2"],
    );
    assert.equal(chapters.label, "章节");
    assert.equal(unitKindOf({ kind: "" }), "chapter");
});

test("集号与后端场次表同口径：有剧集时剧集从 1 数、原文章节为 0；没有剧集时原文章节从 1 数", () => {
    const units = [
        { id: "c1", kind: "chapter", position: 0 },
        { id: "e2", kind: "episode", position: 5 },
        { id: "e1", kind: "episode", position: 3 },
    ];
    assert.equal(unitEpisodeNumber(units, "e1"), 1);
    assert.equal(unitEpisodeNumber(units, "e2"), 2);
    assert.equal(unitEpisodeNumber(units, "c1"), 0);
    const chapters = [
        { id: "c2", kind: "chapter", position: 2 },
        { id: "c1", kind: "", position: 1 },
    ];
    assert.equal(unitEpisodeNumber(chapters, "c2"), 2);
    assert.equal(unitEpisodeNumber(chapters, "missing"), 0);
});

test("第 2 集起单元内编号显示成项目编号：场、小节、镜头、台词都换，第 1 集和别的编号不动", () => {
    assert.equal(projectSceneCode("E01S01-P1", 2), "E02S01-P1");
    assert.equal(projectSceneCode("E01S03", 12), "E12S03");
    assert.equal(projectSceneCode("E01S105-02", 2), "E02S105-02");
    assert.equal(projectSceneCode("台词 E01S01L05 没落在任何镜头（E01S01-03 前一句）", 2), "台词 E02S01L05 没落在任何镜头（E02S01-03 前一句）");
    assert.equal(projectSceneCode("E01S01-P1", 1), "E01S01-P1");
    assert.equal(projectSceneCode("E01S01-P1", 0), "E01S01-P1");
    assert.equal(projectSceneCode("本场色卡（E02S01）", 2), "本场色卡（E02S01）");
    assert.equal(projectSceneCode("XE01S01", 2), "XE01S01");
});
