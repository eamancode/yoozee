import assert from "node:assert/strict";
import test from "node:test";
import type { DramaSegment, DramaSegmentView } from "@/services/api/project-drama";
import { segmentDurationRule } from "./chapter-segments";
import { editShotCard, failedSegmentEditHint, mergeWithNext, moveEdgeShot, shotFixEdit, splitSegment } from "./segment-edit";

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

test("分镜卡编辑：时长没改时段不动，只带改过的镜头；改了时长按新时长重排段", () => {
    const base = view("S01", undefined);
    const list = [{ ...base, spec: { ...base.spec, shots: base.spec.shots.map((shot, index) => ({ ...shot, shot_id: `E01S01-0${index + 1}` })) } }];
    const before = { id: "E01S01-01", scene: "E01S01", action: "推门", est: 3 };
    const same = editShotCard(list, 0, 0, before, { ...before, action: "推开门" }, rule);
    assert.deepEqual(same.segments, [list[0].spec]);
    assert.deepEqual(same.shots, [{ ...before, action: "推开门" }]);
    const longer = editShotCard(list, 0, 0, before, { ...before, est: 5 }, rule);
    assert.deepEqual(
        longer.segments[0].shots.map((shot) => shot.at),
        [0, 5],
    );
    assert.ok(longer.segments[0].dur >= 8);
    assert.throws(() => editShotCard(list, 0, 1, before, before, rule), /对不上/);
    assert.throws(() => editShotCard(list, 0, 0, before, { ...before, est: 0 }, rule), /大于 0/);
    assert.deepEqual(shotFixEdit(list, 0, [before]).segments, [list[0].spec]);
    assert.throws(() => shotFixEdit(list, 0, []), /已经改过/);
});
