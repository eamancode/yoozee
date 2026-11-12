import { useCallback, useEffect, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { App, Button, Dropdown } from "antd";
import { ArrowLeft, History, Inbox, Info, MoreHorizontal } from "lucide-react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";

import { WorkspaceErrorState, WorkspaceLoadingState } from "@/components/layout/workspace-state";
import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { navigateToSettings } from "@/lib/settings-navigation";
import { projectLockedStyle } from "@/pages/projects/workbench/detail/chapter-style-lock";
import { dramaImageStatusOf } from "@/pages/projects/workbench/detail/drama-image-summary";
import { assetImageTaskTemplate } from "@/pages/projects/workbench/detail/drama-image-task";
import { dramaProgressKey } from "@/pages/projects/workbench/detail/drama-panels";
import { continueProjectPipeline } from "@/pages/projects/workbench/detail/project-chapter-ai";
import { VideoChainPanel } from "@/pages/projects/workbench/detail/video-chain-panel";
import { dramaRunModeLabel } from "@/pages/projects/workbench/drama-model";
import { prepareBackendGenerationTask } from "@/services/api/generation-task";
import { downloadDramaDelivery, queueDramaAssetImages, resynthesizeDramaPalettes } from "@/services/api/project-drama";
import { cancelVideoChain } from "@/services/api/project-video-chains";
import type { ProjectAsset } from "@/services/api/projects";
import { useConfigStore } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";

import { projectTasksKey, useFlowData } from "./hooks/use-flow-data";
import { buildInbox, inboxCounts, readIgnoredSuggestions, writeIgnoredSuggestions, type InboxItem } from "./model/inbox";
import { flowPrimaryAction, type FlowAction } from "./model/primary-action";
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

    const data = useFlowData(projectId, search.get("ep") || "");
    const { project, progress, pipeline, pipelineQuery, coreQuery, rail, unit, unitId, segments, unitSegments, steps, activeChains, chainReadOf, assets, bible, episodes, videoModel, refreshVideo } = data;
    const focusId = search.get("item") || undefined;
    const tab = search.get("tab") || undefined;
    const panel = search.get("panel");

    const stages = useMemo(() => flowStageStates(pipeline), [pipeline]);
    const stage: FlowStageKey = isFlowStageKey(stageParam) ? stageParam : defaultFlowStage(pipeline);
    const stageView = stages.find((item) => item.key === stage);

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

    const [ignored, setIgnored] = useState<string[]>(() => readIgnoredSuggestions(userId, projectId));
    useEffect(() => setIgnored(readIgnoredSuggestions(userId, projectId)), [userId, projectId]);
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
        segments: progress?.counts.segments ?? 0,
        generated: progress?.counts.generated ?? 0,
        inCut: progress?.counts.inCut ?? 0,
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
            episodes: episodes && !data.episodesQuery.isError ? episodes.episodes.length : undefined,
            styleLocked: bible && !data.bibleQuery.isError ? Boolean(projectLockedStyle(project?.bibleJson) || bible.style_anchor) : undefined,
            palettes: assetsRead ? { ready: scenePalettes.filter((asset) => asset.palette?.imageStatus === "bound").length, total: scenePalettes.length } : undefined,
            assets: assetsRead ? { bound: visual.filter((asset) => dramaImageStatusOf(asset) === "bound").length, total: visual.length, failed: visual.filter((asset) => ["failed", "stale"].includes(dramaImageStatusOf(asset) || "")).length } : undefined,
            counts: progressRead && progress ? progress.counts : undefined,
            videos: data.videosRead === "ready" ? { failed: failedSteps.length, running: runningVideos } : undefined,
        });
    }, [assets, data.assetsQuery.data, data.assetsQuery.isError, data.episodesQuery.isError, data.bibleQuery.isError, pipelineQuery.isError, episodes, bible, project?.bibleJson, progress, data.videosRead, failedSteps.length, runningVideos]);

    const videoConfig = useMemo(() => ({ ...effectiveConfig, model: videoModel, videoModel, size: project?.aspectRatio || effectiveConfig.size }), [effectiveConfig, videoModel, project?.aspectRatio]);
    const canGenerate = Boolean(videoModel) && isAiConfigReady(videoConfig, videoModel);

    const [inboxOpen, setInboxOpen] = useState(false);
    const [chainTarget, setChainTarget] = useState<{ unitId: string; shotId: string } | undefined>(undefined);
    const [busyAction, setBusyAction] = useState<FlowAction["kind"] | undefined>(undefined);

    const [retryingImages, setRetryingImages] = useState<Set<string>>(new Set());
    const retryImages = useMutation({
        mutationFn: async (list: ProjectAsset[]) => {
            setRetryingImages(new Set(list.map((asset) => asset.id)));
            const palettes = list.filter((asset) => asset.category === "palette");
            const visual = list.filter((asset) => asset.category !== "palette");
            if (palettes.length) await resynthesizeDramaPalettes(projectId, { assetIds: palettes.map((asset) => asset.id) });
            if (!visual.length) return;
            const model = project?.defaultImageModel || effectiveConfig.imageModel;
            const config = { ...effectiveConfig, model };
            if (!model || !isAiConfigReady(config, model)) throw new Error("请先在设置中配置可用的图片模型");
            const result = await queueDramaAssetImages(projectId, { assetIds: visual.map((asset) => asset.id), imageTask: await assetImageTaskTemplate(config, model) });
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
        const textModel = effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const config = { ...effectiveConfig, model: textModel, textModel };
        if (!textModel || !isAiConfigReady(config, textModel)) {
            navigateToSettings({ continueCreation: true });
            return;
        }
        await continueProjectPipeline(project.id, config, project.defaultImageModel);
        await Promise.all([queryClient.invalidateQueries({ queryKey: dramaProgressKey(project.id) }), queryClient.invalidateQueries({ queryKey: projectTasksKey(project.id) })]);
    };

    const openGenerate = (shotId: string, targetUnitId = unitId) => {
        if (!canGenerate) {
            message.warning("还没配好可用的视频模型，先到设置里配好");
            return;
        }
        setChainTarget({ unitId: targetUnitId, shotId });
    };

    // 视频重试一律先开花费预览：预览里写明这次要出的范围（同场后面没出的段也可能一起出）和预估花费，确认后才提交。
    // 不直接调用链重试接口，因为它会从失败步骤接着把整条链往后跑，而且不给花费。
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
        return rail.units.map((item, index) => {
            if (unread) return { id: item.id, title: item.title || `第 ${index + 1} ${rail.noun}`, summary: pipelineQuery.isError ? "进度没读到" : "正在读进度…", warn: false };
            const facts = episodeFacts(item.id, segments, steps);
            return { id: item.id, title: item.title || `第 ${index + 1} ${rail.noun}`, summary: episodeFactsLine(facts, chainReadOf(item.id)), warn: mustUnits.has(item.id), completion: episodeCompletion(facts) };
        });
    }, [rail, segments, steps, inbox, pipelineQuery.isError, progress, chainReadOf]);
    const showRail = rail.units.length > 1 && (stage === "shots" || stage === "video");

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
                    {[rail.units.length ? `${rail.units.length} ${rail.noun}` : "", progress ? `${progress.counts.segments} 段` : "", project.aspectRatio, pipeline ? dramaRunModeLabel(pipeline.runMode) : ""].filter(Boolean).join(" · ")}
                </span>
                <div className="drama-flow-top-actions">
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
            <StageStrip stages={stages} facts={facts} active={stage} onSelect={(next) => go(next)} />
            <div className="drama-flow-main" data-rail={showRail ? undefined : "off"}>
                {showRail ? <EpisodeRail label={rail.label} episodes={railEpisodes} activeId={unitId} onSelect={(id) => go(stage, { ep: id, tab })} /> : null}
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
                        {stage === "shots" ? <StageShots key={unitId} data={data} stage={stageView} selectedId={focusId} onSelect={(shotId) => setParam("item", shotId)} /> : null}
                        {stage === "video" ? (
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
            {chainTarget ? (
                <VideoChainPanel
                    open
                    projectId={projectId}
                    whiteModelStoryboardEnabled={project.whiteModelStoryboardEnabled === true}
                    unitId={chainTarget.unitId}
                    shotId={chainTarget.shotId}
                    onClose={() => setChainTarget(undefined)}
                    onRefresh={refreshVideo}
                    buildModel={async () => {
                        const task = await prepareBackendGenerationTask({ projectId, mode: "video", prompt: "连续生成预览", config: videoConfig });
                        return { model: task.model || videoModel, logicalModelId: task.logicalModelId, input: task.input || {} };
                    }}
                />
            ) : null}
        </div>
    );
}
