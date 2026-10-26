/**
 * 剧本优化的流式剧集视图：服务端在一个任务里逐集续写，模型一集一块（script.adapt@1：一行 JSON 头 + 正文），各集输出和回合标记
 * （{"@turn": {index, total, scene, heading, attempt}}，与生成分段同一种）都追加在同一个任务的 text-events 里。这里按集分组，
 * 正文逐行显示（最后一行没写完的也显示，好看出模型在写）；同一集重试时会再出一行标记，见到就把这一集之前收到的半截输出清掉。
 * 只用于显示，写库在服务端。
 */

import { SHOT_TURN_MARKER } from "./chapter-shot-stream";

export type StreamEpisode = {
    index: number;
    /** JSON 头里的集标题；头还没写出来时是回合标记里的「第 N 集」或原剧集标题。 */
    title: string;
    attempt: number;
    /** 场次头（「## 」开头的行，去掉井号）。 */
    scenes: string[];
    /** 正文各行（场次头、动作、台词）。 */
    lines: string[];
};

export type ScriptAdaptStreamState = {
    /** 一共几集（来自回合标记；还没收到时为 0）。 */
    total: number;
    episodes: StreamEpisode[];
    /** 正在写的集（最后一个标记的集序号）。 */
    current: number;
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

const SCENE_HEADING = /^(?:## |第?[一二三四五六七八九十\d]+场|场\s*\d+|INT\.|EXT\.|内[·.]|外[·.])/;

/** 解析文本流：回合标记开一集，JSON 头补集标题，其余非空行是正文。 */
export function parseScriptAdaptStream(text: string): ScriptAdaptStreamState {
    const state: ScriptAdaptStreamState = { total: 0, episodes: [], current: 0, sceneCount: 0 };
    let episode: StreamEpisode | undefined;
    for (const raw of text.split("\n")) {
        const line = raw.trim();
        if (!line || line.startsWith("```")) continue;
        const parsed = jsonLine(line);
        const marker = record(parsed?.[SHOT_TURN_MARKER]);
        if (marker) {
            const index = Number(marker.index) || state.episodes.length + 1;
            state.total = Number(marker.total) || state.total;
            state.current = index;
            const next: StreamEpisode = { index, title: typeof marker.heading === "string" ? marker.heading : "", attempt: Number(marker.attempt) || 1, scenes: [], lines: [] };
            const existing = state.episodes.findIndex((item) => item.index === index);
            if (existing >= 0) state.episodes[existing] = next;
            else state.episodes.push(next);
            episode = next;
            continue;
        }
        if (parsed && ("no" in parsed || "title" in parsed)) {
            // 没有回合标记（结果正文）时一个 JSON 头开一集。
            if (!episode || episode.lines.length) {
                episode = { index: state.episodes.length + 1, title: "", attempt: 1, scenes: [], lines: [] };
                state.episodes.push(episode);
                state.current = episode.index;
            }
            if (typeof parsed.title === "string" && parsed.title.trim()) episode.title = parsed.title.trim();
            continue;
        }
        if (!episode) continue;
        if (SCENE_HEADING.test(line)) episode.scenes.push(line.replace(/^#+\s*/, ""));
        episode.lines.push(line);
    }
    state.sceneCount = state.episodes.reduce((sum, item) => sum + item.scenes.length, 0);
    return state;
}

type ResultTurn = { index?: number; heading?: string; text?: string };

/** 已经结束的任务从结果还原剧集视图：没写完的任务结果里每集带着输出原文；成功的任务只留各集拼好的 text。 */
export function scriptAdaptStreamFromResult(resultJson?: string): ScriptAdaptStreamState {
    let result: { text?: unknown; dramaRun?: { total?: number; turns?: ResultTurn[] } } = {};
    try {
        result = JSON.parse(resultJson || "{}") as typeof result;
    } catch {
        return parseScriptAdaptStream("");
    }
    const total = Number(result.dramaRun?.total) || 0;
    const turns = (result.dramaRun?.turns || []).filter((turn) => typeof turn.text === "string" && turn.text);
    if (turns.length) {
        return parseScriptAdaptStream(turns.map((turn, i) => `${JSON.stringify({ [SHOT_TURN_MARKER]: { index: turn.index || i + 1, total, heading: turn.heading || "", attempt: 1 } })}\n${String(turn.text).trim()}\n`).join(""));
    }
    const state = parseScriptAdaptStream(typeof result.text === "string" ? result.text : "");
    return { ...state, total: total || state.episodes.length };
}
