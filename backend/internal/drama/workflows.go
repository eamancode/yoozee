package drama

import "strings"

const (
	WorkflowOntology         = "pipeline-1"
	WorkflowIntegrated       = "pipeline-2"
	WorkflowOriginal         = "pipeline-3"
	WorkflowVersion          = "1"
	IntegratedAssetsSkill    = "yz-assets-integrated"
	ContractIntegratedAssets = "assets.extract@2"
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
}

func Workflow(id string) (AssetWorkflow, bool) {
	switch strings.TrimSpace(id) {
	case WorkflowOntology, WorkflowIntegrated:
		workflow := AssetWorkflow{ID: strings.TrimSpace(id), Version: WorkflowVersion, Label: "流水线二 资产一体化", AssetsSkill: IntegratedAssetsSkill, AssetsContract: ContractIntegratedAssets, CharacterLayout: "four-panel", SceneLayout: "spatial-master"}
		if workflow.ID == WorkflowOntology {
			workflow.Label = "流水线一 本体论导演"
			workflow.StoryboardSkill, workflow.StoryboardMode = "yz-ontology-director", "ontology"
		}
		return workflow, true
	case WorkflowOriginal, "":
		return AssetWorkflow{ID: WorkflowOriginal, Version: WorkflowVersion, Label: "流水线三 原流程", AssetsContract: ContractAssetsExtract, CharacterLayout: "turnaround", SceneLayout: "concept"}, true
	default:
		return AssetWorkflow{}, false
	}
}

// IntegratedAssets reports the asset protocol shared by the ontology and integrated workflows.
func IntegratedAssets(id string) bool {
	workflow, ok := Workflow(id)
	return ok && workflow.AssetsContract == ContractIntegratedAssets
}
