import { http } from "@/services/api/request";
import type { DramaStepSkillOption } from "@/services/api/drama";

/** 后台「短剧步骤技能」配置：每个文字步骤可选的技能清单。 */
export type DramaStepSkillConfig = { options: DramaStepSkillOption[]; updatedAt?: string };

export function getAdminDramaStepSkills() {
    return http.get<{ config: DramaStepSkillConfig }>("/admin/settings/drama-step-skills");
}

export function putAdminDramaStepSkills(options: DramaStepSkillOption[]) {
    return http.put<{ config: DramaStepSkillConfig }>("/admin/settings/drama-step-skills", { options });
}
