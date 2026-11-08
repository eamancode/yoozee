package drama

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestAssetsAdjacentObjectsPreserveEveryRecord(t *testing.T) {
	var lines []string
	for i := 0; i < 19; i++ {
		row := integratedTestRow(AssetProp)
		row.Name = fmt.Sprintf("道具%d", i)
		row.GenerationPrompt = "保留 {结构}、\"引号\" 和反斜线\\，不修改字段"
		raw, _ := json.Marshal(row)
		if i == 16 || i == 18 {
			lines[len(lines)-1] += string(raw)
		} else {
			lines = append(lines, string(raw))
		}
	}
	text := "{\"@turn\":{\"index\":1}}\n" + strings.Join(lines, "\n")
	rows, stats := AssetsExtractFromText(text)
	if len(rows) != 19 || stats.Rows != 19 || stats.Invalid != 0 {
		t.Fatal(len(rows), stats)
	}
	if rows[18].GenerationPrompt != "保留 {结构}、\"引号\" 和反斜线\\，不修改字段" {
		t.Fatal("JSON values changed")
	}
	if n, err := CountAssetsExtractTurn(text, TurnContext{Contract: ContractIntegratedAssets}); err != nil || n != 19 {
		t.Fatal(n, err)
	}
}

func TestAssetsAdjacentBrokenTailNeverPartiallyAccepted(t *testing.T) {
	raw, _ := json.Marshal(integratedTestRow(AssetProp))
	for _, tail := range []string{`{"name":`, ` null`, ` []`, `, {}`, ` garbage`} {
		text := string(raw) + tail
		rows, stats := AssetsExtractFromText(text)
		if len(rows) != 0 || stats.Invalid != 1 {
			t.Fatal("accepted a partial physical line", tail, stats)
		}
		if err := ValidateIntegratedAssetsText(text); err == nil {
			t.Fatal("strict validation accepted broken tail", tail)
		}
	}
	bad := integratedTestRow(AssetProp)
	bad.Design.BodyDescription = ""
	second, _ := json.Marshal(bad)
	if err := ValidateIntegratedAssetsText(string(raw) + string(second)); err == nil {
		t.Fatal("second complete object escaped typed validation")
	}
}
