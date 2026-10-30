package kernel

// 业务信封 code：成功为 0；失败默认等于 HTTP 状态。需要区分同一状态的不同原因时，用 status*100+n。
// 不使用与 HTTP 无关的 1001/2001 号段，避免和现有 42901 以及前端按 status/code 判断重试打架。
const (
	CodeOK = 0

	CodeInvalidArgument = 400
	CodeUnauthorized    = 401
	CodeForbidden       = 403
	CodeNotFound        = 404
	CodeConflict        = 409
	CodeTooManyRequests = 429
	CodeInternal        = 500
	CodeBadGateway      = 502
	CodeUnavailable     = 503
	CodeTimeout         = 504

	CodeQuotaExceeded          = 40301
	CodeIdempotencyConflict    = 40901
	CodeCanvasResourcesMissing = 40902
	CodeRateLimited            = 42901
)

// ErrorReason 是稳定机器可读原因，前端应判断 reason 而不是解析 msg。
type ErrorReason string

const (
	ReasonInvalidArgument        ErrorReason = "invalid_argument"
	ReasonUnauthorized           ErrorReason = "unauthorized"
	ReasonForbidden              ErrorReason = "forbidden"
	ReasonNotFound               ErrorReason = "not_found"
	ReasonConflict               ErrorReason = "conflict"
	ReasonProjectNameConflict    ErrorReason = "project_name_conflict"
	ReasonSegmentTooLong         ErrorReason = "segment_too_long"
	ReasonEvidenceMissing        ErrorReason = "evidence_missing"
	ReasonCanvasResourcesMissing ErrorReason = "canvas_history_resources_missing"
	ReasonFailedPrecondition     ErrorReason = "failed_precondition"
	ReasonQuotaExceeded          ErrorReason = "quota_exceeded"
	ReasonRateLimited            ErrorReason = "rate_limited"
	ReasonUnavailable            ErrorReason = "unavailable"
	ReasonTimeout                ErrorReason = "timeout"
	ReasonInternal               ErrorReason = "internal"
	ReasonBadGateway             ErrorReason = "bad_gateway"
	ReasonUpstreamDNSFailed      ErrorReason = "upstream_dns_failed"
	// ReasonTextOutputTruncated 是文本模型因输出上限停笔（finish_reason=length / stop_reason=max_tokens），结果不完整。
	ReasonTextOutputTruncated ErrorReason = "text_output_truncated"
	// ReasonShotlistCheckFailed 是镜头表门禁有 error 级问题，没有保存；details.check 是检查结果（规则项与镜头）。
	ReasonShotlistCheckFailed ErrorReason = "shotlist_check_failed"
	// ReasonShotlistTooLarge 是一章镜头数（或分镜行合计体积）超过整章上限，镜头表没有写入；要把章节拆成两章，重新写入同样会被拒。
	ReasonShotlistTooLarge ErrorReason = "shotlist_too_large"
	// ReasonPackageCheckFailed 是上传的流程技能包没过静态校验，没有保存；details.problems 逐条列出原因。
	ReasonPackageCheckFailed ErrorReason = "package_check_failed"
	// ReasonPackageEvalNotPassed 是流程技能包版本的评测没通过（或还没跑完），不能直接启用；要强制启用就带上原因。
	ReasonPackageEvalNotPassed ErrorReason = "package_eval_not_passed"
	// ReasonDramaEpisodesReplace 是剧本优化会替换已有剧集（或改写有镜头表的剧集），请求没带 replace=true；details.episodes 列出影响面。
	ReasonDramaEpisodesReplace ErrorReason = "drama_episodes_replace_required"
	// ReasonPrevizRequired 是预演闸门生效时给镜头建视频任务，这一段还没有选中的黑白预演（action_board）。
	ReasonPrevizRequired ErrorReason = "previz_required"
	// ReasonPaletteRequired 是这一集（章）还没有场次色卡就提取资产：资产按色卡约束，要先在章节页「生成场次色卡」。
	ReasonPaletteRequired ErrorReason = "palette_required"
	// ReasonProjectSourceInvalid 是建项的「要拍什么」不对：来源类型不认识、正文或切好的章 / 集为空、创意的集数时长超范围。
	ReasonProjectSourceInvalid ErrorReason = "project_source_invalid"
	// ReasonProjectVisualTypeRequired 是建项没选画面类型（或取值不认识）。
	ReasonProjectVisualTypeRequired ErrorReason = "project_visual_type_required"
	// ReasonProjectStyleInvalid 是建项的「画风从哪来」不对：风格库条目不存在或不属于所选画面类型、参考图不是本人已就绪的图片、描述超长。
	ReasonProjectStyleInvalid ErrorReason = "project_style_invalid"
	// ReasonProjectProductionInvalid 是建项的「怎么做」不对：画幅、每段时长、视频模型或生成模式的取值不认识或互相冲突。
	ReasonProjectProductionInvalid ErrorReason = "project_production_invalid"
	// ReasonPipelineVideoManual 是想让流水线接着做出视频或剪辑：出视频要花钱，只能在制作台带着预估花费由人点。
	ReasonPipelineVideoManual ErrorReason = "pipeline_video_manual"
)

func ReasonForStatus(status int) ErrorReason {
	switch status {
	case CodeInvalidArgument:
		return ReasonInvalidArgument
	case CodeUnauthorized:
		return ReasonUnauthorized
	case CodeForbidden:
		return ReasonForbidden
	case CodeNotFound:
		return ReasonNotFound
	case CodeConflict:
		return ReasonConflict
	case CodeTooManyRequests:
		return ReasonRateLimited
	case CodeBadGateway:
		return ReasonBadGateway
	case CodeUnavailable:
		return ReasonUnavailable
	case CodeTimeout:
		return ReasonTimeout
	default:
		if status >= 500 {
			return ReasonInternal
		}
		return ReasonInvalidArgument
	}
}
