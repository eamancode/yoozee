import { App, Button, Checkbox, Modal, Switch } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Palette } from "lucide-react";
import { useNavigate } from "react-router";

import { ModelPicker } from "@/components/model-picker";
import { Callout } from "@/components/ui/product/callout";
import { navigateToSettings } from "@/lib/settings-navigation";
import { dramaRunProgressOf, dramaSceneCode, dramaStyleBibleOutcomeOf, getDramaStepUsage, type DramaStyleAnchor, type DramaStyleBibleOutcome } from "@/services/api/drama";
import type { DramaImageStatus, ProjectDetail, ProjectUnit } from "@/services/api/projects";
import { queryGenerationTask, type GenerationTask } from "@/services/api/task-center";
import { useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";

import { storyboardRunStop } from "./chapter-shot-stream";
import { DramaRunPanel } from "./drama-run-panel";
import { paletteImageTaskTemplate } from "./drama-image-task";
import { dramaImageSummaryText, useDramaImageStatuses } from "./drama-image-summary";
import { followDramaRunTask, isStyleBibleTask, runStyleBible } from "./project-chapter-ai";
import { turnaroundImageModel } from "./project-character-media";
import { parseStyleBibleStream, resumedStyleBibleStream, styleBibleStreamFromResult, type StyleBibleStreamState } from "./style-bible-stream";

type StyleBibleControlProps = {
    detail: ProjectDetail;
    chapters: ProjectUnit[];
    episodes: ProjectUnit[];
    tasks?: GenerationTask[];
    disabled?: boolean;
};

const CHAINED_STYLE_KEY = "drama-style-bible-follow:";
const CHAINED_STYLE_EVENT = "drama-style-bible-follow";

/**
 * 剧本优化跑完服务端自动接着建了导演风格任务：交给导演风格按钮打开侧栏接上它的流。写完剧集会切到剧集的路由，章节页会重新挂载，
 * 所以先记进 sessionStorage（新挂载的按钮读它），同时发事件（没重新挂载时直接接上）。
 */
export function followChainedStyleBible(projectId: string, taskId: string) {
    try {
        sessionStorage.setItem(CHAINED_STYLE_KEY + projectId, JSON.stringify({ taskId, at: Date.now() }));
    } catch {
        // 存不了（隐私模式）时只靠事件。
    }
    window.dispatchEvent(new CustomEvent(CHAINED_STYLE_EVENT, { detail: { projectId, taskId } }));
}

/** 挂载时取走记下的任务；一分钟以前记的不再接（用户早就离开过章节页）。 */
function takeChainedStyleBible(projectId: string) {
    try {
        const raw = sessionStorage.getItem(CHAINED_STYLE_KEY + projectId);
        sessionStorage.removeItem(CHAINED_STYLE_KEY + projectId);
        const saved = raw ? (JSON.parse(raw) as { taskId?: string; at?: number }) : undefined;
        return saved?.taskId && Date.now() - (saved.at || 0) < 60_000 ? saved.taskId : "";
    } catch {
        return "";
    }
}

type Settings = { sourceIds: string[]; textModel: string; images: boolean; imageModel: string };

const ANCHOR_FIELDS: Array<[keyof DramaStyleAnchor, string]> = [
    ["medium", "画风 / 媒介"],
    ["art_style", "美术风格"],
    ["lighting", "光线政策"],
    ["camera", "镜头语言"],
    ["grading", "调色倾向"],
    ["era", "时代"],
];

/** 「从第 N 集继续」要和上一次同一批剧集（服务端核对轮次）：从上一次任务的 metadata.dramaStyle 取。 */
function previousStyleRun(task?: GenerationTask) {
    try {
        const style = (JSON.parse(task?.inputJson || "{}") as { metadata?: { dramaStyle?: { episodes?: Array<{ unitId?: string }>; images?: boolean } } }).metadata?.dramaStyle;
        const ids = (style?.episodes || []).map((item) => item.unitId || "").filter(Boolean);
        return ids.length ? { sourceUnitIds: ids, images: Boolean(style?.images) } : undefined;
    } catch {
        return undefined;
    }
}

export function StyleAnchorSummary({ anchor }: { anchor?: DramaStyleAnchor }) {
    if (!anchor) return null;
    const rows = ANCHOR_FIELDS.filter(([key]) => typeof anchor[key] === "string" && String(anchor[key]).trim());
    return (
        <dl className="grid gap-1 rounded-md border border-border/60 px-2.5 py-1.5 text-xs leading-5">
            {rows.map(([key, label]) => (
                <div key={key} className="grid grid-cols-[5rem_1fr] gap-2">
                    <dt className="text-foreground/45">{label}</dt>
                    <dd className="m-0 text-foreground/80">{String(anchor[key])}</dd>
                </div>
            ))}
            {anchor.forbidden?.length ? (
                <div className="grid grid-cols-[5rem_1fr] gap-2">
                    <dt className="text-foreground/45">禁用</dt>
                    <dd className="m-0 text-foreground/80">{anchor.forbidden.join("、")}</dd>
                </div>
            ) : null}
        </dl>
    );
}

function StyleBibleRunGroups(stream: StyleBibleStreamState) {
    const groups = stream.anchor ? [{ key: "anchor", title: "全局风格锚", meta: "", body: <StyleAnchorSummary anchor={stream.anchor} /> }] : [];
    return [
        ...groups,
        ...stream.episodes.map((episode) => ({
            key: episode.index,
            title: `第 ${episode.episode || episode.index} 集`,
            heading: episode.title || undefined,
            meta: `${episode.scenes.length} 场${episode.attempt > 1 ? ` · 第 ${episode.attempt} 次` : ""}`,
            body: (
                <ul className="space-y-1.5 rounded-md border border-border/60 px-2.5 py-1.5 pl-2.5 text-xs leading-5">
                    {episode.scenes.map((scene) => {
                        const colors = [...scene.shared_colors, ...scene.new_colors];
                        return (
                            <li key={`${scene.episode}-${scene.scene_no}`} className="list-none">
                                <div className="flex items-center gap-2">
                                    <span className="font-mono text-[var(--fs-micro)] text-foreground/45">{dramaSceneCode(scene.episode, scene.scene_no)}</span>
                                    <span className="min-w-0 flex-1 truncate font-medium text-foreground/80">{scene.scene}</span>
                                    {scene.inherits_from ? <span className="shrink-0 text-[var(--fs-micro)] text-foreground/45">承接 {scene.inherits_from}</span> : null}
                                </div>
                                <div className="mt-1 flex gap-0.5" aria-label={`${scene.scene} 的 ${colors.length} 个颜色`}>
                                    {colors.map((color, index) => (
                                        <span
                                            key={`${color.hex}-${index}`}
                                            title={`${color.name_zh} ${color.hex}${color.usage ? ` · ${color.usage}` : ""}`}
                                            className={index < scene.shared_colors.length ? "h-4 min-w-0 flex-1 rounded-sm border border-border/70" : "h-4 min-w-0 flex-1 rounded-sm border border-border/70 ring-1 ring-inset ring-foreground/10"}
                                            style={{ backgroundColor: color.hex }}
                                        />
                                    ))}
                                </div>
                            </li>
                        );
                    })}
                    {!episode.scenes.length ? <li className="list-none text-foreground/40">等待这一集的色彩脚本…</li> : null}
                </ul>
            ),
        })),
    ];
}

/** 这次排了出图的色卡（排上、等空位、没排上）。 */
function imageAssetIds(outcome?: DramaStyleBibleOutcome) {
    return outcome ? [...outcome.images.queued, ...outcome.images.waiting, ...outcome.images.failed] : [];
}

/** 色卡出图的一句话：读到色卡当前的出图状态时按它算（statuses），否则按收尾时排上几张、等空位几张、没排上几张。 */
function imagesText(outcome: DramaStyleBibleOutcome, statuses?: DramaImageStatus[]) {
    const { queued, waiting, failed } = outcome.images;
    if (!queued.length && !waiting.length && !failed.length) return "这次没有给色卡出图，到资产页的「色卡」里可以出图。";
    if (statuses?.length) return `色卡：${dramaImageSummaryText(statuses, "张", "色卡上")}`;
    return [`色卡出图已排 ${queued.length} 张`, waiting.length ? `${waiting.length} 张等前面的出完接着排` : "", failed.length ? `${failed.length} 张没排上（原因写在色卡上）` : ""].filter(Boolean).join("，") + "。";
}

/**
 * 章节页的「导演风格」（项目级步骤 style.bible）：对话框选范围（默认全部剧集，没有剧集时是原文章节）、文本模型、要不要给色卡出图和
 * 用哪个图片模型（默认项目默认图片模型）；技能由项目风格档案的绑定决定。进度侧栏按集流式显示每场的色块。
 */
export function StyleBibleControl({ detail, chapters, episodes, tasks, disabled }: StyleBibleControlProps) {
    const { message } = App.useApp();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const projectId = detail.project.id;
    const effectiveConfig = useEffectiveConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const candidates = episodes.length ? episodes : chapters;
    const fromChapters = !episodes.length;
    const [open, setOpen] = useState(false);
    const [panelOpen, setPanelOpen] = useState(false);
    const [starting, setStarting] = useState(false);
    const [preparing, setPreparing] = useState(false);
    /** 开跑前的检查（图片模型没配好、出图模板组不出来）没过的原因：常驻在对话框里，侧栏不打开。 */
    const [precheckError, setPrecheckError] = useState("");
    const [stream, setStream] = useState<{ taskId: string; text: string }>({ taskId: "", text: "" });
    const [resumeView, setResumeView] = useState<{ from: number; total: number; previous: StyleBibleStreamState }>();
    const followedRef = useRef(new Set<string>());
    const [settings, setSettings] = useState<Settings>({ sourceIds: [], textModel: "", images: true, imageModel: "" });
    const usageQuery = useQuery({
        queryKey: ["project", projectId, "drama-step", "style.bible", detail.project.styleProfile || ""],
        queryFn: ({ signal }) => getDramaStepUsage(projectId, "style.bible", signal).then((result) => result.usage),
        enabled: open,
    });
    const latestTask = useMemo(() => (tasks || []).find(isStyleBibleTask), [tasks]);
    const running = starting || Boolean(latestTask && (latestTask.status === "queued" || latestTask.status === "running"));
    const terminal = latestTask && latestTask.status !== "queued" && latestTask.status !== "running" ? latestTask : undefined;
    const detailQuery = useQuery({ queryKey: ["generation-task", terminal?.id, terminal?.status], queryFn: () => queryGenerationTask(terminal!.id), enabled: Boolean(terminal), staleTime: Infinity });
    const taskDetail = detailQuery.data;
    const outcome = taskDetail?.status === "succeeded" ? dramaStyleBibleOutcomeOf(taskDetail) : undefined;
    const stop = useMemo(() => (taskDetail ? storyboardRunStop(taskDetail, dramaRunProgressOf(taskDetail)) : undefined), [taskDetail]);
    const imageIds = imageAssetIds(outcome);
    // 侧栏打开时按色卡当前的出图状态重算摘要：收尾之后接着排、出完、失败，「继续出图」重出的都算进去。
    const imageStatuses = useDramaImageStatuses(projectId, imageIds, panelOpen && !running);
    const resume = stop?.kind === "resume" ? stop : undefined;
    const streamState = useMemo(() => {
        if (running || (stream.text && (stream.taskId === taskDetail?.id || !taskDetail))) return resumedStyleBibleStream(parseStyleBibleStream(stream.text), resumeView);
        return taskDetail ? styleBibleStreamFromResult(taskDetail.resultJson) : parseStyleBibleStream("");
    }, [resumeView, running, stream, taskDetail]);

    const defaultImageModel = () => turnaroundImageModel(effectiveConfig, detail.project.defaultImageModel);
    const openDialog = () => {
        // 侧栏（AppDrawer）和对话框同一层级、后渲染的在上面：先收起侧栏，免得盖住「定风格」。
        setPanelOpen(false);
        setPrecheckError("");
        setSettings((current) => ({
            sourceIds: candidates.map((unit) => unit.id),
            textModel: current.textModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "",
            images: current.images,
            imageModel: current.imageModel || defaultImageModel(),
        }));
        setOpen(true);
    };

    const refresh = async () => {
        await Promise.all([queryClient.invalidateQueries({ queryKey: ["project", projectId] }), queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] })]);
    };

    const start = async (options: { resumeTaskId?: string } = {}) => {
        const previous = options.resumeTaskId ? previousStyleRun(taskDetail) : undefined;
        if (detail.project.status === "archived") return message.warning("项目已归档，请先在项目设置中恢复");
        const textModel = settings.textModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const config = { ...effectiveConfig, model: textModel, textModel };
        if (!isAiConfigReady(config, textModel)) {
            navigateToSettings({ continueCreation: true });
            return;
        }
        const images = previous ? previous.images : settings.images;
        const imageModel = settings.imageModel || defaultImageModel();
        const imageConfig = { ...effectiveConfig, model: imageModel };
        const sourceUnitIds = previous?.sourceUnitIds || (settings.sourceIds.length === candidates.length ? [] : settings.sourceIds);
        // 出图模板在开跑前组好：组不出来（例如个人图片模型没选请求协议）时不建任务、不打开侧栏，原因留在对话框里。
        const precheckFailed = (reason: string) => {
            if (options.resumeTaskId) {
                message.error(`没有继续导演风格：${reason}`);
                return;
            }
            setPrecheckError(reason);
        };
        let paletteImages: Awaited<ReturnType<typeof paletteImageTaskTemplate>> | undefined;
        if (images) {
            if (!isAiConfigReady(imageConfig, imageModel)) return precheckFailed("色卡出图用的图片模型还没配好：到设置里配置，或关掉色卡出图。");
            setPrecheckError("");
            setPreparing(true);
            try {
                paletteImages = await paletteImageTaskTemplate(imageConfig, imageModel);
            } catch (error) {
                return precheckFailed(`色卡出图模板准备失败：${error instanceof Error ? error.message.replace(/[。.]+$/, "") : "请检查图片模型配置"}。可以换个图片模型，或关掉色卡出图。`);
            } finally {
                setPreparing(false);
            }
        }
        setPrecheckError("");
        setOpen(false);
        setStarting(true);
        setStream({ taskId: "", text: "" });
        setResumeView(options.resumeTaskId && resume ? { from: resume.from, total: resume.total, previous: styleBibleStreamFromResult(taskDetail?.resultJson) } : undefined);
        setPanelOpen(true);
        try {
            const result = await runStyleBible(
                { projectId, config, sourceUnitIds, paletteImages, resumeTaskId: options.resumeTaskId },
                {
                    onTaskUpdate: (task) => {
                        followedRef.current.add(task.id);
                        setStream((current) => ({ taskId: task.id, text: current.taskId && current.taskId !== task.id ? "" : current.text }));
                    },
                    onStreamText: (text) => setStream((current) => ({ ...current, text })),
                },
            );
            await refresh();
            if (result.outcome) message.success(`已写入风格锚和 ${result.outcome.scenes} 场色彩脚本，技能：${result.usage.skill.name} ${result.usage.skill.version}`);
        } catch (error) {
            await refresh();
            message.error(error instanceof Error ? `导演风格失败：${error.message}` : "导演风格失败");
        } finally {
            setStarting(false);
        }
    };

    // 接上正在跑的导演风格：刷新页面后打开侧栏时，或剧本优化跑完自动接着建的任务（followChainedStyleBible）。
    const follow = (task: GenerationTask) => {
        followedRef.current.add(task.id);
        setResumeView(undefined);
        setStream({ taskId: task.id, text: "" });
        void followDramaRunTask(task, { onStreamText: (text) => setStream({ taskId: task.id, text }) })
            .then(() => refresh())
            .catch(() => void queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] }));
    };
    useEffect(() => {
        const task = latestTask;
        if (!panelOpen || !task || (task.status !== "queued" && task.status !== "running") || followedRef.current.has(task.id)) return;
        follow(task);
    }, [latestTask, panelOpen]);
    useEffect(() => {
        const followChained = (taskId: string) => {
            if (!taskId || followedRef.current.has(taskId)) return;
            setPanelOpen(true);
            void queryGenerationTask(taskId)
                .then((task) => {
                    follow(task);
                    void queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] });
                })
                .catch(() => message.warning("没读到接着跑的导演风格任务，稍后在「风格进度」里看"));
        };
        followChained(takeChainedStyleBible(projectId));
        const onChained = (event: Event) => {
            const detail = (event as CustomEvent<{ projectId: string; taskId: string }>).detail;
            if (detail?.projectId === projectId) followChained(detail.taskId);
        };
        window.addEventListener(CHAINED_STYLE_EVENT, onChained);
        return () => window.removeEventListener(CHAINED_STYLE_EVENT, onChained);
    }, [projectId]);

    const usage = usageQuery.data;
    const toggle = (id: string, checked: boolean) =>
        setSettings((current) => ({ ...current, sourceIds: checked ? candidates.filter((unit) => unit.id === id || current.sourceIds.includes(unit.id)).map((unit) => unit.id) : current.sourceIds.filter((item) => item !== id) }));
    const warnings = (outcome?.items || []).filter((item) => item.level !== "error");
    const status = running ? (
        <>
            <span className="font-medium text-foreground/80">{streamState.total ? `正在写第 ${Math.max(1, streamState.current)}/${streamState.total} 集` : "等待模型开始输出"}</span>
            <span>·</span>
            <span>已出 {streamState.sceneCount} 场</span>
            {latestTask?.stage && (latestTask.status === "queued" || latestTask.status === "running") ? (
                <>
                    <span>·</span>
                    <span>{latestTask.stage}</span>
                </>
            ) : null}
        </>
    ) : outcome ? (
        <span>
            已写入风格锚和 {outcome.scenes} 场色彩脚本，色卡新建 {outcome.palettes.created} 张、加版本 {outcome.palettes.versioned} 张{outcome.palettes.retired ? `，${outcome.palettes.retired} 张旧色卡的场次已不存在（已标出，没删）` : ""}
        </span>
    ) : stop?.kind === "failed" ? (
        <span>{stop.status === "cancelled" ? "上一次导演风格已取消" : "上一次导演风格失败"}</span>
    ) : (
        <span>还没有导演风格记录</span>
    );

    return (
        <>
            <Button size="small" icon={<Palette className="size-3.5" />} disabled={disabled || running || !candidates.length} loading={running} onClick={openDialog} aria-label="导演风格">
                {running ? "导演风格中" : "导演风格"}
            </Button>
            {running || latestTask ? (
                <Button size="small" onClick={() => setPanelOpen(true)} aria-label="查看导演风格进度">
                    {resume && !running ? `风格停在第 ${resume.from} 集` : "风格进度"}
                </Button>
            ) : null}
            <Modal
                className="library-modal"
                title="导演风格"
                open={open}
                width={560}
                okText="定风格"
                cancelText="取消"
                okButtonProps={{ disabled: !settings.textModel || !settings.sourceIds.length || !usage || (settings.images && !settings.imageModel), loading: preparing }}
                onCancel={() => setOpen(false)}
                onOk={() => void start()}
                styles={{ body: { paddingTop: 12 } }}
            >
                <div className="grid gap-4">
                    {fromChapters ? (
                        <Callout tone="info" title="项目还没有剧集">
                            按原文章节定风格，一章当一集；做完剧本优化后可以再按剧集重跑，色卡按场加版本。
                        </Callout>
                    ) : null}
                    <div>
                        <div className="mb-1.5 flex items-center justify-between text-xs">
                            <span className="font-medium text-foreground/68">{fromChapters ? "要定风格的原文章节" : "要定风格的剧集"}</span>
                            <span className="tabular-nums text-foreground/40">
                                已选 {settings.sourceIds.length} / {candidates.length}
                            </span>
                        </div>
                        <div className="thin-scrollbar max-h-52 overflow-y-auto rounded-lg border border-border/70 px-2.5 py-1.5">
                            {candidates.map((unit, index) => (
                                <label key={unit.id} className="flex items-center gap-2 py-1 text-xs">
                                    <Checkbox checked={settings.sourceIds.includes(unit.id)} onChange={(event) => toggle(unit.id, event.target.checked)} />
                                    <span className="w-8 shrink-0 tabular-nums text-foreground/35">{String(index + 1).padStart(2, "0")}</span>
                                    <span className="min-w-0 flex-1 truncate text-foreground/80">{unit.title}</span>
                                </label>
                            ))}
                        </div>
                        <p className="mt-1.5 text-[var(--fs-tiny)] leading-[18px] text-foreground/45">没选的集保留已有的色彩脚本，可以被这次的场承接。</p>
                    </div>
                    <label className="block">
                        <span className="mb-1.5 block text-xs font-medium text-foreground/68">文本模型</span>
                        <ModelPicker
                            config={effectiveConfig}
                            capability="text"
                            value={settings.textModel}
                            onChange={(textModel) => setSettings((current) => ({ ...current, textModel }))}
                            variant="creation"
                            fullWidth
                            placeholder="选择用于导演风格的文本模型"
                            showSelectedPrice={false}
                            onMissingConfig={() => navigateToSettings({ continueCreation: true })}
                        />
                    </label>
                    <div className="grid gap-2">
                        <label className="flex items-center justify-between gap-3 text-xs">
                            <span className="font-medium text-foreground/68">写完后给每场色卡出图（16:9）</span>
                            <Switch
                                size="small"
                                checked={settings.images}
                                onChange={(images) => {
                                    setPrecheckError("");
                                    setSettings((current) => ({ ...current, images }));
                                }}
                                aria-label="给色卡出图"
                            />
                        </label>
                        {settings.images ? (
                            <ModelPicker
                                config={effectiveConfig}
                                capability="image"
                                value={settings.imageModel}
                                onChange={(imageModel) => {
                                    setPrecheckError("");
                                    setSettings((current) => ({ ...current, imageModel }));
                                }}
                                fullWidth
                                placeholder="选择色卡出图用的图片模型"
                                showSelectedPrice
                                onMissingConfig={() => navigateToSettings({ continueCreation: true })}
                            />
                        ) : null}
                    </div>
                    {precheckError ? (
                        <Callout tone="error" title="还没开始：色卡出图准备没通过">
                            {precheckError}
                        </Callout>
                    ) : null}
                    {usageQuery.isError ? (
                        <Callout tone="error" title="读不到本步使用的技能">
                            {usageQuery.error instanceof Error ? usageQuery.error.message : "请稍后重试"}
                        </Callout>
                    ) : (
                        <div className="rounded-lg border border-border/70 px-3 py-2.5 text-xs leading-5 text-foreground/62" aria-live="polite">
                            {usage ? (
                                <>
                                    <div>
                                        本步使用：<span className="font-medium text-foreground/85">{usage.skill.name}</span> {usage.skill.version}（{usage.profile.label}）
                                    </div>
                                    <div className="mt-1 text-[var(--fs-tiny)] text-foreground/42">技能由项目的风格档案决定。风格锚和色彩脚本写进连戏表，每场一张色卡（重跑同一场加版本）。</div>
                                </>
                            ) : (
                                "正在读取本步使用的技能…"
                            )}
                        </div>
                    )}
                </div>
            </Modal>
            <DramaRunPanel
                open={panelOpen}
                onClose={() => setPanelOpen(false)}
                title="导演风格"
                running={running}
                status={status}
                unit="集"
                stepLabel="导演风格"
                stop={running ? undefined : stop}
                onResume={() => {
                    if (resume) void start({ resumeTaskId: resume.taskId });
                }}
                onRegenerate={() => {
                    setPanelOpen(false);
                    openDialog();
                }}
                failedNote="连戏表和已有色卡都没有改动。"
                followKey={streamState.sceneCount + (streamState.anchor ? 1 : 0)}
                emptyText="风格锚和每场的色彩脚本会按集出现在这里"
                listLabel="流式色彩脚本"
                groups={StyleBibleRunGroups(streamState)}
            >
                {outcome && !running ? (
                    <Callout
                        tone="success"
                        title="风格锚和色彩脚本已写入"
                        action={
                            <Button size="small" type="primary" onClick={() => navigate(`/projects/${projectId}/assets?category=palette`)}>
                                看色卡
                            </Button>
                        }
                    >
                        {imagesText(outcome, imageStatuses)}
                        {warnings.length ? (
                            <ul className="mt-2 max-h-48 space-y-1 overflow-y-auto pl-0" aria-label="导演风格检查提醒">
                                {warnings.map((item, index) => (
                                    <li key={`${item.rule}-${index}`} className="list-none rounded-md bg-surface-active px-2.5 py-1.5">
                                        <span className="font-medium text-foreground/85">{item.shotId || "全部"}</span>
                                        <span className="ml-2 font-mono text-[var(--fs-micro)] text-foreground/50">{item.rule}</span>
                                        <div>{item.message}</div>
                                    </li>
                                ))}
                            </ul>
                        ) : null}
                    </Callout>
                ) : null}
            </DramaRunPanel>
        </>
    );
}
