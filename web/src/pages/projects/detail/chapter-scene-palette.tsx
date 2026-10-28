import { App, Button, Checkbox, Modal, Switch, Tooltip } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Palette } from "lucide-react";
import { useNavigate } from "react-router";

import { ModelPicker } from "@/components/model-picker";
import { Callout } from "@/components/ui/product/callout";
import { navigateToSettings } from "@/lib/settings-navigation";
import { dramaRunProgressOf, dramaSceneCode, dramaScenePaletteOutcomeOf, getDramaStepUsage, type DramaScenePaletteOutcome } from "@/services/api/drama";
import type { ProjectDetail, ProjectUnit } from "@/services/api/projects";
import { queryGenerationTask, type GenerationTask } from "@/services/api/task-center";
import { useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";

import { followChainedAssetsExtract } from "./chapter-assets-extract";
import { storyboardRunStop } from "./chapter-shot-stream";
import { projectLockedStyle } from "./chapter-style-lock";
import { assetImageTaskTemplate } from "./drama-image-task";
import { DramaRunPanel } from "./drama-run-panel";
import { followDramaRunTask, isScenePaletteTask, runScenePalette } from "./project-chapter-ai";
import { turnaroundImageModel } from "./project-character-media";
import { parseScenePaletteStream, resumedScenePaletteStream, scenePaletteStreamFromResult, type ScenePaletteStreamState } from "./scene-palette-stream";

type ScenePaletteControlProps = {
    detail: ProjectDetail;
    chapters: ProjectUnit[];
    episodes: ProjectUnit[];
    tasks?: GenerationTask[];
    disabled?: boolean;
};

const CHAINED_PALETTE_KEY = "drama-scene-palette-follow:";
const CHAINED_PALETTE_EVENT = "drama-scene-palette-follow";

/**
 * 剧本优化跑完服务端自动接着建了场次色卡任务：交给「生成场次色卡」按钮打开侧栏接上它的流。写完剧集会切到剧集的路由，章节页会重新挂载，
 * 所以先记进 sessionStorage（新挂载的按钮读它），同时发事件（没重新挂载时直接接上）。
 */
export function followChainedScenePalette(projectId: string, taskId: string) {
    try {
        sessionStorage.setItem(CHAINED_PALETTE_KEY + projectId, JSON.stringify({ taskId, at: Date.now() }));
    } catch {
        // 存不了（隐私模式）时只靠事件。
    }
    window.dispatchEvent(new CustomEvent(CHAINED_PALETTE_EVENT, { detail: { projectId, taskId } }));
}

/** 挂载时取走记下的任务；一分钟以前记的不再接（用户早就离开过章节页）。 */
function takeChainedScenePalette(projectId: string) {
    try {
        const raw = sessionStorage.getItem(CHAINED_PALETTE_KEY + projectId);
        sessionStorage.removeItem(CHAINED_PALETTE_KEY + projectId);
        const saved = raw ? (JSON.parse(raw) as { taskId?: string; at?: number }) : undefined;
        return saved?.taskId && Date.now() - (saved.at || 0) < 60_000 ? saved.taskId : "";
    } catch {
        return "";
    }
}

/** chainAssets：写完后按集依次接着提取资产；chainImages / imageModel：提取后给新资产出图用的图片模型。 */
type Settings = { sourceIds: string[]; textModel: string; chainAssets: boolean; chainImages: boolean; imageModel: string };

/** 「从第 N 集继续」要和上一次同一批剧集（服务端核对轮次）：从上一次任务的 metadata.dramaScenePalette 取。 */
function previousPaletteRun(task?: GenerationTask) {
    try {
        const run = (JSON.parse(task?.inputJson || "{}") as { metadata?: { dramaScenePalette?: { episodes?: Array<{ unitId?: string }> } } }).metadata?.dramaScenePalette;
        const ids = (run?.episodes || []).map((item) => item.unitId || "").filter(Boolean);
        return ids.length ? { sourceUnitIds: ids } : undefined;
    } catch {
        return undefined;
    }
}

/** 场编号 → 场次头：模型只写场编号，场次头取任务 metadata.dramaScenePalette 里建任务时的场次表。 */
function sceneHeadings(task?: GenerationTask) {
    const out = new Map<string, string>();
    try {
        const run = (JSON.parse(task?.inputJson || "{}") as { metadata?: { dramaScenePalette?: { episodes?: Array<{ no?: number; scenes?: string[] }> } } }).metadata?.dramaScenePalette;
        for (const episode of run?.episodes || []) (episode.scenes || []).forEach((heading, index) => out.set(dramaSceneCode(episode.no || 0, index + 1), heading));
    } catch {
        // 读不出时只显示场编号。
    }
    return out;
}

function ScenePaletteRunGroups(stream: ScenePaletteStreamState, headings: Map<string, string>) {
    return stream.episodes.map((episode) => ({
        key: episode.index,
        title: `第 ${episode.episode || episode.index} 集`,
        heading: episode.title || undefined,
        meta: `${episode.scenes.length} 场${episode.attempt > 1 ? ` · 第 ${episode.attempt} 次` : ""}`,
        body: (
            <ul className="space-y-1.5 rounded-md border border-border/60 px-2.5 py-1.5 pl-2.5 text-xs leading-5">
                {episode.scenes.map((scene) => {
                    const colors = [...scene.shared_colors, ...scene.new_colors];
                    const code = dramaSceneCode(scene.episode, scene.scene_no);
                    return (
                        <li key={`${scene.episode}-${scene.scene_no}`} className="list-none">
                            <div className="flex items-center gap-2">
                                <span className="font-mono text-[var(--fs-micro)] text-foreground/45">{code}</span>
                                <span className="min-w-0 flex-1 truncate font-medium text-foreground/80">{scene.scene || headings.get(code) || `${colors.length} 色`}</span>
                                {scene.inherits_from ? <span className="shrink-0 text-[var(--fs-micro)] text-foreground/45">承接 {scene.inherits_from}</span> : null}
                            </div>
                            <div className="mt-1 flex gap-0.5" aria-label={`${code} 的 ${colors.length} 个颜色`}>
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
    }));
}

/**
 * 章节页的「生成场次色卡」（项目级步骤 scene.palette）：对话框选范围（默认全部剧集，没有剧集时是原文章节）和文本模型，技能由项目风格档案的
 * 绑定决定。项目要先锁定风格（风格锚 + 主基调色卡），没锁定时按钮禁用并提示。色卡图由代码合成，不选图片模型。进度侧栏按集流式显示每场的色块；
 * 剧本优化跑完服务端自动接着建的任务也在这里接上（followChainedScenePalette）。
 */
export function ScenePaletteControl({ detail, chapters, episodes, tasks, disabled }: ScenePaletteControlProps) {
    const { message } = App.useApp();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const projectId = detail.project.id;
    const effectiveConfig = useEffectiveConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const candidates = episodes.length ? episodes : chapters;
    const fromChapters = !episodes.length;
    const locked = useMemo(() => projectLockedStyle(detail.project.bibleJson).locked, [detail.project.bibleJson]);
    const [open, setOpen] = useState(false);
    const [panelOpen, setPanelOpen] = useState(false);
    const [starting, setStarting] = useState(false);
    const [stream, setStream] = useState<{ taskId: string; text: string }>({ taskId: "", text: "" });
    const [resumeView, setResumeView] = useState<{ from: number; total: number; previous: ScenePaletteStreamState }>();
    const followedRef = useRef(new Set<string>());
    const [settings, setSettings] = useState<Settings>({ sourceIds: [], textModel: "", chainAssets: true, chainImages: true, imageModel: "" });
    const [preparing, setPreparing] = useState(false);
    const [precheckError, setPrecheckError] = useState("");
    const usageQuery = useQuery({
        queryKey: ["project", projectId, "drama-step", "scene.palette", detail.project.styleProfile || ""],
        queryFn: ({ signal }) => getDramaStepUsage(projectId, "scene.palette", signal).then((result) => result.usage),
        enabled: open,
    });
    const latestTask = useMemo(() => (tasks || []).find(isScenePaletteTask), [tasks]);
    const running = starting || Boolean(latestTask && (latestTask.status === "queued" || latestTask.status === "running"));
    const terminal = latestTask && latestTask.status !== "queued" && latestTask.status !== "running" ? latestTask : undefined;
    const detailQuery = useQuery({ queryKey: ["generation-task", terminal?.id, terminal?.status], queryFn: () => queryGenerationTask(terminal!.id), enabled: Boolean(terminal), staleTime: Infinity });
    const taskDetail = detailQuery.data;
    const outcome = taskDetail?.status === "succeeded" ? dramaScenePaletteOutcomeOf(taskDetail) : undefined;
    const stop = useMemo(() => (taskDetail ? storyboardRunStop(taskDetail, dramaRunProgressOf(taskDetail)) : undefined), [taskDetail]);
    const resume = stop?.kind === "resume" ? stop : undefined;
    const headings = useMemo(() => sceneHeadings(taskDetail || latestTask), [latestTask, taskDetail]);
    const streamState = useMemo(() => {
        if (running || (stream.text && (stream.taskId === taskDetail?.id || !taskDetail))) return resumedScenePaletteStream(parseScenePaletteStream(stream.text), resumeView);
        return taskDetail ? scenePaletteStreamFromResult(taskDetail.resultJson) : parseScenePaletteStream("");
    }, [resumeView, running, stream, taskDetail]);

    const openDialog = () => {
        // 侧栏（AppDrawer）和对话框同一层级、后渲染的在上面：先收起侧栏，免得盖住对话框。
        setPanelOpen(false);
        setPrecheckError("");
        setSettings((current) => ({
            ...current,
            sourceIds: candidates.map((unit) => unit.id),
            textModel: current.textModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "",
            imageModel: current.imageModel || turnaroundImageModel(effectiveConfig, detail.project.defaultImageModel),
        }));
        setOpen(true);
    };

    // 写完后服务端按集依次接着提取资产（第一集的任务在色卡结果的 chained 里）：交给「提取资产」按钮接上它的流。
    const followChained = (outcome?: DramaScenePaletteOutcome) => {
        const chained = outcome?.chained;
        if (chained?.taskId) {
            message.info(chained.remaining ? `接着提取资产：先提取第 1 集，后面还有 ${chained.remaining} 集依次接着提取` : "接着提取资产");
            followChainedAssetsExtract(projectId, chained.taskId, chained.unitId);
        } else if (chained?.error) {
            message.warning(`没有接着提取资产：${chained.error}`);
        }
    };

    const refresh = async () => {
        await Promise.all([queryClient.invalidateQueries({ queryKey: ["project", projectId] }), queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] })]);
    };

    const start = async (options: { resumeTaskId?: string } = {}) => {
        const previous = options.resumeTaskId ? previousPaletteRun(taskDetail) : undefined;
        if (detail.project.status === "archived") return message.warning("项目已归档，请先在项目设置中恢复");
        const textModel = settings.textModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const config = { ...effectiveConfig, model: textModel, textModel };
        if (!isAiConfigReady(config, textModel)) {
            navigateToSettings({ continueCreation: true });
            return;
        }
        const sourceUnitIds = previous?.sourceUnitIds || (settings.sourceIds.length === candidates.length ? [] : settings.sourceIds);
        // 接着提取资产要出图时，出图模板在开跑前组好：组不出来时不建任务，原因留在对话框里（续跑时直接提示）。
        let assetImageTask: Awaited<ReturnType<typeof assetImageTaskTemplate>> | undefined;
        if (settings.chainAssets && settings.chainImages) {
            const imageModel = settings.imageModel || turnaroundImageModel(effectiveConfig, detail.project.defaultImageModel);
            const imageConfig = { ...effectiveConfig, model: imageModel };
            const fail = (reason: string) => (options.resumeTaskId ? void message.error(`没有继续场次色卡：${reason}`) : setPrecheckError(reason));
            if (!imageModel || !isAiConfigReady(imageConfig, imageModel)) return fail("资产出图用的图片模型还没配好：到设置里配置，或关掉「提取后出图」。");
            setPreparing(true);
            try {
                assetImageTask = await assetImageTaskTemplate(imageConfig, imageModel);
            } catch (error) {
                return fail(`资产出图模板准备失败：${error instanceof Error ? error.message.replace(/[。.]+$/, "") : "请检查图片模型配置"}。可以换个图片模型，或关掉「提取后出图」。`);
            } finally {
                setPreparing(false);
            }
        }
        setPrecheckError("");
        setOpen(false);
        setStarting(true);
        setStream({ taskId: "", text: "" });
        setResumeView(options.resumeTaskId && resume ? { from: resume.from, total: resume.total, previous: scenePaletteStreamFromResult(taskDetail?.resultJson) } : undefined);
        setPanelOpen(true);
        try {
            const result = await runScenePalette(
                { projectId, config, sourceUnitIds, resumeTaskId: options.resumeTaskId, chainAssets: settings.chainAssets, assetImageTask },
                {
                    onTaskUpdate: (task) => {
                        followedRef.current.add(task.id);
                        setStream((current) => ({ taskId: task.id, text: current.taskId && current.taskId !== task.id ? "" : current.text }));
                    },
                    onStreamText: (text) => setStream((current) => ({ ...current, text })),
                },
            );
            await refresh();
            if (result.outcome) message.success(`已生成 ${result.outcome.scenes} 场场次色卡，技能：${result.usage.skill.name} ${result.usage.skill.version}`);
            followChained(result.outcome);
        } catch (error) {
            await refresh();
            message.error(error instanceof Error ? `场次色卡失败：${error.message}` : "场次色卡失败");
        } finally {
            setStarting(false);
        }
    };

    // 接上正在跑的场次色卡：刷新页面后打开侧栏时，或剧本优化跑完服务端自动接着建的任务（followChainedScenePalette）。
    const follow = (task: GenerationTask) => {
        followedRef.current.add(task.id);
        setResumeView(undefined);
        setStream({ taskId: task.id, text: "" });
        void followDramaRunTask(task, { onStreamText: (text) => setStream({ taskId: task.id, text }) })
            .then((completed) => {
                followChained(dramaScenePaletteOutcomeOf(completed));
                return refresh();
            })
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
                .catch(() => message.warning("没读到接着跑的场次色卡任务，稍后在「色卡进度」里看"));
        };
        followChained(takeChainedScenePalette(projectId));
        const onChained = (event: Event) => {
            const detail = (event as CustomEvent<{ projectId: string; taskId: string }>).detail;
            if (detail?.projectId === projectId) followChained(detail.taskId);
        };
        window.addEventListener(CHAINED_PALETTE_EVENT, onChained);
        return () => window.removeEventListener(CHAINED_PALETTE_EVENT, onChained);
    }, [projectId]);

    const usage = usageQuery.data;
    const toggle = (id: string, checked: boolean) =>
        setSettings((current) => ({ ...current, sourceIds: checked ? candidates.filter((unit) => unit.id === id || current.sourceIds.includes(unit.id)).map((unit) => unit.id) : current.sourceIds.filter((item) => item !== id) }));
    const warnings = (outcome?.items || []).filter((item) => item.level !== "error");
    const failures = outcome?.imageFailures || [];
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
            已生成 {outcome.scenes} 场场次色卡：新建 {outcome.palettes.created} 张、加版本 {outcome.palettes.versioned} 张{outcome.palettes.retired ? `，${outcome.palettes.retired} 张旧色卡的场次已不存在（已标出，没删）` : ""}
        </span>
    ) : stop?.kind === "failed" ? (
        <span>{stop.status === "cancelled" ? "上一次场次色卡已取消" : "上一次场次色卡失败"}</span>
    ) : (
        <span>还没有场次色卡记录</span>
    );
    const lockHint = "项目还没有锁定风格：场次色卡以锁定的风格锚和主基调色卡为参考，先点「分析项目风格」锁定风格";

    return (
        <>
            <Tooltip title={!locked ? lockHint : undefined}>
                <Button size="small" icon={<Palette className="size-3.5" />} disabled={disabled || running || !candidates.length || !locked} loading={running} onClick={openDialog} aria-label={locked ? "生成场次色卡" : `生成场次色卡（${lockHint}）`}>
                    {running ? "场次色卡生成中" : "生成场次色卡"}
                </Button>
            </Tooltip>
            {running || latestTask ? (
                <Button size="small" onClick={() => setPanelOpen(true)} aria-label="查看场次色卡进度">
                    {resume && !running ? `色卡停在第 ${resume.from} 集` : "色卡进度"}
                </Button>
            ) : null}
            <Modal
                className="library-modal"
                title="生成场次色卡"
                open={open}
                width={560}
                okText="生成"
                cancelText="取消"
                okButtonProps={{ disabled: !settings.textModel || !settings.sourceIds.length || !usage || (settings.chainAssets && settings.chainImages && !settings.imageModel), loading: preparing }}
                onCancel={() => setOpen(false)}
                onOk={() => void start()}
                styles={{ body: { paddingTop: 12 } }}
            >
                <div className="grid gap-4">
                    {fromChapters ? (
                        <Callout tone="info" title="项目还没有剧集">
                            按原文章节做色卡，一章当一集；做完剧本优化后会按剧集自动重做，色卡按场加版本。
                        </Callout>
                    ) : null}
                    <div>
                        <div className="mb-1.5 flex items-center justify-between text-xs">
                            <span className="font-medium text-foreground/68">{fromChapters ? "要做色卡的原文章节" : "要做色卡的剧集"}</span>
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
                        <p className="mt-1.5 text-[var(--fs-tiny)] leading-[18px] text-foreground/45">没选的集保留已有的场次色卡，可以被这次的场承接。</p>
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
                            placeholder="选择用于场次色卡的文本模型"
                            showSelectedPrice={false}
                            onMissingConfig={() => navigateToSettings({ continueCreation: true })}
                        />
                    </label>
                    <div className="grid gap-2 rounded-lg border border-border/70 px-3 py-2.5">
                        <label className="flex items-center justify-between gap-3 text-xs">
                            <span className="font-medium text-foreground/68">写完后按集依次提取资产（角色、场景、道具）</span>
                            <Switch
                                size="small"
                                checked={settings.chainAssets}
                                onChange={(chainAssets) => {
                                    setPrecheckError("");
                                    setSettings((current) => ({ ...current, chainAssets }));
                                }}
                                aria-label="写完后接着提取资产"
                            />
                        </label>
                        <p className="text-[var(--fs-tiny)] leading-[18px] text-foreground/45">资产按色卡约束：角色、道具用主基调色卡，场景用本场色卡和主基调色卡；角色换装或换状态时自动拆子卡。一集提完再提下一集。</p>
                        {settings.chainAssets ? (
                            <>
                                <label className="flex items-center justify-between gap-3 text-xs">
                                    <span className="font-medium text-foreground/68">提取后给新资产出图（带色卡图作参考）</span>
                                    <Switch
                                        size="small"
                                        checked={settings.chainImages}
                                        onChange={(chainImages) => {
                                            setPrecheckError("");
                                            setSettings((current) => ({ ...current, chainImages }));
                                        }}
                                        aria-label="提取后给新资产出图"
                                    />
                                </label>
                                {settings.chainImages ? (
                                    <ModelPicker
                                        config={effectiveConfig}
                                        capability="image"
                                        value={settings.imageModel}
                                        onChange={(imageModel) => {
                                            setPrecheckError("");
                                            setSettings((current) => ({ ...current, imageModel }));
                                        }}
                                        fullWidth
                                        placeholder="选择资产出图用的图片模型"
                                        showSelectedPrice
                                        onMissingConfig={() => navigateToSettings({ continueCreation: true })}
                                    />
                                ) : null}
                            </>
                        ) : null}
                    </div>
                    {precheckError ? (
                        <Callout tone="error" title="还没开始：资产出图准备没通过">
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
                                    <div className="mt-1 text-[var(--fs-tiny)] text-foreground/42">以锁定的风格锚和主基调色卡为参考，每场一张色卡；色卡图由代码按色值合成，不调图片模型（重跑同一场加版本）。</div>
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
                title="场次色卡"
                running={running}
                status={status}
                unit="集"
                stepLabel="场次色卡"
                stop={running ? undefined : stop}
                onResume={() => {
                    if (resume) void start({ resumeTaskId: resume.taskId });
                }}
                onRegenerate={() => {
                    setPanelOpen(false);
                    openDialog();
                }}
                failedNote="连戏表和已有色卡都没有改动。"
                followKey={streamState.sceneCount}
                emptyText="每场的色彩脚本会按集出现在这里"
                listLabel="流式色彩脚本"
                groups={ScenePaletteRunGroups(streamState, headings)}
            >
                {outcome && !running ? (
                    <Callout
                        tone={failures.length ? "warning" : "success"}
                        title={failures.length ? `场次色卡已写入，${failures.length} 张图没合成上` : "场次色卡已生成"}
                        action={
                            <Button size="small" type="primary" onClick={() => navigate(`/projects/${projectId}/assets?category=palette`)}>
                                看色卡
                            </Button>
                        }
                    >
                        {failures.length ? `没合成上的：${failures.map((item) => `${item.code}（${item.error}）`).join("、")}。到资产页点「重新合成」补图。` : `${outcome.rendered} 张色卡图已由代码合成，生成分段时绑到这一场的镜头。`}
                        {outcome.chained?.taskId
                            ? `已接着提取资产${outcome.chained.remaining ? `（共 ${outcome.chained.remaining + 1} 集，一集提完再提下一集）` : ""}，进度在「资产进度」里。`
                            : outcome.chained?.error
                              ? `没有接着提取资产：${outcome.chained.error}`
                              : ""}
                        {warnings.length ? (
                            <ul className="mt-2 max-h-48 space-y-1 overflow-y-auto pl-0" aria-label="场次色卡检查提醒">
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
