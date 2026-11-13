import { expect, test } from "bun:test";

import { defaultModelCapabilityConfig } from "../src/lib/model-capabilities";
import { modelCapabilityProblem } from "../src/lib/model-capability-error";
import { chainVideoConfig } from "../src/pages/drama-flow/model/video-config";
import { ApiError } from "../src/services/api/request";
import { defaultConfig, type AiConfig, type ModelChannel } from "../src/stores/use-config-store";

// 第九轮 R8-01：新工作流出片请求按所选模型能力对齐，只影响这次请求，不改全局偏好。

function configWith(options: { audio: boolean; slots?: number[]; resolutions?: string[] }): AiConfig {
    const capabilityConfig = defaultModelCapabilityConfig(undefined, "seedance-mg");
    const video = capabilityConfig.video!;
    video.generateAudio = { supported: options.audio, default: options.audio };
    video.watermark = { supported: false, default: false };
    if (options.slots) video.duration = { ...video.duration, selection: "enum", values: options.slots, default: options.slots[0] };
    if (options.resolutions) {
        video.resolutions = options.resolutions;
        video.defaultResolution = options.resolutions[0];
    }
    const channel: ModelChannel = {
        id: "CHANNEL_000001",
        name: "系统渠道",
        baseUrl: "/api/CHANNEL_000001",
        apiKey: "system",
        apiFormat: "openai",
        scope: "system",
        enabled: true,
        models: ["seedance-mg"],
        modelCosts: [{ model: "seedance-mg", displayName: "Seedance 2.5（MG）", capability: "video", billingMode: "fixed_request", unitPriceMicrocredits: 1, capabilityConfig }],
    } as ModelChannel;
    // 全局偏好：同步音频开、6 秒、1080。
    return { ...defaultConfig, channels: [channel], videoGenerateAudio: "true", videoWatermark: "true", videoSeconds: "6", vquality: "1080" };
}
const MODEL = "CHANNEL_000001::seedance-mg";

test("全局同步音频开、所选模型不支持：这次请求置 false，全局偏好对象不变", () => {
    const global = configWith({ audio: false, slots: [15, 30], resolutions: ["720p"] });
    const snapshot = JSON.stringify(global);
    const request = chainVideoConfig(global, MODEL, { aspectRatio: "9:16", contentSeconds: 20 });
    expect(request.videoGenerateAudio).toBe("false");
    expect(request.videoWatermark).toBe("false");
    expect(request.size).toBe("9:16");
    expect(request.model).toBe(MODEL);
    expect(request.videoModel).toBe(MODEL);
    expect(JSON.stringify(global)).toBe(snapshot);
});

test("模型支持同步音频时保留用户的合法选择", () => {
    const request = chainVideoConfig(configWith({ audio: true, slots: [15, 30] }), MODEL, { contentSeconds: 10 });
    expect(request.videoGenerateAudio).toBe("true");
});

test("时长按这一段的正文取档：全局 6 秒、20 秒镜头、只有 15/30 档 → 30；10 秒 → 15；超过 30 不给档", () => {
    const global = configWith({ audio: false, slots: [15, 30] });
    expect(chainVideoConfig(global, MODEL, { contentSeconds: 20 }).videoSeconds).toBe("30");
    expect(chainVideoConfig(global, MODEL, { contentSeconds: 30 }).videoSeconds).toBe("30");
    expect(chainVideoConfig(global, MODEL, { contentSeconds: 10 }).videoSeconds).toBe("15");
    // 超过最大档时前端不编一个档位出来，留给后端按权威能力拒绝。
    expect(chainVideoConfig(global, MODEL, { contentSeconds: 31 }).videoSeconds).toBe("6");
    // 没有目标段（只用来判断能不能出片）时不改时长。
    expect(chainVideoConfig(global, MODEL, {}).videoSeconds).toBe("6");
});

test("分辨率不在模型可选范围里时用模型默认，写法和后端一致（不带 p）", () => {
    expect(chainVideoConfig(configWith({ audio: false, resolutions: ["720p"] }), MODEL, {}).vquality).toBe("720");
});

test("能力错误说明：按 reason / details 写出参数和原因；没有 details 退回原文案；不是能力错误返回 undefined", () => {
    const detailed = new ApiError("所选模型不支持当前请求", {
        status: 400,
        reason: "model_capability_not_supported",
        details: {
            field: "videoGenerateAudio",
            message: "参数 同步音频超出支持范围",
            capabilityIssues: [
                { kind: "option", name: "videoGenerateAudio", reason: "参数 同步音频超出支持范围" },
                { kind: "option", name: "videoSeconds", reason: "参数 时长超出支持范围" },
            ],
        },
    });
    const text = modelCapabilityProblem(detailed)!;
    expect(text).toContain("同步音频超出支持范围");
    expect(text).toContain("时长超出支持范围");
    expect(text.match(/同步音频超出支持范围/g)?.length).toBe(1);
    expect(modelCapabilityProblem(new ApiError("所选模型不支持当前请求", { status: 400, reason: "model_capability_not_supported" }))).toBe("所选模型不支持当前请求");
    expect(modelCapabilityProblem(new ApiError("项目不存在", { status: 404, reason: "not_found" }))).toBeUndefined();
    expect(modelCapabilityProblem(new Error("network"))).toBeUndefined();
});
