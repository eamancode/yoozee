import type { DramaPipeline, DramaPipelineStageKey } from "@/services/api/project-drama";

/**
 * 短剧新工作流把服务端的 8 格流水线收成用户看得懂的 5 步：
 * 剧本（script）→ 定妆（style + palette + assets）→ 分镜（shots + pack）→ 出片（video）→ 成片（cut）。
 * 这里只做展示归并，流水线怎么走仍以服务端 DramaPipeline 为准。
 */
export type FlowStageKey = "script" | "look" | "shots" | "video" | "cut";

export type FlowStageDef = { key: FlowStageKey; label: string; hint: string; pipelineKeys: DramaPipelineStageKey[] };

export const FLOW_STAGES: FlowStageDef[] = [
    { key: "script", label: "剧本", hint: "拆成一集一集的剧本", pipelineKeys: ["script"] },
    { key: "look", label: "定妆", hint: "画风、色卡、角色和场景", pipelineKeys: ["style", "palette", "assets"] },
    { key: "shots", label: "分镜", hint: "每一段拍什么", pipelineKeys: ["shots", "pack"] },
    { key: "video", label: "出片", hint: "逐段生成视频", pipelineKeys: ["video"] },
    { key: "cut", label: "成片", hint: "挑片、剪辑、导出", pipelineKeys: ["cut"] },
];

/** done 做完；running 正在做；blocked 停在这一步等人处理；ready 轮到这一步但还没开始；pending 还没轮到。 */
export type FlowStageState = "done" | "running" | "blocked" | "ready" | "pending";

export type FlowStageView = FlowStageDef & { state: FlowStageState; detail: string; index: number };

export function isFlowStageKey(value: unknown): value is FlowStageKey {
    return typeof value === "string" && FLOW_STAGES.some((stage) => stage.key === value);
}

export function flowStageOfPipeline(key: DramaPipelineStageKey | undefined): FlowStageKey | undefined {
    return key ? FLOW_STAGES.find((stage) => stage.pipelineKeys.includes(key))?.key : undefined;
}

/** 没有流水线数据（接口还没回、或老项目没有流水线）时 5 步都按「还没轮到」展示，不猜进度。 */
export function flowStageStates(pipeline: DramaPipeline | undefined): FlowStageView[] {
    const stopStage = flowStageOfPipeline(pipeline?.stop?.stage);
    const currentStage = flowStageOfPipeline(pipeline?.current);
    let readyGiven = false;
    return FLOW_STAGES.map((def, index) => {
        const parts = pipeline?.stages.filter((stage) => def.pipelineKeys.includes(stage.key)) ?? [];
        const detail = (parts.find((stage) => stage.state === "stopped" || stage.state === "running") ?? [...parts].reverse().find((stage) => stage.detail))?.detail || "";
        let state: FlowStageState = "pending";
        if (parts.length && parts.every((stage) => stage.state === "done")) state = "done";
        else if (stopStage === def.key || parts.some((stage) => stage.state === "stopped")) state = "blocked";
        else if (currentStage === def.key || parts.some((stage) => stage.state === "running")) state = "running";
        else if (parts.some((stage) => stage.state === "done")) state = "running";
        if (state === "pending" && pipeline && !readyGiven && !stopStage && !currentStage) {
            // 没有在跑也没有停住：第一个没做完的步骤就是轮到的那一步。
            state = "ready";
        }
        if (state !== "done") readyGiven = true;
        return { ...def, state, detail, index };
    });
}

/** 打开项目时默认落在哪一步：停住的那步 > 正在做的那步 > 第一个没做完的 > 最后一步。 */
export function defaultFlowStage(pipeline: DramaPipeline | undefined): FlowStageKey {
    const views = flowStageStates(pipeline);
    return (views.find((stage) => stage.state === "blocked") ?? views.find((stage) => stage.state === "running") ?? views.find((stage) => stage.state !== "done") ?? views[views.length - 1]).key;
}
