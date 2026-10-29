package drama

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// RevisionInputs 是一个镜头版本里会影响产物的字段（与 model.ShotRevision 对应，避免领域包依赖存储结构）。
type RevisionInputs struct {
	PlotDescription string
	Action          string
	Dialogue        string
	ShotSize        string
	CameraAngle     string
	CameraMovement  string
	DurationMs      int64
	ImagePrompt     string
	VideoPrompt     string
	NegativePrompt  string
	ContinuityNotes string
	ActionBeatsJSON string
	SpecJSON        string
}

// 产物类型与它依赖的输入：改了台词只让视频过期，分镜图不动；改了画面描述三种都过期。
const (
	ArtifactStoryboard  = "storyboard"
	ArtifactActionBoard = "action_board"
	ArtifactVideo       = "video"
)

// visualShot 的 End 只在镜头写了结尾状态时出现：没写的旧段指纹不变。
type visualShot struct {
	Size   string `json:"size,omitempty"`
	Camera string `json:"camera,omitempty"`
	Text   string `json:"text"`
	End    string `json:"end,omitempty"`
}

type timedShot struct {
	At     float64 `json:"at"`
	Size   string  `json:"size,omitempty"`
	Camera string  `json:"camera,omitempty"`
	Text   string  `json:"text"`
}

// InputHash 计算一个产物依赖输入的指纹。refIDs 是镜头当前绑定的素材版本 ID。
// 剧情描述按 segmentPlotForHash 归一：制作台保存曾经把段标题拼在派生剧情前面，和装段写法不同，同一段内容不能因此两个指纹。
func InputHash(artifactType string, rev RevisionInputs, refIDs []string) string {
	return inputHash(artifactType, rev, refIDs, true)
}

func inputHash(artifactType string, rev RevisionInputs, refIDs []string, normalizePlot bool) string {
	refs := append([]string(nil), refIDs...)
	sort.Strings(refs)
	var seg *Segment
	if strings.TrimSpace(rev.SpecJSON) != "" {
		if parsed, err := ParseSegment([]byte(rev.SpecJSON)); err == nil {
			seg = &parsed
		}
	}
	parts := map[string]any{"type": artifactType, "refs": refs}
	plot := rev.PlotDescription
	if normalizePlot && seg != nil {
		plot = segmentPlotForHash(plot, *seg)
	}
	visual := func() {
		parts["plot"] = plot
		parts["action"] = rev.Action
		parts["shotSize"] = rev.ShotSize
		parts["cameraAngle"] = rev.CameraAngle
		parts["imagePrompt"] = rev.ImagePrompt
		parts["negative"] = rev.NegativePrompt
		parts["continuity"] = rev.ContinuityNotes
		if seg != nil {
			shots := make([]visualShot, 0, len(seg.Shots))
			for _, sh := range seg.Shots {
				shots = append(shots, visualShot{sh.Size, sh.Camera, sh.Text, sh.EndState})
			}
			parts["spec"] = map[string]any{"scene": seg.Scene, "mode": seg.Mode, "first": seg.FirstFrame, "last": seg.LastFrame, "refs": seg.Refs, "rules": seg.Rules, "shots": shots}
		}
	}
	switch artifactType {
	case ArtifactStoryboard:
		visual()
	case ArtifactActionBoard:
		visual()
		parts["cameraMovement"] = rev.CameraMovement
		parts["beats"] = rev.ActionBeatsJSON
		parts["durationMs"] = rev.DurationMs
		if seg != nil {
			shots := make([]timedShot, 0, len(seg.Shots))
			for _, sh := range seg.Shots {
				shots = append(shots, timedShot{sh.At, sh.Size, sh.Camera, sh.Text})
			}
			parts["timing"] = map[string]any{"dur": seg.Dur, "shots": shots}
		}
	default:
		// 视频（以及未知类型）依赖全部输入。
		visual()
		parts["cameraMovement"] = rev.CameraMovement
		parts["beats"] = rev.ActionBeatsJSON
		parts["durationMs"] = rev.DurationMs
		parts["dialogue"] = rev.Dialogue
		parts["videoPrompt"] = rev.VideoPrompt
		if seg != nil {
			parts["spec"] = videoSpecCanonical(*seg)
		}
	}
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16])
}

// StaleArtifacts 返回保存新版本后应过期的产物 ID：没有指纹的旧产物一律过期（无法判断依赖）。
func StaleArtifacts(artifacts []ArtifactInput, rev RevisionInputs, refIDs []string) []string {
	var out []string
	for _, a := range artifacts {
		if a.InputHash == "" || (a.InputHash != InputHash(a.Type, rev, refIDs) && a.InputHash != titledPlotHash(a.Type, rev, refIDs)) {
			out = append(out, a.ID)
		}
	}
	return out
}

// SegmentPlot 是由分段派生的剧情描述：各镜文字用「；」连起来（装段和制作台保存同一个写法，不拼段标题）。
func SegmentPlot(seg Segment) string {
	texts := make([]string, 0, len(seg.Shots))
	for _, sh := range seg.Shots {
		texts = append(texts, sh.Text)
	}
	return strings.Join(texts, "；")
}

// segmentPlotForHash 把「段标题；各镜文字」（之前制作台保存时的派生写法）归一成各镜文字，和装段写的一样；手写的剧情原样。
func segmentPlotForHash(plot string, seg Segment) string {
	if seg.Title != "" && plot == seg.Title+"；"+SegmentPlot(seg) {
		return SegmentPlot(seg)
	}
	return plot
}

// titledPlotHash 是之前制作台保存过的段的产物当时的指纹写法：剧情是派生的（各镜文字，或「段标题；各镜文字」）时，按「段标题；各镜文字」
// 不归一算一遍。这些产物存的指纹是按那种写法算的，不认它的话部署后第一次重装 / 保存会让它们全部过期。不是派生剧情、没有段标题时为空。
func titledPlotHash(artifactType string, rev RevisionInputs, refIDs []string) string {
	if strings.TrimSpace(rev.SpecJSON) == "" {
		return ""
	}
	seg, err := ParseSegment([]byte(rev.SpecJSON))
	if err != nil || seg.Title == "" {
		return ""
	}
	titled := seg.Title + "；" + SegmentPlot(seg)
	if rev.PlotDescription != SegmentPlot(seg) && rev.PlotDescription != titled {
		return ""
	}
	rev.PlotDescription = titled
	return inputHash(artifactType, rev, refIDs, false)
}

// ArtifactInput 是判断过期所需的产物字段。
type ArtifactInput struct {
	ID        string
	Type      string
	InputHash string
}

// videoSpecCanonical 是视频指纹里的分段：整段照旧（段 refs、镜头文字、时长等进视频输入的都在），只去掉两样不进视频提示词的：
//   - 每镜的 refs：只是分镜卡显示用，实际参考图已经在段 refs 里（照旧进指纹）。
//   - 按小节装的段、首镜没写镜头目的时的段标题：标题不进编译稿，这种标题由装段按首镜动作 / 核心观看体验兜底（卡 G 之前是空的），
//     逐张改了首镜动作后制作台保存的和重新装段写的还会不一样。按空标题算：旧段指纹不变，兜底标题怎么变都不让产物过期。
//     写了镜头目的的段、不是按小节装的段，标题照旧按原文进指纹。
func videoSpecCanonical(seg Segment) string {
	shots := make([]SubShot, len(seg.Shots))
	for i, sh := range seg.Shots {
		sh.Refs = nil
		shots[i] = sh
	}
	seg.Shots = shots
	if derivedSegmentTitle(seg) {
		seg.Title = ""
	}
	return seg.Canonical()
}

// derivedSegmentTitle 是段标题是不是装段兜底写的（按小节装的段、首镜没写镜头目的；见 segmentTitle）。
func derivedSegmentTitle(seg Segment) bool {
	return seg.Section != "" && len(seg.Shots) > 0 && strings.TrimSpace(seg.Shots[0].Purpose) == ""
}
