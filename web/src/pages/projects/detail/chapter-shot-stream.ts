/**
 * 生成分段的流式镜头视图：服务端在一个任务里逐场续写，模型一镜一行（chapter.shots@3），各场输出和回合标记
 * （{"@turn": {index, total, scene, heading, attempt}}）都追加在同一个任务的 text-events 里。这里只解析已经写完的行，
 * 按场分组；同一场重试时会再出一行标记，见到就把这一场之前收到的半截输出清掉。只用于显示，写库在服务端。
 */

export type StreamShot = {
    shotNumber: number;
    id: string;
    size: string;
    plot: string;
    dialogue: string;
};

export type StreamScene = {
    index: number;
    scene: string;
    heading: string;
    attempt: number;
    shots: StreamShot[];
};

export type ShotStreamState = {
    /** 一共几场（来自回合标记；还没收到时为 0）。 */
    total: number;
    scenes: StreamScene[];
    /** 正在写的场（最后一个标记的场序号）。 */
    current: number;
    shotCount: number;
};

/** 回合标记行的键（与后端 drama.ShotLinesMarkerKey 一致）。 */
export const SHOT_TURN_MARKER = "@turn";

function textOf(value: unknown): string {
    if (typeof value === "string") return value.trim();
    if (typeof value === "number") return String(value);
    if (Array.isArray(value)) return value.map(textOf).filter(Boolean).join("、");
    return "";
}

function record(value: unknown): Record<string, unknown> | undefined {
    return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

/** 解析文本流里已经写完（以换行结尾）的行；最后一行没写完的不算。 */
export function parseShotStream(text: string): ShotStreamState {
    const state: ShotStreamState = { total: 0, scenes: [], current: 0, shotCount: 0 };
    const complete = text.slice(0, text.lastIndexOf("\n") + 1);
    let scene: StreamScene | undefined;
    let markers = false;
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
            const index = Number(marker.index) || state.scenes.length + 1;
            state.total = Number(marker.total) || state.total;
            state.current = index;
            const next: StreamScene = { index, scene: textOf(marker.scene), heading: textOf(marker.heading), attempt: Number(marker.attempt) || 1, shots: [] };
            const existing = state.scenes.findIndex((item) => item.index === index);
            if (existing >= 0) state.scenes[existing] = next;
            else state.scenes.push(next);
            scene = next;
            continue;
        }
        const rows = Array.isArray(parsed.rows) ? parsed.rows.map(record).filter((row): row is Record<string, unknown> => Boolean(row)) : [parsed];
        for (const row of rows) {
            const spec = record(row.spec);
            const sceneId = textOf(spec?.scene);
            // 没有回合标记（结果正文、模型没按场续写）时按镜头的 spec.scene 分场。
            if (!scene || (!markers && sceneId && scene.scene !== sceneId)) {
                scene = { index: state.scenes.length + 1, scene: sceneId, heading: "", attempt: 1, shots: [] };
                state.scenes.push(scene);
                state.current = scene.index;
            }
            scene.shots.push({
                shotNumber: Number(row.shotNumber) || scene.shots.length + 1,
                id: textOf(spec?.id),
                size: textOf(row.shotSize) || textOf(spec?.size),
                plot: textOf(row.plotDescription),
                dialogue: textOf(row.dialogue),
            });
        }
    }
    state.shotCount = state.scenes.reduce((sum, item) => sum + item.shots.length, 0);
    return state;
}

type ResultTurn = { index?: number; scene?: string; heading?: string; text?: string };
type ResultRun = { total?: number; scenes?: Array<{ scene: string; heading: string }>; turns?: ResultTurn[] };

/**
 * 已经结束的任务从结果还原镜头视图（不再订阅文本流）：没写完的任务结果里每场带着输出原文；
 * 成功的任务只留各场拼好的 text，按镜头 spec.scene 分场，场标题取 dramaRun.scenes。
 */
export function shotStreamFromResult(resultJson?: string): ShotStreamState {
    let result: { text?: unknown; dramaRun?: ResultRun } = {};
    try {
        result = JSON.parse(resultJson || "{}") as typeof result;
    } catch {
        return parseShotStream("");
    }
    const run = result.dramaRun || {};
    const total = Number(run.total) || 0;
    const marker = (index: number, scene = "", heading = "") => JSON.stringify({ [SHOT_TURN_MARKER]: { index, total, scene, heading, attempt: 1 } });
    const turns = (run.turns || []).filter((turn) => typeof turn.text === "string" && turn.text);
    if (turns.length) {
        return parseShotStream(turns.map((turn, i) => `${marker(turn.index || i + 1, turn.scene, turn.heading)}\n${String(turn.text).trim()}\n`).join(""));
    }
    const text = typeof result.text === "string" ? result.text : "";
    const scenes = run.scenes || [];
    const lines: string[] = [];
    let currentScene: string | undefined;
    for (const raw of text.split("\n")) {
        const line = raw.trim();
        if (!line.startsWith("{")) continue;
        let scene = "";
        try {
            scene = textOf(record(record(JSON.parse(line))?.spec)?.scene);
        } catch {
            continue;
        }
        if (scenes.length && scene !== currentScene) {
            currentScene = scene;
            const index = scenes.findIndex((item) => item.scene === scene);
            lines.push(marker(index >= 0 ? index + 1 : lines.length + 1, scene, index >= 0 ? scenes[index].heading : ""));
        }
        lines.push(line);
    }
    return parseShotStream(lines.length ? lines.join("\n") + "\n" : "");
}
