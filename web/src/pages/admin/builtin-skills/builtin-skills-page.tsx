import { App, Button, Input, Popconfirm, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { History, Pencil, Power, RotateCcw, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router";

import { PromptCodeEditor } from "@/components/prompt/prompt-code-editor";
import { Select } from "@/components/ui/base/select";
import { Callout } from "@/pages/admin/ui/controls";
import { AdminDrawer } from "@/pages/admin/ui/overlays";
import {
    activateAdminBuiltinSkillVersion,
    createAdminBuiltinSkillVersion,
    getAdminBuiltinSkillVersionFile,
    listAdminBuiltinSkills,
    listAdminBuiltinSkillVersionFiles,
    listAdminBuiltinSkillVersions,
    revertAdminBuiltinSkill,
    type BuiltinSkillSummary,
    type BuiltinSkillVersion,
} from "@/services/api/admin-builtin-skills";
import type { SkillPackageFile } from "@/services/api/skills";
import { AdminSearchInput } from "../components/admin-search-input";
import { AdminPageFrame } from "../components/admin-shell";
import { AdminDataTable, AdminRowActions, AdminStatusBadge, AdminTableEmpty } from "../components/admin-ui";

type EditorState = {
    skill: BuiltinSkillSummary;
    versions: BuiltinSkillVersion[];
    base: BuiltinSkillVersion;
    files: SkillPackageFile[];
    path: string;
    originals: Record<string, string>;
    drafts: Record<string, string>;
};

const TEXT_KINDS = new Set(["markdown", "code", "text"]);

export default function BuiltinSkillsPage() {
    const { message } = App.useApp();
    const [searchParams, setSearchParams] = useSearchParams();
    const keyword = searchParams.get("filter") || "";
    const onlyOverridden = searchParams.get("status") === "online";
    const [skills, setSkills] = useState<BuiltinSkillSummary[]>([]);
    const [loading, setLoading] = useState(true);
    const [editor, setEditor] = useState<EditorState | null>(null);
    const [fileLoading, setFileLoading] = useState(false);
    const [note, setNote] = useState("");
    const [saving, setSaving] = useState(false);

    const reload = async () => {
        setLoading(true);
        try {
            setSkills((await listAdminBuiltinSkills()).skills);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "读取内置技能失败");
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        void reload();
    }, []);

    const updateUrl = (patch: { filter?: string; status?: string }) => {
        const next = new URLSearchParams(searchParams);
        Object.entries(patch).forEach(([key, value]) => (value && value !== "all" ? next.set(key, value) : next.delete(key)));
        setSearchParams(next);
    };

    const filtered = useMemo(
        () =>
            skills.filter((skill) => {
                const needle = keyword.trim().toLowerCase();
                if (needle && !`${skill.name} ${skill.skillId}`.toLowerCase().includes(needle)) return false;
                return !onlyOverridden || skill.overridden;
            }),
        [keyword, onlyOverridden, skills],
    );

    const changedPaths = editor ? Object.keys(editor.drafts).filter((path) => editor.drafts[path] !== editor.originals[path]) : [];
    const dirty = changedPaths.length > 0;

    const loadFile = async (state: EditorState, path: string): Promise<EditorState> => {
        if (path in state.originals) return { ...state, path };
        setFileLoading(true);
        try {
            const { file } = await getAdminBuiltinSkillVersionFile(state.skill.skillId, state.base.id, path);
            if (file.binary) {
                message.warning("这个文件不是文本，不能在线编辑");
                return state;
            }
            return { ...state, path, originals: { ...state.originals, [path]: file.content }, drafts: { ...state.drafts, [path]: file.content } };
        } finally {
            setFileLoading(false);
        }
    };

    const openEditor = async (skillId: string, baseVersionId?: string) => {
        try {
            const { skill, versions } = await listAdminBuiltinSkillVersions(skillId);
            const base = versions.find((item) => item.id === (baseVersionId || skill.currentVersion?.id)) || versions[0];
            if (!base) {
                message.warning("这个技能还没有版本记录");
                return;
            }
            const { files } = await listAdminBuiltinSkillVersionFiles(skillId, base.id);
            setNote("");
            setEditor(await loadFile({ skill, versions, base, files, path: "", originals: {}, drafts: {} }, "SKILL.md"));
        } catch (error) {
            message.error(error instanceof Error ? error.message : "读取技能版本失败");
        }
    };

    const switchFile = async (path: string) => {
        if (!editor) return;
        try {
            setEditor(await loadFile(editor, path));
        } catch (error) {
            message.error(error instanceof Error ? error.message : "读取文件失败");
        }
    };

    const save = async (activate: boolean) => {
        if (!editor || !dirty) return;
        if (!note.trim()) {
            message.warning("请写明这一版改了什么");
            return;
        }
        setSaving(true);
        try {
            const result = await createAdminBuiltinSkillVersion(editor.skill.skillId, {
                baseVersionId: editor.base.id,
                files: changedPaths.map((path) => ({ path, content: editor.drafts[path] })),
                note: note.trim(),
                activate,
            });
            message.success(activate ? `在线版本 ${result.version?.versionLabel || ""} 已生效` : `已保存在线版本 ${result.version?.versionLabel || ""}，未启用`);
            setEditor(null);
            await reload();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "保存技能版本失败");
        } finally {
            setSaving(false);
        }
    };

    const activate = async (skillId: string, version: BuiltinSkillVersion) => {
        try {
            await activateAdminBuiltinSkillVersion(skillId, version.id);
            message.success(`已启用 ${version.versionLabel}`);
            setEditor(null);
            await reload();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "启用版本失败");
        }
    };

    const revert = async (skillId: string) => {
        try {
            await revertAdminBuiltinSkill(skillId);
            message.success("已回退到内置版本");
            setEditor(null);
            await reload();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "回退失败");
        }
    };

    const columns: ColumnsType<BuiltinSkillSummary> = [
        {
            title: "技能",
            dataIndex: "name",
            render: (_, skill) => (
                <div>
                    <div className="font-medium">{skill.name}</div>
                    <div className="mt-1 text-xs text-foreground/45">{skill.skillId}</div>
                </div>
            ),
        },
        {
            title: "生效版本",
            width: 220,
            render: (_, skill) => (
                <div className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-xs">{skill.currentVersion?.versionLabel || "--"}</span>
                    <AdminStatusBadge label={skill.overridden ? "在线版本" : "内置版本"} tone={skill.overridden ? "info" : "neutral"} />
                </div>
            ),
        },
        {
            title: "内置包",
            width: 200,
            render: (_, skill) => (
                <div className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-xs">{skill.builtinVersion?.versionLabel || "--"}</span>
                    {skill.builtinUpdated ? <AdminStatusBadge label="内置包有更新" tone="warning" title="在线版本生效期间内置包更新过，确认后合并到新在线版本或回退" /> : null}
                </div>
            ),
        },
        { title: "在线版本数", dataIndex: "onlineVersionCount", width: 110, align: "center" },
        {
            title: "操作",
            width: 260,
            align: "center",
            render: (_, skill) => (
                <AdminRowActions
                    visibleActionCount={1}
                    primary={{ label: "编辑与版本", icon: <Pencil className="size-3.5" />, onClick: () => openEditor(skill.skillId) }}
                    actions={[
                        {
                            key: "revert",
                            label: "回退到内置",
                            icon: <RotateCcw className="size-3.5" />,
                            disabled: !skill.overridden,
                            confirm: { title: "回退到内置版本？", description: "生效版本改回内置包同步的版本，在线版本保留在历史里，随时可以再启用。", okText: "确认回退" },
                            onClick: () => revert(skill.skillId),
                        },
                    ]}
                />
            ),
        },
    ];

    const versionColumns: ColumnsType<BuiltinSkillVersion> = [
        {
            title: "版本",
            dataIndex: "versionLabel",
            render: (label: string, version) => (
                <div className="flex flex-wrap items-center gap-1.5">
                    <span className="font-mono text-xs">{label}</span>
                    {version.active ? <AdminStatusBadge label="生效中" tone="success" /> : null}
                    {version.builtin ? <AdminStatusBadge label="内置" /> : null}
                </div>
            ),
        },
        { title: "来源", dataIndex: "origin", width: 70, render: (origin: string) => (origin === "online" ? "在线" : "内置包") },
        {
            title: "说明",
            dataIndex: "note",
            render: (value: string, version) => (
                <div className="text-xs">
                    <div>{value || "--"}</div>
                    <div className="mt-0.5 text-foreground/45">{formatTime(version.createdAt)}</div>
                </div>
            ),
        },
        {
            title: "",
            width: 150,
            render: (_, version) =>
                editor ? (
                    <div className="flex justify-end gap-1">
                        <Button size="small" type="text" icon={<History className="size-3.5" />} disabled={version.id === editor.base.id} onClick={() => void openEditor(editor.skill.skillId, version.id)}>
                            以此为底
                        </Button>
                        <Popconfirm title={`启用 ${version.versionLabel}？`} description="之后新发起的生成任务都用这个版本。" okText="确认启用" cancelText="取消" disabled={version.active} onConfirm={() => activate(editor.skill.skillId, version)}>
                            <Button size="small" type="text" icon={<Power className="size-3.5" />} disabled={version.active}>
                                启用
                            </Button>
                        </Popconfirm>
                    </div>
                ) : null,
        },
    ];

    const textFiles = editor?.files.filter((file) => TEXT_KINDS.has(file.kind)) || [];

    return (
        <AdminPageFrame title="内置技能" description="在线编辑内置技能生成新版本，优先于内置包；可启用历史版本或回退到内置版本">
            <AdminDataTable
                toolbar={
                    <AdminSearchInput
                        allowClear
                        className="app-list-search"
                        prefix={<Search className="size-4 text-foreground/40" />}
                        value={keyword}
                        aria-label="搜索内置技能"
                        placeholder="搜索技能名或 ID"
                        onValueChange={(value) => updateUrl({ filter: value })}
                    />
                }
                toolbarActive={Boolean(keyword || onlyOverridden)}
                toolbarFilters={
                    <Select
                        aria-label="筛选生效来源"
                        className="w-36"
                        value={onlyOverridden ? "online" : "all"}
                        onChange={(value) => updateUrl({ status: value })}
                        options={[
                            { label: "全部技能", value: "all" },
                            { label: "在线版本生效", value: "online" },
                        ]}
                    />
                }
                onReset={() => updateUrl({ filter: "", status: "all" })}
                table={{ className: "app-data-table", size: "small", rowKey: "skillId", loading, pagination: false, columns, dataSource: filtered, scroll: { x: 960 } }}
                empty={<AdminTableEmpty filtered={Boolean(keyword || onlyOverridden)} />}
            />

            <AdminDrawer
                flush
                title={editor ? `${editor.skill.name} · 以 ${editor.base.versionLabel} 为底编辑` : "编辑内置技能"}
                open={Boolean(editor)}
                size="min(1280px, 100vw)"
                onClose={() => {
                    if (!saving) setEditor(null);
                }}
                rootClassName="admin-drawer"
                closable={false}
                mask={{ closable: false }}
                keyboard={false}
                extra={
                    <div className="flex gap-2">
                        <Popconfirm disabled={!dirty} title="放弃修改？" description="尚未保存的改动将丢失。" okText="放弃修改" cancelText="继续编辑" okButtonProps={{ danger: true }} onConfirm={() => setEditor(null)}>
                            <Button
                                disabled={saving}
                                onClick={() => {
                                    if (!dirty) setEditor(null);
                                }}
                            >
                                关闭
                            </Button>
                        </Popconfirm>
                        <Button loading={saving} disabled={!dirty || !note.trim()} onClick={() => void save(false)}>
                            只保存（不启用）
                        </Button>
                        <Button type="primary" loading={saving} disabled={!dirty || !note.trim()} onClick={() => void save(true)}>
                            保存并启用
                        </Button>
                    </div>
                }
            >
                {editor ? (
                    <div className="grid min-h-full gap-0 lg:grid-cols-5">
                        <aside className="min-h-0 border-b border-border p-4 lg:col-span-2 lg:border-b-0 lg:border-r">
                            {editor.skill.builtinUpdated ? (
                                <Callout tone="warning" title="内置包有更新">
                                    在线版本生效期间内置包更新到 {editor.skill.builtinVersion?.versionLabel}。可以以新内置版本为底重新编辑，或直接回退。
                                </Callout>
                            ) : null}
                            <h3 className="mb-2 mt-1 text-sm font-semibold">版本历史</h3>
                            <Table className="app-data-table" size="small" rowKey="id" pagination={false} columns={versionColumns} dataSource={editor.versions} />
                        </aside>
                        <section className="flex min-h-0 flex-col p-4 lg:col-span-3">
                            <div className="mb-3 flex flex-wrap items-center gap-2">
                                <Select
                                    aria-label="选择要编辑的文件"
                                    className="min-w-64"
                                    value={editor.path}
                                    onChange={(value) => void switchFile(value)}
                                    options={textFiles.map((file) => ({ label: `${file.path}${changedPaths.includes(file.path) ? " · 已改" : ""}`, value: file.path }))}
                                />
                                {dirty ? <AdminStatusBadge label={`已改 ${changedPaths.length} 个文件`} tone="warning" /> : null}
                            </div>
                            <div className="min-h-96 flex-1 overflow-hidden rounded-md border border-border">
                                <PromptCodeEditor
                                    value={editor.drafts[editor.path] ?? ""}
                                    readOnly={fileLoading}
                                    ariaLabel="技能文件内容"
                                    onChange={(value) => setEditor((state) => (state ? { ...state, drafts: { ...state.drafts, [state.path]: value } } : state))}
                                />
                            </div>
                            <Input.TextArea className="mt-3" rows={2} maxLength={500} showCount value={note} onChange={(event) => setNote(event.target.value)} placeholder="这一版改了什么（必填），例如：动作句要求写清起止状态" aria-label="版本说明" />
                            <p className="mt-2 text-xs text-foreground/50">SKILL.md 的 metadata.skillId 不能改。短剧流程技能包（如 yz-chapter-shots）不在这里，去「流程技能」上传新版本。</p>
                        </section>
                    </div>
                ) : null}
            </AdminDrawer>
        </AdminPageFrame>
    );
}

function formatTime(value?: string) {
    return value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "--";
}
