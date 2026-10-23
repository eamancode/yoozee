import { ApiError, http } from "@/services/api/request";
import { API_ERROR_REASON } from "@/services/api/error-codes";

/** 静态校验不过的一条原因；rule 与 backend/internal/drama/step_package.go、yoozee-skills/build.py 同一套编号。 */
export type DramaPackageProblem = { rule: string; message: string; path?: string };

/** 一个版本按当前线上步骤表重跑静态校验的结论；not_package 是内置或在线编辑留下的历史版本，不能在流程里启用。 */
export type DramaPackageCheck = { status: "passed" | "failed" | "not_package"; problems: DramaPackageProblem[] };

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

/** 上传 build.py --package 构建的 zip：通过静态校验存成新版本（不启用）；不通过抛 ApiError，原因用 packageProblems 取。 */
export function uploadAdminDramaPackage(file: File, note: string) {
    const body = new FormData();
    body.append("file", file, file.name);
    if (note.trim()) body.append("note", note.trim());
    return http.post<{ package: DramaPackage; version: DramaPackageVersion }>("/admin/drama/packages", body);
}

/** 手动启用一个版本；回退就是启用历史版本。 */
export function activateAdminDramaPackageVersion(packageId: string, versionId: string) {
    return http.post<{ package: DramaPackage }>(`/admin/drama/packages/${encodeURIComponent(packageId)}/versions/${encodeURIComponent(versionId)}/activate`);
}

/** 上传或启用被静态校验拒绝时的逐条原因；其他错误返回 undefined。 */
export function packageProblems(error: unknown): DramaPackageProblem[] | undefined {
    if (!(error instanceof ApiError) || error.reason !== API_ERROR_REASON.PackageCheckFailed) return undefined;
    const problems = error.details?.problems;
    return Array.isArray(problems) ? (problems as DramaPackageProblem[]) : undefined;
}
