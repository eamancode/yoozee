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
    /** 一章镜头数（或分镜行合计体积）超过整章上限，镜头表没有写入；生成分段任务的 `errorCode` 也是它，此时「重新写入」必然再失败，要拆章。 */
    ShotlistTooLarge: "shotlist_too_large",
    /** 上传的流程技能包没过静态校验，没有保存；`ApiError.details.problems` 逐条列出原因。 */
    PackageCheckFailed: "package_check_failed",
    /** 流程技能包版本的评测没通过或还没跑完，不能直接启用；强制启用要带 `force: true` 和原因。 */
    PackageEvalNotPassed: "package_eval_not_passed",
    /** 剧本优化会替换已有剧集（或改写有镜头表的剧集），请求没带 `replace: true`；`ApiError.details.episodes` 列出影响面。 */
    DramaEpisodesReplace: "drama_episodes_replace_required",
    /** 预演闸门生效时给镜头建视频任务，这一段还没有选中的黑白预演（action_board）。 */
    PrevizRequired: "previz_required",
    /** 这一集（章）还没有场次色卡就提取资产：资产按色卡约束，要先在章节页「生成场次色卡」。 */
    PaletteRequired: "palette_required",
    /** 建项「要拍什么」不对：来源类型不认识、正文为空、创意的集数时长超范围。 */
    ProjectSourceInvalid: "project_source_invalid",
    /** 建项没选画面类型。 */
    ProjectVisualTypeRequired: "project_visual_type_required",
    /** 建项「画风从哪来」不对：风格不存在或不属于所选画面类型、参考图不是本人已就绪的图片、描述超长。 */
    ProjectStyleInvalid: "project_style_invalid",
    /** 建项「怎么做」不对：画幅、每段时长、视频模型或生成模式的取值不认识或冲突（如 30 秒配一段最长 15 秒的模型）。 */
    ProjectProductionInvalid: "project_production_invalid",
    /** 流水线不接出视频、剪辑：出视频要花钱，只能在制作台带着预估花费由人点。 */
    PipelineVideoManual: "pipeline_video_manual",
} as const;

export type ApiErrorReason = (typeof API_ERROR_REASON)[keyof typeof API_ERROR_REASON];
