package app

import "yingce/backend/internal/kernel"

// 错误码和原因已迁到 kernel；此处保留兼容别名。
const (
	CodeOK                  = kernel.CodeOK
	CodeInvalidArgument     = kernel.CodeInvalidArgument
	CodeUnauthorized        = kernel.CodeUnauthorized
	CodeForbidden           = kernel.CodeForbidden
	CodeNotFound            = kernel.CodeNotFound
	CodeConflict            = kernel.CodeConflict
	CodeTooManyRequests     = kernel.CodeTooManyRequests
	CodeInternal            = kernel.CodeInternal
	CodeBadGateway          = kernel.CodeBadGateway
	CodeUnavailable         = kernel.CodeUnavailable
	CodeTimeout             = kernel.CodeTimeout
	CodeQuotaExceeded       = kernel.CodeQuotaExceeded
	CodeIdempotencyConflict = kernel.CodeIdempotencyConflict
	CodeRateLimited         = kernel.CodeRateLimited
)

type ErrorReason = kernel.ErrorReason

const (
	ReasonInvalidArgument     = kernel.ReasonInvalidArgument
	ReasonUnauthorized        = kernel.ReasonUnauthorized
	ReasonForbidden           = kernel.ReasonForbidden
	ReasonNotFound            = kernel.ReasonNotFound
	ReasonConflict            = kernel.ReasonConflict
	ReasonProjectNameConflict = kernel.ReasonProjectNameConflict
	ReasonSegmentTooLong      = kernel.ReasonSegmentTooLong
	ReasonEvidenceMissing     = kernel.ReasonEvidenceMissing
	ReasonFailedPrecondition  = kernel.ReasonFailedPrecondition
	ReasonQuotaExceeded       = kernel.ReasonQuotaExceeded
	ReasonRateLimited         = kernel.ReasonRateLimited
	ReasonUnavailable         = kernel.ReasonUnavailable
	ReasonTimeout             = kernel.ReasonTimeout
	ReasonInternal            = kernel.ReasonInternal
	ReasonBadGateway          = kernel.ReasonBadGateway
	ReasonUpstreamDNSFailed   = kernel.ReasonUpstreamDNSFailed
	ReasonTextOutputTruncated = kernel.ReasonTextOutputTruncated
	ReasonShotlistCheckFailed = kernel.ReasonShotlistCheckFailed
	ReasonShotlistTooLarge    = kernel.ReasonShotlistTooLarge
	ReasonPackageCheckFailed  = kernel.ReasonPackageCheckFailed
	// ReasonPackageEvalNotPassed 见 kernel。
	ReasonPackageEvalNotPassed = kernel.ReasonPackageEvalNotPassed
	// ReasonDramaEpisodesReplace 见 kernel。
	ReasonDramaEpisodesReplace = kernel.ReasonDramaEpisodesReplace
	// ReasonPrevizRequired 见 kernel。
	ReasonPrevizRequired = kernel.ReasonPrevizRequired
	// ReasonPaletteRequired 见 kernel。
	ReasonPaletteRequired = kernel.ReasonPaletteRequired
	// 建项改版：建项各段的错误原因、流水线不接出视频。见 kernel。
	ReasonProjectSourceInvalid      = kernel.ReasonProjectSourceInvalid
	ReasonProjectVisualTypeRequired = kernel.ReasonProjectVisualTypeRequired
	ReasonProjectStyleInvalid       = kernel.ReasonProjectStyleInvalid
	ReasonProjectProductionInvalid  = kernel.ReasonProjectProductionInvalid
	ReasonPipelineVideoManual       = kernel.ReasonPipelineVideoManual
)

func ReasonForStatus(status int) ErrorReason {
	return kernel.ReasonForStatus(status)
}
