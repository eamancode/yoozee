import { App } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useEffect, useState } from "react";

import { Select } from "@/components/ui/base/select";
import { Callout } from "@/pages/admin/ui/controls";
import { getAdminDramaStepBindings, putAdminDramaStepBindings, type DramaStepBindingMatrix } from "@/services/api/admin-drama-step-bindings";
import type { DramaStyleProfile } from "@/services/api/drama";
import { AdminPageFrame } from "../components/admin-shell";
import { AdminDataTable, AdminTableEmpty } from "../components/admin-ui";
import { DramaPackagesPanel } from "./drama-packages-panel";

const EMPTY: DramaStepBindingMatrix = { profiles: [], steps: [], cells: [], skills: [] };
// 下拉里「用默认技能」的取值：删掉这一格。
const DEFAULT_OPTION = "";

/** 短剧流程的「风格档案 × 步骤 → 技能」绑定：每格一个技能，项目按风格档案自动用该技能的当前版本；下方管理流程技能包的版本。 */
export default function DramaStepBindingsPage() {
    const { message } = App.useApp();
    const [data, setData] = useState<DramaStepBindingMatrix>(EMPTY);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState("");

    useEffect(() => {
        const controller = new AbortController();
        getAdminDramaStepBindings(controller.signal)
            .then(setData)
            .catch((error: unknown) => {
                if (!controller.signal.aborted) message.error(error instanceof Error ? error.message : "读取流程技能绑定失败");
            })
            .finally(() => setLoading(false));
        return () => controller.abort();
    }, [message]);

    const cell = (profile: string, step: string) => data.cells.find((item) => item.styleProfile === profile && item.step === step);
    const save = async (profile: string, step: string, skillId: string) => {
        const key = `${profile}:${step}`;
        setSaving(key);
        try {
            setData(await putAdminDramaStepBindings([{ styleProfile: profile, step, skillId }]));
            message.success("绑定已保存，项目下次运行这一步就用新技能");
        } catch (error) {
            message.error(error instanceof Error ? error.message : "保存绑定失败");
        } finally {
            setSaving("");
        }
    };

    const columns: ColumnsType<DramaStyleProfile> = [
        {
            title: "风格档案",
            dataIndex: "label",
            width: 220,
            render: (label: string, profile) => (
                <div>
                    <div className="font-medium">{label}</div>
                    <div className="font-mono text-xs text-foreground/45">{profile.id}</div>
                </div>
            ),
        },
        ...data.steps.map((step) => ({
            title: (
                <div>
                    <div>{`${step.label}（${step.id}，契约 ${step.contract}）`}</div>
                    {step.note ? <div className="mt-0.5 text-xs font-normal text-foreground/50">{step.note}</div> : null}
                </div>
            ),
            key: step.id,
            render: (_: unknown, profile: DramaStyleProfile) => {
                const current = cell(profile.id, step.id);
                const defaultSkill = step.profileSkills?.[profile.id] || step.defaultSkill;
                const fallback = data.skills.find((skill) => skill.id === defaultSkill);
                return (
                    <Select
                        className="w-full min-w-72"
                        aria-label={`${profile.label} · ${step.label} 绑定的技能`}
                        loading={saving === `${profile.id}:${step.id}`}
                        disabled={Boolean(saving)}
                        showSearch
                        optionFilterProp="label"
                        value={current?.skillId || DEFAULT_OPTION}
                        options={[
                            { value: DEFAULT_OPTION, label: defaultSkill ? `用步骤默认：${fallback?.name || defaultSkill}${fallback?.version ? ` ${fallback.version}` : ""}` : "还没有默认技能" },
                            ...data.skills.map((skill) => ({ value: skill.id, label: `${skill.name}${skill.version ? ` ${skill.version}` : ""}` })),
                        ]}
                        onChange={(skillId: string) => void save(profile.id, step.id, skillId)}
                    />
                );
            },
        })),
    ];

    return (
        <AdminPageFrame title="流程技能" description="短剧流程每一步用哪个技能：按项目的风格档案绑定，项目自动用该技能当前生效的版本">
            <Callout tone="info" title="绑定只决定用哪个技能">
                步骤、顺序和输出契约写在代码里，改了要发版；流程技能包的内容在下方上传、启用和回退，线上不在线编辑。没配的格子用该步骤的默认技能。生成分段按戏类绑：项目视频模型单段上限不超过 15 秒用「文戏 15」那一列，更长用「文戏
                30」那一列；分镜制作里改成武戏的小节用「武戏」那一列（真人剧、短剧视感档案默认用去掉 CG / 仙法 / 魔物的精简切片）。
            </Callout>
            <AdminDataTable
                table={{ className: "app-data-table", size: "small", rowKey: "id", loading, pagination: false, columns, dataSource: data.profiles, scroll: { x: 720 } }}
                empty={<AdminTableEmpty title="没有风格档案" description="后端没有返回内置风格档案。" />}
            />
            <DramaPackagesPanel />
        </AdminPageFrame>
    );
}
