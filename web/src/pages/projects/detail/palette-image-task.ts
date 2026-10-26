import { modelCapabilityConfigFor, normalizeImageValue } from "@/lib/model-capabilities";
import type { DramaImageTaskTemplate } from "@/services/api/drama";
import { prepareBackendGenerationTask } from "@/services/api/generation-task";
import type { AiConfig } from "@/stores/use-config-store";

/** 色卡按《色卡提示词卡》出 16:9 横版；模型不支持 16:9 时按它自己的默认画幅。 */
const PALETTE_IMAGE_RATIO = "16:9";

/**
 * 色卡出图的图片任务模板：按图片模型组一个普通图片任务（与场景、道具出图同一套协议、渠道和参数换算），交给服务端；
 * 服务端每张色卡换上自己的英文提示词和资产出图的 metadata 后直接建任务，页面不用等。
 */
export async function paletteImageTaskTemplate(config: AiConfig, imageModel: string): Promise<DramaImageTaskTemplate> {
    const profile = modelCapabilityConfigFor(config, imageModel).image;
    const size = profile ? normalizeImageValue(profile, { size: PALETTE_IMAGE_RATIO, quality: config.quality, count: "1" }).size || config.size : PALETTE_IMAGE_RATIO;
    const task = await prepareBackendGenerationTask({ mode: "image", prompt: "色卡", config: { ...config, model: imageModel, count: "1", size }, metadata: { source: "short-drama-palette" } });
    return {
        model: task.model || imageModel,
        ...(task.logicalModelId ? { logicalModelId: task.logicalModelId } : {}),
        ...(task.provider ? { provider: task.provider } : {}),
        input: task.input as Record<string, unknown>,
    };
}
