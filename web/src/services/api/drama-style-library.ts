import { apiBaseURL, compactApiParams, http } from "@/services/api/request";
import type { DramaMasterPalette, DramaStyleAnchor, DramaStyleLockRecord } from "@/services/api/project-drama";
import type { ProjectAsset } from "@/services/api/projects";

/** 项目类型（与建项目的类型同值），平台风格库按它归档。 */
export type DramaProjectTypeOption = { value: string; label: string };

/** 平台风格库的一条：风格锚 + 主基调色卡，按项目类型归档，团队共享；canEdit 是当前用户能不能改、删（管理员或创建者）。 */
export type DramaStyleLibraryItem = {
    id: string;
    projectType: string;
    projectTypeLabel: string;
    name: string;
    description: string;
    styleAnchor?: DramaStyleAnchor;
    masterPalette: DramaMasterPalette;
    colorCount: number;
    /** 主色卡图（相对 API 根）；用 dramaStyleLibraryCoverUrl 拼成完整地址。 */
    coverUrl?: string;
    source: "analysis" | "manual" | "seed" | string;
    createdBy: string;
    createdByName: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
    canEdit: boolean;
};

export type DramaStyleLibraryList = { items: DramaStyleLibraryItem[]; projectTypes: DramaProjectTypeOption[] };

export function dramaStyleLibraryKey(projectType?: string) {
    return ["drama-style-library", projectType || "all"] as const;
}

export function listDramaStyleLibrary(projectType?: string, signal?: AbortSignal) {
    return http.get<DramaStyleLibraryList>("/drama/style-library", { params: compactApiParams({ projectType }), signal });
}

/** 主色卡图的完整地址（登录态 Cookie 读，所有登录用户都能看）。 */
export function dramaStyleLibraryCoverUrl(item: Pick<DramaStyleLibraryItem, "coverUrl" | "updatedAt">) {
    if (!item.coverUrl) return "";
    const base = String(apiBaseURL).replace(/\/+$/, "");
    return `${base}${item.coverUrl}?v=${encodeURIComponent(item.updatedAt)}`;
}

/** 把项目锁定的风格存进风格库（按项目锁定记录里的类型归档）。 */
export function saveDramaStyleToLibrary(input: { projectId: string; name: string; description?: string; projectType?: string }) {
    return http.post<DramaStyleLibraryItem>("/drama/style-library", input);
}

/** 改一条（管理员或创建者）：改了色板或名称会重新合成主色卡图。 */
export function updateDramaStyleLibraryEntry(id: string, input: { name?: string; description?: string; projectType?: string; styleAnchor?: DramaStyleAnchor; masterPalette?: DramaMasterPalette }) {
    return http.patch<DramaStyleLibraryItem>(`/drama/style-library/${encodeURIComponent(id)}`, input);
}

export function deleteDramaStyleLibraryEntry(id: string) {
    return http.delete<{ deleted: boolean }>(`/drama/style-library/${encodeURIComponent(id)}`);
}

/** 锁定风格的结果：写进项目的风格锚、主基调色卡、来源，主色卡资产（imageError 是图没绑上的原因，锁定已生效）。 */
export type DramaStyleLockResult = {
    styleAnchor: DramaStyleAnchor;
    masterPalette: DramaMasterPalette;
    styleLock: DramaStyleLockRecord;
    asset?: ProjectAsset;
    imageError?: string;
    fontNote?: string;
};

/** 「锁定此风格」：风格分析任务的候选（taskId）或平台风格库的条目（libraryId）二选一，写进项目并合成主色卡。 */
export function lockDramaStyle(projectId: string, input: { taskId: string } | { libraryId: string }) {
    return http.post<DramaStyleLockResult>(`/projects/${encodeURIComponent(projectId)}/drama/style-lock`, input);
}
