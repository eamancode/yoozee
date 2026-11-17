import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Image, Select } from "antd";

import { AppModal } from "@/components/ui/product/app-modal";
import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { assetCategoryLabel } from "@/lib/asset-category";
import { modelCapabilityConfigFor } from "@/lib/model-capabilities";
import { dramaProgressKey, dramaSegmentsKey } from "@/pages/projects/workbench/detail/drama-panels";
import { projectAssetReferenceImage, projectAssetVoiceSampleResourceId } from "@/pages/projects/workbench/detail/workflow-shot-references";
import { compileProjectShot, type DramaSegmentView } from "@/services/api/project-drama";
import { linkShotAsset, unlinkShotAsset, type ProjectAsset } from "@/services/api/projects";
import { resourceFileUrl } from "@/services/api/resources";

import { unitWorkspaceKey, type FlowData } from "../hooks/use-flow-data";
import { availableBindingAssets, isUnitBindingAsset, type UnitBinding } from "../model/shot-bindings";

type Edit = { kind: "pick"; replace?: UnitBinding; asset?: ProjectAsset } | { kind: "remove"; binding: UnitBinding };

const assetLabel = (asset: ProjectAsset) => (asset.category === "character" ? (asset.variant ? "造型子卡" : "人物主卡") : assetCategoryLabel(asset.category));

export function ShotBindingEditor({
    projectId,
    segment,
    bindings,
    assets,
    assetsQuery,
    videoModel,
    onClose,
}: {
    projectId: string;
    segment: DramaSegmentView;
    bindings: UnitBinding[];
    assets: ProjectAsset[];
    videoModel: string;
    assetsQuery: Pick<FlowData["assetsQuery"], "isError" | "isPending" | "refetch">;
    onClose: () => void;
}) {
    const { message } = App.useApp();
    const client = useQueryClient();
    const config = useDramaModelConfig();
    const [edit, setEdit] = useState<Edit>();
    const queryKey = ["project", projectId, "drama", "compile", segment.shotId];
    const compiled = useQuery({
        queryKey: [...queryKey, segment.revisionId || "", videoModel],
        queryFn: async () => (await compileProjectShot(projectId, segment.shotId, { channelModel: videoModel })).compiled,
        staleTime: 30_000,
    });
    const voiceBindings = compiled.data?.voiceBindings || [];
    const audioCount = compiled.data?.result.files.filter((file) => file.role === "voice").length || 0;
    const maxAudios = videoModel ? modelCapabilityConfigFor(config, videoModel).video?.references.maxAudios : undefined;
    const insufficient = maxAudios !== undefined && audioCount > maxAudios;
    const ready = maxAudios !== undefined && !insufficient && !compiled.data?.result.issues.some((issue) => issue.level === "error");
    const options = availableBindingAssets(assets, bindings, edit?.kind === "pick" ? edit.replace : undefined);
    const selection = edit?.kind === "pick" ? edit.asset : undefined;
    const submit = useMutation({
        mutationFn: async (draft: Edit) => {
            if (draft.kind === "remove" && draft.binding.reference) {
                await unlinkShotAsset(projectId, segment.shotId, draft.binding.reference.id);
            } else if (draft.kind === "pick" && draft.asset?.primaryVersionId) {
                await linkShotAsset(projectId, segment.shotId, { assetVersionId: draft.asset.primaryVersionId, role: "reference", replaceReferenceId: draft.replace?.reference?.id });
            } else throw new Error("请选择要保存的资产版本");
        },
        onSuccess: async () => {
            await Promise.all([
                client.invalidateQueries({ queryKey: unitWorkspaceKey(projectId, segment.unitId) }),
                client.invalidateQueries({ queryKey: dramaSegmentsKey(projectId, segment.unitId) }),
                client.invalidateQueries({ queryKey: dramaProgressKey(projectId) }),
                client.invalidateQueries({ queryKey }),
            ]);
            message.success("已保存单元绑定");
            onClose();
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "保存绑定失败"),
    });
    const pending = submit.isPending;
    const preview = selection ? projectAssetReferenceImage(selection)?.url : undefined;
    const sample = selection ? projectAssetVoiceSampleResourceId(selection) : "";
    return (
        <AppModal
            open
            title={`${segment.spec.id || segment.title} · 编辑绑定`}
            width={660}
            closable={!pending}
            mask={{ closable: !pending }}
            keyboard={!pending}
            onCancel={() => {
                if (!pending) onClose();
            }}
            footer={
                <div className="flex justify-end gap-2">
                    <Button disabled={pending} onClick={onClose}>
                        {edit ? "取消编辑" : "关闭"}
                    </Button>
                    {edit ? (
                        <Button type="primary" disabled={edit.kind === "pick" && !selection} loading={pending} onClick={() => submit.mutate(edit)}>
                            保存绑定
                        </Button>
                    ) : null}
                </div>
            }
        >
            <div className="max-h-[65dvh] overflow-y-auto space-y-3">
                <ul className="space-y-2" aria-label="本单元的资产和声音参考">
                    {bindings.map((item) => {
                        const editable = item.reference?.role === "reference" && isUnitBindingAsset(item.asset);
                        const tag = voiceBindings.find((voice) => voice.versionId === item.versionId && voice.sampleResourceId === item.sampleResourceId)?.tag;
                        const imageTag = compiled.data?.result.files.find((file) => file.file === item.versionId && file.role !== "voice")?.tag;
                        const role = item.reference?.role;
                        return (
                            <li key={item.id} className="flex flex-wrap items-center gap-3 rounded-md border border-border/60 p-2">
                                {item.preview ? <Image width={48} height={48} className="object-contain" src={item.preview} alt={`${item.title}固定版本预览`} /> : null}
                                <div className="min-w-0 flex-1">
                                    <strong>{item.title}</strong>
                                    {imageTag ? <span className="ml-2 text-xs">{imageTag}</span> : null}
                                    <div className="text-xs text-foreground/60">
                                        {assetLabel(item.asset)} · {item.version ? `v${item.version}` : "版本"} · <code className="break-all">{item.versionId}</code>
                                        {role && role !== "reference" ? ` · ${role === "style" ? "风格" : role === "continuity" ? "连续性" : role}` : ""}
                                    </div>
                                    {item.asset.category === "character" ? (
                                        <div className="mt-1 text-xs">
                                            {item.sampleResourceId ? (
                                                <>
                                                    <span>
                                                        {compiled.isError ? "已有音样 · 发送清单读取失败" : compiled.isPending ? "已有音样 · 正在读取发送清单" : tag ? `已有音样 · ${ready ? "本段发送" : "本段需要"} ${tag}` : "已有音样 · 本段未引用"}
                                                    </span>
                                                    <audio className="mt-1 h-8 max-w-full" aria-label={`试听${item.title}的声音样本`} controls preload="none" src={resourceFileUrl(item.sampleResourceId)} />
                                                </>
                                            ) : (
                                                "所选版本没有声音样本"
                                            )}
                                        </div>
                                    ) : null}
                                </div>
                                {editable && !edit ? (
                                    <div className="flex gap-1">
                                        <Button size="small" aria-label={`更换${item.title}`} onClick={() => setEdit({ kind: "pick", replace: item })}>
                                            同类更换
                                        </Button>
                                        <Button size="small" aria-label={`解除${item.title}绑定`} onClick={() => setEdit({ kind: "remove", binding: item })}>
                                            解除
                                        </Button>
                                    </div>
                                ) : null}
                            </li>
                        );
                    })}
                </ul>
                {!bindings.length ? <p>本单元尚未绑定资产。</p> : null}
                {compiled.isError ? (
                    <Button size="small" onClick={() => void compiled.refetch()}>
                        重读发送清单
                    </Button>
                ) : null}
                {insufficient ? (
                    <p role="alert" className="text-status-error">
                        本段需要 {audioCount} 个声音参考，当前线路最多支持 {maxAudios} 个。请更换视频线路或调整绑定。
                    </p>
                ) : null}
                {!edit ? (
                    <Button onClick={() => setEdit({ kind: "pick" })}>绑定资产</Button>
                ) : edit.kind === "remove" ? (
                    <p>将解除「{edit.binding.title}」在本单元的绑定。</p>
                ) : (
                    <div className="space-y-2">
                        {assetsQuery.isError ? (
                            <Button size="small" onClick={() => void assetsQuery.refetch()}>
                                资产读取失败，重新读取
                            </Button>
                        ) : null}
                        <label htmlFor="flow-unit-binding-choice">{edit.replace ? `将「${edit.replace.title}」更换为` : "选择本项目资产"}</label>
                        <Select
                            id="flow-unit-binding-choice"
                            className="w-full"
                            disabled={pending || assetsQuery.isError}
                            loading={assetsQuery.isPending}
                            placeholder="搜索人物、造型、场景或道具"
                            showSearch={{ optionFilterProp: "label" }}
                            value={selection?.primaryVersionId}
                            options={options.map((asset) => ({
                                value: asset.primaryVersionId!,
                                label: `${asset.title} · ${assetLabel(asset)} · ${asset.character && asset.character.versionId === asset.primaryVersionId ? `v${asset.character.version}` : asset.primaryVersionId}`,
                            }))}
                            notFoundContent="没有可选的未绑定资产"
                            onChange={(versionId) => setEdit({ ...edit, asset: options.find((asset) => asset.primaryVersionId === versionId) })}
                        />
                        {selection ? (
                            <div className="flex flex-wrap items-center gap-2">
                                {preview ? <Image width={48} height={48} className="object-contain" src={preview} alt={`${selection.title}待绑定版本预览`} /> : null}
                                <div>
                                    <p>
                                        将固定版本 <code className="break-all">{selection.primaryVersionId}</code>
                                    </p>
                                    {sample ? (
                                        <audio className="h-8 max-w-full" controls preload="none" aria-label={`试听${selection.title}的声音样本`} src={resourceFileUrl(sample)} />
                                    ) : selection.category === "character" ? (
                                        <span>此版本没有声音样本</span>
                                    ) : null}
                                </div>
                            </div>
                        ) : null}
                    </div>
                )}
            </div>
        </AppModal>
    );
}
