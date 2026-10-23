package service

import "yingce/backend/internal/app"

type (
	DramaEvalInput   = app.DramaEvalInput
	DramaEvalRow     = app.DramaEvalRow
	DramaEvalSegment = app.DramaEvalSegment
	DramaEvalRun     = app.DramaEvalRun
	DramaEvalRequest = app.DramaEvalRequest

	DramaShotlistRepackRequest = app.DramaShotlistRepackRequest
)

// EvalDramaCompile 在评测集上用基线与候选编译档案各跑一遍装段 + 编译（不碰数据库、不调模型）。
var EvalDramaCompile = app.EvalDramaCompile
