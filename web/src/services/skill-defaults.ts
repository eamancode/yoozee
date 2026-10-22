import { addSkill, getSkill, listAddedSkills, type Skill } from "@/services/api/skills";

/** 新画布 Agent 的单一默认技能；已有画布的会话选择不由此常量覆盖。 */
export const DEFAULT_CANVAS_AGENT_SKILL_ID = "yingce-short-drama-workflow";

/** 短剧项目画布由 yoozee 制片总控统筹，替代旧六步短剧技能。 */
export const DEFAULT_DRAMA_AGENT_SKILL_ID = "yz-producer";

export type AddedSkillsResult = { skills: Skill[] };

/** 绑定了短剧项目的画布默认用制片总控，其余画布沿用通用默认技能。 */
export function defaultCanvasAgentSkillId(domainProjectId?: string | null): string {
    return domainProjectId ? DEFAULT_DRAMA_AGENT_SKILL_ID : DEFAULT_CANVAS_AGENT_SKILL_ID;
}

/**
 * 确保默认技能既存在于用户技能库，也能作为本轮 Agent 的已选技能使用。
 * 先读已安装列表，只有首次需要时才写入用户技能状态。
 */
export async function ensureDefaultCanvasAgentSkill(dependencies: {
    listAdded?: typeof listAddedSkills;
    get?: typeof getSkill;
    add?: typeof addSkill;
} = {}, skillId: string = DEFAULT_CANVAS_AGENT_SKILL_ID): Promise<AddedSkillsResult> {
    const list = dependencies.listAdded || listAddedSkills;
    const get = dependencies.get || getSkill;
    const add = dependencies.add || addSkill;
    const added = await list();
    if (added.skills.some((skill) => skill.skillId === skillId && skill.isAdded)) return added;

    const detail = await get(skillId);
    if (!detail.skill.isAdded) await add(skillId);
    return list();
}
