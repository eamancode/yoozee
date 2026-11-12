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
import { findDramaModel, projectVideoModel, useDramaModels } from "@/pages/projects/workbench/drama-model";
import { dramaEpisodesKey, getDramaBible, getDramaBrief, getDramaEpisodes } from "@/services/api/project-drama";
import { listVideoChains, type VideoChain } from "@/services/api/project-video-chains";
import { getProjectCore, getProjectUnitWorkspace, listProjectAssets, listProjectUnits, type ProjectDetail } from "@/services/api/projects";
import { listGenerationTasks } from "@/services/api/task-center";

import { combineReadStates, queryReadState, videoStateRead, type ReadState } from "../model/read-state";
import { activeChainIds, latestVideoSteps, type ShotVideoStep } from "../model/video-steps";

export const videoChainsKey = (projectId: string, unitId: string) => ["project-video-chains", projectId, unitId] as const;
export const unitWorkspaceKey = (projectId: string, unitId: string) => ["project", projectId, "unit-workspace", unitId] as const;
/** 和旧章节页同一个 key：剧本优化、定画风、色卡、提取资产的控件都从这里认自己的任务。 */
export const projectTasksKey = (projectId: string) => ["project-chapter-generation-tasks", projectId] as const;

const activeTask = (status: string) => status === "queued" || status === "running";

/**
 * 主页面要的全部数据：项目、单元、资产、进度、出片链、当前集工作区、项目任务、风格档案、剧集表。
 * 并按旧工作台的方式拼出 ProjectDetail，好让旧的剧本优化、定画风、色卡、提取资产控件直接用。
 */
export function useFlowData(projectId: string, requestedUnitId: string) {
    const queryClient = useQueryClient();
    const effectiveConfig = useDramaModelConfig();

    const coreQuery = useQuery({ queryKey: ["project", projectId, "core"], queryFn: () => getProjectCore(projectId), enabled: Boolean(projectId), refetchOnMount: "always" });
    const unitsQuery = useQuery({ queryKey: ["project", projectId, "units"], queryFn: () => listProjectUnits(projectId), enabled: Boolean(projectId) });
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
    const episodeUnits = useMemo(() => allUnits.filter((unit) => unit.kind === "episode"), [allUnits]);
    const rail = useMemo(() => {
        const episodes = unitsOfKind(allUnits, "episode");
        return episodes.units.length ? episodes : unitsOfKind(allUnits, "chapter");
    }, [allUnits]);
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

    const segments = useMemo(() => progress?.segments || [], [progress]);
    const unitSegments = useMemo(() => segments.filter((item) => item.unitId === unitId), [segments, unitId]);
    const chainUnitIds = useMemo(() => [...new Set(segments.map((item) => item.unitId))], [segments]);
    const chainQueries = useQueries({
        queries: chainUnitIds.map((id) => ({
            queryKey: videoChainsKey(projectId, id),
            queryFn: async ({ signal }: { signal: AbortSignal }) => (await listVideoChains(projectId, id, signal)).chains,
            enabled: Boolean(projectId),
            refetchInterval: (query: { state: { data?: VideoChain[] } }) => (activeChainIds(query.state.data || []).length ? 8000 : false),
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
            const chains = query.data || [];
            byUnit.set(chainUnitIds[index], chains);
            for (const [shotId, step] of latestVideoSteps(chains)) merged.set(shotId, step);
            active.push(...activeChainIds(chains));
        });
        return { steps: merged, activeChains: active, chainsByUnit: byUnit };
        // chainQueries 每次渲染都是新数组，用各查询的更新时间做依赖。
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [chainsState]);
    // 视频出完一条，进度和挑片数据要跟上。
    useEffect(() => {
        if (!projectId || !chainsState) return;
        void queryClient.invalidateQueries({ queryKey: dramaProgressKey(projectId) });
        if (unitId) void queryClient.invalidateQueries({ queryKey: unitWorkspaceKey(projectId, unitId) });
    }, [projectId, unitId, chainsState, queryClient]);

    const dramaModels = useDramaModels();
    const dramaModel = project ? findDramaModel(dramaModels.data, project.generationModel) : undefined;
    const videoModel = project && dramaModel ? projectVideoModel(effectiveConfig, project.defaultVideoModel, dramaModels.data, dramaModel) : "";
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
        bible: bibleQuery.data,
        brief: briefQuery.data,
        episodes: episodesQuery.data,
        dramaModel,
        videoModel,
        segmentRule,
        dramaRule,
        detail,
        refreshVideo,
        refreshAll,
    };
}

export type FlowData = ReturnType<typeof useFlowData>;
