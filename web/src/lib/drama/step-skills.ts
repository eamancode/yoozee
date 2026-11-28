import type { DramaSkillRef, DramaSkillStepKey, DramaStepSkillOption } from "@/services/api/drama";

/** 下拉里一项技能的取值（系统默认是空串）。 */
export function stepSkillKey(ref?: { skillId: string; path: string; source?: string }) {
    return ref ? `${ref.source || "library"}|${ref.skillId}|${ref.path}` : "";
}

export type StepSkillSelectState = {
    options: Array<{ value: string; label: string; title?: string; disabled?: boolean }>;
    /** 当前值的状态：default 系统默认；listed 在后台清单里；delisted 后台技能已下架；library 早先从自己技能库选的。 */
    status: "default" | "listed" | "delisted" | "library";
};

/**
 * 一个文字步骤的技能下拉：「系统默认」+ 后台为这一步启用的技能。当前值不在清单里（已下架，或早先自选的）时仍放进下拉
 * 显示当前选择，但不能再选回。
 */
export function stepSkillSelectState(catalog: DramaStepSkillOption[], step: DramaSkillStepKey, value?: DramaSkillRef): StepSkillSelectState {
    const options = catalog.filter((option) => option.step === step && option.enabled !== false);
    const listed = Boolean(value && value.source === "system" && options.some((option) => option.skillId === value.skillId && option.path === value.path));
    const items: StepSkillSelectState["options"] = [{ value: "", label: "系统默认" }, ...options.map((option) => ({ value: stepSkillKey({ ...option, source: "system" }), label: option.label, title: option.description || option.label }))];
    let status: StepSkillSelectState["status"] = "default";
    if (value) {
        status = listed ? "listed" : value.source === "system" ? "delisted" : "library";
        if (!listed) items.push({ value: stepSkillKey(value), label: `${value.skillName || value.skillId}${status === "delisted" ? "（已下架）" : "（自选技能）"}`, disabled: true });
    }
    return { options: items, status };
}

/** 下拉选中的取值 → 作品设置里的技能（系统默认返回 undefined）。 */
export function stepSkillFromKey(catalog: DramaStepSkillOption[], step: DramaSkillStepKey, key: string): DramaSkillRef | undefined {
    const option = catalog.find((item) => item.step === step && item.enabled !== false && stepSkillKey({ ...item, source: "system" }) === key);
    return option ? { skillId: option.skillId, path: option.path, skillName: option.label, source: "system" } : undefined;
}
