import assert from "node:assert/strict";
import test from "node:test";

import { unitKindOf, unitsOfKind } from "./unit-numbering";

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
