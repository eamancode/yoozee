import { http } from "@/services/api/request";

/** 编译档案的一个版本：content 只写要改的项，读取时叠在内置默认（版本 0）上。 */
export type DramaCompileProfileVersion = {
    id: string;
    version: number;
    name: string;
    note: string;
    enabled: boolean;
    createdBy: string;
    createdAt: string;
    updatedAt: string;
    content: Record<string, unknown>;
};

export type DramaCompileProfileList = {
    profiles: DramaCompileProfileVersion[];
    /** 生效版本号，0 表示内置默认。 */
    activeVersion: number;
    /** 内置默认全文（风格块与表演块已按来源文件填好）。 */
    default: Record<string, unknown>;
};

export function listAdminDramaCompileProfiles() {
    return http.get<DramaCompileProfileList>("/admin/drama/compile-profiles");
}

export function createAdminDramaCompileProfile(input: { name: string; note: string; content: unknown; enabled: boolean }) {
    return http.post<{ profile: DramaCompileProfileVersion }>("/admin/drama/compile-profiles", input);
}

export function activateAdminDramaCompileProfile(id: string) {
    return http.post<{ ok: boolean }>(`/admin/drama/compile-profiles/${encodeURIComponent(id)}/activate`);
}

export function revertAdminDramaCompileProfile() {
    return http.post<{ ok: boolean }>("/admin/drama/compile-profiles/revert");
}

export function deleteAdminDramaCompileProfile(id: string) {
    return http.delete<{ ok: boolean }>(`/admin/drama/compile-profiles/${encodeURIComponent(id)}`);
}

/** 评测指标表的一行（一个档案版本 × 一章）。specRate 为 -1 表示老镜头表没有分镜行原文、算不出来。 */
export type DramaEvalRow = {
    unit: string;
    version: number;
    versionName: string;
    source: "rows" | "shotlist";
    errors: number;
    warnings: number;
    rows: number;
    specRate: number;
    shots: number;
    segments: number;
    totalSeconds: number;
    minSeconds: number;
    maxSeconds: number;
    chars: number;
    avgChars: number;
    maxChars: number;
    overLimit: number;
    overSoft: number;
    compileWarns: number;
    packProblems: number;
    failure?: string;
};

export type DramaEvalSegment = { unit: string; segment: string; seconds: number; candidate: string; baseline: string; candidateChars: number; baselineChars: number };

export type DramaEvalRun = {
    id?: string;
    stage: "compile";
    projectId?: string;
    model: string;
    candidate: { version: number; name: string };
    baseline: { version: number; name: string };
    rows: DramaEvalRow[];
    segments?: DramaEvalSegment[];
    markdown: string;
    createdAt: string;
};

/** 评测重跑：视频提示词阶段用已存镜头表重新装段、编译（不调模型），对比候选与基线编译档案。 */
export function runAdminDramaEval(input: { projectId: string; unitIds?: string[]; candidateVersion: number; baselineVersion?: number; model?: string }) {
    return http.post<{ run: DramaEvalRun }>("/admin/drama/eval", { stage: "compile", ...input });
}

export function listAdminDramaEvalRuns() {
    return http.get<{ runs: DramaEvalRun[] }>("/admin/drama/eval-runs");
}
