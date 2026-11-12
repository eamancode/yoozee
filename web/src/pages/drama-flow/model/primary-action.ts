import type { DramaPipeline } from "@/services/api/project-drama";

import { flowStageOfPipeline, type FlowStageKey } from "./stages";

/**
 * 底栏「现在该做什么」的唯一规则：同一时刻只给一个主按钮，一句话说清状态。
 * 纯函数，输入是页面已经读到的事实，不发请求；列表卡片和主页面底栏共用，保证两处说法一致。
 */
export type FlowFacts = {
    pipeline?: DramaPipeline;
    /** 段落计数：总段数、已出片、已进成片。 */
    segments: number;
    generated: number;
    inCut: number;
    /** 必须处理的待办数（失败、超时长、校验问题）。 */
    mustHandle: number;
    /** 等你确认的待办数。 */
    toConfirm: number;
    failedVideos: number;
    runningVideos: number;
    /** 积分不够开下一批视频；不知道（没读到报价）时传 false，不拦。 */
    balanceShort?: boolean;
    exported?: boolean;
};

export type FlowActionKind = "continue" | "open_inbox" | "start_video" | "retry_videos" | "stop_video" | "recharge" | "go_stage" | "download";

export type FlowAction = { kind: FlowActionKind; label: string; stage?: FlowStageKey };

/** working 自动在做；attention 等你处理；ready 轮到你点一下；done 都做完了；idle 还没有数据。 */
export type FlowTone = "working" | "attention" | "ready" | "done" | "idle";

export type FlowPrimary = { tone: FlowTone; status: string; primary?: FlowAction; secondary?: FlowAction };

export function flowPrimaryAction(facts: FlowFacts): FlowPrimary {
    const { pipeline } = facts;
    if (!pipeline) return { tone: "idle", status: "正在读取进度…" };
    const stop = pipeline.stop;
    const running = !stop && pipeline.current ? pipeline.stages.find((stage) => stage.key === pipeline.current) : undefined;

    // 1. 有必须处理的问题：不处理后面走不下去，优先级最高。
    if (facts.mustHandle > 0) {
        return {
            tone: "attention",
            status: `有 ${facts.mustHandle} 件事需要你处理，处理完才能继续`,
            primary: { kind: "open_inbox", label: `处理 ${facts.mustHandle} 件待办` },
            secondary: facts.failedVideos > 0 ? { kind: "retry_videos", label: `重新出失败的 ${facts.failedVideos} 段（先看花费）` } : undefined,
        };
    }

    // 2. 视频正在出：只给「停止」，其余等它出完。
    if (facts.runningVideos > 0) {
        return {
            tone: "working",
            status: `正在出片：${facts.generated}/${facts.segments} 段已出好，可以先离开，出完会留在这里`,
            secondary: { kind: "stop_video", label: "停止出片" },
        };
    }

    // 3. 文字和图片环节正在自动做：不需要人。
    if (running) {
        return { tone: "working", status: `正在做「${running.label}」${running.detail ? `：${running.detail}` : ""}，可以先离开` };
    }

    if (stop) {
        const stage = flowStageOfPipeline(stop.stage);
        // 4. 停在出视频：这是唯一要你确认花费的地方。
        if (stop.stage === "video") {
            if (facts.balanceShort) return { tone: "attention", status: "积分不够出下一批视频", primary: { kind: "recharge", label: "去充值" }, secondary: { kind: "go_stage", label: "先看分镜", stage: "shots" } };
            const left = Math.max(facts.segments - facts.generated, 0);
            return {
                tone: "ready",
                status: facts.generated > 0 ? `还有 ${left} 段没出片` : `分镜已备好，共 ${facts.segments} 段，确认花费后开始出片`,
                primary: { kind: "start_video", label: facts.generated > 0 ? "继续出片" : "开始出片" },
                secondary: facts.toConfirm > 0 ? { kind: "open_inbox", label: `${facts.toConfirm} 件待确认` } : undefined,
            };
        }
        // 5. 停在成片：挑片、导出。
        if (stop.stage === "cut") {
            if (facts.exported) return { tone: "done", status: "成片已导出", primary: { kind: "download", label: "下载成片" } };
            return {
                tone: "ready",
                status: facts.toConfirm > 0 ? `视频都出好了，还有 ${facts.toConfirm} 段等你挑片` : "视频都出好了，可以剪辑导出",
                primary: facts.toConfirm > 0 ? { kind: "open_inbox", label: "去挑片" } : { kind: "go_stage", label: "去成片", stage: "cut" },
            };
        }
        // 6. 停在前面的环节且能接着做：一键继续。
        if (stop.continuable) {
            return { tone: "ready", status: stop.reason || `停在「${stop.label}」`, primary: { kind: "continue", label: stop.action || `继续：${stop.label}` }, secondary: stage ? { kind: "go_stage", label: "先看一眼", stage } : undefined };
        }
        // 7. 停住但服务端不让一键继续：带到对应步骤里处理。
        return { tone: "attention", status: stop.reason || `停在「${stop.label}」，需要你处理`, primary: { kind: "go_stage", label: stop.action || `去处理：${stop.label}`, stage } };
    }

    if (pipeline.stages.length && pipeline.stages.every((stage) => stage.state === "done")) {
        return { tone: "done", status: "全部做完了", primary: { kind: "download", label: "下载成片" } };
    }
    return { tone: "idle", status: "还没开始制作" };
}
