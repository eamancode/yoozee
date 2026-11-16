package drama

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func presetRow(t *testing.T, category, name string) string {
	t.Helper()
	row := integratedTestRow(category)
	row.Name = name
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// 线上复现：模型把说明文字和第一个资产（主角林深）写在同一行，其余 8 个各占一行。9 个都要读出、校验通过，不能静默丢掉主角。
func TestAssetsPrefaceLineKeepsTheFirstAsset(t *testing.T) {
	lines := []string{"我会按技能统一提取资产与原子化设计。" + presetRow(t, AssetCharacter, "林深")}
	for i, name := range []string{"阿婆", "老周", "小满", "周岸", "护士长"} {
		_ = i
		lines = append(lines, presetRow(t, AssetCharacter, name))
	}
	lines = append(lines, presetRow(t, AssetEnvironment, "夜市雨棚"), presetRow(t, AssetProp, "湿纸币"), presetRow(t, AssetProp, "保温杯"))
	text := strings.Join(lines, "\n")
	rows, stats := AssetsExtractFromText(text)
	if len(rows) != 9 || stats.Invalid != 0 || rows[0].Name != "林深" {
		t.Fatalf("rows=%d invalid=%d first=%q", len(rows), stats.Invalid, firstName(rows))
	}
	if err := ValidateIntegratedAssetsText(text); err != nil {
		t.Fatal(err)
	}
	if n, err := CountAssetsExtractTurn(text, TurnContext{Contract: ContractIntegratedAssets}); err != nil || n != 9 {
		t.Fatalf("turn count = %d %v", n, err)
	}
	if n, err := CountAssetsExtractTurn(text, TurnContext{}); err != nil || n != 9 {
		t.Fatalf("original contract turn count = %d %v", n, err)
	}
}

func firstName(rows []AssetsExtractRow) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[0].Name
}

// 说明文字后面跟的是读不全的对象：一体化契约整轮不合格（重写本轮），不能部分成功；原契约记一行读不出。
func TestAssetsPrefaceWithBrokenObjectRejectsTheTurn(t *testing.T) {
	good := presetRow(t, AssetCharacter, "阿婆")
	broken := "我会提取资产。" + strings.TrimSuffix(presetRow(t, AssetCharacter, "林深"), "}")
	text := broken + "\n" + good
	if err := ValidateIntegratedAssetsText(text); err == nil {
		t.Fatal("a broken object after a preface must fail the integrated turn")
	}
	if _, err := CountAssetsExtractTurn(text, TurnContext{Contract: ContractIntegratedAssets}); err == nil {
		t.Fatal("turn must be retried, not partially accepted")
	}
	rows, stats := AssetsExtractFromText(text)
	if len(rows) != 1 || stats.Invalid != 1 {
		t.Fatalf("original parser: rows=%d invalid=%d", len(rows), stats.Invalid)
	}
	// 完整对象后面又跟一个坏对象（坏尾）：整行不合格，不能只收前一个。
	tail := good + " " + strings.TrimSuffix(presetRow(t, AssetProp, "湿纸币"), "}")
	if err := ValidateIntegratedAssetsText(tail); err == nil {
		t.Fatal("a broken tail object must fail the integrated turn")
	}
	if rows, stats := AssetsExtractFromText(tail); len(rows) != 0 || stats.Invalid != 1 {
		t.Fatalf("broken tail: rows=%d invalid=%d", len(rows), stats.Invalid)
	}
}

// 解码器逐个读对象：字符串里的花括号、引号、转义不会被误切；同一行相邻对象、对象后跟说明文字、围栏、回合标记、纯说明行都对。
func TestAssetLineObjectsEdgeCases(t *testing.T) {
	row := integratedTestRow(AssetProp)
	row.Name = "账本"
	row.Appearance = `封面写着「{旧账}」和 "收讫" 两字，边角有 \ 划痕`
	raw, _ := json.Marshal(row)
	second := presetRow(t, AssetCharacter, "林深")
	cases := []struct {
		name    string
		line    string
		objects int
		matched bool
		err     bool
	}{
		{"braces and quotes inside strings", string(raw), 1, true, false},
		{"preface with braces in strings", "下面是资产：" + string(raw), 1, true, false},
		{"adjacent objects", string(raw) + string(second), 2, true, false},
		// 对象之后的规则不变（见 TestAssetsAdjacentBrokenTailNeverPartiallyAccepted）：只能紧跟完整对象，其他内容整行不合格。
		{"comma separated objects", string(raw) + ", " + second, 0, true, true},
		{"object then prose", string(raw) + " 以上是道具。", 0, true, true},
		{"pure prose", "我会按技能统一提取资产。", 0, false, false},
		{"prose with a lone brace", "注意不要写成 {占位} 这种格式。", 0, false, false},
		{"fence", "```json", 0, false, false},
		{"turn marker", `{"` + ShotLinesMarkerKey + `":{"scene":"s01"}}`, 1, true, false},
		{"unterminated", `{"name":"林深","category":"character"`, 0, true, true},
	}
	for _, c := range cases {
		objects, matched, err := AssetLineObjects(c.line)
		if len(objects) != c.objects || matched != c.matched || (err != nil) != c.err {
			t.Fatalf("%s: objects=%d matched=%v err=%v", c.name, len(objects), matched, err)
		}
	}
	rows, _ := AssetsExtractFromText("下面是资产：" + string(raw))
	if len(rows) != 1 || rows[0].Appearance != row.Appearance {
		t.Fatalf("string content must survive: %+v", rows)
	}
}

// 覆盖提醒：剧本里有台词的人物，这次没提取、项目里也没有，报 R-ASSET-011；项目已有（含别名）或这次提取到的不报；旁白不报。
func TestAssetsMissingSpeakersWarn(t *testing.T) {
	script := ScriptFromText("## 内·夜市·夜\n林深：当了。\n阿婆：拿着。\n老周（小声）：走吧。\n旁白：雨停了。\n林深：好。", "第一集")
	speakers := ScriptSpeakers(script)
	if strings.Join(speakers, ",") != "林深,阿婆,老周,旁白" {
		t.Fatal(speakers)
	}
	row := integratedTestRow(AssetCharacter)
	row.Name = "阿婆"
	known := []AssetsKnown{{Category: AssetCharacter, Name: "周师傅", Aliases: []string{"老周"}}}
	out := CheckAssetsExtract([]AssetsExtractRow{row}, AssetsExtractCheckContext{Known: known, Speakers: speakers})
	var warned []string
	for _, item := range out.Report.Items {
		if item.Rule == RuleAssetMissingCharacter {
			warned = append(warned, item.ShotID)
		}
	}
	if strings.Join(warned, ",") != "林深" {
		t.Fatalf("missing speakers = %v (items %+v)", warned, out.Report.Items)
	}
	if got := MissingAssetSpeakers(speakers, []AssetsExtractRow{row, func() AssetsExtractRow { r := integratedTestRow(AssetCharacter); r.Name = "林深"; return r }()}, known); len(got) != 0 {
		t.Fatal(fmt.Sprint("all covered, got ", got))
	}
}
