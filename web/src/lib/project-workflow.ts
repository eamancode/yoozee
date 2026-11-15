import type { ProjectAssetWorkflow } from "@/services/api/projects";

/**
 * 流水线（与后端 drama.Workflow 同口径）。storyboardMode 是分镜环节的模式：sss（SSS 导演，八部分）、ontology（本体论，七部分）、
 * 空（原分镜流程）。aspectRatio 是选这条流水线时预填的画幅（用户仍可改）。
 */
export const PROJECT_WORKFLOWS = [
    { value: "pipeline-1", label: "流水线一 SSS 电影导演", description: "SSS 电影导演分镜 · 角色四宫格（固定 9:16） · 微缩场景图", integratedAssets: true, storyboardMode: "sss", aspectRatio: "16:9" },
    { value: "pipeline-2", label: "流水线二 SSS 短剧导演", description: "SSS 短剧导演分镜 · 角色四宫格（固定 9:16） · 微缩场景图", integratedAssets: true, storyboardMode: "sss", aspectRatio: "9:16" },
    { value: "pipeline-3", label: "流水线三 本体论导演", description: "本体论导演分镜 · 角色四宫格（固定 9:16） · 微缩场景图", integratedAssets: true, storyboardMode: "ontology", aspectRatio: undefined },
    { value: "pipeline-4", label: "流水线四 资产一体化", description: "原分镜流程 · 角色四宫格（固定 9:16） · 微缩场景图", integratedAssets: true, storyboardMode: "", aspectRatio: undefined },
    { value: "pipeline-5", label: "流水线五 原流程", description: "原分镜与资产提取 · 角色三视图 · 场景概念图", integratedAssets: false, storyboardMode: "", aspectRatio: undefined },
] as const satisfies ReadonlyArray<{ value: ProjectAssetWorkflow; label: string; description: string; integratedAssets: boolean; storyboardMode: "sss" | "ontology" | ""; aspectRatio: string | undefined }>;

/** 新建项目默认的流水线（与后端 drama.DefaultWorkflow 一致）。 */
export const DEFAULT_PROJECT_WORKFLOW: ProjectAssetWorkflow = "pipeline-2";

export function projectWorkflow(id: ProjectAssetWorkflow | "" | undefined) {
    return PROJECT_WORKFLOWS.find((workflow) => workflow.value === id) ?? PROJECT_WORKFLOWS[4];
}

/** 这条流水线的分镜有没有导演文档（圣经、各集七部分 / 八部分、交接卡、单元返修）。 */
export function hasDirectorDocuments(id: ProjectAssetWorkflow | "" | undefined) {
    return projectWorkflow(id).storyboardMode !== "";
}
