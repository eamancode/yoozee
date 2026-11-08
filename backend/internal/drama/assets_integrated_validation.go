package drama

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ValidateIntegratedAssetsText guards the typed design before checkpoints and writes.
// The original contract keeps its existing permissive parser.
func ValidateIntegratedAssetsText(text string) error {
	records := 0
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(jsTrim(raw), ",")
		if !strings.HasPrefix(line, "{") {
			continue
		}
		objects, err := assetJSONObjects(line)
		if err != nil {
			return errors.New("流水线一资产行不是完整JSON对象")
		}
		for _, fields := range objects {
			if _, marker := fields[ShotLinesMarkerKey]; marker {
				continue
			}
			var row AssetsExtractRow
			encoded, _ := json.Marshal(fields)
			if err := json.Unmarshal(encoded, &row); err != nil {
				var fieldError *json.UnmarshalTypeError
				if errors.As(err, &fieldError) {
					return fmt.Errorf("流水线一字段 %s 类型应为 %s，请按契约修正后输出完整本轮", fieldError.Field, fieldError.Type)
				}
				return errors.New("流水线一资产字段无法按契约读取")
			}
			row.Category = NormalizeAssetCategory(row.Category)
			if strings.TrimSpace(row.Name) == "" || AssetCategoryName(row.Category) == "" {
				return errors.New("流水线一资产必须包含有效name和category")
			}
			fail := func(field string) error {
				return fmt.Errorf("「%s」缺少有效 %s，请保留本轮全部资产并补齐", row.Name, field)
			}
			if row.Design == nil {
				return fail("design对象")
			}
			d := row.Design
			if len(d.Evidence) == 0 {
				return fail("design.evidence")
			}
			for _, evidence := range d.Evidence {
				if !slicesContainsEvidenceKind(evidence.Kind) || jsTrim(evidence.Location) == "" || jsTrim(evidence.Text) == "" {
					return fail("design.evidence的kind/location/text")
				}
			}
			if row.HasLook() {
				if jsTrim(row.Appearance) == "" || jsTrim(row.PromptEn) == "" || jsTrim(row.GenerationPrompt) == "" || jsLen(row.GenerationPrompt) > 16000 {
					return fail("appearance/prompt_en/generation_prompt（完整提示词最多16000字符）")
				}
				switch row.Category {
				case AssetCharacter:
					if jsTrim(d.Role) == "" || jsTrim(d.IdentityAppearance) == "" || jsTrim(d.Clothing) == "" {
						return fail("design.role/identity_appearance/clothing字符串")
					}
				case AssetEnvironment:
					if jsTrim(d.SpatialLayout) == "" {
						return fail("design.spatial_layout字符串")
					}
				case AssetProp:
					if jsTrim(d.BodyDescription) == "" || d.ProductionPriority != "high" && d.ProductionPriority != "medium" && d.ProductionPriority != "low" {
						return fail("design.body_description及production_priority(high/medium/low)")
					}
				}
			}
			for _, variant := range row.Variants {
				if row.Category != AssetCharacter {
					return fail("variants仅属于人物，场景和道具请写空数组")
				}
				if jsTrim(variant.Label) == "" {
					return fail("variants.label")
				}
				if AssetVariantKindName(variant.Kind) == "" {
					return fail("variants.kind：只能选择字符串\"costume\"或\"state\"中的一个，不允许组合值")
				}
				if len(variant.Scenes) == 0 {
					return fail("variants.scenes正整数数组")
				}
				if jsTrim(variant.Appearance) == "" {
					return fail("variants.appearance（复用子卡也需重述本阶段外观）")
				}
				if jsTrim(variant.PromptEn) == "" {
					return fail("variants.prompt_en（复用子卡也需完整英文摘要）")
				}
				if jsTrim(variant.GenerationPrompt) == "" || jsLen(variant.GenerationPrompt) > 16000 {
					return fail("variants.generation_prompt（本阶段完整四宫格提示词，1～16000字符，复用子卡也不可留空）")
				}
				for _, scene := range variant.Scenes {
					if scene < 1 {
						return fail("variants.scenes正整数")
					}
				}
			}
			records++
		}
	}
	if records == 0 {
		return errors.New("流水线一没有完整资产记录")
	}
	return nil
}

func slicesContainsEvidenceKind(kind string) bool {
	return kind == "原文" || kind == "推导" || kind == "设计建议"
}
