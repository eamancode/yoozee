import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button } from "antd";
import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { navigateToSettings } from "@/lib/settings-navigation";
import { backendProviderConfig } from "@/services/api/generation-task";
import { getOntologyDocuments, ontologyDocumentsKey, runDramaProjectStep } from "@/services/api/project-drama";
import { announceCreatedGenerationTask, type GenerationTask } from "@/services/api/task-center";
import { logicalModelIDForConfig, useConfigStore } from "@/stores/use-config-store";
import { DocumentText } from "./ontology-documents-drawer";
import { followDramaRunTask } from "./project-chapter-ai";
import { dialogTextSubmission, type StepDialogModels } from "./step-dialog-models";

export function useSSSDocuments(projectId: string, enabled = true) {
    return useQuery({ queryKey: ontologyDocumentsKey(projectId), queryFn: ({ signal }) => getOntologyDocuments(projectId, signal), enabled: Boolean(projectId && enabled) });
}

export function EpisodeDesign({ projectId, unitId }: { projectId: string; unitId: string }) {
    const docs = useSSSDocuments(projectId);
    if (docs.isError)
        return (
            <p role="alert">
                本集设计读取失败{" "}
                <Button size="small" onClick={() => void docs.refetch()}>
                    重新读取
                </Button>
            </p>
        );
    if (!docs.data) return null;
    const design = docs.data.designs?.find((item) => item.unitId === unitId);
    if (!design) return <p>缺本集设计，可在「补写本集设计」中选择这一集。</p>;
    const [plan, table] = design.content.split(/^##\s+第二部分\s+台词定稿表\s*$/m);
    return (
        <section aria-label="本集导演文档">
            <details>
                <summary>本集设计 · 第 {design.version} 版</summary>
                <DocumentText text={plan.replace(/^##\s+第一部分\s+本集设计/, "").trim()} />
            </details>
            <details>
                <summary>台词定稿表</summary>
                <DocumentText text={table?.trim() || ""} />
            </details>
        </section>
    );
}

export function DirectorSetup({ projectId, tasks, textModel }: { projectId: string; tasks: GenerationTask[]; textModel?: StepDialogModels["text"] }) {
    const docs = useSSSDocuments(projectId);
    const client = useQueryClient();
    const config = useDramaModelConfig();
    const ready = useConfigStore((state) => state.isAiConfigReady);
    const { message } = App.useApp();
    const active = tasks.find((t) => t.dramaRounds?.mode === "setup" && (t.status === "queued" || t.status === "running"));
    const latest = tasks.find((t) => t.dramaRounds?.mode === "setup");
    const checkpoint = latest && (latest.status === "failed" || latest.status === "cancelled") && latest.dramaRounds?.completed ? latest : undefined;
    const run = useMutation({
        mutationFn: async (resumeTaskId?: string) => {
            const { model, issue } = dialogTextSubmission(textModel, config, "", resumeTaskId ? checkpoint?.model || "" : undefined);
            if (issue) throw new Error(issue);
            const selected = { ...config, model, textModel: model };
            if (!model || !ready(selected, model)) {
                navigateToSettings({ continueCreation: true });
                return;
            }
            const { task } = await runDramaProjectStep(projectId, "director.setup", { model, logicalModelId: logicalModelIDForConfig(selected), config: backendProviderConfig(selected, "text") as Record<string, unknown>, resumeTaskId });
            announceCreatedGenerationTask(task);
            await client.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] });
            await followDramaRunTask(task);
            await client.invalidateQueries({ queryKey: ["project", projectId] });
        },
        onError: (e) => {
            message.error(e.message);
        },
    });
    const basis = docs.data?.bibleBasis;
    const bible = docs.data?.bible;
    const rounds = active?.dramaRounds;
    return (
        <section className="drama-flow-panel" aria-label="全剧立项">
            <div className="drama-flow-panel-head">
                <h3>全剧立项</h3>
                {checkpoint && !active && (
                    <Button size="small" disabled={!docs.data || run.isPending} onClick={() => run.mutate(checkpoint.id)}>
                        {checkpoint.dramaRounds!.completed >= checkpoint.dramaRounds!.total ? "继续立项收尾" : `从第 ${checkpoint.dramaRounds!.completed + 1} 批继续`}
                    </Button>
                )}
                <Button size="small" loading={run.isPending || Boolean(active)} disabled={!docs.data} onClick={() => run.mutate(undefined)}>
                    {bible ? "重新立项" : "全剧立项"}
                </Button>
            </div>
            <p>
                {rounds
                    ? `立项中 · 第 ${Math.min(rounds.completed + 1, rounds.total)}/${rounds.total} 批`
                    : docs.isError
                      ? "立项状态读取失败"
                      : !docs.data
                        ? "正在读取立项状态"
                        : bible
                          ? `已立项 · 第 ${bible.version} 版 · 依据 ${basis?.episodes ?? "—"}/${basis?.total ?? "—"} 集`
                          : "未立项"}
            </p>
            {basis?.legacy || basis?.outdated ? <p>当前圣经{basis.legacy ? "仅依据旧分镜流程的一集" : "依据的剧本已有变动"}，建议重新立项。</p> : null}
        </section>
    );
}
