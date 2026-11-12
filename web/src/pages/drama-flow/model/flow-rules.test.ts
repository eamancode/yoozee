import assert from "node:assert/strict";
import { describe, test } from "node:test";

import type { DramaPipeline, DramaPipelineStage, DramaPipelineStageKey, DramaSegmentProgress } from "@/services/api/project-drama";

import { buildInbox, inboxCounts } from "./inbox";
import { flowPrimaryAction, type FlowFacts } from "./primary-action";
import { defaultFlowStage, flowStageStates, isFlowStageKey } from "./stages";

const KEYS: DramaPipelineStageKey[] = ["script", "style", "palette", "assets", "shots", "pack", "video", "cut"];

function pipeline(doneUntil: number, extra: Partial<DramaPipeline> = {}, running?: DramaPipelineStageKey): DramaPipeline {
    const stages: DramaPipelineStage[] = KEYS.map((key, index) => ({ key, label: key, state: index < doneUntil ? "done" : key === running ? "running" : extra.stop?.stage === key ? "stopped" : "pending" }));
    return { runMode: "until_video", runModeLabel: "自动做到出视频前", stages, current: running, ...extra };
}

const facts = (input: Partial<FlowFacts>): FlowFacts => ({ segments: 0, generated: 0, inCut: 0, mustHandle: 0, toConfirm: 0, failedVideos: 0, runningVideos: 0, ...input });

const segment = (input: Partial<DramaSegmentProgress>): DramaSegmentProgress => ({
    shotId: "s1",
    unitId: "u1",
    segmentId: "E01-S01",
    title: "第一段",
    durationMs: 8000,
    overLimit: false,
    hasDialogue: false,
    script: true,
    refs: true,
    generated: false,
    reviewed: false,
    inCut: false,
    accepted: false,
    takes: 0,
    ...input,
});

describe("flow stages", () => {
    test("merges 8 pipeline cells into 5 steps", () => {
        const views = flowStageStates(pipeline(3, {}, "assets"));
        assert.deepEqual(
            views.map((stage) => stage.state),
            ["done", "running", "pending", "pending", "pending"],
        );
    });

    test("stop marks its step blocked and becomes the default step", () => {
        const stopped = pipeline(6, { stop: { stage: "video", label: "出视频", reason: "等你确认花费", action: "去制作台出视频", continuable: false } });
        assert.deepEqual(
            flowStageStates(stopped).map((stage) => stage.state),
            ["done", "done", "done", "blocked", "pending"],
        );
        assert.equal(defaultFlowStage(stopped), "video");
    });

    test("without pipeline nothing is guessed", () => {
        assert.equal(
            flowStageStates(undefined).every((stage) => stage.state === "pending"),
            true,
        );
        assert.equal(defaultFlowStage(undefined), "script");
        assert.equal(isFlowStageKey("look"), true);
        assert.equal(isFlowStageKey("pack"), false);
    });
});

describe("flow primary action", () => {
    test("must-handle items outrank everything", () => {
        const result = flowPrimaryAction(facts({ pipeline: pipeline(3, {}, "assets"), mustHandle: 2, failedVideos: 1 }));
        assert.equal(result.primary?.kind, "open_inbox");
        assert.equal(result.secondary?.kind, "retry_videos");
        // 视频重试不直接提交：按钮写明要先看花费，点了打开花费预览。
        assert.match(result.secondary?.label || "", /先看花费/);
    });

    test("running text step needs no button", () => {
        const result = flowPrimaryAction(facts({ pipeline: pipeline(1, {}, "style") }));
        assert.equal(result.tone, "working");
        assert.equal(result.primary, undefined);
    });

    test("running video only offers stop", () => {
        const result = flowPrimaryAction(facts({ pipeline: pipeline(6, {}, "video"), runningVideos: 2, segments: 10, generated: 3 }));
        assert.equal(result.primary, undefined);
        assert.equal(result.secondary?.kind, "stop_video");
    });

    test("video stop asks to start, or recharge when short", () => {
        const stopped = pipeline(6, { stop: { stage: "video", label: "出视频", reason: "等你确认花费", action: "去制作台出视频", continuable: false } });
        assert.deepEqual(flowPrimaryAction(facts({ pipeline: stopped, segments: 12 })).primary, { kind: "start_video", label: "开始出片" });
        assert.equal(flowPrimaryAction(facts({ pipeline: stopped, segments: 12, generated: 4 })).primary?.label, "继续出片");
        assert.equal(flowPrimaryAction(facts({ pipeline: stopped, segments: 12, balanceShort: true })).primary?.kind, "recharge");
    });

    test("continuable stop continues in one click, manual stop opens its step", () => {
        const confirm = pipeline(3, { stop: { stage: "assets", label: "角色场景资产", reason: "等你看过画风", action: "继续：角色场景资产", continuable: true } });
        assert.equal(flowPrimaryAction(facts({ pipeline: confirm })).primary?.kind, "continue");
        const manual = pipeline(4, { stop: { stage: "shots", label: "分镜", reason: "镜头表有问题", action: "去改镜头表", continuable: false } });
        assert.deepEqual(flowPrimaryAction(facts({ pipeline: manual })).primary, { kind: "go_stage", label: "去改镜头表", stage: "shots" });
    });

    test("cut stop: pick takes first, then export, then download", () => {
        const cut = pipeline(7, { stop: { stage: "cut", label: "剪辑", reason: "", action: "去交付", continuable: false } });
        assert.equal(flowPrimaryAction(facts({ pipeline: cut, toConfirm: 2 })).primary?.kind, "open_inbox");
        assert.deepEqual(flowPrimaryAction(facts({ pipeline: cut })).primary, { kind: "go_stage", label: "去成片", stage: "cut" });
        assert.equal(flowPrimaryAction(facts({ pipeline: cut, exported: true })).primary?.kind, "download");
    });
});

describe("flow inbox", () => {
    test("groups and orders items, video stop is not an inbox item", () => {
        const items = buildInbox({
            pipeline: pipeline(6, { stop: { stage: "video", label: "出视频", reason: "等你确认花费", action: "", continuable: false } }),
            assets: [
                { id: "a1", title: "林晚", kind: "asset", status: "failed", error: "渠道超时" },
                { id: "a2", title: "客厅", kind: "asset", status: "bound" },
            ],
            videoSteps: [
                { chainId: "c1", unitId: "u1", shotId: "s3", title: "第三段", status: "failed", error: "审核未过" },
                { chainId: "c0", unitId: "u1", shotId: "s3", title: "第三段", status: "failed" },
            ],
            segments: [segment({ shotId: "s1", refs: false }), segment({ shotId: "s2", overLimit: true }), segment({ shotId: "s4", generated: true, takes: 3 }), segment({ shotId: "s5", generated: true, takes: 2, selectedArtifactId: "x" })],
        });
        assert.deepEqual(
            items.map((item) => item.id),
            ["image:a1", "video:s3", "segment:s2", "pick:s4", "refs:s1"],
        );
        assert.deepEqual(inboxCounts(items), { must: 3, confirm: 1, suggest: 1 });
        assert.equal(items[0].action.kind, "retry_image");
        assert.deepEqual(items[1].action, { kind: "retry_video", label: "重新出（先看花费）" });
        assert.deepEqual(items[2].target, { unitId: "u1", itemId: "s2" });
    });

    test("continuable stop is a confirm item; ignored suggestions disappear", () => {
        const items = buildInbox({
            pipeline: pipeline(3, { stop: { stage: "assets", label: "角色场景资产", reason: "等你看过画风", action: "", continuable: true } }),
            segments: [segment({ shotId: "s1", refs: false })],
            ignored: ["refs:s1"],
        });
        assert.equal(items.length, 1);
        assert.deepEqual([items[0].id, items[0].group, items[0].stage, items[0].action.kind], ["stop:assets", "confirm", "look", "continue"]);
    });
});
