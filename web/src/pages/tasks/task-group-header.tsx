import { FolderKanban, RotateCcw } from "lucide-react";

import { CONTENT_MODERATION_ERROR_CODE, isContentModerationError } from "@/lib/generation-error";
import type { GenerationTask } from "@/services/api/task-center";
import { isTaskFailed } from "./task-shared";

export type TaskGroup = { key: string; title: string; projectName: string; tasks: GenerationTask[] };

export function TaskGroupHeader({ group, retrying = false, onRetryFailed }: { group: TaskGroup; retrying?: boolean; onRetryFailed: () => void }) {
    const succeeded = group.tasks.filter((task) => task.status === "succeeded").length;
    const active = group.tasks.filter((task) => task.status === "queued" || task.status === "running").length;
    const failedTasks = group.tasks.filter((task) => isTaskFailed(task) && task.errorCode !== CONTENT_MODERATION_ERROR_CODE && !isContentModerationError(task.error));
    const failed = failedTasks.length;
    // 「用已写好的 N 节重新写入」的任务不在这里重试（回章节页重新写入），只剩它们时不给「重试失败任务」。
    const retryable = failedTasks.filter((task) => !task.refinalizeOnly).length;
    const title = group.projectName ? `${group.title} · ${group.projectName}` : group.title;
    return (
        <div className="task-group-head">
            <span className="task-group-ic">
                <FolderKanban />
            </span>
            <div className="min-w-0">
                <div className="task-group-name">
                    <span>{title}</span>
                    <span className="task-group-count">
                        共 {group.tasks.length} 项 · 完成 {succeeded}
                    </span>
                </div>
                <span className="task-group-sub">
                    {active ? <span>{active} 运行中</span> : null}
                    {active && failed ? <span aria-hidden="true"> · </span> : null}
                    {failed ? <b className="is-bad">{failed} 失败</b> : null}
                    {(active || failed) && succeeded ? <span aria-hidden="true"> · </span> : null}
                    {succeeded ? <span>{succeeded} 已完成</span> : null}
                    {!active && !failed && !succeeded ? "暂无进行中的任务" : null}
                </span>
            </div>
            {retryable ? (
                <button type="button" className="task-group-reset" disabled={retrying} onClick={onRetryFailed}>
                    <RotateCcw className={retrying ? "animate-spin" : undefined} />
                    {retrying ? "重试中..." : "重试失败任务"}
                </button>
            ) : null}
        </div>
    );
}
