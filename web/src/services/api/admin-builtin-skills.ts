import { http } from "@/services/api/request";
import type { SkillPackageFile, SkillPackageFileContent } from "@/services/api/skills";

/** 内置技能的一个版本：origin 为 builtin 时来自内置包，online 时来自后台在线编辑。 */
export type BuiltinSkillVersion = {
    id: string;
    versionLabel: string;
    origin: "builtin" | "online" | "";
    contentHash: string;
    fileCount: number;
    totalBytes: number;
    note: string;
    createdBy: string;
    createdAt: string;
    active: boolean;
    builtin: boolean;
};

export type BuiltinSkillSummary = {
    skillId: string;
    name: string;
    description: string;
    tag: string;
    /** 生效的是在线版本。 */
    overridden: boolean;
    /** 在线版本生效期间内置包又更新过。 */
    builtinUpdated: boolean;
    onlineVersionCount: number;
    currentVersion?: BuiltinSkillVersion;
    builtinVersion?: BuiltinSkillVersion;
};

export type BuiltinSkillVersionInput = {
    baseVersionId?: string;
    files: Array<{ path: string; content: string }>;
    deletePaths?: string[];
    note: string;
    activate: boolean;
};

export function listAdminBuiltinSkills() {
    return http.get<{ skills: BuiltinSkillSummary[] }>("/admin/builtin-skills");
}

export function listAdminBuiltinSkillVersions(skillId: string) {
    return http.get<{ skill: BuiltinSkillSummary; versions: BuiltinSkillVersion[] }>(`/admin/builtin-skills/${encodeURIComponent(skillId)}/versions`);
}

export function listAdminBuiltinSkillVersionFiles(skillId: string, versionId: string) {
    return http.get<{ files: SkillPackageFile[] }>(`/admin/builtin-skills/${encodeURIComponent(skillId)}/versions/${encodeURIComponent(versionId)}/files`);
}

export function getAdminBuiltinSkillVersionFile(skillId: string, versionId: string, path: string) {
    return http.get<{ file: SkillPackageFileContent }>(`/admin/builtin-skills/${encodeURIComponent(skillId)}/versions/${encodeURIComponent(versionId)}/file`, { params: { path } });
}

export function createAdminBuiltinSkillVersion(skillId: string, input: BuiltinSkillVersionInput) {
    return http.post<{ skill: BuiltinSkillSummary; version: BuiltinSkillVersion | null }>(`/admin/builtin-skills/${encodeURIComponent(skillId)}/versions`, input);
}

export function activateAdminBuiltinSkillVersion(skillId: string, versionId: string) {
    return http.post<{ skill: BuiltinSkillSummary }>(`/admin/builtin-skills/${encodeURIComponent(skillId)}/activate`, { versionId });
}

export function revertAdminBuiltinSkill(skillId: string) {
    return http.post<{ skill: BuiltinSkillSummary }>(`/admin/builtin-skills/${encodeURIComponent(skillId)}/revert`);
}
