/**
 * 资产设计的流式视图：服务端在一个任务里逐场续写，模型一个资产一行（assets.extract@1），各场输出和回合标记
 * （{"@turn": {...}}，与生成分段、场次色卡同一种）都追加在同一个任务的 text-events 里。这里按场分组，每行显示分类、名字和一句外观；
 * 同一场重试时会再出一行标记，见到就把这一场之前收到的半截输出清掉。只用于显示，写库在服务端。
 */

import { SHOT_TURN_MARKER } from "./chapter-shot-stream";

export type AssetsStreamCategory = "character" | "environment" | "prop";

export type AssetsStreamRow = {
    category: AssetsStreamCategory | "";
    name: string;
    aliases: string[];
    /** 外观描述（压成一行）；已有资产只补别名时为空。 */
    appearance: string;
    firstScene: number;
};

export type AssetsStreamScene = {
    index: number;
    heading: string;
    attempt: number;
    rows: AssetsStreamRow[];
};

export type AssetsExtractStreamState = {
    total: number;
    current: number;
    scenes: AssetsStreamScene[];
    rowCount: number;
};

const CATEGORY_ALIASES: Record<string, AssetsStreamCategory> = { character: "character", role: "character", environment: "environment", scene: "environment", location: "environment", prop: "prop", item: "prop" };

function record(value: unknown): Record<string, unknown> | undefined {
    return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;
}

function aliases(value: unknown): string[] {
    const items = Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : typeof value === "string" ? value.split(/[、，,；;/]/) : [];
    return items.map((item) => item.trim()).filter(Boolean);
}

function assetRow(parsed: Record<string, unknown>): AssetsStreamRow | undefined {
    const name = typeof parsed.name === "string" ? parsed.name.trim() : "";
    if (!name) return undefined;
    const category =
        CATEGORY_ALIASES[
            String(parsed.category ?? "")
                .trim()
                .toLowerCase()
        ] ?? "";
    const scene = Number(String(parsed.first_scene ?? "").match(/(\d+)\s*$/)?.[1]) || 0;
    return {
        category,
        name,
        aliases: aliases(parsed.aliases),
        appearance: String(parsed.appearance ?? "")
            .replace(/\s+/g, " ")
            .trim(),
        firstScene: scene,
    };
}

/** 解析文本流里已经写完（以换行结尾）的行：回合标记开一场，其余带 name 的行是一个资产；没有回合标记时按 first_scene 分场。 */
export function parseAssetsExtractStream(text: string, headings: string[] = []): AssetsExtractStreamState {
    const state: AssetsExtractStreamState = { total: 0, current: 0, scenes: [], rowCount: 0 };
    const complete = text.slice(0, text.lastIndexOf("\n") + 1);
    let scene: AssetsStreamScene | undefined;
    let markers = false;
    const open = (index: number, heading: string, attempt: number) => {
        const next: AssetsStreamScene = { index, heading, attempt, rows: [] };
        const existing = state.scenes.findIndex((item) => item.index === index);
        if (existing >= 0) state.scenes[existing] = next;
        else state.scenes.push(next);
        state.current = index;
        scene = next;
        return next;
    };
    for (const raw of complete.split("\n")) {
        const line = raw.trim().replace(/,$/, "");
        if (!line.startsWith("{")) continue;
        let parsed: Record<string, unknown> | undefined;
        try {
            parsed = record(JSON.parse(line));
        } catch {
            continue;
        }
        if (!parsed) continue;
        const marker = record(parsed[SHOT_TURN_MARKER]);
        if (marker) {
            markers = true;
            state.total = Number(marker.total) || state.total;
            open(Number(marker.index) || state.scenes.length + 1, typeof marker.heading === "string" ? marker.heading : "", Number(marker.attempt) || 1);
            continue;
        }
        const row = assetRow(parsed);
        if (!row) continue;
        if (!markers) {
            // 结果正文没有回合标记：按资产第一次出现的场分组。
            const index = row.firstScene || 1;
            scene = state.scenes.find((item) => item.index === index) || open(index, headings[index - 1] || "", 1);
        }
        (scene || open(1, headings[0] || "", 1)).rows.push(row);
    }
    state.scenes.sort((a, b) => a.index - b.index);
    state.rowCount = state.scenes.reduce((sum, item) => sum + item.rows.length, 0);
    return state;
}

type ResultTurn = { index?: number; heading?: string; text?: string };

/** 已经结束的任务从结果还原：没写完的任务结果里每场带着输出原文；成功的任务只留各场拼好的 text，按 first_scene 分场。 */
export function assetsExtractStreamFromResult(resultJson?: string): AssetsExtractStreamState {
    let result: { text?: unknown; dramaRun?: { total?: number; scenes?: Array<{ heading?: string }>; turns?: ResultTurn[] } } = {};
    try {
        result = JSON.parse(resultJson || "{}") as typeof result;
    } catch {
        return parseAssetsExtractStream("");
    }
    const total = Number(result.dramaRun?.total) || 0;
    const headings = (result.dramaRun?.scenes || []).map((item) => item.heading || "");
    const turns = (result.dramaRun?.turns || []).filter((turn) => typeof turn.text === "string" && turn.text);
    if (turns.length) {
        return parseAssetsExtractStream(turns.map((turn, i) => `${JSON.stringify({ [SHOT_TURN_MARKER]: { index: turn.index || i + 1, total, heading: turn.heading || "", attempt: 1 } })}\n${String(turn.text).trim()}\n`).join(""), headings);
    }
    const text = typeof result.text === "string" ? result.text : "";
    const state = parseAssetsExtractStream(text.endsWith("\n") ? text : `${text}\n`, headings);
    return { ...state, total: total || state.scenes.length };
}

/** 「从第 N 场继续」时已写完的场接在新输出前面。 */
export function resumedAssetsExtractStream(live: AssetsExtractStreamState, resume?: { from: number; total: number; previous: AssetsExtractStreamState }): AssetsExtractStreamState {
    if (!resume) return live;
    const first = live.scenes[0]?.index ?? resume.from;
    const scenes = [...resume.previous.scenes.filter((item) => item.index < first), ...live.scenes];
    return { total: live.total || resume.total, current: live.current || resume.from, scenes, rowCount: scenes.reduce((sum, item) => sum + item.rows.length, 0) };
}
