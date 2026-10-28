import { modelCapabilityConfigFor, normalizeImageValue } from "@/lib/model-capabilities";
import type { DramaImageTaskTemplate } from "@/services/api/drama";
import { prepareBackendGenerationTask } from "@/services/api/generation-task";
import type { AiConfig } from "@/stores/use-config-store";

/**
 * 服务端出图的图片任务模板：按图片模型组一个普通图片任务（与场景、道具出图同一套协议、渠道和参数换算），交给服务端；
 * 服务端每个资产换上自己的提示词和资产出图的 metadata 后直接建任务，页面不用等。画幅按当前设置（色卡由代码合成，不走这里）。
 */
async function dramaImageTaskTemplate(config: AiConfig, imageModel: string, placeholder: { prompt: string; source: string }): Promise<DramaImageTaskTemplate> {
    // 画幅按模型能力换算成它认的值（像素尺寸、比例或预设），换不出来时用当前设置的画幅（没有能力配置时原样交给建任务时校验）。
    const requested = config.size;
    const profile = modelCapabilityConfigFor(config, imageModel).image;
    const size = profile ? normalizeImageValue(profile, { size: requested, quality: config.quality, count: "1" }).size || config.size : requested;
    const task = await prepareBackendGenerationTask({ mode: "image", prompt: placeholder.prompt, config: { ...config, model: imageModel, count: "1", size }, metadata: { source: placeholder.source } });
    // 参考图名额：服务端给资产出图挂色卡图和主卡三视图时按它挑（不知道时不写，建任务被拒再降级）。
    const maxReferenceImages = profile?.references?.maxImages;
    return {
        model: task.model || imageModel,
        ...(task.logicalModelId ? { logicalModelId: task.logicalModelId } : {}),
        ...(task.provider ? { provider: task.provider } : {}),
        input: task.input as Record<string, unknown>,
        ...(typeof maxReferenceImages === "number" && maxReferenceImages >= 0 ? { maxReferenceImages } : {}),
    };
}

/** 资产设计出图的模板：角色三视图、场景和道具概念图，画幅按图片模型的配置（与资产页手动出图一致）。 */
export function assetImageTaskTemplate(config: AiConfig, imageModel: string) {
    return dramaImageTaskTemplate(config, imageModel, { prompt: "资产出图", source: "short-drama-assets" });
}
