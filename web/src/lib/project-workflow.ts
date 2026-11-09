import type { ProjectAssetWorkflow } from "@/services/api/projects";

export const PROJECT_WORKFLOWS = [
    { value: "pipeline-1", label: "流水线一 本体论导演", description: "本体论导演分镜 · 角色四宫格 · 微缩场景图", integratedAssets: true },
    { value: "pipeline-2", label: "流水线二 资产一体化", description: "原分镜流程 · 角色四宫格 · 微缩场景图", integratedAssets: true },
    { value: "pipeline-3", label: "流水线三 原流程", description: "原分镜与资产提取 · 角色三视图 · 场景概念图", integratedAssets: false },
] as const satisfies ReadonlyArray<{ value: ProjectAssetWorkflow; label: string; description: string; integratedAssets: boolean }>;

export function projectWorkflow(id: ProjectAssetWorkflow | "" | undefined) {
    return PROJECT_WORKFLOWS.find((workflow) => workflow.value === id) ?? PROJECT_WORKFLOWS[2];
}
