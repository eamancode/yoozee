package drama

import "strings"

const (
	// WorkflowSSSFilm / WorkflowSSSDrama 是 SSS 电影、SSS 短剧导演流水线（v100 起排在最前）；本体论、资产一体化、原流程顺延为三、四、五。
	WorkflowSSSFilm          = "pipeline-1"
	WorkflowSSSDrama         = "pipeline-2"
	WorkflowOntology         = "pipeline-3"
	WorkflowIntegrated       = "pipeline-4"
	WorkflowOriginal         = "pipeline-5"
	WorkflowVersion          = "1"
	IntegratedAssetsSkill    = "yz-assets-integrated"
	ContractIntegratedAssets = "assets.extract@2"
	// StoryboardModeOntology 表示分镜环节由本体论导演分镜（yz-ontology-director）分轮生成。
	StoryboardModeOntology = "ontology"
	// StoryboardModeSSS 表示分镜环节由 SSS 导演分镜（yz-sss-film-director / yz-sss-drama-director）分轮生成。
	StoryboardModeSSS = "sss"
	// DefaultWorkflow 是新建项目不选流水线时的默认值。
	DefaultWorkflow = WorkflowSSSDrama
)

type AssetWorkflow struct {
	ID              string `json:"id"`
	Version         string `json:"version"`
	Label           string `json:"label"`
	AssetsSkill     string `json:"assetsSkill,omitempty"`
	AssetsContract  string `json:"assetsContract"`
	CharacterLayout string `json:"characterLayout"`
	SceneLayout     string `json:"sceneLayout"`
	StoryboardSkill string `json:"storyboardSkill,omitempty"`
	StoryboardMode  string `json:"storyboardMode,omitempty"`
	// ScriptSkill / StyleSkill 是这条流水线的拆剧本、导演定画风技能（不走风格档案绑定表）；为空时按绑定表。
	ScriptSkill string `json:"scriptSkill,omitempty"`
	StyleSkill  string `json:"styleSkill,omitempty"`
}

func Workflow(id string) (AssetWorkflow, bool) {
	integrated := func(id, label string) AssetWorkflow {
		return AssetWorkflow{ID: id, Version: WorkflowVersion, Label: label, AssetsSkill: IntegratedAssetsSkill, AssetsContract: ContractIntegratedAssets, CharacterLayout: "four-panel", SceneLayout: "spatial-master"}
	}
	switch strings.TrimSpace(id) {
	case WorkflowSSSFilm, WorkflowSSSDrama:
		kind, label := SSSKindFilm, "流水线一 SSS 电影导演"
		if strings.TrimSpace(id) == WorkflowSSSDrama {
			kind, label = SSSKindDrama, "流水线二 SSS 短剧导演"
		}
		workflow := integrated(strings.TrimSpace(id), label)
		workflow.StoryboardSkill, workflow.StoryboardMode = SSSDirectorSkill(kind), StoryboardModeSSS
		workflow.ScriptSkill, workflow.StyleSkill = SSSScriptSkill(kind), SSSStyleSkill(kind)
		return workflow, true
	case WorkflowOntology:
		workflow := integrated(WorkflowOntology, "流水线三 本体论导演")
		workflow.StoryboardSkill, workflow.StoryboardMode = OntologySkillID, StoryboardModeOntology
		return workflow, true
	case WorkflowIntegrated:
		return integrated(WorkflowIntegrated, "流水线四 资产一体化"), true
	case WorkflowOriginal, "":
		return AssetWorkflow{ID: WorkflowOriginal, Version: WorkflowVersion, Label: "流水线五 原流程", AssetsContract: ContractAssetsExtract, CharacterLayout: "turnaround", SceneLayout: "concept"}, true
	default:
		return AssetWorkflow{}, false
	}
}

// IntegratedAssets reports the asset protocol shared by the SSS, ontology and integrated workflows.
func IntegratedAssets(id string) bool {
	workflow, ok := Workflow(id)
	return ok && workflow.AssetsContract == ContractIntegratedAssets
}

// SSSWorkflowKind 是 SSS 流水线的片种（film / drama）；不是 SSS 流水线时为空。
func SSSWorkflowKind(id string) string {
	switch strings.TrimSpace(id) {
	case WorkflowSSSFilm:
		return SSSKindFilm
	case WorkflowSSSDrama:
		return SSSKindDrama
	}
	return ""
}
