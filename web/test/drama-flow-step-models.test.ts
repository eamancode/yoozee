import { afterEach, expect, test } from "bun:test";

import { defaultModelCapabilityConfig } from "../src/lib/model-capabilities";
import { systemChannelModelChannels } from "../src/lib/user-session";
import type { ModelName } from "../src/pages/drama-flow/hooks/use-model-names";
import { flowImageModel, flowTextModel, flowVideoModel, stepModelStatus, videoFamilyMismatch } from "../src/pages/drama-flow/model/step-models";
import type { PublicChannelModel } from "../src/services/api/logical-models";
import type { DramaModel, DramaRegistry } from "../src/services/api/project-drama";
import { defaultConfig, normalizeConfigSnapshot } from "../src/stores/use-config-store";
import { useDramaModelPreferencesStore } from "../src/stores/use-drama-model-preferences-store";

function fixture() {
    const model = (modelKey: string, capability: "text" | "image" | "video"): PublicChannelModel => {
        const protocol = capability === "text" ? "chat-completion" : capability === "image" ? "openai-image" : "seedance";
        return {
            id: modelKey,
            modelKey,
            displayName: modelKey,
            channelLabel: "",
            description: "",
            icon: "",
            capability,
            protocol,
            available: true,
            pricingMode: "provider",
            priceLabel: "",
            capabilityConfig: defaultModelCapabilityConfig(protocol, modelKey),
            priceTiers: [],
        };
    };
    return normalizeConfigSnapshot({
        config: {
            ...defaultConfig,
            channels: systemChannelModelChannels([{ id: "a", name: "test", displayName: "test", models: [model("image-a", "image"), model("image-b", "image"), model("seedance-2.5", "video"), model("kling-3", "video")] }]),
            imageModel: "a::image-a",
            videoModel: "a::seedance-2.5",
        },
    }).config;
}

const family = (id: string, match: string): DramaModel => ({
    id,
    label: id,
    vendor: "",
    kind: "video",
    match: [match],
    duration: { min: 4, max: 15, step: 1 },
    refs: { images: 9, videos: null, audios: null, total: null },
    frames_exclusive_with_refs: null,
    prompt_max_chars: null,
    resolutions: [],
    calibrated: true,
    checked: "",
});
const seedance = family("seedance", "seedance");
const kling = family("kling", "kling");
const registry: DramaRegistry = { version: "1", default: "seedance", models: [seedance, kling] };

afterEach(() => useDramaModelPreferencesStore.setState({ models: {} }));

test("图片模型：上次用的优先，项目默认只作回退", () => {
    const config = fixture();
    expect(flowImageModel(config, { image: "a::image-b" }, "a::image-a")).toBe("a::image-b");
    expect(flowImageModel(config, {}, "a::image-b")).toBe("a::image-b");
    expect(flowImageModel(config, { image: "gone::model" }, "a::image-b")).toBe("a::image-b");
    expect(flowImageModel(config, {}, undefined)).toBe("a::image-a");
});

test("视频模型：上次用的可以是别的族；用不了时回到项目默认", () => {
    const config = fixture();
    useDramaModelPreferencesStore.setState({ models: { video: "a::kling-3" } });
    expect(flowVideoModel(config, { video: "a::kling-3" }, "a::seedance-2.5", registry, seedance)).toBe("a::kling-3");
    useDramaModelPreferencesStore.setState({ models: { video: "gone::video" } });
    expect(flowVideoModel(config, { video: "gone::video" }, "a::seedance-2.5", registry, seedance)).toBe("a::seedance-2.5");
});

test("跨族提醒：同族不提醒，别的族给出所属族，认不出给 null", () => {
    const config = fixture();
    expect(videoFamilyMismatch(config, "a::seedance-2.5", registry, seedance)).toBeUndefined();
    expect(videoFamilyMismatch(config, "a::kling-3", registry, seedance)?.id).toBe("kling");
    expect(videoFamilyMismatch(config, "a::kling-3", { ...registry, models: [seedance] }, seedance)).toBeNull();
    expect(videoFamilyMismatch(config, "", registry, seedance)).toBeUndefined();
    expect(videoFamilyMismatch(config, "a::kling-3", undefined, seedance)).toBeUndefined();
});

test("不能用的模型（个人渠道、平台不可用、画幅不支持）不会成为可提交的默认值", () => {
    const config = fixture();
    const noImageB = (model: string) => model !== "a::image-b";
    expect(flowImageModel(config, { image: "a::image-b" }, undefined, noImageB)).toBe("a::image-a");
    expect(flowImageModel(config, { image: "a::image-b" }, "a::image-b", () => false)).toBe("");
    // 上次用的 kling 给不出项目画幅：回到同族可用的 seedance；都不能用时为空。
    const noKling = (model: string) => model !== "a::kling-3";
    useDramaModelPreferencesStore.setState({ models: { video: "a::kling-3" } });
    expect(flowVideoModel(config, { video: "a::kling-3" }, undefined, registry, seedance, noKling)).toBe("a::seedance-2.5");
    expect(flowVideoModel(config, { video: "a::kling-3" }, "a::seedance-2.5", registry, seedance, () => false)).toBe("");
    expect(flowTextModel(config, {}, () => false)).toBe("");
});

// 按 modelNameOf 的口径造名字：known 是目录确认可用的，cached 是本地缓存认得但目录状态未知的。
function namer(read: "ready" | "loading" | "error", known: string[], cached: string[] = known) {
    return (id: string | undefined, unset: string): ModelName => {
        if (!id) return { text: unset };
        if (read === "ready") return known.includes(id) ? { text: `名:${id}` } : { text: "原来选的模型已不可用，到设置里重选", tone: "bad" };
        if (read === "loading") return { text: cached.includes(id) ? `名:${id}（正在核对是否可用…）` : "正在读模型目录…" };
        return { text: cached.includes(id) ? `名:${id}（模型目录没读到，没法确认现在是否可用）` : "模型目录没读到", tone: "warn" };
    };
}
const status = (read: "ready" | "loading" | "error", chosen: string, effective: string, extra: { known?: string[]; cached?: string[]; reason?: (model: string) => string } = {}) =>
    stepModelStatus({ chosen, effective, catalogRead: read, nameOf: namer(read, extra.known ?? [chosen, effective], extra.cached), disabledReason: extra.reason ?? (() => ""), unset: "还没配好" });

test("模型卡状态：没配、在读、没读到、下架无回退、下架有回退、这一步不能用", () => {
    expect(status("ready", "", "")).toEqual({ text: "还没配好" });
    expect(status("ready", "", "m1")).toEqual({ text: "名:m1" });
    expect(status("ready", "m1", "m1")).toEqual({ text: "名:m1" });
    // 目录在读或没读到：照实写，不说已不可用，也不把缓存名字当成确认过的。
    expect(status("loading", "gone", "", { cached: [] })).toEqual({ text: "正在读模型目录…" });
    expect(status("error", "m1", "m1")).toEqual({ text: "名:m1（模型目录没读到，没法确认现在是否可用）", tone: "warn" });
    expect(status("error", "gone", "", { cached: [] })).toEqual({ text: "模型目录没读到", tone: "warn" });
    // 目录确认下架：没有回退照旧写让人重选；有回退写明改用了谁。
    expect(status("ready", "gone", "", { known: [] })).toEqual({ text: "原来选的模型已不可用，到设置里重选", tone: "bad" });
    expect(status("ready", "gone", "m2", { known: ["m2"] })).toEqual({ text: "原来选的模型已不可用，现在改用 名:m2", tone: "warn" });
    // 平台不可用、画幅不支持：写原因；有回退写改用了谁。
    const ratio = (model: string) => (model === "k" ? "不支持 9:16 画幅（可选 16:9）" : "");
    expect(status("ready", "k", "", { reason: ratio })).toEqual({ text: "原来选的模型不支持 9:16 画幅（可选 16:9），点这里重选", tone: "bad" });
    expect(status("ready", "k", "s", { reason: ratio })).toEqual({ text: "原来选的模型不支持 9:16 画幅（可选 16:9），现在改用 名:s", tone: "warn" });
    // 下架优先于原因：同一个模型既下架又不在短剧目录里，按下架写（与信息面板既有口径一致）。
    expect(status("ready", "gone", "", { known: [], reason: () => "模型列表已更新，请刷新页面后再选" })).toEqual({ text: "原来选的模型已不可用，到设置里重选", tone: "bad" });
    // 重选恢复：原选择就是实际提交的，写名字。
    expect(status("ready", "m2", "m2")).toEqual({ text: "名:m2" });
});
