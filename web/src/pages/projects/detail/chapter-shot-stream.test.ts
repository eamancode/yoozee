import assert from "node:assert/strict";
import test from "node:test";
import { latestStoryboardWrite, parseShotStream, shotStreamFromResult, storyboardOutcomeSummary, storyboardRunningLabel, storyboardRunPartial, storyboardRunStop, streamGroupMeta, streamSceneGroups, streamTurnCount } from "./chapter-shot-stream";
import { projectSceneCode } from "./unit-numbering";

const marker = (index: number, attempt = 1) => JSON.stringify({ "@turn": { index, total: 2, scene: `E01S0${index}`, heading: index === 1 ? "内·病房走廊·夜" : "内·电梯口·夜", attempt } });
const shot = (n: number, plot: string) => JSON.stringify({ shotNumber: n, plotDescription: plot, dialogue: "", shotSize: "中景", spec: { id: `E01S01-0${n}` } });

test("parseShotStream 按场分组，只认已经写完的行", () => {
    const text = [marker(1), shot(1, "推门"), shot(2, "空床"), marker(2), shot(3, "电梯门")].join("\n") + "\n" + `{"shotNumber":4,"plotDe`;
    const state = parseShotStream(text);
    assert.equal(state.total, 2);
    assert.equal(state.current, 2);
    assert.equal(state.shotCount, 3);
    assert.deepEqual(
        state.scenes.map((scene) => [scene.scene, scene.heading, scene.shots.length]),
        [
            ["E01S01", "内·病房走廊·夜", 2],
            ["E01S02", "内·电梯口·夜", 1],
        ],
    );
    assert.equal(state.scenes[0].shots[1].plot, "空床");
    assert.equal(state.scenes[0].shots[0].size, "中景");
});

test("parseShotStream 同一场重试时清掉这一场的半截输出", () => {
    const text = [marker(1), shot(1, "推门"), marker(2), shot(2, "写到一半就断了"), marker(2, 2), shot(2, "电梯门"), shot(3, "周岸")].join("\n") + "\n";
    const state = parseShotStream(text);
    assert.equal(state.scenes.length, 2);
    assert.equal(state.scenes[1].attempt, 2);
    assert.deepEqual(
        state.scenes[1].shots.map((item) => item.plot),
        ["电梯门", "周岸"],
    );
});

test("shotStreamFromResult 从结束的任务还原：失败任务按各场原文，成功任务按 spec.scene 分场", () => {
    const failed = shotStreamFromResult(
        JSON.stringify({
            dramaRun: {
                total: 3,
                turns: [
                    { index: 1, scene: "E01S01", heading: "内·病房走廊·夜", text: shot(1, "推门") },
                    { index: 2, scene: "E01S02", heading: "内·电梯口·夜", text: `${shot(2, "电梯门")}\n${shot(3, "周岸")}` },
                ],
            },
        }),
    );
    assert.equal(failed.total, 3);
    assert.deepEqual(
        failed.scenes.map((scene) => scene.shots.length),
        [1, 2],
    );
    const row = (n: number, scene: string) => JSON.stringify({ shotNumber: n, plotDescription: `镜${n}`, spec: { scene } });
    const succeeded = shotStreamFromResult(
        JSON.stringify({
            text: [row(1, "E01S01"), row(2, "E01S01"), row(3, "E01S02")].join("\n") + "\n",
            dramaRun: {
                total: 2,
                scenes: [
                    { scene: "E01S01", heading: "内·病房走廊·夜" },
                    { scene: "E01S02", heading: "内·电梯口·夜" },
                ],
                turns: [],
            },
        }),
    );
    assert.deepEqual(
        succeeded.scenes.map((scene) => [scene.heading, scene.shots.length]),
        [
            ["内·病房走廊·夜", 2],
            ["内·电梯口·夜", 1],
        ],
    );
});

test("parseShotStream 没有回合标记时按镜头的 spec.scene 分场", () => {
    const row = (n: number, scene: string) => JSON.stringify({ shotNumber: n, plotDescription: `镜${n}`, spec: { scene } });
    const state = parseShotStream([row(1, "E01S01"), row(2, "E01S01"), row(3, "E01S02")].join("\n") + "\n");
    assert.deepEqual(
        state.scenes.map((scene) => [scene.index, scene.scene, scene.shots.length]),
        [
            [1, "E01S01", 2],
            [2, "E01S02", 1],
        ],
    );
});

test("parseShotStream 没有回合标记、坏行和代码块围栏也不报错", () => {
    const state = parseShotStream(["```json", shot(1, "推门"), "{坏行", "", "```"].join("\n") + "\n");
    assert.equal(state.scenes.length, 1);
    assert.equal(state.shotCount, 1);
});

test("storyboardRunStop 写完至少一场时从下一场继续", () => {
    const stop = storyboardRunStop({ id: "t1", status: "failed", error: "第 3 场生成失败：超时" }, { total: 5, completed: 2 });
    assert.deepEqual(stop, { kind: "resume", taskId: "t1", from: 3, total: 5, error: "第 3 场生成失败：超时" });
});

test("storyboardRunStop 第 1 场就失败、没有进度或收尾失败时只能重新生成，并带上失败原因", () => {
    assert.deepEqual(storyboardRunStop({ id: "t1", status: "failed", error: " 第 1 场（E01S01）生成失败：上游 502 " }, { total: 3, completed: 0 }), { kind: "failed", taskId: "t1", status: "failed", error: "第 1 场（E01S01）生成失败：上游 502" });
    assert.deepEqual(storyboardRunStop({ id: "t2", status: "failed", error: "模型没有输出" }), { kind: "failed", taskId: "t2", status: "failed", error: "模型没有输出" });
    assert.deepEqual(storyboardRunStop({ id: "t3", status: "failed", error: "镜头表写入失败" }, { total: 3, completed: 3 }), { kind: "failed", taskId: "t3", status: "failed", error: "镜头表写入失败" });
    assert.deepEqual(storyboardRunStop({ id: "t4", status: "cancelled", error: "" }, { total: 3, completed: 0 }), { kind: "failed", taskId: "t4", status: "cancelled", error: undefined });
});

test("storyboardRunStop 生成分段各节都写完、收尾失败时给重新写入（重跑也给），别的步骤仍只能重新生成", () => {
    const written = { createdAt: "2026-10-27T03:18:01Z", completedAt: "2026-10-27T03:27:46Z" };
    const failed = { id: "t5", status: "failed", error: "本章 300 个镜头超过一章 200 个镜头的上限", createdAt: "2026-10-28T03:00:00Z" };
    assert.deepEqual(storyboardRunStop(failed, { total: 60, completed: 60 }, written, { refinalize: true }), { kind: "refinalize", taskId: "t5", total: 60, status: "failed", error: "本章 300 个镜头超过一章 200 个镜头的上限" });
    assert.equal(storyboardRunStop(failed, { total: 60, completed: 59 }, undefined, { refinalize: true })?.kind, "resume");
    assert.equal(storyboardRunStop(failed, { total: 60, completed: 60 })?.kind, "failed");
    // 整章镜头超上限：重新写入必然再失败，不给，按普通失败（或重跑）说原因。任务详情的 diagnostic.code 与列表的 errorCode 都认。
    assert.equal(storyboardRunStop({ ...failed, errorCode: "shotlist_too_large" }, { total: 60, completed: 60 }, undefined, { refinalize: true })?.kind, "failed");
    assert.equal(storyboardRunStop({ ...failed, diagnostic: { code: "shotlist_too_large" } }, { total: 60, completed: 60 }, written, { refinalize: true })?.kind, "superseded");
});

test("storyboardRunStop 已有成功写入后取消或失败的重跑不给续写，说明当前结果来自哪次运行", () => {
    const written = { createdAt: "2026-10-27T03:18:01Z", completedAt: "2026-10-27T03:27:46Z" };
    const cancelledRerun = { id: "t2", status: "cancelled", error: "任务已取消", createdAt: "2026-10-27T03:44:43Z" };
    assert.deepEqual(storyboardRunStop(cancelledRerun, { total: 2, completed: 1 }, written), { kind: "superseded", taskId: "t2", status: "cancelled", error: "任务已取消", resultAt: "2026-10-27T03:27:46Z" });
    const failedRerun = { id: "t3", status: "failed", error: "第 2 场生成失败", createdAt: "2026-10-27T04:00:00Z" };
    assert.equal(storyboardRunStop(failedRerun, { total: 2, completed: 1 }, written)?.kind, "superseded");
    // 成功写入在停下的任务之后（列表顺序乱时）不算重跑：照常给续写。
    assert.equal(storyboardRunStop({ ...cancelledRerun, createdAt: "2026-10-27T03:00:00Z" }, { total: 2, completed: 1 }, written)?.kind, "resume");
    // 没有成功写入过：第一次运行中途停下，照常从下一场继续。
    assert.equal(storyboardRunStop(cancelledRerun, { total: 2, completed: 1 })?.kind, "resume");
});

test("storyboardRunStop 部分重跑 / 改戏类中途失败：已有写入也给「从第 N 节继续」（M8 验收 B 第 3、5 节失败）", () => {
    const written = { createdAt: "2026-10-30T01:42:00Z", completedAt: "2026-10-30T01:42:30Z" };
    const failed = { id: "f5", status: "failed", error: "第 3 节生成失败，已写完的 2 节已保存，可从第 3 节继续：画布 Agent 接口没有返回内容。", createdAt: "2026-10-30T02:10:00Z" };
    assert.deepEqual(storyboardRunStop(failed, { total: 4, completed: 2 }, written, { refinalize: true, partial: true }), { kind: "resume", taskId: "f5", from: 3, total: 4, error: failed.error, rerun: true });
    // 整章重跑照旧不给续写；部分重跑一节都没写完、或各节都写完只是收尾失败，照旧按失败 / 重新写入。
    assert.equal(storyboardRunStop(failed, { total: 4, completed: 2 }, written, { refinalize: true })?.kind, "superseded");
    assert.equal(storyboardRunStop(failed, { total: 4, completed: 4 }, written, { refinalize: true, partial: true })?.kind, "refinalize");
    assert.equal(storyboardRunStop(failed, { total: 4, completed: 0 }, written, { refinalize: true, partial: true })?.kind, "superseded");
});

test("storyboardRunPartial 认只重写几节的运行（任务 metadata.dramaRun）", () => {
    assert.equal(storyboardRunPartial(JSON.stringify({ metadata: { dramaRun: { runId: "r", onlySections: ["E01S02-P1", "E01S02-P2"] } } })), true);
    assert.equal(storyboardRunPartial(JSON.stringify({ metadata: { dramaRun: { runId: "r", onlyScenes: ["E01S02"] } } })), true);
    assert.equal(storyboardRunPartial(JSON.stringify({ metadata: { dramaRun: { runId: "r" }, dramaShots: { regenre: { from: "E01S02-P1", genre: "wuxi" } } } })), true);
    assert.equal(storyboardRunPartial(JSON.stringify({ metadata: { dramaRun: { runId: "r", onlySections: [] } } })), false);
    assert.equal(storyboardRunPartial("{"), false);
    assert.equal(storyboardRunPartial(undefined), false);
});

test("latestStoryboardWrite 取最近一次成功的生成分段任务", () => {
    const tasks = [
        { id: "a", status: "succeeded", createdAt: "2026-10-27T02:00:00Z" },
        { id: "b", status: "cancelled", createdAt: "2026-10-27T04:00:00Z" },
        { id: "c", status: "succeeded", createdAt: "2026-10-27T03:00:00Z" },
    ];
    assert.equal(latestStoryboardWrite(tasks)?.id, "c");
    assert.equal(latestStoryboardWrite([tasks[1]]), undefined);
});

test("storyboardRunStop 成功或还在跑的任务不算停下", () => {
    assert.equal(storyboardRunStop({ id: "t1", status: "succeeded" }, { total: 3, completed: 3 }), undefined);
    assert.equal(storyboardRunStop({ id: "t1", status: "running" }, { total: 3, completed: 1 }), undefined);
});

test("storyboardOutcomeSummary 段数按本章现在的段说，重新装段后不读任务结果里的旧数字", () => {
    const written = { status: "written" as const, shots: 41, scenes: 3, errors: 0 };
    assert.equal(storyboardOutcomeSummary(written, { segmentCount: 10, draft: false }), "已写入 41 镜，按 3 场装成 10 段");
    const draft = { status: "draft" as const, shots: 41, scenes: 3, errors: 1 };
    assert.equal(storyboardOutcomeSummary(draft, { segmentCount: 6, draft: true }), "已写入 41 镜，检查有 1 个问题，还没装段");
    assert.equal(storyboardOutcomeSummary(draft, { segmentCount: 6 }), "已写入 41 镜，检查有 1 个问题，还没装段");
    assert.equal(storyboardOutcomeSummary(draft, { segmentCount: 8, draft: false }), "已写入 41 镜，按 3 场装成 8 段");
});

test("parseShotStream 记下「重新生成问题场」要重写的场（回合标记的 only）", () => {
    const only = JSON.stringify({ "@turn": { index: 2, total: 3, scene: "E01S02", heading: "内·电梯口·夜", attempt: 1, only: ["E01S02", "E01S03"] } });
    const state = parseShotStream([only, shot(1, "电梯门")].join("\n") + "\n");
    assert.deepEqual(state.only, ["E01S02", "E01S03"]);
    assert.deepEqual(
        state.scenes.map((scene) => [scene.index, scene.scene, scene.shots.length]),
        [[2, "E01S02", 1]],
    );
    assert.equal(parseShotStream([marker(1), shot(1, "推门")].join("\n") + "\n").only, undefined);
});

test("按小节生成：标记带小节和戏类，原文只留最后几行预览，写完一节补的 rows 行按镜头显示", () => {
    const turn = (index: number, scene: string, section: string) => JSON.stringify({ "@turn": { index, total: 3, scene, heading: "内·病房走廊·夜", attempt: 1, section, genre: "文戏 15", est: 12 } });
    const rows = JSON.stringify({ rows: [{ shotNumber: 1, plotDescription: "推门", shotSize: "中景", spec: { id: "E01S01-01", scene: "E01S01", section: "E01S01-P1" } }] });
    const native = ["## 片段 1（时长：12秒）", "**要素提取** [角色：林小满；BGM背景音乐：钢琴]", "S1，0s-3s，中景。林小满推门。"];
    const state = parseShotStream([turn(1, "E01S01", "E01S01-P1"), ...native, rows, turn(2, "E01S01", "E01S01-P2"), "## 片段 2（时长：11秒）"].join("\n") + "\n");
    assert.equal(state.total, 3);
    assert.deepEqual(
        state.scenes.map((item) => [item.section, item.genre, item.est, item.shots.length]),
        [
            ["E01S01-P1", "文戏 15", 12, 1],
            ["E01S01-P2", "文戏 15", 12, 0],
        ],
    );
    assert.equal(state.scenes[1].raw, "## 片段 2（时长：11秒）");
    assert.ok(state.scenes[0].raw.includes("S1，0s-3s"));
    assert.equal(state.shotCount, 1);
    const groups = streamSceneGroups(state);
    assert.deepEqual(
        groups.map((group) => [group.scene, group.sections.length]),
        [["E01S01", 2]],
    );
});

test("shotStreamFromResult 按小节生成成功后按 spec.section 分组，失败时显示各节原文", () => {
    const scenes = [
        { scene: "E01S01", heading: "内·病房走廊·夜", section: "E01S01-P1", genre: "wenxi30", est: 20 },
        { scene: "E01S02", heading: "内·电梯口·夜", section: "E01S02-P1", genre: "wenxi30", est: 28 },
        { scene: "E01S02", heading: "内·电梯口·夜", section: "E01S02-P2", genre: "wenxi30", est: 26 },
    ];
    const row = (n: number, scene: string, section: string) => JSON.stringify({ shotNumber: n, plotDescription: `镜${n}`, spec: { scene, section } });
    const succeeded = shotStreamFromResult(
        JSON.stringify({ text: [row(1, "E01S01", "E01S01-P1"), row(2, "E01S02", "E01S02-P1"), row(3, "E01S02", "E01S02-P2"), row(4, "E01S02", "E01S02-P2")].join("\n") + "\n", dramaRun: { total: 3, genre: "wenxi30", scenes, turns: [] } }),
    );
    assert.deepEqual(
        succeeded.scenes.map((item) => [item.index, item.section, item.genre, item.shots.length]),
        [
            [1, "E01S01-P1", "文戏 30", 1],
            [2, "E01S02-P1", "文戏 30", 1],
            [3, "E01S02-P2", "文戏 30", 2],
        ],
    );
    const failed = shotStreamFromResult(JSON.stringify({ dramaRun: { total: 3, genre: "wenxi30", scenes, turns: [{ index: 1, scene: "E01S01", heading: "内·病房走廊·夜", section: "E01S01-P1", text: "小节1：七床空了\n镜头1（0-5秒）" }] } }));
    assert.equal(failed.scenes[0].section, "E01S01-P1");
    assert.equal(failed.scenes[0].shots.length, 0);
    assert.ok(failed.scenes[0].raw.includes("镜头1（0-5秒）"));
});

test("storyboardOutcomeSummary 按小节生成时说几节", () => {
    assert.equal(storyboardOutcomeSummary({ status: "written", shots: 19, scenes: 2, errors: 0, sections: 3 }, { segmentCount: 3 }), "已写入 19 镜，按 3 节装成 3 段");
});

test("改戏类任务：状态写「正在改为武戏 / 改回文戏」，不写「正在重写问题小节」，编号按项目编号，没转出镜头的节不说 0 镜", () => {
    const turn = (regenre?: string) =>
        JSON.stringify({ "@turn": { index: 2, total: 3, scene: "E01S01", heading: "内·护士站·夜", attempt: 1, section: "E01S01-P2", genre: "武戏", est: 23, only: ["E01S01-P2", "E01S01-P3"], ...(regenre ? { regenre } : {}) } });
    const code = (text: string) => projectSceneCode(text, 2);
    const wuxi = parseShotStream([turn("wuxi"), "【核心观看体验】以伤换位", "镜 1｜0-1s"].join("\n") + "\n");
    assert.equal(wuxi.regenre, "wuxi");
    assert.equal(storyboardRunningLabel(wuxi, code), "正在改为武戏 E02S01-P2（1/2）");
    const back = parseShotStream([turn("wenxi"), "## 片段 1（时长：12秒）"].join("\n") + "\n");
    assert.equal(storyboardRunningLabel(back, code), "正在改回文戏 E02S01-P2（1/2）");
    const problem = parseShotStream([turn(), "## 片段 1（时长：12秒）"].join("\n") + "\n");
    assert.equal(storyboardRunningLabel(problem), "正在重写问题小节 E01S01-P2（1/2）");
    assert.equal(streamTurnCount(wuxi.scenes[0], true), "写作中");
    assert.equal(streamTurnCount(wuxi.scenes[0], false), "");
    assert.equal(streamGroupMeta(wuxi.scenes), "1 节");
    const whole = parseShotStream(JSON.stringify({ "@turn": { index: 1, total: 2, scene: "E01S01", heading: "", attempt: 1, section: "E01S01-P1", genre: "武戏", regenre: "wuxi" } }) + "\n");
    assert.equal(storyboardRunningLabel(whole), "正在改为武戏：正在写第 1/2 节");
});

test("shotStreamFromResult 每一节标自己的戏类：改过戏类的章里武戏节不再显示成默认的文戏", () => {
    const row = (section: string, n: number) => JSON.stringify({ shotNumber: n, plotDescription: "格挡", shotSize: "中景", spec: { id: `E01S01-0${n}`, scene: "E01S01", section } });
    const resultJson = JSON.stringify({
        text: [row("E01S01-P1", 1), row("E01S01-P2", 2)].join("\n") + "\n",
        dramaRun: {
            total: 2,
            genre: "wenxi15",
            scenes: [
                { scene: "E01S01", heading: "内·护士站·夜", section: "E01S01-P1", genre: "wenxi15", est: 12 },
                { scene: "E01S01", heading: "内·护士站·夜", section: "E01S01-P2", genre: "wuxi", est: 8 },
            ],
            turns: [],
        },
    });
    const state = shotStreamFromResult(resultJson);
    assert.deepEqual(
        state.scenes.map((turn) => [turn.section, turn.genre, turn.shots.length]),
        [
            ["E01S01-P1", "文戏 15", 1],
            ["E01S01-P2", "武戏", 1],
        ],
    );
});
