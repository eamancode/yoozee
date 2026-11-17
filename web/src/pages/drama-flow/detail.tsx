import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { App, Button, Dropdown } from "antd";
import { ArrowLeft, BookOpen, History, Inbox, Info, MoreHorizontal } from "lucide-react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";

import { WorkspaceErrorState, WorkspaceLoadingState } from "@/components/layout/workspace-state";
import { ModelPicker } from "@/components/model-picker";
import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { hasDirectorDocuments, projectWorkflow } from "@/lib/project-workflow";
import { navigateToSettings } from "@/lib/settings-navigation";
import { dramaImageStatusOf } from "@/pages/projects/workbench/detail/drama-image-summary";
import { assetImageTaskTemplate } from "@/pages/projects/workbench/detail/drama-image-task";
import { AssetImageRecovery } from "@/pages/projects/workbench/detail/asset-image-recovery";
import { dramaProgressKey } from "@/pages/projects/workbench/detail/drama-panels";
import { OntologyDocumentsDrawer } from "@/pages/projects/workbench/detail/ontology-documents-drawer";
import { continueProjectPipeline } from "@/pages/projects/workbench/detail/project-chapter-ai";
import { useSSSDocuments } from "@/pages/projects/workbench/detail/sss-project-panels";
import { VideoChainPanel } from "@/pages/projects/workbench/detail/video-chain-panel";
import { dramaRunModeLabel } from "@/pages/projects/workbench/drama-model";
import { prepareBackendGenerationTask } from "@/services/api/generation-task";
import { downloadDramaDelivery, queueDramaAssetImages, resynthesizeDramaPalettes } from "@/services/api/project-drama";
import { cancelVideoChain } from "@/services/api/project-video-chains";
import type { ProjectAsset } from "@/services/api/projects";
import { useConfigStore } from "@/stores/use-config-store";
import { useDramaModelPreferencesStore } from "@/stores/use-drama-model-preferences-store";
import { useUserStore } from "@/stores/use-user-store";

import { projectTasksKey, useFlowData } from "./hooks/use-flow-data";
import { assetImageQueueMessage, pendingAssetImages } from "./model/asset-images";
import { chainVideoConfig } from "./model/video-config";
import { buildInbox, inboxCounts, readIgnoredSuggestions, writeIgnoredSuggestions, type InboxItem } from "./model/inbox";
import { flowPrimaryAction, type FlowAction } from "./model/primary-action";
import { queryReadState } from "./model/read-state";
import { episodeCompletion, episodeFacts, episodeFactsLine, stageFacts } from "./model/stage-facts";
import { defaultFlowStage, flowStageStates, isFlowStageKey, type FlowStageKey } from "./model/stages";
import { ActionBar } from "./parts/action-bar";
import { EpisodeRail, type RailEpisode } from "./parts/episode-rail";
import { InboxPanel } from "./parts/inbox-panel";
import { InfoPanel } from "./parts/info-panel";
import { RunsPanel } from "./parts/runs-panel";
import { StageCut } from "./parts/stage-cut";
import { lookAssetError, StageLook } from "./parts/stage-look";
import { StageScript } from "./parts/stage-script";
import { StageShots } from "./parts/stage-shots";
import { StageStrip } from "./parts/stage-strip";
import { StageVideo } from "./parts/stage-video";
import { VideoRouteNotice } from "./parts/video-route-notice";

import "./drama-flow.css";

const errorText = (error: unknown, fallback: string) => (error instanceof Error && error.message ? error.message : fallback);

/**
 * 短剧新工作流主页面：上面五步、左边分集、中间当前步骤的全部产物、右边选中项详情、底下一句话加一个主按钮。
 * 改内容、重试都在这一页做完；和旧版工作台读写同一个项目、同一套接口。
 */
export default function DramaFlowDetailPage() {
    const { projectId = "", stage: stageParam } = useParams();
    const [search, setSearch] = useSearchParams();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const { message } = App.useApp();
    const userId = useUserStore((state) => state.user?.id) || "guest";
    const effectiveConfig = useDramaModelConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);

    const data = useFlowData(projectId, search.get("ep") || "", !isFlowStageKey(stageParam) || stageParam === "video");
    const { project, progress, pipeline, pipelineQuery, coreQuery, rail, unit, unitId, segments, unitSegments, steps, activeChains, chainReadOf, assets, bible, episodes, videoModel, imageModel, refreshVideo } = data;
    const focusId = search.get("item") || undefined;
    const tab = search.get("tab") || undefined;
    const panel = search.get("panel");
    // SSS 电影 / 短剧、本体论流水线：圣经、立意与结构、世界与总谱、各集分镜文档、交接卡、待确认与单元返修（与旧版工作台同一个侧栏）。
    const [directorOpen, setDirectorOpen] = useState(false);

    const stages = useMemo(() => flowStageStates(pipeline), [pipeline]);
    const stage: FlowStageKey = isFlowStageKey(stageParam) ? stageParam : defaultFlowStage(pipeline);
    const stageView = stages.find((item) => item.key === stage);
    const episodeDesigns = useSSSDocuments(projectId, stage === "script" && projectWorkflow(project?.workflowId).storyboardMode === "sss");

    const go = useCallback(
        (nextStage: FlowStageKey, target: { ep?: string; item?: string; tab?: string } = {}, replace = false) => {
            const params = new URLSearchParams();
            const ep = target.ep ?? unitId;
            if (ep) params.set("ep", ep);
            if (target.tab) params.set("tab", target.tab);
            if (target.item) params.set("item", target.item);
            const query = params.toString();
            navigate(`/drama-flow/${encodeURIComponent(projectId)}/${nextStage}${query ? `?${query}` : ""}`, { replace });
        },
        [navigate, projectId, unitId],
    );
    /** 只改地址栏里的一个参数（小节、选中项、浮层），其余保持。 */
    const setParam = useCallback(
        (key: "tab" | "item" | "panel", value?: string) =>
            setSearch(
                (current) => {
                    const next = new URLSearchParams(current);
                    if (value) next.set(key, value);
                    else next.delete(key);
                    if (key === "tab") next.delete("item");
                    return next;
                },
                { replace: key === "item" },
            ),
        [setSearch],
    );

    // 浏览器返回或打开旧链接时，等剧集索引读完再替换失效的集和段。
    const requestedId = search.get("ep");
    useEffect(() => {
        if (!requestedId || requestedId === unitId || !data.unitsQuery.isSuccess || data.unitsQuery.isFetching) return;
        setSearch(
            (current) => {
                if (current.get("ep") !== requestedId) return current;
                const next = new URLSearchParams(current);
                if (unitId) next.set("ep", unitId);
                else next.delete("ep");
                next.delete("item");
                return next;
            },
            { replace: true },
        );
    }, [requestedId, unitId, data.unitsQuery.isSuccess, data.unitsQuery.isFetching, setSearch]);

    const [ignored, setIgnored] = useState<string[]>(() => readIgnoredSuggestions(userId, projectId));
    useEffect(() => {
        setIgnored(readIgnoredSuggestions(userId, projectId));
    }, [userId, projectId]);
    const inbox = useMemo(
        () =>
            buildInbox({
                pipeline,
                segments,
                assets: assets.map((asset) => ({ id: asset.id, title: asset.title, kind: asset.category === "palette" ? ("palette" as const) : ("asset" as const), status: dramaImageStatusOf(asset) || "", error: lookAssetError(asset) || undefined })),
                videoSteps: [...steps.values()].map((step) => ({ chainId: step.chainId, unitId: step.unitId, shotId: step.shotId, title: step.title, status: step.state, error: step.error })),
                ignored,
            }),
        [pipeline, segments, assets, steps, ignored],
    );
    const counts = inboxCounts(inbox);
    const failedSteps = useMemo(() => [...steps.values()].filter((step) => step.state === "failed"), [steps]);
    const runningVideos = useMemo(() => [...steps.values()].filter((step) => step.state === "running" || step.state === "waiting").length, [steps]);
    const flow = flowPrimaryAction({
        pipeline,
        segments: data.episodeCounts?.segments ?? 0,
        generated: data.episodeCounts?.generated ?? 0,
        inCut: data.episodeCounts?.inCut ?? 0,
        mustHandle: counts.must,
        toConfirm: counts.confirm,
        failedVideos: failedSteps.length,
        runningVideos,
    });

    const facts = useMemo(() => {
        const visual = assets.filter((asset) => asset.category !== "palette" && !asset.variant && dramaImageStatusOf(asset));
        const scenePalettes = assets.filter((asset) => asset.palette && !asset.palette.master && !asset.palette.sceneGone);
        // 哪个数据没读到（或这次刷新失败），对应那一截就不写，不拿 0 顶替。
        const assetsRead = Boolean(data.assetsQuery.data) && !data.assetsQuery.isError;
        const progressRead = Boolean(progress) && !pipelineQuery.isError;
        return stageFacts({
            episodes: data.unitsQuery.data && !data.unitsQuery.isError ? rail.units.length : undefined,
            style: data.styleLock === "loading" || data.styleLock === "error" ? undefined : data.styleLock,
            palettes: assetsRead ? { ready: scenePalettes.filter((asset) => asset.palette?.imageStatus === "bound").length, total: scenePalettes.length } : undefined,
            assets: assetsRead ? { bound: visual.filter((asset) => dramaImageStatusOf(asset) === "bound").length, total: visual.length, failed: visual.filter((asset) => ["failed", "stale"].includes(dramaImageStatusOf(asset) || "")).length } : undefined,
            counts: progressRead ? data.episodeCounts : undefined,
            videos: data.videosRead === "ready" ? { failed: failedSteps.length, running: runningVideos } : undefined,
        });
    }, [
        assets,
        data.assetsQuery.data,
        data.assetsQuery.isError,
        data.unitsQuery.data,
        data.unitsQuery.isError,
        rail.units.length,
        data.bibleQuery.isError,
        pipelineQuery.isError,
        data.episodeCounts,
        bible,
        project?.bibleJson,
        progress,
        data.videosRead,
        failedSteps.length,
        runningVideos,
    ]);

    // 按所选视频模型能力对齐（同步音频、水印、分辨率），只用于新工作流的出片请求，不写回全局偏好。
    const videoConfig = useMemo(() => chainVideoConfig(effectiveConfig, videoModel, { aspectRatio: project?.aspectRatio }), [effectiveConfig, videoModel, project?.aspectRatio]);
    const canGenerate = Boolean(videoModel) && isAiConfigReady(videoConfig, videoModel);

    const [inboxOpen, setInboxOpen] = useState(false);
    const [chainTarget, setChainTarget] = useState<{ projectId: string; unitId: string; shotId: string } | undefined>(undefined);
    useEffect(() => setChainTarget(undefined), [projectId]);
    const [busyAction, setBusyAction] = useState<FlowAction["kind"] | undefined>(undefined);
    // 「继续：角色场景资产」先给确认面板（选图片模型、看单价、排除不想出的资产），确认后才建出图任务；与旧工作台资产页同一个面板。
    const [imagePanelOpen, setImagePanelOpen] = useState(false);
    const queueLock = useRef(false);
    const pendingImageAssets = useMemo(() => pendingAssetImages(assets), [assets]);
    const buildAssetImageTask = (model: string) => {
        const reason = model && data.stepModels.image.disabledReason(model);
        if (reason) throw new Error(reason);
        const config = { ...effectiveConfig, model };
        if (!model || !isAiConfigReady(config, model)) throw new Error("请先选择可用的图片模型");
        return assetImageTaskTemplate(config, model);
    };
    const queueImages = useMutation({
        mutationKey: ["drama-flow-asset-images", projectId],
        mutationFn: async ({ model, assetIds }: { model: string; assetIds: string[] }) => {
            if (!assetIds.length) throw new Error("请至少选择一个资产");
            return queueDramaAssetImages(projectId, { assetIds, imageTask: await buildAssetImageTask(model) });
        },
        onSuccess: async (result) => {
            setImagePanelOpen(false);
            await Promise.all([
                queryClient.invalidateQueries({ queryKey: ["project", projectId, "assets"] }),
                queryClient.invalidateQueries({ queryKey: dramaProgressKey(projectId) }),
                queryClient.invalidateQueries({ queryKey: projectTasksKey(projectId) }),
            ]);
            const notice = assetImageQueueMessage(result);
            message[notice.level](notice.text);
        },
        onError: (error) => message.error(errorText(error, "资产出图没排上")),
    });

    const [retryingImages, setRetryingImages] = useState<Set<string>>(new Set());
    const retryImages = useMutation({
        mutationFn: async (list: ProjectAsset[]) => {
            setRetryingImages(new Set(list.map((asset) => asset.id)));
            const palettes = list.filter((asset) => asset.category === "palette");
            const visual = list.filter((asset) => asset.category !== "palette");
            if (palettes.length) await resynthesizeDramaPalettes(projectId, { assetIds: palettes.map((asset) => asset.id) });
            if (!visual.length) return;
            const result = await queueDramaAssetImages(projectId, { assetIds: visual.map((asset) => asset.id), imageTask: await buildAssetImageTask(imageModel) });
            if (result.failed.length) throw new Error(`${result.failed.length} 张没排上，原因写在卡片上`);
        },
        onSuccess: async (_, list) => {
            await queryClient.invalidateQueries({ queryKey: ["project", projectId, "assets"] });
            message.success(list.length > 1 ? `已重新出 ${list.length} 张，只重做这几张` : "已重新出图，只重做这一张");
        },
        onError: async (error) => {
            await queryClient.invalidateQueries({ queryKey: ["project", projectId, "assets"] });
            message.error(errorText(error, "重试没成功"));
        },
        onSettled: () => setRetryingImages(new Set()),
    });
    const stopChain = useMutation({
        mutationFn: (chainId: string) => cancelVideoChain(projectId, chainId),
        onSuccess: async () => {
            await refreshVideo();
            message.success("已停止，出好的都留着");
        },
        onError: (error) => message.error(errorText(error, "停止没成功")),
    });

    const runContinue = async () => {
        if (!project) return;
        // 停在「角色场景资产」：有没出图的资产时先开确认面板，不直接替所有资产出图；还没提取时这一次只跑提取，出图同样要等确认。
        const atAssets = pipeline?.stop?.stage === "assets";
        // 资产列表还没读到时不能当成「没有待出图的资产」往下走：那样会跳过确认面板直接续跑。
        if (atAssets && (!data.assetsQuery.data || data.assetsQuery.isError)) {
            message.info(data.assetsQuery.isError ? "资产列表没读到，刷新后再点「继续」" : "正在读资产列表，读到后再点「继续」");
            if (data.assetsQuery.isError) void data.assetsQuery.refetch();
            return;
        }
        if (atAssets && pendingImageAssets.length) {
            setImagePanelOpen(true);
            return;
        }
        // 与文本模型卡同一个值：只取短剧这一步能用的模型，没有就去设置。
        const textModel = data.textModel;
        // 到资产停靠点时单独确认出图，其余步骤只带模型卡校验过的图片模型；空值表示纯文本继续。
        const config = { ...effectiveConfig, model: textModel, textModel, imageModel: atAssets ? "" : imageModel };
        if (!textModel || !isAiConfigReady(config, textModel)) {
            navigateToSettings({ continueCreation: true });
            return;
        }
        await continueProjectPipeline(project.id, config);
        await Promise.all([queryClient.invalidateQueries({ queryKey: dramaProgressKey(project.id) }), queryClient.invalidateQueries({ queryKey: projectTasksKey(project.id) })]);
    };

    const openGenerate = (shotId: string, targetUnitId = unitId) => {
        const state = steps.get(shotId)?.state;
        if (!canGenerate && state !== "failed" && state !== "paused") {
            message.warning("还没配好可用的视频模型，先到设置里配好");
            return;
        }
        setChainTarget({ projectId, unitId: targetUnitId, shotId });
    };

    // 视频重试一律先开花费预览：预览里写明这次要出的范围（同场后面没出的段也可能一起出）和预估花费，确认后才提交。
    const retryFailedVideos = () => {
        const first = unitSegments.find((item) => steps.get(item.shotId)?.state === "failed") || segments.find((item) => steps.get(item.shotId)?.state === "failed");
        if (!first) return;
        go("video", { ep: first.unitId, item: first.shotId });
        openGenerate(first.shotId, first.unitId);
        if (failedSteps.length > 1) message.info(`先重新出「${first.title || first.segmentId}」；其余 ${failedSteps.length - 1} 段失败的在出片页逐段重新出，每次都会先给你看花费`);
    };
    const stopAllVideos = async () => {
        await Promise.all(activeChains.map((id) => cancelVideoChain(projectId, id)));
        await refreshVideo();
        message.success("已停止出片，出好的都留着");
    };

    const onAction = async (action: FlowAction) => {
        if (action.kind === "open_inbox") return setInboxOpen(true);
        if (action.kind === "go_stage") return go(action.stage || stage);
        if (action.kind === "recharge") return navigate("/wallet");
        if (action.kind === "start_video") {
            // 先在当前这一集里找，没有再找别的集；带着花费预览的面板是出片前唯一的确认。
            const ready = (item: (typeof segments)[number]) => !item.generated && !item.overLimit && !(item.issues ?? 0);
            const next = unitSegments.find(ready) || segments.find(ready);
            if (!next) return go("video");
            go("video", { ep: next.unitId, item: next.shotId });
            return openGenerate(next.shotId, next.unitId);
        }
        setBusyAction(action.kind);
        try {
            if (action.kind === "continue") await runContinue();
            else if (action.kind === "retry_videos") retryFailedVideos();
            else if (action.kind === "stop_video") await stopAllVideos();
            else if (action.kind === "download" && project) await downloadDramaDelivery(projectId, `${project.name}.zip`);
        } catch (error) {
            message.error(errorText(error, "操作没成功"));
        } finally {
            setBusyAction(undefined);
        }
    };

    const locate = (item: InboxItem) => {
        setInboxOpen(false);
        const lookTab = item.stage === "look" ? assets.find((asset) => asset.id === item.target.itemId)?.category : undefined;
        go(item.stage, { ep: item.target.unitId, item: item.target.itemId, tab: lookTab === "palette" ? "palette" : lookTab });
    };
    const actInbox = (item: InboxItem) => {
        if (item.action.kind === "retry_image") {
            const asset = assets.find((candidate) => candidate.id === item.target.itemId);
            if (asset) retryImages.mutate([asset]);
        } else if (item.action.kind === "retry_video") {
            const step = item.target.itemId ? steps.get(item.target.itemId) : undefined;
            if (!step) return;
            setInboxOpen(false);
            go("video", { ep: step.unitId, item: step.shotId });
            openGenerate(step.shotId, step.unitId);
        } else if (item.action.kind === "continue") void onAction({ kind: "continue", label: item.action.label });
        else locate(item);
    };
    const ignore = (item: InboxItem) => {
        const next = [...ignored, item.id];
        setIgnored(next);
        writeIgnoredSuggestions(userId, projectId, next);
    };
    const inboxBusyId = retryImages.isPending && retryImages.variables?.length === 1 ? `image:${retryImages.variables[0].id}` : busyAction === "continue" ? inbox.find((item) => item.action.kind === "continue")?.id : undefined;

    const railEpisodes: RailEpisode[] = useMemo(() => {
        const mustUnits = new Set(inbox.filter((item) => item.group === "must" && item.target.unitId).map((item) => item.target.unitId));
        const unread = pipelineQuery.isError || !progress;
        const rows = new Map(episodes?.episodes.map((row) => [row.unitId, row]));
        const designed = new Set(episodeDesigns.data?.designs?.map((doc) => doc.unitId));
        return rail.units.map((item, index) => {
            if (stage === "script") {
                const row = rows.get(item.id);
                const summary = [
                    `${(row?.wordCount ?? item.wordCount).toLocaleString("zh-CN")} 字`,
                    row && `来源 ${row.sources.length} 处`,
                    row && (row.shots ? `已分镜 ${row.shots} 个镜头` : row.hasShotlist ? "镜头表是草稿" : "还没分镜"),
                    episodeDesigns.isEnabled && episodeDesigns.data && !designed.has(item.id) && "缺本集设计",
                ]
                    .filter(Boolean)
                    .join(" · ");
                return { id: item.id, title: item.title || `第 ${index + 1} 集`, summary, warn: mustUnits.has(item.id) };
            }
            if (unread) return { id: item.id, title: item.title || `第 ${index + 1} ${rail.noun}`, summary: pipelineQuery.isError ? "进度没读到" : "正在读进度…", warn: false };
            const facts = episodeFacts(item.id, segments, steps);
            return { id: item.id, title: item.title || `第 ${index + 1} ${rail.noun}`, summary: episodeFactsLine(facts, chainReadOf(item.id)), warn: mustUnits.has(item.id), completion: episodeCompletion(facts) };
        });
    }, [rail, segments, steps, inbox, pipelineQuery.isError, progress, chainReadOf, stage, episodes, episodeDesigns.data, episodeDesigns.isEnabled]);
    const showRail = stage !== "cut";
    const needsEpisode = (stage === "shots" || stage === "video") && !unit;

    if (coreQuery.isError) return <WorkspaceErrorState title="项目没读出来" description={errorText(coreQuery.error, "请稍后再试")} onRetry={() => void coreQuery.refetch()} />;
    if (!project) return <WorkspaceLoadingState label="正在打开项目" />;

    const pending = counts.must + counts.confirm;
    return (
        <div className="drama-flow-shell">
            <header className="drama-flow-top">
                <Link to="/drama-flow" aria-label="返回项目列表">
                    <Button type="text" icon={<ArrowLeft className="size-4" />} />
                </Link>
                <h1 title={project.name}>{project.name}</h1>
                <span className="drama-flow-top-meta">
                    {[data.unitsQuery.data ? `${rail.units.length} 集` : "", data.episodeCounts ? `${data.episodeCounts.segments} 段` : "", project.aspectRatio, pipeline ? dramaRunModeLabel(pipeline.runMode) : ""].filter(Boolean).join(" · ")}
                </span>
                <div className="drama-flow-top-actions">
                    {hasDirectorDocuments(project.workflowId) && data.detail ? (
                        <Button icon={<BookOpen className="size-4" />} onClick={() => setDirectorOpen(true)}>
                            导演文档
                        </Button>
                    ) : null}
                    <Button icon={<Inbox className="size-4" />} onClick={() => setInboxOpen(true)}>
                        待办
                        {inbox.length ? (
                            <span className="drama-flow-badge" data-quiet={pending ? undefined : "true"}>
                                {pending || inbox.length}
                            </span>
                        ) : null}
                    </Button>
                    <Button icon={<History className="size-4" />} onClick={() => setParam("panel", "runs")}>
                        运行记录
                    </Button>
                    <Button icon={<Info className="size-4" />} onClick={() => setParam("panel", "info")}>
                        项目信息
                    </Button>
                    <Dropdown
                        trigger={["click"]}
                        menu={{
                            items: [
                                { key: "legacy", label: "在旧版工作台打开" },
                                { key: "settings", label: "模型设置" },
                            ],
                            onClick: ({ key }) => (key === "legacy" ? navigate(`/projects/${encodeURIComponent(projectId)}`) : navigateToSettings()),
                        }}
                    >
                        <Button type="text" aria-label="更多" icon={<MoreHorizontal className="size-4" />} />
                    </Dropdown>
                </div>
            </header>
            {hasDirectorDocuments(project.workflowId) && data.detail ? <OntologyDocumentsDrawer detail={data.detail} open={directorOpen} onClose={() => setDirectorOpen(false)} /> : null}
            <StageStrip stages={stages} facts={facts} active={stage} onSelect={(next) => go(next)} />
            <div className="drama-flow-main" data-rail={showRail ? undefined : "off"}>
                {showRail ? (
                    <EpisodeRail
                        key={projectId}
                        projectId={projectId}
                        episodes={railEpisodes}
                        archived={project.status === "archived"}
                        activeId={unitId}
                        readState={queryReadState(data.unitsQuery)}
                        onRetry={() => void data.unitsQuery.refetch()}
                        onSelect={(id, replace) => go(stage, { ep: id, tab }, replace)}
                    />
                ) : null}
                <main className="drama-flow-work">
                    <div className="drama-flow-work-inner">
                        {pipelineQuery.isError ? (
                            <div className="drama-flow-note" data-tone="error">
                                制作进度没读出来。
                                <Button size="small" type="link" onClick={() => void pipelineQuery.refetch()}>
                                    再读一次
                                </Button>
                            </div>
                        ) : null}
                        {stage === "script" ? <StageScript data={data} stage={stageView} tab={tab} onTab={(next) => setParam("tab", next)} onSelectUnit={(id) => go("script", { ep: id, tab })} onRedoShots={(id) => go("shots", { ep: id })} /> : null}
                        {stage === "look" ? (
                            <StageLook data={data} stage={stageView} tab={tab} selectedId={focusId} retrying={retryingImages} onTab={(next) => setParam("tab", next)} onSelect={(id) => setParam("item", id)} onRetry={(list) => retryImages.mutate(list)} />
                        ) : null}
                        {needsEpisode ? (
                            <div className="drama-flow-empty">
                                <strong>{data.unitsQuery.isError ? "剧集列表没读到" : data.unitsQuery.isLoading ? "正在读取剧集…" : project.status === "archived" ? "还没有剧集" : "添加首集后即可开始分镜"}</strong>
                            </div>
                        ) : null}
                        {stage === "shots" && unit ? <StageShots key={unitId} data={data} stage={stageView} selectedId={focusId} onSelect={(shotId) => setParam("item", shotId)} /> : null}
                        {stage === "video" && unit ? (
                            <StageVideo
                                key={unitId}
                                data={data}
                                stage={stageView}
                                selectedId={focusId}
                                canGenerate={canGenerate}
                                stoppingChainId={stopChain.isPending ? stopChain.variables : undefined}
                                onSelect={(shotId) => setParam("item", shotId)}
                                onGenerate={(shotId) => openGenerate(shotId)}
                                onStopChain={(chainId) => stopChain.mutate(chainId)}
                                onRetryAll={retryFailedVideos}
                                onStopAll={() => void stopAllVideos().catch((error) => message.error(errorText(error, "停止没成功")))}
                            />
                        ) : null}
                        {stage === "cut" ? <StageCut data={data} stage={stageView} onGo={(next, target) => go(next, target)} /> : null}
                    </div>
                </main>
            </div>
            <ActionBar flow={flow} busy={busyAction} onAction={(action) => void onAction(action)} />
            <InboxPanel open={inboxOpen} items={inbox} busyId={inboxBusyId} onClose={() => setInboxOpen(false)} onAct={actInbox} onLocate={locate} onIgnore={ignore} />
            <RunsPanel open={panel === "runs"} data={data} onClose={() => setParam("panel")} />
            <InfoPanel open={panel === "info"} data={data} onClose={() => setParam("panel")} />
            {imagePanelOpen ? (
                <AssetImageRecovery
                    assets={pendingImageAssets}
                    config={effectiveConfig}
                    defaultModel={imageModel}
                    modelSelection={{ ...data.stepModels.image, onChange: (model) => useDramaModelPreferencesStore.getState().remember("image", model) }}
                    busy={queueImages.isPending}
                    onClose={() => {
                        if (!queueImages.isPending) setImagePanelOpen(false);
                    }}
                    onSubmit={(selection) => {
                        // 同一拍里连点两下时 isPending 还没刷新：用 ref 锁住，一次确认只建一批出图任务。
                        if (queueLock.current || queueImages.isPending) return;
                        queueLock.current = true;
                        queueImages.mutate(selection, { onSettled: () => void (queueLock.current = false) });
                    }}
                />
            ) : null}
            {chainTarget?.projectId === projectId ? (
                <VideoChainPanel
                    key={JSON.stringify([
                        projectId,
                        chainTarget.unitId,
                        chainTarget.shotId,
                        data.videoRouting.route?.revision,
                        videoModel,
                        project.aspectRatio,
                        data.workspace?.shots.map((shot) => [shot.id, shot.currentRevisionId, shot.durationMs, shot.position]),
                        data.workspace?.shotReferences.map((ref) => [ref.shotId, ref.assetVersionId, ref.role, ref.position, ref.status]),
                        unitSegments.map((item) => [item.shotId, item.durationMs]),
                    ])}
                    open
                    projectId={projectId}
                    whiteModelStoryboardEnabled={project.whiteModelStoryboardEnabled === true}
                    sss={projectWorkflow(project.workflowId).storyboardMode === "sss"}
                    unitId={chainTarget.unitId}
                    shotId={chainTarget.shotId}
                    onClose={() => setChainTarget(undefined)}
                    onRefresh={refreshVideo}
                    rememberModel={false}
                    modelPicker={
                        <>
                            <ModelPicker
                                config={effectiveConfig}
                                capability="video"
                                value={videoModel}
                                onChange={data.stepModels.video.select}
                                modelDisabledReason={data.stepModels.video.disabledReason}
                                triggerAriaLabel="更换本次报价的视频模型"
                                showSelectedPrice={false}
                                showPreferenceNotice={false}
                                showOptionPrices
                            />
                            <VideoRouteNotice routing={data.videoRouting} />
                        </>
                    }
                    onEditShot={(shotId) => {
                        setChainTarget(undefined);
                        go("shots", { ep: chainTarget.unitId, item: shotId });
                    }}
                    buildModel={async (quoteShotId) => {
                        const reason = videoModel && data.stepModels.video.disabledReason(videoModel);
                        if (!videoModel || reason) {
                            throw new Error(reason || data.stepModels.video.status("请选择可用的视频模型").text);
                        }
                        // 费用预览和确认提交都走这里：时长按这一段的正文取模型能提交的档位，不用全局偏好里的秒数。
                        const target = segments.find((item) => item.shotId === (quoteShotId || chainTarget.shotId));
                        const config = chainVideoConfig(effectiveConfig, videoModel, { aspectRatio: project?.aspectRatio, contentSeconds: target ? target.durationMs / 1000 : undefined });
                        const task = await prepareBackendGenerationTask({ projectId, mode: "video", prompt: "连续生成预览", config });
                        return { model: task.model || videoModel, logicalModelId: task.logicalModelId, input: task.input || {} };
                    }}
                />
            ) : null}
        </div>
    );
}
