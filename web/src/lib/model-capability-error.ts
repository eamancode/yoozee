import { ApiError } from "@/services/api/request";
import { API_ERROR_REASON } from "@/services/api/error-codes";

/**
 * 模型能力错误的具体说明：按 reason 和 details 取「哪个参数、为什么不行」，不解析 msg。
 * 不是能力错误时返回 undefined（调用方用自己的通用文案）；是能力错误但后端没给 details 时退回原文案。
 */
export function modelCapabilityProblem(error: unknown): string | undefined {
    if (!(error instanceof ApiError) || error.reason !== API_ERROR_REASON.ModelCapabilityNotSupported) return undefined;
    const details = error.details || {};
    const issues = Array.isArray(details.capabilityIssues) ? details.capabilityIssues : [];
    const reasons = [typeof details.message === "string" ? details.message : "", ...issues.map((issue) => (issue && typeof issue === "object" && typeof (issue as { reason?: unknown }).reason === "string" ? (issue as { reason: string }).reason : ""))];
    const unique = [...new Set(reasons.map((reason) => reason.trim()).filter(Boolean))];
    if (!unique.length) return error.message;
    return `${error.message}：${unique.join("；")}。可以换一个支持这些参数的视频模型，或在设置里调整对应参数后再预览。`;
}
