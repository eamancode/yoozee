import assert from "node:assert/strict";
import { describe, test } from "node:test";

import type { GenerationTask, TaskDramaRounds } from "@/services/api/task-center";

import { directorPhaseText, directorRoundsText, directorRunView } from "./director-run";
import { runRow } from "./runs";

const created = "2026-11-15T08:00:00.000Z";
const at = (seconds: number) => Date.parse(created) + seconds * 1000;
const turns = (...kinds: string[]) => kinds.map((kind, i) => ({ kind, heading: `${kind}-${i + 1}` }));
const task = (rounds: TaskDramaRounds | undefined, status: GenerationTask["status"] = "running", extra: Partial<GenerationTask> = {}): GenerationTask =>
    ({ id: "t1", type: "canvas_text", status, prompt: "", attempts: 1, createdAt: created, clientContext: { chapterId: "u1", chapterOperation: "storyboard" }, dramaRounds: rounds, ...extra }) as GenerationTask;

describe("directorRunView：SSS 导演分镜的轮次进度", () => {
    test("切分前：总轮数是预估，下一轮是切分，阶段里切分正在写", () => {
        const view = directorRunView(task({ mode: "sss", completed: 2, total: 10, turns: turns("plan", "direct") }), { now: at(95) });
        assert.ok(view);
        assert.equal(view.estimated, true);
        assert.deepEqual(view.current, { index: 3, label: "整场镜头表与单元总表" });
        assert.equal(view.waitedSeconds, 95);
        assert.equal(directorRoundsText(view), "已保存 2/10 轮（预估总轮数，切分出单元后改成实际）");
        assert.deepEqual(view.phases.map(directorPhaseText), ["集设计 ✓", "讲戏 ✓", "切分（正在写）", "单元卡 0/3", "编译 0/3", "复看"]);
    });

    test("切分后：总轮数是实际，单元卡和编译交替，按单元数计", () => {
        const view = directorRunView(task({ mode: "sss", completed: 5, total: 8, turns: turns("plan", "direct", "cut", "card", "compile") }), { now: at(125) });
        assert.ok(view);
        assert.equal(view.estimated, false);
        assert.deepEqual(view.current, { index: 6, label: "单元卡（第 2 个单元）" });
        assert.equal(directorRoundsText(view), "已保存 5/8 轮（实际总轮数）");
        assert.deepEqual(
            view.phases.map((phase) => `${directorPhaseText(phase)}|${phase.state}`),
            ["集设计 ✓|done", "讲戏 ✓|done", "切分 ✓|done", "单元卡 1/2（正在写）|current", "编译 1/2|pending", "复看|pending"],
        );
        const compiling = directorRunView(task({ mode: "sss", completed: 6, total: 8, turns: turns("plan", "direct", "cut", "card", "compile", "card") }));
        assert.equal(compiling?.current?.label, "编译（第 2 个单元）");
    });

    test("第一集带立项：总轮数为奇数时有立项阶段", () => {
        const view = directorRunView(task({ mode: "sss", completed: 0, total: 9, turns: [] }));
        assert.equal(view?.phases[0].label, "立项");
        assert.equal(view?.current?.label, "立项：全剧圣经");
        assert.equal(view?.phases.find((phase) => phase.kind === "card")?.planned, 2);
        const later = directorRunView(task({ mode: "sss", completed: 0, total: 8, turns: [] }));
        assert.ok(!later?.phases.some((phase) => phase.kind === "setup"));
    });

    test("流里回合标记的标题只在它就是下一轮时采用", () => {
        const rounds: TaskDramaRounds = { mode: "sss", completed: 5, total: 8, turns: turns("plan", "direct", "cut", "card", "compile") };
        assert.equal(directorRunView(task(rounds), { streamTurn: { index: 6, heading: "单元卡 E1-U2" } })?.current?.label, "单元卡 E1-U2");
        assert.equal(directorRunView(task(rounds), { streamTurn: { index: 5, heading: "编译 E1-U1" } })?.current?.label, "单元卡（第 2 个单元）");
    });

    test("返修：固定两轮，不是预估", () => {
        const view = directorRunView(task({ mode: "sss", completed: 1, total: 2, repair: true, turns: [{ kind: "repair", heading: "返修 E1-U2" }] }));
        assert.equal(view?.estimated, false);
        assert.deepEqual(view?.phases.map(directorPhaseText), ["返修 ✓", "编译（正在写）"]);
    });

    test("失败后：没有当前轮，累计等待算到结束；最后保存的一轮写出来", () => {
        const view = directorRunView(task({ mode: "sss", completed: 3, total: 8, turns: turns("plan", "direct", "cut") }, "failed", { completedAt: new Date(at(600)).toISOString() }), { now: at(9000) });
        assert.equal(view?.current, undefined);
        assert.equal(view?.waitedSeconds, 600);
        assert.equal(view?.lastSaved, "cut-3");
        assert.ok(view?.phases.every((phase) => phase.state !== "current"));
    });

    test("本体论：规划轮前是预估，只显示轮数", () => {
        const early = directorRunView(task({ mode: "ontology", completed: 1, total: 12, turns: turns("project") }));
        assert.equal(early?.estimated, true);
        assert.deepEqual(early?.phases, []);
        assert.equal(directorRunView(task({ mode: "ontology", completed: 2, total: 9, turns: turns("project", "plan") }))?.estimated, false);
    });

    test("不是导演分镜任务时不给进度", () => {
        assert.equal(directorRunView(task(undefined)), undefined);
    });
});

describe("运行记录：导演分镜一行写轮次和累计等待", () => {
    test("进行中写已保存轮次、正在写哪一轮、已等多久", () => {
        const row = runRow(task({ mode: "sss", completed: 2, total: 10, turns: turns("plan", "direct") }), () => "E01", at(130));
        assert.equal(row.rounds, "已保存 2/10 轮（预估总轮数，切分出单元后改成实际） · 正在写：整场镜头表与单元总表");
        assert.equal(row.waited, 130);
    });

    test("结束后写最后保存的一轮，不写已等", () => {
        const row = runRow(task({ mode: "sss", completed: 8, total: 8, turns: turns("plan", "direct", "cut", "card", "compile", "card", "compile", "review") }, "succeeded"), () => "E01", at(900));
        assert.equal(row.rounds, "已保存 8/8 轮（实际总轮数） · 最后保存：review-8");
        assert.equal(row.waited, undefined);
    });

    test("普通任务没有轮次", () => {
        const row = runRow(task(undefined, "running"), () => "", at(10));
        assert.equal(row.rounds, undefined);
        assert.equal(row.waited, 10);
    });
});
