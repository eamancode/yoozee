import { http } from "@/services/api/request";

export type SLLCapabilities = {
    durationMin?: number;
    durationMax?: number;
    durations?: number[] | null;
    aspectRatios?: string[] | null;
    minImages?: number | null;
    maxImages?: number | null;
    maxVideos?: number | null;
    maxAudios?: number | null;
    maxReferences?: number | null;
    maxVideoSeconds?: number | null;
    generateAudio?: boolean | null;
};

export type SLLMetric = {
    state: "value" | "unavailable" | "fetch_failed" | "never";
    source: string;
    windowHours: number;
    rate?: number | null;
    sampleCount?: number | null;
    collectedAt?: string | null;
    attemptedAt?: string | null;
    stale: boolean;
};

export type SLLCatalogRow = {
    modelId: string;
    displayName: string;
    description?: string;
    group: string;
    groupRatio: number;
    enabled: boolean;
    available: boolean;
    reason?: string;
    capabilities: SLLCapabilities;
    tiers: { resolution: string; costMicroCny: number }[] | null;
    metric: SLLMetric;
};

export type SLLCatalogView = {
    group: string;
    needsGroupConfirmation?: boolean;
    source: string;
    version?: string;
    collectedAt?: string | null;
    added: number;
    updated: number;
    excluded: { modelId: string; resolution?: string; reason: string }[] | null;
    rows: SLLCatalogRow[] | null;
};

function path(id: string) {
    return `/admin/channels/${encodeURIComponent(id)}/sll-catalog`;
}

export function getSLLCatalog(id: string, signal?: AbortSignal) {
    return http.get<SLLCatalogView>(path(id), { signal });
}

export function syncSLLCatalog(id: string, group: string) {
    return http.post<SLLCatalogView>(`${path(id)}/sync`, { group });
}

export function refreshSLLMetrics(id: string) {
    return http.post(`${path(id)}/metrics`);
}
