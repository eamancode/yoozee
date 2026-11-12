import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { App, Button, Segmented } from "antd";

import { AppDrawer } from "@/components/ui/product/app-drawer/app-drawer";
import { cancelGenerationTask, type GenerationTask } from "@/services/api/task-center";

import { projectTasksKey, type FlowData } from "../hooks/use-flow-data";
import { useModelNames } from "../hooks/use-model-names";
import { costSummary, costSummaryText, creditsText } from "../model/costs";
import { durationText, runRow } from "../model/runs";
import { ReadError } from "./layout";

type RunFilter = "all" | "active" | "failed";

/** 运行记录：这个项目最近 100 个任务，一行一次运行；失败的写原因，进行中的可以取消。 */
export function RunsPanel({ open, data, onClose }: { open: boolean; data: FlowData; onClose: () => void }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [filter, setFilter] = useState<RunFilter>("all");
    const { projectTasks, tasksQuery, allUnits, segments, project } = data;
    const scopeOf = useMemo(() => {
        const unitTitle = new Map(allUnits.map((unit) => [unit.id, unit.title]));
        const segmentTitle = new Map(segments.map((segment) => [segment.shotId, `${segment.segmentId} ${segment.title}`.trim()]));
        return (task: GenerationTask) => {
            const context = task.clientContext;
            if (context?.shotId) return segmentTitle.get(context.shotId) || "某一段";
            if (context?.chapterId) return unitTitle.get(context.chapterId) || "某一集";
            return "整个项目";
        };
    }, [allUnits, segments]);
    const rows = useMemo(() => projectTasks.map((task) => runRow(task, scopeOf)), [projectTasks, scopeOf]);
    const shown = rows.filter((row) => (filter === "active" ? row.active : filter === "failed" ? row.status === "failed" : true));
    const models = useModelNames(open);
    const spent = costSummaryText(data.tasksRead, costSummary(projectTasks));
    const cancel = useMutation({
        mutationFn: (id: string) => cancelGenerationTask(id),
        onSuccess: async () => {
            if (project) await queryClient.invalidateQueries({ queryKey: projectTasksKey(project.id) });
            message.success("已取消");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "取消没成功"),
    });
    return (
        <AppDrawer open={open} onClose={onClose} title="运行记录" size={560}>
            <div className="drama-flow-runs">
                <div className="drama-flow-runs-head">
                    <Segmented<RunFilter>
                        size="small"
                        value={filter}
                        onChange={setFilter}
                        options={[
                            { value: "all", label: `全部 ${rows.length}` },
                            { value: "active", label: `进行中 ${rows.filter((row) => row.active).length}` },
                            { value: "failed", label: `失败 ${rows.filter((row) => row.status === "failed").length}` },
                        ]}
                    />
                    <span>最近 100 次 · 共花 {spent}</span>
                </div>
                {tasksQuery.isError ? <ReadError what="运行记录" onRetry={() => void tasksQuery.refetch()} /> : null}
                {shown.length ? (
                    <ul className="drama-flow-runs-list">
                        {shown.map((row) => (
                            <li key={row.id} data-status={row.status}>
                                <div className="drama-flow-runs-line">
                                    <strong>{row.step}</strong>
                                    <span>{row.scope}</span>
                                    <span className="drama-flow-chip" data-tone={row.status === "succeeded" ? "ok" : row.status === "failed" ? "bad" : row.active ? "run" : undefined}>
                                        {row.statusLabel}
                                    </span>
                                </div>
                                <div className="drama-flow-runs-meta">
                                    <span>{new Date(row.createdAt).toLocaleString("zh-CN", { hour12: false })}</span>
                                    {row.model ? <span>{models.nameOf(row.model, "").text}</span> : null}
                                    {row.seconds !== undefined ? <span>用时 {durationText(row.seconds)}</span> : null}
                                    {row.cost !== undefined ? <span>{creditsText(row.cost)}</span> : null}
                                </div>
                                {row.error ? <p className="drama-flow-runs-error">{row.error}</p> : null}
                                {row.active ? (
                                    <Button size="small" loading={cancel.isPending && cancel.variables === row.id} onClick={() => cancel.mutate(row.id)}>
                                        取消
                                    </Button>
                                ) : null}
                            </li>
                        ))}
                    </ul>
                ) : (
                    // 只有读到了才说「没有记录」；在读、没读到时不能显示成空。
                    <p className="drama-flow-muted">{data.tasksRead === "loading" ? "正在读运行记录…" : data.tasksRead === "error" ? "运行记录没读到。" : rows.length ? "没有符合的记录。" : "还没有运行记录。"}</p>
                )}
            </div>
        </AppDrawer>
    );
}
