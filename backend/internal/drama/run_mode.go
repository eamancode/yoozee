package drama

// 生成模式（项目字段 run_mode，契约 brief 的 run_mode）：决定一步做完之后服务端要不要自动接着做下一步。
// 出视频要花钱，任何模式都停在它前面，等人带着预估花费点；这一条写死在 ShouldAutoContinue 里。
const (
	// RunUntilVideo 是跑到出视频前（默认）：从拆剧本一路自动接到装段完成。
	RunUntilVideo = "until_video"
	// RunReviewFromAssets 是资产我要过目：自动跑到场次色卡，从资产提取起每一步都停。
	RunReviewFromAssets = "review_from_assets"
	// RunEveryStep 是每步都问我：一步做完都停，不自动接。
	RunEveryStep = "every_step"
)

// RunModes 是三档生成模式（按从最自动到最不自动排）。
var RunModes = []string{RunUntilVideo, RunReviewFromAssets, RunEveryStep}

// ValidRunMode 是否是生成模式的取值。
func ValidRunMode(mode string) bool {
	for _, m := range RunModes {
		if m == mode {
			return true
		}
	}
	return false
}

// RunModeLabel 是生成模式的中文名；不认识返回空串。
func RunModeLabel(mode string) string {
	switch mode {
	case RunUntilVideo:
		return "跑到出视频前"
	case RunReviewFromAssets:
		return "资产我要过目"
	case RunEveryStep:
		return "每步都问我"
	}
	return ""
}

// 流水线的环节（建项后的固定顺序，概览首屏按它画）。分镜和装段是两格：装段在生成分镜的收尾里由代码完成，
// 检查有问题时只存草稿、停在装段。角色场景资产包括提取和给新资产出图。
const (
	StageScript  = "script"
	StageStyle   = "style"
	StagePalette = "palette"
	StageAssets  = "assets"
	StageShots   = "shots"
	StagePack    = "pack"
	StageVideo   = "video"
	StageCut     = "cut"
)

// PipelineStageDef 是流水线的一格。
type PipelineStageDef struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// PipelineStages 是流水线的环节，按顺序。
var PipelineStages = []PipelineStageDef{
	{StageScript, "拆剧本"},
	{StageStyle, "导演定画风"},
	{StagePalette, "场次色卡"},
	{StageAssets, "角色场景资产"},
	{StageShots, "分镜"},
	{StagePack, "装段"},
	{StageVideo, "出视频"},
	{StageCut, "剪辑"},
}

// PipelineStageLabel 是环节的中文名；不认识返回空串。
func PipelineStageLabel(stage string) string {
	for _, s := range PipelineStages {
		if s.Key == stage {
			return s.Label
		}
	}
	return ""
}

// ShouldAutoContinue 是上一步做完之后，服务端要不要自动开始 next 这一环（所有链式触发点都问它）。
// 出视频和剪辑在任何模式下都不自动开始：视频要花钱，必须由人带着预估花费点。
func ShouldAutoContinue(runMode string, next string) bool {
	switch next {
	case StageVideo, StageCut:
		return false
	}
	switch runMode {
	case RunUntilVideo:
		return next == StageScript || next == StageStyle || next == StagePalette || next == StageAssets || next == StageShots || next == StagePack
	case RunReviewFromAssets:
		return next == StageScript || next == StageStyle || next == StagePalette
	}
	return false
}
