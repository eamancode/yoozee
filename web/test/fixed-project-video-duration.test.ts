import { describe, expect, test } from "bun:test";
import { projectVideoDurationSlots, projectVideoSubmissionSeconds } from "../src/lib/fixed-video-duration";
import { defaultModelCapabilityConfig, type VideoCapabilityConfig } from "../src/lib/model-capabilities";
import { modelCompatibilityError, modelVideoSubmissionSeconds } from "../src/lib/model-selection";
import { modelQuoteRequest, requestCreditCost } from "../src/lib/model-pricing";
import { createModelChannel, defaultConfig, type AiConfig } from "../src/stores/use-config-store";
import { segmentDurationRule } from "../src/pages/projects/workbench/detail/chapter-segments";
import { shotVideoSeconds } from "../src/pages/projects/workbench/detail/shot-generation";
import { sourceCharCount, PROJECT_SOURCE_MAX_CHARS } from "../src/lib/project-source";

function fixture(slot: number | number[], selection: "enum" | "range" = "enum") {
    const values = Array.isArray(slot) ? slot : [slot];
    const duration: VideoCapabilityConfig["duration"] = { selection, values, min: Math.min(...values), max: Math.max(...values), default: values[0] };
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
        expect(projectVideoDurationSlots({ selection: "enum", values: [4, 30], default: 4 })).toBeUndefined();
        expect(projectVideoSubmissionSeconds(undefined, 20)).toBe(20);
    });
});

describe("15/30 双档视频模型", () => {
    for (const values of [
        [15, 30],
        [30, 15, 15],
    ]) {
        test(`${values.join("/")}：15～30 秒含端点统一报价和提交 30 秒，内容不变`, () => {
            const { duration, profile, config } = fixture(values);
            for (const content of [0.5, 10, 14.999, 15, 15.001, 20, 25, 29.999, 30]) {
                const seconds = content < 15 ? 15 : 30;
                const requirements = { capability: "video" as const, videoContentSeconds: content, videoSeconds: String(content) };
                expect(modelCompatibilityError(config, config.model, requirements)).toBe("");
                expect(modelVideoSubmissionSeconds(config, config.model, content)).toBe(seconds);
                expect(modelQuoteRequest(config, config.model, "video", requirements)?.intent.options.videoSeconds).toBe(seconds);
                expect(requestCreditCost({ channelMode: "remote", modelCosts: config.channels[0].modelCosts, model: "model", config, capability: "video", seconds: content, requirements })).toBe(seconds);
                expect(segmentDurationRule(15, duration).submitSeconds(content)).toBe(seconds);
                expect(shotVideoSeconds({ shotSeconds: content, profile, segmentMaxSeconds: 15 })).toBe(content);
                expect(shotVideoSeconds({ specSeconds: content, shotSeconds: 1, profile })).toBe(content);
                expect(requirements.videoSeconds).toBe(String(content));
            }
            for (const content of [0, -1, 30.001, 45, Number.NaN, Number.POSITIVE_INFINITY]) {
                const requirements = { capability: "video" as const, videoContentSeconds: content };
                expect(modelCompatibilityError(config, config.model, requirements)).not.toBe("");
                expect(modelVideoSubmissionSeconds(config, config.model, content)).toBeUndefined();
                expect(modelQuoteRequest(config, config.model, "video", requirements)).toBeUndefined();
                expect(requestCreditCost({ channelMode: "remote", modelCosts: config.channels[0].modelCosts, model: "model", config, capability: "video", requirements })).toBeNull();
                expect(segmentDurationRule(15, duration).submitSeconds(content)).toBeUndefined();
            }
            expect(modelCompatibilityError(config, config.model, { capability: "video", videoSeconds: "20" })).not.toBe("");
        });
    }
});

test("15 万字计数按 Unicode 字符计算，不把表情计作两个字符", () => {
    expect(sourceCharCount("雨 😀\n夜")).toBe(3);
    expect(sourceCharCount("雨".repeat(150000))).toBe(PROJECT_SOURCE_MAX_CHARS);
});
