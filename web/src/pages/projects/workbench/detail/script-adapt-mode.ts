import type { DramaScriptAdaptMode } from "@/services/api/project-drama";

/**
 * 剧本优化对话框默认选哪种：导入剧本的项目、没有原文章节，或原文章节都已改编成剧集（没有可改编的了）时默认「只调节奏」，
 * 否则默认「小说转剧本」。adaptedChapterIds 还没读到时按都没改编算，读到后再定一次。
 */
export function scriptAdaptDefaultMode(input: { sourceType?: string; chapterIds: string[]; episodeCount: number; adaptedChapterIds?: Iterable<string> }): DramaScriptAdaptMode {
    if (input.sourceType === "script" || !input.chapterIds.length) return "rhythm";
    if (!input.episodeCount) return "adapt";
    const adapted = new Set(input.adaptedChapterIds || []);
    return input.chapterIds.every((id) => adapted.has(id)) ? "rhythm" : "adapt";
}
