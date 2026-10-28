import { backendProviderConfig } from "@/services/api/generation-task";
import {
    dramaAssetsExtractOutcomeOf,
    dramaRunProgressOf,
    dramaScriptAdaptOutcomeOf,
    dramaStepOutcomeOf,
    dramaScenePaletteOutcomeOf,
    dramaStyleLockOutcomeOf,
    runDramaProjectStep,
    runDramaStep,
    type DramaImageTaskTemplate,
    type DramaScriptAdaptMode,
} from "@/services/api/drama";
import { announceCreatedGenerationTask, queryGenerationTask, waitForGenerationTask, type GenerationTask } from "@/services/api/task-center";
import { logicalModelIDForConfig, type AiConfig } from "@/stores/use-config-store";

import { SHOT_TURN_MARKER } from "./chapter-shot-stream";

type ChapterTaskOptions = {
    onTaskUpdate?: (task: GenerationTask) => void;
    /** 任务文本流到目前为止的全文（各场输出与回合标记），给流式镜头视图用。 */
    onStreamText?: (text: string) => void;
};

export type ChapterTaskKind = "assets" | "storyboard";

/** 资产设计任务的 metadata.source（与后端步骤表 TaskSource 一致）。 */
export const ASSETS_EXTRACT_TASK_SOURCE = "short-drama-assets-extract";

export function chapterTaskIdentity(task: GenerationTask): { chapterId: string; kind: ChapterTaskKind } | null {
    const context = task.clientContext;
    if (context?.chapterId && (context.chapterOperation === "assets" || context.chapterOperation === "storyboard")) {
        return { chapterId: context.chapterId, kind: context.chapterOperation };
    }
    const metadata = generationTaskMetadata(task.inputJson);
    const chapterId = typeof metadata.chapterId === "string" ? metadata.chapterId : "";
    if (!chapterId) return null;
    if (metadata.source === ASSETS_EXTRACT_TASK_SOURCE) return { chapterId, kind: "assets" };
    if (metadata.source === "short-drama-chapter-storyboard") return { chapterId, kind: "storyboard" };
    return null;
}

type ChapterStoryboardGenerationInput = {
    projectId: string;
    chapterId: string;
    config: AiConfig;
    /** 视频渠道实际能提交的段长，服务端装段时只收紧。 */
    rule?: { maxSeconds: number; minSeconds: number };
    /** 「从第 N 节继续」：上一次没写完的任务。 */
    resumeTaskId?: string;
    /** 「重新生成问题小节」：只重写这几节（小节编号），其余小节的镜头行取镜头表里已有的。 */
    onlySections?: string[];
};

// 逐轮续写的任务在服务端按轮计时（一轮一节，每轮一次分镜超时），总时长随节数变；页面只是订阅，不替后端判超时。
const STORYBOARD_RUN_WAIT_MS = 8 * 60 * 60 * 1000;

/**
 * 生成分段（流程步骤 chapter.shots）：服务端按项目视频模型单段上限选戏类、按项目风格档案和戏类取绑定技能的当前版本组装提示词，在一个任务里一轮一节，
 * 任务成功前解析、绑资产、写镜头表并装段。页面只订阅任务文本流显示进度，关掉页面也会写完。
 */
export async function generateChapterStoryboard(input: ChapterStoryboardGenerationInput, options?: ChapterTaskOptions) {
    const model = input.config.textModel || input.config.model;
    const config = { ...input.config, model };
    const { task, usage } = await runDramaStep(input.projectId, input.chapterId, "chapter.shots", {
        model,
        ...(logicalModelIDForConfig(config) ? { logicalModelId: logicalModelIDForConfig(config) } : {}),
        config: backendProviderConfig(config, "text") as Record<string, unknown>,
        ...(input.rule ? { rule: input.rule } : {}),
        ...(input.resumeTaskId ? { resumeTaskId: input.resumeTaskId } : {}),
        ...(input.onlySections?.length ? { onlySections: input.onlySections } : {}),
    });
    announceCreatedGenerationTask(task);
    options?.onTaskUpdate?.(task);
    const completed = await followDramaRunTask(task, options);
    return { task: completed, usage, outcome: dramaStepOutcomeOf(completed), progress: dramaRunProgressOf(completed) };
}

type AssetsExtractInput = {
    projectId: string;
    unitId: string;
    config: AiConfig;
    /** 给新资产出图的图片任务模板；不给时只建资产不出图。 */
    imageTask?: DramaImageTaskTemplate;
    resumeTaskId?: string;
};

/**
 * 资产设计（流程步骤 assets.extract，按章节或剧集）：服务端按项目记忆一场一轮提取角色、场景、道具和外观提示词，任务成功前
 * 按名字和别名并进已有资产、其余直接建成已确认资产，带了 imageTask 时接着排出图。页面只订阅任务文本流按场显示。
 */
export async function runAssetsExtract(input: AssetsExtractInput, options?: ChapterTaskOptions) {
    const model = input.config.textModel || input.config.model;
    const config = { ...input.config, model };
    const { task, usage } = await runDramaStep(input.projectId, input.unitId, "assets.extract", {
        model,
        ...(logicalModelIDForConfig(config) ? { logicalModelId: logicalModelIDForConfig(config) } : {}),
        config: backendProviderConfig(config, "text") as Record<string, unknown>,
        ...(input.imageTask ? { imageTask: input.imageTask } : {}),
        ...(input.resumeTaskId ? { resumeTaskId: input.resumeTaskId } : {}),
    });
    announceCreatedGenerationTask(task);
    options?.onTaskUpdate?.(task);
    const completed = await followDramaRunTask(task, options);
    return { task: completed, usage, outcome: dramaAssetsExtractOutcomeOf(completed) };
}

/** 剧本优化任务的 metadata.source（与后端步骤表 TaskSource 一致）。 */
export const SCRIPT_ADAPT_TASK_SOURCE = "short-drama-script-adapt";

/** 任务列表不带任务输入，靠服务端给的 clientContext（chapterOperation=script-adapt）认；带输入的任务详情按 metadata.source 认。 */
export function isScriptAdaptTask(task: GenerationTask) {
    return task.clientContext?.chapterOperation === "script-adapt" || generationTaskMetadata(task.inputJson).source === SCRIPT_ADAPT_TASK_SOURCE;
}

type ScriptAdaptInput = {
    projectId: string;
    config: AiConfig;
    mode: DramaScriptAdaptMode;
    sourceUnitIds: string[];
    targetEpisodes?: number;
    episodeSeconds?: number;
    /** 已确认替换（或改写）对话框里列出的已有剧集。 */
    replace?: boolean;
    resumeTaskId?: string;
};

/**
 * 剧本优化（流程步骤 script.adapt，项目级）：服务端按项目记忆组装提示词，在一个任务里一集一轮续写，任务成功前把各集写成
 * episode 单元。页面只订阅任务文本流按集显示，关掉页面也会写完。会替换已有剧集时抛 ApiError（drama_episodes_replace_required）。
 */
export async function runScriptAdapt(input: ScriptAdaptInput, options?: ChapterTaskOptions) {
    const model = input.config.textModel || input.config.model;
    const config = { ...input.config, model };
    const { task, usage } = await runDramaProjectStep(input.projectId, "script.adapt", {
        model,
        ...(logicalModelIDForConfig(config) ? { logicalModelId: logicalModelIDForConfig(config) } : {}),
        config: backendProviderConfig(config, "text") as Record<string, unknown>,
        mode: input.mode,
        sourceUnitIds: input.sourceUnitIds,
        ...(input.targetEpisodes ? { targetEpisodes: input.targetEpisodes } : {}),
        ...(input.episodeSeconds ? { episodeSeconds: input.episodeSeconds } : {}),
        ...(input.replace ? { replace: true } : {}),
        ...(input.resumeTaskId ? { resumeTaskId: input.resumeTaskId } : {}),
    });
    announceCreatedGenerationTask(task);
    options?.onTaskUpdate?.(task);
    const completed = await followDramaRunTask(task, options);
    return { task: completed, usage, outcome: dramaScriptAdaptOutcomeOf(completed) };
}

/** 风格锁定任务的 metadata.source（与后端步骤表 TaskSource 一致）。 */
export const STYLE_LOCK_TASK_SOURCE = "short-drama-style-lock";

/** 任务列表靠 clientContext（chapterOperation=style-lock）认，带输入的任务详情按 metadata.source 认。 */
export function isStyleLockTask(task: GenerationTask) {
    return task.clientContext?.chapterOperation === "style-lock" || generationTaskMetadata(task.inputJson).source === STYLE_LOCK_TASK_SOURCE;
}

/** 风格锁定的输入：项目类型、风格文字、参考图资源 ID；lockOnFinish 是建项目时只选了画风，收尾直接锁定。 */
export type StyleLockRunInput = {
    projectId: string;
    config: AiConfig;
    projectType: string;
    styleText?: string;
    referenceImages?: string[];
    lockOnFinish?: boolean;
};

/** 起风格锁定任务（不等它跑完）：建项目后立刻起，章节页的侧栏接上它的流。 */
export async function startStyleLock(input: StyleLockRunInput) {
    const model = input.config.textModel || input.config.model;
    const config = { ...input.config, model };
    const { task, usage } = await runDramaProjectStep(input.projectId, "style.lock", {
        model,
        ...(logicalModelIDForConfig(config) ? { logicalModelId: logicalModelIDForConfig(config) } : {}),
        config: backendProviderConfig(config, "text") as Record<string, unknown>,
        projectType: input.projectType,
        ...(input.styleText?.trim() ? { styleText: input.styleText.trim() } : {}),
        ...(input.referenceImages?.length ? { referenceImages: input.referenceImages } : {}),
        ...(input.lockOnFinish ? { lockOnFinish: true } : {}),
    });
    announceCreatedGenerationTask(task);
    return { task, usage };
}

/**
 * 风格锁定（流程步骤 style.lock，项目级）：一个任务一轮，服务端按项目类型、风格文字、参考图（随任务以图片发给文本模型）和画风写风格锚
 * 和主基调色卡；收尾只出候选，用户点「锁定此风格」才写进项目。页面只订阅任务文本流。
 */
export async function runStyleLock(input: StyleLockRunInput, options?: ChapterTaskOptions) {
    const { task, usage } = await startStyleLock(input);
    options?.onTaskUpdate?.(task);
    const completed = await followDramaRunTask(task, options);
    return { task: completed, usage, outcome: dramaStyleLockOutcomeOf(completed) };
}

/** 场次色卡任务的 metadata.source（与后端步骤表 TaskSource 一致）。 */
export const SCENE_PALETTE_TASK_SOURCE = "short-drama-scene-palette";

/** 任务列表靠 clientContext（chapterOperation=scene-palette）认，带输入的任务详情按 metadata.source 认。 */
export function isScenePaletteTask(task: GenerationTask) {
    return task.clientContext?.chapterOperation === "scene-palette" || generationTaskMetadata(task.inputJson).source === SCENE_PALETTE_TASK_SOURCE;
}

type ScenePaletteInput = {
    projectId: string;
    config: AiConfig;
    /** 要做色卡的剧集（不给时全部；项目还没有剧集时是原文章节）。 */
    sourceUnitIds?: string[];
    resumeTaskId?: string;
};

/**
 * 场次色卡（流程步骤 scene.palette，项目级）：项目锁定风格后，服务端以风格锚和主基调色卡为参考一集一轮写逐场色彩脚本，任务成功前
 * 合并写进连戏表、每场建 / 加版本一张场次色卡并由代码合成色卡图（不调图片模型）。页面只订阅任务文本流按集显示。
 */
export async function runScenePalette(input: ScenePaletteInput, options?: ChapterTaskOptions) {
    const model = input.config.textModel || input.config.model;
    const config = { ...input.config, model };
    const { task, usage } = await runDramaProjectStep(input.projectId, "scene.palette", {
        model,
        ...(logicalModelIDForConfig(config) ? { logicalModelId: logicalModelIDForConfig(config) } : {}),
        config: backendProviderConfig(config, "text") as Record<string, unknown>,
        ...(input.sourceUnitIds?.length ? { sourceUnitIds: input.sourceUnitIds } : {}),
        ...(input.resumeTaskId ? { resumeTaskId: input.resumeTaskId } : {}),
    });
    announceCreatedGenerationTask(task);
    options?.onTaskUpdate?.(task);
    const completed = await followDramaRunTask(task, options);
    return { task: completed, usage, outcome: dramaScenePaletteOutcomeOf(completed) };
}

/** 订阅一个逐轮续写的步骤任务（生成分段、资产设计、风格锁定、剧本优化、场次色卡）直到结束，刷新页面后接回正在跑的任务也用它；失败时抛错，错误信息里有写到第几场 / 集。 */
export function followDramaRunTask(task: GenerationTask, options?: ChapterTaskOptions) {
    // 流结束时的 terminal 事件带的是结果正文（各场拼好的镜头行，没有回合标记），不能拿它盖掉按场分好的流。
    let streamedMarkers = false;
    const onTextDelta = options?.onStreamText && ((text: string) => {
        const hasMarkers = text.includes(`"${SHOT_TURN_MARKER}"`);
        if (streamedMarkers && !hasMarkers) return;
        streamedMarkers ||= hasMarkers;
        options.onStreamText?.(text);
    });
    return waitForGenerationTask(task.id, { initialTask: task, useTextEvents: true, timeoutMs: STORYBOARD_RUN_WAIT_MS, onTaskUpdate: options?.onTaskUpdate, onTextDelta }).catch(async (error: unknown) => {
        // 通用错误文案会把「第 3 场生成失败，可从第 3 场继续：HTTP 503」概括成网络异常；任务失败时直接用服务端写的原因。
        const latest = await queryGenerationTask(task.id).catch(() => undefined);
        if (latest && (latest.status === "failed" || latest.status === "cancelled") && latest.error) throw new Error(latest.error);
        throw error;
    });
}

function generationTaskMetadata(inputJson?: string): Record<string, unknown> {
    if (!inputJson) return {};
    try {
        const input = JSON.parse(inputJson) as { metadata?: unknown };
        return input.metadata && typeof input.metadata === "object" ? input.metadata as Record<string, unknown> : {};
    } catch {
        return {};
    }
}
