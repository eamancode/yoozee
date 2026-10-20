package drama

import (
	"strings"
	"testing"
)

// 拆段：S02 拆出 S02b，新顺序 S01、S02、S02b、S03 —— 不删任何段。
func TestPlanSegmentOrderSplitKeepsEverySegment(t *testing.T) {
	existing := []ExistingSegment{{ID: "S01"}, {ID: "S02"}, {ID: "S03"}}
	removed, err := PlanSegmentOrder([]string{"S01", "S02", "S02b", "S03"}, []string{"S02", "S02b"}, existing)
	if err != nil {
		t.Fatalf("拆段的新顺序应通过：%v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("拆段不该删段：%v", removed)
	}
}

// 合段：S02 吸收 S03，新顺序里没有 S03 —— S03 要删除。
func TestPlanSegmentOrderMergeRemovesAbsorbedSegment(t *testing.T) {
	existing := []ExistingSegment{{ID: "S01"}, {ID: "S02"}, {ID: "S03"}}
	removed, err := PlanSegmentOrder([]string{"S01", "S02"}, []string{"S02"}, existing)
	if err != nil {
		t.Fatalf("合段的新顺序应通过：%v", err)
	}
	if len(removed) != 1 || removed[0] != "S03" {
		t.Fatalf("合段应只删 S03，得到 %v", removed)
	}
}

func TestPlanSegmentOrderRejectsInconsistentOrders(t *testing.T) {
	existing := []ExistingSegment{{ID: "S01"}, {ID: "S02"}, {ID: "S03", Derived: true}}
	cases := []struct {
		name    string
		order   []string
		written []string
		want    string
	}{
		{"写入的段不在顺序里", []string{"S01", "S03"}, []string{"S02"}, "不在段顺序里"},
		{"段号重复", []string{"S01", "S02", "S02", "S03"}, []string{"S02"}, "出现了两次"},
		{"顺序里有不存在的段", []string{"S01", "S02", "S03", "S09"}, []string{"S02"}, "既不是已有的段"},
		{"合成段号的旧镜头不能靠省略删除", []string{"S01", "S02"}, []string{"S02"}, "还不是分段"},
		{"段号格式无效", []string{"S01", "shot-2", "S03"}, nil, "无效"},
	}
	for _, c := range cases {
		_, err := PlanSegmentOrder(c.order, c.written, existing)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s：应报「%s」，得到 %v", c.name, c.want, err)
		}
	}
}

// 不给顺序就是只改段内容：不做任何重排和删除。
func TestPlanSegmentOrderWithoutOrderIsNoop(t *testing.T) {
	removed, err := PlanSegmentOrder(nil, []string{"S09"}, []ExistingSegment{{ID: "S01"}})
	if err != nil || removed != nil {
		t.Fatalf("没有顺序时不应重排：%v %v", removed, err)
	}
}
