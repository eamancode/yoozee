import type { CanvasStylePreset, ProjectStyleMediumId } from "@/lib/canvas/canvas-style-system";

/**
 * 建项目第一步的「类型」。类型不落库，只按画风的媒介（selection.medium）过滤画风预设；
 * 风格档案再由画风对照表（后端 style_map.json）推出。写实 CG、2D 动漫以后补了自己的档案，只改这张表和对照表。
 */
export const PROJECT_CONTENT_TYPES = [
    { value: "live-action", label: "仿真人", media: ["live-action"], hint: "真人实拍质感" },
    { value: "3d-anime", label: "3D 动漫", media: ["3d-anime", "3d-cartoon"], hint: "3D 动画、3D 卡通" },
    { value: "realistic-cg", label: "写实 CG", media: ["live-action"], hint: "暂用真人写实画风，专属画风待补" },
    { value: "2d-anime", label: "2D 动漫", media: ["2d-guoman", "ink"], hint: "国漫 2D、水墨动画" },
] as const satisfies ReadonlyArray<{ value: string; label: string; media: readonly ProjectStyleMediumId[]; hint: string }>;

export type ProjectContentType = (typeof PROJECT_CONTENT_TYPES)[number]["value"];

export function projectContentType(value: string | undefined) {
    return PROJECT_CONTENT_TYPES.find((item) => item.value === value);
}

/** 画风的媒介：系统预设取组合选择，自定义画风取快照里的辅助标注；都没有返回空串（认不出媒介）。 */
export function canvasStyleMedium(preset: Pick<CanvasStylePreset, "selection" | "profile">) {
    return preset.selection?.medium || preset.profile?.selection?.medium || "";
}

/** 画风是否属于这个类型。认不出媒介的自定义画风不归任何类型。 */
export function styleMatchesContentType(preset: Pick<CanvasStylePreset, "selection" | "profile">, type: ProjectContentType | undefined) {
    const media: readonly string[] | undefined = projectContentType(type)?.media;
    if (!media) return true;
    return media.includes(canvasStyleMedium(preset));
}
