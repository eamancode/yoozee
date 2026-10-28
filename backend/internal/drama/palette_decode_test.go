package drama

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// 线上 M7.2 验收（七床 1027）导演风格三个失败任务的原始输出（任务 textDraft，每轮重试一段，以回合标记开头）：
// 5ab2413a（claude-opus-5-5）、ab2284e6（deepseek-v4.1-flash）把 references 写成字符串数组，整行读不出、三次重试都失败；
// 评测任务 c548acab（deepseek-v4.1-flash，评测章节 qichuang-e01）前两次同样是 references 数组，第三次风格锚那一行少了结尾的 }。
// 场次色卡沿用同一套逐字段读法：旧写法（JSON 头 + scene_no）的每一次重试都要读出这一集的全部场、当轮校验通过；风格锚行忽略。
func TestScenePaletteOnlineOutputsParse(t *testing.T) {
	for file, scenes := range map[string]int{"opus-5ab2413a.txt": 2, "deepseek-ab2284e6.txt": 2, "eval-deepseek-c548acab.txt": 3} {
		raw, err := os.ReadFile("testdata/online-style-bible/" + file)
		if err != nil {
			t.Fatal(err)
		}
		attempts := strings.Split(string(raw), `{"@turn"`)[1:]
		if len(attempts) != 3 {
			t.Fatalf("%s 应有三次重试：%d", file, len(attempts))
		}
		for i, a := range attempts {
			text := `{"@turn"` + a
			out, stats := ScenePaletteFromText(text, 1)
			if len(out.Script) != scenes || stats.AnchorIgnored != 1 {
				t.Fatalf("%s 第 %d 次：应读出 %d 场、忽略风格锚：%+v", file, i+1, scenes, stats)
			}
			if n, err := CountScenePaletteTurn(text, TurnContext{Index: 1, Turn: ShotsScene{ID: "1"}}); err != nil || n != scenes {
				t.Fatalf("%s 第 %d 次当轮校验应通过：%d %v", file, i+1, n, err)
			}
			for _, e := range out.Script {
				if strings.Count(e.References, styleTextSep) != 3 || strings.Contains(e.References, "[") || len(e.Coerced) != 0 {
					t.Fatalf("%s %s：references 四项应拼成一个字符串、不算降级：%q %v", file, e.Code(), e.References, e.Coerced)
				}
			}
			report := CheckScenePalette(out, ScenePaletteCheckContext{})
			if report.Errors != 0 {
				t.Fatalf("%s 第 %d 次检查不应有 error：%+v", file, i+1, report.Items)
			}
			if !slices.ContainsFunc(report.Items, func(item CheckItem) bool { return item.Rule == RuleScenePaletteAnchor }) {
				t.Fatalf("%s 多写的风格锚应给 warn：%+v", file, report.Items)
			}
		}
	}
}

// 字段类型不符只降级那个字段：references 数组拼成一句（契约允许，不报）；hex 数组取第一个、usage 对象转文字、集号写成字符串都读出并报 warn。
func TestScenePaletteFieldTypeFallback(t *testing.T) {
	line := `{"episode":"1","scene_no":1,"scene":["内·电梯口·夜"],"inherits_from":"","references":["《误杀》雨夜","《白日焰火》"],` +
		`"shared_colors":[{"name_zh":"炭黑","name_en":"Charcoal","hex":["#1A1518"],"usage":{"角色":"周岸夹克","光源":"无"},"group":"主角组"},"#6e6e6e"],` +
		`"new_colors":{"name_zh":"警示红","name_en":"Alarm Red","hex":"#B3261E","usage":["呼叫铃",1],"group":"props"}}`
	out, stats := ScenePaletteFromText(line, 1)
	if len(out.Script) != 1 || stats.Invalid != 0 || stats.Coerced != 1 {
		t.Fatalf("类型不符不应丢整行：%+v %+v", out.Script, stats)
	}
	e := out.Script[0]
	if e.References != "《误杀》雨夜；《白日焰火》" || e.Code() != "E01S01" || e.Scene != "内·电梯口·夜" {
		t.Fatalf("references 拼接、集号与场次头降级：%+v", e)
	}
	colors := e.Colors()
	if len(colors) != 3 || colors[0].Hex != "#1A1518" || colors[0].Usage != "角色：周岸夹克；光源：无" || colors[0].Group != PaletteGroupProtagonist || colors[1].Hex != "#6E6E6E" || colors[2].Usage != "呼叫铃；1" || colors[2].Group != PaletteGroupProps {
		t.Fatalf("颜色字段降级：%+v", colors)
	}
	report := CheckScenePalette(out, ScenePaletteCheckContext{})
	var notes []string
	for _, item := range report.Items {
		if item.Rule == RuleScenePaletteFieldType {
			notes = append(notes, item.Message)
		}
	}
	joined := strings.Join(notes, "\n")
	for _, want := range []string{"episode 写成了字符串", "scene 写成了数组", "「炭黑」的 hex 写成了数组，取第一个「#1A1518」", "「炭黑」的 usage 写成了对象", "shared_colors 第 2 个颜色只写了 HEX", "new_colors 写成了一个对象", "「警示红」的 usage 写成了数组"} {
		if !strings.Contains(joined, want) {
			t.Errorf("检查缺 warn「%s」：\n%s", want, joined)
		}
	}
	if report.Errors != 0 || strings.Contains(joined, "references") {
		t.Fatalf("降级只报 warn，references 数组不报：%+v", report.Items)
	}
}

// 风格锚（风格锁定读）：没包 style_anchor 也认；空的、读不出的、没写的，错误文字说清缺什么。
func TestStyleAnchorVariants(t *testing.T) {
	group := `{"group": "主角组", "colors": [{"name_zh": "炭黑", "name_en": "Charcoal", "hex": "#1A1518", "usage": "夹克"}]}`
	flat, _ := StyleLockFromText(`{"medium": "真人实拍", "lighting": "实景光"}` + "\n" + group)
	if flat.Anchor.Medium != "真人实拍" || flat.AnchorProblem() != "" || !strings.Contains(flat.Anchor.Coerced[0], "没有包在 style_anchor 里") {
		t.Fatalf("没包 style_anchor 的风格锚：%+v", flat.Anchor)
	}
	for name, c := range map[string]struct{ text, want string }{
		"没写":     {group, "没有写全局风格锚"},
		"读不出":    {`{"style_anchor": {"medium": "真人实拍", "lighting": "实景光}` + "\n" + group, "不是合法的 JSON"},
		"字段名不认识": {`{"style_anchor": {"画风": "真人实拍"}}` + "\n" + group, "字段名一个都不认识：画风"},
	} {
		out, _ := StyleLockFromText(c.text)
		if problem := out.AnchorProblem(); !strings.Contains(problem, c.want) {
			t.Errorf("%s：%q", name, problem)
		}
	}
	// 后面再写一个空的风格锚，不盖掉前面的。
	twice, _ := StyleLockFromText(`{"style_anchor": {"medium": "真人实拍"}}` + "\n" + `{"style_anchor": {}}`)
	if twice.Anchor.Medium != "真人实拍" {
		t.Fatalf("空风格锚盖掉了前面的：%+v", twice.Anchor)
	}
}

// 契约文字：references 写明是一个字符串、多个用「；」分隔（schema 用 oneOf 同时接受字符串数组，契约按首选写法写）；每色带 group。
func TestScenePaletteContractText(t *testing.T) {
	c, err := BuiltinContract(ContractScenePalette)
	if err != nil {
		t.Fatal(err)
	}
	text := c.Text(c.DefaultRequirement(), ContractTextOptions{SkillShaped: true})
	for _, want := range []string{"references（视觉影视参考预案，2 到 4 个，写成一个字符串，多个用「；」分隔）", "references 是一个字符串", "不写成数组", "scene（场编号，如 E01S02）", "group 写它归主基调色卡的哪一组", "不写风格锚"} {
		if !strings.Contains(text, want) {
			t.Errorf("契约缺「%s」：\n%s", want, text)
		}
	}
	if !strings.Contains(string(c.Schema()), `"oneOf"`) {
		t.Fatal("schema 的 references 应是 oneOf")
	}
}

func TestBalanceJSONLine(t *testing.T) {
	for in, want := range map[string]string{
		`{"a": {"b": [1, 2`: `{"a": {"b": [1, 2]}}`,
		`{"a": "x}"`:        `{"a": "x}"}`,
		`{"a": "\"{"`:       `{"a": "\"{"}`,
		`{"a": 1}`:          `{"a": 1}`,
		`{"a": [1}`:         `{"a": [1}`,
		`{"a": "没收尾`:        `{"a": "没收尾`,
	} {
		if got, _ := balanceJSONLine(in); got != want {
			t.Errorf("%s → %s，应为 %s", in, got, want)
		}
	}
	if obj, repaired, ok := parseJSONObjectLine(`{"a": 1}}`); !ok || !repaired || string(obj["a"]) != "1" {
		t.Fatalf("结尾多一个括号也读出：%v %v %v", obj, repaired, ok)
	}
}
