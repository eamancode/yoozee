import assert from "node:assert/strict";
import test from "node:test";
import type { DramaSegment, DramaSegmentView } from "@/services/api/drama";
import { segmentDurationRule } from "./chapter-segments";
import { failedSegmentEditHint, mergeWithNext, moveEdgeShot, splitSegment } from "./segment-edit";

const rule = segmentDurationRule(15);

const view = (id: string, refs: DramaSegment["refs"]): DramaSegmentView => ({
    shotId: `shot-${id}`,
    unitId: "u1",
    title: id,
    position: 0,
    durationMs: 6000,
    derived: false,
    spec: {
        id,
        scene: "E01S01",
        dur: 6,
        mode: "lock_identity",
        refs,
        shots: [
            { at: 0, size: "中景", text: "林小满推开七床的门" },
            { at: 3, size: "近景", text: "床上空了" },
        ],
    },
});

test("拆段时新段继承原段的资产引用，合段、挪镜头时接收的段并上来源段", () => {
    const list = [view("S01", [{ file: "lin.png", role: "人物" }]), view("S02", [{ file: "corridor.png", role: "场景" }])];
    const split = splitSegment(list, 0, 1, rule);
    assert.deepEqual(
        split.segments.map((segment) => segment.id),
        ["S01", "S01b"],
    );
    assert.deepEqual(split.order, ["S01", "S01b", "S02"]);
    assert.deepEqual(split.inherit, { S01b: ["S01"] });

    const merged = mergeWithNext(list, 0, rule);
    assert.deepEqual(merged.inherit, { S01: ["S02"] });
    assert.deepEqual(merged.removedShotIds, ["shot-S02"]);

    assert.deepEqual(moveEdgeShot(list, 1, "prev", rule).inherit, { S01: ["S02"] });
    assert.deepEqual(moveEdgeShot(list, 0, "next", rule).inherit, { S02: ["S01"] });
});

test("段编辑保存失败（如网关 502）时说清哪些段的改动还没写进去，改动保留可重试", () => {
    const list = [view("S01", [{ file: "lin.png", role: "人物" }]), view("S02", [{ file: "corridor.png", role: "场景" }])];
    const edit = moveEdgeShot(list, 1, "prev", rule);
    const hint = failedSegmentEditHint(edit, "后端服务暂时不可用，请稍后重试");
    assert.match(hint, /后端服务暂时不可用/);
    assert.match(hint, /S01、S02 的改动还没写进分段表/);
    assert.match(hint, /重试/);
});
