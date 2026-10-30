import { App, Button, Checkbox, InputNumber, Modal, Radio, Switch } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ScrollText } from "lucide-react";

import { ModelPicker } from "@/components/model-picker";
import { Callout } from "@/components/ui/product/callout";
import { navigateToSettings } from "@/lib/settings-navigation";
import {
    dramaEpisodesKey,
    dramaEpisodesReplaceImpact,
    dramaRunProgressOf,
    dramaScriptAdaptOutcomeOf,
    getDramaBrief,
    getDramaEpisodes,
    getDramaStepUsage,
    type DramaEpisodeImpact,
    type DramaScriptAdaptMode,
    type DramaScriptAdaptOutcome,
} from "@/services/api/drama";
import type { ProjectDetail, ProjectUnit } from "@/services/api/projects";
import { queryGenerationTask, type GenerationTask } from "@/services/api/task-center";
import { useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";

import { storyboardRunStop } from "./chapter-shot-stream";
import { assetImageTaskTemplate } from "./drama-image-task";
import { briefEpisodeSeconds, dramaBriefKey, EPISODE_SECONDS_RANGE } from "./drama-brief";
import { followDramaRunTask, isScriptAdaptTask, runScriptAdapt } from "./project-chapter-ai";
import { turnaroundImageModel } from "./project-character-media";
import { scriptAdaptDefaultMode } from "./script-adapt-mode";
import { ScriptAdaptRunPanel } from "./script-adapt-run-panel";
import { parseScriptAdaptStream, resumedScriptAdaptStream, scriptAdaptStreamFromResult, type ScriptAdaptResume } from "./script-adapt-stream";

type ScriptAdaptControlProps = {
    detail: ProjectDetail;
    /** 原文章节、剧集（项目里的顺序）。 */
    chapters: ProjectUnit[];
    episodes: ProjectUnit[];
    /** 和章节页共用的任务列表（剧本优化任务没有 chapterId，按 metadata.source 认）。 */
    tasks?: GenerationTask[];
    disabled?: boolean;
    /** 写完以后切到剧集视图，选中第一集。 */
    onWritten: (outcome: DramaScriptAdaptOutcome) => void;
    /** 写完剧集后服务端按生成模式接着建了场次色卡任务：交给「生成场次色卡」按钮接上它的流。 */
    onChained?: (taskId: string) => void;
};

/** images / imageModel：一路接着跑的场次色卡 → 提取资产写完后给新资产出图（默认开）。 */
type Settings = { mode: DramaScriptAdaptMode; sourceIds: string[]; countBy: "episodes" | "seconds"; targetEpisodes: number; episodeSeconds: number; textModel: string; images: boolean; imageModel: string };

/** 「从第 N 集继续」要和上一次同一批来源、同一集数（服务端核对）：从上一次任务的 metadata.dramaAdapt 取。 */
function previousAdaptRun(task?: GenerationTask) {
    try {
        const adapt = (JSON.parse(task?.inputJson || "{}") as { metadata?: { dramaAdapt?: { mode?: string; sourceUnitIds?: string[]; episodes?: number; replaceUnitIds?: string[]; assetImages?: boolean } } }).metadata?.dramaAdapt;
        if (!adapt?.sourceUnitIds?.length) return undefined;
        return {
            mode: (adapt.mode === "rhythm" ? "rhythm" : "adapt") as DramaScriptAdaptMode,
            sourceUnitIds: adapt.sourceUnitIds,
            episodes: adapt.episodes || adapt.sourceUnitIds.length,
            replace: Boolean(adapt.replaceUnitIds?.length),
            images: Boolean(adapt.assetImages),
        };
    } catch {
        return undefined;
    }
}

/**
 * 章节页的「剧本优化」（项目级步骤 script.adapt）：对话框选原文章节（默认全部还没改编的）或已有剧集（只调节奏）、
 * 集数或每集秒数、文本模型，技能由项目风格档案的绑定决定；进度侧栏按集流式显示。会替换已有剧集时先列出影响面确认。
 */
export function ScriptAdaptControl({ detail, chapters, episodes, tasks, disabled, onWritten, onChained }: ScriptAdaptControlProps) {
    const { message, modal } = App.useApp();
    const queryClient = useQueryClient();
    const projectId = detail.project.id;
    const effectiveConfig = useEffectiveConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const [open, setOpen] = useState(false);
    const [panelOpen, setPanelOpen] = useState(false);
    const [starting, setStarting] = useState(false);
    const [preparing, setPreparing] = useState(false);
    const [precheckError, setPrecheckError] = useState("");
    const [stream, setStream] = useState<{ taskId: string; text: string }>({ taskId: "", text: "" });
    const followedRef = useRef(new Set<string>());
    // 续写时已写完的集和续写起点：侧栏一开始就显示「正在写第 N 集」，前面的集接在新输出前面。
    const [resumeView, setResumeView] = useState<ScriptAdaptResume>();
    const episodesQuery = useQuery({ queryKey: dramaEpisodesKey(projectId), queryFn: ({ signal }) => getDramaEpisodes(projectId, signal), enabled: open });
    const defaultMode = (adaptedChapterIds?: string[]) => scriptAdaptDefaultMode({ sourceType: detail.project.sourceType, chapterIds: chapters.map((unit) => unit.id), episodeCount: episodes.length, adaptedChapterIds });
    // 打开对话框时剧集清单还没读到：读到后按是否都改编过再定一次默认模式。
    const modePendingRef = useRef(false);
    const [settings, setSettings] = useState<Settings>(() => ({ mode: defaultMode(), sourceIds: [], countBy: "episodes", targetEpisodes: 1, episodeSeconds: 90, textModel: "", images: true, imageModel: "" }));
    const briefQuery = useQuery({ queryKey: dramaBriefKey(projectId), queryFn: async () => (await getDramaBrief(projectId)).document, enabled: open });
    const usageQuery = useQuery({
        queryKey: ["project", projectId, "drama-step", "script.adapt", detail.project.styleProfile || ""],
        queryFn: ({ signal }) => getDramaStepUsage(projectId, "script.adapt", signal).then((result) => result.usage),
        enabled: open,
    });
    const latestTask = useMemo(() => (tasks || []).find(isScriptAdaptTask), [tasks]);
    const running = starting || Boolean(latestTask && (latestTask.status === "queued" || latestTask.status === "running"));
    const terminal = latestTask && latestTask.status !== "queued" && latestTask.status !== "running" ? latestTask : undefined;
    const detailQuery = useQuery({ queryKey: ["generation-task", terminal?.id, terminal?.status], queryFn: () => queryGenerationTask(terminal!.id), enabled: Boolean(terminal), staleTime: Infinity });
    const taskDetail = detailQuery.data;
    const outcome = taskDetail?.status === "succeeded" ? dramaScriptAdaptOutcomeOf(taskDetail) : undefined;
    const stop = useMemo(() => (taskDetail ? storyboardRunStop(taskDetail, dramaRunProgressOf(taskDetail)) : undefined), [taskDetail]);
    const resume = stop?.kind === "resume" ? stop : undefined;
    const streamState = useMemo(() => {
        // 跑着的时候只看这次任务的输出（任务列表还没刷新时 taskDetail 是上一次的）。
        if (running || (stream.text && (stream.taskId === taskDetail?.id || !taskDetail))) return resumedScriptAdaptStream(parseScriptAdaptStream(stream.text), resumeView);
        return taskDetail ? scriptAdaptStreamFromResult(taskDetail.resultJson) : parseScriptAdaptStream("");
    }, [resumeView, running, stream, taskDetail]);
    const adapted = useMemo(() => new Set(episodesQuery.data?.adaptedChapterIds || []), [episodesQuery.data]);
    const candidates = settings.mode === "rhythm" ? episodes : chapters;

    // 打开对话框时按当前项目给默认值：没改编过的原文章节（全都改编过就不勾）、一章一集、每集时长取需求文档。
    const openDialog = () => {
        modePendingRef.current = !episodesQuery.data;
        setPrecheckError("");
        setSettings((current) => ({
            ...current,
            mode: defaultMode(episodesQuery.data?.adaptedChapterIds),
            textModel: current.textModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "",
            imageModel: current.imageModel || turnaroundImageModel(effectiveConfig, detail.project.defaultImageModel),
        }));
        setOpen(true);
    };
    useEffect(() => {
        if (!open || !episodesQuery.data) return;
        if (modePendingRef.current) {
            modePendingRef.current = false;
            const mode = defaultMode(episodesQuery.data.adaptedChapterIds);
            if (mode !== settings.mode) {
                setSettings((current) => ({ ...current, mode }));
                return;
            }
        }
        setSettings((current) => {
            const ids = current.mode === "rhythm" ? episodes.map((unit) => unit.id) : chapters.filter((unit) => !adapted.has(unit.id)).map((unit) => unit.id);
            return { ...current, sourceIds: ids, targetEpisodes: Math.max(1, ids.length) };
        });
    }, [open, episodesQuery.data, settings.mode]);
    useEffect(() => {
        const seconds = briefEpisodeSeconds(briefQuery.data);
        if (open && seconds) setSettings((current) => ({ ...current, episodeSeconds: seconds }));
    }, [open, briefQuery.data]);

    const refresh = async () => {
        await Promise.all([queryClient.invalidateQueries({ queryKey: ["project", projectId] }), queryClient.invalidateQueries({ queryKey: ["projects"] }), queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] })]);
    };

    const confirmReplace = (mode: DramaScriptAdaptMode, impacts: DramaEpisodeImpact[]) =>
        new Promise<boolean>((resolve) => {
            const withShots = impacts.filter((item) => item.shots || item.hasShotlist);
            modal.confirm({
                title: mode === "rhythm" ? `改写 ${impacts.length} 集剧集的正文？` : `替换已有的 ${impacts.length} 集剧集？`,
                content: (
                    <div className="space-y-2 text-sm leading-6 text-foreground/62">
                        <p>
                            {mode === "rhythm"
                                ? "只调节奏会原地改写这些剧集的正文，已有的镜头表和段不会自动更新，需要时在剧集上重新生成分段。"
                                : "所选章节已经改编过。新剧本写完后会整体替换下面这些剧集，连同它们的镜头表、段和生成产物；生成失败时现有剧集不受影响。"}
                        </p>
                        <ul className="max-h-48 space-y-1 overflow-y-auto pl-0">
                            {impacts.map((item) => (
                                <li key={item.unitId} className="list-none rounded-md bg-surface-active px-2.5 py-1.5">
                                    <span className="font-medium text-foreground/85">{item.title}</span>
                                    <span className="ml-2 text-[var(--fs-tiny)] text-foreground/50">
                                        {item.shots ? `${item.shots} 段` : "还没有段"}
                                        {item.hasShotlist ? " · 有镜头表" : ""}
                                    </span>
                                </li>
                            ))}
                        </ul>
                        {withShots.length ? <p>其中 {withShots.length} 集已有镜头表或段，此操作无法撤销。</p> : null}
                    </div>
                ),
                okText: mode === "rhythm" ? "改写" : "生成成功后替换",
                okButtonProps: { danger: true },
                cancelText: "取消",
                centered: true,
                onOk: () => resolve(true),
                onCancel: () => resolve(false),
            });
        });

    const start = async (options: { replace?: boolean; resumeTaskId?: string } = {}) => {
        const previous = options.resumeTaskId ? previousAdaptRun(taskDetail) : undefined;
        const mode = previous?.mode || settings.mode;
        const sourceUnitIds = previous?.sourceUnitIds || settings.sourceIds;
        if (detail.project.status === "archived") return message.warning("项目已归档，请先在项目设置中恢复");
        const textModel = settings.textModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const config = { ...effectiveConfig, model: textModel, textModel };
        if (!isAiConfigReady(config, textModel)) {
            navigateToSettings({ continueCreation: true });
            return;
        }
        if (!sourceUnitIds.length) return message.warning(mode === "rhythm" ? "请选择要调节奏的剧集" : "请选择要改编的原文章节");
        // 出图开着时出图模板在开跑前组好（续跑沿用上一次的开关）：组不出来时不建任务，原因留在对话框里（续跑时直接提示）。
        let assetImageTask: Awaited<ReturnType<typeof assetImageTaskTemplate>> | undefined;
        if (previous ? previous.images : settings.images) {
            const imageModel = settings.imageModel || turnaroundImageModel(effectiveConfig, detail.project.defaultImageModel);
            const imageConfig = { ...effectiveConfig, model: imageModel };
            const fail = (reason: string) => (options.resumeTaskId ? void message.error(`没有继续剧本优化：${reason}`) : setPrecheckError(reason));
            if (!imageModel || !isAiConfigReady(imageConfig, imageModel)) return fail("资产出图用的图片模型还没配好：到设置里配置，或关掉「出图」。");
            setPreparing(true);
            try {
                assetImageTask = await assetImageTaskTemplate(imageConfig, imageModel);
            } catch (error) {
                return fail(`资产出图模板准备失败：${error instanceof Error ? error.message.replace(/[。.]+$/, "") : "请检查图片模型配置"}。可以换个图片模型，或关掉「出图」。`);
            } finally {
                setPreparing(false);
            }
        }
        setPrecheckError("");
        setOpen(false);
        setStarting(true);
        setStream({ taskId: "", text: "" });
        setResumeView(options.resumeTaskId && resume ? { from: resume.from, total: resume.total, episodes: scriptAdaptStreamFromResult(taskDetail?.resultJson).episodes.filter((episode) => episode.index < resume.from) } : undefined);
        setPanelOpen(true);
        try {
            const result = await runScriptAdapt(
                {
                    projectId,
                    config,
                    mode,
                    sourceUnitIds,
                    ...(previous && mode === "adapt" ? { targetEpisodes: previous.episodes } : {}),
                    ...(!previous && mode === "adapt" && settings.countBy === "episodes" ? { targetEpisodes: settings.targetEpisodes } : {}),
                    ...(!previous && mode === "adapt" && settings.countBy === "seconds" ? { episodeSeconds: settings.episodeSeconds } : {}),
                    replace: options.replace || previous?.replace,
                    resumeTaskId: options.resumeTaskId,
                    assetImageTask,
                },
                {
                    onTaskUpdate: (task) => {
                        followedRef.current.add(task.id);
                        setStream((current) => ({ taskId: task.id, text: current.taskId && current.taskId !== task.id ? "" : current.text }));
                    },
                    onStreamText: (text) => setStream((current) => ({ ...current, text })),
                },
            );
            await refresh();
            if (result.outcome) {
                message.success(result.outcome.mode === "rhythm" ? `已改写 ${result.outcome.episodes.length} 集剧集` : `已写入 ${result.outcome.episodes.length} 集剧集，技能：${result.usage.skill.name} ${result.usage.skill.version}`);
                onWritten(result.outcome);
                const chained = result.outcome.chained;
                if (chained?.taskId && chained.step === "style.lock") {
                    message.info("接着让导演定画风：在「项目风格」里看进度，锁定后按生成模式接着做场次色卡");
                } else if (chained?.taskId) {
                    setPanelOpen(false);
                    onChained?.(chained.taskId);
                } else if (chained?.error) {
                    message.warning(`${chained.step === "style.lock" ? "导演定画风" : "场次色卡"}没有自动开始：${chained.error}`);
                }
            }
        } catch (error) {
            const impact = dramaEpisodesReplaceImpact(error);
            if (impact) {
                setStarting(false);
                setPanelOpen(false);
                if (await confirmReplace(impact.mode, impact.episodes)) await start({ ...options, replace: true });
                return;
            }
            await refresh();
            message.error(error instanceof Error ? `剧本优化失败：${error.message}` : "剧本优化失败");
        } finally {
            setStarting(false);
        }
    };

    // 刷新页面或别的标签页起的剧本优化还在跑：打开侧栏时接上它的文本流（从头回放），结束后刷新项目。
    useEffect(() => {
        const task = latestTask;
        if (!panelOpen || !task || (task.status !== "queued" && task.status !== "running") || followedRef.current.has(task.id)) return;
        followedRef.current.add(task.id);
        setStream({ taskId: task.id, text: "" });
        void followDramaRunTask(task, { onStreamText: (text) => setStream({ taskId: task.id, text }) })
            .then(() => refresh())
            .catch(() => void queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] }));
    }, [latestTask, panelOpen]);

    const toggleSource = (id: string, checked: boolean) =>
        setSettings((current) => {
            const ids = checked ? candidates.filter((unit) => unit.id === id || current.sourceIds.includes(unit.id)).map((unit) => unit.id) : current.sourceIds.filter((item) => item !== id);
            return { ...current, sourceIds: ids, targetEpisodes: current.mode === "adapt" ? Math.max(1, ids.length) : current.targetEpisodes };
        });
    const usage = usageQuery.data;
    const replaced = settings.mode === "adapt" ? settings.sourceIds.filter((id) => adapted.has(id)).length : 0;

    return (
        <>
            <Button size="small" icon={<ScrollText className="size-3.5" />} disabled={disabled || running || (!chapters.length && !episodes.length)} loading={running} onClick={openDialog} aria-label="剧本优化">
                {running ? "剧本优化中" : "剧本优化"}
            </Button>
            {running || latestTask ? (
                <Button size="small" onClick={() => setPanelOpen(true)} aria-label="查看剧本优化进度">
                    {resume && !running ? `剧本停在第 ${resume.from} 集` : "剧本进度"}
                </Button>
            ) : null}
            <Modal
                className="library-modal"
                title="剧本优化"
                open={open}
                width={580}
                okText={settings.mode === "rhythm" ? "调节奏" : "改编成剧本"}
                cancelText="取消"
                okButtonProps={{ disabled: !settings.textModel || !settings.sourceIds.length || !usage || (settings.images && !settings.imageModel), loading: preparing }}
                onCancel={() => setOpen(false)}
                onOk={() => void start()}
                styles={{ body: { paddingTop: 12 } }}
            >
                <div className="grid gap-4">
                    <Radio.Group
                        optionType="button"
                        size="small"
                        value={settings.mode}
                        onChange={(event) => setSettings((current) => ({ ...current, mode: event.target.value as DramaScriptAdaptMode }))}
                        options={[
                            { label: "小说转剧本", value: "adapt", disabled: !chapters.length },
                            { label: "只调节奏（已是剧本）", value: "rhythm", disabled: !episodes.length },
                        ]}
                    />
                    <div>
                        <div className="mb-1.5 flex items-center justify-between text-xs">
                            <span className="font-medium text-foreground/68">{settings.mode === "rhythm" ? "要调节奏的剧集" : "要改编的原文章节"}</span>
                            <span className="tabular-nums text-foreground/40">
                                已选 {settings.sourceIds.length} / {candidates.length}
                            </span>
                        </div>
                        <div className="thin-scrollbar max-h-52 overflow-y-auto rounded-lg border border-border/70 px-2.5 py-1.5">
                            {candidates.map((unit, index) => (
                                <label key={unit.id} className="flex items-center gap-2 py-1 text-xs">
                                    <Checkbox checked={settings.sourceIds.includes(unit.id)} onChange={(event) => toggleSource(unit.id, event.target.checked)} />
                                    <span className="w-8 shrink-0 tabular-nums text-foreground/35">{String(index + 1).padStart(2, "0")}</span>
                                    <span className="min-w-0 flex-1 truncate text-foreground/80">{unit.title}</span>
                                    {settings.mode === "adapt" && adapted.has(unit.id) ? <span className="shrink-0 text-[var(--fs-tiny)] text-foreground/40">已改编</span> : null}
                                </label>
                            ))}
                        </div>
                    </div>
                    {settings.mode === "adapt" ? (
                        <div className="grid gap-2">
                            <Radio.Group
                                size="small"
                                value={settings.countBy}
                                onChange={(event) => setSettings((current) => ({ ...current, countBy: event.target.value as Settings["countBy"] }))}
                                options={[
                                    { label: "按集数", value: "episodes" },
                                    { label: "按每集时长估集数", value: "seconds" },
                                ]}
                            />
                            {settings.countBy === "episodes" ? (
                                <label className="flex items-center gap-2 text-sm">
                                    <span className="shrink-0">改编成</span>
                                    <InputNumber min={1} max={40} precision={0} value={settings.targetEpisodes} onChange={(value) => setSettings((current) => ({ ...current, targetEpisodes: value || 1 }))} className="w-24" />
                                    <span className="shrink-0">集（几章可以合成一集，一章也可以拆成几集）</span>
                                </label>
                            ) : (
                                <label className="flex items-center gap-2 text-sm">
                                    <span className="shrink-0">每集约</span>
                                    <InputNumber
                                        min={EPISODE_SECONDS_RANGE.min}
                                        max={EPISODE_SECONDS_RANGE.max}
                                        precision={0}
                                        value={settings.episodeSeconds}
                                        onChange={(value) => setSettings((current) => ({ ...current, episodeSeconds: value || 90 }))}
                                        className="w-24"
                                    />
                                    <span className="shrink-0">秒，按原文字数估集数</span>
                                </label>
                            )}
                        </div>
                    ) : null}
                    {replaced ? (
                        <Callout tone="warning" title={`${replaced} 章已经改编过`}>
                            写完后会替换引用了这些章节的已有剧集，提交前会列出影响的剧集再确认一次。
                        </Callout>
                    ) : null}
                    <label className="block">
                        <span className="mb-1.5 block text-xs font-medium text-foreground/68">文本模型</span>
                        <ModelPicker
                            config={effectiveConfig}
                            capability="text"
                            value={settings.textModel}
                            onChange={(textModel) => setSettings((current) => ({ ...current, textModel }))}
                            variant="creation"
                            fullWidth
                            placeholder="选择用于剧本优化的文本模型"
                            showSelectedPrice={false}
                            onMissingConfig={() => navigateToSettings({ continueCreation: true })}
                        />
                    </label>
                    <div className="grid gap-2 rounded-lg border border-border/70 px-3 py-2.5">
                        <label className="flex items-center justify-between gap-3 text-xs">
                            <span className="font-medium text-foreground/68">出图：接着提取资产后给新资产出图</span>
                            <Switch
                                size="small"
                                checked={settings.images}
                                onChange={(images) => {
                                    setPrecheckError("");
                                    setSettings((current) => ({ ...current, images }));
                                }}
                                aria-label="接着提取资产后给新资产出图"
                            />
                        </label>
                        <p className="text-[var(--fs-tiny)] leading-[18px] text-foreground/45">写完剧集后要不要接着做下一步（导演定画风、场次色卡、按集提取资产）看项目的生成模式；开着时每集提取完直接给新资产出图（角色三视图，场景、道具概念图），不用再到资产页点「继续出图」。</p>
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
                                placeholder="选择资产出图用的图片模型"
                                showSelectedPrice
                                onMissingConfig={() => navigateToSettings({ continueCreation: true })}
                            />
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
                                    <div className="mt-1 text-[var(--fs-tiny)] text-foreground/42">
                                        技能由项目的风格档案决定，管理员在后台「流程技能」里绑定。{settings.mode === "rhythm" ? "所选剧集原地改写，集数不变。" : "原文章节保留，改编出的剧集排在章节之后。"}
                                    </div>
                                </>
                            ) : (
                                "正在读取本步使用的技能…"
                            )}
                        </div>
                    )}
                </div>
            </Modal>
            <ScriptAdaptRunPanel
                open={panelOpen}
                onClose={() => setPanelOpen(false)}
                running={running}
                stage={latestTask && (latestTask.status === "running" || latestTask.status === "queued") ? latestTask.stage : undefined}
                stream={streamState}
                outcome={running ? undefined : outcome}
                stop={running ? undefined : stop}
                onResume={() => {
                    if (resume) void start({ resumeTaskId: resume.taskId });
                }}
                onRegenerate={() => {
                    setPanelOpen(false);
                    openDialog();
                }}
                onOpenEpisodes={() => {
                    setPanelOpen(false);
                    if (outcome) onWritten(outcome);
                }}
            />
        </>
    );
}
