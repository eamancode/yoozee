import { modelCapabilityConfigFor, resolveVideoResolutionValue } from "@/lib/model-capabilities";
import { modelVideoSubmissionSeconds, resolveModelVideoBooleanOptions } from "@/lib/model-selection";
import type { AiConfig } from "@/stores/use-config-store";

/**
 * 新工作流出片请求用的视频配置：按这次所选视频模型的能力对齐，只用于这一次请求，不写回全局偏好。
 * 费用预览和确认后的真正提交都走连续生成面板的同一个 buildModel，所以报价和提交是同一份配置。
 * - 同步音频、水印：模型不支持就置 false，支持就保留全局里的合法选择（和旧工作台同一个 resolveModelVideoBooleanOptions）。
 * - 时长：按这一段的正文时长取模型能提交的档位（15/30 秒档等），不用全局偏好里的秒数；后端仍会按权威能力再定档、校验。
 * - 分辨率：全局值不在模型的可选范围里时取模型默认，写法和后端一致（不带 p 后缀）。
 * - 画幅用项目的；模型是空的时只带上模型字段，交给调用方判断「还没配好」。
 */
export function chainVideoConfig(config: AiConfig, model: string, input: { aspectRatio?: string; contentSeconds?: number }): AiConfig {
    const base = { ...config, model, videoModel: model, size: input.aspectRatio || config.size };
    if (!model) return base;
    const profile = modelCapabilityConfigFor(config, model).video;
    if (!profile) return base;
    const booleans = resolveModelVideoBooleanOptions(config, model, {}, { videoGenerateAudio: config.videoGenerateAudio, videoWatermark: config.videoWatermark });
    const seconds = input.contentSeconds !== undefined ? modelVideoSubmissionSeconds(config, model, input.contentSeconds) : undefined;
    const resolution = resolveVideoResolutionValue(profile, config.vquality).replace(/p$/i, "");
    return { ...base, ...booleans, ...(resolution ? { vquality: resolution } : {}), ...(seconds !== undefined ? { videoSeconds: String(seconds) } : {}) };
}
