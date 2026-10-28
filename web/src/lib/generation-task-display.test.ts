import assert from "node:assert/strict";
import test from "node:test";
import { canCancelGenerationTask, generationTaskCancelBlockedReason } from "./generation-task-display";

test("任务中心只对还没提交给第三方的任务放开取消，已提交的说明原因", () => {
    const queued = { status: "queued" as const, stage: "等待队列调度" };
    assert.equal(canCancelGenerationTask(queued), true);
    assert.equal(generationTaskCancelBlockedReason(queued), "");

    // 出图任务建出后很快就提交上游：拿到 providerRequestId 或进入未知阶段后不可取消。
    const submitted = { status: "running" as const, stage: "生成中", providerRequestId: "req-1" };
    assert.equal(canCancelGenerationTask(submitted), false);
    assert.match(generationTaskCancelBlockedReason(submitted), /已提交给第三方/);
    assert.match(generationTaskCancelBlockedReason({ status: "running" as const, stage: "submission_unknown" }), /提交结果不明确/);
    assert.match(generationTaskCancelBlockedReason({ status: "running" as const, stage: "生成中", mediaStage: "download" as const }), /正在保存结果/);
    assert.match(generationTaskCancelBlockedReason({ status: "running" as const, stage: "生成中", providerCancelStatus: "requested" as const }), /请求取消/);

    // 逐轮续写的文本任务运行中也能取消（后端同一规则）。
    assert.equal(canCancelGenerationTask({ status: "running" as const, stage: "第 2/3 场", dramaRun: true }), true);
    // 已结束的任务没有取消按钮，也没有原因。
    assert.equal(generationTaskCancelBlockedReason({ status: "succeeded" as const }), "");
});
