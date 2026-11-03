import { describe, expect, test } from "bun:test";
import { fixedVideoDuration, fixedVideoSubmissionSeconds } from "../src/lib/fixed-video-duration";
import { defaultModelCapabilityConfig, type VideoCapabilityConfig } from "../src/lib/model-capabilities";
import { modelCompatibilityError, modelVideoSubmissionSeconds } from "../src/lib/model-selection";
import { modelQuoteRequest, requestCreditCost } from "../src/lib/model-pricing";
import { createModelChannel, defaultConfig, type AiConfig } from "../src/stores/use-config-store";
import { segmentDurationRule } from "../src/pages/projects/workbench/detail/chapter-segments";
import { shotVideoSeconds } from "../src/pages/projects/workbench/detail/shot-generation";
import { sourceCharCount, PROJECT_SOURCE_MAX_CHARS } from "../src/lib/project-source";

function fixture(slot: number, selection: "enum" | "range" = "enum") {
    const duration: VideoCapabilityConfig["duration"] = { selection, values: [slot], min: slot, max: slot, default: slot };
    const profile = { ...defaultModelCapabilityConfig().video!, duration };
    const channel = createModelChannel({
        id: "fixed",
        scope: "system",
        models: ["model"],
        modelCosts: [{ model: "model", capability: "video", billingMode: "per_second", unitPriceMicrocredits: 1_000_000, capabilityConfig: { version: 1, video: profile } }],
    });
    const config: AiConfig = { ...defaultConfig, channels: [channel], model: "fixed::model", videoModel: "fixed::model" };
    return { duration, profile, config };
}

describe("项目固定 15/30 秒模型", () => {
    for (const slot of [15, 30])
        for (const selection of ["enum", "range"] as const) {
            test(`${selection} 固定 ${slot} 秒：选择、报价、提交使用同一实际时长`, () => {
                const { duration, profile, config } = fixture(slot, selection);
                for (const seconds of [0.5, 10, slot - 0.001, slot]) {
                    const requirements = { capability: "video" as const, videoContentSeconds: seconds, videoSeconds: String(seconds) };
                    expect(modelCompatibilityError(config, config.model, requirements)).toBe("");
                    expect(modelVideoSubmissionSeconds(config, config.model, seconds)).toBe(slot);
                    expect(modelQuoteRequest(config, config.model, "video", requirements)?.intent.options.videoSeconds).toBe(slot);
                    expect(requestCreditCost({ channelMode: "remote", modelCosts: config.channels[0].modelCosts, model: "model", config, capability: "video", seconds, requirements })).toBe(slot);
                    expect(segmentDurationRule(15, duration).submitSeconds(seconds)).toBe(slot);
                    expect(shotVideoSeconds({ shotSeconds: seconds, profile, segmentMaxSeconds: 15 })).toBe(seconds);
                    expect(shotVideoSeconds({ specSeconds: seconds, shotSeconds: 1, profile })).toBe(seconds);
                }
                for (const seconds of [0, -1, slot + 0.001, Number.NaN, Number.POSITIVE_INFINITY]) {
                    const requirements = { capability: "video" as const, videoContentSeconds: seconds };
                    expect(modelCompatibilityError(config, config.model, requirements)).not.toBe("");
                    expect(modelVideoSubmissionSeconds(config, config.model, seconds)).toBeUndefined();
                    expect(modelQuoteRequest(config, config.model, "video", requirements)).toBeUndefined();
                    expect(segmentDurationRule(15, duration).submitSeconds(seconds)).toBeUndefined();
                }
            });
        }
    test("模型菜单用内容时长重新匹配候选模型，普通画布不改变原规则", () => {
        const fixed30 = fixture(30).config;
        expect(modelCompatibilityError(fixed30, fixed30.model, { capability: "video", videoSeconds: "15", videoContentSeconds: 20 })).toBe("");
        expect(modelCompatibilityError(fixed30, fixed30.model, { capability: "video", videoSeconds: "20" })).not.toBe("");
        expect(fixedVideoDuration({ selection: "enum", values: [4, 30], default: 4 })).toBeUndefined();
        expect(fixedVideoSubmissionSeconds(undefined, 20)).toBe(20);
    });
});

test("15 万字计数按 Unicode 字符计算，不把表情计作两个字符", () => {
    expect(sourceCharCount("雨 😀\n夜")).toBe(3);
    expect(sourceCharCount("雨".repeat(150000))).toBe(PROJECT_SOURCE_MAX_CHARS);
});
