import type { ProjectVisualType } from "@/services/api/projects";
import type { StyleProfileSnapshot } from "./canvas/style-profile";

/**
 * 画面类型（建项第二段，必选、建项后不可改）：与后端 drama.ProjectTypes 同值同序。平台风格库按它归档，
 * 风格档案按它推（仿真人、写实 CG 走 live-realism，3D、2D 动漫走 anime-3d）。
 */
export const PROJECT_VISUAL_TYPES: ReadonlyArray<{ value: ProjectVisualType; label: string; hint: string }> = [
    { value: "live_action", label: "仿真人", hint: "真人实拍质感" },
    { value: "cg_real", label: "写实 CG", hint: "写实渲染的 CG 角色与场景" },
    { value: "anime_3d", label: "3D 动漫", hint: "3D 动画、3D 卡通" },
    { value: "anime_2d", label: "2D 动漫", hint: "国漫 2D、水墨动画" },
];

/** 画面类型的中文名；不认识返回空串。 */
export function projectVisualTypeLabel(value: string | undefined) {
    return PROJECT_VISUAL_TYPES.find((item) => item.value === value)?.label || "";
}

export function isProjectVisualType(value: string | undefined): value is ProjectVisualType {
    return PROJECT_VISUAL_TYPES.some((item) => item.value === value);
}

export function styleProfileSupportsVisualType(profile: StyleProfileSnapshot, visualType: ProjectVisualType) {
    if (profile.director?.visualType) return profile.director.visualType === visualType;
    const medium = profile.selection?.medium;
    if (!medium || !["live-action", "cg-real", "3d-anime", "3d-cartoon", "2d-guoman", "ink"].includes(medium)) return true;
    const allowed: Record<ProjectVisualType, string[]> = { live_action: ["live-action"], cg_real: ["cg-real", "live-action", "3d-anime"], anime_3d: ["3d-anime", "3d-cartoon"], anime_2d: ["2d-guoman", "ink"] };
    return allowed[visualType].includes(medium);
}
