import assert from "node:assert/strict";
import test from "node:test";

import type { ProjectShot, ShotArtifact } from "@/services/api/projects";

import { chapterSegmentDiscards, segmentDiscardLine } from "./segment-discard";

const shot = (id: string, title: string, position: number, unitId = "u1"): ProjectShot => ({ id, projectId: "p", unitId, title, description: "", position, durationMs: 0, status: "draft", createdAt: "", updatedAt: "" });
const artifact = (shotId: string, type: string, status = "ready"): ShotArtifact => ({ id: `${shotId}-${type}-${status}`, projectId: "p", unitId: "u1", shotId, type, version: 1, status, selected: true, metadataJson: "{}", createdAt: "", updatedAt: "" });

test("整章替换前按段列出会丢的产物：失败的不算、没有产物的段不列、按位置排", () => {
    const shots = [shot("b", "S02 雨夜", 1), shot("a", "S01 开门", 0), shot("c", "S03", 2), shot("x", "S01 别章", 0, "u2")];
    const artifacts = [artifact("b", "storyboard"), artifact("a", "storyboard"), artifact("a", "action_board"), artifact("a", "video", "stale"), artifact("c", "storyboard", "failed"), artifact("x", "storyboard")];
    const discards = chapterSegmentDiscards(shots, artifacts, "u1");
    assert.deepEqual(
        discards.map((d) => segmentDiscardLine(d)),
        ["S01：分镜图 1、预演 1、视频 1", "S02：分镜图 1"],
    );
});

test("服务端列出的段带小节时按项目编号显示", () => {
    const line = segmentDiscardLine({ shotId: "s", segment: "S04", section: "E01S02-P3", label: "S04", artifacts: { storyboard: 2 } }, (text) => text.replace("E01S", "E02S"));
    assert.equal(line, "S04（E02S02-P3）：分镜图 2");
});
