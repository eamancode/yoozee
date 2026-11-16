package drama

import (
	"encoding/json"
	"strings"
	"testing"
)

// spacedObjects 把一个紧凑 JSON 的每个对象起点 {" 都写成 { " / {\t"（嵌套对象一起），先确认仍是合法 JSON。
func spacedObjects(t *testing.T, compact, whitespace string) string {
	t.Helper()
	out := strings.ReplaceAll(compact, `{"`, "{"+whitespace+`"`)
	if !json.Valid([]byte(out)) || out == compact {
		t.Fatalf("spaced object is not a changed valid JSON object: %s", out)
	}
	return out
}

// 第四轮复验（R4-P1-01）：说明文字同一行的第一个资产，对象起点（含嵌套对象）写成 { " 或 {\t" 时，原先整行当说明跳过，
// 解析 1 条、校验通过，主角静默丢失。现在两种空白都完整读出 2 条、两种契约都计 2 条。
func TestAssetsPrefaceSpacedObjectStartsKeepTheFirstAsset(t *testing.T) {
	first := presetRow(t, AssetCharacter, "林深")
	second := presetRow(t, AssetCharacter, "阿婆")
	for _, whitespace := range []string{" ", "\t", "  \t "} {
		spaced := spacedObjects(t, first, whitespace)
		text := "我会按技能统一提取资产。" + spaced + "\n" + second
		rows, stats := AssetsExtractFromText(text)
		if len(rows) != 2 || stats.Invalid != 0 || rows[0].Name != "林深" {
			t.Fatalf("%q: rows=%d invalid=%d first=%q", whitespace, len(rows), stats.Invalid, firstName(rows))
		}
		if err := ValidateIntegratedAssetsText(text); err != nil {
			t.Fatalf("%q: %v", whitespace, err)
		}
		for _, contract := range []string{ContractIntegratedAssets, ""} {
			if n, err := CountAssetsExtractTurn(text, TurnContext{Contract: contract}); err != nil || n != 2 {
				t.Fatalf("%q contract %q: count=%d err=%v", whitespace, contract, n, err)
			}
		}
	}
}

// 带空白起点时的其余边界：相邻对象、字符串里的 { " 与转义、坏尾、说明后读不全，都和紧凑写法同一结论——完整接收或整行 / 整轮拒绝，绝不部分成功。
func TestAssetLineObjectsSpacedStarts(t *testing.T) {
	row := integratedTestRow(AssetProp)
	row.Name = "账本"
	row.Appearance = `封面写着 { "旧账" } 和 \"收讫\" 两字，末尾一个反斜杠 \`
	raw, _ := json.Marshal(row)
	tricky := spacedObjects(t, string(raw), " ")
	lin := spacedObjects(t, presetRow(t, AssetCharacter, "林深"), "\t")
	apo := spacedObjects(t, presetRow(t, AssetCharacter, "阿婆"), " ")
	brokenApo := strings.TrimSuffix(apo, "}")
	cases := []struct {
		name    string
		line    string
		objects int
		matched bool
		err     bool
	}{
		{"spaced line start", lin, 1, true, false},
		{"spaced after preface", "下面是资产：" + lin, 1, true, false},
		{"string with brace quote and escapes", "说明：" + tricky, 1, true, false},
		{"adjacent spaced objects after preface", "说明：" + lin + apo, 2, true, false},
		{"adjacent spaced objects with a space", lin + " " + apo, 2, true, false},
		{"spaced broken tail", "说明：" + lin + brokenApo, 0, true, true},
		{"spaced unterminated after preface", "说明：" + brokenApo, 0, true, true},
		{"spaced object then prose", "说明：" + lin + " 以上。", 0, true, true},
		{"prose with a lone spaced brace", "注意不要写成 { 占位 } 这种格式。", 0, false, false},
		{"compact preface still works", "说明：" + presetRow(t, AssetCharacter, "林深"), 1, true, false},
	}
	for _, c := range cases {
		objects, matched, err := AssetLineObjects(c.line)
		if len(objects) != c.objects || matched != c.matched || (err != nil) != c.err {
			t.Fatalf("%s: objects=%d matched=%v err=%v", c.name, len(objects), matched, err)
		}
	}
	rows, _ := AssetsExtractFromText("说明：" + tricky)
	if len(rows) != 1 || rows[0].Appearance != row.Appearance {
		t.Fatalf("string content must survive: %+v", rows)
	}
	// 坏尾在说明之后：一体化整轮拒绝，原契约记一行读不出、不收前面那个完整对象。
	text := "说明：" + lin + brokenApo + "\n" + presetRow(t, AssetCharacter, "老周")
	if err := ValidateIntegratedAssetsText(text); err == nil {
		t.Fatal("a broken tail after spaced objects must fail the integrated turn")
	}
	if rows, stats := AssetsExtractFromText(text); len(rows) != 1 || stats.Invalid != 1 || rows[0].Name != "老周" {
		t.Fatalf("original parser: rows=%d invalid=%d", len(rows), stats.Invalid)
	}
}
