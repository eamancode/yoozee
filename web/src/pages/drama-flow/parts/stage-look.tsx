import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button } from "antd";

import { StyleAnchorSummary } from "@/components/drama/master-palette";
import { AssetsExtractControl } from "@/pages/projects/workbench/detail/chapter-assets-extract";
import { ScenePaletteControl } from "@/pages/projects/workbench/detail/chapter-scene-palette";
import { projectLockedStyle, StyleLockControl } from "@/pages/projects/workbench/detail/chapter-style-lock";
import { StyleBibleEditor } from "@/pages/projects/workbench/detail/drama-style-bible-editor";
import { DramaAssetChecklist, dramaBibleKey } from "@/pages/projects/workbench/detail/drama-panels";
import { dramaImageStatusOf } from "@/pages/projects/workbench/detail/drama-image-summary";
import { GroupedPaletteSwatches, MASTER_PALETTE_SOURCE_LABELS, ProjectPaletteCard } from "@/pages/projects/workbench/detail/project-palette-card";
import { dramaScenesKey, getDramaScenes, putDramaBible, resynthesizeDramaPalettes, type DramaBible } from "@/services/api/project-drama";
import { unlinkProjectAsset, type ProjectAsset } from "@/services/api/projects";

import type { FlowData } from "../hooks/use-flow-data";
import type { FlowStageView } from "../model/stages";
import { LookAssets } from "./look-assets";
import { ReadError, StageLayout, StageTabs, SummaryRow, type StageTab } from "./layout";

export { lookAssetError } from "./look-assets";

type LookTab = "style" | "palette" | "character" | "environment" | "prop" | "checklist";
const LOOK_TABS: LookTab[] = ["style", "palette", "character", "environment", "prop", "checklist"];

/** 定妆：画风、色卡、角色、场景、道具、清单核对，各一节；每节把产物和细节都摆出来，坏了的就地重试。 */
export function StageLook({
    data,
    stage,
    tab,
    selectedId,
    retrying,
    onTab,
    onSelect,
    onRetry,
}: {
    data: FlowData;
    stage?: FlowStageView;
    tab?: string;
    selectedId?: string;
    retrying: Set<string>;
    onTab: (tab: LookTab) => void;
    onSelect: (id?: string) => void;
    onRetry: (assets: ProjectAsset[]) => void;
}) {
    const { project, detail, assets, assetsQuery, bible, bibleQuery, projectTasks, chapters, episodeUnits, unit, allUnits, rail, dramaRule } = data;
    const active: LookTab = LOOK_TABS.includes(tab as LookTab) ? (tab as LookTab) : "style";
    const scenesQuery = useQuery({
        queryKey: [...dramaScenesKey(project?.id || "", unit?.id), unit?.updatedAt || "", dramaRule?.maxSeconds ?? 0, dramaRule?.minSeconds ?? 0],
        queryFn: ({ signal }) => getDramaScenes(project!.id, unit!.id, signal, dramaRule),
        enabled: Boolean(project && unit),
    });
    // 资产没读到时小节上不写数字：不能把读失败显示成 0 个。
    const assetsUnread = assetsQuery.isError || !assetsQuery.data;
    const count = (category: string) => (assetsUnread ? undefined : assets.filter((asset) => asset.category === category && !asset.variant).length);
    const bad = (category: string) => assets.some((asset) => asset.category === category && ["failed", "stale"].includes(dramaImageStatusOf(asset) || ""));
    const palettes = assets.filter((asset) => asset.category === "palette");
    const locked = projectLockedStyle(project?.bibleJson);
    const tabs: StageTab<LookTab>[] = [
        { key: "style", label: "画风" },
        { key: "palette", label: "色卡", count: assetsUnread ? undefined : palettes.length },
        { key: "character", label: "角色", count: count("character"), warn: bad("character") },
        { key: "environment", label: "场景", count: count("environment"), warn: bad("environment") },
        { key: "prop", label: "道具", count: count("prop"), warn: bad("prop") },
        { key: "checklist", label: "清单核对" },
    ];
    if (!project || !detail) return null;
    const assetsTab = active === "character" || active === "environment" || active === "prop";

    return (
        <>
            <div className="drama-flow-work-head">
                <div>
                    <h2>定妆</h2>
                    <p>{stage?.state === "running" ? "正在定画风、做色卡、出定妆图；出好一张显示一张。" : "画风、色卡、角色、场景、道具。后面每一段视频都拿这些当参考。"}</p>
                </div>
                <div className="drama-flow-work-actions">
                    <StyleLockControl detail={detail} tasks={projectTasks} />
                    <ScenePaletteControl detail={detail} chapters={chapters} episodes={episodeUnits} tasks={projectTasks} />
                    {unit ? <AssetsExtractControl detail={detail} unit={unit} units={allUnits} paletteReady={scenesQuery.data?.unitId === unit.id ? scenesQuery.data.paletteReady : undefined} unitNoun={rail.noun} tasks={projectTasks} /> : null}
                </div>
            </div>
            <StageTabs<LookTab> tabs={tabs} active={active} onChange={onTab} />
            {active === "style" ? (
                bibleQuery.isError ? (
                    <ReadError what="风格档案" onRetry={() => void bibleQuery.refetch()} />
                ) : (
                    <LookStyle projectId={project.id} bible={bible} locked={Boolean(locked)} />
                )
            ) : (active === "palette" || assetsTab) && assetsQuery.isError ? (
                <ReadError what="资产和色卡" onRetry={() => void assetsQuery.refetch()} />
            ) : (active === "palette" || assetsTab) && !assetsQuery.data ? (
                <p className="drama-flow-muted">正在读资产…</p>
            ) : active === "palette" ? (
                <LookPalettes projectId={project.id} bible={bible} palettes={palettes} />
            ) : assetsTab ? (
                <LookAssets project={project} category={active} assets={assets} selectedId={selectedId} retrying={retrying} onSelect={onSelect} onRetry={onRetry} />
            ) : (
                <StageLayout>
                    <div className="drama-flow-panel">
                        <div className="drama-flow-panel-head">
                            <h3>资产清单核对</h3>
                            <span>剧本里要用的角色、场景、道具有没有漏，每项都要人勾过</span>
                        </div>
                        <DramaAssetChecklist projectId={project.id} />
                    </div>
                </StageLayout>
            )}
        </>
    );
}

function LookStyle({ projectId, bible, locked }: { projectId: string; bible?: DramaBible; locked: boolean }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const [draft, setDraft] = useState<DramaBible | undefined>(undefined);
    const save = useMutation({
        mutationFn: (next: DramaBible) => putDramaBible(projectId, next),
        onSuccess: async () => {
            await Promise.all([queryClient.invalidateQueries({ queryKey: dramaBibleKey(projectId) }), queryClient.invalidateQueries({ queryKey: ["project", projectId, "core"] })]);
            setDraft(undefined);
            message.success("风格锚已保存");
        },
        onError: (error) => message.error(error instanceof Error ? `没保存上，改动还在：${error.message}` : "没保存上，改动还在"),
    });
    const record = bible?.style_lock;
    const anchor = bible?.style_anchor;
    return (
        <StageLayout>
            <SummaryRow
                items={[
                    { label: "状态", value: locked ? "已锁定" : "还没锁定", tone: locked ? "ok" : "warn" },
                    { label: "来源", value: record ? MASTER_PALETTE_SOURCE_LABELS[record.source] || record.source : "—" },
                    { label: "主基调色", value: bible?.master_palette ? `${(bible.master_palette.groups || []).reduce((sum, group) => sum + (group.colors?.length || 0), 0)} 色` : "—" },
                    { label: "场次色彩脚本", value: bible?.color_script?.length ? `${bible.color_script.length} 场` : "—" },
                ]}
            />
            {record?.font_note ? (
                <div className="drama-flow-note" data-tone="warn">
                    主色卡图上没有中文色名：{record.font_note}
                </div>
            ) : null}
            <div className="drama-flow-panel">
                <div className="drama-flow-panel-head">
                    <h3>风格锚</h3>
                    {draft ? (
                        <div className="drama-flow-work-actions">
                            <Button size="small" disabled={save.isPending} onClick={() => setDraft(undefined)}>
                                放弃改动
                            </Button>
                            <Button size="small" type="primary" loading={save.isPending} onClick={() => save.mutate(draft)}>
                                保存
                            </Button>
                        </div>
                    ) : (
                        <span>每一项都可以直接改，离开输入框记进草稿，点保存才写进项目</span>
                    )}
                </div>
                {bible ? (
                    anchor || draft ? (
                        <StyleBibleEditor document={draft || bible} onChange={setDraft} />
                    ) : (
                        <p className="drama-flow-muted">还没有风格锚：右上角「分析项目风格」让导演定，或建项目时从风格库选。</p>
                    )
                ) : (
                    <p className="drama-flow-muted">正在读风格档案…</p>
                )}
            </div>
            {anchor && !draft ? (
                <div className="drama-flow-panel">
                    <div className="drama-flow-panel-head">
                        <h3>当前锁定的风格</h3>
                    </div>
                    <StyleAnchorSummary anchor={anchor} />
                </div>
            ) : null}
        </StageLayout>
    );
}

function LookPalettes({ projectId, bible, palettes }: { projectId: string; bible?: DramaBible; palettes: ProjectAsset[] }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const refresh = () => queryClient.invalidateQueries({ queryKey: ["project", projectId] });
    const resynth = useMutation({
        mutationFn: (asset: ProjectAsset) => resynthesizeDramaPalettes(projectId, { assetIds: [asset.id] }),
        onSuccess: async (result) => {
            await refresh();
            if (result.failed.length) message.warning(`色卡图没合成上：${result.failed.map((item) => item.error).join("；")}`);
            else message.success("色卡图已重新合成");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "重新合成没成功"),
    });
    const remove = useMutation({
        mutationFn: (asset: ProjectAsset) => unlinkProjectAsset(projectId, asset.id),
        onSuccess: async () => {
            await refresh();
            message.success("色卡已移出项目");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "移出没成功"),
    });
    const master = palettes.filter((asset) => asset.palette?.master);
    const scenes = useMemo(() => {
        const byEpisode = new Map<number, ProjectAsset[]>();
        for (const asset of palettes) {
            if (asset.palette?.master) continue;
            const episode = asset.palette?.entry?.episode ?? 0;
            byEpisode.set(episode, [...(byEpisode.get(episode) || []), asset]);
        }
        return [...byEpisode.entries()].sort((left, right) => left[0] - right[0]);
    }, [palettes]);
    const card = (asset: ProjectAsset) => (
        <ProjectPaletteCard
            key={asset.id}
            asset={asset}
            folderItems={[]}
            resynthesizing={resynth.isPending && resynth.variables?.id === asset.id}
            busy={remove.isPending && remove.variables?.id === asset.id}
            onOpen={() => undefined}
            onResynthesize={() => resynth.mutate(asset)}
            onMove={() => undefined}
            onRemove={() => remove.mutate(asset)}
        />
    );
    const gone = palettes.filter((asset) => asset.palette?.sceneGone).length;
    const missing = palettes.filter((asset) => asset.palette && !asset.palette.master && !asset.palette.sceneGone && asset.palette.imageStatus !== "bound").length;
    return (
        <StageLayout>
            <SummaryRow
                items={[
                    { label: "主基调色卡", value: master.length ? "有" : "没有", tone: master.length ? "ok" : "warn" },
                    { label: "场次色卡", value: palettes.length - master.length },
                    { label: "没合成上", value: missing, tone: missing ? "bad" : undefined },
                    { label: "场次已不在", value: gone, tone: gone ? "warn" : undefined },
                ]}
            />
            <div className="drama-flow-panel">
                <div className="drama-flow-panel-head">
                    <h3>主基调色卡</h3>
                    <span>按主角组、配角组、道具组、环境组分行；图由代码按色值合成</span>
                </div>
                {master.length ? <div className="drama-flow-asset-grid">{master.map(card)}</div> : <p className="drama-flow-muted">锁定风格后自动生成。</p>}
            </div>
            {scenes.map(([episode, list]) => (
                <div key={episode} className="drama-flow-panel">
                    <div className="drama-flow-panel-head">
                        <h3>{episode ? `第 ${episode} 集场次色卡` : "旧版色卡"}</h3>
                        <span>{list.length} 场</span>
                    </div>
                    <div className="drama-flow-asset-grid">{list.map(card)}</div>
                </div>
            ))}
            {bible?.color_script?.length ? (
                <details className="drama-flow-panel">
                    <summary className="drama-flow-panel-head">
                        <h3>逐场色彩脚本</h3>
                        <span>{bible.color_script.length} 场</span>
                    </summary>
                    <table className="drama-flow-table">
                        <thead>
                            <tr>
                                <th>场</th>
                                <th>场次</th>
                                <th>承接</th>
                                <th>颜色</th>
                                <th>用途</th>
                            </tr>
                        </thead>
                        <tbody>
                            {bible.color_script.map((entry) => (
                                <tr key={`${entry.episode}-${entry.scene_no}`}>
                                    <td>
                                        E{String(entry.episode).padStart(2, "0")}S{String(entry.scene_no).padStart(2, "0")}
                                    </td>
                                    <td>{entry.scene}</td>
                                    <td>{entry.inherits_from || "—"}</td>
                                    <td>
                                        <GroupedPaletteSwatches colors={[...entry.shared_colors, ...entry.new_colors]} />
                                    </td>
                                    <td>{entry.purpose || ""}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </details>
            ) : null}
        </StageLayout>
    );
}
