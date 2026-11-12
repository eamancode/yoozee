import assert from "node:assert/strict";
import { describe, test } from "node:test";

import type { DramaSegmentProgress } from "@/services/api/project-drama";
import type { GenerationTask } from "@/services/api/task-center";

import { costSummary, costSummaryText, creditsText, shotCosts } from "./costs";
import { combineReadStates, queryReadState, readValue, videoStateRead } from "./read-state";
import { durationText, runRow, runStepLabel } from "./runs";
import { episodeCompletion, episodeFacts, episodeFactsLine, stageFacts } from "./stage-facts";
import type { ShotVideoStep } from "./video-steps";

const segment = (input: Partial<DramaSegmentProgress>): DramaSegmentProgress => ({
    shotId: "s1",
    unitId: "u1",
    segmentId: "E01-S01",
    title: "",
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
const step = (shotId: string, state: ShotVideoStep["state"]) => [shotId, { chainId: "c", unitId: "u1", shotId, title: "", state, chainActive: false, step: {} as ShotVideoStep["step"] }] as const;
const task = (input: Partial<GenerationTask>): GenerationTask => ({ id: "t", type: "canvas_video", status: "succeeded", prompt: "", attempts: 1, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", ...input });

describe("stage facts", () => {
    test("writes only what was read", () => {
        assert.deepEqual(stageFacts({}), { script: "", look: "", shots: "", video: "", cut: "" });
        const facts = stageFacts({
            episodes: 3,
            style: "locked",
            palettes: { ready: 4, total: 6 },
            assets: { bound: 9, total: 12, failed: 1 },
            counts: { segments: 12, generated: 8, inCut: 6, overLimit: 1, issues: 2 },
            videos: { failed: 1, running: 2 },
        });
        assert.deepEqual(facts, { script: "3 集", look: "画风已定 · 色卡 4/6 · 资产 9/12 · 失败 1", shots: "12 段 · 超时长 1 · 问题 2", video: "已出 8/12 · 在出 2 · 失败 1", cut: "入片 6/12" });
    });

    test("画风：只有风格锚不算定了；没读到不写", () => {
        assert.equal(stageFacts({ style: "none" }).look, "画风未定");
        assert.equal(stageFacts({ style: "anchor_only" }).look, "画风待锁定");
        assert.equal(stageFacts({ style: undefined }).look, "");
    });

    test("episode facts count only that episode", () => {
        const segments = [segment({ shotId: "a", generated: true }), segment({ shotId: "b", issues: 1 }), segment({ shotId: "c" }), segment({ shotId: "d", unitId: "u2", generated: true })];
        const steps = new Map([step("b", "failed"), step("c", "running"), step("d", "failed")]);
        const facts = episodeFacts("u1", segments, steps);
        assert.deepEqual(facts, { segments: 3, generated: 1, failed: 1, running: 1, issues: 1 });
        assert.equal(episodeFactsLine(facts), "3 段 · 出 1 · 在出 1 · 败 1");
        assert.equal(episodeFactsLine(episodeFacts("u3", segments, steps)), "还没分镜");
        assert.equal(episodeCompletion(episodeFacts("u3", segments, steps)), undefined);
    });
});

describe("costs", () => {
    const settled = task({ id: "1", clientContext: { shotId: "s1" }, billing: { amountMicrocredits: 5_000_000, actualAmountMicrocredits: 4_000_000, status: "settled" } });
    const refunded = task({ id: "2", clientContext: { shotId: "s1" }, billing: { amountMicrocredits: 3_000_000, status: "refunded" } });
    const reserved = task({ id: "3", clientContext: { shotId: "s2" }, billing: { amountMicrocredits: 2_000_000, status: "reserved" } });
    const unbilled = task({ id: "4", clientContext: { shotId: "s3" } });

    test("结算取实扣、退款算 0、预授权取预授权；没有计费单的单独计数", () => {
        assert.deepEqual(costSummary([settled, refunded, reserved, unbilled]), { runs: 4, billed: 3, unbilled: 1, total: 6_000_000 });
        assert.deepEqual(
            [...shotCosts([settled, refunded, reserved, unbilled])],
            [
                ["s1", { runs: 2, billed: 2, unbilled: 0, total: 4_000_000 }],
                ["s2", { runs: 1, billed: 1, unbilled: 0, total: 2_000_000 }],
                ["s3", { runs: 1, billed: 0, unbilled: 1, total: 0 }],
            ],
        );
        assert.equal(creditsText(undefined), "没读到");
        assert.equal(creditsText(6_000_000), "6 积分");
    });

    test("花费文案：读取状态优先，读到的空列表才是 0，缺计费单时写明不完整", () => {
        assert.equal(costSummaryText("loading", costSummary([])), "…");
        assert.equal(costSummaryText("error", costSummary([settled])), "没读到");
        assert.equal(costSummaryText("ready", costSummary([])), "0 积分 · 暂无消费");
        assert.equal(costSummaryText("ready", costSummary([settled, reserved])), "6 积分");
        assert.equal(costSummaryText("ready", costSummary([refunded])), "0 积分");
        assert.equal(costSummaryText("ready", costSummary([unbilled, task({ id: "5" })])), "2 次运行都没有计费记录");
        assert.equal(costSummaryText("ready", costSummary([settled, refunded, unbilled])), "已知 4 积分，另有 1 次没有计费记录（合计不完整）");
    });
});

describe("runs", () => {
    test("labels steps and computes duration", () => {
        assert.equal(runStepLabel({ type: "canvas_text", clientContext: { chapterOperation: "storyboard" } }), "生成分镜");
        assert.equal(runStepLabel({ type: "project_video_chain" }), "连续出片");
        const row = runRow(task({ status: "failed", error: "上游超时", startedAt: "2026-01-01T00:00:00Z", completedAt: "2026-01-01T00:01:05Z" }), () => "第 1 集");
        assert.deepEqual([row.step, row.scope, row.statusLabel, row.seconds, row.error, row.active], ["出视频", "第 1 集", "失败", 65, "上游超时", false]);
        assert.equal(durationText(65), "1 分 5 秒");
        assert.equal(durationText(undefined), "");
    });
});

describe("summary batcher", () => {
    test("collects ids into batches of 50 and rejects missing ones", async () => {
        const { createSummaryBatcher } = await import("./summary-batcher");
        const calls: string[][] = [];
        const load = createSummaryBatcher<number>(async (ids) => {
            calls.push(ids);
            return { summaries: Object.fromEntries(ids.filter((id) => id !== "gone").map((id) => [id, Number(id.slice(1))])), missing: ids.includes("gone") ? ["gone"] : [] };
        }, 5);
        const ids = Array.from({ length: 51 }, (_, index) => `p${index}`);
        const results = await Promise.all([...ids, "p0"].map(load));
        assert.deepEqual(
            calls.map((call) => call.length),
            [50, 1],
        );
        assert.equal(results[50], 50);
        assert.equal(results[51], 0);
        await assert.rejects(load("gone"), /没读到/);
    });
});

describe("读取状态", () => {
    test("单个查询：失败（含带旧缓存的刷新失败）算没读到，没数据算还在读", () => {
        assert.equal(queryReadState({ isError: true, data: { segments: [] } }), "error");
        assert.equal(queryReadState({ isError: false, data: undefined }), "loading");
        assert.equal(queryReadState({ isError: false, data: [] }), "ready");
    });

    test("出片状态：进度没读到时链读到也不算；进度读到、这一集没有段时不用读链", () => {
        assert.equal(videoStateRead("error", "ready"), "error");
        assert.equal(videoStateRead("loading", undefined), "loading");
        assert.equal(videoStateRead("ready", undefined), "ready");
        assert.equal(videoStateRead("ready", "loading"), "loading");
        assert.equal(videoStateRead("ready", "error"), "error");
    });

    test("合并：有一个没读到就是没读到，没有失败但有在读的就是在读", () => {
        assert.equal(combineReadStates(["ready", "loading", "error"]), "error");
        assert.equal(combineReadStates(["ready", "loading"]), "loading");
        assert.equal(combineReadStates([]), "ready");
    });

    test("数值：没读到不写 0", () => {
        assert.equal(readValue("ready", 0), 0);
        assert.equal(readValue("loading", 0), "…");
        assert.equal(readValue("error", 0), "没读到");
    });

    test("集列表：出片链没读到时不写在出、败，写读取状态", () => {
        const facts = { segments: 4, generated: 1, failed: 0, running: 0, issues: 0 };
        assert.equal(episodeFactsLine(facts, "error"), "4 段 · 出 1 · 出片状态没读到");
        assert.equal(episodeFactsLine(facts, "loading"), "4 段 · 出 1 · 读出片状态…");
        assert.equal(episodeFactsLine({ ...facts, failed: 2 }), "4 段 · 出 1 · 败 2");
    });
});
