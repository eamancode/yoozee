import { describe, expect, test } from "bun:test";

import { assetImageTaskTemplate } from "../src/pages/projects/workbench/detail/drama-image-task";
import { dramaImageSummaryText } from "../src/pages/projects/workbench/detail/drama-image-summary";
import { createModelChannel, defaultConfig, encodeChannelModel } from "../src/stores/use-config-store";

// 系统渠道的同名模型带声明式参数表（只有 size），个人渠道的同名模型按协议默认能力（有 quality 等）。
function sameNameConfig() {
    const system = createModelChannel({
        id: "system-image",
        name: "系统",
        scope: "system",
        baseUrl: "/api/ai/system/system-image",
        apiKey: "",
        interfaceType: "openai-image",
        models: ["fake-image"],
        modelCosts: [
            {
                model: "fake-image",
                capability: "image",
                protocol: "openai-image",
                billingMode: "fixed_request",
                unitPriceMicrocredits: 1,
                capabilityConfig: { version: 1, image: { parameters: [{ name: "size", type: "enum", values: ["1024x1024"], default: "1024x1024" }] } } as never,
            },
        ],
    });
    const personal = createModelChannel({
        id: "personal",
        name: "个人",
        baseUrl: "https://images.example.com/v1",
        apiKey: "sk-personal",
        models: ["fake-image", "pr30-image"],
        modelCosts: [
            { model: "fake-image", capability: "image", protocol: "openai-image", billingMode: "fixed_request", unitPriceMicrocredits: 0 },
            { model: "pr30-image", capability: "image", protocol: "openai-image", billingMode: "fixed_request", unitPriceMicrocredits: 0 },
        ],
    });
    return { ...defaultConfig, channels: [system, personal], size: "1:1" };
}

describe("短剧出图模板", () => {
    test("个人模型与系统模型同名：能力配置按选中的个人渠道取，不落到系统模型", async () => {
        const config = sameNameConfig();
        const model = encodeChannelModel("personal", "fake-image");
        const template = await assetImageTaskTemplate({ ...config, model }, model);
        const providerConfig = template.input.config as Record<string, unknown>;
        expect(providerConfig.channelId).toBe("");
        expect(providerConfig.baseUrl).toBe("https://images.example.com/v1");
        expect(providerConfig.interfaceType).toBe("openai-image");
        const image = (providerConfig.capabilityConfig as { image?: { parameters?: unknown } }).image;
        expect(image?.parameters).toBeUndefined();
    });

    test("个人渠道不同名的模型：按模型的协议组模板，不报没选协议", async () => {
        const config = sameNameConfig();
        const model = encodeChannelModel("personal", "pr30-image");
        const template = await assetImageTaskTemplate({ ...config, model }, model);
        expect((template.input.config as Record<string, unknown>).interfaceType).toBe("openai-image");
    });
});

describe("侧栏出图摘要", () => {
    test("全部出完写「N 张已出图」", () => {
        expect(dramaImageSummaryText(["bound", "bound", "bound"], "张", "色卡上")).toBe("3 张已出图。");
    });

    test("按当前状态分开写：出图中、等空位、没出成、还没出图", () => {
        expect(dramaImageSummaryText(["bound", "queued", "waiting", "failed", "stale", "none"], "个", "资产上")).toBe(
            "1 个已出图，1 个出图中，1 个等前面的出完接着排，2 个没出成（原因写在资产上，到资产页点「继续出图」重出），1 个还没出图（到资产页点「继续出图」可以补）。",
        );
    });
});

test("个人渠道模型设置：关掉「配置使用」时把显示的推断协议存下来", async () => {
    const source = await Bun.file(new URL("../src/pages/settings/channel-video-pricing.tsx", import.meta.url)).text();
    expect(source).toContain("if (activeModel && !activeModelCost && activeProtocol && !protocolLoading) updateCost(activeModel, {});");
    expect(source).toContain("onClose={closeEditor}");
    expect(source).toContain("onClick={closeEditor}>完成</Button>");
});
