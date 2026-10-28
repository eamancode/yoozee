import { App, Button, Modal, Switch } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { UsersRound } from "lucide-react";
import { useNavigate } from "react-router";

import { ModelPicker } from "@/components/model-picker";
import { Tooltip } from "@/components/ui/base/tooltip";
import { Callout } from "@/components/ui/product/callout";
import { navigateToSettings } from "@/lib/settings-navigation";
import { dramaAssetsExtractOutcomeOf, dramaRunProgressOf, getDramaStepUsage, type DramaAssetsExtractOutcome } from "@/services/api/drama";
import { ApiError } from "@/services/api/request";
import { API_ERROR_REASON } from "@/services/api/error-codes";
import type { DramaImageStatus, ProjectDetail, ProjectUnit } from "@/services/api/projects";
import { queryGenerationTask, type GenerationTask } from "@/services/api/task-center";
import { useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";

import { assetsExtractStreamFromResult, parseAssetsExtractStream, resumedAssetsExtractStream, type AssetsExtractStreamState, type AssetsStreamCategory } from "./assets-extract-stream";
import { storyboardRunStop } from "./chapter-shot-stream";
import { assetImageTaskTemplate } from "./drama-image-task";
import { dramaImageSummaryText, useDramaImageStatuses } from "./drama-image-summary";
import { DramaRunPanel } from "./drama-run-panel";
import { chapterTaskIdentity, followDramaRunTask, runAssetsExtract } from "./project-chapter-ai";
import { turnaroundImageModel } from "./project-character-media";

type AssetsExtractControlProps = {
    detail: ProjectDetail;
    unit?: ProjectUnit;
    /** 项目的全部单元：自动接着提取的任务可能在别的集上，侧栏按它显示标题。 */
    units: ProjectUnit[];
    /** 这一集已有场次色卡（场次表接口 paletteReady）；没有时按钮禁用，提示先生成场次色卡。undefined 是还没读到。 */
    paletteReady?: boolean;
    /** 集或章（按当前单元是剧集还是原文章节）。 */
    unitNoun: string;
    tasks?: GenerationTask[];
    disabled?: boolean;
    /** 不能提取的原因（已改编的原文），按钮上悬停显示。 */
    disabledReason?: string;
};

type Settings = { textModel: string; images: boolean; imageModel: string };

const CHAINED_ASSETS_KEY = "drama-assets-follow:";
const CHAINED_ASSETS_EVENT = "drama-assets-follow";
const PALETTE_REQUIRED_HINT = "先生成场次色卡：资产的颜色按主基调色卡和本场色卡约束，到章节页点「生成场次色卡」";

/**
 * 场次色卡写完后服务端按集依次接着提取资产：交给「提取资产」按钮打开侧栏接上它的流（可能是别的集）。章节页可能重新挂载，
 * 先记进 sessionStorage（新挂载的按钮读它），同时发事件（没重新挂载时直接接上）。
 */
export function followChainedAssetsExtract(projectId: string, taskId: string, unitId?: string) {
    try {
        sessionStorage.setItem(CHAINED_ASSETS_KEY + projectId, JSON.stringify({ taskId, unitId, at: Date.now() }));
    } catch {
        // 存不了（隐私模式）时只靠事件。
    }
    window.dispatchEvent(new CustomEvent(CHAINED_ASSETS_EVENT, { detail: { projectId, taskId, unitId } }));
}

/** 挂载时取走记下的任务；一分钟以前记的不再接。 */
function takeChainedAssets(projectId: string): { taskId: string; unitId: string } | undefined {
    try {
        const raw = sessionStorage.getItem(CHAINED_ASSETS_KEY + projectId);
        sessionStorage.removeItem(CHAINED_ASSETS_KEY + projectId);
        const saved = raw ? (JSON.parse(raw) as { taskId?: string; unitId?: string; at?: number }) : undefined;
        return saved?.taskId && Date.now() - (saved.at || 0) < 60_000 ? { taskId: saved.taskId, unitId: saved.unitId || "" } : undefined;
    } catch {
        return undefined;
    }
}

/** 任务是给哪个单元提取的（metadata.chapterId）。 */
function taskUnitId(task: GenerationTask) {
    return chapterTaskIdentity(task)?.chapterId || "";
}

const CATEGORY_LABEL: Record<AssetsStreamCategory, string> = { character: "角色", environment: "场景", prop: "道具" };

/** 「从第 N 场继续」要不要出图跟上一次一样：从上一次任务的 metadata.dramaAssets 取。 */
function previousImages(task?: GenerationTask) {
    try {
        return Boolean((JSON.parse(task?.inputJson || "{}") as { metadata?: { dramaAssets?: { images?: boolean } } }).metadata?.dramaAssets?.images);
    } catch {
        return false;
    }
}

function AssetsRunGroups(stream: AssetsExtractStreamState) {
    return stream.scenes.map((scene) => ({
        key: scene.index,
        title: `第 ${scene.index} 场`,
        heading: scene.heading || undefined,
        meta: `${scene.rows.length} 个资产${scene.attempt > 1 ? ` · 第 ${scene.attempt} 次` : ""}`,
        body: (
            <ul className="space-y-1 rounded-md border border-border/60 px-2.5 py-1.5 text-xs leading-5">
                {scene.rows.map((row, index) => (
                    <li key={`${row.name}-${index}`} className="flex min-w-0 list-none items-baseline gap-2">
                        <span className="w-8 shrink-0 text-[var(--fs-micro)] text-foreground/45">{row.category ? CATEGORY_LABEL[row.category] : "？"}</span>
                        <span className="shrink-0 font-medium text-foreground/85">{row.name}</span>
                        <span className="min-w-0 flex-1 truncate text-foreground/55" title={row.appearance || undefined}>
                            {row.appearance || (row.variants.length ? `子卡：${row.variants.join("、")}` : row.aliases.length ? `补别名：${row.aliases.join("、")}` : "没写外观")}
                            {row.appearance && row.variants.length ? <span className="ml-1.5 text-foreground/45">子卡：{row.variants.join("、")}</span> : null}
                        </span>
                    </li>
                ))}
                {!scene.rows.length ? <li className="list-none text-foreground/40">等待这一场的资产…</li> : null}
            </ul>
        ),
    }));
}

/** 新增 / 合并 / 跳过的一句话，按分类拆开。 */
function countsText(outcome: DramaAssetsExtractOutcome) {
    const parts = (["character", "environment", "prop"] as const)
        .map((category) => {
            const count = outcome.byCategory[category];
            if (!count || (!count.created && !count.merged && !count.skipped)) return "";
            return `${CATEGORY_LABEL[category]} ${[count.created ? `新增 ${count.created}` : "", count.merged ? `合并 ${count.merged}` : "", count.skipped ? `跳过 ${count.skipped}` : ""].filter(Boolean).join("、")}`;
        })
        .filter(Boolean);
    if (outcome.variants) parts.push(`其中角色子卡 ${outcome.variants} 张`);
    if (outcome.scenes) parts.push(`${outcome.scenes} 场绑上了场景`);
    return parts.join("；");
}

/** 这次新建的资产和排了出图的资产（去重）：侧栏摘要按它们当前的出图状态算。 */
function imageAssetIds(outcome?: DramaAssetsExtractOutcome) {
    if (!outcome) return [];
    const created = outcome.assets.filter((item) => item.action === "created").map((item) => item.assetId);
    return [...new Set([...created, ...outcome.images.queued, ...outcome.images.waiting, ...outcome.images.failed])];
}

/** 出图的一句话：读到资产当前的出图状态时按它算（statuses），否则按收尾时排上几个、等空位几个、没排上几个。 */
function imagesText(outcome: DramaAssetsExtractOutcome, statuses?: DramaImageStatus[]) {
    if (statuses?.length) return `出图：${dramaImageSummaryText(statuses, "个", "资产上")}`;
    const { queued, waiting, failed } = outcome.images;
    if (!queued.length && !waiting.length && !failed.length) return outcome.created ? "这次没有出图，到资产页点「继续出图」可以补。" : "";
    return [`出图已排 ${queued.length} 个`, waiting.length ? `${waiting.length} 个等前面的出完接着排` : "", failed.length ? `${failed.length} 个没排上（原因写在资产上）` : ""].filter(Boolean).join("，") + "。";
}

/**
 * 章节页的「提取资产」（单元级步骤 assets.extract）：对话框选文本模型、要不要给新资产出图和用哪个图片模型（默认项目默认图片模型）；
 * 技能由项目风格档案的绑定决定。进度侧栏按场流式显示资产行，写完显示新增 / 合并 / 跳过并可去资产页。资产直接是已确认的，不用再确认。
 */
export function AssetsExtractControl({ detail, unit: selectedUnit, units, paletteReady, unitNoun, tasks, disabled, disabledReason }: AssetsExtractControlProps) {
    const { message } = App.useApp();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const projectId = detail.project.id;
    // followUnitId 是侧栏正在接的自动提取（场次色卡写完后按集依次起的）所在的集；没有时侧栏看当前选中的这一集。
    const [followUnitId, setFollowUnitId] = useState("");
    const unit = (followUnitId && units.find((item) => item.id === followUnitId)) || selectedUnit;
    const unitId = unit?.id || "";
    const selectedUnitId = selectedUnit?.id || "";
    const effectiveConfig = useEffectiveConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const [open, setOpen] = useState(false);
    const [panelOpen, setPanelOpen] = useState(false);
    const [starting, setStarting] = useState("");
    const [preparing, setPreparing] = useState(false);
    const [precheckError, setPrecheckError] = useState("");
    const [stream, setStream] = useState<{ taskId: string; unitId: string; text: string }>({ taskId: "", unitId: "", text: "" });
    const [resumeView, setResumeView] = useState<{ from: number; total: number; previous: AssetsExtractStreamState }>();
    const followedRef = useRef(new Set<string>());
    const [settings, setSettings] = useState<Settings>({ textModel: "", images: true, imageModel: "" });
    const usageQuery = useQuery({
        queryKey: ["project", projectId, "drama-step", "assets.extract", detail.project.styleProfile || ""],
        queryFn: ({ signal }) => getDramaStepUsage(projectId, "assets.extract", signal).then((result) => result.usage),
        enabled: open,
    });
    const assetsTaskOf = (id: string) => (tasks || []).find((task) => chapterTaskIdentity(task)?.kind === "assets" && chapterTaskIdentity(task)?.chapterId === id);
    const latestTask = useMemo(() => assetsTaskOf(unitId), [tasks, unitId]);
    const running = starting === unitId || Boolean(latestTask && (latestTask.status === "queued" || latestTask.status === "running"));
    const selectedTask = useMemo(() => assetsTaskOf(selectedUnitId), [tasks, selectedUnitId]);
    const selectedRunning = starting === selectedUnitId || Boolean(selectedTask && (selectedTask.status === "queued" || selectedTask.status === "running"));
    const paletteMissing = paletteReady === false;
    const terminal = latestTask && latestTask.status !== "queued" && latestTask.status !== "running" ? latestTask : undefined;
    const detailQuery = useQuery({ queryKey: ["generation-task", terminal?.id, terminal?.status], queryFn: () => queryGenerationTask(terminal!.id), enabled: Boolean(terminal), staleTime: Infinity });
    const taskDetail = detailQuery.data;
    const outcome = taskDetail?.status === "succeeded" ? dramaAssetsExtractOutcomeOf(taskDetail) : undefined;
    const stop = useMemo(() => (taskDetail ? storyboardRunStop(taskDetail, dramaRunProgressOf(taskDetail)) : undefined), [taskDetail]);
    const resume = stop?.kind === "resume" ? stop : undefined;
    const imageIds = imageAssetIds(outcome);
    // 侧栏打开时按资产当前的出图状态重算摘要：收尾之后接着排、出完、失败，资产页「继续出图」重出的都算进去。
    const imageStatuses = useDramaImageStatuses(projectId, imageIds, panelOpen && !running);
    const liveStream = stream.unitId === unitId ? stream : undefined;
    const streamState = useMemo(() => {
        if (liveStream && (running || (liveStream.text && (liveStream.taskId === taskDetail?.id || !taskDetail)))) return resumedAssetsExtractStream(parseAssetsExtractStream(liveStream.text), resumeView);
        return taskDetail ? assetsExtractStreamFromResult(taskDetail.resultJson) : parseAssetsExtractStream("");
    }, [liveStream, resumeView, running, taskDetail]);

    const defaultImageModel = () => turnaroundImageModel(effectiveConfig, detail.project.defaultImageModel);
    const openDialog = () => {
        // 侧栏（AppDrawer）和对话框同一层级、后渲染的在上面：先收起侧栏，免得盖住「开始提取」。
        setPanelOpen(false);
        setFollowUnitId("");
        setPrecheckError("");
        setSettings((current) => ({
            textModel: current.textModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "",
            images: current.images,
            imageModel: current.imageModel || defaultImageModel(),
        }));
        setOpen(true);
    };

    const refresh = async () => {
        await Promise.all([queryClient.invalidateQueries({ queryKey: ["project", projectId] }), queryClient.invalidateQueries({ queryKey: ["projects"] }), queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] })]);
    };

    const start = async (options: { resumeTaskId?: string } = {}) => {
        if (!unit) return;
        if (paletteMissing && unit.id === selectedUnitId) return message.warning(PALETTE_REQUIRED_HINT);
        if (detail.project.status === "archived") return message.warning("项目已归档，请先在项目设置中恢复");
        const textModel = settings.textModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const config = { ...effectiveConfig, model: textModel, textModel };
        if (!isAiConfigReady(config, textModel)) {
            navigateToSettings({ continueCreation: true });
            return;
        }
        const images = options.resumeTaskId ? previousImages(taskDetail) : settings.images;
        const imageModel = settings.imageModel || defaultImageModel();
        const imageConfig = { ...effectiveConfig, model: imageModel };
        // 出图模板在开跑前组好：组不出来时不建任务、不打开侧栏，原因留在对话框里。
        const precheckFailed = (reason: string) => {
            if (options.resumeTaskId) {
                message.error(`没有继续提取资产：${reason}`);
                return;
            }
            setPrecheckError(reason);
        };
        let imageTask: Awaited<ReturnType<typeof assetImageTaskTemplate>> | undefined;
        if (images) {
            if (!isAiConfigReady(imageConfig, imageModel)) return precheckFailed("资产出图用的图片模型还没配好：到设置里配置，或关掉出图。");
            setPrecheckError("");
            setPreparing(true);
            try {
                imageTask = await assetImageTaskTemplate(imageConfig, imageModel);
            } catch (error) {
                return precheckFailed(`资产出图模板准备失败：${error instanceof Error ? error.message.replace(/[。.]+$/, "") : "请检查图片模型配置"}。可以换个图片模型，或关掉出图。`);
            } finally {
                setPreparing(false);
            }
        }
        setPrecheckError("");
        setOpen(false);
        setStarting(unit.id);
        setStream({ taskId: "", unitId: unit.id, text: "" });
        setResumeView(options.resumeTaskId && resume ? { from: resume.from, total: resume.total, previous: assetsExtractStreamFromResult(taskDetail?.resultJson) } : undefined);
        setPanelOpen(true);
        try {
            const result = await runAssetsExtract(
                { projectId, unitId: unit.id, config, imageTask, resumeTaskId: options.resumeTaskId },
                {
                    onTaskUpdate: (task) => {
                        followedRef.current.add(task.id);
                        setStream((current) => ({ taskId: task.id, unitId: unit.id, text: current.taskId && current.taskId !== task.id ? "" : current.text }));
                    },
                    onStreamText: (text) => setStream((current) => ({ ...current, text })),
                },
            );
            await refresh();
            if (result.outcome) message.success(`资产已写入：新增 ${result.outcome.created} · 合并 ${result.outcome.merged} · 跳过 ${result.outcome.skipped}，技能：${result.usage.skill.name} ${result.usage.skill.version}`);
            followNext(result.outcome);
        } catch (error) {
            await refresh();
            if (error instanceof ApiError && error.reason === API_ERROR_REASON.PaletteRequired) {
                setPanelOpen(false);
                message.warning(PALETTE_REQUIRED_HINT);
            } else {
                message.error(error instanceof Error ? `提取资产失败：${error.message}` : "提取资产失败");
            }
        } finally {
            setStarting("");
        }
    };

    // 接上一个正在跑的提取任务的文本流（从头回放），结束后刷新；是自动接着跑的一串时，这一集写完接着跟下一集。
    const follow = (task: GenerationTask) => {
        const id = taskUnitId(task) || unitId;
        followedRef.current.add(task.id);
        setResumeView(undefined);
        setStream({ taskId: task.id, unitId: id, text: "" });
        void followDramaRunTask(task, { onStreamText: (text) => setStream({ taskId: task.id, unitId: id, text }) })
            .then((completed) => {
                followNext(dramaAssetsExtractOutcomeOf(completed));
                return refresh();
            })
            .catch(() => void queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] }));
    };
    const followChained = (taskId: string, chainedUnitId?: string) => {
        if (!taskId || followedRef.current.has(taskId)) return;
        setPanelOpen(true);
        void queryGenerationTask(taskId)
            .then((task) => {
                setFollowUnitId(taskUnitId(task) || chainedUnitId || "");
                follow(task);
                void queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", projectId] });
            })
            .catch(() => message.warning("没读到接着跑的提取资产任务，稍后在「资产进度」里看"));
    };
    // 这一集写完了、后面还有排着的集：服务端已经接着建了下一集的任务。
    const followNext = (outcome?: DramaAssetsExtractOutcome) => {
        const chained = outcome?.chained;
        if (chained?.taskId) {
            const next = units.find((item) => item.id === chained.unitId);
            message.info(`接着提取${next ? `「${next.title}」` : "下一集"}的资产${chained.remaining ? `，后面还有 ${chained.remaining} 集` : ""}`);
            followChained(chained.taskId, chained.unitId);
        } else if (chained?.error) {
            message.warning(`没有接着提取下一集：${chained.error}`);
        }
    };
    useEffect(() => {
        const saved = takeChainedAssets(projectId);
        if (saved) followChained(saved.taskId, saved.unitId);
        const onChained = (event: Event) => {
            const detail = (event as CustomEvent<{ projectId: string; taskId: string; unitId?: string }>).detail;
            if (detail?.projectId === projectId) followChained(detail.taskId, detail.unitId);
        };
        window.addEventListener(CHAINED_ASSETS_EVENT, onChained);
        return () => window.removeEventListener(CHAINED_ASSETS_EVENT, onChained);
    }, [projectId]);
    // 换了一集、侧栏没在接自动提取时，侧栏回到当前这一集。
    useEffect(() => {
        if (followUnitId && followUnitId !== selectedUnitId && !running && !panelOpen) setFollowUnitId("");
    }, [selectedUnitId, running, panelOpen]);

    // 刷新页面后打开侧栏：接上正在跑的提取任务的文本流（从头回放），结束后刷新。
    useEffect(() => {
        const task = latestTask;
        if (!panelOpen || !task || (task.status !== "queued" && task.status !== "running") || followedRef.current.has(task.id)) return;
        follow(task);
    }, [latestTask, panelOpen]);

    const usage = usageQuery.data;
    const warnings = outcome?.items || [];
    const status = running ? (
        <>
            <span className="font-medium text-foreground/80">{streamState.total ? `正在写第 ${Math.max(1, streamState.current)}/${streamState.total} 场` : "等待模型开始输出"}</span>
            <span>·</span>
            <span>已出 {streamState.rowCount} 个资产</span>
            {latestTask?.stage && (latestTask.status === "queued" || latestTask.status === "running") ? (
                <>
                    <span>·</span>
                    <span>{latestTask.stage}</span>
                </>
            ) : null}
        </>
    ) : outcome ? (
        <span>
            新增 {outcome.created} · 合并 {outcome.merged} · 跳过 {outcome.skipped}
        </span>
    ) : resume ? (
        <span>
            上一次写到第 {resume.from} 场停了（共 {resume.total} 场）
        </span>
    ) : stop?.kind === "failed" ? (
        <span>{stop.status === "cancelled" ? "上一次提取资产已取消" : "上一次提取资产失败"}</span>
    ) : (
        <span>这一{unitNoun}还没有提取过资产</span>
    );

    return (
        <>
            <Tooltip title={disabledReason || (paletteMissing ? PALETTE_REQUIRED_HINT : undefined)}>
                <Button
                    size="small"
                    icon={<UsersRound className="size-3.5" />}
                    disabled={disabled || selectedRunning || !selectedUnit || paletteMissing}
                    loading={selectedRunning}
                    onClick={openDialog}
                    aria-label={paletteMissing ? `提取角色、场景与道具（${PALETTE_REQUIRED_HINT}）` : "提取角色、场景与道具"}
                >
                    {selectedRunning ? "提取资产中" : paletteMissing ? "提取资产（先生成场次色卡）" : selectedTask?.status === "succeeded" ? "提取资产（已提取）" : "提取资产"}
                </Button>
            </Tooltip>
            {running || latestTask ? (
                <Button size="small" onClick={() => setPanelOpen(true)} aria-label="查看提取资产进度">
                    {resume && !running ? `资产停在第 ${resume.from} 场` : "资产进度"}
                </Button>
            ) : null}
            <Modal
                className="library-modal"
                title={`提取${unitNoun === "集" ? "本集" : "本章"}角色、场景与道具`}
                open={open}
                width={540}
                okText="开始提取"
                cancelText="取消"
                okButtonProps={{ disabled: !settings.textModel || !usage || (settings.images && !settings.imageModel), loading: preparing }}
                onCancel={() => setOpen(false)}
                onOk={() => void start()}
                styles={{ body: { paddingTop: 12 } }}
            >
                <div className="grid gap-4">
                    <div className="rounded-lg border border-border/70 bg-foreground/[.018] px-3 py-2.5">
                        <div className="text-[var(--fs-tiny)] text-foreground/42">当前{unitNoun}</div>
                        <div className="mt-1 truncate text-sm font-medium text-foreground/85">{unit?.title}</div>
                        <div className="mt-1 text-[var(--fs-tiny)] leading-[18px] text-foreground/45">按场提取角色、场景和道具，写外观和出图提示词；同名或同别名的已有资产只补别名和缺的外观，不改名字、不动已有的图；新资产直接进资产库，不用再确认。</div>
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
                            placeholder="选择用于提取资产的文本模型"
                            showSelectedPrice={false}
                            onMissingConfig={() => navigateToSettings({ continueCreation: true })}
                        />
                    </label>
                    <div className="grid gap-2">
                        <label className="flex items-center justify-between gap-3 text-xs">
                            <span className="font-medium text-foreground/68">写完后给新资产出图（角色三视图，场景、道具概念图）</span>
                            <Switch
                                size="small"
                                checked={settings.images}
                                onChange={(images) => {
                                    setPrecheckError("");
                                    setSettings((current) => ({ ...current, images }));
                                }}
                                aria-label="给新资产出图"
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
                                        技能由项目的风格档案决定。提示词带上项目已有资产、锁定的风格锚、主基调色卡（角色、道具用）和本集场次色卡（场景用）；角色换装或换状态时拆成子卡「名·变体」。出图带色卡图作参考，子卡另带主卡三视图。
                                    </div>
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
                title={`提取资产 · ${unit?.title || ""}`}
                running={running}
                status={status}
                unit="场"
                stepLabel="提取资产"
                stop={running ? undefined : stop}
                onResume={() => {
                    if (resume) void start({ resumeTaskId: resume.taskId });
                }}
                onRegenerate={() => {
                    setPanelOpen(false);
                    openDialog();
                }}
                failedNote="项目资产没有改动。"
                followKey={streamState.rowCount}
                emptyText="每一场的角色、场景和道具会出现在这里"
                listLabel="流式资产"
                groups={AssetsRunGroups(streamState)}
            >
                {outcome && !running ? (
                    <Callout
                        tone="success"
                        title={`资产已写入：新增 ${outcome.created} · 合并 ${outcome.merged} · 跳过 ${outcome.skipped}`}
                        action={
                            <Button size="small" type="primary" onClick={() => navigate(`/projects/${projectId}/assets`)}>
                                看资产
                            </Button>
                        }
                    >
                        {[countsText(outcome), imagesText(outcome, imageStatuses)].filter(Boolean).join("。")}
                        {warnings.length ? (
                            <ul className="mt-2 max-h-48 space-y-1 overflow-y-auto pl-0" aria-label="提取资产检查提醒">
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
