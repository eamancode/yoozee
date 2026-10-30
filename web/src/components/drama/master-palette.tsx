import { Tooltip } from "antd";

import type { DramaMasterPalette, DramaPaletteColor, DramaStyleAnchor } from "@/services/api/project-drama";

const ANCHOR_FIELDS: Array<[keyof DramaStyleAnchor, string]> = [
    ["medium", "画风 / 媒介"],
    ["art_style", "美术风格"],
    ["lighting", "光线政策"],
    ["camera", "镜头语言"],
    ["grading", "调色倾向"],
    ["era", "时代"],
];

/** 风格锚的逐项摘要（场次色卡、风格锁定、风格库共用）。 */
export function StyleAnchorSummary({ anchor }: { anchor?: DramaStyleAnchor }) {
    if (!anchor) return null;
    const rows = ANCHOR_FIELDS.filter(([key]) => typeof anchor[key] === "string" && String(anchor[key]).trim());
    return (
        <dl className="grid gap-1 rounded-md border border-border/60 px-2.5 py-1.5 text-xs leading-5">
            {rows.map(([key, label]) => (
                <div key={key} className="grid grid-cols-[5rem_1fr] gap-2">
                    <dt className="text-foreground/45">{label}</dt>
                    <dd className="m-0 text-foreground/80">{String(anchor[key])}</dd>
                </div>
            ))}
            {anchor.forbidden?.length ? (
                <div className="grid grid-cols-[5rem_1fr] gap-2">
                    <dt className="text-foreground/45">禁用</dt>
                    <dd className="m-0 text-foreground/80">{anchor.forbidden.join("、")}</dd>
                </div>
            ) : null}
        </dl>
    );
}

/** 风格锚的一句话（卡片摘要）：媒介、美术风格、调色。 */
export function styleAnchorBrief(anchor?: DramaStyleAnchor) {
    return [anchor?.medium, anchor?.art_style, anchor?.grading]
        .map((item) => item?.trim())
        .filter(Boolean)
        .join(" · ");
}

function Swatch({ color }: { color: DramaPaletteColor }) {
    return (
        <Tooltip title={[color.name_en, color.usage].filter(Boolean).join(" · ") || undefined}>
            <div className="min-w-0">
                <div className="h-8 rounded border border-border/70" style={{ backgroundColor: color.hex }} />
                <div className="mt-0.5 truncate text-[var(--fs-micro)] text-foreground/75">{color.name_zh || color.name_en}</div>
                <div className="truncate font-mono text-[var(--fs-micro)] text-foreground/45">{color.hex}</div>
            </div>
        </Tooltip>
    );
}

/** 主基调色卡按组显示：组名一行，下面一排色块（色名、HEX，悬停看英文名和用途）。 */
export function MasterPaletteGroups({ palette }: { palette?: DramaMasterPalette }) {
    const groups = (palette?.groups || []).filter((group) => group.colors?.length);
    if (!groups.length) return <p className="text-xs text-foreground/45">还没有颜色</p>;
    return (
        <div className="grid gap-2.5">
            {groups.map((group, index) => (
                <section key={`${group.key}-${group.name}-${index}`} aria-label={group.name}>
                    <div className="mb-1 flex items-baseline gap-2 text-xs">
                        <span className="font-medium text-foreground/80">{group.name}</span>
                        <span className="tabular-nums text-foreground/40">{group.colors.length} 色</span>
                    </div>
                    <div className="grid grid-cols-[repeat(auto-fill,minmax(4.75rem,1fr))] gap-1.5">
                        {group.colors.map((color, colorIndex) => (
                            <Swatch key={`${color.hex}-${colorIndex}`} color={color} />
                        ))}
                    </div>
                </section>
            ))}
        </div>
    );
}

/** 一条色带（卡片缩略）：全部颜色等宽排开。 */
export function MasterPaletteStrip({ palette, className = "h-3" }: { palette?: DramaMasterPalette; className?: string }) {
    const colors = (palette?.groups || []).flatMap((group) => group.colors || []);
    if (!colors.length) return null;
    return (
        <div className={`flex overflow-hidden rounded-sm border border-border/60 ${className}`} aria-label={`${colors.length} 色主基调色卡`}>
            {colors.map((color, index) => (
                <span key={`${color.hex}-${index}`} className="min-w-0 flex-1" style={{ backgroundColor: color.hex }} title={`${color.name_zh || color.name_en} ${color.hex}`} />
            ))}
        </div>
    );
}
