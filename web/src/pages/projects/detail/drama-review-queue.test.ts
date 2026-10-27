import assert from "node:assert/strict";
import test from "node:test";
import type { DramaSegmentProgress } from "@/services/api/drama";
import { pendingReviewSegments, reviewGap } from "./drama-review-queue";
import { briefPrevizGate, withPrevizGate } from "./drama-brief";

const row = (shotId: string, patch: Partial<DramaSegmentProgress>): DramaSegmentProgress => ({
    shotId, unitId: "u1", unitTitle: "第一集", segmentId: shotId, title: "段", durationMs: 8000, overLimit: false, hasDialogue: true,
    script: true, refs: true, generated: false, reviewed: false, inCut: false, accepted: false, takes: 0, ...patch,
});

test("待审列表只列已生成、没过审的段，并说清缺什么", () => {
    const segments = [
        row("S01", { generated: true, takes: 2 }),
        row("S02", { generated: true, takes: 1, selectedArtifactId: "a2", missingEvidence: ["faces", "台词转写"] }),
        row("S03", { generated: true, reviewed: true, selectedArtifactId: "a3" }),
        row("S04", {}),
    ];
    const pending = pendingReviewSegments(segments);
    assert.deepEqual(pending.map((item) => item.shotId), ["S01", "S02"]);
    assert.equal(reviewGap(pending[0]), "还没选定一条候选");
    assert.equal(reviewGap(pending[1]), "缺人脸核对、台词转写");
});

test("预演闸门读写需求文档：auto 时去掉这一项", () => {
    assert.equal(briefPrevizGate(undefined), "auto");
    assert.equal(briefPrevizGate({ previz_gate: "maybe" }), "auto");
    assert.deepEqual(withPrevizGate({ title: "x", previz_gate: "on" }, "auto"), { title: "x" });
    assert.deepEqual(withPrevizGate({ title: "x" }, "off"), { title: "x", previz_gate: "off" });
});
