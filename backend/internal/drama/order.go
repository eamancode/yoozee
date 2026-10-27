package drama

import (
	"fmt"
	"strings"
	"unicode"
)

// ExistingSegment 是一章里已有的段：Derived 表示旧镜头没有分段 JSON、段号是按位置合成的。
type ExistingSegment struct {
	ID      string
	Derived bool
}

// PlanSegmentOrder 校验整章重排（拆段、合段）：order 是本章全部段号的新顺序，written 是这次写入的段号。
// 返回不在新顺序里、要删除的旧段号。合成段号的旧镜头不能靠省略来删除，必须列在 order 里。
func PlanSegmentOrder(order []string, written []string, existing []ExistingSegment) ([]string, error) {
	if len(order) == 0 {
		return nil, nil
	}
	position := make(map[string]int, len(order))
	for i, raw := range order {
		id := strings.TrimSpace(raw)
		if !segmentIDPattern.MatchString(id) {
			return nil, fmt.Errorf("段顺序里的段号「%s」无效：应为 S01、S12b 这样的格式，取自 drama_segments_read", EchoSegmentID(raw))
		}
		if _, dup := position[id]; dup {
			return nil, fmt.Errorf("段顺序里 %s 出现了两次", id)
		}
		position[id] = i
	}
	known := make(map[string]bool, len(existing)+len(written))
	for _, id := range written {
		if _, ok := position[id]; !ok {
			return nil, fmt.Errorf("%s 写入了但不在段顺序里", id)
		}
		known[id] = true
	}
	var removed []string
	for _, seg := range existing {
		known[seg.ID] = true
		if _, ok := position[seg.ID]; ok {
			continue
		}
		if seg.Derived {
			return nil, fmt.Errorf("%s 还不是分段（没有分段 JSON），重排时要列在段顺序里", seg.ID)
		}
		removed = append(removed, seg.ID)
	}
	for _, raw := range order {
		if id := strings.TrimSpace(raw); !known[id] {
			return nil, fmt.Errorf("段顺序里的 %s 既不是已有的段，也不在这次写入里", id)
		}
	}
	return removed, nil
}

// EchoSegmentID 把 Agent 传来的无效段号截短，只留字母、数字和 -_. 再放进报错：
// 错误要原样回给 Agent，不能被长串（比如把镜头 ID 当段号）、换行或引号撑坏。
func EchoSegmentID(raw string) string {
	runes := make([]rune, 0, 24)
	for _, r := range raw {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' {
			continue
		}
		if len(runes) == 24 {
			return string(runes) + "…"
		}
		runes = append(runes, r)
	}
	return string(runes)
}
