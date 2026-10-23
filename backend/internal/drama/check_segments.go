package drama

import (
	"fmt"
	"regexp"
	"strconv"
	"sync"

	builtinAssets "yingce/backend/builtin"
)

// 分段级门禁（check.py check_segments 的一部分）：目前只做 R-SEG-008 分段合计与单集目标片长比，级别取 rules/segments.json。

var (
	segmentRulesOnce sync.Once
	segmentRules     RuleLevels
	segmentRulesErr  error
	segmentEpisode   = regexp.MustCompile(`^E(\d{2})`)
)

// BuiltinSegmentRules 返回分段规则级别（进程内缓存）。
func BuiltinSegmentRules() (RuleLevels, error) {
	segmentRulesOnce.Do(func() {
		segmentRules, segmentRulesErr = LoadRuleLevels(builtinAssets.FS, "segments")
	})
	return segmentRules, segmentRulesErr
}

// SegmentsEpisode 是这批段所属的集号（取第一段场号 E01S02 里的 01）；认不出返回 0。
func SegmentsEpisode(segs []Segment) int {
	for _, s := range segs {
		if m := segmentEpisode.FindStringSubmatch(s.Scene); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

// CheckSegmentTotal 比一集分段合计与目标片长（R-SEG-008）：超过目标 10% 余量提醒并镜或删镜，不足 60% 提醒戏不够。
// target 为 0（没设目标）时不检查。
func (r *CheckReport) CheckSegmentTotal(segs []Segment, target float64, rules RuleLevels) {
	if target <= 0 || len(segs) == 0 {
		return
	}
	total := 0.0
	for _, s := range segs {
		total += s.Dur
	}
	switch {
	case total > target*1.1+1e-6:
		r.add(rules, "R-SEG-008", "", "", fmt.Sprintf("分段合计 %.0f 秒，超过目标片长 %.0f 秒的 10%% 余量（%.0f 秒）：剪辑会压不下去，并镜或删镜", total, target, target*1.1))
	case total < target*0.6-1e-6:
		r.add(rules, "R-SEG-008", "", "", fmt.Sprintf("分段合计 %.0f 秒，不足目标片长 %.0f 秒的 60%%：戏不够，补镜头或场", total, target))
	}
}
