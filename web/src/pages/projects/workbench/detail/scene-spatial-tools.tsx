import { useRef, useState } from "react";
import { App, Button, Select } from "antd";
import { useMutation, useQuery } from "@tanstack/react-query";
import { AppModal } from "@/components/ui/product/app-modal";
import { CachedResourceImage } from "@/components/cached-resource-image";
import { ModelPicker } from "@/components/model-picker";
import { resourceFileUrl } from "@/services/api/resources";
import { runBackendGenerationTask } from "@/services/api/generation-task";
import { cancelGenerationTask } from "@/services/api/task-center";
import { listProjectSpatialReferences, type ProjectAsset, type ProjectSpatialReference as SpatialReference } from "@/services/api/projects";
import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { turnaroundImageModel } from "./project-character-media";

export function SceneSpatialTools({ projectId, asset }: { projectId: string; asset: ProjectAsset }) {
    const { message } = App.useApp();
    const config = useDramaModelConfig();
    const [selectedModel, setSelectedModel] = useState<string>();
    const model = selectedModel || turnaroundImageModel(config);
    const [gridId, setGridId] = useState<string>();
    const [cell, setCell] = useState(1);
    const [taskId, setTaskId] = useState<string>();
    const [preview, setPreview] = useState<SpatialReference>();
    const submitting = useRef(false);
    const references = useQuery({
        queryKey: ["project", projectId, "spatial-references", asset.id, asset.primaryVersionId],
        queryFn: ({ signal }) => listProjectSpatialReferences(projectId, asset.id, signal),
        refetchInterval: taskId ? 3000 : 30000,
    });
    const items = references.data?.items || [];
    const grids = items.filter((item) => item.kind === "grid");
    const selectedGrid = grids.find((item) => item.resourceId === gridId) || grids.at(-1);
    const generate = useMutation({
        mutationFn: async (kind: "grid" | "redraw") => {
            if (submitting.current) throw new Error("空间参考正在提交，请稍候");
            submitting.current = true;
            try {
                if (!model) throw new Error("请先选择可用图片模型");
                if (kind === "redraw" && !selectedGrid) throw new Error("请先生成九宫格");
                setTaskId(undefined);
                const result = await runBackendGenerationTask({
                    projectId,
                    mode: "image",
                    prompt: kind === "grid" ? "生成空间九宫格" : "选格高清重绘",
                    config: { ...config, model, count: "1", size: "16:9" },
                    metadata: {
                        source: "scene-spatial-reference",
                        operation: "project_spatial_reference",
                        domainProjectId: projectId,
                        projectAssetId: asset.id,
                        projectAssetVersionId: asset.primaryVersionId,
                        spatialKind: kind,
                        gridResourceId: selectedGrid?.resourceId,
                        cell,
                        nodeId: `spatial:${asset.id}:${kind}`,
                    },
                    onTaskUpdate: (task) => setTaskId(task.id),
                });
                if (result.spatialImageStatus !== "bound") throw new Error("图片已保留在任务结果中，但场景版本已变化，未绑定到当前场景，请刷新后查看。");
                return result;
            } finally {
                submitting.current = false;
                setTaskId(undefined);
            }
        },
        onSuccess: async () => {
            await references.refetch();
            message.success("空间参考已生成并保存到当前场景");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "空间参考生成失败"),
    });
    const cancel = useMutation({
        mutationFn: async () => {
            if (taskId) await cancelGenerationTask(taskId);
        },
        onError: () => message.error("取消失败，请到任务中心查看"),
    });
    return (
        <div className="mt-3 grid gap-2 border-t border-border/60 pt-2">
            <p className="text-xs text-foreground/60">微缩空间母版 · 05格仅作布局锚点</p>
            <ModelPicker preferenceScope="short-drama" config={config} capability="image" value={model} onChange={setSelectedModel} fullWidth showSelectedPrice />
            {generate.isError ? (
                <p role="alert" className="break-words text-xs text-foreground/60">
                    {generate.error instanceof Error ? generate.error.message : "空间参考生成失败"}
                </p>
            ) : null}
            <Button size="small" disabled={!model || asset.mediaType !== "image" || generate.isPending} loading={generate.isPending && generate.variables === "grid"} onClick={() => generate.mutate("grid")}>
                生成空间九宫格
            </Button>
            {references.isError ? (
                <p role="alert" className="text-xs text-foreground/60">
                    空间参考读取失败，请刷新后重试。
                </p>
            ) : null}
            {grids.length ? <Select size="small" aria-label="选择空间九宫格" value={selectedGrid?.resourceId} onChange={setGridId} options={grids.map((grid, index) => ({ value: grid.resourceId, label: `九宫格 ${index + 1}` }))} /> : null}
            <div className="flex flex-wrap gap-2">
                <Select size="small" aria-label="选择实景格" value={cell} onChange={setCell} options={[1, 2, 3, 4, 6, 7, 8, 9].map((value) => ({ value, label: `第${String(value).padStart(2, "0")}格` }))} />
                <Button size="small" disabled={!selectedGrid || generate.isPending} loading={generate.isPending && generate.variables === "redraw"} onClick={() => generate.mutate("redraw")}>
                    高清重绘
                </Button>
                {generate.isPending && taskId ? (
                    <Button size="small" loading={cancel.isPending} onClick={() => cancel.mutate()}>
                        取消本次生成
                    </Button>
                ) : null}
            </div>
            {items.length ? (
                <div className="flex flex-wrap gap-2">
                    {items.map((item) => (
                        <button key={item.resourceId} type="button" className="w-20" onClick={() => setPreview(item)} aria-label={`预览${item.kind === "grid" ? "九宫格" : `第${item.cell}格重绘`}`}>
                            <CachedResourceImage storageKey={`resource:${item.resourceId}`} src={resourceFileUrl(item.resourceId)} alt="空间参考" className="h-14 w-full object-contain" />
                            <span className="text-xs">{item.kind === "grid" ? "九宫格" : `第${item.cell}格`}</span>
                        </button>
                    ))}
                </div>
            ) : null}
            <AppModal open={Boolean(preview)} onCancel={() => setPreview(undefined)} footer={null} title="场景空间参考" width={1000}>
                {preview ? <CachedResourceImage storageKey={`resource:${preview.resourceId}`} src={resourceFileUrl(preview.resourceId)} alt="完整空间参考" className="max-h-[75vh] w-full object-contain" /> : null}
            </AppModal>
        </div>
    );
}
