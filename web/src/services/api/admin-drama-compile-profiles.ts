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
