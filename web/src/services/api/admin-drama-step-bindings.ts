import { http } from "@/services/api/request";
import type { DramaStyleProfile } from "@/services/api/drama";

/** 绑定矩阵的一格：skillId 为空表示没配，运行时用步骤默认技能。 */
export type DramaStepBindingCell = {
    styleProfile: string;
    step: string;
    skillId: string;
    skillName?: string;
    version?: string;
    updatedBy?: string;
    updatedAt?: string;
};

/** 绑定矩阵的一列：genre 是生成分段的戏类列（chapter.shots#<戏类>），reserved 是只占了位置还不会被调用的戏类（武戏），note 是这一列的说明。 */
export type DramaStepDescriptor = { id: string; label: string; contract: string; defaultSkill: string; genre?: string; reserved?: boolean; note?: string };
export type DramaStepSkillOption = { id: string; name: string; version: string; source: string };

/** 后台「流程技能」：风格档案 × 步骤 → 技能。 */
export type DramaStepBindingMatrix = {
    profiles: DramaStyleProfile[];
    steps: DramaStepDescriptor[];
    cells: DramaStepBindingCell[];
    skills: DramaStepSkillOption[];
};

export function getAdminDramaStepBindings(signal?: AbortSignal) {
    return http.get<DramaStepBindingMatrix>("/admin/drama/step-bindings", { signal });
}

/** 写若干格；skillId 为空删掉这一格（回到步骤默认技能）。返回写完后的矩阵。 */
export function putAdminDramaStepBindings(bindings: Array<Pick<DramaStepBindingCell, "styleProfile" | "step" | "skillId">>) {
    return http.put<DramaStepBindingMatrix>("/admin/drama/step-bindings", { bindings });
}
