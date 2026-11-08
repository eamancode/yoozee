package drama

import (
	"encoding/json"
	"strings"
	"testing"
)

func integratedTestRow(category string) AssetsExtractRow {
	d := &AssetDesign{Role: "主角", IdentityAppearance: "同一人物眉眼", Clothing: "日常服装", SpatialLayout: "后方棚架，左右开放", BodyDescription: "空置杯体", ProductionPriority: "high", Evidence: []AssetEvidence{{Kind: "原文", Location: "第一场", Text: "人物进入市场"}}}
	return AssetsExtractRow{Category: category, Name: "测试资产", Appearance: "稳定外观", PromptEn: "stable appearance", GenerationPrompt: "完整资产图片设计", FirstScene: 1, Design: d}
}

func TestIntegratedDesignValidationPreventsSilentLoss(t *testing.T) {
	for _, category := range []string{AssetCharacter, AssetEnvironment, AssetProp} {
		row := integratedTestRow(category)
		raw, _ := json.Marshal(row)
		if n, err := CountAssetsExtractTurn(string(raw), TurnContext{Contract: ContractIntegratedAssets}); err != nil || n != 1 {
			t.Fatal(category, n, err)
		}
	}
	base := integratedTestRow(AssetProp)
	raw, _ := json.Marshal(base)
	for _, text := range []string{
		strings.Replace(string(raw), `"production_priority":"high"`, `"production_priority":null`, 1),
		strings.Replace(string(raw), `"body_description":"空置杯体"`, `"body_description":{"shape":"杯体"}`, 1),
		strings.Replace(string(raw), `"evidence":[{"kind":"原文","location":"第一场","text":"人物进入市场"}]`, `"evidence":[]`, 1),
		strings.Replace(string(raw), `"generation_prompt":"完整资产图片设计"`, `"generation_prompt":""`, 1),
	} {
		if _, err := CountAssetsExtractTurn(text, TurnContext{Contract: ContractIntegratedAssets}); err == nil {
			t.Fatal("malformed design passed new contract", text)
		}
		if _, err := CountAssetsExtractTurn(text, TurnContext{}); err != nil {
			t.Fatal("original parser changed", err)
		}
	}
}

func TestIntegratedReusedAssetRetainsEvidenceWithoutRepeatingLook(t *testing.T) {
	row := integratedTestRow(AssetCharacter)
	row.Appearance, row.PromptEn, row.GenerationPrompt = "", "", ""
	row.Design.Role, row.Design.IdentityAppearance, row.Design.Clothing = "", "", ""
	raw, _ := json.Marshal(row)
	if err := ValidateIntegratedAssetsText(string(raw)); err != nil {
		t.Fatal(err)
	}
	row.Variants = []AssetsVariant{{Label: "雨湿", Kind: AssetVariantState, Scenes: []int{1}, Appearance: "湿衣", PromptEn: "wet clothes"}}
	raw, _ = json.Marshal(row)
	if err := ValidateIntegratedAssetsText(string(raw)); err == nil {
		t.Fatal("variant without full prompt passed")
	}
	row.Variants[0].GenerationPrompt = "同一人物雨湿四宫格"
	raw, _ = json.Marshal(row)
	if err := ValidateIntegratedAssetsText(string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestIntegratedCharacterRoleCannotBeMissing(t *testing.T) {
	row := integratedTestRow(AssetCharacter)
	row.Design.Role = ""
	raw, _ := json.Marshal(row)
	if err := ValidateIntegratedAssetsText(string(raw)); err == nil {
		t.Fatal("empty role passed")
	}
}

func TestIntegratedVariantFailureNamesExactField(t *testing.T) {
	base := AssetsVariant{Label: "工作服", Kind: AssetVariantCostume, Scenes: []int{2}, Appearance: "具体工作服", PromptEn: "same character in workwear", GenerationPrompt: "同一人物完整四宫格"}
	for _, tc := range []struct {
		field string
		edit  func(*AssetsVariant)
	}{
		{"variants.kind", func(v *AssetsVariant) { v.Kind = "costume/state" }},
		{"variants.appearance", func(v *AssetsVariant) { v.Appearance = "" }},
		{"variants.prompt_en", func(v *AssetsVariant) { v.PromptEn = "" }},
		{"variants.generation_prompt", func(v *AssetsVariant) { v.GenerationPrompt = "" }},
		{"variants.scenes", func(v *AssetsVariant) { v.Scenes = nil }},
	} {
		row := integratedTestRow(AssetCharacter)
		variant := base
		tc.edit(&variant)
		row.Variants = []AssetsVariant{variant}
		raw, _ := json.Marshal(row)
		if err := ValidateIntegratedAssetsText(string(raw)); err == nil || !strings.Contains(err.Error(), tc.field) {
			t.Fatal("field-specific feedback lost", tc.field, err)
		}
	}
	converted, ok := variantFromTitledRow(AssetsExtractRow{Category: AssetCharacter, Name: "人物·工作服", SourceSkillVersionID: "frozen-source"})
	if !ok || converted.SourceSkillVersionID != "frozen-source" {
		t.Fatal("variant normalization lost frozen recipe source")
	}
}
