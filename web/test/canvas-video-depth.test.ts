import { expect, test } from "bun:test";
import { listVideoProcessingModels, videoProcessingSourceReference } from "@/lib/canvas/canvas-video-processing";
import { videoDepthQuoteRequest, videoDepthSourceError, videoDepthTaskInput } from "@/lib/canvas/canvas-video-depth";
import { defaultModelCapabilityConfig, VOLCENGINE_MEDIAKIT_ENHANCE_VIDEO_PROTOCOL, VOLCENGINE_MEDIAKIT_VIDEO_DEPTH_PROTOCOL } from "@/lib/model-capabilities";
import { createModelChannel, defaultConfig, encodeChannelModel, type AiConfig } from "@/stores/use-config-store";
import { prepareBackendGenerationTask } from "@/services/api/generation-task";
import { defaultPriceTier, priceTierPayloadFromForm, priceTierToForm } from "@/pages/admin/components/channel-model-price-tier-form";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

function channel(id: string, protocol: string, scope: "system" | "user" = "system") {
    return createModelChannel({ id, scope, models: [id], modelCosts: [{ model: id, protocol, capability: "video", billingMode: "per_second", unitPriceMicrocredits: 10, capabilityConfig: defaultModelCapabilityConfig(protocol, id) }] });
}

const config: AiConfig = {
    ...defaultConfig,
    channels: [channel("depth", VOLCENGINE_MEDIAKIT_VIDEO_DEPTH_PROTOCOL), channel("enhance", VOLCENGINE_MEDIAKIT_ENHANCE_VIDEO_PROTOCOL), channel("ordinary", "openai-video"), channel("custom", VOLCENGINE_MEDIAKIT_VIDEO_DEPTH_PROTOCOL, "user")],
};
const model = encodeChannelModel("depth", "depth");
const source: CanvasNodeData = {
    id: "source",
    type: CanvasNodeType.Video,
    title: "舞蹈参考",
    position: { x: 0, y: 0 },
    width: 720,
    height: 405,
    metadata: { storageKey: "resource:source", content: "/source.mp4", mimeType: "video/mp4", naturalWidth: 2560, naturalHeight: 1440, durationMs: 300_500 },
};

test("processing model lists isolate depth, enhance, generation and custom channels", () => {
    expect(listVideoProcessingModels(config, "depth")).toEqual([model]);
    expect(listVideoProcessingModels(config, "enhance")).toEqual([encodeChannelModel("enhance", "enhance")]);
    expect(listVideoProcessingModels({ ...config, channels: config.channels.map((item) => ({ ...item, enabled: false })) }, "depth")).toEqual([]);
    expect(config.channels[0]!.modelCosts![0]!.capabilityConfig?.video?.processing?.maxSourceSeconds).toBe(600);
});

test("depth accepts longer 2K sources without enhancement settings and rejects excessive duration", () => {
    expect(videoDepthSourceError(source, 600)).toBe("");
    expect(videoDepthSourceError({ ...source, metadata: { ...source.metadata, durationMs: 600_001 } }, 600)).toContain("600 秒");
    expect(videoDepthSourceError({ ...source, metadata: {} }, 600)).toContain("为空");
});

test("depth uses the real task builder with one source, independent operation and no output specification", async () => {
    const input = videoDepthTaskInput({
        config: { ...config, vquality: "4k", size: "1:1", taskWorkflowProvider: "runninghub" },
        params: { model },
        source: videoProcessingSourceReference(source),
        sourceNodeId: source.id,
        projectId: "canvas",
        nodeId: "result",
    });
    const task = await prepareBackendGenerationTask(input);
    expect(task.operation).toBe("depth");
    expect(task.type).toBe("canvas_video");
    expect(task.input.config).toMatchObject({ channelId: "depth", model: "depth", videoSeconds: "301" });
    for (const name of ["vquality", "size", "videoGenerateAudio", "videoWatermark"]) expect(task.input.config?.[name]).toBeUndefined();
    expect(task.input.referenceVideos).toMatchObject([{ storageKey: "resource:source", durationMs: 300_500 }]);
    expect(task.input.metadata).toMatchObject({ sourceNodeId: "source", videoEditOperation: "depth" });
});

test("quote uses source duration and the depth operation without enhancement billing dimensions", () => {
    expect(videoDepthQuoteRequest(config, { model }, 300_500)).toMatchObject({ channelId: "depth", modelKey: "depth", intent: { operation: "depth", inputs: { video: 1 }, options: { videoSeconds: 301 } } });
    expect(videoDepthQuoteRequest(config, { model }, undefined)).toBeUndefined();
    expect(videoDepthQuoteRequest(config, { model: encodeChannelModel("custom", "custom") }, 1000)).toBeUndefined();
});

test("柚子渠道后台按秒保存、回显独立的相对深度价格，切换协议不携带增强规格", () => {
    const capability = defaultModelCapabilityConfig(VOLCENGINE_MEDIAKIT_VIDEO_DEPTH_PROTOCOL);
    const form = { ...defaultPriceTier("advanced"), operation: "depth", billingMode: "per_second" as const, fpsBand: "le30", resolution: "1080p", unitPrice: 0.5 };
    const tier = priceTierPayloadFromForm("video", form, "model", capability);
    expect(tier.selector).toEqual({ operation: "depth" });
    expect(tier).toMatchObject({ resolution: "*", billingMode: "per_second", unitPriceMicrocredits: 500_000 });
    const saved = { ...tier, id: "tier", channelModelId: "depth", selectorKey: "operation=depth", priceVersion: 1, createdAt: "", updatedAt: "" };
    expect(priceTierToForm(saved)).toMatchObject({ operation: "depth", billingMode: "per_second", resolution: "*", fpsBand: "*", unitPrice: 0.5 });
    expect(() => priceTierPayloadFromForm("video", { ...form, operation: "enhance" }, "model", capability)).toThrow("相对深度");
    const enhance = defaultModelCapabilityConfig(VOLCENGINE_MEDIAKIT_ENHANCE_VIDEO_PROTOCOL);
    expect(priceTierPayloadFromForm("video", { ...form, operation: "enhance" }, "model", enhance).selector).toMatchObject({ operation: "enhance", fpsBand: "le30", vquality: "1080p" });
});
