package drama

import (
	"fmt"
	"strings"
	"testing"
)

func TestSSSConceptWindow(t *testing.T) {
	concept := "## 立意页\n守住摊位\n## 全集结构表\n| 集号 | 主要事件 |\n|---|---|\n"
	for i := 1; i <= 30; i++ {
		label := fmt.Sprintf("第%d集", i)
		if i%3 == 1 {
			label = fmt.Sprint(i)
		}
		if i%3 == 2 {
			label = fmt.Sprintf("E%d", i)
		}
		concept += fmt.Sprintf("|%s|事件-%02d|\n", label, i)
	}
	for _, n := range []int{1, 15, 30} {
		projected := SSSConceptForEpisode(concept, n)
		if !strings.HasPrefix(projected, "## 立意页\n守住摊位") {
			t.Fatal("立意丢失")
		}
		for i := 1; i <= 30; i++ {
			if strings.Contains(projected, fmt.Sprintf("事件-%02d", i)) != (i >= n-1 && i <= n+1) {
				t.Fatalf("%d集结构表错误: %d", n, i)
			}
		}
	}
	bad := strings.Replace(concept, "|E2|", "|未编号|", 1)
	if SSSConceptForEpisode(bad, 1) != bad {
		t.Fatal("不可解析的表被裁掉")
	}
}

func TestSSSDirectProjectionAndPositionLines(t *testing.T) {
	direct := "## 第三部分\n共同要求\n### 第1场 当铺\n甲\n### 第2场 后街\n乙\n### 第3场 车站\n丙"
	got := SSSDirectForScenes(direct, []string{"第2场"})
	if !strings.Contains(got, "共同要求") || !strings.Contains(got, "乙") || strings.Contains(got, "甲") || strings.Contains(got, "丙") {
		t.Fatal(got)
	}
	if SSSDirectForScenes(direct, []string{"未知场"}) != direct {
		t.Fatal("场次缺失未回退")
	}
	mixed := direct + "\n### 雨停后\n场次编号遗漏"
	if SSSDirectForScenes(mixed, []string{"第2场"}) != mixed {
		t.Fatal("部分场次不可解析时仍裁掉了讲戏内容")
	}
	base := "生效参考素材\r\n@站位参考：承接定格。\r\n动作\n"
	if got := ApplyPositionReference(base, "@图片15"); got != strings.Replace(base, "@站位参考", "@图片15", 1) {
		t.Fatal(got)
	}
	if got := StripPositionReference(base); got != "生效参考素材\r\n动作\n" {
		t.Fatal(got)
	}
}
