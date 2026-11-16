import type { GenerationTask } from "@/services/api/task-center";

/**
 * 导演分镜（SSS 电影 / 短剧、本体论）一次运行的进度，全部从任务列表的 dramaRounds 读（服务端每写完一轮就存，摘要只带每轮的种类和标题）。
 * 只做展示，不改模型调用编排和重试。
 *
 * SSS 的轮次与服务端 sssNextTurn 同序：立项（只有第一集）→ 集设计 → 讲戏 → 切分 → 每个单元「单元卡 + 编译」→ 复看；
 * 总轮数 = 4 + 2 × 单元数（+1 立项）。切分轮写出单元总表之前，总轮数是按每集时长估的；切分之后才是实际轮数。
 * 本体论在规划轮写出节奏总表之前同样是估计。
 */
export type DirectorPhase = { kind: string; label: string; done: number; planned: number; state: "done" | "current" | "pending" };

export type DirectorRunView = {
    /** 已保存的轮数（服务端每写完一轮就存，失败后从下一轮接着写）。 */
    saved: number;
    total: number;
    /** true：总轮数还是建任务时的估计（SSS 切分前、本体论规划前）。 */
    estimated: boolean;
    /** 正在写的这一轮（任务进行中时）；任务已结束时为空。 */
    current?: { index: number; label: string };
    /** 最后保存的一轮的标题。 */
    lastSaved?: string;
    /** SSS 的各阶段；本体论只显示轮数。 */
    phases: DirectorPhase[];
    /** 从建任务到现在（进行中）或到结束的累计等待秒数；时间读不出时为空。 */
    waitedSeconds?: number;
    active: boolean;
};

const SSS_PHASES: Array<{ kind: string; label: string }> = [
    { kind: "setup", label: "立项" },
    { kind: "plan", label: "集设计" },
    { kind: "direct", label: "讲戏" },
    { kind: "cut", label: "切分" },
    { kind: "card", label: "单元卡" },
    { kind: "compile", label: "编译" },
    { kind: "review", label: "复看" },
];

const SSS_REPAIR_PHASES: Array<{ kind: string; label: string }> = [
    { kind: "repair", label: "返修" },
    { kind: "compile", label: "编译" },
];

type Turn = { kind: string; heading?: string };

function secondsBetween(from: string | undefined, to: number) {
    const start = Date.parse(from || "");
    return Number.isFinite(start) && to >= start ? Math.round((to - start) / 1000) : undefined;
}

/** SSS 下一轮的种类与标题（与服务端 sssNextTurn 同序）。 */
function sssNext(turns: Turn[], setup: boolean, units: number, repair: boolean, flow: number): { kind: string; label: string } | undefined {
    const count = (kind: string) => turns.filter((turn) => turn.kind === kind).length;
    if (repair) {
        if (!count("repair")) return { kind: "repair", label: "返修" };
        if (!count("compile")) return { kind: "compile", label: "编译" };
        return undefined;
    }
    if (setup && !count("setup")) return { kind: "setup", label: "立项：全剧圣经" };
    if (flow < 2 && !count("plan")) return { kind: "plan", label: "本集设计与台词定稿" };
    if (!count("direct")) return { kind: "direct", label: "讲戏本" };
    if (!count("cut")) return { kind: "cut", label: "整场镜头表与单元总表" };
    const cards = count("card");
    const compiles = count("compile");
    if (compiles < units) return cards === compiles ? { kind: "card", label: `单元卡（第 ${cards + 1} 个单元）` } : { kind: "compile", label: `编译（第 ${compiles + 1} 个单元）` };
    if (!count("review")) return { kind: "review", label: "后期备忘、交接卡与复看" };
    return undefined;
}

/** 不是导演分镜任务（没有 dramaRounds）时返回 undefined。 */
export function directorRunView(task: Pick<GenerationTask, "status" | "dramaRounds" | "createdAt" | "completedAt">, options: { now?: number; streamTurn?: { index: number; heading: string } } = {}): DirectorRunView | undefined {
    const rounds = task.dramaRounds;
    if (!rounds) return undefined;
    const now = options.now ?? Date.now();
    const active = task.status === "queued" || task.status === "running";
    const turns: Turn[] = rounds.turns || [];
    const saved = rounds.completed;
    const total = Math.max(rounds.total, saved);
    const sss = rounds.mode === "sss";
    const repair = Boolean(rounds.repair);
    const estimated = repair ? false : sss ? !turns.some((turn) => turn.kind === "cut") : !turns.some((turn) => turn.kind === "plan");
    // 流里回合标记的标题只在它就是「下一轮」时采用（标记先于输出写出；重连、旧流时编号对不上就按已存轮次推）。
    const streamHeading = options.streamTurn && options.streamTurn.index === saved + 1 ? options.streamTurn.heading : "";
    const end = active ? now : Date.parse(task.completedAt || "") || now;
    const view: DirectorRunView = { saved, total, estimated, phases: [], waitedSeconds: secondsBetween(task.createdAt, end), active, lastSaved: turns.at(-1)?.heading || undefined };
    if (!sss) {
        if (active) view.current = { index: saved + 1, label: streamHeading };
        return view;
    }
    // 立项只在第一集：总轮数为奇数（4 + 2n + 1）或已经写过立项轮。
    const flow = rounds.flow ?? 1;
    const setup = flow < 2 && (turns.some((turn) => turn.kind === "setup") || (!repair && total % 2 === 1));
    const base = flow >= 2 ? 3 : 4 + Number(setup);
    const units = repair ? 1 : Math.max(0, Math.floor((total - base) / 2));
    const next = active ? sssNext(turns, setup, units, repair, flow) : undefined;
    if (next) view.current = { index: saved + 1, label: streamHeading || next.label };
    const phases = repair ? SSS_REPAIR_PHASES : SSS_PHASES.filter((phase) => (phase.kind !== "setup" || setup) && (phase.kind !== "plan" || flow < 2));
    view.phases = phases.map(({ kind, label }) => {
        const done = turns.filter((turn) => turn.kind === kind).length;
        const planned = kind === "card" || kind === "compile" ? units : 1;
        const state = planned > 0 && done >= planned ? "done" : next?.kind === kind ? "current" : "pending";
        return { kind, label, done, planned, state };
    });
    return view;
}

/** 「已保存 5/12 轮（预估总轮数…）」。 */
export function directorRoundsText(view: DirectorRunView) {
    if (!view.total) return view.saved ? `已保存 ${view.saved} 轮` : "";
    return `已保存 ${view.saved}/${view.total} 轮（${view.estimated ? "预估总轮数，切分出单元后改成实际" : "实际总轮数"}）`;
}

/** 阶段标签：「立项 ✓」「讲戏（正在写）」「单元卡 1/4」。 */
export function directorPhaseText(phase: DirectorPhase) {
    const count = phase.planned > 1 ? ` ${phase.done}/${phase.planned}` : "";
    return `${phase.label}${count}${phase.state === "done" ? " ✓" : phase.state === "current" ? "（正在写）" : ""}`;
}
