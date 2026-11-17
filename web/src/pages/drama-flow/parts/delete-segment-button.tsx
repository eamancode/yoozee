import { useRef, useState } from "react";
import { flushSync } from "react-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button } from "antd";
import { Trash2 } from "lucide-react";

import { AppModal } from "@/components/ui/product/app-modal";
import { dramaSegmentsKey } from "@/pages/projects/workbench/detail/drama-panels";
import { projectSceneCode } from "@/pages/projects/workbench/detail/unit-numbering";
import { listDramaSegments, type DramaSegmentView } from "@/services/api/project-drama";
import { videoChainsKey, videoChainsQuery } from "@/services/api/project-video-chains";
import { deleteProjectShot, getProjectUnitWorkspace } from "@/services/api/projects";
import { unitWorkspaceKey } from "../hooks/use-flow-data";

type Props = { projectId: string; segment: DramaSegmentView; episode: number; unitTitle?: string; onDeleted: () => void };

const artifactLabels: Record<string, string> = {
    storyboard: "分镜图",
    action_board: "预演图",
    start_frame: "首帧",
    end_frame: "尾帧",
    video: "视频",
    audio: "音频",
    subtitle: "字幕",
    delivery: "交付件",
};

export function DeleteSegmentButton(props: Props) {
    const [open, setOpen] = useState(false);
    const trigger = useRef<HTMLButtonElement>(null);
    const label = `删除分镜单元 ${props.segment.spec.id || props.segment.shotId}`;
    return (
        <>
            <Button ref={trigger} danger type="text" size="small" aria-label={label} title={label} icon={<Trash2 size={16} />} onClick={() => setOpen(true)} />
            {open && (
                <DeleteSegmentDialog
                    {...props}
                    onDeleted={() => {
                        if (trigger.current) props.onDeleted();
                    }}
                    onCancel={() => {
                        flushSync(() => setOpen(false));
                        trigger.current?.focus({ preventScroll: true });
                    }}
                />
            )}
        </>
    );
}

function DeleteSegmentDialog({ projectId, segment, episode, unitTitle, onDeleted, onCancel }: Props & { onCancel: () => void }) {
    const { message } = App.useApp();
    const client = useQueryClient();
    const submitting = useRef(false);
    const { shotId, unitId, spec } = segment;
    const segmentKey = dramaSegmentsKey(projectId, unitId);
    const workspace = useQuery({
        queryKey: unitWorkspaceKey(projectId, unitId),
        queryFn: () => getProjectUnitWorkspace(projectId, unitId),
        refetchOnMount: "always",
    });
    const chains = useQuery(videoChainsQuery(projectId, unitId));
    const artifacts = (workspace.data?.shotArtifacts || []).filter((item) => item.shotId === shotId);
    const counts = new Map<string, number>();
    for (const item of artifacts) {
        const label = artifactLabels[item.type] || "其他产物";
        counts.set(label, (counts.get(label) || 0) + 1);
    }
    const inUse = (workspace.data?.tasks || []).filter((task) => (task.status === "running" || task.status === "queued") && (task.clientContext?.shotId === shotId || artifacts.some((item) => item.taskId === task.id))).map((task) => `生成任务 ${task.id}`);
    for (const chain of chains.data?.chains || []) {
        if ((chain.status === "queued" || chain.status === "running") && chain.steps.some((step) => step.shotId === shotId || step.sourceShotId === shotId)) {
            inUse.push(`出片链 ${chain.id}`);
        }
    }

    const removeFromView = async () => {
        await client.cancelQueries({ queryKey: segmentKey, exact: true });
        onDeleted();
        client.setQueryData<DramaSegmentView[]>(segmentKey, (rows) => rows?.filter((row) => row.shotId !== shotId));
        client.removeQueries({ queryKey: ["project", projectId, "drama", "compile", shotId] });
        await Promise.all([
            client.invalidateQueries({ queryKey: ["project", projectId], predicate: ({ queryKey }) => !(queryKey[2] === "drama" && queryKey[3] === "compile" && queryKey[4] === shotId) }),
            client.invalidateQueries({ queryKey: videoChainsKey(projectId, unitId) }),
            client.invalidateQueries({ queryKey: ["drama-flow", "summary", projectId] }),
        ]);
    };
    const deletion = useMutation({
        mutationFn: (confirmed: { expectedRevisionId: string; expectedArtifactIds: string[] }) => deleteProjectShot(projectId, shotId, confirmed),
        onSuccess: async (result) => {
            message.success(result.deleted ? `已删除分镜单元 ${spec.id}` : `分镜单元 ${spec.id} 已被删除`);
            await removeFromView();
        },
        onSettled: () => {
            submitting.current = false;
        },
    });
    const refresh = async () => {
        try {
            const { segments } = await listDramaSegments(projectId, unitId);
            if (!segments.some((row) => row.shotId === shotId)) {
                message.info(`分镜单元 ${spec.id} 已被删除`);
                await removeFromView();
                return;
            }
            client.setQueryData(segmentKey, segments);
            await Promise.all([workspace.refetch(), chains.refetch()]);
            deletion.reset();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "读取分镜失败，请重试");
        }
    };
    const canConfirm = Boolean(workspace.data) && !workspace.isFetching && !workspace.isError && !inUse.length && !deletion.isError;
    const number = spec.id || shotId;
    const title = segment.title || spec.title;
    return (
        <AppModal
            title={`删除分镜单元 ${number}？`}
            open
            onCancel={deletion.isPending ? undefined : onCancel}
            closable={!deletion.isPending}
            cancelButtonProps={{ disabled: deletion.isPending }}
            okText="确认删除"
            cancelText="取消"
            okButtonProps={{ danger: true, disabled: !canConfirm }}
            confirmLoading={deletion.isPending}
            onOk={() => {
                if (!canConfirm || submitting.current) return;
                submitting.current = true;
                deletion.mutate({ expectedRevisionId: segment.revisionId || "", expectedArtifactIds: artifacts.map((item) => item.id) });
            }}
        >
            <p>
                {episode ? `第 ${episode} 集` : unitTitle || "当前集"} · 场 {projectSceneCode(spec.scene, episode) || "未编号"} · {title || number}，共 {spec.shots.length} 个子镜头。
            </p>
            <p>删除后，本单元及子镜头从分镜和待出片清单中移除。仅解除本单元的产物记录和素材绑定，保留共享文件及项目资产。</p>
            {workspace.isError ? (
                <p role="alert">本单元的产物未能读到，请重新核对。</p>
            ) : workspace.isFetching ? (
                <p>正在核对关联产物…</p>
            ) : (
                <p>{counts.size ? `将移除的关联产物：${Array.from(counts, ([label, count]) => `${label} × ${count}`).join("、")}` : "本单元没有关联产物。"}</p>
            )}
            {inUse.length > 0 && <p role="alert">{inUse.join("、")} 正在使用本单元，任务结束后可删除。</p>}
            {deletion.isError && <p role="alert">{deletion.error.message}</p>}
            {(deletion.isError || workspace.isError || inUse.length > 0) && (
                <Button size="small" onClick={() => void refresh()}>
                    重新核对
                </Button>
            )}
        </AppModal>
    );
}
