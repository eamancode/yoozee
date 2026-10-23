import { App, Button, Form, Input, Popconfirm, Tabs } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Copy, Plus, Power, RotateCcw, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";

import { PromptCodeEditor } from "@/components/prompt/prompt-code-editor";
import { Select } from "@/components/ui/base/select";
import { Callout } from "@/pages/admin/ui/controls";
import { AdminDrawer } from "@/pages/admin/ui/overlays";
import {
    activateAdminDramaCompileProfile,
    createAdminDramaCompileProfile,
    deleteAdminDramaCompileProfile,
    listAdminDramaCompileProfiles,
    revertAdminDramaCompileProfile,
    type DramaCompileProfileList,
    type DramaCompileProfileVersion,
} from "@/services/api/admin-drama-compile-profiles";
import { AdminPageFrame } from "../components/admin-shell";
import { AdminDataTable, AdminRowActions, AdminStatusBadge, AdminTableEmpty } from "../components/admin-ui";
import { DramaEvalPanel } from "./drama-eval-panel";

type FormValues = { name: string; note: string; enabled: boolean };

const EMPTY: DramaCompileProfileList = { profiles: [], activeVersion: 0, default: {} };

export default function DramaCompileProfilesPage() {
    const { message } = App.useApp();
    const [data, setData] = useState<DramaCompileProfileList>(EMPTY);
    const [loading, setLoading] = useState(true);
    const [drawerOpen, setDrawerOpen] = useState(false);
    const [base, setBase] = useState<DramaCompileProfileVersion | null>(null);
    const [content, setContent] = useState("{}");
    const [saving, setSaving] = useState(false);
    const [form] = Form.useForm<FormValues>();

    const reload = async () => {
        setLoading(true);
        try {
            setData(await listAdminDramaCompileProfiles());
        } catch (error) {
            message.error(error instanceof Error ? error.message : "读取编译档案失败");
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        void reload();
    }, []);

    const openDrawer = (from?: DramaCompileProfileVersion) => {
        setBase(from || null);
        setContent(JSON.stringify(from?.content || {}, null, 2));
        form.setFieldsValue({ name: from ? `${from.name} · 新版本` : "", note: "", enabled: false });
        setDrawerOpen(true);
    };

    const parsedContent = (() => {
        try {
            const value = JSON.parse(content) as unknown;
            return value && typeof value === "object" && !Array.isArray(value) ? { value } : { error: "档案内容必须是 JSON 对象" };
        } catch (error) {
            return { error: error instanceof Error ? error.message : "不是合法 JSON" };
        }
    })();

    const save = async () => {
        const values = await form.validateFields();
        if (!("value" in parsedContent)) {
            message.warning(parsedContent.error);
            return;
        }
        setSaving(true);
        try {
            const { profile } = await createAdminDramaCompileProfile({ name: values.name.trim(), note: values.note.trim(), content: parsedContent.value, enabled: values.enabled === true });
            message.success(profile.enabled ? `v${profile.version} 已生效` : `已保存 v${profile.version}，未启用`);
            setDrawerOpen(false);
            await reload();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "保存编译档案失败");
        } finally {
            setSaving(false);
        }
    };

    const run = async (action: () => Promise<unknown>, success: string) => {
        try {
            await action();
            message.success(success);
            await reload();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "操作失败");
        }
    };

    const columns: ColumnsType<DramaCompileProfileVersion> = [
        { title: "版本", dataIndex: "version", width: 90, render: (version: number) => <span className="font-mono">v{version}</span> },
        {
            title: "名称与说明",
            dataIndex: "name",
            render: (_, profile) => (
                <div>
                    <div className="font-medium">{profile.name}</div>
                    <div className="mt-1 text-xs text-foreground/55">{profile.note}</div>
                </div>
            ),
        },
        { title: "改动项", width: 220, render: (_, profile) => <span className="text-xs text-foreground/65">{changedKeys(profile.content).join("、") || "无"}</span> },
        { title: "状态", dataIndex: "enabled", width: 100, align: "center", render: (enabled: boolean) => <AdminStatusBadge label={enabled ? "生效中" : "历史版"} tone={enabled ? "success" : "neutral"} /> },
        { title: "创建时间", dataIndex: "createdAt", width: 170, align: "center", render: formatTime },
        {
            title: "操作",
            width: 300,
            align: "center",
            render: (_, profile) => (
                <AdminRowActions
                    visibleActionCount={2}
                    primary={{ label: "基于此版本新建", icon: <Copy className="size-3.5" />, onClick: () => openDrawer(profile) }}
                    actions={[
                        {
                            key: "activate",
                            label: "启用",
                            icon: <Power className="size-3.5" />,
                            disabled: profile.enabled,
                            confirm: { title: `启用 v${profile.version}？`, description: "之后的编译和装段都用这一版；已装好的段要重新装段，镜头画面句才会换成新写法。", okText: "确认启用" },
                            onClick: () => run(() => activateAdminDramaCompileProfile(profile.id), `v${profile.version} 已生效`),
                        },
                        {
                            key: "delete",
                            label: "删除",
                            icon: <Trash2 className="size-3.5" />,
                            danger: true,
                            disabled: profile.enabled,
                            confirm: { title: `删除 v${profile.version}？`, description: "删除后不可恢复；镜头表上记的版本号会找不到名称。", okText: "确认删除" },
                            onClick: () => run(() => deleteAdminDramaCompileProfile(profile.id), "已删除"),
                        },
                    ]}
                />
            ),
        },
    ];

    return (
        <AdminPageFrame
            title="编译档案"
            description="装段与视频提示词的文字：镜头画面句、表情链、灯光七项、台词四元、人物声明、参考声明、约束、⚠️ 与风格块"
            actions={
                <div className="flex gap-2">
                    <Popconfirm
                        title="回退到内置默认？"
                        description="停用全部版本，编译与装段改用程序内置的文字。"
                        okText="确认回退"
                        cancelText="取消"
                        disabled={data.activeVersion === 0}
                        onConfirm={() => run(revertAdminDramaCompileProfile, "已回退到内置默认")}
                    >
                        <Button icon={<RotateCcw className="size-4" />} disabled={data.activeVersion === 0}>
                            回退到内置默认
                        </Button>
                    </Popconfirm>
                    <Button type="primary" icon={<Plus className="size-4" />} onClick={() => openDrawer(data.profiles.find((item) => item.enabled))}>
                        新建版本
                    </Button>
                </div>
            }
        >
            <Callout tone="info" title={data.activeVersion ? `生效中：v${data.activeVersion}` : "生效中：内置默认（v0）"}>
                编译期的文字（参考声明、约束、⚠️、风格块）下次编译就生效；镜头画面句类的文字（表情、灯光、动作、人物状态）写在段里，要用已存镜头表重新装段。先在下面的「评测重跑」里跑候选版本，对比指标后再启用。
            </Callout>
            <AdminDataTable
                table={{ className: "app-data-table", size: "small", rowKey: "id", loading, pagination: false, columns, dataSource: data.profiles, scroll: { x: 1080 } }}
                empty={<AdminTableEmpty title="还没有在线版本" description="现在用的是内置默认（v0）。新建版本时只需写要改的项。" />}
            />
            <DramaEvalPanel profiles={data.profiles} activeVersion={data.activeVersion} />

            <AdminDrawer
                flush
                title={base ? `基于 v${base.version} 新建版本` : "新建编译档案版本"}
                open={drawerOpen}
                size="min(1280px, 100vw)"
                onClose={() => {
                    if (!saving) setDrawerOpen(false);
                }}
                rootClassName="admin-drawer"
                extra={
                    <div className="flex gap-2">
                        <Button disabled={saving} onClick={() => setDrawerOpen(false)}>
                            关闭
                        </Button>
                        <Button type="primary" loading={saving} disabled={!("value" in parsedContent)} onClick={() => void save()}>
                            保存版本
                        </Button>
                    </div>
                }
            >
                <Form form={form} layout="vertical" requiredMark={false} className="flex min-h-full flex-col">
                    <div className="grid shrink-0 gap-4 border-b border-border p-4 md:grid-cols-3">
                        <Form.Item name="name" label="版本名称" className="mb-0" rules={[{ required: true, whitespace: true, message: "请填写版本名称" }]}>
                            <Input maxLength={120} placeholder="例如：表情链写得更具体" />
                        </Form.Item>
                        <Form.Item name="note" label="改了什么" className="mb-0" rules={[{ required: true, whitespace: true, message: "请写明这一版改了什么" }]}>
                            <Input maxLength={500} placeholder="例如：灯光七项改成先写主光方向" />
                        </Form.Item>
                        <Form.Item name="enabled" label="保存后状态" className="mb-0">
                            <Select
                                options={[
                                    { label: "只保存（先评测）", value: false },
                                    { label: "保存并立即生效", value: true },
                                ]}
                            />
                        </Form.Item>
                    </div>
                    <div className="grid min-h-0 flex-1 lg:grid-cols-2">
                        <section className="flex min-h-0 flex-col border-b border-border p-4 lg:border-b-0 lg:border-r">
                            <div className="mb-2 flex items-center gap-2">
                                <h3 className="text-sm font-semibold">档案内容（只写要改的项）</h3>
                                {"error" in parsedContent ? <AdminStatusBadge label={parsedContent.error || "格式错误"} tone="error" /> : null}
                            </div>
                            <div className="min-h-96 flex-1 overflow-hidden rounded-md border border-border">
                                <PromptCodeEditor value={content} ariaLabel="编译档案内容" onChange={setContent} />
                            </div>
                            <p className="mt-2 text-xs text-foreground/50">没写的项取内置默认。模板用 {"{name}"} 占位符，每项能用哪些占位符见右侧默认全文；用错或删掉必需的占位符时保存会被拒绝。</p>
                        </section>
                        <aside className="min-h-0 p-4">
                            <Tabs
                                size="small"
                                items={[
                                    { key: "default", label: "内置默认全文", children: <pre className="thin-scrollbar max-h-[70vh] overflow-auto whitespace-pre-wrap text-xs leading-5 text-foreground/70">{JSON.stringify(data.default, null, 2)}</pre> },
                                    {
                                        key: "base",
                                        label: base ? `v${base.version} 内容` : "底版本",
                                        children: <pre className="thin-scrollbar max-h-[70vh] overflow-auto whitespace-pre-wrap text-xs leading-5 text-foreground/70">{base ? JSON.stringify(base.content, null, 2) : "从空档案开始（全部取内置默认）"}</pre>,
                                    },
                                ]}
                            />
                        </aside>
                    </div>
                </Form>
            </AdminDrawer>
        </AdminPageFrame>
    );
}

/** 档案里改了哪些项（顶层分组.键），列表里一眼看出这一版动了什么。 */
function changedKeys(content: Record<string, unknown>) {
    return Object.entries(content || {})
        .flatMap(([group, value]) => (value && typeof value === "object" && !Array.isArray(value) ? Object.keys(value).map((key) => `${group}.${key}`) : [group]))
        .slice(0, 8);
}

function formatTime(value?: string) {
    return value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "--";
}
