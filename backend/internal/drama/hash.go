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

type visualShot struct {
	Size   string `json:"size,omitempty"`
	Camera string `json:"camera,omitempty"`
	Text   string `json:"text"`
}

type timedShot struct {
	At     float64 `json:"at"`
	Size   string  `json:"size,omitempty"`
	Camera string  `json:"camera,omitempty"`
	Text   string  `json:"text"`
}

// InputHash 计算一个产物依赖输入的指纹。refIDs 是镜头当前绑定的素材版本 ID。
func InputHash(artifactType string, rev RevisionInputs, refIDs []string) string {
	refs := append([]string(nil), refIDs...)
	sort.Strings(refs)
	var seg *Segment
	if strings.TrimSpace(rev.SpecJSON) != "" {
		if parsed, err := ParseSegment([]byte(rev.SpecJSON)); err == nil {
			seg = &parsed
		}
	}
	parts := map[string]any{"type": artifactType, "refs": refs}
	visual := func() {
		parts["plot"] = rev.PlotDescription
		parts["action"] = rev.Action
		parts["shotSize"] = rev.ShotSize
		parts["cameraAngle"] = rev.CameraAngle
		parts["imagePrompt"] = rev.ImagePrompt
		parts["negative"] = rev.NegativePrompt
		parts["continuity"] = rev.ContinuityNotes
		if seg != nil {
			shots := make([]visualShot, 0, len(seg.Shots))
			for _, sh := range seg.Shots {
				shots = append(shots, visualShot{sh.Size, sh.Camera, sh.Text})
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
			parts["spec"] = seg.Canonical()
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
		if a.InputHash == "" || a.InputHash != InputHash(a.Type, rev, refIDs) {
			out = append(out, a.ID)
		}
	}
	return out
}

// ArtifactInput 是判断过期所需的产物字段。
type ArtifactInput struct {
	ID        string
	Type      string
	InputHash string
}
