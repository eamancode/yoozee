import { Button, Dropdown, Popconfirm, Tooltip } from "antd";
import { MoveRight, Palette, Sparkles, Trash2 } from "lucide-react";

import { AssetLibraryCard, AssetLibraryCardMedia } from "@/components/assets/asset-library-card";
import { CachedResourceImage } from "@/components/cached-resource-image";
import type { DramaPaletteColor } from "@/services/api/drama";
import type { PaletteCheck, ProjectAsset, ProjectAssetPalette } from "@/services/api/projects";
import { resourceFileUrl, resourceIdFromStorageKey } from "@/services/api/resources";

const IMAGE_STATUS_LABELS: Record<ProjectAssetPalette["imageStatus"], string> = {
    none: "还没出图",
    waiting: "等空位出图",
    queued: "出图中",
    bound: "已出图",
    stale: "图没绑上",
    failed: "出图失败",
};

export function paletteImageStatusLabel(palette: ProjectAssetPalette) {
    if (palette.sceneGone) return "场次已不存在";
    return IMAGE_STATUS_LABELS[palette.imageStatus] || palette.imageStatus;
}

/** 取色校验的一句话：通过、哪几块偏了，或没法校验的原因。 */
export function paletteCheckSummary(check?: PaletteCheck) {
    if (!check) return "";
    if (check.note) return `取色校验没做：${check.note}`;
    if (check.passed) return `取色校验通过（每块 ΔE ≤ ${check.threshold}）`;
    const off = check.blocks.map((block, index) => ({ ...block, index })).filter((block) => block.deltaE < 0 || block.deltaE > check.threshold);
    return `取色偏差：${off.map((block) => `第 ${block.index + 1} 块 ${block.hex} 取到 ${block.sampled}（ΔE ${block.deltaE.toFixed(1)}）`).join("；")}`;
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

/** 资产页的色卡卡片：色卡图（没出图时按色彩脚本画色条）、色块、取色校验、出图状态和重新出图。 */
export function ProjectPaletteCard({
    asset,
    generating,
    busy,
    folderItems,
    onOpen,
    onGenerate,
    onMove,
    onRemove,
}: {
    asset: ProjectAsset;
    generating: boolean;
    busy: boolean;
    folderItems: Array<{ key: string; label: string }>;
    onOpen: () => void;
    onGenerate: () => void;
    onMove: (folderId: string) => void;
    onRemove: () => void;
}) {
    const palette = asset.palette;
    const colors = palette ? [...palette.entry.shared_colors, ...palette.entry.new_colors] : [];
    const resourceId = asset.mediaType === "image" ? resourceIdFromStorageKey(asset.storageKey) : "";
    const queued = generating || palette?.imageStatus === "queued";
    const check = palette?.check;
    const warn = Boolean(check && !check.passed);
    const gone = Boolean(palette?.sceneGone);
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
                    <span className="rounded bg-black/60 px-1.5 py-0.5">色卡 · v{Math.max(1, asset.versionCount)}</span>
                    {palette ? (
                        <span className={!gone && (palette.imageStatus === "failed" || warn) ? "rounded bg-[var(--status-error)] px-1.5 py-0.5" : "rounded bg-black/60 px-1.5 py-0.5"}>
                            {!gone && warn && palette.imageStatus === "bound" ? "取色有偏差" : paletteImageStatusLabel(palette)}
                        </span>
                    ) : null}
                </div>
            </AssetLibraryCardMedia>
            <div className="p-3">
                <button type="button" className="w-full truncate text-left text-sm font-semibold" onClick={onOpen}>
                    {asset.title}
                </button>
                {palette ? (
                    <>
                        {gone ? <p className="mt-1 text-[var(--fs-tiny)] leading-[18px] text-amber-600 dark:text-amber-400">当前剧集里已经没有这一场，色卡保留但不再出图、不计入「色卡齐了」；用不上可以移出项目。</p> : null}
                        <p className="mt-1 truncate text-[var(--fs-tiny)] text-foreground/50">{palette.design}</p>
                        <div className="mt-2">
                            <PaletteSwatches colors={colors} />
                        </div>
                        {check ? <p className={warn ? "mt-2 text-[var(--fs-tiny)] leading-[18px] text-amber-600 dark:text-amber-400" : "mt-2 text-[var(--fs-tiny)] leading-[18px] text-foreground/50"}>{paletteCheckSummary(check)}</p> : null}
                        {palette.imageError ? <p className="mt-2 text-[var(--fs-tiny)] leading-[18px] text-red-600 dark:text-red-400">{palette.imageError}</p> : null}
                    </>
                ) : (
                    <p className="mt-2 text-[var(--fs-label)] text-foreground/55">色彩脚本没读到，请刷新</p>
                )}
                <div className="mt-3 flex flex-wrap items-center gap-1 border-t border-border/60 pt-2">
                    <Button size="small" icon={<Sparkles className="size-3.5" />} loading={queued} disabled={busy || !palette || gone} onClick={onGenerate}>
                        {palette?.imageStatus === "waiting" ? "继续出图" : resourceId ? "重新出图" : "出图"}
                    </Button>
                    <Dropdown trigger={["click"]} menu={{ selectedKeys: [asset.folderId || ""], items: folderItems, onClick: ({ key }) => onMove(key) }}>
                        <Button type="text" size="small" disabled={busy} icon={<MoveRight className="size-3.5" />} aria-label={`移动 ${asset.title}`} />
                    </Dropdown>
                    <Popconfirm title="移出项目资产？" description="重跑导演风格会为这一场重新建一张色卡。" okText="移出" cancelText="取消" onConfirm={onRemove}>
                        <Button type="text" danger size="small" disabled={queued} loading={busy} icon={<Trash2 className="size-3.5" />} aria-label={`移出 ${asset.title}`} />
                    </Popconfirm>
                </div>
            </div>
        </AssetLibraryCard>
    );
}
