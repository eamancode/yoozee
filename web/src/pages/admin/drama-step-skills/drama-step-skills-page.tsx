import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { App, Button, Input, Switch } from "antd";
import { Plus, Trash2 } from "lucide-react";

import { Select } from "@/components/ui/base/select";
import { Callout } from "@/pages/admin/ui/controls";
import { getAdminDramaStepSkills, putAdminDramaStepSkills } from "@/services/api/admin-drama-step-skills";
import type { DramaSkillStepKey, DramaStepSkillOption } from "@/services/api/drama";
import { listAddedSkills, listSkillFiles } from "@/services/api/skills";
import { AdminPageFrame } from "../components/admin-shell";

const STEPS: Array<{ step: DramaSkillStepKey; label: string; hint: string }> = [
    { step: "plan", label: "分集规划", hint: "小说自动分集时把章节组织为连续剧集" },
    { step: "bible", label: "资产提取", hint: "从原文提取角色、道具、场景与全剧画面规则" },
    { step: "script", label: "分镜拆分", hint: "每集改编为镜头表" },
    { step: "prompt", label: "视频提示词", hint: "按镜头表把每个剧情段写成视频提示词" },
];

/** 一项里技能对应的文本文件（默认 SKILL.md）。 */
function SkillFileSelect({ skillId, value, onChange }: { skillId: string; value: string; onChange: (path: string) => void }) {
    const filesQuery = useQuery({ queryKey: ["skills", skillId, "files"], queryFn: () => listSkillFiles(skillId), enabled: Boolean(skillId), staleTime: 60_000 });
    const files = (filesQuery.data?.files || []).filter((file) => file.kind === "markdown" || file.kind === "text");
    const options = files.map((file) => ({ value: file.path, label: file.path }));
    if (value && !files.some((file) => file.path === value)) options.unshift({ value, label: value });
    return <Select className="w-full" aria-label="技能文件" value={value || undefined} placeholder="技能文件" loading={filesQuery.isLoading} disabled={!skillId} options={options} onChange={onChange} />;
}

/**
 * 后台「短剧步骤技能」：给短剧流水线的四个文字步骤分别配置可选技能。用户在作品的制作设置里每步选「系统默认」或这里启用的技能，
 * 新建作品默认用他上次的选择。技能从当前管理员技能库里已添加的技能中选（自己上传或从市场添加），执行时读技能当前版本。
 * 停用或删除一项后，已选它的作品执行到这一步会报错提示改选，不会悄悄改用系统默认。
 */
export default function DramaStepSkillsPage() {
    const { message } = App.useApp();
    const [options, setOptions] = useState<DramaStepSkillOption[]>([]);
    const [dirty, setDirty] = useState(false);
    const configQuery = useQuery({ queryKey: ["admin", "drama-step-skills"], queryFn: getAdminDramaStepSkills });
    const skillsQuery = useQuery({ queryKey: ["skills", "added"], queryFn: listAddedSkills, staleTime: 30_000 });
    const [saving, setSaving] = useState(false);

    useEffect(() => {
        if (configQuery.data && !dirty) setOptions(configQuery.data.config.options);
    }, [configQuery.data, dirty]);

    const skills = skillsQuery.data?.skills || [];
    const update = (index: number, patch: Partial<DramaStepSkillOption>) => {
        setDirty(true);
        setOptions((current) => current.map((item, i) => (i === index ? { ...item, ...patch } : item)));
    };
    const add = (step: DramaSkillStepKey) => {
        setDirty(true);
        setOptions((current) => [...current, { step, skillId: "", path: "SKILL.md", label: "", description: "", enabled: true }]);
    };
    const remove = (index: number) => {
        setDirty(true);
        setOptions((current) => current.filter((_, i) => i !== index));
    };
    const save = async () => {
        setSaving(true);
        try {
            const { config } = await putAdminDramaStepSkills(options);
            setOptions(config.options);
            setDirty(false);
            await configQuery.refetch();
            message.success("已保存；用户之后打开制作设置就能看到");
        } catch (error) {
            message.error(error instanceof Error ? error.message : "保存失败");
        } finally {
            setSaving(false);
        }
    };

    return (
        <AdminPageFrame
            title="短剧步骤技能"
            description="短剧流水线制作设置里每个文字步骤可选的技能；用户每步选「系统默认」或这里启用的技能"
            actions={
                <span className="flex gap-2">
                    <Button disabled={!dirty || saving} onClick={() => { setDirty(false); if (configQuery.data) setOptions(configQuery.data.config.options); }}>
                        还原
                    </Button>
                    <Button type="primary" loading={saving} disabled={!dirty} onClick={() => void save()}>
                        保存
                    </Button>
                </span>
            }
        >
            <Callout tone="info" title="怎么生效">
                技能从你技能库里已添加的技能中选，选其中一个文本文件（默认 SKILL.md）作为这一步的写作方法；执行时读技能的当前版本，改了技能内容下一次执行就用新内容。输出结构始终由系统固定。停用或删掉某项后，已经选了它的作品执行到这一步会报错，提示用户改选。
            </Callout>
            {configQuery.isError ? <Callout tone="error" title="配置没读到">{configQuery.error instanceof Error ? configQuery.error.message : "请稍后重试"}</Callout> : null}
            <div className="mt-4 space-y-4">
                {STEPS.map((step) => {
                    const rows = options.map((option, index) => ({ option, index })).filter((row) => row.option.step === step.step);
                    return (
                        <section key={step.step} className="rounded-lg border border-border/70 p-4" aria-label={`${step.label}可选技能`}>
                            <div className="mb-3 flex items-center justify-between gap-2">
                                <div>
                                    <div className="font-medium">{step.label}</div>
                                    <div className="text-xs text-foreground/50">{step.hint}；用户默认可选「系统默认」</div>
                                </div>
                                <Button size="small" icon={<Plus className="size-3.5" />} onClick={() => add(step.step)}>
                                    添加技能
                                </Button>
                            </div>
                            {rows.length ? (
                                <div className="space-y-2">
                                    {rows.map(({ option, index }) => (
                                        <div key={index} className="grid grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.4fr)_auto_auto] items-center gap-2">
                                            <Select
                                                className="w-full"
                                                aria-label={`${step.label}的技能`}
                                                showSearch
                                                optionFilterProp="label"
                                                placeholder="选择技能"
                                                loading={skillsQuery.isLoading}
                                                value={option.skillId || undefined}
                                                options={[
                                                    ...(option.skillId && !skills.some((skill) => skill.skillId === option.skillId) ? [{ value: option.skillId, label: option.label || option.skillId }] : []),
                                                    ...skills.map((skill) => ({ value: skill.skillId, label: skill.skillName })),
                                                ]}
                                                onChange={(skillId: string) => update(index, { skillId, path: "SKILL.md", label: option.label || skills.find((skill) => skill.skillId === skillId)?.skillName || "" })}
                                            />
                                            <SkillFileSelect skillId={option.skillId} value={option.path} onChange={(path) => update(index, { path })} />
                                            <Input aria-label="给用户看的名字" placeholder="给用户看的名字（默认技能名）" maxLength={60} value={option.label} onChange={(event) => update(index, { label: event.target.value })} />
                                            <Input aria-label="说明" placeholder="说明（可选）" maxLength={200} value={option.description} onChange={(event) => update(index, { description: event.target.value })} />
                                            <Switch checked={option.enabled} checkedChildren="启用" unCheckedChildren="停用" aria-label="启用" onChange={(enabled) => update(index, { enabled })} />
                                            <Button type="text" size="small" icon={<Trash2 className="size-3.5" />} aria-label="删除这一项" onClick={() => remove(index)} />
                                        </div>
                                    ))}
                                </div>
                            ) : (
                                <p className="text-xs text-foreground/45">还没有配置技能，用户在这一步只能选「系统默认」。</p>
                            )}
                        </section>
                    );
                })}
            </div>
        </AdminPageFrame>
    );
}
