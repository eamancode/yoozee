import { App, Button, Form, Input } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Eye, Palette, PencilLine, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { MasterPaletteGroups, MasterPaletteStrip, StyleAnchorSummary, styleAnchorBrief } from "@/components/drama/master-palette";
import { PromptCodeEditor } from "@/components/prompt/prompt-code-editor";
import { Select } from "@/components/ui/base/select";
import { Callout } from "@/pages/admin/ui/controls";
import { AdminDrawer } from "@/pages/admin/ui/overlays";
import { masterPaletteColors, type DramaMasterPalette } from "@/services/api/project-drama";
import { deleteDramaStyleLibraryEntry, dramaStyleLibraryCoverUrl, listDramaStyleLibrary, updateDramaStyleLibraryEntry, type DramaStyleLibraryItem, type DramaStyleLibraryList } from "@/services/api/drama-style-library";
import { AdminPageFrame } from "../components/admin-shell";
import { AdminDataTable, AdminRowActions, AdminStatusBadge, AdminTableEmpty } from "../components/admin-ui";

type EditValues = { name: string; description: string; projectType: string };

const SOURCE_LABELS: Record<string, string> = { analysis: "风格分析", manual: "后台建", seed: "预置" };
const EMPTY: DramaStyleLibraryList = { items: [], projectTypes: [] };

/**
 * 后台「风格库」：平台风格库按项目类型分组列出，查看风格锚和主基调色卡，改名、改类型、删除，替换主色卡（编辑色板 JSON，保存后由代码重新合成主色卡图）。
 * 权限在服务端判：管理员能改删全部条目。已经选用某条风格的项目各自保留复制的风格，改、删不影响它们。
 */
export default function DramaStyleLibraryPage() {
    const { message } = App.useApp();
    const [data, setData] = useState<DramaStyleLibraryList>(EMPTY);
    const [loading, setLoading] = useState(true);
    const [typeFilter, setTypeFilter] = useState("");
    const [viewing, setViewing] = useState<DramaStyleLibraryItem>();
    const [editing, setEditing] = useState<DramaStyleLibraryItem>();
    const [paletteEditing, setPaletteEditing] = useState<DramaStyleLibraryItem>();
    const [paletteText, setPaletteText] = useState("");
    const [saving, setSaving] = useState(false);
    const [form] = Form.useForm<EditValues>();

    const reload = async () => {
        setLoading(true);
        try {
            setData(await listDramaStyleLibrary());
        } catch (error) {
            message.error(error instanceof Error ? error.message : "读取风格库失败");
        } finally {
            setLoading(false);
        }
    };
    useEffect(() => {
        void reload();
    }, []);

    const rows = useMemo(() => {
        const order = data.projectTypes.map((item) => item.value);
        return data.items.filter((item) => !typeFilter || item.projectType === typeFilter).sort((a, b) => order.indexOf(a.projectType) - order.indexOf(b.projectType));
    }, [data, typeFilter]);
    const counts = useMemo(() => Object.fromEntries(data.projectTypes.map((type) => [type.value, data.items.filter((item) => item.projectType === type.value).length])), [data]);

    const parsedPalette = useMemo(() => {
        try {
            const value = JSON.parse(paletteText || "{}") as DramaMasterPalette;
            if (!value || !Array.isArray(value.groups)) return { error: "色板要是 {groups: [{key, name, colors: [...]}]}" };
            return masterPaletteColors(value).length ? { value } : { error: "色板一个颜色都没有" };
        } catch (error) {
            return { error: error instanceof Error ? error.message : "不是合法 JSON" };
        }
    }, [paletteText]);

    const run = async (action: () => Promise<unknown>, success: string) => {
        setSaving(true);
        try {
            await action();
            message.success(success);
            await reload();
            return true;
        } catch (error) {
            message.error(error instanceof Error ? error.message : "操作失败");
            return false;
        } finally {
            setSaving(false);
        }
    };

    const openEdit = (item: DramaStyleLibraryItem) => {
        form.setFieldsValue({ name: item.name, description: item.description, projectType: item.projectType });
        setEditing(item);
    };
    const saveEdit = async () => {
        if (!editing) return;
        const values = await form.validateFields();
        if (await run(() => updateDramaStyleLibraryEntry(editing.id, { name: values.name.trim(), description: values.description.trim(), projectType: values.projectType }), "已保存")) setEditing(undefined);
    };
    const openPalette = (item: DramaStyleLibraryItem) => {
        setPaletteText(JSON.stringify(item.masterPalette, null, 2));
        setPaletteEditing(item);
    };
    const savePalette = async () => {
        if (!paletteEditing || !("value" in parsedPalette) || !parsedPalette.value) return;
        const palette = parsedPalette.value;
        if (await run(() => updateDramaStyleLibraryEntry(paletteEditing.id, { masterPalette: palette }), "主色卡已替换，图已重新合成")) setPaletteEditing(undefined);
    };

    const columns: ColumnsType<DramaStyleLibraryItem> = [
        {
            title: "主色卡",
            width: 200,
            render: (_, item) => {
                const cover = dramaStyleLibraryCoverUrl(item);
                return cover ? <img src={cover} alt={`${item.name} 主色卡`} className="aspect-video w-44 rounded border border-border/60 object-contain" loading="lazy" /> : <MasterPaletteStrip palette={item.masterPalette} className="h-6 w-44" />;
            },
        },
        {
            title: "名称与风格锚",
            width: 300,
            render: (_, item) => (
                <div>
                    <div className="font-medium">{item.name}</div>
                    <div className="mt-1 text-xs text-foreground/55">{styleAnchorBrief(item.styleAnchor) || "—"}</div>
                    {item.description ? <div className="mt-1 text-xs text-foreground/45">{item.description}</div> : null}
                </div>
            ),
        },
        { title: "类型", dataIndex: "projectTypeLabel", width: 100, align: "center" },
        { title: "颜色", dataIndex: "colorCount", width: 80, align: "center", render: (count: number) => <span className="tabular-nums">{count}</span> },
        { title: "来源", dataIndex: "source", width: 100, align: "center", render: (source: string) => <AdminStatusBadge label={SOURCE_LABELS[source] || source} tone="neutral" /> },
        { title: "存入者", dataIndex: "createdByName", width: 120, align: "center", render: (name: string, item) => name || item.createdBy },
        { title: "更新时间", dataIndex: "updatedAt", width: 170, align: "center", render: formatTime },
        {
            title: "操作",
            width: 260,
            align: "center",
            render: (_, item) => (
                <AdminRowActions
                    visibleActionCount={2}
                    primary={{ label: "查看", icon: <Eye className="size-3.5" />, onClick: () => setViewing(item) }}
                    actions={[
                        { key: "edit", label: "改名", icon: <PencilLine className="size-3.5" />, disabled: !item.canEdit, onClick: () => openEdit(item) },
                        { key: "palette", label: "替换主色卡", icon: <Palette className="size-3.5" />, disabled: !item.canEdit, onClick: () => openPalette(item) },
                        {
                            key: "delete",
                            label: "删除",
                            icon: <Trash2 className="size-3.5" />,
                            danger: true,
                            disabled: !item.canEdit,
                            confirm: { title: `删除「${item.name}」？`, description: "删除后建项目时不能再选它；已经选用它的项目保留各自复制的风格，不受影响。", okText: "确认删除" },
                            onClick: () => void run(() => deleteDramaStyleLibraryEntry(item.id), "已删除"),
                        },
                    ]}
                />
            ),
        },
    ];

    return (
        <AdminPageFrame
            title="风格库"
            description="平台风格库：建项目时锁定的风格（风格锚 + 主基调色卡），按项目类型归档，团队所有账号建项目时可选"
            actions={
                <div className="flex gap-2">
                    <Select
                        className="w-40"
                        value={typeFilter}
                        onChange={setTypeFilter}
                        options={[{ label: `全部类型（${data.items.length}）`, value: "" }, ...data.projectTypes.map((type) => ({ label: `${type.label}（${counts[type.value] || 0}）`, value: type.value }))]}
                    />
                    <Button icon={<RefreshCw className="size-4" />} onClick={() => void reload()}>
                        刷新
                    </Button>
                </div>
            }
        >
            <Callout tone="info" title="条目从项目里存进来">
                用户在章节页锁定项目风格后点「存进风格库」就会出现在这里。普通用户只能改、删自己存的条目，管理员能改删全部；替换主色卡后图由代码按新色值重新合成。
            </Callout>
            <AdminDataTable
                table={{ className: "app-data-table", size: "small", rowKey: "id", loading, pagination: false, columns, dataSource: rows, scroll: { x: 1180 } }}
                empty={<AdminTableEmpty title="风格库还是空的" description="用户锁定项目风格后可以存进来。" />}
            />

            <AdminDrawer title={viewing ? `风格：${viewing.name}` : "风格"} open={Boolean(viewing)} size="min(720px, 100vw)" onClose={() => setViewing(undefined)} rootClassName="admin-drawer">
                {viewing ? (
                    <div className="grid gap-4">
                        {dramaStyleLibraryCoverUrl(viewing) ? <img src={dramaStyleLibraryCoverUrl(viewing)} alt={`${viewing.name} 主色卡`} className="w-full rounded border border-border/60" /> : null}
                        <section>
                            <h3 className="mb-2 text-sm font-semibold">风格锚</h3>
                            <StyleAnchorSummary anchor={viewing.styleAnchor} />
                        </section>
                        <section>
                            <h3 className="mb-2 text-sm font-semibold">主基调色卡 · {viewing.colorCount} 色</h3>
                            <MasterPaletteGroups palette={viewing.masterPalette} />
                        </section>
                        <p className="text-xs text-foreground/50">
                            {viewing.projectTypeLabel} · {SOURCE_LABELS[viewing.source] || viewing.source} · {viewing.createdByName || viewing.createdBy} 存于 {formatTime(viewing.createdAt)}
                        </p>
                    </div>
                ) : null}
            </AdminDrawer>

            <AdminDrawer
                title={editing ? `改「${editing.name}」` : "改名"}
                open={Boolean(editing)}
                size="min(520px, 100vw)"
                onClose={() => !saving && setEditing(undefined)}
                rootClassName="admin-drawer"
                extra={
                    <Button type="primary" loading={saving} onClick={() => void saveEdit()}>
                        保存
                    </Button>
                }
            >
                <Form form={form} layout="vertical" requiredMark={false}>
                    <Form.Item name="name" label="名称" rules={[{ required: true, whitespace: true, message: "请填写名称" }]}>
                        <Input maxLength={60} showCount />
                    </Form.Item>
                    <Form.Item name="projectType" label="项目类型">
                        <Select options={data.projectTypes.map((type) => ({ label: type.label, value: type.value }))} />
                    </Form.Item>
                    <Form.Item name="description" label="说明">
                        <Input.TextArea maxLength={300} showCount autoSize={{ minRows: 2, maxRows: 5 }} />
                    </Form.Item>
                </Form>
            </AdminDrawer>

            <AdminDrawer
                flush
                title={paletteEditing ? `替换主色卡：${paletteEditing.name}` : "替换主色卡"}
                open={Boolean(paletteEditing)}
                size="min(1080px, 100vw)"
                onClose={() => !saving && setPaletteEditing(undefined)}
                rootClassName="admin-drawer"
                extra={
                    <Button type="primary" loading={saving} disabled={!("value" in parsedPalette)} onClick={() => void savePalette()}>
                        保存并重新合成
                    </Button>
                }
            >
                <div className="grid min-h-full lg:grid-cols-2">
                    <section className="flex min-h-0 flex-col border-b border-border p-4 lg:border-b-0 lg:border-r">
                        <div className="mb-2 flex items-center gap-2">
                            <h3 className="text-sm font-semibold">色板 JSON</h3>
                            {"error" in parsedPalette ? <AdminStatusBadge label={parsedPalette.error || "格式错误"} tone="error" /> : null}
                        </div>
                        <div className="min-h-96 flex-1 overflow-hidden rounded-md border border-border">
                            <PromptCodeEditor value={paletteText} ariaLabel="主基调色卡 JSON" onChange={setPaletteText} />
                        </div>
                        <p className="mt-2 text-xs text-foreground/50">每组 key 是 protagonist / supporting / props / environment，颜色写 name_zh、name_en、hex（#RRGGBB）、usage。</p>
                    </section>
                    <aside className="p-4">
                        <h3 className="mb-2 text-sm font-semibold">预览</h3>
                        {"value" in parsedPalette ? <MasterPaletteGroups palette={parsedPalette.value} /> : <p className="text-xs text-foreground/45">色板写对了才能预览</p>}
                    </aside>
                </div>
            </AdminDrawer>
        </AdminPageFrame>
    );
}

function formatTime(value?: string) {
    return value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "--";
}
