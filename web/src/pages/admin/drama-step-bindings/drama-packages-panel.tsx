import { App, Button, Input, Popconfirm } from "antd";
import type { ColumnsType } from "antd/es/table";
import { FileArchive, Power, RotateCcw, Upload } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Select } from "@/components/ui/base/select";
import { Callout } from "@/pages/admin/ui/controls";
import { activateAdminDramaPackageVersion, listAdminDramaPackages, listAdminDramaPackageVersions, packageProblems, uploadAdminDramaPackage, type DramaPackage, type DramaPackageProblem, type DramaPackageVersion } from "@/services/api/admin-drama-packages";
import { AdminDataTable, AdminStatusBadge, AdminTableEmpty, type AdminStatusTone } from "../components/admin-ui";

const ORIGIN_LABELS: Record<string, string> = { package: "技能包", builtin: "内置（M6 前）", online: "在线编辑（M6 前）" };
const CHECK_BADGES: Record<DramaPackageVersion["check"]["status"], { label: string; tone: AdminStatusTone }> = {
    passed: { label: "已校验", tone: "success" },
    failed: { label: "不兼容", tone: "error" },
    not_package: { label: "非包版本", tone: "neutral" },
};

type Rejection = { title: string; problems: DramaPackageProblem[] };

/** 流程技能包：上传 build.py --package 构建的 zip（静态校验通过后存成「已校验未启用」的新版本），手动启用，回退就是启用历史版本。 */
export function DramaPackagesPanel() {
    const { message } = App.useApp();
    const fileRef = useRef<HTMLInputElement>(null);
    const [packages, setPackages] = useState<DramaPackage[]>([]);
    const [selected, setSelected] = useState("");
    const [versions, setVersions] = useState<DramaPackageVersion[]>([]);
    const [loading, setLoading] = useState(true);
    const [file, setFile] = useState<File | null>(null);
    const [note, setNote] = useState("");
    const [uploading, setUploading] = useState(false);
    const [activating, setActivating] = useState("");
    const [rejection, setRejection] = useState<Rejection | null>(null);

    const loadVersions = useCallback(async (packageId: string, signal?: AbortSignal) => {
        const result = await listAdminDramaPackageVersions(packageId, signal);
        setVersions(result.versions);
        setPackages((items) => items.map((item) => (item.id === result.package.id ? result.package : item)));
    }, []);

    useEffect(() => {
        const controller = new AbortController();
        listAdminDramaPackages(controller.signal)
            .then(async ({ packages: items }) => {
                setPackages(items);
                const first = items[0]?.id || "";
                setSelected(first);
                if (first) await loadVersions(first, controller.signal);
            })
            .catch((error: unknown) => {
                if (!controller.signal.aborted) message.error(error instanceof Error ? error.message : "读取流程技能包失败");
            })
            .finally(() => setLoading(false));
        return () => controller.abort();
    }, [loadVersions, message]);

    const current = packages.find((item) => item.id === selected);

    const choose = async (packageId: string) => {
        setSelected(packageId);
        setLoading(true);
        try {
            await loadVersions(packageId);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "读取版本失败");
        } finally {
            setLoading(false);
        }
    };

    const upload = async () => {
        if (!file) return;
        setUploading(true);
        setRejection(null);
        try {
            const result = await uploadAdminDramaPackage(file, note);
            message.success(`已上传 ${result.package.name} ${result.version.versionLabel}，静态校验通过，尚未启用`);
            setFile(null);
            setNote("");
            if (fileRef.current) fileRef.current.value = "";
            setPackages((items) => (items.some((item) => item.id === result.package.id) ? items : [...items, result.package]));
            setSelected(result.package.id);
            await loadVersions(result.package.id);
        } catch (error) {
            const problems = packageProblems(error);
            if (problems) setRejection({ title: `${file.name} 没通过静态校验，没有保存`, problems });
            else message.error(error instanceof Error ? error.message : "上传技能包失败");
        } finally {
            setUploading(false);
        }
    };

    const activate = async (version: DramaPackageVersion) => {
        if (!current) return;
        setActivating(version.id);
        setRejection(null);
        try {
            await activateAdminDramaPackageVersion(current.id, version.id);
            message.success(`已启用 ${version.versionLabel}，项目下次运行这一步就用它`);
            await loadVersions(current.id);
        } catch (error) {
            const problems = packageProblems(error);
            if (problems) setRejection({ title: `${version.versionLabel} 按当前流程校验不过，不能启用`, problems });
            else message.error(error instanceof Error ? error.message : "启用版本失败");
        } finally {
            setActivating("");
        }
    };

    const activeCreatedAt = versions.find((item) => item.active)?.createdAt || "";
    const columns: ColumnsType<DramaPackageVersion> = [
        {
            title: "版本",
            dataIndex: "versionLabel",
            width: 170,
            render: (label: string, version) => (
                <div className="flex flex-wrap items-center gap-1.5">
                    <span className="font-mono text-xs">{label}</span>
                    {version.active ? <AdminStatusBadge label="生效中" tone="success" /> : null}
                </div>
            ),
        },
        { title: "来源", dataIndex: "origin", width: 130, render: (origin: string) => ORIGIN_LABELS[origin] || origin || "--" },
        { title: "契约", dataIndex: "contract", width: 130, render: (value?: string) => <span className="font-mono text-xs">{value || "--"}</span> },
        {
            title: "内容哈希",
            dataIndex: "contentHash",
            width: 120,
            render: (hash: string) => (
                <span className="font-mono text-xs" title={hash}>
                    {hash ? hash.slice(0, 12) : "--"}
                </span>
            ),
        },
        {
            title: "校验",
            width: 280,
            render: (_, version) => {
                const badge = CHECK_BADGES[version.check.status];
                return (
                    <div className="text-xs">
                        <AdminStatusBadge label={badge.label} tone={badge.tone} />
                        {version.check.problems.length ? (
                            <ul className="mt-1 list-disc pl-4 text-foreground/60">
                                {version.check.problems.map((problem, index) => (
                                    <li key={index}>{problem.message}</li>
                                ))}
                            </ul>
                        ) : null}
                    </div>
                );
            },
        },
        {
            title: "上传",
            width: 200,
            render: (_, version) => (
                <div className="text-xs">
                    <div>{version.createdByName || (version.createdBy ? version.createdBy : "系统")}</div>
                    <div className="mt-0.5 text-foreground/45">{formatTime(version.createdAt)}</div>
                </div>
            ),
        },
        { title: "说明", dataIndex: "note", render: (value: string) => <span className="text-xs">{value || "--"}</span> },
        {
            title: "",
            width: 110,
            align: "right",
            render: (_, version) => {
                const rollback = Boolean(activeCreatedAt) && version.createdAt < activeCreatedAt;
                const action = rollback ? "回退" : "启用";
                return (
                    <Popconfirm
                        title={`${action}到 ${version.versionLabel}？`}
                        description="所有项目下次运行这一步就用这个版本；其他版本保留在列表里，随时可以再启用。"
                        okText={`确认${action}`}
                        cancelText="取消"
                        disabled={version.active || version.check.status !== "passed"}
                        onConfirm={() => activate(version)}
                    >
                        <Button
                            size="small"
                            type="text"
                            icon={rollback ? <RotateCcw className="size-3.5" /> : <Power className="size-3.5" />}
                            loading={activating === version.id}
                            disabled={version.active || version.check.status !== "passed" || Boolean(activating)}
                        >
                            {action}
                        </Button>
                    </Popconfirm>
                );
            },
        },
    ];

    return (
        <section className="mt-6 grid gap-3" aria-label="流程技能包">
            <div>
                <h2 className="text-base font-semibold">技能包版本</h2>
                <p className="mt-1 text-xs text-foreground/55">
                    技能以本地 yoozee-skills 仓库为准：改好后运行 <code className="font-mono">python yoozee-skills/build.py --package &lt;id&gt;</code> 构建 zip，在这里上传。上传只做静态校验并存成新版本，不会自动启用。
                </p>
            </div>
            <div className="flex flex-wrap items-center gap-2">
                <input
                    ref={fileRef}
                    type="file"
                    accept=".zip,application/zip"
                    className="hidden"
                    aria-label="选择技能包 ZIP"
                    onChange={(event) => {
                        setFile(event.target.files?.[0] || null);
                        setRejection(null);
                    }}
                />
                <Button icon={<FileArchive className="size-4" />} onClick={() => fileRef.current?.click()} disabled={uploading}>
                    {file ? file.name : "选择技能包 ZIP"}
                </Button>
                <Input className="max-w-md" maxLength={500} value={note} onChange={(event) => setNote(event.target.value)} placeholder="这一版改了什么（可选）" aria-label="版本说明" />
                <Button type="primary" icon={<Upload className="size-4" />} loading={uploading} disabled={!file} onClick={() => void upload()}>
                    上传并校验
                </Button>
            </div>
            {rejection ? (
                <Callout tone="error" title={rejection.title}>
                    <ul className="list-disc pl-4">
                        {rejection.problems.map((problem, index) => (
                            <li key={index}>
                                <span className="font-mono text-xs text-foreground/55">[{problem.rule}]</span> {problem.message}
                            </li>
                        ))}
                    </ul>
                </Callout>
            ) : null}
            <div className="flex flex-wrap items-center gap-3 text-sm">
                <Select
                    aria-label="选择流程技能包"
                    className="min-w-64"
                    value={selected || undefined}
                    placeholder="没有流程技能包"
                    onChange={(value: string) => void choose(value)}
                    options={packages.map((item) => ({ value: item.id, label: `${item.name}（${item.id}）` }))}
                />
                {current ? (
                    <span className="text-xs text-foreground/55">
                        步骤 {current.stepLabel || current.step}（线上契约 <span className="font-mono">{current.stepContract || "--"}</span>）· 生效 {current.currentVersion?.versionLabel || "--"} · 共 {current.versionCount} 个包版本
                    </span>
                ) : null}
            </div>
            {current && current.contract && current.stepContract && current.contract !== current.stepContract ? (
                <Callout tone="warning" title="生效版本与流程契约不符">
                    生效版本声明的契约是 {current.contract}，线上这一步是 {current.stepContract}。请上传升级到新契约的包并启用。
                </Callout>
            ) : null}
            <AdminDataTable
                table={{ className: "app-data-table", size: "small", rowKey: "id", loading, pagination: false, columns, dataSource: versions, scroll: { x: 1100 } }}
                empty={<AdminTableEmpty title="还没有版本" description="上传 build.py --package 构建的 zip 后这里会出现新版本。" />}
            />
        </section>
    );
}

function formatTime(value?: string) {
    return value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "--";
}
