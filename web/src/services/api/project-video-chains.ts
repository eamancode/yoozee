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
export const listVideoChains = (projectId: string, unitId: string, signal?: AbortSignal) => http.get<{ chains: VideoChain[] }>(base(projectId), { params: { unitId }, signal });
export const previewVideoChain = (projectId: string, input: VideoChainRequest) => http.post<{ chain: VideoChain }>(`${base(projectId)}/preview`, input);
export const createVideoChain = (projectId: string, input: VideoChainRequest) => http.post<{ chain: VideoChain }>(base(projectId), input);
export const retryVideoChain = (projectId: string, id: string) => http.post<{ chain: VideoChain }>(`${base(projectId)}/${encodeURIComponent(id)}/retry`);
export const cancelVideoChain = (projectId: string, id: string) => http.post<{ chain: VideoChain }>(`${base(projectId)}/${encodeURIComponent(id)}/cancel`);
