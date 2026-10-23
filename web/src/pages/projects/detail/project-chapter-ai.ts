import { runBackendCanvasGenerationTask } from "@/lib/canvas/canvas-project-generation";
import { PromptTemplateOperation, promptTemplateTaskPlaceholder } from "@/lib/prompts";
import { storyboardRowsFromTask } from "@/lib/canvas/canvas-project-domain";
import { parseChapterAssetBreakdown, type ChapterAssetBreakdown } from "@/lib/canvas/chapter-asset-breakdown";
import { parseCharacterBreakdown } from "@/lib/canvas/canvas-character-reference";
import { backendProviderConfig, parseBackendGenerationResult } from "@/services/api/generation-task";
import { runDramaStep } from "@/services/api/drama";
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

export function chapterStoryboardFromGenerationTask(task: GenerationTask) {
    return storyboardRowsFromTask(task);
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
};

/**
 * 生成分段（流程步骤 chapter.shots）：服务端按项目风格档案取绑定技能的当前版本，组装提示词（技能上下文、项目画风、
 * 已确认角色外观、资产清单与锚点、每集时长、按 schema 生成的契约）并建文本任务；这里只交文本模型选择，等任务结束后解析分镜行。
 */
export async function generateChapterStoryboard(input: ChapterStoryboardGenerationInput, options?: ChapterTaskOptions) {
    const model = input.config.textModel || input.config.model;
    const config = { ...input.config, model };
    const { task, usage } = await runDramaStep(input.projectId, input.chapterId, "chapter.shots", {
        model,
        ...(logicalModelIDForConfig(config) ? { logicalModelId: logicalModelIDForConfig(config) } : {}),
        config: backendProviderConfig(config, "text") as Record<string, unknown>,
    });
    announceCreatedGenerationTask(task);
    options?.onTaskUpdate?.(task);
    const completed = await waitForGenerationTask(task.id, { initialTask: task, useTextEvents: true, onTaskUpdate: options?.onTaskUpdate });
    return { ...storyboardRowsFromTask(completed), usage };
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
