import { describe, expect, test } from "bun:test";

import type { VideoCapabilityConfig } from "@/lib/model-capabilities";
import { formatDialogue, parseDialogue, segmentDurationRule, segmentTargetLabel } from "@/pages/projects/workbench/detail/chapter-segments";
import { editSubShot, mergeWithNext, moveEdgeShot, nextSegmentId, segmentLacksReferences, splitSegment, withSegmentRef } from "@/pages/projects/workbench/detail/segment-edit";
import type { DramaSegment, DramaSegmentView } from "@/services/api/project-drama";

// SLL 上 MD 型渠道只能 15/30 秒；HAYA 型按秒、最少 5 秒。
const MD_DURATION: VideoCapabilityConfig["duration"] = { selection: "enum", values: [15, 30], default: 15 };

describe("台词解析", () => {
    test("「无」不算台词，也不算接话", () => {
        expect(parseDialogue("（无）")).toEqual([]);
        expect(parseDialogue("林小满（画外）：七床的人呢？\n他走了")).toEqual([
            { speaker: "林小满", text: "七床的人呢？", vo: true },
            { speaker: "旁白", text: "他走了", vo: true },
        ]);
        expect(parseDialogue(formatDialogue(parseDialogue("林小满（画外）：七床的人呢？")))).toEqual([{ speaker: "林小满", text: "七床的人呢？", vo: true }]);
    });
});

describe("生成分段对话框的段长目标", () => {
    test("按一段上限截断，上限不到目标下限时只说一个数", () => {
        expect(segmentTargetLabel(30)).toBe("15–25 秒");
        expect(segmentTargetLabel(20)).toBe("15–20 秒");
        expect(segmentTargetLabel(15)).toBe("15 秒");
        expect(segmentTargetLabel(10)).toBe("10 秒");
    });
});

describe("分镜制作页的段编辑", () => {
    const rule = segmentDurationRule(30, MD_DURATION);

    test("在镜头前拆段：新段紧跟原段，开始秒从 0 算，段长取档", () => {
        const list = [view("S01", [4, 5, 3]), view("S02", [4, 5, 4]), view("S03", [6])];
        const edit = splitSegment(list, 1, 2, rule);

        expect(edit.order).toEqual(["S01", "S02", "S02b", "S03"]);
        expect(edit.removedShotIds).toEqual([]);
        expect(edit.segments.map((segment) => [segment.id, segment.dur, segment.shots.map((shot) => shot.at)])).toEqual([
            ["S02", 15, [0, 4]],
            ["S02b", 15, [0]],
        ]);
    });

    test("与下一段合并：下一段删除，镜头接在后面，参考取并集；超过上限时拒绝", () => {
        const list = [view("S01", [4, 5], ["version-a"]), view("S02", [6], ["version-a", "version-b"])];
        const edit = mergeWithNext(list, 0, segmentDurationRule(15));

        expect(edit.order).toEqual(["S01"]);
        expect(edit.removedShotIds).toEqual(["shot-S02"]);
        expect(edit.segments[0].shots.map((shot) => shot.at)).toEqual([0, 4, 9]);
        expect(edit.segments[0].dur).toBe(15);
        expect(edit.segments[0].refs?.map((ref) => ref.file)).toEqual(["version-a", "version-b"]);
        expect(edit.segments[0].mode).toBe("lock_identity");
        expect(() => mergeWithNext([view("S01", [10]), view("S02", [8])], 0, segmentDurationRule(15))).toThrow("超过一段上限 15 秒");
    });

    test("首镜头移到上一段、尾镜头移到下一段", () => {
        const list = [view("S01", [4, 5]), view("S02", [3, 6, 2]), view("S03", [5])];
        const toPrev = moveEdgeShot(list, 1, "prev", segmentDurationRule(30));
        expect(toPrev.segments.map((segment) => [segment.id, segment.shots.map((shot) => shot.text)])).toEqual([
            ["S01", ["S01-1", "S01-2", "S02-1"]],
            ["S02", ["S02-2", "S02-3"]],
        ]);
        const toNext = moveEdgeShot(list, 1, "next", segmentDurationRule(30));
        expect(toNext.segments.map((segment) => [segment.id, segment.shots.map((shot) => shot.at)])).toEqual([
            ["S02", [0, 3]],
            ["S03", [0, 2]],
        ]);
        expect(() => moveEdgeShot(list, 2, "prev", rule)).toThrow("只剩一个镜头");
    });

    test("改镜头文字和台词不动段长，保留原有语气；改时长才重算", () => {
        const list = [view("S01", [4, 5])];
        list[0].spec.shots[0].lines = [{ speaker: "林小满", text: "七床的人呢？", delivery: "压低声音" }];
        const textOnly = editSubShot(list, 0, 0, { text: "护士推门", lines: parseDialogue("林小满：七床的人呢？\n护工：走了"), seconds: 4 }, rule);
        expect(textOnly.segments[0].dur).toBe(9);
        expect(textOnly.segments[0].shots[0].lines).toEqual([
            { speaker: "林小满", text: "七床的人呢？", delivery: "压低声音" },
            { speaker: "护工", text: "走了" },
        ]);
        const longer = editSubShot(list, 0, 0, { text: "护士推门", lines: [], seconds: 12 }, rule);
        expect(longer.segments[0].shots.map((shot) => shot.at)).toEqual([0, 12]);
        expect(longer.segments[0].dur).toBe(30);
        expect(longer.segments[0].shots[0].lines).toBeUndefined();
    });

    test("段没有人物/场景参考：纯文本、锁人物没挂参考算；有首帧的锁首帧段不算", () => {
        expect(segmentLacksReferences(view("S01", [4]).spec)).toBe(true);
        expect(segmentLacksReferences({ ...view("S01", [4]).spec, mode: "lock_identity" })).toBe(true);
        expect(segmentLacksReferences(view("S01", [4], ["version-a"]).spec)).toBe(false);
        expect(segmentLacksReferences({ ...view("S01", [4]).spec, mode: "lock_first_frame", first_frame: "version-frame" })).toBe(false);
    });

    test("镜头资产绑定变了，段的参考跟着变；没变化时不写", () => {
        const textOnly = view("S01", [4]).spec;
        const added = withSegmentRef(textOnly, { add: { file: "version-a", role: "identity", note: "林小满" } });
        expect(added?.mode).toBe("lock_identity");
        expect(added?.refs).toEqual([{ file: "version-a", role: "identity", note: "林小满" }]);
        expect(withSegmentRef(added!, { add: { file: "version-a", role: "identity" } })).toBeUndefined();
        const removed = withSegmentRef(added!, { removeFiles: ["version-a"] });
        expect(removed?.refs).toBeUndefined();
        expect(removed?.mode).toBe("text_only");
        expect(withSegmentRef(textOnly, { removeFiles: ["version-x"] })).toBeUndefined();
        expect(withSegmentRef({ ...textOnly, mode: "lock_first_frame", first_frame: "version-frame" }, { add: { file: "version-a", role: "identity" } })).toBeUndefined();
    });

    test("旧镜头（没有分段 JSON）不能参与拆合；段号依次往后取字母", () => {
        const list = [view("S01", [4]), { ...view("S02", [4]), derived: true }];
        expect(() => mergeWithNext(list, 0, rule)).toThrow("还没有分段 JSON");
        expect(nextSegmentId("S02", ["S02", "S02b"])).toBe("S02c");
        expect(nextSegmentId("S02b", ["S02", "S02b"])).toBe("S02c");
    });
});

function view(id: string, seconds: number[], refs: string[] = []): DramaSegmentView {
    let at = 0;
    const shots = seconds.map((value, index) => {
        const shot = { at, text: `${id}-${index + 1}` };
        at += value;
        return shot;
    });
    const spec: DramaSegment = { id, scene: "七床病房", dur: at, mode: refs.length ? "lock_identity" : "text_only", shots, ...(refs.length ? { refs: refs.map((file) => ({ file, role: "identity" })) } : {}) };
    return { shotId: `shot-${id}`, unitId: "unit-1", title: id, position: 0, durationMs: at * 1000, spec, derived: false };
}
