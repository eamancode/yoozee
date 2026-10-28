import { Button, Dropdown, Popconfirm, Tooltip } from "antd";
import { MoveRight, Palette, RefreshCw, Trash2 } from "lucide-react";

import { AssetLibraryCard, AssetLibraryCardMedia } from "@/components/assets/asset-library-card";
import { CachedResourceImage } from "@/components/cached-resource-image";
import { MasterPaletteStrip, styleAnchorBrief } from "@/components/drama/master-palette";
import { masterPaletteColors } from "@/services/api/drama";
import type { DramaPaletteColor } from "@/services/api/drama";
import type { ProjectAsset, ProjectAssetPalette } from "@/services/api/projects";
import { resourceFileUrl, resourceIdFromStorageKey } from "@/services/api/resources";

/** 主基调色卡的来源。 */
export const MASTER_PALETTE_SOURCE_LABELS: Record<string, string> = { analysis: "风格分析", library: "风格库", preset: "按画风生成" };

/** 场次色卡颜色的分组（与主基调色卡同一套）：按这个顺序显示，没写分组的归「其他」。 */
const PALETTE_GROUPS: Array<{ key: string; label: string }> = [
    { key: "protagonist", label: "主角组" },
    { key: "supporting", label: "配角组" },
    { key: "props", label: "道具组" },
    { key: "environment", label: "环境组" },
    { key: "other", label: "其他" },
];

export function paletteImageStatusLabel(palette: ProjectAssetPalette) {
    if (palette.master) return palette.imageStatus === "bound" ? "代码合成" : "图没绑上";
    if (palette.sceneGone) return "场次已不存在";
    if (palette.kind !== "scene") return "旧版色卡";
    return palette.imageStatus === "bound" ? "代码合成" : "图没合成上";
}

/** 一场的颜色按分组排好（承接色在前，组内保持色彩脚本的顺序）。 */
export function groupedPaletteColors(colors: DramaPaletteColor[]) {
    const known = new Set(PALETTE_GROUPS.map((group) => group.key));
    return PALETTE_GROUPS.map((group) => ({ ...group, colors: colors.filter((color) => (known.has(String(color.group || "")) ? color.group : "other") === group.key) })).filter((group) => group.colors.length);
}

/** 一排色块：色块、中文名、HEX、用途（悬停看全）。 */
export function PaletteSwatches({ colors, size = "sm" }: { colors: DramaPaletteColor[]; size?: "sm" | "md" }) {
    return (
        <ul className="grid grid-cols-[repeat(auto-fill,minmax(5.5rem,1fr))] gap-1.5 pl-0" aria-label="色块">
            {colors.map((color, index) => (
                <li key={`${color.hex}-${index}`} className="list-none">
                    <Tooltip title={[color.name_en, color.usage].filter(Boolean).join(" · ") || undefined}>
                        <div className="min-w-0">
                            <div className={size === "md" ? "h-10 rounded border border-border/70" : "h-6 rounded border border-border/70"} style={{ backgroundColor: color.hex }} />
                            <div className="mt-0.5 truncate text-[var(--fs-micro)] text-foreground/75">{color.name_zh || color.name_en}</div>
                            <div className="truncate font-mono text-[var(--fs-micro)] text-foreground/45">{color.hex}</div>
                            {size === "md" && color.usage ? <div className="truncate text-[var(--fs-micro)] text-foreground/45">{color.usage}</div> : null}
                        </div>
                    </Tooltip>
                </li>
            ))}
        </ul>
    );
}

/** 场次色卡的色板按分组显示：组名一行，下面一排色块；没写分组的旧色卡整排显示。 */
export function GroupedPaletteSwatches({ colors, size = "sm" }: { colors: DramaPaletteColor[]; size?: "sm" | "md" }) {
    const groups = groupedPaletteColors(colors);
    if (groups.length <= 1 && !colors.some((color) => color.group)) return <PaletteSwatches colors={colors} size={size} />;
    return (
        <div className="grid gap-2">
            {groups.map((group) => (
                <section key={group.key} aria-label={group.label}>
                    <div className="mb-1 flex items-baseline gap-2 text-[var(--fs-tiny)]">
                        <span className="font-medium text-foreground/70">{group.label}</span>
                        <span className="tabular-nums text-foreground/40">{group.colors.length} 色</span>
                    </div>
                    <PaletteSwatches colors={group.colors} size={size} />
                </section>
            ))}
        </div>
    );
}

/** 卡片上的分组色带：每组一行，组名在左，右边一条等宽色块（悬停看色名、HEX、用途）；没写分组的旧色卡整条显示。 */
function GroupedPaletteStrip({ colors }: { colors: DramaPaletteColor[] }) {
    const groups = colors.some((color) => color.group) ? groupedPaletteColors(colors) : [{ key: "all", label: "", colors }];
    return (
        <div className="grid gap-1" aria-label={`${colors.length} 色`}>
            {groups.map((group) => (
                <div key={group.key} className="flex items-center gap-2">
                    {group.label ? <span className="w-9 shrink-0 text-[var(--fs-micro)] text-foreground/50">{group.label.replace("组", "")}</span> : null}
                    <div className="flex h-4 min-w-0 flex-1 overflow-hidden rounded-sm border border-border/60">
                        {group.colors.map((color, index) => (
                            <Tooltip key={`${color.hex}-${index}`} title={[`${color.name_zh || color.name_en} ${color.hex}`, color.usage].filter(Boolean).join(" · ")}>
                                <span className="min-w-0 flex-1" style={{ backgroundColor: color.hex }} />
                            </Tooltip>
                        ))}
                    </div>
                </div>
            ))}
        </div>
    );
}

/** 资产页的场次色卡卡片：代码合成的色卡图（没合成上时按色彩脚本画色条）、按组的色块、「重新合成」（只重画图，不调模型）。 */
export function ProjectPaletteCard({
    asset,
    resynthesizing,
    busy,
    folderItems,
    onOpen,
    onResynthesize,
    onMove,
    onRemove,
}: {
    asset: ProjectAsset;
    resynthesizing: boolean;
    busy: boolean;
    folderItems: Array<{ key: string; label: string }>;
    onOpen: () => void;
    onResynthesize: () => void;
    onMove: (folderId: string) => void;
    onRemove: () => void;
}) {
    const palette = asset.palette;
    const resourceId = asset.mediaType === "image" ? resourceIdFromStorageKey(asset.storageKey) : "";
    if (palette?.master) return <MasterPaletteCard asset={asset} resourceId={resourceId} busy={busy} folderItems={folderItems} onOpen={onOpen} onMove={onMove} onRemove={onRemove} />;
    const colors = palette ? [...(palette.entry?.shared_colors || []), ...(palette.entry?.new_colors || [])] : [];
    const gone = Boolean(palette?.sceneGone);
    const legacy = Boolean(palette && palette.kind !== "scene");
    const missingImage = Boolean(palette && !gone && !legacy && palette.imageStatus !== "bound");
    return (
        <AssetLibraryCard className="project-asset-library-card">
            <AssetLibraryCardMedia className="relative aspect-video overflow-hidden bg-foreground/[.05]">
                <button type="button" className="project-asset-media-button" onClick={onOpen} aria-label={`查看色卡：${asset.title}`}>
                    {resourceId ? (
                        <CachedResourceImage storageKey={asset.storageKey} src={resourceFileUrl(resourceId)} alt={asset.title} className="h-full w-full object-contain" />
                    ) : colors.length ? (
                        <div className="flex h-full w-full items-stretch gap-1 bg-background p-[10%]" aria-hidden>
                            {colors.map((color, index) => (
                                <span key={`${color.hex}-${index}`} className="min-w-0 flex-1 rounded-sm border border-border/60" style={{ backgroundColor: color.hex }} />
                            ))}
                        </div>
                    ) : (
                        <div className="grid h-full place-content-center text-foreground/40">
                            <Palette className="size-9" />
                        </div>
                    )}
                </button>
                <div className="pointer-events-none absolute inset-x-2 top-2 flex justify-between gap-2 text-[var(--fs-micro)] text-white">
                    <span className="rounded bg-black/60 px-1.5 py-0.5">场次色卡 · v{Math.max(1, asset.versionCount)}</span>
                    {palette ? <span className={missingImage ? "rounded bg-[var(--status-error)] px-1.5 py-0.5" : "rounded bg-black/60 px-1.5 py-0.5"}>{paletteImageStatusLabel(palette)}</span> : null}
                </div>
            </AssetLibraryCardMedia>
            <div className="p-3">
                <button type="button" className="w-full truncate text-left text-sm font-semibold" onClick={onOpen}>
                    {asset.title}
                </button>
                {palette ? (
                    <>
                        {gone ? <p className="mt-1 text-[var(--fs-tiny)] leading-[18px] text-amber-600 dark:text-amber-400">当前剧集里没有这一场了：不再合成、不绑镜头，不计入「色卡齐了」，用不上可以移出。</p> : null}
                        {legacy && !gone ? <p className="mt-1 text-[var(--fs-tiny)] leading-[18px] text-amber-600 dark:text-amber-400">旧版色卡（图片模型出图）：不绑镜头、不作视频参考，章节页「生成场次色卡」后换成代码合成的场次色卡。</p> : null}
                        {palette.note ? <p className="mt-1 text-[var(--fs-tiny)] leading-[18px] text-foreground/50">{palette.note}</p> : null}
                        <p className="mt-1 truncate text-[var(--fs-tiny)] text-foreground/50">{palette.design}</p>
                        <div className="mt-2">
                            <GroupedPaletteStrip colors={colors} />
                        </div>
                    </>
                ) : (
                    <p className="mt-2 text-[var(--fs-label)] text-foreground/55">色彩脚本没读到，请刷新</p>
                )}
                <div className="mt-3 flex flex-wrap items-center gap-1 border-t border-border/60 pt-2">
                    <Tooltip title={legacy ? "旧版色卡要先在章节页重新生成场次色卡" : "按当前色板重画色卡图（不调模型），镜头上的色卡跟到新版本"}>
                        <Button size="small" icon={<RefreshCw className="size-3.5" />} loading={resynthesizing} disabled={busy || !palette || gone || legacy} onClick={onResynthesize}>
                            重新合成
                        </Button>
                    </Tooltip>
                    <Dropdown trigger={["click"]} menu={{ selectedKeys: [asset.folderId || ""], items: folderItems, onClick: ({ key }) => onMove(key) }}>
                        <Button type="text" size="small" disabled={busy} icon={<MoveRight className="size-3.5" />} aria-label={`移动 ${asset.title}`} />
                    </Dropdown>
                    <Popconfirm title="移出项目资产？" description="重跑场次色卡会为这一场重新建一张色卡。" okText="移出" cancelText="取消" onConfirm={onRemove}>
                        <Button type="text" danger size="small" disabled={resynthesizing} loading={busy} icon={<Trash2 className="size-3.5" />} aria-label={`移出 ${asset.title}`} />
                    </Popconfirm>
                </div>
            </div>
        </AssetLibraryCard>
    );
}

/** 资产页的主基调色卡卡片：合成的色卡图（没绑上时画色带）、分组色带、来源；图由代码按色值合成，没有出图按钮。 */
function MasterPaletteCard({
    asset,
    resourceId,
    busy,
    folderItems,
    onOpen,
    onMove,
    onRemove,
}: {
    asset: ProjectAsset;
    resourceId: string;
    busy: boolean;
    folderItems: Array<{ key: string; label: string }>;
    onOpen: () => void;
    onMove: (folderId: string) => void;
    onRemove: () => void;
}) {
    const master = asset.palette?.master;
    const count = masterPaletteColors(master?.palette).length;
    return (
        <AssetLibraryCard className="project-asset-library-card">
            <AssetLibraryCardMedia className="relative aspect-video overflow-hidden bg-foreground/[.05]">
                <button type="button" className="project-asset-media-button" onClick={onOpen} aria-label={`查看主基调色卡：${asset.title}`}>
                    {resourceId ? (
                        <CachedResourceImage storageKey={asset.storageKey} src={resourceFileUrl(resourceId)} alt={asset.title} className="h-full w-full object-contain" />
                    ) : (
                        <div className="h-full w-full bg-background p-[10%]">
                            <MasterPaletteStrip palette={master?.palette} className="h-full" />
                        </div>
                    )}
                </button>
                <div className="pointer-events-none absolute inset-x-2 top-2 flex justify-between gap-2 text-[var(--fs-micro)] text-white">
                    <span className="rounded bg-black/60 px-1.5 py-0.5">主基调色卡 · v{Math.max(1, asset.versionCount)}</span>
                    <span className="rounded bg-black/60 px-1.5 py-0.5">{MASTER_PALETTE_SOURCE_LABELS[master?.source || ""] || "已锁定"}</span>
                </div>
            </AssetLibraryCardMedia>
            <div className="p-3">
                <button type="button" className="w-full truncate text-left text-sm font-semibold" onClick={onOpen}>
                    {asset.title}
                </button>
                <p className="mt-1 truncate text-[var(--fs-tiny)] text-foreground/50">{[`${master?.palette.groups.length || 0} 组 ${count} 色`, styleAnchorBrief(master?.styleAnchor)].filter(Boolean).join(" · ")}</p>
                <MasterPaletteStrip palette={master?.palette} className="mt-2 h-4" />
                <div className="mt-3 flex flex-wrap items-center gap-1 border-t border-border/60 pt-2">
                    <span className="mr-auto text-[var(--fs-tiny)] text-foreground/45">图由代码按色值合成</span>
                    <Dropdown trigger={["click"]} menu={{ selectedKeys: [asset.folderId || ""], items: folderItems, onClick: ({ key }) => onMove(key) }}>
                        <Button type="text" size="small" disabled={busy} icon={<MoveRight className="size-3.5" />} aria-label={`移动 ${asset.title}`} />
                    </Dropdown>
                    <Popconfirm title="移出项目资产？" description="项目锁定的风格不变；重新锁定会再建一张主色卡。" okText="移出" cancelText="取消" onConfirm={onRemove}>
                        <Button type="text" danger size="small" loading={busy} icon={<Trash2 className="size-3.5" />} aria-label={`移出 ${asset.title}`} />
                    </Popconfirm>
                </div>
            </div>
        </AssetLibraryCard>
    );
}
