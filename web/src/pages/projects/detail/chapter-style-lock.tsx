import { App, Button, Input, Modal } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Lock, Palette } from "lucide-react";
import { useNavigate } from "react-router";

import { MasterPaletteGroups, StyleAnchorSummary } from "@/components/drama/master-palette";
import { ModelPicker } from "@/components/model-picker";
import { Select } from "@/components/ui/base/select";
import { Callout } from "@/components/ui/product/callout";
import { PROJECT_CONTENT_TYPES, projectContentType } from "@/lib/canvas/project-content-type";
import { navigateToSettings } from "@/lib/settings-navigation";
import { dramaRunProgressOf, dramaStyleLockOutcomeOf, getDramaStepUsage, masterPaletteColors, type DramaBible } from "@/services/api/drama";
import { dramaStyleLibraryKey, lockDramaStyle, saveDramaStyleToLibrary } from "@/services/api/drama-style-library";
import type { ProjectDetail } from "@/services/api/projects";
import { queryGenerationTask, type GenerationTask } from "@/services/api/task-center";
import { useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";

import { STYLE_LOCK_TEXT_CHARS, StyleReferenceImagesPicker, uploadStyleReferenceImages } from "../project-style-inputs";
import { storyboardRunStop } from "./chapter-shot-stream";
import { DramaRunPanel } from "./drama-run-panel";
import { followDramaRunTask, isStyleLockTask, runStyleLock } from "./project-chapter-ai";
import { parseStyleLockStream, type StyleLockStreamState } from "./style-lock-stream";

const FOLLOW_KEY = "drama-style-lock-follow:";

/**
 * 建项目时起了风格分析：记下任务，章节页的「分析项目风格」挂载后打开侧栏接上它的流（建项目后要跳页面，组件 state 会丢）。
 */
export function followStyleLockAfterCreate(projectId: string, taskId: string) {
    try {
        sessionStorage.setItem(FOLLOW_KEY + projectId, JSON.stringify({ taskId, at: Date.now() }));
    } catch {
        // 存不了（隐私模式）时用户点「风格进度」也能看到。
    }
}

function takeFollowedStyleLock(projectId: string) {
    try {
        const raw = sessionStorage.getItem(FOLLOW_KEY + projectId);
        sessionStorage.removeItem(FOLLOW_KEY + projectId);
        const saved = raw ? (JSON.parse(raw) as { taskId?: string; at?: number }) : undefined;
        return saved?.taskId && Date.now() - (saved.at || 0) < 10 * 60_000 ? saved.taskId : "";
    } catch {
        return "";
    }
}

/** 项目连戏表里锁定的风格（风格锚、主基调色卡、来源）。 */
export function projectLockedStyle(bibleJson?: string) {
    try {
        const bible = JSON.parse(bibleJson || "{}") as DramaBible;
        const locked = Boolean(bible.style_anchor && Object.keys(bible.style_anchor).length && masterPaletteColors(bible.master_palette).length);
        return { locked, anchor: bible.style_anchor, palette: bible.master_palette, record: bible.style_lock };
    } catch {
        return { locked: false, anchor: undefined, palette: undefined, record: undefined };
    }
}

/** 上一次分析的输入（「重新分析」回填项目类型和风格文字；参考图要重新选）。 */
function previousLockInput(task?: GenerationTask) {
    try {
        const lock = (JSON.parse(task?.inputJson || "{}") as { metadata?: { dramaStyleLock?: { projectType?: string; text?: string } } }).metadata?.dramaStyleLock;
        return lock ? { projectType: lock.projectType || "", text: lock.text || "" } : undefined;
    } catch {
        return undefined;
    }
}

const SOURCE_LABELS: Record<string, string> = { analysis: "风格分析后锁定", library: "取自平台风格库", preset: "按画风生成后锁定" };

function StyleLockRunGroups(stream: StyleLockStreamState) {
    const groups = stream.anchor ? [{ key: "anchor", title: "全局风格锚", meta: "", body: <StyleAnchorSummary anchor={stream.anchor} /> }] : [];
    if (stream.colorCount)
        groups.push({ key: "palette", title: "主基调色卡", meta: `${stream.palette.groups.length} 组 · ${stream.colorCount} 色${stream.attempt > 1 ? ` · 第 ${stream.attempt} 次` : ""}`, body: <MasterPaletteGroups palette={stream.palette} /> });
    return groups;
}

type Settings = { projectType: string; text: string; images: File[]; textModel: string };

/**
 * 章节页的「分析项目风格」（项目级步骤 style.lock）：对话框写风格文字、传参考图（随任务发给能读图的文本模型）、选文本模型；侧栏流式显示
 * 风格锚和主基调色卡，跑完给「锁定此风格」，锁定后可「存进风格库」。建项目时起的分析也在这里接上。
 */
export function StyleLockControl({ detail, tasks, disabled }: { detail: ProjectDetail; tasks?: GenerationTask[]; disabled?: boolean }) {
    const { message } = App.useApp();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const projectId = detail.project.id;
    const effectiveConfig = useEffectiveConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const current = useMemo(() => projectLockedStyle(detail.project.bibleJson), [detail.project.bibleJson]);
    const [open, setOpen] = useState(false);
    const [panelOpen, setPanelOpen] = useState(false);
    const [starting, setStarting] = useState(false);
    const [locking, setLocking] = useState(false);
    const [saveOpen, setSaveOpen] = useState(false);
    const [saveName, setSaveName] = useState("");
    const [saving, setSaving] = useState(false);
    const [stream, setStream] = useState<{ taskId: string; text: string }>({ taskId: "", text: "" });
    const followedRef = useRef(new Set<string>());
    const [settings, setSettings] = useState<Settings>({ projectType: "", text: "", images: [], textModel: "" });
    const usageQuery = useQuery({
        queryKey: ["project", projectId, "drama-step", "style.lock", detail.project.styleProfile || ""],
        queryFn: ({ signal }) => getDramaStepUsage(projectId, "style.lock", signal).then((result) => result.usage),
        enabled: open,
    });
    const latestTask = useMemo(() => (tasks || []).find(isStyleLockTask), [tasks]);
    // following：接上的任务（建项目时起的）还没出现在任务列表里，也按正在跑显示。
    const [following, setFollowing] = useState(false);
    const running = starting || following || Boolean(latestTask && (latestTask.status === "queued" || latestTask.status === "running"));
    const terminal = latestTask && latestTask.status !== "queued" && latestTask.status !== "running" ? latestTask : undefined;
    const detailQuery = useQuery({ queryKey: ["generation-task", terminal?.id, terminal?.status], queryFn: () => queryGenerationTask(terminal!.id), enabled: Boolean(terminal), staleTime: Infinity });
    const taskDetail = detailQuery.data;
    const outcome = taskDetail?.status === "succeeded" ? dramaStyleLockOutcomeOf(taskDetail) : undefined;
    const stop = useMemo(() => (taskDetail ? storyboardRunStop(taskDetail, dramaRunProgressOf(taskDetail)) : undefined), [taskDetail]);
    const streamState = useMemo(() => {
        if (running || (stream.text && (stream.taskId === taskDetail?.id || !taskDetail))) return parseStyleLockStream(stream.text);
        if (outcome) return { anchor: outcome.styleAnchor, palette: outcome.masterPalette, attempt: 1, colorCount: masterPaletteColors(outcome.masterPalette).length };
        return parseStyleLockStream("");
    }, [outcome, running, stream, taskDetail]);
    // 这次分析的候选有没有锁进项目：项目的锁定记录指向这个任务。
    const lockedThisTask = Boolean(outcome && current.locked && current.record?.task_id === taskDetail?.id);
    const savedToLibrary = Boolean(current.record?.library_id);

    const refresh = async () => {
        await Promise.all([queryClient.invalidateQueries({ queryKey: ["project", projectId] }), queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] })]);
    };

    const openDialog = () => {
        setPanelOpen(false);
        const previous = previousLockInput(taskDetail);
        setSettings((value) => ({
            projectType: value.projectType || previous?.projectType || current.record?.project_type || "",
            text: value.text || previous?.text || "",
            images: value.images,
            textModel: value.textModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "",
        }));
        setOpen(true);
    };

    const start = async () => {
        if (detail.project.status === "archived") return message.warning("项目已归档，请先在项目设置中恢复");
        const textModel = settings.textModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const config = { ...effectiveConfig, model: textModel, textModel };
        if (!isAiConfigReady(config, textModel)) {
            navigateToSettings({ continueCreation: true });
            return;
        }
        setOpen(false);
        setStarting(true);
        setStream({ taskId: "", text: "" });
        setPanelOpen(true);
        try {
            const referenceImages = await uploadStyleReferenceImages(settings.images);
            const result = await runStyleLock(
                { projectId, config, projectType: settings.projectType, styleText: settings.text, referenceImages },
                {
                    onTaskUpdate: (task) => {
                        followedRef.current.add(task.id);
                        setStream((value) => ({ taskId: task.id, text: value.taskId && value.taskId !== task.id ? "" : value.text }));
                    },
                    onStreamText: (text) => setStream((value) => ({ ...value, text })),
                },
            );
            await refresh();
            if (result.outcome) message.success(`风格分析完成：${masterPaletteColors(result.outcome.masterPalette).length} 色主基调色卡，满意就点「锁定此风格」`);
        } catch (error) {
            await refresh();
            message.error(error instanceof Error ? `风格分析失败：${error.message}` : "风格分析失败");
        } finally {
            setStarting(false);
        }
    };

    const follow = (task: GenerationTask) => {
        followedRef.current.add(task.id);
        setStream({ taskId: task.id, text: "" });
        setFollowing(true);
        void followDramaRunTask(task, { onStreamText: (text) => setStream({ taskId: task.id, text }) })
            .then(() => refresh())
            .catch(() => void queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] }))
            .finally(() => setFollowing(false));
    };
    useEffect(() => {
        const task = latestTask;
        if (!panelOpen || !task || (task.status !== "queued" && task.status !== "running") || followedRef.current.has(task.id)) return;
        follow(task);
    }, [latestTask, panelOpen]);
    // 建项目时起的分析：挂载时打开侧栏接上。
    useEffect(() => {
        const taskId = takeFollowedStyleLock(projectId);
        if (!taskId) return;
        setPanelOpen(true);
        void queryGenerationTask(taskId)
            .then((task) => {
                if (task.status === "queued" || task.status === "running") follow(task);
                void queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] });
            })
            .catch(() => message.warning("没读到建项目时起的风格分析，稍后在「风格进度」里看"));
    }, [projectId]);

    const lock = async () => {
        if (!taskDetail) return;
        setLocking(true);
        try {
            const result = await lockDramaStyle(projectId, { taskId: taskDetail.id });
            await Promise.all([refresh(), queryClient.invalidateQueries({ queryKey: ["project", projectId, "assets"] })]);
            message.success(`已锁定：风格锚和 ${masterPaletteColors(result.masterPalette).length} 色主基调色卡写进项目，主色卡在资产页`);
            if (result.imageError) message.warning(result.imageError);
            if (result.fontNote) message.warning(`主色卡图没有中文字体，只画了 HEX 和英文名：${result.fontNote}`);
        } catch (error) {
            message.error(error instanceof Error ? `锁定失败：${error.message}` : "锁定失败");
        } finally {
            setLocking(false);
        }
    };

    const save = async () => {
        setSaving(true);
        try {
            const item = await saveDramaStyleToLibrary({ projectId, name: saveName });
            await Promise.all([refresh(), queryClient.invalidateQueries({ queryKey: dramaStyleLibraryKey(item.projectType) }), queryClient.invalidateQueries({ queryKey: dramaStyleLibraryKey() })]);
            setSaveOpen(false);
            message.success(`已存进风格库「${item.projectTypeLabel}」：${item.name}，团队里建同类型项目时都能选`);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "存进风格库失败");
        } finally {
            setSaving(false);
        }
    };

    const usage = usageQuery.data;
    const warnings = (outcome?.items || []).filter((item) => item.level !== "error");
    const typeLabel = projectContentType(settings.projectType)?.label;
    const status = running ? (
        <span className="font-medium text-foreground/80">{streamState.colorCount ? `已出 ${streamState.colorCount} 色` : streamState.anchor ? "风格锚已出，正在写色板" : "等待模型开始输出"}</span>
    ) : outcome ? (
        <span>{outcome.status === "locked" || lockedThisTask ? "这次分析的风格已锁定到项目" : "分析完成，还没锁定"}</span>
    ) : stop?.kind === "failed" ? (
        <span>{stop.status === "cancelled" ? "上一次风格分析已取消" : "上一次风格分析失败"}</span>
    ) : current.locked ? (
        <span>项目风格已锁定（{SOURCE_LABELS[current.record?.source || ""] || "已锁定"}）</span>
    ) : (
        <span>还没有分析项目风格</span>
    );
    const canSave = current.locked && current.record?.source !== "library" && !savedToLibrary;

    return (
        <>
            <Button size="small" icon={<Palette className="size-3.5" />} disabled={disabled || running} loading={running} onClick={openDialog} aria-label="分析项目风格">
                {running ? "分析风格中" : "分析项目风格"}
            </Button>
            {running || latestTask || current.locked ? (
                <Button size="small" icon={current.locked ? <Lock className="size-3.5" /> : undefined} onClick={() => setPanelOpen(true)} aria-label="查看项目风格">
                    {current.locked && !running ? "项目风格" : "风格进度"}
                </Button>
            ) : null}
            <Modal
                className="library-modal"
                title="分析项目风格"
                open={open}
                width={600}
                okText="开始分析"
                cancelText="取消"
                okButtonProps={{ disabled: !settings.textModel || !settings.projectType || !usage || settings.text.length > STYLE_LOCK_TEXT_CHARS }}
                onCancel={() => setOpen(false)}
                onOk={() => void start()}
                styles={{ body: { paddingTop: 12 } }}
            >
                <div className="grid gap-4">
                    {current.locked ? (
                        <Callout tone="info" title="项目已经锁定了风格">
                            重新分析不会改动已锁定的风格；分析完满意再点「锁定此风格」替换，主色卡加一个版本。
                        </Callout>
                    ) : null}
                    <label className="block">
                        <span className="mb-1.5 block text-xs font-medium text-foreground/68">项目类型</span>
                        <Select
                            className="w-full"
                            value={settings.projectType || undefined}
                            placeholder="选类型"
                            onChange={(projectType: string) => setSettings((value) => ({ ...value, projectType }))}
                            options={PROJECT_CONTENT_TYPES.map(({ label, value }) => ({ label, value }))}
                        />
                    </label>
                    <label className="block">
                        <span className="mb-1.5 block text-xs font-medium text-foreground/68">风格描述（可选）</span>
                        <Input.TextArea
                            value={settings.text}
                            onChange={(event) => setSettings((value) => ({ ...value, text: event.target.value }))}
                            autoSize={{ minRows: 3, maxRows: 8 }}
                            maxLength={STYLE_LOCK_TEXT_CHARS}
                            showCount
                            className="!mb-5"
                            placeholder="例如：都市冷调悬疑，低饱和、实景光源，少用霓虹；人物写实不精修"
                        />
                    </label>
                    <div>
                        <span className="mb-1.5 block text-xs font-medium text-foreground/68">参考图（可选，最多 6 张）</span>
                        <StyleReferenceImagesPicker value={settings.images} onChange={(images) => setSettings((value) => ({ ...value, images }))} />
                        <p className="mt-1.5 text-[var(--fs-tiny)] leading-[18px] text-foreground/45">参考图会随任务发给下面的文本模型，要选能读图的模型。文字和图都不给时按项目画风{typeLabel ? `（${typeLabel}）` : ""}定。</p>
                    </div>
                    <label className="block">
                        <span className="mb-1.5 block text-xs font-medium text-foreground/68">文本模型</span>
                        <ModelPicker
                            config={effectiveConfig}
                            capability="text"
                            value={settings.textModel}
                            onChange={(textModel) => setSettings((value) => ({ ...value, textModel }))}
                            variant="creation"
                            fullWidth
                            placeholder="选择用于风格分析的文本模型"
                            showSelectedPrice={false}
                            onMissingConfig={() => navigateToSettings({ continueCreation: true })}
                        />
                    </label>
                    {usageQuery.isError ? (
                        <Callout tone="error" title="读不到本步使用的技能">
                            {usageQuery.error instanceof Error ? usageQuery.error.message : "请稍后重试"}
                        </Callout>
                    ) : (
                        <div className="rounded-lg border border-border/70 px-3 py-2.5 text-xs leading-5 text-foreground/62" aria-live="polite">
                            {usage ? (
                                <>
                                    本步使用：<span className="font-medium text-foreground/85">{usage.skill.name}</span> {usage.skill.version}（{usage.profile.label}）
                                    <div className="mt-1 text-[var(--fs-tiny)] text-foreground/42">出风格锚和按主角组、配角组、道具组、环境组分组的主基调色卡；锁定后主色卡图由代码按色值合成。</div>
                                </>
                            ) : (
                                "正在读取本步使用的技能…"
                            )}
                        </div>
                    )}
                </div>
            </Modal>
            <Modal className="library-modal" title="存进风格库" open={saveOpen} width={460} okText="存进去" cancelText="取消" okButtonProps={{ disabled: !saveName.trim(), loading: saving }} onCancel={() => setSaveOpen(false)} onOk={() => void save()}>
                <p className="mb-3 text-xs leading-5 text-foreground/55">风格锚和主基调色卡按项目类型存进平台风格库，团队里所有账号建同类型项目时都能直接选用；管理员和你可以改名、删除。</p>
                <Input value={saveName} maxLength={60} showCount autoFocus placeholder="例如：都市冷调悬疑" onChange={(event) => setSaveName(event.target.value)} onPressEnter={() => saveName.trim() && void save()} />
            </Modal>
            <DramaRunPanel
                open={panelOpen}
                onClose={() => setPanelOpen(false)}
                title="项目风格"
                running={running}
                status={status}
                unit="轮"
                stepLabel="风格分析"
                stop={running ? undefined : stop}
                onResume={() => openDialog()}
                onRegenerate={() => {
                    setPanelOpen(false);
                    openDialog();
                }}
                failedNote="项目已锁定的风格没有改动。"
                followKey={streamState.colorCount + (streamState.anchor ? 1 : 0)}
                emptyText={current.locked && !latestTask ? "" : "风格锚和主基调色卡会出现在这里"}
                listLabel="流式风格分析"
                groups={
                    running || outcome
                        ? StyleLockRunGroups(streamState)
                        : current.locked
                          ? StyleLockRunGroups({ anchor: current.anchor, palette: current.palette || { groups: [] }, attempt: 1, colorCount: masterPaletteColors(current.palette).length })
                          : []
                }
            >
                {outcome && !running ? (
                    <Callout
                        tone={outcome.status === "locked" || lockedThisTask ? "success" : "info"}
                        title={outcome.status === "locked" || lockedThisTask ? "已锁定到项目" : "满意就锁定这个风格"}
                        action={
                            outcome.status === "locked" || lockedThisTask ? (
                                <Button size="small" onClick={() => navigate(`/projects/${projectId}/assets?category=palette`)}>
                                    看主色卡
                                </Button>
                            ) : (
                                <Button size="small" type="primary" icon={<Lock className="size-3.5" />} loading={locking} onClick={() => void lock()}>
                                    锁定此风格
                                </Button>
                            )
                        }
                    >
                        {outcome.status === "locked" || lockedThisTask ? "风格锚和主基调色卡已写进项目，后面的剧本优化、色卡和资产都按它来。" : "锁定后写进项目、合成主色卡图；不满意可以「重新分析」。"}
                        {warnings.length ? (
                            <ul className="mt-2 max-h-40 space-y-1 overflow-y-auto pl-0" aria-label="风格分析检查提醒">
                                {warnings.map((item, index) => (
                                    <li key={`${item.rule}-${index}`} className="list-none rounded-md bg-surface-active px-2.5 py-1.5">
                                        <span className="font-mono text-[var(--fs-micro)] text-foreground/50">{item.rule}</span>
                                        <div>{item.message}</div>
                                    </li>
                                ))}
                            </ul>
                        ) : null}
                    </Callout>
                ) : null}
                {!running ? (
                    <div className="flex flex-wrap gap-2">
                        <Button size="small" onClick={() => openDialog()}>
                            {latestTask ? "重新分析" : "分析项目风格"}
                        </Button>
                        {canSave ? (
                            <Button
                                size="small"
                                onClick={() => {
                                    setSaveName("");
                                    setSaveOpen(true);
                                }}
                            >
                                存进风格库
                            </Button>
                        ) : null}
                        {savedToLibrary && current.record?.source !== "library" ? <span className="self-center text-[var(--fs-tiny)] text-foreground/45">已存进风格库</span> : null}
                    </div>
                ) : null}
            </DramaRunPanel>
        </>
    );
}
