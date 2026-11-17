import { modelCapabilityProblem } from "@/lib/model-capability-error";
import { ApiError } from "@/services/api/request";
import type { VideoChainDiagnostic } from "@/services/api/project-video-chains";

export function videoChainOperationProblem(error: unknown, operation: string) {
    const details = error instanceof ApiError ? error.details : undefined;
    const capabilityProblem = modelCapabilityProblem(error);
    const message = capabilityProblem || (error instanceof Error ? error.message : "操作未完成");
    const spec: string[] = [];
    if (details) {
        if (typeof details.shotId === "string") spec.push(`段 ${details.shotId}`);
        if (typeof details.model === "string") spec.push(`模型 ${details.model}`);
        if (typeof details.contentSeconds === "number") {
            spec.push(`正文 ${details.contentSeconds} 秒`, typeof details.generationSeconds === "number" ? `请求 ${details.generationSeconds} 秒` : "请求时长未定档");
        }
        if (Array.isArray(details.allowedSeconds) && details.allowedSeconds.length) spec.push(`可选档位 ${details.allowedSeconds.join(" / ")} 秒`);
        const range = details.allowedDurationRange;
        if (range && typeof range === "object" && "min" in range && "max" in range) spec.push(`可选时长 ${range.min}–${range.max} 秒${"step" in range && range.step ? `，步长 ${range.step} 秒` : ""}`);
        if (typeof details.referenceCount === "number") spec.push(`参考图 ${details.referenceCount} 张`);
    }
    return {
        text: `${operation}失败。 ${message}${spec.length ? `（${spec.join(" · ")}）` : ""}`,
        shotId: typeof details?.shotId === "string" ? details.shotId : undefined,
        canEditShot: Boolean(capabilityProblem) || typeof details?.contentSeconds === "number",
    };
}

const causes: Record<string, string> = {
    route_unavailable: "线路不可用",
    upstream_service: "上游服务故障",
    timeout: "执行超时",
    provider_failed: "上游生成失败",
    invalid_input: "参数不合规",
    preflight: "预检失败",
    content_rejected: "内容审核未通过",
    insufficient_balance: "余额不足",
    result_persistence: "作品保存失败",
    cancelled: "已取消",
    unknown: "失败原因待核对",
};

export function videoChainFailureLabel(diagnostic?: VideoChainDiagnostic) {
    if (!diagnostic) return "";
    const cause = diagnostic.category ? causes[diagnostic.category] : "";
    return [cause, diagnostic.upstreamCode].filter(Boolean).join(" · ");
}
