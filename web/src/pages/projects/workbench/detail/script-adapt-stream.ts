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
    /** 回合标记里的轮次 ID：剧集轮是集号；SSS 流水线拆剧本的第一轮是 concept（立意与全集结构，不是剧集）。 */
    turn?: string;
};

/** SSS 流水线拆剧本多出的第一轮（与服务端 drama.ScriptAdaptConceptTurn 同值）。 */
export const SCRIPT_ADAPT_CONCEPT_TURN = "concept";

export type ScriptAdaptStreamState = {
    /** 一共几集（来自回合标记；还没收到时为 0）。 */
    total: number;
    episodes: StreamEpisode[];
    /** 正在写的集（最后一个标记的轮次序号；有立意轮时第 1 轮是立意与全集结构）。 */
    current: number;
    sceneCount: number;
    /** 这次运行有没有「立意与全集结构」轮（SSS 流水线）。有时 total 含这一轮，剧集数是 total - 1。 */
    concept?: boolean;
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
            if (typeof marker.scene === "string" && marker.scene) next.turn = marker.scene;
            if (next.turn === SCRIPT_ADAPT_CONCEPT_TURN) state.concept = true;
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
        // 立意轮的「## 立意页」「## 全集结构表」不是场次头。
        if (episode.turn !== SCRIPT_ADAPT_CONCEPT_TURN && SCENE_HEADING.test(line)) episode.scenes.push(line.replace(/^#+\s*/, ""));
        episode.lines.push(line);
    }
    state.sceneCount = state.episodes.reduce((sum, item) => sum + item.scenes.length, 0);
    return state;
}

type ResultTurn = { index?: number; scene?: string; heading?: string; text?: string };

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
        return parseScriptAdaptStream(turns.map((turn, i) => `${JSON.stringify({ [SHOT_TURN_MARKER]: { index: turn.index || i + 1, total, scene: turn.scene || "", heading: turn.heading || "", attempt: 1 } })}\n${String(turn.text).trim()}\n`).join(""));
    }
    const state = parseScriptAdaptStream(typeof result.text === "string" ? result.text : "");
    return { ...state, total: total || state.episodes.length };
}

/** 「从第 N 集继续」时已写完的集：上一次任务存下的前 N-1 集，以及从第几集接着写。 */
export type ScriptAdaptResume = { from: number; total: number; episodes: StreamEpisode[] };

/**
 * 续写任务的文本流只有剩下的集：前面几集取上一次任务存下的接在前面；新任务的第一个回合标记到之前，
 * 「正在写第几集」按续写起点显示，不回落到第 1 集。
 */
export function resumedScriptAdaptStream(live: ScriptAdaptStreamState, resume?: ScriptAdaptResume): ScriptAdaptStreamState {
    if (!resume) return live;
    const first = live.episodes[0]?.index ?? resume.from;
    const episodes = [...resume.episodes.filter((episode) => episode.index < first), ...live.episodes];
    const concept = Boolean(live.concept || episodes.some((episode) => episode.turn === SCRIPT_ADAPT_CONCEPT_TURN));
    return { total: live.total || resume.total, current: live.current || resume.from, episodes, sceneCount: episodes.reduce((sum, item) => sum + item.scenes.length, 0), ...(concept ? { concept } : {}) };
}

/** 一轮的集号：剧集轮的轮次 ID 就是集号；读不出时按轮次序号推（有立意轮时减一）。立意轮返回 0。 */
export function scriptAdaptEpisodeNo(episode: StreamEpisode, stream: Pick<ScriptAdaptStreamState, "concept">) {
    if (episode.turn === SCRIPT_ADAPT_CONCEPT_TURN) return 0;
    const no = Number(episode.turn);
    if (Number.isInteger(no) && no > 0) return no;
    return Math.max(1, episode.index - (stream.concept ? 1 : 0));
}

/** 侧栏里一轮的标题：立意轮是「立意与全集结构」，剧集轮是「第 N 集」（N 是真实集号，不是轮次序号）。 */
export function scriptAdaptGroupTitle(episode: StreamEpisode, stream: Pick<ScriptAdaptStreamState, "concept">) {
    return episode.turn === SCRIPT_ADAPT_CONCEPT_TURN ? "立意与全集结构" : `第 ${scriptAdaptEpisodeNo(episode, stream)} 集`;
}

/** 进行中的进度：「正在写立意与全集结构（第 1/4 轮）」或「正在写第 2/3 集」。剧集数不含立意轮。 */
export function scriptAdaptRunningText(stream: ScriptAdaptStreamState) {
    if (!stream.total) return "等待模型开始输出";
    const current = Math.max(1, stream.current);
    const live = stream.episodes.find((episode) => episode.index === current);
    if (!stream.concept) return `正在写第 ${current}/${stream.total} 集`;
    if (current === 1 || live?.turn === SCRIPT_ADAPT_CONCEPT_TURN) return `正在写立意与全集结构（第 1/${stream.total} 轮）`;
    const episodes = Math.max(1, stream.total - 1);
    return `正在写第 ${live ? scriptAdaptEpisodeNo(live, stream) : current - 1}/${episodes} 集`;
}
