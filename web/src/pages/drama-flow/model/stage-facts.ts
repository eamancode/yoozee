import type { DramaSegmentProgress } from "@/services/api/project-drama";

import type { ReadState } from "./read-state";
import type { FlowStageKey } from "./stages";
import type { ShotVideoStep } from "./video-steps";

/**
 * 阶段条每一步下面那行数字、集列表每一集的数字。
 * 没读到的数据传 undefined，对应的那一截就不写，不拿 0 顶替。
 */
export type StageFactsInput = {
    episodes?: number;
    /** 画风锁定状态；没读到传 undefined。 */
    style?: "none" | "anchor_only" | "locked";
    palettes?: { ready: number; total: number };
    assets?: { bound: number; total: number; failed: number };
    counts?: { segments: number; generated: number; inCut: number; overLimit: number; issues?: number };
    videos?: { failed: number; running: number };
};

export function stageFacts(input: StageFactsInput): Record<FlowStageKey, string> {
    const join = (parts: Array<string | false | undefined>) => parts.filter(Boolean).join(" · ");
    const { counts, videos } = input;
    return {
        script: input.episodes === undefined ? "" : input.episodes ? `${input.episodes} 集` : "还没有剧集",
        look: join([
            input.style === undefined ? undefined : input.style === "locked" ? "画风已定" : input.style === "anchor_only" ? "画风待锁定" : "画风未定",
            input.palettes && input.palettes.total ? `色卡 ${input.palettes.ready}/${input.palettes.total}` : undefined,
            input.assets && input.assets.total ? `资产 ${input.assets.bound}/${input.assets.total}` : undefined,
            input.assets?.failed ? `失败 ${input.assets.failed}` : undefined,
        ]),
        shots: counts ? join([`${counts.segments} 段`, counts.overLimit ? `超时长 ${counts.overLimit}` : undefined, counts.issues ? `问题 ${counts.issues}` : undefined]) : "",
        video: counts && counts.segments ? join([`已出 ${counts.generated}/${counts.segments}`, videos?.running ? `在出 ${videos.running}` : undefined, videos?.failed ? `失败 ${videos.failed}` : undefined]) : "",
        cut: counts && counts.segments ? `入片 ${counts.inCut}/${counts.segments}` : "",
    };
}

export type EpisodeFacts = { segments: number; generated: number; failed: number; running: number; issues: number };

/** 一集的段数、已出、失败、在出、带问题的段。 */
export function episodeFacts(unitId: string, segments: DramaSegmentProgress[], steps: Map<string, ShotVideoStep>): EpisodeFacts {
    const facts: EpisodeFacts = { segments: 0, generated: 0, failed: 0, running: 0, issues: 0 };
    for (const segment of segments) {
        if (segment.unitId !== unitId) continue;
        facts.segments += 1;
        if (segment.generated) facts.generated += 1;
        if (segment.overLimit || (segment.issues ?? 0) > 0) facts.issues += 1;
        const step = steps.get(segment.shotId);
        if (step?.state === "failed") facts.failed += 1;
        else if (step && (step.state === "running" || step.state === "waiting")) facts.running += 1;
    }
    return facts;
}

/** 这一集的出片链没读到时，不写「在出」「败」，改写读取状态，免得把失败的段藏掉。 */
export function episodeFactsLine(facts: EpisodeFacts, videoRead: ReadState = "ready") {
    if (!facts.segments) return "还没分镜";
    const video = videoRead === "ready" ? [facts.running ? `在出 ${facts.running}` : "", facts.failed ? `败 ${facts.failed}` : ""] : [videoRead === "loading" ? "读出片状态…" : "出片状态没读到"];
    return [`${facts.segments} 段`, `出 ${facts.generated}`, ...video].filter(Boolean).join(" · ");
}

/** 矩阵视图一格的完成度：0 还没出，1 全部出完。没有段时返回 undefined。 */
export function episodeCompletion(facts: EpisodeFacts) {
    return facts.segments ? facts.generated / facts.segments : undefined;
}
