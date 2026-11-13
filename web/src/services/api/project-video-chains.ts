import { http } from "./request";
import type { LogicalModelQuote } from "./logical-models";

export interface VideoChainModel {
    model: string;
    logicalModelId?: string;
    input: Record<string, unknown>;
}
export interface VideoChainRequest {
    prepareOnly?: boolean;
    scope?: "scene" | "single";
    unitId: string;
    shotId: string;
    model: VideoChainModel;
    models?: Record<string, VideoChainModel>;
    previewHash?: string;
    requestId?: string;
}
export interface VideoChainStep {
    shotId: string;
    revisionId: string;
    title: string;
    scene: string;
    contentSeconds: number;
    generationSeconds: number;
    model: string;
    status: string;
    phase: string;
    error?: string;
    referenceWarning?: string;
    referenceSkipped?: boolean;
    taskId?: string;
    videoResourceId?: string;
    frameResourceId?: string;
    frameAssetId?: string;
    frameTimeSeconds?: number;
    frameSelection?: string;
    sourceRevisionId?: string;
    sourceVideoTaskId?: string;
    sourceVideoResourceId?: string;
    sourceShotId?: string;
    referenceTag?: string;
    referenceCount: number;
    ending: boolean;
    quote?: LogicalModelQuote;
}
export interface VideoChain {
    whiteModelStoryboardEnabled: boolean;
    scope?: "scene" | "single";
    prepareOnly?: boolean;
    id?: string;
    projectId: string;
    unitId: string;
    status: string;
    stage: string;
    error?: string;
    previewHash: string;
    totalMicrocredits: number;
    steps: VideoChainStep[];
}
const base = (projectId: string) => `/projects/${encodeURIComponent(projectId)}/video-chains`;
export type VideoChainList = { chains: VideoChain[] };
export const listVideoChains = (projectId: string, unitId: string, signal?: AbortSignal) => http.get<VideoChainList>(base(projectId), { params: { unitId }, signal });

/** 一集出片链列表的缓存 key。新工作流和连续生成面板共用这个 key。 */
export const videoChainsKey = (projectId: string, unitId: string) => ["project-video-chains", projectId, unitId] as const;

/**
 * 一集出片链列表的查询（key + 取数），所有调用方都用它。
 * 同一个 key 只缓存接口原样的 `{ chains }`：不同调用方各自解包成不同形状写进同一个 key，
 * 后挂载的一方会读到另一方的形状而崩溃（短剧新工作流写数组、连续生成面板读 `.chains.find`）。
 * 要数组的调用方自己读 `.chains`；轮询判断（refetchInterval）拿到的也是这个原样结构。
 */
export const videoChainsQuery = (projectId: string, unitId: string) => ({
    queryKey: videoChainsKey(projectId, unitId),
    queryFn: ({ signal }: { signal?: AbortSignal }) => listVideoChains(projectId, unitId, signal),
});
export const previewVideoChain = (projectId: string, input: VideoChainRequest) => http.post<{ chain: VideoChain }>(`${base(projectId)}/preview`, input);
export const createVideoChain = (projectId: string, input: VideoChainRequest) => http.post<{ chain: VideoChain }>(base(projectId), input);
export const retryVideoChain = (projectId: string, id: string) => http.post<{ chain: VideoChain }>(`${base(projectId)}/${encodeURIComponent(id)}/retry`);
export const cancelVideoChain = (projectId: string, id: string) => http.post<{ chain: VideoChain }>(`${base(projectId)}/${encodeURIComponent(id)}/cancel`);
