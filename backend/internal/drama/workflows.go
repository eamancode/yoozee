package drama

import "strings"

const (
	WorkflowIntegrated       = "pipeline-1"
	WorkflowOriginal         = "pipeline-2"
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
}

func Workflow(id string) (AssetWorkflow, bool) {
	switch strings.TrimSpace(id) {
	case WorkflowIntegrated:
		return AssetWorkflow{ID: WorkflowIntegrated, Version: WorkflowVersion, Label: "流水线一", AssetsSkill: IntegratedAssetsSkill, AssetsContract: ContractIntegratedAssets, CharacterLayout: "four-panel", SceneLayout: "spatial-master"}, true
	case WorkflowOriginal, "":
		return AssetWorkflow{ID: WorkflowOriginal, Version: WorkflowVersion, Label: "流水线二", AssetsContract: ContractAssetsExtract, CharacterLayout: "turnaround", SceneLayout: "concept"}, true
	default:
		return AssetWorkflow{}, false
	}
}
