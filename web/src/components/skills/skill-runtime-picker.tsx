import { useCallback, useEffect, useMemo, useState } from "react";


import { scopedLocalStorage } from "@/lib/user-scope";
import { listAddedSkills, type Skill } from "@/services/api/skills";
import { SKILL_RUNTIME_PROFILES, type SkillRuntimeProfile } from "@/services/skill-runtime";
import { Select } from "@/components/ui/base/select";

export function useSkillRuntimeCatalog() {
    const [skills, setSkills] = useState<Skill[]>([]);
    const [loading, setLoading] = useState(true);

    useEffect(() => {
        let cancelled = false;
        listAddedSkills()
            .then((result) => {
                if (!cancelled) setSkills(result.skills.filter((skill) => skill.isAdded));
            })
            .catch(() => {
                if (!cancelled) setSkills([]);
            })
            .finally(() => {
                if (!cancelled) setLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, []);

    return { skills, loading };
}

const REMEMBERED_SKILLS_KEY = "yingce:skill-runtime-selection";

/** 读出上次在同一入口选的技能；只是 UI 偏好，按用户 scope 存在 localStorage。 */
export function readRememberedSkillIds(slot: string) {
    try {
        const parsed: unknown = JSON.parse(scopedLocalStorage.getItem(`${REMEMBERED_SKILLS_KEY}:${slot}`) || "[]");
        return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === "string" && Boolean(id)) : [];
    } catch {
        return [];
    }
}

/**
 * 记住每个入口（章节分镜、镜头生成……）上次选的技能。技能库加载完成后丢掉已经移除的技能；
 * 加载前不过滤，避免技能列表还没回来就把记忆清空。
 */
export function useRememberedSkillSelection(slot: string, skills: Skill[], loading: boolean) {
    const [selected, setSelected] = useState(() => readRememberedSkillIds(slot));
    const available = useMemo(() => new Set(skills.map((skill) => skill.skillId)), [skills]);
    const value = loading ? selected : selected.filter((id) => available.has(id));
    const change = useCallback((skillIds: string[]) => {
        setSelected(skillIds);
        scopedLocalStorage.setItem(`${REMEMBERED_SKILLS_KEY}:${slot}`, JSON.stringify(skillIds));
    }, [slot]);
    return [value, change] as const;
}

export function SkillRuntimePicker({ skills, loading, value, onChange, placeholder = "选择本次生成使用的技能", profile = "canvas" }: { skills: Skill[]; loading?: boolean; value: string[]; onChange: (skillIds: string[]) => void; placeholder?: string; profile?: SkillRuntimeProfile }) {
    const options = useMemo(
        () => skills.map((skill) => ({ value: skill.skillId, label: skill.skillName, title: skill.description })),
        [skills],
    );
    const maxSkills = SKILL_RUNTIME_PROFILES[profile].maxSkills;
    // 多选下拉默认选完不收起，浮层会盖住弹窗底部的主按钮，误点就取消了技能；每选一项就收起。
    const [open, setOpen] = useState(false);

    return (
        <Select
            className="w-full"
            style={{ width: "100%" }}
            mode="multiple"
            allowClear
            showSearch
            maxCount={maxSkills || undefined}
            maxTagCount="responsive"
            loading={loading}
            value={value}
            placeholder={placeholder}
            optionFilterProp="label"
            options={options}
            onChange={onChange}
            open={open}
            onOpenChange={setOpen}
            onSelect={() => setOpen(false)}
            notFoundContent={loading ? "正在读取技能库" : "暂无已加入的技能"}
            aria-label="本次生成使用的技能"
        />
    );
}
