package skills

import (
	"strings"
	"testing"
)

// 技能在 metadata.outputFields 声明额外输出字段（单行 JSON 数组），由前端写进分镜输出契约；
// 后端按行解析 frontmatter，必须跳过这个键且不影响其后字段。
func TestBuiltinSkillMetadataAcceptsOutputFields(t *testing.T) {
	metadata, err := parseBuiltinSkillMetadata([]byte(strings.Join([]string{
		"---",
		`name: "章节分镜"`,
		`description: "按场拆镜"`,
		"metadata:",
		`  version: "2.2.1"`,
		`  outputFields: [{"name": "spec", "type": "object", "description": "形状见技能说明：id、scene"}]`,
		"  tag: drama",
		"  initialLikeCount: 3",
		`  extraInfo: ""`,
		"---",
		"# 正文",
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Version != "2.2.1" || metadata.Tag != "drama" || metadata.InitialLike != 3 || metadata.ExtraInfo != "" || metadata.Description != "按场拆镜" {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestBuiltinChapterShotsDeclaresSpecOutputField(t *testing.T) {
	packages, err := loadBuiltinSkillPackages(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range packages {
		if item.skill.ID != "yz-chapter-shots" {
			continue
		}
		if item.skill.Tag != "drama" || item.skill.Name != "yoozee · 章节分镜（镜头表）" || item.archive.Metadata.Version != "2.3.0" {
			t.Fatalf("skill = %s / %s / %s", item.skill.Name, item.skill.Tag, item.archive.Metadata.Version)
		}
		if !strings.Contains(item.skill.Instruction, `  outputFields: [{"name": "spec", "type": "object"`) {
			t.Fatal("yz-chapter-shots SKILL.md 没有在 metadata 里声明 spec 输出字段")
		}
		return
	}
	t.Fatal("yz-chapter-shots 内置技能没有加载")
}
