/**
 * 后端标准错误原因（`ApiError.reason`、任务的 `errorCode`），与 backend/internal/kernel/error_codes.go 一一对应。
 * 业务分支只比较这些值，不解析 msg；含义见 docs/content/docs/backend/http-api.mdx 的错误码表。
 */
export const API_ERROR_REASON = {
    InvalidArgument: "invalid_argument",
    Unauthorized: "unauthorized",
    Forbidden: "forbidden",
    NotFound: "not_found",
    Conflict: "conflict",
    ProjectNameConflict: "project_name_conflict",
    SegmentTooLong: "segment_too_long",
    EvidenceMissing: "evidence_missing",
    CanvasResourcesMissing: "canvas_history_resources_missing",
    FailedPrecondition: "failed_precondition",
    QuotaExceeded: "quota_exceeded",
    RateLimited: "rate_limited",
    Unavailable: "unavailable",
    Timeout: "timeout",
    Internal: "internal",
    BadGateway: "bad_gateway",
    UpstreamDNSFailed: "upstream_dns_failed",
    /** 文本模型因输出上限停笔（finish_reason=length / stop_reason=max_tokens），任务失败、结果不完整。 */
    TextOutputTruncated: "text_output_truncated",
    /** 镜头表门禁有 error，没有保存；`ApiError.details.check` 是检查结果（规则项与镜头）。 */
    ShotlistCheckFailed: "shotlist_check_failed",
    /** 上传的流程技能包没过静态校验，没有保存；`ApiError.details.problems` 逐条列出原因。 */
    PackageCheckFailed: "package_check_failed",
    /** 流程技能包版本的评测没通过或还没跑完，不能直接启用；强制启用要带 `force: true` 和原因。 */
    PackageEvalNotPassed: "package_eval_not_passed",
    /** 剧本优化会替换已有剧集（或改写有镜头表的剧集），请求没带 `replace: true`；`ApiError.details.episodes` 列出影响面。 */
    DramaEpisodesReplace: "drama_episodes_replace_required",
} as const;

export type ApiErrorReason = (typeof API_ERROR_REASON)[keyof typeof API_ERROR_REASON];
