import { useCallback, useEffect, useMemo } from "react";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";

import { useDramaModelConfig } from "@/hooks/use-drama-model-config";
import { modelCapabilityConfigFor } from "@/lib/model-capabilities";
import { segmentDurationRule } from "@/pages/projects/workbench/detail/chapter-segments";
import { dramaBriefKey, segmentMaxSecondsWithBrief } from "@/pages/projects/workbench/detail/drama-brief";
import { dramaImageStatusOf } from "@/pages/projects/workbench/detail/drama-image-summary";
import { dramaBibleKey, dramaProgressKey } from "@/pages/projects/workbench/detail/drama-panels";
import { unitsOfKind } from "@/pages/projects/workbench/detail/unit-numbering";
import { useProjectPipeline } from "@/pages/projects/workbench/detail/use-project-pipeline";
import { useDramaModelAvailability } from "@/pages/projects/drama-model-picker";
import { findDramaModel, useDramaModels } from "@/pages/projects/workbench/drama-model";
import { dramaEpisodesKey, getDramaBible, getDramaBrief, getDramaEpisodes } from "@/services/api/project-drama";
import { videoChainsQuery, type VideoChain, type VideoChainList } from "@/services/api/project-video-chains";
import { projectVideoRoutingKey } from "@/services/api/project-video-routing";
import { getProjectCore, getProjectUnitWorkspace, listProjectAssets, listProjectUnits, type ProjectDetail } from "@/services/api/projects";
import { listGenerationTasks } from "@/services/api/task-center";
import { configuredModelMatchesCapability } from "@/stores/use-config-store";
import { useDramaModelPreferencesStore, type DramaModelCapability } from "@/stores/use-drama-model-preferences-store";

import { flowImageModel, flowTextModel, flowVideoModel, stepModelStatus, videoFamilyMismatch } from "../model/step-models";
import { styleLockState } from "../model/style-lock";
import { combineReadStates, queryReadState, videoStateRead, type ReadState } from "../model/read-state";
import { activeChainIds, latestVideoSteps, type ShotVideoStep } from "../model/video-steps";
import { useModelNames, type ModelName } from "./use-model-names";
import { useProjectVideoRouting } from "./use-project-video-routing";

export const unitWorkspaceKey = (projectId: string, unitId: string) => ["project", projectId, "unit-workspace", unitId] as const;
/** 和旧章节页同一个 key：剧本优化、定画风、色卡、提取资产的控件都从这里认自己的任务。 */
export const projectTasksKey = (projectId: string) => ["project-chapter-generation-tasks", projectId] as const;

const activeTask = (status: string) => status === "queued" || status === "running";

/** 一步要用的一类模型：chosen 是用户原来选的（上次用的或项目默认，未校验），effective 是这次实际会提交的。 */
export type StepModel = {
    capability: DramaModelCapability;
    chosen: string;
    effective: string;
    select?: (model: string) => void;
    /** 这一步不能用这个模型的原因（个人渠道、平台不可用、画幅不支持）；能用为空串。 */
    disabledReason: (model: string) => string;
    /** 给人看的状态：unset 是既没原选择也没有可用模型时写的话。 */
    status: (unset: string) => ModelName;
    /** 模型目录读到没有：没读到时不能把模型判成下架，也不能把空的实际模型判成没配。 */
    catalogRead: ReadState;
    /** 模型目录没读到时重读。 */
    retryCatalog?: () => void;
};

/**
 * 主页面要的全部数据：项目、单元、资产、进度、出片链、当前集工作区、项目任务、风格档案、剧集表。
 * 并按旧工作台的方式拼出 ProjectDetail，好让旧的剧本优化、定画风、色卡、提取资产控件直接用。
 */
export function useFlowData(projectId: string, requestedUnitId: string, watchVideoRouting = false) {
    const queryClient = useQueryClient();
    const effectiveConfig = useDramaModelConfig();
    const recentModels = useDramaModelPreferencesStore((state) => state.models);
    const videoRouting = useProjectVideoRouting(projectId, watchVideoRouting);

    const coreQuery = useQuery({ queryKey: ["project", projectId, "core"], queryFn: () => getProjectCore(projectId), enabled: Boolean(projectId), refetchOnMount: "always" });
    const unitsQuery = useQuery({ queryKey: ["project", projectId, "units"], queryFn: () => listProjectUnits(projectId), enabled: Boolean(projectId), refetchOnMount: "always" });
    const assetsQuery = useQuery({
        queryKey: ["project", projectId, "assets"],
        queryFn: () => listProjectAssets(projectId),
        enabled: Boolean(projectId),
        refetchInterval: (query) => (query.state.data?.assets.some((asset) => ["queued", "waiting"].includes(dramaImageStatusOf(asset) || "")) ? 5000 : false),
    });
    const pipelineQuery = useProjectPipeline(projectId);
    const tasksQuery = useQuery({
        queryKey: projectTasksKey(projectId),
        queryFn: () => listGenerationTasks(100, { projectId }),
        enabled: Boolean(projectId),
        refetchInterval: (query) => (query.state.data?.some((task) => activeTask(task.status)) ? 4000 : false),
        refetchOnWindowFocus: true,
    });
    const bibleQuery = useQuery({ queryKey: dramaBibleKey(projectId), queryFn: async () => (await getDramaBible(projectId)).document, enabled: Boolean(projectId) });
    const briefQuery = useQuery({ queryKey: dramaBriefKey(projectId), queryFn: async () => (await getDramaBrief(projectId)).document, enabled: Boolean(projectId) });
    const episodesQuery = useQuery({ queryKey: dramaEpisodesKey(projectId), queryFn: ({ signal }) => getDramaEpisodes(projectId, signal), enabled: Boolean(projectId) });

    const progress = pipelineQuery.data;
    const pipeline = progress?.pipeline;
    const project = coreQuery.data?.project;

    // 自动环节在后台往前走：流水线一变，就把这个项目除进度外的数据都重新读一遍。
    const pipelineState = JSON.stringify(pipeline);
    useEffect(() => {
        if (!projectId || !pipelineState) return;
        void queryClient.invalidateQueries({ queryKey: ["project", projectId], predicate: (query) => query.queryKey[2] !== "drama" || query.queryKey[3] !== "progress" });
        void queryClient.invalidateQueries({ queryKey: projectTasksKey(projectId) });
    }, [projectId, pipelineState, queryClient]);

    const allUnits = useMemo(() => (unitsQuery.data?.units || []).slice().sort((left, right) => left.position - right.position), [unitsQuery.data]);
    const chapters = useMemo(() => allUnits.filter((unit) => unit.kind !== "episode"), [allUnits]);
    const rail = useMemo(() => unitsOfKind(allUnits, "episode"), [allUnits]);
    const episodeUnits = rail.units;
    const unit = rail.units.find((item) => item.id === requestedUnitId) || rail.units[0];
    const unitId = unit?.id || "";

    const workspaceQuery = useQuery({
        queryKey: unitWorkspaceKey(projectId, unitId),
        queryFn: () => getProjectUnitWorkspace(projectId, unitId),
        enabled: Boolean(projectId && unitId),
        refetchInterval: (query) => (query.state.data?.tasks.some((task) => task.clientContext?.shotId && activeTask(task.status)) ? 3000 : false),
    });
    const workspace = workspaceQuery.data;
    /** 单元列表接口只给摘要（不带正文）；当前集的完整单元只能从 workspace 拿，没拿到之前不显示也不保存正文。 */
    const fullUnit = workspace?.unit && workspace.unit.id === unitId ? workspace.unit : undefined;

    const segments = useMemo(() => {
        const episodeIds = new Set(episodeUnits.map((episode) => episode.id));
        return (progress?.segments || []).filter((segment) => episodeIds.has(segment.unitId));
    }, [progress, episodeUnits]);
    // 旧工作台的全项目进度也含原文章节上的镜头。新页的总数与实际可选、可出片的段使用同一范围。
    const episodeCounts = useMemo(() => {
        if (!progress || !unitsQuery.data) return undefined;
        if (segments.length === (progress.segments || []).length) return progress.counts;
        return {
            segments: segments.length,
            generated: segments.filter((segment) => segment.generated).length,
            inCut: segments.filter((segment) => segment.inCut).length,
            accepted: segments.filter((segment) => segment.accepted).length,
            overLimit: segments.filter((segment) => segment.overLimit).length,
            issues: segments.filter((segment) => (segment.issues ?? 0) > 0).length,
        };
    }, [segments, progress, unitsQuery.data]);
    const unitSegments = useMemo(() => segments.filter((item) => item.unitId === unitId), [segments, unitId]);
    const chainUnitIds = useMemo(() => [...new Set(segments.map((item) => item.unitId))], [segments]);
    const chainQueries = useQueries({
        // 和连续生成面板共用同一个 key 和同一种缓存形状（接口原样的 { chains }），这里读 .chains 拿数组。
        queries: chainUnitIds.map((id) => ({
            ...videoChainsQuery(projectId, id),
            enabled: Boolean(projectId),
            refetchInterval: (query: { state: { data?: VideoChainList } }) => (activeChainIds(query.state.data?.chains || []).length ? 8000 : false),
        })),
    });
    const chainsState = chainQueries.map((query) => query.dataUpdatedAt).join(",");
    const chainsError = chainQueries.some((query) => query.isError);
    // 出片状态要进度和对应集的出片链都读到才能算；没读到或还在读时，页面不能把生成中、排队、失败写成 0。
    const progressRead: ReadState = queryReadState(pipelineQuery);
    const chainReadKey = chainQueries.map((query) => queryReadState(query)).join(",");
    const chainReads = useMemo(() => {
        const states = chainReadKey ? (chainReadKey.split(",") as ReadState[]) : [];
        return new Map(chainUnitIds.map((id, index) => [id, states[index]]));
    }, [chainUnitIds, chainReadKey]);
    /** 某一集的出片状态能不能算。 */
    const chainReadOf = useCallback((id: string) => videoStateRead(progressRead, chainReads.get(id)), [progressRead, chainReads]);
    /** 全项目的出片状态能不能算（阶段条、待办计数用）。 */
    const videosRead = progressRead === "ready" ? combineReadStates([...chainReads.values()]) : progressRead;
    const { steps, activeChains, chainsByUnit } = useMemo(() => {
        const merged = new Map<string, ShotVideoStep>();
        const active: string[] = [];
        const byUnit = new Map<string, VideoChain[]>();
        chainQueries.forEach((query, index) => {
            const chains = query.data?.chains || [];
            byUnit.set(chainUnitIds[index], chains);
            for (const [shotId, step] of latestVideoSteps(chains)) merged.set(shotId, step);
            active.push(...activeChainIds(chains));
        });
        return { steps: merged, activeChains: active, chainsByUnit: byUnit };
        // chainQueries 每次渲染都是新数组，用各查询的更新时间做依赖。
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [chainsState, chainUnitIds]);
    // 视频出完一条，进度和挑片数据要跟上。
    useEffect(() => {
        if (!projectId || !chainsState) return;
        void queryClient.invalidateQueries({ queryKey: projectVideoRoutingKey(projectId) });
        void queryClient.invalidateQueries({ queryKey: dramaProgressKey(projectId) });
        if (unitId) void queryClient.invalidateQueries({ queryKey: unitWorkspaceKey(projectId, unitId) });
    }, [projectId, unitId, chainsState, queryClient]);

    const dramaModels = useDramaModels();
    const dramaModel = project ? findDramaModel(dramaModels.data, project.generationModel) : undefined;
    // 各步骤要用的模型：模型卡显示、确认面板默认值和提交都用这几个值（规则见 model/step-models.ts）。
    // 只取这一步能用的：短剧在服务端执行（个人渠道不行）、平台目录里可用、视频给得出项目画幅，且模型目录没确认下架。
    const modelNames = useModelNames(true);
    const textAvailability = useDramaModelAvailability("text");
    const imageAvailability = useDramaModelAvailability("image");
    const videoAvailability = useDramaModelAvailability("video", { aspectRatio: project?.aspectRatio });
    function resolveStep(capability: DramaModelCapability, chosen: string, availability: ReturnType<typeof useDramaModelAvailability>, resolve: (accepts: (model: string) => boolean) => string): StepModel {
        const directory = availability.catalogQuery;
        const options = directory.data?.options?.[capability];
        const capabilityRead = directory.data && !Array.isArray(options) ? "error" : queryReadState(directory);
        const catalogRead = combineReadStates([modelNames.catalogRead, capabilityRead]);
        const nameOf = (model: string | undefined, unset: string) => modelNames.nameOf(model, unset, catalogRead);
        const disabledReason = (model: string) => {
            if (catalogRead === "loading") return "正在读取模型目录，请稍候";
            if (catalogRead === "error") return "模型目录没读到，没法确认现在是否可用";
            if (!model || nameOf(model, "").tone === "bad") return "模型已不可用";
            return availability.disabledReason(model);
        };
        const effective = resolve((model) => !disabledReason(model));
        return {
            capability,
            chosen,
            effective,
            disabledReason,
            catalogRead,
            status: (unset) => stepModelStatus({ chosen, effective, catalogRead, nameOf, disabledReason, unset }),
            retryCatalog:
                catalogRead === "error"
                    ? () => {
                          if (modelNames.catalogRead === "error") void modelNames.refetch();
                          if (capabilityRead === "error") void directory.refetch();
                      }
                    : undefined,
        };
    }
    const route = videoRouting.route;
    const videoStep = resolveStep("video", route?.model || recentModels.video?.trim() || project?.defaultVideoModel?.trim() || "", videoAvailability, (accepts) => {
        if (videoRouting.issue || !project) return "";
        if (route) return configuredModelMatchesCapability(effectiveConfig, route.model, "video") && accepts(route.model) ? route.model : "";
        return dramaModel ? flowVideoModel(effectiveConfig, recentModels, project.defaultVideoModel, dramaModels.data, dramaModel, accepts) : "";
    });
    const stepModels = {
        text: resolveStep("text", recentModels.text?.trim() || "", textAvailability, (accepts) => flowTextModel(effectiveConfig, recentModels, accepts)),
        image: resolveStep("image", recentModels.image?.trim() || project?.defaultImageModel?.trim() || "", imageAvailability, (accepts) => (project ? flowImageModel(effectiveConfig, recentModels, project.defaultImageModel, accepts) : "")),
        video: {
            ...videoStep,
            select: (model: string) => videoRouting.select(model, route?.model || videoStep.effective),
            disabledReason: (model: string) => videoRouting.issue || videoStep.disabledReason(model),
            status: (unset: string) => videoRouting.issue ? { text: videoRouting.issue, tone: "warn" as const } : videoStep.status(unset),
            retryCatalog: videoRouting.retry || videoStep.retryCatalog,
        },
    };
    const {
        text: { effective: textModel },
        image: { effective: imageModel },
        video: { effective: videoModel },
    } = stepModels;
    // 视频模型可以换到项目生成模型以外的族；换了要提醒分段规则是按原来那一族装的。
    const videoOffFamily = useMemo(() => videoFamilyMismatch(effectiveConfig, videoModel, dramaModels.data, dramaModel), [effectiveConfig, videoModel, dramaModels.data, dramaModel]);
    const segmentLimit = segmentMaxSecondsWithBrief(dramaModel?.duration.max || 0, briefQuery.data);
    const segmentRule = useMemo(
        () => (dramaModel ? segmentDurationRule(segmentLimit.seconds, videoModel ? modelCapabilityConfigFor(effectiveConfig, videoModel).video?.duration : undefined, segmentLimit.byBrief ? "项目设置" : dramaModel.label) : undefined),
        [dramaModel, segmentLimit.seconds, segmentLimit.byBrief, videoModel, effectiveConfig],
    );
    const dramaRule = useMemo(() => (segmentRule ? { maxSeconds: segmentRule.maxSeconds, minSeconds: segmentRule.minSeconds } : undefined), [segmentRule]);

    const assets = useMemo(() => assetsQuery.data?.assets || [], [assetsQuery.data]);
    const projectTasks = useMemo(() => tasksQuery.data || [], [tasksQuery.data]);
    const detail: ProjectDetail | undefined = useMemo(
        () =>
            project
                ? {
                      project,
                      units: workspace?.unit ? allUnits.map((item) => (item.id === workspace.unit.id ? workspace.unit : item)) : allUnits,
                      canvases: [],
                      canvasUnitLinks: [],
                      unitCanvasCounts: unitsQuery.data?.canvasCounts || {},
                      assets: workspace?.assets || [],
                      assetFolders: [],
                      workflows: workspace?.workflows || [],
                      shots: workspace?.shots || [],
                      shotRevisions: workspace?.shotRevisions || [],
                      shotArtifacts: workspace?.shotArtifacts || [],
                      shotReferences: workspace?.shotReferences || [],
                      assetCandidates: workspace?.assetCandidates || [],
                      tasks: workspace?.tasks || [],
                  }
                : undefined,
        [project, workspace, allUnits, unitsQuery.data],
    );

    const refreshVideo = useCallback(
        () =>
            Promise.all([
                queryClient.invalidateQueries({ queryKey: projectVideoRoutingKey(projectId) }),
                queryClient.invalidateQueries({ queryKey: ["project-video-chains", projectId] }),
                queryClient.invalidateQueries({ queryKey: dramaProgressKey(projectId) }),
                queryClient.invalidateQueries({ queryKey: ["project", projectId, "unit-workspace"] }),
            ]),
        [queryClient, projectId],
    );
    const refreshAll = useCallback(
        () => Promise.all([queryClient.invalidateQueries({ queryKey: ["project", projectId] }), queryClient.invalidateQueries({ queryKey: projectTasksKey(projectId) }), queryClient.invalidateQueries({ queryKey: ["project-video-chains", projectId] })]),
        [queryClient, projectId],
    );

    return {
        coreQuery,
        unitsQuery,
        assetsQuery,
        pipelineQuery,
        tasksQuery,
        bibleQuery,
        briefQuery,
        episodesQuery,
        workspaceQuery,
        project,
        progress,
        episodeCounts,
        pipeline,
        allUnits,
        chapters,
        episodeUnits,
        rail,
        unit,
        fullUnit,
        unitId,
        workspace,
        segments,
        unitSegments,
        steps,
        activeChains,
        chainsByUnit,
        chainsError,
        progressRead,
        chainReadOf,
        videosRead,
        assets,
        projectTasks,
        /** 运行记录（最近 100 次）能不能拿来算花费：默认的空数组不等于「读到了空列表」。 */
        tasksRead: queryReadState(tasksQuery),
        bible: bibleQuery.data,
        /** 画风锁定状态：和旧工作台同一口径，读失败单独一态。 */
        styleLock: styleLockState(bibleQuery),
        brief: briefQuery.data,
        episodes: episodesQuery.data,
        dramaModel,
        /** 各步骤的模型卡、信息面板读这里（原选择、实际提交、状态）。 */
        stepModels,
        videoRouting,
        videoModel,
        /** 视频模型不属于项目生成模型那一族时是它所属的族（认不出为 null）；同族为 undefined。 */
        videoOffFamily,
        imageModel,
        textModel,
        segmentRule,
        dramaRule,
        detail,
        refreshVideo,
        refreshAll,
    };
}

export type FlowData = ReturnType<typeof useFlowData>;
