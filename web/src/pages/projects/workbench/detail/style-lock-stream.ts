/**
 * 风格锁定的流式视图：服务端一个任务一轮，模型一行一个 JSON（style.lock@1：第一行风格锚，之后每组一行色板 {"group": "主角组", "colors": [...]}），
 * 回合标记（{"@turn": {...}}）与其他步骤同一种；重试时会再出一行标记，见到就把之前收到的半截输出清掉。只用于显示，候选以任务结果为准。
 */

import type { DramaMasterPalette, DramaMasterPaletteGroupKey, DramaPaletteColor, DramaStyleAnchor } from "@/services/api/project-drama";

import { SHOT_TURN_MARKER } from "./chapter-shot-stream";

export type StyleLockStreamState = {
    anchor?: DramaStyleAnchor;
    palette: DramaMasterPalette;
    attempt: number;
    colorCount: number;
};

/** 固定分组的顺序和中文名（与后端 MasterPaletteGroupDefs 一致）。 */
export const MASTER_PALETTE_GROUPS: Array<{ key: DramaMasterPaletteGroupKey; name: string }> = [
    { key: "protagonist", name: "主角组" },
    { key: "supporting", name: "配角组" },
    { key: "props", name: "道具组" },
    { key: "environment", name: "环境组" },
];

function record(value: unknown): Record<string, unknown> | undefined {
    return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;
}

function jsonLine(line: string): Record<string, unknown> | undefined {
    if (!line.startsWith("{")) return undefined;
    try {
        return record(JSON.parse(line.replace(/,$/, "")));
    } catch {
        return undefined;
    }
}

/** 模型写的组名认成固定分组（与后端 MasterPaletteGroupKey 同一套关键词）。 */
export function masterPaletteGroupKey(name: string): DramaMasterPaletteGroupKey {
    const text = name.trim().toLowerCase();
    if (/主角|主演|protagonist|lead/.test(text)) return "protagonist";
    if (/配角|群演|supporting/.test(text)) return "supporting";
    if (/道具|prop/.test(text)) return "props";
    if (/环境|场景|空间|environment|scene/.test(text)) return "environment";
    return "other";
}

function colors(value: unknown): DramaPaletteColor[] {
    const list = Array.isArray(value) ? value : [];
    return list
        .map((item) => (typeof item === "string" ? { hex: item } : record(item)))
        .filter((item): item is Record<string, unknown> => Boolean(item))
        .map((item) => {
            const hex = Array.isArray(item.hex) ? String(item.hex[0] ?? "") : String(item.hex ?? "");
            const normalized = hex.trim().toUpperCase();
            return { name_zh: String(item.name_zh ?? ""), name_en: String(item.name_en ?? ""), hex: /^[0-9A-F]{6}$/.test(normalized) ? `#${normalized}` : normalized, usage: String(item.usage ?? "") };
        });
}

/** 同一分组合并，按固定顺序排（认不出的排最后），空组去掉。 */
export function normalizeMasterPalette(groups: Array<{ key?: string; name: string; colors: DramaPaletteColor[] }>): DramaMasterPalette {
    const merged: DramaMasterPalette["groups"] = [];
    for (const group of groups) {
        const key = (MASTER_PALETTE_GROUPS.some((item) => item.key === group.key) ? group.key : masterPaletteGroupKey(group.name)) as DramaMasterPaletteGroupKey;
        const name = MASTER_PALETTE_GROUPS.find((item) => item.key === key)?.name || group.name || "其他";
        const existing = merged.find((item) => item.key === key && item.name === name);
        if (existing) existing.colors.push(...group.colors);
        else merged.push({ key, name, colors: [...group.colors] });
    }
    const rank = (key: string) => {
        const index = MASTER_PALETTE_GROUPS.findIndex((item) => item.key === key);
        return index < 0 ? MASTER_PALETTE_GROUPS.length : index;
    };
    return { groups: merged.filter((group) => group.colors.length).sort((a, b) => rank(a.key) - rank(b.key)) };
}

/** 解析文本流：回合标记重开（清掉重试前的半截输出），风格锚单独记下，带 colors 的行是一组，整张色板写在一行也认。 */
export function parseStyleLockStream(text: string): StyleLockStreamState {
    let anchor: DramaStyleAnchor | undefined;
    let groups: Array<{ name: string; colors: DramaPaletteColor[] }> = [];
    let attempt = 1;
    for (const raw of text.split("\n")) {
        const parsed = jsonLine(raw.trim());
        if (!parsed) continue;
        const marker = record(parsed[SHOT_TURN_MARKER]);
        if (marker) {
            attempt = Number(marker.attempt) || 1;
            anchor = undefined;
            groups = [];
            continue;
        }
        const wrapped = record(parsed.style_anchor);
        if (wrapped) anchor = wrapped as DramaStyleAnchor;
        else if (!Array.isArray(parsed.colors) && ["medium", "art_style", "lighting", "grading"].filter((key) => key in parsed).length >= 2) anchor = parsed as DramaStyleAnchor;
        const whole = record(parsed.master_palette)?.groups ?? parsed.master_palette;
        if (Array.isArray(whole)) {
            for (const item of whole) {
                const group = record(item);
                if (group) groups.push({ name: String(group.group ?? group.name ?? ""), colors: colors(group.colors) });
            }
        }
        if (Array.isArray(parsed.colors)) groups.push({ name: String(parsed.group ?? parsed.name ?? ""), colors: colors(parsed.colors) });
    }
    const palette = normalizeMasterPalette(groups);
    return { anchor, palette, attempt, colorCount: palette.groups.reduce((sum, group) => sum + group.colors.length, 0) };
}
