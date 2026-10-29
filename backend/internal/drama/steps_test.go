package drama

import (
	"flag"
	"os"
	"strings"
	"testing"
)

var updateGenerated = flag.Bool("update", false, "重新生成前端契约模板")

// 整段 JSON 的契约文字（@2，画布拆镜仍用它）由 schema 生成：与原先前端手写的契约（含技能 outputFields 补的 spec 行）逐句对得上，
// 只有字段清单的类型写法和折行不同。
func TestChapterShotsContractText(t *testing.T) {
	c, err := BuiltinContract(ContractCanvasStoryboard)
	if err != nil {
		t.Fatal(err)
	}
	got := c.Text(c.DefaultRequirement(), ContractTextOptions{SkillShaped: true})
	want := strings.Join([]string{
		"【受保护输出契约】",
		"- 只返回一个 JSON 对象，不要 Markdown、表格、代码块、注释或任何解释文字。",
		`- 顶层固定为 {"title": string, "rows": object[]}，rows 至少 1 项。每个镜头必须能独立用于生成首帧图片和镜头视频。`,
		"- rows 每项对应一个镜头，按剧情顺序排列；字段缺失时填空字符串或空数组，不要省略字段：",
		"  shotNumber（整数，从 1 递增）、durationSeconds（整数，1-60）、plotDescription（本镜可见的剧情与关键动作）、",
		"  dialogue（本镜实际念出的台词或简短旁白，超长必须拆镜）、characters（字符串数组，本镜出现的角色名）、narrativeIntent、",
		"  viewerPOV、performanceBlocking、shotSize（景别）、emotion、lightingAndAtmosphere、",
		"  audioEffects、camera、motion、timeBeats、imageGenerationPrompt（首帧画面提示词）、",
		"  videoMotionPrompt（运镜与结尾状态）、continuityOut、negativePrompt、mustHave（字符串数组，最多 3 项）、",
		"  optionalDetails（字符串数组）。",
		"- rows 每项再加 spec（对象，形状见技能说明，每个镜头都要写，不能省略）：yoozee 镜头表契约，形状见下文「spec 的形状」；装段、检查、编译都从这里取",
		"- 镜头数量以完整覆盖剧情因果、人物关系和关键台词为准，不要为压缩数量删减关键情节。",
	}, "\n")
	if got != want {
		t.Fatalf("契约文字变了：\n%s", got)
	}
	if line := c.FieldInstruction("assets"); line != "- rows 每项再加 assets（字符串数组）：本镜画面里出现的上面这些角色、场景和道具，名称原样抄写，清单外的不写，没有就写空数组；角色同时写进 characters。镜头换了场景，assets 里就写新场景。" {
		t.Fatalf("assets 字段要求：%s", line)
	}
	canvas := c.Text("X", ContractTextOptions{})
	for _, absent := range []string{"spec", "assets"} {
		if strings.Contains(canvas, absent) {
			t.Fatalf("画布契约不该有 %s：\n%s", absent, canvas)
		}
	}
	if fields := c.RowFields(); len(fields) != 23 || fields[0] != "shotNumber" || fields[22] != "assets" {
		t.Fatalf("字段顺序取 required：%v", fields)
	}
}

// 生成分段 @3 一镜一行：格式句、形状句和逐字段要求换成按行写，字段清单与 @2 相同。
func TestChapterShotsLinesContractText(t *testing.T) {
	c, err := BuiltinContract(ContractChapterShots)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := BuiltinContract(ContractCanvasStoryboard)
	if !c.Lines() || old.Lines() || strings.Join(c.RowFields(), ",") != strings.Join(old.RowFields(), ",") {
		t.Fatalf("@3 一镜一行、字段与 @2 相同：%v", c.RowFields())
	}
	text := c.Text(c.DefaultRequirement(), ContractTextOptions{SkillShaped: true})
	got := strings.Split(text, "\n")
	if !strings.HasPrefix(got[1], "- 逐行输出（JSON Lines）") || got[2] != "- 至少输出 1 个镜头。每个镜头必须能独立用于生成首帧图片和镜头视频。" || !strings.HasPrefix(got[3], "- 每行一个镜头") {
		t.Fatalf("格式句：%s", text)
	}
	if !strings.Contains(text, "- 每个镜头再加 spec（对象") || !strings.HasPrefix(c.FieldInstruction("assets"), "- 每个镜头再加 assets（字符串数组）") {
		t.Fatalf("逐字段要求按镜头写：%s", text)
	}
}

// 前端画布拆镜的契约模板由同一份 schema 生成；schema 改了而没重新生成时这里失败。
func TestCanvasStoryboardContractGenerated(t *testing.T) {
	c, err := BuiltinContract(ContractCanvasStoryboard)
	if err != nil {
		t.Fatal(err)
	}
	const path = "../../../web/src/lib/canvas/storyboard-output-contract.generated.ts"
	want := CanvasContractTS(c)
	if *updateGenerated {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s 与 schema 不一致，按文件头的命令重新生成", path)
	}
}

func TestStepTable(t *testing.T) {
	step, ok := StepByID(StepChapterShots)
	if !ok || step.Contract != ContractChapterShots || step.DefaultSkill != "yz-chapter-shots" || step.SkillBudgetChars != 32000 {
		t.Fatalf("chapter.shots：%+v", step)
	}
	if step.Rows == nil || step.Check == nil || step.Pack == nil {
		t.Fatal("chapter.shots 要带解析器与检查器")
	}
	if g := step.Gate; g.ParseRate != 1 || g.SpecRate != 1 || g.DialogueCoverage != 1 || g.MaxCheckErrors != 0 || g.MaxWarnRatio != 1.2 || g.MaxDurationRatio != 1.1 || g.MaxTruncated != 0 {
		t.Fatalf("启用门槛：%+v", g)
	}
	for _, s := range Steps() {
		// 戏类步骤的输出是技能原生格式，契约只是版本号（没有 JSON schema），格式要求写在每一轮的【本节输出要求】里。
		if _, ok := ShotGenreOfStep(s.ID); ok {
			continue
		}
		if _, err := BuiltinContract(s.Contract); err != nil {
			t.Fatalf("%s：%v", s.ID, err)
		}
	}
	for _, g := range ShotGenres() {
		s, ok := StepByID(g.Step)
		if !ok || s.SkillBudgetChars != 40000 || s.SkillLinkedFiles != 4 || s.Turns.Count == nil || s.Scope != StepScopeUnit || s.Contract != g.Contract {
			t.Fatalf("戏类步骤 %s：%+v", g.Step, s)
		}
		if want := map[string]string{ShotGenreWenxi15: "yz-shots-wenxi15", ShotGenreWenxi30: "yz-shots-wenxi30", ShotGenreWuxi: "yz-shots-wuxi"}[g.ID]; s.DefaultSkill != want {
			t.Fatalf("%s 默认技能 %q", g.Step, s.DefaultSkill)
		}
		// 武戏按风格档案分两个包：仿真人档案（真人剧、短剧视感）用去掉 CG / 仙法 / 魔物的精简切片，其余用完整切片。
		for _, p := range BuiltinProfileIDs() {
			want := s.DefaultSkill
			if g.ID == ShotGenreWuxi && slicesContains(WuxiLiveProfiles, p) {
				want = "yz-shots-wuxi-live"
			}
			if got := s.DefaultSkillFor(p); got != want {
				t.Fatalf("%s 档案 %s 默认技能 %q，应为 %q", g.Step, p, got, want)
			}
		}
	}
	if g := DefaultShotGenre(15); g.ID != ShotGenreWenxi15 {
		t.Fatalf("15 秒模型用文戏 15：%s", g.ID)
	}
	if g := DefaultShotGenre(30); g.ID != ShotGenreWenxi30 {
		t.Fatalf("30 秒模型用文戏 30：%s", g.ID)
	}
}
