import { App, Button, Dropdown, Form, Input, InputNumber, Modal, Popconfirm } from "antd";
import { AppModal } from "@/components/ui/product/app-modal";
import { Tooltip } from "@/components/ui/base/tooltip";
import { useDeferredValue, useEffect, useMemo, useRef, useState, type ChangeEvent, type DragEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";

import { Callout } from "@/components/ui/product/callout";
import CharacterCount from "@tiptap/extension-character-count";
import Color from "@tiptap/extension-color";
import Highlight from "@tiptap/extension-highlight";
import Placeholder from "@tiptap/extension-placeholder";
import TextAlign from "@tiptap/extension-text-align";
import { TextStyle } from "@tiptap/extension-text-style";
import { EditorContent, useEditor, type Editor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import {
    AlignCenter,
    AlignJustify,
    AlignLeft,
    AlignRight,
    Bold,
    Check,
    ChevronDown,
    Clapperboard,
    Code2,
    Crosshair,
    Eraser,
    FileUp,
    GripVertical,
    Highlighter,
    Italic,
    Link2,
    List,
    ListOrdered,
    Minus,
    MoreHorizontal,
    MoveVertical,
    Plus,
    Quote,
    Redo2,
    Save,
    Search,
    Strikethrough,
    Trash2,
    Underline,
    Undo2,
    X,
} from "lucide-react";

import { useNavigate, useParams } from "react-router";

import { ModelPicker } from "@/components/model-picker";
import { WorkspaceState } from "@/components/layout/workspace-state";
import { parseStyleProfile } from "@/lib/canvas/style-profile";
import { decodeNovelText, splitTextIntoChapters } from "@/lib/canvas/canvas-document";
import { navigateToSettings } from "@/lib/settings-navigation";
import { modelCapabilityConfigFor } from "@/lib/model-capabilities";
import {
    createProjectUnit,
    deleteProjectUnit,
    getProjectUnit,
    importProjectUnits,
    listProjectAssetCandidates,
    listProjectAssets,
    reorderProjectUnits,
    updateProjectUnit,
    type ProjectDetail,
    type ProjectUnit,
} from "@/services/api/projects";
import { useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";
import { listGenerationTasks, queryGenerationTask, type GenerationTask } from "@/services/api/task-center";
import { dramaEpisodesKey, dramaScenesKey, dramaRunProgressOf, dramaShotGenreLabel, dramaStepOutcomeOf, getDramaBrief, getDramaEpisodes, getDramaScenes, getDramaShotlist, getDramaStepUsage, repackDramaShotlist, shotlistCheckFailure, type DramaScriptAdaptOutcome } from "@/services/api/drama";

import { formatCount, formatTime, statusLabel, type ProjectDetailViewProps } from "./shared";
import { chapterStoryboardReplaceImpact } from "./chapter-storyboard-production";
import { chapterDialogueCoverage, segmentDurationRule, segmentTargetLabel } from "./chapter-segments";
import { dramaBriefKey, segmentMaxSecondsWithBrief } from "./drama-brief";
import { latestStoryboardWrite, parseShotStream, shotStreamFromResult, storyboardRunStop } from "./chapter-shot-stream";
import { ChapterStoryboardRunPanel } from "./chapter-storyboard-run-panel";
import { findDramaModel, projectVideoModel, useDramaModels } from "../drama-model";
import { appendProjectUnits } from "./project-cache";
import { ImportChapterPreview, MAX_NOVEL_IMPORT_CHAPTERS } from "../import-chapter-preview";
import { plainTextToHtml } from "@/lib/project-source";
import { chapterTaskIdentity, followDramaRunTask, generateChapterStoryboard, isScenePaletteTask, isScriptAdaptTask, isStyleLockTask } from "./project-chapter-ai";
import { AssetsExtractControl } from "./chapter-assets-extract";
import { ScriptAdaptControl } from "./chapter-script-adapt";
import { followChainedScenePalette, ScenePaletteControl } from "./chapter-scene-palette";
import { projectLockedStyle, StyleLockControl } from "./chapter-style-lock";
import { ChapterSceneList } from "./chapter-scene-list";
import { withEpisodeSourcesComment } from "./episode-sources";
import { projectSceneCode, unitEpisodeNumber } from "./unit-numbering";

const CHAPTER_ROW_HEIGHT = 62;
type ChapterOperationKind = "storyboard";
// queued：任务还在排队（或刚提交、还没开始跑），按钮显示「排队中」而不是从 0 秒开始的「已运行」。
type ChapterOperation = { startedAt: number; taskId?: string; queued?: boolean };
// 生成分段的文本流（各场输出与回合标记），按章节存，侧栏解析成按场分组的镜头。
type StoryboardStream = { taskId: string; text: string };
// 章节页左栏看哪一类单元：原文章节（kind=chapter）或剧集（kind=episode）。
type UnitView = "chapter" | "episode";

function formatChapterListCount(value: number) {
    if (value < 10_000) return formatCount(value);
    return new Intl.NumberFormat("zh-CN", { notation: "compact", maximumFractionDigits: 1 }).format(value);
}

export default function ProjectChaptersView({ detail, refreshProject }: ProjectDetailViewProps) {
    const { message, modal } = App.useApp();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const { chapterId = "" } = useParams();
    const initialSelectedId = detail.units.some((unit) => unit.id === chapterId) ? chapterId : detail.units[0]?.id || "";
    const [selectedId, setSelectedId] = useState(initialSelectedId);
    // 有剧集或是导入剧本的项目时默认看剧集；从地址打开某一章时看它所在的那一类。
    const [unitView, setUnitView] = useState<UnitView>(() => {
        const opened = detail.units.find((unit) => unit.id === initialSelectedId);
        if (opened) return opened.kind === "episode" ? "episode" : "chapter";
        return detail.project.sourceType === "script" || detail.units.some((unit) => unit.kind === "episode") ? "episode" : "chapter";
    });
    const [createOpen, setCreateOpen] = useState(false);
    const [importOpen, setImportOpen] = useState(false);
    const [searchQuery, setSearchQuery] = useState("");
    const [moveTargetId, setMoveTargetId] = useState("");
    const [movePosition, setMovePosition] = useState<number | null>(null);
    const [orderedIds, setOrderedIds] = useState(() => detail.units.map((unit) => unit.id));
    const [draggedId, setDraggedId] = useState("");
    const [draftTitle, setDraftTitle] = useState("");
    const [draftHtml, setDraftHtml] = useState("");
    const [dirty, setDirty] = useState(false);
    const [storyboardOpen, setStoryboardOpen] = useState(false);
    // 「重新生成问题小节」打开同一个生成弹窗，只重写这几节；为空是整章生成。
    const [storyboardOnlySections, setStoryboardOnlySections] = useState<string[]>([]);


    const [chapterOperations, setChapterOperations] = useState<Record<string, ChapterOperation>>({});
    const [completedChapterOperations, setCompletedChapterOperations] = useState<Record<string, true>>({});
    const [operationNow, setOperationNow] = useState(() => Date.now());
    const [storyboardStreams, setStoryboardStreams] = useState<Record<string, StoryboardStream>>({});
    const [runPanelOpen, setRunPanelOpen] = useState(false);
    const followedStoryboardTasksRef = useRef(new Set<string>());
    const [selectedTextModel, setSelectedTextModel] = useState("");
    const effectiveConfig = useEffectiveConfig();
    const isAiConfigReady = useConfigStore((state) => state.isAiConfigReady);
    const listRef = useRef<HTMLDivElement>(null);
    const locallyOwnedTaskIdsRef = useRef(new Set<string>());
    const recoveredTaskIdsRef = useRef(new Set<string>());
    const deferredSearchQuery = useDeferredValue(searchQuery.trim().toLocaleLowerCase("zh-CN"));
    const orderedUnits = useMemo(() => {
        const byId = new Map(detail.units.map((unit) => [unit.id, unit]));
        return orderedIds.map((id) => byId.get(id)).filter((unit): unit is ProjectUnit => Boolean(unit));
    }, [detail.units, orderedIds]);
    const selectedUnitSummary = detail.units.find((unit) => unit.id === selectedId) || orderedUnits[0];
    const chapterTasksQuery = useQuery({
        queryKey: ["project-chapter-generation-tasks", detail.project.id],
        queryFn: () => listGenerationTasks(100, { projectId: detail.project.id }),
        refetchInterval: (query) => query.state.data?.some((task) => {
            const identity = chapterTaskIdentity(task);
            return Boolean((identity || isScriptAdaptTask(task) || isScenePaletteTask(task) || isStyleLockTask(task)) && (task.status === "queued" || task.status === "running"));
        }) ? 2_000 : false,
        refetchOnWindowFocus: true,
    });
    const selectedUnit = selectedUnitSummary;
    // 小节、场、镜头号按项目编号显示（第 2 集写 E02S01-P1，和场次条一致）；传给后端的仍是单元内编号。
    const selectedEpisode = selectedUnit ? unitEpisodeNumber(detail.units, selectedUnit.id) : 0;
    const shownSections = storyboardOnlySections.map((section) => projectSceneCode(section, selectedEpisode));
    const unitKind = (unit: ProjectUnit): UnitView => (unit.kind === "episode" ? "episode" : "chapter");
    const chapterUnits = useMemo(() => orderedUnits.filter((unit) => unitKind(unit) === "chapter"), [orderedUnits]);
    const episodeUnits = useMemo(() => orderedUnits.filter((unit) => unitKind(unit) === "episode"), [orderedUnits]);
    const viewUnits = unitView === "episode" ? episodeUnits : chapterUnits;
    // 章与集各自编号。
    const chapterNumberById = useMemo(() => new Map([...chapterUnits.map((unit, index) => [unit.id, index + 1] as const), ...episodeUnits.map((unit, index) => [unit.id, index + 1] as const)]), [chapterUnits, episodeUnits]);
    const unitNoun = unitView === "episode" ? "集" : "章";
    // 对话框里称呼当前单元：剧集叫「剧集」，原文叫「章节」。
    const selectedUnitNoun = selectedUnit && unitKind(selectedUnit) === "episode" ? "剧集" : "章节";
    // 改编出剧集后原文只读：改原文要重新优化，否则剧集和原文对不上。
    const originalLocked = Boolean(selectedUnit && unitKind(selectedUnit) === "chapter" && episodeUnits.length);
    const episodesQuery = useQuery({ queryKey: dramaEpisodesKey(detail.project.id), queryFn: ({ signal }) => getDramaEpisodes(detail.project.id, signal), enabled: episodeUnits.length > 0 });
    const unitTitleById = useMemo(() => new Map(orderedUnits.map((unit) => [unit.id, unit.title])), [orderedUnits]);
    // 由当前原文章节改编出的剧集（来源注释或主要来源指向它）。
    const adaptedEpisodes = useMemo(() => {
        if (!selectedUnit || unitKind(selectedUnit) !== "chapter" || !episodesQuery.data) return [];
        return episodesQuery.data.episodes.filter((episode) => episode.parentId === selectedUnit.id || episode.sources.some((source) => source.unitId === selectedUnit.id));
    }, [episodesQuery.data, selectedUnit]);
    // 已改编的原文上不再提取资产、生成分段（会在原文上另出一套段），到剧集上做；剧集清单还没读到时先按已改编处理。
    const adaptedOriginal = originalLocked && (episodesQuery.isPending || adaptedEpisodes.length > 0);
    const adaptedEpisodeTarget = adaptedEpisodes[0];
    const adaptedEpisodeLabel = adaptedEpisodeTarget ? `第 ${chapterNumberById.get(adaptedEpisodeTarget.unitId) || 1} 集` : "剧集";
    const adaptedOriginalHint = "已改编成剧集，请到剧集上操作";
    // 原文与剧集互相跳转：剧集列来源章节和段号，原文章节列改编出的剧集。
    const unitLinks = useMemo(() => {
        if (!selectedUnit || !episodesQuery.data) return [];
        if (unitKind(selectedUnit) === "episode") {
            const episode = episodesQuery.data.episodes.find((item) => item.unitId === selectedUnit.id);
            return (episode?.sources || []).filter((source) => unitTitleById.has(source.unitId)).map((source) => ({ unitId: source.unitId, label: `${unitTitleById.get(source.unitId)}${source.from ? ` ¶${source.from}-${source.to}` : ""}` }));
        }
        return adaptedEpisodes.map((episode) => ({ unitId: episode.unitId, label: episode.title }));
    }, [adaptedEpisodes, episodesQuery.data, selectedUnit, unitTitleById]);
    const canvasCountByUnitId = useMemo(() => new Map(Object.entries(detail.unitCanvasCounts || {}).map(([unitId, count]) => [unitId, Number(count)])), [detail.unitCanvasCounts]);
    const storyboardImpact = useMemo(() => chapterStoryboardReplaceImpact(detail, selectedUnit?.id || ""), [detail, selectedUnit?.id]);
    const dramaModels = useDramaModels();
    const dramaModel = findDramaModel(dramaModels.data, detail.project.generationModel);
    // 一段最长取生成模型上限和项目单段时长（需求文档 segment_seconds）里更严的，和后端装段同一口径。
    const briefQuery = useQuery({ queryKey: dramaBriefKey(detail.project.id), queryFn: async () => (await getDramaBrief(detail.project.id)).document, enabled: Boolean(dramaModel) });
    const modelSegmentMaxSeconds = dramaModel?.duration.max || 15;
    const segmentLimit = segmentMaxSecondsWithBrief(modelSegmentMaxSeconds, briefQuery.data);
    const segmentMaxSeconds = segmentLimit.seconds;
    const segmentLimitLabel = segmentLimit.byBrief ? "项目设置" : dramaModel?.label;
    // 段长还要能被所选视频渠道提交（如只能 15/30 秒、最少 5 秒）：渠道取分镜制作页默认会用的那个视频模型。
    const segmentVideoModel = projectVideoModel(effectiveConfig, detail.project.defaultVideoModel, dramaModels.data, dramaModel);
    const segmentRule = useMemo(() => segmentDurationRule(segmentMaxSeconds, segmentVideoModel ? modelCapabilityConfigFor(effectiveConfig, segmentVideoModel).video?.duration : undefined, segmentLimitLabel), [effectiveConfig, segmentLimitLabel, segmentMaxSeconds, segmentVideoModel]);
    // 生成分段、装段、步骤用法（弹窗的戏类）和场次表（场次条节数）都带这一个 rule：后端按它选戏类、切小节，四处一致。
    const dramaRule = useMemo(() => ({ maxSeconds: segmentRule.maxSeconds, minSeconds: segmentRule.minSeconds }), [segmentRule.maxSeconds, segmentRule.minSeconds]);
    // 本章工作区只带本章镜头已引用的资产；生成分镜和装段要看项目里全部已确认的资产，否则新章节的段带不上参考。
    const projectAssetsKey = ["project", detail.project.id, "assets", "chapter-storyboard"] as const;
    const loadProjectAssets = async () => (await listProjectAssets(detail.project.id)).assets;
    const projectAssetsQuery = useQuery({ queryKey: projectAssetsKey, queryFn: loadProjectAssets, enabled: storyboardOpen });
    const pendingCandidatesQuery = useQuery({
        queryKey: ["project", detail.project.id, "asset-candidates", "pending-total"],
        queryFn: () => listProjectAssetCandidates(detail.project.id, { page: 1, pageSize: 1, status: "pending_confirmation" }),
        enabled: storyboardOpen,
    });
    const pendingCandidateCount = pendingCandidatesQuery.data?.total ?? detail.assetCandidates.filter((candidate) => candidate.status === "pending_confirmation").length;
    const referenceAssetCount = (projectAssetsQuery.data || []).filter((asset) => asset.primaryVersionId && (asset.category !== "character" || asset.character?.versionId)).length;
    const assetsNotReady = projectAssetsQuery.isSuccess && (pendingCandidateCount > 0 || referenceAssetCount === 0);
    // 建项目时画风可以先空着，但生成分镜要按画风写画面：在对话框里直接说明并给入口，而不是提交后顶部闪一下。
    // 提示词在服务端组装，画风原文取项目保存的画风快照；只存了预设 ID 的老项目要在项目设置里重新保存一次画风。
    // 分镜按项目风格写画面：锁定了风格（导演定画风或从风格中心挑）才能生成；改版前带画风原文的老项目照旧可用。
    const projectStyleMissing = !projectLockedStyle(detail.project.bibleJson).locked && !parseStyleProfile(detail.project.styleProfileJson)?.prompt.trim();
    // 生成分段用哪个技能由项目风格档案绑定（后台「流程技能」），对话框只显示，不让选。
    const storyboardStepQuery = useQuery({
        queryKey: ["project", detail.project.id, "drama-step", "chapter.shots", detail.project.styleProfile || "", dramaRule.maxSeconds, dramaRule.minSeconds],
        queryFn: ({ signal }) => getDramaStepUsage(detail.project.id, "chapter.shots", signal, dramaRule).then((result) => result.usage),
        enabled: storyboardOpen,
    });
    const storyboardStep = storyboardStepQuery.data;
    // 镜头表由服务端在生成分段任务收尾时写：检查有 error 时只存草稿（draft），页面列出问题行并给「重新装段」。
    const selectedUnitId = selectedUnit?.id || "";
    const shotlistQuery = useQuery({
        queryKey: ["project", detail.project.id, "drama-shotlist", selectedUnitId],
        queryFn: ({ signal }) => getDramaShotlist(detail.project.id, selectedUnitId, signal).then((result) => result.shotlist),
        enabled: Boolean(selectedUnitId),
    });
    const shotlist = shotlistQuery.data;
    // 场次表按已保存的正文实时切：正文保存时间变了重新读；规则改变后旧数据编号错位时带 notice（正文上方和生成分段侧栏提示）。
    const scenesQuery = useQuery({
        queryKey: [...dramaScenesKey(detail.project.id, selectedUnitId), selectedUnit?.updatedAt || "", dramaRule.maxSeconds, dramaRule.minSeconds],
        queryFn: ({ signal }) => getDramaScenes(detail.project.id, selectedUnitId, signal, dramaRule),
        enabled: Boolean(selectedUnitId),
    });
    const chapterStoryboardTasks = useMemo(() => (chapterTasksQuery.data || []).filter((task) => {
        const identity = chapterTaskIdentity(task);
        return identity?.kind === "storyboard" && identity.chapterId === selectedUnitId;
    }), [chapterTasksQuery.data, selectedUnitId]);
    const latestStoryboardTask = chapterStoryboardTasks[0];
    // 最近一次成功写入：之后的重跑停下了不给续写，侧栏说明当前结果来自那次运行。
    const lastStoryboardWrite = useMemo(() => latestStoryboardWrite(chapterStoryboardTasks), [chapterStoryboardTasks]);
    const latestStoryboardTerminal = latestStoryboardTask && latestStoryboardTask.status !== "queued" && latestStoryboardTask.status !== "running" ? latestStoryboardTask : undefined;
    // 结束了的任务读详情（带结果）：成功的取写库摘要，失败的取已写完几场给「从第 N 场继续」，一场没写完的给失败原因。
    const storyboardTaskDetailQuery = useQuery({
        queryKey: ["generation-task", latestStoryboardTerminal?.id, latestStoryboardTerminal?.status],
        queryFn: () => queryGenerationTask(latestStoryboardTerminal!.id),
        enabled: Boolean(latestStoryboardTerminal),
        staleTime: Infinity,
    });
    const storyboardTaskDetail = storyboardTaskDetailQuery.data;
    const storyboardOutcome = storyboardTaskDetail?.status === "succeeded" ? dramaStepOutcomeOf(storyboardTaskDetail) : undefined;
    const storyboardStop = useMemo(() => (storyboardTaskDetail ? storyboardRunStop(storyboardTaskDetail, dramaRunProgressOf(storyboardTaskDetail), lastStoryboardWrite, { refinalize: true }) : undefined), [lastStoryboardWrite, storyboardTaskDetail]);
    // 「从第 N 节继续」和各节都写完后的「重新写入」都是接着上一次任务建新任务（后者不再调模型，只收尾）。
    const storyboardResume = storyboardStop?.kind === "resume" || storyboardStop?.kind === "refinalize" ? storyboardStop : undefined;
    const liveStoryboardStream = selectedUnitId ? storyboardStreams[selectedUnitId] : undefined;
    // 台词覆盖率和超长段：拿本章正文里引号中的台词，对照各段 lines；超过项目模型上限的段要拆。
    const unitShots = useMemo(() => detail.shots.filter((shot) => shot.unitId === selectedUnit?.id), [detail.shots, selectedUnit?.id]);
    const dialogueCoverage = useMemo(() => chapterDialogueCoverage(stripHtml(draftHtml || selectedUnit?.sourceText || ""), unitShots, detail.shotRevisions), [detail.shotRevisions, draftHtml, selectedUnit?.sourceText, unitShots]);
    const overLimitSegments = unitShots.filter((shot) => shot.durationMs > segmentMaxSeconds * 1000);
    const serverChapterOperations = useMemo(() => {
        const operations = new Map<string, ChapterOperation>();
        for (const task of chapterTasksQuery.data || []) {
            if (task.status !== "queued" && task.status !== "running") continue;
            const identity = chapterTaskIdentity(task);
            if (identity?.kind !== "storyboard") continue;
            const key = chapterOperationKey(identity.chapterId, identity.kind);
            if (!operations.has(key)) operations.set(key, chapterOperationFromTask(task));
        }
        return operations;
    }, [chapterTasksQuery.data]);
    const serverCompletedOperations = useMemo(() => {
        const latest = new Map<string, GenerationTask>();
        for (const task of chapterTasksQuery.data || []) {
            const identity = chapterTaskIdentity(task);
            if (identity?.kind !== "storyboard") continue;
            const key = chapterOperationKey(identity.chapterId, identity.kind);
            if (!latest.has(key)) latest.set(key, task);
        }
        return new Set([...latest].filter(([, task]) => task.status === "succeeded").map(([key]) => key));
    }, [chapterTasksQuery.data]);
    const runningOperationCount = new Set([...Object.keys(chapterOperations), ...serverChapterOperations.keys()]).size;
    const storyboardOperation = selectedUnit ? chapterOperations[chapterOperationKey(selectedUnit.id, "storyboard")] || serverChapterOperations.get(chapterOperationKey(selectedUnit.id, "storyboard")) : undefined;
    const storyboardRunning = Boolean(storyboardOperation);
    const storyboardStreamState = useMemo(() => {
        const live = liveStoryboardStream ? parseShotStream(liveStoryboardStream.text) : undefined;
        const previous = storyboardTaskDetail && storyboardTaskDetail.id !== liveStoryboardStream?.taskId ? shotStreamFromResult(storyboardTaskDetail.resultJson) : undefined;
        if (!live || (!storyboardRunning && previous && liveStoryboardStream?.taskId)) return previous || live || parseShotStream("");
        // 「从第 N 场继续」的新任务只写剩下的场：前面几场取上一次任务存下的，接在前面一起显示。
        const first = live.scenes[0]?.index ?? Number.POSITIVE_INFINITY;
        const carried = previous && first > 1 ? previous.scenes.filter((scene) => scene.index < first) : [];
        if (!carried.length) return live;
        return { ...live, scenes: [...carried, ...live.scenes], shotCount: live.shotCount + carried.reduce((sum, scene) => sum + scene.shots.length, 0) };
    }, [liveStoryboardStream, storyboardRunning, storyboardTaskDetail]);
    const storyboardGenerated = Boolean(selectedUnit && (completedChapterOperations[chapterOperationKey(selectedUnit.id, "storyboard")] || serverCompletedOperations.has(chapterOperationKey(selectedUnit.id, "storyboard")) || storyboardImpact.shotCount > 0));
    const visibleUnits = useMemo(() => {
        if (!deferredSearchQuery) return viewUnits;
        const numericQuery = /^\d+$/.test(deferredSearchQuery) ? deferredSearchQuery.replace(/^0+/, "") || "0" : "";
        return viewUnits.filter((unit, index) => (numericQuery && String(index + 1).startsWith(numericQuery)) || unit.title.toLocaleLowerCase("zh-CN").includes(deferredSearchQuery));
    }, [deferredSearchQuery, viewUnits]);
    const chapterVirtualizer = useVirtualizer({
        count: visibleUnits.length,
        getScrollElement: () => listRef.current,
        estimateSize: () => CHAPTER_ROW_HEIGHT,
        getItemKey: (index) => visibleUnits[index]?.id || index,
        initialOffset: () => readStoredScroll(`project-chapters:${detail.project.id}`),
        onChange: (instance, scrolling) => {
            if (!scrolling && instance.scrollOffset !== null) sessionStorage.setItem(`project-chapters:${detail.project.id}`, String(instance.scrollOffset));
        },
        overscan: 10,
    });

    useEffect(() => {
        setOrderedIds(detail.units.slice().sort((left, right) => left.position - right.position).map((unit) => unit.id));
        if (!detail.units.some((unit) => unit.id === selectedId)) setSelectedId(detail.units[0]?.id || "");
    }, [detail.units, selectedId]);

    useEffect(() => {
        if (!chapterId || chapterId === selectedId || dirty || !detail.units.some((unit) => unit.id === chapterId)) return;
        setSelectedId(chapterId);
    }, [chapterId, detail.units, dirty, selectedId]);

    useEffect(() => {
        if (!chapterId || dirty || detail.units.some((unit) => unit.id === chapterId)) return;
        const firstId = orderedUnits[0]?.id;
        navigate(firstId ? `/projects/${detail.project.id}/chapters/${firstId}` : `/projects/${detail.project.id}/chapters`, { replace: true });
    }, [chapterId, detail.project.id, detail.units, dirty, navigate, orderedUnits]);

    useEffect(() => {
        if (selectedId) sessionStorage.setItem(`project-active-chapter:${detail.project.id}`, selectedId);
    }, [detail.project.id, selectedId]);

    // 从别处跳到某一章或某一集时，左栏跟着切到它那一类。
    useEffect(() => {
        if (selectedUnitSummary) setUnitView(unitKind(selectedUnitSummary));
    }, [selectedUnitSummary?.id]);

    useEffect(() => {
        if (!runningOperationCount) return;
        setOperationNow(Date.now());
        const timer = window.setInterval(() => setOperationNow(Date.now()), 1_000);
        return () => window.clearInterval(timer);
    }, [runningOperationCount]);

    const saveMutation = useMutation({
        mutationFn: () => selectedUnit
            ? updateProjectUnit(detail.project.id, selectedUnit.id, {
                title: draftTitle.trim(),
                // 剧集正文开头的来源注释编辑器不显示，保存时接回去。
                sourceText: unitKind(selectedUnit) === "episode" ? withEpisodeSourcesComment(selectedUnit.sourceText || "", draftHtml) : draftHtml,
                status: stripHtml(draftHtml) ? "ready" : "draft",
            })
            : Promise.reject(new Error("请选择章节")),
        onSuccess: ({ unit }) => {
            queryClient.setQueryData(["project-unit", detail.project.id, unit.id], { unit });
            queryClient.setQueryData<{ units: ProjectDetail["units"]; canvasCounts: Record<string, number> }>(["project", detail.project.id, "units"], (current) => current ? {
                ...current,
                units: current.units.map((item) => item.id === unit.id ? {
                    ...item,
                    title: unit.title,
                    wordCount: unit.wordCount,
                    status: unit.status,
                    updatedAt: unit.updatedAt,
                } : item),
            } : current);
            setDirty(false);
            refreshProject();
            message.success("章节已保存");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "章节保存失败"),
    });
    // 「导入剧本」的项目导入的就是剧集（只调节奏），其余导入原文章节。
    const importKind: UnitView = detail.project.sourceType === "script" ? "episode" : "chapter";
    const createMutation = useMutation({
        mutationFn: (values: { title: string; sourceText?: string }) => createProjectUnit(detail.project.id, { kind: unitView, title: values.title, sourceText: plainTextToHtml(values.sourceText || ""), position: detail.units.length }),
        onSuccess: ({ unit }) => { queryClient.setQueryData(["project", detail.project.id, "units"], (current: Parameters<typeof appendProjectUnits>[0]) => appendProjectUnits(current, [unit])); setCreateOpen(false); setSelectedId(unit.id); refreshProject(); navigate(`/projects/${detail.project.id}/chapters/${unit.id}`); message.success(unit.kind === "episode" ? "剧集已创建" : "章节已创建"); },
        onError: (error) => message.error(error instanceof Error ? error.message : "创建失败"),
    });
    const importMutation = useMutation({
        mutationFn: (chapters: Array<{ title: string; plainText: string }>) => importProjectUnits(detail.project.id, chapters.map((chapter) => ({ kind: importKind, title: chapter.title, sourceText: plainTextToHtml(chapter.plainText) }))),
        onSuccess: ({ units }) => { queryClient.setQueryData(["project", detail.project.id, "units"], (current: Parameters<typeof appendProjectUnits>[0]) => appendProjectUnits(current, units)); setImportOpen(false); if (units[0]) { setSelectedId(units[0].id); navigate(`/projects/${detail.project.id}/chapters/${units[0].id}`); } refreshProject(); message.success(importKind === "episode" ? `已导入 ${units.length} 集剧本` : `已导入 ${units.length} 章`); },
        onError: (error) => message.error(error instanceof Error ? error.message : importKind === "episode" ? "剧本导入失败" : "小说导入失败"),
    });
    const reorderMutation = useMutation({
        mutationFn: (unitIds: string[]) => reorderProjectUnits(detail.project.id, unitIds),
        onSuccess: () => { refreshProject(); message.success("章节顺序已更新"); },
        onError: (error) => { refreshProject(); message.error(error instanceof Error ? error.message : "章节排序失败"); },
    });
    const deleteMutation = useMutation({
        mutationFn: (unitId: string) => deleteProjectUnit(detail.project.id, unitId),
        onSuccess: (_, unitId) => {
            const index = orderedIds.indexOf(unitId);
            const remaining = orderedIds.filter((id) => id !== unitId);
            setOrderedIds(remaining);
            if (selectedId === unitId) {
                const nextId = remaining[Math.min(index, remaining.length - 1)] || "";
                setSelectedId(nextId);
                navigate(nextId ? `/projects/${detail.project.id}/chapters/${nextId}` : `/projects/${detail.project.id}/chapters`, { replace: true });
            }
            refreshProject();
            message.success("章节已删除");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "章节删除失败"),
    });

    const editor = useEditor({
        immediatelyRender: false,
        extensions: [
            StarterKit.configure({ heading: { levels: [1, 2, 3] }, link: { openOnClick: false, autolink: true } }),
            TextAlign.configure({ types: ["heading", "paragraph"] }),
            TextStyle,
            Color.configure({ types: ["textStyle"] }),
            Highlight.configure({ multicolor: true }),
            CharacterCount,
            Placeholder.configure({ placeholder: "从这一章开始写下故事……" }),
        ],
        content: selectedUnit?.sourceText || "",
        editorProps: { attributes: { class: "project-chapter-editor focus:outline-none" } },
        onUpdate: ({ editor: nextEditor }) => { setDraftHtml(nextEditor.getHTML()); setDirty(true); },
    });

    useEffect(() => {
        if (!selectedUnitSummary) return;
        setDraftTitle(selectedUnitSummary.title);
        setDraftHtml("");
        setDirty(false);
    }, [selectedUnitSummary?.id]);

    useEffect(() => {
        const loadedUnit = selectedUnit;
        if (!loadedUnit || !editor) return;
        setDraftTitle(loadedUnit.title);
        setDraftHtml(loadedUnit.sourceText || "");
        setDirty(false);
        editor.commands.setContent(loadedUnit.sourceText || "", { emitUpdate: false });
        // 只在切换章节时装载服务端内容，避免项目刷新覆盖当前未保存正文。
    }, [editor, selectedUnit?.id]);

    useEffect(() => {
        if (editor && editor.isEditable === originalLocked) editor.setEditable(!originalLocked, false);
    }, [editor, originalLocked]);

    const wordCount = useMemo(() => editor?.storage.characterCount?.characters?.() || stripHtml(draftHtml).length || 0, [draftHtml, editor]);
    const chapterCanvasCount = (unitId: string) => canvasCountByUnitId.get(unitId) || 0;
    const beginChapterOperation = (unitId: string, kind: ChapterOperationKind) => {
        const key = chapterOperationKey(unitId, kind);
        const startedAt = Date.now();
        setOperationNow(startedAt);
        setChapterOperations((current) => ({ ...current, [key]: { startedAt, queued: true } }));
    };
    const updateChapterOperation = (unitId: string, kind: ChapterOperationKind, task: GenerationTask) => {
        const key = chapterOperationKey(unitId, kind);
        locallyOwnedTaskIdsRef.current.add(task.id);
        setChapterOperations((current) => {
            const previous = current[key];
            const queued = task.status === "queued";
            const taskStartedAt = Date.parse(task.startedAt || "");
            // 文本事件流只推状态、不推 startedAt：从排队转为运行的那一刻开始计时。
            const startedAt = Number.isFinite(taskStartedAt) ? taskStartedAt : !queued && previous?.queued ? Date.now() : previous?.startedAt || Date.now();
            return { ...current, [key]: { startedAt, taskId: task.id, queued } };
        });
    };
    const finishChapterOperation = (unitId: string, kind: ChapterOperationKind) => {
        const key = chapterOperationKey(unitId, kind);
        setChapterOperations((current) => {
            if (!current[key]) return current;
            const next = { ...current };
            delete next[key];
            return next;
        });
    };
    const markChapterOperationCompleted = (unitId: string, kind: ChapterOperationKind) => {
        setCompletedChapterOperations((current) => ({ ...current, [chapterOperationKey(unitId, kind)]: true }));
    };
    const refreshStoryboardResult = async (unitId: string) => {
        await Promise.all([
            queryClient.invalidateQueries({ queryKey: ["project", detail.project.id] }),
            queryClient.invalidateQueries({ queryKey: ["projects"] }),
            queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", detail.project.id] }),
        ]);
        markChapterOperationCompleted(unitId, "storyboard");
    };
    const appendStoryboardStream = (unitId: string, taskId: string, text?: string) => setStoryboardStreams((current) => {
        const previous = current[unitId];
        const sameTask = previous && (!previous.taskId || !taskId || previous.taskId === taskId);
        return { ...current, [unitId]: { taskId: taskId || previous?.taskId || "", text: text ?? (sameTask ? previous.text : "") } };
    });
    const repackMutation = useMutation({
        mutationFn: ({ unitId, accept }: { unitId: string; before: number; draft: boolean; accept?: boolean }) => repackDramaShotlist(detail.project.id, unitId, { rule: dramaRule, ...(accept ? { acceptIssues: true } : {}) }),
        onSuccess: async (result, { unitId, before, accept }) => {
            await refreshStoryboardResult(unitId);
            const scenes = new Set(result.segments.map((segment) => segment.spec.scene)).size;
            if (accept) {
                const marked = result.segments.filter((segment) => segment.issues?.length).length;
                message.success(result.accepted ? `已带着 ${result.check.errors} 个问题装成 ${result.segments.length} 段，${marked} 段标了问题，到分镜制作逐张改` : `检查已没有问题，按 ${scenes} 场装成 ${result.segments.length} 段`);
                setRunPanelOpen(false);
                navigate(`/projects/${detail.project.id}/workflow/${unitId}/video`);
                return;
            }
            message.success(before && before !== result.segments.length ? `已重新装段：${before} 段 → ${result.segments.length} 段（按 ${scenes} 场）` : `已重新装段：按 ${scenes} 场装成 ${result.segments.length} 段`);
        },
        onError: (error, { draft, accept }) => {
            const check = shotlistCheckFailure(error);
            message.error(accept ? `没能带着问题装段：${error instanceof Error ? error.message : "未知错误"}` : check ? `重新装段仍有 ${check.errors} 个问题，${draft ? "草稿" : "现有的段"}没变；需要的话重新生成` : error instanceof Error ? error.message : "重新装段失败");
            void queryClient.invalidateQueries({ queryKey: ["project", detail.project.id, "drama-shotlist"] });
        },
    });
    const createStoryboard = async (options: { replaceConfirmed?: boolean; resumeTaskId?: string; refinalize?: boolean; onlySections?: string[] } = {}) => {
        const unit = selectedUnit;
        if (!unit) return message.warning("章节正文尚未加载完成");
        if (dirty) return message.warning("请先保存当前章节，再生成分镜");
        if (adaptedOriginal) return message.warning(adaptedOriginalHint);
        if (detail.project.status === "archived") return message.warning("项目已归档，请先在项目设置中恢复");
        const sourceText = editor?.getText().trim() || stripHtml(unit.sourceText);
        if (!sourceText) return message.warning("当前章节没有可用于分镜的正文");
        if (projectStyleMissing) return message.warning("项目还没锁定风格：先让导演定画风，或从风格中心挑一个，再生成分镜");
        if (segmentRule.problem) return message.warning(`${segmentRule.problem}，先在项目设置里换默认视频模型或生成模型`);
        const textModel = selectedTextModel || effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || "";
        const config = { ...effectiveConfig, model: textModel, textModel };
        if (!isAiConfigReady(config, textModel)) {
            navigateToSettings({ continueCreation: true });
            return;
        }
        if (!options.resumeTaskId && storyboardImpact.shotCount && !options.replaceConfirmed && !(await confirmStoryboardReplacement(storyboardImpact))) return;
        setStoryboardOpen(false);
        beginChapterOperation(unit.id, "storyboard");
        setStoryboardStreams((current) => ({ ...current, [unit.id]: { taskId: "", text: "" } }));
        setRunPanelOpen(true);
        message.info(options.refinalize ? "用上一次写好的各节重新写入镜头表，不再调模型" : options.resumeTaskId ? "从中断的那一节接着写，可继续编辑或切换章节" : options.onlySections?.length ? `只重写 ${options.onlySections.map((section) => projectSceneCode(section, unitEpisodeNumber(detail.units, unit.id))).join("、")}，其余小节的镜头不动；可继续编辑或切换章节` : "分镜生成任务已开始，可继续编辑或切换章节；关掉页面也会在后台写完");
        try {
            const result = await generateChapterStoryboard({ projectId: detail.project.id, chapterId: unit.id, config, rule: dramaRule, resumeTaskId: options.resumeTaskId, onlySections: options.onlySections }, {
                onTaskUpdate: (task) => {
                    followedStoryboardTasksRef.current.add(task.id);
                    updateChapterOperation(unit.id, "storyboard", task);
                    appendStoryboardStream(unit.id, task.id);
                },
                onStreamText: (text) => appendStoryboardStream(unit.id, "", text),
            });
            await refreshStoryboardResult(unit.id);
            const outcome = result.outcome;
            if (outcome?.status === "draft") {
                message.warning(`镜头表检查有 ${outcome.errors} 个问题，已存为草稿，还没装段；在「生成分段」侧栏处理`, 10);
                return;
            }
            const summary = outcome ? `已按 ${outcome.sections ? `${outcome.sections} 节（${dramaShotGenreLabel(outcome.genre)}）` : `${outcome.scenes} 场`}生成 ${outcome.segments} 段（每段 ${segmentRule.label}${outcome.profile ? `，风格档案：${outcome.profile}` : ""}）` : "分段已写入";
            if (outcome?.notes?.length) message.warning(outcome.notes.map((note) => projectSceneCode(note, unitEpisodeNumber(detail.units, unit.id))).join("；"), 10);
            message.success(`${summary}，技能：${result.usage.skill.name} ${result.usage.skill.version}`);
            navigate(`/projects/${detail.project.id}/workflow/${unit.id}/video`);
        } catch (error) {
            refreshProject();
            void queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", detail.project.id] });
            message.error(error instanceof Error ? `章节分镜生成失败：${error.message}` : "章节分镜生成失败");
        } finally {
            finishChapterOperation(unit.id, "storyboard");
        }
    };
    // 草稿重装是处理检查问题；镜头表已写入时重装会整章替换现有的段，先确认。
    const repackStoryboard = async (accept = false) => {
        if (!selectedUnit) return;
        if (segmentRule.problem) return message.warning(`${segmentRule.problem}，先在项目设置里换默认视频模型或生成模型`);
        if (!shotlist?.draft && storyboardImpact.shotCount && !(await confirmStoryboardRepack(storyboardImpact))) return;
        repackMutation.mutate({ unitId: selectedUnit.id, before: storyboardImpact.shotCount, draft: Boolean(shotlist?.draft), accept });
    };
    const confirmStoryboardRepack = (impact: ReturnType<typeof chapterStoryboardReplaceImpact>) => new Promise<boolean>((resolve) => {
        modal.confirm({
            title: `按当前设置重新装段，替换本章 ${impact.shotCount} 段？`,
            content: (
                <div className="space-y-2 text-sm leading-6 text-foreground/62">
                    <p>用已存的镜头表按项目单段时长、资产和编译档案重新装段，不调模型；装段仍要过检查，有问题时现有的段不变。</p>
                    <p>替换会移除 {impact.shotCount} 个镜头、{impact.revisionCount} 个脚本版本、{impact.referenceCount} 个资产引用、{impact.artifactCount} 个生成产物{impact.candidateCount ? `及 ${impact.candidateCount} 个相关候选资产` : ""}，此操作无法撤销。</p>
                </div>
            ),
            okText: "重新装段",
            okButtonProps: { danger: true },
            cancelText: "保留现有的段",
            centered: true,
            onOk: () => resolve(true),
            onCancel: () => resolve(false),
        });
    });
    const confirmStoryboardReplacement = (impact: ReturnType<typeof chapterStoryboardReplaceImpact>) => new Promise<boolean>((resolve) => {
        modal.confirm({
            title: `替换本章已有的 ${impact.shotCount} 个分镜？`,
            content: (
                <div className="space-y-2 text-sm leading-6 text-foreground/62">
                    <p>新分镜生成成功后，系统才会整体替换本章数据；如果生成失败，现有分镜不会受到影响。</p>
                    <p>替换会移除 {impact.shotCount} 个镜头、{impact.revisionCount} 个脚本版本、{impact.referenceCount} 个资产引用、{impact.artifactCount} 个生成产物{impact.candidateCount ? `及 ${impact.candidateCount} 个相关候选资产` : ""}，此操作无法撤销。</p>
                </div>
            ),
            okText: "生成成功后替换",
            okButtonProps: { danger: true },
            cancelText: "保留现有分镜",
            centered: true,
            onOk: () => resolve(true),
            onCancel: () => resolve(false),
        });
    });

    useEffect(() => {
        const latestByOperation = new Map<string, { task: GenerationTask; chapterId: string }>();
        for (const task of chapterTasksQuery.data || []) {
            const identity = chapterTaskIdentity(task);
            if (identity?.kind !== "storyboard") continue;
            const key = chapterOperationKey(identity.chapterId, identity.kind);
            if (!latestByOperation.has(key)) latestByOperation.set(key, { task, chapterId: identity.chapterId });
        }
        for (const { task, chapterId: taskChapterId } of latestByOperation.values()) {
            if (task.status !== "succeeded" || locallyOwnedTaskIdsRef.current.has(task.id) || recoveredTaskIdsRef.current.has(task.id)) continue;
            recoveredTaskIdsRef.current.add(task.id);
            if (chapterTaskResultAlreadyApplied(task, taskChapterId, detail)) {
                markChapterOperationCompleted(taskChapterId, "storyboard");
                continue;
            }
            // 分段由服务端在任务收尾时写好了，这里只刷新本章的段和镜头表。
            void refreshStoryboardResult(taskChapterId);
        }
    }, [chapterTasksQuery.data, detail]);

    // 刷新页面或别的标签页起的生成分段还在跑：打开侧栏时接上它的文本流（从头回放），结束后刷新本章。
    useEffect(() => {
        const task = latestStoryboardTask;
        const unitId = selectedUnit?.id || "";
        if (!runPanelOpen || !unitId || !task || (task.status !== "queued" && task.status !== "running") || followedStoryboardTasksRef.current.has(task.id)) return;
        followedStoryboardTasksRef.current.add(task.id);
        appendStoryboardStream(unitId, task.id, "");
        void followDramaRunTask(task, { onStreamText: (text) => appendStoryboardStream(unitId, task.id, text) })
            .then(() => refreshStoryboardResult(unitId))
            .catch(() => void queryClient.invalidateQueries({ queryKey: ["project-chapter-generation-tasks", detail.project.id] }));
    }, [latestStoryboardTask, runPanelOpen, selectedUnit?.id]);

    // 剧本优化写完：切到剧集视图，改编时选中写出的第一集。
    const showWrittenEpisodes = (outcome: DramaScriptAdaptOutcome) => {
        void queryClient.invalidateQueries({ queryKey: dramaEpisodesKey(detail.project.id) });
        setUnitView("episode");
        // 只调节奏改写了正在打开的这一集：编辑器只在切换单元时装载正文，这里换成改写后的，免得保存时把改写盖回去。
        const openUnit = selectedUnit;
        if (outcome.mode === "rhythm" && openUnit && outcome.rewritten?.includes(openUnit.id)) {
            if (dirty) {
                message.warning("这一集有未保存的修改，编辑器里还是改写前的正文；保存会覆盖改写结果，不需要就切换一下剧集放弃修改", 10);
                return;
            }
            void getProjectUnit(detail.project.id, openUnit.id).then(({ unit }) => {
                editor?.commands.setContent(unit.sourceText || "", { emitUpdate: false });
                setDraftHtml(unit.sourceText || "");
                setDirty(false);
            });
            return;
        }
        const first = outcome.episodes[0]?.unitId;
        if (first && !dirty) {
            setSelectedId(first);
            navigate(`/projects/${detail.project.id}/chapters/${first}`);
        }
    };
    const switchUnitView = (view: UnitView) => {
        if (view === unitView) return;
        setUnitView(view);
        setSearchQuery("");
        const first = (view === "episode" ? episodeUnits : chapterUnits)[0];
        if (first && !dirty) selectChapter(first.id);
    };
    const selectChapter = (unitId: string) => {
        if (unitId === selectedId) return;
        if (dirty) { message.warning("请先保存当前章节，再切换章节"); return; }
        setSelectedId(unitId);
        sessionStorage.setItem(`project-active-chapter:${detail.project.id}`, unitId);
        navigate(`/projects/${detail.project.id}/chapters/${unitId}`);
    };
    const moveChapter = (targetId: string) => {
        if (!draggedId || draggedId === targetId || reorderMutation.isPending) return;
        const next = orderedIds.filter((id) => id !== draggedId);
        next.splice(next.indexOf(targetId), 0, draggedId);
        setOrderedIds(next);
        setDraggedId("");
        reorderMutation.mutate(next);
    };
    const moveChapterToPosition = () => {
        if (!moveTargetId || !movePosition || reorderMutation.isPending) return;
        // 序号是当前这一类（原文或剧集）里的位置：插到这一类第 N 个单元前面，排到最后时接在这一类最后一个后面。
        const viewIds = viewUnits.map((unit) => unit.id).filter((id) => id !== moveTargetId);
        const next = orderedIds.filter((id) => id !== moveTargetId);
        const anchor = viewIds[Math.min(Math.max(movePosition - 1, 0), viewIds.length)];
        const targetIndex = anchor ? next.indexOf(anchor) : viewIds.length ? next.indexOf(viewIds[viewIds.length - 1]) + 1 : next.length;
        next.splice(targetIndex, 0, moveTargetId);
        setOrderedIds(next);
        setMoveTargetId("");
        setMovePosition(null);
        reorderMutation.mutate(next);
        window.setTimeout(() => {
            const index = next.filter((id) => viewIds.includes(id) || id === moveTargetId).indexOf(moveTargetId);
            const visibleIndex = visibleUnits.findIndex((unit) => unit.id === moveTargetId);
            if (!deferredSearchQuery && index >= 0) chapterVirtualizer.scrollToIndex(index, { align: "center" });
            else if (visibleIndex >= 0) chapterVirtualizer.scrollToIndex(visibleIndex, { align: "center" });
        }, 0);
    };
    const revealSelectedChapter = () => {
        if (!selectedId) return;
        setSearchQuery("");
        window.setTimeout(() => {
            const index = viewUnits.findIndex((unit) => unit.id === selectedId);
            if (index >= 0) chapterVirtualizer.scrollToIndex(index, { align: "center" });
        }, 0);
    };
    const handleListDragOver = (event: DragEvent<HTMLDivElement>) => {
        if (!draggedId || deferredSearchQuery) return;
        event.preventDefault();
        const bounds = event.currentTarget.getBoundingClientRect();
        const edge = 48;
        if (event.clientY < bounds.top + edge) event.currentTarget.scrollBy({ top: -24 });
        else if (event.clientY > bounds.bottom - edge) event.currentTarget.scrollBy({ top: 24 });
    };

    return (
        <div className="grid h-full min-h-0 min-w-0 w-full grid-rows-[minmax(180px,34vh)_minmax(0,1fr)] overflow-hidden lg:grid-cols-[280px_minmax(0,1fr)] lg:grid-rows-1">
            <aside className="flex min-h-0 min-w-0 w-full flex-col border-b border-border/70 bg-background/28 lg:border-b-0 lg:border-r">
                <div className="flex h-11 shrink-0 items-center justify-between border-b border-border/70 px-2.5">
                    <div className="flex items-center gap-0.5 rounded-md bg-foreground/[.035] p-0.5 text-xs" role="group" aria-label="原文或剧集">
                        {([["chapter", "原文", chapterUnits.length], ["episode", "剧集", episodeUnits.length]] as const).map(([view, label, count]) => (
                            <button key={view} type="button" aria-pressed={unitView === view} onClick={() => switchUnitView(view)} className={`rounded px-2 py-0.5 font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--workspace-accent)] ${unitView === view ? "bg-background text-foreground/80 shadow-sm" : "text-foreground/45 hover:text-foreground/70"}`}>{label} <span className="tabular-nums text-foreground/35">{count.toLocaleString("zh-CN")}</span></button>
                        ))}
                    </div>
                    <div className="flex items-center gap-0.5">
                        {selectedId ? <Tooltip title={`回到当前${unitNoun}`}><Button type="text" size="small" icon={<Crosshair className="size-3.5" />} aria-label={`回到当前${unitNoun}`} onClick={revealSelectedChapter} /></Tooltip> : null}
                        <Tooltip title={importKind === "episode" ? "导入剧本" : "导入小说"}><Button type="text" size="small" icon={<FileUp className="size-3.5" />} aria-label={importKind === "episode" ? "导入剧本" : "导入小说"} onClick={() => setImportOpen(true)} /></Tooltip>
                        <Tooltip title={unitView === "episode" ? "添加剧集" : "添加章节"}><Button type="text" size="small" icon={<Plus className="size-4" />} aria-label={unitView === "episode" ? "添加剧集" : "添加章节"} onClick={() => setCreateOpen(true)} /></Tooltip>
                    </div>
                </div>
                <div className="shrink-0 border-b border-border/60 p-2">
                    <label className="flex h-8 items-center gap-1.5 rounded-md border border-border/75 bg-background/55 px-2 focus-within:border-[var(--workspace-accent)] focus-within:ring-2 focus-within:ring-[var(--workspace-accent-soft)]">
                        <Search className="size-3.5 shrink-0 text-foreground/32" />
                        <input value={searchQuery} onChange={(event) => setSearchQuery(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter" && visibleUnits[0]) selectChapter(visibleUnits[0].id); }} className="min-w-0 flex-1 bg-transparent text-xs outline-none placeholder:text-foreground/28" placeholder={`搜索标题或${unitNoun}序号`} aria-label={unitView === "episode" ? "搜索剧集" : "搜索章节"} />
                        {searchQuery ? <button type="button" onClick={() => setSearchQuery("")} className="grid size-5 shrink-0 place-items-center rounded text-foreground/32 hover:bg-surface-hover" aria-label="清空章节搜索"><X className="size-3" /></button> : null}
                    </label>
                    {deferredSearchQuery ? <div className="mt-1 px-0.5 text-[var(--fs-micro)] tabular-nums text-foreground/35">找到 {visibleUnits.length.toLocaleString("zh-CN")} {unitNoun} · 搜索时使用“移动到”调整顺序</div> : null}
                </div>
                {viewUnits.length ? (
                    <div ref={listRef} onDragOver={handleListDragOver} className="thin-scrollbar min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain p-1.5">
                        {visibleUnits.length ? <div className="relative w-full" style={{ height: chapterVirtualizer.getTotalSize() }}>
                            {chapterVirtualizer.getVirtualItems().map((virtualItem) => {
                                const unit = visibleUnits[virtualItem.index];
                                if (!unit) return null;
                                const chapterNumber = chapterNumberById.get(unit.id) || virtualItem.index + 1;
                                const displayedWordCount = dirty && selectedUnit?.id === unit.id ? wordCount : unit.wordCount || 0;
                                const chapterMeta = `${statusLabel(unit.status)} · ${formatCount(displayedWordCount)} 字 · ${chapterCanvasCount(unit.id)} 画布`;
                                return (
                                    <div key={unit.id} className="absolute left-0 top-0 w-full" style={{ height: virtualItem.size, transform: `translateY(${virtualItem.start}px)` }}>
                                        <div draggable={!dirty && !reorderMutation.isPending && !deferredSearchQuery} onDragStart={(event) => { setDraggedId(unit.id); event.dataTransfer.effectAllowed = "move"; }} onDragOver={(event) => { if (deferredSearchQuery) return; event.preventDefault(); event.dataTransfer.dropEffect = "move"; }} onDrop={() => moveChapter(unit.id)} onDragEnd={() => setDraggedId("")} className={`group relative flex h-[58px] items-start rounded-lg border px-0.5 transition-colors duration-100 ${unit.id === selectedUnit?.id ? "border-border/90 bg-surface-active" : "border-transparent hover:border-border/60 hover:bg-surface-hover"} ${draggedId === unit.id ? "opacity-45" : ""}`}>
                                            <button type="button" disabled={Boolean(deferredSearchQuery)} className="mt-3.5 grid size-6 shrink-0 cursor-grab place-items-center text-foreground/22 active:cursor-grabbing disabled:cursor-default disabled:opacity-35" aria-label={`拖动第 ${chapterNumber} ${unitNoun}排序`}><GripVertical className="size-3.5" /></button>
                                            <button type="button" onClick={() => selectChapter(unit.id)} className="flex min-w-0 flex-1 items-center gap-2.5 py-2 pr-14 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--workspace-accent)]">
                                                <span className={`grid h-8 min-w-8 shrink-0 place-items-center rounded-md px-1 text-[var(--fs-tiny)] font-semibold tabular-nums ${unit.id === selectedUnit?.id ? "bg-[var(--workspace-accent-soft)] text-[var(--workspace-accent)]" : "bg-foreground/[.035] text-foreground/35"}`}>{String(chapterNumber).padStart(Math.max(2, String(viewUnits.length).length), "0")}</span>
                                                <span className="min-w-0 flex-1 overflow-hidden"><span className={`block truncate text-[var(--fs-body)] ${unit.id === selectedUnit?.id ? "font-semibold text-foreground" : "font-medium text-foreground/68"}`}>{unit.title}</span><span title={chapterMeta} className="mt-1 block truncate whitespace-nowrap text-[var(--fs-tiny)] tabular-nums text-foreground/38">{statusLabel(unit.status)} · {formatChapterListCount(displayedWordCount)} 字 · {chapterCanvasCount(unit.id)} 画布</span></span>
                                            </button>
                                            <Dropdown trigger={["click"]} placement="bottomRight" menu={{ items: [{ key: "move", icon: <MoveVertical className="size-3.5" />, label: "移动到…" }], onClick: () => { setMoveTargetId(unit.id); setMovePosition(chapterNumber); } }}>
                                                <button type="button" className="absolute right-7 top-1/2 z-[1] grid size-6 -translate-y-1/2 place-items-center rounded text-foreground/28 opacity-0 hover:bg-surface-hover hover:text-foreground group-hover:opacity-100 focus-visible:opacity-100" aria-label={`${unit.title}更多操作`}><MoreHorizontal className="size-3.5" /></button>
                                            </Dropdown>
                                            <Popconfirm title={unit.kind === "episode" ? "删除此剧集？" : "删除此章节？"} description={unit.kind === "episode" ? "剧集正文、镜头表和段将被删除，已关联的画布不会删除。" : "章节内容及关联制作记录将被删除，已关联的画布不会删除。"} okText="删除" cancelText="取消" okButtonProps={{ danger: true, loading: deleteMutation.isPending }} onConfirm={() => deleteMutation.mutate(unit.id)}>
                                                <button type="button" className="absolute right-1 top-1/2 z-[1] grid size-6 -translate-y-1/2 place-items-center rounded text-foreground/28 opacity-0 hover:bg-red-500/10 hover:text-red-500 group-hover:opacity-100 focus-visible:opacity-100" aria-label={`删除${unit.title}`}><Trash2 className="size-3.5" /></button>
                                            </Popconfirm>
                                        </div>
                                    </div>
                                );
                            })}
                        </div> : <div className="px-3 py-8 text-center text-xs text-foreground/40">没有匹配的章节</div>}
                    </div>
                ) : unitView === "episode" ? (
                    <WorkspaceState icon="projects" compact className="flex-1 px-4" title="还没有剧集" description={chapterUnits.length ? "在右上角点「剧本优化」把原文章节改编成剧集，或导入剧本。" : "导入剧本，或先导入小说再做剧本优化。"} action={<Button size="small" type="primary" icon={<FileUp className="size-3.5" />} onClick={() => setImportOpen(true)}>{importKind === "episode" ? "导入剧本" : "导入小说"}</Button>} />
                ) : <WorkspaceState icon="projects" compact className="flex-1 px-4" title="还没有章节" description="添加章节后可继续编写正文和关联画布。" action={<Button size="small" type="primary" icon={<Plus className="size-3.5" />} onClick={() => setCreateOpen(true)}>添加章节</Button>} />}
            </aside>

            <section className="min-h-0 min-w-0 w-full bg-background/18 p-2.5 sm:p-3.5">
                {selectedUnit ? (
                    <div className="flex h-full min-h-0 w-full flex-col overflow-hidden rounded-lg border border-border/80 bg-background">
                        <header className="flex shrink-0 flex-wrap items-start gap-3 border-b border-border/70 px-4 py-3">
                            <div className="min-w-[12rem] flex-1">
                                <div className="mb-1 text-[var(--fs-tiny)] font-medium tabular-nums text-foreground/38">{unitKind(selectedUnit) === "episode" ? "剧集" : "原文"} · 第 {String(chapterNumberById.get(selectedUnit.id) || 1).padStart(2, "0")} {unitKind(selectedUnit) === "episode" ? "集" : "章"}</div>
                                <Input variant="borderless" value={draftTitle} disabled={!selectedUnit || originalLocked} onChange={(event) => { setDraftTitle(event.target.value); setDirty(true); }} className="!h-auto !px-0 !py-0 !text-xl !font-semibold !leading-tight disabled:!cursor-wait disabled:!text-foreground" placeholder="章节标题" />
                                <div className="mt-1.5 flex flex-wrap items-center gap-1.5 text-[var(--fs-tiny)] text-foreground/38"><span>{dirty ? "有未保存修改" : `保存于 ${formatTime(selectedUnit.updatedAt)}`}</span><span>·</span><span>{formatCount(wordCount)} 字</span><span>·</span><span>{chapterCanvasCount(selectedUnit.id)} 个画布</span>{unitShots.length ? <><span>·</span><span>{unitShots.length} 段</span></> : null}{dialogueCoverage.expected ? <><span>·</span><span className={dialogueCoverage.covered < dialogueCoverage.expected && unitShots.length ? "text-amber-600 dark:text-amber-400" : ""} title={dialogueCoverage.missing.length ? `没进分段的台词：${dialogueCoverage.missing.slice(0, 5).join(" / ")}` : undefined}>台词覆盖 {dialogueCoverage.covered}/{dialogueCoverage.expected}</span></> : null}{overLimitSegments.length ? <><span>·</span><span className="text-amber-600 dark:text-amber-400">{overLimitSegments.length} 段超过 {segmentMaxSeconds} 秒，需拆分</span></> : null}{shotlist?.draft && !storyboardOperation ? <><span>·</span><button type="button" className="text-red-600 underline-offset-2 hover:underline dark:text-red-400" onClick={() => setRunPanelOpen(true)}>镜头表有 {shotlist.check.errors} 个问题，还没装段</button></> : null}{storyboardResume && !storyboardOperation ? <><span>·</span><button type="button" className="text-amber-600 underline-offset-2 hover:underline dark:text-amber-400" onClick={() => setRunPanelOpen(true)}>{storyboardResume.kind === "refinalize" ? `${storyboardResume.total} 节都写完了，写入没成功` : `分段停在第 ${storyboardResume.from} 场`}</button></> : null}{storyboardStop?.kind === "failed" && storyboardStop.status === "failed" && !storyboardOperation ? <><span>·</span><button type="button" className="text-red-600 underline-offset-2 hover:underline dark:text-red-400" onClick={() => setRunPanelOpen(true)}>上一次生成分段失败</button></> : null}</div>
                                {unitLinks.length ? <div className="mt-1 flex flex-wrap items-center gap-1.5 text-[var(--fs-tiny)] text-foreground/45"><span>{unitKind(selectedUnit) === "episode" ? "改编自" : "已改编成"}</span>{unitLinks.map((link) => <button key={`${link.unitId}-${link.label}`} type="button" className="rounded bg-foreground/[.035] px-1.5 py-0.5 text-foreground/62 hover:text-[var(--workspace-accent)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--workspace-accent)]" onClick={() => selectChapter(link.unitId)}>{link.label}</button>)}</div> : null}
                            </div>
                            <div className="flex min-w-0 max-w-full flex-wrap items-center justify-end gap-1.5">
                                <StyleLockControl detail={detail} tasks={chapterTasksQuery.data} disabled={dirty} />
                                <ScriptAdaptControl detail={detail} chapters={chapterUnits} episodes={episodeUnits} tasks={chapterTasksQuery.data} disabled={dirty} onWritten={showWrittenEpisodes} onChained={(taskId) => followChainedScenePalette(detail.project.id, taskId)} />
                                <ScenePaletteControl detail={detail} chapters={chapterUnits} episodes={episodeUnits} tasks={chapterTasksQuery.data} disabled={dirty} />
                                <AssetsExtractControl detail={detail} unit={selectedUnit} units={[...chapterUnits, ...episodeUnits]} paletteReady={scenesQuery.data?.unitId === selectedUnit.id ? scenesQuery.data.paletteReady : undefined} unitNoun={unitKind(selectedUnit) === "episode" ? "集" : "章"} tasks={chapterTasksQuery.data} disabled={dirty || adaptedOriginal} disabledReason={adaptedOriginal ? adaptedOriginalHint : undefined} />
                                <Tooltip title={adaptedOriginal ? adaptedOriginalHint : undefined}><Button size="small" type="primary" icon={<Clapperboard className="size-3.5" />} disabled={!selectedUnit || dirty || adaptedOriginal || Boolean(storyboardOperation)} loading={Boolean(storyboardOperation)} onClick={() => { setSelectedTextModel(effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || ""); setStoryboardOpen(true); }} aria-label={storyboardOperation ? `生成分段，${chapterOperationLabel(storyboardOperation, operationNow)}` : "生成分段"}>{storyboardOperation ? `生成分段（${chapterOperationLabel(storyboardOperation, operationNow)}）` : storyboardGenerated ? "生成分段（已生成）" : "生成分段"}</Button></Tooltip>
                                {storyboardOperation || storyboardStreamState.shotCount || shotlist || storyboardStop ? <Button size="small" onClick={() => setRunPanelOpen(true)} aria-label="查看生成分段进度">进度</Button> : null}
                                <Button size="small" type={dirty ? "primary" : "default"} icon={dirty ? <Save className="size-3.5" /> : <Check className="size-3.5" />} disabled={!selectedUnit || !dirty || !draftTitle.trim() || saveMutation.isPending || originalLocked} loading={saveMutation.isPending} onClick={() => saveMutation.mutate()}>{dirty ? "保存" : "已保存"}</Button>
                            </div>
                        </header>
                        {originalLocked ? <div className="shrink-0 border-b border-border/70 px-4 py-2"><Callout tone="info" title={adaptedOriginal ? "这一章已改编成剧集，原文只读" : "已有剧集，原文只读"} action={<Button size="small" onClick={() => (adaptedEpisodeTarget ? selectChapter(adaptedEpisodeTarget.unitId) : switchUnitView("episode"))}>{adaptedEpisodeTarget ? `去${adaptedEpisodeLabel}` : "看剧集"}</Button>}>{adaptedOriginal ? "原文只作改编依据：改剧情、提取资产、生成分段都到剧集上操作；要按原文重来，点「剧本优化」勾选这一章重新改编，新剧本会替换由它改编的剧集。" : "原文只作改编依据。这一章还没改编：点「剧本优化」勾选它改编成剧集。"}</Callout></div> : null}
                        <EditorToolbar editor={originalLocked ? null : editor} />
                        <ChapterSceneList scenes={scenesQuery.data} editor={editor} dirty={dirty} />
                        <div className="project-chapter-editor-scroll thin-scrollbar min-h-0 flex-1 overflow-y-auto bg-foreground/[.012]">
                            <div className="project-chapter-editor-wrap min-h-full"><EditorContent editor={editor} /></div>
                        </div>
                    </div>
                ) : <WorkspaceState icon="projects" compact className="h-full" title="请选择章节" description="从左侧章节列表选择一章开始编辑；项目风格可以先定。" action={<div className="flex flex-wrap justify-center gap-1.5"><StyleLockControl detail={detail} tasks={chapterTasksQuery.data} /></div>} />}
            </section>
            <CreateChapterModal open={createOpen} kind={unitView} onClose={() => setCreateOpen(false)} loading={createMutation.isPending} onSubmit={(values) => createMutation.mutate(values)} />
            <ImportNovelModal open={importOpen} kind={importKind} loading={importMutation.isPending} onClose={() => setImportOpen(false)} onImport={(chapters) => importMutation.mutate(chapters)} />
            <ChapterStoryboardRunPanel
                open={runPanelOpen}
                onClose={() => setRunPanelOpen(false)}
                chapterTitle={selectedUnit?.title || ""}
                running={Boolean(storyboardOperation)}
                stage={latestStoryboardTask && (latestStoryboardTask.status === "running" || latestStoryboardTask.status === "queued") ? latestStoryboardTask.stage : undefined}
                stream={storyboardStreamState}
                outcome={storyboardOutcome}
                shotlist={shotlist}
                stop={storyboardStop}
                segmentCount={storyboardImpact.shotCount}
                onResume={() => { if (storyboardResume) void createStoryboard({ resumeTaskId: storyboardResume.taskId, refinalize: storyboardResume.kind === "refinalize" }); }}
                onRepack={() => void repackStoryboard()}
                repacking={repackMutation.isPending && !repackMutation.variables?.accept}
                onAcceptIssues={() => void repackStoryboard(true)}
                accepting={repackMutation.isPending && Boolean(repackMutation.variables?.accept)}
                onRegenerate={() => { setRunPanelOpen(false); setStoryboardOnlySections([]); setSelectedTextModel(effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || ""); setStoryboardOpen(true); }}
                onRegenerateSections={(sections) => { setRunPanelOpen(false); setStoryboardOnlySections(sections); setSelectedTextModel(effectiveConfig.textModel || effectiveConfig.model || effectiveConfig.textModels[0] || ""); setStoryboardOpen(true); }}
                sceneNotice={scenesQuery.data?.notice}
                episode={selectedEpisode}
                onOpenProduction={() => { if (selectedUnit) navigate(`/projects/${detail.project.id}/workflow/${selectedUnit.id}/video`); }}
            />
            <Modal className="library-modal" title={storyboardOnlySections.length ? `重新生成问题小节（${shownSections.join("、")}）` : `生成${selectedUnitNoun}分段`} open={storyboardOpen} width={560} okText={storyboardOnlySections.length ? `只重写这 ${storyboardOnlySections.length} 节` : storyboardImpact.shotCount ? "重新生成分段" : "生成分段"} cancelText="取消" okButtonProps={{ disabled: !selectedTextModel || projectStyleMissing || Boolean(segmentRule.problem) || !storyboardStep }} onCancel={() => setStoryboardOpen(false)} onOk={() => void createStoryboard(storyboardOnlySections.length ? { onlySections: storyboardOnlySections } : {})} styles={{ body: { paddingTop: 12 } }}>
                <div className="grid gap-4">
                    {storyboardOnlySections.length ? <Callout tone="info" title="只重写问题所在的小节">一轮一节：{shownSections.join("、")} 重新调模型写，其余小节用镜头表里已有的镜头（原样输出照原来的顺序放进对话，不调模型），写完按整章重新检查、装段。</Callout> : null}
                    {projectStyleMissing ? <Callout tone="warning" title="项目还没锁定风格">分镜要按项目风格写画面和参考：先在本页「分析项目风格」让导演定画风并锁定，再回来生成。</Callout> : null}
                    <div className="rounded-lg border border-border/70 bg-foreground/[.018] px-3 py-2.5">
                        <div className="text-[var(--fs-tiny)] text-foreground/42">当前{selectedUnitNoun}</div>
                        <div className="mt-1 truncate text-sm font-medium text-foreground/85">{selectedUnit?.title}</div>
                        <div className="mt-1 text-[var(--fs-tiny)] text-foreground/38">正文将作为分段依据：每场先按{storyboardStep?.genre ? `${storyboardStep.genre.label}的口径（一节 ≤${storyboardStep.genre.maxSeconds} 秒）` : "戏类口径"}切成小节，一节 = 一轮生成 = 一段视频，每段目标 {segmentTargetLabel(segmentRule.maxSeconds)}（{segmentLimit.byBrief ? `项目单段最长 ${segmentMaxSeconds} 秒，${dramaModel?.label || "项目生成模型"}上限 ${modelSegmentMaxSeconds} 秒` : `${dramaModel?.label || "项目生成模型"}最长 ${segmentMaxSeconds} 秒`}，视频渠道 {segmentRule.label}），不在接话之间切；结果写入“分镜制作”。</div>
                    </div>
                    {segmentRule.problem ? <Callout tone="warning" title="当前视频渠道交不了这种段长" action={<Button size="small" onClick={() => { setStoryboardOpen(false); navigate(`/projects/${detail.project.id}/settings`); }}>去项目设置</Button>}>{segmentRule.problem}。按这个渠道装出来的段提交时必然被拒，先换默认视频模型或项目生成模型。</Callout> : null}
                    {assetsNotReady ? <Callout tone="warning" title={referenceAssetCount ? "还有待确认的资产" : "先提取资产，段才会自动带上参考"} action={<Button size="small" onClick={() => { setStoryboardOpen(false); navigate(`/projects/${detail.project.id}/assets`); }}>去资产页</Button>}>{pendingCandidateCount ? `项目里还有 ${pendingCandidateCount} 个待确认的候选（手动或 Agent 加的）。` : ""}{referenceAssetCount ? "确认后再生成，同一场的段会带上这一场绑定的全部资产作为参考。" : "现在项目里没有资产，生成的段都不带参考，人物和场景在段与段之间容易不一致；先点「提取资产」。"}也可以先生成，之后在分镜制作页给段绑定资产。</Callout> : null}
                    {storyboardImpact.shotCount ? <Callout tone="warning" title={`本章已有 ${storyboardImpact.shotCount} 段`}>继续后会先生成新分段；生成成功后，再整体替换旧段及其关联数据。</Callout> : null}
                    <label className="block">
                        <span className="mb-1.5 block text-xs font-medium text-foreground/68">文本模型</span>
                        <ModelPicker config={effectiveConfig} capability="text" value={selectedTextModel} onChange={setSelectedTextModel} variant="creation" fullWidth placeholder="选择用于生成分镜的文本模型" showSelectedPrice={false} onMissingConfig={() => navigateToSettings({ continueCreation: true })} />
                    </label>
                    {storyboardStepQuery.isError ? (
                        <Callout tone="error" title="读不到本步使用的技能">{storyboardStepQuery.error instanceof Error ? storyboardStepQuery.error.message : "请稍后重试"}</Callout>
                    ) : (
                        <div className="rounded-lg border border-border/70 px-3 py-2.5 text-xs leading-5 text-foreground/62" aria-live="polite">
                            {storyboardStep ? (
                                <>
                                    {storyboardStep.genre ? (
                                        <div>
                                            戏类：<span className="font-medium text-foreground/85">{storyboardStep.genre.label}</span>（{storyboardStep.genre.model} 单段上限，一节 ≤{storyboardStep.genre.maxSeconds} 秒）
                                        </div>
                                    ) : null}
                                    <div>
                                        本步使用：<span className="font-medium text-foreground/85">{storyboardStep.skill.name}</span> {storyboardStep.skill.version}（{storyboardStep.profile.label}）
                                    </div>
                                    <div className="mt-1 text-[var(--fs-tiny)] text-foreground/42">戏类按项目视频模型单段上限自动选（≤15 秒文戏 15，更长文戏 30）；技能由项目的风格档案和戏类决定，管理员在后台「流程技能」里绑定；换风格档案到项目设置。</div>
                                </>
                            ) : (
                                "正在读取本步使用的技能…"
                            )}
                        </div>
                    )}
                </div>
            </Modal>
            <Modal className="library-modal" title={unitView === "episode" ? "移动剧集" : "移动章节"} open={Boolean(moveTargetId)} width={400} okText="移动" cancelText="取消" okButtonProps={{ disabled: !movePosition || movePosition < 1 || movePosition > viewUnits.length, loading: reorderMutation.isPending }} onCancel={() => { setMoveTargetId(""); setMovePosition(null); }} onOk={moveChapterToPosition} styles={{ body: { paddingTop: 12 } }}>
                <div className="text-xs leading-5 text-foreground/50">输入目标章节位置。适合上千章项目的长距离调整，移动后其他章节会自动顺延。</div>
                <label className="mt-3 flex items-center gap-2 text-sm"><span className="shrink-0">移动到第</span><InputNumber min={1} max={viewUnits.length} precision={0} value={movePosition} onChange={setMovePosition} className="min-w-0 flex-1" /><span className="shrink-0">{unitNoun}</span></label>
            </Modal>
        </div>
    );
}

function chapterOperationKey(unitId: string, kind: ChapterOperationKind) {
    return `${unitId}:${kind}`;
}

function chapterOperationFromTask(task: GenerationTask): ChapterOperation {
    const startedAt = Date.parse(task.startedAt || task.createdAt);
    return { startedAt: Number.isFinite(startedAt) ? startedAt : Date.now(), taskId: task.id, queued: task.status === "queued" };
}

function chapterTaskResultAlreadyApplied(task: GenerationTask, chapterId: string, detail: ProjectDetail) {
    const completedAt = Date.parse(task.completedAt || task.updatedAt);
    if (!Number.isFinite(completedAt)) return false;
    return detail.shots.filter((shot) => shot.unitId === chapterId).some((shot) => {
        const updatedAt = Date.parse(shot.updatedAt);
        return Number.isFinite(updatedAt) && updatedAt >= completedAt;
    });
}

function chapterOperationLabel(operation: ChapterOperation, now: number) {
    return operation.queued ? "排队中" : `已运行${formatOperationElapsed(operation.startedAt, now)}`;
}

function formatOperationElapsed(startedAt: number, now: number) {
    const totalSeconds = Math.max(0, Math.floor((now - startedAt) / 1_000));
    const minutes = Math.floor(totalSeconds / 60);
    const seconds = String(totalSeconds % 60).padStart(2, "0");
    return `${minutes}分钟${seconds}秒`;
}

function EditorToolbar({ editor }: { editor: Editor | null }) {
    const setLink = () => {
        if (!editor) return;
        const current = String(editor.getAttributes("link").href || "");
        const href = window.prompt("输入链接地址", current);
        if (href === null) return;
        if (!href.trim()) editor.chain().focus().unsetLink().run();
        else editor.chain().focus().extendMarkRange("link").setLink({ href: href.trim() }).run();
    };
    const setColor = () => {
        const color = window.prompt("输入文字颜色，例如 #d97706", String(editor?.getAttributes("textStyle").color || "#d97706"));
        if (color?.trim()) editor?.chain().focus().setColor(color.trim()).run();
    };
    const setHighlight = () => {
        const color = window.prompt("输入高亮颜色，例如 #fef3c7", String(editor?.getAttributes("highlight").color || "#fef3c7"));
        if (color?.trim()) editor?.chain().focus().toggleHighlight({ color: color.trim() }).run();
    };
    const blockLabel = editor?.isActive("heading", { level: 1 }) ? "标题 1" : editor?.isActive("heading", { level: 2 }) ? "标题 2" : editor?.isActive("heading", { level: 3 }) ? "标题 3" : "正文";
    const alignment = editor?.isActive({ textAlign: "center" }) ? "center" : editor?.isActive({ textAlign: "right" }) ? "right" : editor?.isActive({ textAlign: "justify" }) ? "justify" : "left";
    const alignmentIcon = alignment === "center" ? <AlignCenter className="size-3.5" /> : alignment === "right" ? <AlignRight className="size-3.5" /> : alignment === "justify" ? <AlignJustify className="size-3.5" /> : <AlignLeft className="size-3.5" />;
    return (
        <div className="hide-scrollbar flex h-10 shrink-0 items-center gap-0.5 overflow-x-auto border-b border-border/70 px-2">
            <EditorTool editor={editor} label="撤销" icon={<Undo2 className="size-3.5" />} onClick={() => editor?.chain().focus().undo().run()} />
            <EditorTool editor={editor} label="重做" icon={<Redo2 className="size-3.5" />} onClick={() => editor?.chain().focus().redo().run()} />
            <ToolbarDivider />
            <Dropdown trigger={["click"]} menu={{ selectedKeys: [blockLabel], items: ["正文", "标题 1", "标题 2", "标题 3"].map((key) => ({ key, label: key })), onClick: ({ key }) => key === "正文" ? editor?.chain().focus().setParagraph().run() : editor?.chain().focus().toggleHeading({ level: Number(key.slice(-1)) as 1 | 2 | 3 }).run() }}>
                <button type="button" className="flex h-7 items-center gap-1 rounded px-2 text-xs text-foreground/60 hover:bg-surface-hover" aria-label="段落格式">{blockLabel}<ChevronDown className="size-3" /></button>
            </Dropdown>
            <EditorTool editor={editor} label="粗体" icon={<Bold className="size-3.5" />} onClick={() => editor?.chain().focus().toggleBold().run()} active={Boolean(editor?.isActive("bold"))} />
            <EditorTool editor={editor} label="斜体" icon={<Italic className="size-3.5" />} onClick={() => editor?.chain().focus().toggleItalic().run()} active={Boolean(editor?.isActive("italic"))} />
            <EditorTool editor={editor} label="下划线" icon={<Underline className="size-3.5" />} onClick={() => editor?.chain().focus().toggleUnderline().run()} active={Boolean(editor?.isActive("underline"))} />
            <EditorTool editor={editor} label="删除线" icon={<Strikethrough className="size-3.5" />} onClick={() => editor?.chain().focus().toggleStrike().run()} active={Boolean(editor?.isActive("strike"))} />
            <ToolbarDivider />
            <Dropdown trigger={["click"]} menu={{ selectedKeys: [alignment], items: [{ key: "left", icon: <AlignLeft className="size-3.5" />, label: "左对齐" }, { key: "center", icon: <AlignCenter className="size-3.5" />, label: "居中" }, { key: "right", icon: <AlignRight className="size-3.5" />, label: "右对齐" }, { key: "justify", icon: <AlignJustify className="size-3.5" />, label: "两端对齐" }], onClick: ({ key }) => editor?.chain().focus().setTextAlign(key).run() }}>
                <button type="button" className="grid size-7 place-items-center rounded text-foreground/60 hover:bg-surface-hover" aria-label="文字对齐">{alignmentIcon}</button>
            </Dropdown>
            <EditorTool editor={editor} label="项目符号" icon={<List className="size-3.5" />} onClick={() => editor?.chain().focus().toggleBulletList().run()} active={Boolean(editor?.isActive("bulletList"))} />
            <EditorTool editor={editor} label="编号列表" icon={<ListOrdered className="size-3.5" />} onClick={() => editor?.chain().focus().toggleOrderedList().run()} active={Boolean(editor?.isActive("orderedList"))} />
            <EditorTool editor={editor} label="引用" icon={<Quote className="size-3.5" />} onClick={() => editor?.chain().focus().toggleBlockquote().run()} active={Boolean(editor?.isActive("blockquote"))} />
            <EditorTool editor={editor} label="链接" icon={<Link2 className="size-3.5" />} onClick={setLink} active={Boolean(editor?.isActive("link"))} />
            <Dropdown trigger={["click"]} placement="bottomRight" menu={{ items: [{ key: "color", icon: <span className="text-[var(--fs-label)] font-bold text-amber-600">A</span>, label: "文字颜色" }, { key: "highlight", icon: <Highlighter className="size-3.5" />, label: "高亮颜色" }, { type: "divider" }, { key: "code", icon: <Code2 className="size-3.5" />, label: "行内代码" }, { key: "rule", icon: <Minus className="size-3.5" />, label: "分隔线" }, { key: "clear", icon: <Eraser className="size-3.5" />, label: "清除格式" }], onClick: ({ key }) => { if (key === "color") setColor(); else if (key === "highlight") setHighlight(); else if (key === "code") editor?.chain().focus().toggleCode().run(); else if (key === "rule") editor?.chain().focus().setHorizontalRule().run(); else if (key === "clear") editor?.chain().focus().clearNodes().unsetAllMarks().run(); } }}>
                <button type="button" className="grid size-7 place-items-center rounded text-foreground/60 hover:bg-surface-hover" aria-label="更多格式"><MoreHorizontal className="size-4" /></button>
            </Dropdown>
        </div>
    );
}

function EditorTool({ editor, label, icon, active = false, onClick }: { editor: Editor | null; label: string; icon: ReactNode; active?: boolean; onClick: () => void }) {
    return <Tooltip title={label}><button type="button" aria-label={label} className={`grid size-7 shrink-0 place-items-center rounded ${active ? "bg-surface-active text-[var(--workspace-accent)]" : "text-foreground/55 hover:bg-surface-hover hover:text-foreground"}`} disabled={!editor} onClick={onClick}>{icon}</button></Tooltip>;
}

function ToolbarDivider() {
    return <span className="mx-1 h-4 w-px shrink-0 bg-border" />;
}

function CreateChapterModal({ open, kind, onClose, loading, onSubmit }: { open: boolean; kind: UnitView; onClose: () => void; loading: boolean; onSubmit: (values: { title: string; sourceText?: string }) => void }) {
    const noun = kind === "episode" ? "剧集" : "章节";
    return <Modal className="library-modal" title={`添加${noun}`} open={open} footer={null} destroyOnHidden onCancel={onClose} width={480} styles={{ body: { paddingTop: 12 } }}><Form layout="vertical" onFinish={onSubmit}><Form.Item name="title" label={`${noun}标题`} rules={[{ required: true, whitespace: true, message: `请输入${noun}标题` }]}><Input autoFocus placeholder="例如：雨夜归城" /></Form.Item><Form.Item name="sourceText" label="正文（可选）"><Input.TextArea rows={4} placeholder={kind === "episode" ? "剧本格式：「## 内·地点·夜」起一场，「人物：台词」一行一句" : "创建后仍可继续编辑和排版"} /></Form.Item><div className="flex justify-end gap-2"><Button onClick={onClose}>取消</Button><Button type="primary" htmlType="submit" loading={loading}>创建{noun}</Button></div></Form></Modal>;
}

/** 导入小说（原文章节）或导入剧本（剧集，「导入剧本」的项目）：按章 / 集标题自动切分后追加到项目。 */
function ImportNovelModal({ open, kind, loading, onClose, onImport }: { open: boolean; kind: UnitView; loading: boolean; onClose: () => void; onImport: (chapters: Array<{ title: string; plainText: string }>) => void }) {
    const script = kind === "episode";
    const noun = script ? "集" : "章";
    const fileInputRef = useRef<HTMLInputElement>(null);
    const [text, setText] = useState("");
    const [fileName, setFileName] = useState("");
    const deferredText = useDeferredValue(text);
    const chapters = useMemo(() => deferredText.trim() ? splitTextIntoChapters(deferredText).map((chapter) => ({ title: chapter.title, plainText: chapter.plainText })) : [], [deferredText]);
    useEffect(() => { if (!open) { setText(""); setFileName(""); } }, [open]);
    const readFile = async (event: ChangeEvent<HTMLInputElement>) => {
        const file = event.target.files?.[0];
        event.target.value = "";
        if (!file) return;
        setFileName(file.name);
        setText(decodeNovelText(await file.arrayBuffer()));
    };
    return (
        <AppModal flush title={null} open={open} footer={null} onCancel={onClose} width={760}>
            <div className="flex min-h-[478px] flex-col">
                <header className="flex h-12 shrink-0 items-center border-b border-border px-4"><div><h2 className="text-sm font-semibold">{script ? "导入剧本" : "导入小说"}</h2><p className="mt-0.5 text-[var(--fs-tiny)] text-foreground/42">{script ? "自动识别「第一集」这类标题，每集成为一个剧集，确认后追加到当前项目" : "自动识别章节标题，确认后追加到当前项目"}</p></div></header>
                <div className="grid min-h-[430px] flex-1 grid-cols-1 md:grid-cols-[minmax(0,1fr)_240px]">
                <div className="border-b border-border p-3 md:border-b-0 md:border-r">
                    <div className="mb-2 flex items-center justify-between gap-2"><div><div className="text-sm font-medium">{script ? "剧本正文" : "小说正文"}</div><div className="mt-0.5 text-[var(--fs-label)] text-foreground/45">{script ? "场次头写「## 内·地点·夜」，台词写「人物：台词」，生成分段按它切场" : "识别章节标题后追加到现有章节"}</div></div><Button size="small" icon={<FileUp className="size-3.5" />} onClick={() => fileInputRef.current?.click()}>{fileName || "选择 TXT"}</Button></div>
                    <input ref={fileInputRef} type="file" accept=".txt,.md,text/plain,text/markdown" className="hidden" onChange={(event) => void readFile(event)} />
                    <Input.TextArea value={text} onChange={(event) => setText(event.target.value)} rows={16} placeholder={script ? '也可以直接粘贴剧本，例如：\n\n第一集 雨夜来信\n## 内·病房走廊·夜\n林小满：七床？\n\n第二集 灯塔以北\n……' : '也可以直接粘贴小说正文，例如：\n\n第一章 雨夜来信\n正文……\n\n第二章 灯塔以北\n正文……'} className="!resize-none" />
                </div>
                <div className="flex min-h-0 flex-col">
                    <div className="flex h-11 shrink-0 items-center justify-between border-b border-border px-3 text-xs"><span className="font-medium">拆分预览</span><span className="tabular-nums text-foreground/45">{chapters.length} {noun}</span></div>
                    <ImportChapterPreview chapters={chapters} noun={noun} />
                    <div className="flex shrink-0 items-center justify-between gap-2 border-t border-border p-3"><span className={`text-[var(--fs-tiny)] ${chapters.length > MAX_NOVEL_IMPORT_CHAPTERS ? "text-red-500" : "text-foreground/38"}`}>{chapters.length > MAX_NOVEL_IMPORT_CHAPTERS ? `最多一次导入 ${MAX_NOVEL_IMPORT_CHAPTERS.toLocaleString("zh-CN")} ${noun}` : `支持最多 ${MAX_NOVEL_IMPORT_CHAPTERS.toLocaleString("zh-CN")} ${noun}`}</span><div className="flex gap-2"><Button size="small" onClick={onClose}>取消</Button><Button size="small" type="primary" disabled={!chapters.length || chapters.length > MAX_NOVEL_IMPORT_CHAPTERS} loading={loading} onClick={() => onImport(chapters)}>导入 {chapters.length || ""} {noun}</Button></div></div>
                </div>
                </div>
            </div>
        </AppModal>
    );
}

function stripHtml(value: string) {
    return value.replace(/<[^>]+>/g, "").replace(/&nbsp;/g, " ").trim();
}

function readStoredScroll(key: string) {
    const value = Number(sessionStorage.getItem(key) || 0);
    return Number.isFinite(value) && value > 0 ? value : 0;
}
