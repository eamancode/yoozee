/**
 * 场次色卡的流式视图：服务端在一个任务里逐集续写，模型每场一行 JSON（scene.palette@1：scene 写场编号 E01S02，每色带分组），
 * 各集输出和回合标记（{"@turn": {...}}，与生成分段、剧本优化同一种）都追加在同一个任务的 text-events 里。这里按集分组，
 * 每场显示色块；同一集重试时会再出一行标记，见到就把这一集之前收到的半截输出清掉。模型多写的风格锚行不显示（这一步不改风格锚）。
 * 只用于显示，写库在服务端。
 */

import type { DramaColorScriptEntry, DramaPaletteColor } from "@/services/api/drama";

import { SHOT_TURN_MARKER } from "./chapter-shot-stream";

export type ScenePaletteStreamEpisode = {
    index: number;
    /** 集号（回合标记里的 scene，或场编号里的集号）。 */
    episode: number;
    title: string;
    attempt: number;
    scenes: DramaColorScriptEntry[];
};

export type ScenePaletteStreamState = {
    total: number;
    current: number;
    episodes: ScenePaletteStreamEpisode[];
    sceneCount: number;
};

const SCENE_CODE = /^E(\d{1,3})S(\d{1,3})$/i;

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

function colors(value: unknown): DramaPaletteColor[] {
    return Array.isArray(value)
        ? value
              .map((item) => record(item))
              .filter((item): item is Record<string, unknown> => Boolean(item))
              .map((item) => ({
                  name_zh: String(item.name_zh ?? ""),
                  name_en: String(item.name_en ?? ""),
                  hex: String(item.hex ?? "")
                      .trim()
                      .toUpperCase(),
                  usage: String(item.usage ?? ""),
                  ...(typeof item.group === "string" && item.group ? { group: item.group } : {}),
              }))
        : [];
}

/** 解析文本流：回合标记开一集，每场一行（scene 是场编号时集号场序从它读；也认旧写法 episode + scene_no），风格锚和集的标题行跳过。 */
export function parseScenePaletteStream(text: string): ScenePaletteStreamState {
    const state: ScenePaletteStreamState = { total: 0, current: 0, episodes: [], sceneCount: 0 };
    let episode: ScenePaletteStreamEpisode | undefined;
    const open = (index: number, no: number, title: string, attempt: number) => {
        const next: ScenePaletteStreamEpisode = { index, episode: no, title, attempt, scenes: [] };
        const existing = state.episodes.findIndex((item) => item.index === index);
        if (existing >= 0) state.episodes[existing] = next;
        else state.episodes.push(next);
        state.current = index;
        episode = next;
    };
    for (const raw of text.split("\n")) {
        const parsed = jsonLine(raw.trim());
        if (!parsed) continue;
        const marker = record(parsed[SHOT_TURN_MARKER]);
        if (marker) {
            state.total = Number(marker.total) || state.total;
            open(Number(marker.index) || state.episodes.length + 1, Number(marker.scene) || 0, typeof marker.heading === "string" ? marker.heading : "", Number(marker.attempt) || 1);
            continue;
        }
        const isScene = "scene_no" in parsed || "shared_colors" in parsed || "new_colors" in parsed;
        if (!isScene) continue;
        const code = String(parsed.scene ?? "")
            .trim()
            .match(SCENE_CODE);
        const no = code ? Number(code[1]) : Number(parsed.episode) || 0;
        // 没有回合标记（结果正文）时集号变了就开一集。
        if (!episode || (no && episode.scenes.length && episode.episode !== no)) open(state.episodes.length + 1, no || 1, "", 1);
        if (no && !episode!.episode) episode!.episode = no;
        episode!.scenes.push({
            episode: no || episode!.episode,
            scene_no: code ? Number(code[2]) : Number(parsed.scene_no) || episode!.scenes.length + 1,
            scene: code ? "" : String(parsed.scene ?? "").replace(/^#+\s*/, ""),
            inherits_from: String(parsed.inherits_from ?? "").toUpperCase(),
            shared_colors: colors(parsed.shared_colors),
            new_colors: colors(parsed.new_colors),
            ...(typeof parsed.purpose === "string" ? { purpose: parsed.purpose } : {}),
        });
    }
    state.sceneCount = state.episodes.reduce((sum, item) => sum + item.scenes.length, 0);
    return state;
}

type ResultTurn = { index?: number; scene?: string; heading?: string; text?: string };

/** 已经结束的任务从结果还原：没写完的任务结果里每集带着输出原文；成功的任务只留各集拼好的 text。 */
export function scenePaletteStreamFromResult(resultJson?: string): ScenePaletteStreamState {
    let result: { text?: unknown; dramaRun?: { total?: number; turns?: ResultTurn[] } } = {};
    try {
        result = JSON.parse(resultJson || "{}") as typeof result;
    } catch {
        return parseScenePaletteStream("");
    }
    const total = Number(result.dramaRun?.total) || 0;
    const turns = (result.dramaRun?.turns || []).filter((turn) => typeof turn.text === "string" && turn.text);
    if (turns.length) {
        return parseScenePaletteStream(turns.map((turn, i) => `${JSON.stringify({ [SHOT_TURN_MARKER]: { index: turn.index || i + 1, total, scene: turn.scene || "", heading: turn.heading || "", attempt: 1 } })}\n${String(turn.text).trim()}\n`).join(""));
    }
    const state = parseScenePaletteStream(typeof result.text === "string" ? result.text : "");
    return { ...state, total: total || state.episodes.length };
}

/** 「从第 N 集继续」时已写完的集接在新输出前面；新任务的第一个回合标记到之前按续写起点显示。 */
export function resumedScenePaletteStream(live: ScenePaletteStreamState, resume?: { from: number; total: number; previous: ScenePaletteStreamState }): ScenePaletteStreamState {
    if (!resume) return live;
    const first = live.episodes[0]?.index ?? resume.from;
    const episodes = [...resume.previous.episodes.filter((item) => item.index < first), ...live.episodes];
    return { total: live.total || resume.total, current: live.current || resume.from, episodes, sceneCount: episodes.reduce((sum, item) => sum + item.scenes.length, 0) };
}
