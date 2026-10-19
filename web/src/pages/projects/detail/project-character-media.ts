import { runBackendCanvasGenerationTask } from "@/lib/canvas/canvas-project-generation";
import { PromptTemplateOperation, promptTemplateTaskPlaceholder } from "@/lib/prompts";
import { resolveStyleExecutionPlan, serializeStyleProfile, type StyleProfileSnapshot } from "@/lib/canvas/style-profile";
import type { GenerationTask } from "@/services/api/task-center";
import { configuredModelMatchesCapability, logicalModelIDForConfig, resolveModelRequestConfig, type AiConfig } from "@/stores/use-config-store";

type ProjectStylePrompt = { id: string; title: string; prompt: string; profile?: StyleProfileSnapshot };

const CHARACTER_TURNAROUND_NODE_PREFIX = "character-turnaround:";

/** 三视图优先用项目默认图片模型；没设或当前渠道里没有这个模型时，才退回全局图片模型。 */
export function turnaroundImageModel(config: AiConfig, projectDefaultImageModel?: string) {
    const projectModel = projectDefaultImageModel?.trim() || "";
    if (projectModel && configuredModelMatchesCapability(config, projectModel, "image")) return projectModel;
    return config.imageModel || config.model;
}

/**
 * 三视图任务由后端在完成时绑定到新角色版本；页面刷新或离开后，前端的 mutation 状态就丢了。
 * 按任务的 nodeId 找回还在排队或生成的角色，页面据此显示生成中，避免重复提交、重复扣费。
 */
export function pendingTurnaroundAssetIds(tasks: GenerationTask[]) {
    const ids = new Set<string>();
    for (const task of tasks) {
        if (task.status !== "queued" && task.status !== "running") continue;
        const nodeId = task.clientContext?.nodeId || "";
        if (nodeId.startsWith(CHARACTER_TURNAROUND_NODE_PREFIX)) ids.add(nodeId.slice(CHARACTER_TURNAROUND_NODE_PREFIX.length));
    }
    return ids;
}

export async function generateCharacterTurnaround(input: { projectId: string; assetId: string; versionId: string; name: string; definition: Record<string, unknown>; projectStyle?: ProjectStylePrompt; config: AiConfig }) {
    const promptTemplateVariables = characterTurnaroundVariables(input.name, input.definition, input.projectStyle);
    const generationConfig = { ...input.config, model: input.config.imageModel || input.config.model, count: "1" };
    const requestConfig = resolveModelRequestConfig(generationConfig, generationConfig.model);
    const styleExecutionPlan = input.projectStyle?.profile ? resolveStyleExecutionPlan(input.projectStyle.profile, { mode: "image", model: requestConfig.model, interfaceType: requestConfig.interfaceType || requestConfig.apiFormat }) : undefined;
    if (styleExecutionPlan?.status === "blocked" && !logicalModelIDForConfig(generationConfig)) throw new Error(`当前图片模型无法完整执行项目画风：${styleExecutionPlan.warnings.join("；")}。请切换图片模型，或在项目设置中停用对应画风资产`);
    await runBackendCanvasGenerationTask({
        projectId: input.projectId,
        nodeId: `${CHARACTER_TURNAROUND_NODE_PREFIX}${input.assetId}`,
        mode: "image",
        prompt: promptTemplateTaskPlaceholder("角色三视图"),
        config: generationConfig,
        metadata: { operation: PromptTemplateOperation.CharacterTurnaround, promptTemplateOperation: PromptTemplateOperation.CharacterTurnaround, promptTemplateVariables, characterAssetId: input.assetId, stylePresetId: input.projectStyle?.id, styleProfileJson: input.projectStyle?.profile ? serializeStyleProfile(input.projectStyle.profile) : undefined, styleExecutionPlan, resolvedCharacterVersions: [{ assetId: input.assetId, versionId: input.versionId }] },
    });
}

function characterTurnaroundVariables(name: string, definition: Record<string, unknown>, projectStyle?: ProjectStylePrompt) {
    const visual = [definition.role, definition.appearance, definition.physique, definition.clothing, definition.personality, definition.props, definition.consistencyPrompt, definition.multiViewPrompt]
        .map((value) => String(value || "").trim())
        .filter(Boolean)
        .join("；");
    if (!visual) throw new Error("请先填写剧情定位、角色外貌、体型或服装，再初始化三视图");
    return { 角色名称: name, 项目画风: characterStyleConstraint(projectStyle) || "项目尚未指定画风，保持角色媒介中性。", 角色设定: visual };
}

function characterStyleConstraint(projectStyle?: ProjectStylePrompt) {
    if (!projectStyle) return "";
    // 三视图只继承角色资产相关规范，避免把建筑、运镜等项目规则错误塞进静态设定表。
    const characterSections = new Set(["风格组合", "项目定位", "题材世界观", "视觉媒介", "角色设计系统", "项目色彩与光影", "服饰、材质与场景", "资产一致性", "全局禁用"]);
    const rules = projectStyle.prompt.split("\n").filter((line) => {
        const title = line.match(/^【([^】]+)】/)?.[1];
        return title ? characterSections.has(title) : false;
    });
    return [
        `项目画风：${projectStyle.title}。角色造型、配色、服装材质与最终渲染媒介必须遵循以下规范：`,
        ...rules,
    ].join("\n");
}
