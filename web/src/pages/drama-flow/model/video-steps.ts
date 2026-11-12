import type { VideoChain, VideoChainStep } from "@/services/api/project-video-chains";

export type ShotVideoState = "running" | "waiting" | "failed" | "succeeded" | "cancelled";

export type ShotVideoStep = { chainId: string; unitId: string; shotId: string; title: string; state: ShotVideoState; error?: string; chainActive: boolean; step: VideoChainStep };

const chainActive = (chain: VideoChain) => chain.status === "queued" || chain.status === "running";

/**
 * 每一段只看它最近一次出片（chains 按创建时间倒序）：旧的失败在重试成功后不再算失败。
 * 预览用的 chain（没有 id、status=preview）不算出片。
 */
export function latestVideoSteps(chains: VideoChain[]): Map<string, ShotVideoStep> {
    const byShot = new Map<string, ShotVideoStep>();
    for (const chain of chains) {
        if (!chain.id || chain.status === "preview" || chain.prepareOnly) continue;
        const active = chainActive(chain);
        for (const step of chain.steps) {
            if (byShot.has(step.shotId)) continue;
            const state: ShotVideoState =
                step.status === "succeeded" ? "succeeded" : step.status === "failed" ? "failed" : active ? (step.status === "running" || step.status === "queued" ? "running" : "waiting") : chain.status === "failed" ? "failed" : "cancelled";
            byShot.set(step.shotId, { chainId: chain.id, unitId: chain.unitId, shotId: step.shotId, title: step.title, state, error: step.error || (state === "failed" ? chain.error : undefined), chainActive: active, step });
        }
    }
    return byShot;
}

export function activeChainIds(chains: VideoChain[]) {
    return chains.flatMap((chain) => (chain.id && chainActive(chain) ? [chain.id] : []));
}
