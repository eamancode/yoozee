import { localForageStorage } from "@/lib/localforage-storage";
import type { ProjectSourceKind } from "@/lib/project-source";
import type { DramaRunMode } from "@/services/api/drama";
import type { ProjectVisualType } from "@/services/api/projects";

/** 建项草稿：按当前账号 scope 存进 localforage（不进 localStorage），刷新或换页回来接着填。参考图是本地文件，不进草稿。 */
export type ProjectCreateDraft = {
    text: string;
    /** 导入的文件名（文件卡显示）；粘贴的正文为空。 */
    fileName?: string;
    /** 导入时选的切法：按章 / 集标题切（默认），false 是整篇算一章（一集）。 */
    fileSplit?: boolean;
    /** 用户改过的识别类型；不给时按正文自动识别。 */
    kind?: ProjectSourceKind;
    /** 先建一个空项目。 */
    blank?: boolean;
    ideaEpisodes: number;
    ideaEpisodeSeconds: number;
    /** 用户改过的项目名；空时按正文自动生成。 */
    name?: string;
    visualType?: ProjectVisualType;
    styleMode: "director" | "library";
    styleBrief: string;
    styleId?: string;
    aspectRatio: string;
    segmentSeconds: number;
    generationModel?: string;
    runMode: DramaRunMode;
};

const DRAFT_KEY = "yingce:project-create-draft";

export const EMPTY_PROJECT_DRAFT: ProjectCreateDraft = {
    text: "",
    ideaEpisodes: 5,
    ideaEpisodeSeconds: 60,
    styleMode: "director",
    styleBrief: "",
    aspectRatio: "9:16",
    segmentSeconds: 15,
    runMode: "until_video",
};

export async function loadProjectDraft(): Promise<ProjectCreateDraft | undefined> {
    const raw = await localForageStorage.getItem(DRAFT_KEY);
    if (typeof raw !== "string" || !raw) return undefined;
    try {
        const draft = JSON.parse(raw) as Partial<ProjectCreateDraft>;
        return { ...EMPTY_PROJECT_DRAFT, ...draft };
    } catch {
        return undefined;
    }
}

export async function saveProjectDraft(draft: ProjectCreateDraft) {
    await localForageStorage.setItem(DRAFT_KEY, JSON.stringify(draft));
}

export async function clearProjectDraft() {
    await localForageStorage.removeItem(DRAFT_KEY);
}

/** 草稿有没有用户填过的内容（只有默认值时不算草稿）。 */
export function projectDraftHasContent(draft: ProjectCreateDraft) {
    return Boolean(draft.text.trim() || draft.blank || draft.visualType || draft.styleBrief.trim() || draft.styleId);
}
