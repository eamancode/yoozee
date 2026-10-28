/**
 * 导演风格的流式视图：服务端在一个任务里逐集续写，模型一行一个 JSON（style.bible@1：第一行风格锚，之后每集一行 JSON 头、每场一行色彩脚本），
 * 各集输出和回合标记（{"@turn": {...}}，与生成分段、剧本优化同一种）都追加在同一个任务的 text-events 里。这里按集分组，
 * 每场显示色块；同一集重试时会再出一行标记，见到就把这一集之前收到的半截输出清掉。只用于显示，写库在服务端。
 */

import type { DramaColorScriptEntry, DramaStyleAnchor } from "@/services/api/drama";

import { SHOT_TURN_MARKER } from "./chapter-shot-stream";

export type StyleStreamEpisode = {
    index: number;
    /** 集号（回合标记里的 scene，或 JSON 头的 episode）。 */
    episode: number;
    title: string;
    attempt: number;
    scenes: DramaColorScriptEntry[];
};

export type StyleBibleStreamState = {
    total: number;
    current: number;
    anchor?: DramaStyleAnchor;
    episodes: StyleStreamEpisode[];
    sceneCount: number;
};

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

function colors(value: unknown): DramaColorScriptEntry["shared_colors"] {
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
              }))
        : [];
}

/** 解析文本流：回合标记开一集，JSON 头补集号和标题，风格锚单独记下，其余带颜色的行是一场。 */
export function parseStyleBibleStream(text: string): StyleBibleStreamState {
    const state: StyleBibleStreamState = { total: 0, current: 0, episodes: [], sceneCount: 0 };
    let episode: StyleStreamEpisode | undefined;
    const open = (index: number, no: number, title: string, attempt: number) => {
        const next: StyleStreamEpisode = { index, episode: no, title, attempt, scenes: [] };
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
        const anchor = record(parsed.style_anchor);
        if (anchor) {
            state.anchor = anchor as DramaStyleAnchor;
            continue;
        }
        const isScene = "scene_no" in parsed || "shared_colors" in parsed || "new_colors" in parsed;
        if (!isScene) {
            const no = Number(parsed.episode) || 0;
            if (!no) continue;
            // 没有回合标记（结果正文）时一个 JSON 头开一集。
            if (!episode || (episode.scenes.length && episode.episode !== no)) open(state.episodes.length + 1, no, "", 1);
            episode!.episode = no;
            if (typeof parsed.title === "string" && parsed.title.trim()) episode!.title = parsed.title.trim();
            continue;
        }
        if (!episode) open(state.episodes.length + 1, Number(parsed.episode) || 1, "", 1);
        episode!.scenes.push({
            episode: Number(parsed.episode) || episode!.episode,
            scene_no: Number(parsed.scene_no) || episode!.scenes.length + 1,
            scene: String(parsed.scene ?? "").replace(/^#+\s*/, ""),
            inherits_from: String(parsed.inherits_from ?? ""),
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
export function styleBibleStreamFromResult(resultJson?: string): StyleBibleStreamState {
    let result: { text?: unknown; dramaRun?: { total?: number; turns?: ResultTurn[] } } = {};
    try {
        result = JSON.parse(resultJson || "{}") as typeof result;
    } catch {
        return parseStyleBibleStream("");
    }
    const total = Number(result.dramaRun?.total) || 0;
    const turns = (result.dramaRun?.turns || []).filter((turn) => typeof turn.text === "string" && turn.text);
    if (turns.length) {
        return parseStyleBibleStream(turns.map((turn, i) => `${JSON.stringify({ [SHOT_TURN_MARKER]: { index: turn.index || i + 1, total, scene: turn.scene || "", heading: turn.heading || "", attempt: 1 } })}\n${String(turn.text).trim()}\n`).join(""));
    }
    const state = parseStyleBibleStream(typeof result.text === "string" ? result.text : "");
    return { ...state, total: total || state.episodes.length };
}

/** 「从第 N 集继续」时已写完的集接在新输出前面；新任务的第一个回合标记到之前按续写起点显示。 */
export function resumedStyleBibleStream(live: StyleBibleStreamState, resume?: { from: number; total: number; previous: StyleBibleStreamState }): StyleBibleStreamState {
    if (!resume) return live;
    const first = live.episodes[0]?.index ?? resume.from;
    const episodes = [...resume.previous.episodes.filter((item) => item.index < first), ...live.episodes];
    return { total: live.total || resume.total, current: live.current || resume.from, anchor: live.anchor || resume.previous.anchor, episodes, sceneCount: episodes.reduce((sum, item) => sum + item.scenes.length, 0) };
}
