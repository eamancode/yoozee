import { describe, expect, test } from "bun:test";
import { CONTENT_MODERATION_MESSAGE, generationErrorMessage, generationFailureMetadata } from "../src/lib/generation-error";

describe("safe upstream error details", () => {
    const detail = "非常抱歉，生成的图片可能违反了关于裸露、色情或情色内容的防护限制。请重试或修改提示语。";
    test("keeps the specific image rejection on the card", () => {
        const message = `模型服务拒绝了请求，请检查模型和参数；上游：${detail}`;
        expect(generationErrorMessage(new Error(message))).toBe(message);
    });
    test("does not replace a safe upstream message with a generic HTTP hint", () => {
        const message = `模型服务暂时不可用（HTTP 500）；上游：${detail}`;
        expect(generationErrorMessage(message)).toBe(message);
        expect(generationErrorMessage("模型服务暂时不可用（HTTP 500）")).toBe("网络异常。");
    });
    test("keeps details when moderation metadata is stored", () => {
        const message = `内容审核未通过；上游：${detail}`;
        const metadata = generationFailureMetadata(message, "test prompt");
        expect(metadata.errorDetails).toBe(`${CONTENT_MODERATION_MESSAGE}；上游：${detail}`);
        expect(metadata.generationErrorCode).toBe("sensitive_words_detected");
    });
    test("output truncation asks for a longer-output model or per-scene generation", () => {
        const truncated = Object.assign(new Error("模型输出达到上限被截断（length），结果不完整"), { reason: "text_output_truncated" });
        expect(generationErrorMessage(truncated)).toBe("输出超过模型上限被截断，结果不完整：请换输出上限更大的模型，或按场分开生成。");
    });
    test("upstream marker does not bypass URL hiding", () => {
        expect(generationErrorMessage("模型服务请求失败；上游：https://private.test")).toBe("网络异常。");
    });
});
