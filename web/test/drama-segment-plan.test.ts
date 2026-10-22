import { describe, expect, test } from "bun:test";

import { videoDurationAllowed, type VideoCapabilityConfig } from "@/lib/model-capabilities";
import { formatDialogue, joinClauses, parseDialogue, segmentDurationRule, storyboardRowsToSegments } from "@/pages/projects/detail/chapter-segments";
import { editSubShot, mergeWithNext, moveEdgeShot, nextSegmentId, segmentLacksReferences, splitSegment, withSegmentRef } from "@/pages/projects/detail/segment-edit";
import type { DramaSegment, DramaSegmentView } from "@/services/api/drama";
import type { ProjectAsset, ProjectDetail } from "@/services/api/projects";
import type { StoryboardRow } from "@/types/canvas";

// SLL 上 MD 型渠道只能 15/30 秒；HAYA 型按秒、最少 5 秒。
const MD_DURATION: VideoCapabilityConfig["duration"] = { selection: "enum", values: [15, 30], default: 15 };
const HAYA_DURATION: VideoCapabilityConfig["duration"] = { selection: "range", min: 5, max: 15, step: 1, default: 5 };

const SEVEN_BED = [4, 5, 3, 4, 5, 4, 3, 3, 5, 5];

describe("章节分镜行装段", () => {
    test("两场戏不跨段，同一场的多段共用整场参考", () => {
        const rows = [row(1, 6, { scene: "ward", bind: ["linxiaoman"] }), row(2, 6, { scene: "ward" }), row(3, 6, {}), row(4, 6, { scene: "ward", bind: ["chart"] }), row(5, 4, { scene: "corridor" }), row(6, 4, {})];
        const plan = storyboardRowsToSegments(rows, detail(), { chapterTitle: "第一章", rule: segmentDurationRule(15) });
        const segments = specs(plan.shots);

        expect(segments.map((segment) => segment.scene)).toEqual(["七床病房", "七床病房", "医院走廊"]);
        // 走廊两镜只有 8 秒，也不并进病房的段。
        expect(segments.map((segment) => segment.shots.map((shot) => shot.text))).toEqual([
            ["镜头01", "镜头02"],
            ["镜头03", "镜头04"],
            ["镜头05", "镜头06"],
        ]);
        const wardRefs = ["version-linxiaoman", "version-ward", "version-chart"];
        expect(segments[0].refs?.map((ref) => ref.file)).toEqual(wardRefs);
        expect(segments[1].refs?.map((ref) => ref.file)).toEqual(wardRefs);
        expect(segments[0].refs?.map((ref) => ref.role)).toEqual(["identity", "location", "prop"]);
        expect(plan.shots[1].assetVersionIds).toEqual(wardRefs);
        expect(segments[2].refs?.map((ref) => ref.file)).toEqual(["version-corridor"]);
    });

    test("没有场景绑定时整章按一场，场景名用章节标题", () => {
        const plan = storyboardRowsToSegments([row(1, 5, {}), row(2, 5, {})], detail(), { chapterTitle: "第三章 夜班", rule: segmentDurationRule(15) });
        expect(specs(plan.shots).map((segment) => segment.scene)).toEqual(["第三章 夜班"]);
    });

    test("41 秒「七床」不在 07 和 08 的接话之间切，段长按目标 15–25 秒", () => {
        const plan = storyboardRowsToSegments(sevenBed(), detail(), { chapterTitle: "七床", rule: segmentDurationRule(30) });
        const segments = specs(plan.shots);

        expect(segments.map((segment) => segment.dur)).toEqual([25, 16]);
        expectTogether(segments, "镜头07", "镜头08");
        expect(plan.notes).toEqual([]);
    });

    test("MD 型渠道只能 15/30 秒：段长向上取档，接话不拆", () => {
        const rule = segmentDurationRule(30, MD_DURATION, "Seedance 2.5");
        const plan = storyboardRowsToSegments(sevenBed(), detail(), { chapterTitle: "七床", rule });
        const segments = specs(plan.shots);

        for (const segment of segments) expect(videoDurationAllowed(profile(MD_DURATION), segment.dur)).toBe(true);
        expect(contentSeconds(segments)).toBe(41);
        expectTogether(segments, "镜头07", "镜头08");
        expect(plan.notes.some((note) => note.includes("按渠道时长（15/30 秒）提交"))).toBe(true);
    });

    test("HAYA 型渠道最少 5 秒、最长 15 秒：每段都能提交，接话不拆", () => {
        const rule = segmentDurationRule(30, HAYA_DURATION, "Seedance 2.5");
        const plan = storyboardRowsToSegments(sevenBed(), detail(), { chapterTitle: "七床", rule });
        const segments = specs(plan.shots);

        for (const segment of segments) expect(videoDurationAllowed(profile(HAYA_DURATION), segment.dur)).toBe(true);
        expect(segments.every((segment) => segment.dur <= 15)).toBe(true);
        expect(contentSeconds(segments)).toBe(41);
        expectTogether(segments, "镜头07", "镜头08");
    });

    test("过短的尾段与相邻镜头合并，而不是单独出一段 3 秒", () => {
        const plan = storyboardRowsToSegments([row(1, 8, {}), row(2, 6, {}), row(3, 3, {})], detail(), { chapterTitle: "一", rule: segmentDurationRule(30, HAYA_DURATION) });
        expect(specs(plan.shots).map((segment) => segment.dur)).toEqual([8, 9]);
    });

    test("渠道档位全部超过项目模型上限时给出明确提示", () => {
        const rule = segmentDurationRule(15, { selection: "enum", values: [20, 30], default: 20 }, "Seedance 2.0");
        expect(rule.problem).toContain("只能生成 20/30 秒");
        const plan = storyboardRowsToSegments([row(1, 5, {})], detail(), { chapterTitle: "一", rule });
        expect(plan.notes[0]).toContain("这个渠道提交不了");
    });

    test("参考超过模型上限时截断并说明", () => {
        const plan = storyboardRowsToSegments([row(1, 5, { scene: "ward", bind: ["linxiaoman", "chart"] })], detail(), { chapterTitle: "一", rule: segmentDurationRule(15), maxRefs: 2 });
        expect(specs(plan.shots)[0].refs?.map((ref) => ref.file)).toEqual(["version-linxiaoman", "version-ward"]);
        expect(plan.notes[0]).toContain("只带前 2 个");
    });

    test("声音和结尾状态记在镜头上，不写段级 sound / rules", () => {
        const rows = [
            { ...row(1, 5, { scene: "corridor" }), audioEffects: "日光灯的嗡声。", continuityOut: "林小满走到七床门口" },
            { ...row(2, 5, { scene: "corridor" }), audioEffects: "门轴轻响", continuityOut: "林小满停在病床侧面，背对镜头。" },
        ];
        const [segment] = specs(storyboardRowsToSegments(rows, detail(), { chapterTitle: "一", rule: segmentDurationRule(15) }).shots);
        expect(segment.sound).toBeUndefined();
        expect(segment.rules).toBeUndefined();
        expect(segment.shots.map((shot) => [shot.sound, shot.end_state])).toEqual([
            ["日光灯的嗡声", "林小满走到七床门口"],
            ["门轴轻响", "林小满停在病床侧面，背对镜头。"],
        ]);
        // 拆段后各段只带自己镜头的声音和结尾状态（编译时段级 sound / rules 从镜头取）。
        const split = splitSegment([{ shotId: "shot-S01", unitId: "unit-1", title: "S01", position: 0, durationMs: 10_000, spec: segment, derived: false }], 0, 1, segmentDurationRule(15));
        expect(split.segments.map((item) => item.shots.map((shot) => shot.sound))).toEqual([["日光灯的嗡声"], ["门轴轻响"]]);
        expect(split.segments.map((item) => item.shots[item.shots.length - 1].end_state)).toEqual(["林小满走到七床门口", "林小满停在病床侧面，背对镜头。"]);
    });

    test("拼接运镜和画面时去掉重复标点", () => {
        const rows = [
            { ...row(1, 5, {}), plotDescription: "林小满推门。", performanceBlocking: "，手停在门把上。", camera: "固定机位。", motion: "缓慢推近。", shotSize: "中景。" },
            { ...row(2, 5, {}), plotDescription: "她愣住！", performanceBlocking: "眼神发紧" },
        ];
        const [segment] = specs(storyboardRowsToSegments(rows, detail(), { chapterTitle: "一", rule: segmentDurationRule(15) }).shots);
        expect(segment.shots[0]).toMatchObject({ size: "中景", camera: "固定机位，缓慢推近", text: "林小满推门。手停在门把上" });
        expect(segment.shots[1].text).toBe("她愣住！眼神发紧");
        expect(joinClauses(["病床空着。", "", "；被子叠好。。"], "；")).toBe("病床空着；被子叠好");
    });

    test("没有场景绑定而项目里有场景资产时提示整章按一场装段", () => {
        const plan = storyboardRowsToSegments([row(1, 5, {})], detail(), { chapterTitle: "一", rule: segmentDurationRule(15) });
        expect(plan.notes.join("；")).toContain("分镜行没有认出场景");
    });

    test("「无」不算台词，也不算接话", () => {
        expect(parseDialogue("（无）")).toEqual([]);
        expect(parseDialogue("林小满（画外）：七床的人呢？\n他走了")).toEqual([
            { speaker: "林小满", text: "七床的人呢？", vo: true },
            { speaker: "旁白", text: "他走了", vo: true },
        ]);
        expect(parseDialogue(formatDialogue(parseDialogue("林小满（画外）：七床的人呢？")))).toEqual([{ speaker: "林小满", text: "七床的人呢？", vo: true }]);
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

function sevenBed() {
    return SEVEN_BED.map((seconds, index) => {
        const number = index + 1;
        // 02 自说一句、07 问 08 答（接话）、10 收尾一句；其余无台词。
        const dialogue = number === 2 ? "林小满：几床？" : number === 7 ? "林小满：七床的人呢？" : number === 8 ? "护工：下午就走了。" : number === 10 ? "林小满（画外）：走了……" : "无";
        return row(number, seconds, { scene: "ward", dialogue });
    });
}

function expectTogether(segments: DramaSegment[], left: string, right: string) {
    const owner = (text: string) => segments.findIndex((segment) => segment.shots.some((shot) => shot.text === text));
    expect(owner(left)).toBeGreaterThanOrEqual(0);
    expect(owner(left)).toBe(owner(right));
}

/** 十个镜头一个不少、按顺序装进各段，每段提交的时长不短于它装的内容；返回内容总秒数。 */
function contentSeconds(segments: DramaSegment[]) {
    const texts = segments.flatMap((segment) => segment.shots.map((shot) => shot.text));
    expect(texts).toEqual(SEVEN_BED.map((_, index) => `镜头${String(index + 1).padStart(2, "0")}`));
    let total = 0;
    for (const segment of segments) {
        const content = segment.shots.reduce((sum, shot) => sum + SEVEN_BED[Number(shot.text.slice(2)) - 1], 0);
        expect(segment.dur).toBeGreaterThanOrEqual(content);
        total += content;
    }
    return total;
}

function specs(shots: ReturnType<typeof storyboardRowsToSegments>["shots"]) {
    return shots.map((shot) => shot.revision.spec as unknown as DramaSegment);
}

function profile(duration: VideoCapabilityConfig["duration"]) {
    return { duration } as VideoCapabilityConfig;
}

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

function row(number: number, seconds: number, options: { scene?: string; bind?: string[]; dialogue?: string }): StoryboardRow {
    const bindings = [
        ...(options.scene ? [{ nodeId: options.scene, role: "environment" as const, priority: 90 }] : []),
        ...(options.bind || []).map((nodeId) => ({ nodeId, role: nodeId === "chart" ? ("prop" as const) : ("character" as const), priority: 80 })),
    ];
    return {
        id: `row-${number}`,
        shotNumber: number,
        durationSeconds: seconds,
        plotDescription: `镜头${String(number).padStart(2, "0")}`,
        dialogue: options.dialogue || "",
        characters: [],
        narrativeIntent: "",
        viewerPOV: "",
        performanceBlocking: "",
        shotSize: "",
        emotion: "",
        lightingAndAtmosphere: "",
        audioEffects: "",
        camera: "",
        motion: "",
        timeBeats: "",
        imageGenerationPrompt: "",
        videoMotionPrompt: "",
        mustHave: [],
        optionalDetails: [],
        continuityOut: "",
        negativePrompt: "",
        assetBindings: bindings,
    };
}

function detail(): ProjectDetail {
    const asset = (id: string, title: string, category: ProjectAsset["category"]): ProjectAsset => ({ id, title, mediaType: "image", category, status: "ready", primaryVersionId: `version-${id}`, versionCount: 1, usages: [], position: 0, updatedAt: "" });
    return {
        assets: [asset("ward", "七床病房", "environment"), asset("corridor", "医院走廊", "environment"), asset("linxiaoman", "林小满", "character"), asset("chart", "病历夹", "prop")],
    } as ProjectDetail;
}
