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
    /** 这一场判出的戏类（武戏 / 文戏 / 悬疑戏 / 单人情绪戏；判定失败按文戏），来自回合标记。 */
    genre?: string;
    genreReason?: string;
    shots: StreamShot[];
};

/** 一场的戏类判定（第一轮的回合标记带全部场的，结果里是 dramaRun.genres）。 */
export type StreamGenre = { scene: string; genre: string; reason?: string; fallback?: boolean };

export type ShotStreamState = {
    /** 一共几场（来自回合标记；还没收到时为 0）。 */
    total: number;
    scenes: StreamScene[];
    /** 正在写的场（最后一个标记的场序号）。 */
    current: number;
    shotCount: number;
    /** 各场戏类（第一轮之前判出，没有时为空）。 */
    genres: StreamGenre[];
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
    return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;
}

/** 解析文本流里已经写完（以换行结尾）的行；最后一行没写完的不算。 */
export function parseShotStream(text: string): ShotStreamState {
    const state: ShotStreamState = { total: 0, scenes: [], current: 0, shotCount: 0, genres: [] };
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
            const genre = textOf(marker.genre);
            if (genre) Object.assign(next, { genre, genreReason: textOf(marker.genreReason) || undefined });
            if (Array.isArray(marker.genres)) state.genres = streamGenres(marker.genres);
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

function streamGenres(value: unknown[]): StreamGenre[] {
    return value.map(record).flatMap((item) => (item && textOf(item.genre) ? [{ scene: textOf(item.scene), genre: textOf(item.genre), reason: textOf(item.reason) || undefined, fallback: item.fallback === true || undefined }] : []));
}

type ResultTurn = { index?: number; scene?: string; heading?: string; genre?: string; text?: string };
type ResultRun = { total?: number; scenes?: Array<{ scene: string; heading: string; genre?: string; genreReason?: string }>; turns?: ResultTurn[]; genres?: unknown[] };

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
    const genres = Array.isArray(run.genres) ? streamGenres(run.genres) : [];
    const genreOf = (scene = "") => genres.find((item) => item.scene === scene);
    const marker = (index: number, scene = "", heading = "") => JSON.stringify({ [SHOT_TURN_MARKER]: { index, total, scene, heading, attempt: 1, genre: genreOf(scene)?.genre, genreReason: genreOf(scene)?.reason } });
    const withGenres = (state: ShotStreamState): ShotStreamState => ({ ...state, genres });
    const turns = (run.turns || []).filter((turn) => typeof turn.text === "string" && turn.text);
    if (turns.length) {
        return withGenres(parseShotStream(turns.map((turn, i) => `${marker(turn.index || i + 1, turn.scene, turn.heading)}\n${String(turn.text).trim()}\n`).join("")));
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
    return withGenres(parseShotStream(lines.length ? lines.join("\n") + "\n" : ""));
}

/** 上一次生成分段任务停下来的样子：写完 ≥1 场的可以从下一场续写（resume），一场都没写完或收尾失败的只能重新生成（failed）。 */
export type StoryboardRunStop =
    | { kind: "resume"; taskId: string; from: number; total: number; error?: string }
    | { kind: "failed"; taskId: string; status: "failed" | "cancelled"; error?: string }
    /** 本章已有一次成功写入，之后的重跑停下了：当前结果仍是 resultAt 那次运行的，不给续写（续写会拿重跑写的前几场替换现有结果）。 */
    | { kind: "superseded"; taskId: string; status: "failed" | "cancelled"; error?: string; resultAt: string };

/**
 * 只看失败或取消的任务；progress 是任务结果里的 dramaRun（整章一轮或还没开始逐场时没有）。
 * lastWrite 是比这个任务更早、成功写入的那次任务（同一章）：有它时停下的是重跑，不给「从第 N 场继续」。
 */
export function storyboardRunStop(task: { id: string; status: string; error?: string; createdAt?: string }, progress?: { total: number; completed: number }, lastWrite?: { createdAt: string; completedAt?: string }): StoryboardRunStop | undefined {
    if (task.status !== "failed" && task.status !== "cancelled") return undefined;
    const error = task.error?.trim() || undefined;
    if (lastWrite && (!task.createdAt || Date.parse(lastWrite.createdAt) < Date.parse(task.createdAt))) return { kind: "superseded", taskId: task.id, status: task.status, error, resultAt: lastWrite.completedAt || lastWrite.createdAt };
    if (progress && progress.completed >= 1 && progress.completed < progress.total) return { kind: "resume", taskId: task.id, from: progress.completed + 1, total: progress.total, error };
    return { kind: "failed", taskId: task.id, status: task.status, error };
}

/** 本章最近一次成功写入的生成分段任务（tasks 是本章的生成分段任务，顺序不限）。 */
export function latestStoryboardWrite<T extends { status: string; createdAt: string }>(tasks: T[]): T | undefined {
    return tasks.filter((task) => task.status === "succeeded").reduce<T | undefined>((latest, task) => (!latest || Date.parse(task.createdAt) > Date.parse(latest.createdAt) ? task : latest), undefined);
}

/**
 * 侧栏摘要行。段数以本章现在的段为准：「重新装段」不产生新任务，任务结果里的段数在重装后就旧了。
 * 草稿后来重新装段成功（镜头表不再是草稿）也按已写入说。
 */
export function storyboardOutcomeSummary(outcome: { status: "written" | "draft"; shots: number; scenes: number; errors: number }, current: { segmentCount: number; draft?: boolean }) {
    if (outcome.status === "written" || current.draft === false) return `已写入 ${outcome.shots} 镜，按 ${outcome.scenes} 场装成 ${current.segmentCount} 段`;
    return `已写入 ${outcome.shots} 镜，检查有 ${outcome.errors} 个问题，还没装段`;
}
