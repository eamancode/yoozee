import { useQuery } from "@tanstack/react-query";
import { Select } from "@/components/ui/base/select";
import { listGenerationTasks, queryGenerationTask, type GenerationTask } from "@/services/api/task-center";
import { dramaRunProgressOf } from "@/services/api/project-drama";
import { checkpointHistoryCandidates } from "./drama-run-checkpoint";

export function DramaCheckpointHistory({ projectId, unitId, step, open, running, value, onChange }: { projectId: string; unitId: string; step: string; open: boolean; running: boolean; value: string; onChange: (id: string) => void }) {
    const history = useQuery({
        queryKey: ["project", projectId, "checkpoint-history", unitId, step],
        enabled: open && !running && Boolean(unitId),
        staleTime: 30_000,
        queryFn: async () => {
            const tasks = checkpointHistoryCandidates(await listGenerationTasks(100, { projectId }), projectId, unitId, step);
            const results: GenerationTask[] = [];
            // Bound parallel reads; no task creation, model requests or billing.
            for (let index = 0; index < tasks.length; index += 4) results.push(...(await Promise.all(tasks.slice(index, index + 4).map((task) => queryGenerationTask(task.id)))));
            return checkpointHistoryCandidates(results, projectId, unitId, step).filter((task) => (dramaRunProgressOf(task)?.completed || 0) > 0);
        },
    });
    if (!open || running) return null;
    return (
        <div className="space-y-1 text-xs">
            <label className="block font-medium" htmlFor={`checkpoint-${step}`}>
                可恢复的历史运行（最近 100 条任务）
            </label>
            <Select
                id={`checkpoint-${step}`}
                className="w-full"
                value={value}
                loading={history.isFetching}
                disabled={history.isError}
                options={[
                    { value: "", label: "当前最新运行" },
                    ...(history.data || []).map((task) => {
                        const progress = dramaRunProgressOf(task)!;
                        const charged = task.billing?.actualAmountMicrocredits;
                        return {
                            value: task.id,
                            label: `${new Date(task.createdAt).toLocaleString("zh-CN")} · ${progress.completed}/${progress.total} 已保存 · ${task.model || "模型未记录"}${charged === undefined ? " · 费用待核实" : ` · 实扣 ${charged / 1_000_000} 积分`}`,
                        };
                    }),
                ]}
                onChange={onChange}
            />
            <p className="text-foreground/55">选择只查看检查点；点击继续才提交任务。服务端会校验原文、场次和资产是否仍匹配。</p>
            {history.isError ? <p role="alert">历史读取失败，请关闭进度后重试；当前检查点仍可使用。</p> : null}
        </div>
    );
}
