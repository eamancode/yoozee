package drama

import (
	"slices"
	"strings"
	"testing"
)

func TestRemoveDirectorUnitPreservesNeighboringMarkdown(t *testing.T) {
	card := func(id string) string {
		return "### " + id + "｜短档｜12 秒｜3 镜｜当铺｜留白\n\n#### 镜头表\n三镜原文\n"
	}
	keep := card("E1-U50")
	doc := "## 第四部分　单元总表\n\n| 单元 | 场 |\n| --- | --- |\n| **E1-U5** | 当铺 |\n| E1-U50 | 当铺 |\n\n" +
		"## 第五部分　单元卡\n\n" + card("E1-U5") + "\n" + keep + "\n## 第六部分　后期备忘\n通用备注\n" +
		"\n## 返修记录 E1-U5\n旧备注\n\n## 返修记录 E1-U50\n相邻卡备注\n"
	updated, err := RemoveDirectorUnit(doc, "E1-U5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated, keep) || !strings.Contains(updated, "## 返修记录 E1-U50\n相邻卡备注") ||
		strings.Contains(updated, card("E1-U5")) || strings.Contains(updated, "旧备注") {
		t.Fatal(updated)
	}
	units, err := SSSUnits(updated)
	if err != nil || !slices.Equal(units, []string{"E1-U50"}) {
		t.Fatal(units, err)
	}
	cards, err := ParseSSSEpisodeCards(updated)
	if err != nil || len(cards) != 1 || cards[0].ID != "E1-U50" {
		t.Fatal(cards, err)
	}
	last, err := RemoveDirectorUnit(updated, "E1-U50")
	if body, ok := SSSSection(last, "第五部分"); err != nil || !ok || body != "本集无" {
		t.Fatalf("last card: %q, %v", body, err)
	}
	if _, err := RemoveDirectorUnit(strings.Replace(doc, keep, card("E1-U5"), 1), "E1-U5"); err == nil {
		t.Fatal("duplicate document IDs must not remove two cards")
	}
}
