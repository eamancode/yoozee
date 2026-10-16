import { ApiError, http } from "@/services/api/request";
import type { GenerationTask } from "@/services/api/task-center";
import type { ShotArtifact } from "@/services/api/projects";

/** 可选的视频生成模型族（kb-dialects/models.json）。一段最长 duration.max 秒。 */
export type DramaModel = {
    id: string;
    label: string;
    vendor: string;
    kind: string;
    duration: { min: number; max: number; step: number };
    refs: { images: number | null; videos: number | null; audios: number | null; total: number | null };
    frames_exclusive_with_refs: boolean | null;
    prompt_max_chars: number | null;
    resolutions: string[];
    calibrated: boolean;
    checked: string;
};

export type DramaRegistry = { version: string; default: string; models: DramaModel[] };

export type DramaAuthMode = "step" | "full";

export type DramaLine = { speaker: string; text: string; vo?: boolean; delivery?: string };
export type DramaSubShot = { at: number; size?: string; camera?: string; text: string; lines?: DramaLine[] };
/** 契约 segment：一段 = 一次生成，段内多镜头。 */
export type DramaSegment = {
    id: string;
    scene: string;
    title?: string;
    dur: number;
    mode: "lock_identity" | "lock_first_frame" | "text_only";
    first_frame?: string;
    last_frame?: string;
    refs?: Array<{ file: string; role: string; note?: string }>;
    voices?: Array<{ character: string; file: string }>;
    shots: DramaSubShot[];
    sound?: string;
    rules?: string;
    hook?: string;
    lang?: string;
};

export type DramaIssue = { level: "error" | "warn"; message: string };
export type DramaCompileResult = {
    shotId: string;
    spec: DramaSegment;
    derived: boolean;
    result: { segment: string; model: string; duration: number; prompt: string; files: Array<{ file: string; tag: string; role: string }>; chars: number; issues: DramaIssue[] };
};

export type DramaSegmentProgress = {
    shotId: string;
    unitId: string;
    unitTitle?: string;
    segmentId: string;
    title: string;
    durationMs: number;
    overLimit: boolean;
    hasDialogue: boolean;
    script: boolean;
    refs: boolean;
    generated: boolean;
    reviewed: boolean;
    inCut: boolean;
    accepted: boolean;
    takes: number;
    selectedArtifactId?: string;
    missingEvidence?: string[];
};

export type DramaEndState = { key: string; label: string; done: boolean; detail: string; skill: string };

export type DramaProgress = {
    model: DramaModel;
    authMode: DramaAuthMode | "";
    segments: DramaSegmentProgress[];
    counts: { segments: number; script: number; refs: number; generated: number; takes: number; reviewed: number; inCut: number; accepted: number; overLimit: number };
    line: string;
    endStates: DramaEndState[];
    next?: DramaEndState;
};

export type DramaEvidenceKind = "transcript" | "frames" | "faces" | "levels" | "voice" | "pick" | "gate" | "review" | "delivery" | "acceptance";

export type DramaEvidence = {
    id: string;
    projectId: string;
    unitId?: string;
    shotId?: string;
    artifactId?: string;
    kind: DramaEvidenceKind;
    passed: boolean | null;
    metricsJson: string;
    resourceId?: string;
    taskId?: string;
    note: string;
    createdAt: string;
};

export type DramaGateCheck = { name: string; ok: boolean; detail: string };
export type DramaGateResult = { report: { passed: boolean; checks: DramaGateCheck[]; metrics: Record<string, unknown> }; evidence: DramaEvidence };

export function listDramaModels() {
    return http.get<{ models: DramaRegistry }>("/drama/models");
}

export function getDramaProgress(projectId: string, signal?: AbortSignal) {
    return http.get<{ progress: DramaProgress }>(`/projects/${projectId}/drama/progress`, { signal });
}

export function getDramaBrief(projectId: string) {
    return http.get<{ document: Record<string, unknown> }>(`/projects/${projectId}/drama/brief`);
}

export function putDramaBrief(projectId: string, document: Record<string, unknown>) {
    return http.put<{ document: Record<string, unknown> }>(`/projects/${projectId}/drama/brief`, { document });
}

export function compileProjectShot(projectId: string, shotId: string, input: { model?: string } = {}) {
    return http.post<{ compiled: DramaCompileResult }>(`/projects/${projectId}/shots/${shotId}/compile`, input);
}

export function listDramaEvidence(projectId: string, signal?: AbortSignal) {
    return http.get<{ evidence: DramaEvidence[] }>(`/projects/${projectId}/drama/evidence`, { signal });
}

export function createDramaEvidence(projectId: string, input: { artifactId?: string; shotId?: string; kind: DramaEvidenceKind; passed?: boolean; note?: string; taskId?: string }) {
    return http.post<{ evidence: DramaEvidence }>(`/projects/${projectId}/drama/evidence`, input);
}

export function pickShotArtifact(projectId: string, artifactId: string, reason: string) {
    return http.post<{ artifact: ShotArtifact }>(`/projects/${projectId}/artifacts/${artifactId}/pick`, { reason });
}

export function transcribeShotArtifact(projectId: string, artifactId: string) {
    return http.post<{ task: GenerationTask }>(`/projects/${projectId}/artifacts/${artifactId}/transcribe`, {});
}

export function framesShotArtifact(projectId: string, artifactId: string) {
    return http.post<{ evidence: DramaEvidence }>(`/projects/${projectId}/artifacts/${artifactId}/frames`, {});
}

export function runDramaGate(projectId: string, input: { resourceId?: string; artifactId?: string; targetLufs?: number }) {
    return http.post<{ gate: DramaGateResult }>(`/projects/${projectId}/drama/gate`, input);
}

/** 下载交付包（zip）。服务端同时记一条「已交付」证据。 */
export async function downloadDramaDelivery(projectId: string, fallbackName = "交付包.zip") {
    let response;
    try {
        response = await http.raw<Blob>({ method: "get", url: `/projects/${projectId}/drama/delivery`, responseType: "blob" });
    } catch (error) {
        // 下载请求出错时 axios 把 JSON 错误包成 Blob，这里还原成可读的提示。
        const blob = error instanceof ApiError ? (error.cause as { response?: { data?: unknown } } | undefined)?.response?.data : undefined;
        if (blob instanceof Blob && error instanceof ApiError) {
            try {
                const envelope = JSON.parse(await blob.text()) as { msg?: string; code?: number; reason?: string };
                if (envelope?.msg) throw new ApiError(envelope.msg, { status: error.status, code: envelope.code, reason: envelope.reason, cause: error });
            } catch (parsed) {
                if (parsed instanceof ApiError) throw parsed;
            }
        }
        throw error;
    }
    const disposition = String(response.headers?.["content-disposition"] || "");
    const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition)?.[1];
    const fileName = encoded ? decodeURIComponent(encoded) : fallbackName;
    const url = URL.createObjectURL(response.data);
    const link = document.createElement("a");
    link.href = url;
    link.download = fileName;
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    return fileName;
}

export function parseEvidenceMetrics(evidence: DramaEvidence): Record<string, unknown> {
    try {
        const parsed = JSON.parse(evidence.metricsJson || "{}");
        return parsed && typeof parsed === "object" ? parsed : {};
    } catch {
        return {};
    }
}
