/**
 * 生成分段的流式镜头视图：服务端在一个任务里逐轮续写，各轮输出和回合标记（{"@turn": {index, total, scene, heading, attempt, section?, genre?}}）
 * 都追加在同一个任务的 text-events 里。按小节生成（M8）时一轮一节，模型写的是技能原生格式（7.3 的 Markdown 片段、即梦的导演分镜字段），
 * 不是 JSON：写的过程中只显示这一节最近的原文，这一节写完后服务端补一行转出的镜头（{"rows": [...]}）。旧的整章生成一镜一行（chapter.shots@3）。
 * 这里只解析已经写完的行；同一轮重试时会再出一行标记，见到就把这一轮之前收到的半截输出清掉。只用于显示，写库在服务端。
 */

export type StreamShot = {
    shotNumber: number;
    id: string;
    size: string;
    plot: string;
    dialogue: string;
};

/** 一轮：按小节生成时是一节（section 是小节编号），旧的整章生成是一场。 */
export type StreamScene = {
    index: number;
    scene: string;
    heading: string;
    attempt: number;
    /** 小节编号（E01S02-P1）、戏类名（文戏 15 / 文戏 30）、估时秒数；旧的整章生成没有。 */
    section?: string;
    genre?: string;
    est?: number;
    /** 这一节还没转出镜头时收到的原文（只留最后几行，写的过程中显示）。 */
    raw: string;
    shots: StreamShot[];
};

export type ShotStreamState = {
    /** 一共几轮（来自回合标记；还没收到时为 0）。 */
    total: number;
    scenes: StreamScene[];
    /** 正在写的轮（最后一个标记的序号）。 */
    current: number;
    shotCount: number;
    /** 「重新生成问题场 / 小节」时要重写的场或小节（回合标记带的；只有这几轮有输出）。 */
    only?: string[];
};

/** 一场下的各节（侧栏按「场 → 小节」分组）。 */
export type StreamSceneGroup = { scene: string; heading: string; sections: StreamScene[] };

/** 回合标记行的键（与后端 drama.ShotLinesMarkerKey 一致）。 */
export const SHOT_TURN_MARKER = "@turn";

/** 原文预览留几行。 */
const RAW_PREVIEW_LINES = 6;

function textOf(value: unknown): string {
    if (typeof value === "string") return value.trim();
    if (typeof value === "number") return String(value);
    if (Array.isArray(value)) return value.map(textOf).filter(Boolean).join("、");
    return "";
}

function record(value: unknown): Record<string, unknown> | undefined {
    return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;
}

function appendRaw(raw: string, line: string) {
    const lines = raw ? raw.split("\n") : [];
    lines.push(line);
    return lines.slice(-RAW_PREVIEW_LINES).join("\n");
}

/** 解析文本流里已经写完（以换行结尾）的行；最后一行没写完的不算。 */
export function parseShotStream(text: string): ShotStreamState {
    const state: ShotStreamState = { total: 0, scenes: [], current: 0, shotCount: 0 };
    const complete = text.slice(0, text.lastIndexOf("\n") + 1);
    let scene: StreamScene | undefined;
    let markers = false;
    for (const raw of complete.split("\n")) {
        const line = raw.trim().replace(/,$/, "");
        if (!line) continue;
        let parsed: Record<string, unknown> | undefined;
        if (line.startsWith("{")) {
            try {
                parsed = record(JSON.parse(line));
            } catch {
                parsed = undefined;
            }
        }
        if (!parsed) {
            // 技能原生格式的原文（不是镜头行）：记进这一节的预览；代码块围栏不算。
            if (scene && !line.startsWith("```")) scene.raw = appendRaw(scene.raw, line);
            continue;
        }
        const marker = record(parsed[SHOT_TURN_MARKER]);
        if (marker) {
            markers = true;
            const index = Number(marker.index) || state.scenes.length + 1;
            state.total = Number(marker.total) || state.total;
            state.current = index;
            const next: StreamScene = { index, scene: textOf(marker.scene), heading: textOf(marker.heading), attempt: Number(marker.attempt) || 1, raw: "", shots: [] };
            const section = textOf(marker.section);
            if (section) Object.assign(next, { section, genre: textOf(marker.genre) || undefined, est: Number(marker.est) || undefined });
            if (Array.isArray(marker.only)) state.only = marker.only.map(textOf).filter(Boolean);
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
            const sectionId = textOf(spec?.section);
            // 没有回合标记（结果正文、模型没按场续写）时按镜头的 spec.section / spec.scene 分组。
            if (!scene || (!markers && ((sectionId && scene.section !== sectionId) || (!sectionId && sceneId && scene.scene !== sceneId)))) {
                scene = { index: state.scenes.length + 1, scene: sceneId, heading: "", attempt: 1, raw: "", shots: [], ...(sectionId ? { section: sectionId } : {}) };
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

/** 把各轮按场分组（同一场的几节排在一起，按出现顺序）。 */
export function streamSceneGroups(state: ShotStreamState): StreamSceneGroup[] {
    const groups: StreamSceneGroup[] = [];
    for (const turn of state.scenes) {
        const last = groups[groups.length - 1];
        if (last && last.scene === turn.scene) {
            last.sections.push(turn);
            if (!last.heading) last.heading = turn.heading;
        } else {
            groups.push({ scene: turn.scene, heading: turn.heading, sections: [turn] });
        }
    }
    return groups;
}

type ResultTurn = { index?: number; scene?: string; heading?: string; section?: string; text?: string };
type ResultScene = { scene: string; heading: string; section?: string; est?: number };
type ResultRun = { total?: number; genre?: string; scenes?: ResultScene[]; turns?: ResultTurn[] };

const genreLabels: Record<string, string> = { wenxi15: "文戏 15", wenxi30: "文戏 30", wuxi: "武戏" };

/**
 * 已经结束的任务从结果还原镜头视图（不再订阅文本流）：没写完的任务结果里每一轮带着输出原文（按小节生成时是技能原生格式，
 * 只能显示原文）；成功的任务只留转出的镜头行（text），按镜头 spec.section / spec.scene 分组，标题取 dramaRun.scenes。
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
    const scenes = run.scenes || [];
    const genre = run.genre ? genreLabels[run.genre] || run.genre : undefined;
    const turnOf = (index: number, scene = "", heading = "", section = "") => {
        const planned = scenes[index - 1];
        return JSON.stringify({ [SHOT_TURN_MARKER]: { index, total, scene, heading, attempt: 1, ...(section ? { section, genre, est: planned?.est } : {}) } });
    };
    const turns = (run.turns || []).filter((turn) => typeof turn.text === "string" && turn.text);
    if (turns.length) {
        return parseShotStream(turns.map((turn, i) => `${turnOf(turn.index || i + 1, turn.scene, turn.heading, turn.section || scenes[(turn.index || i + 1) - 1]?.section)}\n${String(turn.text).trim()}\n`).join(""));
    }
    const text = typeof result.text === "string" ? result.text : "";
    const lines: string[] = [];
    let current: string | undefined;
    for (const raw of text.split("\n")) {
        const line = raw.trim();
        if (!line.startsWith("{")) continue;
        let spec: Record<string, unknown> | undefined;
        try {
            spec = record(record(JSON.parse(line))?.spec);
        } catch {
            continue;
        }
        const scene = textOf(spec?.scene);
        const section = textOf(spec?.section);
        const key = section || scene;
        if (scenes.length && key !== current) {
            current = key;
            const index = scenes.findIndex((item) => (section ? item.section === section : item.scene === scene));
            lines.push(turnOf(index >= 0 ? index + 1 : lines.length + 1, scene, index >= 0 ? scenes[index].heading : "", section));
        }
        lines.push(line);
    }
    return parseShotStream(lines.length ? lines.join("\n") + "\n" : "");
}

/** 上一次生成分段任务停下来的样子：写完 ≥1 节（场）的可以从下一节续写（resume），一节都没写完或收尾失败的只能重新生成（failed）。 */
export type StoryboardRunStop =
    | { kind: "resume"; taskId: string; from: number; total: number; error?: string }
    /** 各节都写完了、收尾（写镜头表、装段）没成：接着这次任务只重新收尾，不再调模型（只有生成分段支持）。 */
    | { kind: "refinalize"; taskId: string; total: number; status: "failed" | "cancelled"; error?: string }
    | { kind: "failed"; taskId: string; status: "failed" | "cancelled"; error?: string }
    /** 本章已有一次成功写入，之后的重跑停下了：当前结果仍是 resultAt 那次运行的，不给续写（续写会拿重跑写的前几场替换现有结果）。 */
    | { kind: "superseded"; taskId: string; status: "failed" | "cancelled"; error?: string; resultAt: string };

/**
 * 只看失败或取消的任务；progress 是任务结果里的 dramaRun（整章一轮或还没开始逐场时没有）。
 * lastWrite 是比这个任务更早、成功写入的那次任务（同一章）：有它时停下的是重跑，不给「从第 N 场继续」。
 * options.refinalize 为 true（生成分段）时各节都写完的任务给「重新收尾」：整章输出都是这次运行的，重跑也照样收尾。
 */
export function storyboardRunStop(
    task: { id: string; status: string; error?: string; createdAt?: string },
    progress?: { total: number; completed: number },
    lastWrite?: { createdAt: string; completedAt?: string },
    options: { refinalize?: boolean } = {},
): StoryboardRunStop | undefined {
    if (task.status !== "failed" && task.status !== "cancelled") return undefined;
    const error = task.error?.trim() || undefined;
    if (options.refinalize && progress && progress.total >= 1 && progress.completed >= progress.total) return { kind: "refinalize", taskId: task.id, total: progress.total, status: task.status, error };
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
export function storyboardOutcomeSummary(outcome: { status: "written" | "draft"; shots: number; scenes: number; errors: number; sections?: number }, current: { segmentCount: number; draft?: boolean }) {
    const unit = outcome.sections ? `${outcome.sections} 节` : `${outcome.scenes} 场`;
    if (outcome.status === "written" || current.draft === false) return `已写入 ${outcome.shots} 镜，按 ${unit}装成 ${current.segmentCount} 段`;
    return `已写入 ${outcome.shots} 镜，检查有 ${outcome.errors} 个问题，还没装段`;
}
