import { modelCapabilityConfigFor, normalizeImageValue } from "@/lib/model-capabilities";
import type { DramaImageTaskTemplate } from "@/services/api/drama";
import { prepareBackendGenerationTask } from "@/services/api/generation-task";
import type { AiConfig } from "@/stores/use-config-store";

/** 色卡按《色卡提示词卡》出 16:9 横版；模型不支持 16:9 时按它自己的默认画幅。 */
const PALETTE_IMAGE_RATIO = "16:9";

/**
 * 服务端出图的图片任务模板：按图片模型组一个普通图片任务（与场景、道具出图同一套协议、渠道和参数换算），交给服务端；
 * 服务端每张色卡、每个资产换上自己的提示词和资产出图的 metadata 后直接建任务，页面不用等。ratio 不给时按图片模型配置的画幅。
 */
async function dramaImageTaskTemplate(config: AiConfig, imageModel: string, placeholder: { prompt: string; source: string }, ratio?: string): Promise<DramaImageTaskTemplate> {
    const profile = ratio ? modelCapabilityConfigFor(config, imageModel).image : undefined;
    const size = ratio ? (profile ? normalizeImageValue(profile, { size: ratio, quality: config.quality, count: "1" }).size || config.size : ratio) : config.size;
    const task = await prepareBackendGenerationTask({ mode: "image", prompt: placeholder.prompt, config: { ...config, model: imageModel, count: "1", size }, metadata: { source: placeholder.source } });
    return {
        model: task.model || imageModel,
        ...(task.logicalModelId ? { logicalModelId: task.logicalModelId } : {}),
        ...(task.provider ? { provider: task.provider } : {}),
        input: task.input as Record<string, unknown>,
    };
}

/** 色卡出图的模板（16:9）。 */
export function paletteImageTaskTemplate(config: AiConfig, imageModel: string) {
    return dramaImageTaskTemplate(config, imageModel, { prompt: "色卡", source: "short-drama-palette" }, PALETTE_IMAGE_RATIO);
}

/** 资产设计出图的模板：角色三视图、场景和道具概念图，画幅按图片模型的配置（与资产页手动出图一致）。 */
export function assetImageTaskTemplate(config: AiConfig, imageModel: string) {
    return dramaImageTaskTemplate(config, imageModel, { prompt: "资产出图", source: "short-drama-assets" });
}
