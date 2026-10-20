import { ApiError, http } from "@/services/api/request";
import type { GenerationTask } from "@/services/api/task-center";
import type { ShotArtifact } from "@/services/api/projects";

/** 可选的视频生成模型族（kb-dialects/models.json）。一段最长 duration.max 秒。 */
export type DramaModel = {
    id: string;
    label: string;
    vendor: string;
    kind: string;
    /** 渠道模型名里出现这些片段就算这个模型族。 */
    match: string[];
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

/** 连戏表（契约 bible）：也是素材清单。每项指向文件（柚子里是资产版本 ID），checked 为 true 才算齐。 */
export type DramaBibleVariant = { label: string; age?: string; face_hair?: string; glasses?: string; marks?: string; costume?: string; lock_card?: string[]; sheet?: string; crops?: string[]; voice?: string; voice_short?: string; checked?: boolean };
export type DramaBibleCharacter = { name: string; speaks?: boolean; variants: DramaBibleVariant[] };
export type DramaBibleProp = { name: string; image?: string; states?: Array<{ when?: string; desc?: string; image?: string }>; checked?: boolean };
export type DramaBibleLocation = { name: string; plate?: string; side_a?: string; side_b?: string; fixtures?: string; light?: string; checked?: boolean };
export type DramaBible = { characters?: DramaBibleCharacter[]; props?: DramaBibleProp[]; locations?: DramaBibleLocation[]; [key: string]: unknown };

export function getDramaBible(projectId: string) {
    return http.get<{ document: DramaBible }>(`/projects/${projectId}/drama/bible`);
}

export function putDramaBible(projectId: string, document: DramaBible) {
    return http.put<{ document: DramaBible }>(`/projects/${projectId}/drama/bible`, { document });
}

/** 一章里的一段：derived 为 true 表示旧镜头没有分段 JSON，段号按位置合成，只能看不能拆合。 */
export type DramaSegmentView = { shotId: string; unitId: string; title: string; position: number; durationMs: number; revisionId?: string; spec: DramaSegment; derived: boolean };

export function listDramaSegments(projectId: string, unitId: string, signal?: AbortSignal) {
    return http.get<{ segments: DramaSegmentView[] }>(`/projects/${projectId}/drama/segments`, { params: { unitId }, signal });
}

/**
 * 按段号写入一章的段（后端按项目生成模型校验段长）。拆段、合段时给 order：本章全部段号的新顺序，
 * 不在其中的段连同生成结果删除；只改段内容时不给。
 */
export function writeDramaSegments(projectId: string, input: { unitId: string; segments: DramaSegment[]; order?: string[] }) {
    return http.put<{ segments: DramaSegmentView[] }>(`/projects/${projectId}/drama/segments`, input);
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
