import { runBackendCanvasGenerationTask } from "@/lib/canvas/canvas-project-generation";
import { PromptTemplateOperation, promptTemplateTaskPlaceholder } from "@/lib/prompts";
import { parseChapterAssetBreakdown, type ChapterAssetBreakdown } from "@/lib/canvas/chapter-asset-breakdown";
import { parseCharacterBreakdown } from "@/lib/canvas/canvas-character-reference";
import { backendProviderConfig, parseBackendGenerationResult } from "@/services/api/generation-task";
import { dramaRunProgressOf, dramaStepOutcomeOf, runDramaStep } from "@/services/api/drama";
import { announceCreatedGenerationTask, waitForGenerationTask, type GenerationTask } from "@/services/api/task-center";
import { logicalModelIDForConfig, type AiConfig } from "@/stores/use-config-store";

type ChapterAnalysisInput = {
    projectId: string;
    projectName: string;
    chapterId: string;
    chapterTitle: string;
    sourceText: string;
    projectStyle: string;
    config: AiConfig;
};

type ChapterTaskOptions = {
    onTaskUpdate?: (task: GenerationTask) => void;
    /** 任务文本流到目前为止的全文（各场输出与回合标记），给流式镜头视图用。 */
    onStreamText?: (text: string) => void;
};

export type ChapterTaskKind = "characters" | "storyboard";

export function chapterTaskIdentity(task: GenerationTask): { chapterId: string; kind: ChapterTaskKind } | null {
    const context = task.clientContext;
    if (context?.chapterId && (context.chapterOperation === "characters" || context.chapterOperation === "storyboard")) {
        return { chapterId: context.chapterId, kind: context.chapterOperation };
    }
    const metadata = generationTaskMetadata(task.inputJson);
    const chapterId = typeof metadata.chapterId === "string" ? metadata.chapterId : "";
    if (!chapterId) return null;
    if (metadata.operation === "chapter_character_breakdown") return { chapterId, kind: "characters" };
    if (metadata.source === "short-drama-chapter-storyboard") return { chapterId, kind: "storyboard" };
    return null;
}

export function chapterAssetsFromGenerationTask(task: GenerationTask) {
    const result = parseBackendGenerationResult(task);
    if (!result.text?.trim()) throw new Error("模型没有返回可用的章节资产提取结果");
    // 历史角色任务的输出契约只有角色；仅按明确的模板操作恢复，不能给新任务缺字段兜底。
    if (generationTaskMetadata(task.inputJson).promptTemplateOperation === PromptTemplateOperation.CharacterExtract) {
        return { characters: parseCharacterBreakdown(result.text), scenes: [], props: [] };
    }
    return parseChapterAssetBreakdown(result.text);
}

export async function extractChapterAssets(input: ChapterAnalysisInput, options?: ChapterTaskOptions): Promise<ChapterAssetBreakdown> {
    const result = await runProjectTextTask(input, "chapter_character_breakdown", {
        项目名称: input.projectName,
        章节名称: input.chapterTitle,
        项目画风: input.projectStyle || "项目尚未指定画风，保持视觉描述中性、可执行。",
        章节正文: input.sourceText,
    }, options);
    return parseChapterAssetBreakdown(result);
}

type ChapterStoryboardGenerationInput = {
    projectId: string;
    chapterId: string;
    config: AiConfig;
    /** 视频渠道实际能提交的段长，服务端装段时只收紧。 */
    rule?: { maxSeconds: number; minSeconds: number };
    /** 「从第 N 场继续」：上一次没写完的任务。 */
    resumeTaskId?: string;
};

// 逐场续写的任务在服务端按场计时（每场一次分镜超时），总时长随场数变；页面只是订阅，不替后端判超时。
const STORYBOARD_RUN_WAIT_MS = 8 * 60 * 60 * 1000;

/**
 * 生成分段（流程步骤 chapter.shots）：服务端按项目风格档案取绑定技能的当前版本组装提示词，在一个任务里按场续写，
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
    });
    announceCreatedGenerationTask(task);
    options?.onTaskUpdate?.(task);
    const completed = await followChapterStoryboardTask(task, options);
    return { task: completed, usage, outcome: dramaStepOutcomeOf(completed), progress: dramaRunProgressOf(completed) };
}

/** 订阅一个生成分段任务直到结束（刷新页面后接回正在跑的任务也用它）；失败时抛错，错误信息里有写到第几场。 */
export function followChapterStoryboardTask(task: GenerationTask, options?: ChapterTaskOptions) {
    return waitForGenerationTask(task.id, { initialTask: task, useTextEvents: true, timeoutMs: STORYBOARD_RUN_WAIT_MS, onTaskUpdate: options?.onTaskUpdate, onTextDelta: options?.onStreamText });
}

async function runProjectTextTask(input: ChapterAnalysisInput, operation: string, promptTemplateVariables: Record<string, string>, options?: ChapterTaskOptions) {
    const model = input.config.textModel || input.config.model;
    const result = await runBackendCanvasGenerationTask({
        projectId: input.projectId,
        nodeId: `${operation}:${input.chapterId}`,
        mode: "text",
        prompt: promptTemplateTaskPlaceholder("章节角色、场景与道具提取"),
        config: { ...input.config, model },
        metadata: { domainProjectId: input.projectId, chapterId: input.chapterId, operation, promptTemplateOperation: PromptTemplateOperation.ChapterAssetsExtract, promptTemplateVariables },
        onTaskCreated: options?.onTaskUpdate,
    });
    if (!result.text?.trim()) throw new Error("模型没有返回可用结果");
    return result.text;
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
