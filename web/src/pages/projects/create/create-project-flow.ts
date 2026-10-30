import { shouldRetryProjectNameConflict } from "@/lib/project-name-conflict";
import { plainTextToHtml } from "@/lib/project-source";
import { PromptTemplateOperation, parseGeneratedStory, promptTemplateTaskPlaceholder, shortDramaOutlineVariables } from "@/lib/prompts";
import { API_ERROR_REASON } from "@/services/api/error-codes";
import { runBackendGenerationTask } from "@/services/api/generation-task";
import { createProject, importProjectUnits, type CreateProjectInput } from "@/services/api/projects";
import { ApiError } from "@/services/api/request";
import type { AiConfig } from "@/stores/use-config-store";

/** 建项页的四段。 */
export type ComposerSection = "source" | "visual" | "style" | "production";

/** 后端按段给的 reason → 错误落在哪一段（其余错误不落段，在摘要栏显示）。 */
export function composerSectionOfError(error: unknown): ComposerSection | undefined {
    if (!(error instanceof ApiError)) return undefined;
    switch (error.reason) {
        case API_ERROR_REASON.ProjectSourceInvalid:
            return "source";
        case API_ERROR_REASON.ProjectVisualTypeRequired:
            return "visual";
        case API_ERROR_REASON.ProjectStyleInvalid:
            return "style";
        case API_ERROR_REASON.ProjectProductionInvalid:
            return "production";
    }
    return undefined;
}

/** 建项：同名时在名字后面加「（2）」「（3）」重试（和原先一样）。 */
export async function createProjectWithUniqueName(input: CreateProjectInput) {
    const base = input.name.trim();
    for (let attempt = 0; ; attempt += 1) {
        try {
            return await createProject({ ...input, name: attempt === 0 ? base : `${base}（${attempt + 1}）` });
        } catch (error) {
            if (!shouldRetryProjectNameConflict(error, attempt)) throw error;
        }
    }
}

/** 创意的每集字数：按每集时长折（约 800 字一分钟），叙述视角、结构、基调、角色规模交给 Agent 用默认值。 */
export function ideaWordCount(episodeSeconds: number) {
    return String(Math.max(300, Math.round((episodeSeconds / 60) * 800)));
}

/**
 * 创意建项后走「AI 生成章节」：按集数生成大纲和章节，导入成原文章节；拆剧本那一步再按集数改编成剧集。
 * onDelta 是模型输出到目前为止的全文（页面显示进度）。
 */
export async function generateIdeaChapters(input: { projectId: string; idea: string; episodes: number; episodeSeconds: number; config: AiConfig; textModel: string; onDelta?: (text: string) => void }) {
    const result = await runBackendGenerationTask({
        projectId: input.projectId,
        mode: "text",
        prompt: promptTemplateTaskPlaceholder("短剧大纲"),
        config: { ...input.config, model: input.textModel, imageModel: input.textModel, videoModel: input.textModel, textModel: input.textModel },
        metadata: {
            source: "project-story-generator",
            projectId: input.projectId,
            promptTemplateOperation: PromptTemplateOperation.ShortDramaOutline,
            promptTemplateVariables: shortDramaOutlineVariables({
                story: input.idea,
                chapterCount: String(input.episodes),
                structure: "单线推进",
                wordCount: ideaWordCount(input.episodeSeconds),
                perspective: "第三人称",
                tone: "平稳叙事",
                characterScale: "3-4 个",
                chapterLength: "中",
            }),
        },
        onTextDelta: input.onDelta,
    });
    const parsed = parseGeneratedStory(result.text || "");
    if (!parsed.chapters.length) throw new Error("AI 没有返回有效的章节内容，项目已建好：到章节页导入正文，或删掉项目重新建");
    await importProjectUnits(
        input.projectId,
        parsed.chapters.map((chapter: { title: string; content: string }) => ({ kind: "chapter", title: chapter.title, sourceText: plainTextToHtml(chapter.content) })),
    );
    return parsed.chapters.length;
}
