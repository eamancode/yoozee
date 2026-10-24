import { ApiError, http } from "@/services/api/request";
import { API_ERROR_REASON } from "@/services/api/error-codes";

/** 静态校验不过的一条原因；rule 与 backend/internal/drama/step_package.go、yoozee-skills/build.py 同一套编号。 */
export type DramaPackageProblem = { rule: string; message: string; path?: string };

/** 一个版本按当前线上步骤表重跑静态校验的结论；not_package 是内置或在线编辑留下的历史版本，不能在流程里启用。 */
export type DramaPackageCheck = { status: "passed" | "failed" | "not_package"; problems: DramaPackageProblem[] };

/** 评测状态：排队 / 运行中 / 通过 / 未通过（门槛挡下）/ 出错（任务没跑成，不是包的问题，可重跑）。 */
export type DramaStepEvalStatus = "queued" | "running" | "passed" | "failed" | "error";

/** 评测集里的一章（按风格档案配置）。 */
export type DramaEvalChapter = { id: string; profile: string; label: string };

/** 一章真跑的结果；指标与 backend/internal/drama/step_gate.go 一致。 */
export type DramaEvalChapterResult = {
    chapter: string;
    label: string;
    profile: string;
    taskId?: string;
    parsed: boolean;
    truncated: boolean;
    failure?: string;
    rows: number;
    specRows: number;
    linesTotal: number;
    linesExact: number;
    errors: number;
    warnings: number;
    rules?: Record<string, number>;
    estSeconds: number;
    targetSeconds: number;
    shots: number;
    segments: number;
    segmentSeconds: number;
    packProblems: number;
};

/** 一项门槛的判定；比率是 0～1，warn / error / 截断是条数，估时是相对每集时长的倍数。 */
export type DramaGateCheck = { metric: string; label: string; value: number; limit: number; baseline?: number; passed: boolean; detail: string };

export type DramaStepEval = {
    id: string;
    step: string;
    stepLabel: string;
    skillId: string;
    skillVersionId: string;
    baselineSkillVersionId?: string;
    status: DramaStepEvalStatus;
    model: string;
    chapters: DramaEvalChapter[];
    tasksDone: number;
    tasksTotal: number;
    gate?: { passed: boolean; checks: DramaGateCheck[] };
    /** 逐章结果：candidate 是这个版本，baseline 是当时的启用版本（baselineRunId 非空表示复用了那次评测的结果）。 */
    result?: { candidate: DramaEvalChapterResult[]; baseline: DramaEvalChapterResult[]; baselineRunId?: string };
    error?: string;
    /** auto：过门槛自动启用；forced：管理员强制启用（activationReason 是原因）。 */
    activation?: "auto" | "forced";
    activationReason?: string;
    activatedBy?: string;
    activatedAt?: string;
    createdBy: string;
    createdAt: string;
    finishedAt?: string;
};

/** 评测用的文本模型：与章节页「生成分段」同一套字段（model、logicalModelId、backendProviderConfig 的 config）。 */
export type DramaStepEvalModel = { model: string; logicalModelId?: string; config: Record<string, unknown> };

export type DramaPackageVersion = {
    id: string;
    versionLabel: string;
    /** package：上传或首次安装的种子；builtin / online：M6 之前的内置或在线编辑版本。 */
    origin: string;
    contentHash: string;
    fileCount: number;
    totalBytes: number;
    note: string;
    step?: string;
    contract?: string;
    profiles: string[];
    createdBy: string;
    createdByName: string;
    createdAt: string;
    active: boolean;
    check: DramaPackageCheck;
    /** 最近一次评测；没评测过（历史版本、没选模型上传的）为空。 */
    eval?: DramaStepEval;
};

/** 一个流程技能包：contract 是生效版本声明的契约，stepContract 是线上这一步现在的契约。 */
export type DramaPackage = {
    id: string;
    name: string;
    description: string;
    step: string;
    stepLabel: string;
    contract: string;
    stepContract: string;
    versionCount: number;
    currentVersion?: DramaPackageVersion;
};

export function listAdminDramaPackages(signal?: AbortSignal) {
    return http.get<{ packages: DramaPackage[] }>("/admin/drama/packages", { signal });
}

export function listAdminDramaPackageVersions(packageId: string, signal?: AbortSignal) {
    return http.get<{ package: DramaPackage; versions: DramaPackageVersion[] }>(`/admin/drama/packages/${encodeURIComponent(packageId)}/versions`, { signal });
}

/**
 * 上传 build.py --package 构建的 zip：通过静态校验存成新版本（不启用），随即用 evalModel 在评测集上真跑评测，过门槛自动启用。
 * 评测没能开始时版本照样保存，原因在 evalError；静态校验不通过抛 ApiError，原因用 packageProblems 取。
 */
export function uploadAdminDramaPackage(file: File, note: string, evalModel?: DramaStepEvalModel) {
    const body = new FormData();
    body.append("file", file, file.name);
    if (note.trim()) body.append("note", note.trim());
    if (evalModel?.model) {
        body.append("model", evalModel.model);
        if (evalModel.logicalModelId) body.append("logicalModelId", evalModel.logicalModelId);
        body.append("config", JSON.stringify(evalModel.config));
    }
    return http.post<{ package: DramaPackage; version: DramaPackageVersion; evalError?: string }>("/admin/drama/packages", body);
}

/** 重跑一个版本的评测（上次出错、上传时没选模型，或想换模型再看）。 */
export function evalAdminDramaPackageVersion(packageId: string, versionId: string, evalModel: DramaStepEvalModel) {
    return http.post<{ eval: DramaStepEval }>(`/admin/drama/packages/${encodeURIComponent(packageId)}/versions/${encodeURIComponent(versionId)}/eval`, evalModel);
}

/** 启用一个版本；回退就是启用历史版本。评测没通过（或没跑完）的版本要 force 并写明原因，否则抛 package_eval_not_passed。 */
export function activateAdminDramaPackageVersion(packageId: string, versionId: string, force?: { reason: string }) {
    return http.post<{ package: DramaPackage }>(`/admin/drama/packages/${encodeURIComponent(packageId)}/versions/${encodeURIComponent(versionId)}/activate`, force ? { force: true, reason: force.reason } : {});
}

/** 上传或启用被静态校验拒绝时的逐条原因；其他错误返回 undefined。 */
export function packageProblems(error: unknown): DramaPackageProblem[] | undefined {
    if (!(error instanceof ApiError) || error.reason !== API_ERROR_REASON.PackageCheckFailed) return undefined;
    const problems = error.details?.problems;
    return Array.isArray(problems) ? (problems as DramaPackageProblem[]) : undefined;
}
