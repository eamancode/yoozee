import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { App, Button } from "antd";
import { ImageUp, RotateCw } from "lucide-react";

import { AssetImageLibraryPicker } from "@/components/assets/asset-image-library-picker";
import { ASSET_CATEGORY_LABELS } from "@/lib/asset-category";
import { projectWorkflow } from "@/lib/project-workflow";
import { dramaImageStatusOf } from "@/pages/projects/workbench/detail/drama-image-summary";
import { ProjectCharacterVariants } from "@/pages/projects/workbench/detail/project-character-variants";
import { assetPreviewUrl } from "@/pages/projects/workbench/detail/workflow-shot-references";
import { bindProjectAssetImage, replaceProjectCharacterRepresentations, type DramaImageStatus, type Project, type ProjectAsset } from "@/services/api/projects";

import { DetailPane, PaneSection, StageLayout, SummaryRow } from "./layout";

export const IMAGE_STATUS: Record<DramaImageStatus, { text: string; tone?: "ok" | "run" | "bad" | "warn" }> = {
    bound: { text: "已出图", tone: "ok" },
    queued: { text: "出图中", tone: "run" },
    waiting: { text: "排队中", tone: "run" },
    paused: { text: "已暂停", tone: "warn" },
    failed: { text: "失败", tone: "bad" },
    stale: { text: "没绑上", tone: "bad" },
    none: { text: "还没出图", tone: "warn" },
};

export const lookAssetError = (asset: ProjectAsset) => asset.image?.error || "";
const isBad = (status?: DramaImageStatus) => status === "failed" || status === "stale";
const text = (value: unknown) => (typeof value === "string" ? value.trim() : Array.isArray(value) ? value.filter((item) => typeof item === "string").join("、") : "");

/** 资产的设定字段：角色看 character.definition，场景道具看 visual.definition；两边字段名不同，统一成几行。 */
export function assetFields(asset: ProjectAsset): Array<{ label: string; value: string }> {
    const def = (asset.character?.definition || asset.visual?.definition || {}) as Record<string, unknown>;
    const rows: Array<[string, unknown]> = asset.character
        ? [
              ["剧情定位", def.role],
              ["外观设定", def.appearance || def.consistencyPrompt],
              ["性格", def.personality],
              ["别名与称呼", def.aliases],
              ["原文依据", def.evidence || def.sourceQuote],
              ["出图提示词", def.prompt || def.promptEn],
          ]
        : [
              ["描述", def.description],
              ["原文依据", def.evidence || def.sourceQuote],
              ["出图提示词", def.prompt],
          ];
    return rows.map(([label, value]) => ({ label, value: text(value) }));
}

/** 角色、场景、道具一个小节：汇总行 + 卡片；卡片上就有状态、失败原因、版本、引用；点开看全部设定。 */
export function LookAssets({
    project,
    category,
    assets,
    selectedId,
    retrying,
    onSelect,
    onRetry,
}: {
    project: Project;
    category: "character" | "environment" | "prop";
    assets: ProjectAsset[];
    selectedId?: string;
    retrying: Set<string>;
    onSelect: (id?: string) => void;
    onRetry: (assets: ProjectAsset[]) => void;
}) {
    const label = ASSET_CATEGORY_LABELS[category];
    const fourPanel = projectWorkflow(project.workflowId).integratedAssets;
    // 子卡挂在主卡下面显示，不单独占一张卡。
    const { mains, variantsOf } = useMemo(() => {
        const own = assets.filter((asset) => asset.category === category);
        const byParent = new Map<string, ProjectAsset[]>();
        for (const asset of own) {
            if (!asset.variant?.parentId) continue;
            byParent.set(asset.variant.parentId, [...(byParent.get(asset.variant.parentId) || []), asset]);
        }
        return { mains: own.filter((asset) => !asset.variant), variantsOf: byParent };
    }, [assets, category]);
    const statuses = mains.map((asset) => dramaImageStatusOf(asset));
    const failed = mains.filter((_, index) => isBad(statuses[index]));
    const selected = mains.find((asset) => asset.id === selectedId);
    const focusRef = useRef<HTMLButtonElement | null>(null);
    useEffect(() => focusRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" }), [selectedId]);

    return (
        <StageLayout
            pane={
                selected ? (
                    <AssetPane
                        project={project}
                        asset={{ ...selected, variants: selected.variants?.length ? selected.variants : variantsOf.get(selected.id) }}
                        fourPanel={fourPanel}
                        retrying={retrying.has(selected.id)}
                        onRetry={() => onRetry([selected])}
                        onClose={() => onSelect(undefined)}
                    />
                ) : undefined
            }
        >
            <SummaryRow
                items={[
                    { label: `${label}`, value: mains.length },
                    { label: "已出图", value: statuses.filter((status) => status === "bound").length, tone: "ok" },
                    { label: "出图中", value: statuses.filter((status) => status === "queued" || status === "waiting").length, tone: "run" },
                    { label: "失败", value: failed.length, tone: failed.length ? "bad" : undefined },
                    { label: "还没出", value: statuses.filter((status) => status === "none" || status === "paused").length },
                ]}
            />
            {failed.length > 1 ? (
                <div className="drama-flow-work-actions">
                    <Button icon={<RotateCw className="size-3.5" />} loading={failed.some((asset) => retrying.has(asset.id))} onClick={() => onRetry(failed)}>
                        重试全部失败的 {failed.length} 张
                    </Button>
                </div>
            ) : null}
            {mains.length ? (
                <div className="drama-flow-asset-grid">
                    {mains.map((asset, index) => {
                        const status = statuses[index];
                        const view = status ? IMAGE_STATUS[status] : undefined;
                        const preview = assetPreviewUrl(asset);
                        const bad = isBad(status);
                        const subs = asset.variants?.length ? asset.variants : variantsOf.get(asset.id) || [];
                        const description = assetFields(asset)[asset.character ? 1 : 0]?.value;
                        return (
                            <button
                                key={asset.id}
                                ref={asset.id === selectedId ? focusRef : undefined}
                                type="button"
                                className="drama-flow-asset"
                                aria-pressed={asset.id === selectedId}
                                data-bad={bad || undefined}
                                onClick={() => onSelect(asset.id === selectedId ? undefined : asset.id)}
                            >
                                <span className="drama-flow-asset-art">{preview ? <img src={preview} alt="" loading="lazy" /> : <span>{view?.text || "没有图"}</span>}</span>
                                <span className="drama-flow-asset-body">
                                    <strong title={asset.title}>{asset.title}</strong>
                                    <span className="drama-flow-asset-meta">
                                        {view ? (
                                            <span className="drama-flow-chip" data-tone={view.tone}>
                                                {view.text}
                                            </span>
                                        ) : null}
                                        <span>v{Math.max(1, asset.character?.version ?? asset.versionCount)}</span>
                                        <span>{asset.usages.length} 处引用</span>
                                        {subs.length ? <span>子卡 {subs.length}</span> : null}
                                    </span>
                                    {bad ? (
                                        <span className="drama-flow-asset-error">{lookAssetError(asset) || (status === "stale" ? "出图期间换了版本，没绑上" : "出图失败，没写原因")}</span>
                                    ) : description ? (
                                        <span className="drama-flow-asset-desc">{description}</span>
                                    ) : null}
                                </span>
                            </button>
                        );
                    })}
                </div>
            ) : (
                <div className="drama-flow-empty">
                    <strong>还没有{label}</strong>
                    <span>提取资产后会出现在这里；右上角可以按当前这一集提取。</span>
                </div>
            )}
        </StageLayout>
    );
}

function AssetPane({ project, asset, fourPanel, retrying, onRetry, onClose }: { project: Project; asset: ProjectAsset; fourPanel: boolean; retrying: boolean; onRetry: () => void; onClose: () => void }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [picking, setPicking] = useState(false);
    const status = dramaImageStatusOf(asset);
    const view = status ? IMAGE_STATUS[status] : undefined;
    const preview = assetPreviewUrl(asset);
    const bind = useMutation({
        mutationFn: async ({ resourceId, sourceAssetId }: { resourceId: string; sourceAssetId: string }) => {
            if (asset.visual) {
                if (!asset.primaryVersionId) throw new Error("资产版本没读到，请刷新");
                return bindProjectAssetImage(project.id, asset.id, { resourceId, expectedVersionId: asset.primaryVersionId });
            }
            return replaceProjectCharacterRepresentations(project.id, asset.id, [
                { role: "turnaround_sheet", resourceId, metadata: { sourceAssetId } },
                { role: "primary", resourceId, metadata: { source: "turnaround_sheet", sourceAssetId } },
            ]);
        },
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: ["project", project.id] });
            setPicking(false);
            message.success("图片已换上，存成了新版本");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "换图没成功"),
    });
    return (
        <DetailPane
            title={asset.title}
            subtitle={`${ASSET_CATEGORY_LABELS[asset.category] || asset.category} · v${Math.max(1, asset.character?.version ?? asset.versionCount)}${view ? ` · ${view.text}` : ""}`}
            onClose={onClose}
            actions={
                <>
                    <Button size="small" type={isBad(status) ? "primary" : "default"} icon={<RotateCw className="size-3.5" />} loading={retrying} onClick={onRetry}>
                        {isBad(status) ? "重试" : status === "bound" ? "重出" : "出图"}
                    </Button>
                    {asset.visual || asset.character ? (
                        <Button size="small" icon={<ImageUp className="size-3.5" />} onClick={() => setPicking(true)}>
                            换图
                        </Button>
                    ) : null}
                </>
            }
        >
            <div className="drama-flow-pane-media">{preview ? <img src={preview} alt={asset.title} /> : <span>{view?.text || "没有图"}</span>}</div>
            {isBad(status) ? (
                <PaneSection label="失败原因">
                    <p data-tone="bad">{lookAssetError(asset) || "没写原因"}</p>
                </PaneSection>
            ) : null}
            {assetFields(asset).map((field) => (
                <PaneSection key={field.label} label={field.label}>
                    {field.value}
                </PaneSection>
            ))}
            <PaneSection label={`引用（${asset.usages.length}）`} empty="还没有段用到它">
                {asset.usages.length ? (
                    <ul className="drama-flow-pane-list">
                        {asset.usages.map((usage) => (
                            <li key={usage}>{usage}</li>
                        ))}
                    </ul>
                ) : undefined}
            </PaneSection>
            {asset.category === "character" ? (
                <PaneSection label="子卡（服装、状态）">
                    <ProjectCharacterVariants projectId={project.id} parent={asset} defaultImageModel={project.defaultImageModel} fourPanel={fourPanel} />
                </PaneSection>
            ) : null}
            <AssetImageLibraryPicker open={picking} title={asset.title} onClose={() => setPicking(false)} onBind={(image) => bind.mutateAsync(image)} />
        </DetailPane>
    );
}
