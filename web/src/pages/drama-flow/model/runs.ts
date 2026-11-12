import type { GenerationTask, TaskStatus } from "@/services/api/task-center";

import { taskCost } from "./costs";

/** 运行记录的一行：哪一步、做的哪一集或哪一段、用的模型、用时、花费、结果。 */
export type RunRow = {
    id: string;
    step: string;
    scope: string;
    model: string;
    status: TaskStatus;
    statusLabel: string;
    createdAt: string;
    /** 用时（秒）；没开始或时间不全时 undefined。 */
    seconds?: number;
    cost?: number;
    error?: string;
    active: boolean;
};

const OPERATION_LABELS: Record<string, string> = {
    "script-adapt": "剧本优化",
    "style-lock": "定画风",
    "scene-palette": "场次色卡",
    assets: "提取资产",
    storyboard: "生成分镜",
};

const STATUS_LABELS: Record<TaskStatus, string> = { queued: "排队中", running: "进行中", succeeded: "完成", failed: "失败", cancelled: "已取消" };

/** 任务属于哪一步：先看 clientContext 标的环节，再按任务类型。 */
export function runStepLabel(task: Pick<GenerationTask, "type" | "clientContext">) {
    const operation = task.clientContext?.chapterOperation;
    if (operation && OPERATION_LABELS[operation]) return OPERATION_LABELS[operation];
    if (task.type === "project_video_chain") return "连续出片";
    if (task.type === "canvas_video") return "出视频";
    if (task.type === "canvas_image") return "出图";
    if (task.type === "canvas_audio") return "配音";
    if (task.type === "timeline_render") return "导出成片";
    if (task.type === "timeline_transcription") return "台词转写";
    if (task.type.startsWith("canvas_text")) return "文本生成";
    return task.type;
}

export function runRow(task: GenerationTask, scopeOf: (task: GenerationTask) => string = () => ""): RunRow {
    const started = Date.parse(task.startedAt || "");
    const ended = Date.parse(task.completedAt || "");
    const seconds = Number.isFinite(started) && Number.isFinite(ended) && ended >= started ? Math.round((ended - started) / 1000) : undefined;
    return {
        id: task.id,
        step: runStepLabel(task),
        scope: scopeOf(task),
        model: task.model || "",
        status: task.status,
        statusLabel: STATUS_LABELS[task.status] || task.status,
        createdAt: task.createdAt,
        seconds,
        cost: taskCost(task),
        error: task.status === "failed" ? task.error || "没写原因" : undefined,
        active: task.status === "queued" || task.status === "running",
    };
}

export function durationText(seconds: number | undefined) {
    if (seconds === undefined) return "";
    if (seconds < 60) return `${seconds} 秒`;
    const minutes = Math.floor(seconds / 60);
    return minutes < 60 ? `${minutes} 分 ${seconds % 60} 秒` : `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分`;
}
