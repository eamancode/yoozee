import { describe, expect, test } from "bun:test";

import { stepSkillFromKey, stepSkillKey, stepSkillSelectState } from "../src/lib/drama/step-skills";
import type { DramaStepSkillOption } from "../src/services/api/drama";

const catalog: DramaStepSkillOption[] = [
    { step: "plan", skillId: "s-plan", path: "SKILL.md", label: "情绪分集", description: "按情绪起伏分集", enabled: true },
    { step: "script", skillId: "s-script", path: "cards/拆镜.md", label: "拆镜法", enabled: true },
    { step: "script", skillId: "s-old", path: "SKILL.md", label: "停用的", enabled: false },
];

describe("短剧文字步骤的技能下拉", () => {
    test("每步只列「系统默认」和这一步启用的后台技能", () => {
        const plan = stepSkillSelectState(catalog, "plan");
        expect(plan.status).toBe("default");
        expect(plan.options.map((item) => item.label)).toEqual(["系统默认", "情绪分集"]);
        const script = stepSkillSelectState(catalog, "script");
        expect(script.options.map((item) => item.label)).toEqual(["系统默认", "拆镜法"]);
        expect(stepSkillSelectState(catalog, "bible").options.map((item) => item.label)).toEqual(["系统默认"]);
    });

    test("选中后台技能写成 system 引用，选系统默认清空", () => {
        const key = stepSkillKey({ skillId: "s-script", path: "cards/拆镜.md", source: "system" });
        expect(stepSkillFromKey(catalog, "script", key)).toEqual({ skillId: "s-script", path: "cards/拆镜.md", skillName: "拆镜法", source: "system" });
        expect(stepSkillFromKey(catalog, "script", "")).toBeUndefined();
        // 别的步骤的技能、停用的技能都不能选上。
        expect(stepSkillFromKey(catalog, "plan", key)).toBeUndefined();
        expect(stepSkillFromKey(catalog, "script", stepSkillKey({ skillId: "s-old", path: "SKILL.md", source: "system" }))).toBeUndefined();
    });

    test("当前值在清单里显示为已选；已下架、早先自选的显示出来但不能再选", () => {
        const listed = stepSkillSelectState(catalog, "plan", { skillId: "s-plan", path: "SKILL.md", skillName: "情绪分集", source: "system" });
        expect(listed.status).toBe("listed");
        expect(listed.options).toHaveLength(2);
        const delisted = stepSkillSelectState(catalog, "script", { skillId: "s-old", path: "SKILL.md", skillName: "停用的", source: "system" });
        expect(delisted.status).toBe("delisted");
        expect(delisted.options.at(-1)).toEqual({ value: stepSkillKey({ skillId: "s-old", path: "SKILL.md", source: "system" }), label: "停用的（已下架）", disabled: true });
        const library = stepSkillSelectState(catalog, "prompt", { skillId: "mine", path: "SKILL.md", skillName: "我的写法" });
        expect(library.status).toBe("library");
        expect(library.options.at(-1)?.label).toBe("我的写法（自选技能）");
        expect(library.options.at(-1)?.disabled).toBe(true);
    });
});
